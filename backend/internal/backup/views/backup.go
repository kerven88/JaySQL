package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gin-contrib/requestid"
	"github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/backup/services"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/response"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/gin-gonic/gin"
)

func mapKeysSorted(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// #region debug-point A:backup-view-report
func reportBackupExecDebugFromView(c *gin.Context, hypothesisID string, location string, msg string, data map[string]any) {
	go func() {
		if data == nil {
			data = map[string]any{}
		}
		if c != nil {
			if _, exists := data["request_id"]; !exists {
				data["request_id"] = requestid.Get(c)
			}
		}
		debugURL := "http://127.0.0.1:7777/event"
		sessionID := "backup-exec-disconnect"
		if raw, err := os.ReadFile(".dbg/backup-exec-disconnect.env"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "DEBUG_SERVER_URL=") {
					debugURL = strings.TrimSpace(strings.TrimPrefix(line, "DEBUG_SERVER_URL="))
				} else if strings.HasPrefix(line, "DEBUG_SESSION_ID=") {
					sessionID = strings.TrimSpace(strings.TrimPrefix(line, "DEBUG_SESSION_ID="))
				}
			}
		}
		payload := map[string]any{
			"sessionId":    sessionID,
			"runId":        "pre-fix",
			"hypothesisId": hypothesisID,
			"location":     location,
			"msg":          "[DEBUG] " + msg,
			"data":         data,
			"ts":           time.Now().UnixMilli(),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return
		}
		req, err := http.NewRequest(http.MethodPost, debugURL, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 800 * time.Millisecond}
		resp, err := client.Do(req)
		if err == nil && resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
}

// #endregion

