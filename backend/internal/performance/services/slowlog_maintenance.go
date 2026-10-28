package services

import (
	"fmt"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
)

const (
	// slowLogMaintenanceSpec 维护任务节奏。对账与保留期清理都是幂等的轻量操作，
	// 每小时一轮足以收敛孤儿数据，且不会给元数据库造成压力。
	slowLogMaintenanceSpec = "@every 1h"

	// 无任何全局参数时的保留期兜底（天）。有全局参数则以参数值为准。
	slowLogRetentionFallbackDays = 30

	// 单轮最多删除的行数与单批删除行数，避免长事务锁表。
	slowLogRetentionBatchSize = 5000
	slowLogRetentionMaxPerRun = 200000

	// 采集器「实例已删除」的终态标记。
	slowLogCollectStatusOrphan = "ORPHAN"
)

// RegisterSlowLogMaintenanceCronJobs 注册慢日志数据卫生维护任务：
// ① 孤儿采集器对账（实例已删除但采集器/明细残留）；
// ② 慢日志明细按全局参数的 retention_days 过期清理。
func RegisterSlowLogMaintenanceCronJobs() {
	if global.App.Cron == nil {
		return
	}
	if _, err := global.App.Cron.AddFunc(slowLogMaintenanceSpec, func() {
		RunSlowLogMaintenance()
	}); err != nil {
		global.App.Log.Warnf("register slowlog maintenance cron failed: %v", err)
	}
	// 启动即跑一轮：部署新版本后无需等一小时即可收敛存量脏数据。
	go RunSlowLogMaintenance()
}

// RunSlowLogMaintenance 执行一轮维护。两步相互独立，单步失败不阻断另一步。
func RunSlowLogMaintenance() {
	if global.App.DB == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			global.App.Log.Errorf("RunSlowLogMaintenance panic: %v", r)
		}
	}()

	orphans := reconcileOrphanSlowLogCollectors()
	expired := cleanupExpiredSlowLogEntries()

	if orphans > 0 || expired > 0 {
		global.App.Log.Infof("[slowlog_maintenance] 孤儿采集器=%d，清理过期明细=%d", orphans, expired)
	}
}

// reconcileOrphanSlowLogCollectors 对账采集器与实例：实例已被删除的采集器标记为 ORPHAN。
//
// 只做标记（enabled=false + collect_status=ORPHAN），不删除行与明细——
// 删除是不可逆操作，交由一次性清理脚本按人工确认执行。
func reconcileOrphanSlowLogCollectors() int {
	var collectorIDs []string
	if err := global.App.DB.Model(&performanceModels.InsightPerformanceSlowLogCollectors{}).
		Where("instance_id <> ''").
		Pluck("instance_id", &collectorIDs).Error; err != nil {
		global.App.Log.Warnf("[slowlog_maintenance] 读取采集器实例列表失败: %v", err)
		return 0
	}
	if len(collectorIDs) == 0 {
		return 0
	}

	var instanceIDs []string
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id <> ''").
		Pluck("instance_id", &instanceIDs).Error; err != nil {
		global.App.Log.Warnf("[slowlog_maintenance] 读取实例列表失败: %v", err)
		return 0
	}

	orphans := diffOrphanInstanceIDs(collectorIDs, instanceIDs)
	if len(orphans) == 0 {
		return 0
	}

	var collectors []performanceModels.InsightPerformanceSlowLogCollectors
	if err := global.App.DB.Where("instance_id IN ?", orphans).Find(&collectors).Error; err != nil {
		global.App.Log.Warnf("[slowlog_maintenance] 读取孤儿采集器失败: %v", err)
		return 0
	}

	marked := 0
	for i := range collectors {
		collector := &collectors[i]
		if strings.TrimSpace(collector.CollectStatus) == slowLogCollectStatusOrphan {
			continue
		}
		if err := global.App.DB.Model(collector).Updates(map[string]any{
			"enabled":        false,
			"collect_status": slowLogCollectStatusOrphan,
		}).Error; err != nil {
			global.App.Log.Warnf("[slowlog_maintenance] 标记孤儿采集器失败: %v, instance=%s", err, collector.InstanceID)
			continue
		}
		message := fmt.Sprintf("[slowlog_maintenance] instance=%s 对应实例已删除，采集器标记为 ORPHAN（可用清理脚本移除）", collector.InstanceID)
		global.App.Log.Info(message)
		_ = global.App.DB.Model(collector).Updates(map[string]any{
			"last_notified_error": truncateNotifySignature(message),
			"last_notified_at":    commonModels.LocalTime(time.Now()),
		}).Error
		marked++
	}
	return marked
}

// diffOrphanInstanceIDs 返回「采集器有、实例没有」的实例 ID（保序、去重）。
func diffOrphanInstanceIDs(collectorIDs, instanceIDs []string) []string {
	alive := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		if key := strings.TrimSpace(id); key != "" {
			alive[key] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(collectorIDs))
	result := make([]string, 0)
	for _, id := range collectorIDs {
		key := strings.TrimSpace(id)
		if key == "" {
			continue
		}
		if _, ok := alive[key]; ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

// cleanupExpiredSlowLogEntries 按保留期清理慢日志明细，分批删除避免长事务。
// 过期基准取事件时间（缺失时回落采集时间），即「这条慢日志本身有多旧」。
func cleanupExpiredSlowLogEntries() int64 {
	days := resolveSlowLogRetentionDays()
	if days < 1 {
		return 0
	}
	cutoff := slowLogRetentionCutoff(time.Now(), days)

	var deleted int64
	for deleted < slowLogRetentionMaxPerRun {
		tx := global.App.DB.
			Where("ifnull(event_time, collect_time) < ?", cutoff).
			Limit(slowLogRetentionBatchSize).
			Delete(&performanceModels.InsightPerformanceSlowLogEntries{})
		if tx.Error != nil {
			global.App.Log.Warnf("[slowlog_maintenance] 清理过期慢日志明细失败: %v", tx.Error)
			break
		}
		if tx.RowsAffected == 0 {
			break
		}
		deleted += tx.RowsAffected
		if tx.RowsAffected < int64(slowLogRetentionBatchSize) {
			break
		}
	}
	return deleted
}

// resolveSlowLogRetentionDays 取启用中的全局快照参数的最大保留天数；
// 无可用参数时使用兜底值。参数值小于 1 视为未配置。
func resolveSlowLogRetentionDays() int {
	var params []performanceModels.InsightPerformanceSnapshotParams
	if err := global.App.DB.
		Where("scope_type = ?", "global").
		Where("enabled = ?", true).
		Find(&params).Error; err != nil {
		global.App.Log.Warnf("[slowlog_maintenance] 读取快照参数失败，使用兜底保留期 %d 天: %v", slowLogRetentionFallbackDays, err)
		return slowLogRetentionFallbackDays
	}
	return resolveRetentionDaysFromParams(params, slowLogRetentionFallbackDays)
}

// resolveRetentionDaysFromParams 从参数列表中取最大保留天数，全部无效时返回 fallback。
func resolveRetentionDaysFromParams(params []performanceModels.InsightPerformanceSnapshotParams, fallback int) int {
	best := 0
	for i := range params {
		if days := params[i].RetentionDays; days > best {
			best = days
		}
	}
	if best < 1 {
		return fallback
	}
	return best
}

// slowLogRetentionCutoff 计算保留期截止时间：早于该时间的明细将过期。
func slowLogRetentionCutoff(now time.Time, days int) time.Time {
	return now.AddDate(0, 0, -days)
}
