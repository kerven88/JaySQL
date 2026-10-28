package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	dasDao "github.com/simajiong/jaysql/internal/das/dao"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
)

func listMySQLDatabases(host string, port int, user string, password string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := dasDao.DB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: "",
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	_, rows, err := db.Query("show databases")
	if err != nil {
		return nil, err
	}
	out := []string{}
	// 系统库收口（2026-10-24）：改用 das 的统一清单（IsSystemSchema），
	// 不再维护本地 skip map——之前这份内联清单缺 metrics_schema，与 das 口径漂移。
	// 备份系统库没有业务意义，这里无条件过滤、不做角色豁免（与既有行为一致）。
	for _, row := range *rows {
		name := fmt.Sprintf("%v", row["Database"])
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if dasServices.IsSystemSchema("MySQL", name) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func listMySQLTables(host string, port int, user string, password string, database string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db := dasDao.DB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: database,
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	_, rows, err := db.Query("show full tables where Table_type = 'BASE TABLE'")
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range *rows {
		for _, v := range row {
			name := strings.TrimSpace(fmt.Sprintf("%v", v))
			if name == "" || strings.EqualFold(name, "BASE TABLE") {
				continue
			}
			out = append(out, name)
			break
		}
	}
	return out, nil
}

func listPostgresDatabases(host string, port int, user string, password string, database string, sslmode string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if database == "" {
		database = "postgres"
	}
	db := dasDao.PostgresDB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: database,
		SSLMode:  sslmode,
		Ctx:      ctx,
	}
	_, rows, err := db.Query("select datname from pg_database where datallowconn and not datistemplate order by datname")
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range *rows {
		name := strings.TrimSpace(fmt.Sprintf("%v", row["datname"]))
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func listPostgresSchemas(host string, port int, user string, password string, database string, sslmode string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if database == "" {
		database = "postgres"
	}
	db := dasDao.PostgresDB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: database,
		SSLMode:  sslmode,
		Ctx:      ctx,
	}
	query := "select schema_name from information_schema.schemata where schema_name not like 'pg_%' and schema_name <> 'information_schema' order by schema_name"
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range *rows {
		name := strings.TrimSpace(fmt.Sprintf("%v", row["schema_name"]))
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func listPostgresTables(host string, port int, user string, password string, database string, sslmode string, schema string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if database == "" {
		database = "postgres"
	}
	db := dasDao.PostgresDB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: database,
		SSLMode:  sslmode,
		Ctx:      ctx,
	}
	query := fmt.Sprintf("select table_name from information_schema.tables where table_schema = '%s' and table_type = 'BASE TABLE' order by table_name", escapeSingleQuotes(schema))
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range *rows {
		name := strings.TrimSpace(fmt.Sprintf("%v", row["table_name"]))
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}
