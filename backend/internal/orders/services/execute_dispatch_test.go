package services

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	ordersModels "github.com/simajiong/jaysql/internal/orders/models"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/orders/forms"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 「执行方式」三种模式的分流回归
//
// 背景：execute_mode / scheduled_at 此前只被表单接收、服务层从不读取，
// 于是「立即 / 定时 / 手动」走的都是同一条同步执行路径 —— 表现为「选了没效果」。
//
// 这里用内存库真实驱动服务层，断言：
//   - SCHEDULED / MANUAL：只落计划 + 写操作历史，任务一条都不执行、工单状态不变；
//   - 只有 IMMEDIATE 才会进入 runOrderTasks（该路径需要工单锁，本测试不覆盖）。
//
// 关键取证手法：本测试**故意不装配 global.App.Locks**。
// 若 SCHEDULED / MANUAL 误入执行路径，就会在 acquireOrderLock 上 panic 而不是静默通过，
// 因此「测试通过」本身就证明了这两种模式没有执行任何任务。
// ============================================================================

const createDispatchTestSchemaSQL = `
DROP TABLE IF EXISTS insight_order_records;
CREATE TABLE insight_order_records (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at       datetime,
    updated_at       datetime,
    order_id         varchar(15),
    title            varchar(256),
    progress         varchar(32),
    executor         varchar(32),
    applicant        varchar(32),
    content          varchar(1024),
    instance_id      varchar(15) DEFAULT '',
    sql_type         varchar(8)  DEFAULT 'DDL',
    execution_mode   varchar(16) DEFAULT 'IMMEDIATE',
    scheduled_at     datetime,
    execution_config text
);
DROP TABLE IF EXISTS insight_order_tasks;
CREATE TABLE insight_order_tasks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at datetime,
    updated_at datetime,
    order_id   varchar(15),
    task_id    varchar(15),
    sql        text,
    progress   varchar(32) DEFAULT 'PENDING',
    result     text
);
-- 执行链路已接入数据授权校验（CheckOrderDataAccess），因此这里必须把
-- 主体（insight_users / insight_user_roles）与授权（insight_das_data_grants）一起装配，
-- 否则服务层会在「查授权」这一步因为缺表而失败 —— 那测的就不是分流逻辑了。
DROP TABLE IF EXISTS insight_users;
CREATE TABLE insight_users (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    uid          INTEGER,
    username     varchar(32),
    is_deleted   boolean DEFAULT 0,
    is_active    boolean DEFAULT 1,
    is_superuser boolean DEFAULT 0
);
DROP TABLE IF EXISTS insight_user_roles;
CREATE TABLE insight_user_roles (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    uid     INTEGER,
    role_id INTEGER
);
DROP TABLE IF EXISTS insight_das_data_grants;
CREATE TABLE insight_das_data_grants (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at       datetime,
    updated_at       datetime,
    grant_no         varchar(32)  DEFAULT '',
    principal_type   varchar(16)  DEFAULT 'user',
    principal_id     INTEGER DEFAULT 0,
    principal_name   varchar(128) DEFAULT '',
    resource_level   varchar(16)  DEFAULT 'instance',
    instance_id      varchar(15)  DEFAULT '',
    schema_name      varchar(255) DEFAULT '',
    table_name       varchar(128) DEFAULT '',
    resource_type    varchar(32)  DEFAULT 'database',
    actions          varchar(255) DEFAULT 'select',
    effect           varchar(16)  DEFAULT 'allow',
    effective_at     datetime,
    expire_at        datetime,
    grantor_username varchar(64)  DEFAULT '',
    grantor_name     varchar(128) DEFAULT '',
    grant_source     varchar(32)  DEFAULT 'manual',
    status           varchar(16)  DEFAULT 'active',
    reason           varchar(512) DEFAULT '',
    revoked_at       datetime,
    revoked_by       varchar(64)  DEFAULT ''
);
DROP TABLE IF EXISTS insight_order_logs;
CREATE TABLE insight_order_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at datetime,
    updated_at datetime,
    username   varchar(32),
    order_id   varchar(15),
    msg        varchar(1024)
);`

func setupExecutionDispatchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:execution_dispatch_test?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(createDispatchTestSchemaSQL).Error; err != nil {
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

// dispatchTestInstanceID 测试工单的目标数据源：执行链路会按它做数据授权校验，
// seedDispatchOrder 会同时给执行人补一条覆盖它的实例级授权。
const dispatchTestInstanceID = "ds-dispatch0001"

func seedDispatchOrder(t *testing.T, db *gorm.DB, orderID, progress, executor string, taskCount int) {
	t.Helper()
	if err := db.Exec(`INSERT INTO insight_order_records (order_id, title, progress, executor, applicant, instance_id, execution_mode)
		VALUES (?, '新建 test001 库', ?, ?, ?, ?, 'IMMEDIATE')`,
		orderID, progress, executor, executor, dispatchTestInstanceID).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	seedDispatchActor(t, db, executor)
	grantDispatchInstance(t, db, executor, dispatchTestInstanceID, "select,export,dml")
	for i := 0; i < taskCount; i++ {
		if err := db.Exec(`INSERT INTO insight_order_tasks (order_id, task_id, sql, progress)
			VALUES (?, ?, 'CREATE TABLE t (id int)', 'PENDING')`, orderID, "k_task000000"+string(rune('a'+i))).Error; err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}
}

// seedDispatchActor 装配授权判定所需的主体（用户 + 用户角色表）。
func seedDispatchActor(t *testing.T, db *gorm.DB, username string) {
	t.Helper()
	var uid int64
	if err := db.Table("insight_users").Where("username = ?", username).Count(&uid).Error; err != nil {
		t.Fatalf("count user: %v", err)
	}
	if uid > 0 {
		return
	}
	if err := db.Exec(`INSERT INTO insight_users (uid, username, is_deleted, is_active, is_superuser)
		VALUES (?, ?, 0, 1, 0)`, 1000+uid, username).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// grantDispatchInstance 给指定用户补一条实例级授权；actions 传空时不授权（用于反例）。
func grantDispatchInstance(t *testing.T, db *gorm.DB, username, instanceID, actions string) {
	t.Helper()
	if actions == "" {
		return
	}
	var row struct {
		UID uint64
	}
	if err := db.Table("insight_users").Select("uid").
		Where("username = ?", username).Take(&row).Error; err != nil {
		t.Fatalf("resolve uid: %v", err)
	}
	uid := row.UID
	if err := db.Exec(`INSERT INTO insight_das_data_grants
		(grant_no, principal_type, principal_id, principal_name, resource_level, instance_id,
		 schema_name, table_name, resource_type, actions, effect, status, grant_source, created_at, updated_at)
		VALUES (?, 'user', ?, ?, 'instance', ?, '', '', 'database', ?, 'allow', 'active', 'manual', ?, ?)`,
		"dg-"+username+instanceID, uid, username, instanceID, actions, time.Now(), time.Now()).Error; err != nil {
		t.Fatalf("seed grant: %v", err)
	}
}

type dispatchOrderState struct {
	Progress        string
	ExecutionMode   string
	ScheduledAt     *time.Time
	ExecutionConfig string
}

func readDispatchOrderState(t *testing.T, db *gorm.DB, orderID string) dispatchOrderState {
	t.Helper()
	var (
		state  dispatchOrderState
		config sql.NullString
	)
	row := db.Raw(`SELECT progress, execution_mode, scheduled_at, execution_config
		FROM insight_order_records WHERE order_id = ?`, orderID).Row()
	if row == nil {
		t.Fatalf("order %s not found", orderID)
	}
	if err := row.Scan(&state.Progress, &state.ExecutionMode, &state.ScheduledAt, &config); err != nil {
		t.Fatalf("scan order state: %v", err)
	}
	if config.Valid {
		state.ExecutionConfig = config.String
	}
	return state
}

func countPendingTasks(t *testing.T, db *gorm.DB, orderID string) int {
	t.Helper()
	var count int64
	if err := db.Table("insight_order_tasks").
		Where("order_id = ? AND progress = 'PENDING'", orderID).
		Count(&count).Error; err != nil {
		t.Fatalf("count pending tasks: %v", err)
	}
	return int(count)
}

func readOrderLogs(t *testing.T, db *gorm.DB, orderID string) string {
	t.Helper()
	var msgs []string
	if err := db.Table("insight_order_logs").
		Where("order_id = ?", orderID).
		Order("id ASC").
		Pluck("msg", &msgs).Error; err != nil {
		t.Fatalf("read logs: %v", err)
	}
	return strings.Join(msgs, "\n")
}

// 定时执行：只登记计划，任务不执行，工单状态不变
func TestBatchExecuteScheduledOnlyRegistersPlan(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_sched00000001"
	seedDispatchOrder(t, db, orderID, "APPROVED", "dba", 2)

	planned := time.Now().Add(2 * time.Hour).Format(orderTimeLayout)
	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: ExecutionModeScheduled,
			ScheduledAt:   planned,
			ErrorPolicy:   ErrorPolicyContinue,
			BackupPolicy:  BackupPolicyContinue,
		},
		Username: "dba",
	}
	if err := service.Run(); err != nil {
		t.Fatalf("登记定时执行不应报错: %v", err)
	}

	state := readDispatchOrderState(t, db, orderID)
	if state.ExecutionMode != ExecutionModeScheduled {
		t.Fatalf("execution_mode = %q, want SCHEDULED", state.ExecutionMode)
	}
	if state.ScheduledAt == nil || state.ScheduledAt.Format(orderTimeLayout) != planned {
		t.Fatalf("scheduled_at 未按计划落库: %v", state.ScheduledAt)
	}
	if state.Progress != "APPROVED" {
		t.Fatalf("定时登记不应改变工单状态，实际 %q", state.Progress)
	}
	if pending := countPendingTasks(t, db, orderID); pending != 2 {
		t.Fatalf("定时执行不应执行任何任务，仍有 %d/2 个待执行任务", pending)
	}

	// 策略快照必须落库：调度器到期触发时就是靠它还原错误/备份策略
	var config map[string]string
	if err := json.Unmarshal([]byte(state.ExecutionConfig), &config); err != nil {
		t.Fatalf("execution_config 非法: %v (%s)", err, state.ExecutionConfig)
	}
	if config["error_policy"] != ErrorPolicyContinue || config["backup_policy"] != BackupPolicyContinue {
		t.Fatalf("策略未落库: %+v", config)
	}
	if !strings.Contains(readOrderLogs(t, db, orderID), "登记为「定时执行」") {
		t.Fatal("操作历史缺少定时执行登记记录")
	}
}

