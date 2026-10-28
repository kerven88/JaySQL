package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
)

// ============================================================================
// 监控大屏数据服务
//
// 关键变化：页面从"触发采集"变为"读库"。
// 采集由后台调度器（monitor_collect.go）负责，页面只查 insight_monitor_metric_latest
// 与 insight_monitor_alert_events。例外是"从未采集过"的实例会补采一次，
// 保证新接入的实例首次打开页面就能看到数据（并受 OnDemandCollectLimit 限制，
// 防止页面退化成采集器）。
// ============================================================================

type GetDashboardService struct {
	*forms.GetDashboardForm
	C *gin.Context
}

// OnDemandCollectLimit 单次请求最多为多少个"从未采集过"的实例做即时补采。
const OnDemandCollectLimit = 20

type DashboardData struct {
	GeneratedAt string                `json:"generated_at"`
	Summary     DashboardSummary      `json:"summary"`
	Prometheus  PrometheusStatus      `json:"prometheus"`
	Filters     DashboardFilters      `json:"filters"`
	Instances   []InstanceMonitorCard `json:"instances"`
	Alerts      []AlertRecord         `json:"alerts"`
	Collect     CollectHealth         `json:"collect"`
	// Screen 大屏聚合块：状态四分类、时段内新增告警、区间聚合（QPS 均值/连接峰值）、
	// 已认领与已恢复告警。算不出的字段为 null，前端显示「未获取」，绝不填模拟数。
	Screen *ScreenData `json:"screen"`
}

type DashboardSummary struct {
	TotalInstances    int `json:"total_instances"`
	HealthyInstances  int `json:"healthy_instances"`
	DegradedInstances int `json:"degraded_instances"`
	ActiveAlerts      int `json:"active_alerts"`
	PrometheusCovered int `json:"prometheus_covered"`
	DirectCollected   int `json:"direct_collected"`
}

// CollectHealth 是"采集健康度"提示条的数据源。
// 它的存在是为了防止把"没采到"当成"健康"—— 这是当前最大的信任风险。
type CollectHealth struct {
	OK           int      `json:"ok"`
	Partial      int      `json:"partial"`
	Failed       int      `json:"failed"`
	StaleOrNever int      `json:"stale_or_never"`
	Unsupported  int      `json:"unsupported"`
	FreshSeconds int      `json:"fresh_seconds"`
	Notes        []string `json:"notes"`
}

type PrometheusStatus struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	BaseURL    string `json:"base_url"`
	Message    string `json:"message"`
}

type DashboardFilters struct {
	Environments []FilterOption     `json:"environments"`
	DbTypes      []FilterOption     `json:"db_types"`
	Businesses   []FilterOption     `json:"businesses"`
	Engines      []EngineCapability `json:"engines"`
}

type FilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type InstanceMonitorCard struct {
	ID                  uint64             `json:"id"`
	InstanceID          string             `json:"instance_id"`
	Hostname            string             `json:"hostname"`
	Port                int                `json:"port"`
	Target              string             `json:"target"`
	DbType              string             `json:"db_type"`
	EnvironmentName     string             `json:"environment_name"`
	Remark              string             `json:"remark"`
	Business            string             `json:"business"`
	GroupName           string             `json:"group_name"`
	DeployMode          string             `json:"deploy_mode"`
	Status              string             `json:"status"`
	StatusMessage       string             `json:"status_message"`
	Quality             string             `json:"quality"`
	QualityMessage      string             `json:"quality_message"`
	CollectStatus       string             `json:"collect_status"`
	ResourceSource      string             `json:"resource_source"`
	EngineSource        string             `json:"engine_source"`
	Metrics             []MetricSampleView `json:"metrics"`
	ResourceMetrics     ResourceMetrics    `json:"resource_metrics"`
	EngineMetrics       EngineMetrics      `json:"engine_metrics"`
	Alerts              []AlertRecord      `json:"alerts"`
	CollectedAt         string             `json:"collected_at"`
	LastSuccessAt       string             `json:"last_success_at"`
	FreshnessSeconds    int                `json:"freshness_seconds"`
	ConsecutiveFailures int                `json:"consecutive_failures"`
	// ScreenClass 大屏四分类：healthy / risk / fault / uncollected。
	// 口径来自健康分级配置驱动的评估引擎 classifyScreenCard ——
	// 大屏环形图、明细表状态列、分组矩阵共用这一份。
	ScreenClass string `json:"screen_class"`
	// HealthGrade 分级评估明细：最终等级（P0~P3）与命中的结果项清单。
	// 与 ScreenClass 同源（screen_class == health_grade.health_state），
	// 前端展示"为什么是这个状态"时直接用它，不要自行推断。
	HealthGrade *HealthGradeResult `json:"health_grade,omitempty"`
	// HealthScore 健康分（0-100，由分级评估结果派生，见 HealthScoreOf）。
	// uncollected / 无评估结果时为 nil —— 数据不可信不给分，前端显示「—」。
	HealthScore *int `json:"health_score,omitempty"`
}

