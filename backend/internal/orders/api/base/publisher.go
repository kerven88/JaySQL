package base

import (
	"context"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/utils"
)

// Publisher 把工单执行过程中的实时消息（日志 / processlist / gh-ost 进度）
// 投递到以工单 ID 命名的 channel。
//
// 名称沿革：原先叫 RedisPublisher（消息确实经 Redis Pub/Sub 中转），2026-10 起
// Redis 依赖移除，消息改走进程内事件总线（pkg/eventbus），遂更名。
// 方法签名保持不变，13 个调用点只需换构造函数名。
type Publisher struct{}

func NewPublisher() *Publisher {
	return &Publisher{}
}

// PublishWithRenderType publishes message with specified render type (processlist, gh-ost, etc.)
func (p *Publisher) Publish(channel string, executionID string, renderType string, data any) {
	if err := utils.Publish(
		context.Background(),
		channel,
		executionID,
		renderType,
		data,
	); err != nil {
		global.App.Log.Error(err)
	}
}
