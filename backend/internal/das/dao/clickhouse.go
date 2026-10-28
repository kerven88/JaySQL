/*
@Desc    :   如果clickhouse版本大于22.3，请使用高版本的clickhouse-go
			 参考：https://github.com/ClickHouse/clickhouse-go/blob/v2.8.3/resources/meta.yml
*/

package dao

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/spf13/cast"
)

type ClickhouseDB struct {
	User     string
	Password string
	Host     string
	Port     int
	Database string
	Settings map[string]interface{}
	Ctx      context.Context
}

func (c *ClickhouseDB) connect() (driver.Conn, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", c.Host, c.Port)},
		Auth: clickhouse.Auth{
			Database: c.Database,
			Username: c.User,
			Password: c.Password,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		DialTimeout:  3 * time.Second,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
		Settings:     c.Settings,
	})
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(c.Ctx); err != nil {
		// if exception, ok := err.(*clickhouse.Exception); ok {
		// 	fmt.Printf("Exception [%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
		// }
		return nil, err
	}
	return conn, nil
}

func (c *ClickhouseDB) Query(query string) (*[]string, *[]map[string]interface{}, error) {
	// 连接到db
	conn, err := c.connect()
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	// 执行查询
	rows, err := conn.Query(c.Ctx, query)
	if err != nil {
		return nil, nil, err
	}
	columns, resultSlice, err := scanClickHouseRows(rows)
	if err != nil {
		return nil, nil, err
	}
	return &columns, &resultSlice, nil
}

// scanClickHouseRows 遍历并物化 ClickHouse 结果集。
//
// rows 必须关闭：clickhouse-go 的 row 会持有池内连接直到 Close，
// 不关就等于把这条连接扣住不归还。单查询场景里 defer conn.Close() 兜住了，
// 但会话场景（ClickHouseSession）下连接是要复用的 —— 不关 rows 就
// 第二次查询必然卡死在等待连接上（MaxOpenConns=1）。统一在这里 defer 关闭，
// 两个入口都不会再漏。
func scanClickHouseRows(rows driver.Rows) ([]string, []map[string]interface{}, error) {
	defer rows.Close()

	// 获取列名
	columns := rows.Columns()
	columnTypes := rows.ColumnTypes()

	vals := make([]interface{}, len(columns))
	for i := range columnTypes {
		vals[i] = reflect.New(columnTypes[i].ScanType()).Interface()
	}

	resultSlice := make([]map[string]interface{}, 0)
	for rows.Next() {
		if err := rows.Scan(vals...); err != nil {
			return nil, nil, err
		}
		vmap := make(map[string]interface{}, len(vals))
		for i, v := range vals {
			// 解决select 1 as id,2 as id,3 as id列名重复的问题，自动加别名
			if _, ok := vmap[columns[i]]; ok {
				columns[i] = fmt.Sprintf("%s_%s[别名]", columns[i], uuid.New())
			}
			var newV string
			// 类型断言
			switch v := v.(type) {
			case *string:
				newV = *v
			case *time.Time:
				newV = fmt.Sprintf("%v", v.Format("2006-01-02 15:04:05"))
			default:
				newV = cast.ToString(v)
			}
			vmap[columns[i]] = newV
		}
		resultSlice = append(resultSlice, vmap)
	}
	// 遍历可能因网络中断提前退出，rows.Err() 才能拿到真实原因；
	// 不检查就会把一个断连当成"查到了 0 行"返回，采集侧误判成空结果。
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return columns, resultSlice, nil
}
