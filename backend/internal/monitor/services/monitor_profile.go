package services

// ============================================================================
// 实例画像（Overview Profile）—— 监控明细页「第二部分」的唯一数据来源
//
// 设计原则（与前端 Demo 逐字对齐，不允许前端自己拼字段或写死文案）：
//  1. 每个展示位都是后端算好的 ProfileValue：有值给 value/sub，没值给 available=false + reason。
//     前端硬规则：available=false 一律渲染 "—" 并把 reason 挂到 title，绝不显示 0 —— 这是"不静默缺数"的落点。
//  2. 健康分由后端按健康状态配置口径计算（100 − Σ(未恢复告警按 P 级扣分)，
//     采集降级/部分指标不可用会把分数同步下落到风险阈值；不可达直接 0），
//     并回传 deductions（扣分项）让前端能解释"为什么是这个分"，避免多页面各算一套。
//  3. 指标卡片的展示文案（含义 / 阈值 / 分组 / 顺序）同样由后端下发（MetricDisplayMeta），
//     前端只负责排版，新增指标不需要改前端。
// ============================================================================

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"
)

// ProfileValue 一个"可能取不到"的展示值。
// 前端渲染规则：available=false → 显示 "—" + reason（title 提示），不参与任何数值比较。
type ProfileValue struct {
	Value     string  `json:"value"`     // 已格式化好的展示值，如 "8.0.34" / "46 天 7 时"
	Sub       string  `json:"sub"`       // 副值，如内核版本、规格后缀
	Num       float64 `json:"num"`       // 原始数值，前端需要排序/比较时用
	Unit      string  `json:"unit"`      // 单位
	Available bool    `json:"available"` // 是否真的取到了
	Reason    string  `json:"reason"`    // 取不到的原因（前端 title 展示）
}

func pv(value string, sub string) ProfileValue {
	return ProfileValue{Value: value, Sub: sub, Available: true}
}

func pvNum(value float64, unit string) ProfileValue {
	return ProfileValue{Value: formatProfileNumber(value), Num: value, Unit: unit, Available: true}
}

// unavailableValue 取不到时的标准形态：可用态为 false，reason 说明"为什么没有"。
// 绝不返回空 available=true —— 那样前端会渲染成 0，是"假 0"错觉的源头。
func unavailableValue(reason string) ProfileValue {
	return ProfileValue{Available: false, Reason: reason}
}

func formatProfileNumber(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%.2f", value)
}

// ScoreDeduction 健康分扣分项：让"为什么是 72 分"可被解释，而不是一个黑盒数字。
type ScoreDeduction struct {
	Label string `json:"label"` // 如 "P2 告警 ×2"
	Score int    `json:"score"` // 扣分（正数）
}

// HealthScoreInfo 健康分。口径固定在后端，前端只渲染。
type HealthScoreInfo struct {
	Score      int              `json:"score"`
	Available  bool             `json:"available"`
	Reason     string           `json:"reason"`
	ClassLabel string           `json:"class_label"` // 健康 / 风险 / 故障 / 未采集
	ClassKey   string           `json:"class_key"`   // healthy / risk / fault / uncollected
	Deductions []ScoreDeduction `json:"deductions"`
	// DeductTotal 扣分合计（未截断的真实扣分）。触底（Capped）时它必然 > 100。
	// 单独下发是为了让界面能解释"为什么是 0 分"，而不是让用户看到一个无从追溯的 0。
	DeductTotal int `json:"deduct_total"`
	// Capped 是否触发 0 分下限兜底：真实扣分合计已超过满分，分数被截断到 0。
	// 前端据此展示「已降至最低 / 超扣 N 分」标记；false 时的 0 分是"刚好扣满"，不是触底。
	Capped bool `json:"capped"`
	// OverDeduct 超扣幅度：Capped 时 = DeductTotal − 100（> 0），未触底恒为 0。
	OverDeduct int `json:"over_deduct"`
	// ChangeText 与上一次采集相比的变化说明（如 "较昨日 75 ↓3"）。取不到时为空串。
	ChangeText string `json:"change_text"`
}

// AlertBrief 核心告警摘要：给概览「健康信息带」用的极简告警项。
type AlertBrief struct {
	Level       string `json:"level"`        // P1 / P2 / P3
	MetricLabel string `json:"metric_label"` // 指标名
	Message     string `json:"message"`      // 简要描述
	Duration    string `json:"duration"`     // 持续时长，如 "2h14m"
	TriggeredAt string `json:"triggered_at"`
}

