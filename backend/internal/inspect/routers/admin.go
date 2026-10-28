package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/inspect/views"
	"github.com/simajiong/jaysql/middleware"
)

func RegisterAdminRoutes(v1 *gin.RouterGroup) {

	// SQL 规范管理
	v1.GET("/sql-standards", middleware.RequirePermission("system.inspect.view"), views.AdminGetSQLStandardsView)
	v1.POST("/sql-standards", middleware.RequirePermission("system.inspect.manage"), views.AdminCreateSQLStandardView)
	// 按实例查绑定规范，必须注册在 /sql-standards/:id 之前，避免被解析为 id
	v1.GET("/sql-standards/by-instance/:instance_id", middleware.RequirePermission("system.inspect.view"), views.AdminGetSQLStandardByInstanceView)
	v1.GET("/sql-standards/:id", middleware.RequirePermission("system.inspect.view"), views.AdminGetSQLStandardDetailView)
	v1.PUT("/sql-standards/:id", middleware.RequirePermission("system.inspect.manage"), views.AdminUpdateSQLStandardView)
	v1.DELETE("/sql-standards/:id", middleware.RequirePermission("system.inspect.manage"), views.AdminDeleteSQLStandardView)
	v1.PUT("/sql-standards/rules/:rule_id", middleware.RequirePermission("system.inspect.manage"), views.AdminUpdateSQLStandardRuleView)
	v1.GET("/sql-standards/:id/instances", middleware.RequirePermission("system.inspect.view"), views.AdminGetSQLStandardInstancesView)
	v1.POST("/sql-standards/:id/instances", middleware.RequirePermission("system.inspect.manage"), views.AdminBindSQLStandardInstanceView)
	v1.DELETE("/sql-standards/instances/:id", middleware.RequirePermission("system.inspect.manage"), views.AdminUnbindSQLStandardInstanceView)
	v1.GET("/sql-standards/:id/available-instances", middleware.RequirePermission("system.inspect.manage"), views.AdminGetSQLStandardAvailableInstancesView)
}
