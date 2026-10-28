package models

// ============================================================
// 系统公告模块 —— 单表 insight_announcements
//
// 管理员在【系统管理 → 系统配置 → 公告管理】里维护公告，
// 所有登录用户在产品头部（logo 同一行）看到当前生效的最新一条。
// ============================================================

import (
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// 公告展示态（派生值，不落库，由 EffectiveState 实时计算）
const (
	StateEffective = "effective" // 生效中（已启用且当前时间落在生效窗内）
	StatePending   = "pending"   // 未开始（已启用但未到生效开始时间）
	StateExpired   = "expired"   // 已过期（已启用且已过生效结束时间）
	StateDisabled  = "disabled"  // 已停用
)

// StateOptions 返回「展示态 → 中文标签」映射
func StateOptions() map[string]string {
	return map[string]string{
		StateEffective: "生效中",
		StatePending:   "未开始",
		StateExpired:   "已过期",
		StateDisabled:  "已停用",
	}
}

// StateLabel 展示态中文标签，未知态回落 '-'
func StateLabel(v string) string {
	if label, ok := StateOptions()[v]; ok {
		return label
	}
	return "-"
}

// InsightAnnouncement 系统公告
//
// 生效时间窗（StartAt / EndAt）语义：
//   - 两端都为 NULL：不限时间，只要启用就一直展示；
//   - 只填一端：另一端视为不限；
//   - 判定为闭区间：StartAt <= now <= EndAt。
type InsightAnnouncement struct {
	*commonModels.Model

	Title   string `gorm:"type:varchar(128);not null;default:'';comment:公告标题" json:"title"`
	Content string `gorm:"type:text;comment:公告内容" json:"content"`
	Link    string `gorm:"type:varchar(512);not null;default:'';comment:跳转链接（可为空）" json:"link"`

	Enabled bool                    `gorm:"type:tinyint(1);not null;default:1;index:idx_announcement_enabled;comment:启用状态" json:"enabled"`
	StartAt *commonModels.LocalTime `gorm:"comment:生效开始时间，NULL 表示不限" json:"start_at"`
	EndAt   *commonModels.LocalTime `gorm:"comment:生效结束时间，NULL 表示不限" json:"end_at"`

	CreatedBy string `gorm:"type:varchar(64);not null;default:'';comment:创建人" json:"created_by"`
	UpdatedBy string `gorm:"type:varchar(64);not null;default:'';comment:最近修改人" json:"updated_by"`
}

func (InsightAnnouncement) TableName() string { return "insight_announcements" }

// EffectiveState 计算展示态：停用优先，其次按时间窗判定，窗缺失的一端视为不限。
func (a *InsightAnnouncement) EffectiveState(now time.Time) string {
	if !a.Enabled {
		return StateDisabled
	}
	if a.StartAt != nil {
		if now.Before(time.Time(*a.StartAt)) {
			return StatePending
		}
	}
	if a.EndAt != nil {
		if now.After(time.Time(*a.EndAt)) {
			return StateExpired
		}
	}
	return StateEffective
}

// IsEffectiveNow 当前是否处于生效中（头部展示的唯一判据）
func (a *InsightAnnouncement) IsEffectiveNow(now time.Time) bool {
	return a.EffectiveState(now) == StateEffective
}

// ============================================================
// 展示设置（全局一份）
//
// 存在通用配置表 insight_system_params（config_key = announcement_display），
// 不单独建表：这是一组纯展示参数，没必要为两个数字多一张表。
// 读取失败 / 从未配置过一律回落默认值，保证公告永远能显示。
// ============================================================

// 展示设置的键名与默认值
const (
	SettingsConfigKey        = "announcement_display"
	DefaultScrollSpeedPxPerS = 90   // 滚动速度 px/s
	DefaultDwellMs           = 3000 // 每条滚完一轮后的停留时长 ms
)

// 取值范围：速度过快没法读、过慢像卡住；停留同理。
const (
	MinScrollSpeed = 10
	MaxScrollSpeed = 300
	MinDwellMs     = 500
	MaxDwellMs     = 20000
)

// AnnouncementDisplaySettings 公告条展示设置
type AnnouncementDisplaySettings struct {
	ScrollSpeed int `json:"scroll_speed"` // 滚动速度 px/s
	DwellMs     int `json:"dwell_ms"`     // 每条停留时长 ms
}

// DefaultSettings 默认展示设置
func DefaultSettings() AnnouncementDisplaySettings {
	return AnnouncementDisplaySettings{
		ScrollSpeed: DefaultScrollSpeedPxPerS,
		DwellMs:     DefaultDwellMs,
	}
}

// NormalizeSettings 把任意输入夹到合法区间，非法/零值回落默认。
// 管理员填了 0 或负数不应该让公告条静止不动，所以这里兜底而不是报错。
func NormalizeSettings(in AnnouncementDisplaySettings) AnnouncementDisplaySettings {
	out := DefaultSettings()
	if in.ScrollSpeed > 0 {
		out.ScrollSpeed = clampInt(in.ScrollSpeed, MinScrollSpeed, MaxScrollSpeed)
	}
	if in.DwellMs > 0 {
		out.DwellMs = clampInt(in.DwellMs, MinDwellMs, MaxDwellMs)
	}
	return out
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
