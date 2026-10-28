// Package eventbus 提供进程内的发布/订阅能力，替代原先基于 Redis Pub/Sub 的实现。
//
// 背景：JaySQL 的工单执行器会把执行日志、SHOW PROCESSLIST 快照、gh-ost 变更进度
// 投递到以工单 ID 命名的 channel，WebSocket 端点 /ws/:channel 订阅同名 channel 后
// 转发给浏览器。这条链路原先依赖 Redis，为此引入了一个与核心业务无关的外部组件。
// 单实例部署下（JaySQL 的默认形态：单二进制 + systemd），进程内广播语义完全等价。
//
// 关键约束：本总线只保证「尽力投递」。订阅者缓冲写满时该条消息会被丢弃，
// Publish 永不阻塞调用方——执行器不能因为暂时没人消费就把 SQL 执行卡住。
// 这与原先 Redis PUBLISH 的 fire-and-forget 语义一致。
//
// 注意：本实现只在单进程内生效。若将来需要多实例部署，同一条消息只会投递给
// 连在本进程上的订阅者，彼时应改用 Redis / 数据库等跨进程通道。
package eventbus

import "sync"

// DefaultBufferSize 单个订阅者的消息缓冲深度。
// 日志流是突发性的（一条大 DDL 可能瞬间产出几十条），给一点缓冲吸收抖动。
const DefaultBufferSize = 256

// subscription 是一个订阅者的接收端点。
// ch 由 Unsubscribe 关闭，因此 Publish 必须在持有读锁期间投递，
// 否则可能与 close(ch) 并发，造成向已关闭通道写入的 panic。
type subscription struct {
	id uint64
	ch chan string
}

// Bus 是进程内的发布/订阅总线，零外部依赖，可安全并发使用。
type Bus struct {
	mu      sync.RWMutex
	topics  map[string]map[uint64]*subscription
	nextID  uint64
	bufSize int
}

// New 创建一条总线。bufSize <= 0 时使用 DefaultBufferSize。
func New(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}
	return &Bus{
		topics:  make(map[string]map[uint64]*subscription),
		bufSize: bufSize,
	}
}

// Subscribe 订阅 channel，返回订阅 ID 与只读消息通道。
// 调用方必须在不再需要时调用 Unsubscribe，否则该端点会一直占用内存。
func (b *Bus) Subscribe(channel string) (uint64, <-chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := &subscription{id: b.nextID, ch: make(chan string, b.bufSize)}
	b.nextID++

	if b.topics[channel] == nil {
		b.topics[channel] = make(map[uint64]*subscription)
	}
	b.topics[channel][sub.id] = sub
	return sub.id, sub.ch
}

// Unsubscribe 退订并关闭消息通道，重复调用安全。
// 关闭通道会让仍在读取该通道的 goroutine 立即收到「通道已关闭」，从而退出。
func (b *Bus) Unsubscribe(channel string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs, ok := b.topics[channel]
	if !ok {
		return
	}
	sub, ok := subs[id]
	if !ok {
		return
	}
	delete(subs, id)
	close(sub.ch)
	if len(subs) == 0 {
		delete(b.topics, channel)
	}
}

// Publish 向 channel 广播一条消息，返回成功投递的订阅者数量。
//
// 投递全程持有读锁：一方面 map 不能被并发写，另一方面 Unsubscribe 需要写锁，
// 持读锁可保证 close(ch) 不会与写入同时发生。每次投递都是非阻塞 select，
// 临界区在纳秒级，不会成为瓶颈。
func (b *Bus) Publish(channel string, payload string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.topics[channel]
	if !ok {
		return 0
	}

	delivered := 0
	for _, sub := range subs {
		select {
		case sub.ch <- payload:
			delivered++
		default:
			// 该订阅者消费不及时，丢弃这一条。
			// 实时日志允许丢，SQL 执行不允许卡——这个取舍是刻意的。
		}
	}
	return delivered
}

// SubscriberCount 返回 channel 当前的订阅者数量，排障时用来确认链路是否接通。
func (b *Bus) SubscriberCount(channel string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.topics[channel])
}
