package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
)

type GetBackupTaskRunsService struct {
	TaskID string
	C      *gin.Context
}

func (s *GetBackupTaskRunsService) Run() (any, error) {
	if _, cleanupErr := cleanupStaleStopRequestedRunsForTask(s.TaskID); cleanupErr != nil {
		return nil, cleanupErr
	}
        if _, cleanupErr := cleanupStaleRunningRunsForTask(s.TaskID); cleanupErr != nil {
                return nil, cleanupErr
        }
	type row struct {
		ID                 uint64                 `json:"id"`
		RunSequence        int64                  `json:"run_sequence"`
		TaskID             string                 `json:"task_id"`
		Status             string                 `json:"status"`
		CreatedAt          commonModels.LocalTime `json:"created_at"`
		StartAt            commonModels.LocalTime `json:"start_at"`
		EndAt              commonModels.LocalTime `json:"end_at"`
		UpdatedAt          commonModels.LocalTime `json:"updated_at"`
		ExecutorNodeID     uint64                 `json:"executor_node_id"`
		ExecutorNodeName   string                 `json:"executor_node_name"`
		ExecutorNodeHost   string                 `json:"executor_node_host"`
		ExecutorNodeStatus string                 `json:"executor_node_status"`
		RepositoryNodeID   uint64                 `json:"repository_node_id"`
		RepositoryNodeName string                 `json:"repository_node_name"`
		BackupType         string                 `json:"backup_type"`
		ObjectMode         string                 `json:"object_mode"`
		ObjectSummary      string                 `json:"object_summary"`
		OutputDir          string                 `json:"output_dir"`
		DriverType         string                 `json:"driver_type"`
		StorageBackend     string                 `json:"storage_backend"`
		RetentionDays      int                    `json:"retention_days"`
		Tool               string                 `json:"tool"`
		ToolImage          string                 `json:"tool_image"`
		ServerVersion      string                 `json:"server_version"`
		FileName           string                 `json:"file_name"`
		FilePath           string                 `json:"file_path"`
		SizeBytes          int64                  `json:"size_bytes"`
		Sha256             string                 `json:"sha256"`
		LogPath            string                 `json:"log_path"`
		LogExcerpt         string                 `json:"log_excerpt"`
		ErrorMessage       string                 `json:"error_message"`
		CreatedBy          string                 `json:"created_by"`
		// TriggerSource 是落库的真实触发来源（MANUAL/SCHEDULED），升级前的历史行为空，
		// 由 resolveRunTriggerMode 回落到 created_by 推断。
		TriggerSource      string                 `json:"trigger_source"`
		// BackupTypeReason 本次全量/增量的判定说明，成功失败都有，前端用于解释「为什么这次是全量」。
		BackupTypeReason   string                 `json:"backup_type_reason"`
		SetID              uint64                 `json:"set_id"`
		BackupSetsID       string                 `json:"backup_sets_id"`
		SetName            string                 `json:"set_name"`
		SetFilePath        string                 `json:"set_file_path"`
		TriggerMode        string                 `json:"trigger_mode"`
		DurationSeconds    int64                  `json:"duration_seconds"`
		ProgressStageLabel string                 `json:"progress_stage_label"`
		StageLabel         string                 `json:"stage_label"`
		StopState          string                 `json:"stop_state"`
		StopStateLabel     string                 `json:"stop_state_label"`
		StopStateMessage   string                 `json:"stop_state_message"`
		StopStateHint      string                 `json:"stop_state_hint"`
		IsRunning          bool                   `json:"is_running"`
	}
	var list []row
	err := global.App.DB.Table("insight_backup_runs br").
		Select(`br.id, br.task_id, br.status, br.created_at, br.start_at, br.end_at, br.updated_at, br.executor_node_id,
			n.name as executor_node_name, n.host as executor_node_host, n.status as executor_node_status,
			br.repository_node_id, rn.name as repository_node_name,
			br.backup_type, br.object_mode, br.object_summary, br.output_dir, br.driver_type, br.storage_backend,
			br.retention_days, br.tool, br.tool_image, br.server_version, br.file_name, br.file_path,
		br.size_bytes, br.sha256, br.log_path, br.log_excerpt, br.error_message, br.created_by, br.trigger_source,
	br.backup_type_reason,
		bs.id as set_id, bs.backup_sets_id as backup_sets_id, bs.name as set_name, bs.file_path as set_file_path`).
		Joins("left join insight_backup_nodes n on n.id = br.executor_node_id").
		Joins("left join insight_backup_nodes rn on rn.id = br.repository_node_id").
		Joins("left join insight_backup_sets bs on bs.run_id = br.id").
		Where("br.task_id = ?", s.TaskID).
		Order("br.id desc").
		Limit(20).
		Scan(&list).Error
	if err != nil {
		return nil, err
	}
	var totalRuns int64
	if countErr := global.App.DB.Table("insight_backup_runs").
		Where("task_id = ?", s.TaskID).
		Count(&totalRuns).Error; countErr != nil {
		return nil, countErr
	}
	for i := range list {
                if isBackupRunStatusRunning(list[i].Status) {
                        if strings.TrimSpace(list[i].LogPath) != "" {
				if real, ok := resolveStorageRefToLocal(list[i].LogPath, 0); ok {
					if tail := tailLogExcerpt(real); strings.TrimSpace(tail) != "" {
						list[i].LogExcerpt = tail
					}
				} else if tail := tailLogExcerpt(list[i].LogPath); strings.TrimSpace(tail) != "" {
                                        list[i].LogExcerpt = tail
                                }
                        }
                        list[i].SizeBytes = liveBackupRunSize(list[i].FilePath, list[i].LogPath, list[i].SizeBytes, list[i].FilePath, 0)
                }
		list[i].RunSequence = totalRuns - int64(i)
		list[i].TriggerMode = resolveRunTriggerMode(list[i].TriggerSource, list[i].CreatedBy)
		list[i].DurationSeconds = calcRunDurationSeconds(list[i].StartAt, list[i].EndAt, list[i].Status)
		list[i].ProgressStageLabel = deriveRunProgressStageLabel(list[i].LogExcerpt, list[i].ErrorMessage)
		list[i].StageLabel = deriveRunStageLabel(list[i].Status, list[i].LogExcerpt, list[i].ErrorMessage)
		stopState := deriveRunStopState(list[i].Status, list[i].ProgressStageLabel, list[i].ExecutorNodeStatus, list[i].UpdatedAt)
		list[i].StopState = stopState.State
		list[i].StopStateLabel = stopState.Label
		list[i].StopStateMessage = stopState.Message
		list[i].StopStateHint = stopState.Hint
		list[i].IsRunning = isBackupRunStatusRunning(list[i].Status)
	}
	return list, nil
}

