package adapters

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ============================================================
// 适配层：把「平台统一的 ChatRequest」翻译成各厂商请求
//
// 设计要点：
//   - 编排层永远只认 ChatRequest / ChatResponse，厂商概念不出现在业务代码里
//   - 90% 厂商走 openai_compat，差异由「厂商模板 + ExtraConfig」表达
//   - 凭证校验与错误分类也由适配器负责，保证排障信息足够精确
// ============================================================

// 错误分类
const (
	ErrCategoryDNS            = "dns"
	ErrCategoryTCP            = "tcp"
	ErrCategoryTLS            = "tls"
	ErrCategoryHTTP           = "http"
	ErrCategoryAuth           = "auth"
	ErrCategoryRateLimit      = "rate_limit"
	ErrCategoryQuota          = "quota"
	ErrCategoryModelNotFound  = "model_not_found"
	ErrCategoryBadRequest     = "bad_request"
	ErrCategoryConfig         = "config"
	ErrCategoryUnknown        = "unknown"
)

// ProviderError 平台统一错误（带分类与排查建议）
type ProviderError struct {
	Category   string
	HTTPStatus int
	Message    string
	Raw        string
	Hint       string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Message, e.HTTPStatus, e.Raw)
	}
	return e.Message
}

// ProviderConfig 运行时配置（APIKey 为解密后的明文，只在内存中流转）
type ProviderConfig struct {
	Vendor         string
	BaseURL        string
	AuthMode       string
	AuthHeaderName string
	APIKey         string
	PathChat       string
	PathModels     string
	Extra          map[string]any
	TimeoutSeconds int
}

// ChatMessage 统一消息结构。
// 除 system / user / assistant 外，工具回灌轮会用到 role=tool（携带 ToolCallID）。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls：role=assistant 时，模型要求调用的工具（回灌给模型时的历史还原）
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID：role=tool 时，对应哪一次工具调用
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolDef 平台提供给模型的工具定义（能力的唯一契约面）
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolCall 模型返回的「我要调用哪个工具、参数是什么」意图。
// 注意：模型只表达意图，真正执行永远在平台侧（执行器 + 闸门）。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 模型输出的 JSON 字符串，需解析+校验后才可用
}

// ChatRequest 统一请求结构
type ChatRequest struct {
	Model       string
	Messages    []ChatMessage
	Temperature *float64
	MaxTokens   int
	Stream      bool
	JSONMode    bool
	System      string
	// Tools 非空时启用工具调用；ToolChoice 为空则默认 "auto"
	Tools      []ToolDef
	ToolChoice string
}

