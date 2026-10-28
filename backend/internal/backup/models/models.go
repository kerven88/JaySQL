package models

import (
	"time"

	"github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type InsightBackupTasks struct {
	ID                   uint64            `gorm:"primaryKey;autoIncrement;comment:自增主键" json:"id"`
	CreatedAt            models.LocalTime  `gorm:"index:idx_created_at;autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt            models.LocalTime  `gorm:"index:idx_updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
	DeletedAt            gorm.DeletedAt    `gorm:"index:idx_deleted_at;comment:逻辑删除时间" json:"-"`
	TaskID               string            `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_backup_task_id;comment:备份任务业务ID" json:"task_id"`
	Name                 string            `gorm:"type:varchar(128);not null;default:'';comment:任务名称;index:idx_name" json:"name"`
	DBType               models.EnumType   `gorm:"type:varchar(32);not null;default:'MySQL';comment:DB类型;index:idx_db_type" json:"db_type"`
	SourceInstanceID     uint64            `gorm:"type:bigint;not null;default:0;comment:源实例ID;index:idx_source_instance_id" json:"source_instance_id"`
	ExecutorNodeID       uint64            `gorm:"type:bigint;not null;default:0;comment:执行节点ID;index:idx_executor_node_id" json:"executor_node_id"`
	RepositoryNodeID     uint64            `gorm:"type:bigint;not null;default:0;comment:仓库节点ID;index:idx_repository_node_id" json:"repository_node_id"`
	StorageTargetID      uint64            `gorm:"type:bigint;not null;default:0;comment:存储目标ID;index:idx_storage_target_id" json:"storage_target_id"`
	SourceDBName         string            `gorm:"type:varchar(255);not null;default:'';comment:兼容字段(单库备份);index:idx_source_db_name" json:"source_db_name"`
	BackupType           models.EnumType   `gorm:"type:varchar(16);not null;default:'FULL';comment:备份类型;index:idx_backup_type" json:"backup_type"`
	ObjectMode           models.EnumType   `gorm:"type:ENUM('ALL','CUSTOM');not null;default:'CUSTOM';comment:备份对象模式;index:idx_object_mode" json:"object_mode"`
	ObjectSpec           datatypes.JSON    `gorm:"type:json;null;default:null;comment:备份对象定义" json:"object_spec"`
	ObjectSummary        string            `gorm:"type:varchar(1024);not null;default:'';comment:备份对象摘要" json:"object_summary"`
	OutputDir            string            `gorm:"type:varchar(1024);not null;default:'';comment:备份目录(本机绝对路径或节点相对目录)" json:"output_dir"`
	DriverType           models.EnumType   `gorm:"type:varchar(32);not null;default:'LOGICAL';comment:备份驱动;index:idx_driver_type" json:"driver_type"`
	DriverConfig         datatypes.JSON    `gorm:"type:json;null;default:null;comment:驱动配置" json:"driver_config"`
	StorageBackend       models.EnumType   `gorm:"type:ENUM('LOCAL','OSS','MINIO','STORAGE_TARGET_REF');not null;default:'LOCAL';comment:存储后端;index:idx_storage_backend" json:"storage_backend"`
	StorageConfig        datatypes.JSON    `gorm:"type:json;null;default:null;comment:存储配置" json:"storage_config"`
	ScheduleDays         datatypes.JSON    `gorm:"type:json;null;default:null;comment:调度星期定义" json:"schedule_days"`
	ScheduleTime         string            `gorm:"type:varchar(5);not null;default:'';comment:调度时间(HH:MM)" json:"schedule_time"`
	RetentionDays        int               `gorm:"type:int;not null;default:30;comment:保留天数" json:"retention_days"`
	Enabled              bool              `gorm:"type:boolean;not null;default:true;comment:是否启用;index:idx_enabled" json:"enabled"`
	PrepareStatus        models.EnumType   `gorm:"type:ENUM('NONE','PREPARING','PREPARE_OK','PREPARE_ERROR');not null;default:'NONE';comment:任务级 prepare 状态" json:"prepare_status"`
	PrepareStage         string            `gorm:"type:varchar(64);not null;default:'';comment:当前 prepare 阶段英文 code" json:"prepare_stage"`
	PrepareStartedAt     *models.LocalTime `gorm:"comment:prepare 开始时间" json:"prepare_started_at"`
	PrepareFinishedAt    *models.LocalTime `gorm:"comment:prepare 结束时间" json:"prepare_finished_at"`
	PrepareTitle         string            `gorm:"type:varchar(512);not null;default:'';comment:prepare 失败中文标题" json:"prepare_title"`
	PrepareSuggestion    string            `gorm:"type:varchar(1024);not null;default:'';comment:prepare 失败修复建议" json:"prepare_suggestion"`
	PrepareDetail        string            `gorm:"type:text;null;default:null;comment:prepare 失败详细描述" json:"prepare_detail"`
	PrepareRawError      string            `gorm:"type:text;null;default:null;comment:prepare 原始错误（附堆栈/SSH 输出）" json:"prepare_raw_error"`
	DriverPrepareVersion int               `gorm:"type:int;not null;default:0;comment:prepare 驱动协议版本号，用于未来兼容" json:"driver_prepare_version"`
	LastPrecheckStatus   string            `gorm:"type:varchar(32);not null;default:'NONE';comment:最近一次预检查状态" json:"last_precheck_status"`
	LastPrecheckAt       models.LocalTime  `gorm:"comment:最近一次预检查时间" json:"last_precheck_at"`
	LastPrecheckResult   datatypes.JSON    `gorm:"type:json;null;default:null;comment:最近一次预检查结果" json:"last_precheck_result"`
	CreatedBy            string            `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
	UpdatedBy            string            `gorm:"type:varchar(64);not null;default:'';comment:更新人" json:"updated_by"`
}

func (InsightBackupTasks) TableName() string {
	return "insight_backup_tasks"
}

type InsightBackupRuns struct {
	*models.Model
	TaskID               string            `gorm:"type:char(15);not null;default:'';comment:备份任务业务ID;index:idx_task_id" json:"task_id"`
	SourceInstanceID     uint64            `gorm:"type:bigint;not null;default:0;comment:源实例ID（0=历史数据）;index:idx_run_source_instance_id" json:"source_instance_id"`
	RunSeq               uint64            `gorm:"type:bigint;not null;default:0;comment:该实例第几次备份（0=历史数据，沿用旧目录规则）" json:"run_seq"`
	Status               models.EnumType   `gorm:"type:ENUM('PENDING','RUNNING','STOPPING','STOPPED','FAILED','COMPLETED','TIMEOUT','PENDING_PREPARE','CANCELLED');not null;default:'PENDING';comment:执行状态;index:idx_status" json:"status"`
	StartAt              models.LocalTime  `gorm:"comment:开始时间" json:"start_at"`
	EndAt                models.LocalTime  `gorm:"comment:结束时间" json:"end_at"`
	ExecutorNodeID       uint64            `gorm:"type:bigint;not null;default:0;comment:执行节点ID;index:idx_executor_node_id" json:"executor_node_id"`
	RepositoryNodeID     uint64            `gorm:"type:bigint;not null;default:0;comment:仓库节点ID;index:idx_repository_node_id" json:"repository_node_id"`
	StorageTargetID      uint64            `gorm:"type:bigint;not null;default:0;comment:存储目标ID;index:idx_storage_target_id" json:"storage_target_id"`
	BackupType           models.EnumType   `gorm:"type:varchar(16);not null;default:'FULL';comment:备份类型;index:idx_backup_type" json:"backup_type"`
	ObjectMode           models.EnumType   `gorm:"type:ENUM('ALL','CUSTOM');not null;default:'CUSTOM';comment:备份对象模式;index:idx_object_mode" json:"object_mode"`
	ObjectSpec           datatypes.JSON    `gorm:"type:json;null;default:null;comment:备份对象定义" json:"object_spec"`
	ObjectSummary        string            `gorm:"type:varchar(1024);not null;default:'';comment:备份对象摘要" json:"object_summary"`
	OutputDir            string            `gorm:"type:varchar(1024);not null;default:'';comment:备份基准目录" json:"output_dir"`
	DriverType           models.EnumType   `gorm:"type:varchar(32);not null;default:'LOGICAL';comment:备份驱动;index:idx_driver_type" json:"driver_type"`
	DriverConfig         datatypes.JSON    `gorm:"type:json;null;default:null;comment:驱动配置" json:"driver_config"`
	StorageBackend       models.EnumType   `gorm:"type:ENUM('LOCAL','OSS','MINIO','STORAGE_TARGET_REF');not null;default:'LOCAL';comment:存储后端;index:idx_storage_backend" json:"storage_backend"`
	StorageConfig        datatypes.JSON    `gorm:"type:json;null;default:null;comment:存储配置" json:"storage_config"`
	RetentionDays        int               `gorm:"type:int;not null;default:30;comment:保留天数" json:"retention_days"`
	Tool                 string            `gorm:"type:varchar(64);not null;default:'';comment:工具" json:"tool"`
	ToolImage            string            `gorm:"type:varchar(64);not null;default:'';comment:Docker镜像" json:"tool_image"`
	ServerVersion        string            `gorm:"type:varchar(64);not null;default:'';comment:服务端版本" json:"server_version"`
	FileName             string            `gorm:"type:varchar(255);not null;default:'';comment:产物文件名" json:"file_name"`
	FilePath             string            `gorm:"type:varchar(1024);not null;default:'';comment:产物路径" json:"file_path"`
	SizeBytes            int64             `gorm:"type:bigint;not null;default:0;comment:文件大小" json:"size_bytes"`
	Sha256               string            `gorm:"type:varchar(128);not null;default:'';comment:sha256" json:"sha256"`
	LogPath              string            `gorm:"type:varchar(1024);not null;default:'';comment:日志路径" json:"log_path"`
	LogExcerpt           string            `gorm:"type:text;comment:日志摘要" json:"log_excerpt"`
	ErrorMessage         string            `gorm:"type:varchar(2048);not null;default:'';comment:错误信息" json:"error_message"`
	PrecheckSummary      datatypes.JSON    `gorm:"type:json;null;default:null;comment:运行前预检查结果摘要" json:"precheck_summary"`
	PrepareStatus        models.EnumType   `gorm:"type:ENUM('NONE','PREPARING','PREPARE_OK','PREPARE_ERROR');not null;default:'NONE';comment:run 级 prepare 状态" json:"prepare_status"`
	PrepareStage         string            `gorm:"type:varchar(64);not null;default:'';comment:当前 prepare 阶段英文 code" json:"prepare_stage"`
	PrepareStartedAt     *models.LocalTime `gorm:"comment:prepare 开始时间" json:"prepare_started_at"`
	PrepareFinishedAt    *models.LocalTime `gorm:"comment:prepare 结束时间" json:"prepare_finished_at"`
	PrepareTitle         string            `gorm:"type:varchar(512);not null;default:'';comment:prepare 失败中文标题" json:"prepare_title"`
	PrepareSuggestion    string            `gorm:"type:varchar(1024);not null;default:'';comment:prepare 失败修复建议" json:"prepare_suggestion"`
	PrepareDetail        string            `gorm:"type:text;null;default:null;comment:prepare 失败详细描述" json:"prepare_detail"`
	PrepareRawError      string            `gorm:"type:text;null;default:null;comment:prepare 原始错误（附堆栈/SSH 输出）" json:"prepare_raw_error"`
	DriverPrepareVersion int               `gorm:"type:int;not null;default:0;comment:prepare 驱动协议版本号，用于未来兼容" json:"driver_prepare_version"`
	CreatedBy            string            `gorm:"type:varchar(64);not null;default:'';comment:执行人" json:"created_by"`
	// TriggerSource 区分「手动立即备份」与「定时调度」两条执行路径。
	// MANUAL=用户点击立即备份（物理任务强制全量，不查基线）；SCHEDULED=时间计划触发（物理任务先查基线）。
	// 空值视为历史数据，按 SCHEDULED 语义兼容处理。
	TriggerSource string `gorm:"type:varchar(16);not null;default:'';comment:触发来源(MANUAL=手动立即备份,SCHEDULED=定时调度);index:idx_run_trigger_source" json:"trigger_source"`
	// BackupTypeReason 记录本次「为什么是全量 / 增量」的决策说明，成功与失败都会写。
	// 早期版本把这段文案塞进 error_message，导致成功的备份也带一段看似报错的文字，
	// 且会污染失败分析模块（它把 error_message 当作失败原因）。这里独立成列。
	BackupTypeReason string `gorm:"type:varchar(1024);not null;default:'';comment:备份类型决策说明（全量/增量判定依据）" json:"backup_type_reason"`
}

func (InsightBackupRuns) TableName() string {
	return "insight_backup_runs"
}

type InsightBackupSets struct {
	*models.Model
	BackupSetsID     string          `gorm:"type:varchar(20);not null;default:'';comment:备份集ID;uniqueIndex:uniq_backup_sets_id" json:"backup_sets_id"`
	RunID            uint64          `gorm:"type:bigint;not null;default:0;comment:RunID;uniqueIndex:uniq_run_id" json:"run_id"`
	TaskID           string          `gorm:"type:char(15);not null;default:'';comment:备份任务业务ID;index:idx_task_id" json:"task_id"`
	DBType           models.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';comment:DB类型;index:idx_db_type" json:"db_type"`
	SourceInstanceID uint64          `gorm:"type:bigint;not null;default:0;comment:源实例ID;index:idx_source_instance_id" json:"source_instance_id"`
	ExecutorNodeID   uint64          `gorm:"type:bigint;not null;default:0;comment:执行节点ID;index:idx_executor_node_id" json:"executor_node_id"`
	RepositoryNodeID uint64          `gorm:"type:bigint;not null;default:0;comment:仓库节点ID;index:idx_repository_node_id" json:"repository_node_id"`
	StorageTargetID  uint64          `gorm:"type:bigint;not null;default:0;comment:存储目标ID;index:idx_storage_target_id" json:"storage_target_id"`
	SourceDBName     string          `gorm:"type:varchar(255);not null;default:'';comment:兼容字段(单库备份);index:idx_source_db_name" json:"source_db_name"`
	BackupType       models.EnumType `gorm:"type:varchar(16);not null;default:'FULL';comment:备份类型;index:idx_backup_type" json:"backup_type"`
	ObjectMode       models.EnumType `gorm:"type:ENUM('ALL','CUSTOM');not null;default:'CUSTOM';comment:备份对象模式;index:idx_object_mode" json:"object_mode"`
	ObjectSpec       datatypes.JSON  `gorm:"type:json;null;default:null;comment:备份对象定义" json:"object_spec"`
	ObjectSummary    string          `gorm:"type:varchar(1024);not null;default:'';comment:备份对象摘要" json:"object_summary"`
	OutputDir        string          `gorm:"type:varchar(1024);not null;default:'';comment:备份基准目录" json:"output_dir"`
	DriverType       models.EnumType `gorm:"type:varchar(32);not null;default:'LOGICAL';comment:备份驱动;index:idx_driver_type" json:"driver_type"`
	DriverConfig     datatypes.JSON  `gorm:"type:json;null;default:null;comment:驱动配置" json:"driver_config"`
	StorageBackend   models.EnumType `gorm:"type:ENUM('LOCAL','OSS','MINIO','STORAGE_TARGET_REF');not null;default:'LOCAL';comment:存储后端;index:idx_storage_backend" json:"storage_backend"`
	StorageConfig    datatypes.JSON  `gorm:"type:json;null;default:null;comment:存储配置" json:"storage_config"`
	RetentionDays    int             `gorm:"type:int;not null;default:30;comment:保留天数" json:"retention_days"`
	ExpireAt         *time.Time      `gorm:"type:datetime;null;default:null;comment:过期时间;index:idx_expire_at" json:"expire_at"`
	Name             string          `gorm:"type:varchar(255);not null;default:'';comment:备份集名称;index:idx_name" json:"name"`
	FileName         string          `gorm:"type:varchar(255);not null;default:'';comment:文件名" json:"file_name"`
	FilePath         string          `gorm:"type:varchar(1024);not null;default:'';comment:文件路径" json:"file_path"`
	SizeBytes        int64           `gorm:"type:bigint;not null;default:0;comment:文件大小" json:"size_bytes"`
	Sha256           string          `gorm:"type:varchar(128);not null;default:'';comment:sha256" json:"sha256"`
	// 备份集验证状态：由备份完成时「写附属文件 + 立即回读校验」写入，恢复预检直接读字段，
	// 不再现场 find 文件（直传/跨节点场景下产物与附属文件往往不在同一处）。
	// VerifiedAt 为 NULL 表示该备份集从未执行过验证流程（本功能上线前的历史数据），
	// 恢复侧需回退到「现场查找附属文件」的旧逻辑，不能直接按未验证处理。
	MetadataVerified bool      `gorm:"type:tinyint(1);not null;default:0;comment:元数据已验证" json:"metadata_verified"`
	ChecksumVerified bool      `gorm:"type:tinyint(1);not null;default:0;comment:校验和已验证" json:"checksum_verified"`
	VerifiedAt       *time.Time `gorm:"type:datetime;null;default:null;comment:验证时间(NULL=历史数据未验证)" json:"verified_at"`
	ServerVersion    string          `gorm:"type:varchar(64);not null;default:'';comment:服务端版本" json:"server_version"`
	Tool             string          `gorm:"type:varchar(64);not null;default:'';comment:工具" json:"tool"`
	ToolImage        string          `gorm:"type:varchar(64);not null;default:'';comment:Docker镜像" json:"tool_image"`
	CreatedBy        string          `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightBackupSets) TableName() string {
	return "insight_backup_sets"
}

type InsightBackupNodes struct {
	*models.Model
	Name          string           `gorm:"type:varchar(128);not null;default:'';comment:节点名称;uniqueIndex:uniq_name" json:"name"`
	Host          string           `gorm:"type:varchar(255);not null;default:'';comment:节点地址" json:"host"`
	Port          int              `gorm:"type:int;not null;default:18083;comment:节点端口" json:"port"`
	BaseDir       string           `gorm:"type:varchar(1024);not null;default:'';comment:节点基准目录" json:"base_dir"`
	SSHUser       string           `gorm:"type:varchar(128);not null;default:'';comment:SSH用户名" json:"ssh_user"`
	SSHPassword   string           `gorm:"type:varchar(255);not null;default:'';comment:SSH密码" json:"-"`
	SSHPrivateKey string           `gorm:"type:text;comment:SSH私钥" json:"-"`
	ServerURL     string           `gorm:"type:varchar(255);not null;default:'';comment:Agent回连地址" json:"server_url"`
	InstallDir    string           `gorm:"type:varchar(1024);not null;default:'';comment:Agent安装目录" json:"install_dir"`
	ToolsDir      string           `gorm:"type:varchar(1024);not null;default:'';comment:工具目录" json:"tools_dir"`
	ServiceName   string           `gorm:"type:varchar(128);not null;default:'';comment:Agent服务名" json:"service_name"`
	LogPath       string           `gorm:"type:varchar(1024);not null;default:'';comment:Agent日志路径" json:"log_path"`
	Token         string           `gorm:"type:varchar(128);not null;default:'';comment:节点令牌" json:"token"`
	Status        models.EnumType  `gorm:"type:ENUM('ONLINE','OFFLINE');not null;default:'OFFLINE';comment:节点状态;index:idx_status" json:"status"`
	AgentVersion  string           `gorm:"type:varchar(64);not null;default:'';comment:agent版本" json:"agent_version"`
	LastHeartbeat models.LocalTime `gorm:"comment:最后心跳时间" json:"last_heartbeat"`
	Remark        string           `gorm:"type:varchar(255);not null;default:'';comment:备注" json:"remark"`
	CreatedBy     string           `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
	UpdatedBy     string           `gorm:"type:varchar(64);not null;default:'';comment:更新人" json:"updated_by"`
}