// InstanceProfile 实例概览：与 Demo 第二部分逐字段对应。
type InstanceProfile struct {
	// ---- 第 1 行：基本信息（我是谁、在哪、归属哪）----
	Name            string       `json:"name"`
	Target          string       `json:"target"`
	InstanceID      string       `json:"instance_id"`
	DbType          string       `json:"db_type"`
	DbVersion       ProfileValue `json:"db_version"`
	OsName          ProfileValue `json:"os_name"`
	EnvironmentName string       `json:"environment_name"`
	GroupName       string       `json:"group_name"`
	DeployProvider  ProfileValue `json:"deploy_provider"`

	// ---- 第 2 行：架构（这个库怎么摆的、当前站在哪）----
	DeployMode   ProfileValue `json:"deploy_mode"`
	Role         ProfileValue `json:"role"`
	ReplicaCount ProfileValue `json:"replica_count"`
	HaMode       ProfileValue `json:"ha_mode"`

	// ---- 第 3 行：规格与运行（规格是所有阈值的分母）----
	CpuCores    ProfileValue `json:"cpu_cores"`
	MemoryGB    ProfileValue `json:"memory_gb"`
	DiskGB      ProfileValue `json:"disk_gb"`
	DiskUsedPct ProfileValue `json:"disk_used_pct"`
	Uptime      ProfileValue `json:"uptime"`

	// ---- 健康信息带（常显）----
	HealthScore   HealthScoreInfo `json:"health_score"`
	Status        string          `json:"status"`
	StatusMessage string          `json:"status_message"`
	Reachable     ProfileValue    `json:"reachable"`
	ProbeLatency  ProfileValue    `json:"probe_latency"`
	ProbeAt       string          `json:"probe_at"`
	// Quality / QualityMessage 数据质量：部分可用时给出受限指标数量提示
	Quality        string `json:"quality"`
	QualityMessage string `json:"quality_message"`
	ScreenClass    string `json:"screen_class"`

	// ---- 核心告警（数量 + 最高级别 + 简要列表）----
	AlertCount    int          `json:"alert_count"`
	AlertTopLevel string       `json:"alert_top_level"`
	AlertBriefs   []AlertBrief `json:"alert_briefs"`
}

// ============================================================================
// 指标展示元数据：卡片上的「含义与关注点 / 参考阈值 / 分组」全部由后端下发
// ============================================================================

// MetricDisplayMeta 指标卡片的展示元数据。
// 前端不得自行维护指标中文名与阈值 —— 一旦前端写死，新增指标就必须改前端，
// 且不同页面会出现"同一指标两套文案"。
type MetricDisplayMeta struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Unit      string `json:"unit"`
	Threshold string `json:"threshold"` // 参考阈值
	Group     string `json:"group"`     // 分组名（同一 Tab 内再分组）
	Layer     string `json:"layer"`     // host / engine / kernel
	Order     int    `json:"order"`     // 分组内展示顺序
}

// 三个 Tab 的分层键（LayerHost / LayerEngine / LayerKernel 定义在 monitor_metrics.go），
// 与 Demo 的三个 Tab 一一对应，此处不再重复声明以免两套常量漂移。

// 无采集源的指标键：这些位置"应该显示，但当前没有数据源"。
// 定义成常量是为了让"不可得"也是显式声明的，而不是前端悄悄少画几张卡。
const (
	MetricResProcessState = "res.process_state"      // 进程状态：需 process-exporter
	MetricOpsLastBackup   = "ops.last_backup"        // 最近成功备份时间：需对接备份模块
	MetricKernRecoveryEst = "kern.recovery_estimate" // 崩溃恢复时长预估：由 checkpoint age 派生
)

