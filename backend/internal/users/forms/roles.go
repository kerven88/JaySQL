package forms

import (
	"github.com/simajiong/jaysql/pkg/pagination"
)

type GetRolesForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
}

type CreateRolesForm struct {
	Code          string   `form:"code"  json:"code" binding:"required,min=2,max=64"`
	Name          string   `form:"name"  json:"name" binding:"required,min=2,max=32"`
	Description   string   `form:"description" json:"description" binding:"max=256"`
	PermissionIDs []uint64 `form:"permission_ids" json:"permission_ids"`
}

type UpdateRolesForm struct {
	Code          string   `form:"code"  json:"code" binding:"required,min=2,max=64"`
	Name          string   `form:"name"  json:"name" binding:"required,min=2,max=32"`
	Description   string   `form:"description" json:"description" binding:"max=256"`
	PermissionIDs []uint64 `form:"permission_ids" json:"permission_ids"`
}
