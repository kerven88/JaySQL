package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/views"
	"github.com/simajiong/jaysql/middleware"
)

func RegisterAdminRoutes(admin *gin.RouterGroup) {
	// 【已废弃 · 只保留新增入口】
	// /schemas/grant 与 /tables/grant 的 GET / DELETE 操作的是旧授权表
	// （insight_das_schema_perms / table_perms），它看不到 system / self / migrate
	// 来源的授权，用它做明细会出现「页面无记录但用户能访问」的口径分裂。
	// 「数据访问」页面的读与撤权已全部切到下面的 /data-grants（新表）。
	// 这两个 POST 仍在使用：新增落旧表后由 ReconcileLegacyGrantsForPrincipal
	// 对账进新表（grant_items 的多规则语义只有旧表结构能表达）。
	// 新代码不要再用这里的 GET / DELETE。
	admin.GET("/schemas/grant", middleware.RequirePermission("system.das.view"), views.AdminGetSchemasGrantView)
	admin.POST("/schemas/grant", middleware.RequirePermission("system.das.manage"), views.AdminCreateSchemasGrantView)
	admin.DELETE("/schemas/grant/:id", middleware.RequirePermission("system.das.manage"), views.AdminDeleteSchemasGrantView)

	admin.GET("/tables/grant", middleware.RequirePermission("system.das.view"), views.AdminGetTablesGrantView)
	admin.POST("/tables/grant", middleware.RequirePermission("system.das.manage"), views.AdminCreateTablesGrantView)
	admin.DELETE("/tables/grant/:id", middleware.RequirePermission("system.das.manage"), views.AdminDeleteTablesGrantView)

	admin.GET("/instances/list", middleware.RequirePermission("system.das.view"), views.AdminGetInstancesListView)
	admin.GET("/schemas/list", middleware.RequirePermission("system.das.view"), views.AdminGetSchemasListView)
	admin.GET("/tables/list", middleware.RequirePermission("system.das.view"), views.AdminGetTablesListView)

	// 细粒度数据授权（新模型 insight_das_data_grants）
	admin.GET("/data-grants", middleware.RequirePermission("system.das.view"), views.ListDataGrantsView)
	admin.POST("/data-grants", middleware.RequirePermission("system.das.manage"), views.CreateDataGrantView)
	admin.POST("/data-grants/revoke", middleware.RequirePermission("system.das.manage"), views.RevokeDataGrantView)
	admin.POST("/data-grants/renew", middleware.RequirePermission("system.das.manage"), views.RenewDataGrantView)
	admin.GET("/data-grants/audits", middleware.RequirePermission("system.das.view"), views.ListDataGrantAuditsView)
}