// metricDisplayCatalog 50 个指标的展示元数据。
// 顺序即卡片顺序：主机 15 / 引擎 20 / 内核 15，与需求文档编号一致。
var metricDisplayCatalog = []MetricDisplayMeta{
	// ---------- 主机层 15 项（Tab：基础指标）----------
	{MetricResCPU, "CPU 使用率", "%", "持续 >80% 告警", "CPU 与负载", LayerHost, 1},
	{MetricResCPULoad1, "CPU 负载 load1/5/15", "", "> 核数告警", "CPU 与负载", LayerHost, 2},
	{MetricResCtxSwitches, "上下文切换", "次/s", "基线对比", "CPU 与负载", LayerHost, 3},
	{MetricResMemory, "内存使用率", "%", ">90% 告警", "内存", LayerHost, 4},
	{MetricResSwapUsedPct, "Swap 使用量", "%", ">0 即关注", "内存", LayerHost, 5},
	{MetricResDisk, "磁盘空间使用率", "%", ">80% 告警", "磁盘", LayerHost, 6},
	{MetricResDiskIOPS, "磁盘 IOPS", "iops", "接近规格上限", "磁盘", LayerHost, 7},
	{MetricResDiskThroughput, "磁盘吞吐", "MB/s", "基线对比", "磁盘", LayerHost, 8},
	{MetricResDiskAwaitMs, "磁盘延迟 await", "ms", "await >20ms 告警", "磁盘", LayerHost, 9},
	{MetricResNetworkIn, "数据库网络流量", "MB/s", "带宽 70%", "网络", LayerHost, 10},
	{MetricResNetErrors, "网络错误 / 丢包", "个", ">0 关注", "网络", LayerHost, 11},
	{MetricResTCPEstablished, "TCP 连接数", "个", "基线对比", "网络", LayerHost, 12},
	{MetricResFDUsedPct, "文件句柄使用率", "%", ">80% 告警", "进程与时钟", LayerHost, 13},
	{MetricResProcessState, "进程状态", "", "D 状态告警", "进程与时钟", LayerHost, 14},
	{MetricResTimeOffsetS, "时钟偏移", "ms", ">500ms 告警", "进程与时钟", LayerHost, 15},

	// ---------- 引擎层 20 项（Tab：数据库指标）----------
	{MetricPerfConnCount, "当前连接数", "个", "基线对比", "连接与会话", LayerEngine, 16},
	{MetricCapConnUsedPct, "连接使用率", "%", ">85% 告警", "连接与会话", LayerEngine, 17},
	{MetricPerfActiveConn, "活跃会话数", "个", "与核数对比", "连接与会话", LayerEngine, 18},
	{MetricEngAbortedConns, "连接错误数", "个/min", "增速告警", "连接与会话", LayerEngine, 19},
	{MetricPerfQPS, "QPS", "req/s", "趋势与基线", "吞吐与查询", LayerEngine, 20},
	{MetricPerfTPS, "TPS", "txn/s", "趋势与基线", "吞吐与查询", LayerEngine, 21},
	{MetricEngSlowQueries, "慢查询数", "个/min", ">0/min 关注", "吞吐与查询", LayerEngine, 22},
	{MetricPerfCacheHitRatio, "缓存命中率", "%", "<95% 关注", "吞吐与查询", LayerEngine, 23},
	{MetricCapTmpDiskPct, "临时表 / 临时文件", "%", "增速告警", "吞吐与查询", LayerEngine, 24},
	{MetricPerfLocks, "锁等待数", "个", ">0 关注", "锁与阻塞", LayerEngine, 25},
	{MetricEngDeadlocks, "死锁次数", "次", ">0 告警", "锁与阻塞", LayerEngine, 26},
	{MetricPerfLongTrx, "长事务", "个", ">60s 告警", "锁与阻塞", LayerEngine, 27},
	{MetricReplLagSeconds, "主从复制延迟", "s", ">10s 告警", "复制与日志", LayerEngine, 28},
	{MetricReplIORunning, "复制线程状态", "", "非 Yes 告警", "复制与日志", LayerEngine, 29},
	{MetricEngBinlogWALRate, "Binlog/WAL 产生速率", "GB/h", "趋势对比", "复制与日志", LayerEngine, 30},
	{MetricEngReplSlotLag, "复制槽堆积", "bytes", ">1GB 告警", "复制与日志", LayerEngine, 31},
	{MetricEngSemiSyncStatus, "半同步 ACK 状态", "", "退化告警", "复制与日志", LayerEngine, 32},
	{MetricPerfReadBytes, "网络协议流量", "MB/s", "趋势对比", "复制与日志", LayerEngine, 33},
	{MetricOpsLastBackup, "最近成功备份时间", "", ">24h 告警", "容量与运维", LayerEngine, 34},
	{MetricEngLargestTable, "大表 / 大索引 TOP", "GB", "趋势对比", "容量与运维", LayerEngine, 35},

	// ---------- 内核层 15 项（Tab：引擎指标）----------
	{MetricKernRedoWriteRate, "Redo/WAL 写入量", "MB/s", "基线对比", "InnoDB · 写入与刷脏", LayerKernel, 36},
	{MetricKernWALSwitchRate, "日志切换频率", "次/h", ">6 次/h 关注", "InnoDB · 写入与刷脏", LayerKernel, 37},
	{MetricKernBufferDirtyPct, "脏页比例", "%", ">60% 关注", "InnoDB · 写入与刷脏", LayerKernel, 38},
	{MetricKernFlushRate, "刷脏速率", "页/s", "与 IO 关联看", "InnoDB · 写入与刷脏", LayerKernel, 39},
	{MetricKernDBLWRWriteRate, "Doublewrite / 全页写", "MB/s", "基线对比", "InnoDB · 写入与刷脏", LayerKernel, 40},
	{MetricKernCheckpointAge, "Checkpoint age", "%", ">75% 告警", "InnoDB · Checkpoint 与恢复", LayerKernel, 41},
	{MetricKernRecoveryEst, "崩溃恢复时长预估", "秒", "与 Checkpoint age 联动", "InnoDB · Checkpoint 与恢复", LayerKernel, 42},
	{MetricKernHistoryList, "历史链长度", "条", ">100 万告警", "InnoDB · Undo 与历史链", LayerKernel, 43},
	{MetricKernRollbackRatio, "回滚率", "%", ">5% 关注", "通用 · 事务质量", LayerKernel, 44},
	{MetricKernLWLockWaits, "闩锁 / 内存门闩等待", "次/s", "增速告警", "InnoDB · 并发与内存争抢", LayerKernel, 45},
	{MetricKernReadAheadEvict, "缓冲池空闲页 / 预读", "%", "基线对比", "InnoDB · 并发与内存争抢", LayerKernel, 46},
	{MetricKernPageSplitRate, "索引页分裂 / 合并", "次/s", "基线对比", "InnoDB · 并发与内存争抢", LayerKernel, 47},
	{MetricKernTableCacheHit, "表缓存命中率", "%", "命中率 >99%", "InnoDB · 并发与内存争抢", LayerKernel, 48},
	{MetricKernDeadTuples, "死元组数 / 表年龄", "条", "age >2^31-1亿 告警", "PostgreSQL 专有", LayerKernel, 49},
	{MetricKernAutovacuumWorkers, "VACUUM / AUTOVACUUM 状态", "个", "积压告警", "PostgreSQL 专有", LayerKernel, 50},
}

