package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ============================================================================
// 监控表结构迁移
//
// 注意：monitor 的模型此前**不在** bootstrap 的 AutoMigrate 列表里，表是外部创建的。
// 所以这里单独提供一个幂等迁移入口，由 bootstrap 调用。
// 时序点表用分区 DDL 创建（AutoMigrate 不会分区），其余表走 AutoMigrate。
// ============================================================================

var monitorSchemaOnce bool

// EnsureMonitorSchema 幂等建表/加列。重复调用无副作用。
//
// 显式接收 *gorm.DB：这样既能被 bootstrap 的表初始化流程同步调用（此时
// global.App.DB 尚未赋值），也能被监控自身的启动引导调用。
func EnsureMonitorSchema(db *gorm.DB) {
	if db == nil || monitorSchemaOnce {
		return
	}

	// 普通表：AutoMigrate 会补上缺失的列与索引（含本次新增的生命周期字段、采集运行态字段）
	if err := db.Set("gorm:table_options", "ENGINE=InnoDB CHARSET=utf8mb4").AutoMigrate(
		&monitorModels.InsightMonitorTargetConfigs{},
		&monitorModels.InsightMonitorAlertRules{},
		&monitorModels.InsightMonitorAlertEvents{},
		&monitorModels.InsightMonitorMetricSnapshots{},
		&monitorModels.InsightMonitorMetricLatest{},
		&monitorModels.InsightMonitorMetricTemplates{},
		&monitorModels.InsightMonitorTemplateMetrics{},
		// 采集策略：模板 - 实例两级配置（实例表同步加 strategy_template_id / strategy_overrides 两列）
		&monitorModels.InsightMonitorScrapeStrategyTemplates{},
		&monitorModels.InsightMonitorScrapeStrategyItems{},
		// 告警策略：模板 - 实例两级配置（规则表加 template_id 列、实例表加 alert_template_id 列）
		&monitorModels.InsightMonitorAlertTemplates{},
		// 告警墓碑：默认模板里被用户显式删除的指标，补档时跳过（删除可持久化）
		&monitorModels.InsightMonitorAlertRuleTombstone{},
		// 采集频率：平台级单行设置，三处周期的唯一数据源
		&monitorModels.InsightMonitorScrapeSettings{},
		// 健康分级：等级/结果项/判定规则的唯一数据源（单行 JSON）
		&monitorModels.InsightMonitorHealthGradeSettings{},
		// 告警通知：渠道实例 / 通知策略 / 投递任务（投递记录与队列合一）
		&monitorModels.InsightMonitorNotifyChannels{},
		&monitorModels.InsightMonitorNotifyPolicies{},
		&monitorModels.InsightMonitorNotifyTasks{},
		// 告警升级链：未认领超时自动升级的状态机（聚合窗口/升级等待/升级目标/认领停止）
		&monitorModels.InsightMonitorAlertEscalations{},
		// 负载快照：头表 + payload JSON（AWR 式两点差分报告）
		&monitorModels.InsightMonitorLoadSnapshots{},
	); err != nil {
		global.App.Log.Errorf("migrate monitor tables failed: %v", err)
		return
	}
	// 建表后播种：库里没有设置行时用 config.yaml 的值（或默认值）建一行。
	// config.yaml 只在这里读一次，之后一律以数据行为准，避免"文件一个值、库里一个值"。
	EnsureScrapeSettings(db)
	// 健康分级播种：首次写入内置默认配置；之后未自定义的行会跟随内置版本升级。
	EnsureHealthGradeSettings(db)
	// 采集策略模板播种：每个已实现引擎一个内置默认模板 + 全套默认策略项。
	// 必须早于任何实例策略解析 —— 否则未挂载实例找不到兜底模板，会退化成"无策略"。
	if err := EnsureScrapeStrategyTemplates(db); err != nil {
		global.App.Log.Warnf("seed scrape strategy templates failed: %v", err)
	}
	// 告警策略模板播种：每个引擎一个内置默认模板 + 历史全局规则归位（rule_id 不变）。
	// 必须早于任何告警评估 —— 否则未挂载实例解析不到兜底模板。
	if err := EnsureAlertTemplates(db); err != nil {
		global.App.Log.Warnf("seed alert templates failed: %v", err)
	}

	// 时序点表：分区表，必须用原生 DDL
	if err := ensureMetricPointsTable(db); err != nil {
		global.App.Log.Warnf("create monitor metric points table failed: %v", err)
	}
	// 分区表在 AutoMigrate 之外，索引也要单独补
	ensureMetricPointsIndexes(db)

	// 告警等级统一为 P 码（P0–P3）：中文旧值迁移 + 事件表同步。
	// 幂等：只在旧值上产生写。P0 是新增等级，绝不由迁移自动产生（避免一夜之间电话轰炸）。
	for _, stmt := range []string{
		"UPDATE insight_monitor_alert_rules SET alert_level='P1' WHERE alert_level IN ('高','严重')",
		"UPDATE insight_monitor_alert_rules SET alert_level='P2' WHERE alert_level IN ('中','一般')",
		"UPDATE insight_monitor_alert_rules SET alert_level='P3' WHERE alert_level IN ('低','提示')",
		"UPDATE insight_monitor_alert_events SET level='P1' WHERE level IN ('高','严重')",
		"UPDATE insight_monitor_alert_events SET level='P2' WHERE level IN ('中','一般')",
		"UPDATE insight_monitor_alert_events SET level='P3' WHERE level IN ('低','提示')",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("migrate monitor alert level failed: %v", err)
		}
	}

	// 告警事件指纹换口径：(rule_id|实例|指标) → (实例|指标)。
	// 阶梯告警下同指标多档共存，指纹带 rule_id 会让"档位迁移"变成新建事件。
	// 只重写旧格式（含两个分隔符）与空值，已是新格式的行不动 —— 幂等且不产生无谓写。
	if err := db.Exec(
		"UPDATE insight_monitor_alert_events " +
			"SET fingerprint = CONCAT(instance_id, '|', metric_key) " +
			"WHERE fingerprint = '' OR fingerprint IS NULL OR fingerprint LIKE '%|%|%'").Error; err != nil {
		global.App.Log.Warnf("backfill monitor alert fingerprint failed: %v", err)
	}
	// 存量事件补对外告警 ID（alm-xxxxxxxxxxxx）。必须在 AutoMigrate 加列之后执行；
	// 幂等：只处理空值行，已生成过的 ID 永不重写（ID 的价值就是"稳定可引用"）。
	backfillAlertEventIDs(db)
	if err := db.Exec(
		"UPDATE insight_monitor_alert_events SET severity = CASE " +
			"WHEN level IN ('P0','紧急') THEN 'P0' WHEN level IN ('P1','高') THEN 'P1' " +
			"WHEN level IN ('P3','低') THEN 'P3' ELSE 'P2' END " +
			"WHERE severity = '' OR severity IS NULL").Error; err != nil {
		global.App.Log.Warnf("backfill monitor alert severity failed: %v", err)
	}
	// 对账：规则表与事件表的 severity 都必须与各自等级列严格一致（P 码口径）。
	// severity 是派生物、不是独立信息：只刷"空值"不够 —— 等级迁移把 level 从旧值翻成 P 码后，
	// 存量行的 severity 会留下与 level 不一致的旧值（如 level=P1 / severity=P2），
	// 页面按 severity 上色就会把"严重"画成"一般"。这里按 level 反向刷平。
	if err := db.Exec(
		"UPDATE insight_monitor_alert_rules SET severity = alert_level " +
			"WHERE alert_level IN ('P0','P1','P2','P3') AND severity <> alert_level").Error; err != nil {
		global.App.Log.Warnf("reconcile monitor alert rule severity failed: %v", err)
	}
	if err := db.Exec(
		"UPDATE insight_monitor_alert_events SET severity = level " +
			"WHERE level IN ('P0','P1','P2','P3') AND severity <> level").Error; err != nil {
		global.App.Log.Warnf("reconcile monitor alert event severity failed: %v", err)
	}

	// 档位唯一性：同模板 + 同指标 + 同等级只允许一行。
	// 先按 min(id) 去重（保留最早那条，告警事件外链不断），再补唯一索引。
	// 索引若已存在，AutoMigrate/重复执行都不会报错（这里显式判存在，避免每次启动打警告日志）。
	if err := db.Exec(
		"DELETE r FROM insight_monitor_alert_rules r " +
			"JOIN (SELECT template_id, metric_key, alert_level, MIN(id) AS keep_id " +
			"FROM insight_monitor_alert_rules GROUP BY template_id, metric_key, alert_level " +
			"HAVING COUNT(*) > 1) d " +
			"ON r.template_id = d.template_id AND r.metric_key = d.metric_key " +
			"AND r.alert_level = d.alert_level AND r.id <> d.keep_id").Error; err != nil {
		global.App.Log.Warnf("dedupe monitor alert rule levels failed: %v", err)
	}
	var indexCount int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() " +
			"AND table_name = 'insight_monitor_alert_rules' AND index_name = 'uk_rule_tpl_metric_level'").
		Scan(&indexCount).Error; err == nil && indexCount == 0 {
		if err := db.Exec(
			"ALTER TABLE insight_monitor_alert_rules " +
				"ADD UNIQUE KEY uk_rule_tpl_metric_level (template_id, metric_key, alert_level)").Error; err != nil {
			global.App.Log.Warnf("create monitor alert rule level unique index failed: %v", err)
		}
	}

	monitorSchemaOnce = true
}

