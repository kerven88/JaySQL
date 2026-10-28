package forms

type GetTablesForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema" binding:"required"`
}

type GetTableInfoForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema" binding:"required"`
	Table      string `form:"table" json:"table" binding:"required"`
	Type       string `form:"type" json:"type" binding:"required,oneof=structure base"`
	// ObjectType 对象类型：table（缺省）/ view / materialized-view / procedure / function / trigger / event。
	// 非表对象用于对象树右键「查看X信息」，与表共用同一接口。
	ObjectType string `form:"object_type" json:"object_type" binding:"omitempty,oneof=table view materialized-view procedure function trigger event"`
}