type ResourceMetrics struct {
	CPU        MetricValue `json:"cpu"`
	Memory     MetricValue `json:"memory"`
	Disk       MetricValue `json:"disk"`
	DiskUsed   MetricValue `json:"disk_used"`
	IO         MetricValue `json:"io"`
	NetworkIn  MetricValue `json:"network_in"`
	NetworkOut MetricValue `json:"network_out"`
	// 主机层扩展（node_exporter 口径）
	CPULoad1       MetricValue `json:"cpu_load1"`
	CtxSwitches    MetricValue `json:"ctx_switches"`
	SwapUsedPct    MetricValue `json:"swap_used_pct"`
	DiskIOPS       MetricValue `json:"disk_iops"`
	DiskThroughput MetricValue `json:"disk_throughput"`
	DiskAwaitMs    MetricValue `json:"disk_await_ms"`
	NetErrors      MetricValue `json:"net_errors"`
	TCPEstablished MetricValue `json:"tcp_established"`
	FDUsedPct      MetricValue `json:"fd_used_pct"`
	TimeOffsetS    MetricValue `json:"time_offset_s"`

	// Errs 记录「查询失败」的资源项，键为指标键（如 res.cpu），值为失败原因。
	//
	// 必须和 MetricValue.Available == false 区分开：
	//   - 有 Errs    → 查询出错（连接失败 / 超时），要标 error、要让人看见；
	//   - 无 Errs 且不可用 → Prometheus 确实没有这条时间序列，是 noData。
	//
	// 混为一谈的后果是：预算超时时 16 项资源指标集体显示"未返回该主机的时间序列"，
	// 把"我们这边超时了"误导成"exporter 没数据"，排查方向整个反掉。
	// 不参与序列化（json:"-"），仅用于采集链路内部传递。
	Errs map[string]string `json:"-"`
}

type EngineMetrics struct {
	QPS               MetricValue `json:"qps"`
	TPS               MetricValue `json:"tps"`
	ReadRows          MetricValue `json:"read_rows"`
	WriteRows         MetricValue `json:"write_rows"`
	ReadBytes         MetricValue `json:"read_bytes"`
	WriteBytes        MetricValue `json:"write_bytes"`
	CacheHitRatio     MetricValue `json:"cache_hit_ratio"`
	Locks             MetricValue `json:"locks"`
	Connections       MetricValue `json:"connections"`
	ActiveConnections MetricValue `json:"active_connections"`
	MaxConnections    MetricValue `json:"max_connections"`
}

// MetricValue 增加 quality / reason 两个字段。
// 前端硬规则：available 为 false 时一律显示 "—" + reason，绝不显示 0 ——
// 这是消灭"PG QPS 假 0""CPU 假 0"这类错觉的落点。
type MetricValue struct {
	Value     float64 `json:"-"`
	Unit      string  `json:"unit"`
	Available bool    `json:"available"`
	Quality   string  `json:"quality"`
	Reason    string  `json:"reason"`
}

// MarshalJSON 让"不可用"在协议层就表现为 value: null，与库里的 NULL 语义对齐。
//
// 直接序列化会输出 value:0，而 0 是一个完全可信的合法读数 —— 任何只读 value、
// 忘了看 available 的消费方（第三方脚本、告警插件、新写的页面）都会把
// "没采到"当成"值就是 0"，正是本套改造要根治的错觉。这里堵死这个口子。
func (m MetricValue) MarshalJSON() ([]byte, error) {
	type metricValueAlias struct {
		Value     *float64 `json:"value"`
		Unit      string   `json:"unit"`
		Available bool     `json:"available"`
		Quality   string   `json:"quality"`
		Reason    string   `json:"reason"`
	}
	out := metricValueAlias{
		Unit:      m.Unit,
		Available: m.Available,
		Quality:   m.Quality,
		Reason:    m.Reason,
	}
	if m.Available {
		v := m.Value
		out.Value = &v
	}
	return json.Marshal(out)
}

// UnmarshalJSON 与 MarshalJSON 对称，保证内部模块回读响应体时不会丢结构。
func (m *MetricValue) UnmarshalJSON(data []byte) error {
	type metricValueAlias struct {
		Value     *float64 `json:"value"`
		Unit      string   `json:"unit"`
		Available bool     `json:"available"`
		Quality   string   `json:"quality"`
		Reason    string   `json:"reason"`
	}
	var in metricValueAlias
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	if in.Value != nil {
		m.Value = *in.Value
	} else {
		m.Value = 0
	}
	m.Unit = in.Unit
	m.Available = in.Available
	m.Quality = in.Quality
	m.Reason = in.Reason
	return nil
}

