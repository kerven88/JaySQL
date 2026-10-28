package views

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/simajiong/jaysql/internal/ai/forms"
	aiServices "github.com/simajiong/jaysql/internal/ai/services"
	backupServices "github.com/simajiong/jaysql/internal/backup/services"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// aiCreateBackupTimeout 物理备份创建会探测备份节点/SSH，沿用备份任务创建页的超时口径
const aiCreateBackupTimeout = 170 * time.Second

// AICreateBackupConfirmView AI 对话：用户在备份草稿卡片上点「确认创建」后建任务。
//
// 与创建工单同一口径：创建人取当前登录会话（JWT），页面与模型都无权指定。
// 创建本身复用 backup 模块的 CreateBackupTaskService（预检查/节点解析/调度注册全沿用）。
// 物理备份的准备动作可能耗时较长（探测备份节点、SSH、pgBackRest stanza），
// 因此沿用备份任务创建页的超时口径，并把「备份程序准备」类错误原样透出。
func AICreateBackupConfirmView(c *gin.Context) {
	username, ok := middleware.GetUserNameFromJWT(c)
	if !ok {
		response.Fail(c, "认证信息无效")
		return
	}
	var form forms.AICreateBackupConfirmForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), aiCreateBackupTimeout)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)

	service := aiServices.ConfirmCreateBackupService{Form: &form, Username: username, C: c}
	result, err := service.Run()
	if ctx.Err() != nil && err == nil {
		err = fmt.Errorf("后端处理超过 %d 秒上限，已强制终止，请稍后重试", int(aiCreateBackupTimeout.Seconds()))
	}
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "backup_task",
			ModuleName:     "备份任务",
			ActionCode:     "create",
			ActionName:     "创建备份任务",
			ActionResult:   "fail",
			ResourceType:   "backup_task",
			ResourceName:   form.Name,
			Summary:        "通过 AI 助手创建备份任务失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, backupFailureMessage(err))
		return
	}

	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "backup_task",
		ModuleName:     "备份任务",
		ActionCode:     "create",
		ActionName:     "创建备份任务",
		ActionResult:   "success",
		ResourceType:   "backup_task",
		ResourceID:     result.TaskID,
		ResourceName:   result.TaskName,
		Summary:        "通过 AI 助手创建备份任务，已纳入调度",
		RequestPayload: form,
	})
	response.Success(c, result, "备份任务已创建，已纳入调度")
}

// backupFailureMessage 备份创建失败文案：与备份任务创建页同一口径，
// 「备份程序准备」这类结构化错误优先展示标题 + 处置建议，其余原样透出。
func backupFailureMessage(err error) string {
	if prepareErr, ok := backupServices.AsPhysicalBackupPrepareError(err); ok && prepareErr != nil {
		text := prepareErr.Title
		if prepareErr.Suggestion != "" {
			text += "；" + prepareErr.Suggestion
		}
		if text != "" {
			return text
		}
	}
	return err.Error()
}

// AICreateOrderConfirmView AI 对话：用户在工单草稿卡片上点「确认创建」后建单。
//
// 创建人取当前登录会话（JWT），页面与模型都无权指定——避免伪造他人身份建单。
// 建单后工单直接落「待规范预审」并已生成预审快照，用户不需要再做任何提交动作。
func AICreateOrderConfirmView(c *gin.Context) {
	username, ok := middleware.GetUserNameFromJWT(c)
	if !ok {
		response.Fail(c, "认证信息无效")
		return
	}
	var form forms.AICreateOrderConfirmForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}

	service := aiServices.ConfirmCreateOrderService{Form: &form, Username: username, C: c}
	result, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "submit",
			ActionName:     "提交工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceName:   form.Title,
			Summary:        "通过 AI 助手创建工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
			SQLText:        form.Content,
		})
		response.Fail(c, err.Error())
		return
	}

	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "submit",
		ActionName:     "提交工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceName:   form.Title,
		Summary:        "通过 AI 助手创建工单，已进入规范预审",
		RequestPayload: form,
		RelatedOrderID: result.OrderID,
		SQLText:        form.Content,
	})
	response.Success(c, result, "工单已创建，进入规范预审")
}

