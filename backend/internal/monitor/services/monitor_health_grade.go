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
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================================
// 实例健康分级：唯一数据源 + 评估引擎
//
// 背景：改造前"健康 / 风险 / 故障 / 未采集"的判定口径写死在 classifyScreenCard 里 ——
// 哪些采集状态算故障、多少秒算过期、有告警就算风险，全是代码常量。想让"10 条慢 SQL"
// 算风险，只能改代码重新发布。
//
// 现在的形态：insight_monitor_health_grade_settings（单行 JSON）是唯一数据源，
// 三段配置全部由它承载，评估引擎不持有任何业务口径：
//   1. levels  —— 等级定义：P0~P3 各自的严重度、名称、以及该等级对应哪种健康状态
//   2. results —— 结果项定义：每个结果项归属哪个等级（结果 → 等级的映射）
//   3. rules   —— 判定规则：对实例事实做条件判断，命中即产出结果项
//
// 评估链路（一次调用走完，见 EvaluateInstanceHealth）：
//   实例卡片 → 提取事实 → 规则匹配 → 命中结果项 → 结果项查等级 → 等级查健康状态
//
// 默认策略：库里没配置（或用户没自定义过）时用内置最佳实践默认；用户保存后一律以用户
// 配置为准；内置默认版本高于库里记录版本且用户没自定义过，自动跟随升级。
// ============================================================================

const (
	// builtinHealthGradeVersion 内置默认配置版本。
	// 只有"用户未自定义（customized=false）"的行才会跟随内置版本升级 —— 这样改默认值后，
	// 老部署不会因为库里躺着一份旧快照而永远卡在旧口径上。
	builtinHealthGradeVersion = 1
	healthGradeRowID          = 1
	// healthGradeCacheTTL 进程内缓存时长，与采集频率配置同款：保存后最多 10 秒全进程生效。
	healthGradeCacheTTL = 10 * time.Second

	// defaultDataStaleSeconds 数据过期阈值的兜底值（仅当配置里取不到 freshness 条件时用）。
	// 真正的生效值在配置规则 data_stale 的 freshness_seconds 条件里，改那里即可。
	defaultDataStaleSeconds = 180

	// healthFactMetricPrefix / healthFactMetricQualityPrefix 动态事实前缀。
	// 事实键形如 metric:perf.qps、metric_quality:res.cpu，指标键由配置指定，
	// 因此新增指标无需改评估代码 —— 只要字典里有、卡片上采到了，规则就能引用。
	healthFactMetricPrefix        = "metric:"
	healthFactMetricQualityPrefix = "metric_quality:"
)

// 健康状态取值集合。这四个值是大屏四分类的固有取值（ScreenStatusCounts 与前端口径），
// 等级定义只能在这四个里选，配置校验会拦住其余值。
const (
	HealthStateHealthy     = "healthy"
	HealthStateRisk        = "risk"
	HealthStateFault       = "fault"
	HealthStateUncollected = "uncollected"
)

// ---------------------------------------------------------------------------
// 配置数据结构
// ---------------------------------------------------------------------------

// HealthLevelDef 一个等级的定义。等级是"结果项 → 健康状态"的中间层：
// 结果项只声明自己属于哪个等级，至于该等级算健康还是故障，由这里的 health_state 决定。
// 因此"把 P3 从提示改成风险"只需改这一行，不必逐个改结果项。
type HealthLevelDef struct {
	Code string `json:"code"` // P0 / P1 / P2 / P3（可自定义扩展，code 唯一即可）
	Name string `json:"name"` // 中文名：致命 / 严重 / 一般 / 提示
	// Rank 严重度排序，0 最严重。多个结果项同时命中时取 rank 最小者作为实例最终等级。
	Rank int `json:"rank"`
	// HealthState 该等级对应的大屏健康状态：healthy / risk / fault / uncollected。
	HealthState string `json:"health_state"`
	Color       string `json:"color,omitempty"`       // 前端配色（可选）
	Response    string `json:"response,omitempty"`    // 值班响应要求（可选，纯展示）
	Description string `json:"description,omitempty"` // 等级含义说明
}

// HealthResultDef 一个结果项（评估产出的"结论单元"）。
// 结果项是规则与等级之间的桥梁：规则负责"命中"，结果项负责"这条命中算多严重"。
type HealthResultDef struct {
	Code     string `json:"code"`     // 结果项唯一标识，如 instance_down
	Name     string `json:"name"`     // 界面展示名
	Level    string `json:"level"`    // 归属等级 code（必须在 levels 中存在）
	Category string `json:"category"` // availability / capacity / performance / collect
	Advice   string `json:"advice,omitempty"`
	// HealthState 可选覆盖：只有少数结果项需要。典型场景是"数据过期"——
	// 它严重度归 P2，但状态必须是 uncollected（数据不可信时不能给健康/风险结论）。
	// 留空则继承所属等级的 health_state。
	HealthState string `json:"health_state,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// HealthCondition 单条条件。Fact 为事实键，Op 为比较符，Value 为比较值
// （in/not_in/between 时为数组；exists/absent 时可为 null）。
type HealthCondition struct {
	Fact  string `json:"fact"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
	// Label 该条件的人类可读说明，用于生成判定依据（basis）文案，如"磁盘使用率"。
	Label string `json:"label,omitempty"`
}

// HealthRuleDef 一条判定规则。Any 内任一条件满足即可（OR），All 内必须全部满足（AND），
// Any 与 All 之间再取 AND；两者都为空视为不匹配。
type HealthRuleDef struct {
	Code     string            `json:"code"`
	Result   string            `json:"result"` // 命中后产出的结果项 code
	Enabled  bool              `json:"enabled"`
	Priority int               `json:"priority"` // 升序评估，越小越先
	Any      []HealthCondition `json:"any,omitempty"`
	All      []HealthCondition `json:"all,omitempty"`
	// StopOnHit 命中后是否终止后续规则。默认 false —— 收集全部命中项，
	// 前端才能展示"这个实例为什么被判为 P1"的完整清单。
	StopOnHit bool `json:"stop_on_hit,omitempty"`
	// Message 命中说明模板，支持 {observed} {threshold} {label} {fact} 占位符。
	Message     string `json:"message,omitempty"`
	Description string `json:"description,omitempty"`
}

// HealthFactDef 事实字典条目。事实是系统能提供的"观测能力"（与策略无关），
// 所以枚举在代码里；规则引用事实时必须命中这张表，否则校验报错。
type HealthFactDef struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Type    string   `json:"type"` // string / number / list
	Ops     []string `json:"ops"`  // 该事实支持的比较符
	Example string   `json:"example,omitempty"`
	// Dynamic 为 true 时事实键需带参数，形如 metric:<指标键>。
	Dynamic   bool     `json:"dynamic"`
	ParamHint string   `json:"param_hint,omitempty"`
	Values    []string `json:"values,omitempty"` // 枚举型事实的候选值（供前端下拉）
}

