package services

import "strings"

// ---------------------------------------------------------------------------
// 数据权限的「三类接口」口径（2026-10-23 收口）
//
// 数据访问授权与接口的对应关系只有三类，并且只有查询需要授权：
//
//	查询 query   SELECT / 元数据读取   —— 走 CheckDataAccess，未授权即不可见
//	变更 change  DML / DDL             —— 由 SQL 工单「提交 → 审批 → 执行」覆盖，不再要求数据授权
//	导出 export  EXPORT                —— 同上，导出本身也是一张工单
//
// 为什么变更 / 导出不需要额外的数据授权：
//  1. 这两类操作的审批对象就是「具体 SQL + 目标库表」，审批人看到的是将要执行的内容，
//     粒度比按库/表申请的常驻授权更细；
//  2. 常驻授权在有效期内可以执行任意变更，反而比工单审批更宽——叠加不是收紧而是放开；
//  3. 同一件事审两遍，两层口径必然漂移（工单批了但没申请数据权限 → 执行被拦，
//     申请人只能在两个入口之间来回补单）。
//  所以申请入口只受理查询权限（见 CreateAccessRequestService），变更 / 导出只认工单审批。
//
// 判定入口只此一个：ActionNeedsAccessGrant。新增操作维度时必须回来改这里，
// 不允许在业务代码里各写一份「要不要校验」的判断。
// ---------------------------------------------------------------------------

const (
	// AccessAPIScopeQuery 查询接口：需要数据访问授权
	AccessAPIScopeQuery = "query"
	// AccessAPIScopeChange 变更接口（DML/DDL）：由工单审批覆盖
	AccessAPIScopeChange = "change"
	// AccessAPIScopeExport 导出接口：由工单审批覆盖
	AccessAPIScopeExport = "export"
)

// AccessAPIScopeOfAction 把数据操作维度映射到接口口径。
// 同时认操作维度（dml/export/select）与工单 SQL 类型（DDL/MIXED）：
// 调用方既有拿 Action 判定的，也有直接拿 SQLType 判定的，两边不能判出不同结果。
// 未知值按查询处理——默认收紧，避免将来新增的维度被静默放行。
func AccessAPIScopeOfAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case DataActionDML, "ddl", "mixed":
		return AccessAPIScopeChange
	case DataActionExport:
		return AccessAPIScopeExport
	default:
		return AccessAPIScopeQuery
	}
}

// ActionNeedsAccessGrant 判断某操作维度是否需要数据访问授权（即是否需要走申请/授权）。
// 只有查询需要；变更与导出由工单审批流程覆盖。
func ActionNeedsAccessGrant(action string) bool {
	return AccessAPIScopeOfAction(action) == AccessAPIScopeQuery
}
