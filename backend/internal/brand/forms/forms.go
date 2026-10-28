package forms

// ============================================================
// 品牌设置 —— 表单定义
//
// 品牌配置（登录页 Logo / 侧栏 Logo）走通用 KV 表 insight_system_params，
// 因此这里没有"配置内容"表单，只有上传 / 恢复默认两个动作的参数。
// ============================================================

// BrandLogoTypeForm 上传 / 重置 Logo 时指定的位置
//
// type 取值：login（登录页大 Logo）| sidebar（页面左上角 Logo）
// 走 multipart 的 PostForm 字段，不走 JSON。
type BrandLogoTypeForm struct {
	Type string `form:"type" json:"type"`
}

// BrandTextForm 品牌文字（名称 / 首页大标题）
//
// 两个字段都允许为空串：空 = 恢复内置默认文案，管理员不必一次都填。
type BrandTextForm struct {
	BrandName string `json:"brand_name"`
	HomeTitle string `json:"home_title"`
}
