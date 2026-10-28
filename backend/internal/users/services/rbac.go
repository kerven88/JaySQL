package services

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/users/models"
	"github.com/simajiong/jaysql/pkg/pagination"
	"gorm.io/gorm"
)

type PermissionListService struct {
	PaginationQ pagination.Pagination
	Search      string
	C           *gin.Context
}

type permissionListItem struct {
	models.InsightPermissions
}

type roleAccessPayload struct {
	ID            uint64   `json:"id"`
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	IsBuiltin     bool     `json:"is_builtin"`
	RoleType      string   `json:"role_type"`
	PermissionIDs []uint64 `json:"permission_ids"`
}

type userAccessPayload struct {
	RoleIDs             []uint64            `json:"role_ids"`
	PrimaryRoleID       uint64              `json:"primary_role_id"`
	AddonRoleIDs        []uint64            `json:"addon_role_ids"`
	DirectPermissionIDs []uint64            `json:"direct_permission_ids"`
	PrimaryRole         *roleAccessPayload  `json:"primary_role,omitempty"`
	AddonRoles          []roleAccessPayload `json:"addon_roles"`
	Roles               []roleAccessPayload `json:"roles"`
	EffectivePermission []string            `json:"effective_permissions"`
}

type GetUserAccessService struct {
	UID uint64
}

