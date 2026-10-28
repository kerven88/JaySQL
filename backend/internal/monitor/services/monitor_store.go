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
	"gorm.io/gorm/clause"
)

// ============================================================================
// 监控数据落库与读取
//
// 落库纪律（三条，缺一不可）：
//   1. value 允许 NULL 且语义为"没采到" —— 这是消灭"假 0"的物理基础；
//   2. 最新值表 UPSERT、时序点表批量 INSERT，两者在同一事务内的语义是"同一轮采集"；
//   3. 采集运行态回写实例配置行，页面据此渲染数据新鲜度与采集健康度。
// ============================================================================

// MetricSampleView 是给前端渲染的采样点视图。
// 页面硬规则：quality != ok 时显示 "—" + reason，不参与聚合、不参与告警。
type MetricSampleView struct {
	Key           string  `json:"key"`
	DisplayName   string  `json:"display_name"`
	Category      string  `json:"category"`
	CategoryLabel string  `json:"category_label"`
	Layer         string  `json:"layer"`
	LayerLabel    string  `json:"layer_label"`
	Unit          string  `json:"unit"`
	Value         float64 `json:"value"`
	Available     bool    `json:"available"`
	Quality       string  `json:"quality"`
	Reason        string  `json:"reason"`
	Source        string  `json:"source"`
	DisplayHint   string  `json:"display_hint"`
	SubDims       string  `json:"sub_dims"`
	Ts            string  `json:"ts"`
	Advice        string  `json:"advice"`
	// ---- 展示元数据：含义 / 阈值 / 分组 / 顺序，全部由后端下发 ----
	// 前端不得自行维护指标中文名与阈值：一旦写死，新增指标必须改前端，
	// 且不同页面会出现"同一指标两套文案"。
	Threshold string `json:"threshold"` // 参考阈值
	Group     string `json:"group"`     // 分组名（同一 Tab 内二次分组）
	Order     int    `json:"order"`     // 展示顺序（与需求文档编号一致）
}

// applyDisplayMeta 把展示元数据挂到采样视图上。
// 未登记的指标回落到字典 display_name，保证卡片不丢、只是没有阈值说明。
func applyDisplayMeta(view *MetricSampleView) {
	meta, ok := metricDisplayMetaOf(view.Key)
	if !ok {
		view.Group = "其他"
		view.Order = 999
		return
	}
	view.Threshold = meta.Threshold
	view.Group = meta.Group
	view.Order = meta.Order
	if view.DisplayName == "" || view.DisplayName == view.Key {
		view.DisplayName = meta.Label
	}
}

// metricDisplayName 从字典取展示名，字典缺失时回退到 code 常量。
func metricDisplayName(metricKey string) (string, string, string, string) {
	for _, item := range builtinMetricCatalog {
		if item.MetricKey == metricKey {
			return item.DisplayName, item.Category, item.Unit, item.DisplayHint
		}
	}
	return metricKey, CategoryPerformance, "", "line"
}

// metricAdvice 取字典里的建议处置
func metricAdvice(metricKey string) string {
	for _, item := range builtinMetricCatalog {
		if item.MetricKey == metricKey {
			return item.Advice
		}
	}
	return ""
}

