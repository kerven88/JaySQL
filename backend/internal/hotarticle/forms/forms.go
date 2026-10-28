package forms

// ============================================================
// 首页相关热文 —— 表单定义
// ============================================================

// HotArticleListQueryForm 热文列表查询（管理员）
//
// keyword 同时匹配标题与来源；status 只支持 enabled / disabled
// （热文没有生效时间窗，状态就是启用开关本身）。
type HotArticleListQueryForm struct {
	Keyword  string `form:"keyword" json:"keyword"`
	Status   string `form:"status" json:"status"`
	Page     int    `form:"page" json:"page"`
	PageSize int    `form:"page_size" json:"page_size"`
}

// HotArticleCreateForm 新增热文
//
// Enabled 用指针：缺省（nil）视为启用，避免 JSON 省略字段时被解析成 false。
type HotArticleCreateForm struct {
	Title     string `json:"title" binding:"required,max=128"`
	Source    string `json:"source" binding:"max=64"`
	Tag       string `json:"tag" binding:"max=32"`
	Heat      int64  `json:"heat" binding:"omitempty,min=0"`
	URL       string `json:"url" binding:"max=512"`
	SortOrder int    `json:"sort_order"`
	Enabled   *bool  `json:"enabled"`
}

// HotArticleUpdateForm 编辑热文（ID 来自路径参数）
type HotArticleUpdateForm struct {
	Title     string `json:"title" binding:"required,max=128"`
	Source    string `json:"source" binding:"max=64"`
	Tag       string `json:"tag" binding:"max=32"`
	Heat      int64  `json:"heat" binding:"omitempty,min=0"`
	URL       string `json:"url" binding:"max=512"`
	SortOrder int    `json:"sort_order"`
	Enabled   *bool  `json:"enabled"`
}

// HotArticleStatusForm 启用 / 停用
type HotArticleStatusForm struct {
	Enabled bool `json:"enabled"`
}

// HotArticleSortForm 批量排序（按传入 id 顺序重写 sort_order）
//
// 列表页「上移 / 下移」只交换相邻两条，前端提交受影响的两条即可；
// 传全量也可以（幂等）。
type HotArticleSortForm struct {
	Items []HotArticleSortItem `json:"items" binding:"required,min=1,dive"`
}

// HotArticleSortItem 单条排序项
type HotArticleSortItem struct {
	ID        uint64 `json:"id" binding:"required"`
	SortOrder int    `json:"sort_order"`
}
