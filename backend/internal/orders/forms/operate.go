package forms

type ApprovalOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
	Status  string `form:"status" json:"status" binding:"required,oneof=APPROVED REJECTED"`
}

type SubmitApprovalForm struct {
	OrderID  string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Approver string `form:"approver" json:"approver" binding:"required,max=32"`
	// NotifyChannels 提交时选择的审批通知渠道。目前仅支持 "dingtalk"（钉钉企业应用），
	// 留空表示不额外发钉钉通知，只走平台原有的通知配置。
	// 之所以做成"提交人可选"而不是全局开关：同一次提交可能发给不同的审批人，
	// 而不同审批人的接收习惯不同，这个选择权应该在提交人手里。
	NotifyChannels []string `form:"notify_channels" json:"notify_channels"`
}

type ClaimOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}
type TransferOrderForm struct {
	OrderID    string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	NewClaimer string `form:"new_claimer" json:"new_claimer" binding:"required,max=32"`
	Msg        string `form:"msg" json:"msg" binding:"max=256"`
}
type RevokeOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}

type UpdateOrderTitleForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Title   string `form:"title" json:"title" binding:"required,max=60"`
}

type CompleteOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}

type FailOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}

type ReviewOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}

type TerminateOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}

// WithdrawOrderForm 撤回工单：执行前（待预审/审批中/审批通过）的工单可由申请人撤回，
// 撤回后工单进入「已撤回」状态，任务不再执行，可编辑后重新提交（工单 ID 保持不变）。
type WithdrawOrderForm struct {
	OrderID string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Msg     string `form:"msg" json:"msg" binding:"max=256"`
}

// UpdateOrderContentForm 编辑已撤回工单的内容：保存后工单回到「待预审」，可再次提交审批。
//
// 撤回后除内容外，还允许改目标数据源（db_type/instance_id）、数据库（database/schema）
// 与执行人（executor）——工单被驳回/撤回的常见原因就是「选错了库」，只让改 SQL 不让换库
// 等于逼用户删单重建。字段为空表示维持原值；一旦传了 instance_id，后端按创建同口径
// 重新校验数据源存在性、目标资源与数据授权（db_type 以实例配置为准，不信任前端传值）。
type UpdateOrderContentForm struct {
	OrderID               string `form:"order_id" json:"order_id" binding:"required,min=3,max=15"`
	Title                 string `form:"title" json:"title" binding:"required,min=1,max=60"`
	Remark                string `form:"remark" json:"remark" binding:"max=2048"`
	Content               string `form:"content" json:"content" binding:"required,min=1"`
	RollbackSQL           string `form:"rollback_sql" json:"rollback_sql" binding:"max=1048576"`
	EstimatedAffectedRows int64  `form:"estimated_affected_rows" json:"estimated_affected_rows"`
	DBType                string `form:"db_type" json:"db_type" binding:"omitempty,max=32"`
	InstanceID            string `form:"instance_id" json:"instance_id" binding:"omitempty,min=3,max=15"`
	Database              string `form:"database" json:"database" binding:"omitempty,max=1024"`
	Schema                string `form:"schema" json:"schema" binding:"omitempty,max=1024"`
	Executor              string `form:"executor" json:"executor" binding:"omitempty,max=32"`
}
