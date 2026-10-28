package views

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/performance/forms"
	"github.com/simajiong/jaysql/internal/performance/services"
	"github.com/simajiong/jaysql/pkg/response"
)

func GetInstanceOptionsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetInstanceOptionsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetInstanceOptionsService{Form: &form, C: c, Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetOverviewView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetOverviewForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetOverviewService{Form: &form, C: c, Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetSlowSQLView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetSlowSQLForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetSlowSQLService{Form: &form, C: c, Username: username}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetSlowSQLInstanceSummaryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetSlowSQLInstanceSummaryForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetSlowSQLInstanceSummaryService{Form: &form, C: c, Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetSlowSQLAnalysisView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetSlowSQLAnalysisForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetSlowSQLAnalysisService{Form: &form, C: c, Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func GetSlowSQLStatsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetSlowSQLStatsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetSlowSQLStatsService{Form: &form, C: c, Username: username}
	data, total, rawTotal, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, map[string]any{
		"rows":        data,
		"group_total": total,
		"raw_total":   rawTotal,
	}, "success")
}

func GetSnapshotParamListView(c *gin.Context) {
	var form forms.GetSnapshotParamListForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetSnapshotParamListService{Form: &form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func CreateSnapshotParamView(c *gin.Context) {
	var form forms.SaveSnapshotParamForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateSnapshotParamService{Form: &form, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func UpdateSnapshotParamView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "参数ID格式错误")
		return
	}
	var form forms.SaveSnapshotParamForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateSnapshotParamService{ID: id, Form: &form, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func DeleteSnapshotParamView(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ValidateFail(c, "参数ID格式错误")
		return
	}
	service := services.DeleteSnapshotParamService{ID: id, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "success")
}

func GetSlowLogCapabilityListView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetInstanceOptionsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetSlowLogCapabilityListService{Form: &form, C: c, Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// ReprobeSlowLogCapabilityView 手动重探某个实例的慢日志能力。
// 语义上等价于「让定时任务立刻跑一次探测」——只刷新能力缓存，不改动用户数据，
// 因此沿用 performance.view 权限，避免只读用户看到提醒却无法自助修复。
func ReprobeSlowLogCapabilityView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	instanceID := strings.TrimSpace(c.Param("instance_id"))
	service := services.ReprobeSlowLogCapabilityService{InstanceID: instanceID, C: c, Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
