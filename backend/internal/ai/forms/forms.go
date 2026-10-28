package forms

// ============================================================
// 大模型配置接入 —— 表单定义
// ============================================================

// LlmProviderForm 新增/更新 Provider。
// APIKey 留空表示「不修改」（更新场景）；新增场景必填校验在服务层做，
// 因为私有化服务（Ollama / vLLM）允许无 Key。
type LlmProviderForm struct {
	ProviderID     string         `json:"provider_id" form:"provider_id"`
	Vendor         string         `json:"vendor" form:"vendor" binding:"required,max=32"`
	DisplayName    string         `json:"display_name" form:"display_name" binding:"required,max=128"`
	BaseURL        string         `json:"base_url" form:"base_url" binding:"max=256"`
	AuthMode       string         `json:"auth_mode" form:"auth_mode" binding:"max=24"`
	AuthHeaderName string         `json:"auth_header_name" form:"auth_header_name" binding:"max=64"`
	APIKey         string         `json:"api_key" form:"api_key" binding:"max=512"`
	ExtraConfig    map[string]any `json:"extra_config" form:"extra_config"`
	TimeoutSeconds int            `json:"timeout_seconds" form:"timeout_seconds"`
	MaxRetries     int            `json:"max_retries" form:"max_retries"`
	IsShared       *bool          `json:"is_shared" form:"is_shared"`
	Remark         string         `json:"remark" form:"remark" binding:"max=256"`
}

// LlmProviderTestForm 不落库的即测即用（接入向导第三步）
// 允许只填凭证、不写 vendor 之外的信息，服务层用模板补默认值
type LlmProviderTestForm struct {
	Vendor         string         `json:"vendor" form:"vendor" binding:"required,max=32"`
	BaseURL        string         `json:"base_url" form:"base_url" binding:"max=256"`
	AuthMode       string         `json:"auth_mode" form:"auth_mode" binding:"max=24"`
	AuthHeaderName string         `json:"auth_header_name" form:"auth_header_name" binding:"max=64"`
	APIKey         string         `json:"api_key" form:"api_key" binding:"max=512"`
	ExtraConfig    map[string]any `json:"extra_config" form:"extra_config"`
	TimeoutSeconds int            `json:"timeout_seconds" form:"timeout_seconds"`
	// Model 可指定探测用模型（私有化服务常无模型列表接口）
	Model string `json:"model" form:"model" binding:"max=128"`
}

// LlmProviderStatusForm 启停入参
type LlmProviderStatusForm struct {
	ProviderID string `json:"provider_id" form:"provider_id" binding:"required"`
}

// LlmModelForm 手工新增/更新模型
//
// 价格与备注使用指针：更新接口是「部分字段更新」（例如只切换启停），
// 用零值判断会把未提交的字段误清零。
type LlmModelForm struct {
	ProviderID      string   `json:"provider_id" form:"provider_id"`
	ModelUID        string   `json:"model_uid" form:"model_uid"`
	ModelName       string   `json:"model_name" form:"model_name" binding:"required,max=128"`
	DisplayName     string   `json:"display_name" form:"display_name" binding:"max=128"`
	ContextWindow   int      `json:"context_window" form:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens" form:"max_output_tokens"`
	Capabilities    []string `json:"capabilities" form:"capabilities"`
	InputPrice      *float64 `json:"input_price" form:"input_price"`
	OutputPrice     *float64 `json:"output_price" form:"output_price"`
	IsEnabled       *bool    `json:"is_enabled" form:"is_enabled"`
	Remark          *string  `json:"remark" form:"remark"`
}

// LlmModelQueryForm 模型列表查询
type LlmModelQueryForm struct {
	ProviderID string `json:"provider_id" form:"provider_id"`
	Keyword    string `json:"keyword" form:"keyword"`
	Page       int    `json:"page" form:"page"`
	PageSize   int    `json:"page_size" form:"page_size"`
}

// LlmVendorTemplateForm 厂商模板新增/更新
type LlmVendorTemplateForm struct {
	Vendor          string           `json:"vendor" form:"vendor" binding:"required,max=32"`
	Label           string           `json:"label" form:"label" binding:"required,max=64"`
	Category        string           `json:"category" form:"category" binding:"max=24"`
	Icon            string           `json:"icon" form:"icon" binding:"max=32"`
	DefaultBaseURL  string           `json:"default_base_url" form:"default_base_url" binding:"max=256"`
	AuthMode        string           `json:"auth_mode" form:"auth_mode" binding:"max=24"`
	AuthHeaderName  string           `json:"auth_header_name" form:"auth_header_name" binding:"max=64"`
	PathChat        string           `json:"path_chat" form:"path_chat" binding:"max=128"`
	PathModels      string           `json:"path_models" form:"path_models" binding:"max=128"`
	RequiredFields  []string         `json:"required_fields" form:"required_fields"`
	ExtraFields     []map[string]any `json:"extra_fields" form:"extra_fields"`
	DefaultCaps     []string         `json:"default_capabilities" form:"default_capabilities"`
	Adapter         string           `json:"adapter" form:"adapter" binding:"max=32"`
	IsEnabled       *bool            `json:"is_enabled" form:"is_enabled"`
	SortOrder       int              `json:"sort_order" form:"sort_order"`
	DocsURL         string           `json:"docs_url" form:"docs_url" binding:"max=256"`
	Remark          string           `json:"remark" form:"remark" binding:"max=256"`
}

