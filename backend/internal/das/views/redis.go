package views

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/pkg/response"
)

func ScanRedisKeysView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.RedisScanForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := services.ScanRedisKeys(ctx, username, form)
	outcome := "success"
	if err != nil {
		outcome = "fail"
	}
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{Username: username, ModuleCode: "sql_query", ModuleName: "Redis Key 查询", ActionCode: "redis_scan", ActionName: "扫描 Redis Key", ActionResult: outcome, ResourceType: "instance", ResourceID: form.InstanceID, RequestPayload: form})
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "success")
}
func ReadRedisValueView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.RedisValueForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := services.ReadRedisValue(ctx, username, form)
	outcome := "success"
	if err != nil {
		outcome = "fail"
	}
	// Only request metadata is audited; values and Redis credentials never enter logs.
	commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{Username: username, ModuleCode: "sql_query", ModuleName: "Redis Key 查询", ActionCode: "redis_read", ActionName: "读取 Redis Key", ActionResult: outcome, ResourceType: "instance", ResourceID: form.InstanceID, RequestPayload: form})
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "success")
}
