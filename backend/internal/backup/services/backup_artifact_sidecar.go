package services

// 备份集附属文件（sidecar）契约。
//
// 背景：恢复预检的「元数据 / 校验和」两项检查依赖产物同目录下的 metadata.json 与
// checksums.sha256，早期版本备份侧从未产出这两个文件，导致两项检查恒为 WARN，
// 生产环境还会被 verified_backupset 一票否决。本文件把「备份侧怎么写」和
// 「恢复侧怎么读」收敛到一处，避免再次出现两侧对不上的情况。
//
// 约定：
//  1. 两个文件与备份产物落在同一批次目录（run dir），随产物一起保留/清理；
//  2. metadata.json 的 backup_type / source_db_type 是恢复预检的强校验字段，改名必须同步改
//     logical_restore_workflow.go 的 validateRestoreMetadata；
//  3. 附属文件写失败只降级告警，不得让已经成功的备份失败。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	backupMetadataFileName = "metadata.json"
	backupChecksumFileName = "checksums.sha256"
	backupMetadataSchema   = "goinsight.backup.metadata/v1"

	// BackupTypeLogical / BackupTypePhysical 对应恢复预检 validateRestoreMetadata 的白名单。
	BackupTypeLogical  = "logical"
	BackupTypePhysical = "physical"
)

