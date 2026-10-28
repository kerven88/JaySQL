package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// anthropic：Claude Messages API
//
// 与 OpenAI 协议的差异（必须独立适配器）：
//   1. 路径 /v1/messages
//   2. 鉴权走 x-api-key 头 + anthropic-version 头
//   3. system 与 messages 分离，不作为 message 传入
//   4. max_tokens 必填
//   5. 响应结构为 content 数组；流式事件名与 OpenAI 不同
// ============================================================

const defaultAnthropicVersion = "2023-06-01"

func init() {
	Register(&anthropicAdapter{})
}

type anthropicAdapter struct{}

func (a *anthropicAdapter) Name() string { return AdapterAnthropic }

func (a *anthropicAdapter) DefaultBaseURL() string { return "https://api.anthropic.com" }

func (a *anthropicAdapter) anthropicVersion(cfg ProviderConfig) string {
	if v := ExtraString(cfg.Extra, "anthropic_version"); v != "" {
		return v
	}
	return defaultAnthropicVersion
}

func (a *anthropicAdapter) BuildChatRequest(cfg ProviderConfig, req ChatRequest) (*http.Request, error) {
	endpoint := cfg.PathChat
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/v1/messages"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return nil, err
	}

	systemPrompt := strings.TrimSpace(req.System)
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		// Anthropic 不接受 system 角色出现在 messages 中
		if strings.EqualFold(msg.Role, "system") {
			if systemPrompt == "" {
				systemPrompt = strings.TrimSpace(msg.Content)
			}
			continue
		}
		appendAnthropicMessage(&messages, msg)
	}
	if len(messages) == 0 {
		return nil, &ProviderError{Category: ErrCategoryConfig, Message: "消息列表为空"}
	}

	// max_tokens 在 Anthropic 为必填项
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	payload := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	if systemPrompt != "" {
		payload["system"] = systemPrompt
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.Stream {
		payload["stream"] = true
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			schema := tool.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": schema,
			})
		}
		payload["tools"] = tools
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, full, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", a.anthropicVersion(cfg))
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	if strings.TrimSpace(cfg.AuthMode) == "" {
		cfg.AuthHeaderName = "x-api-key"
	}
	ApplyAuth(httpReq, cfg)
	applyExtraHeaders(httpReq, cfg)
	return httpReq, nil
}

func (a *anthropicAdapter) ParseChatResponse(body []byte) (*ChatResponse, error) {
	var raw struct {
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := DecodeJSON(body, &raw); err != nil {
		return nil, err
	}

	var builder strings.Builder
	calls := make([]ToolCall, 0)
	for _, item := range raw.Content {
		switch item.Type {
		case "text":
			builder.WriteString(item.Text)
		case "tool_use":
			name := strings.TrimSpace(item.Name)
			if name == "" {
				continue
			}
			args := "{}"
			if len(item.Input) > 0 {
				args = string(item.Input)
			}
			calls = append(calls, ToolCall{
				ID:        strings.TrimSpace(item.ID),
				Name:      name,
				Arguments: args,
			})
		}
	}
	return &ChatResponse{
		Content:      builder.String(),
		Model:        raw.Model,
		FinishReason: raw.StopReason,
		ToolCalls:    calls,
		Usage: Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.OutputTokens,
		},
	}, nil
}

// appendAnthropicMessage 把平台统一消息翻译成 Anthropic 消息。
// 与 OpenAI 的差异：
//   - assistant 的工具调用是 content block（tool_use），不是顶层 tool_calls
//   - 工具结果是 user 消息里的 tool_result block，且可多条合并进同一条 user 消息
func appendAnthropicMessage(messages *[]map[string]any, msg ChatMessage) {
	switch msg.Role {
	case "assistant":
		if len(msg.ToolCalls) > 0 {
			blocks := make([]map[string]any, 0, len(msg.ToolCalls)+1)
			if strings.TrimSpace(msg.Content) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    call.ID,
					"name":  call.Name,
					"input": parseToolArguments(call.Arguments),
				})
			}
			*messages = append(*messages, map[string]any{"role": "assistant", "content": blocks})
			return
		}
	case "tool":
		block := map[string]any{
			"type":        "tool_result",
			"tool_use_id": msg.ToolCallID,
			"content":     msg.Content,
		}
		if n := len(*messages); n > 0 && (*messages)[n-1]["role"] == "user" {
			if blocks, ok := (*messages)[n-1]["content"].([]map[string]any); ok {
				(*messages)[n-1]["content"] = append(blocks, block)
				return
			}
		}
		*messages = append(*messages, map[string]any{
			"role":    "user",
			"content": []map[string]any{block},
		})
		return
	}
	*messages = append(*messages, map[string]any{"role": msg.Role, "content": msg.Content})
}

