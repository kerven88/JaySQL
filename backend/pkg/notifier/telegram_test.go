package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramRegisteredAndSecrets(t *testing.T) {
	c, ok := Get("telegram")
	if !ok {
		t.Fatal("Telegram channel missing")
	}
	if !c.Capabilities().SupportsMarkdown {
		t.Fatal("Markdown capability missing")
	}
	masked, _ := MaskChannelConfig("telegram", map[string]any{"bot_token": "123:secret", "chat_id": "-1001"})
	if masked["bot_token"] != MaskSentinel || masked["chat_id"] != "-1001" {
		t.Fatal(masked)
	}
}
func TestTelegramSendMarkdownAndBusinessError(t *testing.T) {
	allowLoopbackForTest(t)
	c, ok := Get("telegram")
	if !ok {
		t.Fatal("Telegram channel missing")
	}
	var payload map[string]any
	success := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:secret/sendMessage" {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if success {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		} else {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad 123:secret"}`))
		}
	}))
	defer server.Close()
	cfg := map[string]any{"bot_token": "123:secret", "chat_id": "-1001", "message_thread_id": 12, "api_url": server.URL}
	result, err := c.Send(context.Background(), cfg, Message{Markdown: "## CPU\n\n**db_01** & <script>alert(1)</script>\n\n[详情](https://example.com)", Title: "CPU"})
	if err != nil || result.Accepted != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if payload["parse_mode"] != "HTML" || payload["chat_id"] != "-1001" || payload["message_thread_id"] != float64(12) {
		t.Fatal(payload)
	}
	text := payload["text"].(string)
	if !strings.Contains(text, "<b>db_01</b>") || strings.Contains(text, "<script>") || !strings.Contains(text, `href="https://example.com"`) {
		t.Fatal(text)
	}
	success = false
	_, err = c.Send(context.Background(), cfg, Message{PlainText: "hello"})
	if err == nil || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "123:secret") {
		t.Fatalf("unsafe business error: %v", err)
	}
}
func TestTelegramValidationAndLength(t *testing.T) {
	c, ok := Get("telegram")
	if !ok {
		t.Fatal("Telegram channel missing")
	}
	for _, cfg := range []map[string]any{
		{}, {"bot_token": "123:secret"}, {"bot_token": "../bad", "chat_id": "-1001"},
		{"bot_token": "123:secret", "chat_id": "-1001", "message_thread_id": -1},
	} {
		if c.Validate(cfg) == nil {
			t.Fatalf("invalid config accepted: %v", cfg)
		}
	}
	_, err := c.Send(context.Background(), map[string]any{"bot_token": "123:secret", "chat_id": "-1001"}, Message{PlainText: strings.Repeat("😀", 2049)})
	if err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("length error missing: %v", err)
	}
}

func TestMessageMarkdownSafeSubset(t *testing.T) {
	rich, plain := RenderMessageMarkdown("# Hello &amp; world\n\n- **db_01**\n- `a_b`\n\n```sql\nSELECT '<unsafe>';\n```\n\n[bad](javascript:alert)\n\n<img src=x onerror=alert(1)>")
	for _, want := range []string{"<b>Hello &amp; world</b>", "<b>db_01</b>", "<code>a_b</code>", "<pre>"} {
		if !strings.Contains(rich, want) {
			t.Errorf("missing %q in %s", want, rich)
		}
	}
	if strings.Contains(rich, `href="javascript:`) || strings.Contains(rich, "<img ") {
		t.Fatal(rich)
	}
	if !strings.Contains(plain, "Hello & world") || !strings.Contains(plain, "SELECT '<unsafe>';") {
		t.Fatal(plain)
	}
}

func TestTelegramLengthCountsVisibleLinkText(t *testing.T) {
	allowLoopbackForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer server.Close()
	channel, _ := Get("telegram")
	_, err := channel.Send(context.Background(), map[string]any{"bot_token": "123:secret", "chat_id": "-1001", "api_url": server.URL}, Message{Markdown: strings.Repeat("a", 4080) + "[详情](https://example.com/long-detail-link)"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTelegramMarkdownDoesNotNestCodeEntities(t *testing.T) {
	for _, markdown := range []string{"**`db_01`**", "# `db_01`", "[`db_01`](https://example.com)"} {
		rich, plain := RenderMessageMarkdown(markdown)
		if strings.Contains(rich, "<code>") {
			t.Fatalf("Telegram code entity nested inside another entity: %s", rich)
		}
		if !strings.Contains(plain, "db_01") {
			t.Fatal(plain)
		}
	}
}
