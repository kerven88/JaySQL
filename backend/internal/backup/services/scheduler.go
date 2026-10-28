package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const (
	backupStrategyModeSingle = "SINGLE"
	backupStrategyModeMixed  = "MIXED"
)

type backupMixedStrategy struct {
	StrategyMode              string   `json:"strategy_mode"`
	FullBackupDays            []string `json:"full_backup_days"`
	OtherDaysType             string   `json:"other_days_type"`
	FallbackToFullWhenMissing bool     `json:"fallback_to_full_when_missing"`
}

func parseBackupMixedStrategy(driverConfig []byte) backupMixedStrategy {
	strategy := backupMixedStrategy{
		StrategyMode:              backupStrategyModeSingle,
		FullBackupDays:            nil,
		OtherDaysType:             "INCR",
		FallbackToFullWhenMissing: true,
	}
	if len(driverConfig) == 0 {
		return strategy
	}
	raw := map[string]any{}
	if err := json.Unmarshal(driverConfig, &raw); err != nil {
		return strategy
	}
	if mode, ok := raw["strategy_mode"].(string); ok && mode != "" {
		strategy.StrategyMode = strings.ToUpper(strings.TrimSpace(mode))
	}
	if days, ok := raw["full_backup_days"].([]any); ok && len(days) > 0 {
		parsed := make([]string, 0, len(days))
		for _, d := range days {
			text := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", d)))
			if text != "" {
				parsed = append(parsed, text)
			}
		}
		if len(parsed) > 0 {
			strategy.FullBackupDays = parsed
		}
	}
	if other, ok := raw["other_days_type"].(string); ok && other != "" {
		strategy.OtherDaysType = strings.ToUpper(strings.TrimSpace(other))
	}
	if fallback, ok := raw["fallback_to_full_when_missing"].(bool); ok {
		strategy.FallbackToFullWhenMissing = fallback
	}
	return strategy
}

func currentWeekdayLabel() string {
	weekday := time.Now().Weekday()
	switch weekday {
	case time.Monday:
		return "MON"
	case time.Tuesday:
		return "TUE"
	case time.Wednesday:
		return "WED"
	case time.Thursday:
		return "THU"
	case time.Friday:
		return "FRI"
	case time.Saturday:
		return "SAT"
	default:
		return "SUN"
	}
}