func (InsightBackupNodes) TableName() string {
	return "insight_backup_nodes"
}

const (
	StorageTargetTypeLocalServer = "LOCAL_SERVER"
	StorageTargetTypeOSSAliyun   = "OSS_ALIYUN"
	StorageTargetTypeS3Minio     = "S3_MINIO"
	StorageTargetTypeCOSTencent  = "COS_TENCENT"
	StorageTargetTypeOBSHuawei   = "OBS_HUAWEI"
)

const (
	StorageTargetConnStatusUnknown = "UNKNOWN"
	StorageTargetConnStatusOK      = "OK"
	StorageTargetConnStatusFailed  = "FAILED"
	StorageTargetConnStatusPending = "PENDING"
)

type InsightBackupStorageTargets struct {
	ID                      uint64           `gorm:"primaryKey;autoIncrement;comment:自增主键" json:"id"`
	CreatedAt               models.LocalTime `gorm:"index:idx_created_at;autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt               models.LocalTime `gorm:"index:idx_updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
	DeletedAt               gorm.DeletedAt   `gorm:"index:idx_deleted_at;comment:逻辑删除时间" json:"-"`
	Name                    string           `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uniq_storage_target_name;comment:存储目标名称" json:"name"`
	Type                    models.EnumType  `gorm:"type:ENUM('LOCAL_SERVER','OSS_ALIYUN','S3_MINIO','COS_TENCENT','OBS_HUAWEI');not null;default:'LOCAL_SERVER';comment:存储目标类型;index:idx_type" json:"type"`
	Host                    string           `gorm:"type:varchar(255);not null;default:'';comment:主机地址(LOCAL_SERVER)" json:"host"`
	Port                    int              `gorm:"type:int;not null;default:22;comment:端口(LOCAL_SERVER=SSH端口)" json:"port"`
	BasePath                string           `gorm:"type:varchar(1024);not null;default:'';comment:基准路径或Bucket根路径" json:"base_path"`
	SSHUser                 string           `gorm:"type:varchar(128);not null;default:'';comment:SSH用户名(LOCAL_SERVER)" json:"ssh_user"`
	SSHPassword             string           `gorm:"type:varchar(512);not null;default:'';comment:SSH密码(加密存储)" json:"-"`
	SSHPrivateKey           string           `gorm:"type:text;comment:SSH私钥(加密存储)" json:"-"`
	SSHPasswordConfigured   bool             `gorm:"-" json:"ssh_password_configured"`
	SSHPrivateKeyConfigured bool             `gorm:"-" json:"ssh_private_key_configured"`
	SSHHostKeyFingerprint   string           `gorm:"type:varchar(512);not null;default:'';comment:SSH主机指纹(已确认)" json:"ssh_host_key_fingerprint"`
	FingerprintConfirmed    bool             `gorm:"type:boolean;not null;default:false;comment:指纹是否已确认" json:"fingerprint_confirmed"`
	S3Endpoint              string           `gorm:"type:varchar(512);not null;default:'';comment:S3兼容Endpoint" json:"s3_endpoint"`
	S3Region                string           `gorm:"type:varchar(128);not null;default:'';comment:Region" json:"s3_region"`
	S3Bucket                string           `gorm:"type:varchar(255);not null;default:'';comment:Bucket名称" json:"s3_bucket"`
	S3AccessKey             string           `gorm:"type:varchar(255);not null;default:'';comment:Access Key" json:"-"`
	S3SecretKey             string           `gorm:"type:varchar(512);not null;default:'';comment:Secret Key(加密)" json:"-"`
	S3ForcePathStyle        bool             `gorm:"type:boolean;not null;default:false;comment:force-path-style" json:"s3_force_path_style"`
	S3AccessKeyConfigured   bool             `gorm:"-" json:"s3_access_key_configured"`
	S3SecretKeyConfigured   bool             `gorm:"-" json:"s3_secret_key_configured"`
	ConnectionStatus        models.EnumType  `gorm:"type:ENUM('UNKNOWN','OK','FAILED','PENDING');not null;default:'UNKNOWN';comment:连接状态;index:idx_connection_status" json:"connection_status"`
	LastPrecheckAt          models.LocalTime `gorm:"comment:最近预检查时间" json:"last_precheck_at"`
	LastPrecheckResult      datatypes.JSON   `gorm:"type:json;null;default:null;comment:最近预检查结果" json:"last_precheck_result"`
	Remark                  string           `gorm:"type:varchar(255);not null;default:'';comment:备注" json:"remark"`
	CreatedBy               string           `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
	UpdatedBy               string           `gorm:"type:varchar(64);not null;default:'';comment:更新人" json:"updated_by"`
}

func (InsightBackupStorageTargets) TableName() string {
	return "insight_backup_storage_targets"
}

type InsightRestoreRuns struct {
	*models.Model
	TaskID               string           `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_restore_task_id;comment:恢复任务业务编号" json:"task_id"`
	Name                 string           `gorm:"type:varchar(128);not null;default:'';comment:任务名称;index:idx_name" json:"name"`
	BackupSetID          uint64           `gorm:"type:bigint;not null;default:0;comment:备份集ID;index:idx_backup_set_id" json:"backup_set_id"`
	SourceInstanceID     uint64           `gorm:"type:bigint;not null;default:0;comment:源实例ID;index:idx_source_instance_id" json:"source_instance_id"`
	DBType               models.EnumType  `gorm:"type:varchar(32);not null;default:'MySQL';comment:DB类型;index:idx_db_type" json:"db_type"`
	RestoreKind          models.EnumType  `gorm:"type:varchar(16);not null;default:'LOGICAL';comment:恢复类型;index:idx_restore_kind" json:"restore_kind"`
	DriverType           models.EnumType  `gorm:"type:varchar(32);not null;default:'LOGICAL';comment:恢复驱动;index:idx_driver_type" json:"driver_type"`
	RestoreMode          models.EnumType  `gorm:"type:varchar(32);not null;default:'';comment:恢复模式;index:idx_restore_mode" json:"restore_mode"`
	TargetLevel          models.EnumType  `gorm:"type:varchar(32);not null;default:'DATABASE';comment:目标层级;index:idx_target_level" json:"target_level"`
	TargetInstanceID     uint64           `gorm:"type:bigint;not null;default:0;comment:目标实例ID;index:idx_target_instance_id" json:"target_instance_id"`
	TargetNodeID         uint64           `gorm:"type:bigint;not null;default:0;comment:目标节点ID;index:idx_target_node_id" json:"target_node_id"`
	TargetName           string           `gorm:"type:varchar(255);not null;default:'';comment:目标对象名;index:idx_target_name" json:"target_name"`
	TargetDBName         string           `gorm:"type:varchar(255);not null;default:'';comment:目标库名(兼容字段);index:idx_target_db_name" json:"target_db_name"`
	TargetHost           string           `gorm:"type:varchar(255);not null;default:'';comment:目标主机" json:"target_host"`
	TargetPort           int              `gorm:"type:int;not null;default:0;comment:目标端口" json:"target_port"`
	DriverConfigSnapshot datatypes.JSON   `gorm:"type:json;null;default:null;comment:驱动配置快照" json:"driver_config_snapshot"`
	ResolvedContext      datatypes.JSON   `gorm:"type:json;null;default:null;comment:执行上下文快照" json:"resolved_context"`
	Options              datatypes.JSON   `gorm:"type:json;null;default:null;comment:任务选项" json:"options"`
	Status               models.EnumType  `gorm:"type:varchar(32);not null;default:'PENDING';comment:执行状态;index:idx_status" json:"status"`
	StartAt              models.LocalTime `gorm:"comment:开始时间" json:"start_at"`
	EndAt                models.LocalTime `gorm:"comment:结束时间" json:"end_at"`
	CurrentStage         string           `gorm:"type:varchar(64);not null;default:'';comment:当前阶段" json:"current_stage"`
	ProgressPercent      int              `gorm:"type:int;not null;default:0;comment:进度百分比" json:"progress_percent"`
	CurrentObject        string           `gorm:"type:varchar(255);not null;default:'';comment:当前对象" json:"current_object"`
	CurrentAction        string           `gorm:"type:varchar(255);not null;default:'';comment:当前动作" json:"current_action"`
	RiskLevel            string           `gorm:"type:varchar(16);not null;default:'';comment:风险等级" json:"risk_level"`
	PrecheckSummary      datatypes.JSON   `gorm:"type:json;null;default:null;comment:预检查结果摘要" json:"precheck_summary"`
	ResultSummary        datatypes.JSON   `gorm:"type:json;null;default:null;comment:恢复结果摘要" json:"result_summary"`
	LastReportAt         models.LocalTime `gorm:"comment:最近上报时间" json:"last_report_at"`
	LogPath              string           `gorm:"type:varchar(1024);not null;default:'';comment:日志路径" json:"log_path"`
	LogExcerpt           string           `gorm:"type:text;comment:日志摘要" json:"log_excerpt"`
	ErrorMessage         string           `gorm:"type:varchar(2048);not null;default:'';comment:错误信息" json:"error_message"`
	CreatedBy            string           `gorm:"type:varchar(64);not null;default:'';comment:执行人" json:"created_by"`
}

