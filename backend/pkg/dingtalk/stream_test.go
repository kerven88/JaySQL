package dingtalk

// ============================================================================
// Stream 协议层的单测
//
// 这层代码直接承载「钉钉能不能把回调送到我们手里」，但它的大部分函数都不碰网络，
// 因此可以纯离线验证。重点覆盖三类容易静默出错的地方：
//   1. 帧头解析 —— 拿不到 topic 就分派不到 handler，表现为"连上了但毫无反应"；
//   2. WebSocket URL 拼接 —— endpoint 自带 query 时用错分隔符会拿到 401/404；
//   3. ACK 结构 —— 少字段钉钉就认为没确认，于是无限重推同一条回调。
// ============================================================================

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 典型回调帧：钉钉把业务数据放在 data 里（JSON 字符串），topic 在 headers 里。
func TestDecodeFrameCallbackFrame(t *testing.T) {
	raw := []byte(`{
		"specVersion": "1.0",
		"type": "CALLBACK",
		"time": 1700000000000,
		"headers": {
			"topic": "/v1.0/card/instances/callback",
			"messageId": "msg-001",
			"contentType": "application/json",
			"time": "1700000000000"
		},
		"data": "{\"outTrackId\":\"track-1\"}"
	}`)
	frame, err := decodeFrame(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if frame.Type != "CALLBACK" {
		t.Errorf("type=%q，期望 CALLBACK", frame.Type)
	}
	if frame.Topic() != CardInstanceCallbackTopic {
		t.Errorf("topic=%q，期望 %q", frame.Topic(), CardInstanceCallbackTopic)
	}
	if frame.MessageID() != "msg-001" {
		t.Errorf("messageId=%q，期望 msg-001", frame.MessageID())
	}
	if !strings.Contains(frame.Data, "track-1") {
		t.Errorf("data 未原样保留: %q", frame.Data)
	}
}

// headers 缺失时必须初始化成空 map，而不是返回 nil ——
// nil map 上取 Topic() 虽然安全，但后续 Set 会 panic。
func TestDecodeFrameWithoutHeadersDoesNotPanic(t *testing.T) {
	frame, err := decodeFrame([]byte(`{"specVersion":"1.0","type":"SYSTEM"}`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if frame.Headers == nil {
		t.Fatal("Headers 应被初始化为空 map")
	}
	if frame.Topic() != "" || frame.MessageID() != "" {
		t.Error("无 headers 时 topic/messageId 应为空串")
	}
}

func TestDecodeFrameRejectsGarbage(t *testing.T) {
	if _, err := decodeFrame([]byte(`not json at all`)); err == nil {
		t.Fatal("非法 JSON 应当报错")
	}
}

// endpoint 不带 query 时用 ?，带了则必须用 & —— 拼错会拿不到 ticket 而握手失败。
func TestBuildWebSocketURL(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		ticket   string
		want     string
	}{
		{"无 query", "wss://api.dingtalk.com/connect", "t-123", "wss://api.dingtalk.com/connect?ticket=t-123"},
		{"已有 query", "wss://api.dingtalk.com/connect?x=1", "t-123", "wss://api.dingtalk.com/connect?ticket=t-123&x=1"},
		{"ws 明文", "ws://127.0.0.1:9000/c", "t-1", "ws://127.0.0.1:9000/c?ticket=t-1"},
		{"两端空白", "  wss://a.com/c  ", "t-1", "wss://a.com/c?ticket=t-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := buildWebSocketURL(c.endpoint, c.ticket)
			if err != nil {
				t.Fatalf("拼接失败: %v", err)
			}
			if got != c.want {
				t.Errorf("got=%q，want=%q", got, c.want)
			}
		})
	}
}

// 非 ws/wss 的 endpoint 必须拒绝：否则可能把 ticket 明文发给 http 地址。
func TestBuildWebSocketURLRejectsNonWS(t *testing.T) {
	for _, endpoint := range []string{"https://api.dingtalk.com/c", "http://evil.example/c", "ftp://x/c", ""} {
		if _, err := buildWebSocketURL(endpoint, "t-1"); err == nil {
			t.Errorf("endpoint=%q 应当被拒绝", endpoint)
		}
	}
}

// ACK 结构必须齐全：钉钉靠 code+headers.messageId 判断「这条已处理」。
func TestACKResponseShape(t *testing.T) {
	ack := ackResponse{
		Code: codeOK,
		Headers: map[string]string{
			"contentType": "application/json",
			"messageId":   "msg-001",
		},
		Message: "ok",
		Data:    `{"response":{}}`,
	}
	raw, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var decoded struct {
		Code    int               `json:"code"`
		Headers map[string]string `json:"headers"`
		Message string            `json:"message"`
		Data    string            `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if decoded.Code != 200 {
		t.Errorf("code=%d，期望 200（非 200 钉钉会重推）", decoded.Code)
	}
	if decoded.Headers["messageId"] != "msg-001" {
		t.Errorf("headers.messageId=%q，期望 msg-001", decoded.Headers["messageId"])
	}
	if decoded.Headers["contentType"] != "application/json" {
		t.Errorf("headers.contentType=%q，期望 application/json", decoded.Headers["contentType"])
	}
}

// 未注册 topic 必须回 404，让钉钉停止重推我们处理不了的消息。
func TestProtocolCodes(t *testing.T) {
	if codeOK != 200 || codeNotFound != 404 || codeInternalFail != 500 {
		t.Errorf("协议码被改动：ok=%d notFound=%d internalFail=%d", codeOK, codeNotFound, codeInternalFail)
	}
}

// 退避抖动：结果落在 [base, base+base/5] 内，且多次调用不完全相同。
func TestJitterBackoffRangeAndVariation(t *testing.T) {
	const base = 3 * time.Second
	distinct := map[time.Duration]struct{}{}
	for i := 0; i < 200; i++ {
		got := jitterBackoff(base)
		if got < base || got > base+base/5 {
			t.Fatalf("抖动越界: %v（base=%v）", got, base)
		}
		distinct[got] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Error("抖动没有产生变化，多实例会同时重连")
	}
	// 非正数直接原样返回
	if got := jitterBackoff(0); got != 0 {
		t.Errorf("jitterBackoff(0)=%v，期望 0", got)
	}
}

// 订阅类型常量是钉钉协议的一部分，改动即失联。
func TestSubscriptionTypeConstants(t *testing.T) {
	if subscriptionSystem != "SYSTEM" {
		t.Errorf("SYSTEM 常量=%q", subscriptionSystem)
	}
	if subscriptionEvent != "EVENT" {
		t.Errorf("EVENT 常量=%q", subscriptionEvent)
	}
	if subscriptionCallback != "CALLBACK" {
		t.Errorf("CALLBACK 常量=%q", subscriptionCallback)
	}
	if CardInstanceCallbackTopic != "/v1.0/card/instances/callback" {
		t.Errorf("卡片回调 topic=%q", CardInstanceCallbackTopic)
	}
}

// FrameHandler 的注册与取用：注册过才有 handler，未注册必须落空
// （落空时 dispatch 会回 404，而不是静默吞掉）。
func TestStreamClientHandleRegistry(t *testing.T) {
	client := NewStreamClient(NewClient(ClientConfig{AppKey: "k", AppSecret: "s"}))
	if _, ok := client.handlers[CardInstanceCallbackTopic]; ok {
		t.Fatal("新建的客户端不应有已注册 handler")
	}
	called := false
	client.Handle(CardInstanceCallbackTopic, func(_ context.Context, _ Frame) (string, error) {
		called = true
		return "", nil
	})
	if _, ok := client.handlers[CardInstanceCallbackTopic]; !ok {
		t.Fatal("注册后应能取到 handler")
	}
	_ = called
	// 未连接时状态为 false
	if client.Connected() {
		t.Error("未建立连接时 Connected() 应为 false")
	}
}
