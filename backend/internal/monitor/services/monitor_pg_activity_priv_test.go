package services

import (
	"strings"
	"testing"
)

// monitor_pg_activity_priv_test.go
//
// 覆盖的是"会话明细可读性"这道闸门本身。它不需要真实数据库也能验证，
// 因为它被刻意设计成纯函数 —— 真实下面的代码路径依赖 PostgreSQL 权限体系，
// 而这套体系正是我们要防的东西，写集成测试反而会掩盖语义。

func TestPGActivityReadabilityOf(t *testing.T) {
	cases := []struct {
		name         string
		superUser    bool
		statsRole    bool
		monitorRole  bool
		wantReadable bool
	}{
		{"超级用户应可读", true, false, false, true},
		{"具备 pg_read_all_stats 应可读", false, true, false, true},
		{"具备 pg_monitor 应可读", false, false, true, true},
		{"三者皆无应不可读", false, false, false, false},
		{"间接授权的 pg_monitor 也应可读", false, false, true, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := pgActivityReadabilityOf(item.superUser, item.statsRole, item.monitorRole)
			if got.DetailReadable != item.wantReadable {
				t.Fatalf("DetailReadable = %v, want %v", got.DetailReadable, item.wantReadable)
			}
			if !item.wantReadable && strings.TrimSpace(got.Reason) == "" {
				t.Fatalf("不可读时必须给出原因，否则前端无从提示用户如何修复")
			}
		})
	}
}

func TestPGActivityDenialReason(t *testing.T) {
	// 可读时不该产生停用原因
	readable := pgActivityReadabilityOf(true, false, false)
	if got := pgActivityDenialReason(readable); got != "" {
		t.Fatalf("可读时不应有停用原因，got %q", got)
	}

	// 不可读时，原因必须既讲清后果又给出修复动作
	denied := pgActivityReadabilityOf(false, false, false)
	reason := pgActivityDenialReason(denied)
	for _, keyword := range []string{"会话明细不可读", "pg_read_all_stats", "GRANT"} {
		if !strings.Contains(reason, keyword) {
			t.Fatalf("停用原因缺少关键信息 %q：%s", keyword, reason)
		}
	}
}

// TestPGActivityDenialReasonStable 停用文案保持稳定，前端按关键字归并才不会失效。
func TestPGActivityDenialReasonStable(t *testing.T) {
	reason := pgActivityDenialReason(pgActivityReadabilityOf(false, false, false))
	if !strings.HasPrefix(reason, "会话明细不可读：") {
		t.Fatalf("停用原因必须以统一前缀开头，got %q", reason)
	}
}
