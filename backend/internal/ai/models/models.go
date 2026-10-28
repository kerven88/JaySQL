package models

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/pkg/utils"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================
// 大模型配置接入模块 —— 四张表
//   insight_llm_providers         服务商接入配置（凭证密文 + 运行时状态）
//   insight_llm_models            模型清单（独立生命周期，拆表管理）
//   insight_llm_vendor_templates  厂商模板（差异定义，落库可维护、不发版加厂商）
//   insight_llm_call_logs         调用日志（延迟 / token 用量 / 错误）
// ============================================================

// Provider 状态机：draft -> verified -> enabled；异常时 degraded
const (
	LlmProviderStatusDraft    = "draft"
	LlmProviderStatusVerified = "verified"
	LlmProviderStatusEnabled  = "enabled"
	LlmProviderStatusDisabled = "disabled"
	LlmProviderStatusDegraded = "degraded"
)

// Provider 级健康状态
const (
	LlmHealthUnknown  = "unknown"
	LlmHealthHealthy  = "healthy"
	LlmHealthDegraded = "degraded"
	LlmHealthDown     = "down"
)

// 模型来源与状态
const (
	LlmModelSourceAuto   = "auto"
	LlmModelSourceManual = "manual"

	LlmModelStatusAvailable   = "available"
	LlmModelStatusUnavailable = "unavailable"
	LlmModelStatusDeprecated  = "deprecated"
)

// 鉴权方式
const (
	LlmAuthBearer       = "bearer"
	LlmAuthAPIKeyHeader = "api_key_header"
	LlmAuthQueryParam   = "query_param"
	LlmAuthSignature    = "signature" // 由 openai_custom 适配器的 extra_config 描述签名规则
)

// 厂商分类
const (
	LlmVendorCategoryInternational = "international"
	LlmVendorCategoryDomestic      = "domestic"
	LlmVendorCategoryPrivate       = "private"
	LlmVendorCategoryCustom        = "custom"
)

// InsightLlmProviders 大模型服务商接入配置
//
// 字段结构对齐已有的 insight_cloud_accounts（凭证加密 + ExtraConfig + Owner/IsShared + 验证状态），
// 保持团队认知一致。
type InsightLlmProviders struct {
	*commonModels.Model

	ProviderID  string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_llm_provider_id;comment:业务ID" json:"provider_id"`
	Vendor      string `gorm:"type:varchar(32);not null;default:'';index:idx_llm_vendor;comment:厂商标识" json:"vendor"`
	DisplayName string `gorm:"type:varchar(128);not null;default:'';comment:显示名" json:"display_name"`

	BaseURL  string `gorm:"type:varchar(256);not null;default:'';comment:端点地址" json:"base_url"`
	AuthMode string `gorm:"type:varchar(24);not null;default:'bearer';comment:鉴权方式 bearer/api_key_header/query_param/signature" json:"auth_mode"`

	// APIKeyEnc 密文永不参与序列化；对外只暴露 APIKeyMask
	APIKeyEnc  string `gorm:"type:varchar(1024);not null;default:'';comment:API Key 密文(AES-256-GCM)" json:"-"`
	APIKeyMask string `gorm:"type:varchar(48);not null;default:'';comment:API Key 掩码(界面回显)" json:"api_key_mask"`

	ExtraConfig datatypes.JSON `gorm:"type:json;null;default:null;comment:厂商差异化参数" json:"extra_config"`

	// DefaultModelID 指向 insight_llm_models.model_uid（不用模型名，避免改名/删除后悬空）
	DefaultModelID string         `gorm:"type:char(15);not null;default:'';comment:默认模型业务ID" json:"default_model_id"`
	Capabilities   datatypes.JSON `gorm:"type:json;null;default:null;comment:Provider级能力结论" json:"capabilities"`

	TimeoutSeconds int `gorm:"type:int;not null;default:60;comment:单次请求超时(秒)" json:"timeout_seconds"`
	MaxRetries     int `gorm:"type:int;not null;default:1;comment:重试次数" json:"max_retries"`

	Status    string `gorm:"type:varchar(16);not null;default:'draft';index:idx_llm_provider_status;comment:状态 draft/verified/enabled/disabled/degraded" json:"status"`
	IsDefault bool   `gorm:"type:boolean;not null;default:false;comment:是否场景默认Provider" json:"is_default"`
	IsShared  bool   `gorm:"type:boolean;not null;default:false;comment:是否共享给全部使用者" json:"is_shared"`
	Owner     string `gorm:"type:varchar(64);not null;default:'';index:idx_llm_provider_owner;comment:创建人" json:"owner"`

	LastValidatedAt      commonModels.LocalTime `gorm:"comment:最近验证通过时间" json:"last_validated_at"`
	LastValidationStatus string                 `gorm:"type:varchar(16);not null;default:'unknown';comment:验证状态 success/error/unknown" json:"last_validation_status"`
	LastValidationMsg    string                 `gorm:"type:varchar(512);not null;default:'';comment:最近验证结果摘要" json:"last_validation_msg"`

	HealthStatus    string                 `gorm:"type:varchar(16);not null;default:'unknown';comment:健康状态 unknown/healthy/degraded/down" json:"health_status"`
	HealthCheckedAt commonModels.LocalTime `gorm:"comment:最近健康检查时间" json:"health_checked_at"`
	LastLatencyMs   int                    `gorm:"type:int;not null;default:0;comment:最近一次请求耗时(毫秒)" json:"last_latency_ms"`

	Remark string `gorm:"type:varchar(256);not null;default:'';comment:备注" json:"remark"`
}

