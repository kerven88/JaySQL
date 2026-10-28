package services

import (
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// monitor_screen.go —— 大屏（数据库集群监控大屏）聚合数据。
//
// 设计图里有一批"区间统计"：环形图四分类与环比、时段内新增告警、区间内 QPS 均值 /
// 连接峰值、容量项的时段趋势。这些在原 dashboard 响应里没有，全部在这里从真实表算出：
//   - 状态四分类：来自实例卡片本身（screen_class 口径见 classifyScreenCard）
//   - 新增告警 / 已认领 / 已恢复：insight_monitor_alert_events
//   - 区间聚合（QPS 均值、连接峰值、时段起点值）：insight_monitor_metric_points
//
// 硬规则：任何一笔历史查询失败都只 Warn 并把对应字段留 nil，前端显示「未获取」；
// 绝不降级成 0 或模拟数 —— 0 是合法读数，"没算出来"必须显式可见。

// ScreenStatusCounts 大屏环形图四分类。注意四类之和 == 实例总数，前端不再自行归类。
type ScreenStatusCounts struct {
	Healthy     int `json:"healthy"`
	Risk        int `json:"risk"`
	Fault       int `json:"fault"`
	Uncollected int `json:"uncollected"`
}

// ScreenInstanceStats 单实例的区间聚合。指针为 nil 表示该窗口没有可用样本。
type ScreenInstanceStats struct {
	QPSAvg    *float64 `json:"qps_avg"`    // 区间内 QPS 均值
	ConnPeak  *float64 `json:"conn_peak"`  // 区间内连接数峰值
	CPUStart  *float64 `json:"cpu_start"`  // 区间起点附近的 CPU%
	DiskStart *float64 `json:"disk_start"` // 区间起点附近的磁盘已用%
	ConnStart *float64 `json:"conn_start"` // 区间起点附近的连接数
}

// ScreenAlertCounts 大屏「告警数」卡的三个数。
//
// 口径显式分成两类，**不是同一时间维度**：
//   - Pending / Processing 是实时快照（不看页面时间范围），来源与告警事件中心的
//     「待认领 / 处理中」两个 Tab 完全同源（status <> 'recovered' 且 ignored = false，
//     实例可见域 + 模板白名单门控）；
//   - Resolved 是"当前时间范围内"的数据（recovered_at 落在窗口内），
//     与「已解决」Tab 同源。
//
// Pending 含观察期（pending）与已转正未认领（active）两种状态：前端的图例文案是
// 「待认领」、告警事件中心的待认领页签也显式接受 pending，二者必须一致 ——
// 只算 active 会让观察期告警在大屏"隐身"，而告警中心/明细页仍能看到它。
//
// 卡片总数 = Pending + Processing，即"当前未恢复的告警数"，与时间范围无关。
type ScreenAlertCounts struct {
	Pending    int `json:"pending"`    // 待认领快照（pending 观察期 + active 已转正未认领）
	Processing int `json:"processing"` // 处理中（acknowledged）快照
	Resolved   int `json:"resolved"`   // 已解决（recovered_at 落在当前时间范围）
}

// ScreenData 大屏聚合块，挂在 /monitor/dashboard 响应的 screen 字段上。
type ScreenData struct {
	// 统计窗口：预设「最近N分钟」或用户自定义起止。前端区间文案统一用 RangeLabel，
	// 不再自行拼接，避免"页面显示最近1小时、实际按 30 分钟算"这类错位。
	RangeMinutes int    `json:"range_minutes"`
	RangeStart   string `json:"range_start"`
	RangeEnd     string `json:"range_end"`
	RangeLabel   string `json:"range_label"`
	RangeShort   string `json:"range_short"`
	RangeCustom  bool   `json:"range_custom"`
	// Status 当前四分类（来自卡片实时状态）。
	Status ScreenStatusCounts `json:"status"`
	// StatusPrev 时段起点快照。只有当全部实例都能从历史数据确定当时状态时才给出，
	// 任何一个实例的历史不可判定就整体置 null（前端显示「未获取」），不做部分猜测。
	StatusPrev *ScreenStatusCounts `json:"status_prev"`
	// AlertsInRange 时段内新增的告警事件数（含时段内已恢复的 —— 它们确实发生过）。
	AlertsInRange int `json:"alerts_in_range"`
	// AlertCounts 「告警数」卡：活跃/处理中为实时快照，已解决为当前时间范围内（口径注释见类型定义）。
	AlertCounts ScreenAlertCounts `json:"alert_counts"`
	// AlertsPrevRange 上一时段新增数，供「涨速」对比。
	AlertsPrevRange int `json:"alerts_prev_range"`
	// AckedAlerts 已认领（处理中）的告警，按认领时间倒序。
	AckedAlerts []AlertRecord `json:"acked_alerts"`
	// RecoveredAlerts 时段内恢复的告警，按恢复时间倒序。
	RecoveredAlerts []AlertRecord `json:"recovered_alerts"`
	// InstanceStats 区间聚合，键为 instance_id。
	InstanceStats map[string]ScreenInstanceStats `json:"instance_stats"`
	// RiskTop 风险实例 TOP5（按严重度/原因数排序），每项带完整风险原因明细。
	// 全量列表走 /monitor/risks 风险总览接口，这里只露默认 5 个。
	RiskTop []RiskInstance `json:"risk_top"`
}

// classifyScreenCard 把实例卡归入大屏四分类之一。
//
// 判定口径**不再走独立的分级评估引擎**：健康分驱动（先算分再按区间定级，阈值可配，
// 见 monitor_health_state.go），外加固定的采集可信度前置（未采集 / 采集失败 / 数据过期）。
// 全平台只有这一处口径。
func classifyScreenCard(card InstanceMonitorCard) string {
	return evaluateInstanceState(card).Class
}

func countScreenStatus(cards []InstanceMonitorCard) ScreenStatusCounts {
	counts := ScreenStatusCounts{}
	for i := range cards {
		switch cards[i].ScreenClass {
		case "fault":
			counts.Fault++
		case "risk":
			counts.Risk++
		case "uncollected":
			counts.Uncollected++
		default:
			counts.Healthy++
		}
	}
	return counts
}

// screenVisibleInstanceIDs 大屏可见实例 ID 列表（= 卡片来源的 rows）。
//
// 大屏所有告警类统计必须限定在这批实例内。告警中心 buildAlertCenterQuery 用
// `instance_id IN (...)` 把"实例已下线 / 当前不可见"的孤儿事件排除在外
// （口径 2 见 monitor_alert_center.go 顶部注释）。大屏此前只做模板白名单剔除：
// 未知实例在 excludeByInstance 里取不到门控函数（nil）就被原样计入，
// 于是同一个系统里出现"大屏 7 条 / 告警中心 6 条"——差额正是孤儿事件。
// 这里把可见域显式下推到 SQL，两侧口径才真正同构。
func screenVisibleInstanceIDs(rows []monitorInstanceRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.InstanceID)
	}
	return ids
}

