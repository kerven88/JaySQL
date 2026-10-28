package services

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
)

type AgentHeartbeatService struct {
	*backupForms.AgentHeartbeatForm
	C *gin.Context
}

func (s *AgentHeartbeatService) Run() error {
	node, err := authenticateBackupNode(s.NodeID, s.Token)
	if err != nil {
		return err
	}
	return markNodeHeartbeat(node, s.AgentVersion)
}

type AgentPollJobService struct {
	*backupForms.AgentPollJobForm
	C *gin.Context
}

type AgentBackupJobResponse struct {
	RunID            uint64                 `json:"run_id"`
	TaskID           string                 `json:"task_id"`
	DBType           string                 `json:"db_type"`
	DriverType       string                 `json:"driver_type"`
	DriverConfig     interface{}            `json:"driver_config"`
	SourceInstanceID uint64                 `json:"source_instance_id"`
	InstanceBizID    string                 `json:"instance_biz_id"`
	RunSeq           uint64                 `json:"run_seq"`
	SourceDBName     string                 `json:"source_db_name"`
	BackupType       string                 `json:"backup_type"`
	ObjectMode       string                 `json:"object_mode"`
	ObjectSpec       interface{}            `json:"object_spec"`
	ObjectSummary    string                 `json:"object_summary"`
	OutputBaseDir    string                 `json:"output_base_dir"`
	OutputDir        string                 `json:"output_dir"`
	LogPath          string                 `json:"log_path"`
	Instance         AgentBackupJobInstance `json:"instance"`
	StorageTargetID  uint64                 `json:"storage_target_id"`
	StorageTarget    *AgentStorageTargetRef `json:"storage_target,omitempty"`
}

type AgentStorageTargetRef struct {
	ID              uint64            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Host            string            `json:"host"`
	Port            int               `json:"port"`
	BasePath        string            `json:"base_path"`
	SSHUser         string            `json:"ssh_user"`
	SSHPassword     string            `json:"ssh_password,omitempty"`
	SSHPrivateKey   string            `json:"ssh_private_key,omitempty"`
	ConnectTimeout  int               `json:"connect_timeout_seconds"`
	CustomEnv       map[string]string `json:"custom_env,omitempty"`
	S3Endpoint      string            `json:"s3_endpoint,omitempty"`
	S3Region        string            `json:"s3_region,omitempty"`
	S3Bucket        string            `json:"s3_bucket,omitempty"`
	S3AccessKey     string            `json:"s3_access_key,omitempty"`
	S3SecretKey     string            `json:"s3_secret_key,omitempty"`
	S3ForcePathStyle bool              `json:"s3_force_path_style,omitempty"`
}

type AgentBackupJobInstance struct {
	Hostname    string            `json:"hostname"`
	Port        int               `json:"port"`
	User        string            `json:"user"`
	Password    string            `json:"password"`
	DbType      string            `json:"db_type"`
	ExtraConfig map[string]string `json:"extra_config"`
}

