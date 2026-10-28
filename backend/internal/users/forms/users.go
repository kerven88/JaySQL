package forms

import (
	"github.com/simajiong/jaysql/pkg/pagination"
)

type GetUsersForm struct {
	PaginationQ    pagination.Pagination
	RoleID         uint64 `form:"role_id"`
	Search         string `form:"search"`
	IsActiveFilter string `form:"is_active_filter" binding:"omitempty,oneof=1 0"`
	RoleScope      string `form:"role_scope"      binding:"omitempty,oneof=builtin custom none"`
}

type CreateUsersForm struct {
	Username      string   `form:"username"  json:"username" binding:"required,min=2,max=32"`
	Password      string   `form:"password"  json:"password" binding:"required,min=7,max=128"`
	Email         string   `form:"email" json:"email" binding:"required,min=3,max=254"`
	Mobile        string   `form:"mobile" json:"mobile"`
	IsSuperuser   bool     `form:"is_superuser" json:"is_superuser" validate:"boolean"`
	IsActive      bool     `form:"is_active" json:"is_active" validate:"boolean" `
	RoleIDs       []uint64 `form:"role_ids" json:"role_ids"`
	PermissionIDs []uint64 `form:"permission_ids" json:"permission_ids"`
}

type RegisterUserForm struct {
	Username        string `form:"username" json:"username" binding:"required,min=2,max=10"`
	Password        string `form:"password" json:"password" binding:"required,min=8,max=128"`
	ConfirmPassword string `form:"confirm_password" json:"confirm_password" binding:"required,min=8,max=128,eqfield=Password"`
	Email           string `form:"email" json:"email" binding:"required,email,min=3,max=254"`
	Mobile          string `form:"mobile" json:"mobile" binding:"omitempty,len=11"`
}

type UpdateUsersForm struct {
	Username      string   `form:"username"  json:"username" binding:"required,min=2,max=32"`
	Password      string   `form:"password"  json:"password" binding:"omitempty,min=7,max=128"`
	Email         string   `form:"email" json:"email" binding:"required,min=3,max=254"`
	Mobile        string   `form:"mobile" json:"mobile"`
	IsSuperuser   bool     `form:"is_superuser" json:"is_superuser" validate:"boolean"`
	IsActive      bool     `form:"is_active" json:"is_active" validate:"boolean"`
	RoleIDs       []uint64 `form:"role_ids" json:"role_ids"`
	PermissionIDs []uint64 `form:"permission_ids" json:"permission_ids"`
}

type BatchDeleteUsersForm struct {
	UIDs []uint64 `form:"uids" json:"uids" binding:"required,min=1,dive,gt=0"`
}

type ResetUsersPasswordForm struct {
	UID            uint64 `form:"uid" json:"uid" binding:"required"`
	Password       string `form:"password" json:"password" binding:"required,min=7,max=32"`
	VerifyPassword string `form:"verify_password" json:"verify_password" binding:"required,min=7,max=32"`
}
