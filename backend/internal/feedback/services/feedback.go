package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	feedbackForms "github.com/simajiong/jaysql/internal/feedback/forms"
	feedbackModels "github.com/simajiong/jaysql/internal/feedback/models"
	"github.com/simajiong/jaysql/internal/global"
	userServices "github.com/simajiong/jaysql/internal/users/services"
	"github.com/simajiong/jaysql/pkg/utils"

	"gorm.io/datatypes"
)

// 反馈图片约束（客户端校验不可信，服务端二次校验）
const (
	feedbackMaxImages      = 5
	feedbackMaxImageBytes  = 10 << 20 // 单张 10MB
	feedbackUploadDir      = "./media/feedback"
	feedbackContentMaxLen  = 2000
	feedbackDefaultPageSiz = 20
	feedbackMaxPageSize    = 200
)

var feedbackAllowedImageExt = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".gif":  {},
	".webp": {},
	".bmp":  {},
}

// SubmitFeedbackService 提交反馈
type SubmitFeedbackService struct {
	C        *gin.Context
	Form     *feedbackForms.SubmitFeedbackForm
	Files    []*multipart.FileHeader
	UserID   uint64
	Username string
}

func (s *SubmitFeedbackService) Run() (*feedbackModels.InsightFeedback, error) {
	category := strings.TrimSpace(s.Form.Category)
	if !feedbackModels.IsValidCategory(category) {
		return nil, errors.New("请选择有效的问题类型")
	}
	content := strings.TrimSpace(s.Form.Content)
	if content == "" {
		return nil, errors.New("请填写意见描述")
	}
	if len([]rune(content)) > feedbackContentMaxLen {
		return nil, fmt.Errorf("意见描述不能超过 %d 字", feedbackContentMaxLen)
	}

	imageURLs, err := saveFeedbackImages(s.Files)
	if err != nil {
		return nil, err
	}

	imagesJSON, _ := jsonMarshalStrings(imageURLs)

	record := &feedbackModels.InsightFeedback{
		Category:  category,
		Content:   content,
		Contact:   strings.TrimSpace(s.Form.Contact),
		UserID:    s.UserID,
		Username:  s.Username,
		Images:    imagesJSON,
		PagePath:  truncate(strings.TrimSpace(s.Form.PagePath), 512),
		UserAgent: truncate(strings.TrimSpace(s.Form.UserAgent), 512),
		Status:    feedbackModels.StatusPending,
	}
	if err := global.App.DB.Create(record).Error; err != nil {
		return nil, errors.New("提交失败，请稍后重试")
	}

	if s.C != nil {
		_ = commonServices.WriteAuditLog(s.C, commonServices.AuditLogEvent{
			Username:     s.Username,
			ModuleCode:   "feedback",
			ModuleName:   "反馈管理",
			ActionCode:   "feedback.submit",
			ActionName:   "提交反馈",
			ActionResult: "success",
			ResourceType: "feedback",
			ResourceID:   record.FeedbackNo,
			ResourceName: categoryLabel(category),
			Summary:      fmt.Sprintf("提交反馈：%s", categoryLabel(category)),
			AfterData:    record,
		})
	}

	record.Images = normalizeImages(record.Images)
	return record, nil
}

// ListFeedbacksService 反馈列表（管理员）
type ListFeedbacksService struct {
	Form *feedbackForms.FeedbackListQueryForm
}

