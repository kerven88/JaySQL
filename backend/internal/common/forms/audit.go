package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type GetAuditLogsForm struct {
	PaginationQ      pagination.Pagination
	StartTime        string `form:"start_time"`
	EndTime          string `form:"end_time"`
	OperatorUsername string `form:"operator_username"`
	AuditLevel       string `form:"audit_level"`
	ModuleCode       string `form:"module_code"`
	Keyword          string `form:"keyword"`
	ActionResult     string `form:"action_result"`
}