var metricDisplayByKey = func() map[string]MetricDisplayMeta {
	result := make(map[string]MetricDisplayMeta, len(metricDisplayCatalog))
	for _, item := range metricDisplayCatalog {
		result[item.Key] = item
	}
	return result
}()

// metricDisplayMetaOf 取指标展示元数据。未登记的指标返回 nil，
// 由调用方决定是跳过还是回落到字典 display_name（这里选择回落，保证不丢卡片）。
func metricDisplayMetaOf(metricKey string) (MetricDisplayMeta, bool) {
	item, ok := metricDisplayByKey[metricKey]
	return item, ok
}

// MetricDisplayCatalogView 供前端一次性拉取全部展示元数据（可选接口）。
func MetricDisplayCatalogView() []MetricDisplayMeta {
	return metricDisplayCatalog
}

// ============================================================================
// 实例画像构建
// ============================================================================

// instanceFacts 从 insight_instances 读到的原始画像列。
// 单独读一次是为了把"库里没有"和"查库失败"区分开：前者是字段未接入，后者是系统故障。
type instanceFacts struct {
	DbVersion        string
	OsName           string
	OsKernel         string
	DeployProvider   string
	CloudVendor      string
	InstanceRole     string
	HaMode           string
	CpuCores         int
	MemoryGB         float64
	DiskGB           float64
	DiskType         string
	UptimeSeconds    int64
	ProbeLatencyMs   int
	LastCheckStatus  string
	LastCheckMessage string
	LastCheckedAt    *time.Time
}

