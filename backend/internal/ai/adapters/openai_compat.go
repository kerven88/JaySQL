package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// openaiCompatAdapter：覆盖 90% 厂商
//
// OpenAI / DeepSeek / 通义千问（DashScope 兼容模式）/ Kimi / 智谱 /
// 百川 / 硅基流动 / vLLM / Ollama / SGLang / LM Studio / OneAPI ...
//
// 差异（端点、鉴权头、路径）由 ProviderConfig 与厂商模板表达，本文件不做厂商分支。
// ============================================================

func init() {
	Register(&openAICompat{})
}

type openAICompat struct{}

func (a *openAICompat) Name() string { return AdapterOpenAICompat }

func (a *openAICompat) DefaultBaseURL() string { return "https://api.openai.com/v1" }

func (a *openAICompat) BuildChatRequest(cfg ProviderConfig, req ChatRequest) (*http.Request, error) {
	endpoint := cfg.PathChat
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/chat/completions"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return nil, err
	}

	payload, err := buildOpenAIChatPayload(req, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, full, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	ApplyAuth(httpReq, cfg)

	// 自定义附加请求头（openai_custom 场景）
	applyExtraHeaders(httpReq, cfg)
	return httpReq, nil
}

func (a *openAICompat) ParseChatResponse(body []byte) (*ChatResponse, error) {
	var raw struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := DecodeJSON(body, &raw); err != nil {
		return nil, err
	}
	if len(raw.Choices) == 0 {
		return nil, &ProviderError{
			Category: ErrCategoryHTTP,
			Message:  "模型返回结果为空",
			Raw:      NormalizeBodySnippet(body),
		}
	}

	calls := make([]ToolCall, 0, len(raw.Choices[0].Message.ToolCalls))
	for _, item := range raw.Choices[0].Message.ToolCalls {
		name := strings.TrimSpace(item.Function.Name)
		if name == "" {
			continue
		}
		calls = append(calls, ToolCall{
			ID:        strings.TrimSpace(item.ID),
			Name:      name,
			Arguments: strings.TrimSpace(item.Function.Arguments),
		})
	}

	return &ChatResponse{
		Content:      raw.Choices[0].Message.Content,
		Model:        raw.Model,
		FinishReason: raw.Choices[0].FinishReason,
		Usage:        raw.Usage,
		ToolCalls:    calls,
	}, nil
}

func (a *openAICompat) ParseStreamChunk(line []byte) (*StreamChunk, bool, error) {
	return parseSSEDataLine(line, func(payload []byte) (*StreamChunk, error) {
		var raw struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			// 仅在服务商主动回传时才有值（如带了 stream_options.include_usage 的 OpenAI/DeepSeek）
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, nil // 忽略无法解析的心跳片段，不打断流
		}
		chunk := &StreamChunk{Model: raw.Model, Usage: raw.Usage}
		if len(raw.Choices) > 0 {
			chunk.Delta = raw.Choices[0].Delta.Content
			chunk.FinishReason = raw.Choices[0].FinishReason
		}
		return chunk, nil
	})
}

func (a *openAICompat) ListModels(ctx context.Context, cfg ProviderConfig) ([]ModelInfo, error) {
	endpoint := cfg.PathModels
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/models"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	ApplyAuth(req, cfg)
	applyExtraHeaders(req, cfg)

	client := NewHTTPClient(cfg.TimeoutSeconds)
	resp, err := client.Do(req)
	if err != nil {
		return nil, ClassifyTransportError(err)
	}
	body, err := ReadBody(resp)
	if err != nil {
		return nil, &ProviderError{Category: ErrCategoryHTTP, Message: "读取响应失败", Raw: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.MapError(resp.StatusCode, body)
	}

	var raw struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := DecodeJSON(body, &raw); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(raw.Data))
	for _, item := range raw.Data {
		name := strings.TrimSpace(item.ID)
		if name == "" {
			continue
		}
		models = append(models, ModelInfo{Name: name})
	}
	return models, nil
}

