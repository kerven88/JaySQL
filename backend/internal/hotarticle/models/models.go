package models

// ============================================================
// 首页「相关热文」模块 —— 单表 insight_hot_articles
//
// 管理员在【系统管理 → 系统配置 → 热文管理】里维护，
// 首页右下角「相关热文」面板展示「已启用 + 排序值升序」的前 N 条。
//
// 设计口径：
//   - URL 留空 = 首页仅展示不可点击（前端沿用既有语义，不跳空白页）；
//   - Enabled=false 的记录保留配置但不上首页；
//   - SortOrder 升序，同值时按 id 升序，保证顺序稳定不抖动。
// ============================================================

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// HomeDisplayLimit 首页展示条数上限。
// 与前端 WeeklyHotArticles 的 slice(0,3) 口径一致，改这里必须同步改前端。
const HomeDisplayLimit = 3

// MaxTitleLength 标题最大长度（与表字段 varchar(128) 一致）
const MaxTitleLength = 128

// MaxURLLength 链接最大长度（varchar(512)）
const MaxURLLength = 512

// MaxHeat 热度上限（防御性上限，避免前端展示成天文数字）
const MaxHeat = 100000000

// 预设标签：前端下拉用，落库仍是自由文本（管理员可自行填别的）
func TagOptions() []string {
	return []string{"MySQL", "PostgreSQL", "备份恢复", "性能", "架构", "其他"}
}

// InsightHotArticle 首页相关热文
type InsightHotArticle struct {
	*commonModels.Model

	Title     string `gorm:"type:varchar(128);not null;default:'';comment:标题" json:"title"`
	Source    string `gorm:"type:varchar(64);not null;default:'';comment:来源" json:"source"`
	Tag       string `gorm:"type:varchar(32);not null;default:'';comment:标签" json:"tag"`
	Heat      int64  `gorm:"not null;default:0;comment:热度（阅读量）" json:"heat"`
	URL       string `gorm:"type:varchar(512);not null;default:'';comment:外链，留空则首页仅展示不可点" json:"url"`
	SortOrder int    `gorm:"not null;default:0;comment:排序值（升序，越小越靠前）" json:"sort_order"`
	Enabled   bool   `gorm:"type:tinyint(1);not null;default:1;index:idx_hot_article_enabled;comment:启用状态" json:"enabled"`

	CreatedBy string `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
	UpdatedBy string `gorm:"type:varchar(64);not null;default:'';comment:最近修改人" json:"updated_by"`
}

func (InsightHotArticle) TableName() string { return "insight_hot_articles" }