type AlertRecord struct {
	Name string `json:"name"`
	// Instance 数据源地址（hostname:port）。卡片要回答"这条告警来自哪个库"，
	// 而事件表只有 instance_id —— 名称/类型/环境必须在这里补齐后随告警下发，
	// 否则前端要么显示空，要么得自己再拉一次实例列表做二次关联。
	Instance        string  `json:"instance"`
	InstanceID      string  `json:"instance_id"`
	InstanceName    string  `json:"instance_name"`
	InstanceTarget  string  `json:"instance_target"`
	DbType          string  `json:"db_type"`
	EnvironmentName string  `json:"environment_name"`
	Level           string  `json:"level"`
	Severity        string  `json:"severity"`
	Status          string  `json:"status"`
	// StatusKey 是原始状态码（active/acknowledged/pending/recovered）。
	// Status 是中文展示词，会随文案调整变化；前端的分桶/筛选一律按 StatusKey
	// 判断，从根上消除"按中文比较"漏改导致的静默错分。
	StatusKey       string  `json:"status_key"`
	TriggeredAt     string  `json:"triggered_at"`
	RecoveredAt     string  `json:"recovered_at"`
	FirstSeenAt     string  `json:"first_seen_at"`
	MetricKey       string  `json:"metric_key"`
	Message         string  `json:"message"`
	Advice          string  `json:"advice"`
	Threshold       float64 `json:"threshold"`
	ObservedValue   float64 `json:"observed_value"`
	PeakValue       float64 `json:"peak_value"`
	Occurrences     int     `json:"occurrences"`
	DurationSec     int64   `json:"duration_sec"`
	Suppressed      bool    `json:"suppressed"`
	Ignored         bool    `json:"ignored"`
	AckedBy         string  `json:"acked_by"`
	// RowKey 供前端表格做稳定 row-key：同一实例同一指纹的告警只占一行。
	RowKey string `json:"row_key"`
}

// monitorInstanceRow 是实例 + 监控配置 + 采集运行态的联合视图
type monitorInstanceRow struct {
	ID         uint64
	InstanceID string
	Hostname   string
	Port       int
	User       string
	// Password 是**加密后的**实例口令，只在采集链路内部解密使用。
	//
	// 必须 json:"-"：这个结构体会被整体塞进若干响应（以及错误信息里的调试输出），
	// 一旦被序列化出去，密文口令就随着接口响应一起泄露 —— 密文泄露同样是泄露，
	// 拿到 secret_key 就能还原。显式禁掉序列化，让"忘了脱敏"变成编译期不可能。
	Password    string `json:"-"`
	DbType      string
	ExtraConfig []byte
	// InstanceRole 架构角色：master / slave / standalone / cascade，取自 insight_instances.instance_role。
	// 空串表示尚未探测出角色，所有依赖角色的判定必须按 fail-open 处理（见 monitor_role_scope.go）。
	InstanceRole          string
	EnvironmentName       string
	Remark                string
	Business              string
	GroupName             string
	Enabled               bool
	PrometheusTarget      string
	ResourceSourceSetting string
	EngineSourceSetting   string
	// MetricsTemplate 实例绑定的指标模板标识（对应 insight_monitor_metric_templates.name）
	MetricsTemplate string
	// AlertTemplateID 实例挂载的告警策略模板（对应 insight_monitor_target_configs.alert_template_id）；
	// NULL = 未挂载，告警评估时回落到该 db_type 的默认告警模板。
	AlertTemplateID       *uint64
	RetentionHours        int
	ScrapeIntervalSeconds int
	TimeoutSeconds        int
	RetryCount            int
	DeployMode            string
	LastCollectAt         *commonModels.LocalTime
	LastCollectStatus     string
	LastCollectMessage    string
	LastCollectMs         int
	LastSuccessAt         *commonModels.LocalTime
	ConsecutiveFailures   int
	CollectQuality        string
}

func (s *GetDashboardService) Run() (*DashboardData, error) {
	filter := monitorListFilter{}
	if s.GetDashboardForm != nil {
		filter = monitorListFilter{
			Search:      s.Search,
			Environment: s.Environment,
			DbType:      s.DbType,
			Business:    s.Business,
			GroupName:   s.GroupName,
			InstanceID:  s.InstanceID,
		}
	}
	// 大屏统计窗口：自定义起止 > range_minutes > range_hours（兼容）> 默认 30 分钟。
	// 解析失败直接报错（400），不猜区间 —— 用错的时间范围算出的数字比报错危险。
	window, err := resolveMonitorWindow(s.RangeMinutes, s.RangeHours, s.StartTime, s.EndTime)
	if err != nil {
		return nil, err
	}
	requestCtx := context.Background()
	if s.C != nil && s.C.Request != nil {
		requestCtx = s.C.Request.Context()
	}
	rows, cards, filters, promStatus, err := loadMonitorCards(requestCtx, filter)
	if err != nil {
		return nil, err
	}

	// 大屏四分类口径统一在这里落：环形图、明细表状态列、分组矩阵都用它。
	// 健康分驱动判定（monitor_health_state.go）：先算分再定级，状态与分数天然一致。
	for i := range cards {
		state := evaluateInstanceState(cards[i])
		cards[i].ScreenClass = state.Class
		cards[i].HealthScore = state.Score
	}

	payload := &DashboardData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Prometheus:  promStatus,
		Filters:     filters,
		Instances:   cards,
		Alerts:      make([]AlertRecord, 0),
	}
	health := CollectHealth{Notes: make([]string, 0)}
	freshest := -1
	for _, item := range cards {
		payload.Summary.TotalInstances++
		switch item.Status {
		case "healthy":
			payload.Summary.HealthyInstances++
		default:
			payload.Summary.DegradedInstances++
		}
		if item.ResourceSource == "prometheus" {
			payload.Summary.PrometheusCovered++
		}
		if item.EngineSource == "direct" {
			payload.Summary.DirectCollected++
		}
		payload.Alerts = append(payload.Alerts, item.Alerts...)

		switch {
		case item.CollectStatus == "never" || (item.FreshnessSeconds > item.expectedFreshLimit()):
			health.StaleOrNever++
		case item.CollectStatus == "failed":
			health.Failed++
		case item.Quality == QualityUnsupported:
			health.Unsupported++
		case item.Quality == "partial":
			health.Partial++
		default:
			health.OK++
		}
		if item.FreshnessSeconds > 0 && (freshest < 0 || item.FreshnessSeconds < freshest) {
			freshest = item.FreshnessSeconds
		}
	}
	payload.Summary.ActiveAlerts = len(payload.Alerts)
	sortAlerts(payload.Alerts)
	if freshest >= 0 {
		health.FreshSeconds = freshest
	}
	health.Notes = buildCollectNotes(health, promStatus)
	payload.Collect = health

	// 大屏聚合块。历史区间算不出来时内部容忍（只 Warn），状态四分类永远可用 ——
	// 不能因为聚合查询失败让整块大屏 500。
	screen := buildScreenData(rows, cards, window)
	payload.Screen = &screen
	return payload, nil
}

