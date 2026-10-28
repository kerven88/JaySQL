package services

import (
	"fmt"
	"sort"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ============================================================================
// 告警评估引擎
//
// 相对现状的三处关键改进：
//   1. 由后台 tick 驱动，不再依赖页面渲染 —— 否则"没人打开页面，已恢复的告警永远挂在活跃"；
//   2. fingerprint 去重 + 出现次数 + 持续时长 —— 复刻慢日志那次"每实例 4320 条/天"的教训；
//   3. 采集失败抑制指标类告警 —— 采不到不等于是异常，否则一次网络抖动会刷出一整屏"告警"。
// ============================================================================

type ruleEvaluation struct {
	Rule    monitorModels.InsightMonitorAlertRules
	Hit     bool
	Value   float64
	Quality string
	Message string
	Reason  string
}

// EvaluateMonitorAlerts 一轮告警评估。与采集 tick 分开（@every 1m）。
func EvaluateMonitorAlerts() {
	monitorCollectMutex.Lock()
	if monitorAlertRunning {
		monitorCollectMutex.Unlock()
		return
	}
	monitorAlertRunning = true
	monitorCollectMutex.Unlock()
	defer func() {
		monitorCollectMutex.Lock()
		monitorAlertRunning = false
		monitorCollectMutex.Unlock()
		if r := recover(); r != nil {
			global.App.Log.Errorf("EvaluateMonitorAlerts panic: %v", r)
		}
	}()

	if global.App.DB == nil {
		return
	}
	// 只清理引用失效指标的残留规则，不再自动播种默认规则：
	// 指标没有配置规则就不告警，阈值唯一来源是「告警配置 / 告警模板」。
	if err := pruneStaleAlertRules(); err != nil {
		global.App.Log.Warnf("[monitor] 清理失效告警规则失败: %v", err)
	}

	rows, err := listMonitorInstances(monitorListFilter{})
	if err != nil {
		global.App.Log.Errorf("load monitor instances for alert evaluation failed: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	instanceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		instanceIDs = append(instanceIDs, row.InstanceID)
	}
	latestMap := loadLatestMap(instanceIDs)

	// 逐实例解析其告警模板（挂载 → 失效回落 → 类型默认兜底），
	// 同一模板的规则集按模板分组只查一次库，避免逐实例 N+1。
	rulesByTemplate := make(map[uint64][]monitorModels.InsightMonitorAlertRules)
	totalHits := 0
	for _, row := range rows {
		tmpl, _, note := resolveAlertTemplateForInstance(row.DbType, row.AlertTemplateID)
		if tmpl == nil {
			global.App.Log.Warnf("[monitor] 实例 %s 无可用告警模板：%s", row.InstanceID, note)
			continue
		}
		rules, ok := rulesByTemplate[tmpl.ID]
		if !ok {
			rules = loadAlertTemplateRules(tmpl.ID)
			rulesByTemplate[tmpl.ID] = rules
		}
		totalHits += evaluateInstanceAlerts(row, latestMap[row.InstanceID], rules)
	}
	if totalHits > 0 {
		global.App.Log.Infof("[monitor] 告警评估完成：%d 条命中", totalHits)
	}
}

// evaluateInstanceAlerts 评估单个实例，返回命中条数。
func evaluateInstanceAlerts(
	row monitorInstanceRow,
	latestRows []monitorModels.InsightMonitorMetricLatest,
	rules []monitorModels.InsightMonitorAlertRules,
) int {
	latest := latestByKey(latestRows)
	engine := resolveEngine(row.DbType)

	// ---- 采集失败：只出一条"采集异常"，并抑制该实例的全部指标类告警 ----
	collectFailed := false
	if up, ok := latest[MetricAvailUp]; ok {
		collectFailed = up.Quality == QualityError
	} else if row.LastCollectStatus == "failed" {
		collectFailed = true
	}
	if collectFailed {
		reason := row.LastCollectMessage
		if up, ok := latest[MetricAvailUp]; ok && up.Reason != "" {
			reason = up.Reason
		}
		upsertAlertEvent(row, alertCandidate{
			Name:          "数据库监控采集失败",
			MetricKey:     MetricAvailUp,
			Level:         "高",
			Threshold:     1,
			ObservedValue: 0,
			Message:       firstNonEmpty(reason, "实例不可达或认证失败"),
			Advice:        "确认实例网络连通性、账号密码与白名单；若实例已下线请在监控对象中停用",
			RuleID:        0,
		}, false)
	}

	hits := 0
	seen := map[string]struct{}{}
	// 模板外的指标不参与告警评估：指标从模板里移除后，最新值表里可能还留着
	// 移除前采到的旧值，若不拦，这条旧值会继续触发告警，而实际上它已经不再采集了。
	// 被跳过的规则走不到 seen，因此它们的存量事件会在 recoverSilentAlerts 里自动恢复。
	excludedByTemplate := alertMetricExcludedByTemplate(row)

	// 按指标分组、组内按 P0→P3 排序：阶梯告警的核心 —— 同一指标命中最高档即短路。
	// 逐档独立判定的老做法会让一次越限同时触发 P0/P1/P2 三条事件（连环轰炸），
	// 这里改为"只报最严重的那一档"，与服务端事件按指标聚合的口径严格一致。
	groups := make(map[string][]monitorModels.InsightMonitorAlertRules)
	order := make([]string, 0, len(rules))
	for _, rule := range rules {
		if !strings.EqualFold(string(rule.DBType), row.DbType) {
			continue
		}
		if rule.ScopeType == "instance" && strings.TrimSpace(rule.ScopeValue) != "" && rule.ScopeValue != row.InstanceID {
			continue
		}
		if excludedByTemplate(rule.MetricKey) {
			continue
		}
		key := canonicalMetricKey(rule.MetricKey)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], rule)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i] < order[j] })

	for _, key := range order {
		group := groups[key]
		chosen, hit := chooseHighestHitRule(row, group, latest)
		if !hit {
			continue
		}
		rule := chosen.Rule
		hits++
		fingerprint := alertFingerprint(rule.ID, row.InstanceID, rule.MetricKey)
		seen[fingerprint] = struct{}{}

		// 抑制：采集失败时不产生指标类告警（仍记 seen，避免把抑制中的事件误判为恢复）
		suppressed := collectFailed
		upsertAlertEvent(row, alertCandidate{
			Name:          rule.Name,
			MetricKey:     rule.MetricKey,
			Level:         rule.AlertLevel,
			Threshold:     rule.Threshold,
			ObservedValue: chosen.Value,
			Message:       chosen.Message,
			Advice:        metricAdvice(canonicalMetricKey(rule.MetricKey)),
			RuleID:        rule.ID,
			ForSeconds:    holdSecondsOf(rule),
			RecoverySec:   rule.RecoverySeconds,
			Suppressed:    suppressed,
			Engine:        engine,
		}, false)
	}
	recoverSilentAlerts(row.InstanceID, seen, collectFailed)
	return hits
}

