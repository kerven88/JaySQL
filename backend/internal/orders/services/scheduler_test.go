package services

// ============================================================================
// 定时执行调度器的回归测试
//
// 覆盖两件事：
//  1. 认领（claim）后必须保留 scheduled_at —— 它是「定时等待多久」的展示与审计依据；
//     同时二次认领必须失败，否则定时工单会执行两次。
//  2. 进程内定时器可注册、可取消 —— 工单改为立即/手动执行后不应残留触发点。
// ============================================================================

import (
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const createSchedulerTestSchemaSQL = `
DROP TABLE IF EXISTS insight_order_records;
CREATE TABLE insight_order_records (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at     datetime,
    updated_at     datetime,
    order_id       varchar(15),
    progress       varchar(32),
    executor       varchar(32),
    execution_mode varchar(16) DEFAULT 'IMMEDIATE',
    scheduled_at   datetime
);`

func setupSchedulerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:scheduler_test?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(createSchedulerTestSchemaSQL).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	prevDB, prevLog := global.App.DB, global.App.Log
	global.App.DB = db
	global.App.Log = logrus.New()
	t.Cleanup(func() {
		global.App.DB = prevDB
		global.App.Log = prevLog
	})
	return db
}

// 认领后保留计划时间，且只能被认领一次
func TestClaimScheduledOrderKeepsPlanTime(t *testing.T) {
	db := setupSchedulerTestDB(t)
	const orderID = "k_sched0000001"
	planned := time.Now().Add(-1 * time.Minute)
	if err := db.Exec(`INSERT INTO insight_order_records (order_id, progress, executor, execution_mode, scheduled_at)
		VALUES (?, 'APPROVED', 'dba', ?, ?)`, orderID, ExecutionModeScheduled, planned.Format(orderTimeLayout)).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}

	first, err := ClaimScheduledOrder(orderID)
	if err != nil {
		t.Fatalf("首次认领报错: %v", err)
	}
	if !first {
		t.Fatal("首次认领应成功")
	}

	var mode string
	var scheduledAt *time.Time
	row := db.Raw(`SELECT execution_mode, scheduled_at FROM insight_order_records WHERE order_id = ?`, orderID).Row()
	if err := row.Scan(&mode, &scheduledAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if mode != ExecutionModeImmediate {
		t.Fatalf("认领后 execution_mode = %q, want IMMEDIATE", mode)
	}
	if scheduledAt == nil {
		t.Fatal("认领后 scheduled_at 被清空：计划时间丢失，页面无法展示「定时等待多久」，执行日志也不再带计划时间")
	}

	second, err := ClaimScheduledOrder(orderID)
	if err != nil {
		t.Fatalf("二次认领报错: %v", err)
	}
	if second {
		t.Fatal("同一条工单被重复认领，定时工单会执行两次")
	}
}

// 进程内定时器可注册、可取消
func TestRegisterAndCancelScheduledPlan(t *testing.T) {
	setupSchedulerTestDB(t)
	const orderID = "k_sched0000002"

	t.Cleanup(func() { CancelScheduledPlan(orderID) })

	// 断言用相对增量：包内其它用例也可能注册过定时器，绝对值没有意义
	before := PendingScheduledPlanCount()

	RegisterScheduledPlan(orderID, time.Now().Add(time.Hour))
	if got := PendingScheduledPlanCount(); got != before+1 {
		t.Fatalf("注册后待触发计划数 = %d, want %d", got, before+1)
	}

	// 重复注册同一个工单不应产生第二个触发点
	RegisterScheduledPlan(orderID, time.Now().Add(2*time.Hour))
	if got := PendingScheduledPlanCount(); got != before+1 {
		t.Fatalf("重复注册后待触发计划数 = %d, want %d", got, before+1)
	}

	CancelScheduledPlan(orderID)
	if got := PendingScheduledPlanCount(); got != before {
		t.Fatalf("取消后待触发计划数 = %d, want %d", got, before)
	}
}
