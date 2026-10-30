package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simajiong/jaysql/internal/backup/services"
	"gorm.io/datatypes"
)

type apiResponse struct {
	Code    string          `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

type agentStorageTarget struct {
	ID               uint64            `json:"id"`
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	Host             string            `json:"host"`
	Port             int               `json:"port"`
	BasePath         string            `json:"base_path"`
	SSHUser          string            `json:"ssh_user"`
	SSHPassword      string            `json:"ssh_password,omitempty"`
	SSHPrivateKey    string            `json:"ssh_private_key,omitempty"`
	ConnectTimeout   int               `json:"connect_timeout_seconds"`
	CustomEnv        map[string]string `json:"custom_env,omitempty"`
	S3Endpoint       string            `json:"s3_endpoint,omitempty"`
	S3Region         string            `json:"s3_region,omitempty"`
	S3Bucket         string            `json:"s3_bucket,omitempty"`
	S3AccessKey      string            `json:"s3_access_key,omitempty"`
	S3SecretKey      string            `json:"s3_secret_key,omitempty"`
	S3ForcePathStyle bool              `json:"s3_force_path_style,omitempty"`
}

type agentJob struct {
	RunID            uint64              `json:"run_id"`
	TaskID           string              `json:"task_id"`
	DBType           string              `json:"db_type"`
	DriverType       string              `json:"driver_type"`
	DriverConfig     any                 `json:"driver_config"`
	SourceInstanceID uint64              `json:"source_instance_id"`
	InstanceBizID    string              `json:"instance_biz_id"`
	RunSeq           uint64              `json:"run_seq"`
	SourceDBName     string              `json:"source_db_name"`
	BackupType       string              `json:"backup_type"`
	ObjectMode       string              `json:"object_mode"`
	ObjectSpec       any                 `json:"object_spec"`
	ObjectSummary    string              `json:"object_summary"`
	OutputBaseDir    string              `json:"output_base_dir"`
	LogPath          string              `json:"log_path"`
	Instance         agentInstance       `json:"instance"`
	StorageTargetID  uint64              `json:"storage_target_id"`
	StorageTarget    *agentStorageTarget `json:"storage_target,omitempty"`
}

type agentInstance struct {
	Hostname    string            `json:"hostname"`
	Port        int               `json:"port"`
	User        string            `json:"user"`
	Password    string            `json:"password"`
	DbType      string            `json:"db_type"`
	ExtraConfig map[string]string `json:"extra_config"`
}

func main() {
	server := flag.String("server", "http://127.0.0.1:8083", "JaySQL server base url")
	nodeID := flag.Uint64("node-id", 0, "backup node id")
	token := flag.String("token", "", "backup node token")
	interval := flag.Duration("interval", 5*time.Second, "poll interval")
	heartbeatInterval := flag.Duration("heartbeat-interval", 5*time.Second, "heartbeat interval")
	flag.Parse()

	if *nodeID == 0 || strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "node-id 和 token 必填")
		os.Exit(1)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	agentVersion := "backup-agent/0.1.0"
	baseURL := normalizeServerURL(*server)
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "server 参数无效")
		os.Exit(1)
	}
	if *heartbeatInterval <= 0 {
		*heartbeatInterval = 5 * time.Second
	}
	heartbeatPayload := map[string]any{
		"node_id":       *nodeID,
		"token":         *token,
		"agent_version": agentVersion,
	}
	services.SetBackupRunStopProbe(func(runID uint64) (bool, error) {
		var out struct {
			StopRequested bool   `json:"stop_requested"`
			Status        string `json:"status"`
		}
		err := postAgentRequest(httpClient, baseURL+"/api/v1/backup/agent/run-stop-status", map[string]any{
			"node_id":       *nodeID,
			"token":         *token,
			"run_id":        runID,
			"agent_version": agentVersion,
		}, &out)
		if err != nil {
			return false, err
		}
		return out.StopRequested || strings.EqualFold(strings.TrimSpace(out.Status), "STOP_REQUESTED"), nil
	})
	go startHeartbeatLoop(httpClient, baseURL+"/api/v1/backup/agent/heartbeat", heartbeatPayload, *heartbeatInterval)

	pollIdle := 0
	for {
		var job *agentJob
		if err := postAgentRequest(httpClient, baseURL+"/api/v1/backup/agent/poll", map[string]any{
			"node_id":       *nodeID,
			"token":         *token,
			"agent_version": agentVersion,
		}, &job); err != nil {
			fmt.Fprintln(os.Stderr, "poll failed:", err)
			time.Sleep(*interval)
			continue
		}
		if job == nil || job.RunID == 0 {
			pollIdle++
			if pollIdle%12 == 1 {
				fmt.Fprintf(os.Stderr, "[poll] node=%d idle count=%d no pending run claimed\n", *nodeID, pollIdle)
			}
			time.Sleep(*interval)
			continue
		}
		pollIdle = 0
		fmt.Fprintf(os.Stderr, "[poll] node=%d claimed run=%d task=%s db=%s mode=%s driver=%s st_id=%d\n", *nodeID, job.RunID, job.TaskID, job.DBType, job.ObjectMode, job.DriverType, job.StorageTargetID)
		report := executeClaimedJob(httpClient, baseURL, *nodeID, *token, job)
		if err := postAgentReportWithRetry(httpClient, baseURL+"/api/v1/backup/agent/report", report); err != nil {
			fmt.Fprintln(os.Stderr, "report failed:", err)
		}
	}
}

func executeClaimedJob(httpClient *http.Client, baseURL string, nodeID uint64, token string, job *agentJob) (report map[string]any) {
	report = map[string]any{
		"node_id": nodeID,
		"token":   token,
		"run_id":  job.RunID,
	}
	finalLogPath := strings.TrimSpace(job.LogPath)
	var remoteLogWriter io.WriteCloser
	var remoteLogRelPath string
	var remoteLogTargetID uint64

	stopRunReportLoop := startBackupRunReportLoop(httpClient, baseURL+"/api/v1/backup/agent/report", map[string]any{
		"node_id":  nodeID,
		"token":    token,
		"run_id":   job.RunID,
		"status":   "RUNNING",
		"log_path": job.LogPath,
	}, job.LogPath, 10*time.Second)
	defer stopRunReportLoop()
	defer func() {
		if remoteLogWriter != nil {
			_ = remoteLogWriter.Close()
		}
	}()
	defer func() {
		if rec := recover(); rec != nil {
			stack := strings.ToValidUTF8(string(debug.Stack()), "")
			fmt.Fprintf(os.Stderr, "[panic] run=%d task=%s: %v\n%s\n", job.RunID, job.TaskID, rec, stack)
			if finalLogPath != "" {
				_, _ = os.Stderr.WriteString("")
			}
			report["status"] = "FAILED"
			report["log_path"] = finalLogPath
			report["error_message"] = fmt.Sprintf("backup-agent panic: %v", rec)
			if strings.TrimSpace(stack) != "" {
				if len(stack) > 128*1024 {
					stack = stack[len(stack)-128*1024:]
				}
				report["log_excerpt"] = stack
			}
			if remoteLogWriter == nil && job.StorageTargetID > 0 && job.StorageTarget != nil && finalLogPath != "" {
				if logRel, logStID, logErr := transferLogToStorageTarget(job, finalLogPath); logErr == nil {
					report["log_path"] = fmt.Sprintf("st:%d:///%s", logStID, logRel)
				}
			} else if remoteLogTargetID > 0 && remoteLogRelPath != "" {
				report["log_path"] = fmt.Sprintf("st:%d:///%s", remoteLogTargetID, remoteLogRelPath)
			}
		}
	}()

	objectSpec, _ := json.Marshal(job.ObjectSpec)
	driverConfig, _ := json.Marshal(job.DriverConfig)
	useDirectStream := false
	var streamSink services.BackupStreamSink
	var streamRelPath string
	var streamTargetID uint64
	// 零落盘直传：LOCAL_SERVER（SSH）与对象存储（S3 兼容）都建立可写流，
	// 让导出工具边产生边上传，避免 GB 级备份先在 agent 本地落盘再搬运。
	storageTargetType := ""
	if job.StorageTarget != nil {
		storageTargetType = strings.ToUpper(strings.TrimSpace(job.StorageTarget.Type))
	}
	useObjectStorage := services.IsObjectStorageType(storageTargetType)
	if job.StorageTargetID > 0 && job.StorageTarget != nil &&
		(strings.EqualFold(storageTargetType, "LOCAL_SERVER") || useObjectStorage) {
		artifactPlan, planErr := services.PlanBackupArtifact(job.DBType, job.DriverType, job.ObjectMode, job.SourceDBName, job.BackupType, datatypes.JSON(objectSpec), datatypes.JSON(driverConfig))
		if planErr != nil {
			fmt.Fprintf(os.Stderr, "[DirectStream] plan artifact failed, fallback to local-then-upload: %v\n", planErr)
		} else {
			var (
				relSink services.BackupStreamSink
				relPath string
				openErr error
			)
			if useObjectStorage {
				relSink, relPath, openErr = openObjectStorageStreamSink(job, artifactPlan.ArtifactName)
			} else {
				relSink, relPath, openErr = openLocalServerStreamSink(job, artifactPlan.ArtifactName)
			}
			if openErr != nil {
				if strings.EqualFold(strings.TrimSpace(job.DriverType), "XTRABACKUP") {
					fmt.Fprintf(os.Stderr, "[DirectStream] open sink failed for MySQL physical backup: %v\n", openErr)
				} else {
					fmt.Fprintf(os.Stderr, "[DirectStream] open sink failed, fallback to local-then-upload: %v\n", openErr)
				}
			} else {
				useDirectStream = true
				streamSink = relSink
				streamRelPath = relPath
				streamTargetID = job.StorageTargetID
				fmt.Fprintf(os.Stderr, "[DirectStream] zero-disk sink opened. db=%s driver=%s artifact=%s target=%s\n", job.DBType, job.DriverType, artifactPlan.ArtifactName, streamSink.RemoteTarget())
				var (
					logW       io.WriteCloser
					logRel     string
					logStID    uint64
					logOpenErr error
				)
				if useObjectStorage {
					logW, logRel, logStID, logOpenErr = openObjectStorageLogSink(job)
				} else {
					logW, logRel, logStID, logOpenErr = openLocalServerLogSink(job)
				}
				if logOpenErr == nil {
					remoteLogWriter = logW
					remoteLogRelPath = logRel
					remoteLogTargetID = logStID
				} else {
					fmt.Fprintf(os.Stderr, "[DirectStream] open remote run.log sink ignored: %v\n", logOpenErr)
				}
			}
		}
	}
	execInput := services.BackupExecutionInput{
		RunID:            job.RunID,
		TaskID:           job.TaskID,
		SourceInstanceID: job.SourceInstanceID,
		InstanceBizID:    job.InstanceBizID,
		RunSeq:           job.RunSeq,
		DBType:           job.DBType,
		DriverType:       job.DriverType,
		DriverConfig:     datatypes.JSON(driverConfig),
		SourceDBName:     job.SourceDBName,
		BackupType:       job.BackupType,
		ObjectMode:       job.ObjectMode,
		ObjectSpec:       datatypes.JSON(objectSpec),
		ObjectSummary:    job.ObjectSummary,
		OutputBaseDir:    job.OutputBaseDir,
		Instance: services.BackupExecutionInstance{
			Hostname:    job.Instance.Hostname,
			Port:        job.Instance.Port,
			User:        job.Instance.User,
			Password:    job.Instance.Password,
			DBType:      job.Instance.DbType,
			ExtraConfig: mustJSON(job.Instance.ExtraConfig),
		},
		StorageTarget:  toExecutionStorageTarget(job.StorageTargetID, job.StorageTarget),
		DirectStream:   useDirectStream,
		StreamSink:     streamSink,
		ExtraLogWriter: remoteLogWriter,
	}
	result, err := services.ExecuteBackupJob(execInput)
	if result == nil {
		result = &services.BackupExecutionResult{}
	}
	finalLogPath = strings.TrimSpace(result.LogPath)
	if finalLogPath == "" {
		finalLogPath = strings.TrimSpace(job.LogPath)
	}
	if tail, tailErr := readAgentTailText(finalLogPath, 64); tailErr == nil && strings.TrimSpace(tail) != "" {
		if len(tail) > 128*1024 {
			tail = tail[len(tail)-128*1024:]
		}
		report["log_excerpt"] = tail
	}
	if err != nil {
		// 直传失败时主动 abort 分片上传，避免在对象存储里残留未完成的 multipart 分片。
		if streamSink != nil {
			if aborter, ok := streamSink.(interface{ Abort() error }); ok {
				if abortErr := aborter.Abort(); abortErr != nil {
					fmt.Fprintf(os.Stderr, "[DirectStream] abort sink failed: %v\n", abortErr)
				}
			}
		}
		if remoteLogWriter == nil && job.StorageTargetID > 0 && job.StorageTarget != nil {
			if logRel, logStID, logErr := transferLogToStorageTarget(job, finalLogPath); logErr == nil {
				finalLogPath = fmt.Sprintf("st:%d:///%s", logStID, logRel)
			}
		} else if remoteLogTargetID > 0 && remoteLogRelPath != "" {
			finalLogPath = fmt.Sprintf("st:%d:///%s", remoteLogTargetID, remoteLogRelPath)
		}
		report["log_path"] = finalLogPath
		if services.IsBackupRunStoppedError(err) {
			report["status"] = "STOPPED"
			report["error_message"] = "用户手动停止备份"
		} else {
			report["status"] = "FAILED"
			report["error_message"] = err.Error()
		}
		return report
	}

	report["status"] = "SUCCESS"
	report["file_name"] = result.ArtifactName
	report["file_path"] = result.ArtifactPath
	report["size_bytes"] = result.SizeBytes
	report["sha256"] = result.Sha256
	report["server_version"] = result.ServerVersion
	report["tool"] = result.Tool
	report["tool_image"] = result.ToolImage
	// 备份集验证状态：执行节点写完附属文件并回读校验后随成功上报回传主服务，
	// 否则主服务侧只能拿到零值，落库后恢复预检仍会判定为未验证。
	report["metadata_verified"] = result.MetadataVerified
	report["checksum_verified"] = result.ChecksumVerified
	if useDirectStream {
		report["storage_target_id"] = streamTargetID
		report["storage_artifact_path"] = fmt.Sprintf("st:%d:///%s", streamTargetID, streamRelPath)
		result.StorageTargetID = streamTargetID
		fmt.Fprintf(os.Stderr, "[DirectStream] dump succeeded zero-copy sink. st_id=%d rel=%s size=%d bytes\n", streamTargetID, streamRelPath, result.SizeBytes)
		report["file_path"] = report["storage_artifact_path"]
		result.ArtifactPath = report["file_path"].(string)
		if remoteLogTargetID > 0 && remoteLogRelPath != "" {
			finalLogPath = fmt.Sprintf("st:%d:///%s", remoteLogTargetID, remoteLogRelPath)
		}
		// 对象存储直传：附属文件（metadata.json / checksums.sha256）落在本地批次目录，
		// 需要单独补传，否则恢复侧的"元数据/校验和"证据只存在于 agent 磁盘上。
		if useObjectStorage {
			publishObjectStorageExtras(job, result, "")
		}
	} else if job.StorageTargetID > 0 && job.StorageTarget != nil {
		relPath, stID, xferErr := transferArtifactToStorageTarget(job, result.ArtifactPath, result.ArtifactName)
		if xferErr != nil {
			report["status"] = "FAILED"
			// 不能用 fmt.Sprint(report["error_message"])：未设置时该键为 nil，
			// fmt.Sprint(nil) 会得到字符串 "<nil>"，最终错误信息变成 "<nil>; 备份产物…"。
			existing := ""
			if v, ok := report["error_message"].(string); ok {
				existing = strings.TrimSpace(v)
			}
			if existing != "" {
				existing += "; "
			}
			report["error_message"] = existing + "备份产物上传存储目标失败：" + xferErr.Error()
		} else {
			report["storage_target_id"] = stID
			report["storage_artifact_path"] = fmt.Sprintf("st:%d:///%s", stID, relPath)
			result.StorageTargetID = stID
			if useObjectStorage {
				// 先补传附属文件与日志，再做本地清理（清理会删掉整个批次目录）。
				if logRef := publishObjectStorageExtras(job, result, finalLogPath); logRef != "" {
					finalLogPath = logRef
				}
			}
			cleanupLocalArtifactAfterTransfer(job, result.ArtifactPath)
			if !useObjectStorage {
				if logRel, logStID, logErr := transferLogToStorageTarget(job, finalLogPath); logErr == nil {
					finalLogPath = fmt.Sprintf("st:%d:///%s", logStID, logRel)
				}
			}
		}
	} else {
		cleanupLocalArtifactAfterTransfer(job, result.ArtifactPath)
	}
	report["log_path"] = finalLogPath
	return report
}

func toExecutionStorageTarget(storageTargetID uint64, target *agentStorageTarget) *services.BackupExecutionStorageTarget {
	if target == nil && storageTargetID == 0 {
		return nil
	}
	result := &services.BackupExecutionStorageTarget{
		ID: storageTargetID,
	}
	if target == nil {
		return result
	}
	if target.ID > 0 {
		result.ID = target.ID
	}
	result.Type = target.Type
	result.Host = target.Host
	result.Port = target.Port
	result.BasePath = target.BasePath
	result.SSHUser = target.SSHUser
	result.SSHPassword = target.SSHPassword
	result.SSHPrivateKey = target.SSHPrivateKey
	// 对象存储字段：基线读取（LIST+GET）与元数据回写（PUT）在 OSS 场景走 S3 链路，
	// 缺了这组字段会让增量备份拿不到 LSN 基线而被迫降级全量。
	result.S3Endpoint = target.S3Endpoint
	result.S3Region = target.S3Region
	result.S3Bucket = target.S3Bucket
	result.S3AccessKey = target.S3AccessKey
	result.S3SecretKey = target.S3SecretKey
	result.S3ForcePathStyle = target.S3ForcePathStyle
	return result
}

func startHeartbeatLoop(client *http.Client, endpoint string, payload map[string]any, interval time.Duration) {
	sendHeartbeat := func() {
		if err := postAgentRequest(client, endpoint, payload, nil); err != nil {
			fmt.Fprintln(os.Stderr, "heartbeat failed:", err)
		}
	}
	sendHeartbeat()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		sendHeartbeat()
	}
}

func postAgentReportWithRetry(client *http.Client, endpoint string, payload map[string]any) error {
	var lastErr error
	for attempt := 1; attempt <= 12; attempt++ {
		if err := postAgentRequest(client, endpoint, payload, nil); err == nil {
			if attempt > 1 {
				fmt.Fprintf(os.Stderr, "report recovered after retry %d\n", attempt)
			}
			return nil
		} else {
			lastErr = err
			fmt.Fprintf(os.Stderr, "report retry %d failed: %v\n", attempt, err)
		}
		time.Sleep(5 * time.Second)
	}
	return lastErr
}

func postAgentRequest(client *http.Client, endpoint string, payload map[string]any, out any) error {
	var errs []string
	if err := doJSONRequest(client, endpoint, payload, out); err == nil {
		return nil
	} else {
		errs = append(errs, "json_post: "+err.Error())
	}
	if err := doFormRequest(client, http.MethodPost, endpoint, payload, out); err == nil {
		return nil
	} else {
		errs = append(errs, "form_post: "+err.Error())
	}
	if err := doFormRequest(client, http.MethodGet, endpoint, payload, out); err == nil {
		return nil
	} else {
		errs = append(errs, "query_get: "+err.Error())
	}
	return errors.New(strings.Join(errs, "; "))
}

func doJSONRequest(client *http.Client, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doRequest(client, req, out)
}

func doFormRequest(client *http.Client, method string, endpoint string, payload map[string]any, out any) error {
	values := url.Values{}
	for k, v := range payload {
		values.Set(k, fmt.Sprint(v))
	}
	var req *http.Request
	var err error
	if method == http.MethodGet {
		u, parseErr := url.Parse(endpoint)
		if parseErr != nil {
			return parseErr
		}
		q := u.Query()
		for k, list := range values {
			for _, item := range list {
				q.Set(k, item)
			}
		}
		u.RawQuery = q.Encode()
		req, err = http.NewRequest(http.MethodGet, u.String(), nil)
	} else {
		req, err = http.NewRequest(http.MethodPost, endpoint, strings.NewReader(values.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return err
	}
	return doRequest(client, req, out)
}

func doRequest(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var parsed apiResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return err
	}
	if parsed.Code != "0000" {
		return fmt.Errorf(parsed.Message)
	}
	if out == nil || len(parsed.Data) == 0 || string(parsed.Data) == "null" {
		return nil
	}
	return json.Unmarshal(parsed.Data, out)
}

func mustJSON(v any) datatypes.JSON {
	if v == nil {
		return nil
	}
	buf, _ := json.Marshal(v)
	return datatypes.JSON(buf)
}

func normalizeServerURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "`")
	s = strings.Trim(s, "'")
	s = strings.Trim(s, "\"")
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, "/")
}

