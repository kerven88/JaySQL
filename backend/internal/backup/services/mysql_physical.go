package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dasDao "github.com/simajiong/jaysql/internal/das/dao"
	"gorm.io/datatypes"
)

type mysqlPhysicalDriverConfig struct {
	BackupType                string
	ProcessMax                int
	DefaultsFile              string
	SocketPath                string
	OSUser                    string
	ToolPath                  string
	StrategyMode              string
	FullBackupDays            []string
	OtherDaysType             string
	FallbackToFullWhenMissing bool
}

type mysqlPhysicalBackupManifest struct {
	RunID               uint64   `json:"run_id"`
	TaskID              string   `json:"task_id"`
	SourceInstanceID    uint64   `json:"source_instance_id"`
	DBType              string   `json:"db_type"`
	DriverType          string   `json:"driver_type"`
	ServerVersion       string   `json:"server_version"`
	Tool                string   `json:"tool"`
	ToolPath            string   `json:"tool_path"`
	ToolSource          string   `json:"tool_source"`
	BackupType          string   `json:"backup_type"`
	ProcessMax          int      `json:"process_max"`
	DefaultsFile        string   `json:"defaults_file,omitempty"`
	SocketPath          string   `json:"socket_path,omitempty"`
	FromLSN             string   `json:"from_lsn,omitempty"`
	ToLSN               string   `json:"to_lsn,omitempty"`
	LastLSN             string   `json:"last_lsn,omitempty"`
	BaseManifestPath    string   `json:"base_manifest_path,omitempty"`
	BaseStorageArtifact string   `json:"base_storage_artifact_path,omitempty"`
	StorageArtifactPath string   `json:"storage_artifact_path"`
	ManifestPath        string   `json:"manifest_path"`
	ArtifactName        string   `json:"artifact_name"`
	ArtifactSizeBytes   int64    `json:"artifact_size_bytes"`
	ArtifactSHA256      string   `json:"artifact_sha256"`
	CreatedAt           string   `json:"created_at"`
	Warnings            []string `json:"warnings,omitempty"`
}

type mysqlPhysicalBackupBase struct {
	ManifestPath        string
	StorageArtifactPath string
	FromLSN             string
	ToLSN               string
	BackupType          string
	// CreatedAt 基线 manifest 的写入时间（RFC3339）。
	// 定时触发判定基线是否过期时使用：连同备份集被保留策略清理后，这里的时间会明显偏旧。
	CreatedAt string
	// ScannedCount / InvalidCount 是扫描诊断计数，用于区分「基线缺失」与「基线损坏」：
	// ScannedCount=0 → 目录里根本没有 manifest（首次执行或被清理）；
	// ScannedCount>0 且 InvalidCount=ScannedCount → manifest 全损坏（to_lsn 缺失或 JSON 不可解析）。
	ScannedCount int
	InvalidCount int
}

type countingHashWriteCloser struct {
	target io.WriteCloser
	hasher hash.Hash
	size   int64
}

func newCountingHashWriteCloser(target io.WriteCloser) *countingHashWriteCloser {
	return &countingHashWriteCloser{
		target: target,
		hasher: sha256.New(),
	}
}

func (w *countingHashWriteCloser) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	if n > 0 {
		_, _ = w.hasher.Write(p[:n])
		w.size += int64(n)
	}
	return n, err
}

func (w *countingHashWriteCloser) Close() error {
	if w == nil || w.target == nil {
		return nil
	}
	return w.target.Close()
}

func (w *countingHashWriteCloser) SizeBytes() int64 {
	if w == nil {
		return 0
	}
	return w.size
}

func (w *countingHashWriteCloser) SHA256() string {
	if w == nil || w.hasher == nil {
		return ""
	}
	return hex.EncodeToString(w.hasher.Sum(nil))
}

func isMySQLPhysicalDriverType(driverType string) bool {
	return strings.EqualFold(strings.TrimSpace(driverType), "XTRABACKUP")
}

func normalizeMySQLPhysicalBackupType(value string) string {
	text := strings.ToUpper(strings.TrimSpace(value))
	if text == "" {
		return "FULL"
	}
	switch text {
	case "FULL", "INCR":
		return text
	default:
		return ""
	}
}