func GetBackupInstancesView(c *gin.Context) {
	var form *forms.GetInstancesForm = &forms.GetInstancesForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetInstancesService{GetInstancesForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetBackupObjectsView(c *gin.Context) {
	var form forms.GetBackupObjectsForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetBackupObjectsService{GetBackupObjectsForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetBackupNodesView(c *gin.Context) {
	var form *forms.GetBackupNodesForm = &forms.GetBackupNodesForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetBackupNodesService{GetBackupNodesForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetBackupNodeDetailView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.GetBackupNodeDetailService{ID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetBackupNodeTokenView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.GetBackupNodeTokenService{ID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateBackupNodeView(c *gin.Context) {
	var form forms.CreateBackupNodeForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateBackupNodeService{CreateBackupNodeForm: &form, C: c}
	node, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_node",
			ModuleName:     "备份节点",
			ActionCode:     "create",
			ActionName:     "创建备份节点",
			ActionResult:   "fail",
			ResourceType:   "backup_node",
			ResourceName:   form.Name,
			Summary:        "创建备份节点失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_node",
		ModuleName:     "备份节点",
		ActionCode:     "create",
		ActionName:     "创建备份节点",
		ActionResult:   "success",
		ResourceType:   "backup_node",
		ResourceID:     fmt.Sprintf("%d", node.ID),
		ResourceName:   node.Name,
		Summary:        "创建备份节点",
		RequestPayload: form,
	})
	response.Success(c, node, "success")
}

func UpdateBackupNodeView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.UpdateBackupNodeForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateBackupNodeService{UpdateBackupNodeForm: &form, C: c, ID: id}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_node",
			ModuleName:     "备份节点",
			ActionCode:     "update",
			ActionName:     "修改备份节点",
			ActionResult:   "fail",
			ResourceType:   "backup_node",
			ResourceID:     fmt.Sprintf("%d", id),
			ResourceName:   form.Name,
			Summary:        "修改备份节点失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_node",
		ModuleName:     "备份节点",
		ActionCode:     "update",
		ActionName:     "修改备份节点",
		ActionResult:   "success",
		ResourceType:   "backup_node",
		ResourceID:     fmt.Sprintf("%d", id),
		ResourceName:   form.Name,
		Summary:        "修改备份节点",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

func OperateBackupNodeView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.OperateBackupNodeForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.OperateBackupNodeService{OperateBackupNodeForm: &form, C: c, ID: id}
	data, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_node",
			ModuleName:     "备份节点",
			ActionCode:     form.Action,
			ActionName:     "执行节点远程操作",
			ActionResult:   "fail",
			ResourceType:   "backup_node",
			ResourceID:     fmt.Sprintf("%d", id),
			Summary:        "执行节点远程操作失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_node",
		ModuleName:     "备份节点",
		ActionCode:     form.Action,
		ActionName:     "执行节点远程操作",
		ActionResult:   "success",
		ResourceType:   "backup_node",
		ResourceID:     fmt.Sprintf("%d", id),
		Summary:        "执行节点远程操作",
		RequestPayload: form,
	})
	response.Success(c, data, "success")
}

func GetBackupTasksView(c *gin.Context) {
	var form *forms.GetBackupTasksForm = &forms.GetBackupTasksForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetBackupTasksService{GetBackupTasksForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetBackupTaskDetailView(c *gin.Context) {
	id, ok := parseStringParam(c, "id")
	if !ok {
		return
	}
	service := services.GetBackupTaskDetailService{C: c, ID: id}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func PrecheckBackupTaskView(c *gin.Context) {
	var form forms.PrecheckBackupTaskForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if global.App.Log != nil {
		rawDriverConfig := map[string]any{}
		for k, v := range form.DriverConfig {
			rawDriverConfig[k] = v
		}
		global.App.Log.WithFields(logrus.Fields{
			"request_id":         requestid.Get(c),
			"task_id":            strings.TrimSpace(form.TaskID),
			"source_instance_id": form.SourceInstanceID,
			"driver_type":        strings.TrimSpace(form.DriverType),
			"backup_type":        strings.TrimSpace(form.BackupType),
			"driver_config_keys": strings.Join(mapKeysSorted(rawDriverConfig), ","),
			"driver_config_raw":  rawDriverConfig,
			"os_user":            strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["os_user"])),
			"process_user":       strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["process_user"])),
			"db_os_user":         strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["db_os_user"])),
			"tool_path":          strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["tool_path"])),
			"dump_path":          strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["dump_path"])),
			"pg_dump_path":       strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["pg_dump_path"])),
		}).Info("备份预检查请求绑定完成")
	}
	service := services.PrecheckBackupTaskService{PrecheckBackupTaskForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateBackupTaskView(c *gin.Context) {
	var form forms.CreateBackupTaskForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	ctx2, cancel := context.WithTimeout(c.Request.Context(), 170*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx2)
	service := services.CreateBackupTaskService{CreateBackupTaskForm: &form, C: c}
	task, err := service.Run()
	if ctx2.Err() != nil && err == nil {
		err = &services.PhysicalBackupPrepareError{
			Stage:      "timeout",
			Title:      "备份程序准备超时",
			Suggestion: "请检查备份节点的 SSH 连接、CPU 和内存使用情况，稍后重试",
			Detail:     "后端处理超过 170 秒上限，已强制终止",
			RawError:   ctx2.Err().Error(),
		}
	}
	if err != nil {
		var prepareErr *services.PhysicalBackupPrepareError
		castOk := false
		if err != nil {
			prepareErr, castOk = services.AsPhysicalBackupPrepareError(err)
		}
		errorData := interface{}(nil)
		errorMessage := err.Error()
		if castOk && prepareErr != nil {
			errorData = prepareErr
			if prepareErr.Title != "" {
				errorMessage = prepareErr.Title
				if prepareErr.Suggestion != "" {
					errorMessage += "；" + prepareErr.Suggestion
				}
			}
		}
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_task",
			ModuleName:     "备份任务",
			ActionCode:     "create",
			ActionName:     "创建备份任务",
			ActionResult:   "fail",
			ResourceType:   "backup_task",
			ResourceName:   form.Name,
			Summary:        "创建备份任务失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		if errorData != nil {
			response.FailWithData(c, errorMessage, errorData)
		} else {
			response.Fail(c, errorMessage)
		}
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_task",
		ModuleName:     "备份任务",
		ActionCode:     "create",
		ActionName:     "创建备份任务",
		ActionResult:   "success",
		ResourceType:   "backup_task",
		ResourceID:     task.TaskID,
		ResourceName:   task.Name,
		Summary:        "创建备份任务",
		RequestPayload: form,
	})
	if task != nil && strings.EqualFold(strings.TrimSpace(string(task.DriverType)), "PG_BACKREST") {
		go services.AsyncPrepareTask(task.TaskID)
	}
	response.Success(c, task, "success")
}