// alertMetricExcludedByTemplate 返回"该指标的告警是否应当跳过"的判定函数。
//
// 判定依据直接复用采集器的口径（resolveInstanceMetricKeys = 模板 ∩ 字典总闸），
// 保证"采什么"和"报什么"是同一份集合，不会出现"没在采却还在报"的错位。
//
// 两块豁免：
//   - res.* 主机层指标不受模板管辖，照常告警；
//   - avail.* 是采集器固定产出的运行态指标（不受模板门控），实例可达性告警必须保留，
//     否则"实例连不上"这种最该报的告警会被模板误伤成静默。
func alertMetricExcludedByTemplate(row monitorInstanceRow) func(string) bool {
	keys, tmpl, _ := resolveInstanceMetricKeys(row)
	if tmpl == nil {
		return func(string) bool { return false }
	}
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	return func(metricKey string) bool {
		key := canonicalMetricKey(metricKey)
		if strings.HasPrefix(key, "res.") || strings.HasPrefix(key, "avail.") {
			return false
		}
		_, ok := allowed[key]
		return !ok
	}
}

// chooseHighestHitRule 在同一指标的多档规则里挑出"命中的最高档"。
//
// 阶梯告警的判定核心：按 P0→P3 排序后逐档判定，第一个命中即为结果并短路。
// 与之配套的是事件按 (实例, 指标) 聚合（见 alertFingerprint）：档位迁移更新同一条事件，
// 升级时事件等级随之抬升，而不是并发刷出多条告警。
func chooseHighestHitRule(
	row monitorInstanceRow,
	group []monitorModels.InsightMonitorAlertRules,
	latest map[string]monitorModels.InsightMonitorMetricLatest,
) (ruleEvaluation, bool) {
	if len(group) == 0 {
		return ruleEvaluation{}, false
	}
	sorted := make([]monitorModels.InsightMonitorAlertRules, len(group))
	copy(sorted, group)
	sort.SliceStable(sorted, func(i, j int) bool {
		return alertLevelRankOf(sorted[i].AlertLevel) < alertLevelRankOf(sorted[j].AlertLevel)
	})
	for i := range sorted {
		evaluation := evaluateRule(row, sorted[i], latest)
		if evaluation.Hit {
			return evaluation, true
		}
	}
	return ruleEvaluation{}, false
}

