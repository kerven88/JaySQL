package routers

import (
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"

	"github.com/simajiong/jaysql/internal/common/views"

	"github.com/gin-gonic/gin"
)

func AdminRoutes(v1 *gin.RouterGroup) {
	admin := v1.Group("/admin")
	// 环境
	admin.GET("/environments", middleware.RequirePermission("system.environment.view"), views.AdminGetEnvironmentView)
	admin.POST("/environment", middleware.RequirePermission("system.environment.manage"), views.AdminCreateEnvironmentView)
	admin.PUT("/environment/:id", middleware.RequirePermission("system.environment.manage"), views.AdminUpdateEnvironmentView)
	admin.DELETE("/environment/:id", middleware.RequirePermission("system.environment.manage"), views.AdminDeleteEnvironmentView)
	// 实例配置
	admin.GET("/instances", middleware.RequirePermission("system.instance.view"), views.AdminGetInstancesView)
	admin.POST("/instances", middleware.RequirePermission("system.instance.manage"), views.AdminCreateInstancesView)
	admin.POST("/instances/batch-import", middleware.RequirePermission("system.instance.manage"), views.AdminBatchImportInstancesView)
	admin.POST("/instances/test", middleware.RequirePermission("system.instance.test"), views.AdminTestInstanceConnectionByPayloadView)
	admin.PUT("/instances/:id", middleware.RequirePermission("system.instance.manage"), views.AdminUpdateInstancesView)
	admin.PUT("/instances/:id/remark", middleware.RequirePermission("system.instance.manage"), views.AdminUpdateInstanceRemarkView)
	admin.DELETE("/instances/:id", middleware.RequirePermission("system.instance.manage"), views.AdminDeleteInstances)
	admin.POST("/instances/:id/test", middleware.RequirePermission("system.instance.test"), views.AdminTestInstanceConnectionView)
	admin.POST("/instances/:id/sync-meta", middleware.RequirePermission("system.instance.sync_meta"), views.AdminSyncInstanceMetaView)
	// 公有云账号 + 云实例集成
	admin.POST("/cloud-accounts/validate", middleware.RequirePermission("system.instance.manage"), views.AdminValidateCloudAccountView)
	admin.POST("/cloud-accounts/discover", middleware.RequirePermission("system.instance.manage"), views.AdminDiscoverCloudInstancesView)
	admin.POST("/cloud-accounts/import", middleware.RequirePermission("system.instance.manage"), views.AdminImportCloudInstancesView)
}

func Routers(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	v1.Use(global.App.JWT.MiddlewareFunc())

	// 数据源总览（数据源管理一级菜单）：管理侧任一视角权限即可，口径与前端路由 meta 一致
	v1.GET("/datasource/overview", middleware.RequireAnyPermission(
		"system.instance.view", "monitor.view", "backup.task.view", "system.das.view",
	), views.DatasourceOverviewView)

	// 主机登录：拿 shell 等同于能改实例配置，直接复用 system.instance.manage ——
	// 这是「能在实例表单里改 SSH 凭据」的同一个信任级别（全局只读只有 view，拿不到 shell）。
	// 另立权限点会更精确，但要多动权限种子 + 角色定义两处，且必须手工授权否则功能默认是死的。
	v1.POST("/datasource/host-login/precheck", middleware.RequirePermission("system.instance.manage"), views.HostLoginPrecheckView)
	v1.POST("/datasource/host-login/sessions", middleware.RequirePermission("system.instance.manage"), views.CreateHostLoginSessionView)
	v1.GET("/datasource/host-login/sessions", middleware.RequirePermission("system.instance.manage"), views.GetHostLoginSessionsView)
	v1.DELETE("/datasource/host-login/sessions/:id", middleware.RequirePermission("system.instance.manage"), views.CloseHostLoginSessionView)

	// 🔴 终端 WS 必须挂在 JWT 中间件之外：浏览器 WebSocket 握手无法自定义请求头，
	// 带不了 Authorization，挂进 v1 组会 100% 401（工单模块的 /ws/:channel 同理）。
	// 鉴权改用「建会话接口签发的一次性令牌」：随机 UUID、60 秒失效、用一次即作废，
	// 只有持 datasource.host.login 且通过 JWT 校验的用户拿得到，拿到后必须立刻握手。
	r.GET("/api/v1/datasource/host-login/ws/:session_id", views.HostLoginTerminalView)

	v1.GET("/home/dashboard", views.HomeDashboardView)
	v1.POST("/home/risk-dismissed/batch", views.HomeRiskDismissBatchView)
	v1.POST("/home/risk-dismissed/dismiss", views.HomeRiskDismissView)
	v1.POST("/home/risk-dismissed/undismiss", views.HomeRiskUndismissView)
	v1.GET("/audit/logs", middleware.RequirePermission("audit.log.view"), views.GetAuditLogsView)
	v1.GET("/audit/logs/:audit_id", middleware.RequirePermission("audit.log.view"), views.GetAuditLogDetailView)
	AdminRoutes(v1)
}
