package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var ErrRestoreRunStopped = errors.New("restore run stopped")

func IsRestoreRunStoppedError(err error) bool {
	return errors.Is(err, ErrRestoreRunStopped)
}

type restoreRunControl struct {
	runID         uint64
	cancel        context.CancelFunc
	logPath       string
	stopRequested atomic.Bool
}

func attachRestoreRunControl(parent context.Context, runID uint64, logPath string) (context.Context, *restoreRunControl) {
	ctx, cancel := context.WithCancel(parent)
	control := &restoreRunControl{
		runID:   runID,
		cancel:  cancel,
		logPath: strings.TrimSpace(logPath),
	}
	if runID == 0 || global.App.DB == nil {
		return ctx, control
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !isRestoreRunStopRequested(runID) {
					continue
				}
				if control.stopRequested.CompareAndSwap(false, true) {
					appendBackupRunLog(control.logPath, "WARN", "检测到停止请求，准备中止当前恢复进程")
				}
				cancel()
				return
			}
		}
	}()
	return ctx, control
}

func (c *restoreRunControl) Close() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
}

func (c *restoreRunControl) StopRequested() bool {
	return c != nil && c.stopRequested.Load()
}

func isRestoreRunStopRequested(runID uint64) bool {
	if runID == 0 || global.App.DB == nil {
		return false
	}
	var row struct {
		Status string
	}
	if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Select("status").
		Where("id = ?", runID).
		Take(&row).Error; err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(row.Status), "STOP_REQUESTED")
}

func resolveRestoreStopReason(operator string) string {
	text := strings.TrimSpace(operator)
	if text == "" {
		text = "system"
	}
	return fmt.Sprintf("用户手动终止恢复任务，操作人=%s", text)
}

func buildRetryRestoreName(source string) string {
	base := strings.TrimSpace(source)
	if base == "" {
		base = "db"
	}
	return fmt.Sprintf("%s_restore_%s", base, nowLocalTime().Format("20060102150405"))
}

func buildRetryRestoreSuffix() string {
	return fmt.Sprintf("_restore_%s", nowLocalTime().Format("20060102150405"))
}

func finalizeRestoreRunError(runID uint64, logPath string, err error, control *restoreRunControl) {
	if err == nil {
		return
	}
	if (control != nil && control.StopRequested()) || isRestoreRunStopRequested(runID) || errors.Is(err, context.Canceled) {
		writeRestoreStopped(runID, logPath, resolveRestoreStopReason(""))
		return
	}
	if strings.TrimSpace(logPath) != "" {
		writeRestoreFailedWithLog(runID, logPath, err)
		return
	}
	writeRestoreFailed(runID, err)
}

func writeRestoreStopped(id uint64, logPath string, reason string) {
	messageText := strings.TrimSpace(reason)
	if messageText == "" {
		messageText = "恢复任务已终止"
	}
	if strings.TrimSpace(logPath) != "" {
		appendBackupRunLog(logPath, "WARN", "%s", messageText)
	}
	updateRestoreRunProgress(id, restoreRunProgressPatch{
		Status:       "STOPPED",
		CurrentStage: restoreStageFinalize,
		LogPath:      logPath,
		ErrorMessage: messageText,
		Finished:     true,
	})
}

type StopRestoreRunService struct {
	C            *gin.Context
	RestoreRunID uint64
}

type StopRestoreRunResult struct {
	RestoreRunID     uint64 `json:"restore_run_id"`
	Status           string `json:"status"`
	StopState        string `json:"stop_state"`
	StopStateLabel   string `json:"stop_state_label"`
	StopStateMessage string `json:"stop_state_message"`
}

