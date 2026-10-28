package services

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/datatypes"
)

type pgBackRestDriverConfig struct {
	BackupType        string
	ProcessMax        int
	Stanza            string
	StartFast         bool
	RepoNodeID        uint64
	RepoHost          string
	RepoPort          int
	RepoUser          string
	RepoPath          string
	RepoToolsDir      string
	RepoRemote        bool
	RepoSSHPassword   string
	RepoSSHPrivateKey string
	// RepoType 为空表示文件仓库（LOCAL_SERVER，SSH 直连或 repo-host 拓扑）；
	// "s3" 表示 pgBackRest 原生对象存储仓库（repo1-type=s3），备份/恢复端直读写 S3 兼容存储。
	RepoType    string
	S3Bucket    string
	S3Endpoint  string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	// S3URIStyle: "host"（虚拟主机，阿里云 OSS 默认）/ "path"（Path-Style，自建 MinIO 等）
	S3URIStyle string
}

// IsS3Repo S3 兼容对象存储仓库模式。
func (c pgBackRestDriverConfig) IsS3Repo() bool {
	return strings.EqualFold(strings.TrimSpace(c.RepoType), "s3")
}

var pgBackRestStanzaRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func normalizeBackupDriverConfig(dbType string, driverType string, backupType string, raw map[string]any) (string, datatypes.JSON, error) {
	drvType := normalizeEnumOrDefault(driverType, "LOGICAL")
	switch drvType {
	case "LOGICAL":
		cfg, err := normalizeLogicalBackupDriverConfig(raw)
		return drvType, cfg, err
	case "XTRABACKUP":
		if strings.TrimSpace(dbType) != "MySQL" {
			return "", nil, fmt.Errorf("XtraBackup 当前仅支持 MySQL")
		}
		cfg, err := normalizeMySQLPhysicalDriverConfig(raw, backupType)
		if err != nil {
			return "", nil, err
		}
		return drvType, cfg, nil
	case "PG_BACKREST":
		if strings.TrimSpace(dbType) != "PostgreSQL" {
			return "", nil, fmt.Errorf("pgBackRest 当前仅支持 PostgreSQL")
		}
		cfg, err := normalizePGBackRestDriverConfig(raw, backupType)
		if err != nil {
			return "", nil, err
		}
		return drvType, cfg, nil
	case "PHYSICAL":
		return "", nil, fmt.Errorf("PostgreSQL 原物理备份方式已下线，请改用 PG_BACKREST")
	default:
		return "", nil, fmt.Errorf("暂不支持该备份驱动")
	}
}