// AIChatCompletionView AI 对话：运行时真实模型调用。
// 返回 source=llm（真实模型）或 source=rule_engine（已降级，前端回落本地规则）。
func AIChatCompletionView(c *gin.Context) {
	var form forms.LlmChatCompletionForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	result, err := aiServices.ChatCompletion(c.Request.Context(), &form, aiServices.CurrentUsername(c))
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "ok")
}

// AIChatCancelView AI 对话：用户点「取消」时主动终止服务端仍在跑的这次编排。
//
// 与「客户端断开连接」互补而非替代：断开依赖运行时/代理把连接关闭翻译成 context 取消，
// 中间有缓冲层时可能滞后；本接口直接按 run_id 投递取消信号，不依赖长连接状态。
// 取消幂等：运行已结束、或 run_id 不属于调用者，都返回成功态，不暴露他人运行是否存在。
func AIChatCancelView(c *gin.Context) {
	var form forms.LlmChatCancelForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	canceled := aiServices.CancelChatRun(form.RunID, aiServices.CurrentUsername(c))
	response.Success(c, map[string]any{"canceled": canceled}, "ok")
}

// AIChatStreamView AI 对话流式（SSE）：直答路由逐字下发，工具路由整体下发。
// 事件协议见 services/chat_stream.go。
func AIChatStreamView(c *gin.Context) {
	var form forms.LlmChatCompletionForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}

	header := c.Writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// 关键：让 Nginx 不要缓冲本响应，否则前端拿到的仍是一整段（无需改 nginx 配置）
	header.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, _ := c.Writer.(http.Flusher)
	emit := func(event string, payload any) error {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, body); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	username := aiServices.CurrentUsername(c)
	if err := aiServices.ChatCompletionStream(c.Request.Context(), &form, username, emit); err != nil {
		// 响应头已发出，错误只能通过事件告知（前端据 error 事件展示不可用卡片）
		_ = emit(aiServices.ChatStreamEventError, map[string]string{"reason": err.Error()})
	}
}

// ============================================================
// 大模型配置接入
// ============================================================

// LlmVendorListView 厂商模板列表
// all=1 返回含停用项的全部模板（管理页）；默认仅返回启用项（接入向导卡片墙）
func LlmVendorListView(c *gin.Context) {
	var query forms.LlmVendorListQueryForm
	if err := c.ShouldBindQuery(&query); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	rows, err := aiServices.ListLlmVendorTemplates(!query.All)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows, "ok")
}

// LlmVendorTemplateCreateView 新增自定义厂商模板
func LlmVendorTemplateCreateView(c *gin.Context) {
	var form forms.LlmVendorTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	record, err := aiServices.CreateLlmVendorTemplate(&form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "厂商模板已创建")
}

// LlmVendorTemplateUpdateView 更新厂商模板（内置模板仅允许改启停/排序/文案）
func LlmVendorTemplateUpdateView(c *gin.Context) {
	var form forms.LlmVendorTemplateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	record, err := aiServices.UpdateLlmVendorTemplate(form.Vendor, &form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "厂商模板已更新")
}

// LlmVendorTemplateDeleteView 删除自定义厂商模板
func LlmVendorTemplateDeleteView(c *gin.Context) {
	var form forms.LlmVendorNameForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := aiServices.DeleteLlmVendorTemplate(form.Vendor); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "厂商模板已删除")
}

// LlmProviderListView 服务商配置列表
func LlmProviderListView(c *gin.Context) {
	views, err := aiServices.ListLlmProviders(c)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, views, "ok")
}

