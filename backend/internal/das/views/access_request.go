package views

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/services"
	usersServices "github.com/simajiong/jaysql/internal/users/services"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"
)

// ---------------------------------------------------------------------------
// 数据访问权限申请
//
// 入口统一挂在 /api/v1/das/access-requests 下，权限点三个：
//   das.access.apply    发起 / 撤回自己的申请
//   das.access.approve  审批（通过 / 驳回 / 转交）+ 执行授权
//   das.access.view     查看全部申请记录
//
// 数据范围在这里判定而不在服务层：服务层不持有权限体系，只接受
// CanApprove / CanViewAll / IsAdmin 三个布尔入参，避免「谁能看多少」这种策略散落在 SQL 里。
//
// 执行授权复用 das.access.approve：能审批的人就是能把权限落下去的人（DBA 组），
// 单独再开一个 das.access.grant 权限点需要同步改 RBAC 种子，收益不足。
// ---------------------------------------------------------------------------

// ListAccessRequestsView 申请单列表
func ListAccessRequestsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestListForm{}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListAccessRequestsService{
		AccessRequestListForm: form,
		Username:              username,
		CanApprove:            middleware.UserHasAnyPermission(username, "das.access.approve"),
		CanViewAll:            middleware.UserHasAnyPermission(username, "das.access.view", "das.access.approve"),
	}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

// CreateAccessRequestView 发起申请
func CreateAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestCreateForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateAccessRequestService{
		AccessRequestCreateForm: form,
		Username:                username,
		SourceIP:                c.ClientIP(),
	}
	req, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"request_no": req.RequestNo}, "申请已提交，等待审批")
}

// isAdminUser 管理员旁路判定：超管可处理任何人的申请（但仍不能批自己的）。
// 只看超管，不看 role：普通管理员角色仍需是「当前处理人」——否则等于任何人拿到审批权就能处理别人的单子。
func isAdminUser(username string) bool {
	return usersServices.IsSuperuserByUsername(username)
}

// ApproveAccessRequestView 审批通过（推进到待执行授权，不直接写权限）
func ApproveAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestApproveForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ApproveAccessRequestService{
		AccessRequestApproveForm: form,
		RequestNo:                c.Param("request_no"),
		Username:                 username,
		SourceIP:                 c.ClientIP(),
		IsAdmin:                  isAdminUser(username),
	}
	req, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"request_no": req.RequestNo, "status": req.Status}, "审批通过，等待执行授权")
}

// RejectAccessRequestView 审批驳回
func RejectAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestRejectForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.RejectAccessRequestService{
		AccessRequestRejectForm: form,
		RequestNo:               c.Param("request_no"),
		Username:                username,
		SourceIP:                c.ClientIP(),
		IsAdmin:                 isAdminUser(username),
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已驳回")
}

// ForwardAccessRequestView 转交：换一位审批人继续处理本单
func ForwardAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestForwardForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ForwardAccessRequestService{
		AccessRequestForwardForm: form,
		RequestNo:                c.Param("request_no"),
		Username:                 username,
		SourceIP:                 c.ClientIP(),
		IsAdmin:                  isAdminUser(username),
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"approver_username": form.ApproverUsername}, "已转交，等待 "+form.ApproverUsername+" 处理")
}

// TerminateAccessRequestView 终止（申请人或管理员，仅审批中）
func TerminateAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestTerminateForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.TerminateAccessRequestService{
		AccessRequestTerminateForm: form,
		RequestNo:                  c.Param("request_no"),
		Username:                   username,
		SourceIP:                   c.ClientIP(),
		IsAdmin:                    isAdminUser(username),
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已终止")
}

// GrantAccessRequestView 执行授权：把审批通过的申请写成权限
func GrantAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestGrantForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.GrantAccessRequestService{
		AccessRequestGrantForm: form,
		RequestNo:              c.Param("request_no"),
		Username:               username,
		SourceIP:               c.ClientIP(),
	}
	req, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"request_no": req.RequestNo, "grant_no": req.GrantNo}, "授权已生效")
}

// ListAccessApproverOptionsView 审批人候选（申请页选人、详情页转交共用）
func ListAccessApproverOptionsView(c *gin.Context) {
	service := services.ListAccessApproverOptionsService{
		Search:          c.Query("search"),
		ExcludeUsername: c.Query("exclude"),
	}
	rows, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows, "ok")
}

// WithdrawAccessRequestView 申请人撤回
func WithdrawAccessRequestView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	form := &forms.AccessRequestWithdrawForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.WithdrawAccessRequestService{
		AccessRequestWithdrawForm: form,
		RequestNo:                 c.Param("request_no"),
		Username:                  username,
		SourceIP:                  c.ClientIP(),
	}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已撤回")
}

// GetAccessRequestDetailView 申请单详情（含流转时间线与落地授权）
func GetAccessRequestDetailView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.GetAccessRequestDetailService{
		RequestNo:  c.Param("request_no"),
		Username:   username,
		CanView:    middleware.UserHasAnyPermission(username, "das.access.approve", "das.access.view"),
		CanApprove: middleware.UserHasAnyPermission(username, "das.access.approve"),
		IsAdmin:    isAdminUser(username),
	}
	detail, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, detail, "ok")
}

// ListAccessInstanceOptionsView 申请入口：数据源下拉（不按申请人授权过滤）
func ListAccessInstanceOptionsView(c *gin.Context) {
	form := &forms.AccessInstanceOptionsForm{}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListAccessInstanceOptionsService{AccessInstanceOptionsForm: form}
	rows, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows, "ok")
}

// ListAccessSchemaOptionsView 申请入口：库/Schema 下拉
func ListAccessSchemaOptionsView(c *gin.Context) {
	form := &forms.AccessSchemaOptionsForm{}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListAccessSchemaOptionsService{AccessSchemaOptionsForm: form}
	rows, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows, "ok")
}

// ListAccessTableOptionsView 申请入口：表下拉（服务端连实例实时拉取）
func ListAccessTableOptionsView(c *gin.Context) {
	form := &forms.AccessTableOptionsForm{}
	if err := c.ShouldBind(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListAccessTableOptionsService{AccessTableOptionsForm: form, C: c}
	rows, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows, "ok")
}
