package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasForms "github.com/simajiong/jaysql/internal/das/forms"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================
// 实例与库表结构工具（全部只读）
//
// 数据来源分两类：
//   - 实例清单 / 实例画像 / 库清单：平台已登记或已采集的本地数据（insight_instances /
//     insight_instance_schemas），查库即可，不依赖目标实例是否可达；
//   - 表清单 / 表结构：复用 das 模块的实时直连能力（GetTablesService / GetTableInfoService），
//     与「SQL 查询」页共用同一套实现、同一套库表权限口径 —— 不做第二套实现，避免行为漂移。
// ============================================================

func init() {
	register(listInstancesTool())
	register(getInstanceTool())
	register(listTablesTool())
	register(describeTableTool())
}

const (
	defaultInstanceLimit = 20
	maxInstanceLimit     = 50
	defaultTableLimit    = 50
	maxTableLimit        = 200
	maxDatabaseRows      = 200
	ddlMaxChars          = 6000
	// toolQueryTimeout 复用 das 直连查询时的单次超时
	toolQueryTimeout = 15 * time.Second
)

// noopResponseWriter das 服务的 Run() 只会读 C.Request.Context()，
// 不会写响应，这里给一个空实现即可（避免在生产代码里引入 httptest）。
type noopResponseWriter struct{ header http.Header }

func (w *noopResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *noopResponseWriter) Write(body []byte) (int, error) { return len(body), nil }

func (w *noopResponseWriter) WriteHeader(int) {}

// newToolGinContext 构造仅用于复用 das 服务的空 gin 上下文。
// das 的 Run() 会把 C.Request.Context() 交给底层驱动作为查询上下文，
// 这里挂一个独立超时，避免慢实例把 AI 请求一直挂住；返回的 cancel 由调用方 defer。
func newToolGinContext() (*gin.Context, context.CancelFunc) {
	base, cancel := context.WithTimeout(context.Background(), toolQueryTimeout)
	ctx, _ := gin.CreateTestContext(&noopResponseWriter{})
	req, err := http.NewRequestWithContext(base, http.MethodGet, "/", nil)
	if err != nil {
		req = new(http.Request)
	}
	ctx.Request = req
	return ctx, cancel
}

func usernameOf(ctx *Context) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(ctx.Username)
}

// cellString das 的查询结果值可能是 []byte / string / 数字，统一转文本
func cellString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func setIfNotEmpty(target map[string]any, key string, value string) {
	if text := strings.TrimSpace(value); text != "" {
		target[key] = text
	}
}

// timeTextIfValid 时间字段为空/零值时返回空串，避免把 0001-01-01 或零值结构体当作有效时间告诉模型
func timeTextIfValid(value any) string {
	text := strings.TrimSpace(timeText(value))
	if text == "" || strings.HasPrefix(text, "0001-01-01") || strings.HasPrefix(text, "{") {
		return ""
	}
	return text
}

func humanUptime(seconds int64) string {
	switch {
	case seconds >= 86400:
		return fmt.Sprintf("%d 天", seconds/86400)
	case seconds >= 3600:
		return fmt.Sprintf("%d 小时", seconds/3600)
	case seconds > 0:
		return fmt.Sprintf("%d 分钟", seconds/60)
	default:
		return ""
	}
}

