package services

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
	"gorm.io/gorm/clause"
)

// ============================================================================
// 告警升级链 + 聚合窗口
//
// 补齐四个"只落库、不生效"的配置项：
//   1. 聚合窗口(秒)      —— 同类告警在窗口内合并，不重复产生通知与升级，窗口末补一条汇总
//   2. 升级等待(分钟)    —— 未认领超时自动升级，定时调度 + 失败退避重试
//   3. 升级目标策略      —— 按策略链逐级确定通知目标（个人 / 团队），支持多级递进与环路防护
//   4. 人工认领后停止升级 —— 认领即终止当前与后续全部升级
//
// 设计底线（与通知派发层一致）：
//   - 本层只写数据库，绝不发网络请求；真正的发送交给 notify worker。
//   - 幂等靠数据库约束（唯一索引 + ON DUPLICATE KEY 不做任何事），不靠应用层加锁。
//   - 单轮调度串行（mutex 防重入），跨副本靠 CAS 更新（WHERE status='pending'）保证只执行一次。
// ============================================================================

const (
	// 升级调度 tick：30 秒。升级等待以分钟为单位，30 秒的粒度保证误差可控，
	// 同时给"配置改小后立即生效"留出足够快的响应。
	monitorEscalationSpec = "@every 30s"
	// maxEscalationDepth 升级链最大深度：A→B→C→A 这类配置环必须有硬上限兜底
	maxEscalationDepth = 5
	// maxEscalationAttempt 单级升级的最大尝试次数（目标策略暂时不可用时退避重试）
	maxEscalationAttempt = 3
	// escalationRetryDelay 单级升级失败后的退避时间
	escalationRetryDelay = time.Minute
	// escalationBatch 单轮最多处理的升级行数
	escalationBatch = 200
)

// 升级通知 / 聚合汇总的 trigger 值（与 firing / recovered / acknowledged 并列）。
// 三者都塞得进 trigger 的 varchar(16)：escalated 9 字符、aggregated 10 字符。
const (
	NotifyTriggerEscalated  = "escalated"
	NotifyTriggerAggregated = "aggregated"
)

// NotifyTaskTargetAggregate 聚合留痕的 target_type：这一行不是投递目标，
// 只是"本条告警被窗口合并掉了"的留痕，供窗口末汇总时统计、也供投递记录页解释"为什么没发"。
const NotifyTaskTargetAggregate = "aggregate"

// 聚合 dedup_key 前缀。留痕 = agg|…，汇总 = aggd|…（前缀差异让两类行可以单独扫描）。
const (
	aggregateTracePrefix  = "agg|"
	aggregateDigestPrefix = "aggd|"
)

var (
	escalationRunning bool
	escalationMutex   sync.Mutex
)

// ============================================================================
// 一、纯决策逻辑（不碰数据库，便于单测覆盖完整状态矩阵）
// ============================================================================

// escalationDecision 调度器对单条升级行的决策。
type escalationDecision struct {
	// Action: wait（未到期）/ escalate（立即升级）/ cancel（终止）/ skip（本轮不处理）
	Action string
	Reason string
	// DueAt 按**当前配置**算出的到期时间（配置变更实时生效的落点）
	DueAt time.Time
}

// 决策动作。
const (
	escalationActionWait     = "wait"
	escalationActionEscalate = "escalate"
	escalationActionCancel   = "cancel"
	escalationActionSkip     = "skip"
)

// decideEscalation 单条升级行的调度决策（纯函数）。
//
// 参数里 event / fromPolicy / targetPolicy 为 nil 表示"查不到"（事件或策略已不存在）。
// 判定顺序刻意从"终态"往"待处理"排：已经被关闭的告警越早终止越好，避免发出无意义的升级。
func decideEscalation(
	row monitorModels.InsightMonitorAlertEscalations,
	now time.Time,
	event *monitorModels.InsightMonitorAlertEvents,
	fromPolicy *monitorModels.InsightMonitorNotifyPolicies,
	targetPolicy *monitorModels.InsightMonitorNotifyPolicies,
) escalationDecision {
	if row.Status != monitorModels.EscalationStatusPending {
		return escalationDecision{Action: escalationActionSkip, Reason: "该级升级已结束"}
	}
	if event == nil {
		return escalationDecision{Action: escalationActionCancel, Reason: "告警事件已不存在"}
	}
	if event.Status == "recovered" {
		return escalationDecision{Action: escalationActionCancel, Reason: "告警已恢复"}
	}
	if event.Ignored {
		return escalationDecision{Action: escalationActionCancel, Reason: "告警已被人工忽略"}
	}
	if fromPolicy == nil {
		return escalationDecision{Action: escalationActionCancel, Reason: "驱动升级的策略已被删除"}
	}
	if !fromPolicy.Enabled {
		return escalationDecision{Action: escalationActionCancel, Reason: "驱动升级的策略已停用"}
	}
	if strings.EqualFold(event.Status, "acknowledged") && fromPolicy.StopOnAck {
		// 「人工认领后停止升级」的落点：认领即终止当前级与后续所有级。
		// 后续级根本还没生成，因此终止当前级就等于终止整条链。
		return escalationDecision{Action: escalationActionCancel, Reason: "告警已被人工认领，按策略配置停止升级"}
	}
	if fromPolicy.EscalateAfterMinutes <= 0 {
		return escalationDecision{Action: escalationActionCancel, Reason: "升级等待时间已改为不启用"}
	}
	if targetPolicy == nil {
		return escalationDecision{Action: escalationActionCancel, Reason: "升级目标策略已被删除"}
	}
	if !targetPolicy.Enabled {
		return escalationDecision{Action: escalationActionCancel, Reason: "升级目标策略已停用"}
	}

	due := escalationDueAt(time.Time(row.AnchorAt), fromPolicy.EscalateAfterMinutes)
	// 失败退避优先于"按当前配置重算"：重试时 due_at 被显式推后，
	// 若这里无条件重算，退避会被覆盖成"每 30 秒重试一次"，退避序列形同虚设。
	// 配置变更仍然实时生效 —— 只有当配置算出的时间比退避时间更晚时才会覆盖它。
	if row.Attempt > 0 && row.DueAt != nil && time.Time(*row.DueAt).After(due) {
		due = time.Time(*row.DueAt)
	}
	if now.Before(due) {
		return escalationDecision{Action: escalationActionWait, Reason: "未到升级时间", DueAt: due}
	}
	return escalationDecision{Action: escalationActionEscalate, Reason: "已超时未处理", DueAt: due}
}

