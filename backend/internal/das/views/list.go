package views

import (
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-gonic/gin"
)

// 获取环境
func GetEnvironmentsView(c *gin.Context) {
	service := services.GetEnvironmentsService{C: c}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

func GetInstancesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.GetAccessibleInstancesService{C: c, Username: username}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

func GetResourcesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetResourcesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetResourcesService{
		GetResourcesForm: &form,
		C:                c,
		Username:         username,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

// 获取用户授权的库
func GetSchemasView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.GetSchemasService{C: c, Username: username}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

// 获取用户授权的库表
func GetTablesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetTablesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetTablesService{
		GetTablesForm: &form,
		C:             c,
		Username:      username,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

// 获取表元信息
func GetTableInfoView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetTableInfoForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetTableInfoService{
		GetTableInfoForm: &form,
		C:                c,
		Username:         username,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

// 获取db字典
func GetDbDictView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetDbDictForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetDbDictService{
		GetDbDictForm: &form,
		C:             c,
		Username:      username,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, returnData, "success")
}

func GetObjectsFlatIndexView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetObjectsFlatIndexForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetObjectsFlatIndexService{
		GetObjectsFlatIndexForm: &form,
		C:                       c,
		Username:                username,
	}
	returnData, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	if returnData.TooLarge {
		c.Header("X-Flat-Index-Too-Large", "1")
	}
	response.Success(c, returnData, "success")
}

func SearchObjectsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.SearchObjectsForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.SearchObjectsService{
		SearchObjectsForm: &form,
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

func ReportObjectSearchAuditView(c *gin.Context) {
	c.Status(204)
}

// 获取历史SQL
func GetHistoryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetHistoryForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetHistoryService{
		GetHistoryForm: &form,
		C:              c,
		Username:       username,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}
