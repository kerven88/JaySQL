package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	physicalRestoreModeNewInstance      = "NEW_INSTANCE"
	physicalRestoreModeExistingInstance = "EXISTING_INSTANCE"

	physicalRestoreStagePending       = "PENDING"
	physicalRestoreStagePrecheck      = "PRECHECK"
	physicalRestoreStagePrepareTarget = "PREPARE_TARGET"
	physicalRestoreStageRestore       = "RESTORE_BASE"
	physicalRestoreStageStartInstance = "START_INSTANCE"
	physicalRestoreStagePostcheck     = "POSTCHECK"
	physicalRestoreStageFinished      = "FINISHED"
)

type physicalRestorePrecheckItem struct {
	ItemKey  string         `json:"item_key"`
	Label    string         `json:"label"`
	Passed   bool           `json:"passed"`
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	Detail   map[string]any `json:"detail,omitempty"`
}

type physicalRestoreResolved struct {
	TargetHost       string `json:"target_host"`
	TargetDataDir    string `json:"target_data_dir"`
	TargetPort       int    `json:"target_port"`
	TargetPGBinDir   string `json:"target_pg_bin_dir"`
	TargetPGCtl      string `json:"target_pg_ctl"`
	TargetPsql       string `json:"target_psql"`
	TargetPGBackRest string `json:"target_pgbackrest"`
	TargetPGVersion  string `json:"target_pg_version"`
	RepoHost         string `json:"repo_host"`
	RepoPath         string `json:"repo_path"`
	// RepoIsS3 为 true 表示仓库是 pgBackRest 原生对象存储仓库（repo1-type=s3），无仓库节点与本地目录。
	RepoIsS3      bool   `json:"repo_is_s3,omitempty"`
	Stanza        string `json:"stanza"`
	RunAsPostgres bool   `json:"run_as_postgres"`
	// 备份集定位（区分全量 / 非全量备份并精确指定恢复快照）
	RepoBackupCount  int    `json:"repo_backup_count,omitempty"`
	BackupLabel      string `json:"backup_label,omitempty"`
	BackupLabelType  string `json:"backup_label_type,omitempty"`
	BackupSetLocated bool   `json:"backup_set_located"`
	IsFullBackup     bool   `json:"is_full_backup,omitempty"`
}

type physicalRestoreContext struct {
	Form           *backupForms.PrecheckPhysicalRestoreForm
	BackupSet      *backupModels.InsightBackupSets
	TargetInstance *commonModels.InsightInstances
	TargetNode     *backupModels.InsightBackupNodes
	RepoNode       *backupModels.InsightBackupNodes
	RepoConfig     pgBackRestDriverConfig
	ArtifactMeta   physicalRestoreArtifactMeta
	RemoteConfig   *backupNodeRemoteConfig
	Resolved       physicalRestoreResolved
	PrecheckItems  []physicalRestorePrecheckItem
	Passed         bool
}

type physicalRestoreArtifactMeta struct {
	Stanza   string `json:"stanza"`
	RepoPath string `json:"repo_path"`
}

func (c *physicalRestoreContext) TargetInstanceID() uint64 {
	if c == nil || c.TargetInstance == nil {
		return 0
	}
	return c.TargetInstance.ID
}

type GetPhysicalRestoreRunsService struct {
	*backupForms.GetPhysicalRestoreRunsForm
	C *gin.Context
}

func (s *GetPhysicalRestoreRunsService) Run() (data interface{}, total int64, err error) {
	// 收口失联的 STOP_REQUESTED 物理恢复任务，与逻辑恢复列表同一口径。
	if _, cleanupErr := CleanupStaleRestoreStopRequestedRuns(); cleanupErr != nil {
		global.App.Log.Warnf("清理失联的终止中恢复任务失败: %v", cleanupErr)
	}
	type row struct {
		ID               uint64                 `json:"id"`
		TaskID           string                 `json:"task_id"`
		Name             string                 `json:"name"`
		BackupSetID      uint64                 `json:"backup_set_id"`
		BackupSetName    string                 `json:"backup_set_name"`
		DBType           string                 `json:"db_type"`
		DriverType       string                 `json:"driver_type"`
		SourceInstanceID uint64                 `json:"source_instance_id"`
		SourceHost       string                 `json:"source_host"`
		SourcePort       int                    `json:"source_port"`
		SourceRemark     string                 `json:"source_remark"`
		TargetInstanceID uint64                 `json:"target_instance_id"`
		TargetNodeID     uint64                 `json:"target_node_id"`
		TargetNodeName   string                 `json:"target_node_name"`
		TargetRemark     string                 `json:"target_remark"`
		TargetHost       string                 `json:"target_host"`
		TargetPort       int                    `json:"target_port"`
		ResolvedContext  datatypes.JSON         `json:"resolved_context"`
		Options          datatypes.JSON         `json:"options"`
		RestoreMode      string                 `json:"restore_mode"`
		Status           string                 `json:"status"`
		CurrentStage     string                 `json:"current_stage"`
		ProgressPercent  int                    `json:"progress_percent"`
		CurrentAction    string                 `json:"current_action"`
		ErrorMessage     string                 `json:"error_message"`
		CreatedBy        string                 `json:"created_by"`
		CreatedAt        commonModels.LocalTime `json:"created_at"`
		StartAt          commonModels.LocalTime `json:"start_at"`
		EndAt            commonModels.LocalTime `json:"end_at"`
		UpdatedAt        commonModels.LocalTime `json:"updated_at"`
		LastReportAt     commonModels.LocalTime `json:"last_report_at"`
	}
	var list []row
	tx := global.App.DB.Table("insight_restore_runs rr").
		Select(`rr.id, rr.task_id, rr.name, rr.backup_set_id, rr.db_type, rr.driver_type, rr.source_instance_id, rr.target_instance_id,
			rr.target_node_id, rr.target_host, rr.target_port, rr.restore_mode, rr.resolved_context, rr.options,
			rr.status, rr.current_stage, rr.progress_percent, rr.current_action, rr.error_message, rr.created_by,
			rr.created_at, rr.start_at, rr.end_at, rr.updated_at, rr.last_report_at,
			bs.name as backup_set_name,
			si.hostname as source_host, si.port as source_port, si.remark as source_remark,
			n.name as target_node_name, ti.remark as target_remark`).
		Joins("left join insight_backup_sets bs on bs.id = rr.backup_set_id").
		Joins("left join insight_instances si on si.id = rr.source_instance_id").
		Joins("left join insight_backup_nodes n on n.id = rr.target_node_id").
		Joins("left join insight_instances ti on ti.id = rr.target_instance_id").
		Where("rr.restore_kind = ? AND rr.driver_type IN ?", restoreKindPhysical, []string{"PG_BACKREST", "XTRABACKUP"}).
		Order("rr.id desc")
	if text := strings.TrimSpace(s.Search); text != "" {
		like := "%" + text + "%"
		tx = tx.Where(`rr.task_id like ? or rr.name like ? or bs.name like ? or rr.target_host like ? or rr.target_name like ? or
			n.name like ? or si.hostname like ? or si.remark like ? or ti.hostname like ? or ti.remark like ?`,
			like, like, like, like, like, like, like, like, like, like)
	}
	if text := strings.TrimSpace(s.Status); text != "" {
		tx = tx.Where("rr.status = ?", text)
	}
	if s.TargetNodeID != 0 {
		tx = tx.Where("rr.target_node_id = ?", s.TargetNodeID)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &list)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		resolved := decodePhysicalRestoreResolvedContextJSON(item.ResolvedContext)
		options := decodePhysicalRestoreOptionsJSON(item.Options)
		targetName := strings.TrimSpace(item.TargetRemark)
		if targetName == "" {
			targetName = strings.TrimSpace(item.TargetNodeName)
		}
		out = append(out, map[string]any{
			"id":                  item.ID,
			"task_id":             restoreTaskIDOrFallback(item.TaskID, item.ID),
			"name":                normalizeRestoreRunName(item.Name, item.BackupSetName, resolved.TargetDataDir),
			"backup_set_id":       item.BackupSetID,
			"backup_set_name":     item.BackupSetName,
			"db_type":             item.DBType,
			"driver_type":         item.DriverType,
			"source_instance_id":  item.SourceInstanceID,
			"source_host":         item.SourceHost,
			"source_port":         item.SourcePort,
			"source_remark":       item.SourceRemark,
			"target_instance_id":  item.TargetInstanceID,
			"target_node_id":      item.TargetNodeID,
			"target_node_name":    item.TargetNodeName,
			"target_name":         targetName,
			"target_remark":       item.TargetRemark,
			"target_host":         item.TargetHost,
			"target_data_dir":     resolved.TargetDataDir,
			"target_port":         item.TargetPort,
			"restore_mode":        item.RestoreMode,
			"restore_mode_label":  physicalRestoreModeLabel(item.RestoreMode),
			"auto_start":          options.AutoStart,
			"status":              item.Status,
			"status_label":        physicalRestoreStatusLabel(item.Status),
			"current_stage":       item.CurrentStage,
			"current_stage_label": physicalRestoreStageLabel(item.CurrentStage),
			"progress_percent":    clampPercent(item.ProgressPercent),
			"current_action":      item.CurrentAction,
			"error_message":       item.ErrorMessage,
			"created_by":          item.CreatedBy,
			"created_at":          item.CreatedAt,
			"start_at":            item.StartAt,
			"end_at":              item.EndAt,
			"updated_at":          item.UpdatedAt,
			"last_report_at":      item.LastReportAt,
			"duration_seconds":    calcPhysicalRestoreDurationSeconds(item.StartAt, item.EndAt, item.Status),
		})
	}
	return out, total, nil
}

type PrecheckPhysicalRestoreService struct {
	*backupForms.PrecheckPhysicalRestoreForm
	C *gin.Context
}

func (s *PrecheckPhysicalRestoreService) Run() (any, error) {
	// 按备份集驱动类型分流：MySQL XtraBackup 走专用恢复流程
	if set, loadErr := LoadBackupSet(s.BackupSetID); loadErr == nil &&
		strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL") && isMySQLPhysicalDriverType(string(set.DriverType)) {
		mysqlCtx, err := buildMySQLPhysicalRestoreContext(s.PrecheckPhysicalRestoreForm, s.C)
		if err != nil {
			return nil, err
		}
		return gin.H{
			"passed":   mysqlCtx.Passed,
			"summary":  summarizePhysicalRestorePrecheck(mysqlCtx.PrecheckItems),
			"items":    mysqlCtx.PrecheckItems,
			"resolved": mysqlCtx.Resolved,
		}, nil
	}
	ctx, err := buildPhysicalRestoreContext(s.PrecheckPhysicalRestoreForm, s.C)
	if err != nil {
		return nil, err
	}
	return gin.H{
		"passed":   ctx.Passed,
		"summary":  summarizePhysicalRestorePrecheck(ctx.PrecheckItems),
		"items":    ctx.PrecheckItems,
		"resolved": ctx.Resolved,
	}, nil
}

type CreatePhysicalRestoreRunService struct {
	*backupForms.CreatePhysicalRestoreRunForm
	C *gin.Context
}

