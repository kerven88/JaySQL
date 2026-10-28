package views

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/simajiong/jaysql/internal/inspect/forms"
	"github.com/simajiong/jaysql/internal/inspect/services"
	"github.com/simajiong/jaysql/pkg/response"
)

func AdminGetSQLStandardsView(c *gin.Context) {
	var form forms.AdminSQLStandardListForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminSQLStandardListService{
		AdminSQLStandardListForm: &form,
		C:                        c,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

func AdminGetSQLStandardByInstanceView(c *gin.Context) {
	instanceID := strings.TrimSpace(c.Param("instance_id"))
	if instanceID == "" {
		response.ValidateFail(c, "实例 ID 不能为空")
		return
	}
	service := services.AdminSQLStandardByInstanceService{C: c, InstanceID: instanceID}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func AdminGetSQLStandardDetailView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminSQLStandardDetailService{C: c, ID: id}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func AdminCreateSQLStandardView(c *gin.Context) {
	var form forms.AdminSQLStandardSaveForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminSQLStandardSaveService{
		AdminSQLStandardSaveForm: &form,
		C:                        c,
		ID:                       0,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AdminUpdateSQLStandardView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.AdminSQLStandardSaveForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminSQLStandardSaveService{
		AdminSQLStandardSaveForm: &form,
		C:                        c,
		ID:                       id,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AdminDeleteSQLStandardView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminSQLStandardDeleteService{C: c, ID: id}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AdminUpdateSQLStandardRuleView(c *gin.Context) {
	var form forms.AdminSQLStandardRuleUpdateForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	// 路由上带 :rule_id，当请求体未携带或为 0 时以路径参数为准，保证更新目标唯一
	if form.RuleID == 0 {
		if ruleID, err := strconv.ParseUint(c.Param("rule_id"), 10, 64); err == nil {
			form.RuleID = ruleID
		}
	}
	service := services.AdminSQLStandardRuleUpdateService{
		AdminSQLStandardRuleUpdateForm: &form,
		C:                              c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func AdminGetSQLStandardInstancesView(c *gin.Context) {
	var form forms.AdminSQLStandardInstanceListForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminSQLStandardInstanceListService{
		AdminSQLStandardInstanceListForm: &form,
		C:                                c,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

func AdminBindSQLStandardInstanceView(c *gin.Context) {
	var form forms.AdminSQLStandardBindInstanceForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminSQLStandardBindInstanceService{
		AdminSQLStandardBindInstanceForm: &form,
		C:                                c,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AdminUnbindSQLStandardInstanceView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminSQLStandardUnbindInstanceService{C: c, ID: id}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AdminGetSQLStandardAvailableInstancesView(c *gin.Context) {
	var form forms.AdminSQLStandardAvailableInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminSQLStandardAvailableInstancesService{
		AdminSQLStandardAvailableInstancesForm: &form,
		C:                                      c,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}
