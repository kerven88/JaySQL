package forms

type ExecuteMySQLQueryForm struct {
	InstanceID string            `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string            `form:"schema" json:"schema"`
	Params     map[string]string `form:"params" json:"params"`
	SQLText    string            `form:"sqltext" json:"sqltext" binding:"required"`
}

type ExecuteClickHouseQueryForm struct {
	InstanceID string                 `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string                 `form:"schema" json:"schema"`
	Params     map[string]interface{} `form:"params" json:"params"`
	SQLText    string                 `form:"sqltext" json:"sqltext" binding:"required"`
}

type ExecutePostgresQueryForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema"`
	SQLText    string `form:"sqltext" json:"sqltext" binding:"required"`
}

type ExecuteSQLServerQueryForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema"`
	SQLText    string `form:"sqltext" json:"sqltext" binding:"required"`
}

type ExecuteOracleQueryForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema"`
	SQLText    string `form:"sqltext" json:"sqltext" binding:"required"`
}