func buildScreenData(rows []monitorInstanceRow, cards []InstanceMonitorCard, win monitorWindow) ScreenData {
	screen := ScreenData{
		RangeMinutes:    win.Minutes(),
		RangeStart:      win.Start.Format(time.DateTime),
		RangeEnd:        win.End.Format(time.DateTime),
		RangeLabel:      win.Label(),
		RangeShort:      win.ShortLabel(),
		RangeCustom:     win.Custom,
		Status:          countScreenStatus(cards),
		AckedAlerts:     make([]AlertRecord, 0),
		RecoveredAlerts: make([]AlertRecord, 0),
		InstanceStats:   make(map[string]ScreenInstanceStats),
	}
	if global.App.DB == nil {
		return screen
	}

	// 所有区间查询都绑定同一个窗口 (win.Start, win.End]，自定义区间必须带上界，
	// 否则"回看昨晚 23:00-23:30"会把今晚的数据也统计进来。
	start, end := win.Start, win.End
	span := win.Span()

	// 模板白名单：模板外指标的告警事件不参与大屏的任何统计与列表
	//（口径与待处理告警的 loadActiveEventMap 一致，避免"卡片没告警、统计却有数"的错位）。
	excludeByInstance := make(map[string]func(string) bool, len(rows))
	for _, row := range rows {
		excludeByInstance[row.InstanceID] = alertMetricExcludedByTemplate(row)
	}

	// 可见实例域：告警类统计一律限定在内，与告警中心 buildAlertCenterQuery 同口径，
	// 避免孤儿事件（实例已删/不可见）只在大屏侧被计入造成两边数字不一致。
	visibleIDs := screenVisibleInstanceIDs(rows)

	screen.AlertsInRange, screen.AlertsPrevRange = countAlertsInRange(start, end, span, visibleIDs, excludeByInstance)
	screen.AlertCounts = loadScreenAlertCounts(visibleIDs, excludeByInstance, start, end)
	screen.AckedAlerts = loadAckedAlerts(visibleIDs, excludeByInstance)
	screen.RecoveredAlerts = loadRecoveredAlerts(start, end, visibleIDs, excludeByInstance)
	screen.StatusPrev = buildPrevStatus(cards, start, excludeByInstance)
	screen.InstanceStats = loadInstanceRangeStats(start, end)
	// 风险 TOP5：cards 此刻已带 ScreenClass（分类在调用方完成）。
	screen.RiskTop = buildScreenRiskTop(cards, 5)
	return screen
}

