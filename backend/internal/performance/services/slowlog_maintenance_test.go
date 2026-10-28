package services

import (
	"reflect"
	"testing"
	"time"

	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
)

func TestDiffOrphanInstanceIDs(t *testing.T) {
	cases := []struct {
		name        string
		collectorID []string
		instanceID  []string
		want        []string
	}{
		{
			name:        "实例已删除的采集器被识别",
			collectorID: []string{"ds-a", "ds-b", "ds-c"},
			instanceID:  []string{"ds-a", "ds-c"},
			want:        []string{"ds-b"},
		},
		{
			name:        "无孤儿返回空",
			collectorID: []string{"ds-a"},
			instanceID:  []string{"ds-a", "ds-b"},
			want:        []string{},
		},
		{
			name:        "采集器去重且保序",
			collectorID: []string{"ds-b", "ds-a", "ds-b", ""},
			instanceID:  []string{},
			want:        []string{"ds-b", "ds-a"},
		},
		{
			name:        "空白字符被归一化",
			collectorID: []string{" ds-a ", "ds-b"},
			instanceID:  []string{"ds-a"},
			want:        []string{"ds-b"},
		},
		{
			name:        "全部为孤儿",
			collectorID: []string{"ds-x", "ds-y"},
			instanceID:  nil,
			want:        []string{"ds-x", "ds-y"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diffOrphanInstanceIDs(tc.collectorID, tc.instanceID)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("期望 %v，实际 %v", tc.want, got)
			}
		})
	}
}

func TestResolveRetentionDaysFromParams(t *testing.T) {
	cases := []struct {
		name     string
		params   []performanceModels.InsightPerformanceSnapshotParams
		fallback int
		want     int
	}{
		{
			name:     "无参数使用兜底值",
			params:   nil,
			fallback: 30,
			want:     30,
		},
		{
			name: "多个全局参数取最大值",
			params: []performanceModels.InsightPerformanceSnapshotParams{
				{RetentionDays: 7},
				{RetentionDays: 14},
				{RetentionDays: 3},
			},
			fallback: 30,
			want:     14,
		},
		{
			name: "参数值小于 1 视为未配置",
			params: []performanceModels.InsightPerformanceSnapshotParams{
				{RetentionDays: 0},
				{RetentionDays: -5},
			},
			fallback: 30,
			want:     30,
		},
		{
			name: "单个有效参数生效",
			params: []performanceModels.InsightPerformanceSnapshotParams{
				{RetentionDays: 7},
			},
			fallback: 30,
			want:     7,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRetentionDaysFromParams(tc.params, tc.fallback); got != tc.want {
				t.Fatalf("期望 %d，实际 %d", tc.want, got)
			}
		})
	}
}

func TestSlowLogRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 9, 15, 17, 30, 0, 0, time.Local)

	cases := []struct {
		name string
		days int
		want time.Time
	}{
		{name: "7 天", days: 7, want: time.Date(2026, 9, 8, 17, 30, 0, 0, time.Local)},
		{name: "30 天", days: 30, want: time.Date(2026, 8, 16, 17, 30, 0, 0, time.Local)},
		{name: "跨月 1 天", days: 1, want: time.Date(2026, 9, 14, 17, 30, 0, 0, time.Local)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slowLogRetentionCutoff(now, tc.days); !got.Equal(tc.want) {
				t.Fatalf("期望 %s，实际 %s", tc.want, got)
			}
		})
	}
}
