package services

// ---------------------------------------------------------------------------
// 引擎自带系统库（System Schema）的可见性收口
//
// 背景：
//   实例（数据源）上授予「全实例」权限后，用户可以看到该实例下所有已同步的库，
//   引擎自带的系统库也在其中（MySQL 的 sys、ClickHouse 的 system、
//   Oracle 的 SYS / SYSTEM / 各种 SYS* 账号…）。
//   对非 DBA 角色来说这些库没有业务价值，却承载「改一行就毁一个实例」的风险：
//   系统库只要在树里出现一次，误点的概率就不再是零。
//
// 规则（2026-10-15 定稿）：
//   1. 命中「全实例」授权 + 不具备 admin 角色 → 可见库列表里剔除全部系统库；
//   2. 具备 admin 角色                     → 维持原样（DBA 排障要看 performance_schema / sys / pg_catalog）；
//   3. 显式授权单个库 / 单张表的场景         → 完全不受影响（授权人点了名，就按点的算）。
//
// 为什么收敛在这一个文件：
//   库列表有三个出口（资源树、我的库列表、工单选库下拉），每个出口各写一份
//   「系统库黑名单」必然漂移，漏一个出口就是一个绕过点。这里同时提供
//   「判定函数」和「判定上下文（gate）」，出口只负责调用，不复制清单。
// ---------------------------------------------------------------------------

import (
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/common/dbfamily"
	"github.com/simajiong/jaysql/internal/global"
)

// systemSchemasByFamily 引擎家族 → 默认系统库名。
//
// 一律存小写，比较前对目标名做 Lower + TrimSpace：
// 同一个库名在不同引擎里的大小写惯例不同（MySQL 多为小写、Oracle 全大写），
// 不做归一就会漏判。
var systemSchemasByFamily = map[string][]string{
	dbfamily.FamilyMySQL: {
		"information_schema",
		"mysql",
		"performance_schema",
		"sys",
		"metrics_schema", // TiDB 自带的诊断库，同样没有业务语义
	},
	dbfamily.FamilyPostgres: {
		"information_schema",
		"pg_catalog",
		"template0",
		"template1",
	},
	// Oracle 没有 MySQL 那种「系统库 = 四个固定名字」的约定：all_users 会把
	// 几十个内置账号一起同步进来（12c+ 更多）。这里按 12c/19c 默认账号整理，
	// 再配合下面的 sys 前缀 / sys 后缀两条规则收口组件账号。
	// 名单外的内置账号（如 PDBADMIN）按业务库处理——它常被当作工作 schema 使用，
	// 藏掉它的代价比漏掉一个只读字典视图更大。
	dbfamily.FamilyOracle: {
		"sys", "system", "anonymous", "dbsnmp", "outln", "audsys", "xdb", "dvsys", "dvf",
		"dbfwuser", "dbsfwuser", "dip", "gsmadmin_internal", "gsmcatuser", "gsmuser", "appqossys",
		"oradataaccess", "oracle_ocm", "mddata", "orddata", "ordplugins",
		"remote_scheduler_agent", "si_informtn_schema", "xs$null",
	},
	dbfamily.FamilySQLServer: {
		"master",
		"msdb",
		"model",
		"tempdb",
		"sys",
		"information_schema",
		"guest",
	},
	dbfamily.FamilyClickHouse: {
		"system",
		"information_schema",
	},
	dbfamily.FamilyMongoDB: {
		"admin",
		"local",
		"config",
	},
}

// systemSchemaPrefixesByFamily 引擎家族 → 系统库名前缀。
// 有些系统对象是「一批」而不是「一个」（Oracle 的 SYS* 账号、PG 的 toast / temp schema），
// 逐个枚举既写不全也难维护，用前缀收口。
var systemSchemaPrefixesByFamily = map[string][]string{
	dbfamily.FamilyPostgres: {"pg_toast", "pg_temp_"},
	dbfamily.FamilyOracle:   {"sys"}, // SYSBACKUP / SYSDG / SYSKM / SYSRAC / SYSMAN …
}

// systemSchemaSuffixesByFamily 引擎家族 → 系统库名后缀。
// Oracle 的组件账号几乎都以 sys 结尾（WMSYS / CTXSYS / MDSYS / OLAPSYS / ORDSYS /
// LBACSYS / DVSYS / AUDSYS / APPQOSSYS）。业务账号取这种名字的概率极低，
// 而漏掉一个组件账号就等于把一个能改字典的库露出去，故按后缀整体收口。
var systemSchemaSuffixesByFamily = map[string][]string{
	dbfamily.FamilyOracle: {"sys"},
}

