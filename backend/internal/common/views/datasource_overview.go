package views

import (
	"github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// DatasourceOverviewView 数据源总览（数据源管理一级菜单）。
// 权限与前端路由 meta 保持同一集合：管理侧任一视角权限即可进入。
func DatasourceOverviewView(c *gin.Context) {
	service := services.DatasourceOverviewServices{C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