func (s *CreatePhysicalRestoreRunService) Run() (*backupModels.InsightRestoreRuns, error) {
	// 物理恢复是整实例覆盖，破坏力大于逻辑恢复，授权同样不能缺：源备份集 export + 目标实例 dml。
	if err := CheckRestoreAccess(currentUsername(s.C), s.BackupSetID, s.TargetInstanceID, ""); err != nil {
		return nil, err
	}
	// MySQL XtraBackup 恢复走专用流程
	if set, loadErr := LoadBackupSet(s.BackupSetID); loadErr == nil &&
		strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL") && isMySQLPhysicalDriverType(string(set.DriverType)) {
		precheckForm := &backupForms.PrecheckPhysicalRestoreForm{
			BackupSetID:      s.BackupSetID,
			RestoreMode:      s.RestoreMode,
			TargetInstanceID: s.TargetInstanceID,
			TargetNodeID:     s.TargetNodeID,
			TargetDataDir:    s.TargetDataDir,
			TargetPort:       s.TargetPort,
			AutoStart:        s.AutoStart,
		}
		mysqlCtx, err := buildMySQLPhysicalRestoreContext(precheckForm, s.C)
		if err != nil {
			return nil, err
		}
		return createMySQLPhysicalRestoreRun(s, mysqlCtx)
	}
	precheckForm := &backupForms.PrecheckPhysicalRestoreForm{
		BackupSetID:      s.BackupSetID,
		RestoreMode:      s.RestoreMode,
		TargetInstanceID: s.TargetInstanceID,
		TargetNodeID:     s.TargetNodeID,
		TargetDataDir:    s.TargetDataDir,
		TargetPort:       s.TargetPort,
		AutoStart:        s.AutoStart,
	}
	ctx, err := buildPhysicalRestoreContext(precheckForm, s.C)
	if err != nil {
		return nil, err
	}
	if !ctx.Passed {
		return nil, fmt.Errorf("预检查未通过：%s", summarizePhysicalRestoreFailures(ctx.PrecheckItems))
	}
	precheckJSON, err := marshalJSONValue(gin.H{
		"summary":  summarizePhysicalRestorePrecheck(ctx.PrecheckItems),
		"items":    ctx.PrecheckItems,
		"resolved": ctx.Resolved,
	})
	if err != nil {
		return nil, err
	}
	autoStart := true
	if s.AutoStart != nil {
		autoStart = *s.AutoStart
	}
	resolvedJSON, err := buildPhysicalRestoreResolvedContextJSON(ctx.Resolved)
	if err != nil {
		return nil, err
	}
	optionsJSON, err := buildPhysicalRestoreOptionsJSON(autoStart)
	if err != nil {
		return nil, err
	}
	run := &backupModels.InsightRestoreRuns{
		Model:                &commonModels.Model{},
		TaskID:               generateRestoreTaskID(),
		Name:                 normalizeRestoreRunName(s.Name, ctx.BackupSet.Name, ctx.Resolved.TargetDataDir),
		BackupSetID:          ctx.BackupSet.ID,
		SourceInstanceID:     ctx.BackupSet.SourceInstanceID,
		DBType:               ctx.BackupSet.DBType,
		RestoreKind:          restoreKindPhysical,
		DriverType:           ctx.BackupSet.DriverType,
		RestoreMode:          commonModels.EnumType(strings.TrimSpace(s.RestoreMode)),
		TargetLevel:          restoreTargetLevelInstance,
		TargetInstanceID:     ctx.TargetInstanceID(),
		TargetNodeID:         ctx.TargetNode.ID,
		TargetName:           ctx.Resolved.TargetDataDir,
		TargetDBName:         "",
		TargetHost:           ctx.Resolved.TargetHost,
		TargetPort:           ctx.Resolved.TargetPort,
		DriverConfigSnapshot: ctx.BackupSet.DriverConfig,
		ResolvedContext:      resolvedJSON,
		Options:              optionsJSON,
		Status:               "PENDING",
		CurrentStage:         physicalRestoreStagePending,
		ProgressPercent:      0,
		CurrentObject:        "",
		CurrentAction:        "等待执行",
		RiskLevel:            "HIGH",
		PrecheckSummary:      precheckJSON,
		CreatedBy:            currentUsername(s.C),
	}
	if err := global.App.DB.Create(run).Error; err != nil {
		return nil, err
	}
	go executePhysicalRestoreRun(run.ID)
	return run, nil
}

type GetPhysicalRestoreRunDetailService struct {
	PhysicalRestoreRunID uint64
	C                    *gin.Context
}

func (s *GetPhysicalRestoreRunDetailService) Run() (any, error) {
	if _, cleanupErr := cleanupStaleRestoreStopRequestedRun(s.PhysicalRestoreRunID); cleanupErr != nil {
		global.App.Log.Warnf("清理失联的终止中恢复任务失败: %v", cleanupErr)
	}
	type row struct {
		ID               uint64                 `json:"id"`
		TaskID           string                 `json:"task_id"`
		Name             string                 `json:"name"`
		BackupSetID      uint64                 `json:"backup_set_id"`
		BackupSetName    string                 `json:"backup_set_name"`
		DBType           string                 `json:"db_type"`
		DriverType       string                 `json:"driver_type"`
		SourceInstanceID uint64                 `json:"source_instance_id"`
		SourceHost       string                 `json:"source_host"`
		SourcePort       int                    `json:"source_port"`
		SourceRemark     string                 `json:"source_remark"`
		TargetInstanceID uint64                 `json:"target_instance_id"`
		TargetNodeID     uint64                 `json:"target_node_id"`
		TargetNodeName   string                 `json:"target_node_name"`
		TargetRemark     string                 `json:"target_remark"`
		TargetHost       string                 `json:"target_host"`
		TargetPort       int                    `json:"target_port"`
		RestoreMode      string                 `json:"restore_mode"`
		ResolvedContext  datatypes.JSON         `json:"resolved_context"`
		Options          datatypes.JSON         `json:"options"`
		Status           string                 `json:"status"`
		CurrentStage     string                 `json:"current_stage"`
		ProgressPercent  int                    `json:"progress_percent"`
		CurrentAction    string                 `json:"current_action"`
		RiskLevel        string                 `json:"risk_level"`
		PrecheckSummary  datatypes.JSON         `json:"precheck_summary"`
		ResultSummary    datatypes.JSON         `json:"result_summary"`
		StartAt          commonModels.LocalTime `json:"start_at"`
		EndAt            commonModels.LocalTime `json:"end_at"`
		LastReportAt     commonModels.LocalTime `json:"last_report_at"`
		LogPath          string                 `json:"log_path"`
		LogExcerpt       string                 `json:"log_excerpt"`
		ErrorMessage     string                 `json:"error_message"`
		CreatedBy        string                 `json:"created_by"`
		CreatedAt        commonModels.LocalTime `json:"created_at"`
		UpdatedAt        commonModels.LocalTime `json:"updated_at"`
		BackupSetsID     string                 `json:"backup_sets_id"`
	}
	var run row
	if err := global.App.DB.Table("insight_restore_runs rr").
		Select(`rr.id, rr.task_id, rr.name, rr.backup_set_id, rr.db_type, rr.driver_type, rr.source_instance_id, rr.target_instance_id,
			rr.target_node_id, rr.target_host, rr.target_port, rr.restore_mode, rr.resolved_context, rr.options, rr.status, rr.current_stage,
			rr.progress_percent, rr.current_action, rr.risk_level, rr.precheck_summary, rr.result_summary, rr.start_at, rr.end_at,
			rr.last_report_at, rr.log_path, rr.log_excerpt, rr.error_message, rr.created_by, rr.created_at, rr.updated_at,
			bs.name as backup_set_name, bs.backup_sets_id as backup_sets_id, si.hostname as source_host, si.port as source_port, si.remark as source_remark,
			n.name as target_node_name, ti.remark as target_remark`).
		Joins("left join insight_backup_sets bs on bs.id = rr.backup_set_id").
		Joins("left join insight_instances si on si.id = rr.source_instance_id").
		Joins("left join insight_backup_nodes n on n.id = rr.target_node_id").
		Joins("left join insight_instances ti on ti.id = rr.target_instance_id").
		Where("rr.id = ? AND rr.restore_kind = ? AND rr.driver_type IN ?", s.PhysicalRestoreRunID, restoreKindPhysical, []string{"PG_BACKREST", "XTRABACKUP"}).
		Scan(&run).Error; err != nil {
		return nil, err
	}
	if run.ID == 0 {
		return nil, fmt.Errorf("物理恢复任务不存在")
	}
	if strings.TrimSpace(run.LogExcerpt) == "" {
		run.LogExcerpt = tailLogExcerpt(run.LogPath)
	}
	resolved := decodePhysicalRestoreResolvedContextJSON(run.ResolvedContext)
	options := decodePhysicalRestoreOptionsJSON(run.Options)
	targetName := strings.TrimSpace(run.TargetRemark)
	if targetName == "" {
		targetName = strings.TrimSpace(run.TargetNodeName)
	}
	resolvedExtra := map[string]any{}
	if strings.EqualFold(strings.TrimSpace(run.DriverType), "XTRABACKUP") {
		mysqlResolved := decodeMySQLPhysicalRestoreResolvedContextJSON(run.ResolvedContext)
		resolvedExtra = map[string]any{
			"target_version":     mysqlResolved.TargetVersion,
			"source_version":     mysqlResolved.SourceVersion,
			"xtrabackup_path":    mysqlResolved.XtraBackupPath,
			"xtrabackup_version": mysqlResolved.XtraBackupVersion,
			"service_unit":       mysqlResolved.ServiceUnit,
			"mysql_os_user":      mysqlResolved.MysqlOSUser,
			"work_dir":           mysqlResolved.WorkDir,
		}
	}
	return gin.H{
		"run": gin.H{
			"id":                  run.ID,
			"task_id":             restoreTaskIDOrFallback(run.TaskID, run.ID),
			"name":                normalizeRestoreRunName(run.Name, run.BackupSetName, resolved.TargetDataDir),
			"backup_set_id":       run.BackupSetID,
			"backup_sets_id":      run.BackupSetsID,
			"backup_set_name":     run.BackupSetName,
			"db_type":             run.DBType,
			"driver_type":         run.DriverType,
			"source_instance_id":  run.SourceInstanceID,
			"source_host":         run.SourceHost,
			"source_port":         run.SourcePort,
			"source_remark":       run.SourceRemark,
			"target_instance_id":  run.TargetInstanceID,
			"target_node_id":      run.TargetNodeID,
			"target_node_name":    run.TargetNodeName,
			"target_name":         targetName,
			"target_remark":       run.TargetRemark,
			"target_host":         run.TargetHost,
			"target_pg_bin_dir":   resolved.TargetPGBinDir,
			"target_pg_version":   resolved.TargetPGVersion,
			"target_data_dir":     resolved.TargetDataDir,
			"target_port":         run.TargetPort,
			"restore_mode":        run.RestoreMode,
			"restore_mode_label":  physicalRestoreModeLabel(run.RestoreMode),
			"stanza":              resolved.Stanza,
			"repo_host":           resolved.RepoHost,
			"repo_path":           resolved.RepoPath,
			"resolved_extra":      resolvedExtra,
			"auto_start":          options.AutoStart,
			"status":              run.Status,
			"status_label":        physicalRestoreStatusLabel(run.Status),
			"current_stage":       run.CurrentStage,
			"current_stage_label": physicalRestoreStageLabel(run.CurrentStage),
			"progress_percent":    clampPercent(run.ProgressPercent),
			"current_action":      run.CurrentAction,
			"risk_level":          run.RiskLevel,
			"precheck_summary":    decodeJSONValue(run.PrecheckSummary),
			"result_summary":      decodeJSONValue(run.ResultSummary),
			"start_at":            run.StartAt,
			"end_at":              run.EndAt,
			"last_report_at":      run.LastReportAt,
			"log_path":            run.LogPath,
			"log_excerpt":         run.LogExcerpt,
			"error_message":       run.ErrorMessage,
			"created_by":          run.CreatedBy,
			"created_at":          run.CreatedAt,
			"updated_at":          run.UpdatedAt,
			"duration_seconds":    calcPhysicalRestoreDurationSeconds(run.StartAt, run.EndAt, run.Status),
		},
		"log_size_bytes": logFileSize(run.LogPath),
	}, nil
}

