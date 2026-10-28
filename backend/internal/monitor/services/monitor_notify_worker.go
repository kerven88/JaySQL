package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
)

// ============================================================================
// 通知投递 worker
//
// 职责：消费投递任务表 → 调渠道适配器发送 → 记录结果 → 失败退避重试 → 连续失败熔断。
//
// 为什么必须异步：钉钉/飞书/SMTP 都是外部依赖，抖动、超时、限流都是常态。
// 把它们放进告警评估的同步链路，会把"评估"这一个纯数据库动作变成不可靠操作。
// ============================================================================

const (
	// 投递 tick：15 秒一次。相比评估（1 分钟）更密，保证退避重试的节奏够细
	monitorNotifySpec = "@every 15s"
	// 单轮最多处理的任务数：防止一次故障风暴把 worker 占满
	monitorNotifyBatch = 30
	// 熔断阈值：连续失败达到该次数自动禁用渠道
	notifyFailStreakLimit = 5
)

var (
	notifyWorkerRunning bool
	notifyWorkerMutex   sync.Mutex
	// rateLimiter 各渠道的分钟级发送计数（进程内即可：单实例部署，且限流是保护性措施）
	rateLimiterMutex sync.Mutex
	rateLimiterState = map[uint64]*notifyRateWindow{}
)

type notifyRateWindow struct {
	windowStart time.Time
	count       int
}

// RunMonitorNotifyWorker 单轮投递：取一批到期任务并发送。
func RunMonitorNotifyWorker() {
	if global.App.DB == nil {
		return
	}
	notifyWorkerMutex.Lock()
	if notifyWorkerRunning {
		notifyWorkerMutex.Unlock()
		return
	}
	notifyWorkerRunning = true
	notifyWorkerMutex.Unlock()
	defer func() {
		notifyWorkerMutex.Lock()
		notifyWorkerRunning = false
		notifyWorkerMutex.Unlock()
	}()

	tasks := claimNotifyTasks(monitorNotifyBatch)
	for _, task := range tasks {
		deliverNotifyTask(task)
	}
}

// claimNotifyTasks 抢占到期任务。
//
// 抢占方式：逐条 UPDATE ... WHERE id=? AND status='pending'，以影响行数判断是否抢到。
// 相比 SELECT ... FOR UPDATE 更简单，且天然避免多 worker 重复投递（幂等键之外的第二道保险）。
func claimNotifyTasks(limit int) []monitorModels.InsightMonitorNotifyTasks {
	now := time.Now()
	var candidates []monitorModels.InsightMonitorNotifyTasks
	if err := global.App.DB.
		Where("status = ?", monitorModels.NotifyTaskPending).
		Where("next_retry_at IS NULL OR next_retry_at <= ?", now).
		Order("id asc").
		Limit(limit).
		Find(&candidates).Error; err != nil {
		global.App.Log.Warnf("[monitor] 领取投递任务失败: %v", err)
		return nil
	}

	claimed := make([]monitorModels.InsightMonitorNotifyTasks, 0, len(candidates))
	for _, task := range candidates {
		result := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
			Where("id = ? AND status = ?", task.ID, monitorModels.NotifyTaskPending).
			Updates(map[string]any{
				"status":     monitorModels.NotifyTaskSending,
				"updated_at": notifyNowLocal(),
			})
		if result.Error != nil || result.RowsAffected == 0 {
			continue
		}
		claimed = append(claimed, task)
	}
	return claimed
}

// deliverNotifyTask 投递单条任务。
func deliverNotifyTask(task monitorModels.InsightMonitorNotifyTasks) {
	row, cfg, err := loadNotifyChannelRuntime(task.ChannelID)
	if err != nil {
		finishNotifyTaskFailed(task, 0, fmt.Sprintf("渠道不可用：%v", err), false)
		return
	}
	if !row.Enabled {
		finishNotifyTaskFailed(task, 0, "渠道已停用", false)
		return
	}
	// 限流：超限的任务顺延，不消耗重试次数
	if !notifyRateAllow(row) {
		deferNotifyTask(task, 30*time.Second, "渠道限流，任务顺延", false)
		return
	}

	adapter, ok := notifier.Get(row.Kind)
	if !ok {
		finishNotifyTaskFailed(task, 0, fmt.Sprintf("渠道类型 %s 未实现", row.Kind), false)
		return
	}

	payload, err := decodeNotifyPayload(task.Payload)
	if err != nil {
		finishNotifyTaskFailed(task, 0, fmt.Sprintf("消息内容损坏：%v", err), false)
		return
	}
	message := payload.toMessage()

	timeout := row.TimeoutSeconds
	if timeout <= 0 {
		timeout = 8
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+2)*time.Second)
	defer cancel()

	started := time.Now()
	result, sendErr := adapter.Send(ctx, cfg, message)
	elapsed := int(time.Since(started).Milliseconds())

	if sendErr != nil {
		attempt := task.Attempt + 1
		maxAttempt := task.MaxAttempt
		if maxAttempt <= 0 {
			maxAttempt = 4
		}
		global.App.Log.Warnf("[monitor] 投递失败（任务 %d，第 %d/%d 次）：%v", task.ID, attempt, maxAttempt, sendErr)
		if attempt >= maxAttempt {
			finishNotifyTaskFailed(task, elapsed, truncateString(sendErr.Error(), 500), true)
			return
		}
		deferNotifyTask(task, notifyRetryDelay(attempt), truncateString(sendErr.Error(), 500), true)
		return
	}

	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		Where("id = ?", task.ID).
		Updates(map[string]any{
			"status":       monitorModels.NotifyTaskSuccess,
			"attempt":      task.Attempt + 1,
			"sent_at":      notifyNowLocal(),
			"duration_ms":  elapsed,
			"channel_code": result.Code,
			"last_error":   "",
			"updated_at":   notifyNowLocal(),
		}).Error; err != nil {
		global.App.Log.Warnf("[monitor] 更新投递结果失败: %v", err)
	}
	// 成功一次即重置熔断计数
	global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{"fail_streak": 0, "last_error": ""})
}