func (s *StopRestoreRunService) Run() (*StopRestoreRunResult, error) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", s.RestoreRunID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("恢复任务不存在")
		}
		return nil, err
	}
	operator := strings.TrimSpace(currentUsername(s.C))
	if operator == "" {
		operator = "system"
	}
	stopReason := resolveRestoreStopReason(operator)
	buildResult := func(status string, state string, label string, messageText string) *StopRestoreRunResult {
		return &StopRestoreRunResult{
			RestoreRunID:     run.ID,
			Status:           strings.TrimSpace(status),
			StopState:        strings.TrimSpace(state),
			StopStateLabel:   strings.TrimSpace(label),
			StopStateMessage: strings.TrimSpace(messageText),
		}
	}

	switch strings.ToUpper(strings.TrimSpace(string(run.Status))) {
	case "SUCCESS", "FAILED", "STOPPED":
		return nil, fmt.Errorf("当前恢复任务已结束，无需终止")
	case "STOP_REQUESTED":
		// 已经发过终止请求。若执行协程仍在正常收尾，保持「终止中」；
		// 若协程已失联（超过阈值没有任何进展），操作员再次点击即视为强制收口，
		// 否则任务会永久停在这个既不能删、也不能重跑、也执行不了的中间态。
		if restoreStopRequestStale(run.LastReportAt, run.UpdatedAt) {
			appendBackupRunLog(run.LogPath, "WARN", "终止请求超过阈值仍未收口，已按操作人=%s 的请求强制收口", operator)
			if _, err := finalizeStaleRestoreStopRequest(run.ID, stopReason, run.LogPath); err != nil {
				return nil, err
			}
			return buildResult("STOPPED", "IMMEDIATE", "已终止", "恢复进程已无响应，任务已强制收口"), nil
		}
		return buildResult(string(run.Status), "WAITING", "终止中", "已记录终止请求，正在等待恢复进程退出"), nil
	case "PENDING":
		appendBackupRunLog(run.LogPath, "WARN", "收到终止请求，任务尚未开始执行，已直接停止，操作人=%s", operator)
		if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":        "STOPPED",
			"end_at":        commonModels.LocalTime(nowLocalTime()),
			"error_message": stopReason,
			"log_excerpt":   tailLogExcerpt(run.LogPath),
		}).Error; err != nil {
			return nil, err
		}
		return buildResult("STOPPED", "IMMEDIATE", "已终止", "当前任务尚未开始执行，已直接终止"), nil
	case "RUNNING":
		appendBackupRunLog(run.LogPath, "WARN", "收到终止请求，正在中止当前恢复进程，操作人=%s", operator)
		if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":        "STOP_REQUESTED",
			"error_message": stopReason,
			"log_excerpt":   tailLogExcerpt(run.LogPath),
		}).Error; err != nil {
			return nil, err
		}
		return buildResult("STOP_REQUESTED", "WAITING", "终止中", "已发送终止请求，正在等待恢复进程退出"), nil
	default:
		return nil, fmt.Errorf("当前恢复状态不支持终止")
	}
}

type RetryRestoreRunService struct {
	C            *gin.Context
	RestoreRunID uint64
}

