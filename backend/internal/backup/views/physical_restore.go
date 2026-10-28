package views

import (
	"fmt"

	"github.com/simajiong/jaysql/internal/backup/forms"
	"github.com/simajiong/jaysql/internal/backup/services"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

func GetPhysicalRestoreRunsView(c *gin.Context) {
	var form forms.GetPhysicalRestoreRunsForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetPhysicalRestoreRunsService{GetPhysicalRestoreRunsForm: &form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func PrecheckPhysicalRestoreView(c *gin.Context) {
	var form forms.PrecheckPhysicalRestoreForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.PrecheckPhysicalRestoreService{PrecheckPhysicalRestoreForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreatePhysicalRestoreRunView(c *gin.Context) {
	var form forms.CreatePhysicalRestoreRunForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreatePhysicalRestoreRunService{CreatePhysicalRestoreRunForm: &form, C: c}
	run, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_physical_restore",
			ModuleName:     "物理恢复",
			ActionCode:     "create",
			ActionName:     "创建物理恢复",
			ActionResult:   "fail",
			ResourceType:   "physical_restore_run",
			ResourceName:   form.TargetDataDir,
			Summary:        "创建物理恢复失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_physical_restore",
		ModuleName:     "物理恢复",
		ActionCode:     "create",
		ActionName:     "创建物理恢复",
		ActionResult:   "success",
		ResourceType:   "physical_restore_run",
		ResourceID:     fmt.Sprintf("%d", run.ID),
		ResourceName:   form.TargetDataDir,
		Summary:        "创建物理恢复任务",
		RequestPayload: form,
		AfterData:      run,
	})
	response.Success(c, run, "success")
}

func UpdatePhysicalRestoreRunView(c *gin.Context) {
	var uri forms.PhysicalRestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	var form forms.UpdatePhysicalRestoreRunForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdatePhysicalRestoreRunService{PhysicalRestoreRunID: uri.ID, UpdatePhysicalRestoreRunForm: &form, C: c}
	run, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, run, "success")
}

func StopPhysicalRestoreRunView(c *gin.Context) {
	var uri forms.PhysicalRestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.StopPhysicalRestoreRunService{PhysicalRestoreRunID: uri.ID, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func DeletePhysicalRestoreRunView(c *gin.Context) {
	var uri forms.PhysicalRestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.DeletePhysicalRestoreRunService{PhysicalRestoreRunID: uri.ID, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"id": uri.ID}, "success")
}

func GetPhysicalRestoreRunDetailView(c *gin.Context) {
	var uri forms.PhysicalRestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetPhysicalRestoreRunDetailService{PhysicalRestoreRunID: uri.ID, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetPhysicalRestoreRunLogView(c *gin.Context) {
	var uri forms.PhysicalRestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	var form forms.GetPhysicalRestoreRunLogForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetPhysicalRestoreRunLogService{
		PhysicalRestoreRunID:    uri.ID,
		GetPhysicalRestoreRunLogForm: &form,
		C:                       c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
