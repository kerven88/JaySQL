package forms

import "github.com/simajiong/jaysql/pkg/pagination"

// ---------------------------------------------------------------------------
// 数据访问权限申请
//
// 申请人直接表达「要访问哪个数据源的哪个库/表、做什么操作」，
// 提交后进入审批流，审批通过即写入授权表。资源与操作由申请人勾选、
// 服务端做白名单校验（层级/操作类型只认枚举值），审批人是最后一道防线。
// ---------------------------------------------------------------------------

// AccessRequestListForm 申请单列表查询
type AccessRequestListForm struct {
	PaginationQ pagination.Pagination
	// Scope: related 与我相关 / mine 我的申请 / pending 待我审批 / all 全部记录
	Scope  string `form:"scope" json:"scope"`
	Status string `form:"status" json:"status"`
	Search string `form:"search" json:"search"`
}

// AccessRequestCreateForm 发起申请
type AccessRequestCreateForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	// ResourceLevel 申请粒度：instance / database / schema / table
	// （schema=模式，仅 PostgreSQL / SQL Server 这类三层结构的数据源会用到）
	ResourceLevel string `form:"resource_level" json:"resource_level" binding:"required,oneof=instance database schema table"`
	// SchemaName 库/Schema（database / table 级必填；口径与授权表一致，取资源树 schema 路径）
	SchemaName string `form:"schema_name" json:"schema_name" binding:"omitempty,max=255"`
	// DatabaseName 库名。只有「库 → 模式 → 表」三层结构的数据源（PostgreSQL / SQL Server）
	// 才需要单独指定：库级申请的 schema_name 就是库名，模式/表级申请的 schema_name
	// 是「库.模式」完整路径，库名单独存一列用于详情展示与级联回填。
	// 单层结构（MySQL 系）留空即可，服务端回落成 schema_name。
	DatabaseName string `form:"database_name" json:"database_name" binding:"omitempty,max=255"`
	// TableName 表名（table 级必填）
	TableName string `form:"table_name" json:"table_name" binding:"omitempty,max=128"`
	// ---------------------------------------------------------------------------
	// 授权清单（2026-10-23 v2，参考数据授权弹窗的实现方式）：每条清单项 =
	// 一个库 / 模式对象 + 该对象自己的表圈选方式，一单可混搭
	// 「db1 整库 + db2 指定表 + db3 通配」。Entries 非空时 resource_level
	// 由服务端按清单内容派生（表 > 模式 > 库取最细粒度），表单值仅作展示参考。
	// 新前端一律走这组字段；上面的多资源 / 单资源字段保留兼容旧调用方。
	// ---------------------------------------------------------------------------
	Entries []AccessRequestEntryForm `form:"entries" json:"entries" binding:"omitempty,max=100,dive"`
	// DatabaseNames 多库；SchemaNames 多模式（每项为「库.模式」完整路径）；
	// TableMode 表维度圈选方式：list 指定表（单表 = 只选一个）/ wildcard 通配符 /
	// except 排除表（反选）。v1 多资源字段，Entries 为空时仍受理。
	DatabaseNames []string `form:"database_names" json:"database_names" binding:"omitempty,dive,max=255,max=50"`
	// SchemaNames 模式级 / 表级申请的模式清单，每项必须是「库.模式」完整路径
	SchemaNames []string `form:"schema_names" json:"schema_names" binding:"omitempty,dive,max=255,max=100"`
	// TableMode 表维度圈选方式，仅 table 级受理
	TableMode string `form:"table_mode" json:"table_mode" binding:"omitempty,oneof=list wildcard except"`
	// TableNames 指定表清单：三层结构每项为「库.模式.表」，单层结构为「表」
	TableNames []string `form:"table_names" json:"table_names" binding:"omitempty,dive,max=255,max=200"`
	// TablePattern 通配符模式：* 任意字符序列、? 单个字符，其余字面匹配
	TablePattern string `form:"table_pattern" json:"table_pattern" binding:"omitempty,max=128"`
	// ExceptTables 排除表清单（反选）：格式同 TableNames，语义 = 圈选范围内除清单外的全部表
	ExceptTables []string `form:"except_tables" json:"except_tables" binding:"omitempty,dive,max=255,max=200"`
	// Actions 申请的操作类型。只受理 select（查询）：变更与导出由 SQL 工单自身的
	// 审批流程覆盖，再申请一次数据权限等于同一件事审两遍，见 accessActionNeedsGrant。
	Actions string `form:"actions" json:"actions" binding:"omitempty,max=64"`
	Reason  string `form:"reason" json:"reason" binding:"required,max=512"`
	// ExpireDays 期望有效期（天）：1/7/30/180 四档，留 0 按默认 180 天（6 个月），
	// -1 表示「长期」= 不设失效时间（授权表 expire_at 落 NULL）。上限 3650 天。
	ExpireDays int `form:"expire_days" json:"expire_days"`
	// ApproverUsername 申请人指定的审批人。为空时按「有审批权的第一人」兜底不可取——
	// 审批人必须显式指定并落库，否则流转记录里会出现「没人负责」的空档。
	ApproverUsername string `form:"approver_username" json:"approver_username" binding:"omitempty,max=64"`
}

