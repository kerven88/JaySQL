package forms

// 参数管理：实例参数实时查询
type GetInstanceParamsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
}

// 参数管理：多实例对比（逗号分隔，2~5 个同家族实例）
type DiffInstanceParamsForm struct {
	InstanceIDs string `form:"instance_ids" json:"instance_ids" binding:"required"`
}

// 参数管理：修改历史
type GetParamChangesForm struct {
	InstanceID string `form:"instance_id" json:"instance_id"`
	ParamName  string `form:"param_name" json:"param_name"`
	Operator   string `form:"operator" json:"operator"`
}

// 参数管理：修改参数
type ModifyInstanceParamForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	ParamName  string `form:"param_name" json:"param_name" binding:"required,min=1,max=128"`
	NewValue   string `form:"new_value" json:"new_value" binding:"required,max=512"`
	Reason     string `form:"reason" json:"reason" binding:"required,min=2,max=1024"`
	AckRisk    bool   `form:"ack_risk" json:"ack_risk"`
}

// 参数管理：回滚
type RollbackParamForm struct {
	ChangeID uint64 `form:"change_id" json:"change_id" binding:"required"`
}
