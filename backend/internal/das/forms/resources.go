package forms

type GetResourcesForm struct {
	InstanceID   string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	ParentPath   string `form:"parent_path" json:"parent_path"`
	ResourceType string `form:"resource_type" json:"resource_type" binding:"required"`
}

type GetObjectsFlatIndexForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	MaxRows    int    `form:"max_rows" json:"max_rows" binding:"omitempty,min=100,max=100000"`
}

type SearchObjectsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Query      string `form:"q" json:"q" binding:"required,min=2,max=256"`
	Limit      int    `form:"limit" json:"limit" binding:"omitempty,min=1,max=500"`
}
