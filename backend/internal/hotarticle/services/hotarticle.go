package services

// ============================================================
// 首页相关热文 —— 服务层
//
// 三条硬口径：
//  1. 首页只取「已启用 + 排序值升序」的前 HomeDisplayLimit 条（同值按 id 升序，顺序稳定）；
//  2. URL 留空是合法状态：首页展示但不可点击（沿用前端既有语义）；
//  3. 列表口径（管理端）与首页口径（用户端）取自同一张表、同一套排序，
//     避免「后台看到的第一条」与「首页看到的第一条」不是同一条。
// ============================================================

import (
	"errors"
	"regexp"
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	hotForms "github.com/simajiong/jaysql/internal/hotarticle/forms"
	hotModels "github.com/simajiong/jaysql/internal/hotarticle/models"

	"gorm.io/gorm"
)

// urlPattern 只允许 http/https，挡掉 javascript: 等危险协议。
var urlPattern = regexp.MustCompile(`^https?://\S+$`)

// HotArticleItem 对外输出的热文视图
type HotArticleItem struct {
	ID        uint64                 `json:"id"`
	Title     string                 `json:"title"`
	Source    string                 `json:"source"`
	Tag       string                 `json:"tag"`
	Heat      int64                  `json:"heat"`
	URL       string                 `json:"url"`
	SortOrder int                    `json:"sort_order"`
	Enabled   bool                   `json:"enabled"`
	CreatedBy string                 `json:"created_by"`
	UpdatedBy string                 `json:"updated_by"`
	CreatedAt commonModels.LocalTime `json:"created_at"`
	UpdatedAt commonModels.LocalTime `json:"updated_at"`
}

func toItem(row *hotModels.InsightHotArticle) HotArticleItem {
	return HotArticleItem{
		ID:        row.ID,
		Title:     row.Title,
		Source:    row.Source,
		Tag:       row.Tag,
		Heat:      row.Heat,
		URL:       row.URL,
		SortOrder: row.SortOrder,
		Enabled:   row.Enabled,
		CreatedBy: row.CreatedBy,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

// ---------- 校验与归一 ----------

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

func clampHeat(v int64) int64 {
	if v < 0 {
		return 0
	}
	if v > hotModels.MaxHeat {
		return hotModels.MaxHeat
	}
	return v
}

// normalizeURL 空值合法；非空必须是 http/https 绝对地址。
func normalizeURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	if len(v) > hotModels.MaxURLLength {
		return "", errors.New("链接长度不能超过 512 个字符")
	}
	if !urlPattern.MatchString(v) {
		return "", errors.New("链接需以 http:// 或 https:// 开头")
	}
	return v, nil
}

// normalizeTitle 标题必填（去空格后非空）
func normalizeTitle(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errors.New("标题不能为空")
	}
	if len([]rune(v)) > hotModels.MaxTitleLength {
		return "", errors.New("标题长度不能超过 128 个字符")
	}
	return v, nil
}

// 热文没有生效时间窗：状态就是启用开关本身，无需按时间判定。

// ---------- 列表（管理端） ----------

type ListHotArticlesService struct {
	Form *hotForms.HotArticleListQueryForm
}