func buildCollectNotes(health CollectHealth, promStatus PrometheusStatus) []string {
	notes := make([]string, 0, 3)
	if health.Failed > 0 {
		notes = append(notes, fmt.Sprintf("%d 个实例采集失败，其健康状态不可信", health.Failed))
	}
	if health.StaleOrNever > 0 {
		notes = append(notes, fmt.Sprintf("%d 个实例数据滞后或尚未采集", health.StaleOrNever))
	}
	if health.Unsupported > 0 {
		notes = append(notes, fmt.Sprintf("%d 个实例的引擎暂不支持指标采集，已在列表中标注", health.Unsupported))
	}
	if health.Partial > 0 {
		notes = append(notes, fmt.Sprintf("%d 个实例部分指标不适用（如 PG 未装 pg_stat_statements）", health.Partial))
	}
	if !promStatus.Configured {
		notes = append(notes, "未配置 Prometheus，CPU/内存/磁盘/IO/网络指标不可用（显示为 —）")
	} else if !promStatus.Available {
		notes = append(notes, "Prometheus 已配置但当前不可用，主机资源指标暂时为空")
	}
	return notes
}

// expectedFreshLimit 判断"数据是否已经太旧"：超过 2 倍采集周期即视为滞后。
// 阈值不再写死 —— 取自健康分级配置里 data_stale 规则的 freshness_seconds 条件，
// 保证"卡片判定"与"分级评估"用的是同一个值（见 DataStaleLimitSeconds）。
func (card InstanceMonitorCard) expectedFreshLimit() int {
	return DataStaleLimitSeconds()
}

// loadMonitorCards 读取实例清单 + 最新值 + 待处理告警，构建卡片。
// 这是大屏与明细共用的唯一数据入口，避免两处各写一套导致口径漂移。
func loadMonitorCards(ctx context.Context, filter monitorListFilter) ([]monitorInstanceRow, []InstanceMonitorCard, DashboardFilters, PrometheusStatus, error) {
	rows, err := listMonitorInstances(filter)
	if err != nil {
		return nil, nil, DashboardFilters{}, PrometheusStatus{}, err
	}
	promClient := newPrometheusClient()
	promStatus := probePrometheus(ctx, promClient)

	filters := buildFilterOptions(rows)
	filters.Engines = EngineCapabilityList()

	if len(rows) == 0 {
		return rows, []InstanceMonitorCard{}, filters, promStatus, nil
	}

	instanceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		instanceIDs = append(instanceIDs, row.InstanceID)
	}
	latestMap := loadLatestMap(instanceIDs)
	eventsMap := loadActiveEventMap(rows)

	// 从未采集过的实例：即时补采一次，保证新接入实例首次打开页面就有数据。
	// 数量受 OnDemandCollectLimit 限制，避免页面退化成采集器。
	pending := 0
	for _, row := range rows {
		if row.LastCollectAt == nil && len(latestMap[row.InstanceID]) == 0 {
			pending++
		}
	}
	if pending > 0 && pending <= OnDemandCollectLimit {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 6)
		var mu sync.Mutex
		for _, row := range rows {
			if row.LastCollectAt != nil || len(latestMap[row.InstanceID]) > 0 {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(item monitorInstanceRow) {
				defer wg.Done()
				defer func() { <-sem }()
				keys, _, _ := resolveInstanceMetricKeys(item)
				// 首屏补采同样两段预算：引擎段吃实例超时，资源段独立计时。
				engineCtx, engineCancel := context.WithTimeout(ctx, instanceTimeoutSeconds(item)+3*time.Second)
				defer engineCancel()
				resourceCtx, resourceCancel := newResourceCollectCtx(ctx)
				defer resourceCancel()
				samples := collectInstanceSamples(engineCtx, resourceCtx, item, promStatus, promClient, keys)
				mu.Lock()
				latestMap[item.InstanceID] = latestRowsFromSamples(item, samples)
				mu.Unlock()
			}(row)
		}
		wg.Wait()
	}

	cards := make([]InstanceMonitorCard, 0, len(rows))
	for _, row := range rows {
		cards = append(cards, buildCardFromStore(row, latestMap[row.InstanceID], eventsMap[row.InstanceID]))
	}
	return rows, cards, filters, promStatus, nil
}

