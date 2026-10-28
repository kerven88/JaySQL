package services

import (
	"fmt"
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

// 备份执行触发来源。
//
// 两条路径的备份类型判定规则不同，必须在创建执行记录时就明确区分并落库，
// 否则运行记录无法解释「为什么这次是全量 / 增量」。
const (
	// backupTriggerManual 手动触发：用户在任务列表或详情页点击「立即备份」。
	// 物理备份任务在手动触发时**一律执行全量**，不进入任何基线判断流程。
	backupTriggerManual = "MANUAL"
	// backupTriggerScheduled 定时触发：由时间计划（调度器）触发。
	// 物理备份任务在定时触发时必须先探测有效物理基线，有基线走增量、无基线降级全量。
	backupTriggerScheduled = "SCHEDULED"
)

// normalizeBackupTriggerSource 归一化触发来源。
// 空值（历史数据、或调用方未显式传入）按 SCHEDULED 兼容处理 —— 升级前创建的记录都来自调度或走旧逻辑，
// 保持旧语义比引入一个未知分支安全。
func normalizeBackupTriggerSource(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case backupTriggerManual:
		return backupTriggerManual
	case backupTriggerScheduled:
		return backupTriggerScheduled
	default:
		return backupTriggerScheduled
	}
}

func isBackupTriggerManual(raw string) bool {
	return normalizeBackupTriggerSource(raw) == backupTriggerManual
}

// triggerSourceLabel 返回中文触发来源，用于前端与日志展示。
func triggerSourceLabel(raw string) string {
	if isBackupTriggerManual(raw) {
		return "手动触发"
	}
	return "定时触发"
}

// resolveRunTriggerMode 解析运行记录的触发方式，供前端「触发方式」列展示。
//
// 优先使用落库的真实来源；迁移回填前历史记录的 trigger_source 为空，
// 此时回落到 deriveTriggerMode 的旧推断（created_by=system 视为定时）。
// 旧推断不可靠的来源在于：手动执行但取不到当前用户名时也会 fallback 成 "system"，
// 会被误标成定时 —— 落库后这类记录可自证。
func resolveRunTriggerMode(triggerSource string, createdBy string) string {
	if src := strings.TrimSpace(triggerSource); src != "" {
		return normalizeBackupTriggerSource(src)
	}
	return deriveTriggerMode(createdBy)
}

// lookupInstanceBizID 取实例业务 ID（ds-xxxx），用于拼出与执行侧完全一致的批次目录。
// 查不到时返回空串，BackupRunDirSpec 会自动回落数字主键，与历史目录规则保持兼容。
func lookupInstanceBizID(sourceInstanceID uint64) string {
	if sourceInstanceID == 0 || global.App.DB == nil {
		return ""
	}
	var bizID string
	row := global.App.DB.Model(&commonModels.InsightInstances{}).
		Where("id = ?", sourceInstanceID).
		Select("instance_id").
		Row()
	if row == nil {
		return ""
	}
	if err := row.Scan(&bizID); err != nil {
		return ""
	}
	return strings.TrimSpace(bizID)
}

// defaultPhysicalBaseRetentionDays 基线有效期默认值，与任务默认保留天数一致。
const defaultPhysicalBaseRetentionDays = 30

// physicalBaseProbeResult 定时触发前的物理基线探测结果。
type physicalBaseProbeResult struct {
	// Available 是否存在可直接用于增量备份的有效基线。
	Available bool
	// Reason 不可用时的人类可读原因（中文，直接进日志与执行记录提示）。
	Reason string
	// Base 命中有效基线时的基线详情。
	Base *mysqlPhysicalBackupBase
}

// isMySQLPhysicalBackupTask 判断任务是否为 MySQL 物理备份（XtraBackup）。
// 只有物理备份才有 LSN 基线概念；逻辑备份与 PG_BACKREST 走各自原有的策略判定，不受本次改造影响。
func isMySQLPhysicalBackupTask(task *backupModels.InsightBackupTasks) bool {
	if task == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(string(task.DriverType)), "XTRABACKUP") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(task.DBType)), "MySQL")
}

