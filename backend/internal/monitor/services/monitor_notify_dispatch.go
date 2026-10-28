package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================================
// 通知派发决策
//
// 位置：告警评估（monitor_alerts.go）在事件"新建 / 级别升级 / 恢复"这三个状态变更点调用。
//
// 铁律：本层只写数据库，绝不发网络请求。
//   - 评估 cron 周期 1 分钟，不能被钉钉/飞书的网络抖动拖慢；
//   - 发送失败要能重试，而不是"等下一轮评估"（下一轮可能好几分钟后且语义已变）；
//   - 队列化之后，聚合、静默、限流、熔断、投递记录才有落地的地方。
// ============================================================================

// DispatchAlertNotify 为一条告警事件生成投递任务。trigger: firing / recovered。
//
// 同步执行但只做数据库操作，正常耗时在毫秒级；任何异常都只记日志，
// 绝不让通知链路的问题影响告警评估本身。
func DispatchAlertNotify(eventID uint64, trigger string) {
	if global.App.DB == nil || eventID == 0 {
		return
	}

	var event monitorModels.InsightMonitorAlertEvents
	if err := global.App.DB.Where("id = ?", eventID).Take(&event).Error; err != nil {
		global.App.Log.Warnf("[monitor] 派发通知失败：事件 %d 不存在: %v", eventID, err)
		return
	}
	// 采集异常期间的抑制事件、人工忽略的事件不通知
	if event.Suppressed || event.Ignored {
		return
	}

	instance := loadNotifyInstanceContext(event.InstanceID)
	instance.AlertTemplateID = loadNotifyAlertTemplateID(event.InstanceID)

	ctx := notifyMatchContext{
		Level:           event.Level,
		Severity:        event.Severity,
		DbType:          instance.DbType,
		Business:        instance.Business,
		Environment:     instance.Environment,
		InstanceID:      event.InstanceID,
		Layer:           notifyMetricLayer(instance.DbType, event.MetricKey),
		AlertTemplateID: instance.AlertTemplateID,
	}

	policies := matchNotifyPolicies(ctx)
	if len(policies) == 0 {
		// 没有策略命中是正常状态（用户可能还没配通知），只记 debug 级别信息
		global.App.Log.Infof("[monitor] 告警事件 %d 未命中任何通知策略，跳过通知", eventID)
		return
	}

	noticeURL := ""
	if value, err := notifier.LoadNoticeURL(); err == nil {
		noticeURL = value
	}

	created := 0
	for _, policy := range policies {
		if strings.EqualFold(trigger, "firing") {
			// 升级链登记与实际是否发出通知解耦：登记 = "这条告警进入升级观察"。
			// 幂等（同一级重复登记被唯一键挡下），因此放在静默/聚合判定之前，
			// 不会因为"这轮恰好被合并了"就整条告警失去升级兜底。
			ensureEscalationLevel1(event, policy)
		}
		if strings.EqualFold(trigger, "recovered") {
			if !policy.NotifyRecovery {
				continue
			}
			// 只对"确实通知过或已认领"的告警发恢复：策略是后配的存量待处理告警、或触发通知始终
			// 没投递成功的告警，恢复时补一条"已恢复"只会让人困惑（从没被告知过它告警了）。
			// 已认领的告警算"被告知过"——认领时会向渠道发一条认领通知。
			if !notifyEverDelivered(event.Fingerprint, policy.ID) {
				global.App.Log.Infof("[monitor] 告警事件 %d 未成功投递过触发/认领通知，跳过恢复通知（策略「%s」）",
					eventID, policy.Name)
				continue
			}
		}
		if notifySilenceHit(event, policy, trigger) {
			// 留一条静默痕迹（同一静默窗口内至多一条，不会造成行膨胀）
			recordSilentSkip(event, policy, trigger)
			global.App.Log.Infof("[monitor] 告警事件 %d 命中策略「%s」静默窗口（%d 分钟），本轮不通知",
				eventID, policy.Name, policy.SilenceMinutes)
			continue
		}
		if notifyAggregateHit(event, policy, trigger) {
			// 聚合窗口：窗口内同类告警已发过，本条合并掉（首报不延迟，保证告警即时性）。
			// 留痕不只是记录 —— 它是窗口末汇总的数据来源，也是投递记录页"为什么没发"的解释。
			recordAggregateSkip(event, policy, trigger)
			global.App.Log.Infof("[monitor] 告警事件 %d 命中策略「%s」聚合窗口（%d 秒），合并到窗口末汇总",
				eventID, policy.Name, policy.AggregateWindowSeconds)
			continue
		}
		created += dispatchPolicyTasks(event, instance, policy, trigger, "", noticeURL, "", nil)
	}
	if created > 0 {
		global.App.Log.Infof("[monitor] 告警事件 %d（%s）已生成 %d 条投递任务", eventID, trigger, created)
	}
}

