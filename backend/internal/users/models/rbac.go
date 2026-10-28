package models

import commonModels "github.com/simajiong/jaysql/internal/common/models"

type InsightPermissions struct {
	*commonModels.Model
	Code           string `gorm:"type:varchar(128);not null;uniqueIndex:uniq_permission_code;comment:权限编码" json:"code"`
	Name           string `gorm:"type:varchar(64);not null;comment:权限名称" json:"name"`
	Module         string `gorm:"type:varchar(64);not null;default:'';index:idx_permission_module;comment:模块" json:"module"`
	Resource       string `gorm:"type:varchar(64);not null;default:'';comment:资源" json:"resource"`
	Action         string `gorm:"type:varchar(64);not null;default:'';comment:动作" json:"action"`
	PermissionType string `gorm:"type:varchar(32);not null;default:'action';comment:权限类型" json:"permission_type"`
	Description    string `gorm:"type:varchar(255);not null;default:'';comment:权限描述" json:"description"`
	IsBuiltin      bool   `gorm:"type:boolean;not null;default:true;comment:是否内置" json:"is_builtin"`
	IsEnabled      bool   `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"is_enabled"`
}

func (InsightPermissions) TableName() string {
	return "insight_permissions"
}

type InsightRolePermissions struct {
	*commonModels.Model
	RoleID       uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_role_permission;index:idx_role_permission_role;comment:角色ID" json:"role_id"`
	PermissionID uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_role_permission;index:idx_role_permission_permission;comment:权限ID" json:"permission_id"`
}

func (InsightRolePermissions) TableName() string {
	return "insight_role_permissions"
}

type InsightUserRoles struct {
	*commonModels.Model
	UID    uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_user_role;index:idx_user_role_uid;comment:用户ID" json:"uid"`
	RoleID uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_user_role;index:idx_user_role_role;comment:角色ID" json:"role_id"`
}

func (InsightUserRoles) TableName() string {
	return "insight_user_roles"
}

type InsightUserPermissions struct {
	*commonModels.Model
	UID          uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_user_permission;index:idx_user_permission_uid;comment:用户ID" json:"uid"`
	PermissionID uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_user_permission;index:idx_user_permission_permission;comment:权限ID" json:"permission_id"`
}

func (InsightUserPermissions) TableName() string {
	return "insight_user_permissions"
}
