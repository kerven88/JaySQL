package forms

type AdminValidateCloudAccountForm struct {
	Vendor   string         `form:"vendor" json:"vendor" binding:"required,oneof=aliyun huawei tencent"`
	AkId     string         `form:"ak_id" json:"ak_id" binding:"required,min=1,max=128"`
	AkSecret string         `form:"ak_secret" json:"ak_secret" binding:"required,min=1,max=256"`
	Region   string         `form:"region" json:"region" binding:"omitempty,max=64"`
	ExtraCfg map[string]any `form:"extra_config" json:"extra_config"`
}

type AdminDiscoverCloudInstancesForm struct {
	Vendor      string         `form:"vendor" json:"vendor" binding:"required,oneof=aliyun huawei tencent"`
	AkId        string         `form:"ak_id" json:"ak_id" binding:"required,min=1,max=128"`
	AkSecret    string         `form:"ak_secret" json:"ak_secret" binding:"required,min=1,max=256"`
	Regions     []string       `form:"regions" json:"regions" binding:"required,min=1,dive,max=64"`
	OnlyRunning bool           `form:"only_running" json:"only_running"`
	ExtraCfg    map[string]any `form:"extra_config" json:"extra_config"`
}

type CloudInstanceToImport struct {
	CloudInstanceId string `json:"cloud_instance_id" binding:"required,max=128"`
	CloudRegion     string `json:"cloud_region" binding:"required,max=64"`
	Hostname        string `json:"hostname" binding:"required,min=2,max=128"`
	Port            int    `json:"port" binding:"required"`
	DbType          string `json:"db_type" binding:"required,oneof=MySQL TiDB Doris ClickHouse PostgreSQL MongoDB Oracle SQLServer"`
	EngineVersion   string `json:"engine_version"`
	DisplayName     string `json:"display_name" binding:"required,min=2,max=256"`
	OverrideUser    string `json:"override_user" binding:"omitempty,max=128"`
	OverridePwd     string `json:"override_pwd" binding:"omitempty,max=256"`
}

type AdminImportCloudInstancesForm struct {
	Vendor          string                 `form:"vendor" json:"vendor" binding:"required,oneof=aliyun huawei tencent"`
	SavedAccountId  string                 `form:"saved_account_id" json:"saved_account_id" binding:"omitempty,min=3,max=15"`
	AkId            string                 `form:"ak_id" json:"ak_id" binding:"omitempty,min=1,max=128"`
	AkSecret        string                 `form:"ak_secret" json:"ak_secret" binding:"omitempty,min=1,max=256"`
	Environment     int                    `form:"environment" json:"environment" binding:"required"`
	DefaultUser     string                 `form:"default_user" json:"default_user" binding:"required,min=1,max=128"`
	DefaultPwd      string                 `form:"default_pwd" json:"default_pwd" binding:"required,min=1,max=256"`
	DuplicatePolicy string                 `form:"duplicate_policy" json:"duplicate_policy" binding:"required,oneof=skip update abort"`
	ExtraConfig     map[string]any         `form:"extra_config" json:"extra_config"`
	Instances       []CloudInstanceToImport `form:"instances" json:"instances" binding:"required,min=1,dive"`
}
