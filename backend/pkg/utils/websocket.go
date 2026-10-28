package utils

import (
	"context"
	"encoding/json"

	"github.com/simajiong/jaysql/internal/global"
)

const (
	RenderLogStream         = "log"
	RenderProcessListStream = "processlist"
	RenderGhostStream       = "gh-ost"
)

type PublishMSG struct {
	ExecutionID string `json:"execution_id"` // 一次执行的唯一标识，如taskid
	Type        string `json:"type"`         // gh-ost / processlist / log
	Data        any    `json:"data"`
}

// Publish 把消息投递到进程内事件总线（原先走 Redis Pub/Sub，该依赖已移除）。
//
// 语义与旧实现保持一致：fire-and-forget。总线只做「尽力投递」，
// 没有订阅者或订阅者消费不及时时消息会被丢弃，调用方绝不会因此阻塞。
// 保留 error 返回值与函数签名不变，避免波及 13 个调用点。
func Publish(ctx context.Context, channel string, executionID string, renderType string, data any) error {
	msg := PublishMSG{
		ExecutionID: executionID,
		Type:        renderType,
		Data:        data,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		global.App.Log.Error("publish marshal error", err)
		return err
	}

	if global.App.Bus == nil {
		// 正常启动流程不会走到这里（bootstrap.InitializeEventBus 一定先于业务执行）。
		// 防御单元测试等未完整初始化的场景。
		return nil
	}

	global.App.Bus.Publish(channel, string(payload))
	return nil
}
