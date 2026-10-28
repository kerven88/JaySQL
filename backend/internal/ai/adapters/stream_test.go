package adapters

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================================
// 流式对话（StreamChat）的回归
//
// 这一层是「厂商无关」的搬运工：发请求 → 逐行读 → 回调增量 → 收口汇总。
// 重点固定四件事：
//   1. 增量按序回调，且拼接结果与最终 content 一致（前端边收边渲染的依据）；
//   2. model / usage / finish_reason 能从句末分片透传（用量散片也要合并正确）；
//   3. [DONE] 与 EOF 都能正常收口；中途断开保留已生成内容并报错；
//   4. onDelta 报错（前端断开）立即中止，不继续读流。
// ============================================================================

// sseServer 起一个按行吐 SSE 的假服务端，并把收到的请求体回传给调用方
func sseServer(t *testing.T, lines []string) (*httptest.Server, *string) {
	t.Helper()
	gotBody := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range lines {
			_, _ = io.WriteString(w, line)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, &gotBody
}

func openAIDelta(content string) string {
	return `data: {"model":"deepseek-chat","choices":[{"delta":{"content":"` + content + `"}}]}` + "\n\n"
}

func mustOpenAICompat(t *testing.T) ProviderAdapter {
	t.Helper()
	adapter, err := Resolve(AdapterOpenAICompat)
	if err != nil {
		t.Fatalf("取 openai_compat 适配器失败: %v", err)
	}
	return adapter
}

// 增量按序回调 + 汇总结果正确 + 请求体确实开了 stream
func TestStreamChatForwardsDeltasAndAggregates(t *testing.T) {
	server, gotBody := sseServer(t, []string{
		openAIDelta("mysqldump "),
		openAIDelta("的原理是"),
		openAIDelta("先加全局读锁"),
		`data: {"model":"deepseek-chat","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		`data: {"model":"deepseek-chat","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33}}` + "\n\n",
		"data: [DONE]\n\n",
	})

	deltas := make([]string, 0, 4)
	resp, pErr := StreamChat(context.Background(), mustOpenAICompat(t),
		ProviderConfig{BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: "deepseek-chat", Messages: []ChatMessage{{Role: "user", Content: "介绍 mysqldump 原理"}}},
		func(delta string) error {
			deltas = append(deltas, delta)
			return nil
		})

	if pErr != nil {
		t.Fatalf("流式调用不应失败: %v", pErr)
	}
	if len(deltas) != 3 {
		t.Fatalf("应有 3 次增量回调，实际 %d 次: %v", len(deltas), deltas)
	}
	want := "mysqldump 的原理是先加全局读锁"
	if strings.Join(deltas, "") != want || resp.Content != want {
		t.Fatalf("增量拼接与最终内容应一致，want=%q deltas=%q content=%q", want, strings.Join(deltas, ""), resp.Content)
	}
	if resp.Model != "deepseek-chat" {
		t.Fatalf("模型名应透传，实际 %q", resp.Model)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("结束原因应透传，实际 %q", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 22 || resp.Usage.TotalTokens != 33 {
		t.Fatalf("用量应透传，实际 %+v", resp.Usage)
	}
	if !strings.Contains(*gotBody, `"stream":true`) {
		t.Fatalf("请求体必须开启流式: %s", *gotBody)
	}
}

// 用量散片（Anthropic 风格：input 与 output 分别在两片）合并后总数必须重算
func TestMergeStreamUsageRecomputesTotalAcrossChunks(t *testing.T) {
	var usage Usage
	usage = mergeStreamUsage(usage, Usage{PromptTokens: 10})
	if usage.TotalTokens != 10 {
		t.Fatalf("只给 prompt 时应推导总数，实际 %+v", usage)
	}
	usage = mergeStreamUsage(usage, Usage{CompletionTokens: 20})
	if usage.PromptTokens != 10 || usage.CompletionTokens != 20 || usage.TotalTokens != 30 {
		t.Fatalf("后续分片补齐 output 后总数应为 30，实际 %+v", usage)
	}

	// 厂商明确给出 total 时以厂商为准，且不会被后续「只更新分量」的分片带偏
	explicit := mergeStreamUsage(Usage{}, Usage{PromptTokens: 5, CompletionTokens: 6, TotalTokens: 99})
	if explicit.TotalTokens != 99 {
		t.Fatalf("厂商给的总数应被尊重，实际 %+v", explicit)
	}
}

// 没有 [DONE] 只靠 EOF 收口：内容照常返回，不算错误
func TestStreamChatEOFWithoutDoneKeepsContent(t *testing.T) {
	server, _ := sseServer(t, []string{
		openAIDelta("第一段"),
		openAIDelta("第二段"),
	})

	resp, pErr := StreamChat(context.Background(), mustOpenAICompat(t),
		ProviderConfig{BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, nil)

	if pErr != nil {
		t.Fatalf("EOF 属正常收口，不应报错: %v", pErr)
	}
	if resp.Content != "第一段第二段" {
		t.Fatalf("EOF 前的增量应保留，实际 %q", resp.Content)
	}
}

// onDelta 报错（前端已断开）→ 立即中止，返回已生成内容 + 客户端断开错误
func TestStreamChatStopsWhenOnDeltaFails(t *testing.T) {
	server, _ := sseServer(t, []string{
		openAIDelta("第一段"),
		openAIDelta("第二段"),
		openAIDelta("第三段"),
		"data: [DONE]\n\n",
	})

	calls := 0
	boom := errors.New("前端连接已断开")
	resp, pErr := StreamChat(context.Background(), mustOpenAICompat(t),
		ProviderConfig{BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}},
		func(string) error {
			calls++
			if calls == 2 {
				return boom
			}
			return nil
		})

	if pErr == nil {
		t.Fatal("回调失败应中止并报错")
	}
	if pErr.Category != ErrCategoryUnknown || !strings.Contains(pErr.Message, "客户端已断开") {
		t.Fatalf("应为客户端断开类错误，实际 category=%s message=%s", pErr.Category, pErr.Message)
	}
	if resp == nil || resp.Content != "第一段第二段" {
		t.Fatalf("应返回已生成内容，实际 %+v", resp)
	}
	if calls != 2 {
		t.Fatalf("报错后不应继续回调，实际回调 %d 次", calls)
	}
}

// 厂商返回非 2xx：交给 MapError 分类，不当作流式解析
func TestStreamChatMapsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	}))
	t.Cleanup(server.Close)

	_, pErr := StreamChat(context.Background(), mustOpenAICompat(t),
		ProviderConfig{BaseURL: server.URL, APIKey: "bad"},
		ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, nil)

	if pErr == nil {
		t.Fatal("401 应返回错误")
	}
	if pErr.Category != ErrCategoryAuth || pErr.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("应归类为认证失败，实际 category=%s status=%d", pErr.Category, pErr.HTTPStatus)
	}
}

