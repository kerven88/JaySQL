package models

import (
	"github.com/simajiong/jaysql/pkg/utils"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// 工单环境
type InsightInstanceEnvironments struct {
	*Model
	Name          string `gorm:"type:varchar(32);not null;default:'';comment:环境名;uniqueIndex:uniq_name" json:"name"`
	Color         string `gorm:"type:varchar(16);not null;default:'#1677ff';comment:环境颜色" json:"color"`
	EnvType       string `gorm:"type:varchar(32);not null;default:'';comment:环境类型 dev/prod" json:"env_type"`
	SQLStandardID uint64 `gorm:"not null;default:0;comment:环境级SQL规范ID" json:"sql_standard_id"`
	CreatedBy     string `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightInstanceEnvironments) TableName() string {
	return "insight_instance_environments"
}

// 工单DB配置
type InsightInstances struct {
	*Model
	InstanceID string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_instance_id;comment:数据源业务ID" json:"instance_id"`
	Hostname   string `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uniq_hostname;comment:主机名" json:"hostname"`
	Port       int    `gorm:"type:int;not null;default 3306;uniqueIndex:uniq_hostname;comment:端口" json:"port"`
	User       string `gorm:"type:varchar(128);not null;default:'';comment:用户名" json:"user"`
	// password 不参与 JSON 序列化：这是数据库密码的密文，任何接口都不应返回它。
	// 前端判断"是否已配置"用 *_configured 类布尔字段；编辑时留空即表示不修改。
	// 256 Unicode characters can exceed 1 KiB after AES-GCM/Base64; keep the full ciphertext.
	Password         string         `gorm:"type:varchar(2048);not null;default:'';comment:密码(加密存储)" json:"-"`
	UseType          EnumType       `gorm:"type:ENUM('查询', '工单');default:工单;uniqueIndex:uniq_hostname;comment:用途" json:"use_type"`
	DbType           EnumType       `gorm:"type:varchar(32);not null;default:'MySQL';comment:数据库类型" json:"db_type"`
	ExtraConfig      datatypes.JSON `gorm:"type:json;null;default:null;comment:扩展连接参数" json:"extra_config"`
	Environment      int            `gorm:"type:int;null;default:null;comment:环境;index" json:"environment"`
	OrganizationKey  string         `gorm:"type:varchar(256);not null;index:organization_key;comment:搜索路径" json:"organization_key"`
	OrganizationPath datatypes.JSON `gorm:"type:json;null;default:null;comment:绝对路径" json:"organization_path"`
	CreatedBy        string         `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
	LastCheckedAt    LocalTime      `gorm:"index:idx_instance_last_checked_at;comment:最近连通性检测时间" json:"last_checked_at"`
	LastCheckStatus  string         `gorm:"type:varchar(32);not null;default:'unknown';index:idx_instance_last_check_status;comment:最近连通状态" json:"last_check_status"`
	LastCheckMessage string         `gorm:"type:text;comment:最近连通检测结果" json:"last_check_message"`
	Remark           string         `gorm:"type:varchar(256);not null;default:'';comment:备注" json:"remark"`

	// ---- 实例画像：监控明细页「实例概览」18 字段的数据来源 ----
	// 这些列全部可空且默认空串/0：采集侧拿不到就保持空值，
	// 服务层统一转成 available=false + reason，前端显示 "—" 并给出原因，绝不静默缺数。
	DbVersion      string  `gorm:"type:varchar(64);not null;default:'';comment:数据库版本，如 8.0.34" json:"db_version"`
	OsName         string  `gorm:"type:varchar(64);not null;default:'';comment:操作系统，如 CentOS 7.9" json:"os_name"`
	OsKernel       string  `gorm:"type:varchar(128);not null;default:'';comment:内核版本，如 3.10.0-1160.el7" json:"os_kernel"`
	DeployProvider string  `gorm:"type:varchar(32);not null;default:'';comment:部署形态 self_built=自建 / cloud=云" json:"deploy_provider"`
	CloudVendor    string  `gorm:"type:varchar(64);not null;default:'';comment:云厂商，自建为空" json:"cloud_vendor"`
	InstanceRole   string  `gorm:"type:varchar(32);not null;default:'';comment:架构角色 master/slave/standalone" json:"instance_role"`
	HaMode         string  `gorm:"type:varchar(64);not null;default:'';comment:高可用模式，如 MHA / MGR / 无" json:"ha_mode"`
	CpuCores       int     `gorm:"type:int;not null;default:0;comment:CPU 核数（负载与活跃会话的分母）" json:"cpu_cores"`
	MemoryGB       float64 `gorm:"type:decimal(12,2);not null;default:0;comment:内存容量 GB" json:"memory_gb"`
	DiskGB         float64 `gorm:"type:decimal(12,2);not null;default:0;comment:磁盘容量 GB" json:"disk_gb"`
	DiskType       string  `gorm:"type:varchar(32);not null;default:'';comment:磁盘类型，如 ESSD PL1 / SSD / HDD" json:"disk_type"`
	UptimeSeconds  int64   `gorm:"type:bigint;not null;default:0;comment:数据库运行时长（秒），由引擎采集回写" json:"uptime_seconds"`
	ProbeLatencyMs int     `gorm:"type:int;not null;default:0;comment:最近一次 TCP 探测耗时毫秒" json:"probe_latency_ms"`
	// ProfileFactsAt 画像静态事实最近一次采集时间。
	// 版本 / 规格 / OS 这些"几乎不变"的事实不必跟着 10 秒一轮的采集反复查，
	// 采集侧据此节流（见 monitor_profile_collect.go）；为空表示从未采集过，立即补采。
	ProfileFactsAt *LocalTime `gorm:"type:datetime;null;comment:实例画像静态事实最近一次采集时间" json:"profile_facts_at"`
}

func (InsightInstances) TableName() string {
	return "insight_instances"
}

func (u *InsightInstances) BeforeCreate(tx *gorm.DB) (err error) {
	if u.InstanceID == "" {
		u.InstanceID = utils.GenerateInstanceID()
	}
	return
}

// 自动采集和存储InsightInstances配置实例的库
type InsightInstanceSchemas struct {
	*Model
	InstanceID   string `gorm:"type:char(15);not null;default:'';comment:关联insight_instances的instance_id;uniqueIndex:uniq_schema;index:idx_instance_id" json:"instance_id"`
	Schema       string `gorm:"type:varchar(255);not null;default:'';comment:资源路径;uniqueIndex:uniq_schema" json:"schema"`
	ParentPath   string `gorm:"type:varchar(255);not null;default:'';comment:父级资源路径;index:idx_parent_path" json:"parent_path"`
	ResourceType string `gorm:"type:varchar(32);not null;default:'database';comment:资源类型;index:idx_resource_type" json:"resource_type"`
	DisplayName  string `gorm:"type:varchar(255);not null;default:'';comment:显示名称" json:"display_name"`
	IsDeleted    bool   `gorm:"type:boolean;not null;default:false;comment:是否删除;uniqueIndex:uniq_schema" json:"is_deleted"`
}

func (InsightInstanceSchemas) TableName() string {
	return "insight_instance_schemas"
}

type InsightRiskDismissed struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement;comment:自增主键" json:"id"`
	CreatedAt LocalTime      `gorm:"index:idx_created_at;autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt LocalTime      `gorm:"index:idx_updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index:idx_deleted_at;comment:软删除时间" json:"-"`
	RiskType  string         `gorm:"type:varchar(64);not null;default:'';comment:风险类型(instance_conn/manual_backup_fail等);uniqueIndex:uniq_risk_type_key,priority:1" json:"risk_type"`
	RiskKey   string         `gorm:"type:varchar(512);not null;default:'';comment:风险唯一键;uniqueIndex:uniq_risk_type_key,priority:2" json:"risk_key"`
	CreatedBy string         `gorm:"type:varchar(64);not null;default:'';index:idx_created_by;comment:关闭人username" json:"created_by"`
	UpdatedBy string         `gorm:"type:varchar(64);not null;default:'';comment:更新人username" json:"updated_by"`
	Remark    string         `gorm:"type:varchar(256);not null;default:'';comment:备注" json:"remark"`
}

func (InsightRiskDismissed) TableName() string {
	return "insight_risk_dismissed"
}