func (InsightLlmProviders) TableName() string { return "insight_llm_providers" }

func (p *InsightLlmProviders) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ProviderID == "" {
		p.ProviderID = utils.GenerateLlmProviderID()
	}
	return
}

// InsightLlmModels 模型清单（按 Provider 挂载）
type InsightLlmModels struct {
	*commonModels.Model

	ModelUID   string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_llm_model_uid;comment:业务ID" json:"model_uid"`
	ProviderID string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_llm_provider_model,priority:1;index:idx_llm_model_provider;comment:所属Provider" json:"provider_id"`

	// ModelName 为厂商侧调用标识；与 ProviderID 组成唯一键
	ModelName   string `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uniq_llm_provider_model,priority:2;comment:厂商侧模型标识" json:"model_name"`
	DisplayName string `gorm:"type:varchar(128);not null;default:'';comment:界面显示名/别名" json:"display_name"`

	ContextWindow   int `gorm:"type:int;not null;default:0;comment:上下文窗口 token 数" json:"context_window"`
	MaxOutputTokens int `gorm:"type:int;not null;default:0;comment:最大输出 token 数" json:"max_output_tokens"`

	Capabilities datatypes.JSON `gorm:"type:json;null;default:null;comment:模型级能力标签" json:"capabilities"`

	InputPrice  float64 `gorm:"type:decimal(12,4);not null;default:0;comment:每百万输入token单价" json:"input_price"`
	OutputPrice float64 `gorm:"type:decimal(12,4);not null;default:0;comment:每百万输出token单价" json:"output_price"`

	IsDefault bool `gorm:"type:boolean;not null;default:false;comment:是否所属Provider默认模型" json:"is_default"`
	IsEnabled bool `gorm:"type:boolean;not null;default:true;comment:是否对使用者开放" json:"is_enabled"`

	Source    string `gorm:"type:varchar(16);not null;default:'auto';comment:来源 auto/manual" json:"source"`
	Status    string `gorm:"type:varchar(16);not null;default:'available';comment:状态 available/unavailable/deprecated" json:"status"`
	LastSeenAt commonModels.LocalTime `gorm:"comment:最近一次在厂商侧出现的刷新时间" json:"last_seen_at"`

	Remark string `gorm:"type:varchar(256);not null;default:'';comment:备注" json:"remark"`
}

func (InsightLlmModels) TableName() string { return "insight_llm_models" }

func (m *InsightLlmModels) BeforeCreate(tx *gorm.DB) (err error) {
	if m.ModelUID == "" {
		m.ModelUID = utils.GenerateLlmModelID()
	}
	return
}