func UpdateBackupTaskView(c *gin.Context) {
	id, ok := parseStringParam(c, "id")
	if !ok {
		return
	}
	var form forms.UpdateBackupTaskForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	ctx2, cancel := context.WithTimeout(c.Request.Context(), 170*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx2)
	service := services.UpdateBackupTaskService{UpdateBackupTaskForm: &form, C: c, ID: id}
	err := service.Run()
	if ctx2.Err() != nil && err == nil {
		err = &services.PhysicalBackupPrepareError{
			Stage:      "timeout",
			Title:      "备份程序准备超时",
			Suggestion: "请检查备份节点的 SSH 连接、CPU 和内存使用情况，稍后重试",
			Detail:     "后端处理超过 170 秒上限，已强制终止",
			RawError:   ctx2.Err().Error(),
		}
	}
	if err != nil {
		var prepareErr *services.PhysicalBackupPrepareError
		castOk := false
		if err != nil {
			prepareErr, castOk = services.AsPhysicalBackupPrepareError(err)
		}
		errorData := interface{}(nil)
		errorMessage := err.Error()
		auditSummary := "修改备份任务失败"
		if castOk && prepareErr != nil {
			errorData = prepareErr
			if prepareErr.Title != "" {
				errorMessage = prepareErr.Title
				if prepareErr.Suggestion != "" {
					errorMessage += "；" + prepareErr.Suggestion
				}
			}
		}
		// 保留期缩短/存在超期备份时需要二次确认：本次请求尚未生效，属于「待确认」
		// 而非系统故障。把影响明细一并下发，前端确认后带 confirm_retention_reconcile 重试。
		if reconcileErr, isReconcileConfirm := services.AsRetentionReconcileRequiredError(err); isReconcileConfirm {
			errorData = reconcileErr.Impact
			errorMessage = reconcileErr.Error()
			auditSummary = "修改备份任务：保留期对齐待确认"
		}
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_task",
			ModuleName:     "备份任务",
			ActionCode:     "update",
			ActionName:     "修改备份任务",
			ActionResult:   "fail",
			ResourceType:   "backup_task",
			ResourceID:     id,
			ResourceName:   form.Name,
			Summary:        auditSummary,
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		if errorData != nil {
			response.FailWithData(c, errorMessage, errorData)
		} else {
			response.Fail(c, errorMessage)
		}
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_task",
		ModuleName:     "备份任务",
		ActionCode:     "update",
		ActionName:     "修改备份任务",
		ActionResult:   "success",
		ResourceType:   "backup_task",
		ResourceID:     id,
		ResourceName:   form.Name,
		Summary:        "修改备份任务",
		RequestPayload: form,
	})
	if strings.TrimSpace(id) != "" {
		if strings.EqualFold(strings.TrimSpace(form.DriverType), "PG_BACKREST") {
			go services.AsyncPrepareTask(id)
		} else {
			var t backupModels.InsightBackupTasks
			silentDB := global.App.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			if silentDB.Where("task_id = ?", id).Select("driver_type").Take(&t).Error == nil {
				if strings.EqualFold(strings.TrimSpace(string(t.DriverType)), "PG_BACKREST") {
					go services.AsyncPrepareTask(id)
				}
			}
		}
	}
	response.Success(c, nil, "success")
}

