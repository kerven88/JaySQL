package routers

// ============================================================
// 品牌设置 —— 路由
//
// 注意：本模块的 Routers 必须在 internal/app/app.go 的 Routers 列表里登记，
// 否则编译能过、路由却根本不会注册。
// ============================================================

import (
	"github.com/simajiong/jaysql/internal/brand/views"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-gonic/gin"
)

func Routers(r *gin.Engine) {
	// 公开接口：登录页在未登录态就要加载自定义 Logo，所以这里**不能**挂 JWT。
	// 返回内容只有两个 Logo 的相对地址，不含任何敏感信息。
	publicV1 := r.Group("/api/v1")
	publicV1.GET("/brand", views.GetBrandView)

	// 管理侧：品牌设置挂在「系统管理 → 系统配置」下，权限点直接复用 system.access
	// （进入系统管理模块的既有权限），不再新增权限点——省掉 bootstrap 权限种子与
	// 内置角色挂点的两处改动。
	// /logo 走 multipart（上传 / 重置图片），/text 走 JSON（品牌名称 / 首页标题）。
	admin := r.Group("/api/v1/admin/brand")
	admin.Use(global.App.JWT.MiddlewareFunc())
	admin.Use(middleware.RequirePermission("system.access"))
	{
		admin.POST("/logo", views.UploadBrandLogoView)
		admin.DELETE("/logo", views.ResetBrandLogoView)
		admin.PUT("/text", views.UpdateBrandTextView)
	}
}
