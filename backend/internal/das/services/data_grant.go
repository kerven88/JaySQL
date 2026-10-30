package services

// ---------------------------------------------------------------------------
// 细粒度数据授权：统一校验入口
//
// 背景（本次改造要解决的问题）：
//   改造前「管理员角色」会在读取路径被隐式补齐全部实例的授权
//   （ensureMissingGrantsForRole，见 principal.go），即 admin 无需任何显式授权
//   即可看到所有数据源。这与最小权限原则冲突，也无法审计「谁把数据授权给了谁」。
//
// 改造后：
//   1. insight_das_data_grants 是数据可见性的唯一权威来源；
//   2. 授权维度 = 主体（用户/角色）× 资源（实例/库/表）× 操作类型（select/export/dml）；
//   3. 判定顺序固定：deny 优先于 allow，精确层级优先于粗粒度层级；
//      时间窗口内（effective_at <= now < expire_at）才算有效；
//   4. 没有任何命中记录 => 拒绝。admin 与创建人都不再有隐式特权。
//
// 兼容：保留一个回退开关 das_legacy_self_service_grant（默认 false），
//   置 true 时恢复「管理员角色/创建人隐式补齐」的旧行为，用于紧急回退。
// ---------------------------------------------------------------------------

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/gorm"
)

// dataGrantReadyTTL 新授权表启用状态的缓存时长。
// 判定「是否已迁移」要打一次 COUNT，缓存起来避免每次数据访问都多一次查询。
const dataGrantReadyTTL = 30 * time.Second

var (
	dataGrantReadyFlag atomic.Value // bool
	dataGrantReadyAt   atomic.Int64 // UnixNano
)

// dataGrantEnabled 判断新授权模型（insight_das_data_grants）是否已生效。
//
// 判据：新表里至少有一条记录（即存量迁移已执行）。
//   - 未生效 → 走旧表显式授权路径（不再做任何隐式补齐），保证灰度期数据不断供；
//   - 已生效 → 新表是唯一权威，未授权即不可见，撤销/过期立刻生效。
//
// 迁移完成的判定只需一次，缓存 30s 足以覆盖发布窗口，且不会让撤销延迟过久。
func dataGrantEnabled() bool {
	now := time.Now()
	if at := dataGrantReadyAt.Load(); at > 0 && now.Sub(time.Unix(0, at)) < dataGrantReadyTTL {
		if v, ok := dataGrantReadyFlag.Load().(bool); ok {
			return v
		}
	}
	ready := false
	var count int64
	if err := global.App.DB.Table("insight_das_data_grants").Count(&count).Error; err == nil {
		ready = count > 0
	}
	dataGrantReadyFlag.Store(ready)
	dataGrantReadyAt.Store(now.UnixNano())
	return ready
}

// ResetDataGrantReadyCache 清掉启用状态缓存。
// 迁移脚本执行完之后调用（或直接等 30s 自然过期），避免缓存让新表"看起来还没启用"。
func ResetDataGrantReadyCache() {
	dataGrantReadyAt.Store(0)
}

// DataGrantTableReady 对外暴露的只读探针，供健康检查与运维脚本确认迁移状态。
func DataGrantTableReady() bool {
	return dataGrantEnabled()
}

const (
	// 资源层级
	DataResourceLevelInstance = "instance"
	DataResourceLevelDatabase = "database"
	// DataResourceLevelSchema 模式级：只对 PostgreSQL / SQL Server 这类
	// 「库 → 模式 → 表」三层结构有意义。schema_name 存完整路径（库.模式），
	// 与资源树 / 元数据表的 `schema` 列口径一致；库级授权的 schema_name 只存库名。
	DataResourceLevelSchema = "schema"
	DataResourceLevelTable  = "table"

	// 操作类型
	DataActionSelect = "select"
	DataActionExport = "export"
	DataActionDML    = "dml"

	// DataActionAll 全操作集合。
	//
	// 只用于「旧模型等价迁移」的场景：旧授权表（schema_perms / table_perms）没有操作维度，
	// 一条库授权同时蕴含查询、导出、变更三种能力。把这类存量授权搬进新表时如果只写 select，
	// 等于悄悄把用户的 DML / 导出权限收走——这是数据迁移，不是权限收紧，语义必须等价。
	// 新建授权（管理页 / 自助授权）由授权人显式勾选，不走这个常量。
	DataActionAll = DataActionSelect + "," + DataActionExport + "," + DataActionDML

	// 效力
	DataEffectAllow = "allow"
	DataEffectDeny  = "deny"

	// 状态
	DataGrantStatusActive  = "active"
	DataGrantStatusRevoked = "revoked"
	DataGrantStatusExpired = "expired"

	// 来源
	DataGrantSourceManual  = "manual"
	DataGrantSourceSelf    = "self"
	DataGrantSourceMigrate = "migrate"
	DataGrantSourceSystem  = "system"
	// DataGrantSourceApply 由「数据访问权限申请」审批通过后自动写入。
	// 单独留一个来源值，是为了能一眼区分「管理员主动给的」和「批了申请给的」——
	// 事后审计时这两类授权的追溯路径完全不同（前者看管理页日志，后者要回到申请单）。
	DataGrantSourceApply = "apply"

	// 审计动作
	DataGrantAuditCreate = "create"
	DataGrantAuditRevoke = "revoke"
	DataGrantAuditRenew  = "renew"
	DataGrantAuditExpire = "expire"
)

