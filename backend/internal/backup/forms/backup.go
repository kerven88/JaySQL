package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type GetInstancesForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search" json:"search"`
	DBType      string `form:"db_type" json:"db_type"`
}

type GetBackupTasksForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search" json:"search"`
	DBType      string `form:"db_type" json:"db_type"`
	InstanceID  uint64 `form:"instance_id" json:"instance_id"`
	// Mine=true 时只返回当前登录用户创建的任务。首页「我的资源 · 备份任务」卡与该筛选口径一致
	// （created_by = 当前用户），前端从卡片点击进来时带 mine=1，保证卡片数字与列表一致。
	Mine bool `form:"mine" json:"mine"`
}

type BackupTaskIDForm struct {
	ID string `uri:"id" binding:"required,min=1,max=20"`
}

type BackupRunIDForm struct {
	ID uint64 `uri:"id" binding:"required"`
}

type GetBackupRunLogForm struct {
	TailKB int64 `form:"tail_kb" json:"tail_kb"`
}

type GetBackupNodesForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search" json:"search"`
	Status      string `form:"status" json:"status"`
}

type GetStorageTargetsForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search" json:"search"`
	Type        string `form:"type" json:"type"`
	Status      string `form:"status" json:"status"`
}

type StorageTargetIDForm struct {
	ID uint64 `uri:"id" binding:"required"`
}

type CreateStorageTargetForm struct {
	Name             string `form:"name" json:"name" binding:"required,min=2,max=128"`
	Type             string `form:"type" json:"type" binding:"required,oneof=LOCAL_SERVER OSS_ALIYUN S3_MINIO COS_TENCENT OBS_HUAWEI"`
	Host             string `form:"host" json:"host" binding:"omitempty,max=255"`
	Port             int    `form:"port" json:"port" binding:"omitempty,min=1,max=65535"`
	BasePath         string `form:"base_path" json:"base_path" binding:"omitempty,max=1024"`
	SSHUser          string `form:"ssh_user" json:"ssh_user" binding:"omitempty,max=128"`
	SSHPassword      string `form:"ssh_password" json:"ssh_password" binding:"omitempty,max=255"`
	SSHPrivateKey    string `form:"ssh_private_key" json:"ssh_private_key"`
	S3Endpoint       string `form:"s3_endpoint" json:"s3_endpoint" binding:"omitempty,max=512"`
	S3Region         string `form:"s3_region" json:"s3_region" binding:"omitempty,max=128"`
	S3Bucket         string `form:"s3_bucket" json:"s3_bucket" binding:"omitempty,max=255"`
	S3AccessKey      string `form:"s3_access_key" json:"s3_access_key" binding:"omitempty,max=255"`
	S3SecretKey      string `form:"s3_secret_key" json:"s3_secret_key" binding:"omitempty,max=512"`
	S3ForcePathStyle *bool  `form:"s3_force_path_style" json:"s3_force_path_style"`
	Remark           string `form:"remark" json:"remark"`
}

type UpdateStorageTargetForm struct {
	Name             string `form:"name" json:"name" binding:"required,min=2,max=128"`
	Type             string `form:"type" json:"type" binding:"required,oneof=LOCAL_SERVER OSS_ALIYUN S3_MINIO COS_TENCENT OBS_HUAWEI"`
	Host             string `form:"host" json:"host" binding:"omitempty,max=255"`
	Port             int    `form:"port" json:"port" binding:"omitempty,min=1,max=65535"`
	BasePath         string `form:"base_path" json:"base_path" binding:"omitempty,max=1024"`
	SSHUser          string `form:"ssh_user" json:"ssh_user" binding:"omitempty,max=128"`
	SSHPassword      string `form:"ssh_password" json:"ssh_password" binding:"omitempty,max=255"`
	SSHPrivateKey    string `form:"ssh_private_key" json:"ssh_private_key"`
	S3Endpoint       string `form:"s3_endpoint" json:"s3_endpoint" binding:"omitempty,max=512"`
	S3Region         string `form:"s3_region" json:"s3_region" binding:"omitempty,max=128"`
	S3Bucket         string `form:"s3_bucket" json:"s3_bucket" binding:"omitempty,max=255"`
	S3AccessKey      string `form:"s3_access_key" json:"s3_access_key" binding:"omitempty,max=255"`
	S3SecretKey      string `form:"s3_secret_key" json:"s3_secret_key" binding:"omitempty,max=512"`
	S3ForcePathStyle *bool  `form:"s3_force_path_style" json:"s3_force_path_style"`
	Remark           string `form:"remark" json:"remark"`
}