// backupSetMetadata 是备份完成时写在产物同目录的备份集描述文件。
type backupSetMetadata struct {
	Schema           string         `json:"schema"`
	BackupType       string         `json:"backup_type"`
	SourceDBType     string         `json:"source_db_type"`
	SourceDBName     string         `json:"source_db_name,omitempty"`
	DriverType       string         `json:"driver_type,omitempty"`
	Tool             string         `json:"tool,omitempty"`
	ServerVersion    string         `json:"server_version,omitempty"`
	ObjectMode       string         `json:"object_mode,omitempty"`
	ObjectSummary    string         `json:"object_summary,omitempty"`
	RunID            uint64         `json:"run_id,omitempty"`
	TaskID           string         `json:"task_id,omitempty"`
	SourceInstanceID uint64         `json:"source_instance_id,omitempty"`
	ArtifactName     string         `json:"artifact_name,omitempty"`
	ArtifactSize     int64          `json:"artifact_size_bytes,omitempty"`
	ArtifactSHA256   string         `json:"artifact_sha256,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
	CreatedAt        string         `json:"created_at"`
}

// backupChecksumEntry 是 checksums.sha256 中的一行。
type backupChecksumEntry struct {
	Name   string
	Sha256 string
}

// writeBackupSetSidecarFiles 在批次目录下写出 metadata.json 与 checksums.sha256。
// 返回错误时调用方应降级为告警（备份本身已经成功）。
func writeBackupSetSidecarFiles(dir string, meta backupSetMetadata, entries []backupChecksumEntry) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("批次目录为空，跳过备份集附属文件")
	}
	if err := writeBackupSetMetadata(dir, meta); err != nil {
		return err
	}
	return writeBackupSetChecksums(dir, entries)
}

func writeBackupSetMetadata(dir string, meta backupSetMetadata) error {
	if strings.TrimSpace(meta.Schema) == "" {
		meta.Schema = backupMetadataSchema
	}
	if strings.TrimSpace(meta.CreatedAt) == "" {
		meta.CreatedAt = time.Now().Format(time.RFC3339)
	}
	buf, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化备份集元数据失败: %w", err)
	}
	path := filepath.Join(dir, backupMetadataFileName)
	if err := os.WriteFile(path, append(buf, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", backupMetadataFileName, err)
	}
	return nil
}

// writeBackupSetChecksums 按 sha256sum 标准格式写出：<摘要><两个空格><文件名>。
// 恢复侧的 parseChecksumFromFile 按该格式解析并按文件名匹配。
func writeBackupSetChecksums(dir string, entries []backupChecksumEntry) error {
	lines := make([]string, 0, len(entries))
	for _, item := range entries {
		name := strings.TrimSpace(item.Name)
		sum := strings.TrimSpace(item.Sha256)
		if name == "" || sum == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s", sum, name))
	}
	if len(lines) == 0 {
		// 没有可用摘要时不产出空文件，避免恢复侧误判为「已声明但匹配不到」
		return nil
	}
	path := filepath.Join(dir, backupChecksumFileName)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", backupChecksumFileName, err)
	}
	return nil
}

// writeAndVerifyBackupSetSidecar 写出附属文件并**立即回读校验**，返回元数据/校验和两项是否验证通过。
//
// 设计要点：
//  1. 「写了」不等于「可验证」，必须回读校验通过才算 verified —— 否则备份侧写入一个
//     损坏/字段缺失的文件，恢复侧照样会被 verified_backupset 一票否决；
//  2. 校验口径与恢复侧 validateRestoreMetadata / validateRestoreChecksum 保持一致
//     （backup_type / source_db_type 非空、schema 行可解析），避免两侧再次对不上；
//  3. 任一项失败只降级，不影响备份本身的成功状态。
func writeAndVerifyBackupSetSidecar(dir string, meta backupSetMetadata, entries []backupChecksumEntry) (bool, bool) {
	metadataOK := false
	checksumOK := false
	if err := writeBackupSetMetadata(dir, meta); err == nil {
		metadataOK = verifyBackupSetMetadataFile(filepath.Join(dir, backupMetadataFileName))
	}
	if err := writeBackupSetChecksums(dir, entries); err == nil {
		checksumOK = verifyBackupSetChecksumFile(filepath.Join(dir, backupChecksumFileName), entries)
	}
	return metadataOK, checksumOK
}

// verifyBackupSetMetadataFile 回读 metadata.json，确认恢复侧强校验字段存在且合法。
func verifyBackupSetMetadataFile(path string) bool {
	buf, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		return false
	}
	backupType := sidecarStringField(raw, "backup_type")
	sourceDBType := sidecarStringField(raw, "source_db_type")
	// 两个字段都为空时，恢复侧视为「未验证」，这里同样不能算通过
	if backupType == "" && sourceDBType == "" {
		return false
	}
	if backupType != "" &&
		!strings.EqualFold(backupType, BackupTypeLogical) &&
		!strings.EqualFold(backupType, BackupTypePhysical) {
		return false
	}
	return true
}

// verifyBackupSetChecksumFile 回读 checksums.sha256，确认声明的摘要条目确实落盘且可解析。
func verifyBackupSetChecksumFile(path string, entries []backupChecksumEntry) bool {
	// 没有可用摘要时不会产出文件（见 writeBackupSetChecksums），此时不能算验证通过
	if len(entries) == 0 {
		return false
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	declared := map[string]string{}
	for _, item := range entries {
		name := strings.TrimSpace(item.Name)
		sum := strings.TrimSpace(item.Sha256)
		if name == "" || sum == "" {
			continue
		}
		declared[name] = sum
	}
	if len(declared) == 0 {
		return false
	}
	matched := 0
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 格式：<摘要><空白><文件名>
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := fields[0]
		name := strings.TrimSpace(strings.Join(fields[1:], " "))
		if want, ok := declared[name]; ok && strings.EqualFold(want, sum) {
			matched++
		}
	}
	return matched == len(declared)
}

// sidecarStringField 按 key 存在性取字符串字段，缺失一律返回空串（而非 "<nil>"）。
func sidecarStringField(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

// writePGBackRestSidecar 为 pgBackRest 物理备份补充平台统一的备份集附属文件。
//
// 注意：pgBackRest 自己的 backup_type 是 FULL / INCR（备份层级），与平台的
// logical / physical（恢复方式）语义不同，因此层级信息放进 extra.backup_level，
// 顶层 backup_type 固定为 physical，否则恢复预检会误判。
func writePGBackRestSidecar(dir string, logFile backupRunLogWriter, input BackupExecutionInput, stanza string, serverVersion string, artifactName string, size int64, sha string, backupMeta *pgBackRestBackupMetadata, repoPath string) (bool, bool) {
	meta := buildBackupSetMetadata(input, artifactName, size, sha, BackupTypePhysical, "pgbackrest", serverVersion)
	meta.DriverType = "PG_BACKREST"
	extra := map[string]any{
		"stanza":       strings.TrimSpace(stanza),
		"repo_path":    strings.TrimSpace(repoPath),
		"backup_level": strings.ToUpper(strings.TrimSpace(backupMeta.BackupType)),
		"backup_label": strings.TrimSpace(backupMeta.Label),
	}
	if len(backupMeta.Reference) > 0 {
		extra["backup_reference"] = backupMeta.Reference
	}
	meta.Extra = extra
	metadataOK, checksumOK := writeAndVerifyBackupSetSidecar(dir, meta, []backupChecksumEntry{{Name: artifactName, Sha256: sha}})
	if !metadataOK || !checksumOK {
		logFile.Warnf("写入备份集附属文件校验未通过（不影响备份结果）: metadata=%v checksum=%v", metadataOK, checksumOK)
	}
	return metadataOK, checksumOK
}

// buildBackupSetMetadata 构造备份集描述。tool / serverVersion 由调用方从执行结果传入
// （BackupExecutionInput 本身不携带这两项）。
func buildBackupSetMetadata(input BackupExecutionInput, artifactName string, artifactSize int64, artifactSHA string, backupType string, tool string, serverVersion string) backupSetMetadata {
	sourceDBName := strings.TrimSpace(input.SourceDBName)
	if sourceDBName == "" {
		sourceDBName = "all_databases"
	}
	return backupSetMetadata{
		Schema:           backupMetadataSchema,
		BackupType:       backupType,
		SourceDBType:     strings.TrimSpace(input.DBType),
		SourceDBName:     sourceDBName,
		DriverType:       normalizeEnumOrDefault(input.DriverType, "LOGICAL"),
		Tool:             strings.TrimSpace(tool),
		ServerVersion:    strings.TrimSpace(serverVersion),
		ObjectMode:       strings.TrimSpace(input.ObjectMode),
		ObjectSummary:    strings.TrimSpace(input.ObjectSummary),
		RunID:            input.RunID,
		TaskID:           strings.TrimSpace(input.TaskID),
		SourceInstanceID: input.SourceInstanceID,
		ArtifactName:     strings.TrimSpace(artifactName),
		ArtifactSize:     artifactSize,
		ArtifactSHA256:   strings.TrimSpace(artifactSHA),
	}
}