// systemSchemasByDBType 个别 db_type 的补充清单（与家族清单取并集）。
//
// 只有 OceanBase 需要它：一个 db_type 同时对应 MySQL / Oracle 两种租户模式，
// 只按家族判定必然漏一边。这里直接取两个家族的并集 + oceanbase 内部库——
// 宁可多隐藏一个，不可漏一个：这些名字在 OceanBase 租户里都不可能是业务库。
var systemSchemasByDBType = map[string][]string{
	"oceanbase": {
		// MySQL 家族
		"information_schema", "mysql", "performance_schema", "sys", "metrics_schema",
		// Oracle 家族
		"system", "dbsnmp", "outln", "audsys", "xdb", "dvsys",
		"gsmadmin_internal", "appqossys", "oradataaccess",
		// OceanBase 自身
		"oceanbase",
	},
}

// systemSchemasForDBType 汇总某个 db_type 的全部系统库判定口径。
//
// 这里刻意不接 extra_config：OceanBase 两种模式的系统库清单已经按 db_type 取并集，
// 无需再解析 oceanbase_mode，调用方也就不必为了判一次系统库去捞 extra_config。
func systemSchemasForDBType(dbType string) (names []string, prefixes []string, suffixes []string) {
	normalized := dbfamily.Normalize(dbType)
	if extra, ok := systemSchemasByDBType[normalized]; ok {
		names = append(names, extra...)
	}
	family := dbfamily.EffectiveFamily(dbType, nil)
	names = append(names, systemSchemasByFamily[family]...)
	return names, systemSchemaPrefixesByFamily[family], systemSchemaSuffixesByFamily[family]
}