// findLatestSuccessfulFullBackupTypeForInstance 判断该实例上是否存在「同驱动」的成功全量备份。
//
// driverType 必须参与过滤：物理备份（XTRABACKUP）的增量依赖 xtrabackup 产物的 LSN 链，
// 一条逻辑备份（LOGICAL）的全量并不能作为物理增量的基线。早期版本漏了这个维度，
// 导致「实例上有逻辑全量 → 误判有基线 → 强行跑物理增量 → 读基线失败」。
func findLatestSuccessfulFullBackupTypeForInstance(sourceInstanceID uint64, driverType string) (bool, error) {
	if sourceInstanceID == 0 {
		return false, nil
	}
	var count int64
	tx := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("source_instance_id = ? AND status = ? AND backup_type IN ?", sourceInstanceID, "SUCCESS", []string{"FULL"})
	if dt := strings.ToUpper(strings.TrimSpace(driverType)); dt != "" {
		tx = tx.Where("driver_type = ?", dt)
	}
	err := tx.Order("id desc").
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// resolveEffectiveBackupType 决定本次执行实际使用的备份类型。
//
// 两条触发路径规则不同（常量与判定见 backup_trigger.go）：
//   - 手动触发（MANUAL）的物理备份任务：一律 FULL，**不进入任何基线判断流程**；
//   - 定时触发（SCHEDULED）的物理备份任务：先探测有效物理基线，有 → INCR，无 → 自动降级 FULL。
//
// 逻辑备份与 PG_BACKREST 不受本次改造影响，仍走原有的星期混合策略。
func resolveEffectiveBackupType(task *backupModels.InsightBackupTasks, defaultBackupType string, triggerSource string) (string, string, error) {
	baseType := strings.ToUpper(strings.TrimSpace(defaultBackupType))
	if baseType == "" {
		baseType = "FULL"
	}
	if isMySQLPhysicalBackupTask(task) {
		if isBackupTriggerManual(triggerSource) {
			return "FULL", "手动触发：物理备份任务强制执行全量备份，不进入基线判断流程", nil
		}
		return resolveScheduledMySQLPhysicalBackupType(task, baseType)
	}
	strategy := parseBackupMixedStrategy(task.DriverConfig)
	strategyNote := ""
	if strategy.StrategyMode != backupStrategyModeMixed {
		return baseType, strategyNote, nil
	}
	today := currentWeekdayLabel()
	fullDaySet := map[string]struct{}{}
	for _, d := range strategy.FullBackupDays {
		fullDaySet[d] = struct{}{}
	}
	_, isFullDay := fullDaySet[today]
	if isFullDay || len(strategy.FullBackupDays) == 0 {
		if isFullDay {
			strategyNote = fmt.Sprintf("混合策略命中：今天(%s)为全量日，执行 FULL 备份", today)
		} else {
			strategyNote = "混合策略已开启但未配置全量日，默认执行 FULL 备份"
		}
		return "FULL", strategyNote, nil
	}
	otherType := strings.ToUpper(strings.TrimSpace(strategy.OtherDaysType))
	if otherType == "" {
		otherType = "INCR"
	}
	switch otherType {
	case "INCR", "DIFF":
	default:
		otherType = "INCR"
	}
	if strategy.FallbackToFullWhenMissing {
		// driverType 必须参与过滤：逻辑全量不能充当物理备份的基线（历史缺陷）。
		hasFull, lookupErr := findLatestSuccessfulFullBackupTypeForInstance(task.SourceInstanceID, string(task.DriverType))
		if lookupErr == nil && !hasFull {
			strategyNote = fmt.Sprintf("混合策略：今天(%s)为非全量日，但仓库内无可用的 %s FULL 备份，智能降级为 FULL 备份",
				today, strings.ToUpper(strings.TrimSpace(string(task.DriverType))))
			return "FULL", strategyNote, nil
		}
	}
	strategyNote = fmt.Sprintf("混合策略命中：今天(%s)为非全量日，执行 %s 备份，最近一次 FULL 作为基准", today, otherType)
	return otherType, strategyNote, nil
}

// resolveScheduledMySQLPhysicalBackupType 定时触发时的物理备份类型判定。
//
// 本次若本就要跑 FULL（混合策略的全量日、或单策略且任务口径为 FULL），直接走全量：
// FULL 不依赖基线，且执行完会刷新出新的基线，没必要先探一次仓库。
// 只有本次可能跑增量时才探测基线：有有效基线 → INCR；无有效基线 → 降级 FULL。
func resolveScheduledMySQLPhysicalBackupType(task *backupModels.InsightBackupTasks, baseType string) (string, string, error) {
	strategy := parseBackupMixedStrategy(task.DriverConfig)
	if strategy.StrategyMode != backupStrategyModeMixed {
		if baseType == "FULL" {
			return "FULL", "定时触发：任务为单策略全量，执行 FULL 备份", nil
		}
		return resolveScheduledPhysicalIncremental(task, baseType, strategy, "")
	}
	today := currentWeekdayLabel()
	fullDaySet := map[string]struct{}{}
	for _, d := range strategy.FullBackupDays {
		fullDaySet[d] = struct{}{}
	}
	_, isFullDay := fullDaySet[today]
	if isFullDay || len(strategy.FullBackupDays) == 0 {
		if isFullDay {
			return "FULL", fmt.Sprintf("定时触发：混合策略命中，今天(%s)为全量日，执行 FULL 备份", today), nil
		}
		return "FULL", "定时触发：混合策略已开启但未配置全量日，执行 FULL 备份", nil
	}
	return resolveScheduledPhysicalIncremental(task, "INCR", strategy, today)
}

// resolveScheduledPhysicalIncremental 定时触发且本次要走增量时的基线前置检查。
// 基线不可用即降级为全量（fallback_to_full_when_missing=false 时按配置直接报错，不降级）。
func resolveScheduledPhysicalIncremental(task *backupModels.InsightBackupTasks, incrType string, strategy backupMixedStrategy, today string) (string, string, error) {
	probe := probeMySQLPhysicalBaseForTask(task)
	if probe.Available {
		if today != "" {
			return incrType, fmt.Sprintf("定时触发：今天(%s)为非全量日，已检测到有效物理基线（to_lsn=%s），执行增量备份", today, strings.TrimSpace(probe.Base.ToLSN)), nil
		}
		return incrType, fmt.Sprintf("定时触发：已检测到有效物理基线（to_lsn=%s），执行增量备份", strings.TrimSpace(probe.Base.ToLSN)), nil
	}
	if !strategy.FallbackToFullWhenMissing {
		return incrType, "", fmt.Errorf("定时触发的物理增量备份无法继续：%s，且任务未开启「基线缺失时自动降级为全量」", probe.Reason)
	}
	return "FULL", fmt.Sprintf("定时触发：%s，已自动降级为全量备份（本次执行将生成新的物理基线）", probe.Reason), nil
}

var backupScheduleRegistry = struct {
	sync.Mutex
	entries    map[string]cron.EntryID
	signatures map[string]string
}{
	entries:    map[string]cron.EntryID{},
	signatures: map[string]string{},
}

func RegisterBackupTaskCronJobs() {
	if global.App.Cron == nil {
		return
	}
	if _, err := global.App.Cron.AddFunc("@every 1m", func() {
		syncBackupTaskSchedules()
	}); err != nil {
		global.App.Log.Warnf("register backup task sync cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc("@every 1h", func() {
		cleanupExpiredBackupSets()
	}); err != nil {
		global.App.Log.Warnf("register backup set cleanup cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc("@every 1h", func() {
		cleanupExpiredBackupRuns()
	}); err != nil {
		global.App.Log.Warnf("register backup run cleanup cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc("@every 30s", func() {
		rows, cleanupErr := cleanupStaleStopRequestedRuns()
		if cleanupErr != nil {
			global.App.Log.Warnf("cleanup stale stop requested runs failed: %v", cleanupErr)
			return
		}
		if rows > 0 {
			global.App.Log.Infof("cleanup stale stop requested runs success, rows=%d", rows)
		}
	}); err != nil {
		global.App.Log.Warnf("register stale stop request cleanup cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc("@every 30s", func() {
		rows, cleanupErr := cleanupStalePendingPrepareRuns()
		if cleanupErr != nil {
			global.App.Log.Warnf("cleanup stale pending prepare runs failed: %v", cleanupErr)
			return
		}
		if rows > 0 {
			global.App.Log.Infof("cleanup stale pending prepare runs success, rows=%d", rows)
		}
	}); err != nil {
		global.App.Log.Warnf("register stale pending prepare cleanup cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc("@every 30s", func() {
		rows, recoverErr := recoverStrandedPendingPrepareRuns()
		if recoverErr != nil {
			global.App.Log.Warnf("recover stranded pending prepare runs failed: %v", recoverErr)
			return
		}
		if rows > 0 {
			global.App.Log.Infof("recover stranded pending prepare runs success, rows=%d", rows)
		}
	}); err != nil {
		global.App.Log.Warnf("register stranded pending prepare recovery cron failed: %v", err)
	}
	go syncBackupTaskSchedules()
	go cleanupExpiredBackupSets()
	go cleanupExpiredBackupRuns()
	go func() {
		rows, cleanupErr := cleanupStaleStopRequestedRuns()
		if cleanupErr != nil {
			global.App.Log.Warnf("startup cleanup stale stop requested runs failed: %v", cleanupErr)
			return
		}
		if rows > 0 {
			global.App.Log.Infof("startup cleanup stale stop requested runs success, rows=%d", rows)
		}
	}()
	go func() {
		rows, cleanupErr := cleanupStalePendingPrepareRuns()
		if cleanupErr != nil {
			global.App.Log.Warnf("startup cleanup stale pending prepare runs failed: %v", cleanupErr)
			return
		}
		if rows > 0 {
			global.App.Log.Infof("startup cleanup stale pending prepare runs success, rows=%d", rows)
		}
	}()
}

func SyncBackupTaskSchedulesAsync() {
	if global.App.Cron == nil {
		return
	}
	go syncBackupTaskSchedules()
}

func syncBackupTaskSchedules() {
	if global.App.DB == nil || global.App.Cron == nil {
		return
	}
	type taskRow struct {
		TaskID       string
		Enabled      bool
		ScheduleDays []byte
		ScheduleTime string
	}
	var tasks []taskRow
	if err := global.App.DB.Model(&backupModels.InsightBackupTasks{}).
		Select("task_id, enabled, schedule_days, schedule_time").
		Where("deleted_at IS NULL").
		Find(&tasks).Error; err != nil {
		global.App.Log.Warnf("load backup task schedules failed: %v", err)
		return
	}

	backupScheduleRegistry.Lock()
	defer backupScheduleRegistry.Unlock()

	active := map[string]struct{}{}
	for _, task := range tasks {
		active[task.TaskID] = struct{}{}
		days := parseScheduleDays(task.ScheduleDays)
		spec := buildBackupTaskCronSpec(days, task.ScheduleTime)
		signature := fmt.Sprintf("%t|%s", task.Enabled, spec)
		prevSignature, hasPrev := backupScheduleRegistry.signatures[task.TaskID]
		if !task.Enabled || spec == "" {
			if entryID, ok := backupScheduleRegistry.entries[task.TaskID]; ok {
				global.App.Cron.Remove(entryID)
				delete(backupScheduleRegistry.entries, task.TaskID)
			}
			backupScheduleRegistry.signatures[task.TaskID] = signature
			continue
		}
		if hasPrev && prevSignature == signature {
			continue
		}
		if entryID, ok := backupScheduleRegistry.entries[task.TaskID]; ok {
			global.App.Cron.Remove(entryID)
		}
		taskID := task.TaskID
		entryID, err := global.App.Cron.AddFunc(spec, func() {
			triggerScheduledBackupTask(taskID)
		})
		if err != nil {
			global.App.Log.Warnf("register backup task schedule failed: %v, task_id: %s", err, taskID)
			continue
		}
		backupScheduleRegistry.entries[task.TaskID] = entryID
		backupScheduleRegistry.signatures[task.TaskID] = signature
	}

	for taskID, entryID := range backupScheduleRegistry.entries {
		if _, ok := active[taskID]; ok {
			continue
		}
		global.App.Cron.Remove(entryID)
		delete(backupScheduleRegistry.entries, taskID)
		delete(backupScheduleRegistry.signatures, taskID)
	}
}

func buildBackupTaskCronSpec(days []string, scheduleTime string) string {
	if len(days) == 0 || scheduleTime == "" {
		return ""
	}
	parsed, err := time.Parse("15:04", scheduleTime)
	if err != nil {
		return ""
	}
	dayMap := map[string]string{
		"MON": "1",
		"TUE": "2",
		"WED": "3",
		"THU": "4",
		"FRI": "5",
		"SAT": "6",
		"SUN": "0",
	}
	cronDays := make([]string, 0, len(days))
	for _, day := range days {
		mapped, ok := dayMap[day]
		if !ok {
			continue
		}
		cronDays = append(cronDays, mapped)
	}
	if len(cronDays) == 0 {
		return ""
	}
	return fmt.Sprintf("%d %d * * %s", parsed.Minute(), parsed.Hour(), joinCSV(cronDays))
}

func triggerScheduledBackupTask(taskID string) {
	var task backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ? AND deleted_at IS NULL", taskID).First(&task).Error; err != nil {
		return
	}
	if !task.Enabled {
		return
	}
	if _, err := cleanupStaleStopRequestedRunsForTask(task.TaskID); err != nil {
		global.App.Log.Warnf("cleanup stale stop requested runs before scheduling failed: %v, task_id: %s", err, task.TaskID)
		return
	}
	var activeRuns int64
	if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("task_id = ? AND status IN ?", task.TaskID, []string{"PENDING", "RUNNING", "STOP_REQUESTED"}).
		Count(&activeRuns).Error; err != nil {
		return
	}
	if activeRuns > 0 {
		return
	}
	// 定时调度入口：物理备份任务在此路径下会先探测有效基线再决定全量/增量。
	run, err := createBackupRunForTask(&task, "system", backupTriggerScheduled)
	if err != nil {
		global.App.Log.Warnf("create scheduled backup run failed: %v, task_id: %s", err, task.TaskID)
		return
	}
	if run != nil {
		global.App.Log.Infof("[backup] 定时触发已创建执行记录 task_id=%s run_id=%d backup_type=%s trigger=%s",
			task.TaskID, run.ID, strings.TrimSpace(string(run.BackupType)), backupTriggerScheduled)
		// 记录创建后必须显式驱动 run 级 prepare：PG 物理备份的执行记录初始状态是
		// PENDING_PREPARE，只有服务端 prepare 才会把它推进到 PENDING 交给执行节点。
		// 手动路径由 HTTP 视图驱动，定时路径此前漏了这一步，导致定时 PG 物理备份
		// 永久停在 PENDING_PREPARE 并被 stale 收口成失败（见 DispatchBackupRunPrepare）。
		DispatchBackupRunPrepare(run)
	}
}

