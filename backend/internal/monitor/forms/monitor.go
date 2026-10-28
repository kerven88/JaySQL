package forms

import "encoding/json"

// UpdateHealthGradeConfigForm 健康分级配置保存表单。
//
// 用 json.RawMessage 整段接收：结构级校验（等级/结果项/规则互相引用是否成立）
// 在 services.ValidateHealthGradeConfig 里做，表单不重复声明一套结构体 ——
// 否则新增字段要同时改两处，又变成双源。
type UpdateHealthGradeConfigForm struct {
	Config json.RawMessage `json:"config" binding:"required"`
}

type MonitorListFilter struct {
	Search      string `form:"search" json:"search"`
	Environment string `form:"environment" json:"environment"`
	DbType      string `form:"db_type" json:"db_type"`
	Business    string `form:"business" json:"business"`
	GroupName   string `form:"group_name" json:"group_name"`
	InstanceID  string `form:"instance_id" json:"instance_id"`
}

type GetDashboardForm struct {
	MonitorListFilter
	// RangeHours 大屏统计区间（小时）：保留兼容旧调用方，新前端请用 RangeMinutes。
	RangeHours int `form:"range_hours" json:"range_hours"`
	// RangeMinutes 预设区间（分钟）：最近5分钟 = 5，最近30分钟 = 30。
	// 优先级高于 RangeHours；两者都缺省时服务层按 30 分钟。
	RangeMinutes int `form:"range_minutes" json:"range_minutes"`
	// StartTime / EndTime 自定义区间起止（"2006-01-02 15:04:05"）。
	// 任一非空即按自定义窗口解析；EndTime 缺省表示"到当前时刻"。
	StartTime string `form:"start_time" json:"start_time"`
	EndTime   string `form:"end_time" json:"end_time"`
}

// GetRiskOverviewForm 风险总览：暂无过滤参数（值班视角看全系统），
// 保留结构体便于后续加业务线/机房过滤。
type GetRiskOverviewForm struct{}

// AlertActionForm 大屏告警人工处置：ack=认领工单，ignore=忽略，unignore=取消忽略。
type AlertActionForm struct {
	Fingerprint string `json:"fingerprint" binding:"required,max=160"`
	Action      string `json:"action" binding:"required,oneof=ack ignore unignore"`
}

type GetDetailForm struct {
	MonitorListFilter
}

// GetMetricHistoryForm 指标历史（趋势图）取数表单。
//
// Range 是页面时间范围下拉框的取值，与前端下拉一一对应：
// 15m / 1h / 6h / 24h / 7d / custom。
// 之所以由后端定义窗口与分桶：若前端自己算起止时间，
// 多端必然出现"同一范围画出的曲线点数不同"。
// UpdateScrapeSettingsForm 采集频率设置。
// 只收一个间隔值：Prometheus 抓取、落库周期、PromQL 窗口全部由它派生，
// 不接受调用方分别指定三处 —— 那样又会回到各自维护的老路。
type UpdateScrapeSettingsForm struct {
	IntervalSeconds int  `form:"interval_seconds" json:"interval_seconds" binding:"required"`
	AllowCustom     bool `form:"allow_custom" json:"allow_custom"`
}

type GetMetricHistoryForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,max=64"`
	Range      string `form:"range" json:"range"`
	// StartTime / EndTime 自定义区间（"2006-01-02 15:04:05"）。
	// 仅当 Range=custom 且两者都非空时生效；EndTime 缺省表示到当前时刻。
	StartTime string `form:"start_time" json:"start_time"`
	EndTime   string `form:"end_time" json:"end_time"`
	// Keys 可选：逗号分隔的指标键，不传表示取该实例全部已采集指标。
	Keys string `form:"keys" json:"keys"`
}

