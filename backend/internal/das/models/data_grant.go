package models

import (
	"github.com/simajiong/jaysql/internal/common/models"
)

// ---------------------------------------------------------------------------
// 细粒度数据授权
//
// 设计目标：废除「管理员角色默认可见全部数据」的隐式授权，改为
// 「主体 + 数据资源 + 操作类型」三维显式授权，未授权即不可见。
//
// 授权记录本身即审计证据：谁授的（grantor_*）、授给谁（principal_*）、
// 授了什么资源（resource_level + instance/schema/table）、能做什么（actions）、
// 什么时候开始生效（effective_at）、什么时候失效（expire_at）、
// 当前处于什么状态（status）。任何变更另写一条 insight_das_grant_audit 流水。
// ---------------------------------------------------------------------------

// InsightDasDataGrant 单条数据授权记录
type InsightDasDataGrant struct {
	*models.Model
	GrantNo string `gorm:"type:varchar(32);not null;default:'';comment:授权单号;uniqueIndex:uniq_grant_no" json:"grant_no"`
	// 被授权主体：user / role
	PrincipalType string `gorm:"type:varchar(16);not null;default:'user';comment:被授权主体类型;index:idx_grant_principal" json:"principal_type"`
	PrincipalID   uint64 `gorm:"type:bigint;not null;default:0;comment:被授权主体ID;index:idx_grant_principal" json:"principal_id"`
	PrincipalName string `gorm:"type:varchar(128);not null;default:'';comment:被授权主体名称快照" json:"principal_name"`
	// 授权资源：instance / database / table 三级
	ResourceLevel string `gorm:"type:varchar(16);not null;default:'instance';comment:资源层级:instance/database/table;index:idx_grant_resource" json:"resource_level"`
	InstanceID    string `gorm:"type:char(15);not null;default:'';comment:数据源实例ID;index:idx_grant_resource" json:"instance_id"`
	SchemaName    string `gorm:"type:varchar(255);not null;default:'';comment:库/模式名;index:idx_grant_resource" json:"schema_name"`
	// 注意：字段名不能叫 TableName——会和 gorm 要求的 TableName() 方法重名导致编译失败，
	// 这里用 Table + column 显式映射到列 table_name。
	Table string `gorm:"column:table_name;type:varchar(128);not null;default:'';comment:表名;index:idx_grant_resource" json:"table_name"`
	// TableMode 表级授权的圈选方式：exact 精确表名（默认）/ wildcard 通配符（table_name 存
	// 「* 任意串、? 单字符」模式）/ except 排除模式（table_name 存逗号分隔的排除表清单，
	// 语义 = 该库/模式下除清单外的全部表）。库级 / 模式级 / 实例级恒为 exact。
	TableMode string `gorm:"type:varchar(16);not null;default:'exact';comment:表圈选方式:exact/wildcard/except" json:"table_mode"`
	// RequestNo 来源申请单号（多条授权可来自同一张多资源申请单）。
	// 旧数据为空，回退按申请单的 grant_no 关联。
	RequestNo string `gorm:"type:char(15);not null;default:'';comment:来源申请单号;index:idx_grant_request" json:"request_no"`
	ResourceType string `gorm:"type:varchar(32);not null;default:'database';comment:资源类型:database/schema" json:"resource_type"`
	// 操作类型：逗号分隔的 select / export / dml
	Actions string `gorm:"type:varchar(255);not null;default:'select';comment:允许的操作类型,逗号分隔" json:"actions"`
	// 效力：allow 放行 / deny 拒绝（deny 优先级高于 allow）
	Effect string `gorm:"type:varchar(16);not null;default:'allow';comment:效力:allow/deny" json:"effect"`
	// 时间窗口：effective_at <= now < expire_at（expire_at 为空表示长期有效）
	EffectiveAt *models.LocalTime `gorm:"comment:生效时间" json:"effective_at"`
	ExpireAt    *models.LocalTime `gorm:"comment:失效时间,为空表示长期有效;index:idx_grant_expire" json:"expire_at"`
	// 授权人
	GrantorUsername string `gorm:"type:varchar(64);not null;default:'';comment:授权人账号" json:"grantor_username"`
	GrantorName     string `gorm:"type:varchar(128);not null;default:'';comment:授权人名称快照" json:"grantor_name"`
	// 来源：manual 后台授权 / self 自助授权 / migrate 存量迁移 / system 系统内置
	GrantSource string `gorm:"type:varchar(32);not null;default:'manual';comment:授权来源:manual/self/migrate/system" json:"grant_source"`
	// 状态：active 生效中 / revoked 已撤销 / expired 已过期
	Status    string            `gorm:"type:varchar(16);not null;default:'active';comment:状态:active/revoked/expired;index:idx_grant_status" json:"status"`
	Reason    string            `gorm:"type:varchar(512);not null;default:'';comment:授权事由" json:"reason"`
	RevokedAt *models.LocalTime `gorm:"comment:撤销时间" json:"revoked_at"`
	RevokedBy string            `gorm:"type:varchar(64);not null;default:'';comment:撤销人账号" json:"revoked_by"`
}

func (InsightDasDataGrant) TableName() string {
	return "insight_das_data_grants"
}

// InsightDasGrantAudit 授权变更审计流水。
//
// 授权记录本身只保存「当前状态」，历史上每一次创建 / 撤销 / 续期 / 过期都在这里
// 留一份前后快照，供合规审计追溯：谁在什么时候、把什么权限、授给了谁、又撤给了谁。
type InsightDasGrantAudit struct {
	*models.Model
	GrantNo          string `gorm:"type:varchar(32);not null;default:'';comment:授权单号;index:idx_audit_grant_no" json:"grant_no"`
	GrantID          uint64 `gorm:"type:bigint;not null;default:0;comment:授权记录ID;index:idx_audit_grant_id" json:"grant_id"`
	Action           string `gorm:"type:varchar(32);not null;default:'';comment:动作:create/revoke/renew/expire" json:"action"`
	OperatorUsername string `gorm:"type:varchar(64);not null;default:'';comment:操作人账号;index:idx_audit_operator" json:"operator_username"`
	OperatorName     string `gorm:"type:varchar(128);not null;default:'';comment:操作人名称快照" json:"operator_name"`
	// 主体与资源快照：即便授权记录被物理删除，流水仍可还原「授过什么」
	PrincipalType string `gorm:"type:varchar(16);not null;default:'';comment:被授权主体类型" json:"principal_type"`
	PrincipalID   uint64 `gorm:"type:bigint;not null;default:0;comment:被授权主体ID" json:"principal_id"`
	PrincipalName string `gorm:"type:varchar(128);not null;default:'';comment:被授权主体名称快照" json:"principal_name"`
	ResourceDesc  string `gorm:"type:varchar(512);not null;default:'';comment:授权资源描述" json:"resource_desc"`
	Actions       string `gorm:"type:varchar(255);not null;default:'';comment:操作类型" json:"actions"`
	// 前后快照：JSON 文本
	BeforeSnapshot string `gorm:"type:text;comment:变更前快照JSON" json:"before_snapshot"`
	AfterSnapshot  string `gorm:"type:text;comment:变更后快照JSON" json:"after_snapshot"`
	Reason         string `gorm:"type:varchar(512);not null;default:'';comment:操作事由" json:"reason"`
	RequestID      string `gorm:"type:varchar(128);not null;default:'';comment:请求ID,串联同一次操作" json:"request_id"`
	SourceIP       string `gorm:"type:varchar(64);not null;default:'';comment:来源IP" json:"source_ip"`
}

func (InsightDasGrantAudit) TableName() string {
	return "insight_das_grant_audit"
}
