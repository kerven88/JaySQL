package services

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GetBackupSetsService struct {
	*backupForms.GetBackupSetsForm
	C *gin.Context
}

type backupSetListRow struct {
	ID               uint64                 `json:"id"`
	BackupSetsID     string                 `json:"backup_sets_id"`
	Name             string                 `json:"name"`
	DBType           string                 `json:"db_type"`
	DriverType       string                 `json:"driver_type"`
	SourceInstanceID uint64                 `json:"source_instance_id"`
	SourceDBName     string                 `json:"source_db_name"`
	ObjectMode       string                 `json:"object_mode"`
	ObjectSummary    string                 `json:"object_summary"`
	OutputDir        string                 `json:"output_dir"`
	SizeBytes        int64                  `json:"size_bytes"`
	Sha256           string                 `json:"sha256"`
	FileName         string                 `json:"file_name"`
	FilePath         string                 `json:"file_path"`
	CreatedAt        commonModels.LocalTime `json:"created_at"`
	UpdatedAt        commonModels.LocalTime `json:"updated_at"`
	StartAt          commonModels.LocalTime `json:"start_at"`
	EndAt            commonModels.LocalTime `json:"end_at"`
	ServerVersion    string                 `json:"server_version"`
	Tool             string                 `json:"tool"`
	ToolImage        string                 `json:"tool_image"`
	InstanceHost     string                 `json:"instance_host"`
	InstancePort     int                    `json:"instance_port"`
	InstanceRemark   string                 `json:"instance_remark"`
	BackupType       string                 `json:"backup_type"`
	TaskID           string                 `json:"task_id"`
	RunID            uint64                 `json:"run_id"`
	StorageTargetID  uint64                 `json:"storage_target_id"`
	CreatedBy        string                 `json:"created_by"`
	Status           string                 `json:"status"`
	RawStatus        string                 `json:"raw_status"`
	LogPath          string                 `json:"log_path"`
	ErrorMessage     string                 `json:"error_message"`
}

func (s *GetBackupSetsService) Run() (data interface{}, total int64, err error) {
	var list []backupSetListRow
	tx := global.App.DB.Table("insight_backup_sets bs").
		Select(`bs.id, bs.backup_sets_id, bs.name, bs.db_type, bs.driver_type, bs.source_instance_id, bs.source_db_name, bs.object_mode, bs.object_summary,
			bs.output_dir, bs.sha256, bs.file_name, bs.file_path, bs.created_at, bs.updated_at, bs.server_version, bs.tool, bs.tool_image, bs.backup_type,
			bs.task_id, bs.run_id, bs.storage_target_id, COALESCE(NULLIF(bs.created_by, ''), br.created_by) as created_by,
			i.hostname as instance_host, i.port as instance_port, i.remark as instance_remark,
			br.start_at, br.end_at, br.log_path, br.error_message, br.status as raw_status, br.size_bytes`).
		Joins("left join insight_backup_runs br on br.id = bs.run_id").
		Joins("left join insight_instances i on i.id = bs.source_instance_id").
		Order("bs.run_id desc, bs.id desc")
	if s.Search != "" {
		like := "%" + strings.TrimSpace(s.Search) + "%"
		tx = tx.Where(`bs.backup_sets_id like ? or bs.task_id like ? or bs.name like ? or bs.object_summary like ?
			or bs.output_dir like ? or i.hostname like ? or i.remark like ? or COALESCE(NULLIF(bs.created_by, ''), br.created_by) like ?`,
			like, like, like, like, like, like, like, like)
	}
	if s.DBType != "" {
		tx = tx.Where("bs.db_type = ?", s.DBType)
	}
	if s.InstanceID != 0 {
		tx = tx.Where("bs.source_instance_id = ?", s.InstanceID)
	}
	if s.RestorableOnly {
		tx = tx.Where("br.status = ? AND COALESCE(bs.file_path, '') <> ''", "SUCCESS")
	}
	total = pagination.Pager(&s.PaginationQ, tx, &list)
	for i := range list {
		list[i].Status = normalizeBackupSetStatus(list[i].RawStatus)
	}
	return &list, total, nil
}