// notifyPayloadRenderer 消息渲染钩子。
//
// 升级通知（renderEscalationPayload）与常规告警（renderAlertPayload）文案不同，
// 但"渠道解析 → 接收人解析 → 去重落库"这一段完全相同，
// 因此把渲染做成可注入的函数，避免复制出一整套派发逻辑（复制品迟早会漂移）。
type notifyPayloadRenderer func(
	event monitorModels.InsightMonitorAlertEvents,
	instance notifyInstanceContext,
	trigger string,
	operator string,
	mentions notifier.Mentions,
	recipients []notifier.Recipient,
	noticeURL string,
) notifyTaskPayload

// dispatchPolicyTasks 把一条策略展开成投递任务。operator 仅认领回执（trigger=acknowledged）使用。
// render 为消息渲染钩子；传 nil 时回落为常规告警渲染 renderAlertPayload。
func dispatchPolicyTasks(
	event monitorModels.InsightMonitorAlertEvents,
	instance notifyInstanceContext,
	policy monitorModels.InsightMonitorNotifyPolicies,
	trigger string,
	operator string,
	noticeURL string,
	dedupScope string,
	render notifyPayloadRenderer,
) int {
	if render == nil {
		render = renderAlertPayload
	}
	channelIDs := decodeUint64List(policy.ChannelIDs)
	if len(channelIDs) == 0 {
		return 0
	}
	subjects := decodeSubjects(policy.Subjects)
	resolution := resolveNotifySubjects(subjects)
	for _, warning := range resolution.Warnings {
		global.App.Log.Warnf("[monitor] 策略「%s」接收人解析告警：%s", policy.Name, warning)
	}

	channels := loadNotifyChannelsForDispatch(channelIDs)
	created := 0
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		adapter, ok := notifier.Get(channel.Kind)
		if !ok {
			global.App.Log.Warnf("[monitor] 渠道实例 %d 的类型 %s 未实现，跳过投递", channel.ID, channel.Kind)
			continue
		}
		caps := adapter.Capabilities()
		targets, skipped := notifySubjectsByChannel(caps, resolution)
		if len(skipped) > 0 {
			global.App.Log.Warnf("[monitor] 渠道「%s」下 %d 个接收人缺少可用地址，已跳过：%s",
				channel.Name, len(skipped), strings.Join(skipped, "、"))
		}
		if len(targets) == 0 {
			continue
		}

		for _, target := range targets {
			if caps.Personalizable {
				recipients := []notifier.Recipient{{
					Address:     target.Email,
					DisplayName: target.Name,
					UserID:      target.UserID,
				}}
				payload := render(event, instance, trigger, operator, notifier.Mentions{}, recipients, noticeURL)
				if createNotifyTask(event, policy, channel, trigger, "user", target.Email, target.Name, payload, dedupScope) {
					created++
				}
				continue
			}
			// 群渠道：一条任务投递到群，人通过 @ 名单触达
			payload := render(event, instance, trigger, operator, resolution.Mentions, nil, noticeURL)
			if createNotifyTask(event, policy, channel, trigger, "group", "", channel.Name, payload, dedupScope) {
				created++
			}
		}
	}
	return created
}

// createNotifyTask 落一条投递任务。返回是否真正新建（去重命中时返回 false）。
func createNotifyTask(
	event monitorModels.InsightMonitorAlertEvents,
	policy monitorModels.InsightMonitorNotifyPolicies,
	channel monitorModels.InsightMonitorNotifyChannels,
	trigger string,
	targetType string,
	targetValue string,
	targetLabel string,
	payload notifyTaskPayload,
	dedupScope string,
) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		global.App.Log.Warnf("[monitor] 渲染消息序列化失败: %v", err)
		return false
	}
	dedupKey := fmt.Sprintf("%d|%d|%d|%s|%s|%s|%s",
		event.ID, policy.ID, channel.ID, targetType, targetValue, trigger, dedupScope)

	maxAttempt := channel.MaxAttempt
	if maxAttempt <= 0 {
		maxAttempt = 4
	}
	now := notifyNowLocal()
	row := monitorModels.InsightMonitorNotifyTasks{
		EventID:     event.ID,
		Fingerprint: event.Fingerprint,
		InstanceID:  event.InstanceID,
		PolicyID:    policy.ID,
		ChannelID:   channel.ID,
		ChannelKind: channel.Kind,
		Trigger:     trigger,
		TargetType:  targetType,
		TargetValue: targetValue,
		TargetLabel: targetLabel,
		Payload:     encoded,
		Status:      monitorModels.NotifyTaskPending,
		MaxAttempt:  maxAttempt,
		NextRetryAt: &now,
		DedupKey:    dedupKey,
	}
	return insertNotifyTask(&row)
}