// probePrometheus 探测 Prometheus 可用性（供页面提示与资源采集门控使用）
func probePrometheus(ctx context.Context, client *prometheusClient) PrometheusStatus {
	status := PrometheusStatus{
		Configured: client.Configured(),
		BaseURL:    client.baseURL,
		Message:    client.statusMessage,
	}
	if !status.Configured {
		status.Message = "未配置 Prometheus，基础资源指标将显示为 —"
		return status
	}
	status.Available = true
	if err := client.Ping(ctx); err != nil {
		status.Available = false
		status.Message = err.Error()
	} else if status.Message == "" {
		status.Message = "Prometheus 已接入"
	}
	return status
}

func buildFilterOptions(rows []monitorInstanceRow) DashboardFilters {
	envSet := map[string]struct{}{}
	typeSet := map[string]struct{}{}
	bizSet := map[string]struct{}{}
	envs := make([]FilterOption, 0)
	types := make([]FilterOption, 0)
	bizes := make([]FilterOption, 0)

	for _, row := range rows {
		if row.EnvironmentName != "" {
			if _, ok := envSet[row.EnvironmentName]; !ok {
				envSet[row.EnvironmentName] = struct{}{}
				envs = append(envs, FilterOption{Value: row.EnvironmentName, Label: row.EnvironmentName})
			}
		}
		if row.DbType != "" {
			if _, ok := typeSet[row.DbType]; !ok {
				typeSet[row.DbType] = struct{}{}
				types = append(types, FilterOption{Value: row.DbType, Label: row.DbType})
			}
		}
		if row.Business != "" {
			if _, ok := bizSet[row.Business]; !ok {
				bizSet[row.Business] = struct{}{}
				bizes = append(bizes, FilterOption{Value: row.Business, Label: row.Business})
			}
		}
	}

	sort.Slice(envs, func(i, j int) bool { return envs[i].Label < envs[j].Label })
	sort.Slice(types, func(i, j int) bool { return types[i].Label < types[j].Label })
	sort.Slice(bizes, func(i, j int) bool { return bizes[i].Label < bizes[j].Label })
	return DashboardFilters{Environments: envs, DbTypes: types, Businesses: bizes}
}

func sortAlerts(alerts []AlertRecord) {
	weight := map[string]int{"高": 3, "中": 2, "低": 1}
	sort.Slice(alerts, func(i, j int) bool {
		if weight[alerts[i].Level] != weight[alerts[j].Level] {
			return weight[alerts[i].Level] > weight[alerts[j].Level]
		}
		if alerts[i].DurationSec != alerts[j].DurationSec {
			return alerts[i].DurationSec > alerts[j].DurationSec
		}
		return alerts[i].TriggeredAt > alerts[j].TriggeredAt
	})
}

