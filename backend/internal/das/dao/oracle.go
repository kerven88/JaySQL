package dao

import (
	"context"
	"database/sql"
	"fmt"

	go_ora "github.com/sijms/go-ora/v2"
)

type OracleDB struct {
	User          string
	Password      string
	Host          string
	Port          int
	ServiceName   string
	CurrentSchema string
	Ctx           context.Context
}

func (d *OracleDB) dsn() (string, error) {
	if d.ServiceName == "" {
		return "", fmt.Errorf("Oracle 服务名不能为空")
	}
	return go_ora.BuildUrl(d.Host, d.Port, d.ServiceName, d.User, d.Password, nil), nil
}

func (d *OracleDB) Query(query string) (*[]string, *[]map[string]interface{}, error) {
	dsn, err := d.dsn()
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if d.CurrentSchema != "" {
		_, err := db.ExecContext(d.Ctx, fmt.Sprintf("ALTER SESSION SET CURRENT_SCHEMA = %s", d.CurrentSchema))
		if err != nil {
			return nil, nil, err
		}
	}

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

// Execute 执行非查询语句（ALTER SYSTEM 等），用于参数管理写操作。
func (d *OracleDB) Execute(query string) error {
	dsn, err := d.dsn()
	if err != nil {
		return err
	}
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	_, err = db.ExecContext(d.Ctx, query)
	return err
}
