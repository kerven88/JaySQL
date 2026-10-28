package tasks

import (
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	ordersServices "github.com/simajiong/jaysql/internal/orders/services"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 定时执行调度器的「认领」幂等回归
//
// 调度器每分钟扫描一次、且可能多实例部署，因此「同一条工单被触发两次」是必须挡住的风险。
// 挡法是把 execution_mode 从 SCHEDULED 原子改回 IMMEDIATE：
// 只有 UPDATE 影响 1 行的那个调用者才取得执行权。
// ============================================================================

const createClaimTestSchemaSQL = `
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

func setupClaimTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:scheduled_claim_test?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(createClaimTestSchemaSQL).Error; err != nil {
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

func seedOrder(t *testing.T, db *gorm.DB, orderID, mode string, scheduledAt time.Time) {
	t.Helper()
	if err := db.Exec(`INSERT INTO insight_order_records (order_id, progress, executor, execution_mode, scheduled_at)
		VALUES (?, 'APPROVED', 'dba', ?, ?)`,
		orderID, mode, scheduledAt.Format(orderTimeLayout)).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
}

func readMode(t *testing.T, db *gorm.DB, orderID string) (string, *time.Time) {
	t.Helper()
	var mode string
	var scheduledAt *time.Time
	row := db.Raw(`SELECT execution_mode, scheduled_at FROM insight_order_records WHERE order_id = ?`, orderID).Row()
	if row == nil {
		t.Fatalf("order %s not found", orderID)
	}
	if err := row.Scan(&mode, &scheduledAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return mode, scheduledAt
}

// 同一条定时工单只能被认领一次：第二次认领必须失败（模拟下一轮扫描 / 另一台实例）
func TestClaimScheduledOrderIsIdempotent(t *testing.T) {
	db := setupClaimTestDB(t)
	const orderID = "k_claim00000001"
	seedOrder(t, db, orderID, ordersServices.ExecutionModeScheduled, time.Now().Add(-1*time.Minute))

	first, err := claimScheduledOrder(orderID)
	if err != nil {
		t.Fatalf("首次认领报错: %v", err)
	}
	if !first {
		t.Fatal("首次认领应成功")
	}
	mode, scheduledAt := readMode(t, db, orderID)
	if mode != ordersServices.ExecutionModeImmediate {
		t.Fatalf("认领后 execution_mode = %q, want IMMEDIATE", mode)
	}
	// 计划时间必须保留：它是「定时等待多久」的审计依据，也是页面展示计划执行时间的来源。
	// 早期实现会在认领时清空该列，导致执行日志拿不到计划时间、前端也无法回溯。
	if scheduledAt == nil {
		t.Fatal("认领后 scheduled_at 应保留，实际被清空")
	}

	second, err := claimScheduledOrder(orderID)
	if err != nil {
		t.Fatalf("二次认领报错: %v", err)
	}
	if second {
		t.Fatal("同一条工单被重复认领，定时工单会执行两次")
	}
}

// 非 SCHEDULED 工单不能被认领（避免手动/立即执行的工单被调度器抢走）
func TestClaimScheduledOrderSkipsNonScheduled(t *testing.T) {
	db := setupClaimTestDB(t)
	const orderID = "k_claim00000002"
	seedOrder(t, db, orderID, ordersServices.ExecutionModeManual, time.Now().Add(time.Hour))

	claimed, err := claimScheduledOrder(orderID)
	if err != nil {
		t.Fatalf("认领报错: %v", err)
	}
	if claimed {
		t.Fatal("手动执行模式的工单不应被调度器认领")
	}
	if mode, _ := readMode(t, db, orderID); mode != ordersServices.ExecutionModeManual {
		t.Fatalf("非定时工单被改写: %q", mode)
	}
}
