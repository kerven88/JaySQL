package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/orders/views"
	"github.com/simajiong/jaysql/middleware"
)

func RegisterAdminRoutes(v1 *gin.RouterGroup) {
	v1.GET("/unbound-users", middleware.RequirePermission("system.approval_flow.view"), views.AdminGetApprovalFlowUnboundUsersView)
	v1.GET("", middleware.RequirePermission("system.approval_flow.view"), views.AdminGetApprovalFlowsView)
	v1.POST("", middleware.RequirePermission("system.approval_flow.manage"), views.AdminCreateApprovalFlowsView)
	v1.PUT(":id", middleware.RequirePermission("system.approval_flow.manage"), views.AdminUpdateApprovalFlowsView)
	v1.DELETE(":id", middleware.RequirePermission("system.approval_flow.manage"), views.AdminDeleteApprovalFlowsView)
	v1.POST("/bind-users", middleware.RequirePermission("system.approval_flow.manage"), views.AdminBindUsersToApprovalFlowView)
	v1.GET("/bind-users", middleware.RequirePermission("system.approval_flow.view"), views.AdminGetApprovalFlowUsersView)
	v1.DELETE("/bind-users/:id", middleware.RequirePermission("system.approval_flow.manage"), views.AdminDeleteUsersFromApprovalFlowView)
}
