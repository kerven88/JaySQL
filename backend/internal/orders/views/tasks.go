package views

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/orders/forms"
	ordersModels "github.com/simajiong/jaysql/internal/orders/models"
	"github.com/simajiong/jaysql/internal/orders/services"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
)

// 生成执行任务
func GenOrderTasksView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GenOrderTasksForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GenOrderTasksService{
		GenOrderTasksForm: &form,
		C:                 c,
		Username:          username,
	}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "generate_tasks",
			ActionName:     "生成执行任务",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "生成工单执行任务失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "generate_tasks",
		ActionName:     "生成执行任务",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "生成工单执行任务",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

// 获取任务列表
func GetTasksView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetTasksForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetTasksServices{
		GetTasksForm: &form,
		OrderID:      c.Param("order_id"),
		C:            c,
		Username:     username,
	}
	returnData, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, returnData)
}

// 预览任务
func PreviewTasksView(c *gin.Context) {
	var form *forms.PreviewTasksForm = &forms.PreviewTasksForm{}
	if err := c.ShouldBind(&form); err == nil {
		service := services.PreviewTasksServices{
			PreviewTasksForm: form,
			C:                c,
		}
		returnData, err := service.Run()
		if err != nil {
			response.Fail(c, err.Error())
			return
		}
		response.Success(c, returnData, "success")
	} else {
		response.ValidateFail(c, err.Error())
	}
}

// 执行单个任务
func ExecuteTaskView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.ExecuteTaskForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ExecuteTaskService{
		ExecuteTaskForm: &form,
		C:               c,
		Username:        username,
	}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "execute_task",
			ActionName:     "执行工单任务",
			ActionResult:   "fail",
			ResourceType:   "order_task",
			ResourceID:     form.TaskID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "执行工单任务失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "execute_task",
		ActionName:     "执行工单任务",
		ActionResult:   "success",
		ResourceType:   "order_task",
		ResourceID:     form.TaskID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "执行工单任务",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")

}

// 批量执行任务
func ExecuteBatchTasksView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.ExecuteBatchTasksForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &form,
		C:                     c,
		Username:              username,
	}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:       username,
			AuditLevel:     "high",
			ModuleCode:     "db_order",
			ModuleName:     "数据库工单",
			ActionCode:     "execute_batch",
			ActionName:     "批量执行工单任务",
			ActionResult:   "fail",
			ResourceType:   "order",
			ResourceID:     form.OrderID,
			ResourceName:   form.OrderID,
			RelatedOrderID: form.OrderID,
			Summary:        "批量执行工单任务失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "execute_batch",
		ActionName:     "批量执行工单任务",
		ActionResult:   "success",
		ResourceType:   "order",
		ResourceID:     form.OrderID,
		ResourceName:   form.OrderID,
		RelatedOrderID: form.OrderID,
		Summary:        "批量执行工单任务",
		RequestPayload: form,
	})
	response.Success(c, nil, "执行完成")
}

// 下载导出文件
func DownloadExportFileView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	task_id := c.Param("task_id")
	requestID := requestid.Get(c)
	// 判断下载记录是否存在
	var task ordersModels.InsightOrderTasks
	tx := global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
		Where("task_id=?", task_id).Scan(&task)
	if tx.RowsAffected == 0 {
		global.App.Log.WithField("request_id", requestID).WithField("username", username).Errorf("下载记录关联的任务不存在，任务ID：%s", task_id)
		c.JSON(http.StatusNotFound, map[string]interface{}{})
		return
	}
	// 判断用户是否有权限下载
	var record ordersModels.InsightOrderRecords
	global.App.DB.Model(&ordersModels.InsightOrderRecords{}).
		Where("order_id=?", task.OrderID).Scan(&record)
	if record.Applicant != username {
		global.App.Log.WithField("request_id", requestID).WithField("username", username).Errorf("用户%s无权限下载任务%s的导出文件", username, task_id)
		c.JSON(http.StatusForbidden, map[string]interface{}{})
		return
	}
	// 导出结果集只按「申请人本人」放行：导出工单的审批已经覆盖了数据获取的正当性，
	// 不再额外要求一份数据授权（见 das/services/access_action_scope.go）。
	// 获取下载文件信息
	type exportFile struct {
		FileName      string `json:"file_name"`
		FileSize      int64  `json:"file_size"`
		FilePath      string `json:"file_path"`
		ContentType   string `json:"content_type"`
		EncryptionKey string `json:"encryption_key"`
		ExportRows    int64  `json:"export_rows"`
	}
	var file exportFile
	err := json.Unmarshal([]byte(task.Result), &file)
	if err != nil {
		global.App.Log.WithField("request_id", requestID).WithField("username", username).Errorf("解析下载文件信息异常，错误：%s", err.Error())
		c.JSON(http.StatusInternalServerError, map[string]interface{}{})
		return
	}
	// 检查本地文件是否存在
	_, err = os.Stat(file.FilePath)
	if os.IsNotExist(err) {
		global.App.Log.WithField("request_id", requestID).WithField("username", username).Error(fmt.Sprintf("下载的文件%s不存在", file.FilePath))
		c.JSON(http.StatusInternalServerError, map[string]interface{}{})
		return
	}

	// Open the file
	f, err := os.Open(file.FilePath)
	if err != nil {
		global.App.Log.WithField("request_id", requestID).WithField("username", username).Error(fmt.Sprintf("打开下载文件%s失败", file.FilePath))
		c.JSON(http.StatusInternalServerError, map[string]interface{}{})
		return
	}
	defer f.Close()

	// Set headers for file download
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", file.FileName))
	c.Header("Accept-Length", fmt.Sprintf("%d", file.FileSize))
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		Username:       username,
		AuditLevel:     "high",
		ModuleCode:     "db_order",
		ModuleName:     "数据库工单",
		ActionCode:     "download_export",
		ActionName:     "下载导出文件",
		ActionResult:   "success",
		ResourceType:   "order_task",
		ResourceID:     task_id,
		ResourceName:   file.FileName,
		RelatedOrderID: task.OrderID,
		Summary:        "下载工单导出文件",
		AfterData:      map[string]any{"file_name": file.FileName, "file_size": file.FileSize, "export_rows": file.ExportRows},
	})
	c.File(file.FilePath)
}