func sanitizeMetricValue(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

// ---------------------------------------------------------------------------
// rate 缓存：所有 *_total 型计数器都靠进程内差值求速率。
// 注意这是进程内存，服务重启即丢，首次采集回退到 当前值 / Uptime。
// ---------------------------------------------------------------------------

type counterSnapshot struct {
	At       time.Time
	Counters map[string]float64
}

const (
	// rateCacheMaxIdle 超过这么久没被更新的记录视为过期。
	//
	// 取值依据：实例采集周期最长 600 秒（失败退避上限），留 12 倍余量，
	// 保证"周期被拉到最长"的正常实例绝不会被误删。
	rateCacheMaxIdle = 2 * time.Hour
	// rateCacheSweepEvery 清理扫描的最小间隔。扫描是 O(实例数)，
	// 每次 compute 都扫一遍没必要，10 分钟一次足够。
	rateCacheSweepEvery = 10 * time.Minute
)

type rateCacheStore struct {
	mu      sync.Mutex
	records map[string]counterSnapshot
	// lastSweep 上次清理时刻，用于把清理摊薄到每 10 分钟一次。
	lastSweep time.Time
}

var directRateCache = &rateCacheStore{
	records: map[string]counterSnapshot{},
}

// sweepLocked 淘汰长期未更新的记录（必须在持锁下调用）。
//
// 不加淘汰的后果：records 只增不减，实例下线 / 从监控里摘掉后它的快照永远留在内存里，
// 等同一个 instance_id 重新接入时，会用几周前的旧计数器做差分 ——
// 分子是一个巨大负值，被 `current >= prev` 挡掉后回落到 current/Uptime，
// 表现是"刚接进来的实例速率低得离谱，过一轮才正常"。
func (c *rateCacheStore) sweepLocked(now time.Time) {
	if now.Sub(c.lastSweep) < rateCacheSweepEvery {
		return
	}
	c.lastSweep = now
	for key, snap := range c.records {
		if now.Sub(snap.At) > rateCacheMaxIdle {
			delete(c.records, key)
		}
	}
}

func (c *rateCacheStore) compute(key string, counters map[string]float64, fallbackSeconds float64) map[string]float64 {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweepLocked(now)

	result := map[string]float64{}
	prev, ok := c.records[key]
	for metric, current := range counters {
		if ok && current >= prev.Counters[metric] {
			elapsed := now.Sub(prev.At).Seconds()
			if elapsed > 0 {
				result[metric] = (current - prev.Counters[metric]) / elapsed
				continue
			}
		}
		if fallbackSeconds > 0 {
			result[metric] = current / fallbackSeconds
		} else {
			result[metric] = 0
		}
	}
	c.records[key] = counterSnapshot{At: now, Counters: counters}
	return result
}

// ---------------------------------------------------------------------------
// Prometheus 客户端（资源层唯一来源）
// ---------------------------------------------------------------------------

type prometheusClient struct {
	baseURL       string
	httpClient    *http.Client
	statusMessage string
}

func newPrometheusClient() *prometheusClient {
	baseURL := strings.TrimSpace(getEnvString("GOINSIGHT_PROMETHEUS_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(getEnvString("PROMETHEUS_URL"))
	}
	if baseURL == "" && global.App != nil && global.App.ConfigViper != nil {
		baseURL = strings.TrimSpace(global.App.ConfigViper.GetString("monitor.prometheus_url"))
	}

	timeout := 5 * time.Second
	if global.App != nil && global.App.ConfigViper != nil {
		if raw := global.App.ConfigViper.GetInt("monitor.prometheus_timeout_seconds"); raw > 0 {
			timeout = time.Duration(raw) * time.Second
		}
	}

	return &prometheusClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *prometheusClient) Configured() bool {
	return strings.TrimSpace(c.baseURL) != ""
}

func (c *prometheusClient) Ping(ctx context.Context) error {
	if !c.Configured() {
		return nil
	}
	_, _, err := c.query(ctx, "up")
	if err != nil {
		return fmt.Errorf("Prometheus 不可用：%s", err.Error())
	}
	return nil
}

// prometheusInstancePattern 生成可安全内嵌进 PromQL 字符串字面量的正则片段。
//
// 【不能用 regexp.QuoteMeta】它会把点号转义成 `\.`，而 PromQL 的字符串字面量
// 只认 `\\` `\"` `\n` `\t` 这类转义，遇到 `\.` 会直接报：
//
//	parse error: unknown escape sequence U+002E '.'
//
// 整个查询返回 400，6 项资源指标全部拿不到。
// （2026-09-15 在 centos01 接上真实 Prometheus 后实测到该错误。此前
//
//	monitor.prometheus_url 一直为空，这段代码从未真正发出过请求，缺陷被掩盖。）
//
// 这里改用字符类 `[.]`：语义完全等价，且不含反斜杠，不会触发字面量转义。
// 顺带说明一个长期误判 —— "实例必须用主机名注册、用 IP 会失效"并不是标签匹配问题，
// 真正原因是主机名不含点号，QuoteMeta 不产生 `\.`，侥幸绕过了这个缺陷。
func prometheusInstancePattern(host string) string {
	// 反斜杠会破坏 PromQL 字面量转义，先剔除；其余正则元字符不会出现在
	// 实例登记值里（字符集为 [A-Za-z0-9._:-]），故只需处理点号。
	cleaned := strings.ReplaceAll(host, `\`, "")
	return strings.ReplaceAll(cleaned, ".", "[.]")
}

// CollectHostResources 采集主机资源。
//
// 匹配规则：instance=~"^<实例登记地址>(?::[0-9]+)?$"。
// 实例登记 IP 或主机名均可，只要与 Prometheus 抓取目标的地址一致 ——
// target 写成 192.168.1.99:9100 时 instance 标签即该值，尾部端口由正则可选匹配。
// 但不要走 NAT 或别名地址：instance 标签与登记值不一致会静默匹配失败，资源列一直为空。
// promResourceKeyPrefix resourceSampleKeys 里主机层指标键的统一前缀（res.cpu / res.memory ...）。
// CollectHostResources 里的表达式键是去掉前缀的短名（cpu / memory ...），靠它还原成指标键。
const promResourceKeyPrefix = "res."

func (c *prometheusClient) CollectHostResources(ctx context.Context, hostname string) (ResourceMetrics, error) {
	result := ResourceMetrics{Errs: map[string]string{}}
	if !c.Configured() {
		return result, nil
	}
	// 记录单个资源项的失败原因；键用指标键（res.*），便于采集侧按 key 取用。
	recordErr := func(exprKey string, reason string) {
		result.Errs[promResourceKeyPrefix+exprKey] = reason
	}

	// 采样窗口由采集频率派生：max(4 × 周期, 60s)。
	// 模板里的 [5m] 只是占位符，查询前统一替换为真实窗口 —— 这样 PromQL 窗口、
	// Prometheus 抓取间隔、JaySQL 落库周期三者由同一个配置派生，不会各写各的。
	window := fmt.Sprintf("[%ds]", GetScrapeConfig().PromRateWindowSeconds)
	filter := fmt.Sprintf(`instance=~"^%s(?::[0-9]+)?$"`, prometheusInstancePattern(hostname))
	// 磁盘指标的文件系统口径（使用率与使用量共用，两者必须同集合，否则百分比与字节数会互相矛盾）：
	//   1. 排除 tmpfs/overlay/squashfs/ramfs/nsfs 等伪文件系统；
	//   2. 排除 /boot、/boot/efi 等启动分区 —— 它们容量极小（常见 200~500MB）且天然占用 50%+，
	//      一旦参与「取使用率最高分区」就会把真实数据分区盖掉：例如扩容根分区后 / 从 55% 降到 20%，
	//      而 /boot 仍是 54.88%，页面上的「磁盘使用率」看起来完全没变化（真实踩坑案例 2026-10-07）。
	fsFilter := fmt.Sprintf(`%s,fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs",mountpoint!~"/boot.*|/run.*|/sys.*|/dev.*"`, filter)
	expressions := map[string]string{
		"cpu":    fmt.Sprintf(`clamp_min(100 - (avg(irate(node_cpu_seconds_total{%s,mode="idle"}[5m])) * 100), 0)`, filter),
		"memory": fmt.Sprintf(`clamp_min((1 - (avg(node_memory_MemAvailable_bytes{%s}) / avg(node_memory_MemTotal_bytes{%s}))) * 100, 0)`, filter, filter),
		"disk":   fmt.Sprintf(`max(100 * (1 - (node_filesystem_avail_bytes{%s} / node_filesystem_size_bytes{%s})))`, fsFilter, fsFilter),
		// 磁盘使用量：与 disk 同源同过滤，只是把「比率」换成「绝对字节」。
		// 已用 = size - avail（排除 root 保留块，与使用率口径一致）；
		// max by (device) 给重复挂载的同一块盘去重，再 sum 得到主机真实磁盘已用总量。
		"disk_used":   fmt.Sprintf(`sum(max by (device) (node_filesystem_size_bytes{%s})) - sum(max by (device) (node_filesystem_avail_bytes{%s}))`, fsFilter, fsFilter),
		"io":          fmt.Sprintf(`sum(irate(node_disk_io_time_seconds_total{%s,device!~"loop.*|ram.*"}[5m])) * 100`, filter),
		"network_in":  fmt.Sprintf(`sum(irate(node_network_receive_bytes_total{%s,device!~"lo|docker.*|veth.*|br.*|cni.*"}[5m]))`, filter),
		"network_out": fmt.Sprintf(`sum(irate(node_network_transmit_bytes_total{%s,device!~"lo|docker.*|veth.*|br.*|cni.*"}[5m]))`, filter),
		// 主机层扩展
		"cpu_load1":       fmt.Sprintf(`avg(node_load1{%s})`, filter),
		"ctx_switches":    fmt.Sprintf(`sum(irate(node_context_switches_total{%s}[5m]))`, filter),
		"swap_used_pct":   fmt.Sprintf(`clamp_min((1 - avg(node_memory_SwapFree_bytes{%s}) / clamp_min(avg(node_memory_SwapTotal_bytes{%s}), 1)) * 100, 0)`, filter, filter),
		"disk_iops":       fmt.Sprintf(`sum(irate(node_disk_reads_completed_total{%s,device!~"loop.*|ram.*"}[5m])) + sum(irate(node_disk_writes_completed_total{%s,device!~"loop.*|ram.*"}[5m]))`, filter, filter),
		"disk_throughput": fmt.Sprintf(`sum(irate(node_disk_read_bytes_total{%s,device!~"loop.*|ram.*"}[5m])) + sum(irate(node_disk_written_bytes_total{%s,device!~"loop.*|ram.*"}[5m]))`, filter, filter),
		"disk_await_ms":   fmt.Sprintf(`1000 * (sum(irate(node_disk_read_time_seconds_total{%s,device!~"loop.*|ram.*"}[5m])) + sum(irate(node_disk_write_time_seconds_total{%s,device!~"loop.*|ram.*"}[5m]))) / clamp_min(sum(irate(node_disk_reads_completed_total{%s,device!~"loop.*|ram.*"}[5m])) + sum(irate(node_disk_writes_completed_total{%s,device!~"loop.*|ram.*"}[5m])), 1)`, filter, filter, filter, filter),
		"net_errors":      fmt.Sprintf(`sum(irate(node_network_receive_errs_total{%s}[5m])) + sum(irate(node_network_transmit_errs_total{%s}[5m])) + sum(irate(node_network_receive_drop_total{%s}[5m])) + sum(irate(node_network_transmit_drop_total{%s}[5m]))`, filter, filter, filter, filter),
		"tcp_established": fmt.Sprintf(`avg(node_netstat_Tcp_CurrEstab{%s})`, filter),
		"fd_used_pct":     fmt.Sprintf(`clamp_min(100 * avg(node_filefd_allocated{%s}) / clamp_min(avg(node_filefd_maximum{%s}), 1), 0)`, filter, filter),
		"time_offset_s":   fmt.Sprintf(`max(abs(node_timex_offset_seconds{%s}))`, filter),
	}

	// 资源层超时文案单独给：说清"是我们这边预算用尽"，而不是把锅甩给 exporter。
	timeoutReason := "Prometheus 查询超时（资源层预算已用尽，请检查 Prometheus 响应耗时或采集超时配置）"

	assign := func(target *MetricValue, exprKey string, unit string) {
		// 预算已耗尽时后续查询必然全部失败，不再空转，统一按超时归因。
		if ctx.Err() != nil {
			recordErr(exprKey, timeoutReason)
			return
		}
		expr := strings.ReplaceAll(expressions[exprKey], promRateWindowPlaceholder, window)
		value, ok, err := c.query(ctx, expr)
		if err != nil {
			// 查询出错 ≠ 没数据。err 里最典型的就是 ctx deadline（Prometheus 慢或不可用），
			// 以前它和"查不到时间序列"走了同一条 return，全部变成 noData，
			// 页面展示"未返回该主机的时间序列"，把超时伪装成 exporter 缺失。
			if ctx.Err() != nil {
				recordErr(exprKey, timeoutReason)
				return
			}
			recordErr(exprKey, "Prometheus 查询失败："+trimError(err))
			return
		}
		if !ok {
			// 真空结果：Prometheus 正常应答但确实没有这条序列，保持 noData 语义。
			return
		}
		if unit == "%" {
			*target = metricPercent(value)
			return
		}
		*target = metricRateValue(value, unit)
	}

	assign(&result.CPU, "cpu", "%")
	assign(&result.Memory, "memory", "%")
	assign(&result.Disk, "disk", "%")
	assign(&result.DiskUsed, "disk_used", "B")
	assign(&result.IO, "io", "%")
	assign(&result.NetworkIn, "network_in", "B/s")
	assign(&result.NetworkOut, "network_out", "B/s")
	assign(&result.CPULoad1, "cpu_load1", "")
	assign(&result.CtxSwitches, "ctx_switches", "ops/s")
	assign(&result.SwapUsedPct, "swap_used_pct", "%")
	assign(&result.DiskIOPS, "disk_iops", "ops/s")
	assign(&result.DiskThroughput, "disk_throughput", "B/s")
	assign(&result.DiskAwaitMs, "disk_await_ms", "ms")
	assign(&result.NetErrors, "net_errors", "count/s")
	assign(&result.TCPEstablished, "tcp_established", "count")
	assign(&result.FDUsedPct, "fd_used_pct", "%")
	assign(&result.TimeOffsetS, "time_offset_s", "s")
	return result, nil
}

func (c *prometheusClient) query(ctx context.Context, expr string) (float64, bool, error) {
	endpoint := c.baseURL + "/api/v1/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false, err
	}
	q := req.URL.Query()
	q.Set("query", expr)
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&payload); decodeErr != nil {
		return 0, false, decodeErr
	}
	if payload.Status != "success" {
		if payload.Error == "" {
			payload.Error = "unknown error"
		}
		return 0, false, fmt.Errorf("%s", payload.Error)
	}
	if len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return 0, false, nil
	}
	value, err := strconv.ParseFloat(fmt.Sprintf("%v", payload.Data.Result[0].Value[1]), 64)
	if err != nil {
		return 0, false, err
	}
	return sanitizeMetricValue(value), true, nil
}