type GetPhysicalRestoreRunLogService struct {
	PhysicalRestoreRunID uint64
	*backupForms.GetPhysicalRestoreRunLogForm
	C *gin.Context
}

func (s *GetPhysicalRestoreRunLogService) Run() (any, error) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type = ?", s.PhysicalRestoreRunID, restoreKindPhysical, "PG_BACKREST").First(&run).Error; err != nil {
		return nil, err
	}
	tailKB := s.TailKB
	if tailKB <= 0 {
		tailKB = 64
	}
	content, err := readTailText(run.LogPath, tailKB)
	if err != nil {
		content = ""
	}
	return gin.H{
		"status":         run.Status,
		"current_stage":  run.CurrentStage,
		"log_path":       run.LogPath,
		"log_size_bytes": logFileSize(run.LogPath),
		"content":        content,
		"error_message":  run.ErrorMessage,
		"read_mode":      fmt.Sprintf("tail-%dkb", tailKB),
	}, nil
}

type UpdatePhysicalRestoreRunService struct {
	PhysicalRestoreRunID uint64
	*backupForms.UpdatePhysicalRestoreRunForm
	C *gin.Context
}

func (s *UpdatePhysicalRestoreRunService) Run() (*backupModels.InsightRestoreRuns, error) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type = ?", s.PhysicalRestoreRunID, restoreKindPhysical, "PG_BACKREST").First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("物理恢复任务不存在")
		}
		return nil, err
	}
	status := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	currentStage := strings.ToUpper(strings.TrimSpace(run.CurrentStage))
	if status != "PENDING" && !(status == "FAILED" && currentStage == physicalRestoreStagePrecheck) {
		return nil, fmt.Errorf("仅未开始或预检查失败的物理恢复任务支持编辑")
	}
	precheckForm := &backupForms.PrecheckPhysicalRestoreForm{
		BackupSetID:      s.BackupSetID,
		RestoreMode:      s.RestoreMode,
		TargetInstanceID: s.TargetInstanceID,
		TargetNodeID:     s.TargetNodeID,
		TargetDataDir:    s.TargetDataDir,
		TargetPort:       s.TargetPort,
		AutoStart:        s.AutoStart,
	}
	ctx, err := buildPhysicalRestoreContext(precheckForm, s.C)
	if err != nil {
		return nil, err
	}
	if !ctx.Passed {
		return nil, fmt.Errorf("预检查未通过：%s", summarizePhysicalRestoreFailures(ctx.PrecheckItems))
	}
	precheckJSON, err := marshalJSONValue(gin.H{
		"summary":  summarizePhysicalRestorePrecheck(ctx.PrecheckItems),
		"items":    ctx.PrecheckItems,
		"resolved": ctx.Resolved,
	})
	if err != nil {
		return nil, err
	}
	autoStart := true
	if s.AutoStart != nil {
		autoStart = *s.AutoStart
	}
	resolvedJSON, err := buildPhysicalRestoreResolvedContextJSON(ctx.Resolved)
	if err != nil {
		return nil, err
	}
	optionsJSON, err := buildPhysicalRestoreOptionsJSON(autoStart)
	if err != nil {
		return nil, err
	}
	zeroTime := commonModels.LocalTime(time.Time{})
	updates := map[string]any{
		"name":                   normalizeRestoreRunName(s.Name, ctx.BackupSet.Name, ctx.Resolved.TargetDataDir),
		"backup_set_id":          ctx.BackupSet.ID,
		"source_instance_id":     ctx.BackupSet.SourceInstanceID,
		"db_type":                ctx.BackupSet.DBType,
		"restore_kind":           restoreKindPhysical,
		"driver_type":            ctx.BackupSet.DriverType,
		"restore_mode":           strings.TrimSpace(s.RestoreMode),
		"target_level":           restoreTargetLevelInstance,
		"target_instance_id":     ctx.TargetInstanceID(),
		"target_node_id":         ctx.TargetNode.ID,
		"target_name":            ctx.Resolved.TargetDataDir,
		"target_db_name":         "",
		"target_host":            ctx.Resolved.TargetHost,
		"target_port":            ctx.Resolved.TargetPort,
		"driver_config_snapshot": ctx.BackupSet.DriverConfig,
		"resolved_context":       resolvedJSON,
		"options":                optionsJSON,
		"status":                 "PENDING",
		"current_stage":          physicalRestoreStagePending,
		"progress_percent":       0,
		"current_object":         "",
		"current_action":         "等待执行",
		"risk_level":             "HIGH",
		"precheck_summary":       precheckJSON,
		"result_summary":         nil,
		"start_at":               zeroTime,
		"end_at":                 zeroTime,
		"last_report_at":         zeroTime,
		"log_excerpt":            "",
		"error_message":          "",
	}
	if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ? AND restore_kind = ?", run.ID, restoreKindPhysical).Updates(updates).Error; err != nil {
		return nil, err
	}
	go executePhysicalRestoreRun(run.ID)
	var updated backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type = ?", run.ID, restoreKindPhysical, "PG_BACKREST").First(&updated).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

type StopPhysicalRestoreRunService struct {
	C                    *gin.Context
	PhysicalRestoreRunID uint64
}

func (s *StopPhysicalRestoreRunService) Run() (map[string]any, error) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type IN ?", s.PhysicalRestoreRunID, restoreKindPhysical, []string{"PG_BACKREST", "XTRABACKUP"}).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("物理恢复任务不存在")
		}
		return nil, err
	}
	operator := strings.TrimSpace(currentUsername(s.C))
	if operator == "" {
		operator = "system"
	}
	stopReason := resolveRestoreStopReason(operator)
	switch strings.ToUpper(strings.TrimSpace(string(run.Status))) {
	case "SUCCESS", "FAILED", "STOPPED":
		return nil, fmt.Errorf("当前物理恢复任务已结束，无需终止")
	case "STOP_REQUESTED":
		// 与逻辑恢复同口径：协程仍在收尾则保持「终止中」，
		// 已失联则按操作员的再次点击强制收口，避免任务永久停在这个无出口的中间态。
		if restoreStopRequestStale(run.LastReportAt, run.UpdatedAt) {
			appendBackupRunLog(run.LogPath, "WARN", "终止请求超过阈值仍未收口，已按操作人=%s 的请求强制收口", operator)
			if _, err := finalizeStaleRestoreStopRequest(run.ID, stopReason, run.LogPath); err != nil {
				return nil, err
			}
			return map[string]any{
				"restore_run_id":     run.ID,
				"status":             "STOPPED",
				"stop_state":         "IMMEDIATE",
				"stop_state_label":   "已终止",
				"stop_state_message": "恢复进程已无响应，任务已强制收口",
			}, nil
		}
		return map[string]any{
			"restore_run_id":     run.ID,
			"status":             run.Status,
			"stop_state":         "WAITING",
			"stop_state_label":   "终止中",
			"stop_state_message": "已记录终止请求，正在等待恢复进程退出",
		}, nil
	case "PENDING":
		appendBackupRunLog(run.LogPath, "WARN", "收到终止请求，物理恢复任务尚未开始执行，已直接停止，操作人=%s", operator)
		if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ? AND restore_kind = ?", run.ID, restoreKindPhysical).Updates(map[string]any{
			"status":         "STOPPED",
			"end_at":         commonModels.LocalTime(nowLocalTime()),
			"current_action": "已终止",
			"error_message":  stopReason,
			"log_excerpt":    tailLogExcerpt(run.LogPath),
		}).Error; err != nil {
			return nil, err
		}
		return map[string]any{
			"restore_run_id":     run.ID,
			"status":             "STOPPED",
			"stop_state":         "IMMEDIATE",
			"stop_state_label":   "已终止",
			"stop_state_message": "当前任务尚未开始执行，已直接终止",
		}, nil
	case "RUNNING":
		appendBackupRunLog(run.LogPath, "WARN", "收到终止请求，正在中止当前物理恢复进程，操作人=%s", operator)
		if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ? AND restore_kind = ?", run.ID, restoreKindPhysical).Updates(map[string]any{
			"status":         "STOP_REQUESTED",
			"current_action": "终止中",
			"error_message":  stopReason,
			"log_excerpt":    tailLogExcerpt(run.LogPath),
		}).Error; err != nil {
			return nil, err
		}
		return map[string]any{
			"restore_run_id":     run.ID,
			"status":             "STOP_REQUESTED",
			"stop_state":         "WAITING",
			"stop_state_label":   "终止中",
			"stop_state_message": "已发送终止请求，正在等待恢复进程退出",
		}, nil
	default:
		return nil, fmt.Errorf("当前物理恢复状态不支持终止")
	}
}

type DeletePhysicalRestoreRunService struct {
	C                    *gin.Context
	PhysicalRestoreRunID uint64
}

func (s *DeletePhysicalRestoreRunService) Run() error {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type IN ?", s.PhysicalRestoreRunID, restoreKindPhysical, []string{"PG_BACKREST", "XTRABACKUP"}).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("物理恢复任务不存在")
		}
		return err
	}
	status := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	// 物理侧刻意不对 PENDING 放宽：物理恢复在落库后立刻起执行协程
	// （CreatePhysicalRestoreRunService 末尾的 go executePhysicalRestoreRun），
	// 它的 PENDING 一定是「真排队」，不存在逻辑侧那种无人接管的计划态残留。
	if status == "RUNNING" || status == "PENDING" || status == "STOP_REQUESTED" {
		return fmt.Errorf("运行中的物理恢复任务暂不支持删除，请先终止任务")
	}
	return global.App.DB.Delete(&backupModels.InsightRestoreRuns{}, run.ID).Error
}

