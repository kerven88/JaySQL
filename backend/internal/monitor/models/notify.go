package models

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

// ============================================================================
// 监控告警通知渠道
//
// 设计要点（与工单/备份模块的 pkg/notifier 区别）：
//   - 旧模型是"渠道类型单例"：insight_notify_settings 全局一份配置，一个平台只能有一个钉钉群。
//   - 监控需要"渠道实例"：可以有多个钉钉群、多个飞书群、多个收件场景，各自启用与限流。
//   - insight_notify_settings 降级为"类型级默认凭据"（SMTP 账号等），实例可覆盖。
// ============================================================================

// 渠道类型。sms / voice 为二期扩展预留：新增类型只需实现 notifier.Channel 并注册，
// 派发链路（决策 / 队列 / worker / 投递记录）不需要任何改动。
const (
	NotifyKindDingTalk = "dingtalk"
	NotifyKindFeishu   = "feishu"
	NotifyKindTelegram = "telegram"
	NotifyKindEmail    = "email"
	NotifyKindWechat   = "wechat"
	NotifyKindSMS      = "sms"   // 二期
	NotifyKindVoice    = "voice" // 二期
)

// 投递任务状态机：pending → sending → success / failed（skipped_silent 为静默命中时的留痕）
const (
	NotifyTaskPending       = "pending"
	NotifyTaskSending       = "sending"
	NotifyTaskSuccess       = "success"
	NotifyTaskFailed        = "failed"
	NotifyTaskSkippedSilent = "skipped_silent"
)

