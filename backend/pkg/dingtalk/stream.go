package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ============================================================================
// 钉钉 Stream 模式长连接客户端
//
// 与 Webhook 模式的区别：连接由我们主动发起（出站 WSS），钉钉把「卡片按钮被点击」
// 这类回调顺着这条已建立的隧道推下来。因此**不需要公网 IP、不需要开放入站端口**，
// 内网部署（如 192.168.1.99）也能实时收到回调 —— 这是选 Stream 而不是 HTTP 回调的
// 唯一原因，也是本次迭代能在 99 上落地的前提。
//
// 协议（参考 open-dingtalk/developerpedia Stream 协议描述）：
//   1. POST {OpenAPIHost}/v1.0/gateway/connections/open
//      body: {clientId, clientSecret, subscriptions:[{type,topic}], ua, localIp}
//      resp: {endpoint, ticket}  —— ticket 有效期 90 秒且只能用一次
//   2. GET {endpoint}?ticket={ticket}  —— WebSocket 升级
//   3. 钉钉推送帧：{specVersion, type, time, headers:{topic,messageId,contentType,time}, data}
//      type ∈ SYSTEM / EVENT / CALLBACK
//   4. 客户端必须回 ACK，否则钉钉会重复推送：
//      {code:200, headers:{contentType:"application/json", messageId}, message:"ok", data:"..."}
//   5. SYSTEM/ping 需回 pong（data 原样回传）；SYSTEM/disconnect 需回 ACK 后重新建连。
// =============================================================================

const (
	// 订阅类型
	subscriptionSystem   = "SYSTEM"
	subscriptionEvent    = "EVENT"
	subscriptionCallback = "CALLBACK"

	// 帧响应码
	codeOK           = 200
	codeNotFound     = 404
	codeInternalFail = 500

	// userAgent 标识客户端类型，格式 name-sdk-lang/version
	streamUserAgent = "goinsight-stream-go/1.0.0"

	// streamReadTimeout 读超时。钉钉服务端会周期性推 ping；超过这个时间
	// 一帧都没收到，判定连接已死并重连（SDK 的 keepAliveIdle 用的也是 120s）。
	streamReadTimeout = 150 * time.Second

	// 重连退避
	reconnectBaseDelay = 3 * time.Second
	reconnectMaxDelay  = 60 * time.Second
)

// Subscription 订阅项。
type Subscription struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
}

// Frame 钉钉推送的一帧。
type Frame struct {
	SpecVersion string            `json:"specVersion"`
	Type        string            `json:"type"`
	Time        int64             `json:"time"`
	Headers     map[string]string `json:"headers"`
	Data        string            `json:"data"`
}

// Topic 返回帧头里的业务 topic。
func (f Frame) Topic() string { return f.Headers["topic"] }

// MessageID 返回帧头里的消息 ID（ACK 时必须回传同一个）。
func (f Frame) MessageID() string { return f.Headers["messageId"] }

// FrameHandler 业务回调处理函数。返回的字符串会作为 ACK 的 data 回给钉钉
// （互动卡片要求把 cardData 放进 data.response 里，见 card 回调处理）。
type FrameHandler func(ctx context.Context, frame Frame) (string, error)

type ackResponse struct {
	Code    int               `json:"code"`
	Headers map[string]string `json:"headers"`
	Message string            `json:"message"`
	Data    string            `json:"data"`
}

// StreamClient Stream 长连接客户端。一个实例持有一条长连接。
type StreamClient struct {
	appKey    string
	appSecret string
	client    *Client

	mu        sync.RWMutex
	handlers  map[string]FrameHandler
	conn      *websocket.Conn
	writeMu   sync.Mutex
	connected bool
	stopOnce  sync.Once
	stopped   chan struct{}
}

