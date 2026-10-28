package services

import (
	"fmt"
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

// 恢复任务的「终止请求」多久没有进展就认定为执行协程已失联，由系统自动收口为已终止。
//
// 为什么备份侧用 30s（stopRequestedStaleThreshold），而这里要用 2 分钟：
//
//	备份任务的停止检查点很密（executor 在导出/归档/校验各阶段都轮询），30s 足够；
//	恢复链路存在很长的不检查空档 —— 例如 mysql 导入一个大事务、pg_restore 恢复索引，
//	主协程阻塞在 cmd.Wait() 上，期间既不写 last_report_at 也不写 updated_at。
//	阈值过短会在任务正常推进时把它误判成失联。
const restoreStopRequestedStaleThreshold = 2 * time.Minute

// CleanupStaleRestoreStopRequestedRuns 收口所有陷入 STOP_REQUESTED 的恢复任务。
// 这里刻意同时覆盖逻辑恢复与物理恢复：两者共用 insight_restore_runs 表，
// 且「终止 / 删除 / 编辑执行」三条下游链路都不接受 STOP_REQUESTED，一旦卡住无任何出口。
func CleanupStaleRestoreStopRequestedRuns() (int64, error) {
	return cleanupStaleRestoreStopRequestedRunsWithFilter(0)
}

// cleanupStaleRestoreStopRequestedRun 收口指定恢复任务，用于详情/日志这类单条读路径。
func cleanupStaleRestoreStopRequestedRun(runID uint64) (int64, error) {
	return cleanupStaleRestoreStopRequestedRunsWithFilter(runID)
}

// restoreStopRequestStale 判断一条恢复任务的终止请求是否已经失联。
// 与批量收口共用同一判据，保证「自动收口」和「操作员再次点击终止」行为一致。
func restoreStopRequestStale(lastReportAt commonModels.LocalTime, updatedAt commonModels.LocalTime) bool {
	latest := updatedAt
	if ts := time.Time(lastReportAt); !ts.IsZero() && ts.After(time.Time(latest)) {
		latest = lastReportAt
	}
	ts := time.Time(latest)
	if ts.IsZero() {
		return false
	}
	return nowLocalTime().Sub(ts) >= restoreStopRequestedStaleThreshold
}

// finalizeStaleRestoreStopRequest 把一条失联的 STOP_REQUESTED 任务收口为 STOPPED。
// 返回是否确实发生了收口（用于区分「刚下单还没到阈值」与「已失联」）。
func finalizeStaleRestoreStopRequest(runID uint64, stopReason string, logPath string) (bool, error) {
	result := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Where("id = ? AND status = ?", runID, "STOP_REQUESTED").
		Updates(map[string]any{
			"status":         "STOPPED",
			"end_at":         commonModels.LocalTime(nowLocalTime()),
			"current_action": "已终止",
			"error_message":  strings.TrimSpace(stopReason),
			"log_excerpt":    tailLogExcerpt(logPath),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// cleanupStaleRestoreStopRequestedRunsWithFilter 按 runID 收口（runID=0 表示全部）。
//
// 失联判据取「最后活动时刻」= max(last_report_at, updated_at)：
//   - 执行协程正常推进时会持续刷新 last_report_at（逻辑恢复 logical_restore_workflow.go:1530/1551、
//     物理恢复 physical_restores.go:1776、MySQL 物理 mysql_physical_restores.go:976）
//   - StopXXXView 下发终止请求时会刷 updated_at
//     两者都停更即说明协程已无法继续推进，只剩下一张孤立的 STOP_REQUESTED 记录。
func cleanupStaleRestoreStopRequestedRunsWithFilter(runID uint64) (int64, error) {
	if global.App.DB == nil {
		return 0, nil
	}
	cutoff := commonModels.LocalTime(nowLocalTime().Add(-restoreStopRequestedStaleThreshold))
	reason := fmt.Sprintf(
		"终止请求发出后超过 %d 分钟未收口（恢复进程疑似已失联），系统已自动收口为已终止",
		int(restoreStopRequestedStaleThreshold/time.Minute),
	)

	tx := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Where("status = ?", "STOP_REQUESTED").
		Where("COALESCE(GREATEST(last_report_at, updated_at), updated_at) <= ?", cutoff)
	if runID != 0 {
		tx = tx.Where("id = ?", runID)
	}

	// 已收口的任务补齐终态字段，前端「已终止」状态才有完整的时间与动作展示。
	result := tx.Updates(map[string]any{
		"status":         "STOPPED",
		"end_at":         commonModels.LocalTime(nowLocalTime()),
		"current_action": "已终止",
		"error_message":  reason,
	})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
