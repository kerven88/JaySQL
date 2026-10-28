package adapters

import (
	"encoding/json"
	"strings"
	"testing"
)

// 验证 req.System 在 OpenAI 协议下转换为首条 system 消息
// （anthropic 适配器原生支持 System 字段；该行为只针对 OpenAI 系）
func TestBuildOpenAIChatPayloadInjectsSystem(t *testing.T) {
	payload, err := buildOpenAIChatPayload(ChatRequest{
		Model:    "deepseek-chat",
		System:   "你是平台助手",
		Messages: []ChatMessage{{Role: "user", Content: "你是什么模型"}},
	}, true)
	if err != nil {
		t.Fatalf("构造 payload 失败: %v", err)
	}
	var raw struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("解析 payload 失败: %v", err)
	}
	if raw.Model != "deepseek-chat" {
		t.Fatalf("model 应为 deepseek-chat，实际 %s", raw.Model)
	}
	if len(raw.Messages) != 2 {
		t.Fatalf("应注入 system 后共 2 条消息，实际 %d", len(raw.Messages))
	}
	if raw.Messages[0].Role != "system" || !strings.Contains(raw.Messages[0].Content, "平台助手") {
		t.Fatalf("首条消息应为 system 提示词，实际 %+v", raw.Messages[0])
	}
	if raw.Messages[1].Role != "user" {
		t.Fatalf("第二条消息应为 user，实际 %+v", raw.Messages[1])
	}
}

// System 为空时不得注入空 system 消息（探测请求不受影响）
func TestBuildOpenAIChatPayloadNoSystem(t *testing.T) {
	payload, err := buildOpenAIChatPayload(ChatRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, true)
	if err != nil {
		t.Fatalf("构造 payload 失败: %v", err)
	}
	if strings.Contains(string(payload), `"system"`) {
		t.Fatalf("System 为空时不应出现 system 角色: %s", string(payload))
	}
}
