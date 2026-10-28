package services

import (
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"
)

// backupRunRetentionSkipStatuses 不参与保留期清理的执行状态：
// 仍在排队/执行中、或 prepare 尚未收口的记录必须保留，避免误删正在写入的产物。
var backupRunRetentionSkipStatuses = []string{"PENDING", "RUNNING", "STOP_REQUESTED", "PENDING_PREPARE"}

// backupArtifactDirRef 描述一次备份在存储目标上的批次目录（存储目标 ID + 相对目录）。
type backupArtifactDirRef struct {
	StorageTargetID uint64
	RelDir          string
}

// cleanupExpiredBackupRuns 按执行记录自身的 retention_days 清理过期执行记录，
// 并连同该 run 关联的备份集、存储目标上的备份产物/日志/参数文件一起清理。
//
// 过期基准取「结束时间 → 开始时间 → 创建时间」中最早可用值，
// 与 backup_sets.expire_at（写入 retention_days 天后过期）保持一致，
// 即「保存时间设为 7 天」时，执行记录与备份集都会在 7 天后被自动清理。
func cleanupExpiredBackupRuns() {
	if global.App.DB == nil {
		return
	}
	var runs []backupModels.InsightBackupRuns
	if err := global.App.DB.
		Where("status NOT IN ?", backupRunRetentionSkipStatuses).
		Where("retention_days >= 1").
		Where("COALESCE(end_at, start_at, created_at) IS NOT NULL").
		Where("COALESCE(end_at, start_at, created_at) <= DATE_SUB(NOW(), INTERVAL retention_days DAY)").
		Order("id asc").
		Limit(200).
		Find(&runs).Error; err != nil {
		global.App.Log.Warnf("load expired backup runs failed: %v", err)
		return
	}
	if len(runs) == 0 {
		return
	}
	cleaned := 0
	for i := range runs {
		if err := DeleteBackupRunCascade(&runs[i]); err != nil {
			global.App.Log.Warnf("delete expired backup run failed: %v, run_id: %d", err, runs[i].ID)
			continue
		}
		cleaned++
	}
	if cleaned > 0 {
		global.App.Log.Infof("cleanup expired backup runs success, rows=%d", cleaned)
	}
}

// DeleteBackupRunCascade 删除执行记录，并级联清理其关联的备份集记录与存储目标上的
// 全部关联文件（备份产物、日志、参数/元数据配置等），确保不残留。
// 仍在执行的记录一律拒绝，避免删掉正在写入的产物。
func DeleteBackupRunCascade(run *backupModels.InsightBackupRuns) error {
	if run == nil || run.ID == 0 {
		return nil
	}
	if !isBackupRunRetentionEligible(string(run.Status)) {
		return fmt.Errorf("执行记录仍在进行中，暂不支持清理")
	}
	// 1) 关联备份集：先删备份集，其内部会清理批次目录与记录中的文件路径
	var sets []backupModels.InsightBackupSets
	if err := global.App.DB.Select("id").Where("run_id = ?", run.ID).Find(&sets).Error; err != nil {
		return err
	}
	for _, set := range sets {
		if err := DeleteBackupSetCascade(set.ID); err != nil {
			// 单个备份集清理失败不阻断执行记录清理，避免留下无法删除的脏记录
			global.App.Log.Warnf("cascade delete backup set failed: %v, set_id: %d, run_id: %d", err, set.ID, run.ID)
		}
	}
	// 2) 执行记录自身的产出：失败/停止的执行也会留下批次目录、日志与参数文件
	cleanupBackupRunArtifacts(run)
	// 3) 删除执行记录
	return global.App.DB.Delete(&backupModels.InsightBackupRuns{}, run.ID).Error
}

// isBackupRunRetentionEligible 判断该状态是否已收口，可以进入保留期清理。
func isBackupRunRetentionEligible(status string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	for _, skip := range backupRunRetentionSkipStatuses {
		if normalized == skip {
			return false
		}
	}
	return true
}

// cleanupBackupRunArtifacts 清理单条执行记录在存储目标与本机产生的批次文件。
func cleanupBackupRunArtifacts(run *backupModels.InsightBackupRuns) {
	if run == nil || run.ID == 0 {
		return
	}
	recorded := collectBackupRunArtifactPaths(run.ID)
	// 1) 以记录中真实上报的 st: 产物/日志路径为准，整体删除批次目录
	removeArtifactDirsFromStoragePaths(recorded)
	// 2) 兜底：按当前命名规则重建批次目录（覆盖记录路径缺失的历史数据）
	if ref := buildBackupRunArtifactDirRef(run); ref != nil {
		removeArtifactDirOnStorageTargetByID(ref.StorageTargetID, ref.RelDir)
	}
	// 3) 兜底：清理记录中直接指向的本地文件（历史本地模式数据）
	for _, candidate := range recorded {
		removeLocalFileIfSafe(candidate)
	}
}

// collectBackupRunArtifactPaths 读取执行记录中记录的产物/日志路径。
// storage_artifact_path 列为运行期上报字段、未映射进 GORM 模型，故统一按列名查询。
func collectBackupRunArtifactPaths(runID uint64) []string {
	if runID == 0 {
		return nil
	}
	var row struct {
		FilePath            string
		StorageArtifactPath string
		LogPath             string
	}
	if err := global.App.DB.Table("insight_backup_runs").
		Select("file_path, storage_artifact_path, log_path").
		Where("id = ?", runID).
		Take(&row).Error; err != nil {
		return nil
	}
	return []string{row.FilePath, row.StorageArtifactPath, row.LogPath}
}

// buildBackupRunArtifactDirRef 按当前批次目录命名规则重建该执行记录的批次目录。
// 仅作兜底：新规则（RunSeq>0）用「数据源业务 ID / 序号-runID」，历史数据沿用数字命名。
func buildBackupRunArtifactDirRef(run *backupModels.InsightBackupRuns) *backupArtifactDirRef {
	if run == nil || run.StorageTargetID == 0 || run.ID == 0 {
		return nil
	}
	spec := BackupRunDirSpec{
		SourceInstanceID: run.SourceInstanceID,
		TaskID:           run.TaskID,
		RunID:            run.ID,
		RunSeq:           run.RunSeq,
	}
	if run.RunSeq > 0 {
		spec.InstanceBizID = resolveInstanceBizID(run.SourceInstanceID)
	}
	return &backupArtifactDirRef{StorageTargetID: run.StorageTargetID, RelDir: spec.RelDir()}
}
