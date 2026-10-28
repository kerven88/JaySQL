package forms

import "github.com/simajiong/jaysql/pkg/pagination"

// ---------------------------------------------------------------------------
// 细粒度数据授权（insight_das_data_grants）
//
// 授权维度：主体（user/role） × 资源（instance/database/table） × 操作类型（select/export/dml）
// 每条授权都带授权人、生效时间、失效时间与事由，任何变更写一条审计流水。
// ---------------------------------------------------------------------------

// DataGrantListForm 授权列表查询（管理端 / 我的授权共用）
type DataGrantListForm struct {
	PaginationQ   pagination.Pagination
	Search        string `form:"search" json:"search"`
	PrincipalType string `form:"principal_type" json:"principal_type"`
	PrincipalID   uint64 `form:"principal_id" json:"principal_id"`
	InstanceID    string `form:"instance_id" json:"instance_id"`
	// SchemaName 只按库/Schema 定位键过滤，用于「某个库下的表级授权明细」这类下钻查询
	SchemaName string `form:"schema_name" json:"schema_name"`
	Status     string `form:"status" json:"status"`
	// Scope=self 时只返回「我本人 / 我所属角色」的授权（自助页用）
	Scope string `form:"scope" json:"scope"`
}

// DataGrantCreateForm 新增授权
//
// PrincipalType / PrincipalID 在自助场景（Self=true）下由服务端强制改写为操作者本人，
// 因此这里不做必填——否则前端得随便塞一个值才能调用自助接口，既多余又容易被误用。
// 非自助场景缺主体时，服务层会以「被授权主体不能为空」拒绝。
type DataGrantCreateForm struct {
	PrincipalType string `form:"principal_type" json:"principal_type" binding:"omitempty,oneof=user role"`
	PrincipalID   uint64 `form:"principal_id" json:"principal_id"`
	// ResourceLevel 授权粒度：instance / database / schema / table。
	// schema=模式级，schema_name 必须是「库.模式」完整路径（PG / SQL Server）。
	ResourceLevel string `form:"resource_level" json:"resource_level" binding:"required,oneof=instance database schema table"`
	InstanceID    string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	SchemaName    string `form:"schema_name" json:"schema_name"`
	TableName     string `form:"table_name" json:"table_name"`
	// Actions 允许的操作类型，逗号分隔；为空默认 select
	Actions string `form:"actions" json:"actions"`
	Effect  string `form:"effect" json:"effect"`
	// EffectiveAt / ExpireAt 支持 "2006-01-02 15:04:05"；ExpireAt 为空表示长期有效
	EffectiveAt string `form:"effective_at" json:"effective_at"`
	ExpireAt    string `form:"expire_at" json:"expire_at"`
	Reason      string `form:"reason" json:"reason" binding:"omitempty,max=512"`
	// Self=true 表示自助授权：强制把主体改写为操作者本人并标记来源 self
	Self bool `form:"self" json:"self"`
}

// DataGrantRevokeForm 撤销授权
type DataGrantRevokeForm struct {
	GrantNo string `form:"grant_no" json:"grant_no" binding:"required"`
	Reason  string `form:"reason" json:"reason"`
}

// DataGrantRenewForm 续期：延长失效时间
type DataGrantRenewForm struct {
	GrantNo  string `form:"grant_no" json:"grant_no" binding:"required"`
	ExpireAt string `form:"expire_at" json:"expire_at" binding:"required"`
	Reason   string `form:"reason" json:"reason"`
}

// DataGrantAuditListForm 授权变更流水查询
type DataGrantAuditListForm struct {
	PaginationQ pagination.Pagination
	GrantNo     string `form:"grant_no" json:"grant_no"`
	Action      string `form:"action" json:"action"`
	Search      string `form:"search" json:"search"`
}