func loadInstanceFacts(instanceID string) (instanceFacts, error) {
	facts := instanceFacts{}
	if global.App.DB == nil || instanceID == "" {
		return facts, fmt.Errorf("数据库未初始化")
	}
	type row struct {
		DbVersion        string
		OsName           string
		OsKernel         string
		DeployProvider   string
		CloudVendor      string
		InstanceRole     string
		HaMode           string
		CpuCores         int
		MemoryGB         float64
		DiskGB           float64
		DiskType         string
		UptimeSeconds    int64
		ProbeLatencyMs   int
		LastCheckStatus  string
		LastCheckMessage string
		LastCheckedAt    *time.Time
	}
	var dest row
	err := global.App.DB.Table("insight_instances").
		Select("db_version, os_name, os_kernel, deploy_provider, cloud_vendor, instance_role, ha_mode,"+
			" cpu_cores, memory_gb, disk_gb, disk_type, uptime_seconds, probe_latency_ms,"+
			" last_check_status, last_check_message, last_checked_at").
		Where("instance_id = ?", instanceID).Scan(&dest).Error
	if err != nil {
		return facts, err
	}
	facts = instanceFacts(dest)
	return facts, nil
}

// buildInstanceProfile 组装实例概览。
// card 提供实时采集到的部分（状态、质量、屏幕分类），alerts 提供告警，facts 提供库里的静态画像。
func buildInstanceProfile(row monitorInstanceRow, card InstanceMonitorCard, alerts []AlertRecord) InstanceProfile {
	facts, factsErr := loadInstanceFacts(row.InstanceID)
	profile := InstanceProfile{
		Name:            firstNonEmpty(card.Remark, card.Hostname),
		Target:          card.Target,
		InstanceID:      card.InstanceID,
		DbType:          card.DbType,
		EnvironmentName: card.EnvironmentName,
		GroupName:       firstNonEmpty(card.GroupName, "未分组"),
		Status:          card.Status,
		StatusMessage:   card.StatusMessage,
		Quality:         card.Quality,
		QualityMessage:  card.QualityMessage,
		ScreenClass:     card.ScreenClass,
		AlertCount:      len(alerts),
		// 容量按实际告警数预留：概览要完整展示所有待处理告警，不再截断
		AlertBriefs: make([]AlertBrief, 0, len(alerts)),
	}

	// ---- 数据库版本 ----
	if facts.DbVersion != "" {
		profile.DbVersion = pv(facts.DbVersion, "")
	} else {
		profile.DbVersion = unavailableValue("未采集到版本：需引擎侧执行 SELECT VERSION()（PG 为 version()）并回写实例画像")
	}

	// ---- 操作系统 ----
	if facts.OsName != "" {
		profile.OsName = pv(facts.OsName, facts.OsKernel)
	} else {
		profile.OsName = unavailableValue("未采集到操作系统：主机层需查询 node_uname_info（未接 Prometheus 的实例不可得）")
	}

	// ---- 部署形态：自建 / 云 ----
	switch facts.DeployProvider {
	case "self_built":
		profile.DeployProvider = pv("自建", firstNonEmpty(facts.CloudVendor, "物理机"))
	case "cloud":
		profile.DeployProvider = pv("云", firstNonEmpty(facts.CloudVendor, "云实例"))
	default:
		profile.DeployProvider = unavailableValue("未录入部署形态：需在实例表单或云导入时写入 deploy_provider（自建 / 云 + 厂商）")
	}

	// ---- 架构模式 ----
	profile.DeployMode = pv(deployModeLabel(row.DeployMode), row.DeployMode)

	// ---- 当前角色 ----
	if facts.InstanceRole != "" {
		profile.Role = pv(instanceRoleLabel(facts.InstanceRole), facts.InstanceRole)
	} else {
		profile.Role = unavailableValue("未识别角色：需按 @@read_only + Slave_* 状态（PG 用 pg_is_in_recovery()）探测并回写")
	}

	// ---- 复制拓扑：从库数量与延迟 ----
	profile.ReplicaCount = replicaCountValue(card)
	// ---- 高可用模式 ----
	if facts.HaMode != "" {
		profile.HaMode = pv(facts.HaMode, "")
	} else {
		profile.HaMode = unavailableValue("未录入高可用模式：MGR / MHA / 云高可用版属产品事实，无法从数据库侧探测，需运维录入")
	}

	// ---- 规格 ----
	if facts.CpuCores > 0 {
		profile.CpuCores = pvNum(float64(facts.CpuCores), "核")
	} else {
		profile.CpuCores = unavailableValue("未采集 CPU 核数：需查询 count(node_cpu_seconds_total{mode=\"idle\"})，它是负载与活跃会话对比的分母")
	}
	if facts.MemoryGB > 0 {
		profile.MemoryGB = pvNum(facts.MemoryGB, "GB")
	} else {
		profile.MemoryGB = unavailableValue("未采集内存容量：需查询 node_memory_MemTotal_bytes")
	}
	if facts.DiskGB > 0 {
		profile.DiskGB = pvNum(facts.DiskGB, "GB")
		if facts.DiskType != "" {
			profile.DiskGB.Sub = facts.DiskType
		}
	} else {
		profile.DiskGB = unavailableValue("未采集磁盘容量：需查询 node_filesystem_size_bytes（排除 tmpfs / overlay）")
	}

	// ---- 磁盘已用：直接取实时指标 ----
	profile.DiskUsedPct = metricToProfileValue(card.ResourceMetrics.Disk, "%")

	// ---- 运行时长 ----
	if facts.UptimeSeconds > 0 {
		profile.Uptime = pv(formatUptime(time.Duration(facts.UptimeSeconds)*time.Second), "")
	} else {
		profile.Uptime = unavailableValue("未采集运行时长：需引擎侧 Uptime / pg_postmaster_start_time() 并回写实例画像")
	}

	// ---- 可达性：与「采集成功」是两件事，必须分开显示 ----
	if factsErr != nil {
		profile.Reachable = unavailableValue("实例画像读取失败：" + factsErr.Error())
	} else {
		switch facts.LastCheckStatus {
		case "success", "ok":
			profile.Reachable = pv("可达", "")
		case "failed":
			profile.Reachable = pv("不可达", firstNonEmpty(facts.LastCheckMessage, "连通性探测失败"))
		default:
			profile.Reachable = unavailableValue("尚未做连通性探测（last_check_status=" + defaultString(facts.LastCheckStatus, "unknown") + "）")
		}
	}
	if facts.ProbeLatencyMs > 0 {
		profile.ProbeLatency = pvNum(float64(facts.ProbeLatencyMs), "ms")
	} else {
		profile.ProbeLatency = unavailableValue("未记录探测耗时")
	}
	if facts.LastCheckedAt != nil {
		profile.ProbeAt = facts.LastCheckedAt.Format(time.DateTime)
	}

	// ---- 健康分 ----
	profile.HealthScore = buildHealthScore(card, alerts, facts)

	// ---- 核心告警：全量下发，由前端列表内部滚动承载，后端不做截断 ----
	// 之前这里只取前 3 条（"核心告警"语义），但明细页要在概览里完整列全部待处理告警，
	// 截断会让运维误以为只有 3 条。数量收敛交给前端的 max-height + 区域滚动。
	profile.AlertTopLevel = topAlertLevel(alerts)
	for _, alert := range alerts {
		metricName := firstNonEmpty(alert.Name, metricDisplayNameOf(alert.MetricKey))
		profile.AlertBriefs = append(profile.AlertBriefs, AlertBrief{
			Level:       alertLevelLabel(alert),
			MetricLabel: metricName,
			Message:     alert.Message,
			Duration:    formatDurationSeconds(int(alert.DurationSec)),
			TriggeredAt: alert.TriggeredAt,
		})
	}
	return profile
}

