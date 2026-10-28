package routers

import (
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/hotarticle/views"
	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-gonic/gin"
)

func Routers(r *gin.Engine) {
	// 管理侧：热文管理（系统管理 → 系统配置 → 热文管理）
	admin := r.Group("/api/v1/admin/hot-articles")
	admin.Use(global.App.JWT.MiddlewareFunc())

	viewPerm := middleware.RequirePermission("system.hotarticle.view")
	managePerm := middleware.RequirePermission("system.hotarticle.manage")

	admin.GET("", viewPerm, views.ListHotArticlesView)
	admin.POST("", managePerm, views.CreateHotArticleView)
	// 批量排序用 POST 而非 PUT：PUT 树下已有 /:id 参数段，
	// gin 同一 method 树下静态段与参数段不能并存（会 panic），故把静态路由挂到 POST 树。
	admin.POST("/sort", managePerm, views.UpdateHotArticleSortView)
	admin.PUT("/:id", managePerm, views.UpdateHotArticleView)
	admin.PUT("/:id/status", managePerm, views.UpdateHotArticleStatusView)
	admin.DELETE("/:id", managePerm, views.DeleteHotArticleView)

	// 用户侧：首页「相关热文」数据源。仅要求登录，不新增权限点
	// （首页是登录后第一屏，任何登录用户都应能看到）。
	apiV1 := r.Group("/api/v1")
	apiV1.Use(global.App.JWT.MiddlewareFunc())
	apiV1.GET("/home/hot-articles", views.HomeHotArticlesView)
}