// AccessRequestEntryForm 授权清单的单条条目。
// Kind=database：Database 是对象本身（单层结构可携带表圈选方式，整库圈选整库）；
// Kind=schema：Schema 必须是「库.模式」完整路径且以 Database 为前缀，
// 仅三层结构（PostgreSQL / SQL Server）的数据源受理，可携带表圈选方式。
type AccessRequestEntryForm struct {
	// Kind 对象类型：database 库 / schema 模式
	Kind string `form:"kind" json:"kind" binding:"required,oneof=database schema"`
	// Database 库名
	Database string `form:"database" json:"database" binding:"required,max=255"`
	// Schema 模式完整路径「库.模式」，Kind=schema 时必填
	Schema string `form:"schema" json:"schema" binding:"omitempty,max=255"`
	// TableMode 该对象下的表圈选方式：空 = 整个对象；list 指定表 / wildcard 通配符 /
	// except 排除表。三层结构的整库条目（Kind=database）不受理表圈选。
	TableMode string `form:"table_mode" json:"table_mode" binding:"omitempty,oneof=list wildcard except"`
	// Tables 指定表叶子名清单（不带容器前缀，容器由条目自身决定）
	Tables []string `form:"tables" json:"tables" binding:"omitempty,dive,max=128,max=200"`
	// Pattern 通配符模式：* 任意字符序列、? 单个字符，其余字面匹配
	Pattern string `form:"pattern" json:"pattern" binding:"omitempty,max=128"`
	// Except 排除表叶子名清单（反选）
	Except []string `form:"except" json:"except" binding:"omitempty,dive,max=128,max=200"`
}

// AccessRequestApproveForm 审批通过（通过后进入待执行授权）
type AccessRequestApproveForm struct {
	Comment string `form:"comment" json:"comment" binding:"omitempty,max=512"`
	// ExpireDays 审批人可缩短/延长有效期（天）；0 表示按申请值，-1 表示长期
	ExpireDays int `form:"expire_days" json:"expire_days"`
}

// AccessRequestRejectForm 驳回（必须给理由）
type AccessRequestRejectForm struct {
	Comment string `form:"comment" json:"comment" binding:"required,max=512"`
}

// AccessRequestForwardForm 转交：指定另一位审批人继续处理本单
type AccessRequestForwardForm struct {
	// ApproverUsername 转交对象（必填，且不能是当前处理人自己）
	ApproverUsername string `form:"approver_username" json:"approver_username" binding:"required,max=64"`
	// Comment 转交说明（必填，让接手人知道为什么转过来）
	Comment string `form:"comment" json:"comment" binding:"required,max=512"`
}

// AccessRequestWithdrawForm 撤回（申请人本人，仅审批中）
type AccessRequestWithdrawForm struct {
	Comment string `form:"comment" json:"comment" binding:"omitempty,max=512"`
}

// AccessRequestTerminateForm 终止（申请人或管理员，仅审批中）
type AccessRequestTerminateForm struct {
	Comment string `form:"comment" json:"comment" binding:"omitempty,max=512"`
}

// AccessRequestGrantForm 执行授权（审批通过后由执行人落地权限）
type AccessRequestGrantForm struct {
	Comment string `form:"comment" json:"comment" binding:"omitempty,max=512"`
}

// AccessInstanceOptionsForm 申请入口：数据源下拉（申请人无授权也能看到全部启用实例）
type AccessInstanceOptionsForm struct {
	Search string `form:"search" json:"search"`
}

// AccessSchemaOptionsForm 申请入口：库/Schema 下拉
type AccessSchemaOptionsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
}

// AccessTableOptionsForm 申请入口：表下拉（服务端连实例实时拉取，不要求申请人已有授权）
type AccessTableOptionsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema" binding:"required,max=255"`
}
