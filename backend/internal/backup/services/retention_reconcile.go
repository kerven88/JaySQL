package services

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/gorm"
)

// 保留期「快照 → 回溯对齐」机制说明
//
// 保留期在创建执行记录（insight_backup_runs.retention_days）与备份集
// （insight_backup_sets.expire_at = created_at + N 天）时被快照写入，因此历史上
// 修改任务保留期只影响之后新建的备份。本文件提供两件事：
//
//  1. EvaluateTaskRetentionImpact：在保存前算出「对齐到目标保留期后会有多少备份
//     立即消失」，供界面显式确认，避免静默删除；
//  2. ReconcileTaskRetention：保存后把该任务下所有执行记录与备份集的保留期
//     统一对齐到任务当前值。
//
// 两个清理器（cleanupExpiredBackupRuns / cleanupExpiredBackupSets）的判定基准在这里
// 必须保持一致：执行记录看 COALESCE(end_at, start_at, created_at) + N 天，
// 备份集看 created_at + N 天。

// RetentionReconcileImpact 描述把任务保留期对齐到目标天数后立即造成的删除影响。
type RetentionReconcileImpact struct {
	RequiresConfirm bool     `json:"requires_confirm"`
	TaskID          string   `json:"task_id"`
	CurrentDays     int      `json:"current_days"`
	TargetDays      int      `json:"target_days"`
	RunCount        int64    `json:"run_count"`
	SetCount        int64    `json:"set_count"`
	RunningRunCount int64    `json:"running_run_count"`
	SizeBytes       int64    `json:"size_bytes"`
	OldestAt        string   `json:"oldest_at"`
	LatestAt        string   `json:"latest_at"`
	SampleNames     []string `json:"sample_names"`
}

// RetentionReconcileRequiredError 表示本次保存会把已有备份立即清理，需前端显式确认。
// 作为类型化错误返回，让视图层能把影响明细一并下发给前端弹确认框。
type RetentionReconcileRequiredError struct {
	Impact *RetentionReconcileImpact
}

func (e *RetentionReconcileRequiredError) Error() string {
	if e == nil || e.Impact == nil {
		return "本次保存会立即清理已有备份，请确认后重试"
	}
	impact := e.Impact
	return fmt.Sprintf(
		"保存时间改为 %d 天后，%d 条执行记录与 %d 个备份集已超出保留期，保存后将立即清理，请确认后重试",
		impact.TargetDays, impact.RunCount, impact.SetCount,
	)
}