// instanceIntro 实例介绍：基础信息 + 规格画像。
// 空值一律不输出（模型上下文里不需要一堆空字段，也避免把"未知"误读成"空"）。
func instanceIntro(instance *commonModels.InsightInstances, envs map[int]string) map[string]any {
	out := instanceBrief(instance, envs)
	if instance.Environment <= 0 {
		delete(out, "environment")
	}
	for _, key := range []string{"db_type", "env_name", "remark", "last_check_status"} {
		if strings.TrimSpace(fmt.Sprintf("%v", out[key])) == "" {
			delete(out, key)
		}
	}
	setIfNotEmpty(out, "db_version", instance.DbVersion)
	setIfNotEmpty(out, "os_name", instance.OsName)
	setIfNotEmpty(out, "instance_role", instance.InstanceRole)
	setIfNotEmpty(out, "ha_mode", instance.HaMode)
	setIfNotEmpty(out, "deploy_provider", instance.DeployProvider)
	setIfNotEmpty(out, "cloud_vendor", instance.CloudVendor)
	setIfNotEmpty(out, "disk_type", instance.DiskType)
	if instance.CpuCores > 0 {
		out["cpu_cores"] = instance.CpuCores
	}
	if instance.MemoryGB > 0 {
		out["memory_gb"] = instance.MemoryGB
	}
	if instance.DiskGB > 0 {
		out["disk_gb"] = instance.DiskGB
	}
	if text := humanUptime(instance.UptimeSeconds); text != "" {
		out["uptime"] = text
	}
	if text := timeTextIfValid(instance.LastCheckedAt); text != "" {
		out["last_checked_at"] = text
	}
	if text := strings.TrimSpace(instance.LastCheckMessage); text != "" {
		out["last_check_message"] = text
	}
	if instance.ProfileFactsAt != nil {
		if text := timeTextIfValid(*instance.ProfileFactsAt); text != "" {
			out["profile_facts_at"] = text
		}
	}
	return out
}

// envIDsByName 按环境名（模糊）取环境 ID 列表
func envIDsByName(name string) []int {
	keyword := strings.TrimSpace(name)
	if keyword == "" {
		return nil
	}
	var rows []commonModels.InsightInstanceEnvironments
	if err := global.App.DB.Model(&commonModels.InsightInstanceEnvironments{}).
		Where("name LIKE ?", "%"+keyword+"%").
		Limit(20).Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.Model != nil {
			out = append(out, int(row.ID))
		}
	}
	return out
}

// instanceDatabases 该实例已采集的库/schema 清单（本地数据，可能尚未同步）。
// 非管理员的清单剔除系统库（口径与工单下拉一致：das.FilterSystemSchemasForUsername）。
func instanceDatabases(ctx *Context, instance *commonModels.InsightInstances) []map[string]any {
	var rows []commonModels.InsightInstanceSchemas
	if err := global.App.DB.Model(&commonModels.InsightInstanceSchemas{}).
		Where("instance_id = ? AND is_deleted = ?", instance.InstanceID, false).
		Order("`schema` ASC").Limit(maxDatabaseRows).Find(&rows).Error; err != nil {
		return []map[string]any{}
	}
	filtered := dasServices.FilterSystemSchemasForUsername(usernameOf(ctx), string(instance.DbType), schemaNamesOf(rows))
	kept := make(map[string]struct{}, len(filtered))
	for _, name := range filtered {
		kept[name] = struct{}{}
	}
	out := make([]map[string]any, 0, len(filtered))
	for _, row := range rows {
		if _, ok := kept[row.Schema]; !ok {
			continue
		}
		item := map[string]any{"schema": row.Schema}
		setIfNotEmpty(item, "display_name", row.DisplayName)
		setIfNotEmpty(item, "resource_type", row.ResourceType)
		out = append(out, item)
	}
	return out
}

// schemaNamesOf 提取库/schema 名称，供 das 的系统库过滤函数使用。
func schemaNamesOf(rows []commonModels.InsightInstanceSchemas) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Schema)
	}
	return names
}

// wrapTableError 把 das 底层报错转成模型和用户都能理解的提示
func wrapTableError(err error, instance *commonModels.InsightInstances, schema string) error {
	message := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(message, "访问权限"):
		return fmt.Errorf("当前账号在 SQL 查询模块没有库 `%s` 的访问权限（%s）；"+
			"需要管理员在「系统管理 → 权限管理」授予该库的查询权限后重试", schema, message)
	case strings.Contains(message, "record not found"):
		return fmt.Errorf("读取失败：该实例连接信息或当前账号的权限上下文不存在（%s）；"+
			"请确认实例仍在平台登记、账号有效", message)
	case strings.Contains(message, "connection refused"),
		strings.Contains(message, "no such host"),
		strings.Contains(message, "i/o timeout"),
		strings.Contains(message, "connection reset"):
		return fmt.Errorf("连接实例 %s:%d 失败（%s）；请确认目标实例可达、账号密码正确",
			instance.Hostname, instance.Port, message)
	}
	return fmt.Errorf("查询 %s 失败：%s", schema, message)
}

