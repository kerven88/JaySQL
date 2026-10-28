package views

import (
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/services"

	"github.com/gin-gonic/gin"
)

// 获取环境
func GetOrderEnvironmentsView(c *gin.Context) {
	service := services.GetOrderEnvironmentsService{C: c}
	returnData, err := service.Run()
	if err == nil {
		response.Success(c, returnData, "success")
	} else {
		response.Fail(c, err.Error())
	}
}

// 获取指定环境的Schemas
func GetOrderInstancesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetOrderInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetOrderInstancesService{
		GetOrderInstancesForm: &form,
		C:                     c,
		Username:              username,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

// 获取可用数据源
func GetOrderDataSourcesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetOrderDataSourcesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetOrderDataSourcesService{
		GetOrderDataSourcesForm: &form,
		C:                       c,
		Username:                username,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

// 获取指定环境的Schemas
func GetOrderSchemasView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetOrderSchemasForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetOrderSchemasService{
		GetOrderSchemasForm: &form,
		C:                   c,
		Username:            username,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

// 获取审核/复核/抄送人
func GetOrderUsersView(c *gin.Context) {
	var form forms.GetOrderUsersForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetOrderUsersService{
		GetOrderUsersForm: &form,
		C:                 c,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

// 提交工单
func CreateOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.CreateOrderForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateOrderService{
		CreateOrderForm: &form,
		C:               c,
		Username:        username,
	}
	orderID, err := service.Run()
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
			Summary:        "提交工单失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
			RelatedOrderID: "",
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
		Summary:        "提交数据库工单",
		RequestPayload: form,
		SQLText:        form.Content,
	})
	response.Success(c, map[string]any{"order_id": orderID}, "success")
}
