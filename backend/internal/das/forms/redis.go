package forms

type RedisScanForm struct {
	InstanceID string `json:"instance_id" binding:"required,max=64"`
	DB         int    `json:"db" binding:"gte=0,lte=65535"`
	Pattern    string `json:"pattern" binding:"max=512"`
	Cursor     string `json:"cursor" binding:"max=20"`
	Count      int    `json:"count" binding:"gte=0,lte=200"`
}
type RedisValueForm struct {
	InstanceID string  `json:"instance_id" binding:"required,max=64"`
	DB         int     `json:"db" binding:"gte=0,lte=65535"`
	KeyBase64  *string `json:"key_base64" binding:"required,max=87384"`
	Cursor     string  `json:"cursor" binding:"max=20"`
	Offset     int64   `json:"offset" binding:"gte=0,lte=536870912"`
	Count      int     `json:"count" binding:"gte=0,lte=200"`
}