func cleanupExpiredBackupSets() {
	if global.App.DB == nil {
		return
	}
	var sets []backupModels.InsightBackupSets
	if err := global.App.DB.Where("expire_at IS NOT NULL AND expire_at <= ?", time.Now()).Limit(200).Find(&sets).Error; err != nil {
		global.App.Log.Warnf("load expired backup sets failed: %v", err)
		return
	}
	cleaned := 0
	for _, set := range sets {
		// 走级联删除：一次覆盖存储目标批次目录（备份产物/日志/参数文件）与备份集记录本身。
		// 关联执行记录由 cleanupExpiredBackupRuns 按 retention_days 单独收口。
		if err := DeleteBackupSetCascade(set.ID); err != nil {
			global.App.Log.Warnf("delete expired backup set failed: %v, set_id: %d", err, set.ID)
			continue
		}
		cleaned++
	}
	if cleaned > 0 {
		global.App.Log.Infof("cleanup expired backup sets success, rows=%d", cleaned)
	}
}

// nextBackupRunSeq 计算该实例的下一个备份序号，用于批次目录的可读化命名。
//
// 只统计新规则写入的执行记录（source_instance_id 非 0），历史数据 run_seq 恒为 0 不参与计数，
// 因此升级后该实例首次备份从 1 开始。序号仅作目录名前缀，目录唯一性由 run_id 保证，
// 并发下即使取到相同序号也不会造成目录覆盖。
// truncateRunNote 把决策说明裁剪到 run.error_message（varchar(2048)）可容纳的长度。
// 基线探测失败时的原因串可能很长，不截断会导致整条执行记录写入失败。
func truncateRunNote(note string, maxRunes int) string {
	text := strings.TrimSpace(note)
	if maxRunes <= 0 || len([]rune(text)) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRunes]) + "…"
}

func nextBackupRunSeq(sourceInstanceID uint64) uint64 {
	if sourceInstanceID == 0 {
		return 0
	}
	row := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("source_instance_id = ?", sourceInstanceID).
		Select("COALESCE(MAX(run_seq), 0)").
		Row()
	if row == nil {
		return 0
	}
	var maxSeq uint64
	if err := row.Scan(&maxSeq); err != nil {
		return 0
	}
	return maxSeq + 1
}

func createBackupRunForTask(task *backupModels.InsightBackupTasks, createdBy string, triggerSource string) (*backupModels.InsightBackupRuns, error) {
	if task == nil {
		return nil, fmt.Errorf("备份任务不能为空")
	}
	trigger := normalizeBackupTriggerSource(triggerSource)
	if task.StorageTargetID <= 0 {
		return nil, fmt.Errorf("任务 %s 未绑定存储目标：请编辑任务重新选择存储目标（本地备份中心机/OSS/COS）后再执行备份", task.TaskID)
	}
	precheckResult, err := runBackupTaskPrecheckForTask(task)
	if err != nil {
		return nil, err
	}
	precheckJSON, err := marshalJSONMapOrNil(precheckResult)
	if err != nil {
		return nil, err
	}
	activeRuns, err := countActiveBackupRuns(task.TaskID)
	if err != nil {
		return nil, err
	}
	if activeRuns > 0 {
		return nil, fmt.Errorf("任务 %s 仍存在活动备份记录，跳过创建新执行", task.TaskID)
	}
	effectiveBackupType, strategyNote, typeErr := resolveEffectiveBackupType(task, string(task.BackupType), trigger)
	if typeErr != nil {
		return nil, typeErr
	}
	if strings.TrimSpace(strategyNote) != "" {
		global.App.Log.Infof("[backup] 备份类型决策 task_id=%s trigger=%s driver=%s effective_type=%s note=%s",
			task.TaskID, trigger, strings.TrimSpace(string(task.DriverType)), effectiveBackupType, strategyNote)
	}
	strategyNote = truncateRunNote(strategyNote, 1900)
	driverConfig := task.DriverConfig
	isPGBackRest := strings.EqualFold(strings.TrimSpace(string(task.DriverType)), "PG_BACKREST")
	initialStatus := "PENDING"
	if isPGBackRest {
		initialStatus = "PENDING_PREPARE"
	}
	runSeq := nextBackupRunSeq(task.SourceInstanceID)
	run := backupModels.InsightBackupRuns{
		Model:            &commonModels.Model{},
		TaskID:           task.TaskID,
		SourceInstanceID: task.SourceInstanceID,
		RunSeq:           runSeq,
		Status:           commonModels.EnumType(initialStatus),
		ExecutorNodeID:   task.ExecutorNodeID,
		RepositoryNodeID: task.RepositoryNodeID,
		BackupType:       commonModels.EnumType(effectiveBackupType),
		ObjectMode:       task.ObjectMode,
		ObjectSpec:       task.ObjectSpec,
		ObjectSummary:    task.ObjectSummary,
		OutputDir:        task.OutputDir,
		DriverType:       task.DriverType,
		DriverConfig:     driverConfig,
		StorageBackend:   task.StorageBackend,
		StorageConfig:    task.StorageConfig,
		StorageTargetID:  task.StorageTargetID,
		RetentionDays:    task.RetentionDays,
		PrecheckSummary:  precheckJSON,
		CreatedBy:        createdBy,
		TriggerSource:    trigger,
		BackupTypeReason: strategyNote,
	}
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		return ensureBackupSetSnapshotTx(tx, task, &run)
	}); err != nil {
		if isPrepareSchemaMissingError(err) && isPGBackRest {
			run.Status = commonModels.EnumType("PENDING")
			if strings.TrimSpace(strategyNote) != "" {
				strategyNote = strategyNote + " | " + fallbackPrepareSchemaHint
			} else {
				strategyNote = fallbackPrepareSchemaHint
			}
			run.BackupTypeReason = truncateRunNote(strategyNote, 1000)
			run.ErrorMessage = fallbackPrepareSchemaHint
			if innerErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
				if ierr := tx.Create(&run).Error; ierr != nil {
					return ierr
				}
				return ensureBackupSetSnapshotTx(tx, task, &run)
			}); innerErr != nil {
				return nil, newPhysicalPrepareError(
					"schema_missing",
					"数据库尚未执行 prepare 系列迁移脚本",
					"请先在 192.168.53.102 MySQL insight 库执行 001_add_prepare_columns.sql 迁移脚本后，再重新点击“立即执行”",
				).WithDetail(fmt.Sprintf("task_id=%s create_err=%v fallback_err=%v", task.TaskID, err, innerErr)).WithRaw(innerErr)
			}
			return &run, nil
		}
		return nil, err
	}
	return &run, nil
}

