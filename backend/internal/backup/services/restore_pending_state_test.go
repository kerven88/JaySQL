package services

import (
	"testing"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// 计划态幽灵任务的判定。
//
// 背景：PLAN 模式创建的恢复任务落库后没有任何协程接管（restores.go:257 只在
// plan.AutoExecute 为真时起协程，而 PLAN 模式该值恒为 false），记录永久停在 PENDING。
// 删除（restore_control.go:410）与重跑（:235）又都按 status 黑名单拒绝 PENDING，
// 于是它既不能删也不能跑 —— 放开删除的唯一依据就是这个判据。
// 判错方向的后果：漏判 → 幽灵任务继续堆积；误判 → 删掉正在排队执行的真实任务。
func TestIsRestoreRunPendingOrphan(t *testing.T) {
	now := nowLocalTime()
	toLocal := func(d time.Duration) commonModels.LocalTime {
		return commonModels.LocalTime(now.Add(d))
	}
	zero := commonModels.LocalTime(time.Time{})

	build := func(status string, startAt commonModels.LocalTime, updatedAt commonModels.LocalTime) backupModels.InsightRestoreRuns {
		// 嵌入的 *commonModels.Model 必须显式初始化：updated_at 走嵌入字段，
		// Model 为 nil 时读写会 panic（本项目已多次踩雷）。
		return backupModels.InsightRestoreRuns{
			Model:   &commonModels.Model{UpdatedAt: updatedAt},
			Status:  commonModels.EnumType(status),
			StartAt: startAt,
			LogPath: "",
			TaskID:  "rs-unittest",
		}
	}

	cases := []struct {
		name   string
		run    backupModels.InsightRestoreRuns
		expect bool
	}{
		{
			name:   "PLAN 模式残留：PENDING、从未启动、静默数天 —— 应当可清理",
			run:    build("PENDING", zero, toLocal(-120*time.Hour)),
			expect: true,
		},
		{
			name:   "刚落库的 PENDING：静默不足阈值，协程可能马上接管 —— 不能清理",
			run:    build("PENDING", zero, toLocal(-3*time.Second)),
			expect: false,
		},
		{
			name:   "已被接管的 PENDING：start_at 非空（协程已写入开始时间）—— 不能清理",
			run:    build("PENDING", toLocal(-10*time.Second), toLocal(-10*time.Second)),
			expect: false,
		},
		{
			name:   "RUNNING —— 一律不算幽灵",
			run:    build("RUNNING", zero, toLocal(-120*time.Hour)),
			expect: false,
		},
		{
			name:   "STOP_REQUESTED —— 一律不算幽灵（由 restore_stop_state.go 的收口链路负责）",
			run:    build("STOP_REQUESTED", zero, toLocal(-120*time.Hour)),
			expect: false,
		},
		{
			name:   "SUCCESS —— 一律不算幽灵",
			run:    build("SUCCESS", zero, toLocal(-120*time.Hour)),
			expect: false,
		},
	}

	for _, c := range cases {
		if got := isRestoreRunPendingOrphan(c.run); got != c.expect {
			t.Errorf("%s: 期望 %v，实际 %v", c.name, c.expect, got)
		}
	}
}

// 嵌入 Model 为 nil 时不能 panic：调用方均来自 DB 查询（GORM 会填充 Model），
// 但这条防御能避免未来有人在手构造实例时踩坑。
func TestIsRestoreRunPendingOrphanNilModel(t *testing.T) {
	run := backupModels.InsightRestoreRuns{Status: commonModels.EnumType("PENDING")}
	if !isRestoreRunPendingOrphan(run) {
		t.Errorf("Model 为 nil 且 start_at 为零值时，应按无人接管处理")
	}
}
