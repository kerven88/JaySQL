package services

import (
	"encoding/json"
	"errors"
	"fmt"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GetBackupTaskDetailService struct {
	C  *gin.Context
	ID string
}

func (s *GetBackupTaskDetailService) Run() (any, error) {
	var task backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", s.ID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("备份任务不存在")
		}
		return nil, err
	}

	var objectSpec any
	if len(task.ObjectSpec) > 0 {
		_ = json.Unmarshal(task.ObjectSpec, &objectSpec)
	}
	var driverConfig any
	if len(task.DriverConfig) > 0 {
		_ = json.Unmarshal(task.DriverConfig, &driverConfig)
	}
	var lastPrecheckResult any
	if len(task.LastPrecheckResult) > 0 {
		_ = json.Unmarshal(task.LastPrecheckResult, &lastPrecheckResult)
	}

	return map[string]any{
		"id":                 task.TaskID,
		"name":               task.Name,
		"db_type":            string(task.DBType),
		"source_instance_id": task.SourceInstanceID,
		"storage_target_id":  task.StorageTargetID,
		"executor_node_id":   task.ExecutorNodeID,
		"repository_node_id": task.RepositoryNodeID,
		"backup_type":        string(task.BackupType),
		"driver_type":        string(task.DriverType),
		"driver_config":      driverConfig,
		"object_mode":        string(task.ObjectMode),
		"object_spec":        objectSpec,
		"object_summary":     task.ObjectSummary,
		"output_dir":         task.OutputDir,
		"schedule_days":      parseScheduleDays(task.ScheduleDays),
		"schedule_time":      task.ScheduleTime,
		"retention_days":     task.RetentionDays,
		"enabled":            task.Enabled,
		// 任务级 prepare 状态机：物理备份创建后前端轮询本接口等待 PREPARE_OK，
		// 缺少这些字段会导致轮询永远无法终止、页面不跳转（240s 后超时报错）
		"prepare_status":       string(task.PrepareStatus),
		"prepare_stage":        task.PrepareStage,
		"prepare_title":        task.PrepareTitle,
		"prepare_suggestion":   task.PrepareSuggestion,
		"prepare_detail":       task.PrepareDetail,
		"prepare_raw_error":    task.PrepareRawError,
		"last_precheck_status": string(task.LastPrecheckStatus),
		"last_precheck_at":     task.LastPrecheckAt,
		"last_precheck_result": lastPrecheckResult,
		"created_by":           task.CreatedBy,
		"created_at":           task.CreatedAt,
		"updated_at":           task.UpdatedAt,
	}, nil
}
