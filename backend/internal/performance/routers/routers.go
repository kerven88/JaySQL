package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/views"
	"github.com/simajiong/jaysql/middleware"
)

func Routers(r *gin.Engine) {
	v1 := r.Group("/api/v1/performance")
	v1.Use(global.App.JWT.MiddlewareFunc())
	v1.GET("/instances", middleware.RequirePermission("performance.view"), views.GetInstanceOptionsView)
	v1.GET("/overview", middleware.RequirePermission("performance.view"), views.GetOverviewView)
	v1.GET("/slow-sql", middleware.RequirePermission("performance.view"), views.GetSlowSQLView)
	v1.GET("/slow-sql/instances", middleware.RequirePermission("performance.view"), views.GetSlowSQLInstanceSummaryView)
	v1.GET("/slow-sql/analysis", middleware.RequirePermission("performance.view"), views.GetSlowSQLAnalysisView)
	v1.GET("/slow-sql/stats", middleware.RequirePermission("performance.view"), views.GetSlowSQLStatsView)
	// 慢SQL导出：按当前查询条件导出全量 CSV（字段与页面列一致）
	v1.GET("/slow-sql/stats/export", middleware.RequirePermission("performance.view"), views.ExportSlowSQLStatsView)
	v1.GET("/slow-sql/export", middleware.RequirePermission("performance.view"), views.ExportSlowSQLView)
	v1.GET("/snapshot-params", middleware.RequirePermission("performance.manage"), views.GetSnapshotParamListView)
	v1.POST("/snapshot-params", middleware.RequirePermission("performance.manage"), views.CreateSnapshotParamView)
	v1.PUT("/snapshot-params/:id", middleware.RequirePermission("performance.manage"), views.UpdateSnapshotParamView)
	v1.DELETE("/snapshot-params/:id", middleware.RequirePermission("performance.manage"), views.DeleteSnapshotParamView)

	// 慢日志采集能力：探测结果与手动重探（重探仅刷新能力缓存，沿用 view 权限）
	v1.GET("/slowlog-capability", middleware.RequirePermission("performance.view"), views.GetSlowLogCapabilityListView)
	v1.POST("/slowlog-capability/:instance_id/probe", middleware.RequirePermission("performance.view"), views.ReprobeSlowLogCapabilityView)

	// 慢日志采集前提条件：清单 + 实例诊断（失败码 → 前提项关联）
	v1.GET("/slowlog-prerequisites", middleware.RequirePermission("performance.view"), views.GetSlowLogPrerequisitesView)
	v1.GET("/slowlog-diagnosis", middleware.RequirePermission("performance.view"), views.GetSlowLogDiagnosisView)
	v1.GET("/slowlog-prerequisites/:instance_id", middleware.RequirePermission("performance.view"), views.GetSlowLogInstancePrerequisitesView)

	// 参数管理：列表/对比/历史（查看）；修改/回滚（独立权限 performance.param.modify，高危参数服务内二次校验）
	v1.GET("/params", middleware.RequirePermission("performance.view"), views.GetInstanceParamsView)
	v1.GET("/params/diff", middleware.RequirePermission("performance.view"), views.DiffInstanceParamsView)
	v1.GET("/params/changes", middleware.RequirePermission("performance.view"), views.GetParamChangesView)
	v1.POST("/params/modify", middleware.RequirePermission("performance.param.modify"), views.ModifyInstanceParamView)
	v1.POST("/params/rollback", middleware.RequirePermission("performance.param.modify"), views.RollbackParamView)
}
