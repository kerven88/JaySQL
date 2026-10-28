package services

import (
	"encoding/json"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"gorm.io/datatypes"
)

const (
	restoreKindLogical  = "LOGICAL"
	restoreKindPhysical = "PHYSICAL"

	restoreTargetLevelInstance = "INSTANCE"
	restoreTargetLevelDatabase = "DATABASE"

	// 恢复对象范围（向导第 2 步）
	restoreObjectScopeAll    = "ALL"
	restoreObjectScopeCustom = "CUSTOM"

	// 结构冲突策略（向导第 1 步「高级设置」）
	restoreSchemaConflictSkip  = "SKIP"  // 忽略冲突，继续任务
	restoreSchemaConflictAbort = "ABORT" // 对象已存在，则停止任务

	// 数据冲突策略（向导第 1 步「高级设置」）
	restoreDataConflictSkip     = "SKIP"     // 忽略冲突，继续任务
	restoreDataConflictAbort    = "ABORT"    // 数据已存在，则停止任务
	restoreDataConflictTruncate = "TRUNCATE" // 先删除，再重新写入
)

type physicalRestoreRunOptions struct {
	AutoStart bool `json:"auto_start"`
}

// logicalRestoreDBMapping 源库 -> 目标库的显式映射（向导「配置映射」步骤）。
type logicalRestoreDBMapping struct {
	SourceDB string `json:"source_db"`
	TargetDB string `json:"target_db"`
}

type logicalRestoreRunOptions struct {
	ExecutionMode           string                   `json:"execution_mode"`
	RestoreMode             string                   `json:"restore_mode"`
	TargetDBName            string                   `json:"target_db_name"`
	TargetDBSuffix          string                   `json:"target_db_suffix"`
	SelectedDatabases       []string                 `json:"selected_databases,omitempty"`
	SelectedSchemas         []string                 `json:"selected_schemas,omitempty"`
	SelectedTables          []string                 `json:"selected_tables,omitempty"`
	Parallel                int                      `json:"parallel"`
	Timeout                 string                   `json:"timeout"`
	TargetCharset           string                   `json:"target_charset,omitempty"`
	TargetCollation         string                   `json:"target_collation,omitempty"`
	TargetEncoding          string                   `json:"target_encoding,omitempty"`
	CloseBinlog             bool                     `json:"close_binlog"`
	ForeignKeyChecks        bool                     `json:"foreign_key_checks"`
	ExitOnError             bool                     `json:"exit_on_error"`
	NoOwner                 bool                     `json:"no_owner"`
	NoPrivileges            bool                     `json:"no_privileges"`
	CleanIfExists           bool                     `json:"clean_if_exists"`
	IsCloud                 bool                     `json:"is_cloud"`
	ExecuteConfirmed        bool                     `json:"execute_confirmed"`
	ObjectScope             string                   `json:"object_scope,omitempty"`
	DBMappings              []logicalRestoreDBMapping `json:"db_mappings,omitempty"`
	SchemaConflictPolicy    string                   `json:"schema_conflict_policy,omitempty"`
	DataConflictPolicy      string                   `json:"data_conflict_policy,omitempty"`
	RestoreSchema           bool                     `json:"restore_schema"`
	RestoreData             bool                     `json:"restore_data"`
}

type logicalRestoreResolvedContext struct {
	BackupFile         string                     `json:"backup_file"`
	ArtifactFormat     string                     `json:"artifact_format"`
	MetadataFile       string                     `json:"metadata_file,omitempty"`
	ChecksumFile       string                     `json:"checksum_file,omitempty"`
	MetadataVerified   bool                       `json:"metadata_verified"`
	ChecksumVerified   bool                       `json:"checksum_verified"`
	SourceVersion      string                     `json:"source_version,omitempty"`
	TargetVersion      string                     `json:"target_version,omitempty"`
	BackupSizeBytes    int64                      `json:"backup_size_bytes"`
	EstimatedSizeBytes int64                      `json:"estimated_size_bytes"`
	Commands           []string                   `json:"commands,omitempty"`
	Sources            []logicalRestoreSourcePlan `json:"sources,omitempty"`
}

type logicalRestoreSourcePlan struct {
	SourceObjectName string   `json:"source_object_name"`
	TargetObjectName string   `json:"target_object_name"`
	EntryName        string   `json:"entry_name,omitempty"`
	Format           string   `json:"format"`
	IsDir            bool     `json:"is_dir"`
	AllDatabases     bool     `json:"all_databases"`
	SelectedSchemas  []string `json:"selected_schemas,omitempty"`
	SelectedTables   []string `json:"selected_tables,omitempty"`
}

func buildPhysicalRestoreResolvedContextJSON(resolved physicalRestoreResolved) (datatypes.JSON, error) {
	return marshalJSONValue(resolved)
}