type GetBackupRunLogService struct {
	RunID uint64
	*backupForms.GetBackupRunLogForm
	C *gin.Context
}

func (s *GetBackupRunLogService) Run() (any, error) {
	if _, cleanupErr := cleanupStaleStopRequestedRun(s.RunID); cleanupErr != nil {
		return nil, cleanupErr
	}
        if _, cleanupErr := cleanupStaleRunningRun(s.RunID); cleanupErr != nil {
                return nil, cleanupErr
        }
	type row struct {
		ID                 uint64                 `json:"id"`
		Status             string                 `json:"status"`
		LogPath            string                 `json:"log_path"`
		LogExcerpt         string                 `json:"log_excerpt"`
		ErrorMessage       string                 `json:"error_message"`
		FilePath           string                 `json:"file_path"`
		FileName           string                 `json:"file_name"`
		StorageArtifactPath string                `json:"storage_artifact_path"`
		StorageTargetID    uint64                 `json:"storage_target_id"`
		UpdatedAt          commonModels.LocalTime `json:"updated_at"`
		StartAt            commonModels.LocalTime `json:"start_at"`
		EndAt              commonModels.LocalTime `json:"end_at"`
		ExecutorNodeID     uint64                 `json:"executor_node_id"`
		ExecutorNodeName   string                 `json:"executor_node_name"`
		RepositoryNodeID   uint64                 `json:"repository_node_id"`
		RepositoryNodeName string                 `json:"repository_node_name"`
	}
	var run row
	if err := global.App.DB.Table("insight_backup_runs br").
		Select("br.id, br.status, br.log_path, br.log_excerpt, br.error_message, br.file_path, br.file_name, br.storage_artifact_path, br.storage_target_id, br.updated_at, br.start_at, br.end_at, br.executor_node_id, n.name as executor_node_name, br.repository_node_id, rn.name as repository_node_name").
		Joins("left join insight_backup_nodes n on n.id = br.executor_node_id").
		Joins("left join insight_backup_nodes rn on rn.id = br.repository_node_id").
		Where("br.id = ?", s.RunID).
		Scan(&run).Error; err != nil {
		return nil, err
	}
	if run.ID == 0 {
		return nil, fmt.Errorf("执行记录不存在")
	}
	content := strings.TrimSpace(run.LogExcerpt)
	readMode := "db_excerpt"
	effectiveTailKB := normalizeTailKB(s.TailKB)
	if !isBackupRunStatusRunning(run.Status) && s.TailKB <= 0 {
		effectiveTailKB = 256
	}
	candidateLogPaths := resolveBackupRunLogPaths(run.StorageTargetID, strings.TrimSpace(run.LogPath), strings.TrimSpace(run.StorageArtifactPath))
	resolvedLogPath := ""
	bestSize := int64(-1)
	bestTail := ""
	for _, p := range candidateLogPaths {
		if tail, readErr := readTailText(p, effectiveTailKB); readErr == nil {
			trimmed := strings.TrimSpace(tail)
			if trimmed == "" {
				continue
			}
			sz := int64(len([]rune(trimmed)))
			if info, statErr := os.Stat(p); statErr == nil && info != nil {
				sz = info.Size()
			}
			if sz > bestSize {
				bestSize = sz
				bestTail = tail
				resolvedLogPath = p
				readMode = "file_tail"
			}
		}
	}
	if bestSize > 0 {
		content = bestTail
	}
	if content == "" && run.ErrorMessage != "" {
		content = run.ErrorMessage
	}
	reportedLogPath := run.LogPath
	if resolvedLogPath != "" && strings.TrimSpace(reportedLogPath) == "" {
		reportedLogPath = resolvedLogPath
	}
        liveSize := liveBackupRunSize(run.FilePath, run.LogPath, 0, run.StorageArtifactPath, run.StorageTargetID)
	logSize := int64(0)
	if resolvedLogPath != "" {
		logSize = logFileSize(resolvedLogPath)
	} else {
		logSize = logFileSize(run.LogPath)
	}
	if content == "" {
		content = "暂无日志内容。可能尚未开始执行，或执行节点上的日志文件未回传，仅保留了日志路径。"
	}
	return gin.H{
		"run_id":               run.ID,
		"status":               run.Status,
		"log_path":             reportedLogPath,
		"log_excerpt":          content,
		"read_mode":            readMode,
		"error_message":        run.ErrorMessage,
		"file_path":            run.FilePath,
		"file_name":            run.FileName,
		"executor_node_id":     run.ExecutorNodeID,
		"executor_node_name":   run.ExecutorNodeName,
		"repository_node_id":   run.RepositoryNodeID,
		"repository_node_name": run.RepositoryNodeName,
		"updated_at":           run.UpdatedAt,
		"start_at":             run.StartAt,
		"end_at":               run.EndAt,
		"log_size_bytes":       logSize,
                "live_size_bytes":      liveSize,
		"is_running":           isBackupRunStatusRunning(run.Status),
	}, nil
}