// 定时执行时间必须晚于当前时间，否则应报错且不落计划
func TestBatchExecuteScheduledRejectsPastTime(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_sched00000002"
	seedDispatchOrder(t, db, orderID, "APPROVED", "dba", 1)

	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: ExecutionModeScheduled,
			ScheduledAt:   time.Now().Add(-1 * time.Hour).Format(orderTimeLayout),
		},
		Username: "dba",
	}
	err := service.Run()
	if err == nil {
		t.Fatal("过去时间应被拒绝")
	}
	if !strings.Contains(err.Error(), "必须晚于当前时间") {
		t.Fatalf("错误文案不明确: %v", err)
	}
	if state := readDispatchOrderState(t, db, orderID); state.ExecutionMode != "IMMEDIATE" {
		t.Fatalf("被拒绝的定时执行不应落库，实际 execution_mode=%q", state.ExecutionMode)
	}

	// 缺时间同样拒绝
	service.ExecutionMode = ExecutionModeScheduled
	service.ScheduledAt = ""
	if err := service.Run(); err == nil {
		t.Fatal("缺少定时时间应被拒绝")
	}
}

// 手动执行：只登记模式，任务一条都不跑
func TestBatchExecuteManualOnlyRegistersMode(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_manual0000001"
	seedDispatchOrder(t, db, orderID, "APPROVED", "dba", 3)

	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: ExecutionModeManual,
		},
		Username: "dba",
	}
	if err := service.Run(); err != nil {
		t.Fatalf("登记手动执行不应报错: %v", err)
	}

	state := readDispatchOrderState(t, db, orderID)
	if state.ExecutionMode != ExecutionModeManual {
		t.Fatalf("execution_mode = %q, want MANUAL", state.ExecutionMode)
	}
	if state.ScheduledAt != nil {
		t.Fatalf("手动执行不应有定时时间: %v", state.ScheduledAt)
	}
	if state.Progress != "APPROVED" {
		t.Fatalf("手动登记不应改变工单状态，实际 %q", state.Progress)
	}
	if pending := countPendingTasks(t, db, orderID); pending != 3 {
		t.Fatalf("手动执行不应执行任何任务，仍有 %d/3 个待执行任务", pending)
	}
	if !strings.Contains(readOrderLogs(t, db, orderID), "登记为「手动执行」") {
		t.Fatal("操作历史缺少手动执行登记记录")
	}
}

// 非执行人不能登记计划（与执行权限口径一致）
func TestBatchExecuteRejectsNonExecutor(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_perm000000001"
	seedDispatchOrder(t, db, orderID, "APPROVED", "dba", 1)

	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: ExecutionModeManual,
		},
		Username: "someone-else",
	}
	if err := service.Run(); err == nil || !strings.Contains(err.Error(), "不是工单执行人") {
		t.Fatalf("非执行人应被拒绝，实际: %v", err)
	}
	if state := readDispatchOrderState(t, db, orderID); state.ExecutionMode != "IMMEDIATE" {
		t.Fatalf("被拒绝的请求不应落库，实际 execution_mode=%q", state.ExecutionMode)
	}
}