// HealthGradeConfig 完整分级配置。存进单行表的一整个 JSON，也是接口的读写载体。
type HealthGradeConfig struct {
	Version       int               `json:"version"`
	Name          string            `json:"name,omitempty"`
	Levels        []HealthLevelDef  `json:"levels"`
	Results       []HealthResultDef `json:"results"`
	Rules         []HealthRuleDef   `json:"rules"`
	Fallback      string            `json:"fallback"` // 无任何规则命中时使用的结果项 code
	Facts         []HealthFactDef   `json:"facts"`    // 事实字典（只读，随配置下发供前端下拉）
	Customized    bool              `json:"customized"`
	Source        string            `json:"source"`   // builtin / db
	Degraded      bool              `json:"degraded"` // 是否因配置非法而降级运行
	DegradeReason string            `json:"degrade_reason,omitempty"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
	UpdatedBy     string            `json:"updated_by,omitempty"`
	ApplyNote     string            `json:"apply_note,omitempty"`
}

// HealthFinding 一次规则命中。Basis 是给人看的判定依据（"XX 当前值 A，满足 >= B"）。
type HealthFinding struct {
	RuleCode    string   `json:"rule_code"`
	ResultCode  string   `json:"result_code"`
	ResultName  string   `json:"result_name"`
	Level       string   `json:"level"`
	LevelName   string   `json:"level_name"`
	Rank        int      `json:"rank"`
	HealthState string   `json:"health_state"`
	Category    string   `json:"category"`
	Message     string   `json:"message"`
	Basis       string   `json:"basis"`
	Advice      string   `json:"advice,omitempty"`
	Fact        string   `json:"fact,omitempty"`
	Observed    *float64 `json:"observed_value,omitempty"`
	Threshold   *float64 `json:"threshold,omitempty"`
}

// HealthGradeResult 单个实例的评估输出。
// HealthState 直接就是大屏四分类取值，ScreenClass 与其同值（供前端直接消费）。
type HealthGradeResult struct {
	InstanceID    string          `json:"instance_id,omitempty"`
	HealthState   string          `json:"health_state"`
	ScreenClass   string          `json:"screen_class"`
	Level         string          `json:"level"`
	LevelName     string          `json:"level_name"`
	Rank          int             `json:"rank"`
	Findings      []HealthFinding `json:"findings"`
	Source        string          `json:"source"`
	Degraded      bool            `json:"degraded"`
	DegradeReason string          `json:"degrade_reason,omitempty"`
}

// ---------------------------------------------------------------------------
// 事实字典：系统能提供的观测能力（与策略无关，故枚举在代码里）
// ---------------------------------------------------------------------------

var healthFactDefs = []HealthFactDef{
	{Key: "collect_status", Name: "采集状态", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"},
		Example: "failed", Values: []string{"success", "partial", "failed", "never"}},
	{Key: "collect_quality", Name: "采集质量", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"},
		Example: "error", Values: []string{"ok", "partial", "error", "no_data", "stale", "unsupported"}},
	{Key: "card_status", Name: "卡片状态", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"},
		Example: "warning", Values: []string{"healthy", "warning"}},
	{Key: "consecutive_failures", Name: "连续采集失败次数", Type: "number", Ops: []string{"eq", "ne", "gt", "gte", "lt", "lte", "between"}, Example: "3"},
	{Key: "freshness_seconds", Name: "数据新鲜度(秒)", Type: "number", Ops: []string{"gt", "gte", "lt", "lte", "between"}, Example: "180"},
	{Key: "active_alerts", Name: "未忽略的待认领告警数", Type: "number", Ops: []string{"eq", "ne", "gt", "gte", "lt", "lte", "between"}, Example: "1"},
	{Key: "alert_severity_max", Name: "最严重告警级别", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"},
		Example: "P1", Values: []string{"P1", "P2", "P3"}},
	{Key: "alert_metrics", Name: "待认领告警指标集合", Type: "list", Ops: []string{"contains"}, Example: "perf.qps"},
	{Key: "deploy_mode", Name: "部署形态", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"},
		Example: "master_slave", Values: []string{"standalone", "master_slave", "cluster"}},
	{Key: "db_type", Name: "数据库类型", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"}, Example: "MySQL"},
	{Key: "metric", Name: "指标读数", Type: "number", Ops: []string{"eq", "ne", "gt", "gte", "lt", "lte", "between", "exists", "absent"},
		Dynamic: true, ParamHint: "指标键，如 perf.qps；写成 metric:perf.qps"},
	{Key: "metric_quality", Name: "指标数据质量", Type: "string", Ops: []string{"eq", "ne", "in", "not_in"},
		Dynamic: true, ParamHint: "指标键，如 res.cpu；写成 metric_quality:res.cpu",
		Values: []string{"ok", "partial", "error", "no_data", "stale", "unsupported"}},
}

// HealthGradeFacts 暴露事实字典（接口下发，供前端做条件编辑下拉）。
func HealthGradeFacts() []HealthFactDef {
	out := make([]HealthFactDef, len(healthFactDefs))
	copy(out, healthFactDefs)
	return out
}

// ---------------------------------------------------------------------------
// 内置默认配置（最佳实践）
// ---------------------------------------------------------------------------

// builtinHealthGradeConfig 内置默认分级策略。
//
// 取值理由（改动这里等于改动全局默认口径，务必同步单测与文档）：
//
//	P0 故障  —— 只有"数据彻底不可信"：实例连不上、采集报错。这类必须立刻处置。
//	P1 严重  —— 核心能力受损或容量即将耗尽：复制线程停了、P1 告警、磁盘/连接数打满。
//	P2 一般  —— 明确偏离基线或数据可信度下降：有待处理告警、连续失败、资源压力、慢查询激增。
//	P3 提示  —— 轻微偏离，不改变健康结论：少量慢查询、命中率偏低、长事务、锁等待。
//
// 关于"少量慢 SQL 算不算风险"：默认放在 P3（提示），因此大屏仍显示健康，
// 但风险明细和实例详情能看到这条提示。想要"有慢 SQL 就是风险"，把结果项
// slow_queries_elevated 的 level 改成 P2，或把 P3 的 health_state 改成 risk 即可 ——
// 都不需要发版。
func builtinHealthGradeConfig() HealthGradeConfig {
	return HealthGradeConfig{
		Version:  builtinHealthGradeVersion,
		Name:     "内置默认分级策略",
		Fallback: "healthy",
		Facts:    HealthGradeFacts(),
		Levels: []HealthLevelDef{
			{Code: "P0", Name: "致命", Rank: 0, HealthState: HealthStateFault, Color: "#cf1322",
				Response: "立即处置", Description: "实例不可用或数据彻底不可信，业务已受影响或无法观测"},
			{Code: "P1", Name: "严重", Rank: 1, HealthState: HealthStateRisk, Color: "#fa8c16",
				Response: "1 小时内处置", Description: "核心能力受损或容量即将耗尽，短时间内会演变成故障"},
			{Code: "P2", Name: "一般", Rank: 2, HealthState: HealthStateRisk, Color: "#faad14",
				Response: "当班次内处置", Description: "明确偏离基线，或数据可信度下降导致结论可能不完整"},
			{Code: "P3", Name: "提示", Rank: 3, HealthState: HealthStateHealthy, Color: "#52c41a",
				Response: "周维度复盘", Description: "轻微偏离，记录即可，不改变健康结论"},
		},
		Results: []HealthResultDef{
			// --- P0 ---
			{Code: "instance_down", Name: "实例不可达", Level: "P0", Category: "availability", Enabled: true,
				Advice: "检查实例进程、端口连通性、网络策略与采集账号权限"},
			{Code: "collect_failed", Name: "采集失败", Level: "P0", Category: "availability", Enabled: true,
				Advice: "检查采集账号权限与实例连通性，确认采集任务在运行"},
			// --- P1 ---
			{Code: "alert_p1_active", Name: "存在 P1 级待认领告警", Level: "P1", Category: "availability", Enabled: true,
				Advice: "按告警处置建议立即处理"},
			{Code: "replication_stopped", Name: "复制线程停止", Level: "P1", Category: "availability", Enabled: true,
				Advice: "检查主从网络与复制账号，必要时重建复制"},
			{Code: "capacity_exhausted", Name: "容量即将耗尽", Level: "P1", Category: "capacity", Enabled: true,
				Advice: "优先处理磁盘与连接数：清理或扩容"},
			// --- P2 ---
			{Code: "collect_degraded", Name: "采集连续失败", Level: "P2", Category: "availability", Enabled: true,
				Advice: "连续失败 3 次以上，当前展示的是上一次成功采集的旧值"},
			// 数据过期严重度是 P2，但状态必须是 uncollected —— 数据不可信时不给健康/风险结论
			{Code: "data_stale", Name: "数据过期或未采集", Level: "P2", Category: "availability", Enabled: true,
				HealthState: HealthStateUncollected,
				Advice:      "确认采集任务是否运行、实例是否仍启用监控"},
			{Code: "collect_warning", Name: "采集质量降级", Level: "P2", Category: "availability", Enabled: true,
				Advice: "部分指标不可用，当前结论可能不完整"},
			{Code: "alert_active", Name: "存在待认领告警", Level: "P2", Category: "availability", Enabled: true,
				Advice: "按告警处置建议处理"},
			{Code: "replication_lag_high", Name: "复制延迟过高", Level: "P2", Category: "availability", Enabled: true,
				Advice: "排查大事务、从库负载与网络延迟"},
			{Code: "resource_pressure", Name: "主机资源压力", Level: "P2", Category: "capacity", Enabled: true,
				Advice: "关注 CPU / 内存 / 磁盘 / IO 趋势，必要时扩容"},
			{Code: "slow_query_storm", Name: "慢查询激增", Level: "P2", Category: "performance", Enabled: true,
				Advice: "抓取慢日志定位 Top SQL"},
			{Code: "conn_error_high", Name: "连接错误速率偏高", Level: "P2", Category: "performance", Enabled: true,
				Advice: "检查应用账号、密码与连接池配置（连接认证失败会瞬间拉高该值）"},
			// --- P3 ---
			{Code: "slow_queries_elevated", Name: "存在慢查询", Level: "P3", Category: "performance", Enabled: true,
				Advice: "低速率慢查询，纳入周维度优化"},
			{Code: "cache_hit_low", Name: "缓存命中率偏低", Level: "P3", Category: "performance", Enabled: true,
				Advice: "评估热数据规模与缓冲池配置"},
			{Code: "long_trx_present", Name: "存在长事务", Level: "P3", Category: "performance", Enabled: true,
				Advice: "确认是否有未提交事务，避免undo堆积"},
			{Code: "lock_wait_present", Name: "存在锁等待", Level: "P3", Category: "performance", Enabled: true,
				Advice: "定位锁持有者会话"},
			// --- 兜底：无任何规则命中 ---
			{Code: "healthy", Name: "健康", Level: "P3", Category: "availability", Enabled: true,
				Advice: "无需处置"},
		},
		Rules: []HealthRuleDef{
			{Code: "instance_down", Result: "instance_down", Enabled: true, Priority: 10,
				Message: "实例不可达（观测值 {observed}，期望 1）",
				Any:     []HealthCondition{{Fact: "metric:avail.up", Op: "eq", Value: 0, Label: "实例可达性"}}},
			{Code: "collect_failed", Result: "collect_failed", Enabled: true, Priority: 20,
				Message: "采集失败，无可用监控数据（{label}={observed}）",
				Any: []HealthCondition{
					{Fact: "collect_status", Op: "in", Value: []any{"failed"}, Label: "采集状态"},
					{Fact: "collect_quality", Op: "in", Value: []any{"error"}, Label: "采集质量"},
				}},
			// 复制类规则带 deploy_mode 前置条件：standalone 实例没有复制，
			// 不加这个条件会让单机实例永久背着一条假故障（这是"健康 0 个"的典型成因）
			{Code: "replication_stopped", Result: "replication_stopped", Enabled: true, Priority: 30,
				Message: "复制线程未运行（{label}={observed}，期望 1）",
				All:     []HealthCondition{{Fact: "deploy_mode", Op: "in", Value: []any{"master_slave", "cluster"}, Label: "部署形态"}},
				Any: []HealthCondition{
					{Fact: "metric:repl.io_running", Op: "eq", Value: 0, Label: "复制 IO 线程"},
					{Fact: "metric:repl.sql_running", Op: "eq", Value: 0, Label: "复制 SQL 线程"},
				}},
			{Code: "alert_p1_active", Result: "alert_p1_active", Enabled: true, Priority: 40,
				Message: "存在 P1 级待认领告警（{label}={observed}）",
				Any:     []HealthCondition{{Fact: "alert_severity_max", Op: "in", Value: []any{"P1"}, Label: "最严重告警级别"}}},
			{Code: "capacity_exhausted", Result: "capacity_exhausted", Enabled: true, Priority: 50,
				Message: "容量项接近耗尽：{label}={observed}%，达到 {threshold}%",
				Any: []HealthCondition{
					{Fact: "metric:res.disk", Op: "gte", Value: 95, Label: "磁盘使用率"},
					{Fact: "metric:cap.conn_used_pct", Op: "gte", Value: 95, Label: "活跃连接占比"},
					{Fact: "metric:cap.buffer_pool_used_pct", Op: "gte", Value: 98, Label: "Buffer Pool 使用率"},
					{Fact: "metric:res.fd_used_pct", Op: "gte", Value: 95, Label: "文件句柄使用率"},
				}},
			// 连续失败但本轮没彻底失败（彻底失败已由 collect_failed 判 P0），展示的是旧值
			{Code: "collect_degraded", Result: "collect_degraded", Enabled: true, Priority: 60,
				Message: "采集已连续失败 {observed} 次，当前展示的是上次成功采集的旧值",
				All: []HealthCondition{
					{Fact: "consecutive_failures", Op: "gte", Value: 3, Label: "连续失败次数"},
					{Fact: "collect_status", Op: "ne", Value: "failed", Label: "采集状态"},
				}},
			// 数据过期阈值 180 秒 ≈ 2 倍默认采集周期，改这里的 value 即可调整口径
			{Code: "data_stale", Result: "data_stale", Enabled: true, Priority: 70,
				Message: "数据未采集或已过期（{label}={observed}）",
				Any: []HealthCondition{
					{Fact: "collect_status", Op: "in", Value: []any{"never"}, Label: "采集状态"},
					{Fact: "collect_quality", Op: "in", Value: []any{"no_data", "stale"}, Label: "采集质量"},
					{Fact: "freshness_seconds", Op: "gte", Value: defaultDataStaleSeconds, Label: "数据新鲜度(秒)"},
				}},
			{Code: "collect_warning", Result: "collect_warning", Enabled: true, Priority: 80,
				Message: "采集质量降级，部分指标不可用（{label}={observed}）",
				Any: []HealthCondition{
					{Fact: "card_status", Op: "in", Value: []any{"warning"}, Label: "卡片状态"},
					{Fact: "collect_quality", Op: "in", Value: []any{"partial", "unsupported"}, Label: "采集质量"},
				}},
			{Code: "alert_active", Result: "alert_active", Enabled: true, Priority: 90,
				Message: "存在 {observed} 条未忽略的待认领告警",
				Any:     []HealthCondition{{Fact: "active_alerts", Op: "gte", Value: 1, Label: "待认领告警数"}}},
			{Code: "replication_lag_high", Result: "replication_lag_high", Enabled: true, Priority: 100,
				Message: "复制延迟 {observed}s，达到 {threshold}s",
				All:     []HealthCondition{{Fact: "deploy_mode", Op: "in", Value: []any{"master_slave", "cluster"}, Label: "部署形态"}},
				Any:     []HealthCondition{{Fact: "metric:repl.lag_seconds", Op: "gte", Value: 300, Label: "复制延迟(秒)"}}},
			{Code: "resource_pressure", Result: "resource_pressure", Enabled: true, Priority: 110,
				Message: "主机资源压力：{label}={observed}%，达到 {threshold}%",
				Any: []HealthCondition{
					{Fact: "metric:res.cpu", Op: "gte", Value: 85, Label: "CPU 使用率"},
					{Fact: "metric:res.memory", Op: "gte", Value: 90, Label: "内存使用率"},
					{Fact: "metric:res.disk", Op: "gte", Value: 85, Label: "磁盘使用率"},
					{Fact: "metric:res.io", Op: "gte", Value: 90, Label: "IO 使用率"},
					{Fact: "metric:res.swap_used_pct", Op: "gte", Value: 10, Label: "Swap 使用率"},
				}},
			{Code: "slow_query_storm", Result: "slow_query_storm", Enabled: true, Priority: 120,
				Message: "慢查询激增：{observed} 条/秒，达到 {threshold}",
				Any:     []HealthCondition{{Fact: "metric:eng.slow_queries", Op: "gte", Value: 5, Label: "慢查询速率"}}},
			{Code: "conn_error_high", Result: "conn_error_high", Enabled: true, Priority: 130,
				Message: "连接错误速率 {observed} 条/秒，达到 {threshold}",
				Any:     []HealthCondition{{Fact: "metric:eng.aborted_conns", Op: "gte", Value: 1, Label: "连接错误速率"}}},
			// 少量慢查询只做提示：默认不改变健康结论。想让它进风险桶，改结果项 level 即可
			{Code: "slow_queries_elevated", Result: "slow_queries_elevated", Enabled: true, Priority: 140,
				Message: "存在慢查询：{observed} 条/秒",
				Any:     []HealthCondition{{Fact: "metric:eng.slow_queries", Op: "gte", Value: 0.02, Label: "慢查询速率"}}},
			{Code: "cache_hit_low", Result: "cache_hit_low", Enabled: true, Priority: 150,
				Message: "缓存命中率 {observed}%，低于 {threshold}%",
				Any:     []HealthCondition{{Fact: "metric:perf.cache_hit_ratio", Op: "lte", Value: 90, Label: "缓存命中率"}}},
			{Code: "long_trx_present", Result: "long_trx_present", Enabled: true, Priority: 160,
				Message: "存在 {observed} 个长事务",
				Any:     []HealthCondition{{Fact: "metric:perf.long_trx_count", Op: "gte", Value: 1, Label: "长事务数"}}},
			{Code: "lock_wait_present", Result: "lock_wait_present", Enabled: true, Priority: 170,
				Message: "存在 {observed} 个锁等待",
				Any:     []HealthCondition{{Fact: "metric:perf.locks", Op: "gte", Value: 1, Label: "锁等待数"}}},
		},
	}
}

// ---------------------------------------------------------------------------
// 校验（写入路径，严格）与归一化（读取路径，宽容降级）
// ---------------------------------------------------------------------------

var healthStateValues = []string{HealthStateHealthy, HealthStateRisk, HealthStateFault, HealthStateUncollected}

func isHealthState(value string) bool {
	for _, item := range healthStateValues {
		if item == value {
			return true
		}
	}
	return false
}

// factDef 查事实定义：动态事实（metric:xxx）按前缀匹配。
func factDef(fact string) (HealthFactDef, bool) {
	for _, def := range healthFactDefs {
		if def.Dynamic {
			if strings.HasPrefix(fact, def.Key+":") && strings.TrimPrefix(fact, def.Key+":") != "" {
				return def, true
			}
			continue
		}
		if def.Key == fact {
			return def, true
		}
	}
	return HealthFactDef{}, false
}

// ValidateHealthGradeConfig 写入路径校验：任何一处不合法都直接报错，不静默改配置。
// 前端会拦一次，这里再拦一次 —— 绕过页面的调用同样拦得住。
func ValidateHealthGradeConfig(cfg HealthGradeConfig) error {
	if len(cfg.Levels) == 0 {
		return fmt.Errorf("至少需要定义一个等级")
	}
	levelSet := map[string]HealthLevelDef{}
	rankSet := map[int]string{}
	for _, level := range cfg.Levels {
		if strings.TrimSpace(level.Code) == "" {
			return fmt.Errorf("等级 code 不能为空")
		}
		if _, dup := levelSet[level.Code]; dup {
			return fmt.Errorf("等级 code 重复：%s", level.Code)
		}
		if !isHealthState(level.HealthState) {
			return fmt.Errorf("等级 %s 的 health_state 非法：%s（可选 %v）", level.Code, level.HealthState, healthStateValues)
		}
		if owner, dup := rankSet[level.Rank]; dup {
			return fmt.Errorf("等级 %s 与 %s 的 rank 重复（rank 决定严重度顺序，必须唯一）", owner, level.Code)
		}
		levelSet[level.Code] = level
		rankSet[level.Rank] = level.Code
	}
	resultSet := map[string]bool{}
	for _, result := range cfg.Results {
		if strings.TrimSpace(result.Code) == "" {
			return fmt.Errorf("结果项 code 不能为空")
		}
		if _, dup := resultSet[result.Code]; dup {
			return fmt.Errorf("结果项 code 重复：%s", result.Code)
		}
		if _, ok := levelSet[result.Level]; !ok {
			return fmt.Errorf("结果项 %s 引用了不存在的等级：%s", result.Code, result.Level)
		}
		if result.HealthState != "" && !isHealthState(result.HealthState) {
			return fmt.Errorf("结果项 %s 的 health_state 非法：%s", result.Code, result.HealthState)
		}
		resultSet[result.Code] = true
	}
	if strings.TrimSpace(cfg.Fallback) == "" {
		return fmt.Errorf("fallback（无命中时的结果项）不能为空")
	}
	if !resultSet[cfg.Fallback] {
		return fmt.Errorf("fallback 引用了不存在的结果项：%s", cfg.Fallback)
	}
	ruleSet := map[string]bool{}
	for _, rule := range cfg.Rules {
		if strings.TrimSpace(rule.Code) == "" {
			return fmt.Errorf("规则 code 不能为空")
		}
		if _, dup := ruleSet[rule.Code]; dup {
			return fmt.Errorf("规则 code 重复：%s", rule.Code)
		}
		ruleSet[rule.Code] = true
		if !resultSet[rule.Result] {
			return fmt.Errorf("规则 %s 引用了不存在的结果项：%s", rule.Code, rule.Result)
		}
		if len(rule.Any) == 0 && len(rule.All) == 0 {
			return fmt.Errorf("规则 %s 至少要有一个条件（any 或 all）", rule.Code)
		}
		for _, cond := range append(append([]HealthCondition{}, rule.Any...), rule.All...) {
			if err := validateCondition(cond, rule.Code); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCondition(cond HealthCondition, ruleCode string) error {
	def, ok := factDef(cond.Fact)
	if !ok {
		return fmt.Errorf("规则 %s 引用了未知事实：%s", ruleCode, cond.Fact)
	}
	supported := false
	for _, op := range def.Ops {
		if op == cond.Op {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("规则 %s 中事实 %s 不支持比较符 %s（可选 %v）", ruleCode, cond.Fact, cond.Op, def.Ops)
	}
	switch cond.Op {
	case "exists", "absent":
		return nil
	case "in", "not_in", "between":
		if _, err := conditionNumbers(cond.Value); err != nil {
			if _, err2 := conditionStrings(cond.Value); err2 != nil {
				return fmt.Errorf("规则 %s 中事实 %s 的比较符 %s 需要数组或双元素数组：%v", ruleCode, cond.Fact, cond.Op, err)
			}
		}
		if cond.Op == "between" {
			nums, err := conditionNumbers(cond.Value)
			if err != nil || len(nums) != 2 || nums[0] > nums[1] {
				return fmt.Errorf("规则 %s 中事实 %s 的 between 需要 [下限, 上限] 两个数字", ruleCode, cond.Fact)
			}
		}
	case "contains":
		if _, err := conditionStrings(cond.Value); err != nil {
			return fmt.Errorf("规则 %s 中事实 %s 的 contains 需要字符串或字符串数组", ruleCode, cond.Fact)
		}
	default: // eq / ne / gt / gte / lt / lte
		if def.Type == "number" {
			nums, err := conditionNumbers(cond.Value)
			if err != nil || len(nums) != 1 {
				return fmt.Errorf("规则 %s 中数值事实 %s 的比较值必须是数字", ruleCode, cond.Fact)
			}
		} else {
			strs, err := conditionStrings(cond.Value)
			if err != nil || len(strs) != 1 {
				return fmt.Errorf("规则 %s 中字符串事实 %s 的比较值必须是字符串", ruleCode, cond.Fact)
			}
		}
	}
	return nil
}

// NormalizeHealthGradeConfig 读取路径归一化：宽容处理，坏配置降级成能跑的配置并给出原因。
// 与写入路径的区别：库里可能躺着历史脏数据或被手工改坏的内容，读的时候不能让整个大屏停摆。
func NormalizeHealthGradeConfig(raw []byte) (HealthGradeConfig, string) {
	if len(raw) == 0 {
		cfg := builtinHealthGradeConfig()
		cfg.Source = "builtin"
		return cfg, ""
	}
	var cfg HealthGradeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fallback := builtinHealthGradeConfig()
		fallback.Source = "builtin"
		fallback.Degraded = true
		fallback.DegradeReason = fmt.Sprintf("配置 JSON 解析失败（%v），已回退内置默认", err)
		return fallback, fallback.DegradeReason
	}
	reasons := make([]string, 0, 4)

	// 等级：去重、补 rank、非法 health_state 修正为该 rank 的默认状态
	levels := make([]HealthLevelDef, 0, len(cfg.Levels))
	seenLevel := map[string]bool{}
	seenRank := map[int]bool{}
	defaults := builtinHealthGradeConfig()
	for index, level := range cfg.Levels {
		if strings.TrimSpace(level.Code) == "" || seenLevel[level.Code] {
			reasons = append(reasons, fmt.Sprintf("丢弃无效/重复的等级定义（第 %d 项）", index+1))
			continue
		}
		if !isHealthState(level.HealthState) {
			level.HealthState = HealthStateRisk
			reasons = append(reasons, fmt.Sprintf("等级 %s 的 health_state 非法，已修正为 risk", level.Code))
		}
		if seenRank[level.Rank] {
			level.Rank = len(levels)
			reasons = append(reasons, fmt.Sprintf("等级 %s 的 rank 重复，已按配置顺序重排为 %d", level.Code, level.Rank))
		}
		seenLevel[level.Code] = true
		seenRank[level.Rank] = true
		levels = append(levels, level)
	}
	if len(levels) == 0 {
		cfg = defaults
		cfg.Source = "builtin"
		cfg.Degraded = true
		cfg.DegradeReason = "配置中没有可用等级定义，已回退内置默认"
		return cfg, cfg.DegradeReason
	}

	// 结果项：等级不存在 → 挂到最轻等级（rank 最大），保证规则仍能产出结论
	maxRank := levels[0]
	for _, level := range levels {
		if level.Rank > maxRank.Rank {
			maxRank = level
		}
	}
	results := make([]HealthResultDef, 0, len(cfg.Results))
	seenResult := map[string]bool{}
	for index, result := range cfg.Results {
		if strings.TrimSpace(result.Code) == "" || seenResult[result.Code] {
			reasons = append(reasons, fmt.Sprintf("丢弃无效/重复的结果项（第 %d 项）", index+1))
			continue
		}
		if !seenLevel[result.Level] {
			reasons = append(reasons, fmt.Sprintf("结果项 %s 的等级 %s 不存在，已挂到最轻等级 %s",
				result.Code, result.Level, maxRank.Code))
			result.Level = maxRank.Code
		}
		if result.HealthState != "" && !isHealthState(result.HealthState) {
			result.HealthState = ""
			reasons = append(reasons, fmt.Sprintf("结果项 %s 的 health_state 非法，已改为继承等级", result.Code))
		}
		seenResult[result.Code] = true
		results = append(results, result)
	}
	cfg.Results = results

	// fallback 缺失或指向不存在的结果项 → 用最轻等级兜底
	if !seenResult[cfg.Fallback] {
		if seenResult["healthy"] {
			cfg.Fallback = "healthy"
		} else {
			cfg.Fallback = ""
		}
		if cfg.Fallback == "" {
			cfg.Results = append(cfg.Results, HealthResultDef{
				Code: "healthy", Name: "健康", Level: maxRank.Code, Category: "availability", Enabled: true,
			})
			seenResult["healthy"] = true
			cfg.Fallback = "healthy"
		}
		reasons = append(reasons, "fallback 结果项不可用，已补内置 healthy")
	}

	// 规则：条件非法或结果项不存在 → 整条规则停用（宁可少判一条，也不能误判）
	rules := make([]HealthRuleDef, 0, len(cfg.Rules))
	seenRule := map[string]bool{}
	for index := range cfg.Rules {
		rule := cfg.Rules[index]
		if strings.TrimSpace(rule.Code) == "" || seenRule[rule.Code] {
			reasons = append(reasons, fmt.Sprintf("丢弃无效/重复的规则（第 %d 项）", index+1))
			continue
		}
		if !seenResult[rule.Result] {
			reasons = append(reasons, fmt.Sprintf("规则 %s 的结果项 %s 不存在，已停用", rule.Code, rule.Result))
			rule.Enabled = false
		}
		bad := false
		for _, cond := range append(append([]HealthCondition{}, rule.Any...), rule.All...) {
			if err := validateCondition(cond, rule.Code); err != nil {
				reasons = append(reasons, err.Error())
				bad = true
			}
		}
		if bad {
			rule.Enabled = false
		}
		seenRule[rule.Code] = true
		rules = append(rules, rule)
	}
	cfg.Rules = rules
	cfg.Levels = levels
	cfg.Facts = HealthGradeFacts()
	if len(reasons) > 0 {
		cfg.Degraded = true
		cfg.DegradeReason = strings.Join(reasons, "；")
	}
	return cfg, cfg.DegradeReason
}

// ---------------------------------------------------------------------------
// 读取入口：缓存 + 热更新（与采集频率配置同款形态）
// ---------------------------------------------------------------------------

type healthGradeBundle struct {
	cfg     HealthGradeConfig
	results map[string]HealthResultDef
	levels  map[string]HealthLevelDef
	// sortedLevels 按 rank 升序，取"最轻等级"直接取末尾
	sortedLevels []HealthLevelDef
	// sortedRules 按 priority 升序预排序：评估每实例都要遍历，排序只做一次
	sortedRules []HealthRuleDef
}

var (
	healthGradeCache     *healthGradeBundle
	healthGradeLoadedAt  time.Time
	healthGradeMutex     sync.Mutex
	healthGradeLastWarn  time.Time
	healthGradeWarnMutex sync.Mutex
)

func (b *healthGradeBundle) level(code string) (HealthLevelDef, bool) {
	level, ok := b.levels[code]
	return level, ok
}

func (b *healthGradeBundle) lightestLevel() HealthLevelDef {
	if len(b.sortedLevels) == 0 {
		return HealthLevelDef{Code: "P3", Name: "提示", Rank: 3, HealthState: HealthStateHealthy}
	}
	return b.sortedLevels[len(b.sortedLevels)-1]
}

// GetHealthGradeConfig 当前生效的分级配置（带进程内缓存）。接口与评估引擎共用同一份。
func GetHealthGradeConfig() HealthGradeConfig {
	return healthGradeBundleNow().cfg
}

// ReloadHealthGradeConfig 立刻失效缓存，保存配置后调用。
func ReloadHealthGradeConfig() HealthGradeConfig {
	healthGradeMutex.Lock()
	healthGradeCache = nil
	healthGradeMutex.Unlock()
	return GetHealthGradeConfig()
}

func healthGradeBundleNow() *healthGradeBundle {
	healthGradeMutex.Lock()
	defer healthGradeMutex.Unlock()
	if healthGradeCache != nil && time.Since(healthGradeLoadedAt) < healthGradeCacheTTL {
		return healthGradeCache
	}
	bundle := loadHealthGradeBundle()
	healthGradeCache = bundle
	healthGradeLoadedAt = time.Now()
	return bundle
}

// newHealthGradeBundle 由配置构建评估用的索引（等级/结果项查表 + 规则预排序）。
// 排序只在这里做一次，评估每实例时直接遍历，避免逐实例重复排序。
func newHealthGradeBundle(cfg HealthGradeConfig) *healthGradeBundle {
	bundle := &healthGradeBundle{cfg: cfg, results: map[string]HealthResultDef{}, levels: map[string]HealthLevelDef{}}
	for _, item := range cfg.Results {
		bundle.results[item.Code] = item
	}
	for _, item := range cfg.Levels {
		bundle.levels[item.Code] = item
		bundle.sortedLevels = append(bundle.sortedLevels, item)
	}
	sort.SliceStable(bundle.sortedLevels, func(i, j int) bool {
		return bundle.sortedLevels[i].Rank < bundle.sortedLevels[j].Rank
	})
	bundle.sortedRules = append(bundle.sortedRules, cfg.Rules...)
	sort.SliceStable(bundle.sortedRules, func(i, j int) bool {
		return bundle.sortedRules[i].Priority < bundle.sortedRules[j].Priority
	})
	return bundle
}

func loadHealthGradeBundle() *healthGradeBundle {
	builtin := builtinHealthGradeConfig()
	builtin.Source = "builtin"
	builtin.ApplyNote = healthGradeApplyNote()
	toBundle := newHealthGradeBundle
	if global.App == nil || global.App.DB == nil {
		return toBundle(builtin)
	}
	var row monitorModels.InsightMonitorHealthGradeSettings
	err := global.App.DB.Where("id = ?", healthGradeRowID).Take(&row).Error
	if err != nil {
		// 表没建好或行被删了：按内置默认跑，不让大屏停摆
		builtin.Degraded = true
		builtin.DegradeReason = "未读取到健康分级设置，已按内置默认运行"
		warnHealthGrade(builtin.DegradeReason)
		return toBundle(builtin)
	}
	// 用户没自定义 → 一律用内置默认，并跟随内置版本升级（避免卡在历史快照上）
	if !row.Customized {
		if row.Version < builtinHealthGradeVersion || len(row.Payload) == 0 {
			syncBuiltinHealthGrade(row.Version)
		}
		return toBundle(builtin)
	}
	cfg, reason := NormalizeHealthGradeConfig(row.Payload)
	cfg.Source = "db"
	cfg.Customized = true
	cfg.ApplyNote = healthGradeApplyNote()
	if row.Model != nil {
		cfg.UpdatedAt = time.Time(row.UpdatedAt).Format("2006-01-02 15:04:05")
	}
	cfg.UpdatedBy = row.UpdatedByName
	if reason != "" {
		warnHealthGrade(reason)
	}
	return toBundle(cfg)
}

// syncBuiltinHealthGrade 把内置默认写回库（仅用于未自定义的行升级版本）。
func syncBuiltinHealthGrade(oldVersion int) {
	if global.App == nil || global.App.DB == nil {
		return
	}
	payload, err := json.Marshal(builtinHealthGradeConfig())
	if err != nil {
		return
	}
	updates := map[string]any{
		"version":    builtinHealthGradeVersion,
		"payload":    datatypes.JSON(payload),
		"customized": false,
	}
	if err := global.App.DB.Model(&monitorModels.InsightMonitorHealthGradeSettings{}).
		Where("id = ?", healthGradeRowID).Updates(updates).Error; err == nil && global.App.Log != nil {
		global.App.Log.Infof("[monitor] 健康分级内置默认已从版本 %d 升级到 %d（用户未自定义，自动跟随）",
			oldVersion, builtinHealthGradeVersion)
	}
}

func warnHealthGrade(reason string) {
	healthGradeWarnMutex.Lock()
	should := time.Since(healthGradeLastWarn) > 5*time.Minute
	if should {
		healthGradeLastWarn = time.Now()
	}
	healthGradeWarnMutex.Unlock()
	if should && global.App != nil && global.App.Log != nil {
		global.App.Log.Warnf("[monitor] 健康分级配置降级：%s", reason)
	}
}

func healthGradeApplyNote() string {
	return "JaySQL 侧热更新：保存后 10 秒内生效，下一轮采集/下一次大屏查询即按新口径评估；" +
		"历史状态不做回填，环比快照按当时的配置解释。" +
		"判定只依据配置里的规则，改动前建议先用「重置为内置默认」对照一遍结果。"
}

// EnsureHealthGradeSettings 建表后播种：没有行时写入内置默认配置。
// 已存在且用户自定义过的行绝不动 —— 用户配置永远优先。
func EnsureHealthGradeSettings(db *gorm.DB) {
	if db == nil {
		return
	}
	var count int64
	if err := db.Model(&monitorModels.InsightMonitorHealthGradeSettings{}).Count(&count).Error; err != nil {
		return
	}
	payload, err := json.Marshal(builtinHealthGradeConfig())
	if err != nil {
		return
	}
	if count == 0 {
		row := monitorModels.InsightMonitorHealthGradeSettings{
			Model:      &commonModels.Model{ID: healthGradeRowID},
			Version:    builtinHealthGradeVersion,
			Customized: false,
			Payload:    datatypes.JSON(payload),
		}
		if err := db.Create(&row).Error; err != nil && global.App != nil && global.App.Log != nil {
			global.App.Log.Warnf("[monitor] 播种健康分级设置失败: %v", err)
		}
		return
	}
	// 行已存在且未自定义：跟随内置版本升级
	var row monitorModels.InsightMonitorHealthGradeSettings
	if err := db.Where("id = ?", healthGradeRowID).Take(&row).Error; err != nil {
		return
	}
	if !row.Customized && (row.Version < builtinHealthGradeVersion || len(row.Payload) == 0) {
		if err := db.Model(&monitorModels.InsightMonitorHealthGradeSettings{}).
			Where("id = ?", healthGradeRowID).
			Updates(map[string]any{"version": builtinHealthGradeVersion, "payload": datatypes.JSON(payload)}).Error; err != nil {
			if global.App != nil && global.App.Log != nil {
				global.App.Log.Warnf("[monitor] 健康分级内置默认升级失败: %v", err)
			}
		}
	}
}

// SaveHealthGradeConfig 保存用户配置并立即生效。
// 传入原始 JSON：校验在服务端做，绕过页面的调用同样拦得住。
func SaveHealthGradeConfig(raw []byte, operator string) (HealthGradeConfig, error) {
	var cfg HealthGradeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return HealthGradeConfig{}, fmt.Errorf("配置 JSON 解析失败: %v", err)
	}
	if cfg.Version <= 0 {
		cfg.Version = builtinHealthGradeVersion
	}
	if err := ValidateHealthGradeConfig(cfg); err != nil {
		return HealthGradeConfig{}, err
	}
	if global.App == nil || global.App.DB == nil {
		return HealthGradeConfig{}, fmt.Errorf("数据库未就绪")
	}
	// 事实字典是系统能力，不接受用户覆盖，避免前端把只读字段回写造成自相矛盾
	cfg.Facts = HealthGradeFacts()
	payload, err := json.Marshal(cfg)
	if err != nil {
		return HealthGradeConfig{}, err
	}
	updates := map[string]any{
		"version":         cfg.Version,
		"customized":      true,
		"payload":         datatypes.JSON(payload),
		"updated_by_name": strings.TrimSpace(operator),
	}
	var row monitorModels.InsightMonitorHealthGradeSettings
	if err := global.App.DB.Where("id = ?", healthGradeRowID).Take(&row).Error; err != nil {
		row = monitorModels.InsightMonitorHealthGradeSettings{
			Model:         &commonModels.Model{ID: healthGradeRowID},
			Version:       cfg.Version,
			Customized:    true,
			Payload:       datatypes.JSON(payload),
			UpdatedByName: strings.TrimSpace(operator),
		}
		if err := global.App.DB.Create(&row).Error; err != nil {
			return HealthGradeConfig{}, err
		}
	} else if err := global.App.DB.Model(&monitorModels.InsightMonitorHealthGradeSettings{}).
		Where("id = ?", healthGradeRowID).Updates(updates).Error; err != nil {
		return HealthGradeConfig{}, err
	}
	result := ReloadHealthGradeConfig()
	if global.App.Log != nil {
		global.App.Log.Infof("[monitor] 健康分级配置已更新（等级 %d 个 / 结果项 %d 个 / 规则 %d 条），操作人 %s",
			len(cfg.Levels), len(cfg.Results), len(cfg.Rules), strings.TrimSpace(operator))
	}
	return result, nil
}

// ResetHealthGradeConfig 丢弃用户配置，回到内置默认（customized 置回 false，
// 之后仍会跟随内置版本升级）。
func ResetHealthGradeConfig(operator string) (HealthGradeConfig, error) {
	if global.App == nil || global.App.DB == nil {
		return HealthGradeConfig{}, fmt.Errorf("数据库未就绪")
	}
	payload, err := json.Marshal(builtinHealthGradeConfig())
	if err != nil {
		return HealthGradeConfig{}, err
	}
	if err := global.App.DB.Model(&monitorModels.InsightMonitorHealthGradeSettings{}).
		Where("id = ?", healthGradeRowID).
		Updates(map[string]any{
			"version":         builtinHealthGradeVersion,
			"customized":      false,
			"payload":         datatypes.JSON(payload),
			"updated_by_name": strings.TrimSpace(operator),
		}).Error; err != nil {
		return HealthGradeConfig{}, err
	}
	result := ReloadHealthGradeConfig()
	if global.App.Log != nil {
		global.App.Log.Infof("[monitor] 健康分级配置已重置为内置默认，操作人 %s", strings.TrimSpace(operator))
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// 评估引擎
// ---------------------------------------------------------------------------

// healthFactValue 事实值。用显式类型而不是 any，避免评估时反复做类型断言。
type healthFactValue struct {
	Kind string // string / number / list
	Str  string
	Num  float64
	List []string
}

// buildHealthFacts 从实例卡片提取全部事实。
// 指标读数只在 Available 时给出 —— 采集不到就是"没有这个事实"，
// 绝不能用 0 冒充（0 是合法读数，会让"磁盘 0%"之类的规则误命中）。
func buildHealthFacts(card InstanceMonitorCard) map[string]healthFactValue {
	facts := map[string]healthFactValue{
		"collect_status":       {Kind: "string", Str: card.CollectStatus},
		"collect_quality":      {Kind: "string", Str: card.Quality},
		"card_status":          {Kind: "string", Str: card.Status},
		"deploy_mode":          {Kind: "string", Str: card.DeployMode},
		"db_type":              {Kind: "string", Str: card.DbType},
		"consecutive_failures": {Kind: "number", Num: float64(card.ConsecutiveFailures)},
		"freshness_seconds":    {Kind: "number", Num: float64(card.FreshnessSeconds)},
	}
	alertMetrics := make([]string, 0, len(card.Alerts))
	activeAlerts := 0
	maxSeverity := ""
	for _, alert := range card.Alerts {
		if alert.Ignored {
			continue
		}
		activeAlerts++
		if alert.MetricKey != "" {
			alertMetrics = append(alertMetrics, alert.MetricKey)
		}
		if maxSeverity == "" || severityWeight(alert.Severity) < severityWeight(maxSeverity) {
			maxSeverity = alert.Severity
		}
	}
	facts["active_alerts"] = healthFactValue{Kind: "number", Num: float64(activeAlerts)}
	facts["alert_severity_max"] = healthFactValue{Kind: "string", Str: maxSeverity}
	facts["alert_metrics"] = healthFactValue{Kind: "list", List: alertMetrics}
	for _, sample := range card.Metrics {
		if !sample.Available {
			continue
		}
		facts[healthFactMetricPrefix+sample.Key] = healthFactValue{Kind: "number", Num: sample.Value}
		facts[healthFactMetricQualityPrefix+sample.Key] = healthFactValue{Kind: "string", Str: sample.Quality}
	}
	return facts
}

func conditionNumbers(value any) ([]float64, error) {
	switch typed := value.(type) {
	case nil:
		return nil, fmt.Errorf("比较值为空")
	case []any:
		out := make([]float64, 0, len(typed))
		for _, item := range typed {
			num, err := toFloat(item)
			if err != nil {
				return nil, err
			}
			out = append(out, num)
		}
		return out, nil
	case []float64:
		return typed, nil
	case []int:
		out := make([]float64, 0, len(typed))
		for _, item := range typed {
			out = append(out, float64(item))
		}
		return out, nil
	default:
		num, err := toFloat(value)
		if err != nil {
			return nil, err
		}
		return []float64{num}, nil
	}
}

func conditionStrings(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, fmt.Errorf("比较值为空")
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out, nil
	case []string:
		return typed, nil
	default:
		return []string{fmt.Sprintf("%v", value)}, nil
	}
}

func toFloat(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	case string:
		return strconv.ParseFloat(typed, 64)
	default:
		return 0, fmt.Errorf("无法把 %v 转成数字", value)
	}
}

// evalCondition 求值单条条件。返回是否命中，以及（数值型事实的）观测值。
func evalCondition(cond HealthCondition, facts map[string]healthFactValue) (bool, *float64) {
	value, exists := facts[cond.Fact]
	switch cond.Op {
	case "exists":
		return exists, nil
	case "absent":
		return !exists, nil
	}
	if !exists {
		return false, nil
	}
	var observed *float64
	if value.Kind == "number" {
		num := value.Num
		observed = &num
	}
	switch cond.Op {
	case "eq", "ne":
		hit := false
		if value.Kind == "number" {
			nums, err := conditionNumbers(cond.Value)
			if err == nil && len(nums) > 0 {
				hit = value.Num == nums[0]
			}
		} else {
			strs, err := conditionStrings(cond.Value)
			if err == nil && len(strs) > 0 {
				hit = value.Str == strs[0]
			}
		}
		if cond.Op == "ne" {
			return !hit, observed
		}
		return hit, observed
	case "in", "not_in":
		hit := false
		if value.Kind == "list" {
			wanted, err := conditionStrings(cond.Value)
			if err == nil {
				for _, item := range wanted {
					for _, owned := range value.List {
						if owned == item {
							hit = true
							break
						}
					}
					if hit {
						break
					}
				}
			}
		} else {
			wanted, err := conditionStrings(cond.Value)
			if err == nil {
				actual := value.Str
				if value.Kind == "number" {
					actual = strconv.FormatFloat(value.Num, 'f', -1, 64)
				}
				for _, item := range wanted {
					if item == actual {
						hit = true
						break
					}
				}
			}
		}
		if cond.Op == "not_in" {
			return !hit, observed
		}
		return hit, observed
	case "gt", "gte", "lt", "lte":
		if value.Kind != "number" {
			return false, nil
		}
		nums, err := conditionNumbers(cond.Value)
		if err != nil || len(nums) == 0 {
			return false, nil
		}
		switch cond.Op {
		case "gt":
			return value.Num > nums[0], observed
		case "gte":
			return value.Num >= nums[0], observed
		case "lt":
			return value.Num < nums[0], observed
		default:
			return value.Num <= nums[0], observed
		}
	case "between":
		if value.Kind != "number" {
			return false, nil
		}
		nums, err := conditionNumbers(cond.Value)
		if err != nil || len(nums) < 2 {
			return false, nil
		}
		return value.Num >= nums[0] && value.Num <= nums[1], observed
	case "contains":
		if value.Kind != "list" {
			return false, nil
		}
		wanted, err := conditionStrings(cond.Value)
		if err != nil {
			return false, nil
		}
		for _, item := range wanted {
			for _, owned := range value.List {
				if owned == item {
					return true, observed
				}
			}
		}
		return false, nil
	}
	return false, nil
}

// matchRule 求值一条规则：All 全满足 且 Any 至少一个满足（两者为空的部分视为满足）。
func matchRule(rule HealthRuleDef, facts map[string]healthFactValue) (bool, HealthCondition, *float64) {
	for _, cond := range rule.All {
		if hit, _ := evalCondition(cond, facts); !hit {
			return false, HealthCondition{}, nil
		}
	}
	if len(rule.Any) == 0 {
		// 只有 all 且全部满足：取第一条 all 条件作为依据
		if len(rule.All) > 0 {
			cond := rule.All[0]
			_, observed := evalCondition(cond, facts)
			return true, cond, observed
		}
		return false, HealthCondition{}, nil
	}
	for _, cond := range rule.Any {
		if hit, observed := evalCondition(cond, facts); hit {
			return true, cond, observed
		}
	}
	return false, HealthCondition{}, nil
}

func conditionThreshold(cond HealthCondition) *float64 {
	nums, err := conditionNumbers(cond.Value)
	if err != nil || len(nums) == 0 {
		return nil
	}
	value := nums[0]
	return &value
}

// healthValueText 数值文案。nil 表示"这条规则没有数值依据"（如状态类条件），
// 照本包约定显式写「无数值」，绝不用 0 冒充。
func healthValueText(value *float64) string {
	if value == nil {
		return "无数值"
	}
	return formatPlainValue(*value, "")
}

// factValueText 事实当前值的文本形式。字符串型事实（如采集状态）没有数值，
// 这里照原文输出，否则"最严重告警级别=无数值"这种文案没法看。
func factValueText(facts map[string]healthFactValue, fact string) string {
	value, ok := facts[fact]
	if !ok {
		return ""
	}
	switch value.Kind {
	case "number":
		return strconv.FormatFloat(value.Num, 'f', -1, 64)
	case "list":
		return strings.Join(value.List, ",")
	default:
		return value.Str
	}
}

// renderHealthMessage 替换消息模板里的占位符。
// {observed} 优先取事实原文（覆盖字符串事实），取不到才用数值文案。
func renderHealthMessage(template string, cond HealthCondition, observed, threshold *float64, actual string) string {
	if template == "" {
		return ""
	}
	label := cond.Label
	if label == "" {
		label = cond.Fact
	}
	replacer := strings.NewReplacer(
		"{observed}", firstNonEmpty(actual, healthValueText(observed)),
		"{threshold}", healthValueText(threshold),
		"{label}", label,
		"{fact}", cond.Fact,
	)
	return replacer.Replace(template)
}

// healthBasis 生成判定依据文案："磁盘使用率 当前 96%，满足 >= 95"。
func healthBasis(cond HealthCondition, observed, threshold *float64, actual string) string {
	label := cond.Label
	if label == "" {
		label = cond.Fact
	}
	if actual != "" {
		return fmt.Sprintf("%s 当前 %s，满足 %s %s", label, actual, cond.Op, healthValueText(threshold))
	}
	if observed != nil {
		return fmt.Sprintf("%s 当前 %s，满足 %s %s", label, healthValueText(observed),
			cond.Op, healthValueText(threshold))
	}
	return fmt.Sprintf("%s 命中条件 %s %s", label, cond.Op, healthValueText(threshold))
}

// EvaluateInstanceHealth 对单个实例执行分级评估。
//
// 流程：取配置 → 提取事实 → 按 priority 升序匹配规则 → 命中结果项 → 查等级 → 取健康状态。
// 最终结论由"命中的最严重结果项"决定（rank 最小者）：等级取它的等级，
// 若该结果项显式声明了 health_state 则以其为准（典型：数据过期归 P2 但状态是 uncollected）。
func EvaluateInstanceHealth(card InstanceMonitorCard) HealthGradeResult {
	return evaluateWithBundle(healthGradeBundleNow(), card)
}

// evaluateWithBundle 用指定配置评估（单测与"假设配置"预演走这里）。
func evaluateWithBundle(bundle *healthGradeBundle, card InstanceMonitorCard) HealthGradeResult {
	cfg := bundle.cfg
	result := HealthGradeResult{
		InstanceID:    card.InstanceID,
		HealthState:   HealthStateHealthy,
		ScreenClass:   HealthStateHealthy,
		Findings:      make([]HealthFinding, 0, 4),
		Source:        cfg.Source,
		Degraded:      cfg.Degraded,
		DegradeReason: cfg.DegradeReason,
	}
	facts := buildHealthFacts(card)

	findings := make([]HealthFinding, 0, 4)
	for _, rule := range bundle.sortedRules {
		if !rule.Enabled {
			continue
		}
		hit, cond, observed := matchRule(rule, facts)
		if !hit {
			continue
		}
		resultDef, ok := bundle.results[rule.Result]
		if !ok || !resultDef.Enabled {
			continue
		}
		level, ok := bundle.level(resultDef.Level)
		if !ok {
			continue
		}
		state := level.HealthState
		if resultDef.HealthState != "" {
			state = resultDef.HealthState // 结果项级覆盖：数据过期等
		}
		threshold := conditionThreshold(cond)
		actual := factValueText(facts, cond.Fact)
		findings = append(findings, HealthFinding{
			RuleCode:    rule.Code,
			ResultCode:  resultDef.Code,
			ResultName:  resultDef.Name,
			Level:       level.Code,
			LevelName:   level.Name,
			Rank:        level.Rank,
			HealthState: state,
			Category:    resultDef.Category,
			Message:     firstNonEmpty(renderHealthMessage(rule.Message, cond, observed, threshold, actual), resultDef.Name),
			Basis:       healthBasis(cond, observed, threshold, actual),
			Advice:      resultDef.Advice,
			Fact:        cond.Fact,
			Observed:    observed,
			Threshold:   threshold,
		})
		if rule.StopOnHit {
			break
		}
	}

	// 无任何命中：走 fallback 结果项（默认 healthy，因此"健康"本身也是可配置的）
	if len(findings) == 0 {
		code := cfg.Fallback
		resultDef, okDef := bundle.results[code]
		if !okDef {
			resultDef = HealthResultDef{Code: "healthy", Name: "健康", Level: bundle.lightestLevel().Code, Category: "availability"}
		}
		level, okLevel := bundle.level(resultDef.Level)
		if !okLevel {
			level = bundle.lightestLevel()
		}
		state := level.HealthState
		if resultDef.HealthState != "" {
			state = resultDef.HealthState
		}
		result.Level = level.Code
		result.LevelName = level.Name
		result.Rank = level.Rank
		result.HealthState = state
		result.ScreenClass = state
		return result
	}

	// 命中的多条按严重度排序，最严重的一条决定最终结论
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Rank < findings[j].Rank })
	top := findings[0]
	result.Findings = findings
	result.Level = top.Level
	result.LevelName = top.LevelName
	result.Rank = top.Rank
	result.HealthState = top.HealthState
	result.ScreenClass = top.HealthState
	return result
}

// DataStaleLimitSeconds 数据过期阈值：从配置规则里读，避免与 classifyScreenCard 时代的
// 180 秒常量再次分裂成两个源。取不到时用兜底常量。
func DataStaleLimitSeconds() int {
	return dataStaleLimitOf(GetHealthGradeConfig())
}

// healthScoreDeduction 各等级单条命中的扣分。分数是排序用的相对量纲，不是承诺的 SLA：
// P0 一条即近半，P3 只做轻微提示。要调整手感改这里即可，勿在调用方散落写死。
var healthScoreDeduction = map[int]int{0: 45, 1: 25, 2: 12, 3: 3}

// HealthScoreOf 由分级评估结果派生 0-100 的健康分（大屏明细列表排序/展示用）。
// 口径：满分 100，逐条命中结果项按等级扣分（多条命中累加，下限 0）。
// uncollected（数据过期/未采集）返回 nil —— 数据不可信时不能给分数，
// 前端按平台硬规则显示「—」，与指标 quality!=ok 三层一致口径同源。
// 未知 rank（用户自定义等级扩展）按 0 扣分处理，不给分也不炸。
func HealthScoreOf(grade HealthGradeResult) *int {
	if grade.HealthState == HealthStateUncollected {
		return nil
	}
	score := 100
	for _, finding := range grade.Findings {
		if deduct, ok := healthScoreDeduction[finding.Rank]; ok {
			score -= deduct
		}
	}
	if score < 0 {
		score = 0
	}
	return &score
}

func dataStaleLimitOf(cfg HealthGradeConfig) int {
	for _, rule := range cfg.Rules {
		if rule.Code != "data_stale" {
			continue
		}
		for _, cond := range append(append([]HealthCondition{}, rule.Any...), rule.All...) {
			if cond.Fact == "freshness_seconds" {
				nums, err := conditionNumbers(cond.Value)
				if err == nil && len(nums) > 0 && nums[0] > 0 {
					return int(nums[0])
				}
			}
		}
	}
	return defaultDataStaleSeconds
}

// OperatorFromContext 已在 monitor_scrape_config.go 定义，这里不再重复声明。

// LogHealthGradeSelfCheck 启动自检：一行打印当前生效的等级与规则数，
// 并校验配置是否处于降级状态 —— 部署后看这一行就知道口径有没有跑偏。
func LogHealthGradeSelfCheck() {
	cfg := GetHealthGradeConfig()
	if global.App == nil || global.App.Log == nil {
		return
	}
	levels := make([]string, 0, len(cfg.Levels))
	for _, level := range cfg.Levels {
		levels = append(levels, fmt.Sprintf("%s(%s→%s)", level.Code, level.Name, level.HealthState))
	}
	enabled := 0
	for _, rule := range cfg.Rules {
		if rule.Enabled {
			enabled++
		}
	}
	global.App.Log.Infof("[monitor] 健康分级自检：来源 %s | 等级 %v | 结果项 %d | 规则 %d/%d 启用 | 数据过期阈值 %ds",
		cfg.Source, levels, len(cfg.Results), enabled, len(cfg.Rules), DataStaleLimitSeconds())
	if cfg.Degraded {
		global.App.Log.Warnf("[monitor] 健康分级已降级运行：%s", cfg.DegradeReason)
	}
}

// 操作人提取复用 monitor_scrape_config.go 的 OperatorFromContext，不在这里重复实现。