// buildScreenRiskTop 风险实例 TOP N。没有风险实例时返回空数组（不是 null），
// 前端按"当前没有风险实例"处理，而不是「未获取」——空是确定的结论。
func buildScreenRiskTop(cards []InstanceMonitorCard, limit int) []RiskInstance {
	instances := buildRiskInstances(cards)
	if len(instances) > limit {
		instances = instances[:limit]
	}
	if instances == nil {
		instances = make([]RiskInstance, 0)
	}
	return instances
}

// countAlertsInRange 时段内新增告警数：窗口内 + 上一同长时段（供"涨速"对比）。
// 口径用 COALESCE(first_seen_at, triggered_at) —— 生命周期字段是后加的，老事件可能为 NULL。
// 模板白名单在 Go 侧过滤：事件量级小，按模板拼 SQL 的复杂度不值得；失败仍走 Warn + 0。
func countAlertsInRange(start, end time.Time, span time.Duration, visibleIDs []string, excludeByInstance map[string]func(string) bool) (int, int) {
	type eventRef struct {
		InstanceID string
		MetricKey  string
	}
	countRange := func(from, to time.Time) int {
		refs := make([]eventRef, 0, 32)
		if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
			Select("instance_id, metric_key").
			Where("instance_id IN ?", visibleIDs).
			Where("COALESCE(first_seen_at, triggered_at) > ? AND COALESCE(first_seen_at, triggered_at) <= ?",
				commonLocalTime(from), commonLocalTime(to)).
			Find(&refs).Error; err != nil {
			global.App.Log.Warnf("screen: 统计时段内新增告警失败: %v", err)
			return -1
		}
		count := 0
		for _, ref := range refs {
			if exclude := excludeByInstance[ref.InstanceID]; exclude != nil && exclude(ref.MetricKey) {
				continue
			}
			count++
		}
		return count
	}
	inRange := countRange(start, end)
	if inRange < 0 {
		return 0, 0
	}
	prevRange := countRange(start.Add(-span), start)
	if prevRange < 0 {
		return inRange, 0
	}
	return inRange, prevRange
}

// loadScreenAlertCounts 「告警数」卡的三个数。
//
// 待认领 / 处理中是**实时快照**：与 loadActiveEventMap（告警事件中心「待认领/处理中」
// Tab、实例卡异常信息）完全同一条件 —— status <> 'recovered' 且 ignored = false，
// 不带任何时间边界（含观察期 pending）；已忽略的告警不再算作待处理，与采集侧口径一致。
//
// 已解决是**当前时间范围**的数据：status = recovered 且 recovered_at 落在 (start, end]，
// 与 loadRecoveredAlerts（「已解决」Tab）同一条件。
//
// 三类共用同一份模板白名单门控（模板外指标的历史事件一律不计），失败整体回 0 —— 宁可显 0，
// 不给"半个口径"的数字。
func loadScreenAlertCounts(visibleIDs []string, excludeByInstance map[string]func(string) bool, start, end time.Time) ScreenAlertCounts {
	type eventRef struct {
		InstanceID string
		MetricKey  string
		Status     string
	}
	refs := make([]eventRef, 0, 64)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Select("instance_id, metric_key, status").
		Where("instance_id IN ?", visibleIDs).
		Where("(status <> ? AND ignored = ?) OR (status = ? AND recovered_at > ? AND recovered_at <= ?)",
			"recovered", false, "recovered", commonLocalTime(start), commonLocalTime(end)).
		Find(&refs).Error; err != nil {
		global.App.Log.Warnf("screen: 统计告警数失败: %v", err)
		return ScreenAlertCounts{}
	}
	counts := ScreenAlertCounts{}
	for _, ref := range refs {
		if exclude := excludeByInstance[ref.InstanceID]; exclude != nil && exclude(ref.MetricKey) {
			continue
		}
		switch ref.Status {
		case "active", "pending":
			counts.Pending++
		case "acknowledged":
			counts.Processing++
		case "recovered":
			counts.Resolved++
		}
	}
	return counts
}

