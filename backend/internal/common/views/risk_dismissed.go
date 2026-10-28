package views

import (
	"github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

type homeRiskDismissBatchForm struct {
	RiskTypes []string `json:"risk_types" form:"risk_types"`
}

type homeRiskDismissOneForm struct {
	RiskType string `json:"risk_type" form:"risk_type"`
	RiskKey  string `json:"risk_key" form:"risk_key"`
	Remark   string `json:"remark" form:"remark"`
}

func HomeRiskDismissBatchView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form homeRiskDismissBatchForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	svc := services.RiskDismissedService{
		C:        c,
		Username: username,
	}
	data, err := svc.GetDismissedKeys(form.RiskTypes)
	if err != nil {
		response.Fail(c, "加载全局关闭的风险列表失败: "+err.Error())
		return
	}
	response.Success(c, data, "success")
}

func HomeRiskDismissView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form homeRiskDismissOneForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	svc := services.RiskDismissedService{
		C:        c,
		Username: username,
	}
	if err := svc.Dismiss(form.RiskType, form.RiskKey, form.Remark); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{
		"risk_type": form.RiskType,
		"risk_key":  form.RiskKey,
	}, "success")
}

func HomeRiskUndismissView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form homeRiskDismissOneForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	svc := services.RiskDismissedService{
		C:        c,
		Username: username,
	}
	if err := svc.Undismiss(form.RiskType, form.RiskKey); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{
		"risk_type": form.RiskType,
		"risk_key":  form.RiskKey,
	}, "success")
}
