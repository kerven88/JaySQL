package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
	usersModels "github.com/simajiong/jaysql/internal/users/models"
	"gorm.io/gorm"
)

const (
	dasPrincipalUser        = "user"
	dasPrincipalRole        = "role"
	dasResourceTypeInstance = "instance"
	dasInstanceGrantSchema  = "*"
	builtinRoleCodeAdmin    = "admin"
)

type dasPrincipal struct {
	PrincipalType string
	PrincipalID   uint64
}

type dasActorScope struct {
	UID         uint64
	Username    string
	IsSuperuser bool
	Principals  []dasPrincipal
	// HasAdminRole 主体是否持有内置 admin 角色（code='admin'）。
	//
	// 【2026-10-15】它只影响「系统库要不要露出来」这一件事，不再参与数据可见性判定——
	// 数据可见性的唯一权威仍是 insight_das_data_grants（见 data_grant.go）。
	// 这里单独存一份，是因为 Principals 里只有 role_id 没有 code，
	// 每次判定时再回查 insight_roles 会让列表接口退化成 N+1 查询。
	HasAdminRole bool
}

type missingGrantRow struct {
	InstanceID string
}

func insertIgnoreInstanceGrants(tx *gorm.DB, rows []missingGrantRow, principalType string, principalID uint64, principalName string, username string) {
	if len(rows) == 0 {
		return
	}
	args := make([]any, 0, len(rows)*7)
	for _, r := range rows {
		args = append(args,
			principalType,
			principalID,
			principalName,
			username,
			dasInstanceGrantSchema,
			dasResourceTypeInstance,
			r.InstanceID,
		)
	}
	sql := fmt.Sprintf(
		"INSERT IGNORE INTO insight_das_schema_perms (principal_type, principal_id, principal_name, username, `schema`, resource_type, instance_id, created_at, updated_at) VALUES %s",
		strings.TrimRight(strings.Repeat("(?, ?, ?, ?, ?, ?, ?, NOW(), NOW()),", len(rows)), ","),
	)
	_ = tx.Exec(sql, args...).Error
}

func ensureMissingGrantsForRole(tx *gorm.DB, roleID uint64, roleCode string, roleName string) {
	if roleID == 0 {
		return
	}
	pType := dasPrincipalRole
	compatKey := buildCompatibilityPrincipalKey(pType, roleID, "")
	pName := roleName
	if pName == "" {
		pName = roleCode
	}
	var missing []missingGrantRow
	err := tx.Raw(`
		SELECT i.instance_id
		  FROM insight_instances i
		  LEFT JOIN insight_das_schema_perms p
		    ON p.instance_id = i.instance_id
		   AND p.principal_type = ?
		   AND p.principal_id   = ?
		   AND p.schema         = ?
		   AND p.resource_type  = ?
		 WHERE p.id IS NULL
		 LIMIT 5000
	`, pType, roleID, dasInstanceGrantSchema, dasResourceTypeInstance).Scan(&missing).Error
	if err != nil {
		return
	}
	insertIgnoreInstanceGrants(tx, missing, pType, roleID, pName, compatKey)
}

func ensureMissingGrantsForCreatorUser(tx *gorm.DB, uid uint64, username string) {
	if uid == 0 || strings.TrimSpace(username) == "" {
		return
	}
	pType := dasPrincipalUser
	var missing []missingGrantRow
	err := tx.Raw(`
		SELECT i.instance_id
		  FROM insight_instances i
		  LEFT JOIN insight_das_schema_perms p
		    ON p.instance_id = i.instance_id
		   AND p.principal_type = ?
		   AND p.principal_id   = ?
		   AND p.schema         = ?
		   AND p.resource_type  = ?
		 WHERE i.created_by = ?
		   AND p.id IS NULL
		 LIMIT 5000
	`, pType, uid, dasInstanceGrantSchema, dasResourceTypeInstance, username).Scan(&missing).Error
	if err != nil {
		return
	}
	insertIgnoreInstanceGrants(tx, missing, pType, uid, username, username)
}

// ensureSelfServiceGrantsForScope 显式授权改造前的「隐式自服务授权」入口。
//
// 【2026-10-15 改造】默认彻底关闭。
// 旧行为会给 code='admin' 的角色补齐**全部实例**授权、给实例创建人补齐自建实例授权，
// 导致「管理员默认可见全部数据、且查不到授权人」。这与最小权限原则冲突，
// 也无法满足合规审计要求。改造后数据可见性的唯一来源是 insight_das_data_grants
// 里的显式授权（见 data_grant.go）。
//
// 保留 das_legacy_self_service_grant 开关（默认 false）仅作线上紧急回退：
// 置 true 时恢复旧行为。正常态下本函数是空操作。
func ensureSelfServiceGrantsForScope(scope *dasActorScope) {
	if scope == nil {
		return
	}
	if !getLegacySelfServiceGrantEnabled() {
		return
	}
	tx := global.App.DB
	for _, p := range scope.Principals {
		if p.PrincipalType != dasPrincipalRole {
			continue
		}
		var role usersModels.InsightRoles
		if err := tx.Table("insight_roles").
			Where("id = ? AND is_deleted = ?", p.PrincipalID, false).
			Select("id, code, name").
			Take(&role).Error; err != nil {
			continue
		}
		if !strings.EqualFold(role.Code, builtinRoleCodeAdmin) {
			continue
		}
		ensureMissingGrantsForRole(tx, role.ID, role.Code, role.Name)
	}
	if scope.UID > 0 && scope.Username != "" {
		ensureMissingGrantsForCreatorUser(tx, scope.UID, scope.Username)
	}
}

