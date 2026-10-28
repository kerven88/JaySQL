package services

import (
	"context"
	"strings"
	"sync"
)

// ============================================================
// 运行中对话的取消注册表
//
// 为什么不能只靠「客户端断开连接」：
//   - 断开连接依赖运行时把 TCP 关闭翻译成 request ctx 的取消，中间若隔了反向代理
//     或缓冲层，这个传导可能滞后甚至不发生；
//   - 工具路由一次提问最多跑 maxAgentSteps 轮模型调用，中途不写任何响应体，
//     服务端只有 ctx 被取消才能停下 —— 用户点了取消却在后台继续烧 token，
//     是实打实的成本问题。
//
// 因此取消做成「双通道」：前端既断开本地连接，也显式 POST /chat/cancel 投递 run_id。
// 两条通道都收敛到同一个 context.CancelFunc，谁先到谁生效，重复到达天然幂等
// （context.CancelFunc 可重复调用）。
//
// 安全约束：每条注册项绑定发起人用户名。取消时必须校验调用者与发起人一致，
// 否则任何登录用户只要拿到别人的 run_id 就能掐断对方的对话。
// ============================================================

// chatRunEntry 一条运行中的对话。
// 存返回会用到指针比较（判断「这条是不是我自己注册的」），因为函数值只能与 nil 比较。
type chatRunEntry struct {
	username string
	cancel   context.CancelFunc
}

var chatRunRegistry = struct {
	mu sync.Mutex
	m  map[string]*chatRunEntry
}{m: make(map[string]*chatRunEntry)}

// RegisterChatRun 登记一次运行，返回可取消的 ctx 与注销函数。
//
// runID 为空表示调用方不需要显式取消能力（例如未带 run_id 的旧客户端），
// 此时只返回派生 ctx，语义等价于改动前。
// 注销函数必须 defer 调用：未注销的条目会一直留在 map 里，长跑进程会缓慢泄漏。
func RegisterChatRun(parent context.Context, runID, username string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)

	id := strings.TrimSpace(runID)
	if id == "" {
		return ctx, cancel
	}

	entry := &chatRunEntry{username: username, cancel: cancel}

	chatRunRegistry.mu.Lock()
	// 同一 run_id 重复登记（前端重试、请求重放）：先把旧的掐掉，
	// 否则旧编排会因为注册项被覆盖而永远取消不掉，成为后台孤儿。
	if old, ok := chatRunRegistry.m[id]; ok && old != nil {
		old.cancel()
	}
	chatRunRegistry.m[id] = entry
	chatRunRegistry.mu.Unlock()

	return ctx, func() {
		chatRunRegistry.mu.Lock()
		// 只删自己那一条：run_id 可能已被新的一轮覆盖，
		// 按 id 无脑删除会把新一轮的取消能力一起抹掉。
		if cur, ok := chatRunRegistry.m[id]; ok && cur == entry {
			delete(chatRunRegistry.m, id)
		}
		chatRunRegistry.mu.Unlock()
		cancel()
	}
}

// CancelChatRun 向指定运行投递取消信号。
//
// 返回值 true = 找到运行、已发出取消信号；false = 该运行已结束 / 不存在 / 不属于该调用者。
// 三种 false 对外刻意不区分 —— 否则这个接口就成了「探测别人是否正在对话」的工具。
func CancelChatRun(runID, username string) bool {
	id := strings.TrimSpace(runID)
	if id == "" {
		return false
	}

	chatRunRegistry.mu.Lock()
	entry, ok := chatRunRegistry.m[id]
	chatRunRegistry.mu.Unlock()

	if !ok || entry == nil || entry.username != username {
		return false
	}
	// 只发信号，不删条目：真正的清理交给 RegisterChatRun 的注销函数，
	// 保证「已取消」和「已结束」在注册表里是同一件事，不会出现两头状态不一致。
	entry.cancel()
	return true
}

// ActiveChatRuns 当前仍在运行中的对话数（自检 / 排障用，不参与业务判断）
func ActiveChatRuns() int {
	chatRunRegistry.mu.Lock()
	defer chatRunRegistry.mu.Unlock()
	return len(chatRunRegistry.m)
}
