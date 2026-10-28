package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasDao "github.com/simajiong/jaysql/internal/das/dao"
)

func probeMySQLVersion(host string, port int, user string, password string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	_, rows, err := db.Query("select version() as version")
	if err != nil {
		return "", err
	}
	if rows == nil || len(*rows) == 0 {
		return "", fmt.Errorf("无法获取 MySQL 版本")
	}
	raw := (*rows)[0]["version"]
	return fmt.Sprintf("%v", raw), nil
}

func mysqlDatabaseExists(host string, port int, user string, password string, dbName string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	query := fmt.Sprintf("select schema_name from information_schema.schemata where schema_name = '%s' limit 1", escapeSingleQuotes(dbName))
	_, rows, err := db.Query(query)
	if err != nil {
		return false, err
	}
	return rows != nil && len(*rows) > 0, nil
}

func probePostgresVersion(host string, port int, user string, password string, database string, sslmode string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: database,
		SSLMode:  sslmode,
		Ctx:      ctx,
	}
	_, rows, err := db.Query("show server_version")
	if err != nil {
		return "", err
	}
	if rows == nil || len(*rows) == 0 {
		return "", fmt.Errorf("无法获取 PostgreSQL 版本")
	}
	raw := (*rows)[0]["server_version"]
	if raw == nil {
		raw = (*rows)[0]["server_version_num"]
	}
	return fmt.Sprintf("%v", raw), nil
}

func postgresDatabaseExists(host string, port int, user string, password string, database string, sslmode string, targetDB string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: database,
		SSLMode:  sslmode,
		Ctx:      ctx,
	}
	query := fmt.Sprintf("select 1 as ok from pg_database where datname = '%s' limit 1", escapeSingleQuotes(targetDB))
	_, rows, err := db.Query(query)
	if err != nil {
		return false, err
	}
	return rows != nil && len(*rows) > 0, nil
}

func probePostgresArchiveCommand(instance *commonModels.InsightInstances) (string, error) {
	if instance == nil {
		return "", fmt.Errorf("实例为空")
	}
	extra := parseExtraConfig(instance.ExtraConfig)
	database := firstNonEmpty(strings.TrimSpace(extra["database"]), "postgres")
	sslmode := strings.TrimSpace(extra["sslmode"])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     strings.TrimSpace(string(instance.User)),
		Password: strings.TrimSpace(string(instance.Password)),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: database,
		SSLMode:  sslmode,
		Ctx:      ctx,
	}
	_, rows, err := db.Query("select current_setting('archive_command', true) as archive_command")
	if err != nil {
		return "", err
	}
	if rows == nil || len(*rows) == 0 {
		return "", fmt.Errorf("无法获取 PostgreSQL archive_command")
	}
	return strings.TrimSpace(fmt.Sprintf("%v", (*rows)[0]["archive_command"])), nil
}

func postgresMajorTag(serverVersion string) (string, error) {
	v := strings.TrimSpace(serverVersion)
	if v == "" {
		return "", fmt.Errorf("PostgreSQL 版本为空")
	}
	if strings.HasPrefix(v, "9.6") {
		return "9.6", nil
	}
	parts := strings.Split(v, ".")
	majorStr := parts[0]
	major, err := strconv.Atoi(majorStr)
	if err != nil || major < 10 {
		return "", fmt.Errorf("无法解析 PostgreSQL major 版本: %s", v)
	}
	return fmt.Sprintf("%d", major), nil
}

func escapeSingleQuotes(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}
