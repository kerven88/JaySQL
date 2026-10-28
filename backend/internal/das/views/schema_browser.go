package views

import (
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-gonic/gin"
)

// GetSchemaBrowserChildrenView SQL 工作台「对象树」的逐层取数入口。
// 请求参数与返回结构见 forms.SchemaBrowserForm / services.BrowserNode。
func GetSchemaBrowserChildrenView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.SchemaBrowserForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.SchemaBrowserService{
		SchemaBrowserForm: &form,
		C:                 c,
		Username:          username,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}