// InsightMonitorNotifyChannels 渠道实例：一条记录 = 一个可用的发送通道。
type InsightMonitorNotifyChannels struct {
	*commonModels.Model
	Name    string `gorm:"type:varchar(64);not null;default:'';comment:渠道实例名" json:"name"`
	Kind    string `gorm:"type:varchar(16);not null;default:'';index:idx_notify_channel_kind;comment:渠道类型 dingtalk/feishu/email/wechat/sms/voice" json:"kind"`
	Enabled bool   `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"enabled"`

	// Config 实例配置。敏感字段（secret / password / app_secret 等）在入库前用
	// utils.Encrypt 加密后写入；接口回显时按 SecretFields 打码成 ******，绝不回传明文。
	Config datatypes.JSON `gorm:"type:json;null;default:null;comment:实例配置(敏感字段加密)" json:"config"`
	// SecretFields 需要脱敏回显的配置键名列表
	SecretFields datatypes.JSON `gorm:"type:json;null;default:null;comment:需脱敏的配置键" json:"secret_fields"`

	// ScopeType/ScopeValue 仅作默认作用域提示（策略才是真正的路由依据）
	ScopeType  string `gorm:"type:varchar(32);not null;default:'global';comment:默认作用域 global/business/environment/instance" json:"scope_type"`
	ScopeValue string `gorm:"type:varchar(128);not null;default:'';comment:默认作用域值" json:"scope_value"`

	TimeoutSeconds         int `gorm:"type:int;not null;default:8;comment:单次发送超时(秒)" json:"timeout_seconds"`
	MaxAttempt             int `gorm:"type:int;not null;default:4;comment:最大尝试次数" json:"max_attempt"`
	RateLimitPerMin        int `gorm:"type:int;not null;default:0;comment:每分钟发送上限(0=不限)" json:"rate_limit_per_min"`
	AggregateWindowSeconds int `gorm:"type:int;not null;default:0;comment:聚合窗口(秒，0=不聚合，二期)" json:"aggregate_window_seconds"`

	LastTestAt      *commonModels.LocalTime `gorm:"comment:最近测试时间" json:"last_test_at"`
	LastTestOK      bool                    `gorm:"type:boolean;not null;default:false;comment:最近测试是否成功" json:"last_test_ok"`
	LastTestMessage string                  `gorm:"type:varchar(512);not null;default:'';comment:最近测试结果" json:"last_test_message"`

	// FailStreak 连续失败次数：达到阈值由 worker 自动禁用（熔断），避免配置写错时无限重试
	FailStreak int    `gorm:"type:int;not null;default:0;comment:连续失败次数" json:"fail_streak"`
	LastError  string `gorm:"type:varchar(512);not null;default:'';comment:最近错误" json:"last_error"`

	CreatedBy string `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightMonitorNotifyChannels) TableName() string {
	return "insight_monitor_notify_channels"
}

// 接收人类型。notify_group（值班组）为二期扩展预留。
const (
	NotifySubjectUser  = "user"
	NotifySubjectOrg   = "org"
	NotifySubjectGroup = "notify_group"
	NotifySubjectRaw   = "raw"
)

// NotifySubject 接收人条目：配置界面选的是「人 / 组织节点 / 通知组」。
type NotifySubject struct {
	Type string `json:"type"` // user / org / notify_group / raw
	ID   string `json:"id"`   // user=uid；org=organization_key；notify_group=组ID；raw=直接地址
	Note string `json:"note,omitempty"`
}

// InsightMonitorNotifyPolicies 通知策略：把「什么告警」路由到「哪些渠道 + 哪些接收人」。
type InsightMonitorNotifyPolicies struct {
	*commonModels.Model
	Name     string `gorm:"type:varchar(64);not null;default:'';comment:策略名" json:"name"`
	Enabled  bool   `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"enabled"`
	Priority int    `gorm:"type:int;not null;default:100;comment:优先级(小者先)" json:"priority"`

	// 生效条件：空数组 = 不限制该维度
	MatchLevels      datatypes.JSON `gorm:"type:json;null;default:null;comment:告警级别 P0-P3" json:"match_levels"`
	MatchDbTypes     datatypes.JSON `gorm:"type:json;null;default:null;comment:数据库类型" json:"match_db_types"`
	MatchBusiness    datatypes.JSON `gorm:"type:json;null;default:null;comment:所属业务" json:"match_business"`
	MatchEnvs        datatypes.JSON `gorm:"type:json;null;default:null;comment:环境" json:"match_envs"`
	MatchInstances   datatypes.JSON `gorm:"type:json;null;default:null;comment:实例ID" json:"match_instances"`
	MatchLayers      datatypes.JSON `gorm:"type:json;null;default:null;comment:指标层级 host/engine/kernel" json:"match_layers"`
	MatchTemplateIDs datatypes.JSON `gorm:"type:json;null;default:null;comment:告警策略模板ID" json:"match_template_ids"`

	// 通知动作
	ChannelIDs datatypes.JSON `gorm:"type:json;null;default:null;comment:渠道实例ID列表" json:"channel_ids"`
	Subjects   datatypes.JSON `gorm:"type:json;null;default:null;comment:接收人列表" json:"subjects"`

	// 降噪
	SilenceMinutes         int `gorm:"type:int;not null;default:30;comment:同指纹重复通知最小间隔(分钟)" json:"silence_minutes"`
	AggregateWindowSeconds int `gorm:"type:int;not null;default:0;comment:聚合窗口(秒，二期)" json:"aggregate_window_seconds"`
	// RepeatMinutes 持续告警重发间隔：故障持续未恢复/未认领时，每隔 N 分钟重发一次触发通知。
	// 0 = 不重发（只在状态跃迁时通知一次）。已认领（acknowledged）的事件不参与重发。
	RepeatMinutes int `gorm:"type:int;not null;default:0;comment:持续告警重发间隔(分钟,0=不重发)" json:"repeat_minutes"`
	// 升级链（二期）：N 分钟未认领则转升级策略（短信/电话）
	EscalateAfterMinutes int    `gorm:"type:int;not null;default:0;comment:未认领多久后升级(分钟，二期)" json:"escalate_after_minutes"`
	EscalatePolicyID     uint64 `gorm:"type:bigint unsigned;not null;default:0;comment:升级目标策略ID(二期)" json:"escalate_policy_id"`
	StopOnAck            bool   `gorm:"type:boolean;not null;default:true;comment:已认领即停止升级" json:"stop_on_ack"`
	// NotifyRecovery 恢复时是否也通知（只报坏不报好会让人反复人工确认）
	NotifyRecovery bool `gorm:"type:boolean;not null;default:true;comment:恢复时是否通知" json:"notify_recovery"`

	CreatedBy string `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightMonitorNotifyPolicies) TableName() string {
	return "insight_monitor_notify_policies"
}