func normalizeMySQLPhysicalDriverConfig(raw map[string]any, backupType string) (datatypes.JSON, error) {
	cfg := map[string]any{}
	normalizedBackupType := normalizeMySQLPhysicalBackupType(backupType)
	if normalizedBackupType == "" {
		return nil, fmt.Errorf("MySQL 物理备份类型非法，仅支持 FULL/INCR")
	}
	processMax := anyToInt(raw["process_max"], 2)
	if processMax < 1 || processMax > 16 {
		return nil, fmt.Errorf("MySQL 物理备份并行数 process_max 必须在 1-16 之间")
	}
	cfg["backup_type"] = normalizedBackupType
	cfg["process_max"] = processMax
	if text := readStringFromMap(raw, "defaults_file"); text != "" {
		cfg["defaults_file"] = text
	}
	if text := readStringFromMap(raw, "socket_path"); text != "" {
		cfg["socket_path"] = text
	}
	osUser := firstNonEmpty(
		readStringFromMap(raw, "os_user"),
		readStringFromMap(raw, "process_user"),
		readStringFromMap(raw, "db_os_user"),
	)
	if osUser != "" {
		if !logicalOSUserRe.MatchString(osUser) {
			return nil, fmt.Errorf("操作系统用户格式非法，仅支持字母、数字、点、下划线和中划线")
		}
		cfg["os_user"] = osUser
	}
	toolPath := firstNonEmpty(
		readStringFromMap(raw, "tool_path"),
		readStringFromMap(raw, "dump_path"),
		readStringFromMap(raw, "xtrabackup_path"),
	)
	if toolPath != "" {
		cfg["tool_path"] = toolPath
	}
	strategyMode := strings.ToUpper(readStringFromMap(raw, "strategy_mode"))
	if strategyMode == "" {
		strategyMode = "MIXED"
	}
	if strategyMode != "SINGLE" && strategyMode != "MIXED" {
		return nil, fmt.Errorf("MySQL 物理备份策略模式仅支持 SINGLE/MIXED")
	}
	cfg["strategy_mode"] = strategyMode
	fullDays := normalizeStringSlice(raw["full_backup_days"])
	if len(fullDays) > 0 {
		cfg["full_backup_days"] = fullDays
	}
	cfg["other_days_type"] = "INCR"
	cfg["fallback_to_full_when_missing"] = anyToBool(raw["fallback_to_full_when_missing"], true)
	// 单次执行最长运行时间（小时）：任务级可选，缺省不写（回落环境变量 / 平台默认 12 小时）。
	// 该上限作用于 xtrabackup / mariabackup 命令的兜底看门狗。
	if hours := anyToInt(raw[DriverConfigKeyMaxRuntimeHours], 0); hours > 0 {
		if hours < maxRuntimeHoursMin || hours > maxRuntimeHoursMax {
			return nil, fmt.Errorf("单次执行最长运行时间必须在 %d-%d 小时之间", maxRuntimeHoursMin, maxRuntimeHoursMax)
		}
		cfg[DriverConfigKeyMaxRuntimeHours] = hours
	}
	return marshalJSONMapOrNil(cfg)
}

func loadMySQLPhysicalDriverConfig(raw datatypes.JSON) (mysqlPhysicalDriverConfig, error) {
	cfg := mysqlPhysicalDriverConfig{
		BackupType:                "FULL",
		ProcessMax:                2,
		StrategyMode:              "MIXED",
		OtherDaysType:             "INCR",
		FallbackToFullWhenMissing: true,
	}
	if len(raw) == 0 {
		return cfg, nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return cfg, err
	}
	if text := normalizeMySQLPhysicalBackupType(readStringFromAny(data["backup_type"])); text != "" {
		cfg.BackupType = text
	}
	if value := anyToInt(data["process_max"], cfg.ProcessMax); value >= 1 && value <= 16 {
		cfg.ProcessMax = value
	}
	cfg.DefaultsFile = readStringFromAny(data["defaults_file"])
	cfg.SocketPath = readStringFromAny(data["socket_path"])
	cfg.OSUser = firstNonEmpty(
		readStringFromAny(data["os_user"]),
		readStringFromAny(data["process_user"]),
		readStringFromAny(data["db_os_user"]),
	)
	cfg.ToolPath = firstNonEmpty(
		readStringFromAny(data["tool_path"]),
		readStringFromAny(data["dump_path"]),
		readStringFromAny(data["xtrabackup_path"]),
	)
	cfg.StrategyMode = strings.ToUpper(readStringFromAny(data["strategy_mode"]))
	if cfg.StrategyMode == "" {
		cfg.StrategyMode = "MIXED"
	}
	if cfg.StrategyMode != "SINGLE" && cfg.StrategyMode != "MIXED" {
		return cfg, fmt.Errorf("MySQL 物理备份策略模式仅支持 SINGLE/MIXED")
	}
	if cfg.OSUser != "" && !logicalOSUserRe.MatchString(cfg.OSUser) {
		return cfg, fmt.Errorf("操作系统用户格式非法，仅支持字母、数字、点、下划线和中划线")
	}
	cfg.FullBackupDays = normalizeStringSlice(data["full_backup_days"])
	cfg.OtherDaysType = "INCR"
	cfg.FallbackToFullWhenMissing = anyToBool(data["fallback_to_full_when_missing"], true)
	cfg.ToolPath = strings.TrimSpace(cfg.ToolPath)
	return cfg, nil
}

func normalizeStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		text := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", item)))
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		out = append(out, text)
	}
	sort.Strings(out)
	return out
}

func resolveMySQLPhysicalRepoDir(baseDir string, sourceInstanceID uint64, taskID string) string {
	root := resolveBackupStorageBaseDir(baseDir, "XTRABACKUP", "MySQL")
	return filepath.Join(root, fmt.Sprintf("instance-%d", sourceInstanceID), sanitizePathSegment(taskID, "task"))
}

func buildMySQLPhysicalStorageArtifactPath(storageTargetID uint64, spec BackupRunDirSpec, fileName string) string {
	return fmt.Sprintf("st:%d:///%s", storageTargetID, spec.RelPath(fileName))
}

func buildMySQLPhysicalManifestPath(storageTargetID uint64, spec BackupRunDirSpec) string {
	return buildMySQLPhysicalStorageArtifactPath(storageTargetID, spec, "backup.meta.json")
}

func resolveMySQLPhysicalToolLocally(ctx context.Context, serverVersion string, cfg mysqlPhysicalDriverConfig) (*localToolSpec, error) {
	_ = ctx
	errs := make([]string, 0, 4)
	if strings.TrimSpace(cfg.ToolPath) != "" {
		spec, err := resolveConfiguredLogicalToolSpec("MySQL", serverVersion, logicalBackupDriverConfig{ToolPath: cfg.ToolPath})
		if err != nil {
			errs = append(errs, err.Error())
		} else if spec != nil {
			if compatErr := validateMySQLPhysicalToolCompatibility(spec, serverVersion); compatErr != nil {
				errs = append(errs, compatErr.Error())
			} else {
				return spec, nil
			}
		}
	}
	spec, err := resolveMySQLClientTool("xtrabackup", serverVersion)
	if err == nil && spec != nil {
		if compatErr := validateMySQLPhysicalToolCompatibility(spec, serverVersion); compatErr != nil {
			errs = append(errs, compatErr.Error())
		} else {
			return spec, nil
		}
	} else if err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("解析 xtrabackup 工具失败: %s", strings.Join(uniqueStrings(errs), "; "))
	}
	return nil, fmt.Errorf("未找到可用的 xtrabackup 工具")
}