// escalationDueAt 升级到期时间 = 锚点 + 当前配置的等待分钟数。
func escalationDueAt(anchor time.Time, minutes int) time.Time {
	if minutes <= 0 {
		return anchor
	}
	return anchor.Add(time.Duration(minutes) * time.Minute)
}

// escalationNextTarget 计算下一级升级的目标策略（纯函数）。
//
// 四类"到此为止"：达到深度上限、目标策略没配下一级、自环、链上已出现过该策略（环）。
// 返回 0 表示升级链在本级终止（正常终态，不是错误）。
func escalationNextTarget(current monitorModels.InsightMonitorNotifyPolicies, level int, visited map[uint64]bool) (uint64, string) {
	if level+1 > maxEscalationDepth {
		return 0, fmt.Sprintf("已达到升级链最大深度 %d 级", maxEscalationDepth)
	}
	if current.EscalateAfterMinutes <= 0 {
		return 0, "目标策略未配置升级等待时间，升级链到此终止"
	}
	if current.EscalatePolicyID == 0 {
		return 0, "目标策略未配置下一级升级目标，升级链到此终止"
	}
	if current.EscalatePolicyID == current.ID {
		return 0, "升级目标指向自身，已忽略"
	}
	if visited[current.EscalatePolicyID] {
		return 0, "升级链存在循环，已在该级终止"
	}
	return current.EscalatePolicyID, ""
}

// ============================================================================
// 二、聚合窗口
// ============================================================================

// aggregateKey 同类告警的聚合键：同一策略 + 同一实例。
//
// 为什么按实例而不是按指纹：指纹维度的"去重"已经由 silence_minutes 承担，
// 聚合窗口要解决的是"一台实例同时爆了 5 个指标，5 条消息刷屏"—— 因此按实例合并。
func aggregateKey(policyID uint64, instanceID string) string {
	return fmt.Sprintf("%d|%s", policyID, instanceID)
}

// aggregateBucket 窗口桶号：unix 秒 / 窗口长度。
//
// 桶对齐是幂等的关键：同一窗口内的多次评估得到同一个桶号，
// 首报之后窗口内的同类告警都会被合并，不会因为评估跑了 N 轮就发 N 次。
func aggregateBucket(now time.Time, windowSeconds int) int64 {
	if windowSeconds <= 0 {
		return 0
	}
	return now.Unix() / int64(windowSeconds)
}

// aggregateWindowStart 桶的起始时刻（用于窗口内查询，避免用 now-N 秒这种滑动口径）。
func aggregateWindowStart(bucket int64, windowSeconds int) time.Time {
	if windowSeconds <= 0 {
		return time.Unix(0, 0)
	}
	return time.Unix(bucket*int64(windowSeconds), 0)
}

// aggregateTraceKey 被合并告警的留痕幂等键：一桶一事件一行。
func aggregateTraceKey(policyID uint64, instanceID string, bucket int64, eventID uint64) string {
	return fmt.Sprintf("%s%s|%d|%d", aggregateTracePrefix, aggregateKey(policyID, instanceID), bucket, eventID)
}

// aggregateDigestKey 窗口末汇总任务的幂等键：一桶一渠道一目标一行。
func aggregateDigestKey(policyID uint64, instanceID string, bucket int64, channelID uint64, targetType, targetValue string) string {
	return fmt.Sprintf("%s%s|%d|%d|%s|%s", aggregateDigestPrefix, aggregateKey(policyID, instanceID), bucket, channelID, targetType, targetValue)
}

// parseAggregateKey 反解聚合键，供窗口末汇总时按 (实例, 桶) 分组。
func parseAggregateKey(dedupKey string) (policyID uint64, instanceID string, bucket int64, tail string, ok bool) {
	prefix := ""
	switch {
	case strings.HasPrefix(dedupKey, aggregateTracePrefix):
		prefix = aggregateTracePrefix
	case strings.HasPrefix(dedupKey, aggregateDigestPrefix):
		prefix = aggregateDigestPrefix
	default:
		return 0, "", 0, "", false
	}
	parts := strings.Split(strings.TrimPrefix(dedupKey, prefix), "|")
	if len(parts) < 3 {
		return 0, "", 0, "", false
	}
	policyID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, "", 0, "", false
	}
	bucket, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, "", 0, "", false
	}
	tail = ""
	if len(parts) > 3 {
		tail = strings.Join(parts[3:], "|")
	}
	return policyID, parts[1], bucket, tail, true
}

// notifyAggregateHit 判断该告警在该策略下是否落在聚合窗口内（窗口内已发过同类通知）。
//
// 只统计"真实投递过"的任务（pending/sending/success/failed），
// 留痕行（skipped_silent / target_type=aggregate）不算 —— 否则第一条就会把自己挡住。
func notifyAggregateHit(event monitorModels.InsightMonitorAlertEvents, policy monitorModels.InsightMonitorNotifyPolicies, trigger string) bool {
	if policy.AggregateWindowSeconds <= 0 || global.App.DB == nil {
		return false
	}
	window := policy.AggregateWindowSeconds
	start := aggregateWindowStart(aggregateBucket(time.Now(), window), window)
	var count int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		Where("policy_id = ? AND instance_id = ? AND `trigger` = ?", policy.ID, event.InstanceID, trigger).
		Where("created_at >= ?", start).
		Where("status IN ?", []string{
			monitorModels.NotifyTaskPending,
			monitorModels.NotifyTaskSending,
			monitorModels.NotifyTaskSuccess,
			monitorModels.NotifyTaskFailed,
		}).
		Where("target_type NOT IN ?", []string{"silent", NotifyTaskTargetAggregate}).
		Count(&count).Error; err != nil {
		// 查询失败按"未聚合"处理：宁可多发一条，也不能因为查询异常让告警失声
		global.App.Log.Warnf("[monitor] 聚合窗口查询失败，按未聚合处理: %v", err)
		return false
	}
	return count > 0
}

