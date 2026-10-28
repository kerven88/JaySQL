package services

import (
	"fmt"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

const (
	restoreStagePending       = "PENDING"
	restoreStagePrecheck      = "PRECHECK"
	restoreStagePrepareTarget = "PREPARE_TARGET"
	restoreStagePrepareSource = "PREPARE_SOURCE"
	restoreStageRestoring     = "RESTORE"
	restoreStageVerify        = "VERIFY"
	restoreStageFinalize      = "FINALIZE"
	restoreStageFinished      = "FINISHED"
)

type GetRestoreRunDetailService struct {
	RestoreRunID uint64
	C            *gin.Context
}

func (s *GetRestoreRunDetailService) Run() (any, error) {
	// 详情页会被轮询刷新，单条收口即可，避免全表扫描。
	if _, cleanupErr := cleanupStaleRestoreStopRequestedRun(s.RestoreRunID); cleanupErr != nil {
		global.App.Log.Warnf("清理失联的终止中恢复任务失败: %v", cleanupErr)
	}
	type runRow struct {
		ID               uint64                 `json:"id"`
		TaskID           string                 `json:"task_id"`
		Name             string                 `json:"name"`
		BackupSetID      uint64                 `json:"backup_set_id"`
		BackupSetsID     string                 `json:"backup_sets_id"`
		DBType           string                 `json:"db_type"`
		DriverType       string                 `json:"driver_type"`
		RestoreMode      string                 `json:"restore_mode"`
		ObjectMode       string                 `json:"object_mode"`
		ObjectSummary    string                 `json:"object_summary"`
		TargetInstanceID uint64                 `json:"target_instance_id"`
		TargetDBName     string                 `json:"target_db_name"`
		TargetDisplay    string                 `json:"target_display"`
		Status           string                 `json:"status"`
		StartAt          commonModels.LocalTime `json:"start_at"`
		EndAt            commonModels.LocalTime `json:"end_at"`
		CreatedAt        commonModels.LocalTime `json:"created_at"`
		UpdatedAt        commonModels.LocalTime `json:"updated_at"`
		LastReportAt     commonModels.LocalTime `json:"last_report_at"`
		CurrentStage     string                 `json:"current_stage"`
		ProgressPercent  int                    `json:"progress_percent"`
		CurrentObject    string                 `json:"current_object"`
		CurrentAction    string                 `json:"current_action"`
		RiskLevel        string                 `json:"risk_level"`
		ResolvedContext  datatypes.JSON         `json:"resolved_context"`
		Options          datatypes.JSON         `json:"options"`
		PrecheckSummary  datatypes.JSON         `json:"precheck_summary"`
		ResultSummary    datatypes.JSON         `json:"result_summary"`
		LogPath          string                 `json:"log_path"`
		LogExcerpt       string                 `json:"log_excerpt"`
		ErrorMessage     string                 `json:"error_message"`
		SetName          string                 `json:"set_name"`
		SetFileName      string                 `json:"set_file_name"`
		SourceDBName     string                 `json:"source_db_name"`
		TargetHost       string                 `json:"target_host"`
		TargetPort       int                    `json:"target_port"`
		TargetRemark     string                 `json:"target_remark"`
		CreatedBy        string                 `json:"created_by"`
		SourceHost       string                 `json:"source_host"`
		SourcePort       int                    `json:"source_port"`
		SourceRemark     string                 `json:"source_remark"`
	}
	var run runRow
	if err := global.App.DB.Table("insight_restore_runs rr").
		Select(`rr.id, rr.task_id, rr.name, rr.backup_set_id, rr.db_type, rr.restore_mode, rr.target_instance_id, rr.target_db_name, rr.status, rr.start_at, rr.end_at,
                        rr.created_at, rr.updated_at, rr.last_report_at, rr.current_stage, rr.progress_percent, rr.current_object, rr.current_action,
                        rr.risk_level, rr.resolved_context, rr.options, rr.precheck_summary, rr.result_summary, rr.log_path,
                        rr.log_excerpt, rr.error_message, rr.created_by,
			bs.name as set_name, bs.file_name as set_file_name, bs.backup_sets_id as backup_sets_id, bs.source_db_name as source_db_name, bs.object_summary as object_summary,
			bs.object_mode as object_mode, bs.driver_type as driver_type,
			si.hostname as source_host, si.port as source_port, si.remark as source_remark,
			i.hostname as target_host, i.port as target_port, i.remark as target_remark`).
		Joins("left join insight_backup_sets bs on bs.id = rr.backup_set_id").
		// 历史执行记录 rr.source_instance_id 可能为 0，退化到备份集登记的来源实例，避免「源实例」永远为空
		Joins("left join insight_instances si on si.id = COALESCE(NULLIF(rr.source_instance_id, 0), bs.source_instance_id)").
		Joins("left join insight_instances i on i.id = rr.target_instance_id").
		Where("rr.id = ?", s.RestoreRunID).
		Scan(&run).Error; err != nil {
		return nil, err
	}
	if run.ID == 0 {
		return nil, fmt.Errorf("恢复任务不存在")
	}
	run.TaskID = restoreTaskIDOrFallback(run.TaskID, run.ID)
	run.Name = normalizeRestoreRunName(run.Name, run.SetName, run.TargetDBName)
	run.TargetDisplay = deriveRestoreTargetDisplay(run.DBType, run.ObjectMode, run.TargetDBName)
	if strings.TrimSpace(run.LogExcerpt) == "" {
		run.LogExcerpt = tailLogExcerpt(run.LogPath)
	}

	type itemRow struct {
		ID              uint64                 `json:"id"`
		RestoreRunID    uint64                 `json:"restore_run_id"`
		SourceObject    string                 `json:"source_object_name"`
		TargetObject    string                 `json:"target_object_name"`
		Status          string                 `json:"status"`
		CurrentStage    string                 `json:"current_stage"`
		ProgressPercent int                    `json:"progress_percent"`
		DisplayOrder    int                    `json:"display_order"`
		StartAt         commonModels.LocalTime `json:"start_at"`
		EndAt           commonModels.LocalTime `json:"end_at"`
		LastReportAt    commonModels.LocalTime `json:"last_report_at"`
		LogPath         string                 `json:"log_path"`
		LogExcerpt      string                 `json:"log_excerpt"`
		ErrorMessage    string                 `json:"error_message"`
	}
	var items []itemRow
	if err := global.App.DB.Table("insight_restore_run_items").
		Where("restore_run_id = ?", s.RestoreRunID).
		Order("display_order asc, id asc").
		Scan(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		if strings.TrimSpace(items[i].LogExcerpt) == "" {
			items[i].LogExcerpt = tailLogExcerpt(items[i].LogPath)
		}
	}

	type summary struct {
		Total      int   `json:"total"`
		Pending    int   `json:"pending"`
		Running    int   `json:"running"`
		Success    int   `json:"success"`
		Failed     int   `json:"failed"`
		AvgPercent int   `json:"avg_percent"`
		UpdatedAt  int64 `json:"updated_at_unix"`
	}
	stats := summary{}
	for _, item := range items {
		stats.Total++
		stats.AvgPercent += item.ProgressPercent
		switch strings.ToUpper(strings.TrimSpace(item.Status)) {
		case "SUCCESS":
			stats.Success++
		case "FAILED":
			stats.Failed++
		case "RUNNING":
			stats.Running++
		default:
			stats.Pending++
		}
	}
	if stats.Total > 0 {
		stats.AvgPercent = stats.AvgPercent / stats.Total
	}
	itemList := make([]map[string]any, 0, len(items))
	for _, item := range items {
		itemList = append(itemList, map[string]any{
			"id":                  item.ID,
			"source_object_name":  item.SourceObject,
			"target_object_name":  item.TargetObject,
			"status":              item.Status,
			"current_stage":       item.CurrentStage,
			"current_stage_label": restoreStageLabel(item.CurrentStage),
			"progress_percent":    clampPercent(item.ProgressPercent),
			"display_order":       item.DisplayOrder,
			"start_at":            item.StartAt,
			"end_at":              item.EndAt,
			"duration_seconds":    calcRestoreDurationSeconds(item.StartAt, item.EndAt, item.Status),
			"last_report_at":      item.LastReportAt,
			"last_report_ago_sec": calcRestoreHeartbeatAgeSeconds(item.LastReportAt),
			"log_path":            item.LogPath,
			"log_excerpt":         item.LogExcerpt,
			"error_message":       item.ErrorMessage,
		})
	}
	runData := map[string]any{
		"id":                  run.ID,
		"task_id":             run.TaskID,
		"name":                run.Name,
		"backup_set_id":       run.BackupSetID,
		"backup_sets_id":      run.BackupSetsID,
		"db_type":             run.DBType,
		"driver_type":         run.DriverType,
		"object_mode":         run.ObjectMode,
		"object_summary":      run.ObjectSummary,
		"target_instance_id":  run.TargetInstanceID,
		"target_db_name":      run.TargetDBName,
		"target_display":      run.TargetDisplay,
		"status":              run.Status,
		"start_at":            run.StartAt,
		"end_at":              run.EndAt,
		"created_at":          run.CreatedAt,
		"updated_at":          run.UpdatedAt,
		"last_report_at":      run.LastReportAt,
		"last_report_ago_sec": calcRestoreHeartbeatAgeSeconds(run.LastReportAt),
		"current_stage":       run.CurrentStage,
		"current_stage_label": restoreStageLabel(run.CurrentStage),
		"progress_percent":    clampPercent(run.ProgressPercent),
		"current_object":      run.CurrentObject,
		"current_action":      run.CurrentAction,
		"risk_level":          run.RiskLevel,
		"restore_mode":        run.RestoreMode,
		"resolved_context":    decodeJSONValue(run.ResolvedContext),
		"options":             decodeJSONValue(run.Options),
		"precheck_summary":    decodeJSONValue(run.PrecheckSummary),
		"result_summary":      decodeJSONValue(run.ResultSummary),
		"log_path":            run.LogPath,
		"log_excerpt":         run.LogExcerpt,
		"error_message":       run.ErrorMessage,
		"set_name":            run.SetName,
		"set_file_name":       run.SetFileName,
		"source_db_name":      run.SourceDBName,
		"source_host":         run.SourceHost,
		"source_port":         run.SourcePort,
		"source_remark":       run.SourceRemark,
		"target_host":         run.TargetHost,
		"target_port":         run.TargetPort,
		"target_remark":       run.TargetRemark,
		"created_by":          run.CreatedBy,
		"duration_seconds":    calcRestoreDurationSeconds(run.StartAt, run.EndAt, run.Status),
	}
	return gin.H{
		"run":              runData,
		"items":            itemList,
		"item_statistics":  stats,
		"log_size_bytes":   logFileSize(run.LogPath),
		"is_running":       strings.EqualFold(strings.TrimSpace(run.Status), "RUNNING") || strings.EqualFold(strings.TrimSpace(run.Status), "PENDING"),
		"current_stage_ui": restoreStageLabel(run.CurrentStage),
	}, nil
}

type GetRestoreRunLogService struct {
	RestoreRunID uint64
	*backupForms.GetRestoreRunLogForm
	C *gin.Context
}

func (s *GetRestoreRunLogService) Run() (any, error) {
	type runRow struct {
		ID           uint64                 `json:"id"`
		Status       string                 `json:"status"`
		LogPath      string                 `json:"log_path"`
		LogExcerpt   string                 `json:"log_excerpt"`
		ErrorMessage string                 `json:"error_message"`
		UpdatedAt    commonModels.LocalTime `json:"updated_at"`
		LastReportAt commonModels.LocalTime `json:"last_report_at"`
	}
	var run runRow
	if err := global.App.DB.Table("insight_restore_runs").
		Select("id, status, log_path, log_excerpt, error_message, updated_at, last_report_at").
		Where("id = ?", s.RestoreRunID).
		Scan(&run).Error; err != nil {
		return nil, err
	}
	if run.ID == 0 {
		return nil, fmt.Errorf("恢复任务不存在")
	}

	logPath := strings.TrimSpace(run.LogPath)
	logType := "run"
	itemID := s.ItemID
	if itemID != 0 {
		type itemRow struct {
			ID           uint64                 `json:"id"`
			LogPath      string                 `json:"log_path"`
			LogExcerpt   string                 `json:"log_excerpt"`
			ErrorMessage string                 `json:"error_message"`
			Status       string                 `json:"status"`
			UpdatedAt    commonModels.LocalTime `json:"updated_at"`
			LastReportAt commonModels.LocalTime `json:"last_report_at"`
		}
		var item itemRow
		if err := global.App.DB.Table("insight_restore_run_items").
			Select("id, log_path, log_excerpt, error_message, status, updated_at, last_report_at").
			Where("id = ? and restore_run_id = ?", itemID, s.RestoreRunID).
			Scan(&item).Error; err != nil {
			return nil, err
		}
		if item.ID == 0 {
			return nil, fmt.Errorf("恢复对象日志不存在")
		}
		logPath = strings.TrimSpace(item.LogPath)
		run.LogExcerpt = item.LogExcerpt
		run.ErrorMessage = item.ErrorMessage
		run.Status = item.Status
		run.UpdatedAt = item.UpdatedAt
		run.LastReportAt = item.LastReportAt
		logType = "item"
	}

	content := strings.TrimSpace(run.LogExcerpt)
	readMode := "db_excerpt"
	if logPath != "" {
		if tail, readErr := readTailText(logPath, normalizeTailKB(s.TailKB)); readErr == nil && strings.TrimSpace(tail) != "" {
			content = tail
			readMode = "file_tail"
		}
	}
	if content == "" && strings.TrimSpace(run.ErrorMessage) != "" {
		content = run.ErrorMessage
	}
	if content == "" {
		content = "暂无日志内容。恢复可能尚未开始，或当前阶段暂未产生日志输出。"
	}
	return gin.H{
		"restore_run_id": run.ID,
		"item_id":        itemID,
		"log_type":       logType,
		"status":         run.Status,
		"log_path":       logPath,
		"log_excerpt":    content,
		"read_mode":      readMode,
		"error_message":  run.ErrorMessage,
		"updated_at":     run.UpdatedAt,
		"last_report_at": run.LastReportAt,
		"log_size_bytes": logFileSize(logPath),
		"is_running":     strings.EqualFold(strings.TrimSpace(run.Status), "RUNNING") || strings.EqualFold(strings.TrimSpace(run.Status), "PENDING"),
	}, nil
}

func restoreStageLabel(stage string) string {
	switch strings.ToUpper(strings.TrimSpace(stage)) {
	case restoreStagePending:
		return "排队中"
	case restoreStagePrecheck:
		return "预检查中"
	case restoreStagePrepareTarget:
		return "准备目标中"
	case restoreStagePrepareSource:
		return "读取备份集中"
	case restoreStageRestoring:
		return "恢复执行中"
	case restoreStageVerify:
		return "校验中"
	case restoreStageFinalize:
		return "收尾中"
	case restoreStageFinished:
		return "已完成"
	default:
		return "-"
	}
}

func calcRestoreDurationSeconds(startAt commonModels.LocalTime, endAt commonModels.LocalTime, status string) int64 {
	start := time.Time(startAt)
	if start.IsZero() {
		return 0
	}
	end := time.Time(endAt)
	if end.IsZero() && (strings.EqualFold(strings.TrimSpace(status), "RUNNING") || strings.EqualFold(strings.TrimSpace(status), "PENDING")) {
		end = nowLocalTime()
	}
	if end.IsZero() || end.Before(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

func calcRestoreHeartbeatAgeSeconds(last commonModels.LocalTime) int64 {
	ts := time.Time(last)
	if ts.IsZero() {
		return 0
	}
	return int64(nowLocalTime().Sub(ts) / time.Second)
}

func clampPercent(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

type restoreRunProgressPatch struct {
	Status          string
	CurrentStage    string
	ProgressPercent int
	CurrentObject   string
	LogPath         string
	ErrorMessage    string
	Finished        bool
}

func updateRestoreRunProgress(id uint64, patch restoreRunProgressPatch) {
	updates := map[string]any{
		"last_report_at":   commonModels.LocalTime(nowLocalTime()),
		"progress_percent": clampPercent(patch.ProgressPercent),
	}
	if strings.TrimSpace(patch.Status) != "" {
		updates["status"] = strings.TrimSpace(patch.Status)
	}
	if strings.TrimSpace(patch.CurrentStage) != "" {
		updates["current_stage"] = strings.TrimSpace(patch.CurrentStage)
	}
	if patch.CurrentObject != "" {
		updates["current_object"] = strings.TrimSpace(patch.CurrentObject)
	}
	if strings.TrimSpace(patch.LogPath) != "" {
		updates["log_path"] = strings.TrimSpace(patch.LogPath)
		updates["log_excerpt"] = tailLogExcerpt(strings.TrimSpace(patch.LogPath))
	}
	if strings.TrimSpace(patch.ErrorMessage) != "" {
		updates["error_message"] = strings.TrimSpace(patch.ErrorMessage)
	}
	if patch.Finished {
		updates["end_at"] = commonModels.LocalTime(nowLocalTime())
	}
	global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", id).Updates(updates)
}

type restoreItemPatch struct {
	Status          string
	CurrentStage    string
	ProgressPercent int
	LogPath         string
	ErrorMessage    string
	Started         bool
	Finished        bool
}

func createRestoreRunItem(item *backupModels.InsightRestoreRunItems) error {
	return global.App.DB.Create(item).Error
}

func updateRestoreRunItem(id uint64, patch restoreItemPatch) {
	updates := map[string]any{
		"last_report_at":   commonModels.LocalTime(nowLocalTime()),
		"progress_percent": clampPercent(patch.ProgressPercent),
	}
	if strings.TrimSpace(patch.Status) != "" {
		updates["status"] = strings.TrimSpace(patch.Status)
	}
	if strings.TrimSpace(patch.CurrentStage) != "" {
		updates["current_stage"] = strings.TrimSpace(patch.CurrentStage)
	}
	if strings.TrimSpace(patch.LogPath) != "" {
		updates["log_path"] = strings.TrimSpace(patch.LogPath)
		updates["log_excerpt"] = tailLogExcerpt(strings.TrimSpace(patch.LogPath))
	}
	if strings.TrimSpace(patch.ErrorMessage) != "" {
		updates["error_message"] = strings.TrimSpace(patch.ErrorMessage)
	}
	if patch.Started {
		updates["start_at"] = commonModels.LocalTime(nowLocalTime())
	}
	if patch.Finished {
		updates["end_at"] = commonModels.LocalTime(nowLocalTime())
	}
	global.App.DB.Model(&backupModels.InsightRestoreRunItems{}).Where("id = ?", id).Updates(updates)
}

func refreshRestoreRunFromItems(restoreRunID uint64, fallbackStage string, logPath string) {
	type itemAgg struct {
		Status          string `json:"status"`
		ProgressPercent int    `json:"progress_percent"`
		SourceObject    string `json:"source_object_name"`
		TargetObject    string `json:"target_object_name"`
	}
	var items []itemAgg
	if err := global.App.DB.Table("insight_restore_run_items").
		Select("status, progress_percent, source_object_name, target_object_name").
		Where("restore_run_id = ?", restoreRunID).
		Order("display_order asc, id asc").
		Scan(&items).Error; err != nil {
		return
	}
	if len(items) == 0 {
		updateRestoreRunProgress(restoreRunID, restoreRunProgressPatch{
			CurrentStage:    fallbackStage,
			ProgressPercent: 0,
			LogPath:         logPath,
		})
		return
	}
	avg := 0
	runningObjects := make([]string, 0, 2)
	for _, item := range items {
		avg += clampPercent(item.ProgressPercent)
		if strings.EqualFold(strings.TrimSpace(item.Status), "RUNNING") && len(runningObjects) < 2 {
			runningObjects = append(runningObjects, fmt.Sprintf("%s -> %s", item.SourceObject, item.TargetObject))
		}
	}
	avg = avg / len(items)
	currentObject := ""
	if len(runningObjects) > 0 {
		currentObject = strings.Join(runningObjects, "；")
	}
	updateRestoreRunProgress(restoreRunID, restoreRunProgressPatch{
		CurrentStage:    fallbackStage,
		ProgressPercent: avg,
		CurrentObject:   currentObject,
		LogPath:         logPath,
	})
}