// queryFirstSeries 取第一条时间序列的「标签 + 值」。
//
// 与 query 的区别：query 只回数值，而 node_uname_info 这类"信息型"指标值恒为 1，
// 真正有用的内容全在标签里（sysname / release / nodename）—— 拿不到标签就取不到
// 操作系统与内核版本。
func (c *prometheusClient) queryFirstSeries(ctx context.Context, expr string) (map[string]string, float64, bool, error) {
	if !c.Configured() {
		return nil, 0, false, nil
	}
	endpoint := c.baseURL + "/api/v1/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, false, err
	}
	q := req.URL.Query()
	q.Set("query", expr)
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, 0, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&payload); decodeErr != nil {
		return nil, 0, false, decodeErr
	}
	if payload.Status != "success" {
		if payload.Error == "" {
			payload.Error = "unknown error"
		}
		return nil, 0, false, fmt.Errorf("%s", payload.Error)
	}
	if len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return nil, 0, false, nil
	}
	value, err := strconv.ParseFloat(fmt.Sprintf("%v", payload.Data.Result[0].Value[1]), 64)
	if err != nil {
		return nil, 0, false, err
	}
	return payload.Data.Result[0].Metric, sanitizeMetricValue(value), true, nil
}

func metricRateValue(value float64, unit string) MetricValue {
	return MetricValue{Value: sanitizeMetricValue(value), Unit: unit, Available: true, Quality: QualityOK}
}

func metricPercent(value float64) MetricValue {
	available := !math.IsNaN(value) && !math.IsInf(value, 0)
	quality := QualityNoData
	if available {
		quality = QualityOK
	}
	return MetricValue{Value: sanitizeMetricValue(value), Unit: "%", Available: available, Quality: quality}
}

// ---------------------------------------------------------------------------
// 通用小工具
// ---------------------------------------------------------------------------

func parseExtraConfig(extraConfig []byte) (map[string]any, error) {
	if len(extraConfig) == 0 {
		return map[string]any{}, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(extraConfig, &cfg); err != nil {
		return nil, fmt.Errorf("扩展连接参数格式错误")
	}
	return cfg, nil
}

func getStringConfig(cfg map[string]any, key string, fallback string) string {
	if cfg == nil {
		return fallback
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if text == "" {
		return fallback
	}
	return text
}

func getEnvString(key string) string {
	return os.Getenv(key)
}

func toFloat64(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case uint32:
		return float64(typed)
	case string:
		number, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number
	default:
		number, _ := strconv.ParseFloat(strings.TrimSpace(fmt.Sprintf("%v", value)), 64)
		return number
	}
}

func toString(value any) string {
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func pickFirstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