// LlmProviderDetailView 服务商详情（含最近调用记录）
func LlmProviderDetailView(c *gin.Context) {
	var query forms.LlmProviderIDForm
	if err := c.ShouldBindQuery(&query); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	detail, logs, err := aiServices.GetLlmProviderDetail(query.ProviderID)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"provider": detail, "recent_logs": logs}, "ok")
}

// LlmProviderCreateView 新增服务商配置（凭证加密入库）
func LlmProviderCreateView(c *gin.Context) {
	var form forms.LlmProviderForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	view, err := aiServices.CreateLlmProvider(c, &form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, view, "服务商配置已创建，请执行测试连接")
}

// LlmProviderUpdateView 更新服务商配置（api_key 留空表示不修改）
func LlmProviderUpdateView(c *gin.Context) {
	var form forms.LlmProviderForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if form.ProviderID == "" {
		response.ValidateFail(c, "provider_id 不能为空")
		return
	}
	view, err := aiServices.UpdateLlmProvider(c, &form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, view, "服务商配置已更新")
}

// LlmProviderDeleteView 删除服务商配置
func LlmProviderDeleteView(c *gin.Context) {
	var form forms.LlmProviderIDForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := aiServices.DeleteLlmProvider(form.ProviderID); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "服务商配置已删除")
}

// LlmProviderTestView 不落库的即测即用（接入向导第三步）
func LlmProviderTestView(c *gin.Context) {
	var form forms.LlmProviderTestForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	result, err := aiServices.TestLlmProviderConnection(&form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "测试完成")
}

// LlmProviderValidateView 对已落库配置重新验证
func LlmProviderValidateView(c *gin.Context) {
	var form forms.LlmProviderIDForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	result, err := aiServices.ValidateLlmProvider(form.ProviderID)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "验证完成")
}

// LlmProviderEnableView 启用（要求已验证通过）
func LlmProviderEnableView(c *gin.Context) {
	setProviderEnabled(c, true)
}

// LlmProviderDisableView 停用
func LlmProviderDisableView(c *gin.Context) {
	setProviderEnabled(c, false)
}

func setProviderEnabled(c *gin.Context, enabled bool) {
	var form forms.LlmProviderIDForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	view, err := aiServices.SetLlmProviderEnabled(form.ProviderID, enabled)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	msg := "已停用"
	if enabled {
		msg = "已启用"
	}
	response.Success(c, view, msg)
}

// LlmProviderSetDefaultView 设为场景默认服务商
func LlmProviderSetDefaultView(c *gin.Context) {
	var form forms.LlmProviderIDForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := aiServices.SetLlmProviderDefault(form.ProviderID); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已设为默认服务商")
}

// LlmModelListView 模型列表（分页）
func LlmModelListView(c *gin.Context) {
	var query forms.LlmModelQueryForm
	if err := c.ShouldBindQuery(&query); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	rows, total, err := aiServices.ListLlmModels(&query)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, rows)
}

// LlmModelCreateView 手工添加模型
func LlmModelCreateView(c *gin.Context) {
	var form forms.LlmModelForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	record, err := aiServices.CreateLlmModel(&form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "模型已添加")
}

// LlmModelUpdateView 更新模型配置
func LlmModelUpdateView(c *gin.Context) {
	var form forms.LlmModelForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	record, err := aiServices.UpdateLlmModel(&form)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "模型已更新")
}

// LlmModelDeleteView 删除模型（被默认引用时拒绝）
func LlmModelDeleteView(c *gin.Context) {
	var form forms.LlmModelUIDForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := aiServices.DeleteLlmModel(form.ModelUID); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "模型已删除")
}

// LlmModelSetDefaultView 设为所属服务商的默认模型
func LlmModelSetDefaultView(c *gin.Context) {
	var form forms.LlmModelUIDForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	record, err := aiServices.SetDefaultLlmModel(form.ModelUID)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "已设为默认模型")
}
