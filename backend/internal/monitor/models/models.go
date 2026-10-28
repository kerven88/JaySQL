package models

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

type InsightMonitorTargetConfigs struct {
	*commonModels.Model
	InstanceID       string                `gorm:"type:char(15);not null;uniqueIndex:uniq_monitor_instance;comment:实例ID" json:"instance_id"`
	Enabled          bool                  `gorm:"type:boolean;not null;default:true;comment:是否启用监控" json:"enabled"`
	DBType           commonModels.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';comment:数据库类型;index:idx_monitor_db_type" json:"db_type"`
	Business         string                `gorm:"type:varchar(64);not null;default:'';comment:所属业务" json:"business"`
	GroupName        string                `gorm:"column:group_name;type:varchar(64);not null;default:'';comment:实例分组" json:"group_name"`
	PrometheusTarget string                `gorm:"type:varchar(128);not null;default:'';comment:Prometheus目标标识" json:"prometheus_target"`
	ResourceSource   string                `gorm:"type:varchar(32);not null;default:'prometheus';comment:基础资源采集来源" json:"resource_source"`
	EngineSource     string                `gorm:"type:varchar(32);not null;default:'direct';comment:引擎指标采集来源" json:"engine_source"`
	// --- 采集策略的「缓存列」 ---
	// 下面四项 + ResourceSource/EngineSource 是**生效结果的缓存**，不是配置输入。
	// 配置的唯一输入是 strategy_template_id + strategy_overrides（模板-实例两级），
	// 挂载/切换/解绑时由 services.ResolveInstanceStrategy 计算出结果后回写到这里。
	//
	// 为什么要保留这些列而不是直接删掉改读模板：采集循环是每轮都要读的热路径，
	// 让它每次现算"模板 → 覆盖 → 平台默认"会把一次配置读取放大成三张表联查 + JSON 解析。
	// 缓存列让采集器只读一行，模板侧的复杂度被挡在配置写入路径上。
	ScrapeIntervalSeconds int            `gorm:"type:int;not null;default:0;comment:采集周期秒（0=跟随平台周期），由采集策略模板解析回写" json:"scrape_interval_seconds"`
	RetentionHours        int            `gorm:"type:int;not null;default:168;comment:保留时长小时，由采集策略模板解析回写" json:"retention_hours"`
	TimeoutSeconds        int            `gorm:"type:int;not null;default:5;comment:超时秒，由采集策略模板解析回写" json:"timeout_seconds"`
	RetryCount            int            `gorm:"type:int;not null;default:2;comment:失败重试次数，由采集策略模板解析回写" json:"retry_count"`
	MetricsTemplate       string         `gorm:"type:varchar(64);not null;default:'';comment:指标模板（空=未绑定，用类型默认模板）" json:"metrics_template"`
	Labels                datatypes.JSON `gorm:"type:json;null;default:null;comment:实例标签" json:"labels"`

	// --- 采集策略挂载（模板-实例两级配置的「实例」侧） ---
	// StrategyTemplateID 挂载的采集策略模板；NULL = 未挂载，解析时回落到该 db_type 的默认模板。
	// 用外键 + ON DELETE SET NULL 而不是继续沿用"名字字符串"：名字会被改名/删除打断，
	// 变成一条没人认领的悬空引用；用 ID 至少能保证引用要么有效、要么明确为空。
	StrategyTemplateID *uint64 `gorm:"type:bigint unsigned;null;default:null;index:idx_monitor_strategy_tpl;comment:挂载的采集策略模板ID（NULL=回落默认模板）" json:"strategy_template_id"`
	// StrategyOverrides 实例级策略覆盖：{"scrape_interval": 300} 形式，键为策略项类型。
	// 存在的键优先于模板值；不存在的键继承模板。用于"个别实例必须特殊照顾"的场景，
	// 避免为了一两个实例复制出一堆模板。
	StrategyOverrides datatypes.JSON `gorm:"type:json;null;default:null;comment:实例级策略覆盖（覆盖挂载模板的同名策略项）" json:"strategy_overrides"`

	// --- 告警策略挂载（告警模板-实例两级配置的「实例」侧） ---
	// AlertTemplateID 挂载的告警策略模板；NULL = 未挂载，告警评估时回落到该 db_type 的默认告警模板。
	// 与采集策略一样坚持 1 实例 : 1 模板：多对多会让"同一指标两条阈值"的告警语义无法裁决。
	AlertTemplateID *uint64 `gorm:"type:bigint unsigned;null;default:null;index:idx_monitor_alert_tpl;comment:挂载的告警策略模板ID（NULL=回落默认告警模板）" json:"alert_template_id"`

	// --- 采集运行态（由后台采集器维护，页面只读） ---
	DeployMode          string                  `gorm:"type:varchar(24);not null;default:standalone;comment:部署形态 standalone/master_slave/cluster" json:"deploy_mode"`
	LastCollectAt       *commonModels.LocalTime `gorm:"comment:最近一次采集时间" json:"last_collect_at"`
	LastCollectStatus   string                  `gorm:"type:varchar(16);not null;default:'never';comment:最近采集状态 success/partial/failed/never" json:"last_collect_status"`
	LastCollectMessage  string                  `gorm:"type:varchar(255);not null;default:'';comment:最近采集结果说明" json:"last_collect_message"`
	LastCollectMs       int                     `gorm:"type:int;not null;default:0;comment:最近采集耗时毫秒" json:"last_collect_ms"`
	LastSuccessAt       *commonModels.LocalTime `gorm:"comment:最近一次成功采集时间" json:"last_success_at"`
	ConsecutiveFailures int                     `gorm:"type:int;not null;default:0;comment:连续采集失败次数" json:"consecutive_failures"`
	CollectQuality      string                  `gorm:"type:varchar(16);not null;default:'no_data';comment:整体数据质量 ok/partial/error/no_data" json:"collect_quality"`
}