func (s *ListFeedbacksService) Run() ([]feedbackModels.InsightFeedback, int64, error) {
	query := global.App.DB.Model(&feedbackModels.InsightFeedback{})

	if category := strings.TrimSpace(s.Form.Category); category != "" {
		query = query.Where("category = ?", category)
	}
	if status := strings.TrimSpace(s.Form.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if keyword := strings.TrimSpace(s.Form.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where(
			"content LIKE ? OR contact LIKE ? OR username LIKE ? OR feedback_no LIKE ?",
			like, like, like, like,
		)
	}
	if start := strings.TrimSpace(s.Form.StartTime); start != "" {
		if t, err := parseFeedbackTime(start); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if end := strings.TrimSpace(s.Form.EndTime); end != "" {
		if t, err := parseFeedbackTime(end); err == nil {
			query = query.Where("created_at <= ?", t)
		}
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := normalizePage(s.Form.Page)
	pageSize := normalizePageSize(s.Form.PageSize)
	offset := (page - 1) * pageSize

	var rows []feedbackModels.InsightFeedback
	if err := query.Order("id DESC").Limit(pageSize).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].Images = normalizeImages(rows[i].Images)
	}
	return rows, total, nil
}

// FeedbackStatsService 各状态数量统计（列表页头部）
func FeedbackStatsService() (map[string]int64, error) {
	type row struct {
		Status string
		Total  int64
	}
	var rows []row
	if err := global.App.DB.Model(&feedbackModels.InsightFeedback{}).
		Select("status, count(*) AS total").Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	stats := map[string]int64{
		feedbackModels.StatusPending:    0,
		feedbackModels.StatusProcessing: 0,
		feedbackModels.StatusResolved:   0,
		feedbackModels.StatusClosed:     0,
	}
	var all int64
	for _, r := range rows {
		stats[r.Status] = r.Total
		all += r.Total
	}
	stats["total"] = all
	return stats, nil
}

// GetFeedbackService 反馈详情（管理员）
type GetFeedbackService struct {
	ID uint64
}

func (s *GetFeedbackService) Run() (*feedbackModels.InsightFeedback, error) {
	var record feedbackModels.InsightFeedback
	if err := global.App.DB.Where("id = ?", s.ID).First(&record).Error; err != nil {
		return nil, errors.New("反馈不存在")
	}
	record.Images = normalizeImages(record.Images)
	return &record, nil
}

// UpdateFeedbackService 更新处理状态 / 备注（管理员）
type UpdateFeedbackService struct {
	C        *gin.Context
	Form     *feedbackForms.FeedbackUpdateForm
	Operator string
}

func (s *UpdateFeedbackService) Run() (*feedbackModels.InsightFeedback, error) {
	status := strings.TrimSpace(s.Form.Status)
	if !feedbackModels.IsValidStatus(status) {
		return nil, errors.New("无效的处理状态")
	}

	var record feedbackModels.InsightFeedback
	if err := global.App.DB.Where("id = ?", s.Form.ID).First(&record).Error; err != nil {
		return nil, errors.New("反馈不存在")
	}

	updates := map[string]interface{}{
		"status":     status,
		"remark":     truncate(strings.TrimSpace(s.Form.Remark), 1024),
		"handled_by": s.Operator,
		"handled_at": time.Now(),
	}
	if err := global.App.DB.Model(&feedbackModels.InsightFeedback{}).
		Where("id = ?", s.Form.ID).Updates(updates).Error; err != nil {
		return nil, errors.New("更新失败，请稍后重试")
	}

	updated, err := (&GetFeedbackService{ID: s.Form.ID}).Run()
	if err != nil {
		return nil, err
	}

	if s.C != nil {
		_ = commonServices.WriteAuditLog(s.C, commonServices.AuditLogEvent{
			Username:     s.Operator,
			ModuleCode:   "feedback",
			ModuleName:   "反馈管理",
			ActionCode:   "feedback.update",
			ActionName:   "处理反馈",
			ActionResult: "success",
			ResourceType: "feedback",
			ResourceID:   record.FeedbackNo,
			ResourceName: record.FeedbackNo,
			Summary:      fmt.Sprintf("反馈 %s 状态更新为 %s", record.FeedbackNo, statusLabel(status)),
			BeforeData:   record,
			AfterData:    updated,
		})
	}
	return updated, nil
}

// DeleteFeedbackService 删除反馈（管理员）
type DeleteFeedbackService struct {
	C        *gin.Context
	ID       uint64
	Operator string
}

func (s *DeleteFeedbackService) Run() error {
	var record feedbackModels.InsightFeedback
	if err := global.App.DB.Where("id = ?", s.ID).First(&record).Error; err != nil {
		return errors.New("反馈不存在")
	}
	if err := global.App.DB.Where("id = ?", s.ID).Delete(&feedbackModels.InsightFeedback{}).Error; err != nil {
		return errors.New("删除失败，请稍后重试")
	}
	// 同步清理落盘图片，避免孤儿文件
	removeFeedbackImages(record.Images)

	if s.C != nil {
		_ = commonServices.WriteAuditLog(s.C, commonServices.AuditLogEvent{
			Username:     s.Operator,
			ModuleCode:   "feedback",
			ModuleName:   "反馈管理",
			ActionCode:   "feedback.delete",
			ActionName:   "删除反馈",
			ActionResult: "success",
			ResourceType: "feedback",
			ResourceID:   record.FeedbackNo,
			ResourceName: record.FeedbackNo,
			Summary:      fmt.Sprintf("删除反馈 %s", record.FeedbackNo),
			BeforeData:   record,
		})
	}
	return nil
}

// LookupUserIDByUsername 由用户名解析 UID（无则返回 0）
//
// 注意：必须先做确定性解析（同名多行时优先未删除→已激活→uid 最小），
// 否则 First 会按主键取到最早的历史行，把反馈算到已删除用户头上。
func LookupUserIDByUsername(username string) uint64 {
	user, err := userServices.CanonicalUserByUsername(username)
	if err != nil {
		return 0
	}
	return user.Uid
}

// ---------------- 内部工具 ----------------

func saveFeedbackImages(files []*multipart.FileHeader) ([]string, error) {
	if len(files) == 0 {
		return []string{}, nil
	}
	if len(files) > feedbackMaxImages {
		return nil, fmt.Errorf("最多上传 %d 张图片", feedbackMaxImages)
	}

	month := time.Now().Format("200601")
	dir := filepath.Join(feedbackUploadDir, month)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return nil, errors.New("图片保存目录创建失败")
	}

	urls := make([]string, 0, len(files))
	for _, fh := range files {
		if fh == nil {
			continue
		}
		if fh.Size > feedbackMaxImageBytes {
			return nil, fmt.Errorf("图片 %s 超过 %dMB", fh.Filename, feedbackMaxImageBytes>>20)
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext == "" {
			ext = ".png"
		}
		if _, ok := feedbackAllowedImageExt[ext]; !ok {
			return nil, fmt.Errorf("不支持的图片格式：%s", fh.Filename)
		}
		name := utils.GenerateSimpleRandomString(24) + ext
		dst := filepath.Join(dir, name)
		if err := saveMultipartFile(fh, dst); err != nil {
			return nil, errors.New("图片保存失败")
		}
		// 静态目录 ./media 映射为 /media，统一存相对 URL
		urls = append(urls, "/"+path.Join("media", "feedback", month, name))
	}
	return urls, nil
}

func saveMultipartFile(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(src)
	return err
}

func removeFeedbackImages(images datatypes.JSON) {
	var urls []string
	if len(images) == 0 {
		return
	}
	if err := jsonUnmarshalStrings(images, &urls); err != nil {
		return
	}
	for _, u := range urls {
		rel := strings.TrimPrefix(u, "/")
		if !strings.HasPrefix(rel, "media/") {
			continue
		}
		full := filepath.Join(".", filepath.FromSlash(rel))
		// 仅允许删除 ./media 目录下的文件
		abs, err := filepath.Abs(full)
		if err != nil {
			continue
		}
		base, err := filepath.Abs(filepath.Join(".", "media"))
		if err != nil || !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
			continue
		}
		_ = os.Remove(abs)
	}
}

func normalizeImages(images datatypes.JSON) datatypes.JSON {
	if len(images) == 0 {
		out, _ := jsonMarshalStrings([]string{})
		return out
	}
	var urls []string
	if err := jsonUnmarshalStrings(images, &urls); err != nil || urls == nil {
		out, _ := jsonMarshalStrings([]string{})
		return out
	}
	return images
}

func categoryLabel(code string) string {
	if label, ok := feedbackModels.CategoryOptions()[code]; ok {
		return label
	}
	return code
}

func statusLabel(code string) string {
	if label, ok := feedbackModels.StatusOptions()[code]; ok {
		return label
	}
	return code
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func parseFeedbackTime(v string) (time.Time, error) {
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time: %s", v)
}

func normalizePage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func normalizePageSize(size int) int {
	if size < 1 {
		return feedbackDefaultPageSiz
	}
	if size > feedbackMaxPageSize {
		return feedbackMaxPageSize
	}
	return size
}

func jsonMarshalStrings(values []string) (datatypes.JSON, error) {
	if values == nil {
		values = []string{}
	}
	return json.Marshal(values)
}

func jsonUnmarshalStrings(raw datatypes.JSON, out *[]string) error {
	return json.Unmarshal(raw, out)
}
