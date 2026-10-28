package views

import (
	"net/http"

	"github.com/gin-contrib/requestid"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// 创建WebSocket升级器
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func WebSocketHandler(c *gin.Context) {
	channel := c.Param("channel")
	requestID := requestid.Get(c)

	global.App.Log.Infof("WebSocket connected: channel=%s, request_id=%s", channel, requestID)

	// 升级为 WebSocket
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.App.Log.Errorf("WebSocket upgrade failed: channel=%s, request_id=%s, error=%v", channel, requestID, err)
		return
	}
	defer func() {
		ws.Close()
		global.App.Log.Infof("WebSocket closed: channel=%s, request_id=%s", channel, requestID)
	}()

	// 监听客户端是否断开（读协程）
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msgType, msg, err := ws.ReadMessage()
			if err != nil {
				global.App.Log.Infof("WebSocket read message failed: channel=%s, request_id=%s, error=%v", channel, requestID, err)
				return
			}

			// 心跳处理
			if msgType == websocket.TextMessage && string(msg) == "ping" {
				ws.WriteMessage(websocket.TextMessage, []byte("pong"))
				global.App.Log.Infof("WebSocket heartbeat pong: channel=%s, request_id=%s", channel, requestID)
				continue
			}
		}
	}()

	// 订阅进程内事件总线（原先经 Redis Pub/Sub，该依赖已移除）。
	// 原先这里还有一段 `if sub == nil` 的防御，但 go-redis 在 nil receiver 上
	// 调用 Subscribe 会先 panic，根本走不到判断——那是一段从未生效的死代码，
	// 迁移到内存总线后连同无效防御一并移除。
	subID, msgChan := global.App.Bus.Subscribe(channel)
	defer global.App.Bus.Unsubscribe(channel, subID)

	global.App.Log.Infof("WebSocket subscribed: channel=%s, request_id=%s, subscribers=%d", channel, requestID, global.App.Bus.SubscriberCount(channel))

	// 主循环：把总线消息推送给 websocket
	for {
		select {
		case <-done:
			// websocket 被关闭
			global.App.Log.Infof("WebSocket disconnected: channel=%s, request_id=%s", channel, requestID)
			return

		case m, ok := <-msgChan:
			if !ok {
				// 总线端点被关闭（仅发生在本连接自己的 defer Unsubscribe 之后）
				global.App.Log.Infof("WebSocket event channel closed: channel=%s, request_id=%s", channel, requestID)
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, []byte(m)); err != nil {
				// 写入失败 = 连接断开
				global.App.Log.Infof("WebSocket write message failed: channel=%s, request_id=%s, error=%v", channel, requestID, err)
				return
			}
		}
	}
}