func preparePGBackRestRunDriverConfig(instance *commonModels.InsightInstances, task *backupModels.InsightBackupTasks, effectiveBackupType string) ([]byte, error) {
	executorNode, err := loadBackupNodeByID(task.ExecutorNodeID)
	if err != nil {
		return nil, err
	}
	if executorNode == nil {
		return nil, newPhysicalPrepareError(
			"executor_node",
			"PG 物理备份执行节点不存在",
			"请编辑该备份任务，重新选择有效的执行节点后再运行；若 host 与数据库主机一致，执行节点会自动匹配。",
		).WithDetail(fmt.Sprintf("task_id=%s executor_node_id=%d", task.TaskID, task.ExecutorNodeID))
	}
	if !strings.EqualFold(strings.TrimSpace(string(executorNode.Status)), "ONLINE") {
		if repairedNode, repairResult := tryAutoRepairBackupNode(nil, executorNode, instance, "PG 物理备份执行节点"); repairResult != nil {
			if repairedNode != nil {
				executorNode = repairedNode
			}
			if strings.EqualFold(strings.TrimSpace(string(executorNode.Status)), "ONLINE") {
				goto executorRecovered
			}
			detail := fmt.Sprintf("executor_node_id=%d host=%s status=%s", executorNode.ID, strings.TrimSpace(executorNode.Host), strings.TrimSpace(string(executorNode.Status)))
			if repairResult.Detail != nil {
				if actions := strings.TrimSpace(fmt.Sprintf("%v", repairResult.Detail["actions"])); actions != "" {
					detail = detail + " auto_repair=" + actions
				}
			}
			return nil, newPhysicalPrepareError(
				"executor_online",
				"PG 物理备份执行节点离线，且系统自动修复/重建后仍未恢复",
				"请先检查目标主机网络、SSH 凭据、server_url 与 Agent 服务状态，再重新点击“立即执行”。",
			).WithHost(strings.TrimSpace(executorNode.Host)).
				WithDetail(detail)
		}
		return nil, newPhysicalPrepareError(
			"executor_online",
			"PG 物理备份执行节点离线",
			"请先恢复执行节点 Agent 在线，再重新点击“立即执行”。",
		).WithHost(strings.TrimSpace(executorNode.Host)).
			WithDetail(fmt.Sprintf("executor_node_id=%d host=%s status=%s", executorNode.ID, strings.TrimSpace(executorNode.Host), strings.TrimSpace(string(executorNode.Status))))
	}
executorRecovered:
	storageTargetType := ""
	var storageTarget *backupModels.InsightBackupStorageTargets
	if task.StorageTargetID > 0 {
		if st, loadErr := loadStorageTargetByID(task.StorageTargetID); loadErr == nil && st != nil {
			storageTarget = st
			storageTargetType = strings.ToUpper(strings.TrimSpace(string(st.Type)))
		}
	}
	isRepoS3Type := storageTargetType == "OSS_ALIYUN" || storageTargetType == "COS_TENCENT" ||
		storageTargetType == "S3_MINIO" || storageTargetType == "OBS_HUAWEI"
	repositoryNode, err := resolvePGBackRestRepositoryNodeForTask(task)
	if err != nil {
		return nil, err
	}
	if !isRepoS3Type {
		if repositoryNode == nil {
			return nil, newPhysicalPrepareError(
				"repository_node",
				"PG 物理备份仓库节点未解析",
				"请确认该任务已选择存储目标（本地备份中心机 LOCAL_SERVER 或 S3 兼容对象存储），且存储目标配置正确。",
			).WithDetail(fmt.Sprintf("task_id=%s storage_target_id=%d repository_node_id=%d", task.TaskID, task.StorageTargetID, task.RepositoryNodeID))
		}
		if err := validatePGBackRestTopology(instance, executorNode, repositoryNode); err != nil {
			return nil, err
		}
	}
	sshUserNow, sshPasswordNow, sshPrivateKeyNow, sshPortNow, sshOkNow, _ := resolveSSHCredentialsFromInstance(instance)
	if sshOkNow {
		hostFromInstance := strings.TrimSpace(instance.Hostname)
		nodeHost := strings.TrimSpace(executorNode.Host)
		if strings.EqualFold(hostFromInstance, nodeHost) {
			needUpdate := false
			updates := map[string]any{
				"updated_at": time.Now(),
			}
			if strings.TrimSpace(executorNode.SSHUser) != sshUserNow {
				updates["ssh_user"] = sshUserNow
				needUpdate = true
			}
			effPort := executorNode.Port
			if effPort <= 0 {
				effPort = 22
			}
			if effPort != sshPortNow {
				updates["port"] = sshPortNow
				needUpdate = true
			}
			// 比对前先解密库里的密文（历史明文原样返回），写入前再加密
			nodePasswordNow := decryptCredential(executorNode.SSHPassword)
			nodeKeyNow := decryptCredential(executorNode.SSHPrivateKey)
			if sshPasswordNow != "" && nodePasswordNow != sshPasswordNow {
				encrypted, encErr := encryptCredential(sshPasswordNow)
				if encErr != nil {
					return nil, newPhysicalPrepareError("ssh_credential", "备份节点 SSH 密码加密失败", "请检查系统密钥配置后重试").WithRaw(encErr)
				}
				updates["ssh_password"] = encrypted
				needUpdate = true
			}
			if sshPrivateKeyNow != "" && nodeKeyNow != sshPrivateKeyNow {
				encrypted, encErr := encryptCredential(sshPrivateKeyNow)
				if encErr != nil {
					return nil, newPhysicalPrepareError("ssh_credential", "备份节点 SSH 私钥加密失败", "请检查系统密钥配置后重试").WithRaw(encErr)
				}
				updates["ssh_private_key"] = encrypted
				needUpdate = true
			}
			if needUpdate {
				_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
					Where("id = ?", executorNode.ID).
					Updates(updates).Error
				executorNode, _ = loadBackupNodeByID(task.ExecutorNodeID)
			}
		}
	}
	repoRemote := false
	repoPath := ""
	if isRepoS3Type {
		// S3 仓库：无仓库节点、无 repo-host SSH 拓扑，pgbackrest 在数据库主机直写对象存储。
		if storageTarget == nil {
			return nil, newPhysicalPrepareError(
				"storage_target",
				"加载对象存储目标失败",
				"请确认存储目标存在后重试。",
			).WithDetail(fmt.Sprintf("task_id=%s storage_target_id=%d", task.TaskID, task.StorageTargetID))
		}
		repoBase := strings.TrimSpace(storageTarget.BasePath)
		if repoBase == "" {
			repoBase = "/goinsight-backup"
		}
		repoPath = resolvePGBackRestRepoDir(repoBase, task.SourceInstanceID, task.TaskID)
		if !strings.HasPrefix(repoPath, "/") {
			// repo1-path 在 S3 仓库上必须是以 / 开头的前缀
			repoPath = "/" + repoPath
		}
	} else {
		repoRemote = !sameBackupNodeHost(executorNode, repositoryNode)
		repoPath = resolvePGBackRestRepoDir(strings.TrimSpace(repositoryNode.BaseDir), task.SourceInstanceID, task.TaskID)
		if err := ensurePGBackRestRepositoryAccess(instance, executorNode, repositoryNode, repoPath, repoRemote); err != nil {
			stage := "repo_access_remote"
			title := "pgBackRest 仓库节点接入失败"
			suggestion := "请确认备份中心机（存储目标）SSH 地址、凭据、权限、磁盘正确；若为异地仓库，需保证数据库主机可直接 SSH 登录到备份中心机。"
			if !repoRemote {
				stage = "repo_access_local"
				title = "准备本地 pgBackRest 仓库目录失败"
				suggestion = "请确认备份中心机的备份根路径存在、对 SSH 用户可写、且磁盘空间充足。"
			}
			return nil, newPhysicalPrepareError(stage, title, suggestion).
				WithHost(strings.TrimSpace(repositoryNode.Host)).
				WithDetail(fmt.Sprintf("repo_path=%s executor_host=%s", repoPath, strings.TrimSpace(executorNode.Host))).
				WithRaw(err)
		}
	}
	serverVersion, err := probePostgresVersion(instance.Hostname, instance.Port, strings.TrimSpace(string(instance.User)), strings.TrimSpace(string(instance.Password)), "", "")
	if err != nil {
		return nil, newPhysicalPrepareError(
			"instance_version",
			"探测 PostgreSQL 服务端版本失败",
			"请确认数据库连接信息正确、实例在线且允许当前账户连接后再重试。",
		).WithHost(strings.TrimSpace(instance.Hostname)).WithRaw(err)
	}
	if err := validatePostgresPhysicalVersion(serverVersion); err != nil {
		return nil, newPhysicalPrepareError(
			"instance_version",
			"PostgreSQL 版本不满足物理备份要求",
			"PG 物理备份仅支持 PostgreSQL 9.6 及以上版本，请确认实例版本后重试。",
		).WithHost(strings.TrimSpace(instance.Hostname)).WithRaw(err)
	}
	if err := validatePGBackRestFixedChecks(instance, task, executorNode, repositoryNode, serverVersion, repoRemote); err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if len(task.DriverConfig) > 0 {
		if err := json.Unmarshal(task.DriverConfig, &raw); err != nil {
			return nil, err
		}
	}
	normalizedEffectiveType := normalizePGBackRestBackupType(effectiveBackupType)
	if normalizedEffectiveType != "" {
		raw["backup_type"] = normalizedEffectiveType
	}
	if archiveCommand, archiveErr := probePostgresArchiveCommand(instance); archiveErr == nil {
		if stanza := extractPGBackRestArchiveStanza(archiveCommand); stanza != "" {
			raw["stanza"] = stanza
		}
	}
	if isRepoS3Type {
		// S3 仓库：连接参数从存储目标解密后写入 driver_config（随 run/备份集快照落库，恢复端直接复用）。
		if storageTarget == nil {
			return nil, newPhysicalPrepareError(
				"storage_target",
				"加载对象存储目标失败",
				"请确认存储目标存在后重试。",
			).WithDetail(fmt.Sprintf("task_id=%s storage_target_id=%d", task.TaskID, task.StorageTargetID))
		}
		_, _, s3AK, s3SK, decryptErr := decryptStorageTargetCredentials(storageTarget)
		if decryptErr != nil {
			return nil, newPhysicalPrepareError(
				"storage_target_credentials",
				"解密对象存储凭据失败",
				"请到「备份恢复 → 存储目标」重新保存 AccessKey/SecretKey 后重试。",
			).WithDetail(fmt.Sprintf("storage_target_id=%d", storageTarget.ID)).WithRaw(decryptErr)
		}
		if strings.TrimSpace(storageTarget.S3Bucket) == "" || strings.TrimSpace(storageTarget.S3Endpoint) == "" {
			return nil, newPhysicalPrepareError(
				"storage_target_config",
				"对象存储目标缺少 bucket/endpoint 配置",
				"请到「备份恢复 → 存储目标」补全 bucket、endpoint 后重试。",
			).WithDetail(fmt.Sprintf("storage_target_id=%d bucket_empty=%v endpoint_empty=%v", storageTarget.ID,
				strings.TrimSpace(storageTarget.S3Bucket) == "", strings.TrimSpace(storageTarget.S3Endpoint) == ""))
		}
		if strings.TrimSpace(s3AK) == "" || strings.TrimSpace(s3SK) == "" {
			return nil, newPhysicalPrepareError(
				"storage_target_config",
				"对象存储目标缺少 AK/SK 凭据",
				"请到「备份恢复 → 存储目标」补全 AccessKey/SecretKey 后重试。",
			).WithDetail(fmt.Sprintf("storage_target_id=%d", storageTarget.ID))
		}
		raw["repo_type"] = "s3"
		raw["repo_storage_target_type"] = storageTargetType
		raw["s3_bucket"] = strings.TrimSpace(storageTarget.S3Bucket)
		raw["s3_endpoint"] = strings.TrimSpace(storageTarget.S3Endpoint)
		raw["s3_region"] = strings.TrimSpace(storageTarget.S3Region)
		raw["s3_access_key"] = strings.TrimSpace(s3AK)
		raw["s3_secret_key"] = strings.TrimSpace(s3SK)
		uriStyle := "host"
		if storageTarget.S3ForcePathStyle {
			uriStyle = "path"
		}
		raw["s3_uri_style"] = uriStyle
	} else {
		raw["repo_node_id"] = repositoryNode.ID
		raw["repo_host"] = strings.TrimSpace(repositoryNode.Host)
		raw["repo_port"] = repositoryNode.Port
		raw["repo_user"] = defaultBackupNodeSSHUser(repositoryNode.SSHUser)
		raw["repo_tools_dir"] = defaultBackupNodeToolsDir(repositoryNode.ToolsDir)
		// 传给 agent 的必须是明文（历史明文原样返回）
		raw["repo_ssh_password"] = decryptCredential(repositoryNode.SSHPassword)
		raw["repo_ssh_private_key"] = decryptCredential(repositoryNode.SSHPrivateKey)
	}
	raw["repo_path"] = repoPath
	raw["repo_remote"] = repoRemote
	return marshalJSONMapOrNil(raw)
}

