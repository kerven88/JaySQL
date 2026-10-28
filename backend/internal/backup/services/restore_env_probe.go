package services

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasDao "github.com/simajiong/jaysql/internal/das/dao"
)

// 本文件提供「恢复向导第 4 步（预检查）」所需的环境一致性探测原语：
// 账号权限、SQL_MODE、lower_case_table_names、时区等。
// 全部为只读探测，失败时由调用方降级为 WARN，不阻断恢复。

const restoreEnvProbeTimeout = 5 * time.Second

func lowerKeyRow(row map[string]any) map[string]string {
	out := make(map[string]string, len(row))
	for key, value := range row {
		out[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(fmt.Sprintf("%v", value))
	}
	return out
}

// probeMySQLServerVariables 读取 MySQL 若干服务端变量（缺失的变量不会出现在结果里）。
func probeMySQLServerVariables(instance *commonModels.InsightInstances, names []string) (map[string]string, error) {
	if instance == nil {
		return nil, fmt.Errorf("实例为空")
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			filtered = append(filtered, "'"+escapeSingleQuotes(trimmed)+"'")
		}
	}
	if len(filtered) == 0 {
		return map[string]string{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), restoreEnvProbeTimeout)
	defer cancel()
	db := dasDao.DB{
		User:     strings.TrimSpace(string(instance.User)),
		Password: strings.TrimSpace(string(instance.Password)),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	query := "show variables where variable_name in (" + strings.Join(filtered, ",") + ")"
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if rows == nil {
		return out, nil
	}
	for _, row := range *rows {
		normalized := lowerKeyRow(row)
		name := normalized["variable_name"]
		if name == "" {
			continue
		}
		out[name] = normalized["value"]
	}
	return out, nil
}

// probePostgresSettings 读取 PostgreSQL 若干运行时参数（current_setting 第二参数 true 表示缺失返回 NULL）。
func probePostgresSettings(instance *commonModels.InsightInstances, names []string) (map[string]string, error) {
	if instance == nil {
		return nil, fmt.Errorf("实例为空")
	}
	extra := parseExtraConfig(instance.ExtraConfig)
	database := firstNonEmpty(strings.TrimSpace(extra["database"]), "postgres")
	ctx, cancel := context.WithTimeout(context.Background(), restoreEnvProbeTimeout)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     strings.TrimSpace(string(instance.User)),
		Password: strings.TrimSpace(string(instance.Password)),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: database,
		SSLMode:  strings.TrimSpace(extra["sslmode"]),
		Ctx:      ctx,
	}
	out := map[string]string{}
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		query := fmt.Sprintf("select current_setting('%s', true) as value", escapeSingleQuotes(trimmed))
		_, rows, err := db.Query(query)
		if err != nil {
			return out, err
		}
		if rows == nil || len(*rows) == 0 {
			continue
		}
		normalized := lowerKeyRow((*rows)[0])
		if normalized["value"] != "" {
			out[strings.ToLower(trimmed)] = normalized["value"]
		}
	}
	return out, nil
}