// healthScoreFull 健康分满分。扣分合计超过它即为"触底"，分数按 0 展示。
const healthScoreFull = 100

// applyHealthScoreFloor 应用 0 分下限，并回填扣分合计 / 触底标记 / 超扣幅度。
//
// 关键点：扣分明细与扣分合计保留真实值，只有展示用的 Score 被截断 ——
// "为什么是 0 分"全靠这两样解释，跟着一起截断的话，界面上就只剩一个无从追溯的 0，
// 用户无法判断是"刚好扣满"还是"远超满分"。
func applyHealthScoreFloor(info *HealthScoreInfo) {
	for _, item := range info.Deductions {
		info.DeductTotal += item.Score
	}
	score := healthScoreFull - info.DeductTotal
	if score < 0 {
		info.Capped = true
		info.OverDeduct = -score
		score = 0
	}
	info.Score = score
}

// buildHealthScore 健康分口径固定在后端（与 evaluateInstanceState 同一份配置与公式，见
// monitor_health_state.go）：100 − Σ(未恢复告警按 P 级扣分)，下限 0；采集失败/不可达直接 0；
// 未采集不给分。状态分类（class_key）沿用 screen_class —— 它本就是健康分按阈值区间推导的
// 结果（evaluateInstanceState），分数与分类天然一致，前端不许再按分数另行猜状态。
func buildHealthScore(card InstanceMonitorCard, alerts []AlertRecord, facts instanceFacts) HealthScoreInfo {
	info := HealthScoreInfo{Available: true, Deductions: make([]ScoreDeduction, 0, 4)}
	// 不可达 = 最严重：不管指标多好看，连不上就是 0 分。
	if facts.LastCheckStatus == "failed" {
		info.ClassKey = "fault"
		info.ClassLabel = "故障"
		info.Deductions = append(info.Deductions, ScoreDeduction{Label: "实例不可达", Score: healthScoreFull})
		applyHealthScoreFloor(&info)
		return info
	}
	if card.ScreenClass == "uncollected" || card.CollectStatus == "never" {
		return HealthScoreInfo{
			Available:  false,
			Reason:     "尚未采集到数据，无法计算健康分",
			ClassKey:   "uncollected",
			ClassLabel: "未采集",
			Deductions: []ScoreDeduction{},
		}
	}

	cfg := GetHealthStateConfig()
	counts := map[string]int{}
	for _, alert := range alerts {
		counts[healthStateSeverityOf(alert)]++
	}
	for _, level := range healthStateLevels {
		count := counts[level]
		if count == 0 {
			continue
		}
		deduct := count * deductionOfLevel(cfg, level)
		info.Deductions = append(info.Deductions, ScoreDeduction{
			Label: fmt.Sprintf("%s 告警 ×%d", level, count), Score: deduct,
		})
	}
	// 故障但没有对应告警扣分（采集失败/不可达直判）：状态与分数必须自洽，给 0 分
	if card.ScreenClass == "fault" && len(info.Deductions) == 0 {
		info.Deductions = append(info.Deductions, ScoreDeduction{Label: "采集失败或实例不可达", Score: healthScoreFull})
	}
	applyHealthScoreFloor(&info)
	info.ClassKey = card.ScreenClass
	info.ClassLabel = screenClassLabelOf(card.ScreenClass)
	return info
}

