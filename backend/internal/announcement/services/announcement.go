package services

// ============================================================
// 系统公告 —— 服务层
//
// 两条硬口径：
//  1. 生效判定只认 enabled + 生效时间窗，窗缺失的一端视为不限，闭区间判定；
//  2. 头部展示只取「当前生效」里创建时间最新的一条，没有就不展示（前端整块不占位）。
// ============================================================

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	announcementForms "github.com/simajiong/jaysql/internal/announcement/forms"
	announcementModels "github.com/simajiong/jaysql/internal/announcement/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/gorm"
)

// AnnouncementItem 对外输出的公告视图。
// effective_state 是派生字段（不落库），让列表页与头部展示共用同一套判定，
// 避免前端再算一遍、两边口径漂移。
type AnnouncementItem struct {
	ID             uint64                  `json:"id"`
	Title          string                  `json:"title"`
	Content        string                  `json:"content"`
	Link           string                  `json:"link"`
	Enabled        bool                    `json:"enabled"`
	StartAt        *commonModels.LocalTime `json:"start_at"`
	EndAt          *commonModels.LocalTime `json:"end_at"`
	CreatedBy      string                  `json:"created_by"`
	UpdatedBy      string                  `json:"updated_by"`
	CreatedAt      commonModels.LocalTime  `json:"created_at"`
	UpdatedAt      commonModels.LocalTime  `json:"updated_at"`
	EffectiveState string                  `json:"effective_state"`
	StateLabel     string                  `json:"effective_state_label"`
}

func toItem(row *announcementModels.InsightAnnouncement, now time.Time) AnnouncementItem {
	state := row.EffectiveState(now)
	return AnnouncementItem{
		ID:             row.ID,
		Title:          row.Title,
		Content:        row.Content,
		Link:           row.Link,
		Enabled:        row.Enabled,
		StartAt:        row.StartAt,
		EndAt:          row.EndAt,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		EffectiveState: state,
		StateLabel:     announcementModels.StateLabel(state),
	}
}

// parseAnnouncementTime 解析前端传入的时间字符串。
// 前端 DatePicker 用 valueFormat="YYYY-MM-DD HH:mm:ss"，同时兼容 RFC3339 与纯日期。
func parseAnnouncementTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// nullTime 把可空时间转成 *LocalTime；解析失败视为「未填」而不是报错，
// 避免一个格式问题就让整条公告保存不了。
func nullTime(v *string) *commonModels.LocalTime {
	if v == nil {
		return nil
	}
	t, ok := parseAnnouncementTime(*v)
	if !ok {
		return nil
	}
	lt := commonModels.LocalTime(t)
	return &lt
}

func normalizePage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func normalizePageSize(size int) int {
	if size <= 0 {
		return 10
	}
	if size > 200 {
		return 200
	}
	return size
}

