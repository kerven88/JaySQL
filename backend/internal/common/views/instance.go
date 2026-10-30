package views

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/common/forms"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/services"
	commonTasks "github.com/simajiong/jaysql/internal/common/tasks"

	"github.com/gin-gonic/gin"
)

func asyncSyncInstanceMeta(instance commonModels.InsightInstances) {
	if strings.EqualFold(string(instance.DbType), "Redis") {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.App.Log.Error(fmt.Sprintf("异步同步实例%s:%d元数据发生panic: %v", instance.Hostname, instance.Port, r))
			}
		}()
		if err := commonTasks.SyncDBMetaForInstance(instance); err != nil {
			global.App.Log.Error(fmt.Sprintf("异步同步实例%s:%d元数据失败: %s", instance.Hostname, instance.Port, err.Error()))
			_ = global.App.DB.Table("insight_instances").
				Where("instance_id = ?", instance.InstanceID).
				Updates(map[string]interface{}{
					"last_check_status":  "sync_meta_failed",
					"last_check_message": err.Error(),
				}).Error
		}
	}()
}

func updateInstanceCheckStatusSuccess(instance *commonModels.InsightInstances, message string) {
	if instance == nil {
		return
	}
	if message == "" {
		message = "实例创建成功，正在后台同步元数据"
	}
	_ = global.App.DB.Table("insight_instances").
		Where("id = ?", instance.ID).
		Updates(map[string]interface{}{
			"last_check_status":  "success",
			"last_check_message": message,
			"last_checked_at":    time.Now(),
		}).Error
}

// finalizeInstancePostWrite 实例写入成功后的收尾：按需异步同步元数据 + 回写连通状态。
//
// 【2026-10-15 收口】这里不再自动写入任何数据访问授权。
// 历史上它会给创建人、以及 code='admin' 的角色各补一条实例级授权，
// 等于「新建即自动拥有全实例访问权」，与「未授权的数据对 admin 同样不可见」冲突。
// 现在数据源创建后对所有人都不可见，必须由管理端显式授权或用户自助授权后才可访问
// （管理端授权接口只受 RBAC 门控、不受数据授权过滤，因此不会出现运维死锁）。
func finalizeInstancePostWrite(instance *commonModels.InsightInstances, message string, syncMeta bool) {
	if instance == nil {
		return
	}
	if strings.EqualFold(string(instance.DbType), "Redis") {
		// Saving a Redis endpoint is not a connectivity check and has no SQL metadata.
		_ = global.App.DB.Table("insight_instances").Where("id = ?", instance.ID).Updates(map[string]any{
			"last_check_status":  "unknown",
			"last_check_message": "Redis 实例已保存，请测试连接或等待后台探活",
		}).Error
		return
	}

	if syncMeta {
		asyncSyncInstanceMeta(*instance)
	}
	updateInstanceCheckStatusSuccess(instance, message)
}

