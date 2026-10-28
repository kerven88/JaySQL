// Package lock 提供进程内的互斥锁，替代原先基于 Redis SetNX 的工单执行锁。
//
// 背景：工单执行前需要抢占一把以工单 ID 命名的锁，防止「用户手动点执行」与
// 「定时调度器触发」撞车导致同一工单被执行两次。原先这把锁放在 Redis 里，
// 代价是为整个平台引入一个外部组件；单实例部署下进程内锁完全等价。
//
// 相比原实现多了一层保护：每个锁都带租约（TTL）。原实现的 SetNX 过期时间传的是 0
// （永不过期），一旦持有者进程崩溃、defer 未执行，该工单就会永久卡在「执行中」，
// 只能靠运维手工清理。这里即便某条路径漏掉释放，最坏也会在 TTL 之后自动恢复。
package lock

import (
	"sync"
	"time"
)

// DefaultTTL 锁的默认租约时长。
//
// 正常路径下持有者会在 defer 里显式释放，TTL 只用于兜底那些「没走到释放」的异常路径。
// 取值需要大于单次工单执行的合理上限（gh-ost 的大表 DDL 可能持续数小时），
// 否则长任务执行途中锁被别人抢走，反而制造出并发执行。
const DefaultTTL = 6 * time.Hour

// Status 描述一次抢占的结果。
type Status int

const (
	// StatusAcquired 抢到了锁（此前无人持有）。
	StatusAcquired Status = iota
	// StatusBusy 锁正被他人正常持有，本次抢占失败。
	StatusBusy
	// StatusReclaimed 抢占了一个已超过租约的锁，说明上一次持有者没有正常释放。
	// 调用方应当记一条 warn 日志：这是执行路径存在漏洞的信号。
	StatusReclaimed
)

type entry struct {
	expiresAt time.Time
}

// Manager 管理一组进程内的带租约互斥锁，可安全并发使用。
type Manager struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]*entry
}

// New 创建锁管理器，ttl <= 0 时使用 DefaultTTL。
func New(ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Manager{ttl: ttl, m: make(map[string]*entry)}
}

// Acquire 尝试抢占 key。租约从抢占成功的时刻开始计算。
func (m *Manager) Acquire(key string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if e, ok := m.m[key]; ok {
		if now.Before(e.expiresAt) {
			return StatusBusy
		}
		// 租约已过：上一次持有者没能释放（进程崩溃被 recover、分支提前 return 等）。
		// 回收它并继续，避免该 key 永久卡死。
		m.m[key] = &entry{expiresAt: now.Add(m.ttl)}
		return StatusReclaimed
	}

	m.m[key] = &entry{expiresAt: now.Add(m.ttl)}
	return StatusAcquired
}

// Release 释放锁。释放一个不存在或不属于自己的锁是安全的空操作。
func (m *Manager) Release(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
}

// IsLocked 返回 key 是否正处于有效租约内，供排障与自检使用。
func (m *Manager) IsLocked(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.m[key]
	if !ok {
		return false
	}
	return time.Now().Before(e.expiresAt)
}

// Len 返回当前持有的锁数量。
//
// 注意：只有 Release 会删除条目。漏掉释放的过期 key 会在下次 Acquire 时被覆盖，
// 不会无限增长（key 是工单 ID，总量受工单数约束），因此不需要额外的清理协程。
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.m)
}
