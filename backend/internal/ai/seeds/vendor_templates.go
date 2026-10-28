package seeds

// ============================================================
// 内置大模型厂商模板定义
//
// 这里是「厂商差异」的唯一来源：默认端点、鉴权方式、路径模板、
// 差异化字段、适配器归属。启动时由 services.SeedBuiltinVendorTemplates
// 幂等播种到 insight_llm_vendor_templates。
//
// 新增一家厂商：
//   - 若兼容 OpenAI 协议 → 在此追加一条 Adapter=openai_compat 的定义即可（零代码）
//   - 若为新协议 → 先实现一个适配器，再追加定义
//
// 字段类型白名单（ExtraField.Type）：string / password / number / boolean / select
// ============================================================

// ExtraField 厂商差异化字段定义，驱动前端动态表单渲染
type ExtraField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
	Help        string   `json:"help,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
}

// VendorTemplate 厂商模板定义
type VendorTemplate struct {
	Vendor          string
	Label           string
	Category        string
	Icon            string
	DefaultBaseURL  string
	AuthMode        string
	AuthHeaderName  string
	PathChat        string
	PathModels      string
	RequiredFields  []string
	ExtraFields     []ExtraField
	DefaultCaps     []string
	Adapter         string
	SortOrder       int
	DocsURL         string
	Remark          string
}

const (
	AdapterOpenAICompat = "openai_compat"
	AdapterAzureOpenAI  = "azure_openai"
	AdapterAnthropic    = "anthropic"
	AdapterOpenAICustom = "openai_custom"
)

var openAICapabilities = []string{"chat", "stream", "tool_call", "json_mode"}

// BuiltinVendorTemplates 内置模板清单（顺序即默认展示顺序）
var BuiltinVendorTemplates = []VendorTemplate{
	{
		Vendor:         "openai",
		Label:          "OpenAI",
		Category:       "international",
		Icon:           "openai",
		DefaultBaseURL: "https://api.openai.com/v1",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url", "api_key"},
		ExtraFields: []ExtraField{
			{Key: "organization", Label: "Organization ID", Type: "string", Required: false, Help: "企业账号可填，用于区分组织额度"},
		},
		DefaultCaps: openAICapabilities,
		Adapter:     AdapterOpenAICompat,
		SortOrder:   10,
		DocsURL:     "https://platform.openai.com/api-keys",
		Remark:      "官方 OpenAI 服务",
	},
	{
		Vendor:         "gemini",
		Label:          "Gemini",
		Category:       "international",
		Icon:           "gemini",
		DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url", "api_key"},
		ExtraFields:    nil,
		DefaultCaps:    openAICapabilities,
		Adapter:        AdapterOpenAICompat,
		SortOrder:      30,
		DocsURL:        "https://aistudio.google.com/app/apikey",
		Remark:         "Google 官方 OpenAI 兼容端点，鉴权走 Authorization: Bearer <API Key>",
	},
	{
		Vendor:         "deepseek",
		Label:          "DeepSeek 深度求索",
		Category:       "domestic",
		Icon:           "deepseek",
		DefaultBaseURL: "https://api.deepseek.com/v1",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url", "api_key"},
		ExtraFields:    nil,
		DefaultCaps:    openAICapabilities,
		Adapter:        AdapterOpenAICompat,
		SortOrder:      40,
		DocsURL:        "https://platform.deepseek.com/api_keys",
		Remark:         "OpenAI 兼容协议，模型如 deepseek-chat / deepseek-reasoner",
	},
	{
		Vendor:         "dashscope",
		Label:          "通义千问",
		Category:       "domestic",
		Icon:           "qwen",
		DefaultBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url", "api_key"},
		ExtraFields:    nil,
		DefaultCaps:    openAICapabilities,
		Adapter:        AdapterOpenAICompat,
		SortOrder:      50,
		DocsURL:        "https://bailian.console.aliyun.com/",
		Remark:         "使用 DashScope 的 OpenAI 兼容模式端点",
	},
	{
		Vendor:         "moonshot",
		Label:          "Kimi",
		Category:       "domestic",
		Icon:           "moonshot",
		DefaultBaseURL: "https://api.moonshot.cn/v1",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url", "api_key"},
		ExtraFields:    nil,
		DefaultCaps:    openAICapabilities,
		Adapter:        AdapterOpenAICompat,
		SortOrder:      60,
		DocsURL:        "https://platform.moonshot.cn/console/api-keys",
		Remark:         "OpenAI 兼容协议",
	},
	{
		Vendor:         "zhipu",
		Label:          "智谱 GLM",
		Category:       "domestic",
		Icon:           "zhipu",
		DefaultBaseURL: "https://open.bigmodel.cn/api/paas/v4",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url", "api_key"},
		ExtraFields:    nil,
		DefaultCaps:    openAICapabilities,
		Adapter:        AdapterOpenAICompat,
		SortOrder:      70,
		DocsURL:        "https://open.bigmodel.cn/usercenter/apikeys",
		Remark:         "OpenAI 兼容协议",
	},
	{
		Vendor:         "ollama",
		Label:          "Ollama（本地）",
		Category:       "private",
		Icon:           "ollama",
		DefaultBaseURL: "http://127.0.0.1:11434/v1",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url"},
		ExtraFields: []ExtraField{
			{Key: "model", Label: "默认模型名", Type: "string", Required: false, Placeholder: "qwen2.5:14b", Help: "ollama list 可查看本机已拉取的模型"},
		},
		DefaultCaps: []string{"chat", "stream"},
		Adapter:     AdapterOpenAICompat,
		SortOrder:   80,
		DocsURL:     "https://ollama.com/",
		Remark:      "本机/内网 Ollama，接口路径为 OpenAI 兼容模式",
	},
	{
		Vendor:         "vllm",
		Label:          "vLLM / 私有化推理服务",
		Category:       "private",
		Icon:           "vllm",
		DefaultBaseURL: "http://127.0.0.1:8000/v1",
		AuthMode:       "bearer",
		PathChat:       "/chat/completions",
		PathModels:     "/models",
		RequiredFields: []string{"base_url"},
		ExtraFields: []ExtraField{
			{Key: "model", Label: "默认模型名", Type: "string", Required: false, Placeholder: "Qwen/Qwen2.5-32B-Instruct"},
			{Key: "api_key", Label: "API Key（可选）", Type: "password", Required: false, Secret: true, Help: "vLLM 启动时未配置 --api-key 可留空"},
		},
		DefaultCaps: openAICapabilities,
		Adapter:     AdapterOpenAICompat,
		SortOrder:   90,
		DocsURL:     "https://docs.vllm.ai/",
		Remark:      "vLLM / SGLang / LM Studio / FastChat 等 OpenAI 兼容推理服务通用",
	},
	{
		Vendor:          "openai_compatible",
		Label:           "自定义 OpenAI 兼容服务",
		Category:        "custom",
		Icon:            "custom",
		DefaultBaseURL:  "",
		AuthMode:        "bearer",
		PathChat:        "/chat/completions",
		PathModels:      "/models",
		RequiredFields:  []string{"base_url"},
		ExtraFields: []ExtraField{
			{Key: "api_key", Label: "API Key（可选）", Type: "password", Required: false, Secret: true},
			{Key: "model", Label: "默认模型名", Type: "string", Required: false},
			{Key: "auth_header", Label: "自定义鉴权 header 名", Type: "string", Required: false, Placeholder: "Authorization", Help: "留空则使用标准 Authorization: Bearer"},
			{Key: "extra_headers", Label: "附加请求头（JSON）", Type: "string", Required: false, Placeholder: "{\"X-Tenant\":\"db\"}"},
		},
		DefaultCaps: openAICapabilities,
		Adapter:     AdapterOpenAICustom,
		SortOrder:   100,
		DocsURL:     "",
		Remark:      "企业网关 / 自研服务 / 其他厂商：填端点即可，路径与鉴权均可在高级项中覆盖",
	},
}