func normalizeDASPrincipalType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case dasPrincipalRole:
		return dasPrincipalRole
	default:
		return dasPrincipalUser
	}
}

func buildCompatibilityPrincipalKey(principalType string, principalID uint64, username string) string {
	if normalizeDASPrincipalType(principalType) == dasPrincipalRole {
		return fmt.Sprintf("role#%d", principalID)
	}
	return username
}

func displayPrincipalName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(fallback)
}

func resolveGrantPrincipalSnapshot(tx *gorm.DB, principalType string, principalID uint64) (compatKey string, principalName string, err error) {
	switch normalizeDASPrincipalType(principalType) {
	case dasPrincipalRole:
		var role usersModels.InsightRoles
		if err = tx.Table("insight_roles").Where("id = ? AND is_deleted = ?", principalID, false).Take(&role).Error; err != nil {
			return "", "", err
		}
		return buildCompatibilityPrincipalKey(dasPrincipalRole, principalID, ""), displayPrincipalName(role.Name, role.Code), nil
	default:
		var user usersModels.InsightUsers
		if err = tx.Table("insight_users").Where("uid = ? AND is_deleted = ? AND is_active = ?", principalID, false, true).Take(&user).Error; err != nil {
			return "", "", err
		}
		return buildCompatibilityPrincipalKey(dasPrincipalUser, principalID, user.Username), displayPrincipalName(user.Username, user.Username), nil
	}
}

func resolveDASActorScope(username string) (*dasActorScope, error) {
	var user usersModels.InsightUsers
	if err := global.App.DB.Table("insight_users").Where("username = ? AND is_deleted = ? AND is_active = ?", username, false, true).Take(&user).Error; err != nil {
		return nil, err
	}
	scope := &dasActorScope{
		UID:         user.Uid,
		Username:    user.Username,
		IsSuperuser: user.IsSuperuser,
		Principals: []dasPrincipal{
			{PrincipalType: dasPrincipalUser, PrincipalID: user.Uid},
		},
	}
	var userRoles []usersModels.InsightUserRoles
	if err := global.App.DB.Table("insight_user_roles").Where("uid = ?", user.Uid).Find(&userRoles).Error; err != nil {
		return nil, err
	}
	for _, item := range userRoles {
		scope.Principals = append(scope.Principals, dasPrincipal{PrincipalType: dasPrincipalRole, PrincipalID: item.RoleID})
	}
	scope.HasAdminRole = hasBuiltinAdminRole(userRoles)
	return scope, nil
}

// hasBuiltinAdminRole 判断用户所属角色里是否含内置 admin 角色。
//
// 只认 code='admin'：角色名可以被随便改成「超级管理员」，code 才是内置约定，
// 拿名字判定等于把权限开关交给了一次改名操作。
//
// 查询失败时返回 false（fail-closed）：宁可按「非 admin」把系统库藏起来，
// 也不能因为一次查询抖动把系统库放给所有人。
func hasBuiltinAdminRole(userRoles []usersModels.InsightUserRoles) bool {
	if len(userRoles) == 0 {
		return false
	}
	roleIDs := make([]uint64, 0, len(userRoles))
	seen := make(map[uint64]struct{}, len(userRoles))
	for _, item := range userRoles {
		if item.RoleID == 0 {
			continue
		}
		if _, ok := seen[item.RoleID]; ok {
			continue
		}
		seen[item.RoleID] = struct{}{}
		roleIDs = append(roleIDs, item.RoleID)
	}
	if len(roleIDs) == 0 {
		return false
	}
	var count int64
	err := global.App.DB.Table("insight_roles").
		Where("id IN ? AND code = ? AND is_deleted = ?", roleIDs, builtinRoleCodeAdmin, false).
		Count(&count).Error
	if err != nil {
		if global.App.Log != nil {
			global.App.Log.Errorf("resolve admin role for user roles %v: %v", roleIDs, err)
		}
		return false
	}
	return count > 0
}

