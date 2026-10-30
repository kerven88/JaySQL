package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/views"
	"github.com/simajiong/jaysql/middleware"
)

func Routers(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	v1.Use(global.App.JWT.MiddlewareFunc())
	v1.GET("/monitor/dashboard", middleware.RequirePermission("monitor.view"), views.GetDashboardView)
	// 风险总览：全部风险实例及其判定原因（大屏"风险总览"入口与下钻共用）
	v1.GET("/monitor/risks", middleware.RequirePermission("monitor.view"), views.GetRiskOverviewView)
	v1.GET("/monitor/detail", middleware.RequirePermission("monitor.view"), views.GetDetailView)
	// 指标历史：明细页趋势图按时间范围（15m/1h/6h/24h/7d/自定义）取数
	v1.GET("/monitor/metrics/history", middleware.RequirePermission("monitor.view"), views.GetMetricHistoryView)
	v1.GET("/monitor/config/targets", middleware.RequirePermission("monitor.manage"), views.GetTargetConfigsView)
	v1.PUT("/monitor/config/targets/:instance_id", middleware.RequirePermission("monitor.manage"), views.UpdateTargetConfigView)
	// 采集频率：三处周期的唯一配置入口
	v1.GET("/monitor/config/scrape", middleware.RequirePermission("monitor.manage"), views.GetScrapeConfigView)
	v1.PUT("/monitor/config/scrape", middleware.RequirePermission("monitor.manage"), views.UpdateScrapeConfigView)
	// 健康分级：等级(P0~P3) / 结果项归属 / 判定规则的唯一配置入口
	v1.GET("/monitor/config/health-grade", middleware.RequirePermission("monitor.manage"), views.GetHealthGradeConfigView)
	v1.PUT("/monitor/config/health-grade", middleware.RequirePermission("monitor.manage"), views.UpdateHealthGradeConfigView)
	v1.POST("/monitor/config/health-grade/reset", middleware.RequirePermission("monitor.manage"), views.ResetHealthGradeConfigView)
	v1.GET("/monitor/config/rules", middleware.RequirePermission("monitor.manage"), views.GetRuleListView)
	v1.POST("/monitor/config/rules", middleware.RequirePermission("monitor.manage"), views.CreateRuleView)
	v1.PUT("/monitor/config/rules/:id", middleware.RequirePermission("monitor.manage"), views.UpdateRuleView)
	v1.DELETE("/monitor/config/rules/:id", middleware.RequirePermission("monitor.manage"), views.DeleteRuleView)

	// 采集健康度汇总：页面顶部提示条与"为什么没有数据"的答案来源
	v1.GET("/monitor/status", middleware.RequirePermission("monitor.view"), views.GetMonitorStatusView)
	// 立即采集：新接入实例不必干等下一个采集周期
	v1.POST("/monitor/instances/:instance_id/collect", middleware.RequirePermission("monitor.manage"), views.CollectInstanceNowView)
	// 明细弹窗刷新：同步采集一次并返回最新指标值（查看场景，权限放宽到 monitor.view）
	v1.POST("/monitor/detail/refresh", middleware.RequirePermission("monitor.view"), views.RefreshDetailModalView)
	// 内置指标清单（原指标字典接口）：指标定义固定在代码里，只供指标模板勾选弹窗读取。
	// 原 PUT 写入接口随「指标字典」模块一并下线，不再允许运行时改写指标元信息。
	v1.GET("/monitor/metrics/catalog", middleware.RequirePermission("monitor.view"), views.GetMetricCatalogView)
	// 大屏告警人工处置：认领工单（ack）/ 忽略（ignore）/ 取消忽略（unignore）
	v1.PUT("/monitor/alerts/action", middleware.RequirePermission("monitor.manage"), views.UpdateAlertActionView)
	// 告警中心：告警事件检索（含已恢复与被忽略）。看告警只需 monitor.view
	v1.GET("/monitor/alerts", middleware.RequirePermission("monitor.view"), views.GetAlertCenterView)
	// 告警概览统计：今日已恢复 + 近 7 天趋势（概览 Tab 专用；固定取景框，不开放自定义区间）
	v1.GET("/monitor/alerts/stats", middleware.RequirePermission("monitor.view"), views.GetAlertStatsView)
	// 告警概览「告警实例总数」下钻：触发告警的 DB 实例列表（与列表同口径）
	v1.GET("/monitor/alerts/instances", middleware.RequirePermission("monitor.view"), views.GetAlertCenterInstancesView)
	// 大屏「近 7 天告警趋势」卡：预设窗口内每段新增告警数（时间与页面顶部范围解耦）
	v1.GET("/monitor/alerts/trend", middleware.RequirePermission("monitor.view"), views.GetAlertTrendView)

	// 通知渠道：告警消息的出口。渠道实例可多个（多个钉钉群/多套 SMTP），敏感字段加密存储、
	// 接口只回显掩码。测试接口发一条样例消息，用于保存后自检。
	v1.POST("/monitor/notify/templates/preview", middleware.RequirePermission("monitor.manage"), views.PreviewNotifyMessageTemplateView)
	v1.GET("/monitor/notify/channels", middleware.RequirePermission("monitor.view"), views.GetNotifyChannelsView)
	v1.GET("/monitor/notify/channels/brief", middleware.RequirePermission("monitor.view"), views.GetNotifyChannelsBriefView)
	v1.POST("/monitor/notify/channels", middleware.RequirePermission("monitor.manage"), views.SaveNotifyChannelView)
	v1.PUT("/monitor/notify/channels/:id", middleware.RequirePermission("monitor.manage"), views.SaveNotifyChannelView)
	v1.DELETE("/monitor/notify/channels/:id", middleware.RequirePermission("monitor.manage"), views.DeleteNotifyChannelView)
	v1.POST("/monitor/notify/channels/:id/test", middleware.RequirePermission("monitor.manage"), views.TestNotifyChannelView)

	// 平台默认通知配置：只放全局兜底值（通知地址前缀 + 邮件默认 SMTP 凭据）。
	// 邮件渠道实例把 host/port/username/password 留空时继承这里的凭据。
	v1.GET("/monitor/notify/defaults", middleware.RequirePermission("monitor.view"), views.GetNotifyDefaultsView)
	v1.PUT("/monitor/notify/defaults", middleware.RequirePermission("monitor.manage"), views.SaveNotifyDefaultsView)

	// 通知策略：把「什么告警」路由到「哪些渠道 + 哪些接收人」。
	// 注意静态段 brief / preview 必须注册在带路径参数的路由之前（与采集策略段同款顺序）。
	v1.GET("/monitor/notify/policies", middleware.RequirePermission("monitor.view"), views.GetNotifyPoliciesView)
	v1.POST("/monitor/notify/policies", middleware.RequirePermission("monitor.manage"), views.SaveNotifyPolicyView)
	v1.PUT("/monitor/notify/policies/:id", middleware.RequirePermission("monitor.manage"), views.SaveNotifyPolicyView)
	v1.DELETE("/monitor/notify/policies/:id", middleware.RequirePermission("monitor.manage"), views.DeleteNotifyPolicyView)
	v1.POST("/monitor/notify/policies/preview", middleware.RequirePermission("monitor.view"), views.PreviewNotifyPolicyView)
	v1.POST("/monitor/notify/policies/:id/test", middleware.RequirePermission("monitor.manage"), views.TestNotifyPolicyView)

	// 接收人选择器数据源（组织树 + 用户），以及投递记录与重发
	v1.GET("/monitor/notify/subjects/tree", middleware.RequirePermission("monitor.view"), views.GetNotifySubjectTreeView)
	v1.GET("/monitor/notify/tasks", middleware.RequirePermission("monitor.view"), views.GetNotifyTasksView)
	v1.POST("/monitor/notify/tasks/:id/retry", middleware.RequirePermission("monitor.manage"), views.RetryNotifyTaskView)

	// 负载快照：AWR 式实例负载诊断报告。静态段 instances 注册在带 :id 的路由之前。
	//
	// 【2026-10-24 权限归位】页面唯一入口在「性能分析 → 负载快照」（monitor 菜单无此页面），
	// 因此接口鉴权统一到 performance.*：查看用 performance.view、生成/删除用
	// performance.manage，与前端按钮显隐同一口径，不再混用 monitor.*。
	// 数据访问授权不参与判定——性能分析可见性只由功能权限决定。
	v1.GET("/monitor/snapshot/instances", middleware.RequirePermission("performance.view"), views.GetLoadSnapshotInstancesView)
	v1.GET("/monitor/snapshots", middleware.RequirePermission("performance.view"), views.GetLoadSnapshotListView)
	v1.POST("/monitor/snapshots", middleware.RequirePermission("performance.manage"), views.CreateLoadSnapshotView)
	v1.GET("/monitor/snapshots/:id", middleware.RequirePermission("performance.view"), views.GetLoadSnapshotDetailView)
	v1.DELETE("/monitor/snapshots/:id", middleware.RequirePermission("performance.manage"), views.DeleteLoadSnapshotView)

	// 指标模板：按数据库类型组织的采集指标集合；实例通过 metrics_template 绑定
	v1.GET("/monitor/templates", middleware.RequirePermission("monitor.view"), views.GetMetricTemplateListView)
	v1.GET("/monitor/templates/:id", middleware.RequirePermission("monitor.view"), views.GetMetricTemplateDetailView)
	v1.POST("/monitor/templates", middleware.RequirePermission("monitor.manage"), views.CreateMetricTemplateView)
	v1.PUT("/monitor/templates/:id", middleware.RequirePermission("monitor.manage"), views.UpdateMetricTemplateView)
	v1.DELETE("/monitor/templates/:id", middleware.RequirePermission("monitor.manage"), views.DeleteMetricTemplateView)
	v1.PUT("/monitor/templates/:id/instances", middleware.RequirePermission("monitor.manage"), views.BindMetricTemplateInstancesView)

	// 采集策略：模板 - 实例 两级配置（"怎么采"：周期/保留/超时/重试/来源 + 指标模板绑定）。
	// 与上面的指标模板（"采哪些指标"）是两套，实例挂的是采集策略模板。
	v1.GET("/monitor/strategy/templates", middleware.RequirePermission("monitor.view"), views.GetStrategyTemplateListView)
	v1.GET("/monitor/strategy/templates/:id", middleware.RequirePermission("monitor.view"), views.GetStrategyTemplateDetailView)
	v1.POST("/monitor/strategy/templates", middleware.RequirePermission("monitor.manage"), views.CreateStrategyTemplateView)
	v1.PUT("/monitor/strategy/templates/:id", middleware.RequirePermission("monitor.manage"), views.UpdateStrategyTemplateView)
	v1.DELETE("/monitor/strategy/templates/:id", middleware.RequirePermission("monitor.manage"), views.DeleteStrategyTemplateView)
	// 挂载 / 解绑：实例侧唯一的策略配置动作
	v1.PUT("/monitor/strategy/templates/:id/instances", middleware.RequirePermission("monitor.manage"), views.BindStrategyInstancesView)
	v1.PUT("/monitor/strategy/instances/unbind", middleware.RequirePermission("monitor.manage"), views.UnbindStrategyInstancesView)
	// 实例级覆盖与生效策略查询
	v1.GET("/monitor/strategy/instances/:instance_id", middleware.RequirePermission("monitor.view"), views.GetInstanceStrategyView)
	v1.PUT("/monitor/strategy/instances/:instance_id/overrides", middleware.RequirePermission("monitor.manage"), views.SaveInstanceOverridesView)

	// 告警策略：模板 - 实例 两级配置。每类型一个内置默认模板兜底，实例 1:1 挂载，
	// 规则按 (template_id, metric_key) 覆盖式存储。注意 instances/unbind 静态段必须
	// 注册在带路径参数的路由之前（与采集策略段同款顺序）。
	v1.GET("/monitor/alert/templates", middleware.RequirePermission("monitor.view"), views.GetAlertTemplateListView)
	v1.GET("/monitor/alert/templates/:id", middleware.RequirePermission("monitor.view"), views.GetAlertTemplateDetailView)
	v1.POST("/monitor/alert/templates", middleware.RequirePermission("monitor.manage"), views.CreateAlertTemplateView)
	v1.PUT("/monitor/alert/templates/:id", middleware.RequirePermission("monitor.manage"), views.UpdateAlertTemplateView)
	v1.DELETE("/monitor/alert/templates/:id", middleware.RequirePermission("monitor.manage"), views.DeleteAlertTemplateView)
	v1.PUT("/monitor/alert/templates/:id/rules", middleware.RequirePermission("monitor.manage"), views.SaveAlertTemplateRulesView)
	v1.PUT("/monitor/alert/templates/:id/instances", middleware.RequirePermission("monitor.manage"), views.BindAlertInstancesView)
	v1.PUT("/monitor/alert/instances/unbind", middleware.RequirePermission("monitor.manage"), views.UnbindAlertInstancesView)
}
