package views

import (
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// getUsername 取当前操作人。
//
// 注意：性能分析的可见性只由功能权限（performance.view / performance.manage）决定，
// 不再叠加数据访问授权过滤。这里保留取身份有两个用途：
//  1. 兜底校验 JWT 可用（异常时直接返回「认证信息无效」，避免下游空身份悄悄放行）；
//  2. 慢日志能力探测（/slowlog-capability）等链路需要的操作人身份与审计落点。
//
// 注意功能权限与数据授权的分工（2026-10-24 与工单口径对齐）：
// 性能分析的所有能力只认 performance.view / performance.manage，
// 不再叠加 insight_das_data_grants 判断——与 orders 的 CreateOrder 只认 order.submit 一致。
func getUsername(c *gin.Context) (string, bool) {
	username, ok := middleware.GetUserNameFromJWT(c)
	if !ok {
		response.Fail(c, "认证信息无效")
		return "", false
	}
	return username, true
}