func resolvePGBackRestRepositoryNodeForTask(task *backupModels.InsightBackupTasks) (*backupModels.InsightBackupNodes, error) {
	if task == nil {
		return nil, fmt.Errorf("备份任务不存在")
	}
	if task.RepositoryNodeID > 0 {
		return loadBackupNodeByID(task.RepositoryNodeID)
	}
	if task.StorageTargetID == 0 {
		return nil, fmt.Errorf("PG_BACKREST 物理备份需要选择备份存储目标（仓库）")
	}
	target, err := loadStorageTargetByID(task.StorageTargetID)
	if err != nil {
		return nil, fmt.Errorf("加载备份存储目标失败: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("备份存储目标不存在")
	}
	targetType := strings.ToUpper(strings.TrimSpace(string(target.Type)))
	switch targetType {
	case "LOCAL_SERVER":
		node, mappingErr := resolveVirtualRepoNodeFromStorageTarget(target.ID)
		if mappingErr != nil {
			return nil, mappingErr
		}
		return node, nil
	case "OSS_ALIYUN", "COS_TENCENT", "S3_MINIO", "OBS_HUAWEI":
		// S3 兼容对象存储仓库：pgBackRest repo1-type=s3 直读写，无仓库节点（返回 nil, nil）。
		// 仓库连接参数由 preparePGBackRestRunDriverConfig 从存储目标解密后写入 driver_config。
		return nil, nil
	default:
		return nil, fmt.Errorf("不支持的存储目标类型: %s", targetType)
	}
}

func sameBackupNodeHost(left *backupModels.InsightBackupNodes, right *backupModels.InsightBackupNodes) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(left.Host), strings.TrimSpace(right.Host))
}