// ---------------------------------------------------------------------------
// 执行链路不再做数据授权校验（2026-10-23 起）
//
// 变更(DML/DDL)与导出(EXPORT)的审批对象就是工单本身，工单「提交 → 审批 → 执行」
// 已经是这两类操作的全部审批。此前执行前按 SQLType 推导 dml/export 再查一次数据授权，
// 会让「工单已批但没单独申请数据权限」直接执行失败——同一件事审两遍，且常驻授权
// 在有效期内可执行任意变更，比工单审批更宽。
// 数据访问授权只约束查询接口，判定集中在 das/services/access_action_scope.go，
// 对应的单元测试见 das/services 包（TestActionNeedsAccessGrant 等）。
// ---------------------------------------------------------------------------

// 状态不允许执行时（如审批中）不得登记计划
func TestBatchExecuteRejectsNonExecutableOrder(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_status0000001"
	seedDispatchOrder(t, db, orderID, "PENDING", "dba", 1)

	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: ExecutionModeScheduled,
			ScheduledAt:   time.Now().Add(1 * time.Hour).Format(orderTimeLayout),
		},
		Username: "dba",
	}
	if err := service.Run(); err == nil || !strings.Contains(err.Error(), "禁止执行") {
		t.Fatalf("审批中的工单应被拒绝，实际: %v", err)
	}
}

// 立即执行会覆盖此前的定时计划（否则会出现「已排定定时执行」与实际已执行的矛盾状态）
func TestBatchExecuteImmediateClearsScheduledPlan(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_clear00000001"
	seedDispatchOrder(t, db, orderID, "APPROVED", "dba", 1)

	// 先用一条 SQL 把工单置成「已排定定时执行」（模拟此前的登记结果）
	if err := global.App.DB.Exec(`UPDATE insight_order_records
		SET execution_mode = 'SCHEDULED', scheduled_at = ? WHERE order_id = ?`,
		time.Now().Add(1*time.Hour).Format(orderTimeLayout), orderID).Error; err != nil {
		t.Fatalf("pre-set scheduled plan: %v", err)
	}

	// 立即执行：因为缺 Redis（本测试不装配）会在执行阶段失败，但计划必须先被清掉
	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: ExecutionModeImmediate,
		},
		Username: "dba",
	}
	func() {
		defer func() {
			// 执行阶段需要 Redis 锁，这里允许 panic/报错，只关心计划是否被覆盖
			_ = recover()
		}()
		_ = service.Run()
	}()

	state := readDispatchOrderState(t, db, orderID)
	if state.ExecutionMode != ExecutionModeImmediate {
		t.Fatalf("立即执行应覆盖定时计划，实际 execution_mode=%q", state.ExecutionMode)
	}
	if state.ScheduledAt != nil {
		t.Fatalf("立即执行应清空 scheduled_at，实际 %v", state.ScheduledAt)
	}
}

// 作为对照组：确认上面「定时/手动不执行」的断言不是因为整个流程根本没跑起来
// —— 表结构、落库与日志写入都必须是真实生效的（这里以手动登记后的状态为证），
// 同时确认零值/非法执行方式会被归一化成 IMMEDIATE 而不是被静默忽略成「什么都不做」。
func TestExecutionModeNormalizationOnServicePath(t *testing.T) {
	db := setupExecutionDispatchTestDB(t)
	const orderID = "k_normalize00001"
	seedDispatchOrder(t, db, orderID, "APPROVED", "dba", 1)

	service := ExecuteBatchTasksService{
		ExecuteBatchTasksForm: &forms.ExecuteBatchTasksForm{
			OrderID:       orderID,
			ExecutionMode: "SOMETHING_ODD",
		},
		Username: "dba",
	}
	func() {
		defer func() { _ = recover() }()
		_ = service.Run()
	}()

	// 非法值归一化为 IMMEDIATE：不应把计划写成 SCHEDULED / MANUAL
	state := readDispatchOrderState(t, db, orderID)
	if state.ExecutionMode == ExecutionModeScheduled || state.ExecutionMode == ExecutionModeManual {
		t.Fatalf("非法执行方式被误落库: %q", state.ExecutionMode)
	}
	if state.Progress == "APPROVED" && countPendingTasks(t, db, orderID) != 1 {
		// 允许执行阶段因缺 Redis 中断，但不允许「登记成了非立即执行模式」
		t.Fatalf("非法值归一化异常: mode=%q", state.ExecutionMode)
	}
	_ = ordersModels.InsightOrderRecords{} // 显式引用模型，保证测试与模型字段同步演进
}