// NewStreamClient 构造 Stream 客户端。
//
// client 复用同一个 *Client：它带着 AppKey/AppSecret/卡片模板等配置，
// 回调处理里要直接用这些配置去更新卡片，避免重复构造。
func NewStreamClient(client *Client) *StreamClient {
	handlers := map[string]FrameHandler{}
	return &StreamClient{
		appKey:    client.cfg.AppKey,
		appSecret: client.cfg.AppSecret,
		client:    client,
		handlers:  handlers,
		stopped:   make(chan struct{}),
	}
}

// Handle 注册某个 topic 的处理函数。
func (s *StreamClient) Handle(topic string, handler FrameHandler) {
	if s == nil {
		return
	}
	topic = strings.TrimSpace(topic)
	if topic == "" || handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[topic] = handler
}

// Connected 当前是否已建立连接（用于界面展示与健康检查）。
func (s *StreamClient) Connected() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// Stop 主动关闭并阻止后续重连。
func (s *StreamClient) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopped) })
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.connected = false
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// Run 阻塞运行直到 ctx 结束或 Stop 被调用，内部自动重连。
// 建议在独立 goroutine 里跑。
func (s *StreamClient) Run(ctx context.Context) {
	if s == nil {
		return
	}
	if s.appKey == "" || s.appSecret == "" {
		logw("Stream 客户端未启动：AppKey / AppSecret 未配置")
		return
	}
	delay := reconnectBaseDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopped:
			return
		default:
		}

		established, err := s.connectAndServe(ctx)
		if err != nil {
			logw("钉钉 Stream 连接中断：%v，%s 后重连", err, delay)
		}
		// 本轮真正建立过连接 ⇒ 上一轮配置是对的，退避回到起点；
		// 连都没连上（凭据错、网络不通）才继续翻倍，避免刷屏。
		if established {
			delay = reconnectBaseDelay
		} else if delay < reconnectMaxDelay {
			delay *= 2
			if delay > reconnectMaxDelay {
				delay = reconnectMaxDelay
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.stopped:
			return
		case <-time.After(jitterBackoff(delay)):
		}
	}
}

// connectAndServe 建立一次连接并处理消息，直到断开返回。
// established 表示本轮是否成功建立过连接（用于退避策略）。
func (s *StreamClient) connectAndServe(ctx context.Context) (established bool, err error) {
	endpoint, ticket, err := s.openConnection(ctx)
	if err != nil {
		return false, err
	}
	wsURL, err := buildWebSocketURL(endpoint, ticket)
	if err != nil {
		return false, err
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		// 直连：不读 HTTP_PROXY/HTTPS_PROXY。内网部署下走环境代理常导致建连失败，
		// 且这类失败在日志里只表现为 timeout，很难定位。
		Proxy: nil,
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		if resp != nil {
			return false, fmt.Errorf("建立 WebSocket 失败（HTTP %d）: %w", resp.StatusCode, err)
		}
		return false, fmt.Errorf("建立 WebSocket 失败: %w", err)
	}
	defer func() { _ = conn.Close() }()

	s.mu.Lock()
	s.conn = conn
	s.connected = true
	s.mu.Unlock()
	established = true
	logf("Stream 长连接已建立（appKey=%s）", maskSecret(s.appKey))

	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
			s.connected = false
		}
		s.mu.Unlock()
	}()

	for {
		if deadlineErr := conn.SetReadDeadline(time.Now().Add(streamReadTimeout)); deadlineErr != nil {
			return true, deadlineErr
		}
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			return true, readErr
		}
		frame, decodeErr := decodeFrame(raw)
		if decodeErr != nil {
			logw("解析 Stream 帧失败：%v", decodeErr)
			continue
		}
		shouldReconnect, handlerErr := s.dispatch(ctx, conn, frame)
		if handlerErr != nil {
			logw("处理 Stream 帧失败 topic=%s: %v", frame.Topic(), handlerErr)
		}
		if shouldReconnect {
			return true, fmt.Errorf("收到 disconnect 指令")
		}
	}
}