const metricPointsColumnsDDL = `(
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
		instance_id CHAR(15) NOT NULL COMMENT '实例ID',
		db_type VARCHAR(32) NOT NULL DEFAULT '' COMMENT '数据库类型',
		metric_key VARCHAR(64) NOT NULL COMMENT '指标键（三段式）',
		sub_dims VARCHAR(128) NOT NULL DEFAULT '' COMMENT '子维度组合，如 tenant=t1',
		ts DATETIME NOT NULL COMMENT '采集时刻',
		value DOUBLE NULL DEFAULT NULL COMMENT '指标值；quality!=ok 时为 NULL，绝不写 0',
		quality VARCHAR(16) NOT NULL DEFAULT 'ok' COMMENT '数据质量 ok/no_data/unsupported/error/stale',
		source VARCHAR(16) NOT NULL DEFAULT 'direct' COMMENT '来源 direct/prometheus/ssh/platform',
		collect_ms INT NOT NULL DEFAULT 0 COMMENT '采集耗时毫秒',
		created_at DATETIME NOT NULL COMMENT '入库时间',
		PRIMARY KEY (id, ts),
		KEY idx_points_series (instance_id, metric_key, ts),
		KEY idx_points_metric_ts (metric_key, ts),
		KEY idx_points_ts (ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='监控指标原始点'`