func LoadBackupSet(id uint64) (*backupModels.InsightBackupSets, error) {
	var set backupModels.InsightBackupSets
	if err := global.App.DB.Where("id = ?", id).First(&set).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("备份集不存在")
		}
		return nil, err
	}
	return &set, nil
}

type GetBackupSetDetailService struct {
	SetID uint64
	C     *gin.Context
}

func (s *GetBackupSetDetailService) Run() (any, error) {
	var detail backupSetListRow
	err := global.App.DB.Table("insight_backup_sets bs").
		Select(`bs.id, bs.backup_sets_id, bs.name, bs.db_type, bs.driver_type, bs.source_instance_id, bs.source_db_name, bs.object_mode, bs.object_summary,
			bs.output_dir, bs.sha256, bs.file_name, bs.file_path, bs.created_at, bs.updated_at, bs.server_version, bs.tool, bs.tool_image, bs.backup_type,
			bs.task_id, bs.run_id, bs.storage_target_id, COALESCE(NULLIF(bs.created_by, ''), br.created_by) as created_by,
			i.hostname as instance_host, i.port as instance_port, i.remark as instance_remark,
			br.start_at, br.end_at, br.log_path, br.error_message, br.status as raw_status, br.size_bytes`).
		Joins("left join insight_backup_runs br on br.id = bs.run_id").
		Joins("left join insight_instances i on i.id = bs.source_instance_id").
		Where("bs.id = ?", s.SetID).
		Take(&detail).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("备份集不存在")
		}
		return nil, err
	}
	detail.Status = normalizeBackupSetStatus(detail.RawStatus)
	return gin.H{"set": detail}, nil
}

type StopBackupSetService struct {
	SetID uint64
	C     *gin.Context
}

func (s *StopBackupSetService) Run() (*StopBackupRunResult, error) {
	set, err := LoadBackupSet(s.SetID)
	if err != nil {
		return nil, err
	}
	if set.RunID == 0 {
		return nil, fmt.Errorf("当前备份集未关联执行记录，无法终止")
	}
	service := StopBackupRunService{RunID: set.RunID, C: s.C}
	return service.Run()
}

type DeleteBackupSetService struct {
	SetID uint64
	C     *gin.Context
}

func (s *DeleteBackupSetService) Run() error {
	return DeleteBackupSetCascade(s.SetID)
}

// DeleteBatchResult 批量删除备份集的逐项结果
type DeleteBatchResult struct {
	DeletedCount int                   `json:"deleted_count"`
	FailedCount  int                   `json:"failed_count"`
	Failures     []DeleteBatchFailure  `json:"failures"`
}

type DeleteBatchFailure struct {
	ID           uint64 `json:"id"`
	BackupSetsID string `json:"backup_sets_id"`
	Message      string `json:"message"`
}

// DeleteBackupSetsService 批量删除备份集（含级联清理）
type DeleteBackupSetsService struct {
	SetIDs []uint64
	C      *gin.Context
}

func (s *DeleteBackupSetsService) Run() (*DeleteBatchResult, error) {
	result := &DeleteBatchResult{Failures: []DeleteBatchFailure{}}
	for _, id := range s.SetIDs {
		set, loadErr := LoadBackupSet(id)
		if loadErr != nil {
			result.FailedCount++
			result.Failures = append(result.Failures, DeleteBatchFailure{ID: id, Message: loadErr.Error()})
			continue
		}
		if err := DeleteBackupSetCascade(id); err != nil {
			result.FailedCount++
			result.Failures = append(result.Failures, DeleteBatchFailure{
				ID:           id,
				BackupSetsID: set.BackupSetsID,
				Message:      err.Error(),
			})
			continue
		}
		result.DeletedCount++
	}
	if result.DeletedCount == 0 && result.FailedCount > 0 {
		return result, fmt.Errorf("批量删除失败，请查看失败明细")
	}
	return result, nil
}

