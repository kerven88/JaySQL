package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type GetPhysicalRestoreRunsForm struct {
	PaginationQ pagination.Pagination
	Search       string `form:"search" json:"search"`
	Status       string `form:"status" json:"status"`
	TargetNodeID uint64 `form:"target_node_id" json:"target_node_id"`
}

type PrecheckPhysicalRestoreForm struct {
	BackupSetID      uint64 `form:"backup_set_id" json:"backup_set_id" binding:"required"`
	RestoreMode      string `form:"restore_mode" json:"restore_mode" binding:"required,oneof=NEW_INSTANCE EXISTING_INSTANCE"`
	TargetInstanceID uint64 `form:"target_instance_id" json:"target_instance_id"`
	TargetNodeID     uint64 `form:"target_node_id" json:"target_node_id"`
	TargetDataDir    string `form:"target_data_dir" json:"target_data_dir" binding:"omitempty,min=1,max=1024"`
	TargetPort       int    `form:"target_port" json:"target_port" binding:"omitempty,min=1,max=65535"`
	AutoStart        *bool  `form:"auto_start" json:"auto_start"`
}

type CreatePhysicalRestoreRunForm struct {
	Name             string `form:"name" json:"name" binding:"omitempty,max=128"`
	BackupSetID      uint64 `form:"backup_set_id" json:"backup_set_id" binding:"required"`
	RestoreMode      string `form:"restore_mode" json:"restore_mode" binding:"required,oneof=NEW_INSTANCE EXISTING_INSTANCE"`
	TargetInstanceID uint64 `form:"target_instance_id" json:"target_instance_id"`
	TargetNodeID     uint64 `form:"target_node_id" json:"target_node_id"`
	TargetDataDir    string `form:"target_data_dir" json:"target_data_dir" binding:"omitempty,min=1,max=1024"`
	TargetPort       int    `form:"target_port" json:"target_port" binding:"omitempty,min=1,max=65535"`
	AutoStart        *bool  `form:"auto_start" json:"auto_start"`
	ConfirmText      string `form:"confirm_text" json:"confirm_text" binding:"required,oneof=RECOVER"`
}

type UpdatePhysicalRestoreRunForm struct {
	Name             string `form:"name" json:"name" binding:"omitempty,max=128"`
	BackupSetID      uint64 `form:"backup_set_id" json:"backup_set_id" binding:"required"`
	RestoreMode      string `form:"restore_mode" json:"restore_mode" binding:"required,oneof=NEW_INSTANCE EXISTING_INSTANCE"`
	TargetInstanceID uint64 `form:"target_instance_id" json:"target_instance_id"`
	TargetNodeID     uint64 `form:"target_node_id" json:"target_node_id"`
	TargetDataDir    string `form:"target_data_dir" json:"target_data_dir" binding:"omitempty,min=1,max=1024"`
	TargetPort       int    `form:"target_port" json:"target_port" binding:"omitempty,min=1,max=65535"`
	AutoStart        *bool  `form:"auto_start" json:"auto_start"`
}

type PhysicalRestoreRunIDForm struct {
	ID uint64 `uri:"id" binding:"required"`
}

type GetPhysicalRestoreRunLogForm struct {
	TailKB int64 `form:"tail_kb" json:"tail_kb"`
}
