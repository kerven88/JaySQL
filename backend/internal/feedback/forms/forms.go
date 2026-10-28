package forms

// ============================================================
// 用户反馈 —— 表单定义
// ============================================================

// SubmitFeedbackForm 提交反馈。
// 走 multipart/form-data：文本字段 + 可选的多张图片（字段名 images）。
// 图片数量、大小、类型在服务层做二次校验（客户端校验不可信）。
type SubmitFeedbackForm struct {
	Category  string `form:"category" json:"category" binding:"required,max=32"`
	Content   string `form:"content" json:"content" binding:"required,max=2000"`
	Contact   string `form:"contact" json:"contact" binding:"max=128"`
	PagePath  string `form:"page_path" json:"page_path" binding:"max=512"`
	UserAgent string `form:"user_agent" json:"user_agent" binding:"max=512"`
}

// FeedbackListQueryForm 反馈列表查询（管理员）
type FeedbackListQueryForm struct {
	Category  string `form:"category" json:"category"`
	Status    string `form:"status" json:"status"`
	Keyword   string `form:"keyword" json:"keyword"`
	StartTime string `form:"start_time" json:"start_time"`
	EndTime   string `form:"end_time" json:"end_time"`
	Page      int    `form:"page" json:"page"`
	PageSize  int    `form:"page_size" json:"page_size"`
}

// FeedbackUpdateForm 更新反馈处理状态 / 备注（管理员）
type FeedbackUpdateForm struct {
	ID     uint64 `form:"id" json:"id" binding:"required"`
	Status string `form:"status" json:"status" binding:"required,max=16"`
	Remark string `form:"remark" json:"remark" binding:"max=1024"`
}

// FeedbackDeleteForm 删除反馈（管理员）
type FeedbackDeleteForm struct {
	ID uint64 `form:"id" json:"id" binding:"required"`
}