func startBackupRunReportLoop(client *http.Client, endpoint string, payload map[string]any, logPath string, interval time.Duration) func() {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	buildPayload := func() map[string]any {
		cp := make(map[string]any, len(payload)+1)
		for k, v := range payload {
			cp[k] = v
		}
		if strings.TrimSpace(logPath) != "" {
			if tail, err := readAgentTailText(logPath, 32); err == nil && strings.TrimSpace(tail) != "" {
				if len(tail) > 128*1024 {
					tail = tail[len(tail)-128*1024:]
				}
				cp["log_excerpt"] = tail
			}
		}
		return cp
	}
	send := func() {
		if err := postAgentRequest(client, endpoint, buildPayload(), nil); err != nil {
			fmt.Fprintln(os.Stderr, "running report failed:", err)
		}
	}
	send()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

func readAgentTailText(path string, tailKB int64) (string, error) {
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
	return strings.ToValidUTF8(string(buf), ""), nil
}

func debugReport(runID string, hypothesisID string, location string, msg string, data map[string]any) {
	debugURL, debugSession := readDebugEnv(".dbg/backup-progress-stuck.env", "http://127.0.0.1:7777/event", "backup-progress-stuck")
	body, err := json.Marshal(map[string]any{
		"sessionId":    debugSession,
		"runId":        runID,
		"hypothesisId": hypothesisID,
		"location":     location,
		"msg":          msg,
		"data":         data,
		"ts":           time.Now().UnixMilli(),
	})
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, debugURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func readDebugEnv(path string, defaultURL string, defaultSession string) (string, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return defaultURL, defaultSession
	}
	debugURL := defaultURL
	debugSession := defaultSession
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "DEBUG_SERVER_URL=") {
			debugURL = strings.TrimSpace(strings.TrimPrefix(line, "DEBUG_SERVER_URL="))
		}
		if strings.HasPrefix(line, "DEBUG_SESSION_ID=") {
			debugSession = strings.TrimSpace(strings.TrimPrefix(line, "DEBUG_SESSION_ID="))
		}
	}
	return debugURL, debugSession
}

func agentShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func transferArtifactToStorageTarget(job *agentJob, localArtifactPath string, artifactName string) (string, uint64, error) {
	if job == nil {
		return "", 0, fmt.Errorf("job 为空")
	}
	st := job.StorageTarget
	if st == nil {
		return "", 0, fmt.Errorf("storage target 配置为空")
	}
	stID := st.ID
	if stID == 0 {
		stID = job.StorageTargetID
	}
	if stID == 0 {
		return "", 0, fmt.Errorf("storage target id 为 0")
	}
	targetType := strings.ToUpper(strings.TrimSpace(st.Type))
	if targetType == "" {
		targetType = "LOCAL_SERVER"
	}
	// LOCAL_SERVER 的 base_path 是远端目录；对象存储的 base_path 是可选的桶内前缀。
	// 对象存储允许留空，并将完整 Key 保存到备份集引用。
	runDirSpec := services.BackupRunDirSpec{
		SourceInstanceID: job.SourceInstanceID,
		InstanceBizID:    job.InstanceBizID,
		TaskID:           job.TaskID,
		RunID:            job.RunID,
		RunSeq:           job.RunSeq,
	}
	relArtifactPath := runDirSpec.RelPath(artifactName)
	localPath := strings.TrimSpace(localArtifactPath)
	if localPath == "" {
		return relArtifactPath, stID, fmt.Errorf("本地备份产物路径为空")
	}
	if _, err := os.Stat(localPath); err != nil {
		return relArtifactPath, stID, fmt.Errorf("本地备份产物不可读: %w", err)
	}
	switch targetType {
	case "LOCAL_SERVER":
		basePath := filepath.Clean(strings.TrimSpace(st.BasePath))
		if basePath == "" || basePath == "." {
			return relArtifactPath, stID, fmt.Errorf("LOCAL_SERVER 存储目标 base_path 为空：请在存储目标中填写备份根目录（如 /backup）")
		}
		remoteDir := runDirSpec.AbsDir(basePath)
		host := strings.TrimSpace(st.Host)
		if host == "" {
			return relArtifactPath, stID, fmt.Errorf("LOCAL_SERVER 存储目标缺少 host")
		}
		port := st.Port
		if port <= 0 {
			port = 22
		}
		user := strings.TrimSpace(st.SSHUser)
		if user == "" {
			return relArtifactPath, stID, fmt.Errorf("LOCAL_SERVER 存储目标缺少 ssh_user")
		}
		sshPass := strings.TrimSpace(st.SSHPassword)
		sshKey := strings.TrimSpace(st.SSHPrivateKey)
		if sshPass == "" && sshKey == "" {
			return relArtifactPath, stID, fmt.Errorf("LOCAL_SERVER 存储目标缺少 SSH 凭据（密码或私钥）")
		}
		hostIdent := fmt.Sprintf("%s@%s", user, host)
		timeoutSec := st.ConnectTimeout
		if timeoutSec <= 0 {
			timeoutSec = 30
		}
		usePassword := sshKey == "" && sshPass != ""
		batchMode := "BatchMode=yes"
		if usePassword {
			batchMode = "BatchMode=no"
		}
		commonOpts := []string{
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ConnectTimeout=" + strconv.Itoa(timeoutSec),
			"-o", "ServerAliveInterval=30",
			"-o", "ServerAliveCountMax=3",
			"-o", batchMode,
		}
		if usePassword {
			commonOpts = append(commonOpts,
				"-o", "PreferredAuthentications=password",
				"-o", "PubkeyAuthentication=no",
				"-o", "NumberOfPasswordPrompts=1",
			)
		}
		var tmpKeyFile string
		if sshKey != "" {
			f, err := os.CreateTemp("", "goinsight-agent-id-*")
			if err != nil {
				return relArtifactPath, stID, fmt.Errorf("创建临时 SSH 私钥文件失败: %w", err)
			}
			tmpKeyFile = f.Name()
			if _, werr := f.WriteString(strings.TrimSpace(sshKey) + "\n"); werr != nil {
				_ = f.Close()
				_ = os.Remove(tmpKeyFile)
				return relArtifactPath, stID, fmt.Errorf("写入临时 SSH 私钥文件失败: %w", werr)
			}
			_ = f.Close()
			_ = os.Chmod(tmpKeyFile, 0o600)
			defer os.Remove(tmpKeyFile)
			commonOpts = append(commonOpts, "-i", tmpKeyFile, "-o", "IdentitiesOnly=yes")
		}
		var envPrefix []string
		if usePassword {
			if _, lookErr := exec.LookPath("sshpass"); lookErr != nil {
				return relArtifactPath, stID, fmt.Errorf("本机缺少 sshpass 工具，无法使用密码登录 SSH 传输备份产物，请安装 sshpass 后重试")
			}
			envPrefix = append(envPrefix, "SSHPASS="+sshPass)
		}
		portStr := strconv.Itoa(port)
		mkdirScript := fmt.Sprintf("mkdir -p %s", agentShellQuote(remoteDir))
		var sshArgs []string
		for i := 0; i < len(commonOpts); i += 2 {
			if i+1 >= len(commonOpts) {
				break
			}
			sshArgs = append(sshArgs, commonOpts[i], commonOpts[i+1])
		}
		sshArgs = append(sshArgs, "-p", portStr, hostIdent, mkdirScript)
		var sshCmd *exec.Cmd
		if len(envPrefix) > 0 {
			sshFullArgs := append([]string{"-e", "ssh"}, sshArgs...)
			sshCmd = exec.Command("sshpass", sshFullArgs...)
			sshCmd.Env = append(os.Environ(), envPrefix...)
		} else {
			sshCmd = exec.Command("ssh", sshArgs...)
		}
		sshCmd.Stderr = os.Stderr
		sshCmd.Stdout = os.Stderr
		if runErr := sshCmd.Run(); runErr != nil {
			return relArtifactPath, stID, fmt.Errorf("SSH 创建远程目录失败 host=%s dir=%s: %w", host, remoteDir, runErr)
		}
		remoteTarget := fmt.Sprintf("%s:%s", hostIdent, filepath.Join(remoteDir, filepath.Base(artifactName)))
		scpArgs := append([]string{}, commonOpts...)
		scpArgs = append(scpArgs, "-P", portStr, localPath, remoteTarget)
		var scpCmd *exec.Cmd
		if len(envPrefix) > 0 {
			scpFullArgs := append([]string{"-e", "scp"}, scpArgs...)
			scpCmd = exec.Command("sshpass", scpFullArgs...)
			scpCmd.Env = append(os.Environ(), envPrefix...)
		} else {
			scpCmd = exec.Command("scp", scpArgs...)
		}
		scpCmd.Stderr = os.Stderr
		scpCmd.Stdout = os.Stderr
		if scpErr := scpCmd.Run(); scpErr != nil {
			return relArtifactPath, stID, fmt.Errorf("SCP 上传备份产物失败 local=%s remote=%s: %w", localPath, remoteTarget, scpErr)
		}
		fmt.Fprintf(os.Stderr, "storage transfer ok: %s -> %s\n", localPath, remoteTarget)
		return relArtifactPath, stID, nil
	case "OSS_ALIYUN", "S3_MINIO", "COS_TENCENT", "OBS_HUAWEI":
		relArtifactPath = objectStorageRunDirSpec(job).RelPath(artifactName)
		// 对象存储：本地产物走并发分片上传；大文件（XTRABACKUP/PG_BACKREST）
		// 应优先命中 DirectStream 零落盘路径，不会走到这里。
		if err := uploadLocalFileToObjectStorage(job, localPath, relArtifactPath); err != nil {
			return relArtifactPath, stID, err
		}
		return relArtifactPath, stID, nil
	default:
		return relArtifactPath, stID, fmt.Errorf("未知的存储目标类型 %s", targetType)
	}
}