type PrecheckStorageTargetForm struct {
	TargetID      uint64 `form:"target_id" json:"target_id"`
	Type          string `form:"type" json:"type" binding:"omitempty,oneof=LOCAL_SERVER OSS_ALIYUN S3_MINIO COS_TENCENT OBS_HUAWEI"`
	Host          string `form:"host" json:"host" binding:"omitempty,max=255"`
	Port          int    `form:"port" json:"port" binding:"omitempty,min=1,max=65535"`
	BasePath      string `form:"base_path" json:"base_path" binding:"omitempty,max=1024"`
	SSHUser       string `form:"ssh_user" json:"ssh_user" binding:"omitempty,max=128"`
	SSHPassword   string `form:"ssh_password" json:"ssh_password" binding:"omitempty,max=255"`
	SSHPrivateKey string `form:"ssh_private_key" json:"ssh_private_key"`
	S3Endpoint    string `form:"s3_endpoint" json:"s3_endpoint" binding:"omitempty,max=512"`
	S3Region      string `form:"s3_region" json:"s3_region" binding:"omitempty,max=128"`
	S3Bucket      string `form:"s3_bucket" json:"s3_bucket" binding:"omitempty,max=255"`
	S3AccessKey   string `form:"s3_access_key" json:"s3_access_key" binding:"omitempty,max=255"`
	S3SecretKey   string `form:"s3_secret_key" json:"s3_secret_key" binding:"omitempty,max=512"`
}

type ConfirmFingerprintForm struct {
	Fingerprint          string `form:"fingerprint" json:"fingerprint" binding:"required,max=512"`
	FingerprintConfirmed *bool  `form:"fingerprint_confirmed" json:"fingerprint_confirmed" binding:"required"`
}

type CreateBackupTaskForm struct {
	TaskID           string         `form:"task_id" json:"task_id" binding:"omitempty,max=20"`
	Name             string         `form:"name" json:"name" binding:"required,min=2,max=128"`
	SourceInstanceID uint64         `form:"source_instance_id" json:"source_instance_id" binding:"required"`
	StorageTargetID  uint64         `form:"storage_target_id" json:"storage_target_id"`
	ExecutorNodeID   uint64         `form:"executor_node_id" json:"executor_node_id"`
	RepositoryNodeID uint64         `form:"repository_node_id" json:"repository_node_id"`
	BackupType       string         `form:"backup_type" json:"backup_type"`
	ObjectMode       string         `form:"object_mode" json:"object_mode" binding:"required,oneof=ALL CUSTOM"`
	ObjectSpec       map[string]any `form:"object_spec" json:"object_spec"`
	OutputDir        string         `form:"output_dir" json:"output_dir"`
        DriverType       string         `form:"driver_type" json:"driver_type" binding:"omitempty,oneof=LOGICAL PG_BACKREST XTRABACKUP"`
	DriverConfig     map[string]any `form:"driver_config" json:"driver_config"`
	ScheduleDays     []string       `form:"schedule_days" json:"schedule_days"`
	ScheduleTime     string         `form:"schedule_time" json:"schedule_time"`
	RetentionDays    int            `form:"retention_days" json:"retention_days"`
	Enabled          *bool          `form:"enabled" json:"enabled"`
}