func (InsightRestoreRuns) TableName() string {
	return "insight_restore_runs"
}

type InsightRestoreRunItems struct {
	*models.Model
	RestoreRunID     uint64           `gorm:"type:bigint;not null;default:0;comment:恢复任务ID;index:idx_restore_run_id" json:"restore_run_id"`
	SourceObjectName string           `gorm:"type:varchar(255);not null;default:'';comment:来源对象名" json:"source_object_name"`
	TargetObjectName string           `gorm:"type:varchar(255);not null;default:'';comment:目标对象名" json:"target_object_name"`
	Status           string           `gorm:"type:varchar(32);not null;default:'PENDING';comment:执行状态;index:idx_status" json:"status"`
	CurrentStage     string           `gorm:"type:varchar(64);not null;default:'';comment:当前阶段" json:"current_stage"`
	ProgressPercent  int              `gorm:"type:int;not null;default:0;comment:进度百分比" json:"progress_percent"`
	DisplayOrder     int              `gorm:"type:int;not null;default:0;comment:显示顺序" json:"display_order"`
	StartAt          models.LocalTime `gorm:"comment:开始时间" json:"start_at"`
	EndAt            models.LocalTime `gorm:"comment:结束时间" json:"end_at"`
	LastReportAt     models.LocalTime `gorm:"comment:最近上报时间" json:"last_report_at"`
	LogPath          string           `gorm:"type:varchar(1024);not null;default:'';comment:日志路径" json:"log_path"`
	LogExcerpt       string           `gorm:"type:text;comment:日志摘要" json:"log_excerpt"`
	ErrorMessage     string           `gorm:"type:varchar(2048);not null;default:'';comment:错误信息" json:"error_message"`
}

func (InsightRestoreRunItems) TableName() string {
	return "insight_restore_run_items"
}