type sshRemoteCatSink struct {
	targetLabel string
	sshCmd      *exec.Cmd
	stdin       io.WriteCloser
	waitOnce    sync.Once
	waitErr     error
	remotePath  string
	cleanupTemp []string
}

func (s *sshRemoteCatSink) RemoteTarget() string {
	return s.targetLabel
}

func (s *sshRemoteCatSink) Write(p []byte) (int, error) {
	return s.stdin.Write(p)
}

func (s *sshRemoteCatSink) Close() error {
	var firstErr error
	if cerr := s.stdin.Close(); cerr != nil && firstErr == nil {
		firstErr = cerr
	}
	s.waitOnce.Do(func() {
		s.waitErr = s.sshCmd.Wait()
	})
	if s.waitErr != nil && firstErr == nil {
		firstErr = fmt.Errorf("远端 sink ssh 进程退出失败: %w", s.waitErr)
	}
	for _, t := range s.cleanupTemp {
		if t != "" {
			_ = os.Remove(t)
		}
	}
	return firstErr
}

func openLocalServerStreamSink(job *agentJob, artifactName string) (services.BackupStreamSink, string, error) {
	st := job.StorageTarget
	if st == nil {
		return nil, "", fmt.Errorf("缺少存储目标信息")
	}
	host := strings.TrimSpace(st.Host)
	port := st.Port
	if port <= 0 {
		port = 22
	}
	user := strings.TrimSpace(st.SSHUser)
	basePath := strings.TrimSpace(strings.TrimRight(st.BasePath, "/"))
	password := strings.TrimSpace(st.SSHPassword)
	privateKey := strings.TrimSpace(st.SSHPrivateKey)
	if host == "" {
		return nil, "", fmt.Errorf("LOCAL_SERVER 存储目标缺少 host")
	}
	if user == "" {
		return nil, "", fmt.Errorf("LOCAL_SERVER 存储目标缺少 ssh_user")
	}
	if basePath == "" {
		basePath = "/data/backup"
	}
	runDirSpec := services.BackupRunDirSpec{
		SourceInstanceID: job.SourceInstanceID,
		InstanceBizID:    job.InstanceBizID,
		TaskID:           job.TaskID,
		RunID:            job.RunID,
		RunSeq:           job.RunSeq,
	}
	relArtifactPath := runDirSpec.RelPath(artifactName)
	remoteDir := runDirSpec.AbsDir(basePath)
	remotePath := basePath + "/" + relArtifactPath
	timeoutSec := 30
	batchMode := "BatchMode=yes"
	usePassword := false
	var tempFiles []string
	if password != "" {
		batchMode = "BatchMode=no"
		usePassword = true
		if _, lookErr := exec.LookPath("sshpass"); lookErr != nil {
			return nil, "", fmt.Errorf("本机缺少 sshpass 工具，无法使用密码登录 SSH 传输备份产物，请安装 sshpass 后重试")
		}
	}
	commonOpts := []string{
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=" + strconv.Itoa(timeoutSec), "-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3", "-o", batchMode,
	}
	if usePassword {
		commonOpts = append(commonOpts,
			"-o", "PreferredAuthentications=password",
			"-o", "PubkeyAuthentication=no",
			"-o", "NumberOfPasswordPrompts=1",
		)
	}
	hostIdent := user + "@" + host
	portStr := strconv.Itoa(port)
	var sshBaseArgs []string
	var sshBaseEnv []string
	if usePassword {
		sshBaseArgs = []string{"-e", "ssh"}
		sshBaseEnv = []string{"SSHPASS=" + password}
	} else if privateKey != "" {
		tmp, tmpErr := os.CreateTemp("", "goinsight-ssh-key-*")
		if tmpErr != nil {
			return nil, "", fmt.Errorf("创建临时 SSH 私钥文件失败: %w", tmpErr)
		}
		keyPath := tmp.Name()
		tempFiles = append(tempFiles, keyPath)
		if _, werr := tmp.WriteString(privateKey); werr != nil {
			tmp.Close()
			_ = os.Remove(keyPath)
			return nil, "", fmt.Errorf("写入临时 SSH 私钥文件失败: %w", werr)
		}
		tmp.Close()
		if chmodErr := os.Chmod(keyPath, 0600); chmodErr != nil {
			_ = os.Remove(keyPath)
			return nil, "", fmt.Errorf("设置临时 SSH 私钥权限失败: %w", chmodErr)
		}
		commonOpts = append(commonOpts, "-i", keyPath)
		sshBaseArgs = []string{}
	} else {
		return nil, "", fmt.Errorf("LOCAL_SERVER 存储目标缺少 SSH 凭据（密码或私钥）")
	}
	var sshOpts []string
	for i := 0; i+1 < len(commonOpts); i += 2 {
		sshOpts = append(sshOpts, commonOpts[i], commonOpts[i+1])
	}
	mkdirArgs := append([]string{}, sshBaseArgs...)
	mkdirArgs = append(mkdirArgs, sshOpts...)
	mkdirArgs = append(mkdirArgs, "-p", portStr, hostIdent, "umask 022 && mkdir -p "+shellQuote(remoteDir))
	var mkdirCmd *exec.Cmd
	if usePassword {
		mkdirCmd = exec.Command("sshpass", mkdirArgs...)
		mkdirCmd.Env = append(os.Environ(), sshBaseEnv...)
	} else {
		mkdirCmd = exec.Command("ssh", mkdirArgs...)
	}
	mkdirCmd.Stdout = os.Stderr
	mkdirCmd.Stderr = os.Stderr
	if runErr := mkdirCmd.Run(); runErr != nil {
		return nil, "", fmt.Errorf("SSH 创建远程目录失败 host=%s dir=%s err=%w", host, remoteDir, runErr)
	}
	catArgs := append([]string{}, sshBaseArgs...)
	catArgs = append(catArgs, sshOpts...)
	catArgs = append(catArgs, "-p", portStr, hostIdent, "cat > "+shellQuote(remotePath))
	var catCmd *exec.Cmd
	if usePassword {
		catCmd = exec.Command("sshpass", catArgs...)
		catCmd.Env = append(os.Environ(), sshBaseEnv...)
	} else {
		catCmd = exec.Command("ssh", catArgs...)
	}
	stdin, pipeErr := catCmd.StdinPipe()
	if pipeErr != nil {
		for _, t := range tempFiles {
			_ = os.Remove(t)
		}
		return nil, "", fmt.Errorf("创建 SSH sink 标准输入管道失败: %w", pipeErr)
	}
	catCmd.Stdout = os.Stderr
	catCmd.Stderr = os.Stderr
	if startErr := catCmd.Start(); startErr != nil {
		stdin.Close()
		for _, t := range tempFiles {
			_ = os.Remove(t)
		}
		return nil, "", fmt.Errorf("启动 SSH sink 写进程失败: %w", startErr)
	}
	sink := &sshRemoteCatSink{
		targetLabel: fmt.Sprintf("ssh://%s@%s:%d%s", user, host, port, remotePath),
		sshCmd:      catCmd,
		stdin:       stdin,
		remotePath:  remotePath,
		cleanupTemp: tempFiles,
	}
	return sink, relArtifactPath, nil
}

