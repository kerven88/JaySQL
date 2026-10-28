package forms

// SchemaBrowserForm SQL 工作台「对象树」的懒加载取数表单。
//
// 与 GetTablesForm 保持同一套资源路径约定：
//   - MySQL 系 / ClickHouse / Oracle：schema 传库名本身；
//   - PostgreSQL / SQLServer：schema 传 "库名.模式名" 路径（服务层负责拆分）。
type SchemaBrowserForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema" binding:"required,max=128"`
	// scope 取值：
	//   database-groups 某库下的固定分组节点（Tables/Views/Procedures/Triggers/Events/Functions）
	//   group           某个分组下的对象清单（group 参数指定分组）
	//   table-groups    某张表下的固定分组节点（Columns/Indexes/Triggers）
	//   table-group     表级某个分组下的条目清单（group 参数指定分组）
	Scope string `form:"scope" json:"scope" binding:"required,oneof=database-groups group table-groups table-group"`
	// group 取值：库级 tables/views/procedures/triggers/events/functions；
	// 表级 columns/indexes/table-triggers
	Group string `form:"group" json:"group" binding:"omitempty,max=32"`
	// table 仅在 table-groups / table-group 两个 scope 下必填
	Table string `form:"table" json:"table" binding:"omitempty,max=128"`
}