// AsRetentionReconcileRequiredError 从错误链中提取需确认错误。
func AsRetentionReconcileRequiredError(err error) (*RetentionReconcileRequiredError, bool) {
	var target *RetentionReconcileRequiredError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// EvaluateTaskRetentionImpact 计算把任务保留期对齐到 targetDays 后立即会消失的备份。
//
// 只统计「对齐后立刻过期」的部分：这些记录当前尚未到期，仅因保留期被改短而进入
// 清理范围，因此必须让用户显式确认。已经把 expire_at 走到过去的记录不计入，
// 它们本来就会被清理器收走。
func EvaluateTaskRetentionImpact(taskID string, targetDays int) (*RetentionReconcileImpact, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库不可用")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("备份任务ID不能为空")
	}
	target := normalizeRetentionDays(targetDays)

	var task backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("备份任务不存在")
		}
		return nil, err
	}

	impact := &RetentionReconcileImpact{
		TaskID:      taskID,
		CurrentDays: task.RetentionDays,
		TargetDays:  target,
	}
	// target 来自 normalizeRetentionDays 的白名单，拼接进 INTERVAL 无注入风险
	interval := strconv.Itoa(target)

	var runAgg struct {
		Total    int64
		SizeSum  int64
		OldestAt *time.Time
		LatestAt *time.Time
	}
	if err := global.App.DB.Table("insight_backup_runs").
		Select("COUNT(*) AS total, COALESCE(SUM(size_bytes), 0) AS size_sum, "+
			"MIN(COALESCE(end_at, start_at, created_at)) AS oldest_at, "+
			"MAX(COALESCE(end_at, start_at, created_at)) AS latest_at").
		Where("task_id = ?", taskID).
		Where("status NOT IN ?", backupRunRetentionSkipStatuses).
		Where("COALESCE(end_at, start_at, created_at) <= DATE_SUB(NOW(), INTERVAL "+interval+" DAY)").
		Scan(&runAgg).Error; err != nil {
		return nil, err
	}
	impact.RunCount = runAgg.Total
	impact.SizeBytes = runAgg.SizeSum

	var setAgg struct {
		Total    int64
		SizeSum  int64
		OldestAt *time.Time
		LatestAt *time.Time
	}
	if err := global.App.DB.Table("insight_backup_sets").
		Select("COUNT(*) AS total, COALESCE(SUM(size_bytes), 0) AS size_sum, "+
			"MIN(created_at) AS oldest_at, MAX(created_at) AS latest_at").
		Where("task_id = ?", taskID).
		Where("(expire_at IS NULL OR expire_at > NOW())").
		Where("created_at <= DATE_SUB(NOW(), INTERVAL "+interval+" DAY)").
		Scan(&setAgg).Error; err != nil {
		return nil, err
	}
	impact.SetCount = setAgg.Total
	impact.SizeBytes += setAgg.SizeSum

	impact.OldestAt = formatImpactTime(earlierTime(runAgg.OldestAt, setAgg.OldestAt))
	impact.LatestAt = formatImpactTime(laterTime(runAgg.LatestAt, setAgg.LatestAt))

	if impact.SetCount > 0 {
		var names []string
		if err := global.App.DB.Table("insight_backup_sets").
			Select("name").
			Where("task_id = ?", taskID).
			Where("(expire_at IS NULL OR expire_at > NOW())").
			Where("created_at <= DATE_SUB(NOW(), INTERVAL "+interval+" DAY)").
			Order("created_at asc").
			Limit(5).
			Pluck("name", &names).Error; err == nil {
			impact.SampleNames = names
		}
	}

	// 正在执行、不参与保留期清理的记录数量，用于界面上说明「有备份正在跑，不受影响」
	if err := global.App.DB.Table("insight_backup_runs").
		Where("task_id = ?", taskID).
		Where("status IN ?", backupRunRetentionSkipStatuses).
		Count(&impact.RunningRunCount).Error; err != nil {
		return nil, err
	}

	impact.RequiresConfirm = impact.RunCount > 0 || impact.SetCount > 0
	return impact, nil
}

// ReconcileTaskRetention 把任务下所有执行记录与备份集的保留期对齐到 targetDays，
// 返回被更新的执行记录数与备份集数。
//
// 覆盖范围包含正在执行的记录：清理入口本身有状态护栏（进行中的记录不参与清理），
// 改写它们的 retention_days 不会影响正在写入的产物，同时能让「任务保留期」在全
// 任务范围内保持同一口径。备份集到期时间统一按 created_at + N 天重算，与创建时
// 的原始口径一致。
func ReconcileTaskRetention(taskID string, targetDays int) (int64, int64, error) {
	if global.App.DB == nil {
		return 0, 0, fmt.Errorf("数据库不可用")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return 0, 0, nil
	}
	target := normalizeRetentionDays(targetDays)
	interval := strconv.Itoa(target)
	now := time.Now()

	var runsUpdated, setsUpdated int64
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		runRes := tx.Exec(
			"UPDATE insight_backup_runs SET retention_days = ?, updated_at = ? WHERE task_id = ? AND retention_days <> ?",
			target, now, taskID, target,
		)
		if runRes.Error != nil {
			return runRes.Error
		}
		runsUpdated = runRes.RowsAffected

		setRes := tx.Exec(
			"UPDATE insight_backup_sets SET retention_days = ?, "+
				"expire_at = DATE_ADD(created_at, INTERVAL "+interval+" DAY), updated_at = ? "+
				"WHERE task_id = ? AND (retention_days <> ? OR expire_at IS NULL "+
				"OR expire_at <> DATE_ADD(created_at, INTERVAL "+interval+" DAY))",
			target, now, taskID, target,
		)
		if setRes.Error != nil {
			return setRes.Error
		}
		setsUpdated = setRes.RowsAffected
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return runsUpdated, setsUpdated, nil
}

func earlierTime(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.Before(*b):
		return a
	default:
		return b
	}
}

func laterTime(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.After(*b):
		return a
	default:
		return b
	}
}

func formatImpactTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("2006-01-02 15:04:05")
}
