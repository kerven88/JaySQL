package models

type InsightSystemParam struct {
	*Model
	Title       string `gorm:"type:varchar(128);not null;default:'';comment:参数显示名" json:"title"`
	ConfigKey   string `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uniq_system_params_config_key;comment:参数唯一键" json:"config_key"`
	ConfigValue string `gorm:"type:text;not null;comment:参数值(JSON或基本类型字符串)" json:"config_value"`
	ConfigType  string `gorm:"type:varchar(16);not null;default:'string';comment:参数类型:boolean/string/number/array/object" json:"config_type"`
	Description string `gorm:"type:varchar(512);not null;default:'';comment:参数描述" json:"description"`
}

func (InsightSystemParam) TableName() string {
	return "insight_system_params"
}