type PrecheckBackupTaskForm struct {
	TaskID           string         `form:"task_id" json:"task_id" binding:"omitempty,max=20"`
	Name             string         `form:"name" json:"name" binding:"required,min=2,max=128"`
	SourceInstanceID uint64         `form:"source_instance_id" json:"source_instance_id" binding:"required"`
	StorageTargetID  uint64         `form:"storage_target_id" json:"storage_target_id"`
	ExecutorNodeID   uint64         `form:"executor_node_id" json:"executor_node_id"`
	RepositoryNodeID uint64         `form:"repository_node_id" json:"repository_node_id"`
	BackupType       string         `form:"backup_type" json:"backup_type"`
	ObjectMode       string         `form:"object_mode" json:"object_mode" binding:"required,oneof=ALL CUSTOM"`
	ObjectSpec       map[string]any `form:"object_spec" json:"object_spec"`
	OutputDir        string         `form:"output_dir" json:"output_dir"`
        DriverType       string         `form:"driver_type" json:"driver_type" binding:"omitempty,oneof=LOGICAL PG_BACKREST XTRABACKUP"`
	DriverConfig     map[string]any `form:"driver_config" json:"driver_config"`
	ScheduleDays     []string       `form:"schedule_days" json:"schedule_days"`
	ScheduleTime     string         `form:"schedule_time" json:"schedule_time"`
	RetentionDays    int            `form:"retention_days" json:"retention_days"`
	Enabled          *bool          `form:"enabled" json:"enabled"`
}

type UpdateBackupTaskForm struct {
	Name             string         `form:"name" json:"name" binding:"required,min=2,max=128"`
	SourceInstanceID uint64         `form:"source_instance_id" json:"source_instance_id" binding:"required"`
	StorageTargetID  uint64         `form:"storage_target_id" json:"storage_target_id"`
	ExecutorNodeID   uint64         `form:"executor_node_id" json:"executor_node_id"`
	RepositoryNodeID uint64         `form:"repository_node_id" json:"repository_node_id"`
	BackupType       string         `form:"backup_type" json:"backup_type"`
	ObjectMode       string         `form:"object_mode" json:"object_mode" binding:"required,oneof=ALL CUSTOM"`
	ObjectSpec       map[string]any `form:"object_spec" json:"object_spec"`
	OutputDir        string         `form:"output_dir" json:"output_dir"`
        DriverType       string         `form:"driver_type" json:"driver_type" binding:"omitempty,oneof=LOGICAL PG_BACKREST XTRABACKUP"`
	DriverConfig     map[string]any `form:"driver_config" json:"driver_config"`
	ScheduleDays     []string       `form:"schedule_days" json:"schedule_days"`
	ScheduleTime     string         `form:"schedule_time" json:"schedule_time"`
	RetentionDays    int            `form:"retention_days" json:"retention_days"`
	Enabled          *bool          `form:"enabled" json:"enabled"`
	// ConfirmRetentionReconcile 表示用户已确认「保存时间改短后会立即清理超期备份」。
	// 未确认时，服务层会在写库前拦截并返回需要确认的影响明细。
	ConfirmRetentionReconcile bool `form:"confirm_retention_reconcile" json:"confirm_retention_reconcile"`
}

type DeleteBackupTaskForm struct {
	ConfirmName string `form:"confirm_name" json:"confirm_name" binding:"required,min=1,max=128"`
}

// UpdateBackupTaskNameForm 仅用于「轻量改名」，不触发驱动/节点/目录等副作用校验。
type UpdateBackupTaskNameForm struct {
	Name string `form:"name" json:"name" binding:"required,min=2,max=64"`
}

type CreateBackupNodeForm struct {
	Name          string `form:"name" json:"name" binding:"required,min=2,max=128"`
	Host          string `form:"host" json:"host" binding:"required,min=2,max=255"`
	Port          int    `form:"port" json:"port" binding:"required,min=1,max=65535"`
	BaseDir       string `form:"base_dir" json:"base_dir" binding:"required,min=1,max=1024"`
	SSHUser       string `form:"ssh_user" json:"ssh_user" binding:"omitempty,max=128"`
	SSHPassword   string `form:"ssh_password" json:"ssh_password" binding:"omitempty,max=255"`
	SSHPrivateKey string `form:"ssh_private_key" json:"ssh_private_key"`
	ServerURL     string `form:"server_url" json:"server_url" binding:"omitempty,max=255"`
	InstallDir    string `form:"install_dir" json:"install_dir" binding:"omitempty,max=1024"`
	ToolsDir      string `form:"tools_dir" json:"tools_dir" binding:"omitempty,max=1024"`
	ServiceName   string `form:"service_name" json:"service_name" binding:"omitempty,max=128"`
	LogPath       string `form:"log_path" json:"log_path" binding:"omitempty,max=1024"`
	Remark        string `form:"remark" json:"remark"`
}

