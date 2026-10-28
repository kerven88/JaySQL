package services

import "testing"

// 大屏所有告警统计必须先限定在"可见实例域"内（= 卡片来源的 rows），再叠加模板白名单。
//
// 这条断言守住一个已经踩过的坑：告警中心用 instance_id IN (...) 把实例已删除的
// 孤儿事件排除，大屏此前只做模板门控、不知实例归属的行（门控函数取不到 = nil）
// 被原样计入 —— 于是同一个系统里出现「大屏 7 条 / 告警中心 6 条」，差额无法解释。
// 可见域一旦被改空/改漏，两侧数字会再次悄悄分叉，这里用最小代价把它钉住。
func TestScreenVisibleInstanceIDs(t *testing.T) {
	rows := []monitorInstanceRow{{InstanceID: "ds-a"}, {InstanceID: "ds-b"}}
	got := screenVisibleInstanceIDs(rows)
	if len(got) != 2 {
		t.Fatalf("可见实例域应覆盖全部实例，期望 2 个，实际 %d 个: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("可见实例域出现重复 ID: %s", id)
		}
		seen[id] = true
	}
	for _, want := range []string{"ds-a", "ds-b"} {
		if !seen[want] {
			t.Fatalf("可见实例域缺少 %s，实际 %v", want, got)
		}
	}
	// 无可见实例时必须返回长度 0（GORM 对空切片生成 IN (NULL)，不匹配任何行），
	// 而不是 nil 后被误当成"不过滤"——后者会把孤儿事件全部放进来。
	if empty := screenVisibleInstanceIDs(nil); len(empty) != 0 {
		t.Fatalf("无可见实例时应返回空域，实际 %v", empty)
	}
}
