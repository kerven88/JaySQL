package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 监控运维接口：采集健康度汇总 / 立即采集 / 内置指标清单
//
// 这三个接口解决三个具体的信任问题：
//   1. /monitor/status —— 页面顶部能回答"数据到底采到没有、为什么没有"；
//   2. 立即采集 —— 刚接入的实例不必干等下一个采集周期，也不必反复刷新页面；
//   3. 内置指标清单 —— 指标定义固定在代码里，供指标模板勾选弹窗读取。
//      （原「指标字典」可配置模块已下线，见 monitor_metrics.go 的说明）
// ============================================================================

// ---------------------------------------------------------------------------
// 采集健康度汇总
// ---------------------------------------------------------------------------

type MonitorStatusEngineSummary struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Implemented bool   `json:"implemented"`
	Instances   int    `json:"instances"`
	Healthy     int    `json:"healthy"`
	Failed      int    `json:"failed"`
	LatestAt    string `json:"latest_at"`
}

type MonitorStatusFailure struct {
	InstanceID    string `json:"instance_id"`
	Target        string `json:"target"`
	DbType        string `json:"db_type"`
	Message       string `json:"message"`
	Failures      int    `json:"consecutive_failures"`
	LastCollectAt string `json:"last_collect_at"`
}

type MonitorStatusRunner struct {
	// CollectIntervalSeconds 调度扫描节拍（固定 10 秒扫一次，谁到期采谁）
	CollectIntervalSeconds int `json:"collect_interval_seconds"`
	AlertIntervalSeconds   int `json:"alert_interval_seconds"`
	Workers                int `json:"workers"`
	// ScrapeIntervalSeconds 实例采集周期，来自采集频率配置（唯一数据源）
	ScrapeIntervalSeconds int `json:"scrape_interval_seconds"`
	// PromRateWindowSeconds 由周期派生的 PromQL 采样窗口
	PromRateWindowSeconds int `json:"prom_rate_window_seconds"`
	// PromScrapeActualSeconds / PromScrapeCheck：Prometheus 实际抓取间隔与一致性判定
	// （consistent / mismatch / unknown），页面据此提示是否要同步 prometheus.yml
	PromScrapeActualSeconds int    `json:"prom_scrape_actual_seconds"`
	PromScrapeCheck         string `json:"prom_scrape_check"`
	LatestCollectAt         string `json:"latest_collect_at"`
	AverageCollectMs        int    `json:"average_collect_ms"`
	SlowestCollectMs        int    `json:"slowest_collect_ms"`
	OnDemandLimit           int    `json:"on_demand_limit"`
}

type MonitorStatusData struct {
	GeneratedAt string                       `json:"generated_at"`
	Prometheus  PrometheusStatus             `json:"prometheus"`
	Collect     CollectHealth                `json:"collect"`
	Engines     []MonitorStatusEngineSummary `json:"engines"`
	Failures    []MonitorStatusFailure       `json:"failures"`
	Runner      MonitorStatusRunner          `json:"runner"`
}

type GetMonitorStatusService struct {
	C *gin.Context
}