func buildPhysicalRestoreContext(form *backupForms.PrecheckPhysicalRestoreForm, c *gin.Context) (*physicalRestoreContext, error) {
	set, err := LoadBackupSet(form.BackupSetID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(string(set.DBType)), "PostgreSQL") || !isPostgresPhysicalDriverType(string(set.DriverType)) {
		return nil, fmt.Errorf("当前仅支持 PostgreSQL PG_BACKREST 物理备份恢复")
	}
	cfg, err := loadPGBackRestDriverConfig(set.DriverConfig)
	if err != nil {
		return nil, err
	}
	ctx := &physicalRestoreContext{
		Form:       form,
		BackupSet:  set,
		RepoConfig: cfg,
		Passed:     false,
	}
	if artifactPath, artifactErr := ValidateBackupSetFile(set); artifactErr == nil {
		if meta, metaErr := loadPhysicalRestoreArtifactMeta(artifactPath); metaErr == nil {
			ctx.ArtifactMeta = meta
		}
	} else if meta, metaErr := loadPhysicalRestoreArtifactMeta(strings.TrimSpace(set.FilePath)); metaErr == nil {
		ctx.ArtifactMeta = meta
	}
	if form.TargetInstanceID > 0 {
		instance, loadErr := loadInstanceByID(form.TargetInstanceID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "PostgreSQL") {
			return nil, fmt.Errorf("目标实例必须是 PostgreSQL")
		}
		targetNode, nodeErr := loadBackupNodeByHost(instance.Hostname)
		if nodeErr != nil {
			return nil, nodeErr
		}
		ctx.TargetInstance = instance
		ctx.TargetNode = targetNode
	} else {
		if form.TargetNodeID == 0 {
			return nil, fmt.Errorf("请选择目标节点")
		}
		targetNode, nodeErr := loadBackupNodeByID(form.TargetNodeID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		ctx.TargetNode = targetNode
	}
	remoteCfg, err := buildBackupNodeRemoteConfigForRestore(ctx.TargetNode, c)
	if err != nil {
		return nil, err
	}
	ctx.RemoteConfig = remoteCfg
	isS3Repo := physicalRestoreRepoIsS3(set, cfg)
	ctx.Resolved.RepoIsS3 = isS3Repo
	if set.RepositoryNodeID > 0 {
		repoNode, repoErr := loadBackupNodeByID(set.RepositoryNodeID)
		if repoErr == nil {
			ctx.RepoNode = repoNode
		}
	} else if set.StorageTargetID > 0 && !isS3Repo {
		virtualRepo, virtualErr := resolveVirtualRepoNodeFromStorageTarget(set.StorageTargetID)
		if virtualErr == nil && virtualRepo != nil {
			ctx.RepoNode = virtualRepo
			if strings.TrimSpace(cfg.RepoHost) == "" {
				cfg.RepoHost = virtualRepo.Host
				cfg.RepoPort = virtualRepo.Port
				cfg.RepoUser = defaultBackupNodeSSHUser(virtualRepo.SSHUser)
				cfg.RepoRemote = !sameBackupNodeHost(ctx.TargetNode, virtualRepo)
			}
		}
	}
	ctx.Resolved.Stanza = resolvePhysicalRestoreStanza(set, cfg, ctx.ArtifactMeta)
	ctx.Resolved.RepoPath = resolvePhysicalRestoreRepoPath(set, cfg, ctx.ArtifactMeta)
	if isS3Repo && strings.TrimSpace(ctx.Resolved.RepoPath) == "" {
		// 对象存储仓库：未显式配置前缀时按 bucket 根读写（pgBackRest repo1-path 默认即为 /）。
		ctx.Resolved.RepoPath = "/"
	}
	if host := strings.TrimSpace(cfg.RepoHost); host != "" {
		ctx.Resolved.RepoHost = host
	} else if ctx.RepoNode != nil {
		ctx.Resolved.RepoHost = strings.TrimSpace(ctx.RepoNode.Host)
	}
	ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
		ItemKey:  "backup_set_valid",
		Label:    "备份集类型校验",
		Passed:   true,
		Severity: "INFO",
		Message:  fmt.Sprintf("备份集 %s 可用于 PostgreSQL 物理恢复。", displayOrDefault(set.Name, fmt.Sprintf("%d", set.ID))),
		Detail: map[string]any{
			"driver_type": set.DriverType,
			"db_type":     set.DBType,
		},
	})
	if ctx.Resolved.Stanza == "" {
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey:  "stanza_exists",
			Label:    "stanza 校验",
			Passed:   false,
			Severity: "ERROR",
			Message:  "备份集缺少 pgBackRest stanza 信息，无法执行平台内恢复。",
		})
		return finalizePhysicalRestoreContext(ctx), nil
	}
	if ctx.Resolved.RepoPath == "" {
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey:  "repo_path_valid",
			Label:    "仓库路径校验",
			Passed:   false,
			Severity: "ERROR",
			Message:  "备份集缺少仓库路径信息，无法执行平台内恢复。",
		})
		return finalizePhysicalRestoreContext(ctx), nil
	}
	if !cfg.RepoRemote && set.RepositoryNodeID > 0 && set.RepositoryNodeID != ctx.TargetNode.ID {
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey:  "repo_topology_supported",
			Label:    "恢复拓扑校验",
			Passed:   false,
			Severity: "ERROR",
			Message:  "当前 MVP 仅支持恢复到仓库节点本机，或备份任务已配置远端仓库。",
			Detail: map[string]any{
				"repository_node_id": set.RepositoryNodeID,
				"target_node_id":     ctx.TargetNode.ID,
			},
		})
		return finalizePhysicalRestoreContext(ctx), nil
	}
	runner, err := newSSHRunner(remoteCfg)
	if err != nil {
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey:  "target_connectivity",
			Label:    "目标节点连接",
			Passed:   false,
			Severity: "ERROR",
			Message:  fmt.Sprintf("无法连接目标节点: %v", err),
		})
		return finalizePhysicalRestoreContext(ctx), nil
	}
	defer runner.Close()
	remoteOutput, remoteErr := runner.Run(buildPhysicalRestorePrecheckScript(ctx))
	if remoteErr != nil {
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey:  "target_runtime_check",
			Label:    "目标节点环境检查",
			Passed:   false,
			Severity: "ERROR",
			Message:  fmt.Sprintf("目标节点环境检查失败: %v", remoteErr),
			Detail:   map[string]any{"output": remoteOutput},
		})
		return finalizePhysicalRestoreContext(ctx), nil
	}
	parsed := parsePhysicalRestoreKeyValues(remoteOutput)
	ctx.Resolved.TargetHost = firstNonEmpty(parsed["TARGET_HOST"], strings.TrimSpace(ctx.TargetNode.Host))
	ctx.Resolved.TargetDataDir = firstNonEmpty(parsed["TARGET_DATA_DIR"], strings.TrimSpace(form.TargetDataDir))
	if portValue, _ := strconv.Atoi(strings.TrimSpace(parsed["TARGET_PORT"])); portValue > 0 {
		ctx.Resolved.TargetPort = portValue
	}
	if ctx.Resolved.TargetPort <= 0 {
		if form.TargetPort > 0 {
			ctx.Resolved.TargetPort = form.TargetPort
		} else if ctx.TargetInstance != nil && ctx.TargetInstance.Port > 0 {
			ctx.Resolved.TargetPort = ctx.TargetInstance.Port
		}
	}
	ctx.Resolved.TargetPGBinDir = parsed["TARGET_PG_BIN_DIR"]
	ctx.Resolved.TargetPGCtl = parsed["TARGET_PG_CTL"]
	ctx.Resolved.TargetPsql = parsed["TARGET_PSQL"]
	ctx.Resolved.TargetPGBackRest = parsed["TARGET_PGBACKREST"]
	ctx.Resolved.TargetPGVersion = parsed["TARGET_PG_VERSION"]
	ctx.Resolved.RunAsPostgres = strings.EqualFold(parsed["RUN_AS_POSTGRES"], "true")
	// 备份集定位：区分全量 / 非全量备份，恢复阶段据此精确指定 --set 快照。
	ctx.Resolved.IsFullBackup = strings.EqualFold(strings.TrimSpace(string(ctx.BackupSet.BackupType)), "FULL")
	if label, labelType, count, located := locatePGBackRestBackupLabel(ctx.BackupSet, ctx.Resolved.Stanza, parsePGBackRestInfoB64(parsed["PGBACKREST_INFO_B64"])); located {
		ctx.Resolved.BackupLabel = label
		ctx.Resolved.BackupLabelType = labelType
		ctx.Resolved.BackupSetLocated = true
		ctx.Resolved.RepoBackupCount = count
		ctx.Resolved.IsFullBackup = labelType == "full"
	} else if countValue := len(parsePGBackRestInfoB64(parsed["PGBACKREST_INFO_B64"])); countValue > 0 {
		ctx.Resolved.RepoBackupCount = countValue
	}
	locateDetail := map[string]any{
		"backup_type":  strings.TrimSpace(string(ctx.BackupSet.BackupType)),
		"label":        ctx.Resolved.BackupLabel,
		"label_type":   ctx.Resolved.BackupLabelType,
		"repo_backups": ctx.Resolved.RepoBackupCount,
	}
	locateMessage := "备份集为全量备份，将恢复到该全量快照。"
	if !ctx.Resolved.IsFullBackup {
		locateMessage = "备份集为非全量备份，将恢复到该增量快照（自动回放所需基线）。"
	}
	if !ctx.Resolved.BackupSetLocated {
		locateMessage = "未能从仓库定位到对应备份集，将恢复到仓库内最新备份，请人工确认是否符合预期。"
	}
	repoAccessPassed := strings.EqualFold(parsed["REPO_READABLE_AS_POSTGRES"], "true")
	repoAccessMessage := ternaryString(
		repoAccessPassed,
		fmt.Sprintf("恢复仓库目录 %s 可由 postgres 用户读取。", displayOrDefault(ctx.Resolved.RepoPath, "-")),
		fmt.Sprintf("恢复仓库目录 %s 当前不可由 postgres 用户读取，请检查目录权限、属组或 ACL。", displayOrDefault(ctx.Resolved.RepoPath, "-")),
	)
	repoAccessDetail := map[string]any{
		"repo_path": ctx.Resolved.RepoPath,
	}
	if ctx.Resolved.RepoIsS3 {
		// 对象存储仓库无「本地目录可读」概念，仓库访问由 S3 AK/SK + 网络连通性决定（由 pgbackrest 在执行期校验）。
		repoAccessMessage = "仓库为对象存储，AK/SK 凭据与桶访问权限由 pgBackRest S3 客户端在执行期校验。"
		repoAccessDetail["repo_type"] = "s3"
	}
	ctx.PrecheckItems = append(ctx.PrecheckItems,
		physicalRestorePrecheckItem{
			ItemKey:  "backup_set_locate",
			Label:    "恢复备份集定位",
			Passed:   ctx.Resolved.BackupSetLocated,
			Severity: "INFO",
			Message:  locateMessage,
			Detail:   locateDetail,
		},
		physicalRestorePrecheckItem{
			ItemKey:  "target_connectivity",
			Label:    "目标节点连接",
			Passed:   true,
			Severity: "INFO",
			Message:  fmt.Sprintf("已连接目标节点 %s。", ctx.Resolved.TargetHost),
		},
		physicalRestorePrecheckItem{
			ItemKey:  "pgbackrest_detected",
			Label:    "pgBackRest 检测",
			Passed:   parsed["TARGET_PGBACKREST"] != "",
			Severity: "ERROR",
			Message:  ternaryString(parsed["TARGET_PGBACKREST"] != "", fmt.Sprintf("已检测到 pgBackRest: %s", parsed["TARGET_PGBACKREST"]), "目标节点未检测到 pgBackRest。"),
		},
		physicalRestorePrecheckItem{
			ItemKey:  "pg_ctl_detected",
			Label:    "PostgreSQL 工具检测",
			Passed:   parsed["TARGET_PG_CTL"] != "",
			Severity: "ERROR",
			Message:  ternaryString(parsed["TARGET_PG_CTL"] != "", fmt.Sprintf("已检测到 pg_ctl: %s", parsed["TARGET_PG_CTL"]), "目标节点未检测到 pg_ctl。"),
		},
		physicalRestorePrecheckItem{
			ItemKey:  "run_as_postgres",
			Label:    "postgres 权限校验",
			Passed:   strings.EqualFold(parsed["RUN_AS_POSTGRES"], "true"),
			Severity: "ERROR",
			Message:  ternaryString(strings.EqualFold(parsed["RUN_AS_POSTGRES"], "true"), "可切换为 postgres 用户执行恢复。", "当前节点无法切换为 postgres 用户执行恢复。"),
		},
		physicalRestorePrecheckItem{
			ItemKey:  "repo_readable_as_postgres",
			Label:    "仓库访问校验",
			Passed:   repoAccessPassed,
			Severity: "ERROR",
			Message:  repoAccessMessage,
			Detail:   repoAccessDetail,
		},
	)
	if ctx.Resolved.RepoIsS3 {
		repoInfoOK := strings.EqualFold(parsed["REPO_INFO_OK"], "true")
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey:  "object_storage_repo_reachable",
			Label:    "对象存储仓库读取",
			Passed:   repoInfoOK,
			Severity: "INFO",
			Message: ternaryString(
				repoInfoOK,
				"已从目标节点成功读取对象存储仓库信息。",
				"未能从目标节点读取对象存储仓库信息（凭据不可用或出网受阻），恢复可能失败，请先确认 OSS 凭据与目标主机出网策略。",
			),
			Detail: map[string]any{
				"repo_path":    ctx.Resolved.RepoPath,
				"repo_type":    "s3",
				"repo_info_ok": repoInfoOK,
			},
		})
	}
	if ctx.TargetInstance != nil {
		ctx.PrecheckItems = append(ctx.PrecheckItems,
			physicalRestorePrecheckItem{
				ItemKey:  "target_instance_detected",
				Label:    "目标实例识别",
				Passed:   ctx.Resolved.TargetDataDir != "",
				Severity: "ERROR",
				Message:  ternaryString(ctx.Resolved.TargetDataDir != "", fmt.Sprintf("已识别目标实例 %s:%d 的数据目录。", ctx.TargetInstance.Hostname, ctx.TargetInstance.Port), "无法识别目标实例的数据目录，请检查实例连通和本机 psql 工具。"),
			},
			physicalRestorePrecheckItem{
				ItemKey:  "target_dir_exists",
				Label:    "目标实例目录",
				Passed:   strings.EqualFold(parsed["TARGET_DIR_EXISTS"], "true"),
				Severity: "ERROR",
				Message:  ternaryString(strings.EqualFold(parsed["TARGET_DIR_EXISTS"], "true"), "目标实例数据目录存在，可用于实例级恢复。", "目标实例数据目录不存在或不可访问。"),
				Detail:   map[string]any{"target_data_dir": ctx.Resolved.TargetDataDir},
			},
			physicalRestorePrecheckItem{
				ItemKey:  "target_port_runtime",
				Label:    "目标实例端口状态",
				Passed:   true,
				Severity: "INFO",
				Message:  ternaryString(strings.EqualFold(parsed["TARGET_INSTANCE_RUNNING"], "true"), fmt.Sprintf("实例端口 %d 正在监听，恢复时会先停库。", ctx.Resolved.TargetPort), fmt.Sprintf("实例端口 %d 当前未监听，恢复后将直接拉起。", ctx.Resolved.TargetPort)),
			},
		)
	} else {
		ctx.PrecheckItems = append(ctx.PrecheckItems,
			physicalRestorePrecheckItem{
				ItemKey:  "target_dir_empty",
				Label:    "目标目录校验",
				Passed:   strings.EqualFold(parsed["TARGET_DIR_EMPTY"], "true"),
				Severity: "ERROR",
				Message:  ternaryString(strings.EqualFold(parsed["TARGET_DIR_EMPTY"], "true"), "目标数据目录为空或不存在。", "目标数据目录已存在内容，请更换目录或清理后重试。"),
				Detail:   map[string]any{"target_data_dir": strings.TrimSpace(form.TargetDataDir)},
			},
			physicalRestorePrecheckItem{
				ItemKey:  "port_available",
				Label:    "目标端口校验",
				Passed:   strings.EqualFold(parsed["TARGET_PORT_FREE"], "true"),
				Severity: "ERROR",
				Message:  ternaryString(strings.EqualFold(parsed["TARGET_PORT_FREE"], "true"), fmt.Sprintf("端口 %d 可用。", ctx.Resolved.TargetPort), fmt.Sprintf("端口 %d 已被占用。", ctx.Resolved.TargetPort)),
			},
		)
	}
	availableBytes, _ := strconv.ParseInt(parsed["TARGET_AVAILABLE_BYTES"], 10, 64)
	requiredBytes := set.SizeBytes * 2
	if requiredBytes < 2*1024*1024*1024 {
		requiredBytes = 2 * 1024 * 1024 * 1024
	}
	ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
		ItemKey:  "disk_space_ok",
		Label:    "磁盘空间校验",
		Passed:   availableBytes >= requiredBytes,
		Severity: "ERROR",
		Message: ternaryString(availableBytes >= requiredBytes,
			fmt.Sprintf("目标磁盘可用空间 %s，满足恢复要求。", formatPhysicalRestoreBytes(availableBytes)),
			fmt.Sprintf("目标磁盘可用空间仅 %s，建议至少预留 %s。", formatPhysicalRestoreBytes(availableBytes), formatPhysicalRestoreBytes(requiredBytes))),
		Detail: map[string]any{
			"available_bytes": availableBytes,
			"required_bytes":  requiredBytes,
		},
	})
	return finalizePhysicalRestoreContext(ctx), nil
}