// probeMySQLGrants 读取当前账号的授权列表（列名随账号变化，故按值收集）。
func probeMySQLGrants(instance *commonModels.InsightInstances) ([]string, error) {
	if instance == nil {
		return nil, fmt.Errorf("实例为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), restoreEnvProbeTimeout)
	defer cancel()
	db := dasDao.DB{
		User:     strings.TrimSpace(string(instance.User)),
		Password: strings.TrimSpace(string(instance.Password)),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	_, rows, err := db.Query("show grants for current_user()")
	if err != nil {
		return nil, err
	}
	grants := make([]string, 0)
	if rows == nil {
		return grants, nil
	}
	for _, row := range *rows {
		for _, value := range row {
			text := strings.TrimSpace(fmt.Sprintf("%v", value))
			if text != "" {
				grants = append(grants, text)
			}
		}
	}
	return grants, nil
}

// mySQLGrantsAllowRestore 判断授权集合是否足以完成「建库 + 建表 + 写数据 + 必要时删库」。
func mySQLGrantsAllowRestore(grants []string) (bool, []string) {
	missing := make([]string, 0)
	if len(grants) == 0 {
		return false, []string{"未读取到任何授权信息"}
	}
	joined := strings.ToUpper(strings.Join(grants, " || "))
	if strings.Contains(joined, "ALL PRIVILEGES") {
		return true, nil
	}
	required := []struct {
		label   string
		keyword string
	}{
		{"CREATE", "CREATE"},
		{"DROP", "DROP"},
		{"INSERT", "INSERT"},
		{"ALTER", "ALTER"},
	}
	for _, item := range required {
		if !strings.Contains(joined, item.keyword) {
			missing = append(missing, item.label)
		}
	}
	return len(missing) == 0, missing
}

// postgresRolePrivileges 返回 (是否超级用户, 是否可建库, 错误)。
func postgresRolePrivileges(instance *commonModels.InsightInstances) (bool, bool, error) {
	if instance == nil {
		return false, false, fmt.Errorf("实例为空")
	}
	extra := parseExtraConfig(instance.ExtraConfig)
	database := firstNonEmpty(strings.TrimSpace(extra["database"]), "postgres")
	ctx, cancel := context.WithTimeout(context.Background(), restoreEnvProbeTimeout)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     strings.TrimSpace(string(instance.User)),
		Password: strings.TrimSpace(string(instance.Password)),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: database,
		SSLMode:  strings.TrimSpace(extra["sslmode"]),
		Ctx:      ctx,
	}
	_, rows, err := db.Query("select rolsuper, rolcreatedb from pg_roles where rolname = current_user")
	if err != nil {
		return false, false, err
	}
	if rows == nil || len(*rows) == 0 {
		return false, false, fmt.Errorf("未读取到当前账号的 PG 角色信息")
	}
	normalized := lowerKeyRow((*rows)[0])
	return isTruthLike(normalized["rolsuper"]), isTruthLike(normalized["rolcreatedb"]), nil
}

var (
	mysqlDumpSQLModePattern  = regexp.MustCompile(`(?i)(?:SET\s+@@SESSION\.SQL_MODE|SET\s+SQL_MODE)\s*=\s*['"]?([^'"\n;]+)`)
	mysqlDumpCharsetPattern  = regexp.MustCompile(`(?i)/\*!40101\s+SET\s+NAMES\s+([A-Za-z0-9_]+)`)
	mysqlDumpUsePattern      = regexp.MustCompile("(?im)^[ \t]*USE[ \t]+`?([A-Za-z0-9_$]+)`?[ \t]*;")
	mysqlDumpCreateDBPattern = regexp.MustCompile("(?im)^[ \t]*CREATE[ \t]+DATABASE[ \t]+(?:IF[ \t]+NOT[ \t]+EXISTS[ \t]+)?`?([A-Za-z0-9_$]+)`?")
)

// extractMySQLDumpDatabases 从 MySQL 单文件逻辑备份中解析涉及的库名（USE / CREATE DATABASE）。
// 用于「恢复对象」展示；解析不到时由调用方回落到备份集登记的源库名。
func extractMySQLDumpDatabases(setFile string) []string {
	head := readArtifactHead(setFile, 512*1024)
	if head == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 2)
	for _, pattern := range []*regexp.Regexp{mysqlDumpUsePattern, mysqlDumpCreateDBPattern} {
		for _, match := range pattern.FindAllStringSubmatch(head, -1) {
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

// extractMySQLDumpSQLMode 从 MySQL 逻辑备份产物中解析 dump 头部的 SQL_MODE。
// 支持 .sql / .sql.gz / .zip（取首个条目），仅读取头部有限字节，不做全量解析。
func extractMySQLDumpSQLMode(setFile string) string {
	head := readArtifactHead(setFile, 256*1024)
	if head == "" {
		return ""
	}
	match := mysqlDumpSQLModePattern.FindStringSubmatch(head)
	if len(match) < 2 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(match[1]), `"`)
}

// extractMySQLDumpCharset 解析 dump 头部声明的字符集（用于展示与比对提示）。
func extractMySQLDumpCharset(setFile string) string {
	head := readArtifactHead(setFile, 64*1024)
	if head == "" {
		return ""
	}
	match := mysqlDumpCharsetPattern.FindStringSubmatch(head)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func readArtifactHead(path string, limit int64) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		reader, err := zip.OpenReader(trimmed)
		if err != nil {
			return ""
		}
		defer reader.Close()
		for _, file := range reader.File {
			name := strings.ToLower(filepathToSlash(file.Name))
			if !strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".sql.gz") {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				continue
			}
			content := readLimited(rc, limit, strings.HasSuffix(name, ".gz"))
			rc.Close()
			if content != "" {
				return content
			}
		}
		return ""
	case strings.HasSuffix(lower, ".gz"):
		handle, err := os.Open(trimmed)
		if err != nil {
			return ""
		}
		defer handle.Close()
		gz, err := gzip.NewReader(handle)
		if err != nil {
			return ""
		}
		defer gz.Close()
		return readLimited(gz, limit, false)
	case strings.HasSuffix(lower, ".sql"):
		handle, err := os.Open(trimmed)
		if err != nil {
			return ""
		}
		defer handle.Close()
		return readLimited(handle, limit, false)
	default:
		return ""
	}
}

func readLimited(reader io.Reader, limit int64, compressed bool) string {
	if compressed {
		// gzip 已解压到 reader，字节上限按解压后计算
		buf, _ := io.ReadAll(io.LimitReader(reader, limit))
		return string(buf)
	}
	buf, _ := io.ReadAll(io.LimitReader(reader, limit))
	return string(buf)
}

func filepathToSlash(v string) string {
	return strings.ReplaceAll(v, "\\", "/")
}

// normalizeEnvValue 归一化环境参数取值：大小写、多余空白、常见空值。
func normalizeEnvValue(v string) string {
	text := strings.ToLower(strings.TrimSpace(v))
	switch text {
	case "", "null", "none", "system":
		return ""
	}
	return text
}