// insertNotifyTask 落一条投递任务。返回是否真正新建（去重命中时返回 false）。
//
// 幂等的落点：dedup_key 上有唯一索引，配合 ON DUPLICATE KEY 不做任何事 ——
// 同一条告警在同一策略/渠道/目标/触发阶段上重复派发时，数据库直接挡下，
// 不需要应用层判断"是不是发过了"。聚合汇总这类不绑定具体事件的任务也走这里。
func insertNotifyTask(row *monitorModels.InsightMonitorNotifyTasks) bool {
	if global.App.DB == nil || row == nil {
		return false
	}
	if row.NextRetryAt == nil {
		now := notifyNowLocal()
		row.NextRetryAt = &now
	}
	result := global.App.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if result.Error != nil {
		global.App.Log.Warnf("[monitor] 创建投递任务失败: %v", result.Error)
		return false
	}
	return result.RowsAffected > 0
}

// notifyEverDelivered 判断该告警在该策略下是否成功投递过"触发"/"升级"/"认领"通知。
//
// 恢复通知的前提是收件人此前被告知过：触发通知成功过、被升级通知叫过，或有人认领过。
func notifyEverDelivered(fingerprint string, policyID uint64) bool {
	if global.App.DB == nil || strings.TrimSpace(fingerprint) == "" {
		return false
	}
	var count int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		// 升级通知也算"被告知过"：告警被升级到本策略时人已经收到过消息，
		// 恢复时若不补一条"已恢复"，值班人会以为这条告警还挂着。
		Where("fingerprint = ? AND policy_id = ? AND `trigger` IN ? AND status = ?",
			fingerprint, policyID,
			[]string{"firing", NotifyTriggerEscalated, NotifyTriggerAcknowledged},
			monitorModels.NotifyTaskSuccess).
		Count(&count).Error; err != nil {
		// 查询失败按"已通知"处理：宁可多发一条恢复通知，也不要让恢复静默丢失
		return true
	}
	return count > 0
}

// NotifyTriggerAcknowledged 认领通知的 trigger 值。
//
// 任务表 trigger 是 varchar(16)："acknowledged" 12 字符放得下；
// dedup_key 含 trigger，重复认领天然被唯一键挡住，不会重复刷渠道。
const NotifyTriggerAcknowledged = "acknowledged"

// DispatchAckNotify 人工认领后向命中渠道发一条"已被认领，恢复前不再重复发送"。
//
// 由 UpdateAlertActionService 在 ack 落库成功后调用；只发一次（dedup 幂等），
// 不做静默检查——用户主动操作给出的即时反馈不该被静默窗口吃掉。
// 恢复时 recoverSilentAlerts 照常派发恢复通知（notifyEverDelivered 会把认领算作"被告知过"）。
func DispatchAckNotify(eventID uint64, operator string) {
	if global.App.DB == nil || eventID == 0 {
		return
	}

	var event monitorModels.InsightMonitorAlertEvents
	if err := global.App.DB.Where("id = ?", eventID).Take(&event).Error; err != nil {
		global.App.Log.Warnf("[monitor] 认领通知失败：事件 %d 不存在: %v", eventID, err)
		return
	}
	if event.Suppressed || event.Ignored {
		return
	}

	instance := loadNotifyInstanceContext(event.InstanceID)
	instance.AlertTemplateID = loadNotifyAlertTemplateID(event.InstanceID)
	ctx := notifyMatchContext{
		Level:           event.Level,
		Severity:        event.Severity,
		DbType:          instance.DbType,
		Business:        instance.Business,
		Environment:     instance.Environment,
		InstanceID:      event.InstanceID,
		Layer:           notifyMetricLayer(instance.DbType, event.MetricKey),
		AlertTemplateID: instance.AlertTemplateID,
	}
	policies := matchNotifyPolicies(ctx)
	if len(policies) == 0 {
		return
	}

	noticeURL := ""
	if value, err := notifier.LoadNoticeURL(); err == nil {
		noticeURL = value
	}
	created := 0
	for _, policy := range policies {
		created += dispatchPolicyTasks(event, instance, policy, NotifyTriggerAcknowledged, operator, noticeURL, "", nil)
	}
	if created > 0 {
		global.App.Log.Infof("[monitor] 告警事件 %d 已被 %s 认领，已生成 %d 条认领通知", eventID, operator, created)
	}
}