// DeleteBackupSetCascade 删除备份集记录，并级联清理该备份集在存储目标与本地产生的
// 所有关联文件（备份产物、日志、参数/元数据配置文件等），确保无残留。
func DeleteBackupSetCascade(setID uint64) error {
	set, err := LoadBackupSet(setID)
	if err != nil {
		return err
	}
	if err := ensureBackupSetDeletable(set); err != nil {
		return err
	}
	// 文件清理失败不阻断记录删除：DB 记录必须能被移除，避免残留脏数据。
	cleanupBackupSetArtifacts(set)
	return global.App.DB.Delete(&backupModels.InsightBackupSets{}, set.ID).Error
}

func ensureBackupSetDeletable(set *backupModels.InsightBackupSets) error {
	if set == nil {
		return fmt.Errorf("备份集不存在")
	}
	if set.RunID == 0 {
		return nil
	}
	var run backupModels.InsightBackupRuns
	if err := global.App.DB.Select("id, status").Where("id = ?", set.RunID).Take(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if run.ID != 0 && isBackupRunStatusRunning(string(run.Status)) {
		return fmt.Errorf("运行中的备份暂不支持直接删除，请先终止任务")
	}
	return nil
}

// backupSetArtifactDir 描述一次备份在存储目标上产生的批次目录
type backupSetArtifactDir struct {
	StorageTargetID uint64
	BasePath        string
	RelDir          string // <instanceID>/<taskID>/<runID>
}

// buildBackupSetArtifactDir 推导备份集在存储目标上的批次目录，
// 与 Agent 落盘规则保持一致：<base_path>/<instance>/<task>/<run>
// 新数据由 BackupRunDirSpec 统一构造（含数据源业务 ID 与备份序号），
// 历史数据（run_seq=0）自动沿用旧的数字命名，保证老目录仍可定位与清理。
func buildBackupSetArtifactDir(set *backupModels.InsightBackupSets) *backupSetArtifactDir {
	if set == nil || set.StorageTargetID == 0 || set.RunID == 0 {
		return nil
	}
	target, err := loadStorageTargetByID(set.StorageTargetID)
	if err != nil || target == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(string(target.Type)), backupModels.StorageTargetTypeLocalServer) {
		return nil
	}
	base := strings.TrimSpace(target.BasePath)
	if base == "" {
		return nil
	}
	return &backupSetArtifactDir{
		StorageTargetID: set.StorageTargetID,
		BasePath:        base,
		RelDir:          buildBackupRunRelDirForSet(set),
	}
}

// buildBackupRunRelDirForSet 推导备份集对应的批次目录相对路径。
// 只有拿到 run 上的 run_seq（新规则写入）时才启用新命名，否则回落旧规则。
func buildBackupRunRelDirForSet(set *backupModels.InsightBackupSets) string {
	spec := BackupRunDirSpec{
		SourceInstanceID: set.SourceInstanceID,
		TaskID:           set.TaskID,
		RunID:            set.RunID,
	}
	if meta := loadBackupRunDirMeta(set.RunID); meta != nil && meta.RunSeq > 0 {
		spec.RunSeq = meta.RunSeq
		if meta.SourceInstanceID > 0 {
			spec.SourceInstanceID = meta.SourceInstanceID
		}
		spec.InstanceBizID = resolveInstanceBizID(spec.SourceInstanceID)
	}
	return spec.RelDir()
}

type backupRunDirMeta struct {
	SourceInstanceID uint64
	RunSeq           uint64
}

// loadBackupRunDirMeta 读取批次目录所需的 run 侧信息（源实例 ID 与备份序号）。
func loadBackupRunDirMeta(runID uint64) *backupRunDirMeta {
	if runID == 0 {
		return nil
	}
	var meta backupRunDirMeta
	row := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Select("source_instance_id, run_seq").
		Where("id = ?", runID).
		Row()
	if row == nil {
		return nil
	}
	if err := row.Scan(&meta.SourceInstanceID, &meta.RunSeq); err != nil {
		return nil
	}
	return &meta
}

// resolveInstanceBizID 取实例（数据源）的 15 位业务 ID，用于批次目录命名。
// 查不到时返回空串，调用方会自动回落到数字主键，保证路径构造不因缺数据而失败。
func resolveInstanceBizID(instanceID uint64) string {
	if instanceID == 0 {
		return ""
	}
	var bizID string
	if err := global.App.DB.Model(&commonModels.InsightInstances{}).
		Where("id = ?", instanceID).
		Pluck("instance_id", &bizID).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(bizID)
}