// finishNotifyTaskFailed 终态失败：任务置 failed，并累计渠道熔断计数。
// countAsChannelFailure=true 表示这次失败归因于渠道本身（网络/配置），参与熔断计数；
// 内容损坏之类的"我们自己的问题"不计入渠道熔断。
func finishNotifyTaskFailed(task monitorModels.InsightMonitorNotifyTasks, elapsed int, reason string, countAsChannelFailure bool) {
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		Where("id = ?", task.ID).
		Updates(map[string]any{
			"status":      monitorModels.NotifyTaskFailed,
			"attempt":     task.Attempt + 1,
			"duration_ms": elapsed,
			"last_error":  truncateString(reason, 500),
			"updated_at":  notifyNowLocal(),
		}).Error; err != nil {
		global.App.Log.Warnf("[monitor] 置投递任务失败态出错: %v", err)
	}
	if !countAsChannelFailure {
		return
	}

	var row monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Where("id = ?", task.ChannelID).Take(&row).Error; err != nil {
		return
	}
	streak := row.FailStreak + 1
	updates := map[string]any{
		"fail_streak": streak,
		"last_error":  truncateString(reason, 500),
	}
	if streak >= notifyFailStreakLimit && row.Enabled {
		// 熔断：连续失败达阈值自动停用，避免配置写错时无限重试刷日志与触发对端封禁
		updates["enabled"] = false
		global.App.Log.Errorf("[monitor] 渠道「%s」连续失败 %d 次，已自动停用，请检查配置后重新启用", row.Name, streak)
	}
	global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).
		Where("id = ?", row.ID).Updates(updates)
}

// deferNotifyTask 顺延任务（稍后重试）。
//
// countAttempt 区分两种"顺延"：
//   - false：限流等"等一会就好"的场景，不消耗重试预算，attempt 不动；
//   - true ：发送失败后的退避重试，必须累加 attempt —— 否则 max_attempt 形同虚设，
//     任务会永远 pending（既进不了 failed 终态，也不参与渠道熔断），
//     而 notifyRetryDelay 恒收到 attempt=1，退避序列退化成"每 10 秒打一次对端"。
func deferNotifyTask(task monitorModels.InsightMonitorNotifyTasks, delay time.Duration, reason string, countAttempt bool) {
	next := commonModels.LocalTime(time.Now().Add(delay))
	updates := map[string]any{
		"status":        monitorModels.NotifyTaskPending,
		"next_retry_at": next,
		"last_error":    truncateString(reason, 500),
		"updated_at":    notifyNowLocal(),
	}
	if countAttempt {
		updates["attempt"] = task.Attempt + 1
	}
	global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		Where("id = ?", task.ID).
		Updates(updates)
}

// notifyRetryDelay 退避序列：10s → 1m → 5m → 30m（第 5 次及以上按 30 分钟）。
// 注意 attempt 从 1 开始（首次失败后为 1）；渠道 max_attempt 默认 4，
// 因此默认只会用到前三档，第 4 次失败即进入 failed 终态并累计渠道熔断计数。
func notifyRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 10 * time.Second
	case 2:
		return time.Minute
	case 3:
		return 5 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// notifyRateAllow 渠道级分钟限流。rate_limit_per_min<=0 表示不限流。
func notifyRateAllow(channel monitorModels.InsightMonitorNotifyChannels) bool {
	if channel.RateLimitPerMin <= 0 {
		return true
	}
	rateLimiterMutex.Lock()
	defer rateLimiterMutex.Unlock()

	now := time.Now()
	window, ok := rateLimiterState[channel.ID]
	if !ok || now.Sub(window.windowStart) >= time.Minute {
		rateLimiterState[channel.ID] = &notifyRateWindow{windowStart: now, count: 1}
		return true
	}
	if window.count >= channel.RateLimitPerMin {
		return false
	}
	window.count++
	return true
}

// ---------- 投递记录（页面查询与手动重发） ----------