// MaybeDispatchRepeatNotify 持续告警周期性重发：评估器每轮对"持续命中"的活跃事件调用。
//
// 触发条件（全部满足才派发）：
//   - 事件 status = active（acknowledged = 人工已接管，不再打扰；pending 观察期未转正不通知）
//   - 事件未被抑制/忽略
//   - 策略 repeat_minutes > 0
//   - 该指纹在该策略下最近一条触发任务（pending/sending/success/failed，不含静默留痕）
//     距今已满 repeat_minutes。从未发过 → 立即补发一次（顺带解决"策略后配的存量活跃
//     告警永远不通知"的历史痛点）。
//
// 刻意不走 notifySilenceHit：repeat 判断本身就是间隔控制，且有效间隔取
// max(silence, repeat) 的语义太绕——用户设 repeat=10、silence=30 时期待的就是 10 分钟一响。
func MaybeDispatchRepeatNotify(eventID uint64) {
	if global.App.DB == nil || eventID == 0 {
		return
	}

	var event monitorModels.InsightMonitorAlertEvents
	if err := global.App.DB.Where("id = ?", eventID).Take(&event).Error; err != nil {
		return
	}
	if event.Status != "active" || event.Suppressed || event.Ignored {
		return
	}

	instance := loadNotifyInstanceContext(event.InstanceID)
	instance.AlertTemplateID = loadNotifyAlertTemplateID(event.InstanceID)
	ctx := notifyMatchContext{
		Level:           event.Level,
		Severity:        event.Severity,
		DbType:          instance.DbType,
		Business:        instance.Business,
		Environment:     instance.Environment,
		InstanceID:      event.InstanceID,
		Layer:           notifyMetricLayer(instance.DbType, event.MetricKey),
		AlertTemplateID: instance.AlertTemplateID,
	}

	noticeURL := ""
	if value, err := notifier.LoadNoticeURL(); err == nil {
		noticeURL = value
	}
	created := 0
	for _, policy := range matchNotifyPolicies(ctx) {
		if policy.RepeatMinutes <= 0 || !notifyRepeatDue(event.Fingerprint, policy.ID, policy.RepeatMinutes) {
			continue
		}
		// 重发任务必须带时间桶作用域：dedup_key 对真实事件是 (event,policy,channel,trigger) 维度，
		// 不加作用域的话第二轮重发会被唯一键静默吃掉（DoNothing、RowsAffected=0，永远不会真发）。
		// 桶号 = 当前时间 / 重发间隔，同一窗口内多次评估天然幂等。
		scope := fmt.Sprintf("repeat|%d", time.Now().Unix()/(int64(policy.RepeatMinutes)*60))
		created += dispatchPolicyTasks(event, instance, policy, "firing", "", noticeURL, scope, nil)
	}
	if created > 0 {
		global.App.Log.Infof("[monitor] 告警事件 %d 持续未恢复，按重发间隔补发 %d 条通知", eventID, created)
	}
}

// notifyRepeatDue 判断该指纹在该策略下是否到了重发时间。
func notifyRepeatDue(fingerprint string, policyID uint64, minutes int) bool {
	if global.App.DB == nil || strings.TrimSpace(fingerprint) == "" {
		return false
	}
	var last monitorModels.InsightMonitorNotifyTasks
	err := global.App.DB.
		Where("fingerprint = ? AND policy_id = ? AND `trigger` = ? AND status IN ?",
			fingerprint, policyID, "firing", []string{
				monitorModels.NotifyTaskPending,
				monitorModels.NotifyTaskSending,
				monitorModels.NotifyTaskSuccess,
				monitorModels.NotifyTaskFailed,
			}).
		Order("id desc").
		Take(&last).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 该策略从未给这条告警发过触发通知（策略后配的存量告警）→ 立即补发
		return true
	}
	if err != nil {
		// 查询失败按"未到期"处理：重发是周期性动作，下一轮评估再来，宁可少发不多发
		global.App.Log.Warnf("[monitor] 重发时间判断查询失败: %v", err)
		return false
	}
	return time.Since(time.Time(last.CreatedAt)) >= time.Duration(minutes)*time.Minute
}