func AdminGetInstancesView(c *gin.Context) {
	var form *forms.AdminGetInstancesForm = &forms.AdminGetInstancesForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.AdminGetInstancesServices{
			AdminGetInstancesForm: form,
			C:                     c,
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

func AdminCreateInstancesView(c *gin.Context) {
	var form forms.AdminCreateInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminCreateInstancesService{
		AdminCreateInstancesForm: &form,
		C:                        c,
	}
	instance, err := service.Run()
	if err != nil {
		services.TryWriteAuditLog(c, services.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "datasource",
			ModuleName:     "数据源管理",
			ActionCode:     "create",
			ActionName:     "创建数据源",
			ActionResult:   "fail",
			ResourceType:   "instance",
			ResourceName:   fmt.Sprintf("%s:%d", form.Hostname, form.Port),
			Summary:        fmt.Sprintf("创建数据源失败：%s:%d", form.Hostname, form.Port),
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	services.TryWriteAuditLog(c, services.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "create",
		ActionName:     "创建数据源",
		ActionResult:   "success",
		ResourceType:   "instance",
		ResourceID:     instance.InstanceID,
		ResourceName:   fmt.Sprintf("%s:%d", instance.Hostname, instance.Port),
		Summary:        fmt.Sprintf("创建数据源：%s:%d", instance.Hostname, instance.Port),
		RequestPayload: form,
		AfterData: map[string]any{
			"id":          instance.ID,
			"instance_id": instance.InstanceID,
			"hostname":    instance.Hostname,
			"port":        instance.Port,
			"db_type":     instance.DbType,
			"remark":      instance.Remark,
		},
	})
	finalizeInstancePostWrite(instance, "实例创建成功，正在后台同步元数据", true)
	response.Success(c, map[string]any{
		"id":          instance.ID,
		"instance_id": instance.InstanceID,
	}, "success")
}

func AdminUpdateInstancesView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.AdminUpdateInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	var before commonModels.InsightInstances
	_ = global.App.DB.Where("id = ?", id).Take(&before).Error
	service := services.AdminUpdateInstancesService{
		AdminUpdateInstancesForm: &form,
		C:                        c,
		ID:                       id,
	}
	err := service.Run()
	if err != nil {
		services.TryWriteAuditLog(c, services.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "datasource",
			ModuleName:     "数据源管理",
			ActionCode:     "update",
			ActionName:     "修改数据源",
			ActionResult:   "fail",
			ResourceType:   "instance",
			ResourceID:     fmt.Sprintf("%d", id),
			ResourceName:   fmt.Sprintf("%s:%d", form.Hostname, form.Port),
			Summary:        fmt.Sprintf("修改数据源失败：%s:%d", form.Hostname, form.Port),
			ErrorMessage:   err.Error(),
			RequestPayload: form,
			BeforeData:     before,
		})
		response.Fail(c, err.Error())
		return
	}
	var after commonModels.InsightInstances
	_ = global.App.DB.Where("id = ?", id).Take(&after).Error
	services.TryWriteAuditLog(c, services.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "update",
		ActionName:     "修改数据源",
		ActionResult:   "success",
		ResourceType:   "instance",
		ResourceID:     after.InstanceID,
		ResourceName:   fmt.Sprintf("%s:%d", after.Hostname, after.Port),
		Summary:        fmt.Sprintf("修改数据源：%s:%d", after.Hostname, after.Port),
		RequestPayload: form,
		BeforeData:     before,
		AfterData:      after,
	})
	finalizeInstancePostWrite(&after, "实例连接信息已更新，正在后台重新同步元数据", true)
	response.Success(c, nil, "success")
}

// AdminUpdateInstanceRemarkView 行内修改数据源名称：只更新 remark，审计记流水。
func AdminUpdateInstanceRemarkView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.AdminUpdateInstanceRemarkForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	var before commonModels.InsightInstances
	_ = global.App.DB.Where("id = ?", id).Take(&before).Error
	service := services.AdminUpdateInstanceRemarkService{
		C:      c,
		ID:     id,
		Remark: form.Remark,
	}
	if err := service.Run(); err != nil {
		services.TryWriteAuditLog(c, services.AuditLogEvent{
			AuditLevel:     "medium",
			ModuleCode:     "datasource",
			ModuleName:     "数据源管理",
			ActionCode:     "rename",
			ActionName:     "修改数据源名称",
			ActionResult:   "fail",
			ResourceType:   "instance",
			ResourceID:     fmt.Sprintf("%d", id),
			ResourceName:   before.Remark,
			Summary:        fmt.Sprintf("修改数据源名称失败：%s", before.Remark),
			RequestPayload: form,
			BeforeData:     before,
		})
		response.Fail(c, err.Error())
		return
	}
	var after commonModels.InsightInstances
	_ = global.App.DB.Where("id = ?", id).Take(&after).Error
	services.TryWriteAuditLog(c, services.AuditLogEvent{
		AuditLevel:     "medium",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "rename",
		ActionName:     "修改数据源名称",
		ActionResult:   "success",
		ResourceType:   "instance",
		ResourceID:     after.InstanceID,
		ResourceName:   after.Remark,
		Summary:        fmt.Sprintf("修改数据源名称：%s → %s", before.Remark, after.Remark),
		RequestPayload: form,
		BeforeData:     before,
		AfterData:      after,
	})
	response.Success(c, nil, "success")
}

