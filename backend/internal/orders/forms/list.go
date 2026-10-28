package forms

import (
	"github.com/simajiong/jaysql/pkg/pagination"
)

type GetOrderListForm struct {
	PaginationQ    pagination.Pagination
	OnlyMyOrders   bool   `form:"only_my_orders"`
	Search         string `form:"search"`
	Progress       string `form:"progress" json:"progress"`
	Environment    int    `form:"environment" json:"environment" `
	Scope          string `form:"scope" json:"scope"`
	InstanceID     string `form:"instance_id" json:"instance_id"`
	InstanceName   string `form:"instance_name" json:"instance_name"`
	Applicant      string `form:"applicant" json:"applicant"`
	StartAt        string `form:"start_at" json:"start_at"`
	EndAt          string `form:"end_at" json:"end_at"`
	NoDbTypeFilter bool   `form:"no_db_type_filter" json:"no_db_type_filter"`
}