func (s *GetMonitorStatusService) Run() (*MonitorStatusData, error) {
	ctx := context.Background()
	if s.C != nil && s.C.Request != nil {
		ctx = s.C.Request.Context()
	}
	rows, err := listMonitorInstances(monitorListFilter{})
	if err != nil {
		return nil, err
	}
	promClient := newPrometheusClient()
	promStatus := probePrometheus(ctx, promClient)

	scrapeCfg := GetScrapeConfig()
	runner := MonitorStatusRunner{
		CollectIntervalSeconds: 10,
		AlertIntervalSeconds:   60,
		Workers:                monitorCollectWorkers,
		OnDemandLimit:          OnDemandCollectLimit,
		ScrapeIntervalSeconds:  scrapeCfg.IntervalSeconds,
		PromRateWindowSeconds:  scrapeCfg.PromRateWindowSeconds,
		PromScrapeCheck:        "unknown",
		LatestCollectAt:        "",
		AverageCollectMs:       0,
		SlowestCollectMs:       0,
	}
	// 顺手把"Prometheus 实际抓取间隔"校验出来：这是三处周期是否一致最直接的证据
	if promStatus.Available {
		if actual, consistent, err := CheckPrometheusScrapeInterval(ctx, promClient); err == nil {
			runner.PromScrapeActualSeconds = actual
			if consistent {
				runner.PromScrapeCheck = "consistent"
			} else {
				runner.PromScrapeCheck = "mismatch"
			}
		}
	}
	payload := &MonitorStatusData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Prometheus:  promStatus,
		Collect:     CollectHealth{Notes: make([]string, 0)},
		Engines:     make([]MonitorStatusEngineSummary, 0),
		Failures:    make([]MonitorStatusFailure, 0),
		Runner:      runner,
	}

	type engineBucket struct {
		summary MonitorStatusEngineSummary
		latest  time.Time
	}
	buckets := map[string]*engineBucket{}
	for _, capability := range engineCapabilities {
		buckets[capability.Key] = &engineBucket{
			summary: MonitorStatusEngineSummary{
				Key: capability.Key, Label: capability.Label, Implemented: capability.Implemented,
			},
		}
	}

	totalMs := 0
	collected := 0
	newest := time.Time{}
	for _, row := range rows {
		key := resolveEngine(row.DbType)
		bucket, ok := buckets[key]
		if !ok {
			bucket = &engineBucket{summary: MonitorStatusEngineSummary{Key: key, Label: row.DbType}}
			buckets[key] = bucket
		}
		bucket.summary.Instances++

		if row.LastCollectStatus == "success" {
			bucket.summary.Healthy++
			payload.Collect.OK++
		}
		switch {
		case row.LastCollectAt == nil:
			payload.Collect.StaleOrNever++
		case row.LastCollectStatus == "failed":
			payload.Collect.Failed++
			bucket.summary.Failed++
		case row.CollectQuality == QualityUnsupported:
			payload.Collect.Unsupported++
		case row.CollectQuality == "partial":
			payload.Collect.Partial++
		case row.ConsecutiveFailures >= 3:
			payload.Collect.StaleOrNever++
		}
		if row.LastCollectAt != nil {
			collected++
			if row.LastCollectMs > 0 {
				totalMs += row.LastCollectMs
				if row.LastCollectMs > payload.Runner.SlowestCollectMs {
					payload.Runner.SlowestCollectMs = row.LastCollectMs
				}
			}
			stamp := time.Time(*row.LastCollectAt)
			if stamp.After(bucket.latest) {
				bucket.latest = stamp
			}
			if stamp.After(newest) {
				newest = stamp
			}
		}
		if row.LastCollectStatus == "failed" || row.ConsecutiveFailures > 0 {
			payload.Failures = append(payload.Failures, MonitorStatusFailure{
				InstanceID:    row.InstanceID,
				Target:        fmt.Sprintf("%s:%d", row.Hostname, row.Port),
				DbType:        row.DbType,
				Message:       firstNonEmpty(row.LastCollectMessage, "采集失败"),
				Failures:      row.ConsecutiveFailures,
				LastCollectAt: formatLocalTimePtr(row.LastCollectAt),
			})
		}
	}
	// 失败最多的排最前，方便一眼看到"最久没采到的那个"
	sort.Slice(payload.Failures, func(i, j int) bool {
		if payload.Failures[i].Failures != payload.Failures[j].Failures {
			return payload.Failures[i].Failures > payload.Failures[j].Failures
		}
		return payload.Failures[i].InstanceID < payload.Failures[j].InstanceID
	})
	if len(payload.Failures) > 30 {
		payload.Failures = payload.Failures[:30]
	}
	if collected > 0 {
		payload.Runner.AverageCollectMs = totalMs / collected
	}
	if !newest.IsZero() {
		payload.Runner.LatestCollectAt = newest.Format(time.DateTime)
	}

	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := buckets[key]
		if !bucket.latest.IsZero() {
			bucket.summary.LatestAt = bucket.latest.Format(time.DateTime)
		}
		payload.Engines = append(payload.Engines, bucket.summary)
	}
	payload.Collect.Notes = buildCollectNotes(payload.Collect, promStatus)
	if len(payload.Collect.Notes) == 0 {
		payload.Collect.Notes = append(payload.Collect.Notes, "全部实例采集正常")
	}
	payload.Collect.FreshSeconds = freshestSeconds(rows)
	return payload, nil
}

func formatLocalTimePtr(value *commonModels.LocalTime) string {
	if value == nil {
		return ""
	}
	return time.Time(*value).Format(time.DateTime)
}

func freshestSeconds(rows []monitorInstanceRow) int {
	freshest := -1
	for _, row := range rows {
		if row.LastCollectAt == nil {
			continue
		}
		seconds := int(time.Since(time.Time(*row.LastCollectAt)).Seconds())
		if freshest < 0 || seconds < freshest {
			freshest = seconds
		}
	}
	if freshest < 0 {
		return 0
	}
	return freshest
}