// Anthropic 的流事件形态（message_start / content_block_delta / message_delta / message_stop）
func TestStreamChatAnthropicEvents(t *testing.T) {
	server, _ := sseServer(t, []string{
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"model":"claude-3-5-sonnet","usage":{"input_tokens":9,"output_tokens":1}}}` + "\n\n",
		"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"备份"}}` + "\n\n",
		"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"就是复制数据"}}` + "\n\n",
		"event: message_delta\n" +
			`data: {"type":"message_delta","usage":{"output_tokens":18}}` + "\n\n",
		"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	})

	adapter, err := Resolve(AdapterAnthropic)
	if err != nil {
		t.Fatalf("取 anthropic 适配器失败: %v", err)
	}

	resp, pErr := StreamChat(context.Background(), adapter,
		ProviderConfig{BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: "claude-3-5-sonnet", Messages: []ChatMessage{{Role: "user", Content: "什么是备份"}}}, nil)

	if pErr != nil {
		t.Fatalf("流式调用不应失败: %v", pErr)
	}
	if resp.Content != "备份就是复制数据" {
		t.Fatalf("内容拼接错误，实际 %q", resp.Content)
	}
	if resp.Model != "claude-3-5-sonnet" {
		t.Fatalf("message_start 的模型名应透传，实际 %q", resp.Model)
	}
	if resp.Usage.PromptTokens != 9 || resp.Usage.CompletionTokens != 18 || resp.Usage.TotalTokens != 27 {
		t.Fatalf("Anthropic 用量应合并正确，实际 %+v", resp.Usage)
	}
}

// 带 Request body 上下文取消：不应挂死（客户端断开场景的上游保护）
func TestStreamChatStopsWhenContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, openAIDelta("开始"))
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	_, pErr := StreamChat(ctx, mustOpenAICompat(t),
		ProviderConfig{BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}},
		func(string) error {
			cancel() // 收到第一个增量后模拟前端断开
			return nil
		})

	if pErr == nil {
		t.Fatal("上下文取消后应返回错误")
	}
	if pErr.Category != ErrCategoryTCP && pErr.Category != ErrCategoryUnknown {
		t.Fatalf("取消应归为传输类错误，实际 %s", pErr.Category)
	}
}