func AdminDeleteInstances(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var before commonModels.InsightInstances
	_ = global.App.DB.Where("id = ?", id).Take(&before).Error
	service := services.AdminDeleteInstanceConfigsService{
		C:  c,
		ID: id,
	}
	err := service.Run()
	if err != nil {
		// 仍被备份/恢复任务引用：除了给出可读原因，还要把阻塞明细交给前端，
		// 否则用户看到的只是「删不掉」，不知道去哪处理。
		var inUse *services.InstanceInUseError
		blockingPayload := interface{}(nil)
		if errors.As(err, &inUse) && inUse.HasBlocker() {
			blockingPayload = inUse
		}
		services.TryWriteAuditLog(c, services.AuditLogEvent{
			AuditLevel:   "high",
			ModuleCode:   "datasource",
			ModuleName:   "数据源管理",
			ActionCode:   "delete",
			ActionName:   "删除数据源",
			ActionResult: "fail",
			ResourceType: "instance",
			ResourceID:   fmt.Sprintf("%d", id),
			ResourceName: fmt.Sprintf("%s:%d", before.Hostname, before.Port),
			Summary:      fmt.Sprintf("删除数据源失败：%s:%d", before.Hostname, before.Port),
			ErrorMessage: err.Error(),
			BeforeData:   before,
			AfterData:    blockingPayload,
		})
		if blockingPayload != nil {
			response.FailWithData(c, err.Error(), blockingPayload)
			return
		}
		response.Fail(c, err.Error())
		return
	}
	services.TryWriteAuditLog(c, services.AuditLogEvent{
		AuditLevel:   "high",
		ModuleCode:   "datasource",
		ModuleName:   "数据源管理",
		ActionCode:   "delete",
		ActionName:   "删除数据源",
		ActionResult: "success",
		ResourceType: "instance",
		ResourceID:   before.InstanceID,
		ResourceName: fmt.Sprintf("%s:%d", before.Hostname, before.Port),
		Summary:      fmt.Sprintf("删除数据源：%s:%d", before.Hostname, before.Port),
		BeforeData:   before,
		// 访问权限随实例自动回收，把回收行数一并落审计，便于事后核对
		AfterData: map[string]any{
			"cleanup":             service.Summary,
			"revoked_permissions": service.Summary.RevokedPermissions(),
		},
	})
	response.Success(c, nil, "success")
}

func AdminTestInstanceConnectionView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminTestInstanceConnectionService{
		C:  c.Request.Context(),
		ID: id,
	}
	report, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, report, "连接成功")
}

func AdminTestInstanceConnectionByPayloadView(c *gin.Context) {
	var form forms.AdminTestInstanceConnectionForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminTestInstanceConnectionByPayloadService{
		ID:            form.ID,
		ClearPassword: form.ClearPassword,
		C:             c.Request.Context(),
		DbType:        string(form.DbType),
		Hostname:      form.Hostname,
		Port:          form.Port,
		User:          form.User,
		Password:      form.Password,
		ExtraConfig:   form.ExtraConfig,
	}
	report, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, report, "连接成功")
}

func AdminSyncInstanceMetaView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.AdminSyncInstanceMetaService{
		C:  c,
		ID: id,
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AdminBatchImportInstancesView(c *gin.Context) {
	var form forms.AdminBatchImportInstancesForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AdminBatchImportInstancesService{
		AdminBatchImportInstancesForm: &form,
		C:                             c,
	}
	result, err := service.Run()
	if err != nil {
		services.TryWriteAuditLog(c, services.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "datasource",
			ModuleName:     "数据源管理",
			ActionCode:     "batch_import",
			ActionName:     "批量导入数据源",
			ActionResult:   "fail",
			ResourceType:   "instance",
			ResourceName:   form.BatchID,
			Summary:        fmt.Sprintf("批量导入数据源失败: %v", err),
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	services.TryWriteAuditLog(c, services.AuditLogEvent{
		AuditLevel:     "medium",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "batch_import",
		ActionName:     "批量导入数据源",
		ActionResult:   "success",
		ResourceType:   "instance",
		ResourceName:   form.BatchID,
		Summary:        fmt.Sprintf("批量导入数据源完成, batch=%s, result=%+v", form.BatchID, result),
		RequestPayload: form,
		AfterData:      result,
	})
	if rows, ok := result["rows"].([]services.BatchImportInstanceRowResult); ok {
		successIDs := make([]uint64, 0, len(rows))
		for _, row := range rows {
			if strings.EqualFold(row.Status, "success") && row.ID > 0 {
				successIDs = append(successIDs, row.ID)
			}
		}
		if len(successIDs) > 0 {
			var instances []commonModels.InsightInstances
			if err := global.App.DB.Where("id in ?", successIDs).Find(&instances).Error; err == nil {
				for i := range instances {
					finalizeInstancePostWrite(&instances[i], "批量导入实例成功，正在后台同步元数据", true)
				}
			}
		}
	}
	response.Success(c, result, "success")
}
