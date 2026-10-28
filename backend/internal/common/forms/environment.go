package forms

import (
	"github.com/simajiong/jaysql/pkg/pagination"
)

type AdminGetEnvironmentForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
}

type AdminCreateEnvironmentForm struct {
	Name          string   `form:"name"  json:"name" binding:"required,min=2,max=32"`
	Color         string   `form:"color" json:"color" binding:"omitempty,max=16"`
	EnvType       string   `form:"env_type" json:"env_type" binding:"required,oneof=dev prod"`
	SQLStandardID *uint64  `form:"sql_standard_id" json:"sql_standard_id" binding:"required"`
	DataSourceIDs []uint64 `form:"data_source_ids" json:"data_source_ids"`
}

type AdminUpdateEnvironmentForm struct {
	Name          string   `form:"name"  json:"name" binding:"required,min=2,max=32"`
	Color         string   `form:"color" json:"color" binding:"omitempty,max=16"`
	EnvType       string   `form:"env_type" json:"env_type" binding:"omitempty,oneof=dev prod"`
	SQLStandardID *uint64  `form:"sql_standard_id" json:"sql_standard_id" binding:"omitempty"`
	DataSourceIDs []uint64 `form:"data_source_ids" json:"data_source_ids"`
}