// InsightLlmVendorTemplates 厂商模板（差异定义）
//
// 模板决定「新建 Provider 时的默认值 + 表单渲染」，不回写存量 Provider。
// Adapter 字段把「厂商」与「适配器」解耦：新增厂商复用已有适配器即为纯数据操作。
type InsightLlmVendorTemplates struct {
	*commonModels.Model

	TemplateID string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_llm_template_id;comment:业务ID" json:"template_id"`

	// Vendor 唯一；自定义厂商的 vendor 由用户填写
	Vendor   string `gorm:"type:varchar(32);not null;default:'';uniqueIndex:uniq_llm_template_vendor;comment:厂商标识" json:"vendor"`
	Label    string `gorm:"type:varchar(64);not null;default:'';comment:显示名" json:"label"`
	Category string `gorm:"type:varchar(24);not null;default:'custom';index:idx_llm_template_category;comment:分类 international/domestic/private/custom" json:"category"`
	Icon     string `gorm:"type:varchar(32);not null;default:'';comment:图标标识" json:"icon"`

	DefaultBaseURL string `gorm:"type:varchar(256);not null;default:'';comment:默认端点(支持 {resource} 占位)" json:"default_base_url"`
	AuthMode       string `gorm:"type:varchar(24);not null;default:'bearer';comment:鉴权方式" json:"auth_mode"`
	AuthHeaderName string `gorm:"type:varchar(64);not null;default:'';comment:鉴权 header 名" json:"auth_header_name"`
	PathChat       string `gorm:"type:varchar(128);not null;default:'';comment:对话路径模板" json:"path_chat"`
	PathModels     string `gorm:"type:varchar(128);not null;default:'';comment:模型列表路径模板" json:"path_models"`

	RequiredFields      datatypes.JSON `gorm:"type:json;null;default:null;comment:必填基础字段" json:"required_fields"`
	ExtraFields         datatypes.JSON `gorm:"type:json;null;default:null;comment:差异化字段定义(驱动动态表单)" json:"extra_fields"`
	DefaultCapabilities datatypes.JSON `gorm:"type:json;null;default:null;comment:默认能力预设" json:"default_capabilities"`

	// Adapter 指向 adapters 注册表中的适配器名
	Adapter string `gorm:"type:varchar(32);not null;default:'openai_compat';comment:适配器 openai_compat/azure_openai/anthropic/openai_custom" json:"adapter"`

	IsBuiltin bool `gorm:"type:boolean;not null;default:false;comment:是否内置模板(内置禁止删除)" json:"is_builtin"`
	IsEnabled bool `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"is_enabled"`
	SortOrder int  `gorm:"type:int;not null;default:100;comment:排序" json:"sort_order"`

	// DefinitionHash 定义指纹：用于启动播种时判断「定义是否变化」，
	// 变化才更新定义字段，运营侧改过的 is_enabled / sort_order / label 永不被覆盖
	DefinitionHash string `gorm:"type:char(32);not null;default:'';comment:定义指纹" json:"definition_hash"`

	DocsURL string `gorm:"type:varchar(256);not null;default:'';comment:凭证获取指引" json:"docs_url"`
	Remark  string `gorm:"type:varchar(256);not null;default:'';comment:备注" json:"remark"`
}

func (InsightLlmVendorTemplates) TableName() string { return "insight_llm_vendor_templates" }

func (t *InsightLlmVendorTemplates) BeforeCreate(tx *gorm.DB) (err error) {
	if t.TemplateID == "" {
		t.TemplateID = utils.GenerateLlmTemplateID()
	}
	return
}

// InsightLlmCallLogs 调用日志（用量 / 延迟 / 错误）
type InsightLlmCallLogs struct {
	*commonModels.Model

	ProviderID string `gorm:"type:char(15);not null;default:'';index:idx_llm_log_provider;comment:Provider业务ID" json:"provider_id"`
	ModelName  string `gorm:"type:varchar(128);not null;default:'';comment:实际调用的模型" json:"model_name"`
	Scene      string `gorm:"type:varchar(32);not null;default:'';index:idx_llm_log_scene;comment:调用场景 chat/parse/validate/health" json:"scene"`
	Username   string `gorm:"type:varchar(64);not null;default:'';index:idx_llm_log_username;comment:调用人" json:"username"`

	LatencyMs        int `gorm:"type:int;not null;default:0;comment:耗时(毫秒)" json:"latency_ms"`
	PromptTokens     int `gorm:"type:int;not null;default:0;comment:输入token" json:"prompt_tokens"`
	CompletionTokens int `gorm:"type:int;not null;default:0;comment:输出token" json:"completion_tokens"`
	TotalTokens      int `gorm:"type:int;not null;default:0;comment:总token" json:"total_tokens"`

	Status       string `gorm:"type:varchar(16);not null;default:'success';index:idx_llm_log_status;comment:success/error" json:"status"`
	ErrorCode    string `gorm:"type:varchar(32);not null;default:'';comment:错误分类" json:"error_code"`
	ErrorMessage string `gorm:"type:varchar(512);not null;default:'';comment:错误摘要(已脱敏)" json:"error_message"`
}

func (InsightLlmCallLogs) TableName() string { return "insight_llm_call_logs" }
