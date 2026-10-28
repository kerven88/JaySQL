package forms

import (
	"encoding/json"

	"github.com/simajiong/jaysql/pkg/pagination"
)

type AdminSQLStandardListForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
}

type AdminSQLStandardSaveForm struct {
	ID          uint64 `json:"id" form:"id"`
	Name        string `json:"name" form:"name" binding:"required,min=1,max=128"`
	Description string `json:"description" form:"description" binding:"max=512"`
	Enabled     bool   `json:"enabled" form:"enabled"`
}

type AdminSQLStandardRuleUpdateForm struct {
	RuleID      uint64          `json:"rule_id" form:"rule_id" binding:"required"`
	Level       string          `json:"level" form:"level" binding:"required,oneof=must suggest off"`
	Params      json.RawMessage `json:"params" form:"params"`
	Description string          `json:"description" form:"description" binding:"max=512"`
}

type AdminSQLStandardInstanceListForm struct {
	PaginationQ pagination.Pagination
	StandardID  uint64 `form:"standard_id" binding:"required"`
}

type AdminSQLStandardBindInstanceForm struct {
	StandardID uint64 `json:"standard_id" form:"standard_id" binding:"required"`
	InstanceID string `json:"instance_id" form:"instance_id" binding:"required,len=15"`
}

type AdminSQLStandardAvailableInstancesForm struct {
	PaginationQ pagination.Pagination
	StandardID  uint64 `form:"standard_id" binding:"required"`
	Search      string `form:"search"`
}