// InsightMonitorNotifyTasks 投递任务 + 投递记录（合二为一）。
//
// 为什么合并：投递记录就是任务的终态，拆两张表只会带来一致性维护成本；
// "投递记录"页面按 (created_at, status) 索引查询即可满足。
type InsightMonitorNotifyTasks struct {
	*commonModels.Model
	EventID     uint64 `gorm:"type:bigint unsigned;not null;default:0;index:idx_notify_task_event;comment:告警事件ID" json:"event_id"`
	Fingerprint string `gorm:"type:varchar(160);not null;default:'';index:idx_notify_task_fp;comment:告警去重键 instance|metric" json:"fingerprint"`
	InstanceID  string `gorm:"type:char(15);not null;default:'';comment:实例ID" json:"instance_id"`

	PolicyID    uint64 `gorm:"type:bigint unsigned;not null;default:0;index:idx_notify_task_policy;comment:命中策略ID" json:"policy_id"`
	ChannelID   uint64 `gorm:"type:bigint unsigned;not null;default:0;comment:渠道实例ID" json:"channel_id"`
	ChannelKind string `gorm:"type:varchar(16);not null;default:'';comment:渠道类型(冗余便于查询)" json:"channel_kind"`

	// 触发阶段：firing（新告警/升级）/ recovered（恢复）
	Trigger string `gorm:"type:varchar(16);not null;default:'firing';comment:触发阶段 firing/recovered" json:"trigger"`

	// TargetType/TargetValue 投递目标：group=发给群（不按人）；user=发个人地址
	TargetType  string `gorm:"type:varchar(16);not null;default:'';comment:目标类型 group/user" json:"target_type"`
	TargetValue string `gorm:"type:varchar(254);not null;default:'';comment:目标地址(个人渠道)" json:"target_value"`
	TargetLabel string `gorm:"type:varchar(128);not null;default:'';comment:目标展示名" json:"target_label"`

	// Payload 渲染后的消息（标题/正文/链接/提及名单），渠道层直接消费
	Payload datatypes.JSON `gorm:"type:json;null;default:null;comment:渲染后的消息" json:"payload"`

	Status     string `gorm:"type:varchar(16);not null;default:'pending';index:idx_notify_task_status;comment:pending/sending/success/failed/skipped_silent" json:"status"`
	Attempt    int    `gorm:"type:int;not null;default:0;comment:已尝试次数" json:"attempt"`
	MaxAttempt int    `gorm:"type:int;not null;default:4;comment:最大尝试次数" json:"max_attempt"`

	NextRetryAt *commonModels.LocalTime `gorm:"index:idx_notify_task_retry;comment:下次重试时间" json:"next_retry_at"`
	SentAt      *commonModels.LocalTime `gorm:"comment:送达时间" json:"sent_at"`
	DurationMs  int                     `gorm:"type:int;not null;default:0;comment:耗时(毫秒)" json:"duration_ms"`
	ChannelCode string                  `gorm:"type:varchar(64);not null;default:'';comment:渠道返回码" json:"channel_code"`
	LastError   string                  `gorm:"type:varchar(512);not null;default:'';comment:失败原因" json:"last_error"`

	// DedupKey 幂等键，唯一索引防重复投递：event|policy|channel|target|trigger
	DedupKey string `gorm:"type:varchar(200);not null;default:'';uniqueIndex:uniq_notify_task_dedup;comment:幂等键" json:"dedup_key"`
}

func (InsightMonitorNotifyTasks) TableName() string {
	return "insight_monitor_notify_tasks"
}