// recordSilentSkip 记录一条静默留痕（skip 状态不参与投递，只用于页面解释"为什么没发"）。
func recordSilentSkip(event monitorModels.InsightMonitorAlertEvents, policy monitorModels.InsightMonitorNotifyPolicies, trigger string) {
	channelIDs := decodeUint64List(policy.ChannelIDs)
	if len(channelIDs) == 0 {
		return
	}
	row := monitorModels.InsightMonitorNotifyTasks{
		EventID:     event.ID,
		Fingerprint: event.Fingerprint,
		InstanceID:  event.InstanceID,
		PolicyID:    policy.ID,
		ChannelID:   channelIDs[0],
		Trigger:     trigger,
		TargetType:  "silent",
		TargetLabel: fmt.Sprintf("静默 %d 分钟内不重复通知", policy.SilenceMinutes),
		Status:      monitorModels.NotifyTaskSkippedSilent,
		MaxAttempt:  0,
		DedupKey:    fmt.Sprintf("%d|%d|silent|%d", event.ID, policy.ID, silentWindowBucket(policy)),
	}
	global.App.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
}

// silentWindowBucket 静默窗口的桶号：同一窗口内只留一行痕迹。
func silentWindowBucket(policy monitorModels.InsightMonitorNotifyPolicies) int64 {
	minutes := policy.SilenceMinutes
	if minutes <= 0 {
		minutes = 30
	}
	return time.Now().Unix() / (int64(minutes) * 60)
}

// notifySilenceHit 判断该事件在该策略下是否处于静默窗口内。
func notifySilenceHit(event monitorModels.InsightMonitorAlertEvents, policy monitorModels.InsightMonitorNotifyPolicies, trigger string) bool {
	if policy.SilenceMinutes <= 0 || global.App.DB == nil {
		return false
	}
	since := time.Now().Add(-time.Duration(policy.SilenceMinutes) * time.Minute)
	query := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		// 注意：trigger 是 MySQL 8 保留字。这里是原样拼串，必须加反引号，
		// 否则每条 SQL 都是 1064 语法错误 —— 现象是静默窗口静默失效（查询失败被吞，按"未静默"处理）。
		Where("fingerprint = ? AND policy_id = ? AND `trigger` = ?", event.Fingerprint, policy.ID, trigger).
		Where("created_at >= ?", since).
		Where("status IN ?", []string{
			monitorModels.NotifyTaskPending,
			monitorModels.NotifyTaskSending,
			monitorModels.NotifyTaskSuccess,
			monitorModels.NotifyTaskSkippedSilent,
		})
	var count int64
	if err := query.Count(&count).Error; err != nil {
		// 查询失败按"未静默"处理：宁可多发一条，也不能因为查询异常让告警失声。
		// 但必须留日志 —— 静默失效在页面上看不出任何异常（只有"怎么又发了"），
		// 曾经 trigger 是 MySQL 保留字导致这里长期报错被吞，谁都不知道。
		global.App.Log.Warnf("[monitor] 静默窗口查询失败，按未静默处理: %v", err)
		return false
	}
	return count > 0
}

// loadNotifyChannelsForDispatch 批量载入渠道实例（只取派发需要的列）。
func loadNotifyChannelsForDispatch(ids []uint64) []monitorModels.InsightMonitorNotifyChannels {
	if len(ids) == 0 || global.App.DB == nil {
		return nil
	}
	var rows []monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Where("id IN ?", ids).Order("id asc").Find(&rows).Error; err != nil {
		global.App.Log.Warnf("[monitor] 载入通知渠道失败: %v", err)
		return nil
	}
	return rows
}

// loadNotifyAlertTemplateID 取实例挂载的告警策略模板 ID（策略按模板筛选时需要）。
func loadNotifyAlertTemplateID(instanceID string) uint64 {
	if global.App.DB == nil || strings.TrimSpace(instanceID) == "" {
		return 0
	}
	var templateID *uint64
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("alert_template_id").
		Where("instance_id = ?", instanceID).
		Limit(1).Scan(&templateID).Error; err != nil {
		return 0
	}
	if templateID == nil {
		return 0
	}
	return *templateID
}