// ---------------------------------------------------------------------------
// 立即采集
// ---------------------------------------------------------------------------

type CollectInstanceNowData struct {
	InstanceID    string             `json:"instance_id"`
	Target        string             `json:"target"`
	Quality       string             `json:"quality"`
	Status        string             `json:"status"`
	StatusMessage string             `json:"status_message"`
	ElapsedMs     int                `json:"elapsed_ms"`
	Metrics       []MetricSampleView `json:"metrics"`
	Alerts        []AlertRecord      `json:"alerts"`
	CollectedAt   string             `json:"collected_at"`
}

// ---------------------------------------------------------------------------
// 立即采集的限流
//
// 后台调度天然受「槽位 + 周期」约束，但"立即采集"是点一次跑一次，没有任何节流：
// 连点 20 次就是对目标库连发 20 轮采集 SQL，每轮还要强制刷新一次画像（再多几条连接）。
// 偏偏这个按钮的使用场景就是"实例已经吃不消了、DBA 急着看现场"—— 这时候叠加压力最要命。
//
// 两道闸：
//  1. 单实例冷却：同一实例 collectNowCooldown 内只认第一次，重复点击直接拒绝并说明还要等几秒；
//  2. 全局并发：同时最多 collectNowMaxConcurrent 个立即采集，其余排队，
//     排队期间若客户端断开（ctx 取消）立即退出，不会留下空等的 goroutine。
//
// 冷却不区分成功失败：失败往往正是实例已经扛不住的信号，此时放开重试只会更糟。
// ---------------------------------------------------------------------------

const (
	collectNowCooldown      = 10 * time.Second
	collectNowMaxConcurrent = 4
)

// collectNowLastAt instanceID -> 上次立即采集时刻。
// 键的数量上界就是实例总数（几百），不存在无限增长，无需额外清理。
var collectNowLastAt sync.Map

// collectNowSem 全局并发闸。
var collectNowSem = make(chan struct{}, collectNowMaxConcurrent)

// reserveCollectNowSlot 抢占一次立即采集的资格。返回空串表示通过，否则是可直接展示给用户的拒绝原因。
func reserveCollectNowSlot(ctx context.Context, instanceID string) (release func(), reason string) {
	if waiting := collectNowCooldownRemaining(instanceID); waiting > 0 {
		return nil, fmt.Sprintf("该实例刚刚采集过，请 %d 秒后重试", int(waiting.Seconds())+1)
	}
	select {
	case collectNowSem <- struct{}{}:
	case <-ctx.Done():
		return nil, "采集已取消"
	}
	// 冷却在这里落时间戳：抢占到槽位才算真正要跑，
	// 被限流挡掉的点击不该把冷却窗口往后推。
	collectNowLastAt.Store(instanceID, time.Now())
	return func() { <-collectNowSem }, ""
}

// collectNowCooldownRemaining 返回该实例还需要等待多久（0 表示可以立即采集）。
func collectNowCooldownRemaining(instanceID string) time.Duration {
	value, ok := collectNowLastAt.Load(instanceID)
	if !ok {
		return 0
	}
	last, ok := value.(time.Time)
	if !ok {
		return 0
	}
	waiting := collectNowCooldown - time.Since(last)
	if waiting < 0 {
		return 0
	}
	return waiting
}

type CollectInstanceNowService struct {
	InstanceID string
	C          *gin.Context
	Username   string
}