// dispatch 处理一帧并回 ACK。返回 true 表示需要重连（disconnect）。
func (s *StreamClient) dispatch(ctx context.Context, conn *websocket.Conn, frame Frame) (bool, error) {
	switch frame.Type {
	case subscriptionSystem:
		switch frame.Topic() {
		case "ping":
			// pong：原样把 data 回传
			return false, s.writeACK(conn, frame.MessageID(), frame.Data)
		case "disconnect":
			// 钉钉要求先确认再断开，否则会重推已处理过的指令
			_ = s.writeACK(conn, frame.MessageID(), "")
			return true, nil
		default:
			return false, s.writeACK(conn, frame.MessageID(), "")
		}
	case subscriptionCallback, subscriptionEvent:
		s.mu.RLock()
		handler := s.handlers[frame.Topic()]
		s.mu.RUnlock()
		if handler == nil {
			// 未注册的 topic 必须回 404，否则钉钉会一直重推
			return false, s.writeACKWithCode(conn, frame.MessageID(), codeNotFound, "topic not handled", "")
		}
		data, err := handler(ctx, frame)
		if err != nil {
			return false, s.writeACKWithCode(conn, frame.MessageID(), codeInternalFail, err.Error(), "")
		}
		return false, s.writeACK(conn, frame.MessageID(), data)
	default:
		return false, s.writeACK(conn, frame.MessageID(), "")
	}
}

// openConnection 注册连接凭证，换取 endpoint 与 ticket。
func (s *StreamClient) openConnection(ctx context.Context) (endpoint, ticket string, err error) {
	subs := []Subscription{
		{Type: subscriptionSystem, Topic: "ping"},
		{Type: subscriptionSystem, Topic: "disconnect"},
	}
	s.mu.RLock()
	for topic := range s.handlers {
		subs = append(subs, Subscription{Type: subscriptionCallback, Topic: topic})
	}
	s.mu.RUnlock()

	payload := map[string]any{
		"clientId":      s.appKey,
		"clientSecret":  s.appSecret,
		"subscriptions": subs,
		"ua":            streamUserAgent,
	}
	var resp struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
	}
	if err := s.client.doJSON(ctx, http.MethodPost,
		s.client.openAPIHost+"/v1.0/gateway/connections/open", nil, payload, &resp); err != nil {
		return "", "", fmt.Errorf("注册 Stream 连接失败: %w", err)
	}
	if strings.TrimSpace(resp.Endpoint) == "" || strings.TrimSpace(resp.Ticket) == "" {
		return "", "", fmt.Errorf("注册 Stream 连接返回为空")
	}
	return resp.Endpoint, resp.Ticket, nil
}

func (s *StreamClient) writeACK(conn *websocket.Conn, messageID, data string) error {
	return s.writeACKWithCode(conn, messageID, codeOK, "ok", data)
}

func (s *StreamClient) writeACKWithCode(conn *websocket.Conn, messageID string, code int, message, data string) error {
	ack := ackResponse{
		Code: code,
		Headers: map[string]string{
			"contentType": "application/json",
			"messageId":   messageID,
		},
		Message: message,
		Data:    data,
	}
	encoded, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, encoded)
}

func decodeFrame(raw []byte) (Frame, error) {
	var frame Frame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return frame, err
	}
	if frame.Headers == nil {
		frame.Headers = map[string]string{}
	}
	return frame, nil
}

// buildWebSocketURL 按协议拼接 ?ticket=xxx。endpoint 已带 query 时改用 & 连接。
func buildWebSocketURL(endpoint, ticket string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("Stream endpoint 非法: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return "", fmt.Errorf("Stream endpoint 协议非法: %s", parsed.Scheme)
	}
	query := parsed.Query()
	query.Set("ticket", ticket)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// jitterBackoff 给退避加抖动，避免多实例同时重连把钉钉侧打爆。
func jitterBackoff(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	delta := base / 5
	return base + time.Duration(rand.Int63n(int64(delta)+1))
}