// ------------------------------------------------------------------
// list_instances
// ------------------------------------------------------------------

func listInstancesTool() *Tool {
	return &Tool{
		Name: "list_instances",
		Description: "列出平台已登记的数据库实例，可按关键字、数据库类型、环境过滤，" +
			"返回实例 ID、地址、类型、环境、备注、连通状态与规格画像。" +
			"用于回答「平台有哪些实例」「有几个 MySQL 实例」「生产环境实例清单」这类问题。",
		Permission: "system.instance.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"keyword": map[string]any{"type": "string", "description": "按主机地址、实例 ID、备注或账号模糊匹配"},
				"db_type": map[string]any{"type": "string", "description": "数据库类型过滤，如 MySQL / PostgreSQL / TiDB / Oracle / ClickHouse"},
				"env":     map[string]any{"type": "string", "description": "环境名过滤，如 生产 / 测试"},
				"limit":   map[string]any{"type": "integer", "description": fmt.Sprintf("返回条数，默认 %d，最多 %d", defaultInstanceLimit, maxInstanceLimit)},
			},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			limit := argInt(args, "limit", defaultInstanceLimit)
			if limit <= 0 {
				limit = defaultInstanceLimit
			}
			if limit > maxInstanceLimit {
				limit = maxInstanceLimit
			}

			keyword := argString(args, "keyword", "search")
			dbType := argString(args, "db_type", "dbType")
			var envIDs []int
			envName := argString(args, "env", "environment")
			if envName != "" {
				envIDs = envIDsByName(envName)
				if len(envIDs) == 0 {
					return map[string]any{
						"total": 0, "instances": []map[string]any{},
						"hint": fmt.Sprintf("平台没有名为「%s」的环境，可先不带 env 参数查看全部实例", envName),
					}, nil
				}
			}

			visibleIDs, allVisible, visibleErr := visibleInstanceIDs(ctx)
			if visibleErr != nil {
				return nil, visibleErr
			}
			build := func() *gorm.DB {
				// 数据授权收口：实例清单也要收敛——AI 问一句「有哪些实例」
				// 就能枚举出全部数据源，等于把没授权的实例名先泄露出去。
				query := global.App.DB.Model(&commonModels.InsightInstances{})
				if !allVisible {
					if len(visibleIDs) == 0 {
						query = query.Where("1 = 0")
					} else {
						query = query.Where("instance_id IN ?", visibleIDs)
					}
				}
				if keyword != "" {
					like := "%" + keyword + "%"
					query = query.Where("(hostname LIKE ? OR instance_id LIKE ? OR remark LIKE ? OR user LIKE ?)",
						like, like, like, like)
				}
				if dbType != "" {
					query = query.Where("LOWER(db_type) = LOWER(?)", dbType)
				}
				if len(envIDs) > 0 {
					query = query.Where("environment IN ?", envIDs)
				}
				return query
			}

			var total int64
			if err := build().Count(&total).Error; err != nil {
				return nil, err
			}

			var rows []commonModels.InsightInstances
			if err := build().
				Order("id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
				return nil, err
			}
			truncated := len(rows) > limit
			if truncated {
				rows = rows[:limit]
			}

			envs := envNameMap()
			items := make([]map[string]any, 0, len(rows))
			for index := range rows {
				items = append(items, instanceIntro(&rows[index], envs))
			}

			result := map[string]any{
				"total":     total,
				"returned":  len(items),
				"truncated": truncated,
				"instances": items,
			}
			if truncated {
				result["hint"] = fmt.Sprintf("仅返回前 %d 条（共 %d 条），可用 keyword 或 db_type 缩小范围", limit, total)
			}
			return result, nil
		},
	}
}

// ------------------------------------------------------------------
// get_instance
// ------------------------------------------------------------------

