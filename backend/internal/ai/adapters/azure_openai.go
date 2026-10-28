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
// azure_openai：Azure OpenAI Service
//
// 与 OpenAI 的差异：
//   1. URL 结构：/openai/deployments/{deployment}/chat/completions?api-version=xxx
//   2. 鉴权走 api-key 头（而非 Authorization: Bearer）
//   3. 请求体不接受 model 字段（模型由 deployment 决定）
//   4. 模型列表接口与 OpenAI 不同
// ============================================================

func init() {
	Register(&azureOpenAI{})
}

type azureOpenAI struct {
	openAICompat
}

func (a *azureOpenAI) Name() string { return AdapterAzureOpenAI }

func (a *azureOpenAI) DefaultBaseURL() string { return "https://{resource}.openai.azure.com" }

func (a *azureOpenAI) BuildChatRequest(cfg ProviderConfig, req ChatRequest) (*http.Request, error) {
	endpoint := cfg.PathChat
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/openai/deployments/{deployment}/chat/completions?api-version={api_version}"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return nil, err
	}

	payload, err := buildOpenAIChatPayload(req, false)
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
	// Azure 使用 api-key 头；若模板未指定则兜底
	if strings.TrimSpace(cfg.AuthHeaderName) == "" {
		cfg.AuthHeaderName = "api-key"
	}
	if strings.TrimSpace(cfg.AuthMode) == "" {
		cfg.AuthMode = "api_key_header"
	}
	ApplyAuth(httpReq, cfg)
	applyExtraHeaders(httpReq, cfg)
	return httpReq, nil
}

// ListModels Azure 的模型清单需要 data-plane 调用，多数场景不可用，
// 因此直接返回明确的 config 类错误，由上层 Validate 退化为对话探测。
func (a *azureOpenAI) ListModels(ctx context.Context, cfg ProviderConfig) ([]ModelInfo, error) {
	return nil, &ProviderError{
		Category: ErrCategoryBadRequest,
		Message:  "Azure OpenAI 不提供统一的模型列表接口",
		Hint:     "模型名请直接填写部署名（deployment）",
	}
}

// Validate Azure 直接用最小对话探测（模型名 = deployment）
func (a *azureOpenAI) Validate(ctx context.Context, cfg ProviderConfig) (*ValidateResult, error) {
	start := time.Now()
	result := &ValidateResult{Stage: "L2"}

	deployment := ExtraString(cfg.Extra, "deployment")
	if deployment == "" {
		return failureResult(result, &ProviderError{
			Category: ErrCategoryConfig,
			Message:  "缺少部署名（deployment）",
			Hint:     "Azure 的模型由部署名决定，请在差异化参数中填写",
		}, time.Since(start)), nil
	}
	if ExtraString(cfg.Extra, "api_version") == "" {
		return failureResult(result, &ProviderError{
			Category: ErrCategoryConfig,
			Message:  "缺少 API 版本（api_version）",
			Hint:     "例如 2024-06-01",
		}, time.Since(start)), nil
	}

	probe := ChatRequest{
		Model:     deployment,
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
	caps := []string{"chat"}
	streamProbe := ChatRequest{
		Model:     deployment,
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 1,
		Stream:    true,
	}
	if a.probeStreamWith(ctx, cfg, streamProbe) {
		caps = append(caps, "stream")
	}
	if a.probeToolCall(ctx, cfg, []ModelInfo{{Name: deployment}}) {
		caps = append(caps, "tool_call")
	}

	result.OK = true
	result.Message = "连接成功，凭证有效"
	result.LatencyMs = int(time.Since(start).Milliseconds())
	result.Capabilities = caps
	result.Models = []ModelInfo{{Name: deployment, Capabilities: caps}}
	result.ModelCount = 1
	return result, nil
}

// probeStreamWith 复用 openai_compat 的流式探测逻辑（按传入请求）
func (a *azureOpenAI) probeStreamWith(ctx context.Context, cfg ProviderConfig, req ChatRequest) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	httpReq, err := a.BuildChatRequest(cfg, req)
	if err != nil {
		return false
	}
	httpReq = httpReq.WithContext(probeCtx)
	client := NewHTTPClient(cfg.TimeoutSeconds)
	resp, err := client.Do(httpReq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return n > 0
}

// MapError Azure 的错误结构为 {"error":{"code":"...","message":"..."}}
func (a *azureOpenAI) MapError(status int, body []byte) *ProviderError {
	snippet := NormalizeBodySnippet(body)
	var raw struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err == nil && raw.Error.Message != "" {
		snippet = strings.TrimSpace(raw.Error.Code + " " + raw.Error.Message)
	}
	return a.openAICompat.MapError(status, []byte(snippet))
}