// cleanupBackupSetArtifacts 清理备份集在存储目标与本地产生的所有关联文件。
func cleanupBackupSetArtifacts(set *backupModels.InsightBackupSets) {
	if set == nil {
		return
	}
	recorded := collectBackupSetLocalPaths(set)
	// 1) 存储目标上的批次目录：一次删除即可覆盖备份产物、run.log、参数/元数据文件。
	//    优先依据记录中真实上报的 st: 产物/日志路径反推目录——批次目录命名规则
	//    可能随版本演进，只有库里的真实路径才能保证不漏删。
	removeArtifactDirsFromStoragePaths(recorded)
	// 2) 兜底：按当前命名规则重建批次目录（覆盖记录路径缺失的历史数据）
	if artifactDir := buildBackupSetArtifactDir(set); artifactDir != nil {
		removeBackupSetDirOnStorageTarget(artifactDir)
	}
	// 3) 兜底：清理记录中直接指向的本地文件（历史数据可能未关联存储目标）
	for _, candidate := range recorded {
		removeLocalFileIfSafe(candidate)
	}
}

// removeArtifactDirsFromStoragePaths 依据记录中真实上报的 st:<targetID>:///<rel> 路径，
// 反推其在存储目标上的批次目录并整体删除（去重，避免同一目录重复执行删除）。
func removeArtifactDirsFromStoragePaths(rawPaths []string) {
	seen := map[string]struct{}{}
	for _, raw := range rawPaths {
		p := strings.TrimSpace(raw)
		if !strings.HasPrefix(p, "st:") {
			continue
		}
		rel, targetID, err := ParseStorageArtifactPath(p)
		if err != nil || targetID == 0 {
			continue
		}
		dir := path.Dir(strings.Trim(strings.TrimSpace(rel), "/"))
		if dir == "" || dir == "." || dir == "/" {
			continue
		}
		key := fmt.Sprintf("%d|%s", targetID, dir)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		removeArtifactDirOnStorageTargetByID(targetID, dir)
	}
}

// collectBackupSetLocalPaths 汇总备份集/执行记录中可能指向本地文件的路径
func collectBackupSetLocalPaths(set *backupModels.InsightBackupSets) []string {
	out := make([]string, 0, 6)
	seen := map[string]struct{}{}
	appendPath := func(candidate string) {
		p := strings.TrimSpace(candidate)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	appendPath(set.FilePath)
	if set.RunID != 0 {
		var run struct {
			FilePath            string
			StorageArtifactPath string
			LogPath             string
		}
		if err := global.App.DB.Table("insight_backup_runs").
			Select("file_path, storage_artifact_path, log_path").
			Where("id = ?", set.RunID).
			Take(&run).Error; err == nil {
			appendPath(run.FilePath)
			appendPath(run.StorageArtifactPath)
			appendPath(run.LogPath)
		}
	}
	return out
}

// removeBackupSetDirOnStorageTarget 删除存储目标上的备份批次目录。
func removeBackupSetDirOnStorageTarget(artifactDir *backupSetArtifactDir) {
	if artifactDir == nil {
		return
	}
	removeArtifactDirOnStorageTargetByID(artifactDir.StorageTargetID, artifactDir.RelDir)
}

// removeArtifactDirOnStorageTargetByID 删除存储目标上指定的批次目录。
// 平台与存储目标同机时走本地删除；否则通过 SSH 在存储目标主机执行删除。
func removeArtifactDirOnStorageTargetByID(storageTargetID uint64, relDir string) {
	if storageTargetID == 0 {
		return
	}
	target, err := loadStorageTargetByID(storageTargetID)
	if err != nil || target == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(string(target.Type)), backupModels.StorageTargetTypeLocalServer) {
		return
	}
	base := strings.TrimSpace(target.BasePath)
	if base == "" {
		return
	}
	full := path.Join(base, relDir)
	if !isSafeBackupSetRemovalPath(full, relDir) {
		return
	}

	// 仅当存储目标就是本机时才走本地删除，避免在平台主机上误删同名路径
	if isLocalStorageTargetHost(target.Host) {
		_ = os.RemoveAll(full)
	}
	sshPassword, sshPrivateKey, _, _, decryptErr := decryptStorageTargetCredentials(target)
	if decryptErr != nil {
		return
	}
	cfg, cfgErr := buildStorageTargetRemoteConfig(&BackupExecutionStorageTarget{
		ID:            target.ID,
		Type:          string(target.Type),
		Host:          target.Host,
		Port:          target.Port,
		BasePath:      target.BasePath,
		SSHUser:       target.SSHUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	})
	if cfgErr != nil {
		return
	}
	runner, runnerErr := newSSHRunner(cfg)
	if runnerErr != nil {
		return
	}
	defer runner.Close()
	_, _ = runner.Run(fmt.Sprintf("rm -rf %s", shellQuote(full)))
}