func resolveVirtualRepoNodeFromStorageTarget(storageTargetID uint64) (*backupModels.InsightBackupNodes, error) {
	if storageTargetID == 0 {
		return nil, fmt.Errorf("存储目标 ID 为空")
	}
	target, err := loadStorageTargetByID(storageTargetID)
	if err != nil {
		return nil, fmt.Errorf("加载备份存储目标失败: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("备份存储目标不存在")
	}
	targetType := strings.ToUpper(strings.TrimSpace(string(target.Type)))
	if targetType != "LOCAL_SERVER" {
		return nil, fmt.Errorf("当前仅支持 LOCAL_SERVER 类型存储目标作为 pgBackRest 物理恢复仓库映射（暂不支持对象存储恢复：%s）", targetType)
	}
	sshPassword, sshPrivateKey, _, _, decryptErr := decryptStorageTargetCredentials(target)
	if decryptErr != nil {
		return nil, fmt.Errorf("解密存储目标 SSH 凭据失败: %w", decryptErr)
	}
	sshPort := target.Port
	if sshPort <= 0 {
		sshPort = 22
	}
	node := &backupModels.InsightBackupNodes{
		Model:         &commonModels.Model{},
		Host:          strings.TrimSpace(target.Host),
		Port:          sshPort,
		BaseDir:       strings.TrimSpace(target.BasePath),
		SSHUser:       strings.TrimSpace(target.SSHUser),
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
		Name:          strings.TrimSpace(target.Name),
		Remark:        fmt.Sprintf("由存储目标（ID=%d）动态映射为 pgBackRest 仓库节点", target.ID),
		ToolsDir:      defaultBackupNodeToolsDir(""),
		InstallDir:    defaultBackupNodeInstallDir(""),
		ServiceName:   "",
		LogPath:       "",
		ServerURL:     "",
		Token:         "",
		Status:        commonModels.EnumType("ONLINE"),
	}
	node.Model.ID = storageTargetID
	node.ServiceName = defaultBackupNodeServiceName("", node.ID)
	node.LogPath = defaultBackupNodeLogPath("", node.InstallDir)
	if strings.TrimSpace(node.Host) == "" {
		return nil, fmt.Errorf("存储目标（LOCAL_SERVER）缺少主机地址")
	}
	if strings.TrimSpace(node.BaseDir) == "" {
		return nil, fmt.Errorf("存储目标（LOCAL_SERVER）缺少备份根路径 base_path")
	}
	if strings.TrimSpace(node.SSHUser) == "" || (strings.TrimSpace(node.SSHPassword) == "" && strings.TrimSpace(node.SSHPrivateKey) == "") {
		return nil, fmt.Errorf("存储目标（LOCAL_SERVER）缺少 SSH 凭据或凭据未正确保存")
	}
	return node, nil
}

func joinCSV(items []string) string {
	if len(items) == 0 {
		return ""
	}
	out := items[0]
	for i := 1; i < len(items); i++ {
		out += "," + items[i]
	}
	return out
}

const currentDriverPrepareVersion = 1
const prepareAsyncTimeout = 240 * time.Second
const prepareStaleTimeout = 30 * time.Minute

func isPrepareSchemaMissingError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown column 'prepare_") ||
		strings.Contains(msg, "data truncated for column 'status'") ||
		strings.Contains(msg, "unknown column 'prepare_status'") ||
		strings.Contains(msg, "has no column 'prepare_")
}

const fallbackPrepareSchemaHint = "系统提示：数据库尚未执行 prepare 系列列迁移脚本（001_add_prepare_columns.sql），请先在 192.168.53.102 MySQL insight 库执行该迁移脚本后再重试备份；详情：Unknown prepare_* column or status enum missing PENDING_PREPARE。"

func fallbackMarkRunFailedByPrepareSchema(runID uint64, stage string, reason string) {
	if global.App.DB == nil || runID == 0 {
		return
	}
	merged := reason
	if !strings.Contains(merged, "001_add_prepare_columns") {
		merged = reason + " | " + fallbackPrepareSchemaHint
	}
	_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"status":        "FAILED",
			"end_at":        nowLocalTime(),
			"error_message": fmt.Sprintf("[stage=%s] %s", stage, merged),
			"updated_at":    nowLocalTime(),
		}).Error
}

func fallbackMarkTaskPrepareHintBySchema(taskID string, stage string, reason string) {
	if global.App.DB == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	merged := reason
	if !strings.Contains(merged, "001_add_prepare_columns") {
		merged = reason + " | " + fallbackPrepareSchemaHint
	}
	taskErr := global.App.DB.Model(&backupModels.InsightBackupTasks{}).
		Where("task_id = ?", taskID).
		Updates(map[string]any{
			"error_message": fmt.Sprintf("[task_prepare stage=%s] %s", stage, merged),
			"updated_at":    nowLocalTime(),
		}).Error
	_ = taskErr
	_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("task_id = ? AND status IN ('PENDING_PREPARE','PENDING')", taskID).
		Order("id DESC").Limit(1).
		Updates(map[string]any{
			"status":        "FAILED",
			"end_at":        nowLocalTime(),
			"error_message": fmt.Sprintf("[task_prepare stage=%s] %s", stage, merged),
			"updated_at":    nowLocalTime(),
		}).Error
}