func openLocalServerLogSink(job *agentJob) (io.WriteCloser, string, uint64, error) {
	sink, rel, err := openLocalServerStreamSink(job, "run.log")
	if err != nil {
		return nil, "", 0, err
	}
	return sink, rel, job.StorageTargetID, nil
}

func transferLogToStorageTarget(job *agentJob, localLogPath string) (string, uint64, error) {
	return transferArtifactToStorageTarget(job, localLogPath, "run.log")
}

func cleanupLocalArtifactAfterTransfer(job *agentJob, artifactPath string) {
	path := strings.TrimSpace(artifactPath)
	if path == "" {
		return
	}
	baseDir := strings.TrimSpace(strings.TrimRight(job.OutputBaseDir, "/"))
	absPath, absErr := filepath.Abs(path)
	if absErr == nil {
		path = absPath
	}
	fi, statErr := os.Stat(path)
	if statErr != nil {
		fmt.Fprintf(os.Stderr, "cleanup skip: artifact path not found %s: %v\n", path, statErr)
		return
	}
	var removeDir string
	if fi.IsDir() {
		removeDir = path
	} else {
		removeDir = filepath.Dir(path)
	}
	removeDirAbs, absErr2 := filepath.Abs(removeDir)
	if absErr2 == nil {
		removeDir = removeDirAbs
	}
	underOut := false
	if baseDir != "" {
		if bdAbs, err := filepath.Abs(baseDir); err == nil {
			bd := strings.TrimRight(bdAbs, "/")
			rd := strings.TrimRight(removeDir, "/")
			if strings.HasPrefix(rd, bd+"/") || rd == bd {
				underOut = true
			}
		}
	}
	logDir := ""
	if lp := strings.TrimSpace(job.LogPath); lp != "" {
		if adir := filepath.Dir(lp); adir != "" && adir != "." {
			logDir = adir
		}
	}
	if !underOut && logDir != "" {
		if ldAbs, err := filepath.Abs(logDir); err == nil {
			ld := strings.TrimRight(ldAbs, "/")
			rd := strings.TrimRight(removeDir, "/")
			if strings.HasPrefix(rd, ld+"/") || rd == ld {
				underOut = true
			}
		}
	}
	if !underOut {
		fmt.Fprintf(os.Stderr, "cleanup refuse: dir %s is outside OutputBaseDir %s / LogPathDir %s (safety)\n", removeDir, baseDir, logDir)
		return
	}
	if rmErr := os.RemoveAll(removeDir); rmErr != nil {
		fmt.Fprintf(os.Stderr, "cleanup warn: RemoveAll %s failed: %v\n", removeDir, rmErr)
	} else {
		fmt.Fprintf(os.Stderr, "cleanup local artifact ok: removed %s\n", removeDir)
	}
}