func metricToProfileValue(value MetricValue, unit string) ProfileValue {
	if !value.Available {
		return unavailableValue(firstNonEmpty(value.Reason, "本轮未采集到该指标"))
	}
	item := ProfileValue{Available: true, Num: value.Value, Unit: firstNonEmpty(value.Unit, unit)}
	item.Value = formatProfileNumber(value.Value)
	if item.Unit != "" {
		item.Value += item.Unit
	}
	return item
}

// replicaCountValue 复制拓扑：从库数量 + 延迟。
// MySQL 走 repl.lag_seconds / PG 走 pg_stat_replication，取不到时明确说明。
func replicaCountValue(card InstanceMonitorCard) ProfileValue {
	lag := card.Metrics
	if len(lag) == 0 {
		return unavailableValue("未采集到复制拓扑信息")
	}
	for _, item := range lag {
		if item.Key == MetricReplLagSeconds && item.Available {
			return ProfileValue{
				Value:     "1 个从库",
				Sub:       "延迟 " + formatProfileNumber(item.Value) + "s",
				Num:       item.Value,
				Available: true,
			}
		}
	}
	return unavailableValue("当前实例未开启复制，或未采集到复制延迟指标")
}

func deployModeLabel(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "master_slave":
		return "主从"
	case "cluster":
		return "集群"
	case "standalone":
		return "单机"
	default:
		return firstNonEmpty(strings.TrimSpace(mode), "未配置")
	}
}

func instanceRoleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "master":
		return "主库"
	case "slave":
		return "从库"
	case "standalone":
		return "单机"
	default:
		return strings.TrimSpace(role)
	}
}

func formatUptime(duration time.Duration) string {
	if duration <= 0 {
		return "—"
	}
	days := int(duration.Hours()) / 24
	hours := int(duration.Hours()) % 24
	minutes := int(duration.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, minutes)
	default:
		return fmt.Sprintf("%d 分钟", minutes)
	}
}