func (s *ListHotArticlesService) Run() ([]HotArticleItem, int64, error) {
	query := global.App.DB.Model(&hotModels.InsightHotArticle{})

	if keyword := strings.TrimSpace(s.Form.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("title LIKE ? OR source LIKE ?", like, like)
	}
	switch strings.TrimSpace(s.Form.Status) {
	case "enabled":
		query = query.Where("enabled = ?", true)
	case "disabled":
		query = query.Where("enabled = ?", false)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := normalizePage(s.Form.Page)
	pageSize := normalizePageSize(s.Form.PageSize)

	var rows []hotModels.InsightHotArticle
	// 排序与首页一致（sort_order, id），管理员在列表里看到的第一条就是首页第一条。
	if err := query.Order("sort_order ASC, id ASC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	items := make([]HotArticleItem, 0, len(rows))
	for i := range rows {
		items = append(items, toItem(&rows[i]))
	}
	return items, total, nil
}

// ---------- 新增 ----------

type CreateHotArticleService struct {
	Form     *hotForms.HotArticleCreateForm
	Operator string
}

func (s *CreateHotArticleService) Run() (*HotArticleItem, error) {
	title, err := normalizeTitle(s.Form.Title)
	if err != nil {
		return nil, err
	}
	link, err := normalizeURL(s.Form.URL)
	if err != nil {
		return nil, err
	}

	// 排序值缺省（<=0）时排到末尾：管理员新增不用关心已有排序，符合直觉。
	sortOrder := s.Form.SortOrder
	if sortOrder <= 0 {
		var maxOrder int
		if err := global.App.DB.Model(&hotModels.InsightHotArticle{}).
			Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err == nil {
			sortOrder = maxOrder + 10
		}
	}

	source := strings.TrimSpace(s.Form.Source)
	if source == "" {
		source = "程序猿读历史"
	}

	row := hotModels.InsightHotArticle{
		Title:     title,
		Source:    source,
		Tag:       strings.TrimSpace(s.Form.Tag),
		Heat:      clampHeat(s.Form.Heat),
		URL:       link,
		SortOrder: sortOrder,
		Enabled:   enabledValue(s.Form.Enabled),
		CreatedBy: s.Operator,
		UpdatedBy: s.Operator,
	}
	if err := global.App.DB.Create(&row).Error; err != nil {
		return nil, err
	}
	item := toItem(&row)
	return &item, nil
}

// ---------- 编辑 ----------

type UpdateHotArticleService struct {
	ID       uint64
	Form     *hotForms.HotArticleUpdateForm
	Operator string
}

func (s *UpdateHotArticleService) Run() (*HotArticleItem, error) {
	var row hotModels.InsightHotArticle
	if err := global.App.DB.Where("id = ?", s.ID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("热文不存在或已被删除")
		}
		return nil, err
	}

	title, err := normalizeTitle(s.Form.Title)
	if err != nil {
		return nil, err
	}
	link, err := normalizeURL(s.Form.URL)
	if err != nil {
		return nil, err
	}

	source := strings.TrimSpace(s.Form.Source)
	if source == "" {
		source = "程序猿读历史"
	}

	updates := map[string]interface{}{
		"title":      title,
		"source":     source,
		"tag":        strings.TrimSpace(s.Form.Tag),
		"heat":       clampHeat(s.Form.Heat),
		"url":        link,
		"enabled":    enabledValue(s.Form.Enabled),
		"updated_by": s.Operator,
	}
	// 排序值：只在显式传了正数时才覆盖，避免编辑弹窗没带排序把顺序打乱。
	if s.Form.SortOrder > 0 {
		updates["sort_order"] = s.Form.SortOrder
	}
	// 用 Updates(map) 而非 Save：map 会把零值（含 false、空串）正常写入，
	// 而 struct 更新会跳过零值，导致「停用」「清空链接」存不进去。
	if err := global.App.DB.Model(&row).Updates(updates).Error; err != nil {
		return nil, err
	}

	var fresh hotModels.InsightHotArticle
	if err := global.App.DB.Where("id = ?", s.ID).First(&fresh).Error; err != nil {
		return nil, err
	}
	item := toItem(&fresh)
	return &item, nil
}

// ---------- 启停 ----------

type UpdateHotArticleStatusService struct {
	ID       uint64
	Enabled  bool
	Operator string
}

func (s *UpdateHotArticleStatusService) Run() (*HotArticleItem, error) {
	var row hotModels.InsightHotArticle
	if err := global.App.DB.Where("id = ?", s.ID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("热文不存在或已被删除")
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
	item := toItem(&row)
	return &item, nil
}

// ---------- 排序 ----------

// UpdateHotArticleSortService 批量重写排序值。
// 放在事务里：上移/下移会同时改两条，中途失败不能只改一半。
type UpdateHotArticleSortService struct {
	Items []hotForms.HotArticleSortItem
}

func (s *UpdateHotArticleSortService) Run() error {
	if len(s.Items) == 0 {
		return errors.New("排序参数为空")
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range s.Items {
			if item.ID == 0 {
				continue
			}
			result := tx.Model(&hotModels.InsightHotArticle{}).
				Where("id = ?", item.ID).
				Update("sort_order", item.SortOrder)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return errors.New("部分热文不存在或已被删除，请刷新后重试")
			}
		}
		return nil
	})
}

// ---------- 删除 ----------

type DeleteHotArticleService struct {
	ID uint64
}

func (s *DeleteHotArticleService) Run() error {
	result := global.App.DB.Where("id = ?", s.ID).Delete(&hotModels.InsightHotArticle{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("热文不存在或已被删除")
	}
	return nil
}

// ---------- 首页（用户端） ----------

type HomeHotArticlesService struct{}

// Run 返回首页展示的热文：已启用、按排序值升序，取前 HomeDisplayLimit 条。
// 数据为空是合法结果（管理员可主动清空），由前端渲染空态。
func (s *HomeHotArticlesService) Run() ([]HotArticleItem, error) {
	var rows []hotModels.InsightHotArticle
	if err := global.App.DB.
		Where("enabled = ?", true).
		Order("sort_order ASC, id ASC").
		Limit(hotModels.HomeDisplayLimit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]HotArticleItem, 0, len(rows))
	for i := range rows {
		items = append(items, toItem(&rows[i]))
	}
	return items, nil
}

// ---------- 首次启动种子 ----------

// InitializeDefaultHotArticles 首次启动播种：仅在表内无任何记录时写入默认 4 条。
//
// 为什么需要：改造前首页热文来自前端静态常量，若新装环境表为空，
// 首页该区块会直接空掉（相对改造前是体验倒退）。种子与改造前的静态内容一致，
// 管理员登录后可在【热文管理】里自由编辑/停用/删除；一旦有记录就不再播种，
// 保证「管理员主动清空」不会被下次启动复活。
func InitializeDefaultHotArticles(db *gorm.DB) error {
	var count int64
	if err := db.Model(&hotModels.InsightHotArticle{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	seeds := []hotModels.InsightHotArticle{
		{Title: "从 gray 到 crash recovery：MySQL 崩溃恢复的两阶段", Source: "程序猿读历史", Tag: "MySQL", Heat: 2100, SortOrder: 10, Enabled: true, CreatedBy: "system", UpdatedBy: "system"},
		{Title: "备份为什么要校验：一次备份集无法恢复的复盘", Source: "程序猿读历史", Tag: "备份恢复", Heat: 1700, SortOrder: 20, Enabled: true, CreatedBy: "system", UpdatedBy: "system"},
		{Title: "慢 SQL 治理的三板斧：采集、归因、闭环", Source: "程序猿读历史", Tag: "性能", Heat: 1400, SortOrder: 30, Enabled: true, CreatedBy: "system", UpdatedBy: "system"},
		{Title: "PostgreSQL 的 MVCC 与 vacuum，到底在忙什么", Source: "程序猿读历史", Tag: "PostgreSQL", Heat: 1100, SortOrder: 40, Enabled: true, CreatedBy: "system", UpdatedBy: "system"},
	}
	return db.Create(&seeds).Error
}