func (s *PermissionListService) Run() (interface{}, int64, error) {
	var permissions []permissionListItem
	tx := global.App.DB.Table("insight_permissions")
	if s.Search != "" {
		tx = tx.Where("code like ? or name like ? or description like ?", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	tx = tx.Where("is_enabled = ?", true).Order("module asc, code asc")
	total := pagination.Pager(&s.PaginationQ, tx, &permissions)
	return permissions, total, nil
}

func GetPermissionCodesByUsername(username string) ([]string, error) {
	// 同名多行（软删除的历史行 + 新建的同名行）必须解析到「登录时真正使用的那一行」，
	// 否则会把已删除旧 uid 的空权限当成当前用户的权限，见 services/identity.go 的说明。
	user, err := CanonicalUserByUsername(username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, err
	}
	// 解析到的是历史行（已软删/未激活）时不再授予任何权限，避免「幽灵账号」继续持有权限。
	if !IsEffectiveUser(user) {
		return []string{}, nil
	}
	if user.IsSuperuser {
		return []string{"*"}, nil
	}
	return getEffectivePermissionCodesByUID(user.Uid)
}

func getEffectivePermissionCodesByUID(uid uint64) ([]string, error) {
	var codes []string
	query := `
		SELECT DISTINCT p.code
		FROM insight_permissions p
		WHERE p.is_enabled = 1
		  AND (
			EXISTS (
				SELECT 1
				FROM insight_user_permissions up
				WHERE up.uid = ? AND up.permission_id = p.id
			)
			OR EXISTS (
				SELECT 1
				FROM insight_user_roles ur
				JOIN insight_roles r ON ur.role_id = r.id
				JOIN insight_role_permissions rp ON ur.role_id = rp.role_id
				WHERE ur.uid = ? AND r.is_deleted = 0 AND rp.permission_id = p.id
			)
		  )
		ORDER BY p.code ASC
	`
	if err := global.App.DB.Raw(query, uid, uid).Scan(&codes).Error; err != nil {
		return nil, err
	}

	// 补充父级"门栓权限"：当前端路由（如 view.admin /admin）顶层要求 system.access 时，
	// 如果后端返回的有效权限集合里只有 system.instance.view 这种子项，却没有 system.access，
	// 则前端 filterAsyncRoutes 会把整个 /admin 父路由剪掉（侧边栏菜单系统管理直接消失），
	// 让用户以为"授了权限却看不到"。
	// 后端中间件 RequirePermission 不会单独把 system.access 当作 API 层鉴权（真正的接口都挂
	// system.environment.view / system.instance.view 这类具体权限，system.access 仅在
	// middleware.HasAdminPermission 处使用（目前后端路由树里没用到）。因此只要用户**具备
	// 任一 system.<sub>.<action> 权限**，就虚拟补一个 system.access，让父级菜单能露出来。
	if len(codes) > 0 {
		hasSystemChild := false
		hasSystemAccess := false
		for _, c := range codes {
			if c == "system.access" {
				hasSystemAccess = true
				break
			}
			if len(c) > len("system.") && c[:len("system.")] == "system." {
				rest := c[len("system."):]
				for i := 0; i < len(rest); i++ {
					if rest[i] == '.' {
						hasSystemChild = true
						break
					}
				}
				if hasSystemChild {
					break
				}
			}
		}
		if hasSystemChild && !hasSystemAccess {
			codes = append(codes, "system.access")
		}
	}

	return codes, nil
}

func syncRolePermissions(tx *gorm.DB, roleID uint64, permissionIDs []uint64) error {
	if err := tx.Where("role_id = ?", roleID).Delete(&models.InsightRolePermissions{}).Error; err != nil {
		return err
	}
	for _, permissionID := range uniqueUint64s(permissionIDs) {
		if permissionID == 0 {
			continue
		}
		if err := tx.Create(&models.InsightRolePermissions{RoleID: roleID, PermissionID: permissionID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func syncUserRoleAndPermissionAssignments(tx *gorm.DB, uid uint64, roleIDs []uint64, permissionIDs []uint64) error {
	if err := tx.Where("uid = ?", uid).Delete(&models.InsightUserRoles{}).Error; err != nil {
		return err
	}
	for _, roleID := range uniqueUint64s(roleIDs) {
		if roleID == 0 {
			continue
		}
		if err := tx.Create(&models.InsightUserRoles{UID: uid, RoleID: roleID}).Error; err != nil {
			return err
		}
	}

	if err := tx.Where("uid = ?", uid).Delete(&models.InsightUserPermissions{}).Error; err != nil {
		return err
	}
	for _, permissionID := range uniqueUint64s(permissionIDs) {
		if permissionID == 0 {
			continue
		}
		if err := tx.Create(&models.InsightUserPermissions{UID: uid, PermissionID: permissionID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func uniqueUint64s(values []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(values))
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *GetUserAccessService) Run() (interface{}, error) {
	roleIDs := make([]uint64, 0)
	if err := global.App.DB.Table("insight_user_roles ur").
		Joins("join insight_roles r on r.id = ur.role_id and r.is_deleted = ?", false).
		Where("ur.uid = ?", s.UID).
		Order("CASE WHEN r.is_builtin = 1 THEN 0 ELSE 1 END ASC, r.id ASC").
		Pluck("ur.role_id", &roleIDs).Error; err != nil {
		return nil, err
	}
	permissionIDs := make([]uint64, 0)
	if err := global.App.DB.Table("insight_user_permissions").Where("uid = ?", s.UID).Pluck("permission_id", &permissionIDs).Error; err != nil {
		return nil, err
	}

	roles := make([]roleAccessPayload, 0)
	if len(roleIDs) > 0 {
		type roleRow struct {
			ID          uint64
			Code        string
			Name        string
			Description string
			IsBuiltin   bool
		}
		var rows []roleRow
		if err := global.App.DB.Table("insight_roles").Select("id, code, name, description, is_builtin").Where("id in ? and is_deleted = ?", roleIDs, false).Scan(&rows).Error; err != nil {
			return nil, err
		}
		roleRowMap := make(map[uint64]roleRow, len(rows))
		for _, row := range rows {
			roleRowMap[row.ID] = row
		}
		rolePermissionMap := make(map[uint64][]uint64)
		type mapping struct {
			RoleID       uint64
			PermissionID uint64
		}
		var mappings []mapping
		if err := global.App.DB.Table("insight_role_permissions").Select("role_id, permission_id").Where("role_id in ?", roleIDs).Scan(&mappings).Error; err != nil {
			return nil, err
		}
		for _, item := range mappings {
			rolePermissionMap[item.RoleID] = append(rolePermissionMap[item.RoleID], item.PermissionID)
		}
		for _, roleID := range roleIDs {
			row, ok := roleRowMap[roleID]
			if !ok {
				continue
			}
			roleType := "custom"
			if row.IsBuiltin {
				switch row.Code {
				case models.BuiltinRoleCodeAdmin:
					roleType = "admin"
				case models.BuiltinRoleCodeMember:
					roleType = "member"
				case models.BuiltinRoleCodeGlobalReadonly:
					roleType = "global_readonly"
				default:
					roleType = "builtin"
				}
			}
			roles = append(roles, roleAccessPayload{
				ID:            row.ID,
				Code:          row.Code,
				Name:          row.Name,
				Description:   row.Description,
				IsBuiltin:     row.IsBuiltin,
				RoleType:      roleType,
				PermissionIDs: uniqueUint64s(rolePermissionMap[row.ID]),
			})
		}
	}

	effectivePermissions, err := getEffectivePermissionCodesByUID(s.UID)
	if err != nil {
		return nil, err
	}
	var primaryRoleID uint64
	var primaryRole *roleAccessPayload
	addonRoleIDs := make([]uint64, 0)
	addonRoles := make([]roleAccessPayload, 0)
	for idx := range roles {
		role := roles[idx]
		switch role.RoleType {
		case "admin", "member", "global_readonly", "builtin":
			if primaryRole == nil {
				primaryRoleID = role.ID
				roleCopy := role
				primaryRole = &roleCopy
			}
		}
	}
	return userAccessPayload{
		RoleIDs:             uniqueUint64s(roleIDs),
		PrimaryRoleID:       primaryRoleID,
		AddonRoleIDs:        uniqueUint64s(addonRoleIDs),
		DirectPermissionIDs: uniqueUint64s(permissionIDs),
		PrimaryRole:         primaryRole,
		AddonRoles:          addonRoles,
		Roles:               roles,
		EffectivePermission: effectivePermissions,
	}, nil
}
