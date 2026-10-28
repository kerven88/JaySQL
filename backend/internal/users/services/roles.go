package services

import (
	"fmt"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/simajiong/jaysql/internal/users/forms"
	"github.com/simajiong/jaysql/internal/users/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 不可编辑的内置角色（System Principal 硬编码入口，代码级 bypass/升级依赖 code）：
// admin / global_readonly / super_admin —— 后端会在 UPDATE 时校验不可改编码/不可改名称；
// 其它内置角色（member 普通成员 / viewer 只读成员）允许在 UI 上修改权限集合、名称、描述。
var readonlyBuiltinCodes = map[string]struct{}{
	models.BuiltinRoleCodeAdmin:          {},
	models.BuiltinRoleCodeGlobalReadonly: {},
}

func isReadonlyBuiltinCode(code string) bool {
	c := strings.TrimSpace(code)
	if c == "" {
		return false
	}
	_, hit := readonlyBuiltinCodes[c]
	return hit
}

type GetRolesServices struct {
	*forms.GetRolesForm
	C *gin.Context
}

func (s *GetRolesServices) Run() (responseData interface{}, total int64, err error) {
	type roleItem struct {
		models.InsightRoles
		PermissionIDs []uint64 `json:"permission_ids"`
	}
	var roles []roleItem
	tx := global.App.DB.Table("insight_roles").Where("is_deleted = ?", false)
	if s.Search != "" {
		tx = tx.Where("`name` like ? or `code` like ? ", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	tx = tx.Order("is_builtin DESC, id ASC")
	total = pagination.Pager(&s.PaginationQ, tx, &roles)
	if len(roles) == 0 {
		return &roles, total, nil
	}
	roleIDs := make([]uint64, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}
	type mapping struct {
		RoleID       uint64
		PermissionID uint64
	}
	var mappings []mapping
	if err := global.App.DB.Table("insight_role_permissions").Select("role_id, permission_id").Where("role_id in ?", roleIDs).Scan(&mappings).Error; err != nil {
		return nil, total, err
	}
	rolePermissionMap := make(map[uint64][]uint64)
	for _, item := range mappings {
		rolePermissionMap[item.RoleID] = append(rolePermissionMap[item.RoleID], item.PermissionID)
	}
	for idx := range roles {
		roles[idx].PermissionIDs = uniqueUint64s(rolePermissionMap[roles[idx].ID])
	}
	return &roles, total, nil
}

type CreateRolesService struct {
	*forms.CreateRolesForm
	C *gin.Context
}

func (s *CreateRolesService) Run() error {
	if models.IsBuiltinRoleCode(strings.TrimSpace(s.Code)) {
		return fmt.Errorf("角色编码`%s`与内置角色编码冲突，请更换编码", s.Code)
	}
	var existing models.InsightRoles
	if err := global.App.DB.Where("(code = ? OR name = ?) AND is_deleted = ?", s.Code, s.Name, false).
		Select("id").Limit(1).Take(&existing).Error; err == nil {
		return fmt.Errorf("角色编码或名称已存在，请修改")
	} else if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		role := models.InsightRoles{
			Code:        strings.TrimSpace(s.Code),
			Name:        strings.TrimSpace(s.Name),
			Description: s.Description,
			IsBuiltin:   false,
			IsDeleted:   false,
		}
		if err := tx.Create(&role).Error; err != nil {
			return err
		}
		return syncRolePermissions(tx, role.ID, s.PermissionIDs)
	})
}

type UpdateRolesService struct {
	*forms.UpdateRolesForm
	C  *gin.Context
	ID uint64
}

func (s *UpdateRolesService) Run() error {
	var role models.InsightRoles
	if err := global.App.DB.Where("id = ? AND is_deleted = ?", s.ID, false).First(&role).Error; err != nil {
		return err
	}
	code := strings.TrimSpace(s.Code)
	name := strings.TrimSpace(s.Name)
	if role.IsBuiltin {
		// 内置角色一律不允许改编码（避免 initializeBuiltinRoles 下次启动 code 匹配不上）
		if code != role.Code {
			return fmt.Errorf("内置角色不允许修改编码")
		}
		// 仅"不可编辑的内置角色"（admin/global_readonly/super_admin）才同时锁名称
		if isReadonlyBuiltinCode(role.Code) && name != role.Name {
			return fmt.Errorf("内置角色不允许修改名称")
		}
	} else {
		var dup models.InsightRoles
		err := global.App.DB.Where("(code = ? OR name = ?) AND id <> ? AND is_deleted = ?", code, name, s.ID, false).
			Select("id").Limit(1).Take(&dup).Error
		if err == nil {
			return fmt.Errorf("角色编码或名称已存在，请修改")
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
	}

	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"code":        code,
			"name":        name,
			"description": s.Description,
		}
		if err := tx.Model(&models.InsightRoles{}).Where("id = ?", s.ID).Updates(updates).Error; err != nil {
			return err
		}
		return syncRolePermissions(tx, s.ID, s.PermissionIDs)
	})
}

type DeleteRolesService struct {
	C  *gin.Context
	ID uint64
}

func isUserRecentlyOnline(lastLogin time.Time, isActive bool) bool {
	if !isActive {
		return false
	}
	if lastLogin.IsZero() {
		return false
	}
	return time.Since(lastLogin) < 24*time.Hour
}

func (s *DeleteRolesService) Run() error {
	var role models.InsightRoles
	if err := global.App.DB.Where("id = ? AND is_deleted = ?", s.ID, false).First(&role).Error; err != nil {
		return err
	}
	if role.IsBuiltin {
		return fmt.Errorf("系统内置角色不允许删除")
	}
	type bindUser struct {
		Uid       uint64
		Username  string
		IsActive  bool
		LastLogin commonModels.LocalTime
	}
	var bindUsers []bindUser
	err := global.App.DB.Table("insight_user_roles ur").
		Select("a.uid, a.username, a.is_active, a.last_login").
		Joins("join insight_users a on a.uid = ur.uid and a.is_deleted = ?", false).
		Where("ur.role_id = ?", s.ID).
		Scan(&bindUsers).Error
	if err != nil {
		return err
	}
	if len(bindUsers) > 0 {
		return fmt.Errorf("该角色下还有 %d 个绑定用户，请先解除用户与该角色的绑定关系后再删除", len(bindUsers))
	}
	for _, u := range bindUsers {
		if isUserRecentlyOnline(time.Time(u.LastLogin), u.IsActive) {
			return fmt.Errorf("该角色下存在在线用户（%s），请先让该用户退出登录后再执行删除", u.Username)
		}
	}

	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightRoles{}).Where("id = ?", s.ID).
			Update("is_deleted", true).Error; err != nil {
			return err
		}
		if err := tx.Where("role_id = ?", s.ID).Delete(&models.InsightRolePermissions{}).Error; err != nil {
			return err
		}
		return nil
	})
}