// evaluateRule 按规则判定是否命中。
//
// 数据质量是硬门槛：quality != ok 时一律不命中 —— 这正是"没采到 ≠ 异常"的落点。
// 唯一的例外是规则显式声明 no_data_behavior = 'alert'（缺数据本身要告警）。
func evaluateRule(row monitorInstanceRow, rule monitorModels.InsightMonitorAlertRules, latest map[string]monitorModels.InsightMonitorMetricLatest) ruleEvaluation {
	result := ruleEvaluation{Rule: rule}
	metricKey := canonicalMetricKey(rule.MetricKey)
	row_metric, ok := latest[metricKey]

	noDataBehavior := strings.TrimSpace(rule.NoDataBehavior)
	if !ok {
		result.Quality = QualityNoData
		result.Reason = "本轮未采集到该指标"
		if noDataBehavior == "alert" {
			result.Hit = true
			result.Message = fmt.Sprintf("%s 无数据（规则要求缺数据即告警）", metricLabel(metricKey))
		}
		return result
	}
	result.Quality = row_metric.Quality
	if !qualityIsAvailable(row_metric.Quality) {
		result.Reason = row_metric.Reason
		if noDataBehavior == "alert" && row_metric.Quality != QualityUnsupported {
			result.Hit = true
			result.Message = fmt.Sprintf("%s 数据不可用（%s）", metricLabel(metricKey), firstNonEmpty(row_metric.Reason, row_metric.Quality))
		}
		return result
	}
	if row_metric.Value == nil {
		return result
	}
	result.Value = *row_metric.Value
	if compareMetricValue(result.Value, rule.Operator, rule.Threshold) {
		result.Hit = true
		result.Message = fmt.Sprintf("%s 当前值 %s，%s阈值 %s",
			metricLabel(metricKey), formatPlainValue(result.Value, row_metric.Unit), rule.Operator, formatPlainValue(rule.Threshold, row_metric.Unit))
	}
	return result
}

type alertCandidate struct {
	Name          string
	MetricKey     string
	Level         string
	Threshold     float64
	ObservedValue float64
	Message       string
	Advice        string
	RuleID        uint64
	ForSeconds    int
	RecoverySec   int
	Suppressed    bool
	Engine        string
}

// alertFingerprint 事件指纹：以 (实例, 指标) 为粒度，而不是 (规则, 实例, 指标)。
//
// 这是阶梯告警的前提：同一指标配了 P0/P1/P2 多档时，越限程度变化会让"命中的档位"
// 发生迁移（P2 → P0），若指纹里带 rule_id，每次迁移都会新建一条事件，
// 一次故障刷出一串告警。按指标聚合后，档位迁移更新同一条事件（等级/阈值/消息刷新），
// 运维看到的是"这条告警升级了"，而不是"又来了三条告警"。
func alertFingerprint(_ uint64, instanceID string, metricKey string) string {
	return fmt.Sprintf("%s|%s", instanceID, canonicalMetricKey(metricKey))
}

