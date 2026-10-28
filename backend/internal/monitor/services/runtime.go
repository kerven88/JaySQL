package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================================
// 监控明细 / 配置管理服务
//
// 与旧版的区别：
//   - 采集与告警评估已移出本文件（见 monitor_collect.go / monitor_alerts.go）；
//     本文件只保留"读库渲染"与"配置写库"两类职责；
//   - 详情页趋势改从 insight_monitor_metric_points 窄表读取（旧版读快照宽表，
//     而快照只由页面请求写入，实测连续 7 天 0 条 → 趋势恒为空）。
// ============================================================================

var defaultMonitorRulesOnceGuard = make(chan struct{}, 1)

type DetailMetricPoint struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

type DetailMetricComparison struct {
	Current   float64 `json:"current"`
	HourAgo   float64 `json:"hour_ago"`
	DayAgo    float64 `json:"day_ago"`
	Change1H  float64 `json:"change_1h"`
	Change24H float64 `json:"change_24h"`
	Available bool    `json:"available"`
}

type DetailMetricTrend struct {
	Key        string                 `json:"key"`
	Label      string                 `json:"label"`
	Current    MetricValue            `json:"current"`
	Comparison DetailMetricComparison `json:"comparison"`
	Points     []DetailMetricPoint    `json:"points"`
	// Core 是否进首屏「核心指标带」（资源水位 + 吞吐 + 连接，见 coreTrendKeys）。
	// 由后端标记而不是前端自己挑 key：指标键各引擎差异大，口径集中在一处才不会漂。
	Core bool `json:"core"`
}

type MonitorDetailListItem struct {
	ID                uint64      `json:"id"`
	InstanceID        string      `json:"instance_id"`
	Name              string      `json:"name"`
	Target            string      `json:"target"`
	DbType            string      `json:"db_type"`
	EnvironmentName   string      `json:"environment_name"`
	Business          string      `json:"business"`
	GroupName         string      `json:"group_name"`
	DeployMode        string      `json:"deploy_mode"`
	Status            string      `json:"status"`
	StatusMessage     string      `json:"status_message"`
	Quality           string      `json:"quality"`
	AlertLevel        string      `json:"alert_level"`
	ResourceSource    string      `json:"resource_source"`
	EngineSource      string      `json:"engine_source"`
	CPU               MetricValue `json:"cpu"`
	Memory            MetricValue `json:"memory"`
	Disk              MetricValue `json:"disk"`
	QPS               MetricValue `json:"qps"`
	TPS               MetricValue `json:"tps"`
	Connections       MetricValue `json:"connections"`
	ActiveConnections MetricValue `json:"active_connections"`
	AlertCount        int         `json:"alert_count"`
	CollectedAt       string      `json:"collected_at"`
	FreshnessSeconds  int         `json:"freshness_seconds"`
	// ScreenClass 列表状态点与排序依据，口径同 classifyScreenCard（大屏四分类一致）
	ScreenClass string `json:"screen_class"`
	// MetricsTemplate 绑定值（实例配置里的字面值）
	MetricsTemplate string `json:"metrics_template"`
	// TemplateName / TemplateLabel 实际生效的模板（绑定失效时是回落后的默认模板）
	TemplateName  string `json:"template_name"`
	TemplateLabel string `json:"template_label"`
	TemplateFall  bool   `json:"template_fallback"`
}

type MonitorDetailSelected struct {
	Instance         InstanceMonitorCard `json:"instance"`
	Business         string              `json:"business"`
	GroupName        string              `json:"group_name"`
	PrometheusTarget string              `json:"prometheus_target"`
	ResourceTrends   []DetailMetricTrend `json:"resource_trends"`
	EngineTrends     []DetailMetricTrend `json:"engine_trends"`
	AlertRecords     []AlertRecord       `json:"alert_records"`
	// Template 实例当前生效的指标模板（含"已回落默认模板"的说明）
	Template instanceTemplateInfo `json:"template"`
	// OutOfTemplate 该类型适用、但不在当前模板内的指标：明确可见，绝不静默缺数
	OutOfTemplate      []MetricTemplateMetricItem `json:"out_of_template"`
	OutOfTemplateCount int                        `json:"out_of_template_count"`
	// ScreenClass 该实例的四分类（healthy / risk / fault / uncollected），
	// 与大屏环形图、分组矩阵同一份口径，页面头部状态徽标直接用这个值上色。
	ScreenClass string `json:"screen_class"`
	// Conclusion 一句话结论：这台实例现在怎么样、为什么不健康。
	// 由 buildDetailConclusion 从真实原因拼出，取不到原因时回落 status_message。
	Conclusion string `json:"conclusion"`
	// RiskReasons 风险原因明细（阈值 / 观测值 / 持续时长 / 质量降级依据），
	// 提取逻辑与风险总览完全一致；fault / risk 实例有值，healthy 为空数组。
	RiskReasons []RiskReason `json:"risk_reasons"`
	// Profile 实例概览（第二部分）：18 个展示位 + 健康分 + 核心告警摘要。
	// 每个字段都由后端算好并带上"取不到时的原因"，前端只负责排版 —— 不允许前端自己拼。
	Profile InstanceProfile `json:"profile"`
	// Display 展示目录：按数据库类型定制的 Tab 与监控项（名称 / 定义 / 单位 / 顺序 / 曲线）。
	// 由后端下发而不是前端写死，保证"业务口径"只有一处定义；未收录的类型为 null。
	Display *MonitorDisplayLayout `json:"display"`
}

type MonitorDetailData struct {
	GeneratedAt string                  `json:"generated_at"`
	Filters     DashboardFilters        `json:"filters"`
	Instances   []MonitorDetailListItem `json:"instances"`
	Selected    *MonitorDetailSelected  `json:"selected"`
}