func probeMySQLPhysicalVariables(host string, port int, user string, password string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	db := dasDao.DB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: "",
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	_, rows, err := db.Query(`select
		@@global.datadir as datadir,
		@@global.log_bin as log_bin,
		@@global.log_bin_basename as log_bin_basename,
		@@global.version as version,
		@@global.version_comment as version_comment,
		@@global.server_uuid as server_uuid`)
	if err != nil {
		return nil, err
	}
	if rows == nil || len(*rows) == 0 {
		return nil, fmt.Errorf("未读取到 MySQL 物理备份关键参数")
	}
	row := (*rows)[0]
	datadir := mysqlPhysicalStringValue(row["datadir"])
	innodbLogGroupHomeDir := ""
	innodbUndoDirectory := ""
	innodbDataHomeDir := ""
	innodbDirectories := ""
	if _, redoRows, redoErr := db.Query("show global variables like 'innodb_log_group_home_dir'"); redoErr == nil && redoRows != nil {
		for _, item := range *redoRows {
			innodbLogGroupHomeDir = mysqlPhysicalStringValue(item["Value"])
			if innodbLogGroupHomeDir == "" {
				innodbLogGroupHomeDir = mysqlPhysicalStringValue(item["VARIABLE_VALUE"])
			}
			if innodbLogGroupHomeDir != "" {
				break
			}
		}
	}
	if _, undoRows, undoErr := db.Query("show global variables like 'innodb_undo_directory'"); undoErr == nil && undoRows != nil {
		for _, item := range *undoRows {
			innodbUndoDirectory = mysqlPhysicalStringValue(item["Value"])
			if innodbUndoDirectory == "" {
				innodbUndoDirectory = mysqlPhysicalStringValue(item["VARIABLE_VALUE"])
			}
			if innodbUndoDirectory != "" {
				break
			}
		}
	}
	if _, dataHomeRows, dataHomeErr := db.Query("show global variables like 'innodb_data_home_dir'"); dataHomeErr == nil && dataHomeRows != nil {
		for _, item := range *dataHomeRows {
			innodbDataHomeDir = mysqlPhysicalStringValue(item["Value"])
			if innodbDataHomeDir == "" {
				innodbDataHomeDir = mysqlPhysicalStringValue(item["VARIABLE_VALUE"])
			}
			if innodbDataHomeDir != "" {
				break
			}
		}
	}
	if _, dirsRows, dirsErr := db.Query("show global variables like 'innodb_directories'"); dirsErr == nil && dirsRows != nil {
		for _, item := range *dirsRows {
			innodbDirectories = mysqlPhysicalStringValue(item["Value"])
			if innodbDirectories == "" {
				innodbDirectories = mysqlPhysicalStringValue(item["VARIABLE_VALUE"])
			}
			if innodbDirectories != "" {
				break
			}
		}
	}
	normalizedInnoDBDirectories := normalizeMySQLPhysicalExtraDataDirs(datadir, innodbDirectories)
	return map[string]string{
		"datadir":                   datadir,
		"log_bin":                   mysqlPhysicalStringValue(row["log_bin"]),
		"log_bin_basename":          mysqlPhysicalStringValue(row["log_bin_basename"]),
		"version":                   mysqlPhysicalStringValue(row["version"]),
		"version_comment":           mysqlPhysicalStringValue(row["version_comment"]),
		"server_uuid":               mysqlPhysicalStringValue(row["server_uuid"]),
		"innodb_log_group_home_dir": innodbLogGroupHomeDir,
		"innodb_undo_directory":     innodbUndoDirectory,
		"innodb_data_home_dir":      innodbDataHomeDir,
		"innodb_directories":        innodbDirectories,
		"redo_base_dir":             resolveMySQLPhysicalRedoBaseDir(datadir, innodbLogGroupHomeDir),
		"undo_base_dir":             resolveMySQLPhysicalUndoBaseDir(datadir, innodbUndoDirectory),
		"data_home_base_dir":        resolveMySQLPhysicalDataHomeBaseDir(datadir, innodbDataHomeDir),
		"innodb_directories_list":   strings.Join(normalizedInnoDBDirectories, "\n"),
	}, nil
}

func mysqlPhysicalStringValue(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if strings.EqualFold(text, "<nil>") || strings.EqualFold(text, "null") {
		return ""
	}
	return text
}

func resolveMySQLPhysicalRedoBaseDir(datadir string, innodbLogGroupHomeDir string) string {
	return resolveMySQLPhysicalPathBaseDir(datadir, innodbLogGroupHomeDir)
}

func resolveMySQLPhysicalUndoBaseDir(datadir string, innodbUndoDirectory string) string {
	return resolveMySQLPhysicalPathBaseDir(datadir, innodbUndoDirectory)
}

func resolveMySQLPhysicalDataHomeBaseDir(datadir string, innodbDataHomeDir string) string {
	return resolveMySQLPhysicalPathBaseDir(datadir, innodbDataHomeDir)
}

func resolveMySQLPhysicalPathBaseDir(datadir string, configuredPath string) string {
	baseDir := strings.TrimSpace(datadir)
	configured := strings.TrimSpace(configuredPath)
	if configured == "" || configured == "." {
		return baseDir
	}
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured)
	}
	if baseDir == "" {
		return filepath.Clean(configured)
	}
	return filepath.Clean(filepath.Join(baseDir, configured))
}

func normalizeMySQLPhysicalExtraDataDirs(datadir string, raw string) []string {
	items := []string{}
	for _, part := range splitMySQLPhysicalConfiguredDirs(raw) {
		resolved := resolveMySQLPhysicalPathBaseDir(datadir, part)
		if resolved == "" {
			continue
		}
		items = append(items, resolved)
	}
	return uniqueStrings(items)
}