func finalizePhysicalRestoreContext(ctx *physicalRestoreContext) *physicalRestoreContext {
	passed := true
	for _, item := range ctx.PrecheckItems {
		if !item.Passed && strings.EqualFold(strings.TrimSpace(item.Severity), "ERROR") {
			passed = false
			break
		}
	}
	ctx.Passed = passed
	return ctx
}

func buildBackupNodeRemoteConfigForRestore(node *backupModels.InsightBackupNodes, c *gin.Context) (*backupNodeRemoteConfig, error) {
	host := strings.TrimSpace(node.Host)
	if host == "" {
		return nil, fmt.Errorf("目标节点地址不能为空")
	}
	port := node.Port
	if port <= 0 {
		port = 22
	}
	sshUser := defaultBackupNodeSSHUser(strings.TrimSpace(node.SSHUser))
	if strings.TrimSpace(node.SSHPassword) == "" && strings.TrimSpace(node.SSHPrivateKey) == "" {
		return nil, fmt.Errorf("目标节点缺少 SSH 凭据，请先补充节点连接信息")
	}
	return &backupNodeRemoteConfig{
		NodeID:        node.ID,
		Host:          host,
		Port:          port,
		BaseDir:       strings.TrimSpace(node.BaseDir),
		Token:         node.Token,
		SSHUser:       sshUser,
		// 节点凭据在库里是密文，用于 SSH 连接前必须解密（历史明文原样返回）
		SSHPassword:   decryptCredential(node.SSHPassword),
		SSHPrivateKey: decryptCredential(node.SSHPrivateKey),
		ServerURL:     defaultBackupNodeServerURL(strings.TrimSpace(node.ServerURL), c),
		InstallDir:    defaultBackupNodeInstallDir(strings.TrimSpace(node.InstallDir)),
		ToolsDir:      defaultBackupNodeToolsDir(strings.TrimSpace(node.ToolsDir)),
		ServiceName:   defaultBackupNodeServiceName(strings.TrimSpace(node.ServiceName), node.ID),
		LogPath:       defaultBackupNodeLogPath(strings.TrimSpace(node.LogPath), strings.TrimSpace(node.InstallDir)),
	}, nil
}