type UpdateTargetConfigForm struct {
	Enabled   bool   `json:"enabled"`
	Business  string `json:"business" binding:"max=64"`
	GroupName string `json:"group_name" binding:"max=64"`
	// DeployMode 部署形态。用 omitempty 是为了兼容老前端（不传时由服务层兜底 standalone）。
	DeployMode       string `json:"deploy_mode" binding:"omitempty,oneof=standalone master_slave cluster"`
	PrometheusTarget string `json:"prometheus_target" binding:"max=128"`
	ResourceSource   string `json:"resource_source" binding:"required,oneof=prometheus direct none"`
	EngineSource     string `json:"engine_source" binding:"required,oneof=direct prometheus none"`
	// ScrapeIntervalSeconds 0 = 跟随平台周期（insight_monitor_scrape_settings），
	// 大于 0 为实例级覆盖（只能放慢、须为平台周期整数倍，服务层校验）。
	// 不能用 required,min=10：那会把"跟随平台"这个合法值 0 挡在边界校验，
	// 逼着前端把默认值物化进库——全局改小周期时实例永远跟不上。
	ScrapeIntervalSeconds int `json:"scrape_interval_seconds" binding:"min=0,max=3600"`
	RetentionHours        int `json:"retention_hours" binding:"required,min=24,max=2160"`
	TimeoutSeconds        int `json:"timeout_seconds" binding:"required,min=1,max=60"`
	// RetryCount 不能加 required：Gin 的 required 会把 0 视为"未提供"，
	// 而 0 是合法值（不重试）。
	RetryCount      int      `json:"retry_count" binding:"min=0,max=10"`
	MetricsTemplate string   `json:"metrics_template" binding:"required,max=64"`
	Labels          []string `json:"labels"`
}

type GetRuleListForm struct {
	DbType string `form:"db_type" json:"db_type"`
}

// SaveRuleForm 告警规则表单。
//
// 相对旧版放开了两处硬编码校验：
//   - db_type 不再限制为 MySQL/PostgreSQL，改为服务层按"引擎能力注册表"校验，
//     使 TiDB / Doris / ClickHouse / SQLServer / Oracle 也能配置规则；
//   - rule_type 不再只允许 threshold，支持 delta / ratio / absent。
type SaveRuleForm struct {
	Name            string  `json:"name" binding:"required,min=2,max=128"`
	DBType          string  `json:"db_type" binding:"required,max=32"`
	RuleType        string  `json:"rule_type" binding:"required,oneof=threshold delta ratio absent"`
	ConditionType   string  `json:"condition_type" binding:"omitempty,oneof=threshold delta ratio absent"`
	MetricKey       string  `json:"metric_key" binding:"required,max=64"`
	Operator        string  `json:"operator" binding:"required,oneof=>= <= > < = !="`
	Threshold       float64 `json:"threshold"`
	HoldMinutes     int     `json:"hold_minutes" binding:"min=0,max=1440"`
	ForSeconds      int     `json:"for_seconds" binding:"min=0,max=86400"`
	RecoverySeconds int     `json:"recovery_seconds" binding:"min=0,max=86400"`
	NoDataBehavior  string  `json:"no_data_behavior" binding:"omitempty,oneof=ignore alert recover"`
	// AlertLevel 是告警等级的唯一输入。字段 severity（P1/P2/P3）由它派生，
	// 不接受外部传入 —— 曾因前端编辑回填旧 severity 导致两处对不上，
	// 详见 services/runtime.go saveRule 的注释。
	AlertLevel     string   `json:"alert_level" binding:"required,oneof=高 中 低"`
	SilenceMinutes int      `json:"silence_minutes" binding:"min=0,max=1440"`
	GroupKey       string   `json:"group_key" binding:"max=64"`
	ScopeType      string   `json:"scope_type" binding:"required,oneof=global instance"`
	ScopeValue     string   `json:"scope_value" binding:"max=128"`
	NotifyChannels []string `json:"notify_channels"`
	SilencePolicy  []string `json:"silence_policy"`
	Description    string   `json:"description" binding:"max=255"`
	Enabled        bool     `json:"enabled"`
}

// ---------------------------------------------------------------------------
// 指标模板
//
// 模板按数据库类型组织采集指标；实例通过 metrics_template 绑定模板。
// 主机层指标不进模板（与类型无关），因此这里的 metric_keys 只接受引擎层/内核层键。
// ---------------------------------------------------------------------------

// GetMetricTemplateListForm 模板列表过滤。
type GetMetricTemplateListForm struct {
	DBType string `form:"db_type" json:"db_type"`
}

