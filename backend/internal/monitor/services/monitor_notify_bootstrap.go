package services

import (
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// ============================================================================
// 通知配置一次性迁移
//
// 背景：新模型（渠道实例 + 通知策略）上线前，平台只有两处旧配置：
//  1. insight_notify_settings —— 渠道类型级单例（企微/钉钉/邮件各一套 Webhook 或 SMTP）
//  2. insight_monitor_alert_rules.notify_channels / silence_policy —— 前端下拉里存的
//     site/email/webhook 与 night/weekday/maintenance，**后端从未读取过**，属于纯装饰值。
//
// 迁移策略（只做能确定的映射，不做猜测）：
//  - 第 1 处是真实生效的配置 → 迁移为渠道实例（钉钉群 / 企业微信群 / 邮件）。
//  - 第 2 处没有可靠语义（当时既不发站内信也没有 webhook 实现）→ 不迁移，
//    仅打一条日志说明，避免把"假的配置"变成"看起来生效的策略"。
//
// 迁移只在两张表均为空时执行一次，之后靠"表非空"自然幂等，不需要额外标记位。
// ============================================================================

// EnsureNotifyBootstrap 首次启动时按旧配置播种渠道实例与默认策略。
func EnsureNotifyBootstrap() {
	if global.App == nil || global.App.DB == nil {
		return
	}

	var channelCount int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).Count(&channelCount).Error; err != nil {
		global.App.Log.Warnf("[monitor] 统计通知渠道失败: %v", err)
		return
	}
	if channelCount == 0 {
		created := seedNotifyChannelsFromSettings()
		if created > 0 {
			global.App.Log.Infof("[monitor] 已按平台默认通知配置播种 %d 个通知渠道，可在「告警配置 → 通知渠道」中调整", created)
		}
		logLegacyNotifyValuesHint()
	}

	var policyCount int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyPolicies{}).Count(&policyCount).Error; err != nil {
		global.App.Log.Warnf("[monitor] 统计通知策略失败: %v", err)
		return
	}
	if policyCount == 0 {
		if name := seedDefaultNotifyPolicy(); name != "" {
			global.App.Log.Infof("[monitor] 已创建默认通知策略「%s」（群渠道 + 全部告警），可按需细化", name)
		}
	}
}

// seedNotifyChannelsFromSettings 把 insight_notify_settings 里已启用的配置转成渠道实例。
//
// 邮件渠道刻意不复制 SMTP 主机与密码：旧密码字段的加密口径与渠道层不完全一致，
// 直接搬运有双重加密与解密失败的风险。留空即"继承平台默认凭据"，语义上完全等价。
func seedNotifyChannelsFromSettings() int {
	var settings commonModels.InsightNotifySettings
	if err := global.App.DB.Where("config_key = ?", "default").Take(&settings).Error; err != nil {
		return 0
	}

	created := 0
	type seed struct {
		kind string
		name string
		cfg  map[string]any
	}
	seeds := make([]seed, 0, 3)

	if settings.DingTalkEnable && strings.TrimSpace(settings.DingTalkWebhook) != "" {
		seeds = append(seeds, seed{
			kind: monitorModels.NotifyKindDingTalk,
			name: "默认钉钉群（迁移自平台通知配置）",
			cfg: map[string]any{
				"webhook":    strings.TrimSpace(settings.DingTalkWebhook),
				"keywords":   strings.TrimSpace(settings.DingTalkKeywords),
				"at_mobiles": true,
				"at_all":     false,
			},
		})
	}
	if settings.WechatEnable && strings.TrimSpace(settings.WechatWebhook) != "" {
		seeds = append(seeds, seed{
			kind: monitorModels.NotifyKindWechat,
			name: "默认企业微信群（迁移自平台通知配置）",
			cfg: map[string]any{
				"webhook":    strings.TrimSpace(settings.WechatWebhook),
				"at_mobiles": true,
				"at_all":     false,
			},
		})
	}
	if settings.MailEnable && strings.TrimSpace(settings.MailHost) != "" {
		seeds = append(seeds, seed{
			kind: monitorModels.NotifyKindEmail,
			name: "默认邮件（继承平台 SMTP 凭据）",
			cfg:  map[string]any{"subject_prefix": "[数据库告警]"},
		})
	}

	for _, item := range seeds {
		form := NotifyChannelForm{
			Name:    item.name,
			Kind:    item.kind,
			Enabled: boolPtr(true),
			Config:  item.cfg,
		}
		if _, err := SaveNotifyChannel(form, "system"); err != nil {
			// 单个渠道播种失败不影响其余渠道，也不影响主流程
			global.App.Log.Warnf("[monitor] 播种通知渠道 %s 失败: %v", item.kind, err)
			continue
		}
		created++
	}
	return created
}

// seedDefaultNotifyPolicy 建一条"全部告警 → 全部群渠道"的兜底策略。
//
// 只挑群类渠道：邮件等个人渠道必须显式选择接收人，自动填任何人都是越权猜测。
// 没有任何群渠道时返回空串（不建策略，由管理员自行配置）。
func seedDefaultNotifyPolicy() string {
	var rows []monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return ""
	}
	channelIDs := make([]uint64, 0, len(rows))
	for _, row := range rows {
		caps, ok := notifyChannelMetaSafe(row.Kind)
		if !ok || caps.Personalizable {
			continue
		}
		channelIDs = append(channelIDs, row.ID)
	}
	if len(channelIDs) == 0 {
		return ""
	}

	const policyName = "默认通知（全部告警）"
	form := NotifyPolicyForm{
		Name:       policyName,
		Enabled:    boolPtr(true),
		Priority:   100,
		ChannelIDs: channelIDs,
		// 默认策略：每 30 分钟最多打扰一次（含持续告警重发）
		NotifyIntervalMinutes: 30,
		StopOnAck:             boolPtr(true),
		NotifyRecovery:        boolPtr(true),
	}
	if _, err := SaveNotifyPolicy(form, "system"); err != nil {
		global.App.Log.Warnf("[monitor] 创建默认通知策略失败: %v", err)
		return ""
	}
	return policyName
}

// logLegacyNotifyValuesHint 把"历史装饰值不会被迁移"这件事说清楚。
//
// 静默升级最怕的不是少迁移，而是用户以为迁移了。这条日志就是给运维的答复依据。
func logLegacyNotifyValuesHint() {
	var total int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertRules{}).
		Where("notify_channels IS NOT NULL AND notify_channels != '' AND notify_channels != '[]'").
		Count(&total).Error; err != nil || total == 0 {
		return
	}
	global.App.Log.Infof(
		"[monitor] 检测到 %d 条告警规则带有旧版通知渠道/静默选项（site/email/webhook、night/weekday/maintenance）。"+
			"这些值在旧版本中仅作展示、后端未参与投递，因此不做自动映射，请在「告警配置 → 通知策略」中重新配置生效范围。", total)
}

func boolPtr(value bool) *bool {
	return &value
}