// UpdateBackupTaskNameView 轻量改名：只写 name 字段。
// 不触发驱动规范化/节点 ensure/目录解析，因此不会因为 executor_node_id 而失败。
func UpdateBackupTaskNameView(c *gin.Context) {
	id, ok := parseStringParam(c, "id")
	if !ok {
		return
	}
	var form forms.UpdateBackupTaskNameForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateBackupTaskNameService{UpdateBackupTaskNameForm: &form, C: c, ID: id}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_task",
			ModuleName:     "备份任务",
			ActionCode:     "update_name",
			ActionName:     "修改备份任务名称",
			ActionResult:   "fail",
			ResourceType:   "backup_task",
			ResourceID:     id,
			ResourceName:   form.Name,
			Summary:        "修改备份任务名称失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_task",
		ModuleName:     "备份任务",
		ActionCode:     "update_name",
		ActionName:     "修改备份任务名称",
		ActionResult:   "success",
		ResourceType:   "backup_task",
		ResourceID:     id,
		ResourceName:   form.Name,
		Summary:        "修改备份任务名称",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

func DeleteBackupTaskView(c *gin.Context) {
	id, ok := parseStringParam(c, "id")
	if !ok {
		return
	}
	var form forms.DeleteBackupTaskForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.DeleteBackupTaskService{DeleteBackupTaskForm: &form, C: c, ID: id}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_task",
			ModuleName:     "备份任务",
			ActionCode:     "delete",
			ActionName:     "删除备份任务",
			ActionResult:   "fail",
			ResourceType:   "backup_task",
			ResourceID:     id,
			ResourceName:   form.ConfirmName,
			Summary:        "删除备份任务失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_task",
		ModuleName:     "备份任务",
		ActionCode:     "delete",
		ActionName:     "删除备份任务",
		ActionResult:   "success",
		ResourceType:   "backup_task",
		ResourceID:     id,
		ResourceName:   form.ConfirmName,
		Summary:        "删除备份任务",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

func RunBackupTaskView(c *gin.Context) {
	id, ok := parseStringParam(c, "id")
	if !ok {
		return
	}
	// #region debug-point A:run-backup-task-view-entry
	reportBackupExecDebugFromView(c, "A", "backup/views/backup.go:RunBackupTaskView:entry", "run backup task request received", map[string]any{
		"request_id": requestid.Get(c),
		"task_id":    id,
		"path":       c.Request.URL.Path,
		"method":     c.Request.Method,
	})
	// #endregion
	ctx2, cancel := context.WithTimeout(c.Request.Context(), 170*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx2)
	service := services.RunBackupTaskService{C: c, ID: id}
	run, err := service.Run()
	if ctx2.Err() != nil && err == nil {
		err = &services.PhysicalBackupPrepareError{
			Stage:      "timeout",
			Title:      "备份程序准备超时",
			Suggestion: "请检查备份节点的 SSH 连接、CPU 和内存使用情况，稍后重试",
			Detail:     "后端处理超过 170 秒上限，已强制终止",
			RawError:   ctx2.Err().Error(),
		}
	}
	if err != nil {
		// #region debug-point A:run-backup-task-view-error
		reportBackupExecDebugFromView(c, "A", "backup/views/backup.go:RunBackupTaskView:error", "run backup task request failed", map[string]any{
			"request_id": requestid.Get(c),
			"task_id":    id,
			"error":      err.Error(),
			"ctx_done":   ctx2.Err() != nil,
		})
		// #endregion
		var prepareErr *services.PhysicalBackupPrepareError
		castOk := false
		if err != nil {
			prepareErr, castOk = services.AsPhysicalBackupPrepareError(err)
		}
		errorData := interface{}(nil)
		errorMessage := err.Error()
		if castOk && prepareErr != nil {
			errorData = prepareErr
			if prepareErr.Title != "" {
				errorMessage = prepareErr.Title
				if prepareErr.Suggestion != "" {
					errorMessage += "；" + prepareErr.Suggestion
				}
			}
		}
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:   "high",
			ModuleCode:   "backup_task",
			ModuleName:   "备份任务",
			ActionCode:   "run",
			ActionName:   "执行备份任务",
			ActionResult: "fail",
			ResourceType: "backup_task",
			ResourceID:   id,
			ResourceName: id,
			Summary:      "执行备份任务失败",
			ErrorMessage: err.Error(),
		})
		if errorData != nil {
			response.FailWithData(c, errorMessage, errorData)
		} else {
			response.Fail(c, errorMessage)
		}
		return
	}
	// #region debug-point A:run-backup-task-view-success
	reportBackupExecDebugFromView(c, "A", "backup/views/backup.go:RunBackupTaskView:success", "run backup task request succeeded", map[string]any{
		"request_id": requestid.Get(c),
		"task_id":    id,
		"run_id":     run.ID,
		"run_status": strings.TrimSpace(string(run.Status)),
		"driver":     strings.TrimSpace(string(run.DriverType)),
	})
	// #endregion
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:   "high",
		ModuleCode:   "backup_task",
		ModuleName:   "备份任务",
		ActionCode:   "run",
		ActionName:   "执行备份任务",
		ActionResult: "success",
		ResourceType: "backup_run",
		ResourceID:   id,
		ResourceName: id,
		Summary:      "执行备份任务",
		AfterData:    run,
	})
	if run != nil {
		// 统一入口：手动 / 定时两条路径共用同一套 prepare 驱动逻辑，
		// 避免此处与 scheduler 各写一份而再次漂移。
		services.DispatchBackupRunPrepare(run)
	}
	response.Success(c, run, "success")
}