// ErrNoDataGrant 是所有「未授权」场景的统一错误，便于上层与调用方识别。
var ErrNoDataGrant = errors.New("no data grant")

// dataGrantRow 一条命中待判定的授权记录
type dataGrantRow struct {
	ID            uint64
	ResourceLevel string
	Effect        string
	Actions       string
	// TableMode / TableName 表级授权的圈选方式与存储值：
	// wildcard 行的 TableName 是通配符模式，except 行是排除表清单，
	// SQL 侧无法精确判定，取回后在 Go 侧用 matchTableByGrantRow 复核。
	TableMode string
	TableName string
}

// DataAccessRequest 一次数据访问请求的描述
type DataAccessRequest struct {
	// RequireWholeDatabase is for non-SQL key browsing: narrower deny scopes must
	// never be bypassed by scanning the entire logical database.
	RequireWholeDatabase bool

	Username   string
	InstanceID string
	Schema     string
	Table      string
	Action     string
}

// getLegacySelfServiceGrantEnabled 回退开关：true 表示恢复旧的隐式补齐行为。
// 仅用于线上紧急回退，正常应为 false。
func getLegacySelfServiceGrantEnabled() bool {
	return readDasBoolParam("das_legacy_self_service_grant", false)
}

// applyDataGrantPrincipalFilter 按「用户主体 + 用户所属角色主体」过滤授权记录。
//
// 与旧表专用的 applyPrincipalFilter 区别：新表没有 username 兼容列，
// 只按 principal_type / principal_id 精确匹配，不再有 principal_id=0 的兜底分支
// ——那条兜底分支正是历史上「任何人都能命中空主体授权」的隐患来源。
func applyDataGrantPrincipalFilter(tx *gorm.DB, alias string, scope *dasActorScope) *gorm.DB {
	if scope == nil {
		return tx.Where("1 = 0")
	}
	qualified := func(field string) string {
		if alias == "" {
			return field
		}
		return alias + "." + field
	}
	conditions := []string{
		fmt.Sprintf("(%s = ? AND %s = ?)", qualified("principal_type"), qualified("principal_id")),
	}
	args := []any{dasPrincipalUser, scope.UID}

	roleIDs := make([]uint64, 0)
	for _, principal := range scope.Principals {
		if principal.PrincipalType == dasPrincipalRole {
			roleIDs = append(roleIDs, principal.PrincipalID)
		}
	}
	if len(roleIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("(%s = ? AND %s IN ?)", qualified("principal_type"), qualified("principal_id")))
		args = append(args, dasPrincipalRole, roleIDs)
	}
	return tx.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