// isSafeBackupSetRemovalPath 删除护栏：必须是绝对路径，且末段为纯数字 runID，
// 保证不会误删 base_path 或更高层级目录。
func isSafeBackupSetRemovalPath(full string, relDir string) bool {
	relRaw := strings.TrimSpace(relDir)
	// 纵深防御：批次相对目录不应包含上跳段
	if relRaw == "" || strings.Contains(relRaw, "..") {
		return false
	}
	clean := path.Clean(strings.TrimSpace(full))
	if clean == "" || clean == "/" || clean == "." || !strings.HasPrefix(clean, "/") {
		return false
	}
	relClean := path.Clean("/" + relRaw)
	if relClean == "/" || relClean == "." {
		return false
	}
	if !strings.HasSuffix(clean, relClean) {
		return false
	}
	relParts := strings.Split(strings.Trim(relClean, "/"), "/")
	if len(relParts) < 3 {
		return false
	}
	if !isSafeBackupRunDirSegment(relParts[len(relParts)-1]) {
		return false
	}
	return true
}

// isSafeBackupRunDirSegment 校验批次目录末段，兼容两种批次目录命名：
//   - 历史规则：纯 runID，如 399
//   - 新规则：序号-runID，如 000001-399
//
// 末段只允许数字与单个连字符且每段均为数字，确保绝不会指向 base_path 等上层目录。
func isSafeBackupRunDirSegment(segment string) bool {
	s := strings.TrimSpace(segment)
	if s == "" || strings.ContainsAny(s, "/\\.") {
		return false
	}
	parts := strings.Split(s, "-")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

var (
	localHostIdentifiersOnce sync.Once
	localHostIdentifiers     map[string]struct{}
)

// isLocalStorageTargetHost 判断存储目标主机是否就是本机（含 hostname / 回环 / 本机网卡 IP）。
func isLocalStorageTargetHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	localHostIdentifiersOnce.Do(func() {
		ids := map[string]struct{}{
			"127.0.0.1": {},
			"localhost": {},
			"::1":       {},
		}
		if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" {
			ids[strings.ToLower(strings.TrimSpace(hostname))] = struct{}{}
		}
		if ifaceAddrs, err := net.InterfaceAddrs(); err == nil {
			for _, addr := range ifaceAddrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ids[ipNet.IP.String()] = struct{}{}
			}
		}
		localHostIdentifiers = ids
	})
	_, ok := localHostIdentifiers[h]
	return ok
}

// removeLocalFileIfSafe 删除记录中指向的本地文件，支持 st:<id>:///rel 协议解析。
func removeLocalFileIfSafe(candidate string) {
	p := strings.TrimSpace(candidate)
	if p == "" {
		return
	}
	real := p
	if strings.HasPrefix(p, "st:") {
		resolved, ok := resolveStorageRefToLocal(p, 0)
		if !ok {
			return
		}
		real = resolved
	}
	real = strings.TrimSpace(real)
	if !strings.HasPrefix(real, "/") {
		return
	}
	_ = os.Remove(real)
}

func normalizeBackupSetStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING", "RUNNING", "STOP_REQUESTED":
		return "RUNNING"
	case "SUCCESS":
		return "COMPLETED"
	case "FAILED", "STOPPED":
		return "FAILED"
	default:
		return "FAILED"
	}
}