func GetBackupTaskRunsView(c *gin.Context) {
	id, ok := parseStringParam(c, "id")
	if !ok {
		return
	}
	// #region debug-point C:get-backup-task-runs-entry
	reportBackupExecDebugFromView(c, "C", "backup/views/backup.go:GetBackupTaskRunsView:entry", "get backup task runs request received", map[string]any{
		"request_id": requestid.Get(c),
		"task_id":    id,
	})
	// #endregion
	service := services.GetBackupTaskRunsService{TaskID: id, C: c}
	data, err := service.Run()
	if err != nil {
		// #region debug-point C:get-backup-task-runs-error
		reportBackupExecDebugFromView(c, "C", "backup/views/backup.go:GetBackupTaskRunsView:error", "get backup task runs request failed", map[string]any{
			"request_id": requestid.Get(c),
			"task_id":    id,
			"error":      err.Error(),
		})
		// #endregion
		response.Fail(c, err.Error())
		return
	}
	// #region debug-point C:get-backup-task-runs-success
	reportBackupExecDebugFromView(c, "C", "backup/views/backup.go:GetBackupTaskRunsView:success", "get backup task runs request succeeded", map[string]any{
		"request_id": requestid.Get(c),
		"task_id":    id,
	})
	// #endregion
	response.Success(c, data, "success")
}

func GetBackupRunLogView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	// #region debug-point D:get-backup-run-log-entry
	reportBackupExecDebugFromView(c, "D", "backup/views/backup.go:GetBackupRunLogView:entry", "get backup run log request received", map[string]any{
		"request_id": requestid.Get(c),
		"run_id":     id,
	})
	// #endregion
	var form forms.GetBackupRunLogForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetBackupRunLogService{RunID: id, GetBackupRunLogForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		// #region debug-point D:get-backup-run-log-error
		reportBackupExecDebugFromView(c, "D", "backup/views/backup.go:GetBackupRunLogView:error", "get backup run log request failed", map[string]any{
			"request_id": requestid.Get(c),
			"run_id":     id,
			"error":      err.Error(),
		})
		// #endregion
		response.Fail(c, err.Error())
		return
	}
	// #region debug-point D:get-backup-run-log-success
	reportBackupExecDebugFromView(c, "D", "backup/views/backup.go:GetBackupRunLogView:success", "get backup run log request succeeded", map[string]any{
		"request_id": requestid.Get(c),
		"run_id":     id,
	})
	// #endregion
	response.Success(c, data, "success")
}