// recordAggregateSkip 记录一条"被窗口合并"的留痕。
func recordAggregateSkip(event monitorModels.InsightMonitorAlertEvents, policy monitorModels.InsightMonitorNotifyPolicies, trigger string) {
	channelIDs := decodeUint64List(policy.ChannelIDs)
	channelID := uint64(0)
	if len(channelIDs) > 0 {
		channelID = channelIDs[0]
	}
	row := monitorModels.InsightMonitorNotifyTasks{
		EventID:     event.ID,
		Fingerprint: event.Fingerprint,
		InstanceID:  event.InstanceID,
		PolicyID:    policy.ID,
		ChannelID:   channelID,
		ChannelKind: "",
		Trigger:     trigger,
		TargetType:  NotifyTaskTargetAggregate,
		TargetLabel: fmt.Sprintf("聚合窗口 %d 秒内合并，窗口结束后汇总发送", policy.AggregateWindowSeconds),
		Status:      monitorModels.NotifyTaskSkippedSilent,
		MaxAttempt:  0,
		DedupKey:    aggregateTraceKey(policy.ID, event.InstanceID, aggregateBucket(time.Now(), policy.AggregateWindowSeconds), event.ID),
	}
	insertNotifyTask(&row)
}

// flushAggregateDigests 窗口关闭后的汇总补发。
//
// 只处理"已关闭的桶"（桶号 != 当前桶），当前桶还在收集，不能提前汇总。
// 幂等：汇总任务的 dedup_key 带 (桶, 渠道, 目标)，重复执行被唯一键挡住，最多补发一次。
func flushAggregateDigests() {
	if global.App.DB == nil {
		return
	}
	policies := loadAggregatePolicies()
	if len(policies) == 0 {
		return
	}
	noticeURL := ""
	if value, err := notifier.LoadNoticeURL(); err == nil {
		noticeURL = value
	}

	for _, policy := range policies {
		window := policy.AggregateWindowSeconds
		currentBucket := aggregateBucket(time.Now(), window)
		// 多扫两个桶：调度晚点或进程重启时，上一桶可能还没来得及汇总
		since := time.Now().Add(-time.Duration(3*window) * time.Second)

		var traces []monitorModels.InsightMonitorNotifyTasks
		if err := global.App.DB.
			Where("policy_id = ? AND target_type = ? AND status = ? AND dedup_key LIKE ? AND created_at >= ?",
				policy.ID, NotifyTaskTargetAggregate, monitorModels.NotifyTaskSkippedSilent,
				fmt.Sprintf("%s%d|%%", aggregateTracePrefix, policy.ID), since).
			Order("id asc").Limit(200).Find(&traces).Error; err != nil {
			global.App.Log.Warnf("[monitor] 扫描聚合留痕失败（策略「%s」）: %v", policy.Name, err)
			continue
		}
		if len(traces) == 0 {
			continue
		}

		// 按 (实例, 桶) 分组收集被合并的事件
		type group struct {
			instanceID string
			bucket     int64
			eventIDs   []uint64
		}
		groups := map[string]*group{}
		order := make([]string, 0, len(traces))
		for _, trace := range traces {
			_, instanceID, bucket, tail, ok := parseAggregateKey(trace.DedupKey)
			if !ok || bucket == currentBucket || tail == "" || tail == "digest" {
				continue
			}
			eventID, err := strconv.ParseUint(tail, 10, 64)
			if err != nil || eventID == 0 {
				continue
			}
			key := fmt.Sprintf("%s|%d", instanceID, bucket)
			item, exists := groups[key]
			if !exists {
				item = &group{instanceID: instanceID, bucket: bucket}
				groups[key] = item
				order = append(order, key)
			}
			for _, existing := range item.eventIDs {
				if existing == eventID {
					eventID = 0
					break
				}
			}
			if eventID > 0 {
				item.eventIDs = append(item.eventIDs, eventID)
			}
		}
		sort.SliceStable(order, func(i, j int) bool { return order[i] < order[j] })

		for _, key := range order {
			item := groups[key]
			if len(item.eventIDs) == 0 {
				continue
			}
			events := loadAlertEventsByIDs(item.eventIDs)
			if len(events) == 0 {
				continue
			}
			if created := dispatchAggregateDigest(policy, item.instanceID, events, item.bucket, window, noticeURL); created > 0 {
				global.App.Log.Infof("[monitor] 策略「%s」聚合窗口(%d 秒)汇总：实例 %s 合并 %d 条告警，已生成 %d 条汇总通知",
					policy.Name, window, item.instanceID, len(events), created)
			}
		}
	}
}

// loadAggregatePolicies 取所有配置了聚合窗口且启用的策略（数量很少，全量取即可）。
func loadAggregatePolicies() []monitorModels.InsightMonitorNotifyPolicies {
	var rows []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.
		Where("enabled = ? AND aggregate_window_seconds > 0", true).
		Order("priority asc, id asc").Find(&rows).Error; err != nil {
		global.App.Log.Warnf("[monitor] 加载聚合策略失败: %v", err)
		return nil
	}
	return rows
}

// loadAlertEventsByIDs 批量取告警事件（用于汇总正文）。
func loadAlertEventsByIDs(ids []uint64) []monitorModels.InsightMonitorAlertEvents {
	if len(ids) == 0 || global.App.DB == nil {
		return nil
	}
	var rows []monitorModels.InsightMonitorAlertEvents
	if err := global.App.DB.Where("id IN ?", ids).Order("id asc").Find(&rows).Error; err != nil {
		global.App.Log.Warnf("[monitor] 加载告警事件失败: %v", err)
		return nil
	}
	return rows
}