func updateTaskPrepareStatus(taskID string, prepareStatus string, prepareStage string,
	title string, suggestion string, detail string, rawError string,
	setStartedAt bool, setFinishedAt bool) (int64, error) {
	if global.App.DB == nil {
		return 0, errors.New("db not ready")
	}
	updates := map[string]any{
		"prepare_status":         prepareStatus,
		"prepare_stage":          prepareStage,
		"prepare_title":          title,
		"prepare_suggestion":     suggestion,
		"prepare_detail":         detail,
		"prepare_raw_error":      rawError,
		"driver_prepare_version": currentDriverPrepareVersion,
		"updated_at":             nowLocalTime(),
	}
	if setStartedAt {
		updates["prepare_started_at"] = nowLocalTime()
	}
	if setFinishedAt {
		updates["prepare_finished_at"] = nowLocalTime()
	}
	db := global.App.DB.Model(&backupModels.InsightBackupTasks{}).
		Where("task_id = ? AND prepare_status IN ('NONE','PREPARING','PREPARE_ERROR')", taskID).
		Updates(updates)
	if db.Error != nil && isPrepareSchemaMissingError(db.Error) {
		mergedMsg := fmt.Sprintf("prepare_status=%s stage=%s err=%v", prepareStatus, prepareStage, db.Error)
		fallbackMarkTaskPrepareHintBySchema(taskID, prepareStage, mergedMsg)
		return 0, db.Error
	}
	return db.RowsAffected, db.Error
}

func updateRunPrepareStatus(runID uint64, prepareStatus string, prepareStage string,
	title string, suggestion string, detail string, rawError string,
	setStartedAt bool, setFinishedAt bool, alsoPromotePendingPrepare bool) (int64, error) {
	if global.App.DB == nil || runID == 0 {
		return 0, nil
	}
	updates := map[string]any{
		"prepare_status":         prepareStatus,
		"prepare_stage":          prepareStage,
		"prepare_title":          title,
		"prepare_suggestion":     suggestion,
		"prepare_detail":         detail,
		"prepare_raw_error":      rawError,
		"driver_prepare_version": currentDriverPrepareVersion,
		"updated_at":             nowLocalTime(),
	}
	if setStartedAt {
		updates["prepare_started_at"] = nowLocalTime()
	}
	if setFinishedAt {
		updates["prepare_finished_at"] = nowLocalTime()
	}
	forceMarkFailed := strings.EqualFold(strings.TrimSpace(prepareStatus), "PREPARE_ERROR")
	if alsoPromotePendingPrepare {
		updates["status"] = "PENDING"
	}
	if forceMarkFailed {
		updates["status"] = "FAILED"
		updates["end_at"] = nowLocalTime()
		sb := strings.Builder{}
		sb.WriteString("[prepare stage=")
		sb.WriteString(prepareStage)
		sb.WriteString("] ")
		if strings.TrimSpace(title) != "" {
			sb.WriteString("title:")
			sb.WriteString(title)
			sb.WriteString("；")
		}
		if strings.TrimSpace(suggestion) != "" {
			sb.WriteString("suggestion:")
			sb.WriteString(suggestion)
			sb.WriteString("；")
		}
		if strings.TrimSpace(detail) != "" {
			sb.WriteString("detail:")
			sb.WriteString(detail)
			sb.WriteString("；")
		}
		if strings.TrimSpace(rawError) != "" {
			sb.WriteString("raw:")
			sb.WriteString(rawError)
		}
		legacy := sb.String()
		updates["error_message"] = legacy
	}
	q := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("id = ? AND prepare_status IN ('NONE','PREPARING','PREPARE_ERROR')", runID)
	if alsoPromotePendingPrepare {
		q = q.Where("status = ?", "PENDING_PREPARE")
	}
	db := q.Updates(updates)
	if db.Error != nil && isPrepareSchemaMissingError(db.Error) {
		mergedMsg := fmt.Sprintf("prepare_status=%s stage=%s err=%v", prepareStatus, prepareStage, db.Error)
		fallbackMarkRunFailedByPrepareSchema(runID, prepareStage, mergedMsg)
		return 0, db.Error
	}
	if (alsoPromotePendingPrepare || forceMarkFailed) && db.Error == nil && db.RowsAffected == 0 {
		legacyUpdates := map[string]any{
			"updated_at": nowLocalTime(),
		}
		if alsoPromotePendingPrepare && !forceMarkFailed {
			legacyUpdates["status"] = "PENDING"
		}
		if forceMarkFailed {
			legacyUpdates["status"] = "FAILED"
			legacyUpdates["end_at"] = nowLocalTime()
			sb := strings.Builder{}
			sb.WriteString("[prepare stage=")
			sb.WriteString(prepareStage)
			sb.WriteString("] ")
			if strings.TrimSpace(title) != "" {
				sb.WriteString("title:")
				sb.WriteString(title)
				sb.WriteString("；")
			}
			if strings.TrimSpace(suggestion) != "" {
				sb.WriteString("suggestion:")
				sb.WriteString(suggestion)
				sb.WriteString("；")
			}
			if strings.TrimSpace(detail) != "" {
				sb.WriteString("detail:")
				sb.WriteString(detail)
				sb.WriteString("；")
			}
			if strings.TrimSpace(rawError) != "" {
				sb.WriteString("raw:")
				sb.WriteString(rawError)
			}
			legacyUpdates["error_message"] = sb.String()
		}
		fixQ := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
			Where("id = ?", runID)
		if alsoPromotePendingPrepare && !forceMarkFailed {
			fixQ = fixQ.Where("status = ?", "PENDING_PREPARE")
		}
		fix := fixQ.Updates(legacyUpdates)
		if fix.Error == nil && fix.RowsAffected > 0 {
			return fix.RowsAffected, nil
		}
		if fix.Error != nil && isPrepareSchemaMissingError(fix.Error) {
			fallbackMarkRunFailedByPrepareSchema(runID, prepareStage, fmt.Sprintf("promote fallback err=%v", fix.Error))
		}
	}
	return db.RowsAffected, db.Error
}

func min16(s string) int {
	if len(s) < 16 {
		return len(s)
	}
	return 16
}

