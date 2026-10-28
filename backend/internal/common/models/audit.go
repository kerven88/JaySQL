package models

import "gorm.io/datatypes"

// 平台统一审计日志，供安全审计模块检索与详情展示。
type InsightAuditLogs struct {
	*Model
	AuditID          string         `gorm:"type:varchar(64);not null;uniqueIndex:uniq_audit_id;comment:审计ID" json:"audit_id"`
	RequestID        string         `gorm:"type:varchar(128);not null;default:'';index:idx_request_id;comment:请求ID" json:"request_id"`
	OperatedAt       LocalTime      `gorm:"index:idx_operated_at;comment:操作时间" json:"operated_at"`
	OperatorUsername string         `gorm:"type:varchar(64);not null;default:'';index:idx_operator_username;comment:操作用户名" json:"operator_username"`
	OperatorName     string         `gorm:"type:varchar(64);not null;default:'';comment:操作人显示名" json:"operator_name"`
	OperatorIP       string         `gorm:"type:varchar(64);not null;default:'';comment:操作来源IP" json:"operator_ip"`
	OperatorTerminal string         `gorm:"type:varchar(255);not null;default:'';comment:终端信息" json:"operator_terminal"`
	AuditLevel       EnumType       `gorm:"type:ENUM('normal','high');not null;default:'normal';index:idx_audit_level;comment:审计等级" json:"audit_level"`
	ModuleCode       string         `gorm:"type:varchar(64);not null;default:'';index:idx_module_code;comment:模块编码" json:"module_code"`
	ModuleName       string         `gorm:"type:varchar(64);not null;default:'';comment:模块名称" json:"module_name"`
	ActionCode       string         `gorm:"type:varchar(64);not null;default:'';comment:动作编码" json:"action_code"`
	ActionName       string         `gorm:"type:varchar(128);not null;default:'';comment:动作名称" json:"action_name"`
	ActionResult     EnumType       `gorm:"type:ENUM('success','fail','rejected','cancelled');not null;default:'success';index:idx_action_result;comment:操作结果" json:"action_result"`
	ResourceType     string         `gorm:"type:varchar(64);not null;default:'';comment:目标对象类型" json:"resource_type"`
	ResourceID       string         `gorm:"type:varchar(128);not null;default:'';comment:目标对象ID" json:"resource_id"`
	ResourceName     string         `gorm:"type:varchar(255);not null;default:'';comment:目标对象名称" json:"resource_name"`
	RelatedOrderID   string         `gorm:"type:varchar(64);not null;default:'';index:idx_related_order_id;comment:关联工单ID" json:"related_order_id"`
	Summary          string         `gorm:"type:varchar(512);not null;default:'';comment:操作摘要" json:"summary"`
	ErrorMessage     string         `gorm:"type:varchar(2048);not null;default:'';comment:错误信息" json:"error_message"`
	RequestPayload   datatypes.JSON `gorm:"type:json;null;default:null;comment:请求参数快照" json:"request_payload"`
	BeforeData       datatypes.JSON `gorm:"type:json;null;default:null;comment:变更前快照" json:"before_data"`
	AfterData        datatypes.JSON `gorm:"type:json;null;default:null;comment:变更后快照" json:"after_data"`
	SQLText          string         `gorm:"type:text;null;comment:SQL文本" json:"sql_text"`
}

func (InsightAuditLogs) TableName() string {
	return "insight_audit_logs"
}