type MonitorTargetConfigItem struct {
	ID                    uint64 `json:"id"`
	InstanceID            string `json:"instance_id"`
	Name                  string `json:"name"`
	Target                string `json:"target"`
	DbType                string `json:"db_type"`
	EnvironmentName       string `json:"environment_name"`
	Enabled               bool   `json:"enabled"`
	Business              string `json:"business"`
	GroupName             string `json:"group_name"`
	DeployMode            string `json:"deploy_mode"`
	PrometheusTarget      string `json:"prometheus_target"`
	ResourceSource        string `json:"resource_source"`
	EngineSource          string `json:"engine_source"`
	ScrapeIntervalSeconds int    `json:"scrape_interval_seconds"`
	// ScrapeIntervalFollowsPlatform 实例是否跟随平台周期（实例行未显式设置，值为 0/NULL）。
	// 列表里的 ScrapeIntervalSeconds 是生效值（跟随 = 平台值），编辑回填必须看这个
	// 标记，否则把生效值 15 当成显式选择存回去，下次平台改周期又带不动了。
	ScrapeIntervalFollowsPlatform bool   `json:"scrape_interval_follows_platform"`
	RetentionHours                int    `json:"retention_hours"`
	TimeoutSeconds                int    `json:"timeout_seconds"`
	RetryCount                    int    `json:"retry_count"`
	MetricsTemplate               string `json:"metrics_template"`
	// TemplateLabel / TemplateMetricCount 实际生效模板的展示信息（页面直接可用，不用二次请求）
	TemplateLabel       string `json:"template_label"`
	TemplateMetricCount int    `json:"template_metric_count"`
	TemplateFallback    bool   `json:"template_fallback"`
	// TemplateState 绑定状态 bound / unbound / fallback。
	// 注意 MetricsTemplate 下发的是"实际生效模板名"（编辑弹窗回显要用），
	// 单看它分不出"没绑定"和"绑定失效回落"，页面展示必须依赖本字段。
	TemplateState string   `json:"template_state"`
	Labels        []string `json:"labels"`
	// --- 采集策略挂载（模板-实例两级）---
	// StrategyTemplateID 是实例行里的字面挂载值；StrategyTemplateName/Label 是**实际生效**的模板。
	// 两者可能不同：挂载的模板被停用或类型不一致时，实际跑的是默认模板 ——
	// 这个差异只靠名字看不出来，所以 StrategyFallback 必须一并下发，
	// 否则页面会显示"已挂载 A"而实际在跑 B，是个假的成功反馈。
	StrategyTemplateID    *uint64  `json:"strategy_template_id"`
	StrategyTemplateName  string   `json:"strategy_template_name"`
	StrategyTemplateLabel string   `json:"strategy_template_label"`
	StrategyFallback      bool     `json:"strategy_fallback"`
	StrategyFallbackNote  string   `json:"strategy_fallback_note"`
	StrategyOverrides     []string `json:"strategy_overrides"`
	// --- 告警策略挂载（同采集策略口径：字面挂载 + 实际生效一并下发）---
	AlertTemplateID       *uint64 `json:"alert_template_id"`
	AlertTemplateName     string  `json:"alert_template_name"`
	AlertTemplateLabel    string  `json:"alert_template_label"`
	AlertTemplateFallback bool    `json:"alert_template_fallback"`
	AlertFallbackNote     string  `json:"alert_fallback_note"`
	// 采集运行态（只读，用于让运维一眼看出"这个实例到底采到没有"）
	LastCollectAt       string `json:"last_collect_at"`
	LastCollectStatus   string `json:"last_collect_status"`
	LastCollectMessage  string `json:"last_collect_message"`
	CollectQuality      string `json:"collect_quality"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	Collectable         bool   `json:"collectable"`
	EngineNote          string `json:"engine_note"`
	// QualityDetails 降级明细："部分可用"到底缺了哪些指标、为什么。
	// 页面上的汇总标签如果不给下钻入口，用户只能看到"不太好"却无从下手，
	// 于是这个标签就退化成了噪音。仅当存在非 ok 指标时非空。
	QualityDetails []QualityDetailItem `json:"quality_details"`
}

// QualityDetailItem 单个指标的降级说明。
type QualityDetailItem struct {
	MetricKey   string `json:"metric_key"`
	MetricLabel string `json:"metric_label"`
	Quality     string `json:"quality"`
	Reason      string `json:"reason"`
	SubDims     string `json:"sub_dims"`
}

type MonitorTargetConfigListData struct {
	Filters DashboardFilters          `json:"filters"`
	Targets []MonitorTargetConfigItem `json:"targets"`
}

type MonitorRuleItem struct {
	ID              uint64   `json:"id"`
	Name            string   `json:"name"`
	DBType          string   `json:"db_type"`
	RuleType        string   `json:"rule_type"`
	ConditionType   string   `json:"condition_type"`
	MetricKey       string   `json:"metric_key"`
	MetricLabel     string   `json:"metric_label"`
	Operator        string   `json:"operator"`
	Threshold       float64  `json:"threshold"`
	Unit            string   `json:"unit"`
	HoldMinutes     int      `json:"hold_minutes"`
	ForSeconds      int      `json:"for_seconds"`
	RecoverySeconds int      `json:"recovery_seconds"`
	NoDataBehavior  string   `json:"no_data_behavior"`
	AlertLevel      string   `json:"alert_level"`
	Severity        string   `json:"severity"`
	SilenceMinutes  int      `json:"silence_minutes"`
	GroupKey        string   `json:"group_key"`
	ScopeType       string   `json:"scope_type"`
	ScopeValue      string   `json:"scope_value"`
	NotifyChannels  []string `json:"notify_channels"`
	SilencePolicy   []string `json:"silence_policy"`
	Description     string   `json:"description"`
	Enabled         bool     `json:"enabled"`
	Builtin         bool     `json:"builtin"`
	// 规则归属的告警模板（规则是模板的组成部分，"通知与静默"页签按模板聚合展示）
	TemplateID   uint64 `json:"template_id"`
	TemplateName string `json:"template_name"`
}

type MonitorRuleListData struct {
	Filters DashboardFilters  `json:"filters"`
	Rules   []MonitorRuleItem `json:"rules"`
}

type GetDetailService struct {
	*forms.GetDetailForm
	C *gin.Context
}

type GetTargetConfigsService struct {
	*forms.MonitorListFilter
	C *gin.Context
}

type UpsertTargetConfigService struct {
	InstanceID string
	*forms.UpdateTargetConfigForm
	C *gin.Context
}

type GetRuleListService struct {
	*forms.GetRuleListForm
	C *gin.Context
}

type CreateRuleService struct {
	*forms.SaveRuleForm
	C *gin.Context
}

type UpdateRuleService struct {
	ID uint64
	*forms.SaveRuleForm
	C *gin.Context
}

type DeleteRuleService struct {
	ID uint64
	C  *gin.Context
}

// ============================================================================
// 默认告警规则播种：已下线
//
// 原先这里有 defaultRuleEngines / defaultRuleMetrics / buildDefaultAlertRules 三个函数，
// 会在每个告警评估周期自动往默认模板里补规则，阈值取自指标字典的 default_threshold。
// 后果是"用户从没配过规则的指标也会告警"，且阈值来源对运维不可见 ——
// 与"告警判定只以配置页规则为唯一依据"的原则冲突。
//
// 现在：不再自动播种任何规则。指标没有配置规则就不触发告警、不发送通知。
// 保留的能力只有下面 pruneStaleAlertRules —— 清理引用失效指标的残留规则。
// ============================================================================

func formatRuleThreshold(threshold float64, unit string) string {
	switch unit {
	case "%":
		return fmt.Sprintf("%.0f%%", threshold)
	case "count", "":
		return fmt.Sprintf("%.0f", threshold)
	default:
		return fmt.Sprintf("%.0f%s", threshold, unit)
	}
}

// pruneStaleAlertRules 清理默认模板里引用「已不可配指标」的残留规则。
//
// 这是原 ensureDefaultAlertRules 里保留下来的唯一能力：只删、不补。
// 指标定义固定在代码里（builtinMetricCatalog），发版移除或收窄某指标后，
// 默认模板里引用它的规则会永远评估不到，也会让用户在配置页看到"找不到对应指标"的行。
//
// 边界（与原实现一致，避免影响历史数据）：
//   - 只处理默认模板（IsDefault && Enabled），自定义模板的规则完全由用户维护；
//   - 只清理不在告警配置页可勾选清单里的指标（templateApplicableMetricsWithHost）；
//   - 不再自动补任何新规则 —— 没有配置规则就不告警，这是刻意的设计。
func pruneStaleAlertRules() error {
	select {
	case defaultMonitorRulesOnceGuard <- struct{}{}:
		defer func() { <-defaultMonitorRulesOnceGuard }()
	default:
		// 已有一轮在跑，本轮跳过（避免告警 tick 与启动引导并发）
		return nil
	}
	if global.App == nil || global.App.DB == nil {
		return nil
	}
	// 先保证默认模板在（含历史规则归位）
	if err := EnsureAlertTemplates(global.App.DB); err != nil {
		return err
	}
	_, _, list := loadAlertTemplateCache()
	templateIDs := make(map[string]uint64, len(list))
	for _, tmpl := range list {
		if tmpl.IsDefault && tmpl.Enabled {
			templateIDs[tmpl.DBType] = tmpl.ID
		}
	}
	existing := make([]monitorModels.InsightMonitorAlertRules, 0)
	if err := global.App.DB.Select("id, template_id, metric_key").Find(&existing).Error; err != nil {
		return err
	}

	// 清理默认模板里引用「失效/不可配指标」的规则（只清默认模板，
	// 自定义模板的规则完全由用户维护）：指标定义收窄 EngineScope、指标移除、
	// 或被移出告警配置页勾选清单后，残留规则会让用户在监控明细里找不到对应指标，
	// 且永远不会被评估到。
	//
	// 判断基准是 templateApplicableMetricsWithHost（与告警配置页可勾选清单对齐），
	// 而不是"平台会给默认阈值的指标"——配置页允许为 TPS/QPS 等纯观测型指标手工设阈值。
	idToEngine := make(map[uint64]string, len(templateIDs))
	for engineKey, id := range templateIDs {
		idToEngine[id] = engineKey
	}
	allowedCache := make(map[string]map[string]struct{})
	staleIDs := make([]uint64, 0)
	for _, item := range existing {
		engineKey, ok := idToEngine[item.TemplateID]
		if !ok {
			continue
		}
		allowed, ok := allowedCache[engineKey]
		if !ok {
			descriptors := templateApplicableMetricsWithHost(engineKey)
			allowed = make(map[string]struct{}, len(descriptors))
			for _, desc := range descriptors {
				allowed[canonicalMetricKey(desc.MetricKey)] = struct{}{}
			}
			allowedCache[engineKey] = allowed
		}
		// 退役指标同样算"失效"：它已从该类型的全部指标模板里摘掉，不再采集，
		// 引用它的规则永远评估不到（no_data_behavior=ignore 时更是彻底静默）。
		// 与"指标定义收窄 EngineScope"同一处理路径 —— 只删、不补。
		if metricKeyRetiredForEngine(engineKey, canonicalMetricKey(item.MetricKey)) {
			staleIDs = append(staleIDs, item.ID)
			continue
		}
		if _, ok := allowed[canonicalMetricKey(item.MetricKey)]; ok {
			continue
		}
		staleIDs = append(staleIDs, item.ID)
	}
	if len(staleIDs) > 0 {
		if err := global.App.DB.Where("id IN ?", staleIDs).
			Delete(&monitorModels.InsightMonitorAlertRules{}).Error; err != nil {
			return err
		}
		global.App.Log.Infof("[monitor] 已清理 %d 条引用失效指标的默认告警规则", len(staleIDs))
	}

	return nil
}

// ============================================================================
// 指标取值与标签
// ============================================================================

// metricValueByKey 从卡片的指标列表取某个键的取值。
// 旧版是 15 分支 switch 且只覆盖裸名；现在统一走三段式键（兼容层负责别名映射）。
func metricValueByKey(card InstanceMonitorCard, key string, maxConnections float64) (float64, MetricValue, bool) {
	canonical := canonicalMetricKey(key)
	if canonical == MetricCapConnUsedPct && maxConnections > 0 {
		// 老规则里的 active_connection_ratio 语义：活跃连接 / 最大连接
		active := sampleValue(card.Metrics, MetricPerfActiveConn)
		if !active.Available {
			return 0, MetricValue{Unit: "%", Quality: QualityNoData, Reason: "活跃连接数不可用"}, false
		}
		value := metricPercent(active.Value / maxConnections * 100)
		return value.Value, value, value.Available
	}
	metric := sampleValue(card.Metrics, canonical)
	return metric.Value, metric, metric.Available
}

func compareMetricValue(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	case "=":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return false
	}
}

// metricLabel 取指标展示名。旧版是写死的 15 项 map；现在查字典，新增指标自动有中文名。
func metricLabel(key string) string {
	canonical := canonicalMetricKey(key)
	for _, item := range builtinMetricCatalog {
		if item.MetricKey == canonical {
			return item.DisplayName
		}
	}
	return canonical
}

// loadAlertHistory 明细页告警时间线。与待处理告警同一模板白名单口径：
// 模板外指标的历史事件（模板变更/裁剪前落库）不进入时间线。
func loadAlertHistory(row monitorInstanceRow) ([]AlertRecord, error) {
	exclude := alertMetricExcludedByTemplate(row)
	records := make([]monitorModels.InsightMonitorAlertEvents, 0)
	if err := global.App.DB.
		Where("instance_id = ?", row.InstanceID).
		Order("last_seen_at desc, triggered_at desc").
		Limit(50).
		Find(&records).Error; err != nil {
		return nil, err
	}
	result := make([]AlertRecord, 0, len(records))
	alertMeta := metaFromRow(row)
	for _, item := range records {
		if exclude(item.MetricKey) {
			continue
		}
		record := eventToAlertRecord(item, alertMeta)
		record.Status = statusTextFromEvent(item.Status)
		result = append(result, record)
	}
	return result, nil
}

func statusTextFromEvent(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "recovered":
		return "已恢复"
	case "pending":
		return "观察中"
	case "active":
		return "待认领"
	case "acknowledged":
		return "处理中"
	default:
		return defaultString(value, "待认领")
	}
}

// alertStatusText 与 statusTextFromEvent 同口径，供事件落 DTO 时使用。
func alertStatusText(value string) string {
	return statusTextFromEvent(value)
}

// ============================================================================
// 明细页
// ============================================================================

func (s *GetDetailService) Run() (*MonitorDetailData, error) {
	filter := monitorListFilter{}
	if s.GetDetailForm != nil {
		filter = monitorListFilter{
			Search:      s.Search,
			Environment: s.Environment,
			DbType:      s.DbType,
			Business:    s.Business,
			GroupName:   s.GroupName,
			// 注意：InstanceID 不参与列表过滤，仅用于决定选中实例（见下方 selectedID 逻辑）。
			// 若加入过滤，URL 带 instance_id 时实例列表会被过滤成只剩 1 个。
		}
	}
	requestCtx := context.Background()
	if s.C != nil && s.C.Request != nil {
		requestCtx = s.C.Request.Context()
	}
	rows, cards, filters, _, err := loadMonitorCards(requestCtx, filter)
	if err != nil {
		return nil, err
	}
	// 先统一分类：列表状态点、头部徽标、风险原因提取都依赖 screen_class，
	// 口径只走 evaluateInstanceState 一处（健康分驱动，与大屏四分类完全一致）。
	for i := range cards {
		cards[i].ScreenClass = evaluateInstanceState(cards[i]).Class
	}
	payload := &MonitorDetailData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Filters:     filters,
		Instances:   make([]MonitorDetailListItem, 0, len(cards)),
	}
	rowMap := map[string]monitorInstanceRow{}
	cardMap := map[string]InstanceMonitorCard{}
	for idx, card := range cards {
		row := rows[idx]
		rowMap[card.InstanceID] = row
		cardMap[card.InstanceID] = card
		templateInfo := templateInfoForInstance(row)
		payload.Instances = append(payload.Instances, MonitorDetailListItem{
			ID:                card.ID,
			InstanceID:        card.InstanceID,
			Name:              firstNonEmpty(card.Remark, card.Target),
			Target:            card.Target,
			DbType:            card.DbType,
			EnvironmentName:   card.EnvironmentName,
			Business:          row.Business,
			GroupName:         row.GroupName,
			DeployMode:        card.DeployMode,
			Status:            card.Status,
			StatusMessage:     card.StatusMessage,
			Quality:           card.Quality,
			AlertLevel:        highestAlertLevel(card.Alerts),
			ResourceSource:    card.ResourceSource,
			EngineSource:      card.EngineSource,
			CPU:               card.ResourceMetrics.CPU,
			Memory:            card.ResourceMetrics.Memory,
			Disk:              card.ResourceMetrics.Disk,
			QPS:               card.EngineMetrics.QPS,
			TPS:               card.EngineMetrics.TPS,
			Connections:       card.EngineMetrics.Connections,
			ActiveConnections: card.EngineMetrics.ActiveConnections,
			AlertCount:        len(card.Alerts),
			CollectedAt:       card.CollectedAt,
			FreshnessSeconds:  card.FreshnessSeconds,
			ScreenClass:       card.ScreenClass,
			MetricsTemplate:   strings.TrimSpace(row.MetricsTemplate),
			TemplateName:      templateInfo.Name,
			TemplateLabel:     firstNonEmpty(templateInfo.DisplayName, templateInfo.Name),
			TemplateFall:      templateInfo.Fallback,
		})
	}
	if len(cards) == 0 {
		return payload, nil
	}
	// 列表按「最需要看的排最前」排序（故障 > 风险 > 未采集 > 健康），
	// 排序只影响展示顺序，selectedID 的显式指定仍然优先。
	sortDetailInstancesByRisk(payload.Instances)
	selectedID := strings.TrimSpace(s.InstanceID)
	if selectedID == "" || cardMap[selectedID].InstanceID == "" {
		// 未显式指定实例时默认落在列表第一个 —— 进页面先看问题实例，
		// 而不是随机看一个恰好排在最前的健康实例。
		selectedID = payload.Instances[0].InstanceID
	}
	row := rowMap[selectedID]
	card := cardMap[selectedID]
	// 只在明细页补齐"应展示但本轮没采到"的指标：
	// 明细页要的是"一个不漏"，大屏要的是"快速扫一眼"，补齐逻辑不能污染大屏的指标视图。
	card.Metrics = appendMissingMetricViews(card.Metrics, row)
	alertHistory, err := loadAlertHistory(row)
	if err != nil {
		return nil, err
	}
	resourceDefs, engineDefs := trendDefinitions(card)
	keys := make([]string, 0, len(resourceDefs)+len(engineDefs))
	for _, def := range resourceDefs {
		keys = append(keys, def.Key)
	}
	for _, def := range engineDefs {
		keys = append(keys, def.Key)
	}
	series := loadMetricSeriesMap(row.InstanceID, keys, time.Now().Add(-26*time.Hour))
	outOfTemplate := outOfTemplateMetrics(row, 80)
	// 结论与原因：先分类（上面已算好），再复用风险提取，保证与大屏「为什么是风险」口径一致。
	riskReasons := detailRiskReasons(card)
	payload.Selected = &MonitorDetailSelected{
		Instance:           card,
		Business:           row.Business,
		GroupName:          row.GroupName,
		PrometheusTarget:   row.PrometheusTarget,
		ResourceTrends:     buildMetricTrends(resourceDefs, series),
		EngineTrends:       buildMetricTrends(engineDefs, series),
		AlertRecords:       alertHistory,
		Template:           templateInfoForInstance(row),
		OutOfTemplate:      outOfTemplate,
		OutOfTemplateCount: len(outOfTemplate),
		ScreenClass:        card.ScreenClass,
		Conclusion:         buildDetailConclusion(card, card.ScreenClass, riskReasons),
		RiskReasons:        riskReasons,
		// 概览画像：用待处理告警（card.Alerts）而不是全量历史，
		// 因为"核心告警项目"要回答的是"现在有哪些告警还没恢复"。
		Profile: buildInstanceProfile(row, card, card.Alerts),
		// 展示目录：按数据库类型定制的 Tab 与监控项（名称 / 定义 / 单位 / 顺序 / 曲线）。
		// 未收录的类型返回 nil，前端回落到旧的分层推导逻辑。
		// 传入实例角色后，本角色下结构性不存在的指标（如单机的备库复制延迟）
		// 会被整行剔除，页面既不渲染卡片也不留"无数据"占位。
		// 目录裁剪之后再按模板勾选过滤：MySQL / PostgreSQL 未勾选的指标不下发明细，
		// 与历史查询接口（filterKeysByTemplate）共同保证"勾了才可见"。
		Display: filterDisplayByTemplate(MonitorDisplayLayoutFor(row.DbType, row.InstanceRole), displayGateTemplate(row)),
	}
	return payload, nil
}

type trendDefinition struct {
	Key    string
	Label  string
	Metric MetricValue
}

func trendDefinitions(card InstanceMonitorCard) ([]trendDefinition, []trendDefinition) {
	resourceDefs := []trendDefinition{
		{MetricResCPU, "CPU", card.ResourceMetrics.CPU},
		{MetricResMemory, "内存", card.ResourceMetrics.Memory},
		{MetricResDisk, "磁盘", card.ResourceMetrics.Disk},
		{MetricResIO, "IO", card.ResourceMetrics.IO},
		{MetricResNetworkIn, "网络流入", card.ResourceMetrics.NetworkIn},
		{MetricResNetworkOut, "网络流出", card.ResourceMetrics.NetworkOut},
	}
	engineDefs := []trendDefinition{
		{MetricPerfQPS, "QPS", card.EngineMetrics.QPS},
		{MetricPerfTPS, "TPS", card.EngineMetrics.TPS},
		{MetricPerfReadRows, "读行数", card.EngineMetrics.ReadRows},
		{MetricPerfWriteRows, "写行数", card.EngineMetrics.WriteRows},
		{MetricPerfReadBytes, "协议流出", card.EngineMetrics.ReadBytes},
		{MetricPerfWriteBytes, "协议流入", card.EngineMetrics.WriteBytes},
		{MetricPerfCacheHitRatio, "缓存命中率", card.EngineMetrics.CacheHitRatio},
		{MetricPerfLocks, "锁等待", card.EngineMetrics.Locks},
		{MetricPerfConnCount, "连接数", card.EngineMetrics.Connections},
		{MetricPerfActiveConn, "活跃连接", card.EngineMetrics.ActiveConnections},
	}
	return resourceDefs, engineDefs
}

// loadMetricSeriesMap 从时序点窄表读取每条指标近期的点。
//
// 关键修复：旧版读 insight_monitor_metric_snapshots 且 `ORDER BY created_at asc LIMIT 120`，
// 由于宽表只由页面请求写入，趋势图长期冻结在最早写下的那批点。窄表由后台采集器持续写入，
// 并带 (instance_id, metric_key, ts) 复合索引，取最近 N 点后反转为时间正序。
func loadMetricSeriesMap(instanceID string, keys []string, since time.Time) map[string][]DetailMetricPoint {
	result := make(map[string][]DetailMetricPoint, len(keys))
	if global.App.DB == nil {
		return result
	}
	type pointRow struct {
		Ts    commonModels.LocalTime
		Value *float64
	}
	for _, key := range keys {
		canonical := canonicalMetricKey(key)
		rows := make([]pointRow, 0, 240)
		if err := global.App.DB.Table("insight_monitor_metric_points").
			Select("ts, value").
			Where("instance_id = ? AND metric_key = ? AND ts >= ? AND value IS NOT NULL", instanceID, canonical, since).
			Order("ts desc").
			Limit(240).
			Scan(&rows).Error; err != nil {
			continue
		}
		points := make([]DetailMetricPoint, 0, len(rows))
		for index := len(rows) - 1; index >= 0; index-- {
			if rows[index].Value == nil {
				continue
			}
			points = append(points, DetailMetricPoint{
				Time:  time.Time(rows[index].Ts).Format("01-02 15:04"),
				Value: *rows[index].Value,
			})
		}
		result[canonical] = points
	}
	return result
}

func buildMetricTrends(defs []trendDefinition, series map[string][]DetailMetricPoint) []DetailMetricTrend {
	result := make([]DetailMetricTrend, 0, len(defs))
	for _, def := range defs {
		points := series[def.Key]
		if points == nil {
			points = make([]DetailMetricPoint, 0)
		}
		result = append(result, DetailMetricTrend{
			Key:        def.Key,
			Label:      def.Label,
			Current:    def.Metric,
			Comparison: buildMetricComparison(def.Metric, points),
			Points:     points,
			Core:       isCoreTrendKey(def.Key),
		})
	}
	return result
}

func buildMetricComparison(current MetricValue, points []DetailMetricPoint) DetailMetricComparison {
	if !current.Available {
		return DetailMetricComparison{Available: false}
	}
	hourAgo, hasHour := nearestSeriesValue(points, time.Now().Add(-time.Hour))
	dayAgo, hasDay := nearestSeriesValue(points, time.Now().Add(-24*time.Hour))
	if !hasHour && !hasDay {
		return DetailMetricComparison{Current: current.Value, Available: false}
	}
	return DetailMetricComparison{
		Current:   current.Value,
		HourAgo:   hourAgo,
		DayAgo:    dayAgo,
		Change1H:  current.Value - hourAgo,
		Change24H: current.Value - dayAgo,
		Available: true,
	}
}

// nearestSeriesValue 在时间序列里找离目标时刻最近的点。
// 点的 Time 字段是 "01-02 15:04" 展示格式，因此这里用"距末尾的位置"换算：
// 序列按采集周期等间隔，末尾是now，倒数第 N 个点即 N 个周期之前。
func nearestSeriesValue(points []DetailMetricPoint, target time.Time) (float64, bool) {
	if len(points) == 0 {
		return 0, false
	}
	offsets := seriesOffsets(points)
	best := points[len(points)-1].Value
	bestDelta := absDuration(time.Since(target))
	for index := len(points) - 1; index >= 0; index-- {
		delta := absDuration(time.Since(target.Add(-offsets[index])))
		if delta < bestDelta {
			bestDelta = delta
			best = points[index].Value
		}
	}
	return best, true
}

// seriesOffsets 依据展示时间戳估算每个点距末尾的时间偏移。
// 目的是让"1 小时前 / 24 小时前"的比较基于真实时间间距而非固定下标。
func seriesOffsets(points []DetailMetricPoint) []time.Duration {
	offsets := make([]time.Duration, len(points))
	parsed := make([]time.Time, len(points))
	layout := "01-02 15:04"
	now := time.Now()
	for index, point := range points {
		value, err := time.ParseInLocation(layout, point.Time, time.Local)
		if err != nil {
			continue
		}
		// 展示格式不含年份，补齐为"最近的这一年"
		value = value.AddDate(now.Year(), 0, 0)
		if value.After(now.Add(24 * time.Hour)) {
			value = value.AddDate(-1, 0, 0)
		}
		parsed[index] = value
	}
	tail := parsed[len(parsed)-1]
	if tail.IsZero() {
		// 解析失败时退化为"按等间隔估算"
		if len(points) > 1 {
			step := 60 * time.Second
			for index := len(points) - 1; index >= 0; index-- {
				offsets[index] = time.Duration(len(points)-1-index) * step
			}
		}
		return offsets
	}
	for index := range parsed {
		if parsed[index].IsZero() {
			offsets[index] = time.Duration(len(points)-1-index) * 60 * time.Second
			continue
		}
		offsets[index] = -tail.Sub(parsed[index])
	}
	return offsets
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func highestAlertLevel(alerts []AlertRecord) string {
	levelWeight := map[string]int{"高": 3, "中": 2, "低": 1}
	maxLevel := "无"
	maxWeight := 0
	for _, alert := range alerts {
		if levelWeight[alert.Level] > maxWeight {
			maxLevel = alert.Level
			maxWeight = levelWeight[alert.Level]
		}
	}
	return maxLevel
}

// ============================================================================
// 监控对象（配置）页
// ============================================================================

type targetConfigRow struct {
	ID              uint64
	InstanceID      string
	Hostname        string
	Port            int
	DbType          string
	EnvironmentName string
	Remark          string
	// InstanceRole 架构角色（master / slave / standalone / cascade），取自
	// insight_instances.instance_role。降级明细要用它做角色适用性过滤，
	// 与卡片渲染（buildCardFromStore）保持同一口径。
	InstanceRole string
	// Enabled 用 *bool 而不是自定义 Scanner 类型：GORM 对"只实现 Scanner、
	// 未实现 driver.Valuer"的自定义类型会当成关联字段解析，直接报
	// "define a valid foreign key for relations"。*bool 既能接 NULL 又能被正常扫描。
	Enabled               *bool
	Business              string
	GroupName             string
	DeployMode            string
	PrometheusTarget      string
	ResourceSource        string
	EngineSource          string
	ScrapeIntervalSeconds int
	RetentionHours        int
	TimeoutSeconds        int
	RetryCount            int
	MetricsTemplate       string
	// StrategyTemplateID 挂载的采集策略模板（NULL = 未挂载，套用类型默认模板）。
	// 用 *uint64 接 NULL：与 Enabled 同理，GORM 对指针类型才不会当成关联字段。
	StrategyTemplateID *uint64
	// StrategyOverrides 实例级策略覆盖（JSON），用于列表展示"哪些项被单独改过"
	StrategyOverrides []byte
	// AlertTemplateID 挂载的告警策略模板（NULL = 未挂载，告警评估回落类型默认模板）。
	AlertTemplateID     *uint64
	LastCollectAt       *commonModels.LocalTime
	LastCollectStatus   string
	LastCollectMessage  string
	CollectQuality      string
	ConsecutiveFailures int
	Labels              []byte
}

func (s *GetTargetConfigsService) Run() (*MonitorTargetConfigListData, error) {
	// 新增实例在服务重启前没有监控配置行，会让「监控对象」列表看不到它，
	// 默认指标模板也挂不上去（TiDB 新实例必须自动挂 tidb-default）。
	// 这里先幂等补齐一次：instance_id 上有唯一索引，并发重复插入会被拦下，不会出脏行。
	if created, err := ensureTargetConfigRows(); err != nil {
		global.App.Log.Warnf("[monitor] 补齐实例监控配置行失败: %v", err)
	} else if created > 0 {
		global.App.Log.Infof("[monitor] 已为 %d 个新实例补齐监控配置行", created)
	}
	rows, err := listTargetConfigRows(*s.MonitorListFilter)
	if err != nil {
		return nil, err
	}
	filters := DashboardFilters{}
	for _, row := range rows {
		if row.EnvironmentName != "" {
			filters.Environments = append(filters.Environments, FilterOption{Value: row.EnvironmentName, Label: row.EnvironmentName})
		}
		if row.DbType != "" {
			filters.DbTypes = append(filters.DbTypes, FilterOption{Value: row.DbType, Label: row.DbType})
		}
	}
	filters.Environments = uniqueFilterOptions(filters.Environments)
	filters.DbTypes = uniqueFilterOptions(filters.DbTypes)
	filters.Engines = EngineCapabilityList()

	data := &MonitorTargetConfigListData{
		Filters: filters,
		Targets: make([]MonitorTargetConfigItem, 0, len(rows)),
	}
	// 「部分可用」的下钻数据：一次性把本次列出的实例查完再回填，
	// 避免逐实例查询形成 N+1。
	// 传整行而不是只传 instance_id：过滤口径需要实例当前生效的模板与角色。
	degradations := listQualityDegradations(rows)

	for _, row := range rows {
		templateInfo := templateInfoForInstance(monitorInstanceRow{
			DbType:          row.DbType,
			MetricsTemplate: row.MetricsTemplate,
		})
		_, strategyName, strategyLabel, strategyFallback, strategyNote :=
			StrategyInfoForInstance(row.DbType, row.StrategyTemplateID)
		// 告警模板：同样下发"字面挂载 + 实际生效"，绑定弹窗勾选与回显都要用字面值
		alertTmpl, alertFallback, alertNote := resolveAlertTemplateForInstance(row.DbType, row.AlertTemplateID)
		alertName, alertLabel := "", ""
		if alertTmpl != nil {
			alertName = alertTmpl.Name
			alertLabel = firstNonEmpty(alertTmpl.DisplayName, alertTmpl.Name)
		}
		_, overrideTypes := parseStrategyOverrides(datatypes.JSON(row.StrategyOverrides))
		item := MonitorTargetConfigItem{
			ID:               row.ID,
			InstanceID:       row.InstanceID,
			Name:             firstNonEmpty(row.Remark, fmt.Sprintf("%s:%d", row.Hostname, row.Port)),
			Target:           fmt.Sprintf("%s:%d", row.Hostname, row.Port),
			DbType:           row.DbType,
			EnvironmentName:  row.EnvironmentName,
			Enabled:          row.Enabled == nil || *row.Enabled,
			Business:         row.Business,
			GroupName:        row.GroupName,
			DeployMode:       defaultString(row.DeployMode, "standalone"),
			PrometheusTarget: row.PrometheusTarget,
			ResourceSource:   defaultString(row.ResourceSource, "prometheus"),
			EngineSource:     defaultString(row.EngineSource, "direct"),
			// 实例未单独设置周期时，回落到平台采集频率（而不是一个写死的 60），
			// 这样页面显示的"采集周期"永远等于实际执行的周期。
			ScrapeIntervalSeconds:         defaultInt(row.ScrapeIntervalSeconds, GetScrapeConfig().IntervalSeconds),
			ScrapeIntervalFollowsPlatform: row.ScrapeIntervalSeconds <= 0,
			RetentionHours:                defaultInt(row.RetentionHours, defaultRetentionHours),
			TimeoutSeconds:                defaultInt(row.TimeoutSeconds, defaultTimeoutSeconds),
			RetryCount:                    defaultInt(row.RetryCount, 2),
			// 下发"实际生效的模板名"而不是配置里的字面值：编辑弹窗要能正确回显下拉选中项，
			// 老数据里的 default/oltp 也能落到真实模板上。
			MetricsTemplate:     firstNonEmpty(templateInfo.Name, row.MetricsTemplate),
			TemplateLabel:       firstNonEmpty(templateInfo.DisplayName, templateInfo.Name),
			TemplateMetricCount: templateInfo.MetricCount,
			TemplateFallback:    templateInfo.Fallback,
			TemplateState:       templateInfo.State,
			Labels:              decodeJSONStringArray(row.Labels),
			// 字面挂载值（NULL = 未挂载）：页面判断"挂在哪个模板"要用它，
			// 而不是下面那个"实际生效"的名字 —— 两者不一致正是要暴露出来的回退状态。
			StrategyTemplateID:    row.StrategyTemplateID,
			StrategyTemplateName:  strategyName,
			StrategyTemplateLabel: strategyLabel,
			StrategyFallback:      strategyFallback,
			StrategyFallbackNote:  strategyNote,
			StrategyOverrides:     overrideTypes,
			// 告警模板挂载（同上：字面挂载 + 实际生效一并下发）
			AlertTemplateID:       row.AlertTemplateID,
			AlertTemplateName:     alertName,
			AlertTemplateLabel:    alertLabel,
			AlertTemplateFallback: alertFallback,
			AlertFallbackNote:     alertNote,
			LastCollectStatus:     defaultString(row.LastCollectStatus, "never"),
			LastCollectMessage:    row.LastCollectMessage,
			CollectQuality:        defaultString(row.CollectQuality, QualityNoData),
			ConsecutiveFailures:   row.ConsecutiveFailures,
			Collectable:           engineImplemented(row.DbType),
			QualityDetails:        degradations[row.InstanceID],
		}
		if row.LastCollectAt != nil {
			item.LastCollectAt = time.Time(*row.LastCollectAt).Format(time.DateTime)
		}
		if capability, ok := engineCapabilityIndex[resolveEngine(row.DbType)]; ok {
			item.EngineNote = capability.Note
		}
		data.Targets = append(data.Targets, item)
	}
	return data, nil
}

// listTargetConfigRows 列出监控对象。
// 关键修复：不再硬编码 db_type IN ('MySQL','PostgreSQL')，改为引擎能力注册表白名单，
// 同时保留"已经建过配置行"的实例，避免历史配置在页面上凭空消失。
func listTargetConfigRows(filter forms.MonitorListFilter) ([]targetConfigRow, error) {
	rows := make([]targetConfigRow, 0)
	tx := global.App.DB.Table("insight_instances a").
		Select(`a.instance_id, a.hostname, a.port, a.db_type, a.remark,
			COALESCE(a.instance_role, '') as instance_role,
			COALESCE(env.name, '') as environment_name,
			mt.id, mt.enabled, COALESCE(mt.business, '') as business, COALESCE(mt.group_name, '') as group_name,
			COALESCE(mt.deploy_mode, 'standalone') as deploy_mode,
			COALESCE(mt.prometheus_target, '') as prometheus_target,
			COALESCE(mt.resource_source, 'prometheus') as resource_source,
			COALESCE(mt.engine_source, 'direct') as engine_source,
			COALESCE(mt.scrape_interval_seconds, 0) as scrape_interval_seconds,
			COALESCE(mt.retention_hours, 168) as retention_hours,
			COALESCE(mt.timeout_seconds, 5) as timeout_seconds,
			COALESCE(mt.retry_count, 2) as retry_count,
			COALESCE(mt.metrics_template, '') as metrics_template,
			mt.strategy_template_id, mt.strategy_overrides, mt.alert_template_id,
			mt.last_collect_at,
			COALESCE(mt.last_collect_status, 'never') as last_collect_status,
			COALESCE(mt.last_collect_message, '') as last_collect_message,
			COALESCE(mt.collect_quality, 'no_data') as collect_quality,
			COALESCE(mt.consecutive_failures, 0) as consecutive_failures,
			mt.labels`).
		Joins("left join insight_instance_environments env on env.id = a.environment").
		Joins("left join insight_monitor_target_configs mt on mt.instance_id = a.instance_id").
		Where("LOWER(a.db_type) IN ? OR mt.id IS NOT NULL", monitorableEngineKeys())
	if value := strings.TrimSpace(filter.Search); value != "" {
		like := "%" + value + "%"
		tx = tx.Where("(a.hostname like ? or a.remark like ? or a.instance_id like ?)", like, like, like)
	}
	if value := strings.TrimSpace(filter.Environment); value != "" {
		tx = tx.Where("env.name = ?", value)
	}
	if value := strings.TrimSpace(filter.DbType); value != "" {
		tx = tx.Where("LOWER(a.db_type) = ?", resolveEngine(value))
	}
	if value := strings.TrimSpace(filter.Business); value != "" {
		tx = tx.Where("mt.business = ?", value)
	}
	if value := strings.TrimSpace(filter.GroupName); value != "" {
		tx = tx.Where("mt.group_name = ?", value)
	}
	if value := strings.TrimSpace(filter.InstanceID); value != "" {
		tx = tx.Where("a.instance_id = ?", value)
	}
	if err := tx.Order("a.id desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// qualityDetailScanLimit 下钻明细的总量上限。
// 单实例指标数受字典约束（数十项），但带子维度的指标会展开成多行，
// 极端情况下可能膨胀，这里设一道闸门保护列表接口的响应体积。
const qualityDetailScanLimit = 2000

// listQualityDegradations 批量取出各实例「非正常」指标的降级明细。
//
// 存在的理由：监控对象列表里"部分可用"这个标签此前是个死胡同 ——
// 用户看到标签知道不太对，但不知道缺哪几项、是权限问题还是扩展没装，
// 最后只能忽略它。标签 + 不可下钻 = 纯噪音。
//
// 失败时返回空 map 而不是 error：这是页面增强信息，
// 不该因为查不到明细就让整个列表接口失败。
//
// 过滤口径（2026-10-27 修正）：此前这里只对 metric_latest 按
// instance_id IN ? AND quality <> 'ok' 全表取，完全没做"该实例当前生效模板"的过滤。
// 于是裁剪模板 / 换绑模板之后，那些已经不再采集的历史行会被永久留在明细里报
// 「取不到」—— 表现就是"模板里没勾集群指标，却一直提示集群指标获取不到"。
// 现在与卡片渲染共用 metricVisibleUnderCurrentConfig，两处口径必须一致。
func listQualityDegradations(rows []targetConfigRow) map[string][]QualityDetailItem {
	result := make(map[string][]QualityDetailItem, len(rows))
	if len(rows) == 0 {
		return result
	}
	instanceIDs := make([]string, 0, len(rows))
	scopeByInstance := make(map[string]targetConfigRow, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.InstanceID) == "" {
			continue
		}
		instanceIDs = append(instanceIDs, row.InstanceID)
		scopeByInstance[row.InstanceID] = row
	}
	if len(instanceIDs) == 0 {
		return result
	}
	type degradationRow struct {
		InstanceID string
		MetricKey  string
		Quality    string
		Reason     string
		SubDims    string
	}
	latestRows := make([]degradationRow, 0, 32)
	err := global.App.DB.Table("insight_monitor_metric_latest").
		Select("instance_id, metric_key, quality, reason, sub_dims").
		Where("instance_id IN ? AND quality <> ?", instanceIDs, QualityOK).
		Limit(qualityDetailScanLimit).
		Scan(&latestRows).Error
	if err != nil {
		global.App.Log.Warnf("scan monitor quality degradations failed: %v", err)
		return result
	}
	for _, item := range latestRows {
		scope, ok := scopeByInstance[item.InstanceID]
		if !ok {
			continue
		}
		// 模板外 / 本角色不适用的指标不算"降级"：它压根不在当前配置的采集范围里，
		// 报出来只会被读成"配了但取不到"，方向是错的。
		if !metricVisibleUnderCurrentConfig(scope.DbType, scope.MetricsTemplate, scope.InstanceRole, item.MetricKey) {
			continue
		}
		result[item.InstanceID] = append(result[item.InstanceID], QualityDetailItem{
			MetricKey:   item.MetricKey,
			MetricLabel: metricLabel(item.MetricKey),
			Quality:     item.Quality,
			Reason:      strings.TrimSpace(item.Reason),
			SubDims:     strings.TrimSpace(item.SubDims),
		})
	}
	return result
}

func uniqueFilterOptions(items []FilterOption) []FilterOption {
	seen := map[string]struct{}{}
	result := make([]FilterOption, 0, len(items))
	for _, item := range items {
		if item.Value == "" {
			continue
		}
		if _, ok := seen[item.Value]; ok {
			continue
		}
		seen[item.Value] = struct{}{}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}

func (s *UpsertTargetConfigService) Run() error {
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return fmt.Errorf("实例ID不能为空")
	}
	var instance struct {
		InstanceID string
		DbType     string
	}
	if queryErr := global.App.DB.Table("insight_instances").
		Select("instance_id, db_type").
		Where("instance_id = ?", instanceID).
		Take(&instance).Error; queryErr != nil {
		if queryErr == gorm.ErrRecordNotFound {
			return fmt.Errorf("实例不存在")
		}
		return queryErr
	}
	// 旧版在此硬性拒绝非 MySQL/PostgreSQL，导致 TiDB / Doris / ClickHouse 等
	// 已实现采集的引擎无法纳入监控。现在改为只校验"是否具备采集能力"。
	if !engineImplemented(instance.DbType) {
		return fmt.Errorf("实例类型 %s 暂未实现监控采集，无法保存监控配置", instance.DbType)
	}
	deployMode := strings.TrimSpace(s.DeployMode)
	if deployMode == "" {
		deployMode = "standalone"
	}
	// 实例级采集周期：0 = 跟随平台周期（推荐）；显式覆盖时必须 ≥15 秒
	// （15 以下会被调度器 MinScrapeIntervalSeconds 钳掉，存进去只会造成假象）。
	if s.ScrapeIntervalSeconds < 0 || (s.ScrapeIntervalSeconds > 0 && s.ScrapeIntervalSeconds < MinScrapeIntervalSeconds) {
		return fmt.Errorf("采集周期 %d 秒非法：0 表示跟随平台周期，显式指定时不能小于 %d 秒",
			s.ScrapeIntervalSeconds, MinScrapeIntervalSeconds)
	}
	// 模板绑定要落到"实际存在的模板名"上：空值与旧版的 default 都归一化为该类型默认模板，
	// 写错的名字直接拒绝而不是静默存进去（否则采集范围会悄悄变成默认模板）。
	templateName, err := normalizeTemplateBinding(instance.DbType, s.MetricsTemplate)
	if err != nil {
		return err
	}
	payload := monitorModels.InsightMonitorTargetConfigs{
		Model:                 &commonModels.Model{},
		InstanceID:            instanceID,
		Enabled:               s.Enabled,
		DBType:                commonModels.EnumType(instance.DbType),
		Business:              strings.TrimSpace(s.Business),
		GroupName:             strings.TrimSpace(s.GroupName),
		DeployMode:            deployMode,
		PrometheusTarget:      strings.TrimSpace(s.PrometheusTarget),
		ResourceSource:        strings.TrimSpace(s.ResourceSource),
		EngineSource:          strings.TrimSpace(s.EngineSource),
		ScrapeIntervalSeconds: s.ScrapeIntervalSeconds,
		RetentionHours:        s.RetentionHours,
		TimeoutSeconds:        s.TimeoutSeconds,
		RetryCount:            s.RetryCount,
		MetricsTemplate:       templateName,
		Labels:                mustJSONSlice(s.Labels),
	}
	var existing monitorModels.InsightMonitorTargetConfigs
	err = global.App.DB.Where("instance_id = ?", instanceID).Take(&existing).Error
	if err == nil {
		updateErr := global.App.DB.Model(&existing).Updates(map[string]any{
			"enabled":                 payload.Enabled,
			"db_type":                 payload.DBType,
			"business":                payload.Business,
			"group_name":              payload.GroupName,
			"deploy_mode":             payload.DeployMode,
			"prometheus_target":       payload.PrometheusTarget,
			"resource_source":         payload.ResourceSource,
			"engine_source":           payload.EngineSource,
			"scrape_interval_seconds": payload.ScrapeIntervalSeconds,
			"retention_hours":         payload.RetentionHours,
			"timeout_seconds":         payload.TimeoutSeconds,
			"retry_count":             payload.RetryCount,
			"metrics_template":        payload.MetricsTemplate,
			"labels":                  payload.Labels,
		}).Error
		// 绑定关系可能变了：模板缓存失效，避免页面统计停在旧归属上
		if updateErr == nil {
			invalidateMetricTemplateCache()
		}
		return updateErr
	}
	if err == gorm.ErrRecordNotFound {
		payload.LastCollectStatus = "never"
		payload.CollectQuality = QualityNoData
		if createErr := global.App.DB.Create(&payload).Error; createErr != nil {
			return createErr
		}
		invalidateMetricTemplateCache()
		return nil
	}
	return err
}

// ============================================================================
// 告警规则 CRUD
// ============================================================================

func (s *GetRuleListService) Run() (*MonitorRuleListData, error) {
	if err := pruneStaleAlertRules(); err != nil {
		return nil, err
	}
	var rows []monitorModels.InsightMonitorAlertRules
	tx := global.App.DB.Model(&monitorModels.InsightMonitorAlertRules{}).Order("db_type asc, id asc")
	if value := strings.TrimSpace(s.DbType); value != "" {
		tx = tx.Where("db_type = ?", value)
	}
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	// 模板名回填：按模板 ID 一次查全，避免逐行 N+1
	tplNames := map[uint64]string{}
	tpls := make([]monitorModels.InsightMonitorAlertTemplates, 0)
	if err := global.App.DB.Find(&tpls).Error; err == nil {
		for _, t := range tpls {
			tplNames[t.ID] = firstNonEmpty(t.DisplayName, t.Name)
		}
	}
	data := &MonitorRuleListData{
		Filters: DashboardFilters{Engines: EngineCapabilityList()},
		Rules:   make([]MonitorRuleItem, 0, len(rows)),
	}
	dbTypes := make([]FilterOption, 0)
	for _, row := range rows {
		canonical := canonicalMetricKey(row.MetricKey)
		_, _, unit, _ := metricDisplayName(canonical)
		data.Rules = append(data.Rules, MonitorRuleItem{
			ID:              row.ID,
			Name:            row.Name,
			DBType:          string(row.DBType),
			RuleType:        row.RuleType,
			ConditionType:   defaultString(row.ConditionType, "threshold"),
			MetricKey:       canonical,
			MetricLabel:     metricLabel(canonical),
			Operator:        row.Operator,
			Threshold:       row.Threshold,
			Unit:            unit,
			HoldMinutes:     row.HoldMinutes,
			ForSeconds:      row.ForSeconds,
			RecoverySeconds: row.RecoverySeconds,
			NoDataBehavior:  defaultString(row.NoDataBehavior, "ignore"),
			AlertLevel:      row.AlertLevel,
			Severity:        defaultString(row.Severity, severityFromLevel(row.AlertLevel)),
			SilenceMinutes:  row.SilenceMinutes,
			GroupKey:        row.GroupKey,
			ScopeType:       row.ScopeType,
			ScopeValue:      row.ScopeValue,
			NotifyChannels:  decodeJSONStringArray(row.NotifyChannels),
			SilencePolicy:   decodeJSONStringArray(row.SilencePolicy),
			Description:     row.Description,
			Enabled:         row.Enabled,
			Builtin:         isBuiltinRuleName(row.Name),
			TemplateID:      row.TemplateID,
			TemplateName:    tplNames[row.TemplateID],
		})
		dbTypes = append(dbTypes, FilterOption{Value: string(row.DBType), Label: string(row.DBType)})
	}
	data.Filters.DbTypes = uniqueFilterOptions(dbTypes)
	return data, nil
}

// isBuiltinRuleName 历史默认规则名由已下线的 buildDefaultAlertRules 生成，形如
// "MySQL 复制延迟 >= 300s"，据此在页面上标注"内置"。
// 播种已停止（不再自动补规则），但存量规则仍在，标注逻辑保留。
func isBuiltinRuleName(name string) bool {
	trimmed := strings.TrimSpace(name)
	for _, item := range engineCapabilities {
		if strings.HasPrefix(trimmed, item.Label+" ") {
			return true
		}
	}
	return false
}

func (s *CreateRuleService) Run() error {
	return saveRule(0, s.SaveRuleForm)
}

func (s *UpdateRuleService) Run() error {
	return saveRule(s.ID, s.SaveRuleForm)
}

func (s *DeleteRuleService) Run() error {
	return global.App.DB.Delete(&monitorModels.InsightMonitorAlertRules{}, s.ID).Error
}

func saveRule(id uint64, form *forms.SaveRuleForm) error {
	if !engineImplemented(form.DBType) {
		return fmt.Errorf("暂不支持为 %s 配置告警规则（该引擎尚未实现监控采集）", form.DBType)
	}
	metricKey := canonicalMetricKey(form.MetricKey)
	if metricKey == "" {
		return fmt.Errorf("指标键不能为空")
	}
	conditionType := defaultString(form.ConditionType, "threshold")
	// severity 是 alert_level 的派生产物（高→P1 / 中→P2 / 低→P3），不接受外部传入。
	//
	// 历史实现是 defaultString(form.Severity, severityFromLevel(form.AlertLevel))，
	// 而前端编辑规则时会把创建时落库的 severity 一并回填提交，导致"把级别从『中』改成『高』
	// 之后 severity 仍停在 P2"—— 配置页列表显示 P2，告警事件却按『高』派生出 P1，两处对不上。
	// 告警评估链路（evaluateAlerts → upsertAlertEvent）本来就是拿 rule.AlertLevel 现算 severity，
	// 规则表这一列只在列表展示里用到，因此它必须与 alert_level 保持严格派生关系，不能独立写入。
	//
	// 存量不一致数据由 monitor_schema.go 的启动期对账 SQL 兜底刷平。
	severity := severityFromLevel(form.AlertLevel)
	payload := monitorModels.InsightMonitorAlertRules{
		Model:           &commonModels.Model{ID: id},
		Name:            strings.TrimSpace(form.Name),
		DBType:          commonModels.EnumType(form.DBType),
		RuleType:        defaultString(form.RuleType, "threshold"),
		ConditionType:   conditionType,
		MetricKey:       metricKey,
		Operator:        form.Operator,
		Threshold:       form.Threshold,
		HoldMinutes:     form.HoldMinutes,
		ForSeconds:      form.ForSeconds,
		RecoverySeconds: defaultInt(form.RecoverySeconds, defaultRecoverySeconds),
		NoDataBehavior:  defaultString(form.NoDataBehavior, "ignore"),
		AlertLevel:      form.AlertLevel,
		Severity:        severity,
		SilenceMinutes:  defaultInt(form.SilenceMinutes, 30),
		GroupKey:        strings.TrimSpace(form.GroupKey),
		ScopeType:       defaultString(form.ScopeType, "global"),
		ScopeValue:      strings.TrimSpace(form.ScopeValue),
		NotifyChannels:  mustJSONSlice(form.NotifyChannels),
		SilencePolicy:   mustJSONSlice(form.SilencePolicy),
		Description:     strings.TrimSpace(form.Description),
		Enabled:         form.Enabled,
	}
	if id == 0 {
		// 规则是模板的组成部分：走旧规则接口新建的规则也必须归入该类型的默认模板，
		// 否则会成为 template_id=0 的无主规则（评估链按模板取规则，永远轮不到它）。
		if payload.TemplateID == 0 {
			if err := EnsureAlertTemplates(global.App.DB); err == nil {
				if tmpl := defaultAlertTemplate(string(payload.DBType)); tmpl != nil {
					payload.TemplateID = tmpl.ID
				}
			}
		}
		return global.App.DB.Create(&payload).Error
	}
	return global.App.DB.Model(&monitorModels.InsightMonitorAlertRules{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"name":             payload.Name,
			"db_type":          payload.DBType,
			"rule_type":        payload.RuleType,
			"condition_type":   payload.ConditionType,
			"metric_key":       payload.MetricKey,
			"operator":         payload.Operator,
			"threshold":        payload.Threshold,
			"hold_minutes":     payload.HoldMinutes,
			"for_seconds":      payload.ForSeconds,
			"recovery_seconds": payload.RecoverySeconds,
			"no_data_behavior": payload.NoDataBehavior,
			"alert_level":      payload.AlertLevel,
			"severity":         payload.Severity,
			"silence_minutes":  payload.SilenceMinutes,
			"group_key":        payload.GroupKey,
			"scope_type":       payload.ScopeType,
			"scope_value":      payload.ScopeValue,
			"notify_channels":  payload.NotifyChannels,
			"silence_policy":   payload.SilencePolicy,
			"description":      payload.Description,
			"enabled":          payload.Enabled,
		}).Error
}

func mustJSONSlice(values []string) datatypes.JSON {
	if len(values) == 0 {
		return nil
	}
	data, _ := json.Marshal(values)
	return datatypes.JSON(data)
}

func decodeJSONStringArray(value []byte) []string {
	if len(value) == 0 {
		return []string{}
	}
	var items []string
	if err := json.Unmarshal(value, &items); err != nil {
		return []string{}
	}
	return items
}