// NotifyTaskItem 投递记录条目。
type NotifyTaskItem struct {
	ID           uint64 `json:"id"`
	EventID      uint64 `json:"event_id"`
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	PolicyID     uint64 `json:"policy_id"`
	PolicyName   string `json:"policy_name"`
	ChannelID    uint64 `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	ChannelKind  string `json:"channel_kind"`
	Trigger      string `json:"trigger"`
	TargetType   string `json:"target_type"`
	TargetLabel  string `json:"target_label"`
	Status       string `json:"status"`
	StatusText   string `json:"status_text"`
	Attempt      int    `json:"attempt"`
	MaxAttempt   int    `json:"max_attempt"`
	DurationMs   int    `json:"duration_ms"`
	ChannelCode  string `json:"channel_code"`
	LastError    string `json:"last_error"`
	NextRetryAt  string `json:"next_retry_at"`
	CreatedAt    string `json:"created_at"`
	SentAt       string `json:"sent_at"`
}

// NotifyTaskFilter 投递记录查询条件。
type NotifyTaskFilter struct {
	EventID     uint64
	Fingerprint string
	InstanceID  string
	Status      string
	ChannelID   uint64
	Limit       int
}

// ListNotifyTasks 查询投递记录。
func ListNotifyTasks(filter NotifyTaskFilter) ([]NotifyTaskItem, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	query := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{})
	if filter.EventID > 0 {
		query = query.Where("event_id = ?", filter.EventID)
	}
	if strings.TrimSpace(filter.Fingerprint) != "" {
		query = query.Where("fingerprint = ?", strings.TrimSpace(filter.Fingerprint))
	}
	if strings.TrimSpace(filter.InstanceID) != "" {
		query = query.Where("instance_id = ?", strings.TrimSpace(filter.InstanceID))
	}
	if strings.TrimSpace(filter.Status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(filter.Status))
	}
	if filter.ChannelID > 0 {
		query = query.Where("channel_id = ?", filter.ChannelID)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	var rows []monitorModels.InsightMonitorNotifyTasks
	if err := query.Order("id desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}

	policies := map[uint64]string{}
	var policyRows []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Select("id, name").Find(&policyRows).Error; err == nil {
		for _, row := range policyRows {
			policies[row.ID] = row.Name
		}
	}
	channels := map[uint64]string{}
	var channelRows []monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Select("id, name").Find(&channelRows).Error; err == nil {
		for _, row := range channelRows {
			channels[row.ID] = row.Name
		}
	}

	items := make([]NotifyTaskItem, 0, len(rows))
	for _, row := range rows {
		instanceName := ""
		if row.InstanceID != "" {
			instanceName = row.InstanceID
		}
		items = append(items, NotifyTaskItem{
			ID:           row.ID,
			EventID:      row.EventID,
			InstanceID:   row.InstanceID,
			InstanceName: instanceName,
			PolicyID:     row.PolicyID,
			PolicyName:   policies[row.PolicyID],
			ChannelID:    row.ChannelID,
			ChannelName:  channels[row.ChannelID],
			ChannelKind:  row.ChannelKind,
			Trigger:      row.Trigger,
			TargetType:   row.TargetType,
			TargetLabel:  row.TargetLabel,
			Status:       row.Status,
			StatusText:   notifyTaskStatusText(row.Status),
			Attempt:      row.Attempt,
			MaxAttempt:   row.MaxAttempt,
			DurationMs:   row.DurationMs,
			ChannelCode:  row.ChannelCode,
			LastError:    row.LastError,
			NextRetryAt:  formatLocalTime(row.NextRetryAt),
			CreatedAt:    time.Time(row.CreatedAt).Format("2006-01-02 15:04:05"),
			SentAt:       formatLocalTime(row.SentAt),
		})
	}
	return items, nil
}

// RetryNotifyTask 手动重发：重置次数与状态，让它重新进入投递队列。
func RetryNotifyTask(id uint64) error {
	if global.App.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := notifyNowLocal()
	result := global.App.DB.Model(&monitorModels.InsightMonitorNotifyTasks{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        monitorModels.NotifyTaskPending,
			"attempt":       0,
			"next_retry_at": now,
			"last_error":    "",
			"updated_at":    now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("投递记录不存在")
	}
	return nil
}

func notifyTaskStatusText(status string) string {
	switch status {
	case monitorModels.NotifyTaskPending:
		return "待投递"
	case monitorModels.NotifyTaskSending:
		return "投递中"
	case monitorModels.NotifyTaskSuccess:
		return "已送达"
	case monitorModels.NotifyTaskFailed:
		return "投递失败"
	case monitorModels.NotifyTaskSkippedSilent:
		return "已静默"
	default:
		return status
	}
}

func decodeNotifyPayload(raw []byte) (notifyTaskPayload, error) {
	var payload notifyTaskPayload
	if len(raw) == 0 {
		return payload, fmt.Errorf("消息内容为空")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func formatLocalTime(value *commonModels.LocalTime) string {
	if value == nil {
		return ""
	}
	return time.Time(*value).Format("2006-01-02 15:04:05")
}