// dispatchAggregateDigest 把一组被合并的告警渲染成一条汇总消息并落投递任务。
func dispatchAggregateDigest(
	policy monitorModels.InsightMonitorNotifyPolicies,
	instanceID string,
	events []monitorModels.InsightMonitorAlertEvents,
	bucket int64,
	windowSeconds int,
	noticeURL string,
) int {
	channelIDs := decodeUint64List(policy.ChannelIDs)
	if len(channelIDs) == 0 {
		return 0
	}
	instance := loadNotifyInstanceContext(instanceID)
	instance.AlertTemplateID = loadNotifyAlertTemplateID(instanceID)
	resolution := resolveNotifySubjects(decodeSubjects(policy.Subjects))

	created := 0
	for _, channel := range loadNotifyChannelsForDispatch(channelIDs) {
		if !channel.Enabled {
			continue
		}
		adapter, ok := notifier.Get(channel.Kind)
		if !ok {
			continue
		}
		caps := adapter.Capabilities()
		targets, _ := notifySubjectsByChannel(caps, resolution)
		if len(targets) == 0 {
			continue
		}
		for _, target := range targets {
			if caps.Personalizable {
				payload := renderAggregatePayload(policy, instance, events, windowSeconds,
					notifier.Mentions{}, []notifier.Recipient{{
						Address:     target.Email,
						DisplayName: target.Name,
						UserID:      target.UserID,
					}}, noticeURL)
				if insertAggregateTask(policy, channel, instanceID, events[0].Fingerprint,
					"user", target.Email, target.Name, payload,
					aggregateDigestKey(policy.ID, instanceID, bucket, channel.ID, "user", target.Email)) {
					created++
				}
				continue
			}
			payload := renderAggregatePayload(policy, instance, events, windowSeconds, resolution.Mentions, nil, noticeURL)
			if insertAggregateTask(policy, channel, instanceID, events[0].Fingerprint,
				"group", "", channel.Name, payload,
				aggregateDigestKey(policy.ID, instanceID, bucket, channel.ID, "group", "")) {
				created++
			}
		}
	}
	return created
}

// insertAggregateTask 落一条汇总投递任务（不绑定具体事件，event_id 记 0）。
func insertAggregateTask(
	policy monitorModels.InsightMonitorNotifyPolicies,
	channel monitorModels.InsightMonitorNotifyChannels,
	instanceID string,
	fingerprint string,
	targetType string,
	targetValue string,
	targetLabel string,
	payload notifyTaskPayload,
	dedupKey string,
) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	maxAttempt := channel.MaxAttempt
	if maxAttempt <= 0 {
		maxAttempt = 4
	}
	row := monitorModels.InsightMonitorNotifyTasks{
		EventID:     0,
		Fingerprint: fingerprint,
		InstanceID:  instanceID,
		PolicyID:    policy.ID,
		ChannelID:   channel.ID,
		ChannelKind: channel.Kind,
		Trigger:     NotifyTriggerAggregated,
		TargetType:  targetType,
		TargetValue: targetValue,
		TargetLabel: targetLabel,
		Payload:     encoded,
		Status:      monitorModels.NotifyTaskPending,
		MaxAttempt:  maxAttempt,
		DedupKey:    dedupKey,
	}
	return insertNotifyTask(&row)
}

// ============================================================================
// 三、升级链登记与取消
// ============================================================================

// ensureEscalationLevel1 为一条告警登记第 1 级升级（幂等）。
//
// 登记与"有没有真的发出通知"解耦：登记的含义是"这条告警进入升级观察"，
// 是否真的升级由调度器在到期时再判断（那时会检查认领/恢复/忽略）。
func ensureEscalationLevel1(event monitorModels.InsightMonitorAlertEvents, policy monitorModels.InsightMonitorNotifyPolicies) {
	if global.App.DB == nil || event.ID == 0 {
		return
	}
	if policy.EscalateAfterMinutes <= 0 || policy.EscalatePolicyID == 0 || policy.EscalatePolicyID == policy.ID {
		return
	}

	anchor := time.Time(event.TriggeredAt)
	if event.FirstSeenAt != nil {
		anchor = time.Time(*event.FirstSeenAt)
	}
	if anchor.IsZero() {
		anchor = time.Now()
	}

	row := monitorModels.InsightMonitorAlertEscalations{
		Model:        &commonModels.Model{},
		EventID:      event.ID,
		Fingerprint:  event.Fingerprint,
		InstanceID:   event.InstanceID,
		RootPolicyID: policy.ID,
		Level:        1,
		FromPolicyID: policy.ID,
		PolicyID:     policy.EscalatePolicyID,
		AnchorAt:     commonModels.LocalTime(anchor),
		Status:       monitorModels.EscalationStatusPending,
	}
	result := global.App.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		global.App.Log.Warnf("[monitor] 登记升级链失败（事件 %d / 策略「%s」）: %v", event.ID, policy.Name, result.Error)
		return
	}
	if result.RowsAffected > 0 {
		return
	}

	// 已存在同一级：区分"同一次事故重复登记"与"同一指纹的新事故"。
	var existing monitorModels.InsightMonitorAlertEscalations
	if err := global.App.DB.
		Where("fingerprint = ? AND root_policy_id = ? AND level = ?", event.Fingerprint, policy.ID, 1).
		Take(&existing).Error; err != nil {
		return
	}
	if existing.EventID == event.ID {
		// 同一次事故：保持原状态（pending 的倒计时绝不能刷新，否则"持续告警"永远升不了级）
		return
	}
	// 新事故：重置第 1 级，并清掉旧事故遗留的更深层待升级行
	now := notifyNowLocal()
	global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
		Where("id = ? AND event_id = ?", existing.ID, existing.EventID).
		Updates(map[string]any{
			"event_id":     event.ID,
			"status":       monitorModels.EscalationStatusPending,
			"anchor_at":    commonModels.LocalTime(anchor),
			"escalated_at": nil,
			"due_at":       nil,
			"attempt":      0,
			"last_error":   "",
			"updated_at":   now,
		})
	global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
		Where("fingerprint = ? AND root_policy_id = ? AND level > ? AND status = ?",
			event.Fingerprint, policy.ID, 1, monitorModels.EscalationStatusPending).
		Updates(map[string]any{
			"status":     monitorModels.EscalationStatusCancelled,
			"last_error": "同一指纹产生新告警，旧升级链已作废",
			"updated_at": now,
		})
}

// cancelAlertEscalations 终止某条告警的全部待升级项（恢复 / 人工忽略 / 事件消失）。
//
// 用 status 条件做 CAS：只有 pending 的行会被改，并发下重复调用无副作用。
func cancelAlertEscalations(fingerprint string, reason string) int64 {
	if global.App.DB == nil || strings.TrimSpace(fingerprint) == "" {
		return 0
	}
	result := global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
		Where("fingerprint = ? AND status = ?", fingerprint, monitorModels.EscalationStatusPending).
		Updates(map[string]any{
			"status":     monitorModels.EscalationStatusCancelled,
			"last_error": truncateString(reason, 500),
			"updated_at": notifyNowLocal(),
		})
	if result.Error != nil {
		global.App.Log.Warnf("[monitor] 取消升级链失败（%s）: %v", fingerprint, result.Error)
		return 0
	}
	if result.RowsAffected > 0 {
		global.App.Log.Infof("[monitor] 告警 %s 的 %d 项待升级已终止：%s", fingerprint, result.RowsAffected, reason)
	}
	return result.RowsAffected
}

