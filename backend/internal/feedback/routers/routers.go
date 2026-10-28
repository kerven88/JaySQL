package routers

import (
	"github.com/simajiong/jaysql/internal/feedback/views"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-gonic/gin"
)

func Routers(r *gin.Engine) {
	// 用户侧：提交反馈。仅要求登录，不新增权限点（任何登录用户都能反馈）
	apiV1 := r.Group("/api/v1")
	apiV1.Use(global.App.JWT.MiddlewareFunc())
	apiV1.POST("/feedback", views.SubmitFeedbackView)

	// 管理侧：反馈管理（系统管理 → 系统配置 → 反馈管理）
	admin := r.Group("/api/v1/admin/feedback")
	admin.Use(global.App.JWT.MiddlewareFunc())

	viewPerm := middleware.RequirePermission("system.feedback.view")
	managePerm := middleware.RequirePermission("system.feedback.manage")

	admin.GET("", viewPerm, views.ListFeedbacksView)
	admin.GET("/stats", viewPerm, views.FeedbackStatsView)
	// 详情用 query 参数（id）而非 /:id：gin 同一 method 树下不允许 static 段与 param 段并存
	admin.GET("/detail", viewPerm, views.GetFeedbackView)
	admin.PUT("/:id", managePerm, views.UpdateFeedbackView)
	admin.DELETE("/:id", managePerm, views.DeleteFeedbackView)
}