func normalizeTailKB(v int64) int64 {
	if v <= 0 {
		return 32
	}
	if v > 256 {
		return 256
	}
	return v
}

func readTailText(path string, tailKB int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	maxBytes := tailKB * 1024
	offset := int64(0)
	if size > maxBytes {
		offset = size - maxBytes
	}
	if _, seekErr := f.Seek(offset, 0); seekErr != nil {
		return "", seekErr
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func resolveLogExcerpt(logPath string, reportedExcerpt string) string {
	reported := strings.ToValidUTF8(strings.TrimSpace(reportedExcerpt), "")
	if reported != "" {
		if len(reported) > 4096 {
			runes := []rune(reported)
			if len(runes) > 1024 {
				runes = runes[len(runes)-1024:]
			}
			reported = strings.ToValidUTF8(string(runes), "")
		}
		return reported
	}
	return tailLogExcerpt(logPath)
}

func tailLogExcerpt(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	readPath := path
	if real, ok := resolveStorageRefToLocal(path, 0); ok {
		readPath = real
	}
	content, err := readTailText(readPath, 16)
	if err != nil {
		return ""
	}
	if len(content) > 4096 {
		content = content[len(content)-4096:]
	}
	content = strings.ToValidUTF8(strings.TrimSpace(content), "")
	if len(content) > 4096 {
		content = string([]rune(content))
		runes := []rune(content)
		if len(runes) > 1024 {
			runes = runes[len(runes)-1024:]
		}
		content = strings.ToValidUTF8(string(runes), "")
	}
	return content
}

func logFileSize(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	realPath, ok := resolveStorageRefToLocal(path, 0)
	if !ok {
		realPath = path
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

func liveBackupRunSize(filePath string, logPath string, currentSize int64, storageArtifactPath string, storageTargetID uint64) int64 {
        if currentSize > 0 {
                return currentSize
        }
	if strings.TrimSpace(filePath) != "" {
		if real, ok := resolveStorageRefToLocal(filePath, storageTargetID); ok {
			if size, err := pathSize(real); err == nil && size > 0 {
				return size
			}
		}
                if size, err := pathSize(strings.TrimSpace(filePath)); err == nil && size > 0 {
                        return size
                }
        }
	if strings.TrimSpace(logPath) != "" {
		if real, ok := resolveStorageRefToLocal(logPath, storageTargetID); ok {
			runDir := strings.TrimSpace(filepath.Dir(strings.TrimSpace(real)))
			if runDir != "" && runDir != "." && runDir != "/" {
				if size, err := pathSize(runDir); err == nil && size > 0 {
					return size
				}
			}
		}
                runDir := strings.TrimSpace(filepath.Dir(strings.TrimSpace(logPath)))
                if runDir != "" && runDir != "." && runDir != "/" {
                        if size, err := pathSize(runDir); err == nil && size > 0 {
                                return size
                        }
                }
        }
	if strings.TrimSpace(storageArtifactPath) != "" {
		if real, ok := resolveStorageRefToLocal(storageArtifactPath, storageTargetID); ok {
			runDir := strings.TrimSpace(filepath.Dir(strings.TrimSpace(real)))
			if runDir != "" && runDir != "." && runDir != "/" {
				if size, err := pathSize(runDir); err == nil && size > 0 {
					return size
				}
			}
		}
	}
        return currentSize
}

// resolveStorageRefToLocal 把 st:<id>:///rel/path 协议路径解析为 LOCAL_SERVER 存储目标本机真实路径。
// storageTargetID=0 时从协议里取 id；返回 (真实路径, 解析成功)。
// 非 LOCAL_SERVER 类型暂返回 ok=false。
func resolveStorageRefToLocal(path string, fallbackStorageTargetID uint64) (string, bool) {
	stid, relPath, target, ok := loadStorageTargetForPath(path, fallbackStorageTargetID)
	if !ok {
		return "", false
	}
	_ = stid
	_ = relPath
	if strings.ToUpper(strings.TrimSpace(string(target.Type))) != backupModels.StorageTargetTypeLocalServer {
		return "", false
	}
	base := strings.TrimSpace(target.BasePath)
	if base == "" {
		return "", false
	}
	joined := filepath.Join(base, relPath)
	info, err := os.Stat(joined)
	if err != nil || info == nil {
		return joined, false
	}
	return joined, true
}

// isObjectStorageRef 判断给定路径是否指向对象存储目标。
// 对 st:<id>:///path 引用，会查询对应存储目标的类型；对普通路径若提供了 fallbackStorageTargetID 也会检查。
func isObjectStorageRef(path string, fallbackStorageTargetID uint64) bool {
	_, _, target, ok := loadStorageTargetForPath(path, fallbackStorageTargetID)
	if !ok {
		return false
	}
	tp := strings.ToUpper(strings.TrimSpace(string(target.Type)))
	return tp != "" && tp != backupModels.StorageTargetTypeLocalServer
}

// loadStorageTargetForPath 解析 st: 引用或 fallback ID，加载存储目标基本信息。
// 返回 (storageTargetID, relPath, target, ok)。
func loadStorageTargetForPath(path string, fallbackStorageTargetID uint64) (uint64, string, *backupModels.InsightBackupStorageTargets, bool) {
	s := strings.TrimSpace(path)
	if s == "" {
		return 0, "", nil, false
	}
	var storageTargetID uint64
	var relPath string
	if strings.HasPrefix(s, "st:") {
		rel, id64, err := ParseStorageArtifactPath(s)
		if err != nil || id64 == 0 {
			return 0, "", nil, false
		}
		storageTargetID = id64
		relPath = strings.TrimPrefix(rel, "/")
	} else {
		if fallbackStorageTargetID == 0 {
			return 0, "", nil, false
		}
		storageTargetID = fallbackStorageTargetID
		relPath = strings.TrimPrefix(s, "/")
	}
	var target backupModels.InsightBackupStorageTargets
	if err := global.App.DB.Select("id, type, host, port, base_path").Where("id = ? AND deleted_at IS NULL", storageTargetID).Take(&target).Error; err != nil {
		return 0, "", nil, false
	}
	return storageTargetID, relPath, &target, true
}

// resolveBackupRunLogPaths 按优先级枚举 run.log 可能存在的本地路径，全部候选后 bestSize 取最大。
// 优先级：① logPath(st 协议解析) ② storageArtifactPath st 同目录 ③ logPath 旧路径 /tmp/media/backup/<rel> 映射到 storageTarget 下 <rel> 推断 ④ logPath 本地绝对
func resolveBackupRunLogPaths(storageTargetID uint64, logPath string, storageArtifactPath string) []string {
	out := make([]string, 0, 12)
	appendDistinct := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		for _, exist := range out {
			if exist == p {
				return
			}
		}
		out = append(out, p)
	}
	if strings.HasPrefix(logPath, "st:") {
		if real, ok := resolveStorageRefToLocal(logPath, storageTargetID); ok {
			appendDistinct(real)
		}
	}
	if storageTargetID > 0 && strings.HasPrefix(strings.TrimSpace(logPath), "/tmp/media/backup/") {
		rel := strings.TrimPrefix(strings.TrimSpace(logPath), "/tmp/media/backup/")
		rel = strings.TrimPrefix(rel, "/")
		if rel != "" {
			stcand := fmt.Sprintf("st:%d:///%s", storageTargetID, rel)
			if real, ok := resolveStorageRefToLocal(stcand, storageTargetID); ok {
				appendDistinct(real)
			}
		}
	}
	if logPath != "" && !strings.HasPrefix(logPath, "st:") {
		if real, ok := resolveStorageRefToLocal(logPath, storageTargetID); ok {
			appendDistinct(real)
		}
		appendDistinct(logPath)
	}
	if strings.HasPrefix(storageArtifactPath, "st:") {
		rel, _, err := ParseStorageArtifactPath(storageArtifactPath)
		if err == nil && strings.TrimSpace(rel) != "" {
			dir := filepath.Dir(strings.TrimSpace(rel))
			for _, cand := range []string{
				filepath.Join(dir, "run.log"),
				filepath.Join(dir, "backup.log"),
			} {
				stcand := storageArtifactPath[:strings.Index(storageArtifactPath, ":///")+len(":///")] + strings.TrimPrefix(cand, "/")
				if real, ok := resolveStorageRefToLocal(stcand, storageTargetID); ok {
					appendDistinct(real)
				}
			}
		}
		if realArt, ok := resolveStorageRefToLocal(storageArtifactPath, storageTargetID); ok {
			dir := filepath.Dir(realArt)
			appendDistinct(filepath.Join(dir, "run.log"))
			appendDistinct(filepath.Join(dir, "backup.log"))
		}
	}
	if storageTargetID > 0 && strings.HasPrefix(logPath, "st:") {
		if real, ok := resolveStorageRefToLocal(logPath, storageTargetID); ok {
			appendDistinct(real)
		}
	}
	return out
}

func deriveTriggerMode(createdBy string) string {
	if strings.EqualFold(strings.TrimSpace(createdBy), "system") {
		return "SCHEDULED"
	}
	return "MANUAL"
}

func calcRunDurationSeconds(startAt commonModels.LocalTime, endAt commonModels.LocalTime, status string) int64 {
	start := time.Time(startAt)
	if start.IsZero() {
		return 0
	}
	end := time.Time(endAt)
	if end.IsZero() && isBackupRunStatusRunning(status) {
		end = nowLocalTime()
	}
	if end.IsZero() || end.Before(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

func deriveRunStageLabel(status string, logExcerpt string, errorMessage string) string {
	progressStage := deriveRunProgressStageLabel(logExcerpt, errorMessage)
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING":
		return "等待执行节点领取"
	case "STOPPED":
		return "已终止"
	case "SUCCESS":
		return "执行完成"
	case "FAILED":
		if progressStage == "" && strings.TrimSpace(errorMessage) == "" {
			return "执行失败"
		}
	}
	if progressStage != "" {
		return progressStage
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RUNNING", "STOP_REQUESTED":
		return "执行中"
	case "FAILED":
		return "执行失败"
	default:
		return "-"
	}
}
