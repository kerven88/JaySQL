package views

import (
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/services"

	"github.com/gin-gonic/gin"
)

// 语法审核
func InspectOrderSyntaxView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.InspectOrderSyntaxForm = &forms.InspectOrderSyntaxForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.InspectOrderSyntaxService{
			InspectOrderSyntaxForm: form,
			C:                      c,
			Username:               username,
		}
		returnData, err := service.Run()
		if err != nil {
			response.Fail(c, err.Error())
		} else {
			response.Success(c, returnData, "success")
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

// 规范预审（实时执行，仅用于「待预审」阶段重新生成快照）
func PreInspectOrderView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.InspectOrderSyntaxForm = &forms.InspectOrderSyntaxForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.PreInspectOrderService{
			InspectOrderSyntaxForm: form,
			C:                      c,
			Username:               username,
		}
		returnData, err := service.Run()
		if err != nil {
			response.Fail(c, err.Error())
		} else {
			response.Success(c, returnData, "success")
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

// 获取工单规范预审快照（详情页读取历史态）
// 「待预审 / 被驳回」阶段按需重建快照，其余阶段（含执行成功、已终止）只读历史快照，
// 不再触发实时预审。
func GetOrderPreInspectSnapshotView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.GetOrderPreInspectSnapshotService{
		OrderID:  c.Param("order_id"),
		Username: username,
		C:        c,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
	} else {
		response.Success(c, returnData, "success")
	}
}
