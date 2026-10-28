package views

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"
)

func GetAuditLogsView(c *gin.Context) {
	var form forms.GetAuditLogsForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GetAuditLogsService{
		GetAuditLogsForm: &form,
		C:                c,
	}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func GetAuditLogDetailView(c *gin.Context) {
	auditID := strings.TrimSpace(c.Param("audit_id"))
	if auditID == "" {
		response.ValidateFail(c, "非法参数: audit_id")
		return
	}
	service := services.GetAuditLogDetailService{
		C:       c,
		AuditID: auditID,
	}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}
