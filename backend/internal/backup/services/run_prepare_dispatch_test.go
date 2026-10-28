package services

import (
	"testing"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 定时 + PG 物理备份「执行记录卡在 PENDING_PREPARE」的回归
//
// 背景：PG 物理备份（PG_BACKREST）的执行记录初始状态是 PENDING_PREPARE，
// 而执行节点只领取 status='PENDING' 的记录，因此必须由服务端驱动 run 级 prepare
// 才能推进到 PENDING。历史缺陷是定时调度路径漏了这一步，记录永远停在
// PENDING_PREPARE，10 分钟后被 cleanupStalePendingPrepareRuns 收口成
// prepare_stage=stale_pending_prepare 的「假失败」（start_at 为 NULL）。
//
// 说明：这里不用 AutoMigrate —— 模型上的 MySQL ENUM 类型标签在 SQLite 里是非法 DDL
// （near "'PENDING'": syntax error），直接用最小可用的建表语句即可覆盖被测查询涉及的列。
// ============================================================================

const createRunsTableSQL = `
DROP TABLE IF EXISTS insight_backup_runs;
CREATE TABLE insight_backup_runs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at     datetime,
    updated_at     datetime,
    task_id        varchar(15),
    status         varchar(32),
    driver_type    varchar(32),
    prepare_status varchar(32)
);`

func setupPendingPrepareTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:prepare_dispatch_test?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(createRunsTableSQL).Error; err != nil {
		t.Fatalf("create insight_backup_runs: %v", err)
	}
	prev := global.App.DB
	global.App.DB = db
	t.Cleanup(func() { global.App.DB = prev })
	return db
}

// seedRun 插入一条执行记录，并把 updated_at 回写成「age 之前」，
// 以模拟「记录创建后就再没被任何人碰过」。
func seedRun(t *testing.T, db *gorm.DB, status string, prepareStatus string, driver string, age time.Duration) uint64 {
	t.Helper()
	ts := nowLocalTime().Add(-age)
	if err := db.Table("insight_backup_runs").Create(map[string]any{
		"created_at":     ts,
		"updated_at":     ts,
		"task_id":        "bk-testtask001",
		"status":         status,
		"driver_type":    driver,
		"prepare_status": prepareStatus,
	}).Error; err != nil {
		t.Fatalf("seed run (%s/%s): %v", status, prepareStatus, err)
	}
	var id uint64
	if err := db.Table("insight_backup_runs").Select("COALESCE(MAX(id), 0)").Row().Scan(&id); err != nil {
		t.Fatalf("read seeded id: %v", err)
	}
	return id
}

// 兜底扫描只应挑出「从未启动过 prepare」的记录：
// status=PENDING_PREPARE 且 prepare_status 仍为 NONE，且已过宽限期。
func TestListStrandedPendingPrepareRunIDs(t *testing.T) {
	db := setupPendingPrepareTestDB(t)

	neverStarted := seedRun(t, db, "PENDING_PREPARE", "NONE", "PG_BACKREST", 5*time.Minute)
	_ = seedRun(t, db, "PENDING_PREPARE", "PREPARING", "PG_BACKREST", 5*time.Minute)
	_ = seedRun(t, db, "PENDING_PREPARE", "PREPARE_OK", "PG_BACKREST", 5*time.Minute)
	_ = seedRun(t, db, "PENDING_PREPARE", "PREPARE_ERROR", "PG_BACKREST", 5*time.Minute)
	_ = seedRun(t, db, "PENDING_PREPARE", "NONE", "PG_BACKREST", 10*time.Second)
	_ = seedRun(t, db, "PENDING", "NONE", "PG_BACKREST", 5*time.Minute)
	_ = seedRun(t, db, "FAILED", "NONE", "PG_BACKREST", 5*time.Minute)
	_ = seedRun(t, db, "RUNNING", "NONE", "PG_BACKREST", 5*time.Minute)

	ids, err := listStrandedPendingPrepareRunIDs()
	if err != nil {
		t.Fatalf("list stranded: %v", err)
	}
	if len(ids) != 1 || ids[0] != neverStarted {
		t.Fatalf("want only run %d, got %v", neverStarted, ids)
	}
}

// 宽限期内的记录不能被兜底驱动：创建侧的 goroutine 可能还在准备中。
func TestListStrandedPendingPrepareRunIDsRespectsGrace(t *testing.T) {
	db := setupPendingPrepareTestDB(t)
	_ = seedRun(t, db, "PENDING_PREPARE", "NONE", "PG_BACKREST", strandedPendingPrepareGrace-time.Second)

	ids, err := listStrandedPendingPrepareRunIDs()
	if err != nil {
		t.Fatalf("list stranded: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("run inside grace window must not be dispatched, got %v", ids)
	}
}

// DispatchBackupRunPrepare 必须对空输入安全（调度路径在某些分支会拿到 nil / 空壳结构体）。
func TestDispatchBackupRunPrepareNilSafe(t *testing.T) {
	DispatchBackupRunPrepare(nil)
	DispatchBackupRunPrepare(&backupModels.InsightBackupRuns{})
	DispatchBackupRunPrepare(&backupModels.InsightBackupRuns{Model: &commonModels.Model{}})
}
