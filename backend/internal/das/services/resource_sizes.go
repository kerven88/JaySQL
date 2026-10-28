package services

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
)

// 库 / Schema 名称右侧的体积展示依赖这份「尽力而为」的体量数据：
//   - 能查到就回填 size_bytes（0 是有效值，表示空库）；
//   - 查不到（引擎不支持 / 权限不足 / 连接失败）就不回填，前端不渲染，避免展示假数据。
//
// 任何失败都不影响资源列表本身：调用方拿 nil 即可。
const resourceSizeQueryTimeout = 5 * time.Second

// resourceSizeIndex 分两层存放体量，避免 PostgreSQL 的「库名」与「库.模式」在同一个 map 里撞键：
//   - databases：库级（MySQL / ClickHouse / PostgreSQL 的 database 节点）
//   - schemas：模式级（PostgreSQL 的 db.schema、Oracle 的 owner）
type resourceSizeIndex struct {
	databases map[string]int64
	schemas   map[string]int64
}

func (idx *resourceSizeIndex) empty() bool {
	return idx == nil || (len(idx.databases) == 0 && len(idx.schemas) == 0)
}

// lookUp 先精确匹配，再大小写不敏感兜底：Oracle 的 owner 恒为大写，
// 而历史入库的 schema 值大小写不一定统一。
func lookUp(sizes map[string]int64, name string) (int64, bool) {
	key := strings.TrimSpace(name)
	if key == "" || len(sizes) == 0 {
		return 0, false
	}
	if value, ok := sizes[key]; ok {
		return value, true
	}
	for candidate, value := range sizes {
		if strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return 0, false
}

func (idx *resourceSizeIndex) databaseSize(name string) (int64, bool) {
	if idx == nil {
		return 0, false
	}
	return lookUp(idx.databases, name)
}

// schemaSize 依次尝试「库.模式」原值 → 派生的「库.模式」→ 模式名本身，
// 兼容不同引擎写入 insight_instance_schemas 时的路径拼接差异。
func (idx *resourceSizeIndex) schemaSize(item *resourceListResult) (int64, bool) {
	if idx == nil || item == nil {
		return 0, false
	}
	candidates := make([]string, 0, 4)
	candidates = append(candidates, item.Schema)
	if item.DatabaseName != "" && item.SchemaName != "" {
		candidates = append(candidates, item.DatabaseName+"."+item.SchemaName)
	}
	candidates = append(candidates, item.SchemaName, item.DisplayName)
	for _, candidate := range candidates {
		if value, ok := lookUp(idx.schemas, candidate); ok {
			return value, true
		}
	}
	return 0, false
}

func loadResourceSizes(ctx context.Context, instanceID, family string, items []resourceListResult) *resourceSizeIndex {
	if instanceID == "" {
		return nil
	}
	var cfg InstanceCfg
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id=?", instanceID).Take(&cfg).Error; err != nil {
		return nil
	}
	plain, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil
	}
	cfg.PlainPassword = plain

	queryCtx, cancel := context.WithTimeout(ctx, resourceSizeQueryTimeout)
	defer cancel()

	switch family {
	case "mysql", "clickhouse":
		return loadDatabaseLevelSizes(queryCtx, &cfg, family, items)
	case "postgresql":
		return loadPostgresResourceSizes(queryCtx, &cfg, items)
	case "oracle":
		return loadOracleResourceSizes(queryCtx, &cfg, items)
	}
	return nil
}