// persistSamples 把一轮采集结果写入 最新值表 + 时序点表 + 采集运行态。
// collectErr 非空表示整体不可用（连不上/认证失败），此时不写任何指标点。
//
// collectTs 是采样点的归属时刻（槽位起点，相位对齐的周期网格点），
// 时序点表 / 最新值表 / last_collect_at 都用它 —— 不用执行时刻，
// 否则点会漂离聚合桶边界，趋势图出现"每分钟固定缺一个点"的假缺口。
func persistSamples(row monitorInstanceRow, samples []MetricSample, elapsedMs int, collectErr error, collectTs time.Time) {
	if global.App.DB == nil {
		return
	}
	now := collectTs
	if now.IsZero() {
		now = time.Now()
	}

	if collectErr != nil {
		// 整体不可用：只回写采集运行态，让页面能说明"为什么没有数据"
		markCollectFailure(row.InstanceID, elapsedMs, collectErr)
		return
	}

	// ---- 1. 最新值表：UPSERT（每实例每指标每子维度一行） ----
	latestRows := make([]monitorModels.InsightMonitorMetricLatest, 0, len(samples))
	unitOf := map[string]string{}
	nowLocal := commonModels.LocalTime(now)
	for _, sample := range samples {
		key := canonicalMetricKey(sample.Key)
		if key == "" {
			continue
		}
		_, _, unit, _ := metricDisplayName(key)
		if sample.Unit != "" {
			unit = sample.Unit
		}
		unitOf[key] = unit
		var value *float64
		if qualityIsAvailable(sample.Quality) {
			v := sanitizeMetricValue(sample.Value)
			value = &v
		}
		latestRows = append(latestRows, monitorModels.InsightMonitorMetricLatest{
			InstanceID: row.InstanceID,
			MetricKey:  key,
			SubDims:    sample.SubDims,
			Value:      value,
			Quality:    sample.Quality,
			Unit:       unit,
			Source:     sample.Source,
			Reason:     truncateText(sample.Reason, 240),
			CollectMs:  elapsedMs,
			Ts:         nowLocal,
			UpdatedAt:  nowLocal,
		})
	}
	if len(latestRows) > 0 {
		if err := global.App.DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "instance_id"}, {Name: "metric_key"}, {Name: "sub_dims"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"value", "quality", "unit", "source", "reason", "collect_ms", "ts", "updated_at",
			}),
		}).CreateInBatches(&latestRows, 200).Error; err != nil {
			global.App.Log.Warnf("upsert monitor metric latest failed: %v", err)
		}
	}

	// ---- 2. 时序点表：只落"有值"与"明确不可用"的采样，NULL 表达"没采到" ----
	pointRows := make([]monitorModels.InsightMonitorMetricPoints, 0, len(samples))
	for _, sample := range samples {
		key := canonicalMetricKey(sample.Key)
		if key == "" {
			continue
		}
		var value *float64
		if qualityIsAvailable(sample.Quality) {
			v := sanitizeMetricValue(sample.Value)
			value = &v
		}
		pointRows = append(pointRows, monitorModels.InsightMonitorMetricPoints{
			InstanceID: row.InstanceID,
			DBType:     row.DbType,
			MetricKey:  key,
			SubDims:    sample.SubDims,
			Ts:         nowLocal,
			Value:      value,
			Quality:    sample.Quality,
			Source:     sample.Source,
			CollectMs:  elapsedMs,
			CreatedAt:  nowLocal,
		})
	}
	if len(pointRows) > 0 {
		if err := global.App.DB.CreateInBatches(&pointRows, 500).Error; err != nil {
			// 时序点表可能是分区表尚未创建，不能因此判定采集失败
			global.App.Log.Warnf("insert monitor metric points failed: %v", err)
		}
	}

	// ---- 3. 采集运行态 ----
	quality := summarizeCollectQuality(samples)
	markCollectSuccess(row.InstanceID, elapsedMs, quality, now)
}

// summarizeCollectQuality 把一组采样归纳为实例级数据质量。
// ok：全部可信；partial：部分不可用；error：一条可信的都没有。
func summarizeCollectQuality(samples []MetricSample) string {
	if len(samples) == 0 {
		return QualityNoData
	}
	okCount := 0
	unsupported := 0
	for _, sample := range samples {
		switch sample.Quality {
		case QualityOK:
			okCount++
		case QualityUnsupported:
			unsupported++
		}
	}
	if okCount == 0 {
		if unsupported == len(samples) {
			return QualityUnsupported
		}
		return QualityError
	}
	if okCount < len(samples) {
		return "partial"
	}
	return QualityOK
}

// markCollectSuccess 记录采集成功。collectTs 用槽位起点：调度判定基于槽位跨越，
// last_collect_at 与采样点同刻才能保证"本轮采过"就是"本槽位已占用"，不会重复采。
func markCollectSuccess(instanceID string, elapsedMs int, quality string, collectTs time.Time) {
	now := commonModels.LocalTime(collectTs)
	if collectTs.IsZero() {
		now = commonModels.LocalTime(time.Now())
	}
	if err := global.App.DB.Model(&monitorModels.InsightMonitorTargetConfigs{}).
		Where("instance_id = ?", instanceID).
		Updates(map[string]any{
			"last_collect_at":      now,
			"last_success_at":      now,
			"last_collect_status":  "success",
			"last_collect_message": "",
			"last_collect_ms":      elapsedMs,
			"consecutive_failures": 0,
			"collect_quality":      quality,
		}).Error; err != nil {
		global.App.Log.Warnf("update monitor collect state failed: %v", err)
	}
}

