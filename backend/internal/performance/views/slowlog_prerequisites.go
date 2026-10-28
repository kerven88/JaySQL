package views

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/performance/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// GetSlowLogPrerequisitesView 慢 SQL 采集前提条件清单。
//
// 前端「采集前提条件」面板的唯一数据源：不传 db_type 时返回三种引擎的全量清单，
// 传了则只返回该引擎的清单。文案由后端声明库统一下发，前端不硬编码。
func GetSlowLogPrerequisitesView(c *gin.Context) {
	service := services.GetSlowLogPrerequisitesService{
		DBType: strings.TrimSpace(c.Query("db_type")),
		C:      c,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetSlowLogDiagnosisView 全部可见实例的采集诊断结论（含失败 → 前提项关联）。
//
// 慢 SQL 列表的【状态】列直接消费这里的 prerequisite_keys：
// 采集正常时只显示「正常」；异常时显示「库类型 + 具体原因」，
// 并在旁边提供跳转到对应前提项的入口。
func GetSlowLogDiagnosisView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.GetSlowLogDiagnosisService{Username: username}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// GetSlowLogInstancePrerequisitesView 单个实例的前提清单 + 它自己的失败关联。
//
// 用于实例详情抽屉：既回答「这类库要开什么」，也回答「这台卡在哪一项」。
func GetSlowLogInstancePrerequisitesView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.GetSlowLogInstancePrerequisitesService{
		InstanceID: strings.TrimSpace(c.Param("instance_id")),
		Username:   username,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
