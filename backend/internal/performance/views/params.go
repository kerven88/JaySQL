package views

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/performance/forms"
	"github.com/simajiong/jaysql/internal/performance/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// GetInstanceParamsView 实时查询实例参数列表（当前值不落平台库）
func GetInstanceParamsView(c *gin.Context) {
	var form forms.GetInstanceParamsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	username, ok := getUsername(c)
	if !ok {
		return
	}
	_ = username // 实时查询不区分操作人，保留登录校验
	service := services.GetInstanceParamsService{C: c, InstanceID: form.InstanceID}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// DiffInstanceParamsView 多实例参数对比（同数据库家族，2~5 个）
func DiffInstanceParamsView(c *gin.Context) {
	var form forms.DiffInstanceParamsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	ids := strings.Split(form.InstanceIDs, ",")
	service := services.DiffInstanceParamsService{C: c, InstanceIDs: ids}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetParamChangesView 参数修改历史
func GetParamChangesView(c *gin.Context) {
	var form forms.GetParamChangesForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetParamChangesService{
		C:          c,
		InstanceID: form.InstanceID,
		ParamName:  form.ParamName,
		Operator:   form.Operator,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// ModifyInstanceParamView 修改参数（performance.param.modify）
func ModifyInstanceParamView(c *gin.Context) {
	var form forms.ModifyInstanceParamForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.ModifyInstanceParamService{
		C:          c,
		InstanceID: form.InstanceID,
		ParamName:  form.ParamName,
		NewValue:   form.NewValue,
		Reason:     form.Reason,
		AckRisk:    form.AckRisk,
		Operator:   username,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// RollbackParamView 回滚参数到历史值（performance.param.modify）
func RollbackParamView(c *gin.Context) {
	var form forms.RollbackParamForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.RollbackParamService{C: c, ChangeID: form.ChangeID, Operator: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