// SaveMetricTemplateForm 新建模板。
// MetricKeys 为空时按 CopyFrom（默认取该类型默认模板）复制一份，便于"以默认为基础裁剪"。
type SaveMetricTemplateForm struct {
	Name        string   `json:"name" binding:"required,min=2,max=64"`
	DisplayName string   `json:"display_name" binding:"max=64"`
	DBType      string   `json:"db_type" binding:"required,max=32"`
	Description string   `json:"description" binding:"max=255"`
	Enabled     *bool    `json:"enabled"`
	MetricKeys  []string `json:"metric_keys"`
	CopyFrom    string   `json:"copy_from" binding:"max=64"`
}

// UpdateMetricTemplateForm 修改模板（标识不可改，实例靠它绑定）。
// Description / MetricKeys 用指针区分"不传"与"清空"。
type UpdateMetricTemplateForm struct {
	Name        string    `json:"name" binding:"max=64"`
	DisplayName string    `json:"display_name" binding:"max=64"`
	Description *string   `json:"description"`
	Enabled     *bool     `json:"enabled"`
	MetricKeys  *[]string `json:"metric_keys"`
}

// BindTemplateInstancesForm 批量绑定实例；mode=unbind 时回落到该类型默认模板。
type BindTemplateInstancesForm struct {
	InstanceIDs []string `json:"instance_ids" binding:"required,min=1,max=200"`
	Mode        string   `json:"mode" binding:"omitempty,oneof=bind unbind"`
}

// ---------------------------------------------------------------------------
// 采集策略：模板 - 实例 两级配置
//
// 表单里不出现任何具体策略字段（周期/保留/超时…只有一个通用 Items 列表），
// 新增策略类型时这里一行都不用改 —— 可扩展性的关键就在这一处克制。
// ---------------------------------------------------------------------------

// StrategyItemInput 一条策略项。Value 的类型由 item_type 的注册表定义决定，
// 所以这里只能是 any，真正的约束在服务层 parseStrategyValue 里做。
type StrategyItemInput struct {
	ItemType string `json:"item_type" binding:"required,max=32"`
	// ItemKey 同类多条时的区分键；单条类型留空。
	ItemKey string `json:"item_key" binding:"max=64"`
	Value   any    `json:"value"`
}

// GetStrategyTemplateListForm 采集策略模板列表过滤。
type GetStrategyTemplateListForm struct {
	DBType string `form:"db_type" json:"db_type"`
}

// SaveStrategyTemplateForm 新建采集策略模板。
// Items 为空时按 CopyFrom（默认取该类型默认模板）复制一份，便于"以默认为基础改两个值"。
type SaveStrategyTemplateForm struct {
	Name        string              `json:"name" binding:"required,min=2,max=64"`
	DisplayName string              `json:"display_name" binding:"max=64"`
	DBType      string              `json:"db_type" binding:"required,max=32"`
	Description string              `json:"description" binding:"max=255"`
	Enabled     *bool               `json:"enabled"`
	Items       []StrategyItemInput `json:"items"`
	CopyFrom    string              `json:"copy_from" binding:"max=64"`
}

// UpdateStrategyTemplateForm 修改采集策略模板。
// Description / Items 用指针区分"不传"与"清空"。
type UpdateStrategyTemplateForm struct {
	Name        string               `json:"name" binding:"max=64"`
	DisplayName string               `json:"display_name" binding:"max=64"`
	Description *string              `json:"description"`
	Enabled     *bool                `json:"enabled"`
	Items       *[]StrategyItemInput `json:"items"`
}

// BindStrategyInstancesForm 把一批实例挂载到某个采集策略模板。
type BindStrategyInstancesForm struct {
	InstanceIDs []string `json:"instance_ids" binding:"required,min=1,max=200"`
}

// UnbindStrategyInstancesForm 解绑一批实例（置空挂载，回落默认模板）。
// 与"切换模板"是同一个动作的两半：先解绑再挂载等于切换，
// 所以这里不提供独立的 switch 接口，避免出现三种语义重叠的写路径。
type UnbindStrategyInstancesForm struct {
	InstanceIDs []string `json:"instance_ids" binding:"required,min=1,max=200"`
}