func getInstanceTool() *Tool {
	return &Tool{
		Name: "get_instance",
		Description: "查看单个实例的详细介绍：地址、类型、环境、备注、连通状态、规格画像" +
			"（数据库版本、操作系统、CPU/内存/磁盘、架构角色、高可用模式、运行时长）与已采集的库清单。" +
			"需要提供 instance_id，或 host（建议同时给 port）。库清单为空表示该实例尚未同步元数据。",
		Permission: "system.instance.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
			},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := lookupInstance(
				argString(args, "instance_id", "instanceId"),
				argString(args, "host"),
				argInt(args, "port", 0),
			)
			if err != nil {
				return nil, err
			}
			// 数据授权收口：未授权的实例按「不存在」处理，
			// 不能让 AI 借实例画像/表结构把没授权的实例摸清楚。
			visible, visibleErr := instanceVisible(ctx, instance.InstanceID)
			if visibleErr != nil {
				return nil, visibleErr
			}
			if !visible {
				return nil, fmt.Errorf("未找到实例 %s", instance.InstanceID)
			}
			// 系统库收口（2026-10-24）：与工单下拉同一口径，非 admin 的库清单
			// 剔除 information_schema / mysql / sys / pg_catalog 等系统库——
			// 否则 AI 会借 get_instance 把系统库结构原样报给用户。
			databases := instanceDatabases(ctx, instance)
			result := map[string]any{
				"instance":       instanceIntro(instance, envNameMap()),
				"database_count": len(databases),
				"databases":      databases,
			}
			if len(databases) == 0 {
				result["hint"] = "该实例尚未采集到库清单，可在「工单 → 数据源」对该实例执行「同步元数据」后再查；" +
					"若已知库名，也可直接用 list_tables 查询"
			}
			return result, nil
		},
	}
}

// ------------------------------------------------------------------
// list_tables
// ------------------------------------------------------------------

func listTablesTool() *Tool {
	return &Tool{
		Name: "list_tables",
		Description: "列出某个实例下指定库（schema）的表清单，实时查询目标实例，返回表名、表注释、行数估算、引擎。" +
			"用于回答「这个库有哪些表」「某实例的表规模」。schema 取 get_instance 返回的 databases[].schema，" +
			"PostgreSQL 形如 database.schema。字段结构请用 describe_table。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
				"schema":      map[string]any{"type": "string", "description": "库名 / 资源路径，必填"},
				"keyword":     map[string]any{"type": "string", "description": "按表名或表注释模糊过滤"},
				"limit":       map[string]any{"type": "integer", "description": fmt.Sprintf("返回条数，默认 %d，最多 %d", defaultTableLimit, maxTableLimit)},
			},
			"required": []string{"schema"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := lookupInstance(
				argString(args, "instance_id", "instanceId"),
				argString(args, "host"),
				argInt(args, "port", 0),
			)
			if err != nil {
				return nil, err
			}
			// 数据授权收口：未授权的实例按「不存在」处理，
			// 不能让 AI 借实例画像/表结构把没授权的实例摸清楚。
			visible, visibleErr := instanceVisible(ctx, instance.InstanceID)
			if visibleErr != nil {
				return nil, visibleErr
			}
			if !visible {
				return nil, fmt.Errorf("未找到实例 %s", instance.InstanceID)
			}
			schema := argString(args, "schema", "database", "db")
			if schema == "" {
				return nil, fmt.Errorf("需要提供 schema（库名）；可先用 get_instance 获取该实例的库清单")
			}

			ginCtx, cancel := newToolGinContext()
			defer cancel()
			service := dasServices.GetTablesService{
				GetTablesForm: &dasForms.GetTablesForm{InstanceID: instance.InstanceID, Schema: schema},
				C:             ginCtx,
				Username:      usernameOf(ctx),
			}
			data, err := service.Run()
			if err != nil {
				return nil, wrapTableError(err, instance, schema)
			}

			limit := argInt(args, "limit", defaultTableLimit)
			if limit <= 0 {
				limit = defaultTableLimit
			}
			if limit > maxTableLimit {
				limit = maxTableLimit
			}
			keyword := strings.ToLower(argString(args, "keyword", "search"))

			total := 0
			items := make([]map[string]any, 0, limit)
			if data != nil {
				for _, row := range *data {
					name := cellString(row["table_name"])
					if name == "" {
						continue
					}
					comment := cellString(row["table_comment"])
					if keyword != "" &&
						!strings.Contains(strings.ToLower(name), keyword) &&
						!strings.Contains(strings.ToLower(comment), keyword) {
						continue
					}
					total++
					if len(items) >= limit {
						continue
					}
					item := map[string]any{"table_name": name}
					setIfNotEmpty(item, "table_comment", comment)
					setIfNotEmpty(item, "engine", cellString(row["engine"]))
					if rows := cellString(row["table_rows"]); rows != "" {
						item["table_rows"] = rows
					}
					if size := cellString(row["data_length_kb"]); size != "" {
						item["data_length_kb"] = size
					}
					setIfNotEmpty(item, "update_time", cellString(row["update_time"]))
					items = append(items, item)
				}
			}

			result := map[string]any{
				"instance_id": instance.InstanceID,
				"schema":      schema,
				"db_type":     plainString(instance.DbType),
				"total":       total,
				"returned":    len(items),
				"truncated":   total > len(items),
				"tables":      items,
			}
			if len(items) == 0 {
				result["hint"] = "该库下没有匹配的表；请确认库名是否正确（可先用 get_instance 查看库清单）"
			}
			return result, nil
		},
	}
}