// Run 对单个实例做一次同步采集并落库。
//
// 与后台采集共用 collectAndPersist / persistSamples ——
// 保证"手动点一次"和"后台跑一轮"产出完全一致的结果，不会出现两套会各自漂移的口径。
func (s *CollectInstanceNowService) Run() (*CollectInstanceNowData, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("实例ID不能为空")
	}
	// 立即采集会对目标实例真实执行采集语句（SHOW STATUS / 系统表查询等），
	// 属于对实例的访问：必须持有该实例的 dml 授权，不能只看 RBAC 功能权限。
	// 后台定时采集是系统行为（无用户上下文），不受此约束。
	if err := dasServices.CheckDataAccess(dasServices.DataAccessRequest{
		Username:   strings.TrimSpace(s.Username),
		InstanceID: instanceID,
		Action:     dasServices.DataActionDML,
	}); err != nil {
		return nil, err
	}
	rows, err := listMonitorInstances(monitorListFilter{InstanceID: instanceID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// 查不到时给出精确原因，而不是笼统的"不存在"
		return nil, describeCollectUnavailable(instanceID)
	}
	row := rows[0]

	promClient := newPrometheusClient()
	ctx := context.Background()
	if s.C != nil && s.C.Request != nil {
		ctx = s.C.Request.Context()
	}

	// 限流闸：放在授权与取行之后、真正建连之前 —— 被挡掉的请求一次连接都不会建。
	release, reason := reserveCollectNowSlot(ctx, instanceID)
	if reason != "" {
		return nil, fmt.Errorf("%s", reason)
	}
	defer release()

	promStatus := probePrometheus(ctx, promClient)

	started := time.Now()
	keys, _, _ := resolveInstanceMetricKeys(row)
	collectCtx, cancel := context.WithTimeout(context.Background(), instanceTimeoutSeconds(row)+3*time.Second)
	defer cancel()
	// 资源层独立预算：手动采集时同样不能让 16 条 PromQL 去抢引擎段剩下的时间。
	resourceCtx, resourceCancel := newResourceCollectCtx(ctx)
	defer resourceCancel()
	samples := collectInstanceSamples(collectCtx, resourceCtx, row, promStatus, promClient, keys)
	elapsedMs := int(time.Since(started).Milliseconds())
	// 手动采集的落库时刻同样对齐到槽位起点：与后台采集同一个网格，
	// 否则手采的点会漂进别的聚合桶，趋势图上多出一个错位点。
	nowTs := time.Now()
	persistSamples(row, samples, elapsedMs, nil, collectSlotTime(nowTs, row.InstanceID, effectiveCollectInterval(row)))
	// 手动"立即采集"强制刷新一次画像：用户点了就该看到最新版本 / 运行时长 / 规格，
	// 不能因为节流还差 50 分钟就毫无变化。
	refreshInstanceProfileFacts(ctx, row, promStatus, promClient, true)

	// 采完立刻评估一次该实例的告警，让"点一次就能看到告警"成立。
	// 其余实例的待处理告警不受影响：evaluateInstanceAlerts 只处理传入的这一个实例。
	// 规则集取实例自己的告警模板（挂载 → 失效回落 → 类型默认兜底），与定时评估同一口径；
	// 若用全局规则集评估，阈值可能不属于该实例挂载的模板，会产生口径不一致的告警。
	if tmpl, _, note := resolveAlertTemplateForInstance(row.DbType, row.AlertTemplateID); tmpl != nil {
		evaluateInstanceAlerts(row, latestRowsFromSamples(row, samples), loadAlertTemplateRules(tmpl.ID))
	} else {
		global.App.Log.Warnf("[monitor] 实例 %s 手动采集后跳过告警评估：%s", row.InstanceID, note)
	}
	latestRows := loadLatestMap([]string{instanceID})
	card := buildCardFromStore(row, latestRows[instanceID], loadActiveEventMap([]monitorInstanceRow{row})[instanceID])

	return &CollectInstanceNowData{
		InstanceID:    card.InstanceID,
		Target:        card.Target,
		Quality:       card.Quality,
		Status:        card.Status,
		StatusMessage: card.StatusMessage,
		ElapsedMs:     elapsedMs,
		Metrics:       card.Metrics,
		Alerts:        card.Alerts,
		CollectedAt:   card.CollectedAt,
	}, nil
}

// describeCollectUnavailable 为"采集不了"给出可执行的原因。
func describeCollectUnavailable(instanceID string) error {
	var probe struct {
		DbType  string
		Enabled *bool
	}
	err := global.App.DB.Table("insight_instances i").
		Select("i.db_type, mt.enabled").
		Joins("left join insight_monitor_target_configs mt on mt.instance_id = i.instance_id").
		Where("i.instance_id = ?", instanceID).
		Take(&probe).Error
	if err != nil {
		return fmt.Errorf("实例不存在：%s", instanceID)
	}
	if !engineImplemented(probe.DbType) {
		return fmt.Errorf("实例类型 %s 暂未实现监控采集", probe.DbType)
	}
	if probe.Enabled != nil && !*probe.Enabled {
		return fmt.Errorf("该实例的监控已在配置中停用，请先在监控对象页启用")
	}
	return fmt.Errorf("实例 %s 不在监控范围内，请确认监控配置是否已生成", instanceID)
}