func splitMySQLPhysicalConfiguredDirs(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer("\r", "\n", ";", "\n", ",", "\n")
	parts := strings.Split(replacer.Replace(text), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return uniqueStrings(out)
}

func buildStorageTargetRemoteConfig(target *BackupExecutionStorageTarget) (*backupNodeRemoteConfig, error) {
	if target == nil {
		return nil, fmt.Errorf("存储目标配置为空")
	}
	if !strings.EqualFold(strings.TrimSpace(target.Type), "LOCAL_SERVER") {
		return nil, fmt.Errorf("MySQL 物理备份当前仅支持 LOCAL_SERVER 类型存储目标")
	}
	port := target.Port
	if port <= 0 {
		port = 22
	}
	if strings.TrimSpace(target.Host) == "" || strings.TrimSpace(target.SSHUser) == "" {
		return nil, fmt.Errorf("LOCAL_SERVER 存储目标缺少 host 或 ssh_user")
	}
	if strings.TrimSpace(target.SSHPassword) == "" && strings.TrimSpace(target.SSHPrivateKey) == "" {
		return nil, fmt.Errorf("LOCAL_SERVER 存储目标缺少 SSH 凭据")
	}
	return &backupNodeRemoteConfig{
		Host:          strings.TrimSpace(target.Host),
		Port:          port,
		SSHUser:       strings.TrimSpace(target.SSHUser),
		SSHPassword:   strings.TrimSpace(target.SSHPassword),
		SSHPrivateKey: strings.TrimSpace(target.SSHPrivateKey),
	}, nil
}

// validateStorageTargetForPhysicalBase 校验存储目标是否具备"读物理基线"的最低配置。
// 按类型走各自的校验：对象存储校验 bucket/endpoint/AK/SK，LOCAL_SERVER 校验 host/SSH 凭据。
func validateStorageTargetForPhysicalBase(target *BackupExecutionStorageTarget) error {
	if target == nil {
		return fmt.Errorf("存储目标配置为空")
	}
	if IsObjectStorageType(storageTargetTypeOf(target)) {
		_, err := buildStorageTargetObjectClient(target)
		return err
	}
	_, err := buildStorageTargetRemoteConfig(target)
	return err
}

// findLatestMySQLPhysicalBase 查找该任务最新的有效物理基线。
//
// 按存储目标类型分派读取链路，两条链路返回结构与语义完全一致（见各自实现注释）：
//   - 对象存储（OSS/COS/S3/OBS）：按任务目录前缀 LIST 枚举 + 逐个 GET 下载解析；
//   - LOCAL_SERVER：SSH 登录仓库机 find + cat。
func findLatestMySQLPhysicalBase(target *BackupExecutionStorageTarget, spec BackupRunDirSpec) (*mysqlPhysicalBackupBase, error) {
	if IsObjectStorageType(storageTargetTypeOf(target)) {
		return findLatestMySQLPhysicalBaseViaObjectStorage(target, spec)
	}
	cfg, err := buildStorageTargetRemoteConfig(target)
	if err != nil {
		return nil, err
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return nil, err
	}
	defer runner.Close()

	// 兼容期同时扫描新老两种命名下的任务根目录：
	// 升级前落盘的历史批次仍在旧目录（数字实例 ID），漏扫会导致增量备份丢失基线而降级为全量。
	roots := spec.TaskRootCandidates(strings.TrimSpace(target.BasePath))
	quotedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		quotedRoots = append(quotedRoots, shellQuote(root))
	}
	listOutput, runErr := runner.Run(strings.Join([]string{
		"set -e",
		"for ROOT in " + strings.Join(quotedRoots, " ") + "; do",
		`  [ -d "$ROOT" ] || continue`,
		`  find "$ROOT" -type f -name 'backup.meta.json'`,
		"done | LC_ALL=C sort -V",
	}, "\n"))
	if runErr != nil {
		return nil, runErr
	}
	lines := strings.Split(strings.ReplaceAll(listOutput, "\r", ""), "\n")
	scanned := 0
	invalid := 0
	for i := len(lines) - 1; i >= 0; i-- {
		path := strings.TrimSpace(lines[i])
		if path == "" {
			continue
		}
		scanned++
		content, catErr := runner.Run("cat " + shellQuote(path))
		if catErr != nil {
			invalid++
			continue
		}
		var manifest mysqlPhysicalBackupManifest
		if err := json.Unmarshal([]byte(content), &manifest); err != nil {
			invalid++
			continue
		}
		if strings.TrimSpace(manifest.ToLSN) == "" {
			invalid++
			continue
		}
		return &mysqlPhysicalBackupBase{
			ManifestPath:        strings.TrimSpace(manifest.ManifestPath),
			StorageArtifactPath: strings.TrimSpace(manifest.StorageArtifactPath),
			FromLSN:             strings.TrimSpace(manifest.FromLSN),
			ToLSN:               strings.TrimSpace(manifest.ToLSN),
			BackupType:          strings.ToUpper(strings.TrimSpace(manifest.BackupType)),
			CreatedAt:           strings.TrimSpace(manifest.CreatedAt),
			ScannedCount:        scanned,
			InvalidCount:        invalid,
		}, nil
	}
	// 没有命中任何有效基线：返回带诊断计数的空基线，让调用方区分「从未备份过」与「基线全部损坏」。
	return &mysqlPhysicalBackupBase{ScannedCount: scanned, InvalidCount: invalid}, nil
}