func (s *RetryRestoreRunService) Run() (*backupModels.InsightRestoreRuns, error) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", s.RestoreRunID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("恢复任务不存在")
		}
		return nil, err
	}
	status := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	// 刻意不对 PENDING 放宽：重跑是以 Mode="plan" 新建一条记录（见下方 form.Mode），
	// 对无人接管的计划态残留放开重跑，只会再造一条同样的幽灵任务。
	// 清理这类记录走删除（DeleteRestoreRunService 已按 isRestoreRunPendingOrphan 放行）。
	if status == "RUNNING" || status == "PENDING" || status == "STOP_REQUESTED" {
		return nil, fmt.Errorf("当前恢复任务仍在执行中，暂不能重跑")
	}
	options := decodeLogicalRestoreOptionsJSON(run.Options)
	targetDBName := strings.TrimSpace(run.TargetDBName)
	targetDBSuffix := strings.TrimSpace(options.TargetDBSuffix)
	if targetDBSuffix != "" {
		targetDBSuffix = buildRetryRestoreSuffix()
		targetDBName = ""
	} else if targetDBName != "" {
		targetDBName = buildRetryRestoreName(targetDBName)
	}
	form := &backupForms.CreateRestoreRunForm{
		Name:                    run.Name,
		BackupSetID:             run.BackupSetID,
		TargetInstanceID:        run.TargetInstanceID,
		TargetDBName:            targetDBName,
		TargetDBSuffix:          targetDBSuffix,
		Mode:                    "plan",
		RestoreMode:             ternaryString(strings.TrimSpace(string(run.RestoreMode)) != "", string(run.RestoreMode), "NEW"),
		SelectedDatabases:       options.SelectedDatabases,
		SelectedSchemas:         options.SelectedSchemas,
		SelectedTables:          options.SelectedTables,
		Parallel:                options.Parallel,
		Timeout:                 options.Timeout,
		TargetCharset:           options.TargetCharset,
		TargetCollation:         options.TargetCollation,
		TargetEncoding:          options.TargetEncoding,
		CloseBinlog:             boolPtr(options.CloseBinlog),
		ForeignKeyChecks:        boolPtr(options.ForeignKeyChecks),
		ExitOnError:             boolPtr(options.ExitOnError),
		NoOwner:                 boolPtr(options.NoOwner),
		NoPrivileges:            boolPtr(options.NoPrivileges),
		CleanIfExists:           boolPtr(options.CleanIfExists),
		IsCloud:                 boolPtr(options.IsCloud),
	}
	service := CreateRestoreRunService{
		CreateRestoreRunForm: form,
		C:                    s.C,
	}
	return service.Run()
}

type UpdateRestoreRunService struct {
	C            *gin.Context
	RestoreRunID uint64
	*backupForms.UpdateRestoreRunForm
}

func (s *UpdateRestoreRunService) Run() (*backupModels.InsightRestoreRuns, error) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", s.RestoreRunID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("恢复任务不存在")
		}
		return nil, err
	}
	status := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	currentStage := strings.ToUpper(strings.TrimSpace(run.CurrentStage))
	if status != "PENDING" && !(status == "FAILED" && currentStage == restoreStagePrecheck) {
		return nil, fmt.Errorf("仅未开始或预检查失败的恢复任务支持编辑")
	}
	plan, err := prepareLogicalRestorePlan(buildLogicalRestorePlanInputFromUpdateForm(s.UpdateRestoreRunForm), s.C)
	if err != nil {
		return nil, err
	}
	zeroTime := commonModels.LocalTime(time.Time{})
	updates := map[string]any{
		"name":               normalizeRestoreRunName(s.Name, plan.Set.Name, firstNonEmpty(plan.Options.TargetDBName, plan.Options.TargetDBSuffix)),
		"backup_set_id":      plan.Set.ID,
		"source_instance_id": plan.Set.SourceInstanceID,
		"db_type":            plan.Set.DBType,
		"restore_kind":       restoreKindLogical,
		"driver_type":        plan.Set.DriverType,
		"restore_mode":       plan.Options.RestoreMode,
		"target_level":       restoreTargetLevelDatabase,
		"target_instance_id": s.TargetInstanceID,
		"target_name":        firstNonEmpty(plan.Options.TargetDBName, plan.Options.TargetDBSuffix),
		"target_db_name":     firstNonEmpty(plan.Options.TargetDBName, plan.Options.TargetDBSuffix),
		"resolved_context":   plan.ResolvedJSON,
		"options":            plan.OptionsJSON,
		"status":             plan.Status,
		"current_stage":      plan.Stage,
		"progress_percent":   0,
		"current_object":     "",
		"current_action":     plan.Action,
		"risk_level":         plan.RiskLevel,
		"precheck_summary":   plan.PrecheckJSON,
		"result_summary":     nil,
		"start_at":           zeroTime,
		"end_at":             zeroTime,
		"last_report_at":     zeroTime,
		"log_excerpt":        "",
		"error_message":      plan.ErrorMessage,
	}
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("restore_run_id = ?", run.ID).Delete(&backupModels.InsightRestoreRunItems{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", run.ID).Updates(updates).Error; err != nil {
			return err
		}
		for _, item := range plan.Items {
			item.RestoreRunID = run.ID
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if plan.AutoExecute {
		go executeRestoreRun(run.ID)
	}
	var updated backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", run.ID).First(&updated).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

// UpdateRestoreRunNameService 轻量改名：只写 name 字段。
// 之所以单独提供，是因为完整 Update 服务会重建恢复计划、校验任务状态、
// 清空进度与日志字段，且仅允许 PENDING/预检查失败状态编辑——改名不应受这些限制。
// 逻辑恢复与物理恢复共用 insight_restore_runs 表，因此按数字 ID 直接更新即可覆盖两者。
type UpdateRestoreRunNameService struct {
	*backupForms.UpdateRestoreRunNameForm
	C            *gin.Context
	RestoreRunID uint64
}

func (s *UpdateRestoreRunNameService) Run() error {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return fmt.Errorf("任务名称不能为空")
	}
	if runeLen := len([]rune(name)); runeLen < 2 {
		return fmt.Errorf("任务名称至少 2 个字符")
	} else if runeLen > 64 {
		return fmt.Errorf("任务名称最长 64 个字符")
	}
	var current backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", s.RestoreRunID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("恢复任务不存在")
		}
		return err
	}
	if strings.TrimSpace(current.Name) == name {
		return nil
	}
	return global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Where("id = ?", s.RestoreRunID).
		Update("name", name).Error
}

