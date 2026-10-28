package forms

// ============================================================
// 系统公告 —— 表单定义
// ============================================================

// AnnouncementListQueryForm 公告列表查询（管理员）
//
// status 取值：enabled / disabled / effective / pending / expired
// 前两个按 enabled 字段筛，后三个按「当前时间落在生效窗的哪一侧」筛。
type AnnouncementListQueryForm struct {
	Keyword  string `form:"keyword" json:"keyword"`
	Status   string `form:"status" json:"status"`
	Page     int    `form:"page" json:"page"`
	PageSize int    `form:"page_size" json:"page_size"`
}

// AnnouncementCreateForm 新增公告
//
// Enabled 用指针：缺省（nil）视为启用，避免 JSON 省略字段时被解析成 false。
type AnnouncementCreateForm struct {
	Title   string  `json:"title" binding:"required,max=128"`
	Content string  `json:"content" binding:"required,max=2000"`
	Link    string  `json:"link" binding:"max=512"`
	Enabled *bool   `json:"enabled"`
	StartAt *string `json:"start_at"`
	EndAt   *string `json:"end_at"`
}

// AnnouncementUpdateForm 编辑公告（ID 来自路径参数）
type AnnouncementUpdateForm struct {
	Title   string  `json:"title" binding:"required,max=128"`
	Content string  `json:"content" binding:"required,max=2000"`
	Link    string  `json:"link" binding:"max=512"`
	Enabled *bool   `json:"enabled"`
	StartAt *string `json:"start_at"`
	EndAt   *string `json:"end_at"`
}

// AnnouncementStatusForm 启用 / 停用
type AnnouncementStatusForm struct {
	Enabled bool `json:"enabled"`
}

// AnnouncementSettingsForm 展示设置（全局一份，存 insight_system_params）
//
// 两个值都允许缺省：缺省走 models.DefaultSettings()，管理员不必一次填全。
type AnnouncementSettingsForm struct {
	ScrollSpeed int `json:"scroll_speed"`
	DwellMs     int `json:"dwell_ms"`
}
