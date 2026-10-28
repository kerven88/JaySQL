package models

import (
	"encoding/json"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// SQL 规范
type InsightSQLStandard struct {
	*commonModels.Model
	Name        string `gorm:"type:varchar(128);not null;comment:规范名称" json:"name"`
	Description string `gorm:"type:varchar(512);not null;default:'';comment:规范描述" json:"description"`
	IsDefault   bool   `gorm:"not null;default:false;comment:是否系统默认规范" json:"is_default"`
	EnvType     string `gorm:"type:varchar(32);not null;default:'custom';comment:环境类型 dev/prod/custom" json:"env_type"`
	Enabled     bool   `gorm:"not null;default:true;comment:是否启用" json:"enabled"`
	SortOrder   int    `gorm:"not null;default:0;comment:排序" json:"sort_order"`
	CreatedBy   string `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightSQLStandard) TableName() string {
	return "insight_sql_standards"
}

// SQL 规范下的规则
type InsightSQLStandardRule struct {
	*commonModels.Model
	StandardID  uint64          `gorm:"not null;index:idx_standard_id;comment:所属规范ID" json:"standard_id"`
	RuleKey     string          `gorm:"type:varchar(128);not null;comment:规则唯一标识，如 mysql.ct.require_pk" json:"rule_key"`
	Category    string          `gorm:"type:varchar(32);not null;comment:结构 structure 或 查询更新 query_update" json:"category"`
	RuleType    string          `gorm:"type:varchar(32);not null;default:'';comment:规则类型(表/列/索引/更新/写入/删除/查询等)" json:"rule_type"`
	DbType      string          `gorm:"type:varchar(32);not null;comment:mysql/postgres/all" json:"db_type"`
	Name        string          `gorm:"type:varchar(256);not null;comment:规则名称" json:"name"`
	Description string          `gorm:"type:text;not null;comment:规则描述" json:"description"`
	BaseLevel   string          `gorm:"type:enum('must','suggest');not null;default:'suggest';comment:规则基础级别(必须改进/建议改进)" json:"base_level"`
	Level       string          `gorm:"type:enum('must','suggest','off');not null;default:'off';comment:必须改进/建议改进/关闭" json:"level"`
	Params      json.RawMessage `gorm:"type:json;comment:规则参数" json:"params"`
	SortOrder   int             `gorm:"not null;default:0;comment:排序" json:"sort_order"`
	Implemented bool            `gorm:"not null;default:false;comment:该规则是否已在引擎中实现" json:"implemented"`
}

func (InsightSQLStandardRule) TableName() string {
	return "insight_sql_standard_rules"
}

// 实例与 SQL 规范的绑定关系，每个实例有且只能绑定一个规范
type InsightInstanceSQLStandardBinding struct {
	*commonModels.Model
	InstanceID string `gorm:"type:char(15);not null;uniqueIndex:uniq_instance_id;comment:实例ID" json:"instance_id"`
	StandardID uint64 `gorm:"not null;index:idx_standard_id;comment:规范ID" json:"standard_id"`
}

func (InsightInstanceSQLStandardBinding) TableName() string {
	return "insight_instance_sql_standard_bindings"
}
