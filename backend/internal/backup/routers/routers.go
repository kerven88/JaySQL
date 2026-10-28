package routers

import (
	"github.com/simajiong/jaysql/internal/backup/views"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-gonic/gin"
)

func Routers(r *gin.Engine) {
	api := r.Group("/api/v1/backup")
	api.Use(global.App.JWT.MiddlewareFunc())

	api.GET("/instances", middleware.RequirePermission("backup.task.view"), views.GetBackupInstancesView)
	api.GET("/objects", middleware.RequirePermission("backup.task.view"), views.GetBackupObjectsView)
	api.GET("/nodes", middleware.RequirePermission("backup.task.view"), views.GetBackupNodesView)
	api.GET("/nodes/:id", middleware.RequirePermission("backup.task.view"), views.GetBackupNodeDetailView)
	api.GET("/nodes/:id/token", middleware.RequirePermission("backup.task.manage"), views.GetBackupNodeTokenView)
	api.POST("/nodes", middleware.RequirePermission("backup.task.manage"), views.CreateBackupNodeView)
	api.PUT("/nodes/:id", middleware.RequirePermission("backup.task.manage"), views.UpdateBackupNodeView)
	api.POST("/nodes/:id/operate", middleware.RequirePermission("backup.task.manage"), views.OperateBackupNodeView)

	api.GET("/storage_targets", middleware.RequirePermission("backup.task.view"), views.GetStorageTargetsView)
	api.GET("/storage_targets/:id", middleware.RequirePermission("backup.task.view"), views.GetStorageTargetDetailView)
	api.POST("/storage_targets", middleware.RequirePermission("backup.task.manage"), views.CreateStorageTargetView)
	api.PUT("/storage_targets/:id", middleware.RequirePermission("backup.task.manage"), views.UpdateStorageTargetView)
	api.DELETE("/storage_targets/:id", middleware.RequirePermission("backup.task.manage"), views.DeleteStorageTargetView)
	api.POST("/storage_targets/:id/precheck", middleware.RequirePermission("backup.task.manage"), views.PrecheckStorageTargetView)
	api.POST("/storage_targets/:id/confirm_fingerprint", middleware.RequirePermission("backup.task.manage"), views.ConfirmStorageTargetFingerprintView)

	api.GET("/tasks", middleware.RequirePermission("backup.task.view"), views.GetBackupTasksView)
	api.GET("/tasks/:id", middleware.RequirePermission("backup.task.view"), views.GetBackupTaskDetailView)
	api.POST("/tasks/precheck", middleware.RequirePermission("backup.task.manage"), views.PrecheckBackupTaskView)
	api.POST("/tasks", middleware.RequirePermission("backup.task.manage"), views.CreateBackupTaskView)
	api.PUT("/tasks/:id", middleware.RequirePermission("backup.task.manage"), views.UpdateBackupTaskView)
	api.PUT("/tasks/:id/name", middleware.RequirePermission("backup.task.manage"), views.UpdateBackupTaskNameView)
	api.DELETE("/tasks/:id", middleware.RequirePermission("backup.task.manage"), views.DeleteBackupTaskView)
	api.POST("/tasks/:id/run", middleware.RequirePermission("backup.task.manage"), views.RunBackupTaskView)
	api.GET("/tasks/:id/runs", middleware.RequirePermission("backup.task.view"), views.GetBackupTaskRunsView)
	api.GET("/runs/:id/log", middleware.RequirePermission("backup.task.view"), views.GetBackupRunLogView)
	api.POST("/runs/:id/stop", middleware.RequirePermission("backup.task.manage"), views.StopBackupRunView)

	api.GET("/sets", middleware.RequirePermission("backup.set.view"), views.GetBackupSetsView)
	api.POST("/sets/batch-delete", middleware.RequirePermission("backup.task.manage"), views.BatchDeleteBackupSetsView)
	api.GET("/sets/:id", middleware.RequirePermission("backup.set.view"), views.GetBackupSetDetailView)
	api.GET("/sets/:id/objects", middleware.RequirePermission("backup.set.view"), views.GetBackupSetObjectsView)
	api.POST("/sets/:id/stop", middleware.RequirePermission("backup.task.manage"), views.StopBackupSetView)
	api.DELETE("/sets/:id", middleware.RequirePermission("backup.task.manage"), views.DeleteBackupSetView)
	api.GET("/sets/:id/download", middleware.RequirePermission("backup.set.download"), views.DownloadBackupSetView)

	api.GET("/restores", middleware.RequirePermission("backup.restore.view"), views.GetRestoreRunsView)
	// 恢复向导预检：必须注册在 /restores/:id 之前，且无副作用（不落 insight_restore_runs）
	api.POST("/restores/precheck", middleware.RequirePermission("backup.restore.execute"), views.PrecheckRestoreRunView)
	api.POST("/restores", middleware.RequirePermission("backup.restore.execute"), views.CreateRestoreRunView)
	api.PUT("/restores/:id", middleware.RequirePermission("backup.restore.execute"), views.UpdateRestoreRunView)
	api.PUT("/restores/:id/name", middleware.RequirePermission("backup.restore.execute"), views.UpdateRestoreRunNameView)
	api.DELETE("/restores/:id", middleware.RequirePermission("backup.restore.execute"), views.DeleteRestoreRunView)
	api.GET("/restores/:id", middleware.RequirePermission("backup.restore.view"), views.GetRestoreRunDetailView)
	api.GET("/restores/:id/log", middleware.RequirePermission("backup.restore.view"), views.GetRestoreRunLogView)
	api.GET("/restores/:id/log/download", middleware.RequirePermission("backup.restore.view"), views.DownloadRestoreRunLogView)
	api.POST("/restores/:id/stop", middleware.RequirePermission("backup.restore.execute"), views.StopRestoreRunView)
	api.POST("/restores/:id/rerun", middleware.RequirePermission("backup.restore.execute"), views.RetryRestoreRunView)
	api.GET("/physical-restores", middleware.RequirePermission("backup.restore.view"), views.GetPhysicalRestoreRunsView)
	api.POST("/physical-restores/precheck", middleware.RequirePermission("backup.restore.execute"), views.PrecheckPhysicalRestoreView)
	api.POST("/physical-restores", middleware.RequirePermission("backup.restore.execute"), views.CreatePhysicalRestoreRunView)
	api.PUT("/physical-restores/:id", middleware.RequirePermission("backup.restore.execute"), views.UpdatePhysicalRestoreRunView)
	api.DELETE("/physical-restores/:id", middleware.RequirePermission("backup.restore.execute"), views.DeletePhysicalRestoreRunView)
	api.GET("/physical-restores/:id", middleware.RequirePermission("backup.restore.view"), views.GetPhysicalRestoreRunDetailView)
	api.GET("/physical-restores/:id/log", middleware.RequirePermission("backup.restore.view"), views.GetPhysicalRestoreRunLogView)
	api.POST("/physical-restores/:id/stop", middleware.RequirePermission("backup.restore.execute"), views.StopPhysicalRestoreRunView)

	agent := r.Group("/api/v1/backup/agent")
	agent.GET("/heartbeat", views.AgentHeartbeatView)
	agent.POST("/heartbeat", views.AgentHeartbeatView)
	agent.GET("/poll", views.AgentPollJobView)
	agent.POST("/poll", views.AgentPollJobView)
	agent.GET("/report", views.AgentReportRunView)
	agent.POST("/report", views.AgentReportRunView)
	agent.GET("/run-stop-status", views.AgentRunStopStatusView)
	agent.POST("/run-stop-status", views.AgentRunStopStatusView)
}