// markCollectFailure 采集整体失败：递增连续失败次数，并把原因落到运行态。
// 连续失败会触发退避（见 monitor_collect.go 的 dueForCollect），避免对已下线实例空转。
func markCollectFailure(instanceID string, elapsedMs int, collectErr error) {
	now := commonModels.LocalTime(time.Now())
	message := truncateText(collectErr.Error(), 240)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorTargetConfigs{}).
		Where("instance_id = ?", instanceID).
		Updates(map[string]any{
			"last_collect_at":      now,
			"last_collect_status":  "failed",
			"last_collect_message": message,
			"last_collect_ms":      elapsedMs,
			"collect_quality":      QualityError,
			"consecutive_failures": gorm.Expr("consecutive_failures + 1"),
		}).Error; err != nil {
		global.App.Log.Warnf("update monitor collect failure state failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 读取：页面首屏只查最新值表，扫描量恒定，不碰时序表
// ---------------------------------------------------------------------------

func loadLatestMap(instanceIDs []string) map[string][]monitorModels.InsightMonitorMetricLatest {
	result := map[string][]monitorModels.InsightMonitorMetricLatest{}
	if global.App.DB == nil || len(instanceIDs) == 0 {
		return result
	}
	rows := make([]monitorModels.InsightMonitorMetricLatest, 0, len(instanceIDs)*16)
	// 分批查询，避免 IN 列表过长
	for start := 0; start < len(instanceIDs); start += 200 {
		end := start + 200
		if end > len(instanceIDs) {
			end = len(instanceIDs)
		}
		batch := make([]monitorModels.InsightMonitorMetricLatest, 0, (end-start)*16)
		if err := global.App.DB.Where("instance_id IN ?", instanceIDs[start:end]).Find(&batch).Error; err != nil {
			global.App.Log.Warnf("load monitor metric latest failed: %v", err)
			return result
		}
		rows = append(rows, batch...)
	}
	for _, row := range rows {
		result[row.InstanceID] = append(result[row.InstanceID], row)
	}
	return result
}

// loadActiveEventMap 读取各实例当前活跃的告警事件（含未恢复但已被抑制的）。
//
// 活跃判定为 status <> 'recovered'（含观察期 pending）：pending 只是尚未转正、还没
// 发过通知，它依然是一条未闭合的告警，且大屏「待认领」图例与告警事件中心的待认领
// 页签都显式接受 pending。此前只取 active/acknowledged 会让明细页实例卡的告警数
// 比同页「实例告警明细」弹窗（按 unresolved=true 取数）少，出现"卡显示 0 条、
// 点开却有 1 条"的自相矛盾。
//
// 模板白名单过滤在查询出口完成：模板外指标的历史告警事件不返回给任何页面。
// 新告警的产生已在评估侧（evaluateInstanceAlerts）用同一口径拦截，这里拦的是
// 模板变更/裁剪之前落库的存量事件 —— 只改前端隐藏不算过滤，统计口径也会被污染。
func loadActiveEventMap(rows []monitorInstanceRow) map[string][]monitorModels.InsightMonitorAlertEvents {
	result := map[string][]monitorModels.InsightMonitorAlertEvents{}
	if global.App.DB == nil || len(rows) == 0 {
		return result
	}
	instanceIDs := make([]string, 0, len(rows))
	excludeByInstance := make(map[string]func(string) bool, len(rows))
	for _, item := range rows {
		instanceIDs = append(instanceIDs, item.InstanceID)
		excludeByInstance[item.InstanceID] = alertMetricExcludedByTemplate(item)
	}
	events := make([]monitorModels.InsightMonitorAlertEvents, 0, len(instanceIDs)*4)
	for start := 0; start < len(instanceIDs); start += 200 {
		end := start + 200
		if end > len(instanceIDs) {
			end = len(instanceIDs)
		}
		batch := make([]monitorModels.InsightMonitorAlertEvents, 0, (end-start)*4)
		if err := global.App.DB.
			Where("instance_id IN ? AND status <> ? AND ignored = ?",
				instanceIDs[start:end], "recovered", false).
			Order("severity asc, last_seen_at desc").
			Find(&batch).Error; err != nil {
			global.App.Log.Warnf("load monitor alert events failed: %v", err)
			return result
		}
		events = append(events, batch...)
	}
	for _, event := range events {
		if exclude := excludeByInstance[event.InstanceID]; exclude != nil && exclude(event.MetricKey) {
			continue
		}
		result[event.InstanceID] = append(result[event.InstanceID], event)
	}
	return result
}

// alertInstanceMeta 告警卡片展示"数据源"所需的实例元信息。
// 事件表只存 instance_id 与 db_type，hostname/port/备注/环境都在实例表上，
// 由调用方按实例一次性取好再传给 eventToAlertRecord，避免逐条告警查库。
type alertInstanceMeta struct {
	InstanceID      string
	InstanceName    string // remark（业务名），为空时前端回落地址
	InstanceTarget  string // hostname:port
	DbType          string
	EnvironmentName string
}

// metaFromRow 从采集行直接构造（明细/卡片路径已持有完整实例信息，无需再查）。
func metaFromRow(row monitorInstanceRow) alertInstanceMeta {
	return alertInstanceMeta{
		InstanceID:      row.InstanceID,
		InstanceName:    row.Remark,
		InstanceTarget:  fmt.Sprintf("%s:%d", row.Hostname, row.Port),
		DbType:          row.DbType,
		EnvironmentName: row.EnvironmentName,
	}
}

// loadAlertInstanceMetaMap 批量取实例元信息（大屏 screen 的已认领/已恢复列表用）。
// 实例量级是百级，一次 IN 查询远好于 N 次单查；查不到时返回空 meta，
// 前端按"只有地址"降级展示，绝不因此丢掉告警本身。
func loadAlertInstanceMetaMap(instanceIDs []string) map[string]alertInstanceMeta {
	result := make(map[string]alertInstanceMeta, len(instanceIDs))
	if global.App.DB == nil || len(instanceIDs) == 0 {
		return result
	}
	type metaRow struct {
		InstanceID      string
		Hostname        string
		Port            int
		DbType          string
		Remark          string
		EnvironmentName string
	}
	rows := make([]metaRow, 0, len(instanceIDs))
	if err := global.App.DB.Table("insight_instances a").
		Select(`a.instance_id, a.hostname, a.port, a.db_type, a.remark,
			COALESCE(env.name, '') as environment_name`).
		Joins("left join insight_instance_environments env on a.environment = env.id").
		Where("a.instance_id IN ?", instanceIDs).
		Find(&rows).Error; err != nil {
		global.App.Log.Warnf("load alert instance meta failed: %v", err)
		return result
	}
	for _, row := range rows {
		result[row.InstanceID] = alertInstanceMeta{
			InstanceID:      row.InstanceID,
			InstanceName:    row.Remark,
			InstanceTarget:  fmt.Sprintf("%s:%d", row.Hostname, row.Port),
			DbType:          row.DbType,
			EnvironmentName: row.EnvironmentName,
		}
	}
	return result
}

// collectAlertInstanceIDs 去重取事件涉及的实例，供 loadAlertInstanceMetaMap 批量查询。
func collectAlertInstanceIDs(events []monitorModels.InsightMonitorAlertEvents) []string {
	seen := make(map[string]struct{}, len(events))
	ids := make([]string, 0, len(events))
	for _, event := range events {
		id := strings.TrimSpace(event.InstanceID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func eventToAlertRecord(row monitorModels.InsightMonitorAlertEvents, meta alertInstanceMeta) AlertRecord {
	recoveredAt := ""
	if row.RecoveredAt != nil {
		recoveredAt = time.Time(*row.RecoveredAt).Format(time.DateTime)
	}
	observed := row.ObservedValue
	// 数据源元信息：事件表自带 instance_id/db_type 兜底，
	// 实例表补的姓名/地址/环境优先（meta 为空时前端只显示地址，不影响告警本体）。
	dbType := defaultString(meta.DbType, string(row.DBType))
	target := defaultString(meta.InstanceTarget, row.InstanceID)
	record := AlertRecord{
		Name:            row.Name,
		Instance:        target,
		InstanceID:      defaultString(meta.InstanceID, row.InstanceID),
		InstanceName:    meta.InstanceName,
		InstanceTarget:  target,
		DbType:          dbType,
		EnvironmentName: meta.EnvironmentName,
		Level:           row.Level,
		Severity:        defaultString(row.Severity, severityFromLevel(row.Level)),
		Status:          alertStatusText(row.Status),
		StatusKey:       strings.ToLower(strings.TrimSpace(row.Status)),
		TriggeredAt:     time.Time(row.TriggeredAt).Format(time.DateTime),
		RecoveredAt:     recoveredAt,
		MetricKey:       row.MetricKey,
		Message:         row.Message,
		Threshold:       row.Threshold,
		ObservedValue:   observed,
		Advice:          row.Advice,
		Occurrences:     row.Occurrences,
		DurationSec:     row.DurationSec,
		PeakValue:       row.PeakValue,
		Suppressed:      row.Suppressed,
		Ignored:         row.Ignored,
		AckedBy:         row.AckedBy,
		RowKey:          defaultString(row.Fingerprint, fmt.Sprintf("%s|%s", row.InstanceID, row.MetricKey)),
	}
	if row.FirstSeenAt != nil {
		record.FirstSeenAt = time.Time(*row.FirstSeenAt).Format(time.DateTime)
	}
	return record
}

// ---------------------------------------------------------------------------
// 从最新值构建实例卡片（页面只读路径）
// ---------------------------------------------------------------------------

func buildCardFromStore(
	row monitorInstanceRow,
	latestRows []monitorModels.InsightMonitorMetricLatest,
	events []monitorModels.InsightMonitorAlertEvents,
) InstanceMonitorCard {
	card := InstanceMonitorCard{
		ID:              row.ID,
		InstanceID:      row.InstanceID,
		Hostname:        row.Hostname,
		Port:            row.Port,
		Target:          fmt.Sprintf("%s:%d", row.Hostname, row.Port),
		DbType:          row.DbType,
		EnvironmentName: row.EnvironmentName,
		Remark:          row.Remark,
		Business:        row.Business,
		GroupName:       row.GroupName,
		DeployMode:      defaultString(row.DeployMode, "standalone"),
		Status:          "healthy",
		StatusMessage:   "采集正常",
		ResourceSource:  "none",
		EngineSource:    "none",
		Metrics:         make([]MetricSampleView, 0, len(latestRows)),
		Alerts:          make([]AlertRecord, 0, len(events)),
	}
	if row.LastCollectAt != nil {
		card.CollectedAt = time.Time(*row.LastCollectAt).Format(time.DateTime)
		card.FreshnessSeconds = int(time.Since(time.Time(*row.LastCollectAt)).Seconds())
	}
	if row.LastSuccessAt != nil {
		card.LastSuccessAt = time.Time(*row.LastSuccessAt).Format(time.DateTime)
	}
	card.ConsecutiveFailures = row.ConsecutiveFailures
	card.CollectStatus = defaultString(row.LastCollectStatus, "never")
	card.QualityMessage = row.LastCollectMessage

	// 告警卡片展示数据源（名称/地址/类型/环境）：元信息来自本行实例，不再二次查库
	alertMeta := metaFromRow(row)
	for _, event := range events {
		card.Alerts = append(card.Alerts, eventToAlertRecord(event, alertMeta))
	}

	// 未采集过 / 采集失败：给出明确状态与原因，不假装健康
	switch {
	case row.LastCollectAt == nil:
		card.Status = "degraded"
		card.StatusMessage = "尚未采集（等待后台采集任务首轮执行）"
		card.Quality = QualityNoData
	case card.CollectStatus == "failed":
		card.Status = "degraded"
		card.StatusMessage = firstNonEmpty(row.LastCollectMessage, "采集失败")
		card.Quality = QualityError
	}

	if len(latestRows) == 0 {
		return card
	}

	metrics := make([]MetricSampleView, 0, len(latestRows))
	resourceAvailable, engineAvailable := 0, 0
	// 展示以实例当前生效模板为基准：模板裁剪后残留的历史最新值一律不渲染。
	// 之前这里是"保留 + 标记已移出当前模板"，但残留卡片会让运维误以为还在采集，
	// 与「展示=绑定模板」的口径冲突，改为直接过滤。
	// 口径与「部分可用」下钻明细共用 metricVisibleUnderCurrentConfig，
	// 边界同样：未绑定/绑定无效时回落到该类型默认模板，没有默认模板（模板表未播种）
	// 时不过滤，与"全量采集"兜底语义一致；res.* 主机层不受模板约束，照常展示。
	dbType, boundTemplate, instanceRole := row.DbType, row.MetricsTemplate, row.InstanceRole
	for _, row := range latestRows {
		if !metricVisibleUnderCurrentConfig(dbType, boundTemplate, instanceRole, row.MetricKey) {
			continue
		}
		displayName, category, unit, hint := metricDisplayName(row.MetricKey)
		if row.Unit != "" {
			unit = row.Unit
		}
		layer := metricLayer(row.MetricKey)
		quality := row.Quality
		reason := row.Reason
		view := MetricSampleView{
			Key:           row.MetricKey,
			DisplayName:   displayName,
			Category:      category,
			CategoryLabel: categoryLabel(category),
			Layer:         layer,
			LayerLabel:    layerLabel(layer),
			Unit:          unit,
			Quality:       quality,
			Reason:        reason,
			Source:        row.Source,
			DisplayHint:   hint,
			SubDims:       row.SubDims,
			Advice:        metricAdvice(row.MetricKey),
			Ts:            time.Time(row.Ts).Format(time.DateTime),
			Available:     qualityIsAvailable(quality),
		}
		if view.Available && row.Value != nil {
			view.Value = *row.Value
		}
		applyDisplayMeta(&view)
		metrics = append(metrics, view)

		isResource := strings.HasPrefix(row.MetricKey, "res.")
		if view.Available {
			if isResource {
				resourceAvailable++
			} else {
				engineAvailable++
			}
		}
	}
	card.Metrics = metrics
	sort.SliceStable(card.Metrics, func(i, j int) bool {
		left, right := card.Metrics[i], card.Metrics[j]
		// 用 qualityWeight 而非直接读 map：未登记状态不能因零值而冒充 ok。
		if qualityWeight(left.Quality) != qualityWeight(right.Quality) {
			return qualityWeight(left.Quality) < qualityWeight(right.Quality)
		}
		if left.Category != right.Category {
			return categoryOrder(left.Category) < categoryOrder(right.Category)
		}
		return left.Key < right.Key
	})

	if resourceAvailable > 0 {
		card.ResourceSource = "prometheus"
	}
	if engineAvailable > 0 {
		card.EngineSource = "direct"
	}

	card.ResourceMetrics = resourceMetricsFromSamples(card.Metrics)
	card.EngineMetrics = engineMetricsFromSamples(card.Metrics)

	if row.LastCollectAt != nil {
		switch {
		case card.CollectStatus == "failed":
			// 保持上面的降级文案
		case resourceAvailable == 0 && engineAvailable == 0:
			card.Status = "degraded"
			card.Quality = QualityError
			card.StatusMessage = "本轮采集未获取到任何可用指标"
		case row.ConsecutiveFailures >= 3:
			card.Status = "warning"
			card.Quality = QualityStale
			card.StatusMessage = "数据来自上次成功采集，采集已连续失败，数值可能已过期"
		default:
			quality := defaultString(row.CollectQuality, QualityOK)
			card.Quality = quality
			if quality == QualityOK {
				card.StatusMessage = "采集正常"
			} else if quality == QualityUnsupported {
				card.Status = "warning"
				card.StatusMessage = "该引擎当前仅支持连接类指标，其余指标不适用"
			} else if quality == "partial" {
				card.Status = "warning"
				card.StatusMessage = "部分指标不可用（详见指标卡上的质量标记）"
			}
		}
	}
	if len(card.Alerts) > 0 && card.Status == "healthy" {
		card.Status = "warning"
		card.StatusMessage = "存在待认领告警"
	}
	return card
}

func categoryOrder(category string) int {
	switch category {
	case CategoryAvailability:
		return 0
	case CategoryCapacity:
		return 1
	case CategoryPerformance:
		return 2
	case CategoryRisk:
		return 3
	case CategoryChange:
		return 4
	default:
		return 9
	}
}

// sampleValue 从指标视图列表里取某个键的 MetricValue
func sampleValue(metrics []MetricSampleView, key string) MetricValue {
	for _, item := range metrics {
		if item.Key == key {
			return MetricValue{
				Value:     item.Value,
				Unit:      item.Unit,
				Available: item.Available,
				Quality:   item.Quality,
				Reason:    item.Reason,
			}
		}
	}
	return MetricValue{Unit: metricUnitOf(key), Quality: QualityUnsupported, Reason: "该引擎不支持此指标"}
}

func metricUnitOf(key string) string {
	for _, item := range builtinMetricCatalog {
		if item.MetricKey == key {
			return item.Unit
		}
	}
	return ""
}

func metricFromSamples(metrics []MetricSampleView, key string) MetricValue {
	for _, item := range metrics {
		if item.Key == key {
			return MetricValue{Value: item.Value, Unit: item.Unit, Available: item.Available, Quality: item.Quality, Reason: item.Reason}
		}
	}
	return MetricValue{Unit: metricUnitOf(key), Quality: QualityNoData, Reason: "本轮未采集到该指标"}
}

func resourceMetricsFromSamples(metrics []MetricSampleView) ResourceMetrics {
	return ResourceMetrics{
		CPU:            metricFromSamples(metrics, MetricResCPU),
		Memory:         metricFromSamples(metrics, MetricResMemory),
		Disk:           metricFromSamples(metrics, MetricResDisk),
		DiskUsed:       metricFromSamples(metrics, MetricResDiskUsed),
		IO:             metricFromSamples(metrics, MetricResIO),
		NetworkIn:      metricFromSamples(metrics, MetricResNetworkIn),
		NetworkOut:     metricFromSamples(metrics, MetricResNetworkOut),
		CPULoad1:       metricFromSamples(metrics, MetricResCPULoad1),
		CtxSwitches:    metricFromSamples(metrics, MetricResCtxSwitches),
		SwapUsedPct:    metricFromSamples(metrics, MetricResSwapUsedPct),
		DiskIOPS:       metricFromSamples(metrics, MetricResDiskIOPS),
		DiskThroughput: metricFromSamples(metrics, MetricResDiskThroughput),
		DiskAwaitMs:    metricFromSamples(metrics, MetricResDiskAwaitMs),
		NetErrors:      metricFromSamples(metrics, MetricResNetErrors),
		TCPEstablished: metricFromSamples(metrics, MetricResTCPEstablished),
		FDUsedPct:      metricFromSamples(metrics, MetricResFDUsedPct),
		TimeOffsetS:    metricFromSamples(metrics, MetricResTimeOffsetS),
	}
}

func engineMetricsFromSamples(metrics []MetricSampleView) EngineMetrics {
	return EngineMetrics{
		QPS:               metricFromSamples(metrics, MetricPerfQPS),
		TPS:               metricFromSamples(metrics, MetricPerfTPS),
		ReadRows:          metricFromSamples(metrics, MetricPerfReadRows),
		WriteRows:         metricFromSamples(metrics, MetricPerfWriteRows),
		ReadBytes:         metricFromSamples(metrics, MetricPerfReadBytes),
		WriteBytes:        metricFromSamples(metrics, MetricPerfWriteBytes),
		CacheHitRatio:     metricFromSamples(metrics, MetricPerfCacheHitRatio),
		Locks:             metricFromSamples(metrics, MetricPerfLocks),
		Connections:       metricFromSamples(metrics, MetricPerfConnCount),
		ActiveConnections: metricFromSamples(metrics, MetricPerfActiveConn),
		MaxConnections:    metricFromSamples(metrics, MetricPerfMaxConn),
	}
}

// latestByKey 用于告警评估：把最新值压成 map，便于按 metric_key 直接取。
func latestByKey(rows []monitorModels.InsightMonitorMetricLatest) map[string]monitorModels.InsightMonitorMetricLatest {
	result := make(map[string]monitorModels.InsightMonitorMetricLatest, len(rows))
	for _, row := range rows {
		result[row.MetricKey] = row
	}
	return result
}

// latestRowsFromSamples 把刚采到的采样点直接转成"最新值行"的内存形态。
// 用途：后台采集完立刻构建卡片（免一次回表），以及手动"立即采集"的即时反馈。
func latestRowsFromSamples(row monitorInstanceRow, samples []MetricSample) []monitorModels.InsightMonitorMetricLatest {
	now := commonModels.LocalTime(time.Now())
	result := make([]monitorModels.InsightMonitorMetricLatest, 0, len(samples))
	for _, sample := range samples {
		key := canonicalMetricKey(sample.Key)
		if key == "" {
			continue
		}
		_, _, unit, _ := metricDisplayName(key)
		if sample.Unit != "" {
			unit = sample.Unit
		}
		var value *float64
		if qualityIsAvailable(sample.Quality) {
			v := sanitizeMetricValue(sample.Value)
			value = &v
		}
		result = append(result, monitorModels.InsightMonitorMetricLatest{
			InstanceID: row.InstanceID,
			MetricKey:  key,
			SubDims:    sample.SubDims,
			Value:      value,
			Quality:    sample.Quality,
			Unit:       unit,
			Source:     sample.Source,
			Reason:     truncateText(sample.Reason, 240),
			Ts:         now,
			UpdatedAt:  now,
		})
	}
	return result
}