// enabledValue Enabled 缺省（nil）视为启用
func enabledValue(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

// ---------- 列表 ----------

type ListAnnouncementsService struct {
	Form *announcementForms.AnnouncementListQueryForm
}

func (s *ListAnnouncementsService) Run() ([]AnnouncementItem, int64, error) {
	now := time.Now()
	query := global.App.DB.Model(&announcementModels.InsightAnnouncement{})

	if keyword := strings.TrimSpace(s.Form.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("title LIKE ? OR content LIKE ?", like, like)
	}

	// 状态筛选：enabled/disabled 直接按字段；effective/pending/expired 按时间窗相对位置。
	switch strings.TrimSpace(s.Form.Status) {
	case "enabled":
		query = query.Where("enabled = ?", true)
	case "disabled":
		query = query.Where("enabled = ?", false)
	case announcementModels.StateEffective:
		query = query.Where("enabled = ?", true).
			Where("(start_at IS NULL OR start_at <= ?)", now).
			Where("(end_at IS NULL OR end_at >= ?)", now)
	case announcementModels.StatePending:
		query = query.Where("enabled = ?", true).Where("start_at > ?", now)
	case announcementModels.StateExpired:
		query = query.Where("enabled = ?", true).Where("end_at < ?", now)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := normalizePage(s.Form.Page)
	pageSize := normalizePageSize(s.Form.PageSize)

	var rows []announcementModels.InsightAnnouncement
	if err := query.Order("id DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	items := make([]AnnouncementItem, 0, len(rows))
	for i := range rows {
		items = append(items, toItem(&rows[i], now))
	}
	return items, total, nil
}

// ---------- 新增 ----------

type CreateAnnouncementService struct {
	Form     *announcementForms.AnnouncementCreateForm
	Operator string
}

func (s *CreateAnnouncementService) Run() (*AnnouncementItem, error) {
	row := announcementModels.InsightAnnouncement{
		Title:     strings.TrimSpace(s.Form.Title),
		Content:   strings.TrimSpace(s.Form.Content),
		Link:      strings.TrimSpace(s.Form.Link),
		Enabled:   enabledValue(s.Form.Enabled),
		StartAt:   nullTime(s.Form.StartAt),
		EndAt:     nullTime(s.Form.EndAt),
		CreatedBy: s.Operator,
		UpdatedBy: s.Operator,
	}
	if err := global.App.DB.Create(&row).Error; err != nil {
		return nil, err
	}
	item := toItem(&row, time.Now())
	return &item, nil
}

// ---------- 编辑 ----------

type UpdateAnnouncementService struct {
	ID       uint64
	Form     *announcementForms.AnnouncementUpdateForm
	Operator string
}

func (s *UpdateAnnouncementService) Run() (*AnnouncementItem, error) {
	var row announcementModels.InsightAnnouncement
	if err := global.App.DB.Where("id = ?", s.ID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("公告不存在或已被删除")
		}
		return nil, err
	}

	updates := map[string]interface{}{
		"title":      strings.TrimSpace(s.Form.Title),
		"content":    strings.TrimSpace(s.Form.Content),
		"link":       strings.TrimSpace(s.Form.Link),
		"enabled":    enabledValue(s.Form.Enabled),
		"start_at":   nullTime(s.Form.StartAt),
		"end_at":     nullTime(s.Form.EndAt),
		"updated_by": s.Operator,
	}
	// 用 Updates(map) 而非 Save：map 会把零值（含 false）正常写入，
	// 而 struct 更新会跳过零值，导致「停用」存不进去。
	if err := global.App.DB.Model(&row).Updates(updates).Error; err != nil {
		return nil, err
	}

	var fresh announcementModels.InsightAnnouncement
	if err := global.App.DB.Where("id = ?", s.ID).First(&fresh).Error; err != nil {
		return nil, err
	}
	item := toItem(&fresh, time.Now())
	return &item, nil
}

// ---------- 启停 ----------

type UpdateAnnouncementStatusService struct {
	ID       uint64
	Enabled  bool
	Operator string
}

func (s *UpdateAnnouncementStatusService) Run() (*AnnouncementItem, error) {
	var row announcementModels.InsightAnnouncement
	if err := global.App.DB.Where("id = ?", s.ID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("公告不存在或已被删除")
		}
		return nil, err
	}
	if err := global.App.DB.Model(&row).Updates(map[string]interface{}{
		"enabled":    s.Enabled,
		"updated_by": s.Operator,
	}).Error; err != nil {
		return nil, err
	}
	row.Enabled = s.Enabled
	row.UpdatedBy = s.Operator
	item := toItem(&row, time.Now())
	return &item, nil
}

// ---------- 删除 ----------

type DeleteAnnouncementService struct {
	ID uint64
}

func (s *DeleteAnnouncementService) Run() error {
	result := global.App.DB.Where("id = ?", s.ID).Delete(&announcementModels.InsightAnnouncement{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("公告不存在或已被删除")
	}
	return nil
}

// ---------- 页面公告条：当前生效的全部公告 + 展示设置 ----------

// ActiveAnnouncementsService 页面公告条数据源。
//
// 与「头部只取最新一条」不同，页面公告条要逐条轮播，所以返回全部生效公告，
// 排序口径沿用「创建时间倒序」；展示设置一并下发，省掉前端第二次请求。
type ActiveAnnouncementsService struct{}

type ActiveAnnouncementsResult struct {
	Items    []AnnouncementItem                             `json:"items"`
	Settings announcementModels.AnnouncementDisplaySettings `json:"settings"`
}

func (s *ActiveAnnouncementsService) Run() (*ActiveAnnouncementsResult, error) {
	now := time.Now()
	var rows []announcementModels.InsightAnnouncement
	if err := global.App.DB.
		Where("enabled = ?", true).
		Where("(start_at IS NULL OR start_at <= ?)", now).
		Where("(end_at IS NULL OR end_at >= ?)", now).
		Order("created_at DESC, id DESC").
		Limit(50). // 公告条轮播不需要太多条，兜个上限防止配置失控
		Find(&rows).Error; err != nil {
		return nil, err
	}

	items := make([]AnnouncementItem, 0, len(rows))
	for i := range rows {
		items = append(items, toItem(&rows[i], now))
	}
	return &ActiveAnnouncementsResult{
		Items:    items,
		Settings: (&GetAnnouncementSettingsService{}).Run(),
	}, nil
}

// ---------- 展示设置 ----------

type GetAnnouncementSettingsService struct{}

// Run 读取展示设置。读不到（没配过 / 值坏了）一律回落默认值，
// 不把错误抛给调用方——公告条因为配置损坏就整个不显示，代价太大。
func (s *GetAnnouncementSettingsService) Run() announcementModels.AnnouncementDisplaySettings {
	var row commonModels.InsightSystemParam
	if err := global.App.DB.
		Where("config_key = ?", announcementModels.SettingsConfigKey).
		Take(&row).Error; err != nil {
		return announcementModels.DefaultSettings()
	}
	var cfg announcementModels.AnnouncementDisplaySettings
	if err := json.Unmarshal([]byte(row.ConfigValue), &cfg); err != nil {
		return announcementModels.DefaultSettings()
	}
	return announcementModels.NormalizeSettings(cfg)
}

type UpdateAnnouncementSettingsService struct {
	Form *announcementForms.AnnouncementSettingsForm
}

func (s *UpdateAnnouncementSettingsService) Run() (announcementModels.AnnouncementDisplaySettings, error) {
	next := announcementModels.NormalizeSettings(announcementModels.AnnouncementDisplaySettings{
		ScrollSpeed: s.Form.ScrollSpeed,
		DwellMs:     s.Form.DwellMs,
	})
	raw, err := json.Marshal(next)
	if err != nil {
		return next, err
	}

	// 先查后写：这张表是通用 KV，行数极少，没必要上 upsert 的复杂度。
	var row commonModels.InsightSystemParam
	err = global.App.DB.Where("config_key = ?", announcementModels.SettingsConfigKey).Take(&row).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return next, err
		}
		row = commonModels.InsightSystemParam{
			ConfigKey:   announcementModels.SettingsConfigKey,
			ConfigValue: string(raw),
			ConfigType:  "object",
			Description: "页面公告条展示设置：滚动速度(px/s) 与 每条停留时长(ms)",
		}
		if err := global.App.DB.Create(&row).Error; err != nil {
			return next, err
		}
		return next, nil
	}
	if err := global.App.DB.Model(&row).Update("config_value", string(raw)).Error; err != nil {
		return next, err
	}
	return next, nil
}
