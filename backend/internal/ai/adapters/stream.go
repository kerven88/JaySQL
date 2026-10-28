package adapters

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// 通用流式对话
//
// 为什么放在适配器层：SSE 分片的解析已经由各适配器的 ParseStreamChunk 负责
// （协议差异在适配器里消化完），这一层只做「发请求 → 逐行读 → 回调增量」，
// 与厂商无关，因此任何适配器都能直接复用。
//
// 两个关键点：
//   - 客户端不能设总超时（http.Client.Timeout 会把长回答掐断），
//     改为 ResponseHeaderTimeout + 调用方传入的 ctx 控制生命周期；
//   - 读行用大缓冲，避免单行超长 JSON（含代码块）被截断。
// ============================================================

// 单行缓冲上限：SSE 单行最长约几十 KB，给 256KB 足够且不浪费
const streamLineBufferBytes = 256 * 1024

// NewStreamHTTPClient 流式专用 HTTP 客户端：无总超时，只限制「响应头等待」
func NewStreamHTTPClient(timeoutSeconds int) *http.Client {
	headerTimeout := timeoutSeconds
	if headerTimeout <= 0 {
		headerTimeout = DefaultTimeoutSeconds
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: time.Duration(headerTimeout) * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// StreamChat 发起流式对话，逐分片回调增量文本，返回汇总响应（内容/模型/用量/结束原因）。
// req.Stream 由本函数强制打开，调用方无需自己设置。
// onDelta 返回错误（如前端连接已断）时立即中止并向上抛出。
func StreamChat(
	ctx context.Context,
	adapter ProviderAdapter,
	cfg ProviderConfig,
	req ChatRequest,
	onDelta func(delta string) error,
) (*ChatResponse, *ProviderError) {

	req.Stream = true
	httpReq, err := adapter.BuildChatRequest(cfg, req)
	if err != nil {
		var pErr *ProviderError
		if errors.As(err, &pErr) {
			return nil, pErr
		}
		return nil, &ProviderError{Category: ErrCategoryConfig, Message: err.Error()}
	}
	httpReq = httpReq.WithContext(ctx)

	client := NewStreamHTTPClient(cfg.TimeoutSeconds)
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ClassifyTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ReadBody(resp)
		return nil, adapter.MapError(resp.StatusCode, body)
	}
	if strings.TrimSpace(resp.Header.Get("Content-Type")) == "" {
		// 少数网关不声明 Content-Type，仍按 SSE 读，不阻断
	}

	out := &ChatResponse{}
	var builder strings.Builder
	reader := bufio.NewReaderSize(resp.Body, streamLineBufferBytes)

	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			chunk, done, parseErr := adapter.ParseStreamChunk(line)
			if parseErr != nil {
				var pErr *ProviderError
				if errors.As(parseErr, &pErr) {
					out.Content = builder.String()
					out.LatencyMs = int(time.Since(start).Milliseconds())
					return out, pErr
				}
				return nil, &ProviderError{Category: ErrCategoryHTTP, Message: "解析流式响应失败", Raw: parseErr.Error()}
			}
			if chunk != nil {
				if chunk.Delta != "" {
					builder.WriteString(chunk.Delta)
					if onDelta != nil {
						if err := onDelta(chunk.Delta); err != nil {
							out.Content = builder.String()
							out.LatencyMs = int(time.Since(start).Milliseconds())
							return out, &ProviderError{Category: ErrCategoryUnknown, Message: "客户端已断开，已停止生成", Raw: err.Error()}
						}
					}
				}
				if chunk.Model != "" {
					out.Model = chunk.Model
				}
				if chunk.Usage != nil {
					out.Usage = mergeStreamUsage(out.Usage, *chunk.Usage)
				}
				if chunk.FinishReason != "" {
					out.FinishReason = chunk.FinishReason
				}
				if done {
					break
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			// 网络中途断开：已生成的内容仍要返回，交给上层决定是否提示重试
			out.Content = builder.String()
			out.LatencyMs = int(time.Since(start).Milliseconds())
			return out, ClassifyTransportError(readErr)
		}
	}

	out.Content = builder.String()
	out.LatencyMs = int(time.Since(start).Milliseconds())
	return out, nil
}

// mergeStreamUsage 合并分片用量：厂商可能把输入/输出用量分散在不同分片里。
//
// 关键点：厂商若给出 total_tokens 就用它的值；若只给 prompt/completion（Anthropic 就是
// message_start 给 input、message_delta 给 output），则在本次确实更新了任一分量后重算总数 ——
// 只算一次不够：早期算出的 total 会被后续分片覆盖成过期值（10 + 20 变成 11）。
func mergeStreamUsage(base Usage, next Usage) Usage {
	updated := false
	if next.PromptTokens > 0 {
		base.PromptTokens = next.PromptTokens
		updated = true
	}
	if next.CompletionTokens > 0 {
		base.CompletionTokens = next.CompletionTokens
		updated = true
	}
	if next.TotalTokens > 0 {
		base.TotalTokens = next.TotalTokens
	} else if updated {
		base.TotalTokens = base.PromptTokens + base.CompletionTokens
	}
	return base
}