func StopBackupRunView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.StopBackupRunService{RunID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetBackupSetsView(c *gin.Context) {
	var form *forms.GetBackupSetsForm = &forms.GetBackupSetsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetBackupSetsService{GetBackupSetsForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetBackupSetDetailView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.GetBackupSetDetailService{SetID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetBackupSetObjectsView 枚举备份集内可恢复对象（恢复向导第 2 步「恢复对象」）。
func GetBackupSetObjectsView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	data, err := services.GetBackupSetObjectsService(id)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func StopBackupSetView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.StopBackupSetService{SetID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func DeleteBackupSetView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.DeleteBackupSetService{SetID: id, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func BatchDeleteBackupSetsView(c *gin.Context) {
	var form forms.BatchDeleteBackupSetsForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.DeleteBackupSetsService{SetIDs: form.IDs, C: c}
	result, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:   "high",
		ModuleCode:   "backup_set",
		ModuleName:   "备份集",
		ActionCode:   "batch_delete",
		ActionName:   "批量删除备份集",
		ActionResult: "success",
		ResourceType: "backup_set",
		Summary:      fmt.Sprintf("请求 %d 个，成功 %d 个，失败 %d 个", len(form.IDs), result.DeletedCount, result.FailedCount),
	})
	response.Success(c, result, "success")
}

func DownloadBackupSetView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	set, err := services.LoadBackupSet(id)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	// 下载备份集 = 把整份数据带走，必须持有源实例/源库的 export 授权。
	// 此前只校验了 RBAC 功能权限（能进备份模块就能下载），与数据授权完全脱节。
	if err := services.CheckBackupSetAccess(services.CurrentUsername(c), set, dasServices.DataActionExport); err != nil {
		response.Fail(c, err.Error())
		return
	}
	file, err := services.ValidateBackupSetFile(set)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:   "high",
		ModuleCode:   "backup_set",
		ModuleName:   "备份集",
		ActionCode:   "download",
		ActionName:   "下载备份集",
		ActionResult: "success",
		ResourceType: "backup_set",
		ResourceID:   fmt.Sprintf("%d", id),
		ResourceName: set.Name,
		Summary:      "下载备份集",
		AfterData:    map[string]any{"file_name": set.FileName, "size_bytes": set.SizeBytes},
	})
	c.FileAttachment(file, set.FileName)
}

func GetRestoreRunsView(c *gin.Context) {
	var form *forms.GetRestoreRunsForm = &forms.GetRestoreRunsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetRestoreRunsService{GetRestoreRunsForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

// PrecheckRestoreRunView 恢复向导第 3 步的预检入口。
// 与 CreateRestoreRunView(mode=plan) 的区别：本接口不落库、不产生恢复任务，
// 物理/逻辑按备份集驱动类型自动分流，前端只需调用这一个。
func PrecheckRestoreRunView(c *gin.Context) {
	var form forms.PrecheckRestoreRunForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.PrecheckRestoreRunService{PrecheckRestoreRunForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateRestoreRunView(c *gin.Context) {
	var form forms.CreateRestoreRunForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateRestoreRunService{CreateRestoreRunForm: &form, C: c}
	rr, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_restore",
			ModuleName:     "备份恢复",
			ActionCode:     "create_restore",
			ActionName:     "创建数据恢复",
			ActionResult:   "fail",
			ResourceType:   "restore_run",
			ResourceName:   form.TargetDBName,
			Summary:        "创建数据恢复失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_restore",
		ModuleName:     "备份恢复",
		ActionCode:     "create_restore",
		ActionName:     "创建数据恢复",
		ActionResult:   "success",
		ResourceType:   "restore_run",
		ResourceID:     fmt.Sprintf("%d", rr.ID),
		ResourceName:   form.TargetDBName,
		Summary:        "创建数据恢复任务",
		RequestPayload: form,
		AfterData:      rr,
	})
	response.Success(c, rr, "success")
}

func UpdateRestoreRunView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.UpdateRestoreRunForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateRestoreRunService{UpdateRestoreRunForm: &form, RestoreRunID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// UpdateRestoreRunNameView 轻量改名：只写 name 字段，逻辑/物理恢复共用。
func UpdateRestoreRunNameView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.UpdateRestoreRunNameForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateRestoreRunNameService{UpdateRestoreRunNameForm: &form, C: c, RestoreRunID: id}
	if err := service.Run(); err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_restore",
			ModuleName:     "数据恢复",
			ActionCode:     "update_name",
			ActionName:     "修改恢复任务名称",
			ActionResult:   "fail",
			ResourceType:   "restore_run",
			ResourceID:     fmt.Sprintf("%d", id),
			ResourceName:   form.Name,
			Summary:        "修改恢复任务名称失败",
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_restore",
		ModuleName:     "数据恢复",
		ActionCode:     "update_name",
		ActionName:     "修改恢复任务名称",
		ActionResult:   "success",
		ResourceType:   "restore_run",
		ResourceID:     fmt.Sprintf("%d", id),
		ResourceName:   form.Name,
		Summary:        "修改恢复任务名称",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

func DeleteRestoreRunView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.DeleteRestoreRunService{RestoreRunID: id, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func GetRestoreRunDetailView(c *gin.Context) {
	var uri forms.RestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetRestoreRunDetailService{RestoreRunID: uri.ID, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetRestoreRunLogView(c *gin.Context) {
	var uri forms.RestoreRunIDForm
	if err := c.ShouldBindUri(&uri); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	var form forms.GetRestoreRunLogForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetRestoreRunLogService{RestoreRunID: uri.ID, GetRestoreRunLogForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func StopRestoreRunView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.StopRestoreRunService{RestoreRunID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func RetryRestoreRunView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.RetryRestoreRunService{RestoreRunID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func DownloadRestoreRunLogView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.GetRestoreRunLogForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ResolveRestoreRunLogFileService{
		RestoreRunID: id,
		ItemID:       form.ItemID,
	}
	file, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	c.FileAttachment(file.FilePath, file.FileName)
}

func AgentHeartbeatView(c *gin.Context) {
	var form forms.AgentHeartbeatForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AgentHeartbeatService{AgentHeartbeatForm: &form, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AgentPollJobView(c *gin.Context) {
	var form forms.AgentPollJobForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AgentPollJobService{AgentPollJobForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func AgentReportRunView(c *gin.Context) {
	var form forms.AgentReportRunForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AgentReportRunService{AgentReportRunForm: &form, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func AgentRunStopStatusView(c *gin.Context) {
	var form forms.AgentRunStopStatusForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.AgentRunStopStatusService{AgentRunStopStatusForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetStorageTargetsView(c *gin.Context) {
	var form forms.GetStorageTargetsForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetStorageTargetsService{GetStorageTargetsForm: &form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetStorageTargetDetailView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.GetStorageTargetDetailService{ID: id, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func CreateStorageTargetView(c *gin.Context) {
	var form forms.CreateStorageTargetForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateStorageTargetService{CreateStorageTargetForm: &form, C: c}
	target, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_storage_target",
			ActionCode:     "create",
			ActionName:     "新增存储目标",
			ActionResult:   "failed",
			ResourceType:   "backup_storage_target",
			ResourceName:   form.Name,
			Summary:        "新增存储目标失败: " + err.Error(),
			ErrorMessage:   err.Error(),
			RequestPayload: hideCredentials(form),
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_storage_target",
		ActionCode:     "create",
		ActionName:     "新增存储目标",
		ActionResult:   "succeed",
		ResourceType:   "backup_storage_target",
		ResourceID:     fmt.Sprintf("%d", target.ID),
		ResourceName:   target.Name,
		Summary:        fmt.Sprintf("新增存储目标成功: %s(%s)", target.Name, target.Type),
		RequestPayload: hideCredentials(form),
	})
	response.Success(c, target, "success")
}

func UpdateStorageTargetView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.UpdateStorageTargetForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateStorageTargetService{UpdateStorageTargetForm: &form, C: c, ID: id}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_storage_target",
			ActionCode:     "update",
			ActionName:     "更新存储目标",
			ActionResult:   "failed",
			ResourceType:   "backup_storage_target",
			ResourceID:     fmt.Sprintf("%d", id),
			ResourceName:   form.Name,
			Summary:        "更新存储目标失败: " + err.Error(),
			ErrorMessage:   err.Error(),
			RequestPayload: hideCredentials(form),
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_storage_target",
		ActionCode:     "update",
		ActionName:     "更新存储目标",
		ActionResult:   "succeed",
		ResourceType:   "backup_storage_target",
		ResourceID:     fmt.Sprintf("%d", id),
		ResourceName:   form.Name,
		Summary:        "更新存储目标成功",
		RequestPayload: hideCredentials(form),
	})
	response.Success(c, nil, "success")
}

func DeleteStorageTargetView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	service := services.DeleteStorageTargetService{C: c, ID: id}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:   "high",
			ModuleCode:   "backup_storage_target",
			ActionCode:   "delete",
			ActionName:   "删除存储目标",
			ActionResult: "failed",
			ResourceType: "backup_storage_target",
			ResourceID:   fmt.Sprintf("%d", id),
			Summary:      "删除存储目标失败: " + err.Error(),
			ErrorMessage: err.Error(),
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:   "high",
		ModuleCode:   "backup_storage_target",
		ActionCode:   "delete",
		ActionName:   "删除存储目标",
		ActionResult: "succeed",
		ResourceType: "backup_storage_target",
		ResourceID:   fmt.Sprintf("%d", id),
		Summary:      "删除存储目标成功",
	})
	response.Success(c, nil, "success")
}

func PrecheckStorageTargetView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.PrecheckStorageTargetForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.PrecheckStorageTargetService{PrecheckStorageTargetForm: &form, C: c, ID: id}
	result, err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:   "high",
			ModuleCode:   "backup_storage_target",
			ActionCode:   "precheck",
			ActionName:   "存储目标连通测试",
			ActionResult: "failed",
			ResourceType: "backup_storage_target",
			ResourceID:   fmt.Sprintf("%d", id),
			Summary:      "存储目标连通测试失败: " + err.Error(),
			ErrorMessage: err.Error(),
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:   "high",
		ModuleCode:   "backup_storage_target",
		ActionCode:   "precheck",
		ActionName:   "存储目标连通测试",
		ActionResult: ternary(result.OK, "succeed", "failed"),
		ResourceType: "backup_storage_target",
		ResourceID:   fmt.Sprintf("%d", id),
		Summary:      fmt.Sprintf("存储目标连通测试完成，结果: %v %s", result.OK, result.ErrMessage),
	})
	response.Success(c, result, "success")
}

func ConfirmStorageTargetFingerprintView(c *gin.Context) {
	id, ok := parseUint64Param(c, "id")
	if !ok {
		return
	}
	var form forms.ConfirmFingerprintForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ConfirmStorageTargetFingerprintService{ConfirmFingerprintForm: &form, C: c, ID: id}
	err := service.Run()
	if err != nil {
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			AuditLevel:     "high",
			ModuleCode:     "backup_storage_target",
			ActionCode:     "confirm_fingerprint",
			ActionName:     "确认存储目标指纹",
			ActionResult:   "failed",
			ResourceType:   "backup_storage_target",
			ResourceID:     fmt.Sprintf("%d", id),
			Summary:        "确认存储目标指纹失败: " + err.Error(),
			ErrorMessage:   err.Error(),
			RequestPayload: form,
		})
		response.Fail(c, err.Error())
		return
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
		AuditLevel:     "high",
		ModuleCode:     "backup_storage_target",
		ActionCode:     "confirm_fingerprint",
		ActionName:     "确认存储目标指纹",
		ActionResult:   "succeed",
		ResourceType:   "backup_storage_target",
		ResourceID:     fmt.Sprintf("%d", id),
		Summary:        "确认存储目标指纹成功",
		RequestPayload: form,
	})
	response.Success(c, nil, "success")
}

func ternary(ok bool, s1, s2 string) string {
	if ok {
		return s1
	}
	return s2
}
