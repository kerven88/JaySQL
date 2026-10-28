package views

import (
	"fmt"

	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-gonic/gin"
)

// 获取收藏的SQL
func GetFavoritesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.GetFavoritesForm = &forms.GetFavoritesForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.GetFavoritesService{
			GetFavoritesForm: form,
			C:                c,
			Username:         username,
		}
		returnData, total, err := service.Run()
		if err != nil {
			response.Fail(c, err.Error())
		} else {
			response.PaginationSuccess(c, total, returnData)
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

// 新建
func CreateFavoritesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.CreateFavoritesForm = &forms.CreateFavoritesForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.CreateFavoritesService{
			CreateFavoritesForm: form,
			C:                   c,
			Username:            username,
		}
		err := service.Run()
		if err != nil {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_favorite",
				ModuleName:     "SQL 收藏",
				ActionCode:     "create",
				ActionName:     "收藏 SQL",
				ActionResult:   "fail",
				ResourceType:   "favorite",
				ResourceName:   form.Title,
				Summary:        "收藏 SQL 失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.Sqltext,
			})
			response.Fail(c, err.Error())
		} else {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_favorite",
				ModuleName:     "SQL 收藏",
				ActionCode:     "create",
				ActionName:     "收藏 SQL",
				ActionResult:   "success",
				ResourceType:   "favorite",
				ResourceName:   form.Title,
				Summary:        "收藏常用 SQL",
				RequestPayload: form,
				SQLText:        form.Sqltext,
			})
			response.Success(c, nil, "success")
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

// 更新
func UpdateFavoritesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.UpdateFavoritesForm = &forms.UpdateFavoritesForm{}
	id, ok := parseUint32Param(c, "id")
	if !ok {
		return
	}
	if err := c.ShouldBind(&form); err == nil {
		service := services.UpdateFavoritesService{
			UpdateFavoritesForm: form,
			C:                   c,
			ID:                  id,
			Username:            username,
		}
		err := service.Run()
		if err != nil {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_favorite",
				ModuleName:     "SQL 收藏",
				ActionCode:     "update",
				ActionName:     "更新 SQL 收藏",
				ActionResult:   "fail",
				ResourceType:   "favorite",
				ResourceID:     fmt.Sprintf("%d", id),
				ResourceName:   form.Title,
				Summary:        "更新 SQL 收藏失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.Sqltext,
			})
			response.Fail(c, err.Error())
		} else {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_favorite",
				ModuleName:     "SQL 收藏",
				ActionCode:     "update",
				ActionName:     "更新 SQL 收藏",
				ActionResult:   "success",
				ResourceType:   "favorite",
				ResourceName:   form.Title,
				Summary:        "更新常用 SQL 收藏",
				RequestPayload: form,
				SQLText:        form.Sqltext,
			})
			response.Success(c, nil, "success")
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

// 删除
func DeleteFavoritesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	id, ok := parseUint32Param(c, "id")
	if !ok {
		return
	}
	service := services.DeleteFavoritesService{
		C:        c,
		ID:       id,
		Username: username,
	}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_favorite",
			ModuleName:   "SQL 收藏",
			ActionCode:   "delete",
			ActionName:   "删除 SQL 收藏",
			ActionResult: "fail",
			ResourceType: "favorite",
			Summary:      "删除 SQL 收藏失败",
			ErrorMessage: err.Error(),
		})
		response.Fail(c, err.Error())
	} else {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_favorite",
			ModuleName:   "SQL 收藏",
			ActionCode:   "delete",
			ActionName:   "删除 SQL 收藏",
			ActionResult: "success",
			ResourceType: "favorite",
			Summary:      "删除常用 SQL 收藏",
		})
		response.Success(c, nil, "success")
	}
}