// ------------------------------------------------------------------
// describe_table
// ------------------------------------------------------------------

func describeTableTool() *Tool {
	return &Tool{
		Name: "describe_table",
		Description: "查看单张表的建表语句（DDL），实时查询目标实例，用于了解字段、类型、索引与列注释。" +
			"需要 instance（instance_id 或 host）、schema、table 三者。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
				"schema":      map[string]any{"type": "string", "description": "库名 / 资源路径（PostgreSQL 形如 database.schema）"},
				"table":       map[string]any{"type": "string", "description": "表名"},
			},
			"required": []string{"schema", "table"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := lookupInstance(
				argString(args, "instance_id", "instanceId"),
				argString(args, "host"),
				argInt(args, "port", 0),
			)
			if err != nil {
				return nil, err
			}
			// 数据授权收口：未授权的实例按「不存在」处理，
			// 不能让 AI 借实例画像/表结构把没授权的实例摸清楚。
			visible, visibleErr := instanceVisible(ctx, instance.InstanceID)
			if visibleErr != nil {
				return nil, visibleErr
			}
			if !visible {
				return nil, fmt.Errorf("未找到实例 %s", instance.InstanceID)
			}
			schema := argString(args, "schema", "database", "db")
			table := argString(args, "table", "table_name")
			if schema == "" || table == "" {
				return nil, fmt.Errorf("需要同时提供 schema 与 table；可先用 list_tables 获取该库的表清单")
			}

			ginCtx, cancel := newToolGinContext()
			defer cancel()
			service := dasServices.GetTableInfoService{
				GetTableInfoForm: &dasForms.GetTableInfoForm{
					InstanceID: instance.InstanceID,
					Schema:     schema,
					Table:      table,
					Type:       "structure",
				},
				C:        ginCtx,
				Username: usernameOf(ctx),
			}
			data, err := service.Run()
			if err != nil {
				return nil, wrapTableError(err, instance, schema)
			}

			// 各引擎返回的字段名不同（MySQL 是 Create Table，PostgreSQL 是拼好的建表语句），
			// 取最长的一段文本即 DDL 本体。
			ddl := ""
			if data != nil {
				for _, row := range *data {
					for _, value := range row {
						if text := cellString(value); len(text) > len(ddl) {
							ddl = text
						}
					}
				}
			}
			truncated := false
			if len(ddl) > ddlMaxChars {
				ddl = ddl[:ddlMaxChars]
				truncated = true
			}
			result := map[string]any{
				"instance_id": instance.InstanceID,
				"schema":      schema,
				"table":       table,
				"db_type":     plainString(instance.DbType),
				"ddl":         ddl,
			}
			if ddl == "" {
				result["hint"] = "未取到建表语句，请确认库名与表名是否正确；表清单可用 list_tables 查看"
			}
			if truncated {
				result["truncated"] = true
			}
			return result, nil
		},
	}
}