// IsSystemSchema 判定某个库名是否为该 db_type 的系统库。
// 空名一律返回 false——「没有库名」不是系统库，避免把空路径整批过滤掉。
func IsSystemSchema(dbType string, schemaName string) bool {
	name := dbfamily.Normalize(schemaName)
	if name == "" {
		return false
	}
	names, prefixes, suffixes := systemSchemasForDBType(dbType)
	for _, item := range names {
		if name == item {
			return true
		}
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// IsSystemSchemaPath 判定资源树里的「资源路径」是否指向系统库。
//
// 与 IsSystemSchema 的差别：PostgreSQL / SQLServer 的库与 schema 是父子关系，
// 资源路径形如 `mydb.pg_catalog`，而系统库名只体现在最后一段上。
// MySQL 系的库名理论上可以带点，但这种命名极其罕见，取最后一段的收益远大于风险。
func IsSystemSchemaPath(dbType string, schemaPath string) bool {
	return IsSystemSchema(dbType, schemaPathLeaf(schemaPath))
}

// schemaPathLeaf 取资源路径的最后一段（PostgreSQL / SQLServer 的 schema 名）。
func schemaPathLeaf(schemaPath string) string {
	trimmed := strings.TrimSpace(schemaPath)
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}

// ---------------------------------------------------------------------------
// 可见性判定上下文（gate）
// ---------------------------------------------------------------------------

// systemSchemaGate 一次请求内复用的判定上下文。
//
// 之所以要预先把「哪些实例命中了全实例授权」「哪些库被显式点名授权」查出来，
// 而不是逐行回查：库列表是列表接口，逐行回查会把一次查询放大成 N 次。
type systemSchemaGate struct {
	// instanceIDs 主体持有「全实例」有效授权的实例集合。
	instanceIDs map[string]struct{}
	// explicitSchemas 主体被显式点名授权（库级 / 表级）的库，key 见 grantSchemaKey。
	// 显式授权优先于系统库隐藏规则：授权人点了名，就按点的算。
	explicitSchemas map[string]struct{}
}

// grantSchemaKey 显式授权集合的 key。
// 用 \x00 分隔是因为它不可能出现在实例 ID 或库名里，不会拼出歧义 key。
func grantSchemaKey(instanceID string, schema string) string {
	return instanceID + "\x00" + dbfamily.Normalize(schema)
}

// newSystemSchemaGate 构造判定上下文。
//
// 返回 nil 表示「不需要隐藏任何系统库」——admin 或未知主体，
// 调用方直接放行即可，不必再往下查库。
func newSystemSchemaGate(scope *dasActorScope) *systemSchemaGate {
	if scope == nil || scope.HasAdminRole {
		return nil
	}
	gate := &systemSchemaGate{
		instanceIDs:     make(map[string]struct{}),
		explicitSchemas: make(map[string]struct{}),
	}
	gate.load(scope)
	return gate
}

func (g *systemSchemaGate) logError(err error) {
	if err == nil {
		return
	}
	if global.App.Log != nil {
		global.App.Log.Errorf("resolve system schema visibility: %v", err)
	}
}

// load 分别取「全实例授权实例集合」与「显式点名授权的库」。
// 查询失败时保持空集合：结果是最多隐藏过头（fail-closed），不会漏放。
func (g *systemSchemaGate) load(scope *dasActorScope) {
	now := time.Now()
	// actions 是逗号分隔串，用四种形态覆盖「唯一 / 首位 / 末位 / 中间」。
	// 不用 FIND_IN_SET —— MySQL 专有函数会让单测（sqlite）跑不起来。
	actionCond := "(g.actions = ? OR g.actions LIKE ? OR g.actions LIKE ? OR g.actions LIKE ?)"
	actionArgs := []any{
		DataActionSelect,
		DataActionSelect + ",%",
		"%," + DataActionSelect,
		"%," + DataActionSelect + ",%",
	}

	if dataGrantEnabled() {
		var instanceRows []struct {
			InstanceID string
		}
		tx := global.App.DB.Table("insight_das_data_grants g").
			Select("distinct g.instance_id").
			Where("g.status = ? AND g.effect = ? AND g.resource_level = ?",
				DataGrantStatusActive, DataEffectAllow, DataResourceLevelInstance).
			Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
			Where("(g.expire_at IS NULL OR g.expire_at > ?)", now).
			Where(actionCond, actionArgs...)
		tx = applyDataGrantPrincipalFilter(tx, "g", scope)
		if err := tx.Scan(&instanceRows).Error; err != nil {
			g.logError(err)
		}
		for _, row := range instanceRows {
			g.instanceIDs[row.InstanceID] = struct{}{}
		}

		var schemaRows []struct {
			InstanceID string
			SchemaName string
		}
		stx := global.App.DB.Table("insight_das_data_grants g").
			Select("distinct g.instance_id, g.schema_name").
			Where("g.status = ? AND g.effect = ? AND g.resource_level IN ?",
				DataGrantStatusActive, DataEffectAllow,
				[]string{DataResourceLevelDatabase, DataResourceLevelTable}).
			Where("g.schema_name <> ''").
			Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
			Where("(g.expire_at IS NULL OR g.expire_at > ?)", now).
			Where(actionCond, actionArgs...)
		stx = applyDataGrantPrincipalFilter(stx, "g", scope)
		if err := stx.Scan(&schemaRows).Error; err != nil {
			g.logError(err)
		}
		for _, row := range schemaRows {
			g.explicitSchemas[grantSchemaKey(row.InstanceID, row.SchemaName)] = struct{}{}
		}
		return
	}

	// 迁移前兼容：旧授权表没有操作维度与时间窗口，只按显式行判定。
	var legacyInstanceRows []struct {
		InstanceID string
	}
	ltx := global.App.DB.Table("insight_das_schema_perms p").
		Select("distinct p.instance_id").
		Where("p.`schema` = ? AND p.resource_type = ?", dasInstanceGrantSchema, dasResourceTypeInstance)
	ltx = applyPrincipalFilter(ltx, "p", scope)
	if err := ltx.Scan(&legacyInstanceRows).Error; err != nil {
		g.logError(err)
	}
	for _, row := range legacyInstanceRows {
		g.instanceIDs[row.InstanceID] = struct{}{}
	}

	var legacySchemaRows []struct {
		InstanceID string
		Schema     string
	}
	lstx := global.App.DB.Table("insight_das_schema_perms p").
		Select("distinct p.instance_id, p.`schema`").
		Where("p.`schema` <> '' AND p.`schema` <> ?", dasInstanceGrantSchema)
	lstx = applyPrincipalFilter(lstx, "p", scope)
	if err := lstx.Scan(&legacySchemaRows).Error; err != nil {
		g.logError(err)
	}
	for _, row := range legacySchemaRows {
		g.explicitSchemas[grantSchemaKey(row.InstanceID, row.Schema)] = struct{}{}
	}
}

// hidden 判断某个库是否因「全实例授权 + 非 admin」规则被隐藏。
//
// 三个放行条件（任一成立即不隐藏）：
//   - gate 为空（admin / 未知主体）；
//   - 该实例上没有全实例授权 —— 可见性来自显式授权，与本次规则无关；
//   - 该库被显式点名授权 —— 显式授权优先。
func (g *systemSchemaGate) hidden(instanceID string, dbType string, schemaPath string) bool {
	if g == nil {
		return false
	}
	if strings.TrimSpace(schemaPath) == "" {
		return false
	}
	if _, ok := g.instanceIDs[instanceID]; !ok {
		return false
	}
	if _, ok := g.explicitSchemas[grantSchemaKey(instanceID, schemaPath)]; ok {
		return false
	}
	return IsSystemSchemaPath(dbType, schemaPath)
}

// filterSchemaPaths 按规则剔除系统库，返回新切片（不改动入参底层数组）。
func (g *systemSchemaGate) filterSchemaPaths(instanceID string, dbType string, schemaPaths []string) []string {
	if g == nil || len(schemaPaths) == 0 {
		return schemaPaths
	}
	kept := make([]string, 0, len(schemaPaths))
	for _, path := range schemaPaths {
		if g.hidden(instanceID, dbType, path) {
			continue
		}
		kept = append(kept, path)
	}
	return kept
}

// ---------------------------------------------------------------------------
// 跨模块统一入口（工单 / AI 助手）
//
// workbench 的 gate 是「全实例授权 + 非 admin → 隐藏」，因为那里的可见性来自
// 授权；工单与 AI 是变更/代查入口，持有 order.submit 或 AI 会话的用户不一定
// 有任何数据授权，不能复用「没授权就不过滤」的语义——否则系统库照样漏出去。
// 因此这里的口径是 2026-10-24 定稿的：**非 admin 一律隐藏**。
//
// 刻意不做「显式授权豁免」：工单提交校验同样对非 admin 一律驳回系统库目标，
// 两处口径严格对齐，避免「下拉看得到、提交被驳回」或反向的不一致。
// 管理员如确需让某人变更系统库，走管理员自己的工单通道。
// ---------------------------------------------------------------------------

// HasBuiltinAdminRoleByUsername 判断用户是否具备内置 admin 角色。
//
// 与 das 各出口的「系统库要不要露出来」同一口径（角色 code='admin'，
// 而不是 is_superuser 字段——两个口径不一致时会出现「下拉看得到、
// 提交被驳回」的自相矛盾）。解析失败按非 admin 处理（fail-closed）。
func HasBuiltinAdminRoleByUsername(username string) bool {
	scope, err := resolveDASActorScope(strings.TrimSpace(username))
	if err != nil {
		return false
	}
	return scope.HasAdminRole
}

// SystemSchemaFilterTerms 返回某 db_type 的系统库名（小写）/前缀/后缀。
//
// 供「分页计数必须含过滤」的列表接口把过滤下推到 SQL：
// names 用于 NOT IN，prefix/suffix 用于 NOT LIKE（如 Oracle 的 SYS* 账号、
// PG 的 pg_toast），逐条内存过滤会打乱 total 与页内条数的一致性。
func SystemSchemaFilterTerms(dbType string) (names []string, prefixes []string, suffixes []string) {
	return systemSchemasForDBType(dbType)
}

// FilterSystemSchemasForUsername 按工单/AI 口径过滤库/schema 清单：
// admin 原样返回；非 admin 剔除全部系统库。username 为空或解析失败
// 一律按非 admin 处理（fail-closed）。
func FilterSystemSchemasForUsername(username string, dbType string, schemaPaths []string) []string {
	if HasBuiltinAdminRoleByUsername(username) {
		return schemaPaths
	}
	return filterAllSystemSchemas(dbType, schemaPaths)
}

// filterAllSystemSchemas 无条件剔除系统库（严格口径，不查授权）。
func filterAllSystemSchemas(dbType string, schemaPaths []string) []string {
	if len(schemaPaths) == 0 {
		return schemaPaths
	}
	kept := make([]string, 0, len(schemaPaths))
	for _, path := range schemaPaths {
		if strings.TrimSpace(path) != "" && IsSystemSchemaPath(dbType, path) {
			continue
		}
		kept = append(kept, path)
	}
	return kept
}
