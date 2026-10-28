package models

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/pkg/utils"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================
// 用户反馈模块 —— 单表 insight_feedback
//
// 用户通过页面右缘的反馈入口提交问题/建议（含截图），
// 管理员在 系统管理 → 系统配置 → 反馈管理 里查看与处理。
// ============================================================

// 问题类型（与前端下拉选项一一对应）
const (
	CategoryAIAssistant    = "ai_assistant"
	CategorySQLWorkbench   = "sql_workbench"
	CategoryBackupRestore  = "backup_restore"
	CategoryPerformance    = "performance"
	CategorySecurityAudit  = "security_audit"
	CategorySystemAdmin    = "system_admin"
	CategoryOther          = "other"
)

// 处理状态（四态）
const (
	StatusPending    = "pending"    // 待处理
	StatusProcessing = "processing" // 处理中
	StatusResolved   = "resolved"   // 已解决
	StatusClosed     = "closed"     // 已关闭
)

// CategoryOptions 返回「值 → 中文标签」映射，供服务端校验与展示使用
func CategoryOptions() map[string]string {
	return map[string]string{
		CategoryAIAssistant:   "AI助手",
		CategorySQLWorkbench:  "SQL工作台",
		CategoryBackupRestore: "备份恢复",
		CategoryPerformance:   "性能分析",
		CategorySecurityAudit: "安全审计",
		CategorySystemAdmin:   "系统管理",
		CategoryOther:         "其他",
	}
}

// StatusOptions 返回「值 → 中文标签」映射
func StatusOptions() map[string]string {
	return map[string]string{
		StatusPending:    "待处理",
		StatusProcessing: "处理中",
		StatusResolved:   "已解决",
		StatusClosed:     "已关闭",
	}
}

// IsValidCategory 校验问题类型合法性
func IsValidCategory(v string) bool {
	_, ok := CategoryOptions()[v]
	return ok
}

// IsValidStatus 校验处理状态合法性
func IsValidStatus(v string) bool {
	_, ok := StatusOptions()[v]
	return ok
}

// InsightFeedback 用户反馈
type InsightFeedback struct {
	*commonModels.Model

	FeedbackNo string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_feedback_no;comment:业务编号" json:"feedback_no"`
	Category   string `gorm:"type:varchar(32);not null;default:'other';index:idx_feedback_category;comment:问题类型" json:"category"`
	Content    string `gorm:"type:text;comment:意见描述" json:"content"`
	Contact    string `gorm:"type:varchar(128);not null;default:'';comment:联系方式" json:"contact"`

	UserID   uint64 `gorm:"type:bigint unsigned;not null;default:0;index:idx_feedback_user;comment:提交人UID" json:"user_id"`
	Username string `gorm:"type:varchar(64);not null;default:'';index:idx_feedback_username;comment:提交人用户名" json:"username"`

	// Images 图片地址数组（JSON），元素形如 /media/feedback/202610/xxxx.png
	Images    datatypes.JSON `gorm:"type:json;null;default:null;comment:图片URL数组" json:"images"`
	PagePath  string         `gorm:"type:varchar(512);not null;default:'';comment:提交时所在页面路径" json:"page_path"`
	UserAgent string         `gorm:"type:varchar(512);not null;default:'';comment:浏览器UA" json:"user_agent"`

	Status string `gorm:"type:varchar(16);not null;default:'pending';index:idx_feedback_status;comment:处理状态 pending/processing/resolved/closed" json:"status"`
	Remark string `gorm:"type:varchar(1024);not null;default:'';comment:管理员处理备注" json:"remark"`

	HandledBy string                 `gorm:"type:varchar(64);not null;default:'';comment:最近处理人" json:"handled_by"`
	HandledAt commonModels.LocalTime `gorm:"comment:最近处理时间" json:"handled_at"`
}

func (InsightFeedback) TableName() string { return "insight_feedback" }

func (f *InsightFeedback) BeforeCreate(tx *gorm.DB) (err error) {
	if f.FeedbackNo == "" {
		f.FeedbackNo = utils.GenerateFeedbackID()
	}
	if f.Status == "" {
		f.Status = StatusPending
	}
	return
}