func (InsightMonitorTargetConfigs) TableName() string {
	return "insight_monitor_target_configs"
}

// InsightMonitorScrapeSettings 采集频率设置（平台级单行表，id 固定为 1）。
//
// 这一行就是「采集频率」的唯一数据源：Prometheus 抓取间隔、JaySQL 落库周期、
// PromQL 采样窗口与历史聚合桶宽，全部由 IntervalSeconds 派生，任何地方都不再硬编码。
// 之所以单独建表而不是塞进实例配置：Prometheus 的 global.scrape_interval 是全局的，
// 一个平台只能有一个抓取节奏，实例级周期只能在此基础上取整数倍，不能反过来。
type InsightMonitorScrapeSettings struct {
	*commonModels.Model
	IntervalSeconds int    `gorm:"type:int;not null;default:60;comment:采集周期秒（唯一数据源，需为档位值或15的倍数）" json:"interval_seconds"`
	AllowCustom     bool   `gorm:"type:boolean;not null;default:false;comment:是否允许档位之外的自定义数值" json:"allow_custom"`
	UpdatedByName   string `gorm:"type:varchar(64);not null;default:'';comment:最近修改人" json:"updated_by_name"`
}

func (InsightMonitorScrapeSettings) TableName() string {
	return "insight_monitor_scrape_settings"
}

// InsightMonitorHealthGradeSettings 实例健康分级设置（平台级单行表，id 固定为 1）。
//
// 这一行是"什么是健康 / 风险 / 故障"的唯一数据源：等级定义（P0~P3）、结果项归属、
// 判定规则全部存在 Payload 这一份 JSON 里，评估引擎不持有任何硬编码口径。
//
// Customized=false 时，进程始终使用内置默认配置，且当内置默认版本（Version）高于库里
// 记录的版本时会自动跟随升级 —— 这样"用户没改过"的部署不会卡在历史默认值上。
type InsightMonitorHealthGradeSettings struct {
	*commonModels.Model
	Version       int            `gorm:"type:int;not null;default:1;comment:内置默认配置版本（未自定义时自动跟随升级）" json:"version"`
	Customized    bool           `gorm:"type:boolean;not null;default:false;comment:是否用户自定义（false=始终用内置默认）" json:"customized"`
	Payload       datatypes.JSON `gorm:"type:json;null;default:null;comment:完整分级配置 JSON" json:"payload"`
	UpdatedByName string         `gorm:"type:varchar(64);not null;default:'';comment:最近修改人" json:"updated_by_name"`
}