// 库 = schema，直接按 table_schema / database 汇总，与表节点的 data_length 口径一致。
func loadDatabaseLevelSizes(ctx context.Context, cfg *InstanceCfg, family string, items []resourceListResult) *resourceSizeIndex {
	query := ""
	switch family {
	case "mysql":
		query = `SELECT table_schema AS name, SUM(IFNULL(data_length,0)) AS size_bytes
			FROM information_schema.tables GROUP BY table_schema`
	case "clickhouse":
		query = `SELECT database AS name, SUM(bytes_on_disk) AS size_bytes
			FROM system.parts WHERE active GROUP BY database`
	}
	if query == "" {
		return nil
	}
	db := dao.DB{
		User:     cfg.User,
		Password: cfg.PlainPassword,
		Host:     cfg.Hostname,
		Port:     cfg.Port,
		Ctx:      ctx,
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Warn("资源体积查询失败: " + err.Error())
		return nil
	}
	sizes := make(map[string]int64, len(*rows))
	for _, row := range *rows {
		name := stringField(row, "name")
		if name == "" {
			continue
		}
		sizes[name] = int64Field(row, "size_bytes")
	}
	// 聚合成功但列表里没有的库 = 空库（没有任何表），体积 0，按「0 显示 0」补齐
	for i := range items {
		if items[i].ResourceType != "database" {
			continue
		}
		if _, ok := lookUp(sizes, items[i].Schema); ok {
			continue
		}
		sizes[items[i].Schema] = 0
	}
	if len(sizes) == 0 {
		return nil
	}
	return &resourceSizeIndex{databases: sizes}
}

type postgresSizeExtraConfig struct {
	SSLMode  string `json:"sslmode"`
	Database string `json:"database"`
}

type oracleSizeExtraConfig struct {
	ServiceName string `json:"service_name"`
}

func pgSizeDB(cfg *InstanceCfg, ctx context.Context, database string) *dao.PostgresDB {
	extra := postgresSizeExtraConfig{}
	if len(cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(cfg.ExtraConfig, &extra)
	}
	if database == "" {
		database = strings.TrimSpace(extra.Database)
	}
	if database == "" {
		database = "postgres"
	}
	return &dao.PostgresDB{
		User:     cfg.User,
		Password: cfg.PlainPassword,
		Host:     cfg.Hostname,
		Port:     cfg.Port,
		Database: database,
		SSLMode:  extra.SSLMode,
		Ctx:      ctx,
	}
}

// PostgreSQL：库级用 pg_database_size（任意库都能查），
// 模式级没有系统视图，只能连到目标库统计 pg_class 的关系体积；只统计当前列出的模式所属库。
func loadPostgresResourceSizes(ctx context.Context, cfg *InstanceCfg, items []resourceListResult) *resourceSizeIndex {
	index := &resourceSizeIndex{databases: map[string]int64{}, schemas: map[string]int64{}}

	// 无 CONNECT 权限的库调用 pg_database_size 会直接报错，先用 has_database_privilege 过滤。
	dbSizesQuery := `SELECT datname AS name, pg_database_size(datname) AS size_bytes
		FROM pg_database WHERE has_database_privilege(datname, 'CONNECT')`
	if _, rows, err := pgSizeDB(cfg, ctx, "").Query(dbSizesQuery); err != nil {
		global.App.Log.Warn("PostgreSQL 库体积查询失败: " + err.Error())
	} else {
		for _, row := range *rows {
			name := stringField(row, "name")
			if name == "" {
				continue
			}
			index.databases[name] = int64Field(row, "size_bytes")
		}
	}

	schemaSizeQuery := `SELECT n.nspname AS name, SUM(pg_total_relation_size(c.oid)) AS size_bytes
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','m','p') GROUP BY n.nspname`
	// 统计成功的库才允许补 0：查询失败的库不能把「查不到」当成「空」
	measured := make(map[string]bool, 2)
	for _, database := range postgresSizeDatabases(items) {
		if ctx.Err() != nil {
			break
		}
		_, rows, err := pgSizeDB(cfg, ctx, database).Query(schemaSizeQuery)
		if err != nil {
			global.App.Log.Warn("PostgreSQL 模式体积查询失败(" + database + "): " + err.Error())
			continue
		}
		measured[database] = true
		for _, row := range *rows {
			name := stringField(row, "name")
			if name == "" {
				continue
			}
			index.schemas[database+"."+name] = int64Field(row, "size_bytes")
		}
	}
	// 没有任何表/物化视图的模式（PG 不会出现在上面的聚合里）体积就是 0，
	// 与「大小为 0 时只显示 0」的口径对齐。
	for i := range items {
		if items[i].ResourceType != "schema" {
			continue
		}
		if items[i].ParentPath != "" && !measured[items[i].ParentPath] {
			continue
		}
		if _, ok := lookUp(index.schemas, items[i].Schema); ok {
			continue
		}
		index.schemas[items[i].Schema] = 0
	}

	if index.empty() {
		return nil
	}
	return index
}

