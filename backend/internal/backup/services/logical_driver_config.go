package services

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/datatypes"
)

type logicalBackupDriverConfig struct {
	ProcessMax      int
	OSUser          string
	ToolPath        string
	CompressEnabled bool
	CompressLevel   string
}

var logicalOSUserRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)

func normalizeLogicalBackupDriverConfig(raw map[string]any) (datatypes.JSON, error) {
	cfg := map[string]any{}
	processMax := anyToInt(raw["process_max"], 2)
	if processMax < 1 || processMax > 64 {
		return nil, fmt.Errorf("逻辑备份并行数 process_max 必须在 1-64 之间")
	}
	cfg["process_max"] = processMax

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
		readStringFromMap(raw, "mysqldump_path"),
		readStringFromMap(raw, "pg_dump_path"),
	)
	if toolPath != "" {
		cfg["tool_path"] = toolPath
	}

	// 单次执行最长运行时间（小时）：任务级可选，缺省不写（回落到环境变量 / 平台默认 12 小时）。
	// 该上限作用于 mysqldump / pg_dump 等数据库命令的兜底看门狗。
	if hours := anyToInt(raw[DriverConfigKeyMaxRuntimeHours], 0); hours > 0 {
		if hours < maxRuntimeHoursMin || hours > maxRuntimeHoursMax {
			return nil, fmt.Errorf("单次执行最长运行时间必须在 %d-%d 小时之间", maxRuntimeHoursMin, maxRuntimeHoursMax)
		}
		cfg[DriverConfigKeyMaxRuntimeHours] = hours
	}

	// 产物压缩：任务级可选，缺省启用压缩并采用「快」模式
	cfg[DriverConfigKeyCompressEnabled] = anyToBool(raw[DriverConfigKeyCompressEnabled], DefaultArtifactCompressEnabled)
	cfg[DriverConfigKeyCompressLevel] = normalizeArtifactCompressLevel(raw[DriverConfigKeyCompressLevel])

	return marshalJSONMapOrNil(cfg)
}

func loadLogicalBackupDriverConfig(raw datatypes.JSON) (logicalBackupDriverConfig, error) {
	cfg := logicalBackupDriverConfig{
		ProcessMax:      2,
		CompressEnabled: DefaultArtifactCompressEnabled,
		CompressLevel:   DefaultArtifactCompressLevel,
	}
	if len(raw) == 0 {
		return cfg, nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return cfg, err
	}
	cfg.ProcessMax = anyToInt(data["process_max"], cfg.ProcessMax)
	if cfg.ProcessMax < 1 || cfg.ProcessMax > 64 {
		cfg.ProcessMax = 2
	}
	cfg.OSUser = firstNonEmpty(
		readStringFromAny(data["os_user"]),
		readStringFromAny(data["process_user"]),
		readStringFromAny(data["db_os_user"]),
	)
	cfg.ToolPath = firstNonEmpty(
		readStringFromAny(data["tool_path"]),
		readStringFromAny(data["dump_path"]),
		readStringFromAny(data["mysqldump_path"]),
		readStringFromAny(data["pg_dump_path"]),
	)
	if cfg.OSUser != "" && !logicalOSUserRe.MatchString(cfg.OSUser) {
		return cfg, fmt.Errorf("操作系统用户格式非法，仅支持字母、数字、点、下划线和中划线")
	}
	cfg.ToolPath = strings.TrimSpace(cfg.ToolPath)

	if _, ok := data[DriverConfigKeyCompressEnabled]; ok {
		cfg.CompressEnabled = anyToBool(data[DriverConfigKeyCompressEnabled], cfg.CompressEnabled)
	}
	if _, ok := data[DriverConfigKeyCompressLevel]; ok {
		cfg.CompressLevel = normalizeArtifactCompressLevel(data[DriverConfigKeyCompressLevel])
	}
	return cfg, nil
}