// matchDataGrants 取出主体在指定资源上**当前有效**的全部授权记录。
//
// 有效 = status=active 且落在 [effective_at, expire_at) 时间窗口内。
// resourceLevel 为空表示不限层级（用于取可变实例集合）；否则按精确层级匹配。
func matchDataGrants(scope *dasActorScope, instanceID, schema, table string) ([]dataGrantRow, error) {
	if scope == nil || strings.TrimSpace(instanceID) == "" {
		return nil, nil
	}
	var rows []dataGrantRow
	// 时间窗口用应用侧传入的 now 而不是数据库的 NOW()：
	// 一是与 LocalTime 的口径一致，二是避免绑定具体数据库函数（单测跑 sqlite）。
	now := time.Now()
	tx := global.App.DB.Table("insight_das_data_grants g").
		Select("g.id, g.resource_level, g.effect, g.actions, g.table_mode, g.table_name").
		Where("g.instance_id = ?", instanceID).
		Where("g.status = ?", DataGrantStatusActive).
		Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
		Where("(g.expire_at IS NULL OR g.expire_at > ?)", now)

	// 资源层级匹配：只取「能覆盖当前请求资源」的那些记录
	schemaName := strings.TrimSpace(schema)
	tableName := strings.TrimSpace(table)
	// 库名：PostgreSQL / SQL Server 的 schema 路径是「库.模式」，而库级授权只写库名。
	// 不按前缀判定的话，「授了整个库」对库内任何模式都不生效，等于授了用不了。
	dbPrefix := ""
	if idx := strings.Index(schemaName, "."); idx > 0 {
		dbPrefix = schemaName[:idx]
	}
	resourceConds := make([]string, 0, 4)
	resourceArgs := make([]any, 0, 8)
	if tableName != "" {
		// 表级：exact 行按表名精确命中；wildcard / except 行覆盖面无法在 SQL 里精确表达
		//（通配符 / 排除清单存在 table_name 列里），先按「同库下全部取出」，Go 侧复核。
		resourceConds = append(resourceConds,
			"(g.resource_level = ? AND g.schema_name = ? AND ((g.table_mode IN ('', 'exact') AND g.table_name = ?) OR g.table_mode IN (?, ?)))")
		resourceArgs = append(resourceArgs, DataResourceLevelTable, schemaName, tableName, AccessTableModeWildcard, AccessTableModeExcept)
	}
	if schemaName != "" {
		// 模式级：精确命中完整路径（库.模式）
		resourceConds = append(resourceConds, "(g.resource_level = ? AND g.schema_name = ? AND g.table_name = '')")
		resourceArgs = append(resourceArgs, DataResourceLevelSchema, schemaName)
		// 库级：精确命中（MySQL 库名 / 未带模式的库）或按库名前缀覆盖该库下的模式
		resourceConds = append(resourceConds, "(g.resource_level = ? AND g.table_name = '' AND (g.schema_name = ? OR (? <> '' AND g.schema_name = ?)))")
		resourceArgs = append(resourceArgs, DataResourceLevelDatabase, schemaName, dbPrefix, dbPrefix)
	}
	resourceConds = append(resourceConds, "(g.resource_level = ? AND g.schema_name = '' AND g.table_name = '')")
	resourceArgs = append(resourceArgs, DataResourceLevelInstance)
	tx = tx.Where("("+strings.Join(resourceConds, " OR ")+")", resourceArgs...)

	tx = applyDataGrantPrincipalFilter(tx, "g", scope)
	if err := tx.Order("g.id ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	// wildcard / except 行在 SQL 侧只做了「同库」粗筛，表级精确覆盖必须在这里复核：
	// 通配符按模式匹配，排除清单按「不在清单内」判定。
	if tableName != "" {
		filtered := rows[:0]
		for _, row := range rows {
			if row.ResourceLevel == DataResourceLevelTable &&
				!matchTableByGrantRow(row.TableMode, row.TableName, tableName) {
				continue
			}
			filtered = append(filtered, row)
		}
		rows = filtered
	}
	return rows, nil
}

// grantAllowsAction 判断一条授权记录是否包含请求的操作类型。
// actions 为空视为「无任何操作被授权」，不匹配。
func grantAllowsAction(actions string, action string) bool {
	target := strings.ToLower(strings.TrimSpace(action))
	if target == "" {
		target = DataActionSelect
	}
	for _, item := range strings.Split(actions, ",") {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

// resolveDataGrantDecision 判定最终结果。
//
// 顺序（固定，不可调换）：
//  1. 任一命中记录的 effect=deny 且覆盖该操作 => 直接拒绝（deny 优先于 allow）
//  2. 否则任一命中记录 effect=allow 且覆盖该操作 => 放行
//  3. 都没有 => 拒绝（这正是「未授权即不可见」的落点）
func resolveDataGrantDecision(rows []dataGrantRow, action string) bool {
	allow := false
	for _, row := range rows {
		if !grantAllowsAction(row.Actions, action) {
			continue
		}
		if strings.EqualFold(row.Effect, DataEffectDeny) {
			return false
		}
		allow = true
	}
	return allow
}

// HasAnyInstanceGrant 判断主体是否对实例持有任一覆盖指定操作的有效 allow 授权
// （实例/库/表任一层级皆可）。deny 优先：该实例上存在覆盖该操作的 deny 记录时，
// 即使另有 allow 也判不通过，与 resolveDataGrantDecision 的优先级保持一致。
//
// 用途：执行入口的「实例准入闸」。SELECT 1、SHOW VARIABLES 这类无表引用语句
// 提取不出库表名，不会进入逐表校验循环；若不在循环之前先确认主体至少
// 「进得了这个实例」，未授权用户就能对任何实例执行此类语句探测环境信息。
func HasAnyInstanceGrant(scope *dasActorScope, instanceID, action string) (bool, error) {
	if scope == nil || strings.TrimSpace(instanceID) == "" {
		return false, nil
	}
	now := time.Now()
	tx := global.App.DB.Table("insight_das_data_grants g").
		Select("g.effect, g.actions").
		Where("g.instance_id = ?", instanceID).
		Where("g.status = ?", DataGrantStatusActive).
		Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
		Where("(g.expire_at IS NULL OR g.expire_at > ?)", now)
	tx = applyDataGrantPrincipalFilter(tx, "g", scope)
	var rows []dataGrantRow
	if err := tx.Scan(&rows).Error; err != nil {
		return false, err
	}
	deny := false
	allow := false
	for _, row := range rows {
		if !grantAllowsAction(row.Actions, action) {
			continue
		}
		if strings.EqualFold(row.Effect, DataEffectDeny) {
			deny = true
		} else {
			allow = true
		}
	}
	return allow && !deny, nil
}

// hasTableLevelGrantInSchema 判断该库下是否存在覆盖指定操作的表级授权。
//
// 用途：表级授权只授了「某张表」，因此不能让整个库都变成可见/可查，
// 否则等于越权；但反过来，库本身必须「够得着」——否则用户在资源树里
// 看不到这个库、执行 SELECT 时也会先被库级校验（HasSchemaPerms）挡死，
// 刚批下来的表级权限等于拿不到手。
// 所以库级闸门的口径是：库级授权 OR 库内任意表级授权命中该操作。
func hasTableLevelGrantInSchema(scope *dasActorScope, instanceID, schema, action string) (bool, error) {
	if scope == nil || strings.TrimSpace(instanceID) == "" || strings.TrimSpace(schema) == "" {
		return false, nil
	}
	now := time.Now()
	tx := global.App.DB.Table("insight_das_data_grants g").
		Select("g.id, g.resource_level, g.effect, g.actions").
		Where("g.instance_id = ?", instanceID).
		Where("g.schema_name = ?", schema).
		// 模式级授权同样是「库内的细粒度授权」，只是粒度比表粗一级；
		// 漏掉它会让「只授了某模式」的人在库级闸门被挡死。
		Where("g.resource_level IN ?", []string{DataResourceLevelTable, DataResourceLevelSchema}).
		Where("g.status = ?", DataGrantStatusActive).
		Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
		Where("(g.expire_at IS NULL OR g.expire_at > ?)", now)
	tx = applyDataGrantPrincipalFilter(tx, "g", scope)
	var rows []dataGrantRow
	if err := tx.Scan(&rows).Error; err != nil {
		return false, err
	}
	return resolveDataGrantDecision(rows, action), nil
}

// HasDataGrant 判断主体是否对指定资源拥有指定操作的有效授权。
// 这是数据访问控制的唯一判定入口，所有查询/修改入口都必须经过它。
func HasDataGrant(scope *dasActorScope, instanceID, schema, table, action string) (bool, error) {
	if scope == nil {
		return false, nil
	}
	// 没有超管旁路：admin 与普通人走同一套判定，未授权即不可见。
	// 超管的特权只体现在 RBAC 功能权限上（能进页面、能操作授权管理），
	// 不体现在「能看见哪些数据」上。
	rows, err := matchDataGrants(scope, instanceID, schema, table)
	if err != nil {
		return false, err
	}
	return resolveDataGrantDecision(rows, action), nil
}

// CheckDataAccess 供外部模块调用的便捷校验：传用户名而不是 scope。
// 返回 nil 表示放行，否则返回带资源描述的错误。
//
// 这是所有「查询 / 修改」入口的唯一校验入口（工单、备份下载、恢复、监控采集、AI 工具…），
// 不允许各模块再自己拼一条 SQL 去查授权表。
//
// 三类接口口径（见 access_action_scope.go）：变更(dml)与导出(export)由 SQL 工单
// 自身的「提交→审批→执行」覆盖，不再要求数据访问授权——所以这里对这两类直接放行。
// 这一行必须放在取 scope 之前：不需要授权的操作不该为它多打一次用户/角色查询。
func CheckDataAccess(req DataAccessRequest) error {
	if !ActionNeedsAccessGrant(req.Action) {
		return nil
	}
	scope, err := resolveDASActorScope(req.Username)
	if err != nil {
		return err
	}
	// 灰度保护：新授权表还没有任何记录（迁移未执行）时回退旧模型。
	// 少了这一层，未迁移环境上所有新接入口会一律 403 —— 那不叫收口，叫断供。
	if !dataGrantEnabled() {
		if req.RequireWholeDatabase {
			return fmt.Errorf("%w: Redis Key 浏览需要显式的实例或 DB 级查询授权", ErrNoDataGrant)
		}
		return legacyCheckDataAccess(scope, req)
	}
	var ok bool
	if req.RequireWholeDatabase {
		ok, err = hasWholeDatabaseGrant(scope, req.InstanceID, req.Schema, req.Action)
	} else {
		ok, err = HasDataGrant(scope, req.InstanceID, req.Schema, req.Table, req.Action)
	}
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return fmt.Errorf("%w: 账号 %s 没有对资源 %s 执行 %s 的有效授权", ErrNoDataGrant, req.Username, describeDataResource(req.InstanceID, req.Schema, req.Table), req.Action)
}

// legacyCheckDataAccess 旧模型（insight_das_schema_perms / table_perms）下的等价判定。
//
// 与 CheckUserPerm 保持同一套顺序：实例级授权优先，否则按库/表逐项判定。
// 旧模型没有操作维度（一条库授权同时蕴含查询/导出/变更），因此这里忽略 req.Action，
// 只有迁移完成后的新模型才区分 select / export / dml。
func legacyCheckDataAccess(scope *dasActorScope, req DataAccessRequest) error {
	instanceGranted, err := hasDASInstanceGrant(scope, req.InstanceID)
	if err != nil {
		return err
	}
	if instanceGranted {
		return nil
	}
	checker := CheckUserPerm{
		UserName:   req.Username,
		InstanceID: req.InstanceID,
		Tables:     []parser.Table{{Schema: req.Schema, Table: req.Table}},
	}
	if strings.TrimSpace(req.Table) != "" {
		if err := checker.HasTablePerms(); err != nil {
			return err
		}
	}
	return checker.HasSchemaPerms()
}

// applyDataGrantInstanceVisibilityFilter 用于「数据源列表」：只要该实例上存在
// 一条当前有效的 allow 授权（不论落在实例级 / 库级 / 表级），该实例就可见。
//
// 与 applyDataGrantVisibilityFilter 的差别：后者还要用 schema 列进一步收敛，
// 前者只按 instance_id 关联——数据源列表不需要区分库表粒度。
func applyDataGrantInstanceVisibilityFilter(tx *gorm.DB, alias string, scope *dasActorScope) *gorm.DB {
	if scope == nil {
		return tx.Where("1 = 0")
	}
	instanceCol := "instance_id"
	if alias != "" {
		instanceCol = alias + "." + instanceCol
	}
	now := time.Now()

	principalConds := []string{"(g.principal_type = ? AND g.principal_id = ?)"}
	args := []any{
		DataGrantStatusActive, DataEffectAllow,
		DataActionSelect, DataActionSelect + ",%", "%," + DataActionSelect, "%," + DataActionSelect + ",%",
		now, now,
		dasPrincipalUser, scope.UID,
	}
	roleIDs := make([]uint64, 0)
	for _, principal := range scope.Principals {
		if principal.PrincipalType == dasPrincipalRole {
			roleIDs = append(roleIDs, principal.PrincipalID)
		}
	}
	if len(roleIDs) > 0 {
		principalConds = append(principalConds, "(g.principal_type = ? AND g.principal_id IN ?)")
		args = append(args, dasPrincipalRole, roleIDs)
	}

	sub := fmt.Sprintf(`EXISTS (
		SELECT 1 FROM insight_das_data_grants g
		 WHERE g.instance_id = %s
		   AND g.status = ?
		   AND g.effect = ?
		   AND (g.actions = ? OR g.actions LIKE ? OR g.actions LIKE ? OR g.actions LIKE ?)
		   AND (g.effective_at IS NULL OR g.effective_at <= ?)
		   AND (g.expire_at IS NULL OR g.expire_at > ?)
		   AND (%s)
	)`, instanceCol, strings.Join(principalConds, " OR "))
	return tx.Where(sub, args...)
}

// applyDataGrantVisibilityFilter 用新授权表给「资源列表类」查询做可见性过滤。
//
// 与判定入口 HasDataGrant 的区别：
//
//	HasDataGrant             —— 单点判定，回答「这一个资源能不能访问」；
//	applyDataGrantVisibilityFilter —— 列表过滤，回答「这一批资源里哪些能看到」。
//
// 关联方式是拿外层行自带的两个列（instance_id / schema）去匹配授权记录：
//
//	实例级授权              → 该实例下所有资源可见
//	库级 / 表级授权          → schema_name 命中的那批资源可见
//
// schema 匹配用「相等 + 双向前缀」而不是相等：
//
//	PostgreSQL / SQLServer 的库与 schema 是父子关系（库=db，schema=db.public），
//	而授权记录可能只落在其中一层；双向前缀让两侧都能对上，
//	且不会把 `appdb` 误配到 `appdb2`（前缀必须带分隔点）。
func applyDataGrantVisibilityFilter(tx *gorm.DB, alias string, scope *dasActorScope) *gorm.DB {
	if scope == nil {
		return tx.Where("1 = 0")
	}
	qualified := func(field string) string {
		if alias == "" {
			return field
		}
		return alias + "." + field
	}
	now := time.Now()
	args := make([]any, 0, 12)

	// 操作类型：actions 是逗号分隔串，用四种形态覆盖「唯一 / 首位 / 末位 / 中间」。
	// 不用 FIND_IN_SET —— 那是 MySQL 专有函数，会让单测（sqlite）跑不起来。
	action := DataActionSelect
	actionConds := "(g.actions = ? OR g.actions LIKE ? OR g.actions LIKE ? OR g.actions LIKE ?)"
	actionArgs := []any{action, action + ",%", "%," + action, "%," + action + ",%"}

	principalConds := []string{"(g.principal_type = ? AND g.principal_id = ?)"}
	principalArgs := []any{dasPrincipalUser, scope.UID}
	roleIDs := make([]uint64, 0)
	for _, principal := range scope.Principals {
		if principal.PrincipalType == dasPrincipalRole {
			roleIDs = append(roleIDs, principal.PrincipalID)
		}
	}
	if len(roleIDs) > 0 {
		principalConds = append(principalConds, "(g.principal_type = ? AND g.principal_id IN ?)")
		principalArgs = append(principalArgs, dasPrincipalRole, roleIDs)
	}

	schemaCol := qualified("schema")
	resourceConds := []string{
		"(g.resource_level = ?)",
		fmt.Sprintf("(g.resource_level IN (?, ?) AND (g.schema_name = %s OR g.schema_name LIKE CONCAT(%s, '.%%') OR %s LIKE CONCAT(g.schema_name, '.%%')))",
			schemaCol, schemaCol, schemaCol),
	}
	resourceArgs := []any{DataResourceLevelInstance, DataResourceLevelDatabase, DataResourceLevelTable}

	sub := fmt.Sprintf(`EXISTS (
		SELECT 1 FROM insight_das_data_grants g
		 WHERE g.instance_id = %s
		   AND g.status = ?
		   AND g.effect = ?
		   AND %s
		   AND (g.effective_at IS NULL OR g.effective_at <= ?)
		   AND (g.expire_at IS NULL OR g.expire_at > ?)
		   AND (%s)
		   AND (%s)
	)`,
		qualified("instance_id"),
		actionConds,
		strings.Join(principalConds, " OR "),
		strings.Join(resourceConds, " OR "),
	)

	args = append(args, DataGrantStatusActive, DataEffectAllow)
	args = append(args, actionArgs...)
	args = append(args, now, now)
	args = append(args, principalArgs...)
	args = append(args, resourceArgs...)
	return tx.Where(sub, args...)
}

// describeDataResource 生成人类可读的资源描述，用于报错与审计。
func describeDataResource(instanceID, schema, table string) string {
	parts := []string{instanceID}
	if strings.TrimSpace(schema) != "" {
		parts = append(parts, schema)
	}
	if strings.TrimSpace(table) != "" {
		parts = append(parts, table)
	}
	return strings.Join(parts, " / ")
}

// AccessibleInstanceIDsByGrant 取主体当前有授权的数据源实例 ID 列表。
// 用于列表类接口（数据源树、我的资源统计、工单可选实例）的可见性过滤。
func AccessibleInstanceIDsByGrant(scope *dasActorScope) ([]string, error) {
	if scope == nil {
		return nil, nil
	}
	// 无超管旁路：admin 也只在被显式授权的实例上可见。
	var instanceIDs []string
	now := time.Now()
	tx := global.App.DB.Table("insight_das_data_grants g").
		Select("distinct g.instance_id").
		Where("g.status = ?", DataGrantStatusActive).
		Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
		Where("(g.expire_at IS NULL OR g.expire_at > ?)", now).
		Where("g.effect = ?", DataEffectAllow).
		Where("g.instance_id <> ''")
	tx = applyDataGrantPrincipalFilter(tx, "g", scope)
	if err := tx.Scan(&instanceIDs).Error; err != nil {
		return nil, err
	}
	return instanceIDs, nil
}

// AccessibleSchemasByUsername 取「某账号在某实例上可见的库/Schema 列表」。
//
// 返回 (schemas, allAllowed)：
//   - allAllowed=true  —— 命中实例级授权，该实例下所有库都可见（schemas 为空即可，调用方不要用它过滤）；
//   - allAllowed=false —— 只有 schemas 里的库可见；列表为空表示「一个都看不到」。
//
// 用途：工单页选库下拉、资源树库层级这类「先列出来再选」的入口。
// 少了这一层，用户能看到全部库名、只在真正查询时才被拒——元数据本身也是信息。
func AccessibleSchemasByUsername(username string, instanceID string) ([]string, bool, error) {
	if strings.TrimSpace(username) == "" || strings.TrimSpace(instanceID) == "" {
		return nil, false, nil
	}
	scope, err := resolveDASActorScope(username)
	if err != nil {
		return nil, false, err
	}
	instanceGranted, err := hasDASInstanceGrant(scope, instanceID)
	if err != nil {
		return nil, false, err
	}
	if instanceGranted {
		// 【2026-10-15】全实例授权 + 非 admin：引擎自带系统库不在可见范围内。
		// 这里不能像以前那样直接回「全部放行」——调用方（工单选库下拉）拿不到系统库清单，
		// 只能靠本函数把可见范围收敛好再交出去。显式点名授权的库仍会保留（含系统库）。
		if scope.HasAdminRole {
			return nil, true, nil
		}
		visible, err := visibleSchemasForInstanceGrant(scope, instanceID)
		if err != nil {
			return nil, false, err
		}
		return visible, false, nil
	}
	now := time.Now()
	action := DataActionSelect
	actionCond := "(g.actions = ? OR g.actions LIKE ? OR g.actions LIKE ? OR g.actions LIKE ?)"
	actionArgs := []any{action, action + ",%", "%," + action, "%," + action + ",%"}

	if dataGrantEnabled() {
		var allowed []string
		tx := global.App.DB.Table("insight_das_data_grants g").
			Select("distinct g.schema_name").
			Where("g.instance_id = ? AND g.status = ? AND g.effect = ?", instanceID, DataGrantStatusActive, DataEffectAllow).
			Where("g.schema_name <> ''").
			Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
			Where("(g.expire_at IS NULL OR g.expire_at > ?)", now).
			Where(actionCond, actionArgs...)
		tx = applyDataGrantPrincipalFilter(tx, "g", scope)
		if err := tx.Scan(&allowed).Error; err != nil {
			return nil, false, err
		}
		// deny 优先：被显式拒绝的库要从可见列表里剔除
		var denied []string
		dtx := global.App.DB.Table("insight_das_data_grants g").
			Select("distinct g.schema_name").
			Where("g.instance_id = ? AND g.status = ? AND g.effect = ?", instanceID, DataGrantStatusActive, DataEffectDeny).
			Where("g.schema_name <> ''").
			Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
			Where("(g.expire_at IS NULL OR g.expire_at > ?)", now).
			Where(actionCond, actionArgs...)
		dtx = applyDataGrantPrincipalFilter(dtx, "g", scope)
		if err := dtx.Scan(&denied).Error; err != nil {
			return nil, false, err
		}
		denySet := make(map[string]struct{}, len(denied))
		for _, item := range denied {
			denySet[item] = struct{}{}
		}
		result := make([]string, 0, len(allowed))
		for _, item := range allowed {
			if _, ok := denySet[item]; ok {
				continue
			}
			result = append(result, item)
		}
		return result, false, nil
	}

	// 旧模型：库可见性 = insight_das_schema_perms 里显式授权的库
	var schemas []string
	tx := global.App.DB.Table("insight_das_schema_perms p").
		Select("distinct p.`schema`").
		Where("p.instance_id = ? AND p.`schema` <> '' AND p.`schema` <> ?", instanceID, dasInstanceGrantSchema)
	tx = applyPrincipalFilter(tx, "p", scope)
	if err := tx.Scan(&schemas).Error; err != nil {
		return nil, false, err
	}
	return schemas, false, nil
}

// visibleSchemasForInstanceGrant 取「全实例授权」场景下账号在某实例上真正可见的库清单。
//
// 存在的原因：命中全实例授权时，可见范围不再等价于「全部」——对非 admin 还要扣掉系统库，
// 而调用方（工单选库下拉）拿不到系统库清单，只能由本函数把范围算实再交出去。
// 显式点名授权的库会被 gate 保留，因此「单独授权某个系统库」的用法不受影响。
func visibleSchemasForInstanceGrant(scope *dasActorScope, instanceID string) ([]string, error) {
	type instanceType struct {
		DbType string
	}
	var inst instanceType
	if err := global.App.DB.Table("insight_instances").
		Select("db_type").
		Where("instance_id = ?", instanceID).
		Take(&inst).Error; err != nil {
		return nil, err
	}
	var schemas []string
	if err := global.App.DB.Table("insight_instance_schemas").
		Select("distinct `schema`").
		Where("instance_id = ? AND is_deleted = ?", instanceID, false).
		Scan(&schemas).Error; err != nil {
		return nil, err
	}
	gate := newSystemSchemaGate(scope)
	return gate.filterSchemaPaths(instanceID, inst.DbType, schemas), nil
}

// ExpireStaleGrants 把已过有效期但仍标记为 active 的授权批量置为 expired。
// 由定时任务调用，保证「失效时间」在列表与统计口径上真实生效，而不只是查询时过滤。
func ExpireStaleGrants() (int64, error) {
	// 只改 status，保留 expire_at —— 它既是「为什么失效」的证据，
	// 也是续期时展示原失效时间的依据，清空会让审计无从对照。
	result := global.App.DB.Table("insight_das_data_grants").
		Where("status = ?", DataGrantStatusActive).
		Where("expire_at IS NOT NULL AND expire_at <= ?", time.Now()).
		Updates(map[string]any{
			"status": DataGrantStatusExpired,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// grantTimeWindowValid 判断一条授权是否落在时间窗口内（供单条记录校验与测试使用）
func grantTimeWindowValid(effectiveAt, expireAt *time.Time, now time.Time) bool {
	if effectiveAt != nil && now.Before(*effectiveAt) {
		return false
	}
	if expireAt != nil && !now.Before(*expireAt) {
		return false
	}
	return true
}

// hasWholeDatabaseGrant fails closed when narrower deny rules exist. Redis Key
// browsing currently supports instance/database scopes, not SQL table scopes.
func hasWholeDatabaseGrant(scope *dasActorScope, instanceID, schema, action string) (bool, error) {
	allowed, err := HasDataGrant(scope, instanceID, schema, "", action)
	if err != nil || !allowed {
		return false, err
	}
	now := time.Now()
	tx := global.App.DB.Table("insight_das_data_grants g").Select("g.actions").
		Where("g.instance_id = ? AND g.resource_level IN ? AND g.schema_name IN ? AND g.effect = ? AND g.status = ?", instanceID, []string{DataResourceLevelSchema, DataResourceLevelTable}, []string{schema, ""}, DataEffectDeny, DataGrantStatusActive).
		Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).Where("(g.expire_at IS NULL OR g.expire_at > ?)", now)
	tx = applyDataGrantPrincipalFilter(tx, "g", scope)
	var rows []dataGrantRow
	if err := tx.Scan(&rows).Error; err != nil {
		return false, err
	}
	for _, row := range rows {
		if grantAllowsAction(row.Actions, action) {
			return false, nil
		}
	}
	return true, nil
}
