package services

import (
	"errors"
	"fmt"
	"math/rand"
	"mime/multipart"
	"regexp"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"

	dasModels "github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/users/forms"
	"github.com/simajiong/jaysql/internal/users/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var (
	reValidUsername   = regexp.MustCompile(`^[\p{Han}A-Za-z0-9_\-@]+$`)
	reValidEmail      = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	rePasswordDigit   = regexp.MustCompile(`[0-9]`)
	rePasswordUpper   = regexp.MustCompile(`[A-Z]`)
	rePasswordLower   = regexp.MustCompile(`[a-z]`)
	rePasswordSpecial = regexp.MustCompile(`[!@#$%^&*? ]`)
	reValidCNMobile   = regexp.MustCompile(`^1[3-9]\d{9}$`)
)

const (
	minPasswordLength = 8
)

func validateStrongPassword(p string) bool {
	if len(p) < minPasswordLength {
		return false
	}
	if !rePasswordDigit.MatchString(p) {
		return false
	}
	if !rePasswordUpper.MatchString(p) {
		return false
	}
	if !rePasswordLower.MatchString(p) {
		return false
	}
	if !rePasswordSpecial.MatchString(p) {
		return false
	}
	return true
}

type GetUsersServices struct {
	*forms.GetUsersForm
	C *gin.Context
}

func (s *GetUsersServices) Run() (responseData interface{}, total int64, err error) {
	type user struct {
		models.InsightUsers
		Role string `json:"role,omitempty"`
	}
	var users []user
	tx := global.App.DB.Table("insight_users a").
		Select(`a.*, 
			ifnull(
				GROUP_CONCAT(DISTINCT ifnull(d.name, '-/-') ORDER BY d.name ASC SEPARATOR '; '), 
				''
			) as role`).
		Joins("left join insight_user_roles b on a.uid=b.uid").
		Joins("left join insight_roles d on d.id=b.role_id and d.is_deleted = ?", false).
		Where("IFNULL(a.is_deleted,0) = 0").
		Group("a.uid")
	if s.Search != "" {
		tx = tx.Where("`username` like ? or `email` like ? or `mobile` like ?", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	if s.RoleID > 0 {
		tx = tx.Where("b.role_id=?", s.RoleID)
	}
	switch s.IsActiveFilter {
	case "1":
		tx = tx.Where("a.is_active = 1 AND IFNULL(a.is_deleted,0) = 0")
	case "0":
		tx = tx.Where("NOT (a.is_active = 1 AND IFNULL(a.is_deleted,0) = 0)")
	}
	switch s.RoleScope {
	case "builtin":
		sub := global.App.DB.Table("insight_user_roles ur").
			Select("1").
			Joins("JOIN insight_roles r ON ur.role_id=r.id AND r.is_deleted=0 AND r.is_builtin=1").
			Where("ur.uid = a.uid")
		tx = tx.Where("EXISTS (?)", sub)
	case "custom":
		subHasAny := global.App.DB.Table("insight_user_roles ur").
			Select("1").
			Joins("JOIN insight_roles r ON ur.role_id=r.id AND r.is_deleted=0").
			Where("ur.uid = a.uid")
		subHasBuiltin := global.App.DB.Table("insight_user_roles ur").
			Select("1").
			Joins("JOIN insight_roles r ON ur.role_id=r.id AND r.is_deleted=0 AND r.is_builtin=1").
			Where("ur.uid = a.uid")
		tx = tx.Where("EXISTS (?) AND NOT EXISTS (?)", subHasAny, subHasBuiltin)
	case "none":
		sub := global.App.DB.Table("insight_user_roles ur").
			Select("1").
			Joins("JOIN insight_roles r ON ur.role_id=r.id AND r.is_deleted=0").
			Where("ur.uid = a.uid")
		tx = tx.Where("NOT EXISTS (?)", sub)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &users)
	return &users, total, nil
}

type CreateUsersService struct {
	*forms.CreateUsersForm
	C *gin.Context
}

type RegisterUserService struct {
	*forms.RegisterUserForm
	C *gin.Context
}

func (s *CreateUsersService) Run() error {
	roleIDs, isSuperuser, err := validateRoleAssignment(s.RoleIDs, s.PermissionIDs)
	if err != nil {
		return err
	}
	hashedPassword := models.BcryptPW(s.Password)
	user := models.InsightUsers{
		Username:    s.Username,
		Password:    hashedPassword,
		Email:       s.Email,
		Mobile:      s.Mobile,
		IsSuperuser: isSuperuser,
		IsActive:    s.IsActive,
		IsDeleted:   false,
	}
	// 用户唯一性只由 uid（自增主键）决定：username/email/mobile 不再做任何唯一性比对。
	// 不同用户即使用户名/邮箱/手机号相同也允许创建；软删除用户也不再占用"唯一"名额。
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightUsers{}).Create(&user).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		return syncUserRoleAndPermissionAssignments(tx, user.Uid, roleIDs, s.PermissionIDs)
	})
}