// ---------------------------------------------------------------------------
// 指标清单（原「指标字典」已下线）
//
// 这里仍叫 Catalog，但它已不是一份可运维改写的字典表：
// 指标定义唯一来源是代码内的 builtinMetricCatalog，本接口只做"按引擎过滤后映射给前端"，
// 供指标模板勾选弹窗使用。原先的「指标字典」管理页与 PUT 写入接口均已移除。
// ---------------------------------------------------------------------------

type MetricCatalogData struct {
	GeneratedAt string              `json:"generated_at"`
	Categories  []FilterOption      `json:"categories"`
	Engines     []EngineCapability  `json:"engines"`
	Metrics     []MetricCatalogItem `json:"metrics"`
}

type GetMetricCatalogService struct {
	Category string
	Engine   string
	// Layout 为 true 时按「监控明细展示目录」输出指标清单（多值指标逐行展开、
	// 保持明细原始顺序、无采集源行以 available=false 列出）。仅指标模板勾选弹窗使用，
	// 指标字典管理页不受影响（不传该参数走原字典口径）。
	Layout bool
	C      *gin.Context
}

func (s *GetMetricCatalogService) Run() (*MetricCatalogData, error) {
	data := &MetricCatalogData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Categories: []FilterOption{
			{Value: CategoryAvailability, Label: categoryLabel(CategoryAvailability)},
			{Value: CategoryCapacity, Label: categoryLabel(CategoryCapacity)},
			{Value: CategoryPerformance, Label: categoryLabel(CategoryPerformance)},
			{Value: CategoryRisk, Label: categoryLabel(CategoryRisk)},
		},
		Engines: EngineCapabilityList(),
	}
	// 明细口径优先：仅收录了展示目录的类型（MySQL / PostgreSQL / TiDB / Doris）支持
	if items := displayLayoutCatalogItems(s.Engine); len(items) > 0 {
		data.Metrics = items
		return data, nil
	}
	items, err := LoadMetricCatalogItems(s.Engine)
	if err != nil {
		return nil, err
	}
	data.Metrics = items
	if category := strings.TrimSpace(s.Category); category != "" {
		filtered := make([]MetricCatalogItem, 0, len(items))
		for _, item := range items {
			if item.Category == category {
				filtered = append(filtered, item)
			}
		}
		data.Metrics = filtered
	}
	return data, nil
}

// LoadMetricCatalogItems 返回指定引擎适用的内置指标清单。
//
// 改造说明：原先这里读 insight_monitor_metric_catalog 表（库空才回退代码内置全集），
// 字典表移除后只有内置全集这一条路径，不再有数据库来源，也不再有"库与代码不一致"的可能。
func LoadMetricCatalogItems(engine string) ([]MetricCatalogItem, error) {
	engineKey := resolveEngine(engine)
	result := make([]MetricCatalogItem, 0, len(builtinMetricCatalog))
	for _, item := range builtinMetricCatalog {
		if engineKey != "" && !engineInScope(engineKey, item.EngineScope) {
			continue
		}
		result = append(result, metricCatalogItemFromDescriptor(item))
	}
	return result, nil
}

func metricCatalogItemFromDescriptor(item MetricDescriptor) MetricCatalogItem {
	return MetricCatalogItem{
		MetricKey:     item.MetricKey,
		DisplayName:   item.DisplayName,
		Category:      item.Category,
		CategoryLabel: categoryLabel(item.Category),
		Scope:         item.Scope,
		EngineScope:   item.EngineScope,
		Engines:       engineLabelsOf(item.EngineScope),
		ValueType:     item.ValueType,
		Unit:          item.Unit,
		SubDims:       item.SubDims,
		SourceType:    item.SourceType,
		SourceRef:     item.SourceRef,
		Agg:           item.Agg,
		DisplayHint:   item.DisplayHint,
		Advice:        item.Advice,
		// 内置指标都有采集源，均可作为模板采集项
		Enabled: true,
	}
}

// engineLabelsOf 把 engine_scope 白名单转成人看的标签；空表示"全部引擎"。
func engineLabelsOf(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		labels := make([]string, 0, len(engineCapabilities))
		for _, item := range engineCapabilities {
			labels = append(labels, item.Label)
		}
		return labels
	}
	labels := make([]string, 0, 4)
	for _, item := range strings.Split(scope, ",") {
		key := resolveEngine(item)
		if capability, ok := engineCapabilityIndex[key]; ok {
			labels = append(labels, capability.Label)
			continue
		}
		labels = append(labels, strings.TrimSpace(item))
	}
	return labels
}