// Validate L2 连通性 + L3 能力探测
func (a *openAICompat) Validate(ctx context.Context, cfg ProviderConfig) (*ValidateResult, error) {
	start := time.Now()
	result := &ValidateResult{Stage: "L2"}

	// ---- L2：优先模型列表接口，失败则退化为最小 chat 探测 ----
	models, listErr := a.ListModels(ctx, cfg)
	if listErr != nil {
		var pErr *ProviderError
		if !asProviderError(listErr, &pErr) {
			return nil, listErr
		}
		// 鉴权/配额类错误直接判定失败，不做 chat 探测
		if pErr.Category == ErrCategoryAuth || pErr.Category == ErrCategoryQuota ||
			pErr.Category == ErrCategoryDNS || pErr.Category == ErrCategoryTCP || pErr.Category == ErrCategoryTLS {
			return failureResult(result, pErr, time.Since(start)), nil
		}
		// 404/405/400：该服务不提供 /models，用最小 chat 探测替代
		probe := ChatRequest{
			Model:     probeModel(cfg, nil),
			Messages:  []ChatMessage{{Role: "user", Content: "ping"}},
			MaxTokens: 1,
		}
		_, chatErr := a.doChat(ctx, cfg, probe)
		if chatErr != nil {
			var chatPErr *ProviderError
			if asProviderError(chatErr, &chatPErr) {
				chatPErr.Hint = strings.TrimSpace(chatPErr.Hint + " （该服务未提供模型列表接口，已改用对话探测）")
				return failureResult(result, chatPErr, time.Since(start)), nil
			}
			return nil, chatErr
		}
		result.ModelCount = 0
	} else {
		result.ModelCount = len(models)
		result.Models = models
	}

	// ---- L3：能力探测（流式 / 工具调用 / JSON mode）----
	result.Stage = "L3"
	caps := []string{"chat"}
	if a.probeStream(ctx, cfg, models) {
		caps = append(caps, "stream")
	}
	if a.probeToolCall(ctx, cfg, models) {
		caps = append(caps, "tool_call")
	}
	if a.probeJSONMode(ctx, cfg, models) {
		caps = append(caps, "json_mode")
	}

	result.OK = true
	result.Category = ""
	result.Message = "连接成功，凭证有效"
	result.LatencyMs = int(time.Since(start).Milliseconds())
	result.Capabilities = caps
	return result, nil
}

// doChat 发起一次非流式对话
func (a *openAICompat) doChat(ctx context.Context, cfg ProviderConfig, req ChatRequest) (*ChatResponse, error) {
	httpReq, err := a.BuildChatRequest(cfg, req)
	if err != nil {
		return nil, err
	}
	httpReq = httpReq.WithContext(ctx)
	client := NewHTTPClient(cfg.TimeoutSeconds)
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ClassifyTransportError(err)
	}
	body, err := ReadBody(resp)
	if err != nil {
		return nil, &ProviderError{Category: ErrCategoryHTTP, Message: "读取响应失败", Raw: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.MapError(resp.StatusCode, body)
	}
	parsed, err := a.ParseChatResponse(body)
	if err != nil {
		return nil, err
	}
	parsed.LatencyMs = int(time.Since(start).Milliseconds())
	return parsed, nil
}

func (a *openAICompat) probeStream(ctx context.Context, cfg ProviderConfig, models []ModelInfo) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := a.BuildChatRequest(cfg, ChatRequest{
		Model:     probeModel(cfg, models),
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 1,
		Stream:    true,
	})
	if err != nil {
		return false
	}
	req = req.WithContext(probeCtx)
	client := NewHTTPClient(cfg.TimeoutSeconds)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	// 能拿到 SSE 流（哪怕是首个分片）即认为支持流式
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return n > 0
}

func (a *openAICompat) probeToolCall(ctx context.Context, cfg ProviderConfig, models []ModelInfo) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	endpoint := cfg.PathChat
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/chat/completions"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return false
	}
	payload := map[string]any{
		"model":     probeModel(cfg, models),
		"messages":  []map[string]any{{"role": "user", "content": "现在几点？"}},
		"max_tokens": 16,
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_current_time",
				"description": "获取当前时间",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, full, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	ApplyAuth(req, cfg)
	applyExtraHeaders(req, cfg)

	client := NewHTTPClient(cfg.TimeoutSeconds)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	raw, err := ReadBody(resp)
	if err != nil {
		return false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	return len(parsed.Choices) > 0 && len(parsed.Choices[0].Message.ToolCalls) > 0
}

func (a *openAICompat) probeJSONMode(ctx context.Context, cfg ProviderConfig, models []ModelInfo) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := a.BuildChatRequest(cfg, ChatRequest{
		Model:      probeModel(cfg, models),
		Messages:   []ChatMessage{{Role: "user", Content: `返回 {"ok":true}`}},
		MaxTokens:  16,
		JSONMode:   true,
	})
	if err != nil {
		return false
	}
	req = req.WithContext(probeCtx)
	client := NewHTTPClient(cfg.TimeoutSeconds)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	body, err := ReadBody(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	parsed, err := a.ParseChatResponse(body)
	if err != nil {
		return false
	}
	return strings.Contains(parsed.Content, "{")
}

func (a *openAICompat) MapError(status int, body []byte) *ProviderError {
	snippet := NormalizeBodySnippet(body)
	lower := strings.ToLower(snippet)

	pErr := &ProviderError{HTTPStatus: status, Raw: snippet}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		pErr.Category = ErrCategoryAuth
		pErr.Message = "认证失败：API Key 无效或无权限"
		pErr.Hint = "确认 Key 未被禁用、额度未耗尽，且环境匹配（测试/生产 Key 不通用）"
	case http.StatusTooManyRequests:
		pErr.Category = ErrCategoryRateLimit
		pErr.Message = "触发厂商限流（429）"
		pErr.Hint = "降低并发或提升配额；该配置仍可保存，健康检查会持续标记状态"
	case http.StatusNotFound:
		pErr.Category = ErrCategoryModelNotFound
		pErr.Message = "接口或模型不存在（404）"
		pErr.Hint = "检查端点路径与模型名是否正确，模型是否已在厂商侧开通"
	case http.StatusBadRequest:
		pErr.Category = ErrCategoryBadRequest
		pErr.Message = "请求被拒绝（400）"
		pErr.Hint = "常见原因：模型名不存在、上下文超限、参数不被支持"
	default:
		pErr.Category = ErrCategoryHTTP
		pErr.Message = fmt.Sprintf("服务返回异常状态码 %d", status)
		pErr.Hint = "请结合原始错误信息排查"
	}

	if strings.Contains(lower, "insufficient") || strings.Contains(lower, "quota") ||
		strings.Contains(lower, "balance") || strings.Contains(lower, "欠费") || strings.Contains(lower, "余额") {
		pErr.Category = ErrCategoryQuota
		pErr.Message = "账户额度或余额不足"
		pErr.Hint = "充值或更换 Key；健康检查会持续标记为降级"
	}
	return pErr
}