func (s *RegisterUserService) Run() error {
	if s.Password != s.ConfirmPassword {
		return errors.New("两次输入的密码不一致")
	}

	trimmedUsername := strings.TrimSpace(s.Username)
	if trimmedUsername == "" {
		return errors.New("用户名不能为空")
	}
	if len([]rune(trimmedUsername)) > 10 {
		return errors.New("用户名长度不能超过 10 个字符")
	}
	if !reValidUsername.MatchString(trimmedUsername) {
		return errors.New("用户名仅支持英文、汉字、数字及 _ - @ 字符")
	}

	trimmedEmail := strings.TrimSpace(s.Email)
	if trimmedEmail == "" {
		return errors.New("邮箱不能为空")
	}
	if !reValidEmail.MatchString(trimmedEmail) {
		return errors.New("请输入合法的邮箱地址")
	}
	if !validateStrongPassword(s.Password) {
		return errors.New("密码长度至少 8 个字符，且必须包含大小写字母、数字和特殊字符")
	}
	trimmedMobile := strings.TrimSpace(s.Mobile)
	if trimmedMobile != "" && !reValidCNMobile.MatchString(trimmedMobile) {
		return errors.New("请输入合法的手机号码")
	}

	hashedPassword := models.BcryptPW(s.Password)
	user := models.InsightUsers{
		Username:    trimmedUsername,
		Password:    hashedPassword,
		Email:       trimmedEmail,
		Mobile:      "",
		IsSuperuser: false,
		IsActive:    true,
		IsDeleted:   false,
	}

	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if trimmedMobile != "" {
			user.Mobile = trimmedMobile
		} else {
			user.Mobile = generateRegisterMobile()
		}
		memberRoleID, err := getBuiltinRoleIDByCode(tx, models.BuiltinRoleCodeMember)
		if err != nil {
			return err
		}
		if err := tx.Model(&models.InsightUsers{}).Create(&user).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		return syncUserRoleAndPermissionAssignments(tx, user.Uid, []uint64{memberRoleID}, nil)
	})
}

type UpdateUsersService struct {
	*forms.UpdateUsersForm
	C   *gin.Context
	UID uint64
}

func (s *UpdateUsersService) Run() error {
	roleIDs, isSuperuser, err := validateRoleAssignment(s.RoleIDs, s.PermissionIDs)
	if err != nil {
		return err
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"username":     s.Username,
			"email":        s.Email,
			"mobile":       s.Mobile,
			"is_active":    s.IsActive,
			"is_superuser": isSuperuser,
		}
		// 管理员把用户重新激活时，自动撤销软删除标记（允许 is_deleted=1 的用户「复活」）
		if s.IsActive {
			updates["is_deleted"] = false
		}
		// 管理员直接重置密码：表单传了非空密码，就写入新的 bcrypt hash
		pw := strings.TrimSpace(s.Password)
		if pw != "" {
			if len(pw) < 7 {
				return fmt.Errorf("新密码长度至少为7个字符")
			}
			updates["password"] = models.BcryptPW(pw)
		}
		if err := tx.Model(&models.InsightUsers{}).Where("uid=?", s.UID).Updates(updates).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		return syncUserRoleAndPermissionAssignments(tx, s.UID, roleIDs, s.PermissionIDs)
	})

}