type UpdateBackupNodeForm struct {
	Name          string `form:"name" json:"name" binding:"required,min=2,max=128"`
	Host          string `form:"host" json:"host" binding:"required,min=2,max=255"`
	Port          int    `form:"port" json:"port" binding:"required,min=1,max=65535"`
	BaseDir       string `form:"base_dir" json:"base_dir" binding:"required,min=1,max=1024"`
	SSHUser       string `form:"ssh_user" json:"ssh_user" binding:"omitempty,max=128"`
	SSHPassword   string `form:"ssh_password" json:"ssh_password" binding:"omitempty,max=255"`
	SSHPrivateKey string `form:"ssh_private_key" json:"ssh_private_key"`
	ServerURL     string `form:"server_url" json:"server_url" binding:"omitempty,max=255"`
	InstallDir    string `form:"install_dir" json:"install_dir" binding:"omitempty,max=1024"`
	ToolsDir      string `form:"tools_dir" json:"tools_dir" binding:"omitempty,max=1024"`
	ServiceName   string `form:"service_name" json:"service_name" binding:"omitempty,max=128"`
	LogPath       string `form:"log_path" json:"log_path" binding:"omitempty,max=1024"`
	Remark        string `form:"remark" json:"remark"`
}

type OperateBackupNodeForm struct {
	Action        string `form:"action" json:"action" binding:"required,oneof=connect_test install_agent repair_agent restart_agent stop_agent"`
	SSHUser       string `form:"ssh_user" json:"ssh_user" binding:"omitempty,max=128"`
	SSHPassword   string `form:"ssh_password" json:"ssh_password" binding:"omitempty,max=255"`
	SSHPrivateKey string `form:"ssh_private_key" json:"ssh_private_key"`
	ServerURL     string `form:"server_url" json:"server_url" binding:"omitempty,max=255"`
	InstallDir    string `form:"install_dir" json:"install_dir" binding:"omitempty,max=1024"`
	ToolsDir      string `form:"tools_dir" json:"tools_dir" binding:"omitempty,max=1024"`
	ServiceName   string `form:"service_name" json:"service_name" binding:"omitempty,max=128"`
	LogPath       string `form:"log_path" json:"log_path" binding:"omitempty,max=1024"`
	WaitOnlineSec int    `form:"wait_online_sec" json:"wait_online_sec" binding:"omitempty,min=0,max=60"`
}

type GetBackupSetsForm struct {
	PaginationQ    pagination.Pagination
	Search         string `form:"search" json:"search"`
	DBType         string `form:"db_type" json:"db_type"`
	InstanceID     uint64 `form:"instance_id" json:"instance_id"`
	RestorableOnly bool   `form:"restorable_only" json:"restorable_only"`
}

type BackupSetIDForm struct {
	ID uint64 `uri:"id" binding:"required"`
}

// BatchDeleteBackupSetsForm 批量删除备份集入参
type BatchDeleteBackupSetsForm struct {
	IDs []uint64 `json:"ids" binding:"required,min=1,max=200,dive,gt=0"`
}

type GetRestoreRunsForm struct {
	PaginationQ      pagination.Pagination
	Search           string `form:"search" json:"search"`
	DBType           string `form:"db_type" json:"db_type"`
	TargetInstanceID uint64 `form:"target_instance_id" json:"target_instance_id"`
	Status           string `form:"status" json:"status"`
	StartAtFrom      string `form:"start_at_from" json:"start_at_from"`
	StartAtTo        string `form:"start_at_to" json:"start_at_to"`
}

// RestoreDBMapping 恢复向导「配置映射」步骤的一条映射：源库 -> 目标库。
// 未提供映射时回落到 target_db_name / target_db_suffix 的隐式规则（向后兼容）。
type RestoreDBMapping struct {
	SourceDB string `form:"source_db" json:"source_db"`
	TargetDB string `form:"target_db" json:"target_db"`
}

