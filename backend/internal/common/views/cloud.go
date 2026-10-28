package views

import (
	"fmt"

	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

func AdminValidateCloudAccountView(c *gin.Context) {
	var form forms.AdminValidateCloudAccountForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminValidateCloudAccountService{
		AdminValidateCloudAccountForm: &form,
		C:                             c,
	}
	result, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "success")
}

func AdminDiscoverCloudInstancesView(c *gin.Context) {
	var form forms.AdminDiscoverCloudInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminDiscoverCloudInstancesService{
		AdminDiscoverCloudInstancesForm: &form,
		C:                               c,
	}
	result, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "success")
}

func AdminImportCloudInstancesView(c *gin.Context) {
	var form forms.AdminImportCloudInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminImportCloudInstancesService{
		AdminImportCloudInstancesForm: &form,
		C:                             c,
	}
	result, err := service.Run()
	if err != nil {
		services.TryWriteAuditLog(c, services.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "datasource",
			ModuleName:     "数据源管理",
			ActionCode:     "cloud_import",
			ActionName:     "从公有云批量导入数据源",
			ActionResult:   "fail",
			ResourceType:   "instance",
			ResourceName:   fmt.Sprintf("vendor=%s,total=%d", form.Vendor, len(form.Instances)),
			Summary:        fmt.Sprintf("从%s云批量导入数据源失败", form.Vendor),
			ErrorMessage:   err.Error(),
			RequestPayload: sanitizeCloudPayload(&form),
		})
		response.Fail(c, err.Error())
		return
	}
	services.TryWriteAuditLog(c, services.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "cloud_import",
		ActionName:     "从公有云批量导入数据源",
		ActionResult:   "success",
		ResourceType:   "instance",
		ResourceName:   fmt.Sprintf("vendor=%s,total=%d,success=%d,failed=%d,skipped=%d", form.Vendor, result.Total, result.Success, result.Failed, result.Skipped),
		Summary:        fmt.Sprintf("从%s云批量导入数据源:成功%d/失败%d/跳过%d", form.Vendor, result.Success, result.Failed, result.Skipped),
		RequestPayload: sanitizeCloudPayload(&form),
		AfterData: map[string]any{
			"total":   result.Total,
			"success": result.Success,
			"failed":  result.Failed,
			"skipped": result.Skipped,
		},
	})
	response.Success(c, result, "success")
}

func sanitizeCloudPayload(form *forms.AdminImportCloudInstancesForm) map[string]any {
	return map[string]any{
		"vendor":           form.Vendor,
		"saved_account_id": form.SavedAccountId,
		"environment":      form.Environment,
		"duplicate_policy": form.DuplicatePolicy,
		"instances_count":  len(form.Instances),
	}
}