func loadAckedAlerts(visibleIDs []string, excludeByInstance map[string]func(string) bool) []AlertRecord {
	events := make([]monitorModels.InsightMonitorAlertEvents, 0, 8)
	if err := global.App.DB.
		Where("instance_id IN ?", visibleIDs).
		Where("status = ? AND ignored = ?", "acknowledged", false).
		Order("acked_at desc").
		Limit(20).
		Find(&events).Error; err != nil {
		global.App.Log.Warnf("screen: 加载已认领告警失败: %v", err)
		return make([]AlertRecord, 0)
	}
	// 数据源元信息：已认领列表跨实例，一次性批量取（逐条查库会放大成 N+1）
	metas := loadAlertInstanceMetaMap(collectAlertInstanceIDs(events))
	records := make([]AlertRecord, 0, len(events))
	for _, event := range events {
		if exclude := excludeByInstance[event.InstanceID]; exclude != nil && exclude(event.MetricKey) {
			continue
		}
		records = append(records, eventToAlertRecord(event, metas[event.InstanceID]))
	}
	return records
}

func loadRecoveredAlerts(start, end time.Time, visibleIDs []string, excludeByInstance map[string]func(string) bool) []AlertRecord {
	events := make([]monitorModels.InsightMonitorAlertEvents, 0, 8)
	if err := global.App.DB.
		Where("instance_id IN ?", visibleIDs).
		Where("status = ? AND recovered_at >= ? AND recovered_at <= ?", "recovered",
			commonLocalTime(start), commonLocalTime(end)).
		Order("recovered_at desc").
		Limit(20).
		Find(&events).Error; err != nil {
		global.App.Log.Warnf("screen: 加载时段内已恢复告警失败: %v", err)
		return make([]AlertRecord, 0)
	}
	// 数据源元信息：已认领列表跨实例，一次性批量取（逐条查库会放大成 N+1）
	metas := loadAlertInstanceMetaMap(collectAlertInstanceIDs(events))
	records := make([]AlertRecord, 0, len(events))
	for _, event := range events {
		if exclude := excludeByInstance[event.InstanceID]; exclude != nil && exclude(event.MetricKey) {
			continue
		}
		records = append(records, eventToAlertRecord(event, metas[event.InstanceID]))
	}
	return records
}

// buildPrevStatus 时段起点快照。
//   - fault：取各实例 avail.up 在 cutoff 前最近一个值，命中 0 即故障；
//   - risk：cutoff 时点仍处于活跃/认领状态的告警所属实例（排除已判故障的）。
//
// 任何实例在 cutoff 前没有 avail.up 历史（刚接入 / 时序被清理）都算"不可判定"，
// 整体返回 null —— 宁可显示「未获取」，也不给一个看起来很确定的假环比。
func buildPrevStatus(cards []InstanceMonitorCard, cutoff time.Time, excludeByInstance map[string]func(string) bool) *ScreenStatusCounts {
	faultSet, known := prevFaultInstances(cutoff)
	if !known {
		return nil
	}
	if len(cards) == 0 {
		return &ScreenStatusCounts{}
	}
	for i := range cards {
		if _, ok := faultSet[cards[i].InstanceID]; !ok {
			// 该实例在时点前无可达性历史，无法判定当时状态
			return nil
		}
	}
	riskSet := prevAlertedInstances(cutoff, excludeByInstance)
	counts := ScreenStatusCounts{}
	for i := range cards {
		id := cards[i].InstanceID
		switch {
		case faultSet[id]:
			counts.Fault++
		case riskSet[id]:
			counts.Risk++
		default:
			counts.Healthy++
		}
	}
	return &counts
}

func prevFaultInstances(cutoff time.Time) (map[string]bool, bool) {
	type row struct {
		InstanceID string   `gorm:"column:instance_id"`
		Value      *float64 `gorm:"column:value"`
	}
	rows := make([]row, 0, 32)
	// 每个实例取 cutoff 前最近一次 avail.up 采样。
	err := global.App.DB.
		Table("insight_monitor_metric_points p").
		Select("p.instance_id, p.value").
		Joins(`JOIN (
			SELECT instance_id, MAX(ts) AS mts
			FROM insight_monitor_metric_points
			WHERE metric_key = ? AND ts <= ? AND value IS NOT NULL
			GROUP BY instance_id
		) m ON m.instance_id = p.instance_id AND m.mts = p.ts`, MetricAvailUp, commonLocalTime(cutoff)).
		Where("p.metric_key = ?", MetricAvailUp).
		Find(&rows).Error
	if err != nil {
		global.App.Log.Warnf("screen: 查询时段起点可达性失败: %v", err)
		return nil, false
	}
	set := make(map[string]bool, len(rows))
	for _, item := range rows {
		set[item.InstanceID] = item.Value != nil && *item.Value == 0
	}
	return set, true
}