type CreateRestoreRunForm struct {
	Name                    string   `form:"name" json:"name" binding:"omitempty,max=128"`
	BackupSetID             uint64   `form:"backup_set_id" json:"backup_set_id" binding:"required"`
	TargetInstanceID        uint64   `form:"target_instance_id" json:"target_instance_id" binding:"required"`
	TargetDBName            string   `form:"target_db_name" json:"target_db_name" binding:"max=255"`
	TargetDBSuffix          string   `form:"target_db_suffix" json:"target_db_suffix" binding:"max=255"`
	Mode                    string   `form:"mode" json:"mode" binding:"omitempty,oneof=plan execute PLAN EXECUTE"`
	RestoreMode             string   `form:"restore_mode" json:"restore_mode" binding:"omitempty,oneof=new overwrite append NEW OVERWRITE APPEND"`
	SelectedDatabases       []string `form:"selected_databases" json:"selected_databases"`
	SelectedSchemas         []string `form:"selected_schemas" json:"selected_schemas"`
	SelectedTables          []string `form:"selected_tables" json:"selected_tables"`
	Parallel                int      `form:"parallel" json:"parallel" binding:"omitempty,min=1,max=32"`
	Timeout                 string   `form:"timeout" json:"timeout" binding:"omitempty,max=64"`
	TargetCharset           string   `form:"target_charset" json:"target_charset" binding:"omitempty,max=64"`
	TargetCollation         string   `form:"target_collation" json:"target_collation" binding:"omitempty,max=128"`
	TargetEncoding          string   `form:"target_encoding" json:"target_encoding" binding:"omitempty,max=64"`
	CloseBinlog             *bool    `form:"close_binlog" json:"close_binlog"`
	ForeignKeyChecks        *bool    `form:"foreign_key_checks" json:"foreign_key_checks"`
	ExitOnError             *bool    `form:"exit_on_error" json:"exit_on_error"`
	NoOwner                 *bool    `form:"no_owner" json:"no_owner"`
	NoPrivileges            *bool    `form:"no_privileges" json:"no_privileges"`
	CleanIfExists           *bool    `form:"clean_if_exists" json:"clean_if_exists"`
	IsCloud                 *bool    `form:"is_cloud" json:"is_cloud"`
	ExecuteConfirmed        *bool    `form:"execute_confirmed" json:"execute_confirmed"`
	// —— 新版恢复向导字段（均可选，缺省时向后兼容旧行为）——
	ObjectScope          string             `form:"object_scope" json:"object_scope" binding:"omitempty,oneof=ALL CUSTOM all custom"`
	DBMappings           []RestoreDBMapping `form:"db_mappings" json:"db_mappings"`
	SchemaConflictPolicy string             `form:"schema_conflict_policy" json:"schema_conflict_policy" binding:"omitempty,oneof=SKIP ABORT skip abort"`
	DataConflictPolicy   string             `form:"data_conflict_policy" json:"data_conflict_policy" binding:"omitempty,oneof=SKIP ABORT TRUNCATE skip abort truncate"`
	RestoreSchema        *bool              `form:"restore_schema" json:"restore_schema"`
	RestoreData          *bool              `form:"restore_data" json:"restore_data"`
}

