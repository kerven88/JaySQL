package services

import (
	"testing"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// 恢复任务「终止请求是否失联」的判定。
//
// 背景：STOP_REQUESTED 是一个无出口的中间态 —— 删除拒绝（算运行中）、
// 编辑/执行拒绝（只认 PENDING）、重跑拒绝（算执行中），唯一的收敛路径是执行协程自己退出。
// 协程一旦死了（服务重启 / 崩溃），任务就永久卡死，所以必须有兜底收口。
// 收口的正确性全押在这个判据上：误判会杀掉正在正常收尾的任务，漏判则解锁不了。
func TestRestoreStopRequestStale(t *testing.T) {
	now := nowLocalTime()
	toLocal := func(d time.Duration) commonModels.LocalTime {
		return commonModels.LocalTime(now.Add(d))
	}
	zero := commonModels.LocalTime(time.Time{})

	cases := []struct {
		name         string
		lastReportAt commonModels.LocalTime
		updatedAt    commonModels.LocalTime
		expect       bool
	}{
		{
			name:         "刚下发终止请求，协程还在收尾，不能收口",
			lastReportAt: zero,
			updatedAt:    toLocal(-5 * time.Second),
			expect:       false,
		},
		{
			name:         "协程仍在持续上报进度，即使整体耗时很长也不能收口",
			lastReportAt: toLocal(-10 * time.Second),
			updatedAt:    toLocal(-3 * time.Hour), // 模拟长期未整体写库、但一直在上报进度
			expect:       false,
		},
		{
			name:         "两个时间戳都停更且超过阈值，判定失联",
			lastReportAt: toLocal(-restoreStopRequestedStaleThreshold - time.Minute),
			updatedAt:    toLocal(-restoreStopRequestedStaleThreshold - time.Minute),
			expect:       true,
		},
		{
			name:         "last_report_at 为空时退化为只看 updated_at",
			lastReportAt: zero,
			updatedAt:    toLocal(-restoreStopRequestedStaleThreshold - time.Minute),
			expect:       true,
		},
		{
			name:         "刚过阈值边界也应收口",
			lastReportAt: zero,
			updatedAt:    toLocal(-restoreStopRequestedStaleThreshold),
			expect:       true,
		},
		{
			name:         "时间戳全为零值不得判定失联（缺数据不能等于失联）",
			lastReportAt: zero,
			updatedAt:    zero,
			expect:       false,
		},
		{
			name:         "updated_at 比 last_report_at 新时以较新者为准",
			lastReportAt: toLocal(-10 * time.Minute),
			updatedAt:    toLocal(-1 * time.Minute),
			expect:       false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := restoreStopRequestStale(c.lastReportAt, c.updatedAt); got != c.expect {
				t.Fatalf("restoreStopRequestStale() = %v, want %v", got, c.expect)
			}
		})
	}
}

// 阈值必须显著大于备份侧的同类阈值：恢复链路存在长时间不检查的白块
// （mysql 导入大事务时主协程阻塞在 cmd.Wait()），阈值太小会误杀正常任务。
func TestRestoreStopThresholdIsConservative(t *testing.T) {
	if restoreStopRequestedStaleThreshold < time.Minute {
		t.Fatalf("恢复任务终止收口阈值 %v 过小，容易误杀正在正常收尾的任务", restoreStopRequestedStaleThreshold)
	}
	if restoreStopRequestedStaleThreshold <= stopRequestedStaleThreshold {
		t.Fatalf(
			"恢复侧阈值 %v 必须大于备份侧 %v：恢复执行阶段存在长时间不轮询的空档",
			restoreStopRequestedStaleThreshold, stopRequestedStaleThreshold,
		)
	}
}