// LlmProviderIDForm 按 provider_id 操作的通用入参
type LlmProviderIDForm struct {
	ProviderID string `json:"provider_id" form:"provider_id" binding:"required"`
}

// LlmModelUIDForm 按 model_uid 操作的通用入参
type LlmModelUIDForm struct {
	ModelUID string `json:"model_uid" form:"model_uid" binding:"required"`
}

// LlmVendorNameForm 按 vendor 操作的通用入参
type LlmVendorNameForm struct {
	Vendor string `json:"vendor" form:"vendor" binding:"required"`
}

// LlmVendorListQueryForm 厂商模板列表查询
type LlmVendorListQueryForm struct {
	// All=true 时返回含停用在内的全部模板（管理页用）；否则仅返回启用项（接入向导用）
	All bool `json:"all" form:"all"`
}

// LlmChatMessage 运行时对话的单条消息。
// 角色只允许 user / assistant：系统提示词由服务端统一注入（req.System），
// 不开放给前端，避免调用方伪造系统指令。
type LlmChatMessage struct {
	Role    string `json:"role" form:"role" binding:"required,oneof=user assistant"`
	Content string `json:"content" form:"content" binding:"required,max=16000"`
}

// LlmChatCompletionForm 运行时对话入参。
// ProviderID 留空 = 使用默认服务商（is_default 且 enabled；无默认则取任一 enabled）。
type LlmChatCompletionForm struct {
	ProviderID string `json:"provider_id" form:"provider_id" binding:"max=15"`
	// RunID 本次运行的标识，由前端在发起提问时生成，取消时原样回传（见 /chat/cancel）。
	// 留空 = 不提供显式取消能力，只依赖「客户端断开连接」这一条通道。
	// alphanum 限制让 run_id 只可能是字母数字，杜绝奇怪字符进入服务端注册表。
	RunID    string           `json:"run_id" form:"run_id" binding:"omitempty,min=8,max=48,alphanum"`
	Messages []LlmChatMessage `json:"messages" form:"messages" binding:"required,min=1,max=40,dive"`
}

// LlmChatCancelForm 主动取消运行中的一次对话入参。
type LlmChatCancelForm struct {
	RunID string `json:"run_id" form:"run_id" binding:"required,min=8,max=48,alphanum"`
}

// AICreateOrderConfirmForm AI 对话中「确认创建工单」入参。
//
// 只接收草稿里由模型产出、用户已过目的业务字段。两个身份字段刻意不接收：
//   - 申请人（创建人）：一律取当前登录会话，前端传什么都不认；
//   - 执行人：留空即取当前登录用户，用户明确指定时才作为「他想让谁执行」的意图传入，
//     服务端仍会校验该用户存在且启用。
//
// db_type / environment 也不接收：统一由服务端按 instance_id 查实例补齐，
// 避免前端传一个与实例不符的类型绕过目标库权限校验。
type AICreateOrderConfirmForm struct {
	Title       string `json:"title" form:"title" binding:"required,min=3,max=196"`
	Content     string `json:"content" form:"content" binding:"required"`
	InstanceID  string `json:"instance_id" form:"instance_id" binding:"required,min=3,max=15"`
	Database    string `json:"database" form:"database" binding:"max=1024"`
	Schema      string `json:"schema" form:"schema" binding:"max=1024"`
	RollbackSQL string `json:"rollback_sql" form:"rollback_sql"`
	Remark      string `json:"remark" form:"remark" binding:"max=1024"`
	Executor    string `json:"executor" form:"executor" binding:"omitempty,max=32"`
}

// AICreateBackupConfirmForm AI 对话中「确认创建备份任务」入参。
//
// 与创建工单同一口径：只接草稿里由模型产出、用户已过目的业务字段，
// 创建人一律取当前登录会话（前端传什么都不认）。
// 备份方式（driver_type）留空时由服务端按实例数据库类型推导，
// 存储目标留空时由服务端选备份中心（LOCAL_SERVER），都不开放「任意指定」。
type AICreateBackupConfirmForm struct {
	Name          string   `json:"name" form:"name" binding:"required,min=2,max=128"`
	InstanceID    string   `json:"instance_id" form:"instance_id" binding:"required,min=3,max=15"`
	ObjectMode    string   `json:"object_mode" form:"object_mode" binding:"required,oneof=ALL CUSTOM"`
	Databases     []string `json:"databases" form:"databases" binding:"omitempty,max=200,dive,max=255"`
	BackupType    string   `json:"backup_type" form:"backup_type" binding:"omitempty,oneof=FULL SCHEMA DATA"`
	DriverType    string   `json:"driver_type" form:"driver_type" binding:"omitempty,oneof=LOGICAL PG_BACKREST XTRABACKUP"`
	ScheduleDays  []string `json:"schedule_days" form:"schedule_days" binding:"omitempty,max=7,dive,max=8"`
	ScheduleTime  string   `json:"schedule_time" form:"schedule_time" binding:"omitempty,len=5"`
	RetentionDays int      `json:"retention_days" form:"retention_days" binding:"omitempty,min=1,max=3650"`
	StorageTarget string   `json:"storage_target" form:"storage_target" binding:"omitempty,max=64"`
}