func sanitizePathSegment(raw string, fallback string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = fallback
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "_", " ", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	s = replacer.Replace(s)
	s = strings.Trim(s, "-_.")
	if s == "" {
		s = fallback
	}
	return s
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// ---- 对象存储（S3 兼容）上传 ----
//
// 一份实现覆盖阿里云 OSS / 腾讯云 COS / 华为云 OBS / MinIO / AWS S3：
// 差异只在 endpoint / region / path-style 三个配置上。
// 大文件走并发分片上传（Multipart Upload），可调参数见 S3UploadTuningFromEnv。

const backupAgentUserAgent = "goinsight-backup-agent/0.1.0"

// objectStorageClient 依据 agent 收到的存储目标配置构造 S3 客户端。
func objectStorageClient(st *agentStorageTarget) (*services.S3Client, error) {
	if st == nil {
		return nil, fmt.Errorf("存储目标配置为空")
	}
	partSize, concurrency, maxRetries, storageClass := services.S3UploadTuningFromEnv(st.CustomEnv)
	return services.NewS3Client(services.S3ClientOptions{
		Endpoint:       strings.TrimSpace(st.S3Endpoint),
		Region:         strings.TrimSpace(st.S3Region),
		Bucket:         strings.TrimSpace(st.S3Bucket),
		AccessKey:      strings.TrimSpace(st.S3AccessKey),
		SecretKey:      st.S3SecretKey,
		ForcePathStyle: st.S3ForcePathStyle,
		PartSize:       partSize,
		Concurrency:    concurrency,
		MaxRetries:     maxRetries,
		StorageClass:   storageClass,
		UserAgent:      backupAgentUserAgent,
	})
}

func objectStorageRunDirSpec(job *agentJob) services.BackupRunDirSpec {
	prefix := ""
	if job.StorageTarget != nil {
		prefix = strings.Trim(job.StorageTarget.BasePath, "/")
	}
	return services.BackupRunDirSpec{
		ObjectPrefix:     prefix,
		SourceInstanceID: job.SourceInstanceID,
		InstanceBizID:    job.InstanceBizID,
		TaskID:           job.TaskID,
		RunID:            job.RunID,
		RunSeq:           job.RunSeq,
	}
}

// objectStorageUploadTimeout 依据文件大小给出上传超时（大文件按体积放大）。
func objectStorageUploadTimeout(size int64) time.Duration {
	if size <= 0 {
		return 30 * time.Minute
	}
	// 按 1MB/s 的保守吞吐估算并留 3 倍余量
	est := time.Duration(size/(1024*1024)) * time.Second * 3
	if est < 30*time.Minute {
		est = 30 * time.Minute
	}
	return est
}

// uploadLocalFileToObjectStorage 把本地产物以并发分片方式上传到对象存储。
func uploadLocalFileToObjectStorage(job *agentJob, localPath string, relPath string) error {
	client, err := objectStorageClient(job.StorageTarget)
	if err != nil {
		return fmt.Errorf("初始化对象存储客户端失败: %w", err)
	}
	info, statErr := os.Stat(localPath)
	if statErr != nil {
		return fmt.Errorf("本地备份产物不可读: %w", statErr)
	}
	if info.IsDir() {
		return fmt.Errorf("对象存储暂不支持直接上传目录（%s）：该驱动需要走零落盘直传，请确认存储目标与驱动组合是否受支持", localPath)
	}
	key := strings.TrimLeft(strings.TrimSpace(relPath), "/")
	if key == "" {
		key = filepath.Base(localPath)
	}
	fmt.Fprintf(os.Stderr, "[ObjectStorage] upload start bucket=%s key=%s size=%.2fMB part=%dMB conc=%d\n",
		client.Bucket(), key, float64(info.Size())/(1024*1024), client.PartSize()>>20, client.Concurrency())
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), objectStorageUploadTimeout(info.Size()))
	defer cancel()
	res, upErr := client.UploadFile(ctx, key, localPath)
	if upErr != nil {
		return fmt.Errorf("对象存储上传失败 bucket=%s key=%s: %w", client.Bucket(), key, upErr)
	}
	fmt.Fprintf(os.Stderr, "[ObjectStorage] upload done key=%s parts=%d size=%d bytes elapsed=%s etag=%s\n",
		res.Key, res.PartCount, res.Size, time.Since(start).Round(time.Millisecond), res.ETag)
	return nil
}