// cancelEscalationsOnAck 人工认领后的升级终止（受策略 stop_on_ack 开关控制）。
//
// 逐条判断而不是一刀切：只有"驱动该级升级的策略"开启了「认领后停止升级」才终止，
// 运维显式关掉开关的策略（如"认领了也要通知值班经理"）会继续升级。
func cancelEscalationsOnAck(fingerprint string) int64 {
	if global.App.DB == nil || strings.TrimSpace(fingerprint) == "" {
		return 0
	}
	var rows []monitorModels.InsightMonitorAlertEscalations
	if err := global.App.DB.
		Where("fingerprint = ? AND status = ?", fingerprint, monitorModels.EscalationStatusPending).
		Find(&rows).Error; err != nil || len(rows) == 0 {
		return 0
	}
	policyIDs := make([]uint64, 0, len(rows))
	seen := map[uint64]struct{}{}
	for _, row := range rows {
		if _, ok := seen[row.FromPolicyID]; ok {
			continue
		}
		seen[row.FromPolicyID] = struct{}{}
		policyIDs = append(policyIDs, row.FromPolicyID)
	}
	policies := loadNotifyPoliciesByIDs(policyIDs)

	cancelled := int64(0)
	now := notifyNowLocal()
	for _, row := range rows {
		policy, ok := policies[row.FromPolicyID]
		if !ok || !policy.StopOnAck {
			continue
		}
		result := global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
			Where("id = ? AND status = ?", row.ID, monitorModels.EscalationStatusPending).
			Updates(map[string]any{
				"status":     monitorModels.EscalationStatusCancelled,
				"last_error": "告警已被人工认领，按策略配置停止升级",
				"updated_at": now,
			})
		cancelled += result.RowsAffected
	}
	if cancelled > 0 {
		global.App.Log.Infof("[monitor] 告警 %s 已被认领，终止 %d 项待升级", fingerprint, cancelled)
	}
	return cancelled
}

// loadNotifyPoliciesByIDs 批量取策略。
func loadNotifyPoliciesByIDs(ids []uint64) map[uint64]monitorModels.InsightMonitorNotifyPolicies {
	index := map[uint64]monitorModels.InsightMonitorNotifyPolicies{}
	if len(ids) == 0 || global.App.DB == nil {
		return index
	}
	var rows []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		global.App.Log.Warnf("[monitor] 加载通知策略失败: %v", err)
		return index
	}
	for _, row := range rows {
		index[row.ID] = row
	}
	return index
}

// escalationVisitedPolicies 取该升级链上已经出现过的策略集合（用于环路检测）。
func escalationVisitedPolicies(fingerprint string, rootPolicyID uint64) map[uint64]bool {
	visited := map[uint64]bool{rootPolicyID: true}
	var rows []monitorModels.InsightMonitorAlertEscalations
	if err := global.App.DB.
		Where("fingerprint = ? AND root_policy_id = ?", fingerprint, rootPolicyID).
		Find(&rows).Error; err != nil {
		return visited
	}
	for _, row := range rows {
		visited[row.FromPolicyID] = true
		visited[row.PolicyID] = true
	}
	return visited
}

// ============================================================================
// 四、升级调度
// ============================================================================

// RunMonitorAlertEscalation 一轮升级调度：重算到期时间 → 到期的立即升级 → 汇总聚合窗口。
//
// 与采集/评估一致地用 mutex 防重入：升级等待以分钟计，30 秒一轮，
// 若上一轮因为数据库慢还没跑完，这一轮直接跳过（下一轮再来，不会堆积）。
func RunMonitorAlertEscalation() {
	if global.App.DB == nil {
		return
	}
	escalationMutex.Lock()
	if escalationRunning {
		escalationMutex.Unlock()
		return
	}
	escalationRunning = true
	escalationMutex.Unlock()
	defer func() {
		escalationMutex.Lock()
		escalationRunning = false
		escalationMutex.Unlock()
		if r := recover(); r != nil {
			global.App.Log.Errorf("RunMonitorAlertEscalation panic: %v", r)
		}
	}()

	// 先给"还没登记升级链的活跃告警"补登记：
	//   - 存量告警（升级功能上线前就在活跃）从未走过登记路径，不补就永远不会升级；
	//   - 正常运行时这里查不到数据（新告警在派发时已登记），成本可忽略。
	reconcileMissingEscalations(escalationRegisterBatch)
	runEscalationSweep()
	flushAggregateDigests()
}

// escalationRegisterBatch 单轮补登记的最大条数（限流：避免升级功能首次上线时一轮扫全表）
const escalationRegisterBatch = 50

// reconcileMissingEscalations 为"活跃但没有任何升级链"的告警补登记第 1 级升级。
//
// 用 NOT EXISTS 而不是"取全部再过滤"：绝大多数轮次这里返回 0 行，
// 必须让数据库把"已经登记过"的告警直接排除掉，否则每轮都要把活跃告警全捞一遍。
func reconcileMissingEscalations(limit int) {
	if limit <= 0 {
		return
	}
	var events []monitorModels.InsightMonitorAlertEvents
	if err := global.App.DB.
		Where("status IN ? AND ignored = ? AND suppressed = ?",
			[]string{"active", "acknowledged"}, false, false).
		Where("NOT EXISTS (SELECT 1 FROM insight_monitor_alert_escalations s WHERE s.fingerprint = insight_monitor_alert_events.fingerprint)").
		Order("id asc").Limit(limit).Find(&events).Error; err != nil {
		global.App.Log.Warnf("[monitor] 扫描待补登记的告警失败: %v", err)
		return
	}
	for _, event := range events {
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
		for _, policy := range matchNotifyPolicies(ctx) {
			ensureEscalationLevel1(event, policy)
		}
	}
}

