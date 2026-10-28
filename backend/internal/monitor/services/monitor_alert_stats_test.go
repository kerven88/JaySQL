package services

import (
	"testing"
	"time"
)

func TestBuildAlertTrend(t *testing.T) {
	now := time.Date(2026, 9, 20, 18, 30, 0, 0, time.Local)
	windowStart := time.Date(2026, 9, 14, 0, 0, 0, 0, time.Local)

	rows := []alertTrendRow{
		// 今天 3 条：P0×1 / P1×1 / P3×1
		{TriggeredAt: now.Add(-1 * time.Hour), Severity: "P0"},
		{TriggeredAt: now.Add(-2 * time.Hour), Severity: "P1"},
		{TriggeredAt: now.Add(-3 * time.Hour), Severity: "P3"},
		// 6 天前（窗口首日）2 条 P2
		{TriggeredAt: windowStart.Add(5 * time.Hour), Severity: "P2"},
		{TriggeredAt: windowStart.Add(6 * time.Hour), Severity: "p2"}, // 小写也要归档
		// 窗口外的旧事件必须被忽略（调用方 SQL 已限窗，这里防御性验证）
		{TriggeredAt: windowStart.AddDate(0, 0, -10), Severity: "P1"},
		// 未来时间戳（时钟漂移）必须落到今天，不能 panic 或越界
		{TriggeredAt: now.Add(2 * time.Hour), Severity: "P2"},
	}

	got := buildAlertTrend(rows, windowStart, now, 7)
	if len(got) != 7 {
		t.Fatalf("趋势必须固定 7 点，实际 %d", len(got))
	}
	first, last := got[0], got[6]
	if first.Date != "09-14" || last.Date != "09-20" {
		t.Fatalf("日期轴不符合预期：首 %s 尾 %s", first.Date, last.Date)
	}
	if first.Total != 2 || first.P2 != 2 {
		t.Fatalf("窗口首日应聚到 2 条 P2，实际 %+v", first)
	}
	if last.Total != 4 || last.P0 != 1 || last.P1 != 1 || last.P3 != 1 || last.P2 != 1 {
		t.Fatalf("今天应聚到 4 条（含漂移的 1 条 P2），实际 %+v", last)
	}
	// 中间的空白天必须补 0，前端不需要自己补日期轴
	for i := 1; i < 6; i++ {
		if got[i].Total != 0 {
			t.Fatalf("中间空白日 %s 应补 0，实际 %d", got[i].Date, got[i].Total)
		}
	}
}

func TestBuildAlertTrendSpan30(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	windowStart := now.AddDate(0, 0, -29)
	windowStart = time.Date(windowStart.Year(), windowStart.Month(), windowStart.Day(), 0, 0, 0, 0, time.Local)

	rows := []alertTrendRow{
		// 首日 1 条 P1、今天 2 条 P0，窗口外旧事件必须丢弃
		{TriggeredAt: windowStart.Add(3 * time.Hour), Severity: "P1"},
		{TriggeredAt: now.Add(-1 * time.Hour), Severity: "P0"},
		{TriggeredAt: now.Add(-2 * time.Hour), Severity: "P0"},
		{TriggeredAt: windowStart.AddDate(0, 0, -1), Severity: "P2"},
	}

	got := buildAlertTrend(rows, windowStart, now, 30)
	if len(got) != 30 {
		t.Fatalf("30 天窗口必须固定 30 点，实际 %d", len(got))
	}
	if got[0].Date != "09-07" || got[29].Date != "10-06" {
		t.Fatalf("30 天日期轴不符合预期：首 %s 尾 %s", got[0].Date, got[29].Date)
	}
	if got[0].Total != 1 || got[0].P1 != 1 {
		t.Fatalf("窗口首日应聚到 1 条 P1，实际 %+v", got[0])
	}
	if got[29].Total != 2 || got[29].P0 != 2 {
		t.Fatalf("今天应聚到 2 条 P0，实际 %+v", got[29])
	}
}

func TestTrendDaysNormalize(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 7}, {-5, 7}, {7, 7}, {1, 1}, {30, 30}, {31, 30}, {365, 30},
	}
	for _, c := range cases {
		if got := trendDays(c.in); got != c.want {
			t.Fatalf("trendDays(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestGenerateAlertIDRawFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := generateAlertIDRaw()
		if len(id) != 16 || id[:4] != "alm-" {
			t.Fatalf("ID 格式非法：%q", id)
		}
		for _, ch := range id[4:] {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
				t.Fatalf("ID 含非法字符：%q", id)
			}
		}
	}
	// 随机性抽查：200 次生成应几乎不可能全部相同
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		seen[generateAlertIDRaw()] = struct{}{}
	}
	if len(seen) < 190 {
		t.Fatalf("随机性不足：200 次生成仅 %d 个不同 ID", len(seen))
	}
}