func (InsightMonitorHealthGradeSettings) TableName() string {
	return "insight_monitor_health_grade_settings"
}

type InsightMonitorAlertRules struct {
	*commonModels.Model
	Name   string                `gorm:"type:varchar(128);not null;default:'';comment:规则名称" json:"name"`
	DBType commonModels.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';comment:数据库类型;index:idx_alert_rule_db_type" json:"db_type"`
	// TemplateID 归属的告警策略模板。规则是模板的组成部分，模板被删时规则随之删除；
	// 0 = 历史遗留（迁移前的全局规则），正常情况下迁移完成后不应再出现。
	// 模板内规则按 (template_id, metric_key) 覆盖式存储：保存时先查同键规则，
	// 有则原行更新（保持 rule_id 稳定，告警事件外链不断），无则插入，不追加重复记录。
	TemplateID     uint64         `gorm:"type:bigint unsigned;not null;default:0;index:idx_alert_rule_template;comment:归属告警模板ID（0=迁移前历史数据）" json:"template_id"`
	RuleType       string         `gorm:"type:varchar(32);not null;default:'threshold';comment:规则类型" json:"rule_type"`
	MetricKey      string         `gorm:"type:varchar(64);not null;default:'';comment:指标键" json:"metric_key"`
	Operator       string         `gorm:"type:varchar(8);not null;default:'>=';comment:比较符" json:"operator"`
	Threshold      float64        `gorm:"type:decimal(20,4);not null;default:0;comment:阈值" json:"threshold"`
	HoldMinutes    int            `gorm:"type:int;not null;default:0;comment:持续分钟数（兼容字段，等价于 for_seconds）" json:"hold_minutes"`
	AlertLevel     string         `gorm:"type:varchar(16);not null;default:'P2';comment:告警级别 P0/P1/P2/P3" json:"alert_level"`
	ScopeType      string         `gorm:"type:varchar(32);not null;default:'global';comment:作用域类型" json:"scope_type"`
	ScopeValue     string         `gorm:"type:varchar(128);not null;default:'';comment:作用域值" json:"scope_value"`
	NotifyChannels datatypes.JSON `gorm:"type:json;null;default:null;comment:通知策略" json:"notify_channels"`
	SilencePolicy  datatypes.JSON `gorm:"type:json;null;default:null;comment:静默策略" json:"silence_policy"`
	Description    string         `gorm:"type:varchar(255);not null;default:'';comment:规则说明" json:"description"`
	Enabled        bool           `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"enabled"`

	// --- 告警引擎字段（新增，此前是"声明了但全仓无人读"的死字段） ---
	ConditionType   string `gorm:"type:varchar(32);not null;default:'threshold';comment:条件类型 threshold/delta/ratio/absent" json:"condition_type"`
	ForSeconds      int    `gorm:"type:int;not null;default:0;comment:持续多少秒才真正触发" json:"for_seconds"`
	RecoverySeconds int    `gorm:"type:int;not null;default:300;comment:连续正常多少秒才判定恢复" json:"recovery_seconds"`
	NoDataBehavior  string `gorm:"type:varchar(16);not null;default:'ignore';comment:无数据行为 ignore/alert/recover" json:"no_data_behavior"`
	// Severity 是 AlertLevel 的派生产物（P0/P1/P2/P3 同码直通），只用于列表展示。
	// 告警评估链路不读这一列：事件 severity 由 rule.AlertLevel 现算（monitor_alerts.go）。
	// 因此它必须与 alert_level 严格同步，只允许由 saveRule 派生写入，不接受外部传值；
	// 存量不一致由 monitor_schema.go 的启动期对账 SQL 刷平。
	Severity       string `gorm:"type:varchar(8);not null;default:'P2';comment:严重级别 P0/P1/P2/P3（由 alert_level 派生，不可独立设置）" json:"severity"`
	SilenceMinutes int    `gorm:"type:int;not null;default:30;comment:同指纹重复通知间隔(分钟)" json:"silence_minutes"`
	GroupKey       string `gorm:"type:varchar(64);not null;default:'';comment:收敛分组" json:"group_key"`
}

func (InsightMonitorAlertRules) TableName() string {
	return "insight_monitor_alert_rules"
}

