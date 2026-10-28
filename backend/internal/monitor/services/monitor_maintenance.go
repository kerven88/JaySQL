package services

import (
	"fmt"
	"time"

	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 监控数据卫生维护
//
// 直接复刻 performance 侧 slowlog_maintenance.go 的形态：两个动作、幂等、
// 每小时跑一轮且启动即跑。紧迫性来自实测：71 条告警事件里 69 条是孤儿（实例已删），
// 389 条快照里 344 条是孤儿 —— 它们正在污染所有统计口径。
// ============================================================================

const (
	// 时序点保留兜底天数（实例未配置 retention_hours 时）
	monitorPointsFallbackRetentionHours = 168
	// 快照宽表保留天数（仅服务历史数据，新版趋势已改走时序点表）
	monitorSnapshotRetentionDays = 30
	// 单批删除行数，避免长事务锁表
	monitorCleanupBatchSize = 5000
	// 单轮最多删除的批次，防止清理任务长时间占用连接
	monitorCleanupMaxBatches = 40
)

// RunMonitorMaintenance 执行一轮维护。各步骤相互独立，单步失败不阻断其他步骤。
func RunMonitorMaintenance() {
	if global.App.DB == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			global.App.Log.Errorf("RunMonitorMaintenance panic: %v", r)
		}
	}()

	ensureMonitorPartitions(global.App.DB)

	orphanPoints := deleteOrphanMonitorRows("insight_monitor_metric_points", "instance_id", "ts < NOW()")
	orphanLatest := deleteOrphanMonitorLatest()
	orphanSnapshots := deleteOrphanMonitorRows("insight_monitor_metric_snapshots", "instance_id", "created_at < NOW()")
	closedEvents := closeOrphanMonitorAlerts()

	expiredPoints := deleteExpiredMonitorPoints()
	expiredSnapshots := deleteExpiredMonitorSnapshots()
	expiredLatest := deleteStaleMonitorLatest()
	droppedPartitions := dropExpiredMonitorPartitions(global.App.DB, time.Now().AddDate(0, 0, -monitorSnapshotRetentionDays))

	if orphanPoints+orphanLatest+orphanSnapshots+closedEvents+expiredPoints+expiredSnapshots+expiredLatest+droppedPartitions > 0 {
		global.App.Log.Infof(
			"[monitor_maintenance] 孤儿清理 points=%d latest=%d snapshots=%d 事件关闭=%d；过期清理 points=%d snapshots=%d latest=%d 分区=%d",
			orphanPoints, orphanLatest, orphanSnapshots, closedEvents,
			expiredPoints, expiredSnapshots, expiredLatest, droppedPartitions)
	}
}

// deleteOrphanMonitorRows 分批删除"实例已不存在"的行（适用于带自增 id 的表）。
// 注意：告警事件不在此列 —— 它有审计价值，只做标记（见 closeOrphanMonitorAlerts）。
func deleteOrphanMonitorRows(table string, column string, extraWhere string) int64 {
	total := int64(0)
	for batch := 0; batch < monitorCleanupMaxBatches; batch++ {
		idSQL := fmt.Sprintf(
			"SELECT id FROM %s WHERE %s NOT IN (SELECT instance_id FROM insight_instances) AND %s LIMIT %d",
			table, column, extraWhere, monitorCleanupBatchSize)
		var ids []uint64
		if err := global.App.DB.Raw(idSQL).Scan(&ids).Error; err != nil {
			global.App.Log.Warnf("scan orphan rows of %s failed: %v", table, err)
			return total
		}
		if len(ids) == 0 {
			return total
		}
		result := global.App.DB.Exec(fmt.Sprintf("DELETE FROM %s WHERE id IN ?", table), ids)
		if result.Error != nil {
			global.App.Log.Warnf("delete orphan rows of %s failed: %v", table, result.Error)
			return total
		}
		total += result.RowsAffected
		if len(ids) < monitorCleanupBatchSize {
			return total
		}
	}
	return total
}

// deleteOrphanMonitorLatest 清理"实例已删除"的最新值行。
//
// 该表主键是 (instance_id, metric_key, sub_dims)，没有自增 id，
// 因此不能复用 deleteOrphanMonitorRows（实测会报 Unknown column 'id'）。
func deleteOrphanMonitorLatest() int64 {
	total := int64(0)
	for batch := 0; batch < monitorCleanupMaxBatches; batch++ {
		var keys []latestKey
		if err := global.App.DB.Raw(`
			SELECT instance_id, metric_key, sub_dims FROM insight_monitor_metric_latest
			WHERE instance_id NOT IN (SELECT instance_id FROM insight_instances)
			LIMIT ?`, monitorCleanupBatchSize).Scan(&keys).Error; err != nil {
			global.App.Log.Warnf("scan orphan monitor latest failed: %v", err)
			return total
		}
		if len(keys) == 0 {
			return total
		}
		affected, err := deleteLatestKeys(keys)
		total += affected
		if err != nil {
			return total
		}
		if len(keys) < monitorCleanupBatchSize {
			return total
		}
	}
	return total
}

