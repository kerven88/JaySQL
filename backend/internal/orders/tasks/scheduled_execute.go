// Package tasks 承载工单模块的定时任务。
//
// 这里目前只有一件事：驱动「定时执行」工单。
// 背景：执行弹窗的「执行方式」此前是纯装饰（表单收了参数、服务层从不读取），
// 三种方式都走同步立即执行。改造后 SCHEDULED 只在提交时落计划，真正的触发点是：
//
//  1. 进程内定时器（internal/orders/services/scheduler.go）—— 登记计划时注册，
//     到点秒级触发，是精度保障的主路径；
//  2. 本包注册的轮询扫描 —— 兜底路径。进程重启会丢失内存定时器，
//     重启后由轮询兜住尚未触发的计划；周期越短，兜底延迟越小。
//
// 为什么要双轨：cron 的 @every 从「进程启动时刻」起算，与系统时钟整分不对齐，
// 只靠轮询时到期工单必须等下一次 tick，延迟区间等于一个轮询周期
// （历史 @every 1m 实测延迟 55s：tk-6108asmh5no3 计划 16:31:51、实际 16:32:46 触发）。
package tasks

import (
	"errors"
	"time"

	ordersModels "github.com/simajiong/jaysql/internal/orders/models"
	ordersServices "github.com/simajiong/jaysql/internal/orders/services"

	"github.com/simajiong/jaysql/internal/global"
)

const (
	// scheduledScanLimit 单轮最多触发的工单数：避免某个时间点堆积大量定时工单时，
	// 一轮扫描把请求/连接全部占满（剩余工单在下一轮继续）。
	scheduledScanLimit = 10
	// orderTimeLayout 与工单模块统一的时间格式
	orderTimeLayout = "2006-01-02 15:04:05"
	// scheduledScanInterval 兜底轮询周期。
	// 主路径是进程内定时器（秒级），轮询只为覆盖「进程重启后内存定时器丢失」的场景，
	// 周期取 10s：既把兜底延迟压到 10s 内，扫描成本也可忽略（有 idx_execution_mode 索引）。
	scheduledScanInterval = "@every 10s"
)

// RegisterOrderCronJobs 注册工单相关的定时任务（由 bootstrap.InitializeCron 调用）
func RegisterOrderCronJobs() {
	if global.App.Cron == nil {
		return
	}
	// 启动时先把库里未触发的定时计划挂回内存定时器（上次进程遗留的）
	ordersServices.LoadPendingScheduledPlans()

	if _, err := global.App.Cron.AddFunc(scheduledScanInterval, func() {
		RunScheduledOrderExecutions()
	}); err != nil {
		global.App.Log.Warnf("register scheduled order execution cron failed: %v", err)
	}
}

// claimScheduledOrder 原子认领一个定时工单（薄封装，实际实现在 services 包，
// 便于进程内定时器与轮询两条轨道共用同一套幂等约束）。
func claimScheduledOrder(orderID string) (bool, error) {
	return ordersServices.ClaimScheduledOrder(orderID)
}

// RunScheduledOrderExecutions 扫描到期的「定时执行」工单并触发执行（兜底路径）。
//
// 幂等与并发安全依赖两处：
//  1. 认领（claim）：先把 execution_mode 从 SCHEDULED 原子改回 IMMEDIATE，
//     只有 UPDATE 影响行数为 1 的那个执行者才真正触发，
//     多实例部署 / 同一轮重复扫描 / 与进程内定时器撞车都不会重复执行；
//  2. 执行侧仍保留 Redis 工单锁（order:lock:<order_id>），防止与用户手动执行叠加。
//
// 只扫描 progress 允许执行的工单（APPROVED/FAILED），审批中或已完成的一律不动。
func RunScheduledOrderExecutions() {
	now := time.Now().Format(orderTimeLayout)

	var records []ordersModels.InsightOrderRecords
	if err := global.App.DB.
		Where("execution_mode = ?", ordersServices.ExecutionModeScheduled).
		Where("scheduled_at IS NOT NULL AND scheduled_at <= ?", now).
		Where("progress IN ?", []string{"APPROVED", "FAILED"}).
		Order("scheduled_at ASC").
		Limit(scheduledScanLimit).
		Find(&records).Error; err != nil {
		global.App.Log.Warnf("scan scheduled orders failed: %v", err)
		return
	}
	if len(records) == 0 {
		return
	}

	for _, record := range records {
		// 原子认领：把计划状态清掉，抢到的才执行
		claimed, err := claimScheduledOrder(record.OrderID)
		if err != nil {
			global.App.Log.Warnf("claim scheduled order %s failed: %v", record.OrderID, err)
			continue
		}
		if !claimed {
			// 已被其它实例/上一轮认领
			continue
		}

		global.App.Log.Infof("trigger scheduled order %s (planned at %s)", record.OrderID, time.Time(record.ScheduledAt).Format(orderTimeLayout))
		if err := ordersServices.ExecuteScheduledOrder(record.OrderID); err != nil {
			// 只对「工单正忙」这类可重试失败写回计划，让下一轮继续尝试；
			// 其余失败（状态不允许、数据异常）不写回，避免每分钟无意义重试打日志。
			if errors.Is(err, ordersServices.ErrOrderBusy) || errors.Is(err, ordersServices.ErrOrderTaskExecuting) {
				global.App.Log.Warnf("execute scheduled order %s deferred (busy): %v", record.OrderID, err)
				ordersServices.RestoreScheduledPlan(record.OrderID)
				continue
			}
			global.App.Log.Warnf("execute scheduled order %s failed: %v", record.OrderID, err)
		}
	}
}