type UpdateRestoreRunForm struct {
	Name                    string   `form:"name" json:"name" binding:"omitempty,max=128"`
	BackupSetID             uint64   `form:"backup_set_id" json:"backup_set_id" binding:"required"`
	TargetInstanceID        uint64   `form:"target_instance_id" json:"target_instance_id" binding:"required"`
	TargetDBName            string   `form:"target_db_name" json:"target_db_name" binding:"max=255"`
	TargetDBSuffix          string   `form:"target_db_suffix" json:"target_db_suffix" binding:"max=255"`
	Mode                    string   `form:"mode" json:"mode" binding:"omitempty,oneof=plan execute PLAN EXECUTE"`
	RestoreMode             string   `form:"restore_mode" json:"restore_mode" binding:"omitempty,oneof=new overwrite append NEW OVERWRITE APPEND"`
	SelectedDatabases       []string `form:"selected_databases" json:"selected_databases"`
	SelectedSchemas         []string `form:"selected_schemas" json:"selected_schemas"`
	SelectedTables          []string `form:"selected_tables" json:"selected_tables"`
	Parallel                int      `form:"parallel" json:"parallel" binding:"omitempty,min=1,max=32"`
	Timeout                 string   `form:"timeout" json:"timeout" binding:"omitempty,max=64"`
	TargetCharset           string   `form:"target_charset" json:"target_charset" binding:"omitempty,max=64"`
	TargetCollation         string   `form:"target_collation" json:"target_collation" binding:"omitempty,max=128"`
	TargetEncoding          string   `form:"target_encoding" json:"target_encoding" binding:"omitempty,max=64"`
	CloseBinlog             *bool    `form:"close_binlog" json:"close_binlog"`
	ForeignKeyChecks        *bool    `form:"foreign_key_checks" json:"foreign_key_checks"`
	ExitOnError             *bool    `form:"exit_on_error" json:"exit_on_error"`
	NoOwner                 *bool    `form:"no_owner" json:"no_owner"`
	NoPrivileges            *bool    `form:"no_privileges" json:"no_privileges"`
	CleanIfExists           *bool    `form:"clean_if_exists" json:"clean_if_exists"`
	IsCloud                 *bool    `form:"is_cloud" json:"is_cloud"`
	ExecuteConfirmed        *bool    `form:"execute_confirmed" json:"execute_confirmed"`
	// —— 新版恢复向导字段（均可选，缺省时向后兼容旧行为）——
	ObjectScope          string             `form:"object_scope" json:"object_scope" binding:"omitempty,oneof=ALL CUSTOM all custom"`
	DBMappings           []RestoreDBMapping `form:"db_mappings" json:"db_mappings"`
	SchemaConflictPolicy string             `form:"schema_conflict_policy" json:"schema_conflict_policy" binding:"omitempty,oneof=SKIP ABORT skip abort"`
	DataConflictPolicy   string             `form:"data_conflict_policy" json:"data_conflict_policy" binding:"omitempty,oneof=SKIP ABORT TRUNCATE skip abort truncate"`
	RestoreSchema        *bool              `form:"restore_schema" json:"restore_schema"`
	RestoreData          *bool              `form:"restore_data" json:"restore_data"`
}

// PrecheckRestoreRunForm 恢复向导「恢复预检查」步骤的「无副作用预检」请求体。
// 与 CreateRestoreRunForm 的区别：
//  1. 不落 insight_restore_runs（向导不再用 create(plan)+delete 制造幽灵任务）；
//  2. 逻辑分支强制按 PLAN 模式计算，因此不校验 execute_confirmed；
//  3. 额外承载物理恢复字段（target_node_id / target_data_dir / target_port），
//     由后端按备份集驱动类型分流，前端始终只发一份 payload。
type PrecheckRestoreRunForm struct {
	BackupSetID      uint64   `form:"backup_set_id" json:"backup_set_id" binding:"required"`
	TargetInstanceID uint64   `form:"target_instance_id" json:"target_instance_id"`
	TargetDBName     string   `form:"target_db_name" json:"target_db_name" binding:"max=255"`
	TargetDBSuffix   string   `form:"target_db_suffix" json:"target_db_suffix" binding:"max=255"`
	RestoreMode      string   `form:"restore_mode" json:"restore_mode" binding:"omitempty,max=32"`
	SelectedDatabases []string `form:"selected_databases" json:"selected_databases"`
	SelectedSchemas   []string `form:"selected_schemas" json:"selected_schemas"`
	SelectedTables    []string `form:"selected_tables" json:"selected_tables"`
	CloseBinlog      *bool    `form:"close_binlog" json:"close_binlog"`
	CleanIfExists    *bool    `form:"clean_if_exists" json:"clean_if_exists"`
	ForeignKeyChecks *bool    `form:"foreign_key_checks" json:"foreign_key_checks"`
	ExitOnError      *bool    `form:"exit_on_error" json:"exit_on_error"`
	// —— 新版恢复向导策略字段 ——
	ObjectScope          string             `form:"object_scope" json:"object_scope" binding:"omitempty,oneof=ALL CUSTOM all custom"`
	DBMappings           []RestoreDBMapping `form:"db_mappings" json:"db_mappings"`
	SchemaConflictPolicy string             `form:"schema_conflict_policy" json:"schema_conflict_policy" binding:"omitempty,oneof=SKIP ABORT skip abort"`
	DataConflictPolicy   string             `form:"data_conflict_policy" json:"data_conflict_policy" binding:"omitempty,oneof=SKIP ABORT TRUNCATE skip abort truncate"`
	RestoreSchema        *bool              `form:"restore_schema" json:"restore_schema"`
	RestoreData          *bool              `form:"restore_data" json:"restore_data"`
	// —— 物理恢复字段（逻辑备份集上这些字段被忽略）——
	TargetNodeID  uint64 `form:"target_node_id" json:"target_node_id"`
	TargetDataDir string `form:"target_data_dir" json:"target_data_dir" binding:"omitempty,min=1,max=1024"`
	TargetPort    int    `form:"target_port" json:"target_port" binding:"omitempty,min=1,max=65535"`
	AutoStart     *bool  `form:"auto_start" json:"auto_start"`
}