func (s *AgentPollJobService) Run() (*AgentBackupJobResponse, error) {
	refreshBackupNodeStatus()
	node, err := authenticateBackupNode(s.NodeID, s.Token)
	if err != nil {
		return nil, err
	}
	if err := markNodeHeartbeat(node, s.AgentVersion); err != nil {
		return nil, err
	}

	type candidate struct {
		RunID            uint64
		TaskID           string
		DBType           string
		DriverType       string
		DriverConfig     []byte
		SourceInstanceID uint64
		RunSeq           uint64
		SourceDBName     string
		BackupType       string
		ObjectMode       string
		ObjectSpec       []byte
		ObjectSummary    string
		OutputDir        string
		StorageTargetID  uint64
	}
	var row candidate
	err = global.App.DB.Table("insight_backup_runs br").
		Select("br.id as run_id, br.task_id, br.run_seq, bt.db_type, br.driver_type, br.driver_config, bt.source_instance_id, bt.source_db_name, br.backup_type, br.object_mode, br.object_spec, br.object_summary, bt.output_dir, br.storage_target_id").
		Joins("join insight_backup_tasks bt on bt.task_id = br.task_id and bt.deleted_at IS NULL").
		Where("br.status = ? and bt.executor_node_id = ?", "PENDING", node.ID).
		Order("br.id asc").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.RunID == 0 {
		return nil, nil
	}
	instance, err := loadInstanceByID(row.SourceInstanceID)
	if err != nil {
		return nil, err
	}
	outputBaseDir, err := resolveNodeExecutionBaseDir(node.BaseDir, row.OutputDir)
	if err != nil {
		return nil, err
	}
	runDirSpec := BackupRunDirSpec{
		SourceInstanceID: row.SourceInstanceID,
		InstanceBizID:    strings.TrimSpace(instance.InstanceID),
		TaskID:           row.TaskID,
		RunID:            row.RunID,
		RunSeq:           row.RunSeq,
	}
	logPath := resolveBackupRunLogPathByDriver(outputBaseDir, row.DriverType, row.DBType, runDirSpec)
	start := nowLocalTime()
	if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ? and status = ?", row.RunID, "PENDING").Updates(map[string]any{
		"status":           "RUNNING",
		"start_at":         commonModels.LocalTime(start),
		"executor_node_id": node.ID,
		"output_dir":       outputBaseDir,
		"log_path":         logPath,
		"log_excerpt":      "",
	}).Error; err != nil {
		return nil, err
	}
	var storageTargetRef *AgentStorageTargetRef
	if row.StorageTargetID > 0 {
		st, loadErr := loadStorageTargetByID(row.StorageTargetID)
		if loadErr != nil {
			return nil, fmt.Errorf("加载存储目标失败: %w", loadErr)
		}
		if st != nil {
			decSSHPassword, decSSHPrivateKey, decS3AK, decS3SK, decryptErr := decryptStorageTargetCredentials(st)
			if decryptErr != nil {
				return nil, fmt.Errorf("解密存储目标凭据失败: %w", decryptErr)
			}
			storageTargetRef = &AgentStorageTargetRef{
				ID:                st.ID,
				Name:              st.Name,
				Type:              string(st.Type),
				Host:              st.Host,
				Port:              st.Port,
				BasePath:          st.BasePath,
				SSHUser:           st.SSHUser,
				SSHPassword:       decSSHPassword,
				SSHPrivateKey:     decSSHPrivateKey,
				S3Endpoint:        st.S3Endpoint,
				S3Region:          st.S3Region,
				S3Bucket:          st.S3Bucket,
				S3AccessKey:       decS3AK,
				S3SecretKey:       decS3SK,
				S3ForcePathStyle:  st.S3ForcePathStyle,
			}
		}
	}
	return &AgentBackupJobResponse{
		RunID:            row.RunID,
		TaskID:           row.TaskID,
		DBType:           row.DBType,
		DriverType:       row.DriverType,
		DriverConfig:     jsonRawToAny(row.DriverConfig),
		SourceInstanceID: row.SourceInstanceID,
		InstanceBizID:    strings.TrimSpace(instance.InstanceID),
		RunSeq:           row.RunSeq,
		SourceDBName:     row.SourceDBName,
		BackupType:       row.BackupType,
		ObjectMode:       row.ObjectMode,
		ObjectSpec:       jsonRawToAny(row.ObjectSpec),
		ObjectSummary:    row.ObjectSummary,
		OutputBaseDir:    outputBaseDir,
		OutputDir:        row.OutputDir,
		LogPath:          logPath,
		StorageTargetID: row.StorageTargetID,
		StorageTarget:    storageTargetRef,
		Instance: AgentBackupJobInstance{
			Hostname:    instance.Hostname,
			Port:        instance.Port,
			User:        instance.User,
			Password:    instance.Password,
			DbType:      string(instance.DbType),
			ExtraConfig: parseExtraConfig(instance.ExtraConfig),
		},
	}, nil
}

type AgentReportRunService struct {
	*backupForms.AgentReportRunForm
	C *gin.Context
}

type AgentRunStopStatusService struct {
	*backupForms.AgentRunStopStatusForm
	C *gin.Context
}

