package views

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/users/services"
	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/response"
)

type getPermissionsForm struct {
	pagination.Pagination
	Search string `form:"search"`
}

func GetPermissionsView(c *gin.Context) {
	var form getPermissionsForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.PermissionListService{
		C:      c,
		Search: form.Search,
		PaginationQ: form.Pagination,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}