// postgresSizeDatabases 收集当前资源行里出现过的库（模式行的 parent_path 即库名）。
func postgresSizeDatabases(items []resourceListResult) []string {
	seen := make(map[string]struct{}, len(items))
	databases := make([]string, 0, 4)
	for i := range items {
		if items[i].ResourceType != "schema" {
			continue
		}
		name := strings.TrimSpace(items[i].ParentPath)
		if name == "" {
			name = strings.TrimSpace(items[i].DatabaseName)
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		databases = append(databases, name)
	}
	return databases
}

// Oracle：模式即 owner，按段（segment）汇总体积。
// dba_segments 需要 DBA 权限，取不到时回落到 all_segments（仅当前用户可见的对象）。
func loadOracleResourceSizes(ctx context.Context, cfg *InstanceCfg, items []resourceListResult) *resourceSizeIndex {
	extra := oracleSizeExtraConfig{}
	if len(cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(cfg.ExtraConfig, &extra)
	}
	db := &dao.OracleDB{
		User:        cfg.User,
		Password:    cfg.PlainPassword,
		Host:        cfg.Hostname,
		Port:        cfg.Port,
		ServiceName: extra.ServiceName,
		Ctx:         ctx,
	}
	// Oracle 会把未加引号的别名转成大写，这里用带引号的小写别名，保证与其它引擎的取列口径一致。
	queries := []string{
		`SELECT owner AS "name", SUM(bytes) AS "size_bytes" FROM dba_segments GROUP BY owner`,
		`SELECT owner AS "name", SUM(bytes) AS "size_bytes" FROM all_segments GROUP BY owner`,
	}
	for _, query := range queries {
		if ctx.Err() != nil {
			return nil
		}
		_, rows, err := db.Query(query)
		if err != nil {
			global.App.Log.Warn("Oracle 模式体积查询失败: " + err.Error())
			continue
		}
		sizes := make(map[string]int64, len(*rows))
		for _, row := range *rows {
			name := stringField(row, "name")
			if name == "" {
				continue
			}
			sizes[name] = int64Field(row, "size_bytes")
		}
		if len(sizes) > 0 {
			// 聚合成功但对没有出现，说明该 owner 没有任何段 → 体积 0，按「0 显示 0」补齐。
			filled := make(map[string]int64, len(items))
			for i := range items {
				if items[i].ResourceType != "schema" {
					continue
				}
				key := strings.ToUpper(strings.TrimSpace(items[i].Schema))
				if key == "" {
					continue
				}
				if _, ok := lookUp(sizes, key); ok {
					continue
				}
				filled[key] = 0
			}
			for key, value := range filled {
				sizes[key] = value
			}
			return &resourceSizeIndex{schemas: sizes}
		}
	}
	return nil
}

func int64Field(row map[string]interface{}, key string) int64 {
	value, ok := row[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	case []byte:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return 0
		}
		return n
	case string:
		// Oracle / PostgreSQL 的 SUM() 可能以小数形式返回（如 "12345.6"），按整数截断
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			if f, ferr := strconv.ParseFloat(v, 64); ferr == nil {
				return int64(f)
			}
			return 0
		}
		return n
	}
	return 0
}