// runEscalationSweep 扫一遍待升级行，按当前配置决定等待 / 升级 / 终止。
func runEscalationSweep() {
	var rows []monitorModels.InsightMonitorAlertEscalations
	if err := global.App.DB.
		Where("status = ?", monitorModels.EscalationStatusPending).
		Order("id asc").Limit(escalationBatch).
		Find(&rows).Error; err != nil {
		global.App.Log.Warnf("[monitor] 加载待升级项失败: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	// 事件与策略都批量载入：升级行可能有上百条，逐条查库会把调度变成 N+1
	eventIDs := make([]uint64, 0, len(rows))
	policyIDs := make([]uint64, 0, len(rows)*2)
	eventSeen := map[uint64]struct{}{}
	policySeen := map[uint64]struct{}{}
	for _, row := range rows {
		if _, ok := eventSeen[row.EventID]; !ok {
			eventSeen[row.EventID] = struct{}{}
			eventIDs = append(eventIDs, row.EventID)
		}
		for _, id := range []uint64{row.FromPolicyID, row.PolicyID} {
			if _, ok := policySeen[id]; ok {
				continue
			}
			policySeen[id] = struct{}{}
			policyIDs = append(policyIDs, id)
		}
	}
	events := loadAlertEventsByIDs(eventIDs)
	eventIndex := map[uint64]monitorModels.InsightMonitorAlertEvents{}
	for _, event := range events {
		eventIndex[event.ID] = event
	}
	policies := loadNotifyPoliciesByIDs(policyIDs)

	now := time.Now()
	for _, row := range rows {
		event, eventExists := eventIndex[row.EventID]
		var eventPtr *monitorModels.InsightMonitorAlertEvents
		if eventExists {
			eventPtr = &event
		}
		fromPolicy, fromExists := policies[row.FromPolicyID]
		var fromPtr *monitorModels.InsightMonitorNotifyPolicies
		if fromExists {
			fromPtr = &fromPolicy
		}
		targetPolicy, targetExists := policies[row.PolicyID]
		var targetPtr *monitorModels.InsightMonitorNotifyPolicies
		if targetExists {
			targetPtr = &targetPolicy
		}

		decision := decideEscalation(row, now, eventPtr, fromPtr, targetPtr)
		switch decision.Action {
		case escalationActionCancel:
			cancelEscalationRow(row, decision.Reason)
			continue
		case escalationActionWait, escalationActionSkip:
			// 到期时间按当前配置回写：运维改了「升级等待」，这里立刻对齐（配置实时生效）
			syncEscalationDueAt(row, decision.DueAt)
			continue
		}

		// 到期：先派发（幂等，靠 dedup_key 唯一键），再用 CAS 收敛状态
		created := dispatchEscalationNotify(row, event, targetPolicy)
		if created == 0 {
			retryEscalationRow(row, "目标策略未生成任何投递任务（渠道未启用或接收人无可用地址）")
			continue
		}
		escalatedAt := notifyNowLocal()
		result := global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
			Where("id = ? AND status = ?", row.ID, monitorModels.EscalationStatusPending).
			Updates(map[string]any{
				"status":       monitorModels.EscalationStatusEscalated,
				"escalated_at": escalatedAt,
				"last_error":   "",
				"updated_at":   escalatedAt,
			})
		if result.RowsAffected == 0 {
			// 并发：另一个副本已经完成本级升级，本轮放弃（通知有 dedup 保护，不会重复发）
			continue
		}
		global.App.Log.Infof("[monitor] 告警 %s 已升级到第 %d 级（策略「%s」），发出 %d 条升级通知",
			row.Fingerprint, row.Level, targetPolicy.Name, created)
		ensureNextEscalationLevel(row, event, targetPolicy, time.Time(escalatedAt))
	}
}

// syncEscalationDueAt 回写按当前配置算出的到期时间（只在变化时写，避免无谓的行更新）。
func syncEscalationDueAt(row monitorModels.InsightMonitorAlertEscalations, due time.Time) {
	if due.IsZero() {
		return
	}
	if row.DueAt != nil && time.Time(*row.DueAt).Equal(due) {
		return
	}
	global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{"due_at": commonModels.LocalTime(due), "updated_at": notifyNowLocal()})
}

// cancelEscalationRow 终止单条升级项。
func cancelEscalationRow(row monitorModels.InsightMonitorAlertEscalations, reason string) {
	global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
		Where("id = ? AND status = ?", row.ID, monitorModels.EscalationStatusPending).
		Updates(map[string]any{
			"status":     monitorModels.EscalationStatusCancelled,
			"last_error": truncateString(reason, 500),
			"updated_at": notifyNowLocal(),
		})
}

// retryEscalationRow 升级派发失败：累加尝试次数并退避重试，超过上限则终止并留原因。
func retryEscalationRow(row monitorModels.InsightMonitorAlertEscalations, reason string) {
	attempt := row.Attempt + 1
	now := notifyNowLocal()
	if attempt >= maxEscalationAttempt {
		global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
			Where("id = ? AND status = ?", row.ID, monitorModels.EscalationStatusPending).
			Updates(map[string]any{
				"status":     monitorModels.EscalationStatusCancelled,
				"attempt":    attempt,
				"last_error": truncateString(reason+"（已重试至上限，终止本轮升级）", 500),
				"updated_at": now,
			})
		global.App.Log.Warnf("[monitor] 告警 %s 第 %d 级升级失败，重试 %d 次后终止：%s",
			row.Fingerprint, row.Level, attempt, reason)
		return
	}
	next := commonModels.LocalTime(time.Now().Add(escalationRetryDelay))
	global.App.DB.Model(&monitorModels.InsightMonitorAlertEscalations{}).
		Where("id = ? AND status = ?", row.ID, monitorModels.EscalationStatusPending).
		Updates(map[string]any{
			"attempt":    attempt,
			"due_at":     next,
			"last_error": truncateString(fmt.Sprintf("%s（第 %d/%d 次，%s 后重试）", reason, attempt, maxEscalationAttempt, escalationRetryDelay), 500),
			"updated_at": now,
		})
}

// ensureNextEscalationLevel 本级升级完成后登记下一级（幂等）。
func ensureNextEscalationLevel(
	row monitorModels.InsightMonitorAlertEscalations,
	event monitorModels.InsightMonitorAlertEvents,
	targetPolicy monitorModels.InsightMonitorNotifyPolicies,
	anchor time.Time,
) {
	nextID, reason := escalationNextTarget(targetPolicy, row.Level, escalationVisitedPolicies(row.Fingerprint, row.RootPolicyID))
	if nextID == 0 {
		global.App.Log.Infof("[monitor] 告警 %s 的升级链在第 %d 级终止：%s", row.Fingerprint, row.Level, reason)
		return
	}
	next := monitorModels.InsightMonitorAlertEscalations{
		Model:        &commonModels.Model{},
		EventID:      event.ID,
		Fingerprint:  row.Fingerprint,
		InstanceID:   row.InstanceID,
		RootPolicyID: row.RootPolicyID,
		Level:        row.Level + 1,
		FromPolicyID: targetPolicy.ID,
		PolicyID:     nextID,
		AnchorAt:     commonModels.LocalTime(anchor),
		Status:       monitorModels.EscalationStatusPending,
	}
	if err := global.App.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&next).Error; err != nil {
		global.App.Log.Warnf("[monitor] 登记第 %d 级升级失败（告警 %s）: %v", row.Level+1, row.Fingerprint, err)
	}
}