// parseToolArguments 模型输出的工具参数是 JSON 字符串；Anthropic 的 input 需要对象
func parseToolArguments(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func (a *anthropicAdapter) ParseStreamChunk(line []byte) (*StreamChunk, bool, error) {
	text := strings.TrimSpace(string(line))
	if text == "" || strings.HasPrefix(text, ":") || strings.HasPrefix(text, "event:") {
		return nil, false, nil
	}
	if !strings.HasPrefix(text, "data:") {
		return nil, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(text, "data:"))

	var raw struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
		Message struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, false, nil
	}

	if raw.Type == "error" {
		return nil, true, &ProviderError{
			Category: ErrCategoryHTTP,
			Message:  "模型返回流式错误",
			Raw:      strings.TrimSpace(raw.Error.Message),
		}
	}

	chunk := &StreamChunk{Delta: raw.Delta.Text}
	// message_start 带模型名与输入 token；message_delta 带输出 token
	if raw.Type == "message_start" {
		chunk.Model = raw.Message.Model
		chunk.Usage = &Usage{
			PromptTokens:     raw.Message.Usage.InputTokens,
			CompletionTokens: raw.Message.Usage.OutputTokens,
		}
	}
	if raw.Type == "message_delta" && raw.Usage.OutputTokens > 0 {
		chunk.Usage = &Usage{CompletionTokens: raw.Usage.OutputTokens}
	}
	if raw.Type == "message_stop" {
		chunk.Done = true
		return chunk, true, nil
	}
	return chunk, false, nil
}

func (a *anthropicAdapter) ListModels(ctx context.Context, cfg ProviderConfig) ([]ModelInfo, error) {
	endpoint := cfg.PathModels
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/v1/models"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("anthropic-version", a.anthropicVersion(cfg))
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
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
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

func (a *anthropicAdapter) Validate(ctx context.Context, cfg ProviderConfig) (*ValidateResult, error) {
	start := time.Now()
	result := &ValidateResult{Stage: "L2"}

	models, err := a.ListModels(ctx, cfg)
	if err != nil {
		var pErr *ProviderError
		if !asProviderError(err, &pErr) {
			return nil, err
		}
		if pErr.Category != ErrCategoryModelNotFound && pErr.Category != ErrCategoryBadRequest {
			return failureResult(result, pErr, time.Since(start)), nil
		}
		// 无模型列表接口 → 用最小对话探测
		models = []ModelInfo{{Name: ExtraString(cfg.Extra, "model")}}
	}

	probe := ChatRequest{
		Model:     probeModel(cfg, models),
		Messages:  []ChatMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	}
	if _, err := a.doChat(ctx, cfg, probe); err != nil {
		var pErr *ProviderError
		if asProviderError(err, &pErr) {
			return failureResult(result, pErr, time.Since(start)), nil
		}
		return nil, err
	}

	result.Stage = "L3"
	result.OK = true
	result.Message = "连接成功，凭证有效"
	result.LatencyMs = int(time.Since(start).Milliseconds())
	// Anthropic 全系支持流式；tool_call 通过增加可用性评估给出
	result.Capabilities = []string{"chat", "stream", "tool_call"}
	result.Models = models
	result.ModelCount = len(models)
	return result, nil
}

func (a *anthropicAdapter) MapError(status int, body []byte) *ProviderError {
	snippet := NormalizeBodySnippet(body)
	var raw struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err == nil && raw.Error.Message != "" {
		snippet = strings.TrimSpace(raw.Error.Type + " " + raw.Error.Message)
	}

	pErr := &ProviderError{HTTPStatus: status, Raw: snippet}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		pErr.Category = ErrCategoryAuth
		pErr.Message = "认证失败：API Key 无效或无权限"
		pErr.Hint = "确认 Key 有效且未被禁用"
	case http.StatusTooManyRequests:
		pErr.Category = ErrCategoryRateLimit
		pErr.Message = "触发厂商限流（429）"
		pErr.Hint = "降低并发或提升配额"
	case http.StatusNotFound:
		pErr.Category = ErrCategoryModelNotFound
		pErr.Message = "模型或接口不存在（404）"
		pErr.Hint = "检查模型名是否正确"
	case http.StatusBadRequest:
		pErr.Category = ErrCategoryBadRequest
		pErr.Message = "请求被拒绝（400）"
		pErr.Hint = "常见原因：缺少 max_tokens、上下文超限、模型名错误"
	default:
		pErr.Category = ErrCategoryHTTP
		pErr.Message = "服务返回异常状态码"
		pErr.Hint = "请结合原始错误信息排查"
	}
	if strings.Contains(strings.ToLower(snippet), "credit") || strings.Contains(strings.ToLower(snippet), "quota") {
		pErr.Category = ErrCategoryQuota
		pErr.Message = "账户额度或余额不足"
	}
	return pErr
}

// doChat 发起一次非流式对话
func (a *anthropicAdapter) doChat(ctx context.Context, cfg ProviderConfig, req ChatRequest) (*ChatResponse, error) {
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