// upsertAlertEvent 是告警事件的唯一写入口。
//
// 状态推进：
//   - 首次命中：for_seconds == 0 → 直接 active；否则先 pending（观察中，不通知）
//   - 持续命中：pending 满 for_seconds → active；已 active → 累加 occurrences / peak / duration
//   - 不再命中：recovery_seconds 内保持 active（防抖动），满足后置 recovered
func upsertAlertEvent(row monitorInstanceRow, candidate alertCandidate, _ bool) {
	if global.App.DB == nil {
		return
	}
	fingerprint := alertFingerprint(candidate.RuleID, row.InstanceID, candidate.MetricKey)
	now := time.Now()
	nowLocal := commonModels.LocalTime(now)
	level := candidate.Level
	if strings.TrimSpace(level) == "" {
		level = "中"
	}

	var event monitorModels.InsightMonitorAlertEvents
	// acknowledged（已认领）也要命中同一行：认领只是人工接管，不是恢复，
	// 否则下一轮评估会因查不到活跃行而新建一条重复告警。
	err := global.App.DB.
		Where("instance_id = ? AND fingerprint = ?", row.InstanceID, fingerprint).
		Where("status IN ?", []string{"active", "pending", "acknowledged"}).
		Order("id desc").
		Take(&event).Error

	if err == gorm.ErrRecordNotFound {
		status := "active"
		if candidate.ForSeconds > 0 {
			status = "pending"
		}
		record := monitorModels.InsightMonitorAlertEvents{
			Model:         &commonModels.Model{},
			InstanceID:    row.InstanceID,
			DBType:        commonModels.EnumType(row.DbType),
			Name:          candidate.Name,
			MetricKey:     canonicalMetricKey(candidate.MetricKey),
			Level:         level,
			Severity:      severityFromLevel(level),
			Status:        status,
			Threshold:     candidate.Threshold,
			ObservedValue: candidate.ObservedValue,
			PeakValue:     candidate.ObservedValue,
			Message:       candidate.Message,
			Advice:        candidate.Advice,
			TriggeredAt:   nowLocal,
			RuleID:        candidate.RuleID,
			Fingerprint:   fingerprint,
			AlertID:       newAlertEventID(global.App.DB),
			FirstSeenAt:   &nowLocal,
			LastSeenAt:    &nowLocal,
			Occurrences:   1,
			DurationSec:   0,
			Suppressed:    candidate.Suppressed,
		}
		if err := global.App.DB.Create(&record).Error; err != nil {
			global.App.Log.Warnf("create monitor alert event failed: %v", err)
			return
		}
		// 新事件只在 status=active 时通知：pending 是 for_seconds 观察期，还没到"真告警"
		if record.Status == "active" {
			DispatchAlertNotify(record.ID, "firing")
		}
		return
	}
	if err != nil {
		global.App.Log.Warnf("load monitor alert event failed: %v", err)
		return
	}

	firstSeen := now
	if event.FirstSeenAt != nil {
		firstSeen = time.Time(*event.FirstSeenAt)
	}
	duration := int64(now.Sub(firstSeen).Seconds())
	status := event.Status
	if status == "pending" && duration >= int64(candidate.ForSeconds) {
		status = "active"
	}
	// acknowledged 不回退：人工认领后评估器只更新数值，不动状态。
	peak := event.PeakValue
	if candidate.ObservedValue > peak {
		peak = candidate.ObservedValue
	}
	updates := map[string]any{
		"status":         status,
		"level":          level,
		"severity":       severityFromLevel(level),
		"observed_value": candidate.ObservedValue,
		"peak_value":     peak,
		"message":        candidate.Message,
		"advice":         candidate.Advice,
		"threshold":      candidate.Threshold,
		"last_seen_at":   nowLocal,
		"occurrences":    event.Occurrences + 1,
		"duration_sec":   duration,
		"suppressed":     candidate.Suppressed,
	}
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("id = ?", event.ID).Updates(updates).Error; err != nil {
		global.App.Log.Warnf("update monitor alert event failed: %v", err)
		return
	}
	// 通知触发点：观察期结束转正（pending→active）或级别发生变化（升降档都算）。
	// 持续命中同一档不重复通知——去重交给通知链路的静默窗口负责，
	// 这里只判断"语义上是否是一次新的告知"。
	// 持续命中的周期性重发由 MaybeDispatchRepeatNotify 负责：策略配了
	// repeat_minutes 才生效，已认领（acknowledged）的事件人工已接管、不再打扰。
	becameActive := event.Status == "pending" && status == "active"
	levelChanged := normalizeNotifyLevel(event.Level) != normalizeNotifyLevel(level)
	if becameActive || levelChanged {
		DispatchAlertNotify(event.ID, "firing")
	} else if status == "active" {
		MaybeDispatchRepeatNotify(event.ID)
	}
}