// SaveInstanceOverridesForm 保存实例级策略覆盖。
// 传空对象表示清空覆盖、恢复全量继承模板。
type SaveInstanceOverridesForm struct {
	Overrides map[string]any `json:"overrides"`
}

// GetInstanceStrategyForm 查看实例生效策略。
type GetInstanceStrategyForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,max=64"`
}

// ============================================================================
// 告警策略模板（与采集策略模板同构：默认模板兜底 + 实例 1:1 挂载 + 规则覆盖式存储）
// ============================================================================

// GetAlertTemplateListForm 告警策略模板列表过滤。
type GetAlertTemplateListForm struct {
	DBType string `form:"db_type" json:"db_type"`
}

// SaveAlertTemplateForm 新建告警策略模板。规则集为空时按 CopyFrom（默认取该类型默认模板）复制一份。
type SaveAlertTemplateForm struct {
	Name        string `json:"name" binding:"required,min=2,max=64"`
	DisplayName string `json:"display_name" binding:"max=64"`
	DBType      string `json:"db_type" binding:"required,max=32"`
	Description string `json:"description" binding:"max=255"`
	Enabled     *bool  `json:"enabled"`
	CopyFrom    string `json:"copy_from" binding:"max=64"`
}

// UpdateAlertTemplateForm 修改告警策略模板基础信息。
type UpdateAlertTemplateForm struct {
	DisplayName string  `json:"display_name" binding:"max=64"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

// BindAlertInstancesForm 把一批实例挂载到某个告警策略模板。
type BindAlertInstancesForm struct {
	InstanceIDs []string `json:"instance_ids" binding:"required,min=1,max=200"`
}

// UnbindAlertInstancesForm 解绑一批实例（置空挂载，回落默认告警模板）。
type UnbindAlertInstancesForm struct {
	InstanceIDs []string `json:"instance_ids" binding:"required,min=1,max=200"`
}

// AlertRuleInput 模板内单条规则的覆盖值。MetricKey 引用指标字典，
// 不允许自由填写；通知渠道/静默策略/作用域不在此保存（由规则行级接口维护）。
type AlertRuleInput struct {
	MetricKey       string  `json:"metric_key" binding:"required,max=64"`
	Name            string  `json:"name" binding:"max=128"`
	Operator        string  `json:"operator" binding:"max=8"`
	Threshold       float64 `json:"threshold"`
	AlertLevel      string  `json:"alert_level" binding:"max=16"`
	ForSeconds      int     `json:"for_seconds" binding:"min=0,max=86400"`
	RecoverySeconds int     `json:"recovery_seconds" binding:"min=0,max=86400"`
	NoDataBehavior  string  `json:"no_data_behavior" binding:"omitempty,oneof=ignore alert recover"`
	SilenceMinutes  int     `json:"silence_minutes" binding:"min=0,max=1440"`
	Description     string  `json:"description" binding:"max=255"`
	Enabled         bool    `json:"enabled"`
}

// AlertLevelInput 单个告警档位（P0–P3 之一）。
// 一个指标可以有 1~4 档，档数不强制配满；等级决定"有多严重"，阈值决定"何时算命中"。
type AlertLevelInput struct {
	Level          string  `json:"level" binding:"max=16"`
	Operator       string  `json:"operator" binding:"max=8"`
	Threshold      float64 `json:"threshold"`
	ForSeconds     int     `json:"for_seconds" binding:"min=0,max=86400"`
	SilenceMinutes int     `json:"silence_minutes" binding:"min=0,max=1440"`
	Enabled        bool    `json:"enabled"`
}

// AlertMetricRulesInput 一个监控项的全部档位 + 指标级策略。
// 指标级策略（恢复判定 / 无数据行为 / 说明）落库时写到该指标的每一档行上，
// 读取时任取一行即可 —— 避免"策略到底存在哪一档"的歧义。
type AlertMetricRulesInput struct {
	MetricKey string `json:"metric_key" binding:"required,max=64"`
	// Operator 指标级比较方向：同指标所有档位必须一致（前端只读展示，由字典决定）
	Operator        string            `json:"operator" binding:"omitempty,max=8"`
	RecoverySeconds int               `json:"recovery_seconds" binding:"min=0,max=86400"`
	NoDataBehavior  string            `json:"no_data_behavior" binding:"omitempty,oneof=ignore alert recover"`
	Description     string            `json:"description" binding:"max=255"`
	Levels          []AlertLevelInput `json:"levels" binding:"max=4,dive"`
}

// SaveAlertTemplateRulesForm 保存模板规则集（同 (指标,等级) 覆盖、新档插入）。
//
// Metrics 采用指针：nil = 老客户端仍在提交 Rules（走兼容分支，语义为全量覆盖），
// 非 nil = 分组口径提交。
//
// ReplaceAll 决定"未提交的指标"怎么办：
//   - false（默认）：只替换本次提交里出现过的指标，其余指标原样保留 —— 部分提交安全；
//   - true：提交集即该模板的完整期望状态，未提交的指标规则会被删除（配置页走这条）。
//
// 注意：Metrics 为 nil 时（老口径）服务端强制按 ReplaceAll 处理，保持向后兼容。
type SaveAlertTemplateRulesForm struct {
	Metrics    *[]AlertMetricRulesInput `json:"metrics"`
	ReplaceAll bool                     `json:"replace_all"`
	Rules      []AlertRuleInput         `json:"rules" binding:"max=200,dive"`
}

// GetAlertTemplateDetailForm 模板详情（含规则集）。
type GetAlertTemplateDetailForm struct{}

// GetAlertCenterForm 告警中心检索。
//
// 与「大屏告警区」的区别：大屏只给"当前这一刻"的切片，告警中心要回答
// "过去一段时间内发生过什么、谁处理的、恢复了没有、有没有被忽略"——
// 因此这里保留已恢复（recovered）事件，且默认按"未恢复优先"排序。
type GetAlertCenterForm struct {
	MonitorListFilter
	// Status 事件状态：active / pending / acknowledged / recovered，空表示全部。
	// 另支持聚合值 unresolved —— 未恢复（active + pending + acknowledged），
	// 供告警历史的状态筛选使用；该聚合项不排除已忽略（审计视角，忽略由
	// only_ignored 独立控制），与 Unresolved 布尔参数的排除口径不同。
	Status string `form:"status" json:"status"`
	// Unresolved 只看未恢复（active + pending + acknowledged）——「当前告警」视图专用。
	// 判定是 status <> recovered：pending 只是观察期尚未转正，它依然是一条未闭合的
	// 告警；把它排除会让同一时刻出现"大屏 0 条 / 告警中心 3 条"这类两个数。
	// 当前告警是"状态维度"的切片，不该被触发时间截断：于 20 天前触发、
	// 至今未恢复的 P1 必须留在当前告警里，因此该视图不传 range_hours。
	// 注意：与 Status 同时出现时是 AND 叠加（GORM Where 逐条追加），不是覆盖 ——
	// 需要收敛语义时用更具体的参数（Unclaimed/RecoveredToday），别指望它盖掉 status。
	Unresolved bool `form:"unresolved" json:"unresolved"`
	// Unclaimed 只看「未认领」：pending（观察期）+ active（已转正未认领），
	// 排除 acknowledged（已认领）与 recovered（已恢复）。
	// 与告警概览「未认领告警」卡片严格同口径 —— 该卡片取 summary.active，而后端
	// 聚合时把非 recovered/acknowledged 的状态都计入（见 monitor_alert_center.go 的
	// default 分支），因此下钻必须用本参数；若沿用 status=active，观察期事件会被
	// 漏掉，出现"卡片 3 条、点开 0 条"的自相矛盾。
	Unclaimed bool `form:"unclaimed" json:"unclaimed"`
	// AlertID 对外告警 ID（alm-xxxxxxxxxxxx）精确查询；空表示不过滤。
	// 保留以兼容旧调用；新搜索请使用 Keyword。
	AlertID string `form:"alert_id" json:"alert_id"`
	// Keyword 模糊匹配：告警ID、实例名称（remark）、IP/Host（hostname:port）。
	Keyword string `form:"keyword" json:"keyword"`
	// Severity 严重级别 P0 / P1 / P2 / P3，空表示全部；支持逗号分隔多级别
	// （如 "P0,P1"，概览「高危未处理」下钻一次查齐两个级别）。
	Severity  string `form:"severity" json:"severity"` // MetricKey 指标键，空表示全部。
	MetricKey string `form:"metric_key" json:"metric_key"`
	// OnlyIgnored 只看被人工忽略的事件（含已恢复的）。
	OnlyIgnored bool `form:"only_ignored" json:"only_ignored"`
	// ExcludeIgnored 排除被人工忽略的事件。供大屏「告警数」圆饼的待认领/处理中
	// 下钻使用 —— 圆饼计数（loadScreenAlertCounts）口径为
	// status <> 'recovered' AND ignored = false，弹窗列表必须同口径，
	// 否则出现"圆饼 11 / 弹窗 13"的差额。与 OnlyIgnored 同时为真时互斥成空集，
	// 调用方不应这样传。告警历史 Tab 的审计视角不用此参数（需能查到忽略事实）。
	ExcludeIgnored bool `form:"exclude_ignored" json:"exclude_ignored"`
	// RangeHours 最近 N 小时（按触发时间）；0 表示不限。
	RangeHours int `form:"range_hours" json:"range_hours"`
	// RecoveredToday 只看今日恢复（按恢复时间 recovered_at 过滤，与概览
	// today_recovered 完全同口径——触发可能在多天前，range_hours 会漏）。
	// 置真时隐含 status=recovered，覆盖 Status 参数。
	RecoveredToday bool `form:"recovered_today" json:"recovered_today"`
	// StartTime / EndTime 自定义区间（"2006-01-02 15:04:05"）。
	// 状态为 recovered / RecoveredToday 时按恢复时间过滤；
	// FirstSeen=true 时按首次命中时间 first_seen_at 过滤（与趋势图下钻同口径）；
	// 其余按触发时间过滤。
	StartTime string `form:"start_time" json:"start_time"`
	EndTime   string `form:"end_time" json:"end_time"`
	// FirstSeen 为 true 时按 first_seen_at 过滤，供「近 N 天告警趋势」柱体下钻使用，
	// 保证弹窗列表与趋势图「新增告警数」落桶口径完全一致（触发时间可能晚于首次命中时间）。
	FirstSeen bool `form:"first_seen" json:"first_seen"`
	// SortBy 排序口径：triggered_at = 按触发时间倒序（后发生在前，告警历史 Tab 用）；
	// 空或其他值 = 默认排序（状态优先 → 严重度 → 最近命中，概览与下钻弹窗用）。
	SortBy   string `form:"sort_by" json:"sort_by"`
	Page     int    `form:"page" json:"page"`
	PageSize int    `form:"page_size" json:"page_size"`
	// Scope 实例下钻弹窗的实例集合口径（仅 /monitor/alerts/instances 使用）：
	//   alert（默认）有未恢复告警的实例（unresolved=true 时同口径）；
	//   all 全部纳管实例 / healthy 健康 / risk 风险 / fault 故障——由健康分级
	//   引擎的四分类圈定集合，此时状态类过滤（unresolved/status）不参与圈定。
	Scope string `form:"scope" json:"scope"`
}

// GetAlertStatsForm 告警概览统计（今日已恢复 + 近 N 天趋势）。
// 只接受实例维度筛选；趋势窗口默认近 7 天，days 可在 1-30 内调整
// （告警中心概览趋势支持最长 30 天的自定义窗口，非法/超限值按默认或上限处理）。
type GetAlertStatsForm struct {
	MonitorListFilter
	// Days 趋势窗口天数（含今天），默认 7，上限 30。
	Days int `form:"days" json:"days"`
}

// GetAlertTrendForm 大屏「近 7 天告警趋势」卡。
// 时间刻意与页面顶部范围解耦（全局管实时、本卡管中长期），只接受预设窗口，
// 不开放自定义区间——大屏场景放日期选择器太重，预设 chips 点一下就切。
type GetAlertTrendForm struct {
	MonitorListFilter
	// Preset 预设窗口：today / 3d / 7d / 14d，空串或未知值按 7d。
	Preset string `form:"preset" json:"preset"`
}
