package services

import (
	"compress/gzip"
	"encoding/json"
	"strings"

	"gorm.io/datatypes"
)

// 产物压缩级别（任务级可选）
const (
	// ArtifactCompressLevelFast 快：吞吐优先，CPU 占用低
	ArtifactCompressLevelFast = "FAST"
	// ArtifactCompressLevelHigh 高：压缩比优先，CPU 占用较高
	ArtifactCompressLevelHigh = "HIGH"
)

// 压缩默认值：默认启用压缩，默认「快」模式
const (
	DefaultArtifactCompressEnabled = true
	DefaultArtifactCompressLevel   = ArtifactCompressLevelFast
)

// driver_config 中承载压缩选项的字段名
const (
	DriverConfigKeyCompressEnabled = "compress_enabled"
	DriverConfigKeyCompressLevel   = "compress_level"
)

// ArtifactCompressionOption 产物压缩选项，缺省即默认值（启用 + 快）。
type ArtifactCompressionOption struct {
	Enabled bool
	Level   string
}

// DefaultArtifactCompression 返回默认压缩选项：启用压缩 + 快模式。
func DefaultArtifactCompression() ArtifactCompressionOption {
	return ArtifactCompressionOption{
		Enabled: DefaultArtifactCompressEnabled,
		Level:   DefaultArtifactCompressLevel,
	}
}

// normalizeArtifactCompressLevel 归一化压缩级别，非法或缺失时回落默认「快」。
func normalizeArtifactCompressLevel(v any) string {
	switch strings.ToUpper(strings.TrimSpace(readStringFromAny(v))) {
	case ArtifactCompressLevelFast:
		return ArtifactCompressLevelFast
	case ArtifactCompressLevelHigh:
		return ArtifactCompressLevelHigh
	default:
		return DefaultArtifactCompressLevel
	}
}

// resolveArtifactCompression 从 driver_config JSON 解析压缩选项。
// 字段缺失（含存量任务、旧版前端提交）时按默认值生效：启用压缩 + 快。
func resolveArtifactCompression(rawDriverConfig datatypes.JSON) ArtifactCompressionOption {
	opt := DefaultArtifactCompression()
	if len(rawDriverConfig) == 0 {
		return opt
	}
	var data map[string]any
	if err := json.Unmarshal(rawDriverConfig, &data); err != nil {
		return opt
	}
	if _, ok := data[DriverConfigKeyCompressEnabled]; ok {
		opt.Enabled = anyToBool(data[DriverConfigKeyCompressEnabled], opt.Enabled)
	}
	if _, ok := data[DriverConfigKeyCompressLevel]; ok {
		opt.Level = normalizeArtifactCompressLevel(data[DriverConfigKeyCompressLevel])
	}
	return opt
}

// GzipLevel 返回该选项对应的 gzip 压缩级别。
func (o ArtifactCompressionOption) GzipLevel() int {
	if o.Level == ArtifactCompressLevelHigh {
		return gzip.DefaultCompression
	}
	return gzip.BestSpeed
}

// gzipLevelForArtifact 按级别名解析 gzip 压缩级别，非法值按默认「快」处理。
func gzipLevelForArtifact(level string) int {
	return ArtifactCompressionOption{Level: normalizeArtifactCompressLevel(level)}.GzipLevel()
}