// dispatchEscalationNotify 向升级目标策略的渠道与接收人派发升级通知。
//
// 同样受聚合窗口约束：同一策略同一实例在同一窗口内只发一条升级通知，
// 避免"一台实例 5 条告警同时到期"刷出 5 条升级消息。
func dispatchEscalationNotify(
	row monitorModels.InsightMonitorAlertEscalations,
	event monitorModels.InsightMonitorAlertEvents,
	targetPolicy monitorModels.InsightMonitorNotifyPolicies,
) int {
	if len(decodeUint64List(targetPolicy.ChannelIDs)) == 0 {
		return 0
	}
	instance := loadNotifyInstanceContext(event.InstanceID)
	instance.AlertTemplateID = loadNotifyAlertTemplateID(event.InstanceID)

	if notifyAggregateHit(event, targetPolicy, NotifyTriggerEscalated) {
		global.App.Log.Infof("[monitor] 告警 %s 的升级通知命中聚合窗口（策略「%s」/%d 秒），本轮不重复打扰",
			row.Fingerprint, targetPolicy.Name, targetPolicy.AggregateWindowSeconds)
		// 合并掉的升级也算"已升级"：链要继续往下走，只是这一级不再打扰人
		recordAggregateSkip(event, targetPolicy, NotifyTriggerEscalated)
		return 1
	}

	noticeURL := ""
	if value, err := notifier.LoadNoticeURL(); err == nil {
		noticeURL = value
	}
	level := row.Level
	fromName := notifyPolicyName(row.FromPolicyID)
	render := func(
		evt monitorModels.InsightMonitorAlertEvents,
		inst notifyInstanceContext,
		trigger string,
		operator string,
		mentions notifier.Mentions,
		recipients []notifier.Recipient,
		url string,
	) notifyTaskPayload {
		return renderEscalationPayload(evt, inst, level, fromName, targetPolicy.Name, mentions, recipients, url)
	}
	scope := fmt.Sprintf("escalate|%d|%d", row.RootPolicyID, row.Level)
	return dispatchPolicyTasks(event, instance, targetPolicy, NotifyTriggerEscalated, "", noticeURL, scope, render)
}

// notifyPolicyName 取策略名（仅用于文案，取不到返回占位）。
func notifyPolicyName(id uint64) string {
	if id == 0 || global.App.DB == nil {
		return ""
	}
	var name string
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyPolicies{}).
		Where("id = ?", id).Pluck("name", &name).Error; err != nil {
		return ""
	}
	return name
}

// ============================================================================
// 五、消息渲染：升级通知与聚合汇总
// ============================================================================

// renderEscalationPayload 升级通知文案。
//
// 与 firing 的区别必须一眼可见：值班人收到时要立刻知道"这不是新告警，
// 而是某条告警超时没人处理被升级了"，并且知道升级到第几级、从哪条策略升上来。
func renderEscalationPayload(
	event monitorModels.InsightMonitorAlertEvents,
	instance notifyInstanceContext,
	level int,
	fromPolicyName string,
	targetPolicyName string,
	mentions notifier.Mentions,
	recipients []notifier.Recipient,
	noticeURL string,
) notifyTaskPayload {
	base := renderAlertPayload(event, instance, NotifyTriggerEscalated, "", mentions, recipients, noticeURL)

	levelText := fmt.Sprintf("第 %d 级", level)
	fromText := strings.TrimSpace(fromPolicyName)
	targetText := strings.TrimSpace(targetPolicyName)

	// 标题与普通告警保持一致：规则名内嵌的等级段（P0–P3）剥掉；
	// 「[升级·第 N 级]」前缀是升级通道标识，非告警等级，保留。
	title := fmt.Sprintf("[升级·%s] %s（%s未处理）", levelText, operatorToChinese(stripAlertLevelToken(event.Name)),
		formatNotifyDuration(event.DurationSec))

	lines := []string{
		fmt.Sprintf("升级级别：%s", levelText),
	}
	if fromText != "" {
		lines = append(lines, fmt.Sprintf("升级来源：%s", fromText))
	}
	if targetText != "" {
		lines = append(lines, fmt.Sprintf("通知目标：%s", targetText))
	}
	lines = append(lines, "后续：告警恢复或被人工认领后停止升级")

	var markdown strings.Builder
	fmt.Fprintf(&markdown, "### %s\n\n", title)
	for _, line := range lines {
		fmt.Fprintf(&markdown, "- %s\n", line)
	}
	markdown.WriteString("\n")
	markdown.WriteString(base.Markdown)

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n", title)
	for _, line := range lines {
		fmt.Fprintf(&plain, "%s\n", line)
	}
	plain.WriteString(base.PlainText)

	meta := base.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	meta["trigger"] = NotifyTriggerEscalated
	meta["escalation_level"] = fmt.Sprintf("%d", level)

	return notifyTaskPayload{
		Title:      title,
		Markdown:   markdown.String(),
		PlainText:  strings.TrimRight(plain.String(), "\n"),
		Level:      base.Level,
		URL:        base.URL,
		Mentions:   mentions,
		Recipients: recipients,
		Meta:       meta,
	}
}