func uploadMySQLPhysicalManifest(target *BackupExecutionStorageTarget, spec BackupRunDirSpec, manifest *mysqlPhysicalBackupManifest) error {
	// 与基线读取同口径分派：读走哪条链路，写就走哪条，
	// 否则会出现「从 OSS 读到基线、却往 SSH 写回 manifest」的半截链路。
	if IsObjectStorageType(storageTargetTypeOf(target)) {
		return uploadMySQLPhysicalManifestViaObjectStorage(target, spec, manifest)
	}
	cfg, err := buildStorageTargetRemoteConfig(target)
	if err != nil {
		return err
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return err
	}
	defer runner.Close()

	manifestRel := spec.RelPath("backup.meta.json")
	manifestAbs := filepath.Join(strings.TrimSpace(target.BasePath), filepath.FromSlash(manifestRel))
	if _, runErr := runner.Run("mkdir -p " + shellQuote(filepath.Dir(manifestAbs))); runErr != nil {
		return runErr
	}
	buf, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return runner.Upload(manifestAbs, buf)
}

func readXtraBackupCheckpoint(metaDir string) (fromLSN string, toLSN string, lastLSN string, err error) {
	data, err := os.ReadFile(filepath.Join(metaDir, "xtrabackup_checkpoints"))
	if err != nil {
		return "", "", "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
	parsed := map[string]string{}
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		parsed[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return parsed["from_lsn"], parsed["to_lsn"], parsed["last_lsn"], nil
}

func executeMySQLPhysicalBackupJob(ctx context.Context, runControl *backupRunControl, logFile backupRunLogWriter, dir string, logPath string, input BackupExecutionInput, normalized *normalizedObjectSpec, _ map[string]string) (*BackupExecutionResult, error) {
	if !strings.EqualFold(strings.TrimSpace(input.DBType), "MySQL") {
		return nil, fmt.Errorf("MySQL 物理备份仅支持 MySQL")
	}
	cfg, err := loadMySQLPhysicalDriverConfig(input.DriverConfig)
	if err != nil {
		return nil, err
	}
	if input.StorageTarget == nil {
		return nil, fmt.Errorf("MySQL 物理备份需要有效的存储目标配置")
	}
	if !input.DirectStream || input.StreamSink == nil {
		return nil, fmt.Errorf("MySQL 物理备份必须启用 DirectStream 零落盘直传，当前存储目标未建立可写流")
	}
	plan, serverVersion, err := buildMySQLPhysicalExecutionPlanForRun(ctx, input)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Tool == nil {
		return nil, fmt.Errorf("MySQL 物理备份统一执行计划未生成工具信息")
	}
	if err := validateMySQLPhysicalAdmission(plan); err != nil {
		return nil, fmt.Errorf("MySQL 物理备份准入校验失败: %w", err)
	}
	toolSpec := plan.Tool
	vars := plan.RuntimeVars
	logFile.Infof("MySQL 版本: %s", serverVersion)
	logFile.Infof("MySQL 物理备份工具: %s (%s)", toolSpec.Path, toolSpec.Source)
	if datadir := strings.TrimSpace(vars["datadir"]); datadir != "" {
		logFile.Infof("MySQL datadir: %s", datadir)
	}
	if redoBaseDir := strings.TrimSpace(vars["redo_base_dir"]); redoBaseDir != "" {
		logFile.Infof("MySQL redo base dir: %s (innodb_log_group_home_dir=%s)", redoBaseDir, strings.TrimSpace(vars["innodb_log_group_home_dir"]))
	}
	if undoBaseDir := strings.TrimSpace(vars["undo_base_dir"]); undoBaseDir != "" {
		logFile.Infof("MySQL undo base dir: %s (innodb_undo_directory=%s)", undoBaseDir, strings.TrimSpace(vars["innodb_undo_directory"]))
	}
	if dataHomeBaseDir := strings.TrimSpace(vars["data_home_base_dir"]); dataHomeBaseDir != "" {
		logFile.Infof("MySQL InnoDB data home dir: %s (innodb_data_home_dir=%s)", dataHomeBaseDir, strings.TrimSpace(vars["innodb_data_home_dir"]))
	}
	if dataDirs := splitMySQLPhysicalConfiguredDirs(strings.TrimSpace(vars["innodb_directories_list"])); len(dataDirs) > 0 {
		logFile.Infof("MySQL InnoDB extra directories: %s", strings.Join(dataDirs, ", "))
	}

	artifactPlan, planErr := PlanBackupArtifact(input.DBType, input.DriverType, input.ObjectMode, input.SourceDBName, input.BackupType, input.ObjectSpec, input.DriverConfig)
	if planErr != nil {
		return nil, planErr
	}
	artifactName := strings.TrimSpace(artifactPlan.ArtifactName)
	if artifactName == "" {
		artifactName = "backup.xbstream"
	}
	artifactPath := ""
	metaDir := filepath.Join(dir, "xtrabackup-meta")
	if err := ensureDir(metaDir); err != nil {
		return nil, err
	}

	var writer *countingHashWriteCloser
	writer = newCountingHashWriteCloser(input.StreamSink)
	defer writer.Close()

	effectiveBackupType := strings.TrimSpace(plan.EffectiveBackupType)
	if effectiveBackupType == "" {
		effectiveBackupType = "FULL"
	}
	baseInfo := plan.BaseInfo
	warnings := append([]string(nil), plan.Warnings...)
	if strings.EqualFold(strings.TrimSpace(plan.RequestedBackupType), "INCR") && strings.EqualFold(effectiveBackupType, "FULL") && len(warnings) > 0 {
		logFile.Warnf("未找到可用的 MySQL FULL/INCR 基线，当前增量任务自动降级为 FULL")
	}

	args := []string{}
	if strings.TrimSpace(cfg.DefaultsFile) != "" {
		args = append(args, "--defaults-file="+strings.TrimSpace(cfg.DefaultsFile))
	}
	args = append(args,
		"--backup",
		"--stream=xbstream",
		"--extra-lsndir="+metaDir,
		"--user="+input.Instance.User,
		fmt.Sprintf("--port=%d", input.Instance.Port),
		fmt.Sprintf("--parallel=%d", cfg.ProcessMax),
	)
	// --stream 模式下 xtrabackup 仍会在 cwd 创建默认目录 xtrabackup_backupfiles；
	// 切换执行用户(sudo)后 cwd=/ 无写权限会报 cannot mkdir: 13，必须显式指定可写的 target-dir
	args = append(args, "--target-dir="+filepath.Join(dir, "xtrabackup-tmp"))
	// xtrabackup 8.0 不读取 MYSQL_PWD 环境变量，必须显式传 --password
	if pw := strings.TrimSpace(input.Instance.Password); pw != "" {
		args = append(args, "--password="+pw)
	}
	if strings.TrimSpace(cfg.SocketPath) != "" {
		args = append(args, "--socket="+strings.TrimSpace(cfg.SocketPath))
	} else if strings.TrimSpace(input.Instance.Hostname) != "" {
		args = append(args, "--host="+strings.TrimSpace(input.Instance.Hostname))
	}
	// 注意：xtrabackup（含 8.0.x）没有独立的 --incremental 开关，增量语义完全由 --incremental-lsn
	// 或 --incremental-basedir 决定。传入裸 --incremental 会被 xtrabackup 判为 unknown option，
	// 进程在初始化 InnoDB 后直接退出，产出 0 字节的 backup.xbstream。
	if effectiveBackupType == "INCR" && baseInfo != nil && strings.TrimSpace(baseInfo.ToLSN) != "" {
		args = append(args, "--incremental-lsn="+strings.TrimSpace(baseInfo.ToLSN))
		logFile.Infof("MySQL 物理备份将基于上一条链路继续增量 from_lsn=%s", strings.TrimSpace(baseInfo.ToLSN))
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}

	env := withLibraryPath(map[string]string{"MYSQL_PWD": input.Instance.Password}, toolSpec.LibDirs)
	runAsUser := strings.TrimSpace(plan.EffectiveOSUser)
	runErr := error(nil)
	if runAsUser != "" {
		runErr = runLocalCommandAsUserWithStdoutOverride(ctx, runAsUser, "", toolSpec.Path, args, env, logFile, writer)
	} else {
		runErr = runLocalCommandAsDBUserWithStdoutOverride(ctx, "MySQL", "", toolSpec.Path, args, env, logFile, writer)
	}
	closeErr := writer.Close()
	if runErr != nil {
		if runControl.StopRequested() {
			return nil, ErrBackupRunStopped
		}
		return nil, runErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}

	fromLSN, toLSN, lastLSN, checkpointErr := readXtraBackupCheckpoint(metaDir)
	if checkpointErr != nil {
		logFile.Warnf("读取 xtrabackup_checkpoints 失败（降级为告警）: %v", checkpointErr)
	}

	manifest := &mysqlPhysicalBackupManifest{
		RunID:               input.RunID,
		TaskID:              input.TaskID,
		SourceInstanceID:    input.SourceInstanceID,
		DBType:              "MySQL",
		DriverType:          "XTRABACKUP",
		ServerVersion:       strings.TrimSpace(serverVersion),
		Tool:                filepath.Base(toolSpec.Path),
		ToolPath:            strings.TrimSpace(toolSpec.Path),
		ToolSource:          strings.TrimSpace(toolSpec.Source),
		BackupType:          effectiveBackupType,
		ProcessMax:          cfg.ProcessMax,
		DefaultsFile:        strings.TrimSpace(cfg.DefaultsFile),
		SocketPath:          strings.TrimSpace(cfg.SocketPath),
		FromLSN:             strings.TrimSpace(fromLSN),
		ToLSN:               strings.TrimSpace(toLSN),
		LastLSN:             strings.TrimSpace(lastLSN),
		StorageArtifactPath: buildMySQLPhysicalStorageArtifactPath(input.StorageTarget.ID, input.RunDirSpec(), artifactName),
		ManifestPath:        buildMySQLPhysicalManifestPath(input.StorageTarget.ID, input.RunDirSpec()),
		ArtifactName:        artifactName,
		ArtifactSizeBytes:   writer.SizeBytes(),
		ArtifactSHA256:      writer.SHA256(),
		CreatedAt:           time.Now().Format(time.RFC3339),
		Warnings:            warnings,
	}
	if baseInfo != nil {
		manifest.BaseManifestPath = strings.TrimSpace(baseInfo.ManifestPath)
		manifest.BaseStorageArtifact = strings.TrimSpace(baseInfo.StorageArtifactPath)
	}
	if err := uploadMySQLPhysicalManifest(input.StorageTarget, input.RunDirSpec(), manifest); err != nil {
		return nil, fmt.Errorf("上传 MySQL 物理备份元数据失败: %w", err)
	}
	logFile.Infof("MySQL 物理备份完成 type=%s size=%d bytes sha256=%s", manifest.BackupType, manifest.ArtifactSizeBytes, manifest.ArtifactSHA256)
	if strings.TrimSpace(manifest.ToLSN) != "" {
		logFile.Infof("MySQL 物理备份链路 LSN from=%s to=%s", displayOrDefault(manifest.FromLSN, "-"), manifest.ToLSN)
	}

	// 备份集附属文件：与逻辑备份共用同一套目录规范，供恢复预检校验元数据与校验和。
	// 链路细节（增量类型/LSN）走 backup.meta.json，这里只放平台统一字段 + extra 摘要。
	physicalMeta := buildBackupSetMetadata(input, artifactName, manifest.ArtifactSizeBytes, manifest.ArtifactSHA256, BackupTypePhysical, manifest.Tool, strings.TrimSpace(serverVersion))
	physicalMeta.Extra = map[string]any{
		"backup_level": effectiveBackupType,
		"from_lsn":     manifest.FromLSN,
		"to_lsn":       manifest.ToLSN,
		"manifest":     "backup.meta.json",
	}
	metadataOK, checksumOK := writeAndVerifyBackupSetSidecar(dir, physicalMeta,
		[]backupChecksumEntry{{Name: artifactName, Sha256: manifest.ArtifactSHA256}},
	)
	if !metadataOK || !checksumOK {
		logFile.Warnf("写入备份集附属文件校验未通过（不影响备份结果）: metadata=%v checksum=%v", metadataOK, checksumOK)
	}

	return &BackupExecutionResult{
		ArtifactName:        artifactName,
		ArtifactPath:        artifactPath,
		StorageArtifactPath: manifest.StorageArtifactPath,
		StorageTargetID:     input.StorageTarget.ID,
		SizeBytes:           manifest.ArtifactSizeBytes,
		Sha256:              manifest.ArtifactSHA256,
		ServerVersion:       strings.TrimSpace(serverVersion),
		Tool:                manifest.Tool,
		ToolImage:           displayOrDefault(strings.TrimSpace(toolSpec.Source), "path"),
		LogPath:             logPath,
		LogDir:              dir,
		OutputDir:           input.OutputBaseDir,
		ObjectMode:          normalized.ObjectMode,
		ObjectSpec:          normalized.ObjectSpec,
		ObjectSummary:       normalized.ObjectSummary,
		LegacyDBName:        normalized.LegacyDBName,
		SourceDBName:        input.SourceDBName,

		MetadataVerified: metadataOK,
		ChecksumVerified: checksumOK,
	}, nil
}
