package lock

import (
	"testing"
	"time"
)

func TestAcquireAndRelease(t *testing.T) {
	m := New(time.Minute)

	if s := m.Acquire("order-1"); s != StatusAcquired {
		t.Errorf("首次抢占应成功，实际 %v", s)
	}
	if s := m.Acquire("order-1"); s != StatusBusy {
		t.Errorf("已持有的锁再抢应返回 Busy，实际 %v", s)
	}
	if !m.IsLocked("order-1") {
		t.Error("IsLocked 应为 true")
	}

	m.Release("order-1")
	if m.IsLocked("order-1") {
		t.Error("释放后 IsLocked 应为 false")
	}
	// 释放后可再次抢占
	if s := m.Acquire("order-1"); s != StatusAcquired {
		t.Errorf("释放后再次抢占应成功，实际 %v", s)
	}
}

func TestReleaseUnheldKeyIsSafe(t *testing.T) {
	m := New(time.Minute)
	m.Release("never-acquired") // 不应 panic
	if m.IsLocked("never-acquired") {
		t.Error("从未抢占的 key 不应处于锁定状态")
	}
}

func TestExpiredLockIsReclaimed(t *testing.T) {
	m := New(30 * time.Millisecond)

	if s := m.Acquire("order-x"); s != StatusAcquired {
		t.Fatalf("首次抢占应成功，实际 %v", s)
	}
	// 不释放，等租约过期
	time.Sleep(50 * time.Millisecond)

	// 过期后抢占应返回 Reclaimed（而不是 Busy）——这是修复「锁永久卡死」的核心行为
	if s := m.Acquire("order-x"); s != StatusReclaimed {
		t.Errorf("过期锁被抢占应返回 StatusReclaimed，实际 %v", s)
	}
	if !m.IsLocked("order-x") {
		t.Error("回收后应是新的有效租约")
	}
}

func TestIndependentKeys(t *testing.T) {
	m := New(time.Minute)
	m.Acquire("a")
	if s := m.Acquire("b"); s != StatusAcquired {
		t.Errorf("不同 key 互不影响，实际 %v", s)
	}
	if m.Len() != 2 {
		t.Errorf("应持有 2 把锁，实际 %d", m.Len())
	}
}

func TestNewDefaults(t *testing.T) {
	m := New(0) // ttl <= 0 → DefaultTTL
	if m.ttl != DefaultTTL {
		t.Errorf("ttl<=0 应回落 DefaultTTL，实际 %v", m.ttl)
	}
}
