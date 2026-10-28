package services

import (
	"github.com/simajiong/jaysql/internal/global"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"gorm.io/gorm"
)

// ============================================================================
// 慢日志表结构：AutoMigrate 之外的补充迁移
//
// 为什么需要它：EventKey 的唯一索引不能交给 AutoMigrate。
// AutoMigrate 的顺序是「先 ALTER ADD COLUMN，再 CREATE UNIQUE INDEX」，
// 而存量表加完列后 event_key 全是 ''，多行 '' 会让唯一索引当场
// Duplicate entry '' 失败；bootstrap 对 AutoMigrate 的 error 是 Fatal，
// 于是服务起不来。
//
// 与 monitor 模块的做法一致（EnsureMonitorSchema + backfillAlertEventIDs）：
// 索引这类「依赖存量数据」的变更统一放在启动期的 ensure 里，
// 按顺序做「回填 → 去重 → 建索引」，且任何一步失败都只告警不阻断启动。
// ============================================================================

const (
	slowLogEntriesTable  = "insight_performance_slowlog_entries"
	slowLogEventKeyIndex = "uniq_perf_slowlog_event_key"
	// 单轮回填上限：启动期动作，不能一把锁住整张明细表。
	slowLogBackfillBatch = 5000
	// 轮数上限（5000 × 2000 = 1000 万行）：存量表正常远小于此。
	// 超了也不阻断启动——剩余行 event_key 仍为 ''，
	// 唯一索引会跳过创建并告警，由 DBA 用 SQL 脚本离线补齐。
	slowLogBackfillMaxRound = 2000
)

// EnsureSlowLogSchema 补齐慢日志表在 AutoMigrate 之外需要的结构变更。
// 幂等，可在每次启动时调用。
func EnsureSlowLogSchema(db *gorm.DB) {
	if db == nil {
		return
	}
	if !db.Migrator().HasTable(&performanceModels.InsightPerformanceSlowLogEntries{}) {
		return
	}
	backfillSlowLogEventKey(db)
	ensureSlowLogEventKeyUniqueIndex(db)
}

// backfillSlowLogEventKey 给存量明细补 event_key。
//
// 两条规则：
//  1. 有 inode 的历史行（MySQL / PG 文件采集）：instance_id|inode|offset
//     —— 与新版写入规则逐字符一致，且受原有唯一键 (instance_id, inode, offset) 保护，不会撞车。
//  2. 兜底：拿不到 inode 的异常行用 legacy|<主键>，保证非空唯一。
//
// 只处理 event_key = ” 的行，重复执行安全。
func backfillSlowLogEventKey(db *gorm.DB) {
	total := int64(0)
	for round := 0; round < slowLogBackfillMaxRound; round++ {
		result := db.Exec(
			"UPDATE `"+slowLogEntriesTable+"` "+
				"SET `event_key` = CONCAT(`instance_id`, '|', `file_inode`, '|', `offset_start`) "+
				"WHERE `event_key` = '' AND `file_inode` <> '' LIMIT ?", slowLogBackfillBatch)
		if result.Error != nil {
			global.App.Log.Warnf("backfill slowlog event_key failed: %v", result.Error)
			return
		}
		total += result.RowsAffected
		if result.RowsAffected < slowLogBackfillBatch {
			break
		}
	}
	for round := 0; round < slowLogBackfillMaxRound; round++ {
		result := db.Exec(
			"UPDATE `"+slowLogEntriesTable+"` SET `event_key` = CONCAT('legacy|', `id`) "+
				"WHERE `event_key` = '' LIMIT ?", slowLogBackfillBatch)
		if result.Error != nil {
			global.App.Log.Warnf("backfill slowlog event_key (fallback) failed: %v", result.Error)
			return
		}
		total += result.RowsAffected
		if result.RowsAffected < slowLogBackfillBatch {
			break
		}
	}
	if total > 0 {
		global.App.Log.Infof("backfilled event_key for %d slowlog entries", total)
	}
}

// ensureSlowLogEventKeyUniqueIndex 在回填完成后建 event_key 唯一索引。
//
// 建索引前先确认没有重复值：理论上不会（受原唯一键保护），
// 但若历史数据被手工改过而出现重复，宁可跳过索引也不让启动失败——
// 幂等由入库侧的 FirstOrCreate 保证，索引只是加速器与最后一道防线。
func ensureSlowLogEventKeyUniqueIndex(db *gorm.DB) {
	if hasIndex(db, slowLogEntriesTable, slowLogEventKeyIndex) {
		return
	}
	var duplicate int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM (SELECT `event_key` FROM `" + slowLogEntriesTable + "` " +
			"GROUP BY `event_key` HAVING COUNT(*) > 1) t").Scan(&duplicate).Error; err != nil {
		global.App.Log.Warnf("check duplicate slowlog event_key failed: %v", err)
		return
	}
	if duplicate > 0 {
		global.App.Log.Warnf("skip creating %s: %d duplicated event_key found, "+
			"run backend/sql/20261023_slowlog_multilengine_prereq.sql to clean up",
			slowLogEventKeyIndex, duplicate)
		return
	}
	if err := db.Exec("ALTER TABLE `" + slowLogEntriesTable +
		"` ADD UNIQUE KEY `" + slowLogEventKeyIndex + "` (`event_key`)").Error; err != nil {
		global.App.Log.Warnf("create %s failed: %v", slowLogEventKeyIndex, err)
		return
	}
	global.App.Log.Infof("created unique index %s on %s", slowLogEventKeyIndex, slowLogEntriesTable)
}

// hasIndex 判断某张表上是否已存在指定索引。
// 用 information_schema 而不是 gorm Migrator：后者对「索引存在但列顺序不同」
// 的判定不够直观，这里只需要一个纯粹的「在不在」。
func hasIndex(db *gorm.DB, table, index string) bool {
	if db == nil || table == "" || index == "" {
		return false
	}
	var count int64
	err := db.Raw("SELECT COUNT(*) FROM information_schema.STATISTICS "+
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?", table, index).
		Scan(&count).Error
	if err != nil {
		global.App.Log.Warnf("check index %s on %s failed: %v", index, table, err)
		return false
	}
	return count > 0
}
