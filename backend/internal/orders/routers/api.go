package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/orders/views"
	"github.com/simajiong/jaysql/middleware"
)

func RegisterApiRoutes(v1 *gin.RouterGroup) {
	v1.GET("environments", middleware.RequirePermission("order.submit"), views.GetOrderEnvironmentsView)
	v1.GET("data-sources", middleware.RequirePermission("order.submit"), views.GetOrderDataSourcesView)
	v1.GET("instances", middleware.RequirePermission("order.submit"), views.GetOrderInstancesView)
	v1.GET("schemas", middleware.RequirePermission("order.submit"), views.GetOrderSchemasView)
	// users：提交工单选审批人也需要（仅以 order.submit 作为提交链路权限依据，不依赖 order.view）
	v1.GET("users", middleware.RequireAnyPermission("order.submit", "order.view"), views.GetOrderUsersView)
	// 语法检查
	v1.POST("inspect-syntax", middleware.RequirePermission("order.submit"), views.InspectOrderSyntaxView)
	// 规范预审
	v1.POST("pre-inspect", middleware.RequirePermission("order.submit"), views.PreInspectOrderView)
	// 创建工单
	v1.POST("", middleware.RequirePermission("order.submit"), views.CreateOrderView)
	// 获取工单
	v1.GET("", middleware.RequirePermission("order.view"), views.GetOrderListView)
	// 详情：撤回后编辑链路需要 order.submit 用户回读自己的工单；服务内限制无 order.view 用户只能读自己提交的
	v1.GET(":order_id", middleware.RequireAnyPermission("order.submit", "order.view"), views.GetOrderDetailView)
	// 工单规范预审快照（历史态，终态工单不重跑预审）
	v1.GET(":order_id/pre-inspect", middleware.RequirePermission("order.view"), views.GetOrderPreInspectSnapshotView)
	// 获取审批流和日志
	v1.GET("approvals/:order_id", middleware.RequirePermission("order.view"), views.GetOrderApprovalsView)
	v1.GET("logs/:order_id", middleware.RequirePermission("order.view"), views.GetOrderLogsView)
	// 钉钉审批集成：提交审批弹窗的可用性预览 / 长连接状态 / HTTP 回调入口。
	// 注意这些是静态多段路径，与下面的 `:order_id` 单段参数路由共存 —— gin 会优先匹配静态段。
	v1.GET("approval/dingtalk/preview", middleware.RequirePermission("order.submit"), views.PreviewDingTalkNotifyView)
	v1.GET("approval/dingtalk/status", middleware.RequirePermission("order.view"), views.DingTalkStreamStatusView)
	v1.POST("approval/dingtalk/callback", middleware.RequirePermission("order.manage"), views.DingTalkCardCallbackView)
	// 操作
	v1.PUT("actions/submit-approval", middleware.RequirePermission("order.submit"), views.SubmitApprovalView)
	v1.PUT("actions/approval", middleware.RequirePermission("order.approve"), views.ApprovalOrderView)
	v1.PUT("actions/transfer", middleware.RequirePermission("order.approve"), views.TransferOrderView)
	v1.PUT("actions/revoke", middleware.RequirePermission("order.manage"), views.RevokeOrderView)
	// 撤回工单（申请人本人，服务内二次校验；撤回后可编辑再提交，工单 ID 不变）
	v1.PUT("actions/withdraw", middleware.RequirePermission("order.submit"), views.WithdrawOrderView)
	v1.PUT("actions/update-content", middleware.RequirePermission("order.submit"), views.UpdateOrderContentView)
	// 修改工单名称（申请人本人或超管，服务内二次校验）
	v1.PUT("actions/update-title", middleware.RequirePermission("order.view"), views.UpdateOrderTitleView)
	v1.PUT("actions/terminate", middleware.RequirePermission("order.manage"), views.TerminateOrderView)
	v1.PUT("actions/complete", middleware.RequirePermission("order.execute"), views.CompleteOrderView)
	v1.PUT("actions/fail", middleware.RequirePermission("order.execute"), views.FailOrderView)
	v1.PUT("actions/review", middleware.RequirePermission("order.approve"), views.ReviewOrderView)
	// 新增生成执行任务接口
	v1.POST("tasks", middleware.RequirePermission("order.execute"), views.GenOrderTasksView)
	// 获取执行任务列表
	v1.GET("tasks/:order_id", middleware.RequirePermission("order.view"), views.GetTasksView)
	// 执行任务
	v1.POST("tasks/execute", middleware.RequirePermission("order.execute"), views.ExecuteTaskView)
	// 批量执行任务
	v1.POST("tasks/execute-batch", middleware.RequirePermission("order.execute"), views.ExecuteBatchTasksView)
	// 预览
	v1.GET("tasks/preview", middleware.RequirePermission("order.view"), views.PreviewTasksView)
	// 下载导出文件
	v1.GET("tasks/exports/:task_id", middleware.RequirePermission("order.view"), views.DownloadExportFileView)
}