// InsightMonitorAlertTemplates 告警策略模板：一组告警规则的集合，按数据库类型分类。
// 与采集策略模板同构：每类型一个内置默认模板（兜底），用户可自建模板并把实例 1:1 挂载上来。
type InsightMonitorAlertTemplates struct {
	*commonModels.Model
	Name        string                `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_alert_tpl_name;comment:模板标识（唯一键）" json:"name"`
	DisplayName string                `gorm:"type:varchar(128);not null;default:'';comment:展示名" json:"display_name"`
	DBType      commonModels.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';index:idx_alert_tpl_db_type;comment:数据库类型" json:"db_type"`
	Description string                `gorm:"type:varchar(255);not null;default:'';comment:模板说明" json:"description"`
	// Builtin 内置模板（播种产生）：不可删除；IsDefault 该类型的兜底模板：不可停用、不可删除。
	Builtin   bool `gorm:"type:boolean;not null;default:false;comment:是否内置模板" json:"builtin"`
	IsDefault bool `gorm:"type:boolean;not null;default:false;comment:是否该类型的默认兜底模板" json:"is_default"`
	Enabled   bool `gorm:"type:boolean;not null;default:true;comment:是否启用（停用后挂载实例回落默认模板）" json:"enabled"`
}

func (InsightMonitorAlertTemplates) TableName() string {
	return "insight_monitor_alert_templates"
}

// InsightMonitorAlertRuleTombstone 默认模板里被用户显式删除的告警指标墓碑。
//
// 默认模板的规则由平台播种并持续补齐（ensureDefaultAlertRules），这带来一个矛盾：
// 用户删掉某条默认告警后，下一个周期会被原样加回，删除无法持久化。
// 墓碑就是"用户删过"的持久凭证：补档时跳过有墓碑的 (模板, 指标)；
// 用户重新为该指标配置档位时撤碑，恢复默认补档资格。
// 墓碑只影响告警规则，不触碰采集与展示——监控数据的采集挂 target_configs、
// 展示读 metric_latest，与告警规则完全解耦。
type InsightMonitorAlertRuleTombstone struct {
	*commonModels.Model
	TemplateID uint64 `gorm:"not null;default:0;uniqueIndex:uk_alert_rule_tombstone,priority:1;comment:模板ID" json:"template_id"`
	MetricKey  string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_alert_rule_tombstone,priority:2;comment:指标键（规范键）" json:"metric_key"`
}

func (InsightMonitorAlertRuleTombstone) TableName() string {
	return "insight_monitor_alert_rule_tombstones"
}

type InsightMonitorMetricSnapshots struct {
	*commonModels.Model
	InstanceID        string                `gorm:"type:char(15);not null;index:idx_monitor_snapshot_instance;comment:实例ID" json:"instance_id"`
	DBType            commonModels.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';index:idx_monitor_snapshot_db_type;comment:数据库类型" json:"db_type"`
	Status            string                `gorm:"type:varchar(32);not null;default:'healthy';comment:采集状态" json:"status"`
	AlertCount        int                   `gorm:"type:int;not null;default:0;comment:告警数量" json:"alert_count"`
	CPU               float64               `gorm:"type:double;not null;default:0;comment:CPU" json:"cpu"`
	Memory            float64               `gorm:"type:double;not null;default:0;comment:内存" json:"memory"`
	Disk              float64               `gorm:"type:double;not null;default:0;comment:磁盘" json:"disk"`
	IO                float64               `gorm:"type:double;not null;default:0;comment:IO" json:"io"`
	NetworkIn         float64               `gorm:"type:double;not null;default:0;comment:网络入" json:"network_in"`
	NetworkOut        float64               `gorm:"type:double;not null;default:0;comment:网络出" json:"network_out"`
	QPS               float64               `gorm:"type:double;not null;default:0;comment:QPS" json:"qps"`
	TPS               float64               `gorm:"type:double;not null;default:0;comment:TPS" json:"tps"`
	ReadRows          float64               `gorm:"type:double;not null;default:0;comment:读行数" json:"read_rows"`
	WriteRows         float64               `gorm:"type:double;not null;default:0;comment:写行数" json:"write_rows"`
	ReadBytes         float64               `gorm:"type:double;not null;default:0;comment:读字节" json:"read_bytes"`
	WriteBytes        float64               `gorm:"type:double;not null;default:0;comment:写字节" json:"write_bytes"`
	CacheHitRatio     float64               `gorm:"type:double;not null;default:0;comment:缓存命中率" json:"cache_hit_ratio"`
	Locks             float64               `gorm:"type:double;not null;default:0;comment:锁等待" json:"locks"`
	Connections       float64               `gorm:"type:double;not null;default:0;comment:连接数" json:"connections"`
	ActiveConnections float64               `gorm:"type:double;not null;default:0;comment:活跃连接数" json:"active_connections"`
	MaxConnections    float64               `gorm:"type:double;not null;default:0;comment:最大连接数" json:"max_connections"`
}

