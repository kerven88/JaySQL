package views

import (
	"fmt"

	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-gonic/gin"
)

// 获取管理后台权限
func AdminGetSchemasGrantView(c *gin.Context) {
	var form *forms.AdminSchemasGrantForm = &forms.AdminSchemasGrantForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminGetSchemasGrantService{AdminSchemasGrantForm: form, C: c}
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

func AdminGetInstancesListView(c *gin.Context) {
	var form *forms.AdminGetInstancesListForm = &forms.AdminGetInstancesListForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminGetInstancesListService{
			AdminGetInstancesListForm: form,
			C:                         c,
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

// 获取指定环境的Schemas
func AdminGetSchemasListView(c *gin.Context) {
	var form *forms.AdminGetSchemasListForm = &forms.AdminGetSchemasListForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminGetSchemasListService{
			AdminGetSchemasListForm: form,
			C:                       c,
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

func AdminGetTablesListView(c *gin.Context) {
	var form *forms.AdminGetTablesListForm = &forms.AdminGetTablesListForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminGetTablesListService{
			AdminGetTablesListForm: form,
			C:                      c,
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

func AdminCreateSchemasGrantView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.AdminCreateSchemasGrantForm = &forms.AdminCreateSchemasGrantForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminCreateSchemasGrantService{
			AdminCreateSchemasGrantForm: form,
			C:                           c,
		}
		err := service.Run()
		if err != nil {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "high",
				ModuleCode:     "das_grant",
				ModuleName:     "数据授权",
				ActionCode:     "grant_schema",
				ActionName:     "授权库资源",
				ActionResult:   "fail",
				ResourceType:   form.ResourceType,
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        fmt.Sprintf("授权库资源失败：%s", form.Schema),
				ErrorMessage:   err.Error(),
				RequestPayload: form,
			})
			response.Fail(c, err.Error())
		} else {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "high",
				ModuleCode:     "das_grant",
				ModuleName:     "数据授权",
				ActionCode:     "grant_schema",
				ActionName:     "授权库资源",
				ActionResult:   "success",
				ResourceType:   form.ResourceType,
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        fmt.Sprintf("授权库资源：%s", form.Schema),
				RequestPayload: form,
			})
			response.Success(c, nil, "success")
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

func AdminGetTablesGrantView(c *gin.Context) {
	var form *forms.AdminGetTablesGrantForm = &forms.AdminGetTablesGrantForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminGetTablesGrantService{AdminGetTablesGrantForm: form, C: c}
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

func AdminCreateTablesGrantView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.AdminCreateTablesGrantForm = &forms.AdminCreateTablesGrantForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminCreateTablesGrantService{
			AdminCreateTablesGrantForm: form,
			C:                          c,
		}
		err := service.Run()
		if err != nil {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "high",
				ModuleCode:     "das_grant",
				ModuleName:     "数据授权",
				ActionCode:     "grant_table",
				ActionName:     "授权表资源",
				ActionResult:   "fail",
				ResourceType:   form.ResourceType,
				ResourceID:     form.InstanceID,
				ResourceName:   fmt.Sprintf("%s/%d张表", form.Schema, len(form.Tables)),
				Summary:        fmt.Sprintf("授权表资源失败：%s", form.Schema),
				ErrorMessage:   err.Error(),
				RequestPayload: form,
			})
			response.Fail(c, err.Error())
		} else {
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "high",
				ModuleCode:     "das_grant",
				ModuleName:     "数据授权",
				ActionCode:     "grant_table",
				ActionName:     "授权表资源",
				ActionResult:   "success",
				ResourceType:   form.ResourceType,
				ResourceID:     form.InstanceID,
				ResourceName:   fmt.Sprintf("%s/%d张表", form.Schema, len(form.Tables)),
				Summary:        fmt.Sprintf("授权表资源：%s", form.Schema),
				RequestPayload: form,
			})
			response.Success(c, nil, "success")
		}
	} else {
		response.ValidateFail(c, err.Error())
	}
}

func AdminDeleteSchemasGrantView(c *gin.Context) {
	username, okUser := getUsername(c)
	if !okUser {
		return
	}
	id, ok := parseUint32Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminDeleteSchemasGrantService{
		C:  c,
		ID: id,
	}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "high",
			ModuleCode:   "das_grant",
			ModuleName:   "数据授权",
			ActionCode:   "revoke_schema",
			ActionName:   "撤销库资源授权",
			ActionResult: "fail",
			ResourceType: "schema_grant",
			ResourceID:   fmt.Sprintf("%d", id),
			Summary:      "撤销库资源授权失败",
			ErrorMessage: err.Error(),
		})
		response.Fail(c, err.Error())
	} else {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "high",
			ModuleCode:   "das_grant",
			ModuleName:   "数据授权",
			ActionCode:   "revoke_schema",
			ActionName:   "撤销库资源授权",
			ActionResult: "success",
			ResourceType: "schema_grant",
			ResourceID:   fmt.Sprintf("%d", id),
			Summary:      "撤销库资源授权",
		})
		response.Success(c, nil, "success")
	}
}

func AdminDeleteTablesGrantView(c *gin.Context) {
	username, okUser := getUsername(c)
	if !okUser {
		return
	}
	id, ok := parseUint32Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminDeleteTablesGrantService{
		C:  c,
		ID: id,
	}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "high",
			ModuleCode:   "das_grant",
			ModuleName:   "数据授权",
			ActionCode:   "revoke_table",
			ActionName:   "撤销表资源授权",
			ActionResult: "fail",
			ResourceType: "table_grant",
			ResourceID:   fmt.Sprintf("%d", id),
			Summary:      "撤销表资源授权失败",
			ErrorMessage: err.Error(),
		})
		response.Fail(c, err.Error())
	} else {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "high",
			ModuleCode:   "das_grant",
			ModuleName:   "数据授权",
			ActionCode:   "revoke_table",
			ActionName:   "撤销表资源授权",
			ActionResult: "success",
			ResourceType: "table_grant",
			ResourceID:   fmt.Sprintf("%d", id),
			Summary:      "撤销表资源授权",
		})
		response.Success(c, nil, "success")
	}
}