func AsyncPrepareTask(taskID string) {
	if strings.TrimSpace(taskID) == "" {
		return
	}
	go func() {
		taskIDSafe := strings.TrimSpace(taskID)
		defer func() {
			if r := recover(); r != nil {
				_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", "panic",
					"任务级拓扑准备发生系统异常", "请稍后重试，或联系研发排查 nohup.out",
					fmt.Sprintf("panic=%v", r), fmt.Sprintf("%v", r), false, true)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), prepareAsyncTimeout)
		defer cancel()
		var task backupModels.InsightBackupTasks
		if err := global.App.DB.Where("task_id = ?", taskIDSafe).First(&task).Error; err != nil {
			return
		}
		isPG := strings.EqualFold(strings.TrimSpace(string(task.DriverType)), "PG_BACKREST")
		if !isPG {
			_, _ = updateTaskPrepareStatus(taskIDSafe, "NONE", "", "", "", "", "", false, false)
			return
		}
		rows, upErr := updateTaskPrepareStatus(taskIDSafe, "PREPARING", "validate_topology",
			"", "", "", "", true, false)
		if upErr != nil && isPrepareSchemaMissingError(upErr) {
			fallbackMarkTaskPrepareHintBySchema(taskIDSafe, "validate_topology", fmt.Sprintf("task prepare probe schema err=%v", upErr))
			return
		}
		if rows <= 0 {
			return
		}
		instance, err := loadInstanceByID(task.SourceInstanceID)
		if err != nil {
			_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", "instance_missing",
				"源数据库实例不存在或已被删除", "请编辑备份任务重新选择源实例",
				err.Error(), err.Error(), false, true)
			return
		}
		executorNode, execErr := loadBackupNodeByID(task.ExecutorNodeID)
		if executorNode == nil || execErr != nil {
			_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", "executor_node",
				"PG 物理备份执行节点不存在", "编辑任务重新选择执行节点",
				fmt.Sprintf("executor_node_id=%d err=%v", task.ExecutorNodeID, execErr), fmt.Sprintf("%v", execErr), false, true)
			return
		}
		// 对象存储仓库（repo1-type=s3）：没有仓库节点，也没有 repo-host 拓扑可校验。
		repoNode, repoErr := resolvePGBackRestRepositoryNodeForTask(&task)
		if repoErr != nil {
			_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", "repository_node",
				"PG 物理备份仓库未解析", "请确认存储目标配置正确",
				fmt.Sprintf("storage_target_id=%d err=%v", task.StorageTargetID, repoErr), fmt.Sprintf("%v", repoErr), false, true)
			return
		}
		if repoNode != nil {
			if topoErr := validatePGBackRestTopology(instance, executorNode, repoNode); topoErr != nil {
				if prepareErr, ok := AsPhysicalBackupPrepareError(topoErr); ok {
					_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", prepareErr.Stage,
						prepareErr.Title, prepareErr.Suggestion, prepareErr.Detail, prepareErr.RawError, false, true)
				} else {
					_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", "validate_topology",
						"PG 物理备份拓扑校验失败", "请检查执行节点、仓库节点、源实例之间的网络可达与 SSH 凭据",
						topoErr.Error(), topoErr.Error(), false, true)
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_ERROR", "timeout",
				"任务级拓扑准备超时", "请检查 SSH 连接稳定性后重试",
				ctx.Err().Error(), ctx.Err().Error(), false, true)
		default:
			_, _ = updateTaskPrepareStatus(taskIDSafe, "PREPARE_OK", "validate_topology",
				"", "", "", "", false, true)
		}
	}()
}

func AsyncPrepareRun(runID uint64) {
	if runID == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				_, _ = updateRunPrepareStatus(runID, "PREPARE_ERROR", "panic",
					"运行级 prepare 发生系统异常", "请稍后重新点击立即执行",
					fmt.Sprintf("panic=%v", r), fmt.Sprintf("%v", r), false, true, false)
				fallbackMarkRunFailedByPrepareSchema(runID, "panic", fmt.Sprintf("panic=%v", r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), prepareAsyncTimeout)
		defer cancel()
		if global.App.DB == nil {
			return
		}
		probeRows, probeErr := updateRunPrepareStatus(runID, "PREPARING", "schema_probe",
			"", "", "", "", true, false, false)
		if probeErr != nil && isPrepareSchemaMissingError(probeErr) {
			fallbackMarkRunFailedByPrepareSchema(runID, "schema_probe", fmt.Sprintf("schema probe update err=%v", probeErr))
			return
		}
		var run backupModels.InsightBackupRuns
		tx := global.App.DB.Begin()
		if tx.Error != nil {
			fallbackMarkRunFailedByPrepareSchema(runID, "begin_tx", fmt.Sprintf("begin tx err=%v", tx.Error))
			return
		}
		lockQ := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", runID)
		if probeErr == nil && probeRows > 0 {
			lockQ = lockQ.Where("prepare_status IN ('NONE','PREPARING','PREPARE_ERROR')")
			lockQ = lockQ.Where("status = ?", "PENDING_PREPARE")
		}
		firstErr := lockQ.First(&run).Error
		if firstErr != nil {
			_ = tx.Rollback()
			if errors.Is(firstErr, gorm.ErrRecordNotFound) {
				if probeErr == nil && probeRows > 0 {
					fix := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
						Where("id = ? AND status = ?", runID, "PENDING_PREPARE").
						Updates(map[string]any{
							"status":     "PENDING",
							"updated_at": nowLocalTime(),
						})
					if fix.Error != nil && isPrepareSchemaMissingError(fix.Error) {
						fallbackMarkRunFailedByPrepareSchema(runID, "promote_lock_miss", fmt.Sprintf("for update miss err=%v", firstErr))
					}
				}
				return
			}
			if isPrepareSchemaMissingError(firstErr) {
				_ = tx.Rollback()
				fallbackMarkRunFailedByPrepareSchema(runID, "for_update", fmt.Sprintf("for update select err=%v", firstErr))
				return
			}
			return
		}
		if probeRows <= 0 {
			rows, upErr := updateRunPrepareStatus(runID, "PREPARING", "start",
				"", "", "", "", true, false, false)
			if upErr != nil && isPrepareSchemaMissingError(upErr) {
				_ = tx.Rollback()
				fallbackMarkRunFailedByPrepareSchema(runID, "start", fmt.Sprintf("start prepare update err=%v", upErr))
				return
			}
			if rows <= 0 {
				_ = tx.Rollback()
				return
			}
		}
		_ = tx.Commit()
		isPG := strings.EqualFold(strings.TrimSpace(string(run.DriverType)), "PG_BACKREST")
		if !isPG {
			_, _ = updateRunPrepareStatus(runID, "PREPARE_OK", "", "", "", "", "", false, true, true)
			return
		}
		var task backupModels.InsightBackupTasks
		if err := global.App.DB.Where("task_id = ?", run.TaskID).First(&task).Error; err != nil {
			_, _ = updateRunPrepareStatus(runID, "PREPARE_ERROR", "task_missing",
				"关联备份任务不存在", "请重新创建执行记录", err.Error(), err.Error(), false, true, false)
			return
		}
		instance, err := loadInstanceByID(task.SourceInstanceID)
		if err != nil {
			_, _ = updateRunPrepareStatus(runID, "PREPARE_ERROR", "instance_missing",
				"源数据库实例不存在", "请编辑任务重新选择实例后再执行", err.Error(), err.Error(), false, true, false)
			return
		}
		effectiveBackupType := strings.TrimSpace(string(run.BackupType))
		if effectiveBackupType == "" {
			effectiveBackupType = strings.TrimSpace(string(task.BackupType))
		}
		done := make(chan struct{})
		var enrichedCfg []byte
		var prepareErr error
		go func() {
			defer close(done)
			enrichedCfg, prepareErr = preparePGBackRestRunDriverConfig(instance, &task, effectiveBackupType)
		}()
		select {
		case <-ctx.Done():
			_, _ = updateRunPrepareStatus(runID, "PREPARE_ERROR", "timeout",
				"运行级 prepare 超时（超过 240 秒）", "请检查 SSH 连接、磁盘空间、pgBackRest 安装后重新执行",
				ctx.Err().Error(), ctx.Err().Error(), false, true, false)
			return
		case <-done:
		}
		if prepareErr != nil {
			if casted, ok := AsPhysicalBackupPrepareError(prepareErr); ok {
				_, _ = updateRunPrepareStatus(runID, "PREPARE_ERROR", casted.Stage,
					casted.Title, casted.Suggestion, casted.Detail, casted.RawError, false, true, false)
			} else {
				_, _ = updateRunPrepareStatus(runID, "PREPARE_ERROR", "unknown",
					"PG 物理备份运行级 prepare 失败", "请根据详细错误排查后重试", prepareErr.Error(), prepareErr.Error(), false, true, false)
			}
			return
		}
		if len(enrichedCfg) > 0 {
			_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).
				Where("id = ?", runID).
				Update("driver_config", enrichedCfg).Error
		}
		_, _ = updateRunPrepareStatus(runID, "PREPARE_OK", "done",
			"", "", "", "", false, true, true)
	}()
}
