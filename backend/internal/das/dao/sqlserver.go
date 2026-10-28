package dao

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
)

type SQLServerDB struct {
	User     string
	Password string
	Host     string
	Port     int
	Database string
	Ctx      context.Context
}

func (d *SQLServerDB) dsn() string {
	return sqlserverdsn.DSN(d.User, d.Password, d.Host, d.Port, d.Database)
}

func (d *SQLServerDB) Query(query string) (*[]string, *[]map[string]interface{}, error) {
	db, err := sql.Open("sqlserver", d.dsn())
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	rows, err := db.QueryContext(d.Ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	vals := make([]interface{}, len(columns))
	for i := range columns {
		vals[i] = new(sql.RawBytes)
	}
	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		if err := rows.Scan(vals...); err != nil {
			return nil, nil, err
		}
		vmap := make(map[string]interface{}, len(vals))
		for i, c := range vals {
			switch v := c.(type) {
			case *sql.RawBytes:
				if *v == nil {
					vmap[columns[i]] = nil
				} else {
					vmap[columns[i]] = string(*v)
				}
			default:
				vmap[columns[i]] = fmt.Sprintf("%v", c)
			}
		}
		result = append(result, vmap)
	}
	if rows.Err() != nil {
		return nil, nil, rows.Err()
	}
	return &columns, &result, nil
}

// Execute 执行非查询语句（sp_configure/RECONFIGURE 等），用于参数管理写操作。
func (d *SQLServerDB) Execute(query string) error {
	db, err := sql.Open("sqlserver", d.dsn())
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	_, err = db.ExecContext(d.Ctx, query)
	return err
}
