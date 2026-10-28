package services

import (
	"fmt"
	"testing"
)

func TestSortInstanceStats(t *testing.T) {
	// 关键断言：有 P0/P1 的实例必须排在"条数更多但只有 P3"的实例之前。
	// 若把规则写成"按总条数排序"，这条会失败——而那正是值班最容易被坑的场景。
	stats := []AlertCenterInstanceStat{
		{InstanceID: "noisy-p3", Instance: "10.0.0.9:3306", Total: 40, P3: 40, Active: 40, LatestAt: "2026-09-20 15:00:00"},
		{InstanceID: "danger-p1", Instance: "10.0.0.1:3306", Total: 2, P1: 2, Active: 2, LatestAt: "2026-09-20 14:00:00"},
		{InstanceID: "critical-p0", Instance: "10.0.0.7:3306", Total: 1, P0: 1, Active: 1, LatestAt: "2026-09-20 13:00:00"},
		{InstanceID: "acked-p2", Instance: "10.0.0.2:3306", Total: 10, P2: 10, Acked: 10, Active: 0, LatestAt: "2026-09-20 15:10:00"},
		{InstanceID: "active-p3", Instance: "10.0.0.3:3306", Total: 5, P3: 5, Active: 5, LatestAt: "2026-09-20 15:20:00"},
	}
	got := sortInstanceStats(stats)
	// 高危组（P0 或 P1）内部按未恢复条数降序：danger-p1(2) → critical-p0(1)
	if got[0].InstanceID != "danger-p1" {
		t.Fatalf("P0/P1 组内未恢复条数多的排第一，实际第一是 %s", got[0].InstanceID)
	}
	if got[1].InstanceID != "critical-p0" {
		t.Fatalf("P0 实例必须仍留在第一档，实际第二位是 %s", got[1].InstanceID)
	}
	// 其余按"未恢复条数"降序：noisy-p3(40) → active-p3(5) → acked-p2(0)。
	if got[2].InstanceID != "noisy-p3" {
		t.Fatalf("非高危组按未恢复条数排 noisy-p3，实际是 %s", got[2].InstanceID)
	}
	if got[3].InstanceID != "active-p3" {
		t.Fatalf("第四位应为 active-p3，实际是 %s", got[3].InstanceID)
	}
	if got[len(got)-1].InstanceID != "acked-p2" {
		t.Fatalf("已全部认领(Active=0)的实例应垫底，实际最后是 %s", got[len(got)-1].InstanceID)
	}
}

func TestSortInstanceStatsTruncates(t *testing.T) {
	stats := make([]AlertCenterInstanceStat, 0, alertCenterInstanceStatLimit+20)
	for i := 0; i < alertCenterInstanceStatLimit+20; i++ {
		stats = append(stats, AlertCenterInstanceStat{
			InstanceID: fmt.Sprintf("ins-%03d", i),
			Total:      i + 1,
			Active:     i + 1,
			LatestAt:   "2026-09-20 15:00:00",
		})
	}
	got := sortInstanceStats(stats)
	if len(got) != alertCenterInstanceStatLimit {
		t.Fatalf("截断后长度应为 %d，实际 %d", alertCenterInstanceStatLimit, len(got))
	}
	if got[0].InstanceID != "ins-069" {
		t.Fatalf("截断应保留最吵的实例，实际首位是 %s", got[0].InstanceID)
	}
}

func TestParseAlertCenterTime(t *testing.T) {
	if _, ok := parseAlertCenterTime(""); ok {
		t.Fatal("空串应当表示不过滤")
	}
	if _, ok := parseAlertCenterTime("昨天下午"); ok {
		t.Fatal("非法格式应当表示不过滤，而不是把列表查空")
	}
	got, ok := parseAlertCenterTime("2026-09-18 13:31:35")
	if !ok {
		t.Fatal("合法时间应当解析成功")
	}
	if got.Format("2006-01-02 15:04:05") != "2026-09-18 13:31:35" {
		t.Fatalf("解析结果不符合预期：%v", got)
	}
}

func TestNormalizeAlertCenterPage(t *testing.T) {
	cases := []struct {
		page, pageSize, wantPage, wantSize int
	}{
		{0, 0, 1, alertCenterDefaultPageSize},
		{-3, -1, 1, alertCenterDefaultPageSize},
		{2, 50, 2, 50},
		{1, 9999, 1, alertCenterMaxPageSize},
	}
	for _, item := range cases {
		page, size := normalizeAlertCenterPage(item.page, item.pageSize)
		if page != item.wantPage || size != item.wantSize {
			t.Fatalf("normalizeAlertCenterPage(%d,%d) = (%d,%d), want (%d,%d)",
				item.page, item.pageSize, page, size, item.wantPage, item.wantSize)
		}
	}
}

// TestIsUnresolvedAlertEvent 守住「未恢复告警」的口径：
// 被人工忽略的告警不得再计入未恢复（曾导致告警中心 13 条 vs 大屏 11 条）。
func TestIsUnresolvedAlertEvent(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		ignored bool
		want    bool
	}{
		{"active 未忽略算未恢复", "active", false, true},
		{"acknowledged 未忽略算未恢复", "acknowledged", false, true},
		{"已恢复不算未恢复", "recovered", false, false},
		{"状态大小写不敏感", "RECOVERED", false, false},
		{"两侧空格不影响判定", "  active  ", false, true},
		{"被忽略的 active 不算未恢复", "active", true, false},
		{"被忽略的 acknowledged 不算未恢复", "acknowledged", true, false},
		{"既忽略又恢复也不算", "recovered", true, false},
	}
	for _, item := range cases {
		if got := isUnresolvedAlertEvent(item.status, item.ignored); got != item.want {
			t.Fatalf("%s：status=%q ignored=%v 期望 %v，实际 %v",
				item.name, item.status, item.ignored, item.want, got)
		}
	}
}
