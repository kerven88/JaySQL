package services

// ============================================================================
// 定时执行的进程内精确定时器
//
// 背景（工单 tk-6108asmh5no3 实测）：
//   计划执行时间 16:31:51，实际 16:32:46 才触发，延迟 55 秒；
//   而任务结果里 execute_cost_time 只有 57ms，页面据此显示"耗时几十毫秒"，
//   两个数字对不上，用户无法判断时间花在哪。
//
// 延迟根因：调度器原本只有 internal/orders/tasks 里的 cron 轮询（@every 1m）。
//   robfig/cron 的 @every 从「进程启动时刻」起算，与系统时钟整分不对齐，
//   到期工单必须等到下一次 tick 才被扫到 —— 理论延迟区间 [0, 60s]，平均 30s。
//   单纯把周期调小只能把上界压到周期长度（10s 周期仍有 ≤10s 延迟），做不到秒级。
//
// 解法：双轨
//   1) 登记定时计划时注册一个进程内 time.Timer，到点直接认领并执行 —— 秒级精度；
//   2) cron 轮询保留（周期缩短）作为兜底：
//      进程重启会丢失内存定时器，重启后由轮询兜住尚未触发的计划。
//
// 重复触发防护：两条轨道都先走 ClaimScheduledOrder（把 execution_mode 从
//   SCHEDULED 原子改回 IMMEDIATE），只有 UPDATE 影响 1 行的调用者才取得执行权，
//   因此定时器与轮询同时到点、或多实例部署，都只会执行一次。
// ============================================================================

import (
	"errors"
	"sync"
	"time"

	ordersModels "github.com/simajiong/jaysql/internal/orders/models"

	"github.com/simajiong/jaysql/internal/global"
)

var (
	scheduledTimersMu sync.Mutex
	// scheduledTimers 记录已注册的进程内定时触发点，key 为 order_id
	scheduledTimers = make(map[string]*time.Timer)
)

// ClaimScheduledOrder 原子认领一个定时工单：
// 仅当记录仍处于 SCHEDULED 时把 execution_mode 改回 IMMEDIATE。
// 返回 true 表示本次调用抢到了执行权（UPDATE 影响 1 行）。
//
// 注意：**不清空 scheduled_at**。计划时间是审计与前端展示「定时等待多久」的依据；
// 调度器的扫描条件含 execution_mode='SCHEDULED'，改回 IMMEDIATE 后自然不再被扫到，
// 保留该列不会造成重复触发。
func ClaimScheduledOrder(orderID string) (bool, error) {
	result := global.App.DB.Model(&ordersModels.InsightOrderRecords{}).
		Where("order_id = ? AND execution_mode = ?", orderID, ExecutionModeScheduled).
		Update("execution_mode", ExecutionModeImmediate)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// RegisterScheduledPlan 为一条定时工单注册进程内触发点。
// 到点即尝试认领并执行；若此时工单已被撤回/手动执行，认领会失败，静默跳过。
func RegisterScheduledPlan(orderID string, scheduledAt time.Time) {
	scheduledTimersMu.Lock()
	defer scheduledTimersMu.Unlock()

	// 重复登记时先停掉旧定时器，保证一个工单只有一个触发点
	if old, ok := scheduledTimers[orderID]; ok {
		old.Stop()
		delete(scheduledTimers, orderID)
	}

	delay := time.Until(scheduledAt)
	if delay < 0 {
		delay = 0
	}
	scheduledTimers[orderID] = time.AfterFunc(delay, func() {
		scheduledTimersMu.Lock()
		delete(scheduledTimers, orderID)
		scheduledTimersMu.Unlock()

		fireScheduledPlan(orderID)
	})
}

// CancelScheduledPlan 取消已登记的进程内触发点（工单被撤回、改为立即/手动执行时调用）
func CancelScheduledPlan(orderID string) {
	scheduledTimersMu.Lock()
	defer scheduledTimersMu.Unlock()

	if timer, ok := scheduledTimers[orderID]; ok {
		timer.Stop()
		delete(scheduledTimers, orderID)
	}
}

// PendingScheduledPlanCount 当前进程内待触发的定时计划数量（便于排障与测试）
func PendingScheduledPlanCount() int {
	scheduledTimersMu.Lock()
	defer scheduledTimersMu.Unlock()
	return len(scheduledTimers)
}

// fireScheduledPlan 定时器到点后的执行入口：先认领，抢到才真正执行。
func fireScheduledPlan(orderID string) {
	claimed, err := ClaimScheduledOrder(orderID)
	if err != nil {
		global.App.Log.Warnf("claim scheduled order %s failed: %v", orderID, err)
		return
	}
	if !claimed {
		// 已被轮询轨道 / 其它实例 / 用户手动执行认领
		return
	}
	if err := ExecuteScheduledOrder(orderID); err != nil {
		// 只对「工单正忙」这类可重试失败写回计划，让下一轮继续尝试；
		// 其余失败（状态不允许、数据异常）不写回，避免无意义重试打日志。
		if errors.Is(err, ErrOrderBusy) || errors.Is(err, ErrOrderTaskExecuting) {
			global.App.Log.Warnf("execute scheduled order %s deferred (busy): %v", orderID, err)
			RestoreScheduledPlan(orderID)
			return
		}
		global.App.Log.Warnf("execute scheduled order %s failed: %v", orderID, err)
	}
}

// LoadPendingScheduledPlans 从库里加载所有未触发的定时计划并注册进程内定时器。
// 仅在服务启动时调用一次：进程重启会丢失内存定时器，靠它恢复。
// 已经过期的计划会被立即注册（delay<=0 → 马上触发），不会因重启而丢失。
func LoadPendingScheduledPlans() {
	var records []ordersModels.InsightOrderRecords
	if err := global.App.DB.
		Where("execution_mode = ?", ExecutionModeScheduled).
		Where("scheduled_at IS NOT NULL").
		Find(&records).Error; err != nil {
		global.App.Log.Warnf("load pending scheduled plans failed: %v", err)
		return
	}
	for _, record := range records {
		scheduledAt := time.Time(record.ScheduledAt)
		if scheduledAt.IsZero() {
			continue
		}
		RegisterScheduledPlan(record.OrderID, scheduledAt)
	}
	if len(records) > 0 {
		global.App.Log.Infof("restored %d pending scheduled order plan(s)", len(records))
	}
}