// 校验角色分配：允许任意组合（内置/自定义均可，可多选），并校验是否越权授予 system.* 权限
func validateRoleAssignment(roleIDs []uint64, permissionIDs []uint64) ([]uint64, bool, error) {
	roleIDs = uniqueUint64s(roleIDs)
	permissionIDs = uniqueUint64s(permissionIDs)

	if len(roleIDs) == 0 {
		return nil, false, fmt.Errorf("请至少分配一个角色")
	}

	var roles []models.InsightRoles
	if err := global.App.DB.Where("id in ? AND is_deleted = ?", roleIDs, false).Find(&roles).Error; err != nil {
		return nil, false, err
	}
	if len(roles) != len(roleIDs) {
		return nil, false, fmt.Errorf("请选择有效的角色")
	}

	// 1. 收集所有角色+直接权限涉及的权限ID（用于后续可能的校验）
	//    说明：RBAC 体系里，「角色」是管理员在「角色管理」里定义并保存的权限集合，
	//    既然管理员在角色管理里允许把某个权限组合保存下来，那么在用户管理里绑定该角色时
	//    就不应该再做越权拦截，否则会出现"角色能保存，绑用户却报错"的自相矛盾。
	//    越权校验只作用在「直接权限」（绕过角色、单独给用户勾选的权限）上，因为
	//    直接权限才是真正跳过了角色审批层的越权风险点。
	allPermIDs := make([]uint64, 0, len(permissionIDs))
	type rolePermMapping struct {
		PermissionID uint64
	}
	roleMap := make(map[uint64]models.InsightRoles, len(roles))
	hasAdminBuiltin := false
	for _, role := range roles {
		roleMap[role.ID] = role
		if role.IsBuiltin && role.Code == models.BuiltinRoleCodeAdmin {
			hasAdminBuiltin = true
		}
		var m []rolePermMapping
		global.App.DB.Table("insight_role_permissions").Select("permission_id").Where("role_id = ?", role.ID).Scan(&m)
		for _, mp := range m {
			allPermIDs = append(allPermIDs, mp.PermissionID)
		}
	}
	allPermIDs = append(allPermIDs, permissionIDs...)
	allPermIDs = uniqueUint64s(allPermIDs)

	// 2. 直接权限（绕过角色单独授权）不再对 system.* 做拦截。
	//    旧版本会在这里强制拦截所有 system.*（除非挂了 admin 内置角色，或挂 global_readonly 且是 system.xxx.view/system.access），
	//    导致"普通成员（member）"这种内置角色用户无法被授予系统管理相关权限。
	//    现在按业务诉求：允许管理员给任何内置角色的用户直接授予 system.* 的任意权限，
	//    真正的权限边界由运行时 middleware.RequirePermission 基于 getEffectivePermissionCodesByUID 返回的 union
	//    （insight_user_permissions ∪ insight_roles→role_permissions）判断，不再在此处做前置白名单。
	//
	//    仍然保留一个纯防御性校验：直接权限必须都是 insight_permissions 中已启用的权限 ID（防止传了不存在的 ID 写脏数据）。
	if len(permissionIDs) > 0 {
		var validCnt int64
		if err := global.App.DB.Table("insight_permissions").
			Where("id in ? AND is_enabled = ?", permissionIDs, true).
			Count(&validCnt).Error; err != nil {
			return nil, false, err
		}
		if int(validCnt) != len(permissionIDs) {
			return nil, false, fmt.Errorf("存在无效或已禁用的权限ID，请刷新后重试")
		}
	}

	// 3. 收集角色中是否存在直接传了管理员角色，存在则标记 is_superuser = true
	isSuperuser := hasAdminBuiltin

	orderedRoleIDs := make([]uint64, 0, len(roleIDs))
	// 先把内置角色按顺序排前面，自定义排后面（可选顺序保持不变
	for _, id := range roleIDs {
		role := roleMap[id]
		if role.IsBuiltin {
			orderedRoleIDs = append([]uint64{id}, orderedRoleIDs...)
		} else {
			orderedRoleIDs = append(orderedRoleIDs, id)
		}
	}
	return orderedRoleIDs, isSuperuser, nil
}

func buildRegisterEmail(username, email string) string {
	if email != "" {
		return email
	}
	return fmt.Sprintf("%s@local.invalid", username)
}

func generateRegisterMobile() string {
	// mobile 列是 NOT NULL，自助注册未填手机号时生成一个符合中国大陆手机号格式（1[3-9]xxxxxxxxx）的占位值，
	// 满足后端 11 位长度与 /^1[3-9]\d{9}$/ 正则校验，后续用户可在「用户管理/编辑」页面把占位值替换为真实手机号。
	// 注：手机号已不再有唯一性约束，多个用户允许使用同一手机号。
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	prefixOptions := []int{3, 4, 5, 6, 7, 8, 9}
	prefix := prefixOptions[rng.Intn(len(prefixOptions))]
	suffix := rng.Int63n(100000000) // 9 位: 000000000 - 999999999
	return fmt.Sprintf("1%d%09d", prefix, suffix)
}

func getBuiltinRoleIDByCode(tx *gorm.DB, code string) (uint64, error) {
	var role models.InsightRoles
	if err := tx.Where("code = ? AND is_builtin = ? AND is_deleted = ?", code, true, false).Take(&role).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("内置角色`%s`不存在，请先完成系统初始化", code)
		}
		return 0, err
	}
	return role.ID, nil
}

type DeleteUsersService struct {
	C               *gin.Context
	UID             uint64
	CurrentUsername string
}