// openObjectStorageStreamSink 打开对象存储的零落盘直传 sink。
func openObjectStorageStreamSink(job *agentJob, artifactName string) (services.BackupStreamSink, string, error) {
	client, err := objectStorageClient(job.StorageTarget)
	if err != nil {
		return nil, "", fmt.Errorf("初始化对象存储客户端失败: %w", err)
	}
	spec := objectStorageRunDirSpec(job)
	relPath := spec.RelPath(artifactName)
	writer, err := client.NewMultipartWriter(context.Background(), relPath, 0, 0)
	if err != nil {
		return nil, "", fmt.Errorf("创建对象存储分片写入器失败: %w", err)
	}
	sink := services.NewS3StreamSink(writer)
	fmt.Fprintf(os.Stderr, "[ObjectStorage] stream sink opened target=%s part=%dMB conc=%d\n",
		sink.RemoteTarget(), client.PartSize()>>20, client.Concurrency())
	return sink, relPath, nil
}

func openObjectStorageLogSink(job *agentJob) (io.WriteCloser, string, uint64, error) {
	sink, rel, err := openObjectStorageStreamSink(job, "run.log")
	if err != nil {
		return nil, "", 0, err
	}
	return sink, rel, job.StorageTargetID, nil
}

// publishObjectStorageExtras 补传批次目录中的附属文件（metadata.json / checksums.sha256）
// 与本地运行日志，返回日志的 st: 引用（未上传则返回空串）。
// 必须在 cleanupLocalArtifactAfterTransfer 之前调用，否则本地目录已被删除。
func publishObjectStorageExtras(job *agentJob, result *services.BackupExecutionResult, logPath string) string {
	if job == nil || job.StorageTarget == nil {
		return ""
	}
	client, err := objectStorageClient(job.StorageTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ObjectStorage] init client for extras failed: %v\n", err)
		return ""
	}
	spec := objectStorageRunDirSpec(job)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if result != nil {
		if dir := strings.TrimSpace(result.LogDir); dir != "" {
			for _, name := range []string{"metadata.json", "checksums.sha256"} {
				p := filepath.Join(dir, name)
				fi, statErr := os.Stat(p)
				if statErr != nil || fi.IsDir() {
					continue
				}
				b, readErr := os.ReadFile(p)
				if readErr != nil {
					fmt.Fprintf(os.Stderr, "[ObjectStorage] read extra %s failed: %v\n", name, readErr)
					continue
				}
				key := spec.RelPath(name)
				if upErr := client.PutObject(ctx, key, b, "text/plain; charset=utf-8"); upErr != nil {
					fmt.Fprintf(os.Stderr, "[ObjectStorage] upload extra %s failed: %v\n", name, upErr)
				} else {
					fmt.Fprintf(os.Stderr, "[ObjectStorage] extra uploaded key=%s\n", key)
				}
			}
		}
	}

	lp := strings.TrimSpace(logPath)
	if lp == "" || strings.HasPrefix(lp, "st:") || strings.HasPrefix(lp, "s3://") {
		return ""
	}
	f, fErr := os.Open(lp)
	if fErr != nil {
		return ""
	}
	defer f.Close()

	logRel := spec.RelPath("run.log")
	writer, wErr := client.NewMultipartWriter(ctx, logRel, 0, 0)
	if wErr != nil {
		fmt.Fprintf(os.Stderr, "[ObjectStorage] open log writer failed: %v\n", wErr)
		return ""
	}
	if _, copyErr := io.Copy(writer, f); copyErr != nil {
		_ = writer.Abort()
		fmt.Fprintf(os.Stderr, "[ObjectStorage] upload run.log failed: %v\n", copyErr)
		return ""
	}
	if _, closeErr := writer.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "[ObjectStorage] finalize run.log failed: %v\n", closeErr)
		return ""
	}
	return fmt.Sprintf("st:%d:///%s", job.StorageTargetID, logRel)
}
