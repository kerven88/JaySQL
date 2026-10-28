package services

import (
	"context"
	"sync"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/dingtalk"
	"github.com/simajiong/jaysql/pkg/notifier"
)

// ============================================================================
// 钉钉 Stream 长连接的生命周期管理
//
// 为什么走 Stream 而不是 HTTP 回调：Stream 由我们主动出站建连，钉钉把
// 「卡片按钮被点击」顺着这条隧道推下来，**不需要公网 IP、不需要开放入站端口**。
// 99 那套部署在 192.168.1.99 内网，HTTP 回调根本进不来 —— 这是唯一可行的方式。
//
// 启动时机：服务启动时（DB 就绪后）读一次「钉钉企业应用」渠道配置；
// 渠道配置被改动时通过 Reload 重连（换 AppKey 后旧连接会一直失败）。
// 没有任何渠道配置时静默跳过，不影响平台其它功能。
// ============================================================================

var (
	streamMu     sync.Mutex
	streamCancel context.CancelFunc
	streamClient *dingtalk.StreamClient
)

// StartDingTalkApprovalStream 启动 Stream 长连接（幂等：已在运行则直接返回）。
func StartDingTalkApprovalStream() {
	streamMu.Lock()
	defer streamMu.Unlock()
	if streamCancel != nil {
		return
	}
	row, cfg, err := loadDingTalkChannel()
	if err != nil {
		// 没有配置属于常见状态（并非所有部署都用钉钉），用 Info 而非 Warn
		global.App.Log.Infof("[order-dingtalk] 未启动 Stream 长连接：%v", err)
		return
	}

	client := notifier.DingTalkAppClientFromConfig(cfg)
	stream := dingtalk.NewStreamClient(client)
	stream.Handle(dingtalk.CardInstanceCallbackTopic, func(ctx context.Context, frame dingtalk.Frame) (string, error) {
		return HandleDingTalkCardCallback(ctx, frame.Data)
	})

	ctx, cancel := context.WithCancel(context.Background())
	streamCancel = cancel
	streamClient = stream
	go stream.Run(ctx)
	global.App.Log.Infof("[order-dingtalk] 已启动钉钉 Stream 长连接（渠道实例：%s），审批卡片回调将在此通道接收", row.Name)
}

// StopDingTalkApprovalStream 停止长连接（优雅停机 / 配置重载时调用）。
func StopDingTalkApprovalStream() {
	streamMu.Lock()
	defer streamMu.Unlock()
	stopDingTalkStreamLocked()
}

func stopDingTalkStreamLocked() {
	if streamClient != nil {
		streamClient.Stop()
		streamClient = nil
	}
	if streamCancel != nil {
		streamCancel()
		streamCancel = nil
	}
}

// ReloadDingTalkApprovalStream 配置变更后重连（先停后起）。
//
// 必须重连的原因：AppKey/AppSecret 变了以后，旧连接会被钉钉拒绝或一直收不到回调；
// 同时清掉 accessToken 缓存，避免继续用旧应用身份调接口。
func ReloadDingTalkApprovalStream() {
	streamMu.Lock()
	stopDingTalkStreamLocked()
	streamMu.Unlock()
	// 凭据可能整体换掉了，token 缓存一律作废（按 appKey 缓存，这里全清最省心）
	dingtalk.ResetTokenCache("")
	global.App.Log.Infof("[order-dingtalk] 钉钉渠道配置变更，正在重载 Stream 长连接")
	StartDingTalkApprovalStream()
}

// DingTalkStreamConnected 长连接是否处于已连接状态（供状态接口/诊断使用）。
func DingTalkStreamConnected() bool {
	streamMu.Lock()
	client := streamClient
	streamMu.Unlock()
	if client == nil {
		return false
	}
	return client.Connected()
}

// init 订阅「渠道配置变更」事件：
// 钉钉企业应用的 Stream 长连接在建立时就把 AppKey/AppSecret 固化进去了，
// 在「通知渠道」页面改完凭据或停用渠道后必须重连，否则改动看起来生效了、
// 实际回调仍走旧连接（旧凭据改错时表现为一直收不到卡片点击）。
//
// 这里不做 import 反向依赖：monitor 侧的渠道保存逻辑只负责 Notify 一声，
// 由本模块自己决定收到通知后做什么，两个 internal 包之间无需互相引用。
func init() {
	notifier.OnChannelChanged(func(kind string) {
		if kind != notifier.DingTalkAppKind {
			return
		}
		ReloadDingTalkApprovalStream()
	})
}