func deleteUserWithTx(tx *gorm.DB, uid uint64, currentUsername string) error {
	var user models.InsightUsers
	if err := tx.Where("uid = ?", uid).First(&user).Error; err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(user.Username), strings.TrimSpace(currentUsername)) {
		return fmt.Errorf("不能删除当前登录用户")
	}
	// 清理用户-角色关联
	if err := tx.Where("uid = ?", uid).Delete(&models.InsightUserRoles{}).Error; err != nil {
		return err
	}
	// 清理用户-直接权限
	if err := tx.Where("uid = ?", uid).Delete(&models.InsightUserPermissions{}).Error; err != nil {
		return err
	}
	// 清理用户-资源访问权限（DAS 库级授权）
	if err := tx.Where("principal_type = ? AND principal_id = ?", "user", uid).Delete(&dasModels.InsightDasSchemaPerms{}).Error; err != nil {
		return err
	}
	// 清理用户-资源访问权限（DAS 表级授权）
	if err := tx.Where("principal_type = ? AND principal_id = ?", "user", uid).Delete(&dasModels.InsightDasTablePerms{}).Error; err != nil {
		return err
	}
	// 用户软删除：is_deleted=true, is_active=false（兼容已经 is_deleted=1 的用户）
	return tx.Model(&models.InsightUsers{}).Where("uid = ?", uid).
		Updates(map[string]interface{}{
			"is_deleted": true,
			"is_active":  false,
		}).Error
}

func (s *DeleteUsersService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		return deleteUserWithTx(tx, s.UID, s.CurrentUsername)
	})
}

type BatchDeleteUsersService struct {
	C               *gin.Context
	UIDs            []uint64
	CurrentUsername string
}

func (s *BatchDeleteUsersService) Run() (int, error) {
	if len(s.UIDs) == 0 {
		return 0, fmt.Errorf("请选择需要删除的用户")
	}
	seen := make(map[uint64]struct{}, len(s.UIDs))
	uids := make([]uint64, 0, len(s.UIDs))
	for _, uid := range s.UIDs {
		if uid == 0 {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		uids = append(uids, uid)
	}
	if len(uids) == 0 {
		return 0, fmt.Errorf("请选择需要删除的用户")
	}
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		for _, uid := range uids {
			if err := deleteUserWithTx(tx, uid, s.CurrentUsername); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return len(uids), nil
}

type ResetUsersPasswordService struct {
	*forms.ResetUsersPasswordForm
	C *gin.Context
}

func (s *ResetUsersPasswordService) Run() error {
	if s.Password != s.VerifyPassword {
		return errors.New("您两次输入的密码不一致")
	}
	// 加密密码
	hashedPassword := models.BcryptPW(s.Password)
	global.App.DB.Model(&models.InsightUsers{}).Where("uid=? AND is_deleted = ?", s.UID, false).Update("password", hashedPassword)
	return nil
}

type ChangeUserAvatarService struct {
	C        *gin.Context
	Username string
	File     *multipart.FileHeader
}

type GetUserOrganizationsService struct {
	UID uint64
}

func (s *GetUserOrganizationsService) Run() (responseData interface{}, err error) {
	type organization struct {
		OrganizationKey  string `json:"organization_key"`
		OrganizationName string `json:"organization_name"`
		RoleID           uint64 `json:"role_id"`
		RoleName         string `json:"role_name"`
	}

	var organizations []organization

	global.App.DB.Table("insight_org_users b").
		Select(`
			b.organization_key,
			ifnull(
				concat(
					(
						SELECT GROUP_CONCAT(o.name ORDER BY o.name ASC SEPARATOR '/')
						FROM insight_orgs o
						WHERE JSON_CONTAINS(c.path, CONCAT('"', o.key, '"'))
					),
					'/',
					c.name
				),
				c.name
			) as organization_name,
			b.role_id,
			d.name as role_name
		`).
		Joins("JOIN insight_orgs c ON c.key = b.organization_key").
		Joins("LEFT JOIN insight_roles d ON d.id = b.role_id").
		Where("b.uid = ?", s.UID).
		Scan(&organizations)

	return organizations, nil
}

func (s *ChangeUserAvatarService) Run() error {
	// 保存图片文件
	fileName := fmt.Sprintf("%s_%d.jpg", s.Username, time.Now().Unix())
	err := s.C.SaveUploadedFile(s.File, "./media/avatars/"+fileName)
	if err != nil {
		return err
	}

	// 调用业务逻辑：先解析出确定性的目标行，再按 uid 写入，
	// 避免同名多行时把头像写到历史行（或一次更新多行）上。
	target, err := CanonicalUserByUsername(s.Username)
	if err != nil {
		return fmt.Errorf("用户`%s`不存在", s.Username)
	}
	if err := global.App.DB.Model(&models.InsightUsers{}).
		Where("uid = ?", target.Uid).
		Update("avatar_file", "/media/avatars/"+fileName).Error; err != nil {
		return err
	}
	return nil
}