// recoverSilentAlerts 把"本轮不再命中"的活跃事件置为已恢复。
//
// 恢复不是即时判定：配置了 recovery_seconds 的规则要求"连续正常满该时长"才落定，
// 避免指标在阈值附近抖动导致告警反复触发/恢复。
func recoverSilentAlerts(instanceID string, seen map[string]struct{}, collectFailed bool) {
	if global.App.DB == nil {
		return
	}
	var active []monitorModels.InsightMonitorAlertEvents
	// acknowledged 同样参与恢复判定：人工认领不代表故障还在，指标正常了就该落定为已恢复。
	if err := global.App.DB.
		Where("instance_id = ? AND status IN ?", instanceID, []string{"active", "pending", "acknowledged"}).
		Find(&active).Error; err != nil {
		return
	}
	if len(active) == 0 {
		return
	}
	now := time.Now()
	nowLocal := commonModels.LocalTime(now)
	for _, event := range active {
		if _, ok := seen[event.Fingerprint]; ok {
			continue
		}
		// 采集失败期间不判定恢复：此时指标一律不可用，判定"恢复正常"没有依据
		if collectFailed {
			continue
		}
		recoverySec := int64(recoverySecondsOf(event))
		if recoverySec > 0 {
			// last_seen_at 为空 = 这条事件从未被评估器刷新过，常见于升级前遗留的历史事件
			// （rule_id=0、metric_key 还是裸名如 cache_hit_ratio、时间戳整列缺失）。
			//
			// 此时若直接用 now 当 last_seen，差值恒为 0 < recoverySec，条件永远成立 → 永远 continue，
			// 事件就变成"僵尸告警"：页面一直显示，observed_value 停在几个月前的旧值，
			// 而且运维把阈值调高、指标早已正常之后它也不会消失。
			//
			// 因此依次回退到 first_seen_at / triggered_at；一个都取不到时视为"久未见"直接放行 ——
			// 宁可让陈旧事件落定为已恢复，也不能让它永远挂着误导运维。
			lastSeen := now
			switch {
			case event.LastSeenAt != nil:
				lastSeen = time.Time(*event.LastSeenAt)
			case event.FirstSeenAt != nil:
				lastSeen = time.Time(*event.FirstSeenAt)
			case !time.Time(event.TriggeredAt).IsZero():
				lastSeen = time.Time(event.TriggeredAt)
			default:
				lastSeen = now.Add(-time.Duration(recoverySec) * time.Second)
			}
			if now.Sub(lastSeen) < time.Duration(recoverySec)*time.Second {
				continue
			}
		}
		if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
			Where("id = ?", event.ID).
			Updates(map[string]any{
				"status":       "recovered",
				"recovered_at": nowLocal,
			}).Error; err != nil {
			global.App.Log.Warnf("recover monitor alert event failed: %v", err)
			continue
		}
		// 恢复也通知：只说坏了不说好了，值班人会反复人工确认（策略可关闭该行为）
		DispatchAlertNotify(event.ID, "recovered")
		// 告警已关闭：终止该告警尚未执行的升级（已恢复的告警不该再把人叫起来）
		cancelAlertEscalations(event.Fingerprint, "告警已恢复")
	}
}

func recoverySecondsOf(event monitorModels.InsightMonitorAlertEvents) int {
	// 事件本身不落 recovery_seconds；统一采用默认 300 秒防抖窗口。
	// 若规则单独配置，会在 upsertAlertEvent 时以事件维度体现出来（下个版本改为落列）。
	return defaultRecoverySeconds
}

const defaultRecoverySeconds = 300

func holdSecondsOf(rule monitorModels.InsightMonitorAlertRules) int {
	if rule.ForSeconds > 0 {
		return rule.ForSeconds
	}
	if rule.HoldMinutes > 0 {
		// 兼容老字段：hold_minutes 曾经是"死字段"（全仓 0 处引用），现在真正生效
		return rule.HoldMinutes * 60
	}
	return 0
}

func severityFromLevel(level string) string {
	// 等级即 P 码（P0–P3）；中文旧值（高/中/低）由 normalizeAlertLevel 兼容映射。
	// 未知等级保守落入 P3（提示），避免脏数据被当成高优先级唤醒值班。
	if normalized := normalizeAlertLevelStrict(level); normalized != "" {
		return normalized
	}
	return AlertLevelP3
}

func levelFromSeverity(severity string) string {
	if normalized := normalizeAlertLevelStrict(severity); normalized != "" {
		return normalized
	}
	return AlertLevelP3
}

func formatPlainValue(value float64, unit string) string {
	// 数值去尾零：0.00→0、1.00→1、92.50→92.5——小数位没有信息量时收敛为整数，
	// 避免告警描述里出现「0.00」「1.00」这类噪声。
	num := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
	switch unit {
	case "%":
		return num + "%"
	case "count":
		return num
	case "":
		return num
	default:
		return num + " " + unit
	}
}