func ensureMetricPointsTable(db *gorm.DB) error {
	if db.Migrator().HasTable("insight_monitor_metric_points") {
		return nil
	}
	now := time.Now()
	currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	nextMonth := currentMonth.AddDate(0, 1, 0)
	afterNext := currentMonth.AddDate(0, 2, 0)

	partitionedDDL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS insight_monitor_metric_points %s
	PARTITION BY RANGE (TO_DAYS(ts)) (
		PARTITION %s VALUES LESS THAN (TO_DAYS('%s')),
		PARTITION %s VALUES LESS THAN (TO_DAYS('%s')),
		PARTITION p_max VALUES LESS THAN MAXVALUE
	)`,
		metricPointsColumnsDDL,
		partitionName(currentMonth), currentMonth.Format("2006-01-02"),
		partitionName(nextMonth), afterNext.Format("2006-01-02"),
	)
	if err := db.Exec(partitionedDDL).Error; err == nil {
		return nil
	} else {
		global.App.Log.Warnf("create partitioned monitor points table failed: %v，回退为普通表", err)
	}

	// 分区 DDL 失败（例如托管 MySQL 禁用了分区）时退回普通表，保证采集不中断
	plainDDL := "CREATE TABLE IF NOT EXISTS insight_monitor_metric_points " + metricPointsColumnsDDL
	return db.Exec(plainDDL).Error
}

func ensureMetricPointsIndexes(db *gorm.DB) {
	statements := []string{
		"CREATE INDEX idx_points_series ON insight_monitor_metric_points (instance_id, metric_key, ts)",
		"CREATE INDEX idx_points_metric_ts ON insight_monitor_metric_points (metric_key, ts)",
		"CREATE INDEX idx_points_ts ON insight_monitor_metric_points (ts)",
	}
	for _, statement := range statements {
		var count int64
		name := strings.Fields(statement)[2]
		if err := db.Raw(
			"SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() "+
				"AND TABLE_NAME = 'insight_monitor_metric_points' AND INDEX_NAME = ?", name).Scan(&count).Error; err != nil {
			continue
		}
		if count > 0 {
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			global.App.Log.Warnf("create monitor points index failed: %v", err)
		}
	}
}

func partitionName(month time.Time) string {
	return fmt.Sprintf("p_%s", month.Format("200601"))
}

// ensureMonitorPartitions 保证"当月 + 次月"分区已就位。
// 做法是把 p_max 这个兜底分区 REORGANIZE 拆出下一个月分区，再留一个 p_max。
func ensureMonitorPartitions(db *gorm.DB) {
	var names []string
	if err := db.Raw(
		"SELECT PARTITION_NAME FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = DATABASE() " +
			"AND TABLE_NAME = 'insight_monitor_metric_points' AND PARTITION_NAME IS NOT NULL").
		Scan(&names).Error; err != nil || len(names) == 0 {
		return // 非分区表或查询失败，静默跳过
	}
	existing := map[string]struct{}{}
	for _, name := range names {
		existing[name] = struct{}{}
	}

	now := time.Now()
	nextMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, 0)
	afterNext := nextMonth.AddDate(0, 1, 0)
	if _, ok := existing[partitionName(nextMonth)]; ok {
		return
	}
	if _, ok := existing["p_max"]; !ok {
		return
	}
	statement := fmt.Sprintf(
		"ALTER TABLE insight_monitor_metric_points REORGANIZE PARTITION p_max INTO ("+
			"PARTITION %s VALUES LESS THAN (TO_DAYS('%s')), PARTITION p_max VALUES LESS THAN MAXVALUE)",
		partitionName(nextMonth), afterNext.Format("2006-01-02"))
	if err := db.Exec(statement).Error; err != nil {
		global.App.Log.Warnf("reorganize monitor points partition failed: %v", err)
	}
}

// dropExpiredMonitorPartitions 丢弃整体早于保留期的分区（秒级、无锁）。
// 配合下面按天分批的 DELETE，一起覆盖"跨月全丢 + 月内滑动窗口"两种情形。
func dropExpiredMonitorPartitions(db *gorm.DB, cutoff time.Time) int64 {
	type partitionRow struct {
		PartitionName string
		Description   string
	}
	var rows []partitionRow
	if err := db.Raw(
		"SELECT PARTITION_NAME AS partition_name, PARTITION_DESCRIPTION AS description FROM information_schema.PARTITIONS " +
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'insight_monitor_metric_points' " +
			"AND PARTITION_NAME IS NOT NULL").
		Scan(&rows).Error; err != nil {
		return 0
	}
	dropped := int64(0)
	for _, row := range rows {
		if row.PartitionName == "p_max" {
			continue
		}
		// PARTITION_DESCRIPTION 形如 TO_DAYS('2026-10-01')
		open := strings.Index(row.Description, "'")
		closeIdx := strings.LastIndex(row.Description, "'")
		if open < 0 || closeIdx <= open {
			continue
		}
		boundary, err := time.ParseInLocation("2006-01-02", row.Description[open+1:closeIdx], time.Local)
		if err != nil {
			continue
		}
		if boundary.After(cutoff) {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE insight_monitor_metric_points DROP PARTITION %s", row.PartitionName)
		if err := db.Exec(statement).Error; err != nil {
			global.App.Log.Warnf("drop monitor points partition %s failed: %v", row.PartitionName, err)
			continue
		}
		dropped++
	}
	return dropped
}
