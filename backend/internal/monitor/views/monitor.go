package views

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	"github.com/simajiong/jaysql/internal/monitor/services"
	"github.com/simajiong/jaysql/pkg/response"
)

func GetDashboardView(c *gin.Context) {
	var form forms.GetDashboardForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}

	service := services.GetDashboardService{
		GetDashboardForm: &form,
		C:                c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetRiskOverviewView(c *gin.Context) {
	// 风险总览：全系统 screen_class=risk 的实例 + 每实例的原因明细（判定依据）。
	data, err := services.GetRiskOverviewService{C: c}.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetDetailView(c *gin.Context) {
	var form forms.GetDetailForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetDetailService{
		GetDetailForm: &form,
		C:             c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetMetricHistoryView 指标历史（趋势图）：按时间范围返回每个指标聚合后的序列。
// 页面「时间范围」下拉框切换时调用这里，曲线始终来自真实采集点。
// GetScrapeConfigView 采集频率配置：档位、当前值、派生结果、生效说明。
func GetScrapeConfigView(c *gin.Context) {
	response.Success(c, services.GetScrapeConfig(), "success")
}

// UpdateScrapeConfigView 保存采集频率。校验在 services.SaveScrapeSettings 里，
// 前端拦一次、后端再拦一次，绕过页面的调用同样拦得住。
func UpdateScrapeConfigView(c *gin.Context) {
	var form forms.UpdateScrapeSettingsForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	cfg, err := services.SaveScrapeSettings(form.IntervalSeconds, form.AllowCustom, services.OperatorFromContext(c))
	if err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	response.Success(c, cfg, "已保存，JaySQL 侧立即生效")
}

// GetHealthGradeConfigView 健康状态判定配置（原「健康分级」，现由告警模版规则驱动）：
// 故障/风险阈值 + 各 P 级扣分，页面上的"什么算健康/风险/故障"全部来自这一份。
func GetHealthGradeConfigView(c *gin.Context) {
	response.Success(c, services.GetHealthStateConfig(), "success")
}

// UpdateHealthGradeConfigView 保存健康状态判定配置。
// 校验在 services.SaveHealthStateConfig 里：前端拦一次、后端再拦一次。
func UpdateHealthGradeConfigView(c *gin.Context) {
	var form forms.UpdateHealthGradeConfigForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	cfg, err := services.SaveHealthStateConfig(form.Config, services.OperatorFromContext(c))
	if err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	response.Success(c, cfg, "已保存，立即生效")
}

// ResetHealthGradeConfigView 丢弃用户配置，回到内置默认判定策略。
func ResetHealthGradeConfigView(c *gin.Context) {
	cfg, err := services.ResetHealthStateConfig(services.OperatorFromContext(c))
	if err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	response.Success(c, cfg, "已恢复为内置默认判定策略")
}

func GetMetricHistoryView(c *gin.Context) {
	var form forms.GetMetricHistoryForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetMetricHistoryService{
		GetMetricHistoryForm: &form,
		C:                    c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetTargetConfigsView(c *gin.Context) {
	var form forms.MonitorListFilter
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetTargetConfigsService{
		MonitorListFilter: &form,
		C:                 c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func UpdateTargetConfigView(c *gin.Context) {
	var form forms.UpdateTargetConfigForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpsertTargetConfigService{
		InstanceID:             c.Param("instance_id"),
		UpdateTargetConfigForm: &form,
		C:                      c,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func GetRuleListView(c *gin.Context) {
	var form forms.GetRuleListForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetRuleListService{
		GetRuleListForm: &form,
		C:               c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateRuleView(c *gin.Context) {
	var form forms.SaveRuleForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateRuleService{
		SaveRuleForm: &form,
		C:            c,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func UpdateRuleView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "规则ID格式错误")
		return
	}
	var form forms.SaveRuleForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateRuleService{
		ID:           id,
		SaveRuleForm: &form,
		C:            c,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func DeleteRuleView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "规则ID格式错误")
		return
	}
	service := services.DeleteRuleService{
		ID: id,
		C:  c,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

// GetMonitorStatusView 采集健康度汇总：回答"数据采到没有、为什么没有"。
func GetMonitorStatusView(c *gin.Context) {
	service := services.GetMonitorStatusService{C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// CollectInstanceNowView 立即采集单个实例（同步返回采集结果）。
func CollectInstanceNowView(c *gin.Context) {
	service := services.CollectInstanceNowService{
		InstanceID: c.Param("instance_id"),
		C:          c,
		Username:   services.OperatorFromContext(c),
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "采集完成")
}

// RefreshDetailModalView 监控明细弹窗「刷新」：对当前实例做一次同步采集，
// 返回弹窗所需的最新数据（实例全部指标的最新值 + 质量 + 采集时间）。
//
// 与"立即采集"（CollectInstanceNowView）共用 CollectInstanceNowService ——
// 同一套 collectAndPersist/persistSamples 口径落库，手采点同样对齐槽位网格；
// 区别只在权限：明细页是查看场景，放宽到 monitor.view，让只读用户也能刷新。
// instance_id 同时支持 query 与 JSON body 两种传法（前端走 body）。
func RefreshDetailModalView(c *gin.Context) {
	instanceID := strings.TrimSpace(c.Query("instance_id"))
	if instanceID == "" {
		var body struct {
			InstanceID string `json:"instance_id"`
		}
		if c.Request != nil && c.Request.Body != nil {
			if raw, err := c.GetRawData(); err == nil && len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
				instanceID = strings.TrimSpace(body.InstanceID)
			}
		}
	}
	if instanceID == "" {
		response.Fail(c, "instance_id 不能为空")
		return
	}
	service := services.CollectInstanceNowService{
		InstanceID: instanceID,
		C:          c,
		Username:   services.OperatorFromContext(c),
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "刷新完成")
}

// GetMetricCatalogView 读取内置指标清单（可按分类/引擎过滤）。
//
// 注意：接口路径保留 /monitor/metrics/catalog，但它不再是「指标字典」——
// 字典模块已下线，这里只输出代码内置的指标定义，供指标模板勾选弹窗使用。
func GetMetricCatalogView(c *gin.Context) {
	service := services.GetMetricCatalogService{
		Category: c.Query("category"),
		Engine:   c.Query("engine"),
		Layout:   c.Query("layout") == "1",
		C:        c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// UpdateAlertActionView 大屏告警人工处置（认领工单 / 忽略 / 取消忽略）。
func UpdateAlertActionView(c *gin.Context) {
	var form forms.AlertActionForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateAlertActionService{
		Fingerprint: form.Fingerprint,
		Action:      form.Action,
		C:           c,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

// GetAlertCenterView 告警中心：告警事件检索（含已恢复与被忽略的）。
//
// 权限刻意放宽到 monitor.view：值班同学"看告警、认领告警"不该被挡在
// monitor.manage 之外 —— 改配置才需要 manage（见告警配置页）。
func GetAlertCenterView(c *gin.Context) {
	var form forms.GetAlertCenterForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetAlertCenterService{
		GetAlertCenterForm: &form,
		C:                  c,
	}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

// GetAlertCenterInstancesView 告警概览「告警实例总数」下钻：触发告警的 DB 实例列表。
// 权限与告警列表一致（monitor.view）：只读聚合，不涉及处置动作。
func GetAlertCenterInstancesView(c *gin.Context) {
	var form forms.GetAlertCenterForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetAlertCenterInstancesService{
		GetAlertCenterForm: &form,
		C:                  c,
	}
	items, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"items": items, "total": len(items)}, "success")
}

// GetAlertStatsView 告警概览统计：今日已恢复 + 近 7 天趋势（概览 Tab 专用）。
func GetAlertStatsView(c *gin.Context) {
	var form forms.GetAlertStatsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetAlertStatsService{
		GetAlertStatsForm: &form,
		C:                 c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetAlertTrendView 大屏「近 7 天告警趋势」卡：预设窗口内每段新增告警数。
// 时间与页面顶部范围解耦（全局管实时、本卡管中长期），权限同大屏 monitor.view。
func GetAlertTrendView(c *gin.Context) {
	var form forms.GetAlertTrendForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetAlertTrendService{
		GetAlertTrendForm: &form,
		C:                 c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// ============================================================================
// 指标模板：按数据库类型组织的采集指标集合
// ============================================================================

// GetMetricTemplateListView 模板列表（含每条模板的指标数、实际绑定实例数）。
func GetMetricTemplateListView(c *gin.Context) {
	var form forms.GetMetricTemplateListForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetMetricTemplateListService{DBType: form.DBType, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetMetricTemplateDetailView 模板详情：已勾选指标 + 该类型全部可选指标（勾选界面用）。
func GetMetricTemplateDetailView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	service := services.GetMetricTemplateDetailService{ID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateMetricTemplateView(c *gin.Context) {
	var form forms.SaveMetricTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateMetricTemplateService{SaveMetricTemplateForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func UpdateMetricTemplateView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.UpdateMetricTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateMetricTemplateService{ID: id, UpdateMetricTemplateForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func DeleteMetricTemplateView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	service := services.DeleteMetricTemplateService{ID: id, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

// BindMetricTemplateInstancesView 批量绑定实例到该模板 / 回落到默认模板。
func BindMetricTemplateInstancesView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.BindTemplateInstancesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.BindTemplateInstancesService{ID: id, BindTemplateInstancesForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// ---------------------------------------------------------------------------
// 采集策略：模板 - 实例 两级配置
//
// 与指标模板是两套东西，路由也分开（/monitor/strategy/*）：
// 指标模板管"采哪些指标"，采集策略模板管"怎么采"。
// ---------------------------------------------------------------------------

func GetStrategyTemplateListView(c *gin.Context) {
	var form forms.GetStrategyTemplateListForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetStrategyTemplateListService{DBType: form.DBType, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetStrategyTemplateDetailView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	service := services.GetStrategyTemplateDetailService{ID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateStrategyTemplateView(c *gin.Context) {
	var form forms.SaveStrategyTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateStrategyTemplateService{SaveStrategyTemplateForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func UpdateStrategyTemplateView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.UpdateStrategyTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateStrategyTemplateService{ID: id, UpdateStrategyTemplateForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func DeleteStrategyTemplateView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	service := services.DeleteStrategyTemplateService{ID: id, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

// BindStrategyInstancesView 把一批实例挂载到该模板（切换模板 = 先解绑再挂载，
// 这里一次搞定：直接覆盖 strategy_template_id 并重算生效策略）。
func BindStrategyInstancesView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.BindStrategyInstancesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.BindStrategyInstancesService{ID: id, BindStrategyInstancesForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// UnbindStrategyInstancesView 解绑实例：置空挂载并立即回落到该类型的默认模板。
// 解绑不等于"停止采集"，这一点在返回消息里说清楚，避免误操作造成监控盲区。
func UnbindStrategyInstancesView(c *gin.Context) {
	var form forms.UnbindStrategyInstancesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UnbindStrategyInstancesService{UnbindStrategyInstancesForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "已解绑，实例按默认模板采集")
}

// GetInstanceStrategyView 查看某个实例当前生效的完整采集策略（含每一项来自哪里）。
// 页面上的"这个周期为什么是这个值"只能靠它回答。
func GetInstanceStrategyView(c *gin.Context) {
	var form forms.GetInstanceStrategyForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetInstanceStrategyService{InstanceID: form.InstanceID, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// SaveInstanceOverridesView 保存实例级策略覆盖。
func SaveInstanceOverridesView(c *gin.Context) {
	var form forms.SaveInstanceOverridesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if form.Overrides == nil {
		form.Overrides = map[string]any{}
	}
	instanceID := strings.TrimSpace(c.Param("instance_id"))
	if instanceID == "" {
		response.ValidateFail(c, "实例ID不能为空")
		return
	}
	service := services.SaveInstanceOverridesService{InstanceID: instanceID, SaveInstanceOverridesForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// ============================================================================
// 告警策略模板（默认模板兜底 + 实例 1:1 挂载 + 规则覆盖式存储）
// ============================================================================

func GetAlertTemplateListView(c *gin.Context) {
	var form forms.GetAlertTemplateListForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetAlertTemplateListService{DBType: form.DBType, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetAlertTemplateDetailView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	service := services.GetAlertTemplateDetailService{ID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateAlertTemplateView(c *gin.Context) {
	var form forms.SaveAlertTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateAlertTemplateService{SaveAlertTemplateForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func UpdateAlertTemplateView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.UpdateAlertTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateAlertTemplateService{ID: id, UpdateAlertTemplateForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// DeleteAlertTemplateView 删除自定义模板：内置默认不可删，有挂载需先解绑。
func DeleteAlertTemplateView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	service := services.DeleteAlertTemplateService{ID: id, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func BindAlertInstancesView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.BindAlertInstancesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.BindAlertInstancesService{ID: id, BindAlertInstancesForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// UnbindAlertInstancesView 解绑实例：置空挂载，告警评估自动回落默认模板。
func UnbindAlertInstancesView(c *gin.Context) {
	var form forms.UnbindAlertInstancesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UnbindAlertInstancesService{UnbindAlertInstancesForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "已解绑，实例按默认模板告警")
}

// SaveAlertTemplateRulesView 保存模板规则集。
//
// 新口径按监控项分组提交（metrics: 每个指标带 1~4 个 P0–P3 档位）：
// 同 (指标, 等级) 覆盖（rule_id 不变）、新档插入。
// 删除范围默认只覆盖"本次提交出现过的指标"，避免部分提交静默抹掉其他指标的规则；
// 需要整模板覆盖时显式传 replace_all=true（配置页一次提交全部监控项时走这条）。
// 仍接受老的平铺 rules 入参（按指标聚合后走同一套校验），老口径视为全量覆盖。
func SaveAlertTemplateRulesView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "模板ID格式错误")
		return
	}
	var form forms.SaveAlertTemplateRulesForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.SaveAlertTemplateRulesService{
		ID:              id,
		Rules:           form.Rules,
		ReplaceAll:      form.ReplaceAll,
		MetricsProvided: form.Metrics != nil,
		C:               c,
	}
	if form.Metrics != nil {
		service.Metrics = *form.Metrics
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
