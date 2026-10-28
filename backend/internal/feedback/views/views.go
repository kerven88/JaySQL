package views

import (
	"mime/multipart"
	"strconv"
	"strings"

	feedbackForms "github.com/simajiong/jaysql/internal/feedback/forms"
	feedbackServices "github.com/simajiong/jaysql/internal/feedback/services"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// SubmitFeedbackView 用户提交反馈（multipart：文本字段 + 可选多张图片，字段名 images）
func SubmitFeedbackView(c *gin.Context) {
	var form feedbackForms.SubmitFeedbackForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}

	username, _ := middleware.GetUserNameFromJWT(c)
	userID := feedbackServices.LookupUserIDByUsername(username)

	var files []*multipart.FileHeader
	if mf, err := c.MultipartForm(); err == nil && mf != nil {
		if list, ok := mf.File["images"]; ok {
			files = list
		}
	}

	service := feedbackServices.SubmitFeedbackService{
		C:        c,
		Form:     &form,
		Files:    files,
		UserID:   userID,
		Username: username,
	}
	record, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "反馈提交成功")
}

// ListFeedbacksView 反馈列表（管理员）
func ListFeedbacksView(c *gin.Context) {
	var form feedbackForms.FeedbackListQueryForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := feedbackServices.ListFeedbacksService{Form: &form}
	rows, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, rows)
}

// FeedbackStatsView 各状态数量统计
func FeedbackStatsView(c *gin.Context) {
	stats, err := feedbackServices.FeedbackStatsService()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, stats, "success")
}

// GetFeedbackView 反馈详情（管理员，id 走 query 参数）
func GetFeedbackView(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("id"))
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		response.ValidateFail(c, "非法参数: id")
		return
	}
	record, err := (&feedbackServices.GetFeedbackService{ID: id}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "success")
}

// UpdateFeedbackView 更新处理状态 / 备注（管理员）
func UpdateFeedbackView(c *gin.Context) {
	id, ok := parseFeedbackID(c)
	if !ok {
		return
	}
	var form feedbackForms.FeedbackUpdateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	form.ID = id
	operator, _ := middleware.GetUserNameFromJWT(c)
	service := feedbackServices.UpdateFeedbackService{C: c, Form: &form, Operator: operator}
	record, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "已更新")
}

// DeleteFeedbackView 删除反馈（管理员）
func DeleteFeedbackView(c *gin.Context) {
	id, ok := parseFeedbackID(c)
	if !ok {
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	service := feedbackServices.DeleteFeedbackService{C: c, ID: id, Operator: operator}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已删除")
}

func parseFeedbackID(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		response.ValidateFail(c, "非法参数: id")
		return 0, false
	}
	return id, true
}
