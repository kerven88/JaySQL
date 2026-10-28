package services

import (
	"testing"
	"time"
)

func TestAlertTrendBuckets(t *testing.T) {
	now := time.Date(2026, 9, 21, 14, 30, 0, 0, time.Local)

	// 7d：7 个自然日（含今天），首段 6 天前 00:00，末段 end 截到 now
	d7 := alertTrendBuckets("7d", now)
	if len(d7) != 7 {
		t.Fatalf("7d 应 7 段，got %d", len(d7))
	}
	if d7[0].label != "9/15" || d7[6].label != "9/21" {
		t.Fatalf("7d 标签错误: %s ~ %s", d7[0].label, d7[6].label)
	}
	if !d7[6].end.Equal(now) {
		t.Fatalf("末段 end 应截到 now，got %v", d7[6].end)
	}
	if d7[0].drill != "9月15日" {
		t.Fatalf("下钻标签错误: %s", d7[0].drill)
	}

	// 未知预设回 nil（服务层兜底 7d，这里是防御）
	if got := alertTrendBuckets("bogus", now); got != nil {
		t.Fatalf("未知预设应返回 nil")
	}

	// today：只到当前小时（14:30 → 15 段），末段 end 截到 now
	td := alertTrendBuckets("today", now)
	if len(td) != 15 {
		t.Fatalf("today 应 15 段（00时~14时），got %d", len(td))
	}
	if td[0].label != "0时" || td[14].label != "14时" {
		t.Fatalf("today 标签错误: %s ~ %s", td[0].label, td[14].label)
	}
	if !td[14].end.Equal(now) {
		t.Fatalf("today 末段 end 应截到 now，got %v", td[14].end)
	}
	if td[14].drill != "9月21日 14:00–15:00" {
		t.Fatalf("today 下钻标签错误: %s", td[14].drill)
	}

	// 14d：14 段
	if got := alertTrendBuckets("14d", now); len(got) != 14 {
		t.Fatalf("14d 应 14 段，got %d", len(got))
	}
	// 3d：3 段
	if got := alertTrendBuckets("3d", now); len(got) != 3 {
		t.Fatalf("3d 应 3 段，got %d", len(got))
	}
}

// 趋势柱「未恢复」段判定：与下钻弹窗 isRecovered 分组同口径（仅看 status），
// 不复用 isUnresolvedAlertEvent（它额外排除 ignored，会造成柱-弹窗分组对不上）。
func TestTrendEventUnresolved(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"", true},
		{"active", true},
		{"pending", true},
		{"acknowledged", true},
		{"recovered", false},
		{"RECOVERED", false},
		{" recovered ", false},
		{"weird", true},
	}
	for _, c := range cases {
		if got := trendEventUnresolved(c.status); got != c.want {
			t.Fatalf("trendEventUnresolved(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}