func (s *AgentReportRunService) Run() error {
	node, err := authenticateBackupNode(s.NodeID, s.Token)
	if err != nil {
		return err
	}
	if err := markNodeHeartbeat(node, ""); err != nil {
		return err
	}
	var run backupModels.InsightBackupRuns
	if err := global.App.DB.Where("id = ?", s.RunID).First(&run).Error; err != nil {
		return err
	}
	var task backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", run.TaskID).First(&task).Error; err != nil {
		return err
	}
	updates := map[string]any{
		"executor_node_id": node.ID,
		"log_path":         strings.TrimSpace(s.LogPath),
		"log_excerpt":      resolveLogExcerpt(strings.TrimSpace(s.LogPath), strings.TrimSpace(s.LogExcerpt)),
	}
	reportErrMsg := strings.TrimSpace(s.ErrorMessage)
	currentDBStatus := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	reportStorageTargetID := s.StorageTargetID
	if reportStorageTargetID > 0 {
		updates["storage_target_id"] = reportStorageTargetID
	} else if run.StorageTargetID > 0 {
		reportStorageTargetID = run.StorageTargetID
	}
	artifactPath := strings.TrimSpace(s.FilePath)
	storageArtifactPath := strings.TrimSpace(s.StorageArtifactPath)
	if storageArtifactPath != "" {
		artifactPath = storageArtifactPath
	}
	if reportStorageTargetID > 0 && storageArtifactPath != "" {
		_, prefixID, parseErr := ParseStorageArtifactPath(storageArtifactPath)
		if parseErr == nil && prefixID > 0 {
			if prefixID != reportStorageTargetID {
				appendBackupRunLog(strings.TrimSpace(s.LogPath), "WARN", "上报的 storage_artifact_path 存储目标ID=%d 与 run.storage_target_id=%d 不一致，强制以 run 为准", prefixID, reportStorageTargetID)
			}
		} else if parseErr != nil {
			appendBackupRunLog(strings.TrimSpace(s.LogPath), "WARN", "上报的 storage_artifact_path 不符合 st:<id>:/// 协议：%v，保留原值", parseErr)
		}
	}
	switch s.Status {
	case "RUNNING":
		if currentDBStatus == "STOP_REQUESTED" {
			if reportErrMsg != "" {
				updates["error_message"] = reportErrMsg
			}
			if storageArtifactPath != "" {
				updates["storage_artifact_path"] = storageArtifactPath
			}
			return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
		}
		if currentDBStatus == "SUCCESS" || currentDBStatus == "FAILED" || currentDBStatus == "STOPPED" {
			if reportErrMsg != "" {
				updates["error_message"] = reportErrMsg
			}
			if storageArtifactPath != "" {
				updates["storage_artifact_path"] = storageArtifactPath
			}
			return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
		}
		updates["status"] = "RUNNING"
		updates["error_message"] = reportErrMsg
		if time.Time(run.StartAt).IsZero() {
			updates["start_at"] = commonModels.LocalTime(nowLocalTime())
		}
		if storageArtifactPath != "" {
			updates["storage_artifact_path"] = storageArtifactPath
		}
		return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
	case "FAILED":
		if strings.EqualFold(strings.TrimSpace(string(run.Status)), "STOP_REQUESTED") {
			appendBackupRunLog(strings.TrimSpace(s.LogPath), "WARN", "节点已响应终止请求，结束当前备份")
			updates["status"] = "STOPPED"
			updates["end_at"] = commonModels.LocalTime(nowLocalTime())
			updates["error_message"] = "用户手动终止备份"
			if storageArtifactPath != "" {
				updates["storage_artifact_path"] = storageArtifactPath
			}
			return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
		}
		appendBackupRunLog(strings.TrimSpace(s.LogPath), "ERROR", "节点上报失败: %s", strings.TrimSpace(s.ErrorMessage))
		updates["status"] = "FAILED"
		updates["end_at"] = commonModels.LocalTime(nowLocalTime())
		updates["error_message"] = reportErrMsg
		if storageArtifactPath != "" {
			updates["storage_artifact_path"] = storageArtifactPath
		}
		return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
	case "STOPPED":
		appendBackupRunLog(strings.TrimSpace(s.LogPath), "WARN", "节点已终止当前备份")
		updates["status"] = "STOPPED"
		updates["end_at"] = commonModels.LocalTime(nowLocalTime())
		if reportErrMsg == "" {
			updates["error_message"] = "用户手动终止备份"
		} else {
			updates["error_message"] = reportErrMsg
		}
		if storageArtifactPath != "" {
			updates["storage_artifact_path"] = storageArtifactPath
		}
		return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
	case "SUCCESS":
		if strings.EqualFold(strings.TrimSpace(string(run.Status)), "STOP_REQUESTED") {
			appendBackupRunLog(strings.TrimSpace(s.LogPath), "WARN", "节点执行已完成，但服务端已收到终止请求，本次不写入备份集")
			updates["status"] = "STOPPED"
			updates["end_at"] = commonModels.LocalTime(nowLocalTime())
			updates["error_message"] = "用户手动终止备份"
			if storageArtifactPath != "" {
				updates["storage_artifact_path"] = storageArtifactPath
			}
			return global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(updates).Error
		}
		appendBackupRunLog(strings.TrimSpace(s.LogPath), "INFO", "节点上报成功 artifact=%s storage_target_id=%d", artifactPath, reportStorageTargetID)
		run.OutputDir = strings.TrimSpace(run.OutputDir)
		result := &BackupExecutionResult{
			ArtifactName:        strings.TrimSpace(s.FileName),
			ArtifactPath:    artifactPath,
			StorageArtifactPath: storageArtifactPath,
			SizeBytes:       s.SizeBytes,
			Sha256:          strings.TrimSpace(s.Sha256),
			ServerVersion:   strings.TrimSpace(s.ServerVersion),
			Tool:            strings.TrimSpace(s.Tool),
			ToolImage:       strings.TrimSpace(s.ToolImage),
			LogPath:         strings.TrimSpace(s.LogPath),
			OutputDir:       run.OutputDir,
			ObjectMode:      string(run.ObjectMode),
			ObjectSpec:     run.ObjectSpec,
			ObjectSummary:  run.ObjectSummary,
			SourceDBName:  task.SourceDBName,

			// 执行节点上报的备份集验证状态，落库后恢复预检直接读取，不再现场 find 附属文件。
			MetadataVerified: s.MetadataVerified,
			ChecksumVerified: s.ChecksumVerified,
		}
		if reportStorageTargetID > 0 {
			result.StorageTargetID = reportStorageTargetID
		}
		return finalizeBackupRunSuccess(&task, &run, result)
	default:
		return fmt.Errorf("未知状态")
	}
}

