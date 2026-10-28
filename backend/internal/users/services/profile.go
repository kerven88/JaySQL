package services

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/users/forms"
	"github.com/simajiong/jaysql/internal/users/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type GetUserInfoServices struct {
	C        *gin.Context
	Username string
}

func (s *GetUserInfoServices) Run() (responseData interface{}, err error) {
	// 先把 username 解析成唯一一行（同名软删除历史行会让 join+GROUP BY 取到任意一行，
	// 详见 services/identity.go 的说明），之后所有读取都按 uid 进行。
	target, err := CanonicalUserByUsername(s.Username)
	if err != nil {
		return nil, fmt.Errorf("用户`%s`不存在", s.Username)
	}

	var user map[string]interface{}
	tx := global.App.DB.Table("insight_users a").
		Where("a.uid=?", target.Uid).
		Scan(&user)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return user, fmt.Errorf("用户`%s`不存在", s.Username)
	}
	if user == nil {
		user = map[string]interface{}{}
	}
	// 角色名单独取（内置角色），避免 GROUP_CONCAT + GROUP BY a.* 这种依赖
	// ONLY_FULL_GROUP_BY 关闭的写法；没有内置角色时维持历史语义 '-/-'。
	var roleNames []string
	if err := global.App.DB.Table("insight_user_roles b").
		Joins("join insight_roles d on d.id = b.role_id and d.is_builtin = ?", true).
		Where("b.uid = ?", target.Uid).
		Order("d.name asc").
		Distinct().
		Pluck("d.name", &roleNames).Error; err != nil {
		return nil, err
	}
	if len(roleNames) == 0 {
		user["role"] = "-/-"
	} else {
		user["role"] = strings.Join(roleNames, "; ")
	}
	// 不要把手里的密码哈希回传给前端（此前会随 a.* 一并下发）。
	delete(user, "password")
	uidValue := target.Uid
	var permissionIDs []uint64
	if uidValue > 0 {
		if err := global.App.DB.Table("insight_user_permissions").Where("uid = ?", uidValue).Pluck("permission_id", &permissionIDs).Error; err != nil {
			return nil, err
		}
		codes, err := getEffectivePermissionCodesByUID(uidValue)
		if err != nil {
			return nil, err
		}
		user["permission_ids"] = permissionIDs
		user["effective_permissions"] = codes
	} else {
		user["permission_ids"] = []uint64{}
		user["effective_permissions"] = []string{}
	}
	user["organization"] = ""
	return user, err
}

type UpdateUserInfoService struct {
	*forms.UpdateUserInfoForm
	C   *gin.Context
	UID uint32
}

func (s *UpdateUserInfoService) Run() error {
	tx := global.App.DB.Model(&models.InsightUsers{}).Where("uid=?", s.UID)
	data := make(map[string]interface{})
	if s.Mobile != "" {
		data["Mobile"] = s.Mobile
	}
	if s.Email != "" {
		data["Email"] = s.Email
	}
	tx.Updates(data)
	if tx.Error != nil {
		return tx.Error
	}
	return nil
}

// 用户更改密码
type ChangeUserPasswordService struct {
	*forms.ChangeUserPasswordForm
	C        *gin.Context
	Username string
}

func (s *ChangeUserPasswordService) Run() error {
	if err := validatePasswordConfirmation(s.NewPassword, s.ConfirmPassword); err != nil {
		return err
	}

	var user models.InsightUsers
	// 按 username 裸查会落到软删除的同名历史行（改了它的密码，当前账号却毫无变化），
	// 因此先解析出确定性的目标行，再按 uid 读改。
	target, err := CanonicalUserByUsername(s.Username)
	if err != nil {
		return fmt.Errorf("用户不存在，无法修改密码")
	}
	if err := global.App.DB.Model(&models.InsightUsers{}).Where("uid=?", target.Uid).Take(&user).Error; err != nil {
		return fmt.Errorf("用户不存在，无法修改密码")
	}
	// 验证老密码是否正确
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(s.OldPassword))
	if err != nil {
		return fmt.Errorf("密码更改失败，请检查旧密码是否正确")
	}
	// 验证新老密码是否一致
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(s.NewPassword))
	if err == nil {
		return fmt.Errorf("密码更改失败，新密码不能与旧密码相同")
	}
	// 加密密码
	hashedPassword := models.BcryptPW(s.NewPassword)
	global.App.DB.Model(&models.InsightUsers{}).Where("uid=?", target.Uid).Update("password", hashedPassword)
	return nil
}

func validatePasswordConfirmation(newPassword, confirmPassword string) error {
	if newPassword != confirmPassword {
		return fmt.Errorf("密码更改失败，两次输入的新密码不一致")
	}
	return nil
}
