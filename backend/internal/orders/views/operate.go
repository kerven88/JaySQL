package views

import (
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/services"

	"github.com/gin-gonic/gin"
)

// 提交审批
func SubmitApprovalView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.SubmitApprovalForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.SubmitApprovalService{
		SubmitApprovalForm: &form,
		C:                  c,
		Username:           username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "submit_approval",
			ActionName:     "提交审批",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "提交工单审批失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "submit_approval",
		ActionName:     "提交审批",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "提交工单审批",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 审批
func ApprovalOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.ApprovalOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ApprovalOrderService{
		ApprovalOrderForm: &form,
		C:                 c,
		Username:          username,
	}
	if err := service.Run(); err != nil {
		result := "fail"
		if form.Status == "REJECTED" {
			result = "rejected"
		}
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "approval",
			ActionName:     "审批工单",
			ActionResult:   result,
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "审批工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	result := "success"
	actionName := "审批通过"
	if form.Status == "REJECTED" {
		result = "rejected"
		actionName = "审批驳回"
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "approval",
		ActionName:     actionName,
		ActionResult:   result,
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        actionName + "工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 转交工单给其他执行人
func TransferOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.TransferOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.TransferOrderService{
		TransferOrderForm: &form,
		C:                 c,
		Username:          username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "transfer",
			ActionName:     "转交工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "转交工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "transfer",
		ActionName:     "转交工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "转交数据库工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 终止工单
func TerminateOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.TerminateOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.TerminateOrderService{
		TerminateOrderForm: &form,
		C:                  c,
		Username:           username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "terminate",
			ActionName:     "终止工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "终止工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "terminate",
		ActionName:     "终止工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "终止数据库工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 撤回工单（进入「已撤回」状态，可编辑后重新提交，工单 ID 不变）
func WithdrawOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.WithdrawOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.WithdrawOrderService{
		WithdrawOrderForm: &form,
		C:                 c,
		Username:          username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "withdraw",
			ActionName:     "撤回工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "撤回工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "withdraw",
		ActionName:     "撤回工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "撤回数据库工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 编辑已撤回工单内容（保存后回到「待预审」，可重新提交审批）
func UpdateOrderContentView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.UpdateOrderContentForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateOrderContentService{
		UpdateOrderContentForm: &form,
		C:                      c,
		Username:               username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "update_content",
			ActionName:     "编辑工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "编辑已撤回工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "update_content",
		ActionName:     "编辑工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "编辑已撤回工单内容",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 撤销工单
func RevokeOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.RevokeOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.RevokeOrderService{
		RevokeOrderForm: &form,
		C:               c,
		Username:        username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "revoke",
			ActionName:     "撤销工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "撤销工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "revoke",
		ActionName:     "撤销工单",
		ActionResult:   "cancelled",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "撤销数据库工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 手动更新工单为已完成
func CompleteOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.CompleteOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CompleteOrderService{
		CompleteOrderForm: &form,
		C:                 c,
		Username:          username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "complete",
			ActionName:     "完成工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "完成工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "complete",
		ActionName:     "完成工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "手动完成数据库工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 手动更新工单为失败
func FailOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.FailOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.FailOrderService{
		FailOrderForm: &form,
		C:             c,
		Username:      username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "fail",
			ActionName:     "标记工单失败",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "标记工单失败未成功",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "fail",
		ActionName:     "标记工单失败",
		ActionResult:   "fail",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "手动标记工单失败",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 复核
func ReviewOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.ReviewOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ReviewOrderService{
		ReviewOrderForm: &form,
		C:               c,
		Username:        username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "review",
			ActionName:     "复核工单",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "复核工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "review",
		ActionName:     "复核工单",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "复核数据库工单",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 修改工单名称
func UpdateOrderTitleView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.UpdateOrderTitleForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateOrderTitleService{
		UpdateOrderTitleForm: &form,
		C:                    c,
		Username:             username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "medium",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "update-title",
			ActionName:     "修改工单名称",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "修改工单名称失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "medium",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "update-title",
		ActionName:     "修改工单名称",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "修改工单名称",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}
