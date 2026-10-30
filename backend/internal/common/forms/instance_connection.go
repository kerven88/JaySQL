package forms

import "github.com/simajiong/jaysql/internal/common/models"

type AdminTestInstanceConnectionForm struct {
	ID            uint64          `json:"id"`
	ClearPassword bool            `json:"clear_password"`
	Hostname      string          `form:"hostname"  json:"hostname" binding:"required,min=2,max=128"`
	Port          int             `form:"port"  json:"port" binding:"required"`
	User          string          `form:"user"  json:"user" binding:"required_unless=DbType Redis,omitempty,min=1,max=128"`
	Password      string          `form:"password"  json:"password" binding:"required_unless=DbType Redis,omitempty,min=1,max=256"`
	DbType        models.EnumType `form:"db_type"  json:"db_type" binding:"required,oneof=MySQL TiDB Doris ClickHouse PostgreSQL MongoDB Oracle SQLServer OceanBase Redis"`
	ExtraConfig   map[string]any  `form:"extra_config" json:"extra_config"`
}