func buildPhysicalRestorePrecheckScript(ctx *physicalRestoreContext) string {
	targetDir := strings.TrimSpace(ctx.Form.TargetDataDir)
	port := ctx.Form.TargetPort
	// 对象存储仓库：无本地仓库目录可读性检查，且 `pgbackrest info` 需要携带 S3 仓库参数。
	repoReadableCheckLine := `if [ -n "$REPO_PATH" ] && run_pg_test "test -d \"$REPO_PATH\" && test -r \"$REPO_PATH\" && find \"$REPO_PATH\" -maxdepth 2 -type d >/dev/null 2>&1"; then REPO_READABLE_AS_POSTGRES=true; fi`
	s3OptionSuffix := ""
	if ctx.Resolved.RepoIsS3 {
		repoReadableCheckLine = `:`
		if opts := pgBackRestS3CommandOptions(ctx.RepoConfig); opts != "" {
			s3OptionSuffix = " " + opts
		}
	}
	lines := []string{
		"set -e",
		`find_bin() {`,
		`  for item in "$@"; do`,
		`    if [ -n "$item" ] && [ -x "$item" ]; then echo "$item"; return 0; fi`,
		`  done`,
		`  return 1`,
		`}`,
		`run_pg_test() {`,
		`  if [ "$(id -un)" = "postgres" ]; then sh -lc "$1"; return $?; fi`,
		`  if [ "$(id -un)" = "root" ]; then su - postgres -s /bin/sh -c "$1"; return $?; fi`,
		`  if command -v sudo >/dev/null 2>&1; then sudo -n -u postgres sh -lc "$1"; return $?; fi`,
		`  return 1`,
		`}`,
		`TARGET_HOST="$(hostname -f 2>/dev/null || hostname || echo unknown)"`,
		`PG_CTL_PATH="$(find_bin "$(command -v pg_ctl 2>/dev/null || true)" /usr/bin/pg_ctl /usr/local/bin/pg_ctl /usr/local/pgsql/bin/pg_ctl /usr/lib/postgresql/13/bin/pg_ctl /usr/lib/postgresql/14/bin/pg_ctl /usr/pgsql-13/bin/pg_ctl /usr/pgsql-14/bin/pg_ctl || true)"`,
		`PGBACKREST_PATH="$(find_bin "$(command -v pgbackrest 2>/dev/null || true)" /usr/bin/pgbackrest /usr/local/bin/pgbackrest /usr/lib/postgresql/13/bin/pgbackrest /usr/lib/postgresql/14/bin/pgbackrest || true)"`,
		`PG_BIN_DIR=""`,
		`if [ -n "$PG_CTL_PATH" ]; then PG_BIN_DIR="$(dirname "$PG_CTL_PATH")"; fi`,
		`PG_VERSION=""`,
		`if [ -n "$PG_CTL_PATH" ]; then PG_VERSION="$("$PG_CTL_PATH" --version 2>/dev/null | head -n1 | sed 's/pg_ctl (PostgreSQL) //g')"; fi`,
		`RUN_AS_POSTGRES=false`,
		`if [ "$(id -un)" = "postgres" ] || [ "$(id -un)" = "root" ]; then RUN_AS_POSTGRES=true;`,
		`elif command -v sudo >/dev/null 2>&1 && sudo -n -u postgres sh -lc 'true' >/dev/null 2>&1; then RUN_AS_POSTGRES=true; fi`,
		fmt.Sprintf(`REPO_PATH=%s`, shellQuote(strings.TrimSpace(ctx.Resolved.RepoPath))),
		fmt.Sprintf(`REPO_READABLE_AS_POSTGRES=%s`, ternaryString(ctx.Resolved.RepoIsS3, "true", "false")),
		repoReadableCheckLine,
		`TARGET_PSQL=""`,
		`if [ -n "$PG_BIN_DIR" ] && [ -x "$PG_BIN_DIR/psql" ]; then TARGET_PSQL="$PG_BIN_DIR/psql"; fi`,
	}
	if ctx.TargetInstance != nil {
		extra := parseExtraConfig(ctx.TargetInstance.ExtraConfig)
		defaultDB := firstNonEmpty(strings.TrimSpace(extra["database"]), "postgres")
		lines = append(lines,
			fmt.Sprintf(`PGHOST_VALUE=%s`, shellQuote(ctx.TargetInstance.Hostname)),
			fmt.Sprintf(`PGPORT_VALUE=%s`, shellQuote(fmt.Sprintf("%d", ctx.TargetInstance.Port))),
			fmt.Sprintf(`PGUSER_VALUE=%s`, shellQuote(ctx.TargetInstance.User)),
			fmt.Sprintf(`PGPASSWORD_VALUE=%s`, shellQuote(ctx.TargetInstance.Password)),
			fmt.Sprintf(`PGDATABASE_VALUE=%s`, shellQuote(defaultDB)),
			fmt.Sprintf(`PGSSLMODE_VALUE=%s`, shellQuote(strings.TrimSpace(extra["sslmode"]))),
			`TARGET_INSTANCE_RUNNING=false`,
			`if command -v ss >/dev/null 2>&1; then`,
			`  if ss -lnt 2>/dev/null | grep -q ":${PGPORT_VALUE}[[:space:]]"; then TARGET_INSTANCE_RUNNING=true; fi`,
			`elif command -v netstat >/dev/null 2>&1; then`,
			`  if netstat -lnt 2>/dev/null | grep -q ":${PGPORT_VALUE}[[:space:]]"; then TARGET_INSTANCE_RUNNING=true; fi`,
			`fi`,
			`TARGET_DATA_DIR=""`,
			`if [ -n "$TARGET_PSQL" ]; then`,
			`  TARGET_DATA_DIR=$(PGHOST="$PGHOST_VALUE" PGPORT="$PGPORT_VALUE" PGUSER="$PGUSER_VALUE" PGPASSWORD="$PGPASSWORD_VALUE" PGDATABASE="$PGDATABASE_VALUE" PGSSLMODE="$PGSSLMODE_VALUE" "$TARGET_PSQL" -Atqc "show data_directory" 2>/dev/null | head -n1 | tr -d '\r')`,
			`fi`,
			`TARGET_PORT="$PGPORT_VALUE"`,
			`TARGET_DIR="$TARGET_DATA_DIR"`,
			`TARGET_PARENT="$(dirname "$TARGET_DIR")"`,
			`TARGET_DIR_EXISTS=false`,
			`if [ -n "$TARGET_DIR" ] && [ -d "$TARGET_DIR" ]; then TARGET_DIR_EXISTS=true; fi`,
			`AVAILABLE_BYTES=0`,
			`if [ -n "$TARGET_PARENT" ] && command -v df >/dev/null 2>&1; then AVAILABLE_BYTES=$(df -Pk "$TARGET_PARENT" 2>/dev/null | awk 'NR==2{print $4*1024}'); fi`,
		)
	} else {
		lines = append(lines,
			fmt.Sprintf(`TARGET_PARENT=%s`, shellQuote(filepath.Dir(targetDir))),
			fmt.Sprintf(`TARGET_DIR=%s`, shellQuote(targetDir)),
			fmt.Sprintf(`TARGET_PORT=%s`, shellQuote(fmt.Sprintf("%d", port))),
			`mkdir -p "$TARGET_PARENT"`,
			`TARGET_DIR_EMPTY=true`,
			`if [ -d "$TARGET_DIR" ] && [ -n "$(ls -A "$TARGET_DIR" 2>/dev/null)" ]; then TARGET_DIR_EMPTY=false; fi`,
			`TARGET_PORT_FREE=true`,
			`if command -v ss >/dev/null 2>&1; then`,
			`  if ss -lnt 2>/dev/null | grep -q ":${TARGET_PORT}[[:space:]]"; then TARGET_PORT_FREE=false; fi`,
			`elif command -v netstat >/dev/null 2>&1; then`,
			`  if netstat -lnt 2>/dev/null | grep -q ":${TARGET_PORT}[[:space:]]"; then TARGET_PORT_FREE=false; fi`,
			`fi`,
			`AVAILABLE_BYTES=0`,
			`if command -v df >/dev/null 2>&1; then AVAILABLE_BYTES=$(df -Pk "$TARGET_PARENT" 2>/dev/null | awk 'NR==2{print $4*1024}'); fi`,
		)
	}
	lines = append(lines,
		fmt.Sprintf(`STANZA_NAME=%s`, shellQuote(strings.TrimSpace(ctx.Resolved.Stanza))),
		fmt.Sprintf(`REPO_PATH_VALUE=%s`, shellQuote(strings.TrimSpace(ctx.Resolved.RepoPath))),
		`PGBACKREST_INFO_B64=""`,
		`REPO_INFO_OK=false`,
		`if [ -n "$PGBACKREST_PATH" ] && [ -n "$STANZA_NAME" ]; then`,
		fmt.Sprintf(`  if INFO_JSON=$(run_pg_test "$PGBACKREST_PATH --stanza=$STANZA_NAME --repo1-path=$REPO_PATH_VALUE%s --log-level-console=off --output=json info" 2>/dev/null); then REPO_INFO_OK=true; else INFO_JSON=""; fi`, s3OptionSuffix),
		`  if [ -n "$INFO_JSON" ]; then PGBACKREST_INFO_B64=$(printf '%s' "$INFO_JSON" | base64 -w0 2>/dev/null || printf '%s' "$INFO_JSON" | base64 | tr -d '\n'); fi`,
		`fi`,
		`echo "TARGET_DATA_DIR=$TARGET_DIR"`,
		`echo "TARGET_PORT=$TARGET_PORT"`,
		`echo "TARGET_HOST=$TARGET_HOST"`,
		`echo "TARGET_PG_CTL=$PG_CTL_PATH"`,
		`echo "TARGET_PGBACKREST=$PGBACKREST_PATH"`,
		`echo "TARGET_PG_BIN_DIR=$PG_BIN_DIR"`,
		`echo "TARGET_PG_VERSION=$PG_VERSION"`,
		`echo "TARGET_PSQL=$TARGET_PSQL"`,
		`echo "TARGET_DIR_EMPTY=${TARGET_DIR_EMPTY:-false}"`,
		`echo "TARGET_DIR_EXISTS=${TARGET_DIR_EXISTS:-false}"`,
		`echo "TARGET_PORT_FREE=${TARGET_PORT_FREE:-false}"`,
		`echo "TARGET_INSTANCE_RUNNING=${TARGET_INSTANCE_RUNNING:-false}"`,
		`echo "TARGET_AVAILABLE_BYTES=$AVAILABLE_BYTES"`,
		`echo "RUN_AS_POSTGRES=$RUN_AS_POSTGRES"`,
		`echo "REPO_READABLE_AS_POSTGRES=$REPO_READABLE_AS_POSTGRES"`,
		`echo "REPO_INFO_OK=$REPO_INFO_OK"`,
		`echo "PGBACKREST_INFO_B64=$PGBACKREST_INFO_B64"`,
	)
	return strings.Join(lines, "\n")
}

// sanitizePGBackRestLabel 校验 pgBackRest backup label 仅含安全字符，避免拼入远端命令时被注入。
func sanitizePGBackRestLabel(label string) string {
	text := strings.TrimSpace(label)
	if text == "" {
		return ""
	}
	for _, ch := range text {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_' || ch == '.' || ch == '+':
		default:
			return ""
		}
	}
	return text
}

// pgBackRestInfoStanza 对应 `pgbackrest info --output=json` 输出中的单个 stanza。
type pgBackRestInfoStanza struct {
	Name   string `json:"name"`
	Status struct {
		Code int `json:"code"`
	} `json:"status"`
	Backup []pgBackRestInfoBackup `json:"backup"`
}

type pgBackRestInfoBackup struct {
	Label     string `json:"label"`
	Type      string `json:"type"`
	Timestamp struct {
		Start int64 `json:"start"`
		Stop  int64 `json:"stop"`
	} `json:"timestamp"`
}

// parsePGBackRestInfoB64 解析预检脚本回传的 base64 编码 pgBackRest 仓库信息。
func parsePGBackRestInfoB64(raw string) []pgBackRestInfoStanza {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil
	}
	var stanzas []pgBackRestInfoStanza
	if err := json.Unmarshal(decoded, &stanzas); err != nil {
		return nil
	}
	return stanzas
}

// locatePGBackRestBackupLabel 在仓库备份列表中定位当前备份集对应的 backup label。
// 匹配规则：优先类型一致（full/diff/incr），其次取时间最接近备份集创建时刻的一条。
// 定位成功后恢复阶段用 --set 精确恢复到该快照，从而区分全量与非全量备份。
func locatePGBackRestBackupLabel(set *backupModels.InsightBackupSets, stanza string, stanzas []pgBackRestInfoStanza) (string, string, int, bool) {
	if len(stanzas) == 0 {
		return "", "", 0, false
	}
	picked := stanzas[0]
	for _, item := range stanzas {
		if strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(stanza)) {
			picked = item
			break
		}
	}
	count := len(picked.Backup)
	if count == 0 {
		return "", "", 0, false
	}
	expectedType := strings.ToLower(strings.TrimSpace(string(set.BackupType)))
	if expectedType == "" {
		expectedType = "full"
	}
	var (
		bestLabel string
		bestType  string
		bestDelta int64 = -1
	)
	targetTime := time.Time(set.CreatedAt).Unix()
	for _, backup := range picked.Backup {
		label := strings.TrimSpace(backup.Label)
		if label == "" {
			continue
		}
		backupType := strings.ToLower(strings.TrimSpace(backup.Type))
		matchType := backupType == expectedType
		if !matchType && !(expectedType == "full" && backupType == "") {
			continue
		}
		stamp := backup.Timestamp.Start
		if stamp <= 0 {
			stamp = backup.Timestamp.Stop
		}
		delta := stamp - targetTime
		if delta < 0 {
			delta = -delta
		}
		if bestDelta < 0 || delta < bestDelta {
			bestDelta = delta
			bestLabel = label
			bestType = backupType
		}
	}
	if bestLabel == "" {
		return "", "", count, false
	}
	return bestLabel, bestType, count, true
}