// deleteLatestKeys 按主键组合删除最新值行，返回受影响行数。
func deleteLatestKeys(keys []latestKey) (int64, error) {
	affected := int64(0)
	for _, key := range keys {
		result := global.App.DB.Exec(
			"DELETE FROM insight_monitor_metric_latest WHERE instance_id = ? AND metric_key = ? AND sub_dims = ?",
			key.InstanceID, key.MetricKey, key.SubDims)
		if result.Error != nil {
			global.App.Log.Warnf("delete stale latest row failed: %v", result.Error)
			return affected, result.Error
		}
		affected += result.RowsAffected
	}
	return affected, nil
}

// closeOrphanMonitorAlerts 实例已删除时关闭其待处理告警。只标记不删。
func closeOrphanMonitorAlerts() int64 {
	result := global.App.DB.Exec(`
		UPDATE insight_monitor_alert_events e
		LEFT JOIN insight_instances i ON i.instance_id = e.instance_id
		SET e.status = 'recovered', e.recovered_at = NOW(), e.suppressed = 1,
		    e.message = CONCAT(e.message, '（实例已删除，告警自动关闭）')
		WHERE i.instance_id IS NULL AND e.status IN ('active', 'pending')`)
	if result.Error != nil {
		global.App.Log.Warnf("close orphan monitor alerts failed: %v", result.Error)
		return 0
	}
	return result.RowsAffected
}

// deleteExpiredMonitorPoints 按实例保留期分批清理过期时序点。
// 保留期取自实例自己的 retention_hours，未配置的按兜底 7 天。
func deleteExpiredMonitorPoints() int64 {
	return deleteByIDs(`
		SELECT p.id FROM insight_monitor_metric_points p
		LEFT JOIN insight_monitor_target_configs c ON c.instance_id = p.instance_id
		WHERE p.ts < DATE_SUB(NOW(), INTERVAL COALESCE(NULLIF(c.retention_hours, 0), ?) HOUR)
		LIMIT ?
	`, monitorPointsFallbackRetentionHours, "insight_monitor_metric_points")
}

func deleteExpiredMonitorSnapshots() int64 {
	return deleteByIDs(`
		SELECT id FROM insight_monitor_metric_snapshots
		WHERE created_at < DATE_SUB(NOW(), INTERVAL ? DAY)
		LIMIT ?
	`, monitorSnapshotRetentionDays, "insight_monitor_metric_snapshots")
}

// deleteStaleMonitorLatest 清理"很久没有更新"的最新值行。
// 它通常意味着实例已被移出监控范围但配置行还在，属于噪音。
// 注意：该表主键是 (instance_id, metric_key, sub_dims)，没有自增 id，所以按主键组合取。
func deleteStaleMonitorLatest() int64 {
	return deleteByIDs(`
		SELECT instance_id, metric_key, sub_dims FROM insight_monitor_metric_latest
		WHERE updated_at < DATE_SUB(NOW(), INTERVAL ? DAY)
		LIMIT ?
	`, monitorSnapshotRetentionDays, "insight_monitor_metric_latest")
}

// 说明：insight_monitor_metric_latest 的主键是 (instance_id, metric_key, sub_dims)，
// 没有自增 id。为统一批次删除逻辑，这里对无 id 的表按主键组合删除。
type latestKey struct {
	InstanceID string
	MetricKey  string
	SubDims    string
}

// deleteByIDs 按过期条件分批删除，返回受影响行数。
// insight_monitor_metric_latest 没有自增 id，走主键组合删除；其余表按 id 删除。
func deleteByIDs(scanSQL string, arg any, table string) int64 {
	total := int64(0)
	for batch := 0; batch < monitorCleanupMaxBatches; batch++ {
		if table == "insight_monitor_metric_latest" {
			var keys []latestKey
			if err := global.App.DB.Raw(scanSQL, arg, monitorCleanupBatchSize).Scan(&keys).Error; err != nil {
				global.App.Log.Warnf("scan expired rows of %s failed: %v", table, err)
				return total
			}
			if len(keys) == 0 {
				return total
			}
			affected, err := deleteLatestKeys(keys)
			total += affected
			if err != nil {
				return total
			}
			if len(keys) < monitorCleanupBatchSize {
				return total
			}
			continue
		}

		var ids []uint64
		if err := global.App.DB.Raw(scanSQL, arg, monitorCleanupBatchSize).Scan(&ids).Error; err != nil {
			global.App.Log.Warnf("scan expired rows of %s failed: %v", table, err)
			return total
		}
		if len(ids) == 0 {
			return total
		}
		result := global.App.DB.Exec(fmt.Sprintf("DELETE FROM %s WHERE id IN ?", table), ids)
		if result.Error != nil {
			global.App.Log.Warnf("delete expired rows of %s failed: %v", table, result.Error)
			return total
		}
		total += result.RowsAffected
		if len(ids) < monitorCleanupBatchSize {
			return total
		}
	}
	return total
}
