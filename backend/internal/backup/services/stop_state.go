package services

import (
	"fmt"
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

const stopRequestedStaleThreshold = 30 * time.Second
const staleRunningRunThreshold = 15 * time.Minute
const stalePendingPrepareRunThreshold = 10 * time.Minute

type stopStateDescriptor struct {
	State   string
	Label   string
	Message string
	Hint    string
}

func deriveRunProgressStageLabel(logExcerpt string, errorMessage string) string {
	text := strings.TrimSpace(logExcerpt + "\n" + errorMessage)
	switch {
	case strings.Contains(text, "开始执行 PostgreSQL 物理备份"):
		return "基础备份中"
	case strings.Contains(text, "物理备份产物"):
		return "收集产物"
	case strings.Contains(text, "开始归档物理备份产物"):
		return "归档物理产物"
	case strings.Contains(text, "写入备份集成功"):
		return "写入备份集"
	case strings.Contains(text, "开始计算校验和"):
		return "校验中"
	case strings.Contains(text, "校验完成"):
		return "校验完成"
	case strings.Contains(text, "开始归档"), strings.Contains(text, "归档输出文件"):
		return "归档中"
	case strings.Contains(text, "导出完成"):
		return "导出完成"
	case strings.Contains(text, "开始导出"):
		return "导出中"
	case strings.Contains(text, "探测"), strings.Contains(text, "连接"), strings.Contains(text, "version"):
		return "连接源库"
	default:
		return ""
	}
}

func isStopUnsafeStage(stage string) bool {
	switch strings.TrimSpace(stage) {
	case "归档中", "校验中", "收集产物", "归档物理产物":
		return true
	default:
		return false
	}
}

func isStopRequestStale(updatedAt commonModels.LocalTime) bool {
	ts := time.Time(updatedAt)
	if ts.IsZero() {
		return false
	}
	return nowLocalTime().Sub(ts) >= stopRequestedStaleThreshold
}

func cleanupStaleStopRequestedRuns() (int64, error) {
	return cleanupStaleStopRequestedRunsWithFilter("", 0)
}

func cleanupStaleRunningRuns() (int64, error) {
	return cleanupStaleRunningRunsWithFilter("", 0)
}

func cleanupStalePendingPrepareRuns() (int64, error) {
	return cleanupStalePendingPrepareRunsWithFilter("", 0)
}

func cleanupStaleRunningRunsForTask(taskID string) (int64, error) {
	return cleanupStaleRunningRunsWithFilter(strings.TrimSpace(taskID), 0)
}

func cleanupStaleRunningRun(runID uint64) (int64, error) {
	return cleanupStaleRunningRunsWithFilter("", runID)
}

func cleanupStalePendingPrepareRun(runID uint64) (int64, error) {
	return cleanupStalePendingPrepareRunsWithFilter("", runID)
}

func cleanupStaleStopRequestedRunsForTask(taskID string) (int64, error) {
	return cleanupStaleStopRequestedRunsWithFilter(strings.TrimSpace(taskID), 0)
}

func cleanupStaleStopRequestedRun(runID uint64) (int64, error) {
	return cleanupStaleStopRequestedRunsWithFilter("", runID)
}

func cleanupStalePendingPrepareRunsForTask(taskID string) (int64, error) {
	return cleanupStalePendingPrepareRunsWithFilter(strings.TrimSpace(taskID), 0)
}

func cleanupStaleStopRequestedRunsWithFilter(taskID string, runID uint64) (int64, error) {
	if global.App.DB == nil {
		return 0, nil
	}
	cutoff := commonModels.LocalTime(nowLocalTime().Add(-stopRequestedStaleThreshold))
	reason := fmt.Sprintf("终止请求超过 %d 秒未收口，系统已自动收口为已终止", int(stopRequestedStaleThreshold/time.Second))

	tx := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("status = ?", "STOP_REQUESTED").
		Where("updated_at <= ?", cutoff)
	if taskID != "" {
		tx = tx.Where("task_id = ?", taskID)
	}
	if runID != 0 {
		tx = tx.Where("id = ?", runID)
	}

	result := tx.Updates(map[string]any{
		"status":        "STOPPED",
		"end_at":        commonModels.LocalTime(nowLocalTime()),
		"error_message": reason,
	})
	return result.RowsAffected, result.Error
}

func cleanupStalePendingPrepareRunsWithFilter(taskID string, runID uint64) (int64, error) {
	if global.App.DB == nil {
		return 0, nil
	}
	cutoff := commonModels.LocalTime(nowLocalTime().Add(-stalePendingPrepareRunThreshold))
	reason := fmt.Sprintf("prepare 等待超过 %d 分钟未启动，系统已自动收口，请重新执行备份", int(stalePendingPrepareRunThreshold/time.Minute))

	tx := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("status = ?", "PENDING_PREPARE").
		Where("prepare_status IN ?", []string{"", "NONE"}).
		Where("updated_at <= ?", cutoff)
	if taskID != "" {
		tx = tx.Where("task_id = ?", taskID)
	}
	if runID != 0 {
		tx = tx.Where("id = ?", runID)
	}

	result := tx.Updates(map[string]any{
		"status":         "FAILED",
		"prepare_status": "PREPARE_ERROR",
		"prepare_stage":  "stale_pending_prepare",
		"prepare_title":  "运行级 prepare 长时间未启动",
		"prepare_suggestion": "请重新执行备份；若持续出现，请检查 JaySQL 服务日志中的 prepare 调度异常",
		"prepare_detail":     reason,
		"error_message":      fmt.Sprintf("[prepare stage=stale_pending_prepare] title:运行级 prepare 长时间未启动；suggestion:请重新执行备份；若持续出现，请检查 JaySQL 服务日志中的 prepare 调度异常；detail:%s", reason),
		"end_at":             commonModels.LocalTime(nowLocalTime()),
		"updated_at":         commonModels.LocalTime(nowLocalTime()),
	})
	return result.RowsAffected, result.Error
}

func cleanupStaleRunningRunsWithFilter(taskID string, runID uint64) (int64, error) {
	if global.App.DB == nil {
		return 0, nil
	}
	cutoff := commonModels.LocalTime(nowLocalTime().Add(-staleRunningRunThreshold))
	reasonRunning := fmt.Sprintf("执行记录超过 %d 分钟未更新，系统判定为异常中断并已自动收口", int(staleRunningRunThreshold/time.Minute))
	reasonPendingOffline := fmt.Sprintf("执行节点离线超过 %d 分钟仍未领取该任务，系统已自动收口为失败", int(staleRunningRunThreshold/time.Minute))
	prepareStaleCutoff := commonModels.LocalTime(nowLocalTime().Add(-prepareStaleTimeout))
	reasonPrepareTimeout := fmt.Sprintf("准备阶段超过 %d 分钟仍未完成，系统已自动收口；请重新点击“立即执行”或刷新后重试", int(prepareStaleTimeout/time.Minute))

	var affected int64

	txRunning := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("status = ?", "RUNNING").
		Where("start_at IS NOT NULL").
		Where("updated_at <= ?", cutoff)
	if taskID != "" {
		txRunning = txRunning.Where("task_id = ?", taskID)
	}
	if runID != 0 {
		txRunning = txRunning.Where("id = ?", runID)
	}
	resRunning := txRunning.Updates(map[string]any{
		"status":        "FAILED",
		"end_at":        commonModels.LocalTime(nowLocalTime()),
		"error_message": reasonRunning,
	})
	if resRunning.Error != nil {
		return 0, resRunning.Error
	}
	affected += resRunning.RowsAffected

	subQuery := global.App.DB.Model(&backupModels.InsightBackupNodes{}).
		Select("1").
		Where("id = insight_backup_runs.executor_node_id").
		Where("status != ?", "ONLINE")

	txPending := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("status = ?", "PENDING").
		Where("executor_node_id IS NOT NULL AND executor_node_id != 0").
		Where("updated_at <= ?", cutoff).
		Where("EXISTS (?)", subQuery)
	if taskID != "" {
		txPending = txPending.Where("task_id = ?", taskID)
	}
	if runID != 0 {
		txPending = txPending.Where("id = ?", runID)
	}
	resPending := txPending.Updates(map[string]any{
		"status":        "FAILED",
		"end_at":        commonModels.LocalTime(nowLocalTime()),
		"error_message": reasonPendingOffline,
	})
	if resPending.Error != nil {
		return affected, resPending.Error
	}
	affected += resPending.RowsAffected

	txPrepareRuns := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("prepare_status = ?", "PREPARING").
		Where("prepare_started_at IS NOT NULL").
		Where("prepare_started_at <= ?", prepareStaleCutoff)
	if taskID != "" {
		txPrepareRuns = txPrepareRuns.Where("task_id = ?", taskID)
	}
	if runID != 0 {
		txPrepareRuns = txPrepareRuns.Where("id = ?", runID)
	}
	resPrepareRuns := txPrepareRuns.Updates(map[string]any{
		"prepare_status":     "PREPARE_ERROR",
		"prepare_stage":      "timeout",
		"prepare_title":      "准备阶段超时",
		"prepare_suggestion": reasonPrepareTimeout,
		"prepare_detail":     reasonPrepareTimeout,
		"prepare_finished_at": nowLocalTime(),
		"prepare_raw_error":  fmt.Sprintf("stale prepare_started_at before %v", prepareStaleCutoff),
		"error_message":      reasonPrepareTimeout,
	})
	if resPrepareRuns.Error != nil {
		return affected, resPrepareRuns.Error
	}
	affected += resPrepareRuns.RowsAffected

	txPrepareTasks := global.App.DB.Model(&backupModels.InsightBackupTasks{}).
		Where("prepare_status = ?", "PREPARING").
		Where("prepare_started_at IS NOT NULL").
		Where("prepare_started_at <= ?", prepareStaleCutoff)
	if taskID != "" {
		txPrepareTasks = txPrepareTasks.Where("task_id = ?", taskID)
	}
	resPrepareTasks := txPrepareTasks.Updates(map[string]any{
		"prepare_status":      "PREPARE_ERROR",
		"prepare_stage":       "timeout",
		"prepare_title":       "准备阶段超时",
		"prepare_suggestion":  reasonPrepareTimeout,
		"prepare_detail":      reasonPrepareTimeout,
		"prepare_finished_at": nowLocalTime(),
		"prepare_raw_error":   fmt.Sprintf("stale task prepare_started_at before %v", prepareStaleCutoff),
	})
	if resPrepareTasks.Error != nil {
		return affected, resPrepareTasks.Error
	}
	affected += resPrepareTasks.RowsAffected

	return affected, nil
}

func deriveRunStopState(status string, progressStage string, executorNodeStatus string, updatedAt commonModels.LocalTime) stopStateDescriptor {
	runStatus := strings.ToUpper(strings.TrimSpace(status))
	stage := strings.TrimSpace(progressStage)
	nodeStatus := strings.ToUpper(strings.TrimSpace(executorNodeStatus))

	switch runStatus {
	case "PENDING":
		return stopStateDescriptor{
			State:   "IMMEDIATE",
			Label:   "可立即终止",
			Message: "当前任务仍在排队，确认后会直接终止，不会真正启动备份进程。",
			Hint:    "适用于任务尚未被执行节点领取的阶段。",
		}
	case "RUNNING":
		if nodeStatus == "OFFLINE" {
			return stopStateDescriptor{
				State:   "UNABLE",
				Label:   "终止失败/无法终止",
				Message: "执行节点当前离线，平台无法确认正在运行的备份进程是否仍可控。",
				Hint:    "建议先检查 Agent 和执行节点，再决定是否继续发送终止请求。",
			}
		}
		if isStopUnsafeStage(stage) {
			stageLabel := defaultStopText(stage, "当前阶段")
			return stopStateDescriptor{
				State:   "UNINTERRUPTIBLE",
				Label:   "当前阶段不可立即中断",
				Message: fmt.Sprintf("当前处于%s，不能安全瞬时中断。你仍可发送终止请求，系统会在本阶段结束后尽快收口。", stageLabel),
				Hint:    "继续终止后，本次备份不会生成可用备份集。",
			}
		}
	case "STOP_REQUESTED":
		if nodeStatus == "OFFLINE" {
			return stopStateDescriptor{
				State:   "UNABLE",
				Label:   "终止失败/无法终止",
				Message: "已记录终止请求，但执行节点当前离线，平台暂时无法确认进程是否真正退出。",
				Hint:    "请优先检查 Agent、节点进程和日志，必要时做人工收口。",
			}
		}
		if isStopUnsafeStage(stage) {
			stageLabel := defaultStopText(stage, "当前阶段")
			return stopStateDescriptor{
				State:   "UNINTERRUPTIBLE",
				Label:   "当前阶段不可立即中断",
				Message: fmt.Sprintf("已收到终止请求，但当前仍处于%s，暂时不能安全中断。", stageLabel),
				Hint:    "系统会在本阶段结束后尽快收口，请继续观察日志和执行状态。",
			}
		}
		if isStopRequestStale(updatedAt) {
			return stopStateDescriptor{
				State:   "UNABLE",
				Label:   "终止失败/无法终止",
				Message: "终止请求已发出较长时间，当前仍未完成收口，可能存在执行控制器丢失或进程脱离控制。",
				Hint:    "建议检查节点侧备份进程和 Agent 日志，必要时进行人工收口。",
			}
		}
		return stopStateDescriptor{
			State:   "STOPPING",
			Label:   "正在终止",
			Message: "已通知执行节点中止，正在等待当前子进程退出。",
			Hint:    "正常情况下页面会在短时间内刷新为已终止；如果超过 30 秒仍未收口，系统会提示可能无法终止。",
		}
	}
	return stopStateDescriptor{}
}

func defaultStopText(value string, fallback string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return strings.TrimSpace(fallback)
	}
	return text
}
