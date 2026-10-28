package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/ai/adapters"
)

// TestDecideChatRoutePlatformSignals 平台信号必须命中工具路由
func TestDecideChatRoutePlatformSignals(t *testing.T) {
	cases := []struct {
		name string
		text string
		want chatRoute
	}{
		{"实例清单", "平台上有哪些实例", routeTools},
		{"我的实例", "我的实例都正常吗", routeTools},
		{"IP 端口", "看看 192.168.1.99:3306 这个库有什么表", routeTools},
		{"工单创建", "帮我创建一个工单，在 test001 库建表", routeTools},
		{"工单列表", "待我审批的工单有几个", routeTools},
		{"备份结果", "昨天备份成功了吗", routeTools},
		{"备份集", "最近的备份集有哪些", routeTools},
		{"告警", "当前有哪些告警", routeTools},
		{"慢查询", "平台里的慢查询 TOP10", routeTools},
		{"表结构", "把这张表的结构发我", routeTools},
		{"死锁排查", "现在有死锁吗", routeTools},
		{"建库", "建一个 test001 库", routeTools},
		{"上下文+时间", "昨天的备份结果如何", routeTools},
		{"监控指标", "监控指标采集周期是多少", routeTools},
		{"变更记录", "平台有哪些变更记录", routeTools},
		{"慢日志", "看看慢日志", routeTools},
		{"带指向的通识疑问", "我的 MySQL 怎么调优", routeTools},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideChatRoute([]adapters.ChatMessage{{Role: "user", Content: tc.text}})
			if got != tc.want {
				t.Fatalf("text=%q 期望 %s，实际 %s", tc.text, tc.want, got)
			}
		})
	}
}

// 通识/平台各一组的正向断言（与上面的表合并成一个入口，便于统一查看）
func TestDecideChatRouteGeneralQuestions(t *testing.T) {
	general := []string{
		"介绍 mysqldump 的实现原理",
		"什么是慢查询",
		"什么是备份集",
		"MySQL 主从复制的实现机制是怎样的",
		"redo log 和 undo log 的区别",
		"MySQL 备份原理讲一下",
		"你是什么模型",
		"SQL 优化有哪些常见手段",
		"PostgreSQL 的 MVCC 是怎么工作的",
		"DDL 是什么意思",
		"数据库变更流程的最佳实践",
	}
	for _, text := range general {
		if got := decideChatRoute([]adapters.ChatMessage{{Role: "user", Content: text}}); got != routeDirect {
			t.Fatalf("通识问题 %q 期望直答，实际 %s", text, got)
		}
	}
}

// 只按最后一条用户消息判定：前面聊平台数据，最后问通识 → 直答
func TestDecideChatRouteUsesLastUserMessage(t *testing.T) {
	history := []adapters.ChatMessage{
		{Role: "user", Content: "平台上有哪些实例"},
		{Role: "assistant", Content: "共 3 个实例……"},
		{Role: "user", Content: "介绍 mysqldump 的实现原理"},
	}
	if got := decideChatRoute(history); got != routeDirect {
		t.Fatalf("应以最后一条用户消息判定，期望直答，实际 %s", got)
	}

	// 反向：前面问通识，最后问平台数据 → 挂工具
	history = append(history[:0], []adapters.ChatMessage{
		{Role: "user", Content: "介绍 mysqldump 的实现原理"},
		{Role: "assistant", Content: "mysqldump 会……"},
		{Role: "user", Content: "昨天备份成功了吗"},
	}...)
	if got := decideChatRoute(history); got != routeTools {
		t.Fatalf("最后一条是平台问题，期望工具路由，实际 %s", got)
	}
}

// 直答请求不得携带任何工具定义（纯转发）
func TestBuildChatRequestDirectHasNoTools(t *testing.T) {
	req := buildChatRequest(routeDirect, "m", []adapters.ChatMessage{{Role: "user", Content: "hi"}})
	if len(req.Tools) != 0 {
		t.Fatalf("直答路径不应下发工具，实际 %d 个", len(req.Tools))
	}
	if strings.TrimSpace(req.ToolChoice) != "" {
		t.Fatalf("直答路径不应设置 tool_choice，实际 %q", req.ToolChoice)
	}
	if req.System != chatSystemPromptDirect {
		t.Fatalf("直答路径应使用 direct 提示词")
	}
	if strings.Contains(req.System, "平台已为你提供一组工具") {
		t.Fatalf("直答提示词里不应出现工具能力描述")
	}
}

