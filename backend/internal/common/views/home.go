package views

import (
	"github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

type homeDashboardForm struct {
	Environment int `form:"environment"`
}

func HomeDashboardView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}

	var form homeDashboardForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}

	service := services.HomeDashboardService{
		C:           c,
		Username:    username,
		Environment: form.Environment,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