func parsePhysicalRestoreKeyValues(raw string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		parts := strings.SplitN(text, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out
}

func summarizePhysicalRestorePrecheck(items []physicalRestorePrecheckItem) string {
	if len(items) == 0 {
		return "未执行预检查"
	}
	passed := 0
	for _, item := range items {
		if item.Passed {
			passed++
		}
	}
	if passed == len(items) {
		return fmt.Sprintf("全部 %d 项检查通过", len(items))
	}
	return fmt.Sprintf("共 %d 项检查，通过 %d 项，未通过 %d 项", len(items), passed, len(items)-passed)
}

func resolvePhysicalRestoreStanza(set *backupModels.InsightBackupSets, cfg pgBackRestDriverConfig, meta physicalRestoreArtifactMeta) string {
	if text := strings.TrimSpace(meta.Stanza); text != "" {
		return text
	}
	if text := strings.TrimSpace(cfg.Stanza); text != "" {
		return text
	}
	if set == nil {
		return ""
	}
	sourcePort := 0
	if set.SourceInstanceID > 0 {
		if instance, err := loadInstanceByID(set.SourceInstanceID); err == nil && instance != nil {
			sourcePort = instance.Port
		}
	}
	return fmt.Sprintf("pg_%d_%d_%s", set.SourceInstanceID, sourcePort, sanitizePathSegment(set.TaskID, "task"))
}

// physicalRestoreRepoIsS3 判断备份集仓库是否为 pgBackRest 原生对象存储仓库（repo1-type=s3）。
// 优先读备份集 driver_config 里的 repo_type（备份期由 prepare 写入），
// 缺失时回退到按存储目标类型判断（LOCAL_SERVER / 仓库节点均视为文件仓库）。
func physicalRestoreRepoIsS3(set *backupModels.InsightBackupSets, cfg pgBackRestDriverConfig) bool {
	if cfg.IsS3Repo() {
		return true
	}
	if set == nil || set.RepositoryNodeID > 0 || set.StorageTargetID == 0 {
		return false
	}
	target, err := loadStorageTargetByID(set.StorageTargetID)
	if err != nil || target == nil {
		return false
	}
	return pgBackRestStorageTargetIsS3(target)
}

func resolvePhysicalRestoreRepoPath(set *backupModels.InsightBackupSets, cfg pgBackRestDriverConfig, meta physicalRestoreArtifactMeta) string {
	if text := strings.TrimSpace(meta.RepoPath); text != "" {
		return text
	}
	if text := strings.TrimSpace(cfg.RepoPath); text != "" {
		return text
	}
	if set == nil {
		return ""
	}
	return resolvePGBackRestRepoDir(set.OutputDir, set.SourceInstanceID, set.TaskID)
}

func loadPhysicalRestoreArtifactMeta(filePath string) (physicalRestoreArtifactMeta, error) {
	meta := physicalRestoreArtifactMeta{}
	path := strings.TrimSpace(filePath)
	if path == "" {
		return meta, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	if len(raw) == 0 {
		return meta, nil
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func summarizePhysicalRestoreFailures(items []physicalRestorePrecheckItem) string {
	failures := make([]string, 0, len(items))
	for _, item := range items {
		if item.Passed || !strings.EqualFold(strings.TrimSpace(item.Severity), "ERROR") {
			continue
		}
		text := strings.TrimSpace(item.Message)
		if text == "" {
			text = strings.TrimSpace(item.Label)
		}
		if text != "" {
			failures = append(failures, text)
		}
		if len(failures) >= 2 {
			break
		}
	}
	if len(failures) == 0 {
		return "请先执行物理恢复预检查并处理未通过项"
	}
	return strings.Join(failures, "；")
}

func formatPhysicalRestoreBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes) / 1024
	if value < 1024 {
		return fmt.Sprintf("%.1f KB", value)
	}
	value = value / 1024
	if value < 1024 {
		return fmt.Sprintf("%.1f MB", value)
	}
	value = value / 1024
	return fmt.Sprintf("%.2f GB", value)
}

func physicalRestoreModeLabel(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case physicalRestoreModeNewInstance:
		return "恢复到新实例"
	case physicalRestoreModeExistingInstance:
		return "恢复到目标实例"
	default:
		return "实例级恢复"
	}
}

func physicalRestoreStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING":
		return "待执行"
	case "RUNNING":
		return "执行中"
	case "SUCCESS":
		return "成功"
	case "FAILED":
		return "失败"
	default:
		return strings.TrimSpace(status)
	}
}

func physicalRestoreStageLabel(stage string) string {
	switch strings.ToUpper(strings.TrimSpace(stage)) {
	case physicalRestoreStagePending:
		return "等待执行"
	case physicalRestoreStagePrecheck:
		return "预检查"
	case physicalRestoreStagePrepareTarget:
		return "准备目标目录"
	case physicalRestoreStageRestore:
		return "恢复基础数据"
	case physicalRestoreStageStartInstance:
		return "启动实例"
	case physicalRestoreStagePostcheck:
		return "恢复后校验"
	case physicalRestoreStageFinished:
		return "执行完成"
	default:
		return strings.TrimSpace(stage)
	}
}

func calcPhysicalRestoreDurationSeconds(startAt commonModels.LocalTime, endAt commonModels.LocalTime, status string) int64 {
	start := time.Time(startAt)
	if start.IsZero() {
		return 0
	}
	end := time.Time(endAt)
	if end.IsZero() && strings.EqualFold(strings.TrimSpace(status), "RUNNING") {
		end = nowLocalTime()
	}
	if end.IsZero() || end.Before(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

func marshalJSONValue(value any) (datatypes.JSON, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(raw), nil
}

func decodeJSONValue(raw datatypes.JSON) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func ternaryString(cond bool, okValue string, failValue string) string {
	if cond {
		return okValue
	}
	return failValue
}

func isPhysicalRestoreStopRequested(runID uint64) bool {
	if runID == 0 || global.App.DB == nil {
		return false
	}
	var row struct {
		Status string
	}
	if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Select("status").
		Where("id = ? AND restore_kind = ? AND driver_type = ?", runID, restoreKindPhysical, "PG_BACKREST").
		Take(&row).Error; err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(row.Status), "STOP_REQUESTED")
}

func attachPhysicalRestoreStopWatcher(runID uint64, runner *sshRunner, logPath string) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if !isPhysicalRestoreStopRequested(runID) {
					continue
				}
				appendBackupRunLog(logPath, "WARN", "检测到停止请求，准备中止当前物理恢复进程")
				_ = runner.Close()
				return
			}
		}
	}()
	return func() {
		close(done)
	}
}

func writePhysicalRestoreStopped(id uint64, logPath string, reason string) {
	messageText := strings.TrimSpace(reason)
	if messageText == "" {
		messageText = "物理恢复任务已终止"
	}
	if strings.TrimSpace(logPath) != "" {
		appendBackupRunLog(logPath, "WARN", "%s", messageText)
	}
	updatePhysicalRestoreRunProgress(id, physicalRestorePatch{
		Status:          "STOPPED",
		CurrentStage:    physicalRestoreStagePostcheck,
		ProgressPercent: 100,
		CurrentAction:   "已终止",
		LogPath:         logPath,
		ErrorMessage:    messageText,
	})
}

func executePhysicalRestoreRun(runID uint64) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type = ?", runID, restoreKindPhysical, "PG_BACKREST").First(&run).Error; err != nil {
		return
	}
	resolved := restoreRunResolvedContext(&run)
	autoStart := restoreRunAutoStart(&run)
	switch strings.ToUpper(strings.TrimSpace(string(run.Status))) {
	case "STOPPED":
		return
	case "STOP_REQUESTED":
		writePhysicalRestoreStopped(run.ID, run.LogPath, resolveRestoreStopReason(run.CreatedBy))
		return
	}
	set, err := LoadBackupSet(run.BackupSetID)
	if err != nil {
		writePhysicalRestoreFailed(run.ID, "", err)
		return
	}
	targetNode, err := loadBackupNodeByID(run.TargetNodeID)
	if err != nil {
		writePhysicalRestoreFailed(run.ID, "", err)
		return
	}
	remoteCfg, err := buildBackupNodeRemoteConfigForRestore(targetNode, nil)
	if err != nil {
		writePhysicalRestoreFailed(run.ID, "", err)
		return
	}
	baseDir, err := absMediaBackupDir()
	if err != nil {
		writePhysicalRestoreFailed(run.ID, "", err)
		return
	}
	logDir := filepath.Join(baseDir, "restore", "physical", "postgresql", fmt.Sprintf("%d", run.ID))
	if err := ensureDir(logDir); err != nil {
		writePhysicalRestoreFailed(run.ID, "", err)
		return
	}
	logPath := filepath.Join(logDir, "run.log")
	logger, err := openBackupRunLogger(logPath)
	if err != nil {
		writePhysicalRestoreFailed(run.ID, "", err)
		return
	}
	defer logger.Close()
	updatePhysicalRestoreRunProgress(run.ID, physicalRestorePatch{
		Status:          "RUNNING",
		CurrentStage:    physicalRestoreStagePrecheck,
		ProgressPercent: 5,
		CurrentAction:   "连接目标节点并准备恢复",
		LogPath:         logPath,
	})
	logger.Infof("开始执行 PostgreSQL 物理恢复 run=%d backup_set=%d target_node=%d data_dir=%s port=%d", run.ID, run.BackupSetID, run.TargetNodeID, resolved.TargetDataDir, run.TargetPort)
	cfg, err := loadPGBackRestDriverConfig(set.DriverConfig)
	if err != nil {
		writePhysicalRestoreFailed(run.ID, logPath, err)
		return
	}
	repoPath := strings.TrimSpace(resolved.RepoPath)
	if repoPath == "" {
		repoPath = strings.TrimSpace(cfg.RepoPath)
	}
	if repoPath == "" {
		if cfg.IsS3Repo() {
			// 对象存储仓库未配置前缀时读写 bucket 根。
			repoPath = "/"
		} else {
			repoPath = resolvePGBackRestRepoDir(set.OutputDir, set.SourceInstanceID, set.TaskID)
		}
	}
	stanza := strings.TrimSpace(resolved.Stanza)
	if stanza == "" {
		stanza = strings.TrimSpace(cfg.Stanza)
	}
	if stanza == "" {
		writePhysicalRestoreFailed(run.ID, logPath, fmt.Errorf("缺少 pgBackRest stanza，无法恢复"))
		return
	}
	repoHost := strings.TrimSpace(resolved.RepoHost)
	if repoHost == "" {
		repoHost = strings.TrimSpace(cfg.RepoHost)
	}
	repoPort := cfg.RepoPort
	repoUser := strings.TrimSpace(cfg.RepoUser)
	if set.RepositoryNodeID > 0 {
		if repoNode, repoErr := loadBackupNodeByID(set.RepositoryNodeID); repoErr == nil {
			if repoHost == "" {
				repoHost = strings.TrimSpace(repoNode.Host)
			}
			if repoPort <= 0 {
				repoPort = repoNode.Port
			}
			if repoUser == "" {
				repoUser = defaultBackupNodeSSHUser(strings.TrimSpace(repoNode.SSHUser))
			}
		}
	}
	if repoPort <= 0 {
		repoPort = 22
	}
	resolvedPGBinDir := strings.TrimSpace(resolved.TargetPGBinDir)
	if resolvedPGBinDir == "" {
		resolvedPGBinDir = "/usr/lib/postgresql/13/bin"
	}
	runner, err := newSSHRunner(remoteCfg)
	if err != nil {
		writePhysicalRestoreFailed(run.ID, logPath, err)
		return
	}
	defer runner.Close()
	stopWatcherDone := attachPhysicalRestoreStopWatcher(run.ID, runner, logPath)
	defer stopWatcherDone()
	confPath := fmt.Sprintf("/tmp/goinsight-physical-restore-%d.conf", run.ID)
	updatePhysicalRestoreRunProgress(run.ID, physicalRestorePatch{
		Status:          "RUNNING",
		CurrentStage:    physicalRestoreStagePrepareTarget,
		ProgressPercent: 18,
		CurrentAction:   "生成 pgBackRest 配置并准备目标目录",
		LogPath:         logPath,
	})
	configContent := buildPGBackRestConfig(pgBackRestDriverConfig{
		ProcessMax:  2,
		StartFast:   cfg.StartFast,
		RepoType:    cfg.RepoType,
		S3Bucket:    cfg.S3Bucket,
		S3Endpoint:  cfg.S3Endpoint,
		S3Region:    cfg.S3Region,
		S3AccessKey: cfg.S3AccessKey,
		S3SecretKey: cfg.S3SecretKey,
		S3URIStyle:  cfg.S3URIStyle,
		RepoRemote:  cfg.RepoRemote || (set.RepositoryNodeID > 0 && set.RepositoryNodeID != run.TargetNodeID),
		RepoHost:    repoHost,
		RepoPort:    repoPort,
		RepoUser:    repoUser,
		RepoPath:    repoPath,
	}, repoPath, "", "", stanza, resolved.TargetDataDir, run.TargetPort, "postgres", true)
	if err := runner.Upload(confPath, []byte(configContent)); err != nil {
		writePhysicalRestoreFailed(run.ID, logPath, fmt.Errorf("上传恢复配置失败: %w", err))
		return
	}
	logger.Infof("已生成恢复配置: %s", confPath)
	updatePhysicalRestoreRunProgress(run.ID, physicalRestorePatch{
		Status:          "RUNNING",
		CurrentStage:    physicalRestoreStageRestore,
		ProgressPercent: 45,
		CurrentAction:   "执行 pgBackRest restore",
		LogPath:         logPath,
	})
	stopExisting := run.TargetInstanceID > 0 || strings.EqualFold(string(run.RestoreMode), physicalRestoreModeExistingInstance)
	restoreOutput, err := runner.Run(buildPhysicalRestoreExecuteScript(run.ID, confPath, stanza, resolved.TargetDataDir, run.TargetPort, autoStart, resolvedPGBinDir, stopExisting, resolved.BackupLabel))
	if strings.TrimSpace(restoreOutput) != "" {
		logger.Infof("远端输出:\n%s", restoreOutput)
	}
	if err != nil {
		if isPhysicalRestoreStopRequested(run.ID) {
			writePhysicalRestoreStopped(run.ID, logPath, resolveRestoreStopReason(""))
			return
		}
		writePhysicalRestoreFailed(run.ID, logPath, fmt.Errorf("执行物理恢复失败: %v", err))
		return
	}
	updatePhysicalRestoreRunProgress(run.ID, physicalRestorePatch{
		Status:          "RUNNING",
		CurrentStage:    physicalRestoreStagePostcheck,
		ProgressPercent: 88,
		CurrentAction:   ternaryString(autoStart, "执行恢复后校验", "恢复完成，等待人工启动"),
		LogPath:         logPath,
	})
	resultJSON, _ := marshalJSONValue(gin.H{
		"target_data_dir": resolved.TargetDataDir,
		"target_port":     run.TargetPort,
		"stanza":          stanza,
		"repo_path":       repoPath,
		"auto_start":      autoStart,
	})
	updatePhysicalRestoreRunProgress(run.ID, physicalRestorePatch{
		Status:          "SUCCESS",
		CurrentStage:    physicalRestoreStageFinished,
		ProgressPercent: 100,
		CurrentAction:   "恢复完成",
		LogPath:         logPath,
		ResultSummary:   resultJSON,
	})
	logger.Infof("PostgreSQL 物理恢复完成。")
}

