package routers

import (
	"github.com/simajiong/jaysql/internal/ai/views"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-gonic/gin"
)

func Routers(r *gin.Engine) {
	ai := r.Group("/api/v1/ai")
	ai.Use(global.App.JWT.MiddlewareFunc())

	// AI 对话：运行时真实模型调用（复用 ai.chat.use 权限，无需新增种子）
	ai.POST("/chat/completions", middleware.RequirePermission("ai.chat.use"), views.AIChatCompletionView)
	// AI 对话流式（SSE）：直答路由逐字下发；工具路由仍整体下发
	ai.POST("/chat/stream", middleware.RequirePermission("ai.chat.use"), views.AIChatStreamView)
	// AI 对话：主动取消运行中的一次编排（run_id 由前端生成，与 stream/completions 同权限）。
	// 注册项绑定发起人，跨用户取消会被拒绝（同权限不等于可以掐别人的对话）。
	ai.POST("/chat/cancel", middleware.RequirePermission("ai.chat.use"), views.AIChatCancelView)

	// AI 对话创建工单：草稿卡片上点「确认创建」后落库。
	// 复用 order.submit（与创建页同一权限点），创建人取当前登录会话。
	// 落库即进入规范预审（WAIT_PRECHECK）并生成预审快照，无需额外提交动作。
	ai.POST("/orders/confirm", middleware.RequirePermission("order.submit"), views.AICreateOrderConfirmView)

	// AI 对话创建备份任务：草稿卡片上点「确认创建」后落库。
	// 复用 backup.task.manage（与备份任务管理同一权限点），创建人取当前登录会话。
	ai.POST("/backups/confirm", middleware.RequirePermission("backup.task.manage"), views.AICreateBackupConfirmView)

	// ---------------- 大模型配置接入 ----------------
	// 读接口：system.ai.view；写接口：system.ai.manage
	viewPerm := middleware.RequirePermission("system.ai.view")
	managePerm := middleware.RequirePermission("system.ai.manage")

	// 厂商模板（落库可维护 → 新增厂商不发版）
	ai.GET("/vendors", viewPerm, views.LlmVendorListView)
	ai.POST("/vendor-templates", managePerm, views.LlmVendorTemplateCreateView)
	ai.POST("/vendor-templates/update", managePerm, views.LlmVendorTemplateUpdateView)
	ai.POST("/vendor-templates/delete", managePerm, views.LlmVendorTemplateDeleteView)

	// 服务商配置
	ai.GET("/providers", viewPerm, views.LlmProviderListView)
	ai.GET("/providers/detail", viewPerm, views.LlmProviderDetailView)
	ai.POST("/providers", managePerm, views.LlmProviderCreateView)
	ai.POST("/providers/update", managePerm, views.LlmProviderUpdateView)
	ai.POST("/providers/delete", managePerm, views.LlmProviderDeleteView)
	ai.POST("/providers/enable", managePerm, views.LlmProviderEnableView)
	ai.POST("/providers/disable", managePerm, views.LlmProviderDisableView)
	ai.POST("/providers/set-default", managePerm, views.LlmProviderSetDefaultView)

	// 凭证校验：test-connection 不落库（先测后存），validate 针对已存配置
	ai.POST("/providers/test-connection", managePerm, views.LlmProviderTestView)
	ai.POST("/providers/validate", managePerm, views.LlmProviderValidateView)

	// 模型管理
	ai.GET("/models", viewPerm, views.LlmModelListView)
	ai.POST("/models", managePerm, views.LlmModelCreateView)
	ai.POST("/models/update", managePerm, views.LlmModelUpdateView)
	ai.POST("/models/delete", managePerm, views.LlmModelDeleteView)
	ai.POST("/models/set-default", managePerm, views.LlmModelSetDefaultView)
}