func normalizePGBackRestDriverConfig(raw map[string]any, backupType string) (datatypes.JSON, error) {
	cfg := map[string]any{}
	normalizedBackupType := normalizePGBackRestBackupType(backupType)
	if normalizedBackupType == "" {
		return nil, fmt.Errorf("pgBackRest 备份类型非法，仅支持 FULL/DIFF/INCR")
	}
	processMax := anyToInt(raw["process_max"], 2)
	if processMax < 1 || processMax > 64 {
		return nil, fmt.Errorf("pgBackRest 并行数 process_max 必须在 1-64 之间")
	}
	cfg["backup_type"] = normalizedBackupType
	cfg["process_max"] = processMax
	cfg["start_fast"] = anyToBool(raw["start_fast"], true)
	stanza := readStringFromMap(raw, "stanza")
	if stanza != "" {
		if !pgBackRestStanzaRe.MatchString(stanza) {
			return nil, fmt.Errorf("pgBackRest stanza 非法，仅支持字母、数字、下划线和中划线")
		}
		cfg["stanza"] = stanza
	}
	if text := readStringFromMap(raw, "repo_host"); text != "" {
		cfg["repo_host"] = text
	}
	if value := anyToInt(raw["repo_port"], 22); value > 0 {
		cfg["repo_port"] = value
	}
	if text := readStringFromMap(raw, "repo_user"); text != "" {
		cfg["repo_user"] = text
	}
	if text := readStringFromMap(raw, "repo_path"); text != "" {
		cfg["repo_path"] = text
	}
	if text := readStringFromMap(raw, "repo_tools_dir"); text != "" {
		cfg["repo_tools_dir"] = text
	}
	if value := anyToInt(raw["repo_node_id"], 0); value > 0 {
		cfg["repo_node_id"] = value
	}
	if text := readStringFromMap(raw, "repo_ssh_password"); text != "" {
		cfg["repo_ssh_password"] = text
	}
	if text := readStringFromMap(raw, "repo_ssh_private_key"); text != "" {
		cfg["repo_ssh_private_key"] = text
	}
	cfg["repo_remote"] = anyToBool(raw["repo_remote"], false)
	// S3 仓库参数由运行级 prepare（preparePGBackRestRunDriverConfig）写入，这里原样透传，
	// 保证「创建任务 → 预校验影子任务 → 执行」全链路配置一致。
	if text := readStringFromMap(raw, "repo_type"); strings.EqualFold(strings.TrimSpace(text), "s3") {
		cfg["repo_type"] = "s3"
	}
	if text := readStringFromMap(raw, "s3_bucket"); text != "" {
		cfg["s3_bucket"] = text
	}
	if text := readStringFromMap(raw, "s3_endpoint"); text != "" {
		cfg["s3_endpoint"] = text
	}
	if text := readStringFromMap(raw, "s3_region"); text != "" {
		cfg["s3_region"] = text
	}
	if text := readStringFromMap(raw, "s3_access_key"); text != "" {
		cfg["s3_access_key"] = text
	}
	if text := readStringFromMap(raw, "s3_secret_key"); text != "" {
		cfg["s3_secret_key"] = text
	}
	if text := strings.ToLower(readStringFromMap(raw, "s3_uri_style")); text == "path" || text == "host" {
		cfg["s3_uri_style"] = text
	}
	// 单次执行最长运行时间（小时）：任务级可选，缺省不写（回落环境变量 / 平台默认 12 小时）。
	// 该上限作用于 pgbackrest 命令（备份 / check / expire）的兜底看门狗。
	if hours := anyToInt(raw[DriverConfigKeyMaxRuntimeHours], 0); hours > 0 {
		if hours < maxRuntimeHoursMin || hours > maxRuntimeHoursMax {
			return nil, fmt.Errorf("单次执行最长运行时间必须在 %d-%d 小时之间", maxRuntimeHoursMin, maxRuntimeHoursMax)
		}
		cfg[DriverConfigKeyMaxRuntimeHours] = hours
	}
	return marshalJSONMapOrNil(cfg)
}

func loadPGBackRestDriverConfig(raw datatypes.JSON) (pgBackRestDriverConfig, error) {
	cfg := pgBackRestDriverConfig{
		BackupType: "FULL",
		ProcessMax: 2,
		StartFast:  true,
	}
	if len(raw) == 0 {
		return cfg, nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return cfg, err
	}
	if text := normalizePGBackRestBackupType(fmt.Sprintf("%v", data["backup_type"])); text != "" {
		cfg.BackupType = text
	}
	if v := anyToInt(data["process_max"], cfg.ProcessMax); v >= 1 && v <= 64 {
		cfg.ProcessMax = v
	}
	cfg.StartFast = anyToBool(data["start_fast"], cfg.StartFast)
	cfg.Stanza = readStringFromAny(data["stanza"])
	cfg.RepoNodeID = uint64(anyToInt(data["repo_node_id"], 0))
	cfg.RepoHost = readStringFromAny(data["repo_host"])
	cfg.RepoPort = anyToInt(data["repo_port"], 22)
	cfg.RepoUser = readStringFromAny(data["repo_user"])
	cfg.RepoPath = readStringFromAny(data["repo_path"])
	cfg.RepoToolsDir = readStringFromAny(data["repo_tools_dir"])
	cfg.RepoRemote = anyToBool(data["repo_remote"], false)
	cfg.RepoSSHPassword = readStringFromAny(data["repo_ssh_password"])
	cfg.RepoSSHPrivateKey = readStringFromAny(data["repo_ssh_private_key"])
	if strings.EqualFold(strings.TrimSpace(readStringFromAny(data["repo_type"])), "s3") {
		cfg.RepoType = "s3"
		cfg.S3Bucket = readStringFromAny(data["s3_bucket"])
		cfg.S3Endpoint = readStringFromAny(data["s3_endpoint"])
		cfg.S3Region = readStringFromAny(data["s3_region"])
		cfg.S3AccessKey = readStringFromAny(data["s3_access_key"])
		cfg.S3SecretKey = readStringFromAny(data["s3_secret_key"])
		if style := strings.ToLower(readStringFromAny(data["s3_uri_style"])); style == "path" || style == "host" {
			cfg.S3URIStyle = style
		} else {
			cfg.S3URIStyle = "host"
		}
	}
	if cfg.Stanza != "" && !pgBackRestStanzaRe.MatchString(cfg.Stanza) {
		return cfg, fmt.Errorf("pgBackRest stanza 非法，仅支持字母、数字、下划线和中划线")
	}
	return cfg, nil
}