// renderAggregatePayload 聚合窗口的汇总消息：把窗口内被合并的告警合成一条。
func renderAggregatePayload(
	policy monitorModels.InsightMonitorNotifyPolicies,
	instance notifyInstanceContext,
	events []monitorModels.InsightMonitorAlertEvents,
	windowSeconds int,
	mentions notifier.Mentions,
	recipients []notifier.Recipient,
	noticeURL string,
) notifyTaskPayload {
	instanceLabel := instance.Name
	if instanceLabel == "" && len(events) > 0 {
		instanceLabel = events[0].InstanceID
	}

	// 严重的排前面：P0 → P3
	sorted := make([]monitorModels.InsightMonitorAlertEvents, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		return notifyLevelRank(normalizeNotifyLevel(sorted[i].Level)) < notifyLevelRank(normalizeNotifyLevel(sorted[j].Level))
	})

	title := fmt.Sprintf("[汇总] %s：%d 条告警（%d 秒内合并）", instanceLabel, len(sorted), windowSeconds)
	const maxListed = 10
	lines := make([]string, 0, maxListed+4)
	for index, event := range sorted {
		if index >= maxListed {
			lines = append(lines, fmt.Sprintf("… 其余 %d 条未一一列出", len(sorted)-maxListed))
			break
		}
		lines = append(lines, fmt.Sprintf("[%s] %s（当前值 %s）",
			normalizeNotifyLevel(event.Level), operatorToChinese(event.Name), formatNotifyValue(event.ObservedValue)))
	}
	lines = append(lines, fmt.Sprintf("合并窗口：%d 秒（策略「%s」）", windowSeconds, policy.Name))
	lines = append(lines, "说明：窗口内首条告警已即时通知，其余同类告警合并为本条汇总，以避免消息轰炸")

	url := ""
	if strings.TrimSpace(noticeURL) != "" && instance.InstanceID != "" {
		url = fmt.Sprintf("%s/monitor/detail?instance_id=%s", strings.TrimRight(noticeURL, "/"), instance.InstanceID)
	}

	var markdown strings.Builder
	fmt.Fprintf(&markdown, "### %s\n\n", title)
	for _, line := range lines {
		fmt.Fprintf(&markdown, "- %s\n", line)
	}
	if url != "" {
		fmt.Fprintf(&markdown, "\n[查看实例明细](%s)\n", url)
	}
	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n", title)
	for _, line := range lines {
		fmt.Fprintf(&plain, "%s\n", line)
	}
	if url != "" {
		fmt.Fprintf(&plain, "查看详情：%s\n", url)
	}

	level := ""
	if len(sorted) > 0 {
		level = normalizeNotifyLevel(sorted[0].Level)
	}
	return notifyTaskPayload{
		Title:      title,
		Markdown:   markdown.String(),
		PlainText:  strings.TrimRight(plain.String(), "\n"),
		Level:      level,
		URL:        url,
		Mentions:   mentions,
		Recipients: recipients,
		Meta: map[string]string{
			"instance_id":       instance.InstanceID,
			"policy_id":         fmt.Sprintf("%d", policy.ID),
			"trigger":           NotifyTriggerAggregated,
			"aggregate_window":  fmt.Sprintf("%d", windowSeconds),
			"aggregated_alerts": fmt.Sprintf("%d", len(sorted)),
		},
	}
}

// ============================================================================
// 六、策略保存时的升级链校验
// ============================================================================

// validateEscalationChain 校验升级目标不会形成环（含自环）。
//
// 为什么必须在保存时拦：运行期虽然有深度上限与 visited 兜底，但那只是"不刷屏"，
// 配置本身是错的却没人告诉运维 —— 保存前报错才能把问题前移。
func validateEscalationChain(policyID uint64, targetID uint64) error {
	if targetID == 0 {
		return nil
	}
	if policyID > 0 && targetID == policyID {
		return fmt.Errorf("升级目标策略不能是自身")
	}
	if global.App.DB == nil {
		return nil
	}
	var rows []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Select("id, name, escalate_policy_id").Find(&rows).Error; err != nil {
		return nil
	}
	next := map[uint64]uint64{}
	names := map[uint64]string{}
	for _, row := range rows {
		next[row.ID] = row.EscalatePolicyID
		names[row.ID] = row.Name
	}
	if _, ok := next[targetID]; !ok {
		return fmt.Errorf("升级目标策略不存在，请刷新后重选")
	}
	if escalationChainHasCycle(policyID, targetID, next) {
		return fmt.Errorf("升级链存在循环：%s", escalationChainText(policyID, targetID, next, names))
	}
	return nil
}

// escalationChainHasCycle 从升级目标往前走，判断能否回到起始策略（纯函数，便于单测）。
func escalationChainHasCycle(policyID uint64, targetID uint64, next map[uint64]uint64) bool {
	if policyID > 0 && targetID == policyID {
		return true
	}
	seen := map[uint64]struct{}{}
	current := targetID
	// 上界取 策略总数 + 深度上限：链长不可能超过策略总数，超出说明一定是环
	for hop := 0; hop < len(next)+maxEscalationDepth; hop++ {
		if current == 0 {
			return false
		}
		if policyID > 0 && current == policyID {
			return true
		}
		if _, ok := seen[current]; ok {
			return true
		}
		seen[current] = struct{}{}
		current = next[current]
	}
	return true
}

// escalationChainText 把环路拼成 "A → B → A" 这样的可读文本。
func escalationChainText(policyID uint64, targetID uint64, next map[uint64]uint64, names map[uint64]string) string {
	name := func(id uint64) string {
		if value := strings.TrimSpace(names[id]); value != "" {
			return value
		}
		return fmt.Sprintf("策略#%d", id)
	}
	parts := []string{name(policyID), name(targetID)}
	seen := map[uint64]struct{}{policyID: {}, targetID: {}}
	current := next[targetID]
	for hop := 0; hop < maxEscalationDepth+len(next) && current != 0; hop++ {
		parts = append(parts, name(current))
		if _, ok := seen[current]; ok {
			break
		}
		seen[current] = struct{}{}
		current = next[current]
	}
	return strings.Join(parts, " → ")
}
