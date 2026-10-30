package views

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/monitor/services"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"
)

// ============================================================================
// 通知渠道 / 通知策略 / 投递记录 接口层
//
// 权限约定：
//   - 渠道与策略的读写需要 monitor.manage（渠道含 webhook 与密钥，比看板敏感）
//   - 投递记录与接收人树只需要 monitor.view
// ============================================================================

// notifyOperator 取当前登录用户名（审计字段）。
//
// 注意：loginUserName 只在登录请求链路里被 LoginCheckMiddleware 写入，
// 常规 JWT 认证请求里永远是空串——必须从 JWT claims 里取（与 users/orders/das 同口径），
// 否则 created_by / updated_by 这类审计字段会静默落成空字符串。
func notifyOperator(c *gin.Context) string {
	if username, ok := middleware.GetUserNameFromJWT(c); ok && strings.TrimSpace(username) != "" {
		return strings.TrimSpace(username)
	}
	return strings.TrimSpace(c.GetString("loginUserName"))
}

// GetNotifyChannelsView 渠道实例列表 + 渠道类型元数据。
func GetNotifyChannelsView(c *gin.Context) {
	items, err := services.ListNotifyChannels()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{
		"channels": items,
		"kinds":    services.NotifyChannelKinds(),
	}, "success")
}

// SaveNotifyChannelView 新建 / 更新渠道实例。
func SaveNotifyChannelView(c *gin.Context) {
	var form services.NotifyChannelForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	// 路径参数优先：PUT /channels/:id
	if raw := c.Param("id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.ValidateFail(c, "渠道ID格式错误")
			return
		}
		form.ID = id
	}
	id, err := services.SaveNotifyChannel(form, notifyOperator(c))
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"id": id}, "渠道已保存")
}

// DeleteNotifyChannelView 删除渠道实例（被策略引用时拒绝）。
func DeleteNotifyChannelView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "渠道ID格式错误")
		return
	}
	if err := services.DeleteNotifyChannel(id); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已删除")
}

// TestNotifyChannelView 立即测试渠道（发一条样例消息）。
func TestNotifyChannelView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "渠道ID格式错误")
		return
	}
	result, sendErr := services.TestNotifyChannel(id)
	if sendErr != nil {
		response.Fail(c, sendErr.Error())
		return
	}
	response.Success(c, map[string]any{
		"accepted": result.Accepted,
		"code":     result.Code,
	}, "测试消息已发送，请到目标渠道确认")
}

// GetNotifyChannelsBriefView 渠道精简列表（策略编辑器选择用）。
func GetNotifyChannelsBriefView(c *gin.Context) {
	items, err := services.ListNotifyChannelsRaw()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"channels": items}, "success")
}

// GetNotifyPoliciesView 策略列表。
func GetNotifyPoliciesView(c *gin.Context) {
	items, err := services.ListNotifyPolicies()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"policies": items}, "success")
}

// SaveNotifyPolicyView 新建 / 更新策略。
func SaveNotifyPolicyView(c *gin.Context) {
	var form services.NotifyPolicyForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if raw := c.Param("id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.ValidateFail(c, "策略ID格式错误")
			return
		}
		form.ID = id
	}
	id, err := services.SaveNotifyPolicy(form, notifyOperator(c))
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"id": id}, "策略已保存")
}

// DeleteNotifyPolicyView 删除策略。
func DeleteNotifyPolicyView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "策略ID格式错误")
		return
	}
	if err := services.DeleteNotifyPolicy(id); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已删除")
}

// PreviewNotifyPolicyView 保存前预览：命中多少实例 / 多少规则。
func PreviewNotifyPolicyView(c *gin.Context) {
	var form services.NotifyPolicyForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	data, err := services.PreviewNotifyPolicy(form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// TestNotifyPolicyView 策略连通性测试：用一条样例告警跑完整的派发链路。
func TestNotifyPolicyView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "策略ID格式错误")
		return
	}
	data, testErr := services.TestNotifyPolicy(id)
	if testErr != nil {
		response.Fail(c, testErr.Error())
		return
	}
	response.Success(c, data, "测试告警已入队，约 15 秒内送达")
}

// GetNotifyDefaultsView 平台默认通知配置（通知地址前缀 + 邮件默认 SMTP 凭据）。
func GetNotifyDefaultsView(c *gin.Context) {
	data, err := services.GetNotifyDefaults()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// SaveNotifyDefaultsView 保存平台默认通知配置。
func SaveNotifyDefaultsView(c *gin.Context) {
	var form services.NotifyDefaultsForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := services.SaveNotifyDefaults(form); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已保存")
}

// GetNotifySubjectTreeView 接收人选择器数据源（组织树 + 用户）。
func GetNotifySubjectTreeView(c *gin.Context) {
	data, err := services.NotifySubjectTree()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetNotifyTasksView 投递记录查询。
func GetNotifyTasksView(c *gin.Context) {
	filter := services.NotifyTaskFilter{
		Fingerprint: c.Query("fingerprint"),
		InstanceID:  c.Query("instance_id"),
		Status:      c.Query("status"),
	}
	if raw := strings.TrimSpace(c.Query("event_id")); raw != "" {
		if value, err := strconv.ParseUint(raw, 10, 64); err == nil {
			filter.EventID = value
		}
	}
	if raw := strings.TrimSpace(c.Query("channel_id")); raw != "" {
		if value, err := strconv.ParseUint(raw, 10, 64); err == nil {
			filter.ChannelID = value
		}
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			filter.Limit = value
		}
	}
	items, err := services.ListNotifyTasks(filter)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"tasks": items}, "success")
}

// RetryNotifyTaskView 手动重发一条投递记录。
func RetryNotifyTaskView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "记录ID格式错误")
		return
	}
	if err := services.RetryNotifyTask(id); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已重新入队，将在下个投递周期发送")
}

// PreviewNotifyMessageTemplateView renders sample data only; it never sends messages.
func PreviewNotifyMessageTemplateView(c *gin.Context) {
	var form struct {
		Template string `json:"template"`
		Trigger  string `json:"trigger"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	preview, err := services.PreviewNotifyMessageTemplate(form.Template, form.Trigger)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, preview, "success")
}