// ParseStorageArtifactPath 解析 st:<id>:///relative/path 协议，返回相对路径、存储目标ID、错误。
// 非协议格式返回 ("", 0, err)。
func ParseStorageArtifactPath(raw string) (string, uint64, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "st:") {
		return "", 0, fmt.Errorf("missing st: prefix")
	}
	rest := strings.TrimPrefix(s, "st:")
	// rest 应该形如 <id>:///path
	sepIdx := strings.Index(rest, ":///")
	if sepIdx < 0 {
		return "", 0, fmt.Errorf("missing :/// separator")
	}
	idStr := strings.TrimSpace(rest[:sepIdx])
	if idStr == "" {
		return "", 0, fmt.Errorf("empty storage target id")
	}
	id64, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid storage target id %q: %w", idStr, err)
	}
	rel := rest[sepIdx+len(":///"):]
	return rel, id64, nil
}

func (s *AgentRunStopStatusService) Run() (any, error) {
	node, err := authenticateBackupNode(s.NodeID, s.Token)
	if err != nil {
		return nil, err
	}
	if err := markNodeHeartbeat(node, s.AgentVersion); err != nil {
		return nil, err
	}
	var run backupModels.InsightBackupRuns
	if err := global.App.DB.Select("id, task_id, status, executor_node_id, updated_at").Where("id = ?", s.RunID).Take(&run).Error; err != nil {
		return nil, err
	}
	if run.ExecutorNodeID != 0 && run.ExecutorNodeID != node.ID {
		return nil, fmt.Errorf("执行记录不属于当前节点")
	}
	if run.ExecutorNodeID == 0 {
		var task backupModels.InsightBackupTasks
		if err := global.App.DB.Select("executor_node_id").Where("id = ?", run.TaskID).Take(&task).Error; err == nil {
			if task.ExecutorNodeID != 0 && task.ExecutorNodeID != node.ID {
				return nil, fmt.Errorf("执行记录不属于当前节点")
			}
		}
	}
	status := strings.ToUpper(strings.TrimSpace(string(run.Status)))
	return gin.H{
		"run_id":         run.ID,
		"status":         status,
		"stop_requested": status == "STOP_REQUESTED",
		"updated_at":     run.UpdatedAt,
	}, nil
}

func jsonRawToAny(raw []byte) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