// ------------------------------------------------------------------
// 共享小工具
// ------------------------------------------------------------------

// buildOpenAIChatPayload 组装 OpenAI 协议的请求体。
// includeModel=false 用于 Azure（模型由 URL 中的 deployment 决定，body 不接受 model 字段）。
// req.System 非空时转换为首条 system 消息（OpenAI 协议没有独立的 system 参数）。
// req.Tools 非空时下发工具清单 —— 这是模型「能调用平台能力」的唯一入口。
func buildOpenAIChatPayload(req ChatRequest, includeModel bool) ([]byte, error) {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, msg := range req.Messages {
		messages = append(messages, openAIMessage(msg))
	}

	payload := map[string]any{
		"messages": messages,
	}
	if includeModel {
		payload["model"] = req.Model
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if req.Stream {
		payload["stream"] = true
	}
	if req.JSONMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildOpenAITools(req.Tools)
		choice := strings.TrimSpace(req.ToolChoice)
		if choice == "" {
			choice = "auto"
		}
		payload["tool_choice"] = choice
	}
	return json.Marshal(payload)
}

// openAIMessage 把平台统一消息翻译成 OpenAI 协议消息。
// assistant 的 ToolCalls 需要转成 {id,type,function:{name,arguments}} 结构；
// role=tool 需要带上 tool_call_id。
func openAIMessage(msg ChatMessage) map[string]any {
	item := map[string]any{"role": msg.Role, "content": msg.Content}
	if len(msg.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   call.ID,
				"type": "function",
				"function": map[string]any{
					"name":      call.Name,
					"arguments": call.Arguments,
				},
			})
		}
		item["tool_calls"] = calls
	}
	if msg.Role == "tool" && msg.ToolCallID != "" {
		item["tool_call_id"] = msg.ToolCallID
	}
	return item
}

// buildOpenAITools 平台工具定义 → OpenAI tools 数组
func buildOpenAITools(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		schema := tool.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  schema,
			},
		})
	}
	return out
}

// probeModel 挑选探测用模型：优先 ExtraConfig.model，其次模型清单首个，最后兜底
func probeModel(cfg ProviderConfig, models []ModelInfo) string {
	if v := ExtraString(cfg.Extra, "model"); v != "" {
		return v
	}
	if len(models) > 0 && strings.TrimSpace(models[0].Name) != "" {
		return models[0].Name
	}
	if v := ExtraString(cfg.Extra, "deployment"); v != "" {
		return v
	}
	return "gpt-3.5-turbo"
}

// parseSSEDataLine 统一的 SSE 解析骨架
func parseSSEDataLine(line []byte, decode func([]byte) (*StreamChunk, error)) (*StreamChunk, bool, error) {
	text := strings.TrimSpace(string(line))
	if text == "" || strings.HasPrefix(text, ":") {
		return nil, false, nil
	}
	if !strings.HasPrefix(text, "data:") {
		return nil, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(text, "data:"))
	if payload == "[DONE]" {
		return &StreamChunk{Done: true}, true, nil
	}
	chunk, err := decode([]byte(payload))
	if err != nil {
		return nil, false, err
	}
	return chunk, false, nil
}

// applyExtraHeaders 应用 ExtraConfig 中的自定义请求头
func applyExtraHeaders(req *http.Request, cfg ProviderConfig) {
	raw := ExtraString(cfg.Extra, "extra_headers")
	if raw == "" {
		return
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		req.Header.Set(key, value)
	}
}

func asProviderError(err error, target **ProviderError) bool {
	if err == nil {
		return false
	}
	if pErr, ok := err.(*ProviderError); ok {
		*target = pErr
		return true
	}
	return false
}

func failureResult(result *ValidateResult, pErr *ProviderError, elapsed time.Duration) *ValidateResult {
	result.OK = false
	result.Category = pErr.Category
	result.HTTPStatus = pErr.HTTPStatus
	result.Message = pErr.Message
	result.Raw = pErr.Raw
	result.Hint = pErr.Hint
	result.LatencyMs = int(elapsed.Milliseconds())
	return result
}
