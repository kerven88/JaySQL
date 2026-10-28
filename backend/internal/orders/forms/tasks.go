package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type GenOrderTasksForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
}

type GetTasksForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
	Progress    string `form:"progress" json:"progress"`
}

type PreviewTasksForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
}

type ExecuteTaskForm struct {
	TaskID  string `form:"task_id" json:"task_id" binding:"required,min=3,max=15"`
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	ErrorPolicy string `form:"error_policy" json:"error_policy" binding:"omitempty,oneof=STOP CONTINUE ROLLBACK"`
	BackupPolicy string `form:"backup_policy" json:"backup_policy" binding:"omitempty,oneof=CONTINUE STOP"`
	ExecutionMode string `form:"execution_mode" json:"execution_mode" binding:"omitempty,oneof=IMMEDIATE SCHEDULED MANUAL"`
	ScheduledAt   string `form:"scheduled_at" json:"scheduled_at"`
}

type ExecuteBatchTasksForm struct {
	OrderID       string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	ErrorPolicy   string `form:"error_policy" json:"error_policy" binding:"omitempty,oneof=STOP CONTINUE ROLLBACK"`
	BackupPolicy  string `form:"backup_policy" json:"backup_policy" binding:"omitempty,oneof=CONTINUE STOP"`
	ExecutionMode string `form:"execution_mode" json:"execution_mode" binding:"omitempty,oneof=IMMEDIATE SCHEDULED MANUAL"`
	ScheduledAt   string `form:"scheduled_at" json:"scheduled_at"`
}