func buildPhysicalRestoreExecuteScript(runID uint64, confPath string, stanza string, targetDataDir string, targetPort int, autoStart bool, pgBinDir string, stopExisting bool, backupLabel string) string {
	startFlag := "0"
	if autoStart {
		startFlag = "1"
	}
	stopFlag := "0"
	if stopExisting {
		stopFlag = "1"
	}
	// 备份集定位成功时用 --set 精确恢复到该快照（全量 / 非全量分别对应各自的备份集）。
	setClause := ""
	if label := sanitizePGBackRestLabel(backupLabel); label != "" {
		setClause = " --set=" + label
	}
	return strings.Join([]string{
		"set -e",
		`run_pg() {`,
		`  if [ "$(id -un)" = "postgres" ]; then sh -lc "$1"; return 0; fi`,
		`  if [ "$(id -un)" = "root" ]; then su - postgres -s /bin/sh -c "$1"; return 0; fi`,
		`  if command -v sudo >/dev/null 2>&1 && sudo -n -u postgres sh -lc 'true' >/dev/null 2>&1; then sudo -n -u postgres sh -lc "$1"; return 0; fi`,
		`  echo "无法切换为 postgres 用户执行恢复"`,
		`  exit 18`,
		`}`,
		`find_bin() {`,
		`  for item in "$@"; do if [ -n "$item" ] && [ -x "$item" ]; then echo "$item"; return 0; fi; done`,
		`  return 1`,
		`}`,
		fmt.Sprintf(`CONF_PATH=%s`, shellQuote(confPath)),
		fmt.Sprintf(`TARGET_DIR=%s`, shellQuote(targetDataDir)),
		fmt.Sprintf(`TARGET_PORT=%s`, shellQuote(fmt.Sprintf("%d", targetPort))),
		fmt.Sprintf(`PREFERRED_PG_BIN=%s`, shellQuote(pgBinDir)),
		fmt.Sprintf(`AUTO_START=%s`, shellQuote(startFlag)),
		fmt.Sprintf(`STOP_EXISTING=%s`, shellQuote(stopFlag)),
		`mkdir -p "$(dirname "$TARGET_DIR")"`,
		`PGBACKREST_PATH="$(find_bin "$(command -v pgbackrest 2>/dev/null || true)" "$PREFERRED_PG_BIN/pgbackrest" /usr/bin/pgbackrest /usr/local/bin/pgbackrest || true)"`,
		`PG_CTL_PATH="$(find_bin "$(command -v pg_ctl 2>/dev/null || true)" "$PREFERRED_PG_BIN/pg_ctl" /usr/bin/pg_ctl /usr/local/bin/pg_ctl || true)"`,
		`[ -n "$PGBACKREST_PATH" ] || { echo "未找到 pgbackrest"; exit 13; }`,
		`[ -n "$PG_CTL_PATH" ] || { echo "未找到 pg_ctl"; exit 14; }`,
		`if [ "$STOP_EXISTING" = "1" ]; then`,
		`  run_pg "$PG_CTL_PATH -D ` + shellQuote(targetDataDir) + ` status >/dev/null 2>&1 && $PG_CTL_PATH -D ` + shellQuote(targetDataDir) + ` stop -m fast -w || true"`,
		`else`,
		`  if [ -d "$TARGET_DIR" ] && [ -n "$(ls -A "$TARGET_DIR" 2>/dev/null)" ]; then echo "目标目录非空: $TARGET_DIR"; exit 12; fi`,
		`fi`,
		`run_pg "mkdir -p ` + shellQuote(targetDataDir) + ` && find ` + shellQuote(targetDataDir) + ` -mindepth 1 -maxdepth 1 -exec rm -rf {} + 2>/dev/null || true && chmod 700 ` + shellQuote(targetDataDir) + `"`,
		`run_pg "$PGBACKREST_PATH --config=$CONF_PATH --stanza=` + stanza + setClause + ` --log-level-console=info restore --pg1-path=` + shellQuote(targetDataDir) + `"`,
		`if [ "$AUTO_START" = "1" ]; then`,
		`  STARTUP_LOG="$TARGET_DIR/startup.log"`,
		`  run_pg "$PG_CTL_PATH -D ` + shellQuote(targetDataDir) + ` -l $STARTUP_LOG -o '-p ` + fmt.Sprintf("%d", targetPort) + `' -w start"`,
		`  run_pg "$PG_CTL_PATH -D ` + shellQuote(targetDataDir) + ` status"`,
		`  if [ -x "$(dirname "$PG_CTL_PATH")/psql" ]; then`,
		`    run_pg "$(dirname "$PG_CTL_PATH")/psql -h 127.0.0.1 -p ` + fmt.Sprintf("%d", targetPort) + ` -d postgres -Atqc 'select version()'"`,
		`  fi`,
		`fi`,
		fmt.Sprintf(`echo "RESTORE_RUN_ID=%d"`, runID),
		`echo "RESTORE_DONE=true"`,
	}, "\n")
}

type physicalRestorePatch struct {
	Status          string
	CurrentStage    string
	ProgressPercent int
	CurrentAction   string
	LogPath         string
	ErrorMessage    string
	ResultSummary   datatypes.JSON
}

func updatePhysicalRestoreRunProgress(id uint64, patch physicalRestorePatch) {
	updates := map[string]any{
		"last_report_at": commonModels.LocalTime(nowLocalTime()),
	}
	if text := strings.TrimSpace(patch.Status); text != "" {
		updates["status"] = text
		if strings.EqualFold(text, "RUNNING") {
			updates["start_at"] = commonModels.LocalTime(nowLocalTime())
		}
		if strings.EqualFold(text, "SUCCESS") || strings.EqualFold(text, "FAILED") {
			updates["end_at"] = commonModels.LocalTime(nowLocalTime())
		}
	}
	if text := strings.TrimSpace(patch.CurrentStage); text != "" {
		updates["current_stage"] = text
	}
	if patch.ProgressPercent >= 0 {
		updates["progress_percent"] = clampPercent(patch.ProgressPercent)
	}
	if text := strings.TrimSpace(patch.CurrentAction); text != "" {
		updates["current_action"] = text
	}
	if text := strings.TrimSpace(patch.LogPath); text != "" {
		updates["log_path"] = text
		updates["log_excerpt"] = tailLogExcerpt(text)
	}
	if text := strings.TrimSpace(patch.ErrorMessage); text != "" {
		updates["error_message"] = text
	}
	if len(patch.ResultSummary) > 0 {
		updates["result_summary"] = patch.ResultSummary
	}
	global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ? AND restore_kind = ? AND driver_type = ?", id, restoreKindPhysical, "PG_BACKREST").Updates(updates)
}

func writePhysicalRestoreFailed(id uint64, logPath string, err error) {
	if strings.TrimSpace(logPath) != "" {
		appendBackupRunLog(logPath, "ERROR", "%v", err)
	}
	updatePhysicalRestoreRunProgress(id, physicalRestorePatch{
		Status:          "FAILED",
		CurrentStage:    physicalRestoreStagePostcheck,
		ProgressPercent: 100,
		CurrentAction:   "执行失败",
		LogPath:         logPath,
		ErrorMessage:    strings.TrimSpace(err.Error()),
	})
}