func buildBackupSetName(taskName string, createdAt time.Time) string {
	base := strings.TrimSpace(taskName)
	if base == "" {
		base = "backup_set"
	}
	ts := createdAt
	if ts.IsZero() {
		ts = time.Now()
	}
	name := base + "_" + ts.Format("20060102150405")
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

func ensureBackupSetSnapshotTx(tx *gorm.DB, task *backupModels.InsightBackupTasks, run *backupModels.InsightBackupRuns) error {
	if tx == nil {
		tx = global.App.DB
	}
	if task == nil || run == nil || run.ID == 0 {
		return fmt.Errorf("备份执行快照参数不完整")
	}
	createdAt := time.Time(run.CreatedAt)
	setName := buildBackupSetName(task.Name, createdAt)
	updates := map[string]any{
		"task_id":            task.TaskID,
		"db_type":            task.DBType,
		"source_instance_id": task.SourceInstanceID,
		"executor_node_id":   run.ExecutorNodeID,
		"repository_node_id": run.RepositoryNodeID,
		"source_db_name":     task.SourceDBName,
		"backup_type":        run.BackupType,
		"object_mode":        run.ObjectMode,
		"object_spec":        run.ObjectSpec,
		"object_summary":     run.ObjectSummary,
		"output_dir":         run.OutputDir,
		"driver_type":        run.DriverType,
		"driver_config":      run.DriverConfig,
		"storage_backend":    run.StorageBackend,
		"storage_config":     run.StorageConfig,
		"storage_target_id":  run.StorageTargetID,
		"retention_days":     run.RetentionDays,
		"expire_at":          calculateExpireAt(run.RetentionDays),
		"name":               setName,
		"created_by":         run.CreatedBy,
	}
	var set backupModels.InsightBackupSets
	err := tx.Where("run_id = ?", run.ID).Take(&set).Error
	if err == nil {
		if strings.TrimSpace(set.BackupSetsID) == "" {
			updates["backup_sets_id"] = utils.GenerateBackupSetID()
		}
		return tx.Model(&backupModels.InsightBackupSets{}).Where("id = ?", set.ID).Updates(updates).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	newSet := backupModels.InsightBackupSets{
		Model:            &commonModels.Model{},
		BackupSetsID:     utils.GenerateBackupSetID(),
		RunID:            run.ID,
		TaskID:           task.TaskID,
		DBType:           task.DBType,
		SourceInstanceID: task.SourceInstanceID,
		ExecutorNodeID:   run.ExecutorNodeID,
		RepositoryNodeID: run.RepositoryNodeID,
		SourceDBName:     task.SourceDBName,
		BackupType:       run.BackupType,
		ObjectMode:       run.ObjectMode,
		ObjectSpec:       run.ObjectSpec,
		ObjectSummary:    run.ObjectSummary,
		OutputDir:        run.OutputDir,
		DriverType:       run.DriverType,
		DriverConfig:     run.DriverConfig,
		StorageBackend:   run.StorageBackend,
		StorageConfig:    run.StorageConfig,
		StorageTargetID:  run.StorageTargetID,
		RetentionDays:    run.RetentionDays,
		ExpireAt:         calculateExpireAt(run.RetentionDays),
		Name:             setName,
		CreatedBy:        run.CreatedBy,
	}
	return tx.Create(&newSet).Error
}

// boolToTinyInt 把布尔转成 MySQL tinyint 字面量。
// gorm 的 Updates(map[string]any) 对 bool 会正确写 1/0，这里显式转换只为让落库值一目了然，
// 同时避免将来换驱动时出现类型差异。
func boolToTinyInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func persistBackupSetSuccess(task *backupModels.InsightBackupTasks, run *backupModels.InsightBackupRuns, result *BackupExecutionResult) (*backupModels.InsightBackupSets, error) {
	if err := ensureBackupSetSnapshotTx(global.App.DB, task, run); err != nil {
		return nil, err
	}
	var set backupModels.InsightBackupSets
	if err := global.App.DB.Where("run_id = ?", run.ID).Take(&set).Error; err != nil {
		return nil, err
	}
	updates := map[string]any{
		"task_id":               task.TaskID,
		"db_type":               task.DBType,
		"source_instance_id":    task.SourceInstanceID,
		"executor_node_id":      run.ExecutorNodeID,
		"repository_node_id":    run.RepositoryNodeID,
		"source_db_name":        result.SourceDBName,
		"backup_type":           run.BackupType,
		"object_mode":           result.ObjectMode,
		"object_spec":           result.ObjectSpec,
		"object_summary":        result.ObjectSummary,
		"output_dir":            result.OutputDir,
		"driver_type":           run.DriverType,
		"driver_config":         run.DriverConfig,
		"storage_backend":       run.StorageBackend,
		"storage_config":        run.StorageConfig,
		"storage_target_id":     run.StorageTargetID,
		"storage_artifact_path": result.StorageArtifactPath,
		"retention_days":        run.RetentionDays,
		"expire_at":             calculateExpireAt(run.RetentionDays),
		"name":                  buildBackupSetName(task.Name, time.Now()),
		"file_name":             result.ArtifactName,
		"file_path":             result.ArtifactPath,
		"size_bytes":            result.SizeBytes,
		"sha256":                result.Sha256,
		"server_version":        result.ServerVersion,
		"tool":                  result.Tool,
		"tool_image":            result.ToolImage,
		"created_by":            run.CreatedBy,
		// 备份集验证状态：由备份完成时回读附属文件校验得出，落库后恢复预检直接读取。
		// verified_at 有值即代表「本备份集执行过验证流程」，恢复侧不再现场 find 文件；
		// 历史备份集该列为 NULL，恢复侧回退旧逻辑，行为与升级前一致。
		"metadata_verified": boolToTinyInt(result.MetadataVerified),
		"checksum_verified": boolToTinyInt(result.ChecksumVerified),
		"verified_at":       time.Now(),
	}
	if strings.TrimSpace(set.BackupSetsID) == "" {
		updates["backup_sets_id"] = utils.GenerateBackupSetID()
	}
	if err := global.App.DB.Model(&backupModels.InsightBackupSets{}).Where("id = ?", set.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := global.App.DB.Where("id = ?", set.ID).Take(&set).Error; err != nil {
		return nil, err
	}
	return &set, nil
}

func ValidateBackupSetFile(set *backupModels.InsightBackupSets) (string, error) {
	if set == nil {
		return "", fmt.Errorf("备份文件不存在")
	}
	candidate := strings.TrimSpace(set.FilePath)
	if candidate != "" {
		if real, ok := resolveStorageRefToLocal(candidate, set.StorageTargetID); ok {
			return real, nil
		}
		// 对象存储引用（OSS/S3/COS/OBS）在服务端本地没有文件，返回引用本身；
		// 执行阶段会负责把对象下载到本地/目标机。
		if isObjectStorageRef(candidate, set.StorageTargetID) {
			return candidate, nil
		}
		if !strings.HasPrefix(candidate, "st:") {
			absFile, err := filepath.Abs(candidate)
			if err != nil {
				return "", err
			}
			if _, err := os.Stat(absFile); err == nil {
				return absFile, nil
			}
		}
	}
	if set.RunID == 0 {
		return "", fmt.Errorf("备份文件不可用：未解析到可访问的本地文件")
	}
	var run struct {
		FilePath            string
		StorageArtifactPath string
		StorageTargetID     uint64
	}
	if err := global.App.DB.Table("insight_backup_runs").
		Select("file_path, storage_artifact_path, storage_target_id").
		Where("id = ?", set.RunID).
		Take(&run).Error; err == nil {
		candidates := []string{strings.TrimSpace(run.FilePath), strings.TrimSpace(run.StorageArtifactPath)}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if real, ok := resolveStorageRefToLocal(candidate, run.StorageTargetID); ok {
				return real, nil
			}
			if isObjectStorageRef(candidate, run.StorageTargetID) {
				return candidate, nil
			}
			if strings.HasPrefix(candidate, "st:") {
				continue
			}
			absFile, err := filepath.Abs(candidate)
			if err != nil {
				return "", err
			}
			if _, err := os.Stat(absFile); err == nil {
				return absFile, nil
			}
		}
	}
	return "", fmt.Errorf("备份文件不可用：未解析到可访问的本地文件")
}
