package services

import (
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

// strandedPendingPrepareGrace 兜底扫描的宽限期。
// 正常路径下执行记录创建后会立即被驱动 prepare，1 分钟的窗口足以让创建侧的 goroutine 先手，
// 避免同一记录被并发驱动两次。
const strandedPendingPrepareGrace = time.Minute

// DispatchBackupRunPrepare 在「执行记录创建成功」后统一驱动 run 级 prepare。
//
// 为什么必须显式驱动：PG 物理备份（PG_BACKREST）的执行记录初始状态是 PENDING_PREPARE，
// 而执行节点侧只领取 status='PENDING' 的记录（见 backup/services/agent.go 的候选查询）。
// 也就是说 PENDING_PREPARE → PENDING 这一步只能由服务端进程内的 prepare 完成，
// 它负责校验拓扑、探测版本并把 driver_config 补全后写回执行记录。
//
// 历史缺陷：此前只有「手动立即执行」的 HTTP 视图调用了 AsyncPrepareRun，
// 定时调度路径（triggerScheduledBackupTask）创建记录后没有驱动 prepare，
// 导致所有「定时 + PG 物理备份」的记录永远停在 PENDING_PREPARE，
// 10 分钟后被 cleanupStalePendingPrepareRuns 收口为
// prepare_stage=stale_pending_prepare 的失败记录（执行从未真正开始，start_at 为 NULL）。
func DispatchBackupRunPrepare(run *backupModels.InsightBackupRuns) {
	// InsightBackupRuns 内嵌的是 *commonModels.Model 指针，Model 为 nil 时取 run.ID 会 panic，
	// 这里连 Model 一起判空（调用方偶尔会传未落库的空壳结构体）。
	if run == nil || run.Model == nil || run.ID == 0 {
		return
	}
	runID := run.ID
	if strings.EqualFold(strings.TrimSpace(string(run.DriverType)), "PG_BACKREST") {
		// AsyncPrepareRun 内部已自行起 goroutine，不必再包一层。
		AsyncPrepareRun(runID)
		return
	}
	// 非 PG 驱动正常情况下创建的就是 PENDING，不会进入 PENDING_PREPARE。
	// 这里仅做兜底降级：极端情况下（例如 prepare 列迁移缺失的回退路径）出现了
	// PENDING_PREPARE，就把它推回 PENDING，避免记录永久卡住、被收口成假失败。
	if !strings.EqualFold(strings.TrimSpace(string(run.Status)), "PENDING_PREPARE") {
		return
	}
	go func() {
		if global.App.DB == nil {
			return
		}
		_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).
			Where("id = ? AND status = ?", runID, "PENDING_PREPARE").
			Updates(map[string]any{
				"status":     "PENDING",
				"updated_at": nowLocalTime(),
			}).Error
	}()
}

// listStrandedPendingPrepareRunIDs 选出「已创建但从未被驱动过 prepare」的执行记录 ID。
//
// 只挑选 prepare_status 仍为空或 NONE 的记录：正在 PREPARING 的记录（prepare 已启动）
// 不在此列，避免与在途 goroutine 重复做 SSH 探活与拓扑准备。
func listStrandedPendingPrepareRunIDs() ([]uint64, error) {
	if global.App.DB == nil {
		return nil, nil
	}
	var ids []uint64
	if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("status = ?", "PENDING_PREPARE").
		Where("prepare_status IN ?", []string{"", "NONE"}).
		Where("updated_at <= ?", commonModels.LocalTime(nowLocalTime().Add(-strandedPendingPrepareGrace))).
		Order("id asc").
		Limit(20).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// recoverStrandedPendingPrepareRuns 兜底扫描：把「已创建但从未被驱动过 prepare」的执行记录
// 重新交给 AsyncPrepareRun，避免因进程重启、goroutine 丢失等意外导致记录永久卡在
// PENDING_PREPARE，再被 stale 收口成一条「看似备份失败、实则从未执行」的脏记录。
func recoverStrandedPendingPrepareRuns() (int64, error) {
	ids, err := listStrandedPendingPrepareRunIDs()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if global.App.Log != nil {
			global.App.Log.Infof("[backup] 兜底驱动未启动的 prepare run_id=%d", id)
		}
		AsyncPrepareRun(id)
	}
	return int64(len(ids)), nil
}