func setPGBackRestDriverConfigStanza(raw datatypes.JSON, stanza string) (datatypes.JSON, error) {
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}
	text := strings.TrimSpace(stanza)
	if text == "" {
		delete(cfg, "stanza")
	} else {
		if !pgBackRestStanzaRe.MatchString(text) {
			return nil, fmt.Errorf("pgBackRest stanza 非法，仅支持字母、数字、下划线和中划线")
		}
		cfg["stanza"] = text
	}
	return marshalJSONMapOrNil(cfg)
}

func normalizePGBackRestBackupType(value string) string {
	text := strings.ToUpper(strings.TrimSpace(value))
	if text == "" {
		return "FULL"
	}
	switch text {
	case "FULL", "DIFF", "INCR":
		return text
	default:
		return ""
	}
}

func validatePostgresPhysicalVersion(serverVersion string) error {
	prefix := versionPrefixRe.FindString(strings.TrimSpace(serverVersion))
	if prefix == "" {
		return fmt.Errorf("无法识别 PostgreSQL 版本: %s", strings.TrimSpace(serverVersion))
	}
	parts := strings.Split(prefix, ".")
	major, _ := strconv.Atoi(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if major < 9 || (major == 9 && minor < 6) {
		return fmt.Errorf("PostgreSQL 物理备份仅支持 9.6 及以上版本")
	}
	return nil
}

func isPostgresPhysicalDriverType(driverType string) bool {
	switch strings.ToUpper(strings.TrimSpace(driverType)) {
	case "PHYSICAL", "PG_BACKREST":
		return true
	default:
		return false
	}
}

func resolveBackupStorageBaseDir(baseDir string, driverType string, dbType string) string {
	if strings.TrimSpace(dbType) == "MySQL" && isMySQLPhysicalDriverType(driverType) {
		return filepath.Join(baseDir, "xtrabackup", "mysql")
	}
	if strings.TrimSpace(dbType) != "PostgreSQL" {
		return baseDir
	}
	switch strings.ToUpper(strings.TrimSpace(driverType)) {
	case "PG_BACKREST":
		return filepath.Join(baseDir, "pgbackrest", "postgresql")
	case "PHYSICAL":
		return filepath.Join(baseDir, "physical", "postgresql")
	default:
		return baseDir
	}
}

func resolvePGBackRestRepoDir(baseDir string, sourceInstanceID uint64, taskID string) string {
	root := resolveBackupStorageBaseDir(baseDir, "PG_BACKREST", "PostgreSQL")
	return filepath.Join(root, fmt.Sprintf("instance-%d", sourceInstanceID), sanitizePathSegment(taskID, "task"), "repo")
}

func readStringFromMap(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	return readStringFromAny(raw[key])
}

func readStringFromAny(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func anyToInt(v any, def int) int {
	switch value := v.(type) {
	case int:
		return value
	case int8:
		return int(value)
	case int16:
		return int(value)
	case int32:
		return int(value)
	case int64:
		return int(value)
	case uint:
		return int(value)
	case uint8:
		return int(value)
	case uint16:
		return int(value)
	case uint32:
		return int(value)
	case uint64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return def
}

func anyToBool(v any, def bool) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	case int, int8, int16, int32, int64:
		return anyToInt(v, 0) != 0
	case uint, uint8, uint16, uint32, uint64:
		return anyToInt(v, 0) != 0
	case float32, float64:
		return anyToInt(v, 0) != 0
	}
	return def
}