// probeMySQLPhysicalBaseForTask 探测某个物理备份任务当前是否存在有效基线。
//
// 覆盖的异常场景：
//   - 基线缺失：任务从未成功执行过，任务目录下没有 backup.meta.json；
//   - 基线被清理：批次目录随备份集过期清理一起被删除，manifest 一并消失；
//   - 基线过期：manifest 存在但创建时间已超过任务保留天数（retention_days）；
//   - 基线损坏：manifest 存在但 JSON 不可解析，或 to_lsn 为空（LSN 链断裂）；
//   - 基线不可用：存储目标类型无对应读取链路、SSH 不通、对象存储列举失败、凭据解密失败。
//
// 探测失败**不返回 error**：调用方的正确处置是「视为无基线 → 降级全量」，而不是让整次备份失败。
func probeMySQLPhysicalBaseForTask(task *backupModels.InsightBackupTasks) physicalBaseProbeResult {
	if task == nil {
		return physicalBaseProbeResult{Available: false, Reason: "备份任务为空"}
	}
	if task.StorageTargetID <= 0 {
		return physicalBaseProbeResult{Available: false, Reason: "任务未绑定存储目标，无法定位物理基线仓库"}
	}
	target, err := loadStorageTargetByID(task.StorageTargetID)
	if err != nil || target == nil {
		return physicalBaseProbeResult{Available: false, Reason: fmt.Sprintf("加载存储目标失败: %v", err)}
	}
	targetType := strings.TrimSpace(string(target.Type))
	// 两类仓库都能做物理基线：LOCAL_SERVER 走 SSH，对象存储走 LIST+GET。
	// 其余类型（未知的、未来新增的）没有对应读取链路，只能按"无基线"处理。
	if !strings.EqualFold(targetType, backupModels.StorageTargetTypeLocalServer) && !IsObjectStorageType(targetType) {
		return physicalBaseProbeResult{
			Available: false,
			Reason:    fmt.Sprintf("存储目标「%s」类型为 %s，物理增量基线当前仅支持 LOCAL_SERVER 与对象存储类型仓库", strings.TrimSpace(target.Name), targetType),
		}
	}
	sshPassword, sshPrivateKey, s3AK, s3SK, decryptErr := decryptStorageTargetCredentials(target)
	if decryptErr != nil {
		return physicalBaseProbeResult{Available: false, Reason: fmt.Sprintf("解密存储目标凭据失败: %v", decryptErr)}
	}
	execTarget := &BackupExecutionStorageTarget{
		ID:               target.ID,
		Type:             targetType,
		Host:             target.Host,
		Port:             target.Port,
		BasePath:         target.BasePath,
		SSHUser:          target.SSHUser,
		SSHPassword:      sshPassword,
		SSHPrivateKey:    sshPrivateKey,
		S3Endpoint:       strings.TrimSpace(target.S3Endpoint),
		S3Region:         strings.TrimSpace(target.S3Region),
		S3Bucket:         strings.TrimSpace(target.S3Bucket),
		S3AccessKey:      strings.TrimSpace(s3AK),
		S3SecretKey:      s3SK,
		S3ForcePathStyle: target.S3ForcePathStyle,
	}
	if cfgErr := validateStorageTargetForPhysicalBase(execTarget); cfgErr != nil {
		return physicalBaseProbeResult{Available: false, Reason: fmt.Sprintf("存储目标配置不完整: %v", cfgErr)}
	}
	spec := BackupRunDirSpec{
		SourceInstanceID: task.SourceInstanceID,
		InstanceBizID:    lookupInstanceBizID(task.SourceInstanceID),
		TaskID:           task.TaskID,
	}
	base, probeErr := findLatestMySQLPhysicalBase(execTarget, spec)
	if probeErr != nil {
		// 网络抖动是基线探测最常见的失败原因（SSH 连不上 / 对象存储 LIST 超时），
		// 重试一次再下结论，避免一次抖动就把本可以走增量的执行误降级成全量。
		time.Sleep(2 * time.Second)
		base, probeErr = findLatestMySQLPhysicalBase(execTarget, spec)
	}
	if probeErr != nil {
		return physicalBaseProbeResult{Available: false, Reason: fmt.Sprintf("读取物理基线失败（将降级为全量）: %v", probeErr)}
	}
	if base == nil || strings.TrimSpace(base.ToLSN) == "" {
		return physicalBaseProbeResult{Available: false, Reason: describeMissingPhysicalBase(base)}
	}
	if expired, detail := isPhysicalBaseExpired(base, task.RetentionDays); expired {
		return physicalBaseProbeResult{Available: false, Reason: detail}
	}
	return physicalBaseProbeResult{Available: true, Base: base}
}

// describeMissingPhysicalBase 区分「从未备份 / 基线被清理」与「基线损坏」，给出不同提示。
func describeMissingPhysicalBase(base *mysqlPhysicalBackupBase) string {
	if base == nil {
		return "未找到任何物理基线（首次执行或基线已被清理）"
	}
	if base.ScannedCount == 0 {
		return "任务目录下未找到 backup.meta.json（首次执行或基线已被清理）"
	}
	if base.InvalidCount >= base.ScannedCount {
		return fmt.Sprintf("扫描到 %d 个基线 manifest 但全部无效（to_lsn 缺失或文件损坏），LSN 链已断裂", base.ScannedCount)
	}
	return fmt.Sprintf("扫描到 %d 个基线 manifest，但未找到可用的 LSN 终点", base.ScannedCount)
}

// isPhysicalBaseExpired 判定基线是否过期。
// 基线产物与备份集同生命周期，因此沿用任务的保留天数（retention_days）作为有效期：
// 超过该天数后备份集大概率已被清理，即使 manifest 侥幸残留也不应再作为增量基线。
// manifest 时间缺失或无法解析时不判过期（宽容处理，避免误伤历史数据）。
func isPhysicalBaseExpired(base *mysqlPhysicalBackupBase, retentionDays int) (bool, string) {
	created := strings.TrimSpace(base.CreatedAt)
	if created == "" {
		return false, ""
	}
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return false, ""
	}
	days := retentionDays
	if days <= 0 {
		days = defaultPhysicalBaseRetentionDays
	}
	if time.Since(createdAt) <= time.Duration(days)*24*time.Hour {
		return false, ""
	}
	return true, fmt.Sprintf("基线创建于 %s，已超过保留天数 %d 天，视为过期", createdAt.Format("2006-01-02 15:04:05"), days)
}