// readDasBoolParam 读取 DAS 相关的布尔型系统参数。
// 参数缺失或解析失败时返回 fallback，避免因为一行配置缺失导致鉴权整体失效。
func readDasBoolParam(configKey string, fallback bool) bool {
	type paramRow struct {
		ConfigValue string
	}
	var row paramRow
	err := global.App.DB.Table("insight_system_params").
		Select("config_value").
		Where("config_key = ?", configKey).
		Take(&row).Error
	if err != nil {
		return fallback
	}
	trimmed := strings.TrimSpace(row.ConfigValue)
	switch strings.ToLower(trimmed) {
	case "true", "1", "yes", "on", "\"true\"":
		return true
	case "false", "0", "no", "off", "\"false\"", "null", "":
		return false
	}
	var v bool
	if err2 := json.Unmarshal([]byte(trimmed), &v); err2 == nil {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// 【2026-10-15 收口】数据授权链路上已不存在「超管旁路」。
//
// 历史上这里有两个开关：
//   - das_allow_superuser_bypass：让 is_superuser 在 9 处判定链上直接放行；
//   - das_legacy_self_service_grant（见 data_grant.go）：恢复「admin 角色补齐全实例授权」。
//
// 两者都与「未授权的数据对 admin 同样不可见」冲突：开关一开就等于给 admin
// 开全库后门，而它的存在本身就会诱导运维在排障时顺手打开。因此数据侧彻底移除。
//
// 超管仍然保留 RBAC 层面的特权（能进所有页面、能操作授权管理），
// 只是「能看到哪些数据」一律由 insight_das_data_grants 决定，与普通用户同规则。
// ---------------------------------------------------------------------------

// applyPrincipalFilter 给「资源列表类」查询加主体可见性过滤。
//
// 【2026-10-15 改造】新授权模型生效后（存量迁移完成），这里整体切到
// insight_das_data_grants 的 EXISTS 过滤，旧表不再参与判定。
// 切换点收敛在这一个函数里，是因为资源树 / 库列表 / 授权明细等多处
// 都复用它——分散改容易漏，漏一处就是一个越权可见的入口。
//
// 【2026-10-15 收口】不再有「超管不过滤」的分支：scope 非 nil 就一定会加过滤条件。
func applyPrincipalFilter(tx *gorm.DB, alias string, scope *dasActorScope) *gorm.DB {
	if scope == nil {
		return tx
	}
	if dataGrantEnabled() {
		// 新模型：按显式授权过滤（未授权即不可见，admin 同样如此）
		return applyDataGrantVisibilityFilter(tx, alias, scope)
	}
	qualified := func(field string) string {
		if alias == "" {
			return field
		}
		return alias + "." + field
	}
	conditions := []string{
		fmt.Sprintf("(%s = ? and %s = ?)", qualified("principal_type"), qualified("principal_id")),
		fmt.Sprintf("((%s = '' or %s = 'user') and %s = 0 and %s = ?)", qualified("principal_type"), qualified("principal_type"), qualified("principal_id"), qualified("username")),
	}
	args := []any{dasPrincipalUser, scope.UID, scope.Username}

	roleIDs := make([]uint64, 0)
	for _, principal := range scope.Principals {
		if principal.PrincipalType == dasPrincipalRole {
			roleIDs = append(roleIDs, principal.PrincipalID)
		}
	}
	if len(roleIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf("(%s = ? and %s in ?)", qualified("principal_type"), qualified("principal_id")))
		args = append(args, dasPrincipalRole, roleIDs)
	}

	return tx.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func GetAccessibleInstanceIDsByUsername(username string) ([]string, bool, error) {
	scope, err := resolveDASActorScope(username)
	if err != nil {
		return nil, false, err
	}
	// 新授权模型生效后（存量迁移完成），可见实例完全由显式授权决定：
	// 没有任何隐式补齐，admin 也不再默认可见全部实例（超管旁路已从数据链路移除）。
	if dataGrantEnabled() {
		instanceIDs, err := AccessibleInstanceIDsByGrant(scope)
		return instanceIDs, false, err
	}
	// 迁移前兼容路径：只读旧表里已存在的显式授权行。
	// ensureSelfServiceGrantsForScope 默认已是空操作（受 das_legacy_self_service_grant 控制），
	// 仅线上紧急回退时才恢复隐式补齐。
	ensureSelfServiceGrantsForScope(scope)
	var instanceIDs []string
	tx := global.App.DB.Table("insight_das_schema_perms p").Select("distinct p.instance_id")
	if err := applyPrincipalFilter(tx, "p", scope).Scan(&instanceIDs).Error; err != nil {
		return nil, false, err
	}
	return instanceIDs, false, nil
}

// hasDASInstanceGrant 判断主体对某实例是否「可访问」。
//
// 【2026-10-15 改造】新授权模型生效后，唯一依据是 insight_das_data_grants
// 里的显式授权（含 deny 优先、生效/失效时间窗口）。这是「admin 不再默认
// 拥有全实例可见性」的关键落点：不再看角色 code，也不再看创建人。
func hasDASInstanceGrant(scope *dasActorScope, instanceID string) (bool, error) {
	if scope == nil {
		return false, nil
	}
	if dataGrantEnabled() {
		return HasDataGrant(scope, instanceID, "", "", DataActionSelect)
	}
	type countResult struct {
		Count int64
	}
	var result countResult
	tx := global.App.DB.Table("insight_das_schema_perms p").
		Select("count(*) as count").
		Where("p.instance_id = ? and p.resource_type = ? and p.`schema` = ?", instanceID, dasResourceTypeInstance, dasInstanceGrantSchema)
	if err := applyPrincipalFilter(tx, "p", scope).Scan(&result).Error; err != nil {
		return false, err
	}
	return result.Count > 0, nil
}
