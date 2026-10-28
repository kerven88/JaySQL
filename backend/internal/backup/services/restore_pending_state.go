package services

import (
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
)

// 计划态恢复任务（execution_mode=PLAN）写入 insight_restore_runs 后没有任何协程会接管它：
//   - 逻辑恢复 restores.go:257 只有 plan.AutoExecute 为真才会 go executeRestoreRun；
//   - PLAN 模式的 AutoExecute 恒为 false（logical_restore_workflow.go:389）。
//
// 于是这条记录永久停在 PENDING（start_at 为空、log_path 为空），而「删除 / 重跑」又按
// status 黑名单一律拒绝（restore_control.go:410 / :235），形成既不能删、也不能跑、
// 自动收口（只认 STOP_REQUESTED，restore_stop_state.go:85）也覆盖不到的幽灵任务，
// 唯一出口竟是「立即执行」—— 那会真的跑一次恢复。
//
// 本文件给出判据：**PENDING 且从未被接管 且 静默超过阈值** = 无人接管的计划态残留，允许清理。
const restorePendingOrphanIdleThreshold = 5 * time.Minute

// isRestoreRunPendingOrphan 判断一条 PENDING 恢复任务是否是无人接管的计划态残留。
//
// 三个条件缺一不可：
//  1. status 必须是 PENDING（RUNNING / STOP_REQUESTED 一律不认，它们是真在执行）；
//  2. start_at 必须为零值 —— 执行协程 executeRestoreRun 的第一步就会写 start_at，
//     非空即说明已被接管，哪怕此刻还没跑完；
//  3. updated_at 静默超过阈值 —— 兜住「刚落库、协程还没来得及写 start_at」的竞争窗口。
//     真实执行任务创建后毫秒级就会变成 RUNNING，5 分钟足以区分。
func isRestoreRunPendingOrphan(run backupModels.InsightRestoreRuns) bool {
	if !strings.EqualFold(strings.TrimSpace(string(run.Status)), "PENDING") {
		return false
	}
	// start_at 是直接字段，不经过嵌入的 Model，可安全先判。
	if !time.Time(run.StartAt).IsZero() {
		return false
	}
	// updated_at 来自嵌入的 *models.Model；Model 为 nil 时取它会 panic，
	// 这里没有时间戳可依，按无人接管处理（调用方均来自 DB 查询，正常不会走到）。
	if run.Model == nil {
		return true
	}
	updatedAt := time.Time(run.UpdatedAt)
	if updatedAt.IsZero() {
		return true
	}
	return nowLocalTime().Sub(updatedAt) >= restorePendingOrphanIdleThreshold
}