// Usage token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse 统一响应结构
type ChatResponse struct {
	Content      string     `json:"content"`
	Model        string     `json:"model"`
	FinishReason string     `json:"finish_reason"`
	Usage        Usage      `json:"usage"`
	LatencyMs    int        `json:"latency_ms"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
}

// StreamChunk 流式分片
type StreamChunk struct {
	Delta        string
	Done         bool
	FinishReason string
	// Model：部分厂商在分片里回显模型名（OpenAI 系每片都带，Anthropic 在 message_start 带）
	Model string
	// Usage：厂商按自身策略在流里给用量（OpenAI 需 stream_options.include_usage，
	// Anthropic 在 message_start/message_delta 给）。给不到就是 nil，不臆造。
	Usage *Usage
}

// ModelInfo 模型信息
type ModelInfo struct {
	Name          string   `json:"name"`
	ContextWindow int      `json:"context_window"`
	Capabilities  []string `json:"capabilities"`
}

// ValidateResult 结构化校验结果
type ValidateResult struct {
	OK           bool        `json:"ok"`
	Stage        string      `json:"stage"`
	Category     string      `json:"category,omitempty"`
	HTTPStatus   int         `json:"http_status,omitempty"`
	Message      string      `json:"message"`
	Raw          string      `json:"raw,omitempty"`
	LatencyMs    int         `json:"latency_ms"`
	Hint         string      `json:"hint,omitempty"`
	ModelCount   int         `json:"model_count"`
	Models       []ModelInfo `json:"models,omitempty"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

// ProviderAdapter 厂商适配器契约
type ProviderAdapter interface {
	Name() string
	DefaultBaseURL() string

	// BuildChatRequest 统一请求 → 厂商 HTTP 请求（端点拼接、鉴权头、参数改名）
	BuildChatRequest(cfg ProviderConfig, req ChatRequest) (*http.Request, error)

	ParseChatResponse(body []byte) (*ChatResponse, error)

	// ParseStreamChunk 解析一行流式数据，返回分片、是否结束、错误
	ParseStreamChunk(line []byte) (*StreamChunk, bool, error)

	// Validate 凭证与连通性校验（最小代价探测）
	Validate(ctx context.Context, cfg ProviderConfig) (*ValidateResult, error)

	ListModels(ctx context.Context, cfg ProviderConfig) ([]ModelInfo, error)

	// MapError 厂商错误码 → 平台统一错误
	MapError(status int, body []byte) *ProviderError
}

// ------------------------------------------------------------------
// 注册表
// ------------------------------------------------------------------

var registry = map[string]ProviderAdapter{}

// Register 适配器注册（各适配器 init 中调用）
func Register(a ProviderAdapter) {
	if a == nil || a.Name() == "" {
		return
	}
	registry[a.Name()] = a
}

// Resolve 按适配器名取用；空名回退到 openai_compat
func Resolve(name string) (ProviderAdapter, error) {
	key := strings.TrimSpace(name)
	if key == "" {
		key = AdapterOpenAICompat
	}
	if a, ok := registry[key]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("未找到适配器 `%s`，请在厂商模板中选择已支持的适配器", key)
}

// RegisteredAdapters 已注册适配器名列表（供前端模板编辑页选择）
func RegisteredAdapters() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// ------------------------------------------------------------------
// 公共工具
// ------------------------------------------------------------------

const (
	AdapterOpenAICompat = "openai_compat"
	AdapterAzureOpenAI  = "azure_openai"
	AdapterAnthropic    = "anthropic"
	AdapterOpenAICustom = "openai_custom"
)

// DefaultTimeoutSeconds 单次请求默认超时
const DefaultTimeoutSeconds = 60

// ExtraString 读取 ExtraConfig 中的字符串项
func ExtraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	v, ok := extra[key]
	if !ok || v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", typed), "0"), ".")
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

// BuildURL 拼接端点：替换 {key} 占位符（来自 Extra），并规范化斜杠
func BuildURL(cfg ProviderConfig, pathTemplate string) (string, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return "", &ProviderError{Category: ErrCategoryConfig, Message: "端点地址为空，请填写 Base URL"}
	}
	base = expandPlaceholders(base, cfg.Extra)
	path := expandPlaceholders(strings.TrimSpace(pathTemplate), cfg.Extra)

	parsed, err := url.Parse(base)
	if err != nil {
		return "", &ProviderError{Category: ErrCategoryConfig, Message: "端点地址不是合法的 URL", Raw: err.Error()}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", &ProviderError{Category: ErrCategoryConfig, Message: "端点地址必须以 http:// 或 https:// 开头"}
	}

	if path == "" {
		return strings.TrimRight(base, "/"), nil
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	full := strings.TrimRight(base, "/") + path

	// 校验占位符是否全部替换（{resource} / {deployment} 之类漏填要明确报错，而不是打到一个坏地址）
	if strings.Contains(full, "{") {
		return "", &ProviderError{
			Category: ErrCategoryConfig,
			Message:  "端点或路径中存在未替换的占位符，请检查厂商差异化参数是否填写完整",
			Raw:      full,
		}
	}
	if _, err := url.Parse(full); err != nil {
		return "", &ProviderError{Category: ErrCategoryConfig, Message: "拼接后的请求地址非法", Raw: err.Error()}
	}
	return full, nil
}

func expandPlaceholders(input string, extra map[string]any) string {
	if input == "" || !strings.Contains(input, "{") {
		return input
	}
	out := input
	for key, value := range extra {
		if value == nil {
			continue
		}
		token := "{" + key + "}"
		if !strings.Contains(out, token) {
			continue
		}
		str := strings.TrimSpace(fmt.Sprintf("%v", value))
		out = strings.ReplaceAll(out, token, str)
	}
	// 兼容 base_url 中直接写 {resource} 的场景：从 extra 的 resource 取
	return out
}

// NewHTTPClient 按配置构造带超时的客户端
func NewHTTPClient(timeoutSeconds int) *http.Client {
	timeout := timeoutSeconds
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	return &http.Client{Timeout: time.Duration(timeout) * time.Second}
}

// ApplyAuth 按鉴权方式写入请求
func ApplyAuth(req *http.Request, cfg ProviderConfig) {
	switch strings.TrimSpace(cfg.AuthMode) {
	case "query_param":
		if cfg.APIKey != "" {
			query := req.URL.Query()
			query.Set("api_key", cfg.APIKey)
			req.URL.RawQuery = query.Encode()
		}
	case "api_key_header":
		headerName := strings.TrimSpace(cfg.AuthHeaderName)
		if headerName == "" {
			headerName = "api-key"
		}
		if cfg.APIKey != "" {
			req.Header.Set(headerName, cfg.APIKey)
		}
	case "signature":
		// 签名鉴权交给自定义适配器处理（需要 body 参与签名）
	default: // bearer
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
	}
}

// ReadBody 读取响应体（带长度保护）
func ReadBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("响应为空")
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// NormalizeBodySnippet 生成可回显的错误片段（已做长度裁剪）
func NormalizeBodySnippet(body []byte) string {
	snippet := strings.TrimSpace(string(body))
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	if len(snippet) > 300 {
		snippet = snippet[:300] + "..."
	}
	return snippet
}

// ClassifyTransportError 把传输层错误分类为 dns / tcp / tls / http
func ClassifyTransportError(err error) *ProviderError {
	if err == nil {
		return nil
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &ProviderError{
			Category: ErrCategoryDNS,
			Message:  "域名解析失败",
			Raw:      err.Error(),
			Hint:     "检查端点域名拼写、内网 DNS 配置，或该地址是否需要走代理",
		}
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return &ProviderError{
			Category: ErrCategoryTLS,
			Message:  "TLS 证书校验失败",
			Raw:      err.Error(),
			Hint:     "自签证书需要把 CA 导入系统信任链；平台不提供跳过证书校验的开关",
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return &ProviderError{
				Category: ErrCategoryTCP,
				Message:  "请求超时",
				Raw:      err.Error(),
				Hint:     "检查网络可达性；若为私有化服务，确认服务进程是否已启动、端口是否放通",
			}
		}
		return &ProviderError{
			Category: ErrCategoryTCP,
			Message:  "连接失败",
			Raw:      err.Error(),
			Hint:     "检查网络可达性与端口；私有化服务确认已启动",
		}
	}

	if strings.Contains(strings.ToLower(err.Error()), "certificate") ||
		strings.Contains(strings.ToLower(err.Error()), "x509") {
		return &ProviderError{
			Category: ErrCategoryTLS,
			Message:  "TLS 证书校验失败",
			Raw:      err.Error(),
			Hint:     "自签证书需要把 CA 导入系统信任链",
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{
			Category: ErrCategoryTCP,
			Message:  "请求超时",
			Raw:      err.Error(),
			Hint:     "加大超时时间，或检查网络链路质量",
		}
	}

	return &ProviderError{Category: ErrCategoryUnknown, Message: "请求失败", Raw: err.Error()}
}

// DecodeJSON 宽松解码（错误时返回带片段的可读错误）
func DecodeJSON(body []byte, target any) error {
	if err := json.Unmarshal(body, target); err != nil {
		return &ProviderError{
			Category: ErrCategoryHTTP,
			Message:  "响应不是合法的 JSON，可能端点地址指向了非模型服务",
			Raw:      NormalizeBodySnippet(body),
		}
	}
	return nil
}