func (InsightMonitorMetricSnapshots) TableName() string {
	return "insight_monitor_metric_snapshots"
}

type InsightMonitorAlertEvents struct {
	*commonModels.Model
	InstanceID    string                  `gorm:"type:char(15);not null;index:idx_monitor_alert_instance;comment:实例ID" json:"instance_id"`
	DBType        commonModels.EnumType   `gorm:"type:varchar(32);not null;default:'MySQL';index:idx_monitor_alert_db_type;comment:数据库类型" json:"db_type"`
	Name          string                  `gorm:"type:varchar(128);not null;default:'';comment:告警名称" json:"name"`
	MetricKey     string                  `gorm:"type:varchar(64);not null;default:'';index:idx_monitor_alert_metric;comment:指标键" json:"metric_key"`
	Level         string                  `gorm:"type:varchar(16);not null;default:'P2';comment:告警级别 P0/P1/P2/P3" json:"level"`
	Status        string                  `gorm:"type:varchar(16);not null;default:'active';index:idx_monitor_alert_status;comment:告警状态" json:"status"`
	Threshold     float64                 `gorm:"type:double;not null;default:0;comment:阈值" json:"threshold"`
	ObservedValue float64                 `gorm:"type:double;not null;default:0;comment:触发值" json:"observed_value"`
	Message       string                  `gorm:"type:varchar(255);not null;default:'';comment:告警消息" json:"message"`
	TriggeredAt   commonModels.LocalTime  `gorm:"comment:触发时间" json:"triggered_at"`
	RecoveredAt   *commonModels.LocalTime `gorm:"comment:恢复时间" json:"recovered_at"`

	// --- 生命周期与去重（新增） ---
	RuleID uint64 `gorm:"type:bigint unsigned;not null;default:0;comment:命中规则ID，硬编码阈值告警为0" json:"rule_id"`
	// Fingerprint 事件去重键 = 实例 + 指标（不含 rule_id）。
	// 阶梯告警下同指标多档共存，若把 rule_id 编进指纹，"同一指标从 P2 升到 P1"会被当成
	// 两个不同事件：旧事件永远不恢复、新事件从零开始累计，用户同时看到两条互相矛盾的告警。
	Fingerprint string `gorm:"type:varchar(160);not null;default:'';index:idx_monitor_alert_fp;comment:去重键 instance|metric" json:"fingerprint"`
	// AlertID 对外展示的告警ID：alm- + 12 位随机小写字母数字（如 alm-7s9dg2k5p8az）。
	// 用普通索引而非唯一索引：存量回填完成前多行空值会让唯一索引创建失败；
	// 唯一性由「生成时查库校验 + 插入冲突告警」保证（见 monitor_alert_ids.go）。
	AlertID     string                  `gorm:"type:char(16);not null;default:'';index:idx_monitor_alert_alert_id;comment:对外告警ID alm-xxxxxxxxxxxx" json:"alert_id"`
	Severity    string                  `gorm:"type:varchar(8);not null;default:'P2';index:idx_monitor_alert_severity;comment:严重级别 P0/P1/P2/P3（由 level 派生，不可独立设置）" json:"severity"`
	FirstSeenAt *commonModels.LocalTime `gorm:"comment:首次命中时间" json:"first_seen_at"`
	LastSeenAt  *commonModels.LocalTime `gorm:"comment:最近命中时间" json:"last_seen_at"`
	Occurrences int                     `gorm:"type:int;not null;default:0;comment:累计命中次数" json:"occurrences"`
	DurationSec int64                   `gorm:"type:bigint;not null;default:0;comment:持续时长(秒)" json:"duration_sec"`
	PeakValue   float64                 `gorm:"type:double;not null;default:0;comment:峰值" json:"peak_value"`
	AckedBy     string                  `gorm:"type:varchar(64);not null;default:'';comment:确认人" json:"acked_by"`
	AckedAt     *commonModels.LocalTime `gorm:"comment:确认时间" json:"acked_at"`
	Suppressed  bool                    `gorm:"type:boolean;not null;default:false;comment:是否被抑制(页面展示但不通知)" json:"suppressed"`
	Advice      string                  `gorm:"type:varchar(255);not null;default:'';comment:建议处置" json:"advice"`

	// --- 人工处置（大屏「忽略」按钮；与 Suppressed 区分：Suppressed 是采集失败期的自动抑制，
	// 会被评估器按轮覆写，人工忽略必须独立存列，否则下一轮就被冲掉） ---
	Ignored   bool                    `gorm:"type:boolean;not null;default:false;comment:是否被人工忽略" json:"ignored"`
	IgnoredBy string                  `gorm:"type:varchar(64);not null;default:'';comment:忽略人" json:"ignored_by"`
	IgnoredAt *commonModels.LocalTime `gorm:"comment:忽略时间" json:"ignored_at"`
}

