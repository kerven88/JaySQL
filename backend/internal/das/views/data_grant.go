package views

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// ---------------------------------------------------------------------------
// 细粒度数据授权
//
// 两条入口：
//   /api/v1/das/data-grants/*   —— 自助授权，任何持有 system.das.manage 的人
//                                  可以为「自己」发起授权并立即生效；
//   /api/v1/admin/das/data-grants/* —— 管理端，为任意用户/角色授权。
// 两者的写入路径共用 services 层，都会落 insight_das_grant_audit 流水。
// ---------------------------------------------------------------------------

// GetMyDataGrantsView 我的授权（自助页）
func GetMyDataGrantsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.DataGrantListForm{Scope: "self"}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	form.Scope = "self" // 强制自助视角，防止传参越权看别人的授权
	service := services.ListDataGrantsService{DataGrantListForm: form, C: c, Username: username}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

// CreateSelfDataGrantView 自助授权：主体强制为本人
func CreateSelfDataGrantView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.DataGrantCreateForm{Self: true}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	form.Self = true // 只可能为本人授权
	service := services.CreateDataGrantService{
		DataGrantCreateForm: form,
		C:                   c,
		Username:            username,
		SourceIP:            c.ClientIP(),
	}
	grant, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"grant_no": grant.GrantNo}, "授权已生效")
}

// ListDataGrantsView 管理端授权列表
func ListDataGrantsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.DataGrantListForm{}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListDataGrantsService{DataGrantListForm: form, C: c, Username: username}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

// CreateDataGrantView 管理端新增授权
func CreateDataGrantView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.DataGrantCreateForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateDataGrantService{
		DataGrantCreateForm: form,
		C:                   c,
		Username:            username,
		SourceIP:            c.ClientIP(),
	}
	grant, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"grant_no": grant.GrantNo}, "授权已生效")
}

// RevokeDataGrantView 撤销授权
func RevokeDataGrantView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.DataGrantRevokeForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.RevokeDataGrantService{
		DataGrantRevokeForm: form,
		C:                   c,
		Username:            username,
		SourceIP:            c.ClientIP(),
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已撤销")
}

// RenewDataGrantView 续期
func RenewDataGrantView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.DataGrantRenewForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.RenewDataGrantService{
		DataGrantRenewForm: form,
		C:                  c,
		Username:           username,
		SourceIP:           c.ClientIP(),
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已续期")
}

// ListDataGrantAuditsView 授权变更流水
func ListDataGrantAuditsView(c *gin.Context) {
	form := &forms.DataGrantAuditListForm{}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListDataGrantAuditsService{DataGrantAuditListForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}
