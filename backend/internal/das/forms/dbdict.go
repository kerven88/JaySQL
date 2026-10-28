package forms

type GetDbDictForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema" binding:"required"`
}
