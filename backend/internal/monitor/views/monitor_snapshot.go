package views

import (
	"strconv"

	"github.com/gin-gonic/gin"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/internal/monitor/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"
)

// 负载快照视图层：首页实例列表 / 快照列表 / 生成 / 详情 / 删除。
// 权限口径：查看 monitor.view、生成与删除 monitor.manage（与监控模块一致）。

// GetLoadSnapshotInstancesView 首页实例列表（8 列数据一次带回）。
func GetLoadSnapshotInstancesView(c *gin.Context) {
	var form services.ListLoadSnapshotInstancesService
	form.Search = c.Query("search")
	form.DbType = c.Query("db_type")
	form.Environment = c.Query("environment")
	items, err := form.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"items": items}, "success")
}

// GetLoadSnapshotListView 实例的快照历史列表（不含 payload，列表场景不需要全量报告）。
func GetLoadSnapshotListView(c *gin.Context) {
	instanceID := c.Query("instance_id")
	if instanceID == "" {
		response.Fail(c, "instance_id 不能为空")
		return
	}
	var rows []monitorModels.InsightMonitorLoadSnapshots
	if err := global.App.DB.Omit("payload").
		Where("instance_id = ?", instanceID).
		Order("id desc").Limit(200).Find(&rows).Error; err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"items": rows}, "success")
}

// CreateLoadSnapshotView 生成快照任务（异步：pending → 基线 → 窗口 → 差分 → ready）。
func CreateLoadSnapshotView(c *gin.Context) {
	var form services.CreateLoadSnapshotService
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	form.CreatedBy = operator
	id, err := form.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{"id": id}, "快照任务已提交")
}

// GetLoadSnapshotDetailView 快照详情（含 payload 全量报告）。
func GetLoadSnapshotDetailView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "快照 ID 不合法")
		return
	}
	svc := services.GetLoadSnapshotDetailService{ID: id}
	record, err := svc.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "success")
}

// DeleteLoadSnapshotView 删除快照。
func DeleteLoadSnapshotView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "快照 ID 不合法")
		return
	}
	svc := services.DeleteLoadSnapshotService{ID: id}
	if err := svc.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已删除")
}