type DeleteRestoreRunService struct {
	C            *gin.Context
	RestoreRunID uint64
}

func (s *DeleteRestoreRunService) Run() error {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", s.RestoreRunID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("恢复任务不存在")
		}
		return err
	}
	status := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	switch status {
	case "RUNNING", "STOP_REQUESTED":
		return fmt.Errorf("运行中的恢复任务暂不支持删除，请先终止任务")
	case "PENDING":
		// PENDING 有两种截然不同的来源，不能一律拒绝：
		//   ① 真实执行任务排队中 —— 已被协程接管（start_at 非空），拒绝删除；
		//   ② PLAN 模式落库的计划态快照 —— 无人接管、永远不会推进，
		//      一律拒绝会让它在列表里永久堆积成删不掉的幽灵任务。
		// 判据见 isRestoreRunPendingOrphan（restore_pending_state.go）。
		if !isRestoreRunPendingOrphan(run) {
			return fmt.Errorf("恢复任务正在排队等待执行，暂不支持删除，请先终止任务")
		}
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("restore_run_id = ?", run.ID).Delete(&backupModels.InsightRestoreRunItems{}).Error; err != nil {
			return err
		}
		return tx.Delete(&backupModels.InsightRestoreRuns{}, run.ID).Error
	})
}

type ResolveRestoreRunLogFileService struct {
	RestoreRunID uint64
	ItemID       uint64
}

type RestoreRunLogFile struct {
	FilePath string
	FileName string
}

func (s *ResolveRestoreRunLogFileService) Run() (*RestoreRunLogFile, error) {
	type row struct {
		ID      uint64
		LogPath string
	}
	var run row
	if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Select("id, log_path").
		Where("id = ?", s.RestoreRunID).
		Take(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("恢复任务不存在")
		}
		return nil, err
	}
	logPath := strings.TrimSpace(run.LogPath)
	fileName := fmt.Sprintf("restore-run-%d.log", s.RestoreRunID)
	if s.ItemID != 0 {
		var item row
		if err := global.App.DB.Model(&backupModels.InsightRestoreRunItems{}).
			Select("id, log_path").
			Where("id = ? AND restore_run_id = ?", s.ItemID, s.RestoreRunID).
			Take(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("恢复对象日志不存在")
			}
			return nil, err
		}
		logPath = strings.TrimSpace(item.LogPath)
		fileName = fmt.Sprintf("restore-run-%d-item-%d.log", s.RestoreRunID, s.ItemID)
	}
	if logPath == "" {
		return nil, fmt.Errorf("当前任务暂无可下载日志文件")
	}
	return &RestoreRunLogFile{
		FilePath: logPath,
		FileName: fileName,
	}, nil
}