// GetBackupSetObjectsForm 枚举备份集内部可恢复对象（库级）。
type GetBackupSetObjectsForm struct {
	ID uint64 `uri:"id" binding:"required"`
}

// UpdateRestoreRunNameForm 仅用于恢复任务「轻量改名」，逻辑/物理恢复共用同一张表，
// 不触发备份集校验、目标实例解析等完整 Update 服务的副作用。
type UpdateRestoreRunNameForm struct {
	Name string `form:"name" json:"name" binding:"required,min=2,max=64"`
}

type RestoreRunIDForm struct {
	ID uint64 `uri:"id" binding:"required"`
}

type GetRestoreRunLogForm struct {
	ItemID uint64 `form:"item_id" json:"item_id"`
	TailKB int64  `form:"tail_kb" json:"tail_kb"`
}

type GetBackupObjectsForm struct {
	InstanceID uint64 `form:"instance_id" json:"instance_id" binding:"required"`
	Level      string `form:"level" json:"level" binding:"required,oneof=databases schemas tables"`
	Database   string `form:"database" json:"database"`
	Schema     string `form:"schema" json:"schema"`
}

type AgentHeartbeatForm struct {
	NodeID       uint64 `form:"node_id" json:"node_id" binding:"required"`
	Token        string `form:"token" json:"token" binding:"required"`
	AgentVersion string `form:"agent_version" json:"agent_version"`
}

type AgentPollJobForm struct {
	NodeID       uint64 `form:"node_id" json:"node_id" binding:"required"`
	Token        string `form:"token" json:"token" binding:"required"`
	AgentVersion string `form:"agent_version" json:"agent_version"`
}

type AgentReportRunForm struct {
	NodeID              uint64 `form:"node_id" json:"node_id" binding:"required"`
	Token               string `form:"token" json:"token" binding:"required"`
	RunID               uint64 `form:"run_id" json:"run_id" binding:"required"`
	Status              string `form:"status" json:"status" binding:"required,oneof=RUNNING SUCCESS FAILED STOPPED"`
	StorageTargetID     uint64 `form:"storage_target_id" json:"storage_target_id"`
	StorageArtifactPath string `form:"storage_artifact_path" json:"storage_artifact_path"`
	LogPath             string `form:"log_path" json:"log_path"`
	LogExcerpt          string `form:"log_excerpt" json:"log_excerpt"`
	ErrorMessage        string `form:"error_message" json:"error_message"`
	FileName            string `form:"file_name" json:"file_name"`
	FilePath            string `form:"file_path" json:"file_path"`
	SizeBytes           int64  `form:"size_bytes" json:"size_bytes"`
	Sha256              string `form:"sha256" json:"sha256"`
	ServerVersion       string `form:"server_version" json:"server_version"`
	Tool                string `form:"tool" json:"tool"`
	ToolImage           string `form:"tool_image" json:"tool_image"`
	// 备份集验证状态由执行节点（agent）在备份完成时回读附属文件校验得出，
	// 随 SUCCESS 上报回主服务落库，恢复预检据此直接判定，不再现场 find 文件。
	MetadataVerified bool `form:"metadata_verified" json:"metadata_verified"`
	ChecksumVerified bool `form:"checksum_verified" json:"checksum_verified"`
}

type AgentRunStopStatusForm struct {
	NodeID       uint64 `form:"node_id" json:"node_id" binding:"required"`
	Token        string `form:"token" json:"token" binding:"required"`
	RunID        uint64 `form:"run_id" json:"run_id" binding:"required"`
	AgentVersion string `form:"agent_version" json:"agent_version"`
}