// 工具路由必须带全部工具定义
func TestBuildChatRequestToolsHasDefinitions(t *testing.T) {
	req := buildChatRequest(routeTools, "m", []adapters.ChatMessage{{Role: "user", Content: "hi"}})
	if len(req.Tools) == 0 {
		t.Fatalf("工具路由必须下发工具清单")
	}
	if req.ToolChoice != "auto" {
		t.Fatalf("tool_choice 应为 auto，实际 %q", req.ToolChoice)
	}
	if req.System != chatSystemPromptWithTools {
		t.Fatalf("工具路由应使用 with-tools 提示词")
	}
}

// 直答请求经适配器组装后的真实 payload 里不得出现 tools 字段
func TestDirectRoutePayloadOmitsToolsField(t *testing.T) {
	adapter, err := adapters.Resolve(adapters.AdapterOpenAICompat)
	if err != nil {
		t.Fatalf("取适配器失败: %v", err)
	}
	cfg := adapters.ProviderConfig{BaseURL: "https://example.com/v1", APIKey: "k"}
	httpReq, err := adapter.BuildChatRequest(cfg,
		buildChatRequest(routeDirect, "m", []adapters.ChatMessage{{Role: "user", Content: "介绍 mysqldump 原理"}}))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	body, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("读取请求体失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("解析请求体失败: %v", err)
	}
	if _, ok := payload["tools"]; ok {
		t.Fatalf("直答 payload 不应包含 tools 字段: %s", string(body))
	}
	if _, ok := payload["tool_choice"]; ok {
		t.Fatalf("直答 payload 不应包含 tool_choice 字段: %s", string(body))
	}
}

// 直答路径：模型即便返回 tool_calls 也不执行，内容原样返回（系统只做转发）
func TestRunDirectChatIgnoresToolCalls(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "fake-model",
			"choices": [{
				"message": {
					"content": "mysqldump 的原理是先 FLUSH TABLES WITH READ LOCK……",
					"tool_calls": [{"id":"call_1","type":"function","function":{"name":"list_instances","arguments":"{}"}}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 12, "completion_tokens": 34, "total_tokens": 46}
		}`))
	}))
	defer server.Close()

	adapter, err := adapters.Resolve(adapters.AdapterOpenAICompat)
	if err != nil {
		t.Fatalf("取适配器失败: %v", err)
	}
	cfg := adapters.ProviderConfig{BaseURL: server.URL, APIKey: "k"}

	scenes := make([]string, 0, 1)
	result, pErr := runDirectChat(context.Background(), adapter, cfg, "fake-model",
		[]adapters.ChatMessage{{Role: "user", Content: "介绍 mysqldump 的实现原理"}},
		func(scene string, _ *adapters.ChatResponse, _ *adapters.ProviderError, _ int) {
			scenes = append(scenes, scene)
		})
	if pErr != nil {
		t.Fatalf("直答调用失败: %v", pErr)
	}
	if result.Route != routeDirect {
		t.Fatalf("路由应为 direct，实际 %s", result.Route)
	}
	if !strings.Contains(result.Content, "mysqldump 的原理") {
		t.Fatalf("应原样返回模型内容，实际 %q", result.Content)
	}
	if len(result.Traces) != 0 || result.Pending != nil {
		t.Fatalf("直答路径不得产生工具轨迹或草稿: traces=%d pending=%v", len(result.Traces), result.Pending)
	}
	if result.Usage.TotalTokens != 46 {
		t.Fatalf("token 统计应透传，实际 %d", result.Usage.TotalTokens)
	}
	if len(scenes) != 1 || scenes[0] != "chat.direct" {
		t.Fatalf("调用日志 scene 应为 chat.direct，实际 %v", scenes)
	}
	if strings.Contains(gotBody, `"tools"`) {
		t.Fatalf("直答请求体不应含 tools: %s", gotBody)
	}
}
