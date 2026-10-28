package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/internal/users/views"
)

func RegisterAdminRoutes(v1 *gin.RouterGroup) {
	// 用户
	v1.GET("/users", middleware.RequirePermission("system.user.view"), views.GetUsersView)
	v1.POST("/users", middleware.RequirePermission("system.user.manage"), views.CreateUsersView)
	v1.PUT("/users/:uid", middleware.RequirePermission("system.user.manage"), views.UpdateUsersView)
	v1.DELETE("/users/:uid", middleware.RequirePermission("system.user.manage"), views.DeleteUsersView)
	v1.POST("/users/batch-delete", middleware.RequirePermission("system.user.manage"), views.BatchDeleteUsersView)
	v1.GET("/users/:uid/access", middleware.RequirePermission("system.user.view"), views.GetUserAccessView)
	v1.POST("/users/reset-password", middleware.RequirePermission("system.user.manage"), views.ResetUsersPasswordView)
	// 角色
	v1.GET("/roles", middleware.RequirePermission("system.role.view"), views.GetRolesView)
	v1.POST("/roles", middleware.RequirePermission("system.role.manage"), views.CreateRolesView)
	v1.PUT("/roles/:id", middleware.RequirePermission("system.role.manage"), views.UpdateRolesView)
	v1.DELETE("/roles/:id", middleware.RequirePermission("system.role.manage"), views.DeleteRolesView)
	// 权限
	v1.GET("/permissions", middleware.RequirePermission("system.permission.view"), views.GetPermissionsView)
}