func formatDurationSeconds(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	duration := time.Duration(seconds) * time.Second
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func topAlertLevel(alerts []AlertRecord) string {
	top := ""
	for _, alert := range alerts {
		level := alertLevelLabel(alert)
		if level == "P1" {
			return "P1"
		}
		if level == "P2" {
			top = "P2"
			continue
		}
		if top == "" {
			top = level
		}
	}
	return top
}

// alertLevelLabel 告警级别：优先用字典里的 severity，缺省回落到 level 再回落 P3。
func alertLevelLabel(alert AlertRecord) string {
	level := strings.ToUpper(strings.TrimSpace(alert.Severity))
	if level == "" {
		level = strings.ToUpper(strings.TrimSpace(alert.Level))
	}
	switch level {
	case "P0", "P1", "P2", "P3":
		return level
	case "CRITICAL", "FATAL":
		return "P1"
	case "WARNING", "WARN", "MAJOR":
		return "P2"
	default:
		return "P3"
	}
}

func qualityLabelOf(quality string) string {
	switch quality {
	case QualityOK:
		return "正常"
	case "partial":
		return "部分可用"
	case QualityStale:
		return "数据过期"
	case QualityUnsupported:
		return "不适用"
	case QualityNoData:
		return "无数据"
	case QualityError:
		return "采集失败"
	case QualityDisabled:
		return "未启用"
	default:
		return quality
	}
}

// metricDisplayNameOf 告警里的 metric_key → 中文名，取不到就还用键本身。
func metricDisplayNameOf(metricKey string) string {
	name, _, _, _ := metricDisplayName(metricKey)
	return name
}

// ============================================================================
// 补齐"应展示但没采到"的指标
// ============================================================================

// appendMissingMetricViews 把本轮没采到的指标补成"明确不可用"的卡片。
//
// 为什么必须补：指标一旦因为"没采到"就不出现在列表里，
// 运维看到的是"这个实例没有该指标"，而不是"该指标不可得" —— 这就是静默缺数。
// 补齐后每张卡片都带 quality + reason，前端渲染成 "—" 并给出原因。
//
// 补齐范围必须与采集门控（resolveInstanceMetricKeys）同口径：
//   - 实例有生效模板（含回落默认模板）→ 只补模板内的有效键，
//     模板裁剪掉的指标不再以"未采集"卡片出现，展示=绑定模板；
//   - 连默认模板都没有（模板表未播种）→ 维持全目录补齐，对应"全量采集"兜底。
func appendMissingMetricViews(views []MetricSampleView, row monitorInstanceRow) []MetricSampleView {
	present := make(map[string]bool, len(views))
	for _, item := range views {
		present[item.Key] = true
	}
	engineKey := resolveEngine(row.DbType)

	var keys []string
	if tmpl, _ := resolveTemplateForInstance(row.DbType, row.MetricsTemplate); tmpl != nil && len(tmpl.Keys) > 0 {
		// 有模板：有效键已含「模板 ∩ 字典启用 ∩ 引擎适用」三层过滤
		keys, _, _ = resolveInstanceMetricKeys(row)
	} else {
		// 无模板：按展示目录全量补齐（原兜底行为）
		keys = make([]string, 0, len(metricDisplayCatalog))
		for _, meta := range metricDisplayCatalog {
			keys = append(keys, meta.Key)
		}
		// 这条兜底路径绕开了 resolveInstanceMetricKeys，必须单独再过一次角色门控：
		// 本机不承担复制角色的实例不该把「备库延迟」补成"无数据"卡片 ——
		// 那会让人以为拓扑在的复制挂了，而它压根不该出现。
		keys = filterRoleInapplicableKeys(engineKey, row.InstanceRole, keys)
	}

	for _, key := range keys {
		if present[key] {
			continue
		}
		// 不在字典里的 key（如运维补充项）直接补；在字典里的要判断引擎适用性。
		if descriptor, exists := metricDescriptorOf(key); exists && !engineInScope(engineKey, descriptor.EngineScope) {
			continue
		}
		present[key] = true
		displayName, category, unit, hint := metricDisplayName(key)
		view := MetricSampleView{
			Key:           key,
			DisplayName:   displayName,
			Category:      category,
			CategoryLabel: categoryLabel(category),
			Layer:         metricLayer(key),
			LayerLabel:    layerLabel(metricLayer(key)),
			Unit:          unit,
			Quality:       QualityNoData,
			Reason:        uncollectedMetricReason(key),
			DisplayHint:   hint,
			Advice:        metricAdvice(key),
			Available:     false,
		}
		applyDisplayMeta(&view)
		views = append(views, view)
	}
	return views
}

// uncollectedMetricReason 未采集原因：写清楚"缺什么"，而不是笼统的"无数据"。
func uncollectedMetricReason(key string) string {
	switch key {
	case MetricOpsLastBackup:
		return "最近成功备份时间来自备份模块，尚未接入监控采集"
	case MetricKernRecoveryEst:
		return "崩溃恢复时长由 checkpoint age 推导，需先采集到 checkpoint age"
	default:
		return "本轮未采集到该指标（可能未启用、不在模板内或数据源不可用）"
	}
}