func (InsightMonitorAlertEvents) TableName() string {
	return "insight_monitor_alert_events"
}

// InsightMonitorMetricLatest 指标最新值：每实例每指标（含子维度）一行。
// 页面首屏只查这张表，扫描量恒定，不碰时序表。
type InsightMonitorMetricLatest struct {
	InstanceID string                 `gorm:"column:instance_id;type:char(15);not null;primaryKey;comment:实例ID" json:"instance_id"`
	MetricKey  string                 `gorm:"column:metric_key;type:varchar(64);not null;primaryKey;comment:指标键" json:"metric_key"`
	SubDims    string                 `gorm:"column:sub_dims;type:varchar(128);not null;default:'';primaryKey;comment:子维度组合" json:"sub_dims"`
	Value      *float64               `gorm:"column:value;type:double;null;default:null;comment:指标值，quality!=ok 时为 NULL" json:"value"`
	Quality    string                 `gorm:"column:quality;type:varchar(16);not null;default:'no_data';comment:数据质量" json:"quality"`
	Unit       string                 `gorm:"column:unit;type:varchar(16);not null;default:'';comment:单位" json:"unit"`
	Source     string                 `gorm:"column:source;type:varchar(16);not null;default:'direct';comment:来源 direct/prometheus/ssh/platform" json:"source"`
	Reason     string                 `gorm:"column:reason;type:varchar(255);not null;default:'';comment:不可用原因" json:"reason"`
	CollectMs  int                    `gorm:"column:collect_ms;type:int;not null;default:0;comment:采集耗时毫秒" json:"collect_ms"`
	Ts         commonModels.LocalTime `gorm:"column:ts;comment:该值采集时刻" json:"ts"`
	UpdatedAt  commonModels.LocalTime `gorm:"column:updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
}

func (InsightMonitorMetricLatest) TableName() string {
	return "insight_monitor_metric_latest"
}

// InsightMonitorMetricPoints 指标原始点（窄表）。
// 加指标是纯 INSERT、零 DDL；quality!=ok 时 value 为 NULL（绝不写 0）。
// 该表由 EnsureMonitorSchema 以分区 DDL 创建，不参与 AutoMigrate。
type InsightMonitorMetricPoints struct {
	ID         uint64                 `gorm:"column:id;type:bigint unsigned;not null;primaryKey;autoIncrement" json:"id"`
	InstanceID string                 `gorm:"column:instance_id;type:char(15);not null;comment:实例ID" json:"instance_id"`
	DBType     string                 `gorm:"column:db_type;type:varchar(32);not null;default:'';comment:数据库类型" json:"db_type"`
	MetricKey  string                 `gorm:"column:metric_key;type:varchar(64);not null;comment:指标键" json:"metric_key"`
	SubDims    string                 `gorm:"column:sub_dims;type:varchar(128);not null;default:'';comment:子维度组合" json:"sub_dims"`
	Ts         commonModels.LocalTime `gorm:"column:ts;comment:采集时刻" json:"ts"`
	Value      *float64               `gorm:"column:value;type:double;null;default:null;comment:指标值，quality!=ok 时为 NULL" json:"value"`
	Quality    string                 `gorm:"column:quality;type:varchar(16);not null;default:'ok';comment:数据质量" json:"quality"`
	Source     string                 `gorm:"column:source;type:varchar(16);not null;default:'direct';comment:来源" json:"source"`
	CollectMs  int                    `gorm:"column:collect_ms;type:int;not null;default:0;comment:采集耗时毫秒" json:"collect_ms"`
	CreatedAt  commonModels.LocalTime `gorm:"column:created_at;comment:入库时间" json:"created_at"`
}

func (InsightMonitorMetricPoints) TableName() string {
	return "insight_monitor_metric_points"
}

// ============================================================================
// 指标定义说明（原「指标字典」已下线）
//
// 原先指标元信息落在 insight_monitor_metric_catalog 表里，运维可改 enabled /
// default_threshold 等字段，但大量指标并未被纳入实际监控项，反而带来冗余配置与
// 「页面显示一套、告警判定另一套」的歧义。
// 现在指标定义唯一来源是代码内的 builtinMetricCatalog（services/monitor_metrics.go），
// 表已移除，不再落库、不再有可运维改写的副本。
// ============================================================================

// InsightMonitorMetricTemplates 指标模板：按数据库类型组织的采集指标集合。
//
// 定位：把原先"代码里写死的 EngineScope 约束"升级为可运维的实例级配置。
// 一个实例通过 insight_monitor_target_configs.metrics_template 绑定一个模板，
// 采集时的 wantMetric 门控 = 模板勾选 ∩ 字典全局启用 ∩ EngineScope。
//
// 约束（与设计评审一致）：
//   - 主机层（res.*）指标与数据库类型无关，不进模板，始终按全局字典采集；
//   - 每个数据库类型至少有一个 is_default=true 的内置模板，实例未绑定或绑定失效时回落，
//     避免"未绑定 = 不采集"造成静默监控盲区。
type InsightMonitorMetricTemplates struct {
	*commonModels.Model
	Name        string `gorm:"type:varchar(64);not null;uniqueIndex:uk_template_name;comment:模板标识（实例绑定用）" json:"name"`
	DisplayName string `gorm:"type:varchar(64);not null;default:'';comment:模板名称" json:"display_name"`
	DBType      string `gorm:"type:varchar(32);not null;default:'';comment:归属数据库类型（小写引擎键）" json:"db_type"`
	Description string `gorm:"type:varchar(255);not null;default:'';comment:模板说明" json:"description"`
	Builtin     bool   `gorm:"type:boolean;not null;default:false;comment:是否内置" json:"builtin"`
	IsDefault   bool   `gorm:"type:boolean;not null;default:false;comment:是否该类型的默认模板（实例兜底绑定）" json:"is_default"`
	Enabled     bool   `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"enabled"`
}

func (InsightMonitorMetricTemplates) TableName() string {
	return "insight_monitor_metric_templates"
}

// InsightMonitorTemplateMetrics 模板 × 指标 关联表。
// 只允许存 scope IN (engine, kernel) 的指标键：主机层不进模板。
type InsightMonitorTemplateMetrics struct {
	ID         uint64                 `gorm:"type:bigint unsigned;not null;primaryKey;autoIncrement" json:"id"`
	TemplateID uint64                 `gorm:"type:bigint unsigned;not null;uniqueIndex:uk_template_metric,priority:1;comment:模板ID" json:"template_id"`
	MetricKey  string                 `gorm:"type:varchar(64);not null;uniqueIndex:uk_template_metric,priority:2;comment:指标键" json:"metric_key"`
	CreatedAt  commonModels.LocalTime `gorm:"comment:创建时间" json:"created_at"`
}

func (InsightMonitorTemplateMetrics) TableName() string {
	return "insight_monitor_template_metrics"
}

// ============================================================================
// 采集策略：模板 - 实例 两级配置
//
// 与「指标模板」是两件事，别混：
//   指标模板   = 采**哪些指标**（集合，存在 insight_monitor_metric_templates）
//   采集策略模板 = 怎么采**（周期 / 保留 / 超时 / 重试 / 来源），本组表
// 采集策略模板里有一项 metric_template，用来指向指标模板，两级由此串起来。
//
// 关系：一个策略模板可以挂 N 个实例（一对多），一个实例任一时刻只挂一个模板。
// 实例不直接配策略，只选择「挂哪个模板」；挂了就实时继承模板的全部策略项。
// ============================================================================

// InsightMonitorScrapeStrategyTemplates 采集策略模板：一组采集策略项的集合。
//
// 语义：
//   - 按 db_type 隔离：MySQL 模板只能挂 MySQL 实例，跨类型挂载直接拒绝；
//   - 每个 db_type 有且仅有一个 is_default=true 的内置模板，作为"未挂载 / 挂载失效"的兜底，
//     保证任何实例都有策略可用，不会出现"没配就不采"的监控盲区；
//   - builtin 模板不可删除；is_default 模板不可删除、不可停用。
type InsightMonitorScrapeStrategyTemplates struct {
	*commonModels.Model
	Name        string `gorm:"type:varchar(64);not null;uniqueIndex:uk_strategy_tpl_name;comment:模板标识（小写字母数字中划线下划线）" json:"name"`
	DisplayName string `gorm:"type:varchar(64);not null;default:'';comment:模板名称" json:"display_name"`
	DBType      string `gorm:"type:varchar(32);not null;default:'';index:idx_strategy_tpl_db_type;comment:适用数据库类型（小写引擎键）" json:"db_type"`
	Description string `gorm:"type:varchar(255);not null;default:'';comment:模板说明" json:"description"`
	Builtin     bool   `gorm:"type:boolean;not null;default:false;comment:是否内置" json:"builtin"`
	IsDefault   bool   `gorm:"type:boolean;not null;default:false;comment:是否该类型的默认模板（未挂载实例的兜底）" json:"is_default"`
	Enabled     bool   `gorm:"type:boolean;not null;default:true;comment:是否启用（停用后已挂载实例回落默认模板）" json:"enabled"`
}

func (InsightMonitorScrapeStrategyTemplates) TableName() string {
	return "insight_monitor_scrape_strategy_templates"
}

// InsightMonitorScrapeStrategyItems 采集策略项：模板里的每一行具体策略。
//
// 为什么用 (item_type, item_key, value_json) 这种"通用行"而不是给每个策略项建一列：
// 新增一种策略（比如将来加"采集窗口""采样率""连接池上限"）不需要改表结构，
// 只要在服务层的类型注册表里加一条定义，前端表单、校验、解析全部自动跟随。
// 代价是值没有列级类型约束，所以校验一律放在服务层的 StrategyItemTypeSpec 上做。
type InsightMonitorScrapeStrategyItems struct {
	ID         uint64                 `gorm:"type:bigint unsigned;not null;primaryKey;autoIncrement" json:"id"`
	TemplateID uint64                 `gorm:"type:bigint unsigned;not null;uniqueIndex:uk_strategy_item,priority:1;comment:所属模板ID" json:"template_id"`
	ItemType   string                 `gorm:"type:varchar(32);not null;uniqueIndex:uk_strategy_item,priority:2;comment:策略项类型（见服务层类型注册表）" json:"item_type"`
	ItemKey    string                 `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_strategy_item,priority:3;comment:策略项键（同类型内区分多条，单条类型留空）" json:"item_key"`
	ValueJSON  datatypes.JSON         `gorm:"type:json;null;default:null;comment:策略项值（按 item_type 的 ValueKind 解析）" json:"value_json"`
	Enabled    bool                   `gorm:"type:boolean;not null;default:true;comment:是否启用（停用等同该项不存在，回落平台默认）" json:"enabled"`
	SortOrder  int                    `gorm:"type:int;not null;default:0;comment:展示顺序" json:"sort_order"`
	CreatedAt  commonModels.LocalTime `gorm:"comment:创建时间" json:"created_at"`
	UpdatedAt  commonModels.LocalTime `gorm:"comment:更新时间" json:"updated_at"`
}

func (InsightMonitorScrapeStrategyItems) TableName() string {
	return "insight_monitor_scrape_strategy_items"
}