func decodePhysicalRestoreResolvedContextJSON(raw datatypes.JSON) physicalRestoreResolved {
	var out physicalRestoreResolved
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func buildPhysicalRestoreOptionsJSON(autoStart bool) (datatypes.JSON, error) {
	return marshalJSONValue(physicalRestoreRunOptions{AutoStart: autoStart})
}

func decodePhysicalRestoreOptionsJSON(raw datatypes.JSON) physicalRestoreRunOptions {
	var out physicalRestoreRunOptions
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func buildLogicalRestoreResolvedContextJSON(resolved logicalRestoreResolvedContext) (datatypes.JSON, error) {
	return marshalJSONValue(resolved)
}

func decodeLogicalRestoreResolvedContextJSON(raw datatypes.JSON) logicalRestoreResolvedContext {
	var out logicalRestoreResolvedContext
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func buildLogicalRestoreOptionsJSON(options logicalRestoreRunOptions) (datatypes.JSON, error) {
	return marshalJSONValue(options)
}

func decodeLogicalRestoreOptionsJSON(raw datatypes.JSON) logicalRestoreRunOptions {
	out := logicalRestoreRunOptions{
		ExecutionMode:    restoreExecutionModePlan,
		Parallel:         4,
		Timeout:          "4h",
		ForeignKeyChecks: false,
		ExitOnError:      true,
		NoOwner:          true,
		NoPrivileges:     true,
		RestoreSchema:    true,
		RestoreData:      true,
	}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	if strings.TrimSpace(out.ExecutionMode) == "" {
		out.ExecutionMode = restoreExecutionModePlan
	}
	if out.Parallel <= 0 {
		out.Parallel = 4
	}
	if strings.TrimSpace(out.Timeout) == "" {
		out.Timeout = "4h"
	}
	// 兼容历史任务：旧 options 不含恢复类型字段，语义等价于「结构与数据都恢复」。
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) == nil {
		if _, ok := probe["restore_schema"]; !ok {
			out.RestoreSchema = true
		}
		if _, ok := probe["restore_data"]; !ok {
			out.RestoreData = true
		}
	}
	if strings.TrimSpace(out.ObjectScope) == "" {
		if len(out.SelectedDatabases) > 0 {
			out.ObjectScope = restoreObjectScopeCustom
		} else {
			out.ObjectScope = restoreObjectScopeAll
		}
	}
	if strings.TrimSpace(out.SchemaConflictPolicy) == "" {
		out.SchemaConflictPolicy = deriveSchemaConflictPolicy(out.RestoreMode)
	}
	if strings.TrimSpace(out.DataConflictPolicy) == "" {
		out.DataConflictPolicy = deriveDataConflictPolicy(out.RestoreMode)
	}
	return out
}

// deriveRestoreModeFromPolicies 由「结构冲突策略 + 数据冲突策略」推导底层 restore_mode。
// 新向导只暴露两个策略，底层仍复用 NEW/OVERWRITE/APPEND 三条既有执行路径：
//   - 数据冲突 = TRUNCATE（先删除再写入）      -> OVERWRITE
//   - 结构冲突 = ABORT（对象已存在则停止）      -> NEW
//   - 其余（忽略冲突继续）                      -> APPEND
func deriveRestoreModeFromPolicies(schemaPolicy string, dataPolicy string) string {
	switch strings.ToUpper(strings.TrimSpace(dataPolicy)) {
	case restoreDataConflictTruncate:
		return logicalRestoreModeOverwrite
	}
	if strings.EqualFold(strings.TrimSpace(schemaPolicy), restoreSchemaConflictAbort) {
		return logicalRestoreModeNew
	}
	return logicalRestoreModeAppend
}

// deriveSchemaConflictPolicy 由底层 restore_mode 反推结构冲突策略（历史任务展示用）。
func deriveSchemaConflictPolicy(restoreMode string) string {
	if strings.EqualFold(strings.TrimSpace(restoreMode), logicalRestoreModeNew) {
		return restoreSchemaConflictAbort
	}
	return restoreSchemaConflictSkip
}

// deriveDataConflictPolicy 由底层 restore_mode 反推数据冲突策略（历史任务展示用）。
func deriveDataConflictPolicy(restoreMode string) string {
	if strings.EqualFold(strings.TrimSpace(restoreMode), logicalRestoreModeOverwrite) {
		return restoreDataConflictTruncate
	}
	return restoreDataConflictSkip
}

// logicalRestoreTargetForSource 解析某个源库对应的目标库名：
// 优先使用显式映射（向导「配置映射」），其次回落到后缀规则。
func logicalRestoreTargetForSource(options logicalRestoreRunOptions, sourceDB string) (string, bool) {
	key := strings.TrimSpace(sourceDB)
	if key == "" {
		return "", false
	}
	for _, mapping := range options.DBMappings {
		if strings.EqualFold(strings.TrimSpace(mapping.SourceDB), key) {
			target := strings.TrimSpace(mapping.TargetDB)
			if target == "" {
				return "", false
			}
			return target, true
		}
	}
	return "", false
}

func restoreRunAutoStart(run *backupModels.InsightRestoreRuns) bool {
	if run == nil {
		return false
	}
	return decodePhysicalRestoreOptionsJSON(run.Options).AutoStart
}

func restoreRunResolvedContext(run *backupModels.InsightRestoreRuns) physicalRestoreResolved {
	if run == nil {
		return physicalRestoreResolved{}
	}
	return decodePhysicalRestoreResolvedContextJSON(run.ResolvedContext)
}

func logicalRestoreTargetName(run *backupModels.InsightRestoreRuns) string {
	if run == nil {
		return ""
	}
	if text := strings.TrimSpace(run.TargetDBName); text != "" {
		return text
	}
	return strings.TrimSpace(run.TargetName)
}
