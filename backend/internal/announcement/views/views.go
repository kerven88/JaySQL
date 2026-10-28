package views

// ============================================================
// 系统公告 —— 视图层
//
// 管理侧：/api/v1/admin/announcements（system.announcement.* 权限）
// 用户侧：/api/v1/announcements/active（仅要求登录，所有登录用户都能看到）
// ============================================================

import (
	"strconv"
	"strings"

	announcementForms "github.com/simajiong/jaysql/internal/announcement/forms"
	announcementServices "github.com/simajiong/jaysql/internal/announcement/services"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// ListAnnouncementsView 公告列表（管理员，分页）
func ListAnnouncementsView(c *gin.Context) {
	var form announcementForms.AnnouncementListQueryForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	rows, total, err := (&announcementServices.ListAnnouncementsService{Form: &form}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, rows)
}

// CreateAnnouncementView 新增公告
func CreateAnnouncementView(c *gin.Context) {
	var form announcementForms.AnnouncementCreateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	record, err := (&announcementServices.CreateAnnouncementService{Form: &form, Operator: operator}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "新增成功")
}

// UpdateAnnouncementView 编辑公告
func UpdateAnnouncementView(c *gin.Context) {
	id, ok := parseAnnouncementID(c)
	if !ok {
		return
	}
	var form announcementForms.AnnouncementUpdateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	record, err := (&announcementServices.UpdateAnnouncementService{ID: id, Form: &form, Operator: operator}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "已保存")
}

// UpdateAnnouncementStatusView 启用 / 停用
func UpdateAnnouncementStatusView(c *gin.Context) {
	id, ok := parseAnnouncementID(c)
	if !ok {
		return
	}
	var form announcementForms.AnnouncementStatusForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	record, err := (&announcementServices.UpdateAnnouncementStatusService{
		ID:       id,
		Enabled:  form.Enabled,
		Operator: operator,
	}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "已更新")
}

// DeleteAnnouncementView 删除公告
func DeleteAnnouncementView(c *gin.Context) {
	id, ok := parseAnnouncementID(c)
	if !ok {
		return
	}
	if err := (&announcementServices.DeleteAnnouncementService{ID: id}).Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已删除")
}

// ActiveAnnouncementsView 页面公告条数据源：全部生效公告 + 展示设置。
// 与「当前最新一条」不同，公告条要逐条轮播，所以一次把生效条目都给前端，
// 由前端按会话内关闭状态过滤后轮播（关闭状态不上报后端，纯本地会话级）。
func ActiveAnnouncementsView(c *gin.Context) {
	result, err := (&announcementServices.ActiveAnnouncementsService{}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, result, "success")
}

// GetAnnouncementSettingsView 读取公告条展示设置
func GetAnnouncementSettingsView(c *gin.Context) {
	settings := (&announcementServices.GetAnnouncementSettingsService{}).Run()
	response.Success(c, settings, "success")
}

// UpdateAnnouncementSettingsView 保存公告条展示设置
func UpdateAnnouncementSettingsView(c *gin.Context) {
	var form announcementForms.AnnouncementSettingsForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	settings, err := (&announcementServices.UpdateAnnouncementSettingsService{Form: &form}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, settings, "已保存")
}

func parseAnnouncementID(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		response.ValidateFail(c, "非法参数: id")
		return 0, false
	}
	return id, true
}
