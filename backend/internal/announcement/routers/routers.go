package routers

import (
	"github.com/simajiong/jaysql/internal/announcement/views"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-gonic/gin"
)

func Routers(r *gin.Engine) {
	// 管理侧：公告管理（系统管理 → 系统配置 → 公告管理）
	admin := r.Group("/api/v1/admin/announcements")
	admin.Use(global.App.JWT.MiddlewareFunc())

	viewPerm := middleware.RequirePermission("system.announcement.view")
	managePerm := middleware.RequirePermission("system.announcement.manage")

	admin.GET("", viewPerm, views.ListAnnouncementsView)
	admin.POST("", managePerm, views.CreateAnnouncementView)
	admin.PUT("/:id", managePerm, views.UpdateAnnouncementView)
	admin.PUT("/:id/status", managePerm, views.UpdateAnnouncementStatusView)
	admin.DELETE("/:id", managePerm, views.DeleteAnnouncementView)

	// 展示设置：滚动速度 / 每条停留时长。静态路由，与上面的 /:id 由 gin 静态优先匹配。
	admin.GET("/settings", viewPerm, views.GetAnnouncementSettingsView)
	admin.PUT("/settings", managePerm, views.UpdateAnnouncementSettingsView)

	// 用户侧：页面公告条数据源。仅要求登录，不新增权限点
	// （公告是面向全员的站内通知，任何登录用户都应能看到）。
	apiV1 := r.Group("/api/v1")
	apiV1.Use(global.App.JWT.MiddlewareFunc())
	apiV1.GET("/announcements/active", views.ActiveAnnouncementsView)
}
