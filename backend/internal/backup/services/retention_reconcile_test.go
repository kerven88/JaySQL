package services

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// 保留期缩短需确认的错误文案：nil 明细走兜底，带明细时输出目标天数与删除数量。
func TestRetentionReconcileRequiredErrorMessage(t *testing.T) {
	cases := []struct {
		name   string
		impact *RetentionReconcileImpact
		want   string
	}{
		{
			name:   "nil 影响明细走兜底文案",
			impact: nil,
			want:   "本次保存会立即清理已有备份，请确认后重试",
		},
		{
			name:   "带明细输出目标天数与删除数量",
			impact: &RetentionReconcileImpact{TargetDays: 7, RunCount: 6, SetCount: 4},
			want:   "保存时间改为 7 天后，6 条执行记录与 4 个备份集已超出保留期，保存后将立即清理，请确认后重试",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := &RetentionReconcileRequiredError{Impact: c.impact}
			if got := err.Error(); got != c.want {
				t.Fatalf("Error() = %q, want %q", got, c.want)
			}
		})
	}
}

// 错误识别必须穿透包装，且不能把普通错误误判为需确认。
func TestAsRetentionReconcileRequiredError(t *testing.T) {
	target := &RetentionReconcileRequiredError{Impact: &RetentionReconcileImpact{TargetDays: 7}}

	if got, ok := AsRetentionReconcileRequiredError(target); !ok || got != target {
		t.Fatalf("直接传入应命中，got %v ok=%v", got, ok)
	}
	if got, ok := AsRetentionReconcileRequiredError(fmt.Errorf("wrap: %w", target)); !ok || got != target {
		t.Fatalf("包装后仍应命中，got %v ok=%v", got, ok)
	}
	if _, ok := AsRetentionReconcileRequiredError(errors.New("普通错误")); ok {
		t.Fatal("普通错误不应命中")
	}
	if _, ok := AsRetentionReconcileRequiredError(nil); ok {
		t.Fatal("nil 不应命中")
	}
}

// 影响明细的时间范围要覆盖执行记录与备份集两侧，任一侧缺失时回退到另一侧。
func TestEarlierAndLaterTime(t *testing.T) {
	early := time.Date(2026, 9, 15, 13, 12, 42, 0, time.Local)
	late := time.Date(2026, 9, 17, 1, 0, 0, 0, time.Local)

	if got := earlierTime(&late, &early); got == nil || !got.Equal(early) {
		t.Fatalf("earlierTime 应取更早者，got %v", got)
	}
	if got := laterTime(&early, &late); got == nil || !got.Equal(late) {
		t.Fatalf("laterTime 应取更晚者，got %v", got)
	}
	if got := earlierTime(nil, &early); got == nil || !got.Equal(early) {
		t.Fatalf("单侧为 nil 应回退，got %v", got)
	}
	if got := laterTime(&late, nil); got == nil || !got.Equal(late) {
		t.Fatalf("单侧为 nil 应回退，got %v", got)
	}
	if earlierTime(nil, nil) != nil || laterTime(nil, nil) != nil {
		t.Fatal("两侧都 nil 应返回 nil")
	}
}

func TestFormatImpactTime(t *testing.T) {
	if got := formatImpactTime(nil); got != "" {
		t.Fatalf("nil 应返回空串，got %q", got)
	}
	value := time.Date(2026, 9, 17, 1, 0, 20, 0, time.Local)
	if got := formatImpactTime(&value); got != "2026-09-17 01:00:20" {
		t.Fatalf("formatImpactTime = %q", got)
	}
}

// 与清理器共用同一份「进行中状态」清单，避免重算把正在写入的产物算成待删除。
func TestRetentionReconcileUsesCleanupSkipStatuses(t *testing.T) {
	expect := map[string]bool{
		"PENDING":         true,
		"RUNNING":         true,
		"STOP_REQUESTED":  true,
		"PENDING_PREPARE": true,
	}
	for _, status := range backupRunRetentionSkipStatuses {
		if !expect[status] {
			t.Fatalf("出现未预期的进行中状态：%s", status)
		}
		delete(expect, status)
	}
	if len(expect) != 0 {
		t.Fatalf("缺少进行中状态：%v", expect)
	}
}