// prevAlertedInstances 时段起点仍处于告警状态的实例集合（供环比判定 risk）。
// 模板外指标的告警不参与判定 —— 否则"卡片上没有告警、环比却显示之前有风险"。
func prevAlertedInstances(cutoff time.Time, excludeByInstance map[string]func(string) bool) map[string]bool {
	type eventRef struct {
		InstanceID string
		MetricKey  string
	}
	refs := make([]eventRef, 0, 32)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Select("instance_id, metric_key").
		Where("COALESCE(first_seen_at, triggered_at) <= ?", commonLocalTime(cutoff)).
		Where("(recovered_at IS NULL OR recovered_at > ?)", commonLocalTime(cutoff)).
		Where("ignored = ?", false).
		Find(&refs).Error; err != nil {
		global.App.Log.Warnf("screen: 查询时段起点待处理告警失败: %v", err)
		return map[string]bool{}
	}
	set := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if exclude := excludeByInstance[ref.InstanceID]; exclude != nil && exclude(ref.MetricKey) {
			continue
		}
		set[ref.InstanceID] = true
	}
	return set
}

// loadInstanceRangeStats 区间聚合：QPS 均值、连接峰值，以及区间起点附近的
// CPU / 磁盘 / 连接值（供"较时段初 +N"趋势）。无样本的字段保持 nil。
func loadInstanceRangeStats(start, end time.Time) map[string]ScreenInstanceStats {
	stats := make(map[string]ScreenInstanceStats)

	type aggRow struct {
		InstanceID string   `gorm:"column:instance_id"`
		MetricKey  string   `gorm:"column:metric_key"`
		AvgV       *float64 `gorm:"column:avg_v"`
		MaxV       *float64 `gorm:"column:max_v"`
	}
	aggs := make([]aggRow, 0, 64)
	if err := global.App.DB.
		Table("insight_monitor_metric_points").
		Select("instance_id, metric_key, AVG(value) AS avg_v, MAX(value) AS max_v").
		Where("ts > ? AND ts <= ? AND value IS NOT NULL AND metric_key IN ?",
			commonLocalTime(start), commonLocalTime(end),
			[]string{MetricPerfQPS, MetricPerfConnCount, MetricResCPU, MetricResDisk}).
		Group("instance_id, metric_key").
		Find(&aggs).Error; err != nil {
		global.App.Log.Warnf("screen: 区间聚合查询失败: %v", err)
		return stats
	}
	for _, row := range aggs {
		item := stats[row.InstanceID]
		switch row.MetricKey {
		case MetricPerfQPS:
			item.QPSAvg = row.AvgV
		case MetricPerfConnCount:
			item.ConnPeak = row.MaxV
		}
		stats[row.InstanceID] = item
	}

	type startRow struct {
		InstanceID string   `gorm:"column:instance_id"`
		MetricKey  string   `gorm:"column:metric_key"`
		Value      *float64 `gorm:"column:value"`
	}
	starts := make([]startRow, 0, 64)
	if err := global.App.DB.
		Table("insight_monitor_metric_points p").
		Select("p.instance_id, p.metric_key, p.value").
		Joins(`JOIN (
			SELECT instance_id, metric_key, MIN(ts) AS mts
			FROM insight_monitor_metric_points
			WHERE ts > ? AND ts <= ? AND value IS NOT NULL AND metric_key IN ?
			GROUP BY instance_id, metric_key
		) m ON m.instance_id = p.instance_id AND m.metric_key = p.metric_key AND m.mts = p.ts`,
			commonLocalTime(start), commonLocalTime(end),
			[]string{MetricResCPU, MetricResDisk, MetricPerfConnCount}).
		Find(&starts).Error; err != nil {
		global.App.Log.Warnf("screen: 区间起点查询失败: %v", err)
		return stats
	}
	for _, row := range starts {
		item := stats[row.InstanceID]
		switch row.MetricKey {
		case MetricResCPU:
			item.CPUStart = row.Value
		case MetricResDisk:
			item.DiskStart = row.Value
		case MetricPerfConnCount:
			item.ConnStart = row.Value
		}
		stats[row.InstanceID] = item
	}
	return stats
}

// commonLocalTime time.Time → 库层 LocalTime（实现 driver.Valuer）的统一入口。
func commonLocalTime(t time.Time) commonModels.LocalTime {
	return commonModels.LocalTime(t)
}
