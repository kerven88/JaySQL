package models

import (
	"github.com/simajiong/jaysql/internal/common/models"
)

// 用户的库权限
type InsightDasSchemaPerms struct {
	*models.Model
	PrincipalType string `gorm:"type:varchar(16);not null;default:'user';comment:授权主体类型;uniqueIndex:uniq_schema_principal;index:idx_schema_principal" json:"principal_type"`
	PrincipalID   uint64 `gorm:"type:bigint;not null;default:0;comment:授权主体ID;uniqueIndex:uniq_schema_principal;index:idx_schema_principal" json:"principal_id"`
	PrincipalName string `gorm:"type:varchar(128);not null;default:'';comment:授权主体名称快照" json:"principal_name"`
	Username      string `gorm:"type:varchar(128);not null;default:'';comment:兼容主体标识;uniqueIndex:uniq_schema" json:"username"`
	Schema        string `gorm:"type:varchar(255);not null;default:'';comment:资源路径;uniqueIndex:uniq_schema;uniqueIndex:uniq_schema_principal" json:"schema"`
	ResourceType  string `gorm:"type:varchar(32);not null;default:'database';comment:资源类型" json:"resource_type"`
	InstanceID    string `gorm:"type:char(15);not null;default:'';comment:关联das_config的instance_id;uniqueIndex:uniq_schema;uniqueIndex:uniq_schema_principal;index:idx_instance_id" json:"instance_id"`
}

func (InsightDasSchemaPerms) TableName() string {
	return "insight_das_schema_perms"
}

// 用户的表权限
type InsightDasTablePerms struct {
	*models.Model
	PrincipalType string          `gorm:"type:varchar(16);not null;default:'user';comment:授权主体类型;uniqueIndex:uniq_table_principal;index:idx_table_principal" json:"principal_type"`
	PrincipalID   uint64          `gorm:"type:bigint;not null;default:0;comment:授权主体ID;uniqueIndex:uniq_table_principal;index:idx_table_principal" json:"principal_id"`
	PrincipalName string          `gorm:"type:varchar(128);not null;default:'';comment:授权主体名称快照" json:"principal_name"`
	Username      string          `gorm:"type:varchar(128);not null;default:'';comment:兼容主体标识;uniqueIndex:uniq_table" json:"username"`
	Schema        string          `gorm:"type:varchar(255);not null;default:'';comment:资源路径;uniqueIndex:uniq_table;uniqueIndex:uniq_table_principal" json:"schema"`
	ResourceType  string          `gorm:"type:varchar(32);not null;default:'database';comment:资源类型" json:"resource_type"`
	Table         string          `gorm:"type:varchar(128);not null;default:'';comment:表名;uniqueIndex:uniq_table;uniqueIndex:uniq_table_principal" json:"table"`
	InstanceID    string          `gorm:"type:char(15);not null;default:'';comment:关联insight_instances的instance_id;uniqueIndex:uniq_table;uniqueIndex:uniq_table_principal;index:idx_instance_id" json:"instance_id"`
	Rule          models.EnumType `gorm:"type:ENUM('allow', 'deny');default:allow;comment:规则" json:"rule"`
}

func (InsightDasTablePerms) TableName() string {
	return "insight_das_table_perms"
}

// 允许用户执行的操作
type InsightDASOperations struct {
	*models.Model
	Name     string `gorm:"type:varchar(128);not null;comment:语句类型;uniqueIndex:uniq_name" json:"name"`
	IsEnable bool   `gorm:"type:boolean;null;default:False;comment:是否启用,0未启用,1启用" json:"is_finish"`
	Remark   string `gorm:"type:varchar(1024);not null;default:'';comment:备注" json:"remark"`
}

func (InsightDASOperations) TableName() string {
	return "insight_das_operations"
}
