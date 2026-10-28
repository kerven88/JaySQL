package models

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// ============================================================================
// 告警升级链状态
//
// 为什么必须独立一张表（而不是靠洞察 insight_monitor_notify_tasks 反推）：
//   - 升级是"有状态的等待"：需要记住「倒计时锚点 / 当前第几级 / 是否已升级 / 重试了几次」；
//   - 升级必须可取消：认领、恢复、忽略都要立刻终止后续升级，没有状态行就只能靠扫描推断；
//   - 幂等要靠约束保证：(fingerprint, root_policy_id, level) 唯一索引让"重复登记"在数据库层就被挡住，
//     不依赖应用层加锁，多副本部署也不会重复升级。
//
// 本表为新增表，现有表字段结构零改动。
// ============================================================================

// 升级链状态机：pending（待升级）→ escalated（已升级）→ 下一级 pending；
// 任意时刻可被 cancelled（已取消）终止。
const (
	EscalationStatusPending   = "pending"
	EscalationStatusEscalated = "escalated"
	EscalationStatusCancelled = "cancelled"
)

// InsightMonitorAlertEscalations 告警升级链的一级状态。
//
// 一条告警（fingerprint）在一条起始策略（root_policy_id）下最多拥有一条升级链，
// 链上的每一级是一行：level 1 → 2 → 3 …
type InsightMonitorAlertEscalations struct {
	*commonModels.Model
	EventID uint64 `gorm:"type:bigint unsigned;not null;default:0;index:idx_esc_event;comment:告警事件ID" json:"event_id"`
	// 幂等的落点：(告警, 起始策略, 级别) 三元组唯一。
	// 同一级被重复登记时由数据库直接挡下（配合 INSERT ... ON DUPLICATE KEY 不做任何事），
	// 不依赖应用层判断，因此多副本并发也不会刷出两条升级。
	Fingerprint string `gorm:"type:varchar(160);not null;default:'';uniqueIndex:uniq_alert_escalation,priority:1;comment:告警去重键 instance|metric" json:"fingerprint"`
	InstanceID  string `gorm:"type:char(15);not null;default:'';comment:实例ID" json:"instance_id"`

	// RootPolicyID 升级链的起始策略（首轮命中且配置了升级的那条策略）。
	// 保留它才能在页面上回答"这条升级是从哪条策略发起的"。
	RootPolicyID uint64 `gorm:"type:bigint unsigned;not null;default:0;uniqueIndex:uniq_alert_escalation,priority:2;comment:起始策略ID" json:"root_policy_id"`
	// Level 第几级升级，1 起。
	Level int `gorm:"type:int;not null;default:0;uniqueIndex:uniq_alert_escalation,priority:3;comment:升级级别(1起)" json:"level"`
	// FromPolicyID 驱动本级倒计时的策略：它的 escalate_after_minutes 决定本级何时到期。
	// 第 1 级 = 起始策略；第 N 级 = 第 N-1 级升级到的那条策略。
	FromPolicyID uint64 `gorm:"type:bigint unsigned;not null;default:0;comment:驱动倒计时的策略ID" json:"from_policy_id"`
	// PolicyID 本级升级的目标策略：升级通知发给它的渠道与接收人。
	PolicyID uint64 `gorm:"type:bigint unsigned;not null;default:0;comment:本级目标策略ID" json:"policy_id"`

	// AnchorAt 本级倒计时起点：第 1 级 = 告警首次命中时间，第 N 级 = 第 N-1 级实际升级时间。
	AnchorAt commonModels.LocalTime `gorm:"comment:倒计时起点" json:"anchor_at"`
	// DueAt 计划升级时间 = anchor_at + from_policy.escalate_after_minutes。
	//
	// 它是**派生列**：每轮调度按当前配置重算并回写。这样运维把"升级等待"从 30 分钟改成 5 分钟，
	// 下一轮（30 秒内）就立刻生效，不必等旧的倒计时走完 —— 这就是"配置变更实时生效"的落点。
	DueAt *commonModels.LocalTime `gorm:"index:idx_esc_due;comment:计划升级时间(按当前配置实时重算)" json:"due_at"`

	Status      string                  `gorm:"type:varchar(16);not null;default:'pending';index:idx_esc_status;comment:pending/escalated/cancelled" json:"status"`
	EscalatedAt *commonModels.LocalTime `gorm:"comment:实际升级时间" json:"escalated_at"`
	// Attempt 已尝试次数：目标策略暂时不可用（未配渠道 / 接收人无可用地址）时不直接放弃，
	// 按轮次退避重试，超过上限才置为 cancelled 并留错误原因。
	Attempt   int    `gorm:"type:int;not null;default:0;comment:已尝试次数" json:"attempt"`
	LastError string `gorm:"type:varchar(512);not null;default:'';comment:最近错误/取消原因" json:"last_error"`
}

func (InsightMonitorAlertEscalations) TableName() string {
	return "insight_monitor_alert_escalations"
}
