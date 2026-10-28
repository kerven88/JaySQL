package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 「删除数据源」前置拦截的回归
//
// 背景：删数据源此前只清 7 张配置/权限子表，对备份/恢复任务零校验，于是会留下
// 「实例已删、任务还在」的脏状态 —— 调度侧报 instance_missing，列表里数据源列变空。
//
// 这里用内存库真实驱动服务层，锁定四条口径：
//  1. 有「未软删」的备份任务 → 拒绝删除，且不改动任何数据（含权限行）；
//  2. 已软删的历史任务 → 不阻塞（insight_backup_tasks 是软删模型，漏 deleted_at 条件
//     会让数据源永远删不掉，这是最容易回归的一点）；
//  3. 在途执行记录（PENDING/RUNNING/STOP_REQUESTED）→ 单独归类为「执行中」，拒绝删除；
//  4. 恢复记录按 source/target 命中 → 拒绝删除，并带上命中关系；
//  5. 无任何关联 → 删除成功，且访问权限（库/表授权）随实例自动回收、行数如实回传。
// ============================================================================

const instanceDeleteGuardTestSchemaSQL = `
DROP TABLE IF EXISTS insight_instances;
CREATE TABLE insight_instances (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32),
    hostname    varchar(128),
    port        INTEGER,
    created_by  varchar(64)
);
DROP TABLE IF EXISTS insight_backup_tasks;
CREATE TABLE insight_backup_tasks (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id            varchar(32),
    name               varchar(128),
    source_instance_id INTEGER DEFAULT 0,
    deleted_at         datetime NULL
);
DROP TABLE IF EXISTS insight_backup_runs;
CREATE TABLE insight_backup_runs (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id            varchar(32),
    source_instance_id INTEGER DEFAULT 0,
    status             varchar(32)
);
DROP TABLE IF EXISTS insight_restore_runs;
CREATE TABLE insight_restore_runs (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               varchar(128),
    status             varchar(32),
    source_instance_id INTEGER DEFAULT 0,
    target_instance_id INTEGER DEFAULT 0
);
DROP TABLE IF EXISTS insight_instance_sql_standard_bindings;
CREATE TABLE insight_instance_sql_standard_bindings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32)
);
DROP TABLE IF EXISTS insight_instance_schemas;
CREATE TABLE insight_instance_schemas (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32)
);
DROP TABLE IF EXISTS insight_das_schema_perms;
CREATE TABLE insight_das_schema_perms (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id    varchar(32),
    principal_type varchar(16),
    principal_id   INTEGER,
    schema         varchar(64),
    resource_type  varchar(32)
);
DROP TABLE IF EXISTS insight_das_table_perms;
CREATE TABLE insight_das_table_perms (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32)
);
-- 细粒度数据授权：2026-10-15 起删除数据源时也一并清理，
-- 测试 DDL 必须跟上，否则 deleteInstanceRelatedConfigs 会报 no such table
DROP TABLE IF EXISTS insight_das_data_grants;
CREATE TABLE insight_das_data_grants (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id    varchar(32),
    principal_type varchar(16),
    principal_id   INTEGER
);
DROP TABLE IF EXISTS insight_monitor_target_configs;
CREATE TABLE insight_monitor_target_configs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32)
);
DROP TABLE IF EXISTS insight_performance_slowlog_collectors;
CREATE TABLE insight_performance_slowlog_collectors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32)
);
DROP TABLE IF EXISTS insight_inspect_instance_params;
CREATE TABLE insight_inspect_instance_params (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id varchar(32)
);
`

func setupInstanceDeleteGuardDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:instance_delete_guard_" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(
		sqlite.Open(dsn),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(instanceDeleteGuardTestSchemaSQL).Error; err != nil {
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

// seedInstanceWithRelatedData 造一个实例 + 一组子表数据，返回实例主键。
func seedInstanceWithRelatedData(t *testing.T, db *gorm.DB, instanceID string) uint64 {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO insight_instances (instance_id, hostname, port, created_by) VALUES (?, '192.168.1.99', 3306, 'admin')`,
		instanceID,
	).Error; err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	var pk uint64
	if err := db.Raw(`SELECT id FROM insight_instances WHERE instance_id = ?`, instanceID).Scan(&pk).Error; err != nil {
		t.Fatalf("read instance pk: %v", err)
	}

	// 7 张"外键在子表侧"的配置/权限表各来一行，用于验证成功路径确实清理干净
	// （insight_das_data_grants 是 2026-10-15 补进清理清单的：当时漏了它，
	//   导致删除数据源后细粒度授权残留成孤儿）
	stmts := []string{
		`INSERT INTO insight_instance_sql_standard_bindings (instance_id) VALUES (?)`,
		`INSERT INTO insight_instance_schemas (instance_id) VALUES (?)`,
		`INSERT INTO insight_das_schema_perms (instance_id, principal_type, principal_id, schema, resource_type) VALUES (?, 'role', 12, '*', 'instance')`,
		`INSERT INTO insight_das_schema_perms (instance_id, principal_type, principal_id, schema, resource_type) VALUES (?, 'user', 1, 'biz_db', 'database')`,
		`INSERT INTO insight_das_table_perms (instance_id) VALUES (?)`,
		`INSERT INTO insight_das_data_grants (instance_id, principal_type, principal_id) VALUES (?, 'role', 12)`,
		`INSERT INTO insight_das_data_grants (instance_id, principal_type, principal_id) VALUES (?, 'user', 1)`,
		`INSERT INTO insight_monitor_target_configs (instance_id) VALUES (?)`,
		`INSERT INTO insight_performance_slowlog_collectors (instance_id) VALUES (?)`,
		`INSERT INTO insight_inspect_instance_params (instance_id) VALUES (?)`,
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt, instanceID).Error; err != nil {
			t.Fatalf("seed related row: %v", err)
		}
	}
	return pk
}

func countRows(t *testing.T, db *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var cnt int64
	if err := db.Table(table).Where(where, args...).Count(&cnt).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return cnt
}

func runInstanceDelete(t *testing.T, id uint64) (*AdminDeleteInstanceConfigsService, error) {
	t.Helper()
	service := &AdminDeleteInstanceConfigsService{ID: id}
	return service, service.Run()
}

// 1) 有未软删的备份任务 → 拒绝，且一行数据都不许动
func TestInstanceDeleteBlockedByLiveBackupTask(t *testing.T) {
	db := setupInstanceDeleteGuardDB(t)
	const instanceID = "ds-livebackup01"
	pk := seedInstanceWithRelatedData(t, db, instanceID)

	if err := db.Exec(
		`INSERT INTO insight_backup_tasks (task_id, name, source_instance_id, deleted_at) VALUES ('bk-live0000001', '每日全备', ?, NULL)`,
		pk,
	).Error; err != nil {
		t.Fatalf("seed backup task: %v", err)
	}

	_, err := runInstanceDelete(t, pk)
	if err == nil {
		t.Fatalf("存在活跃备份任务时必须拒绝删除")
	}
	var inUse *InstanceInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("错误类型应为 *InstanceInUseError，实际 %T: %v", err, err)
	}
	if len(inUse.BackupTasks) != 1 {
		t.Fatalf("应报告 1 个阻塞任务，实际 %d", len(inUse.BackupTasks))
	}
	if inUse.BackupTasks[0].ID != "bk-live0000001" || inUse.BackupTasks[0].Name != "每日全备" {
		t.Fatalf("阻塞明细不对：%+v", inUse.BackupTasks[0])
	}
	if len(inUse.ActiveItems) != 0 {
		t.Fatalf("没有在途执行记录时不应报执行中：%+v", inUse.ActiveItems)
	}
	if inUse.ID != pk || inUse.InstanceID != instanceID || inUse.HostPort != "192.168.1.99:3306" {
		t.Fatalf("阻塞上下文不完整：%+v", inUse)
	}

	// 关键：拒绝必须"什么都没发生"，否则会出现"配置清了、实例还在"
	if got := countRows(t, db, "insight_instances", "id = ?", pk); got != 1 {
		t.Fatalf("实例不应被删除，剩余 %d 行", got)
	}
	if got := countRows(t, db, "insight_das_schema_perms", "instance_id = ?", instanceID); got != 2 {
		t.Fatalf("拒绝删除时权限行必须保持原样，剩余 %d 行", got)
	}
}

// 2) 只剩历史软删任务 → 放行（软删条件漏了会让数据源永远删不掉）
func TestInstanceDeleteIgnoresSoftDeletedBackupTask(t *testing.T) {
	db := setupInstanceDeleteGuardDB(t)
	const instanceID = "ds-softdeleted1"
	pk := seedInstanceWithRelatedData(t, db, instanceID)

	if err := db.Exec(
		`INSERT INTO insight_backup_tasks (task_id, name, source_instance_id, deleted_at) VALUES ('bk-gone0000001', '已删任务', ?, '2026-10-01 10:00:00')`,
		pk,
	).Error; err != nil {
		t.Fatalf("seed soft-deleted task: %v", err)
	}

	service, err := runInstanceDelete(t, pk)
	if err != nil {
		t.Fatalf("已软删的历史任务不应阻塞删除：%v", err)
	}
	if service.Summary.DasSchemaPerms != 2 || service.Summary.RevokedPermissions() != 5 {
		t.Fatalf("权限回收行数不对：summary=%+v", service.Summary)
	}
	if got := countRows(t, db, "insight_instances", "id = ?", pk); got != 0 {
		t.Fatalf("实例应已删除，剩余 %d 行", got)
	}
}

// 3) 在途执行记录 → 归类为「执行中」并拒绝
func TestInstanceDeleteBlockedByRunningBackupRun(t *testing.T) {
	db := setupInstanceDeleteGuardDB(t)
	const instanceID = "ds-running00001"
	pk := seedInstanceWithRelatedData(t, db, instanceID)

	if err := db.Exec(
		`INSERT INTO insight_backup_runs (task_id, source_instance_id, status) VALUES ('bk-running00001', ?, 'RUNNING')`,
		pk,
	).Error; err != nil {
		t.Fatalf("seed running run: %v", err)
	}
	// 终态记录不参与拦截
	if err := db.Exec(
		`INSERT INTO insight_backup_runs (task_id, source_instance_id, status) VALUES ('bk-done000000001', ?, 'SUCCESS')`,
		pk,
	).Error; err != nil {
		t.Fatalf("seed finished run: %v", err)
	}

	_, err := runInstanceDelete(t, pk)
	var inUse *InstanceInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("存在在途执行记录时必须拒绝删除，实际 %v", err)
	}
	if len(inUse.ActiveItems) != 1 || inUse.ActiveItems[0].Kind != "backup_run" || inUse.ActiveItems[0].Status != "RUNNING" {
		t.Fatalf("执行中条目不对：%+v", inUse.ActiveItems)
	}
}

// 4) 恢复记录命中（含目标实例）→ 拒绝并带命中关系
func TestInstanceDeleteBlockedByRestoreRun(t *testing.T) {
	db := setupInstanceDeleteGuardDB(t)
	const instanceID = "ds-restore00001"
	pk := seedInstanceWithRelatedData(t, db, instanceID)

	if err := db.Exec(
		`INSERT INTO insight_restore_runs (name, status, source_instance_id, target_instance_id) VALUES ('回到目标实例', 'RUNNING', 0, ?)`,
		pk,
	).Error; err != nil {
		t.Fatalf("seed restore run: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO insight_restore_runs (name, status, source_instance_id, target_instance_id) VALUES ('已完成的恢复', 'SUCCESS', ?, 0)`,
		pk,
	).Error; err != nil {
		t.Fatalf("seed finished restore: %v", err)
	}

	_, err := runInstanceDelete(t, pk)
	var inUse *InstanceInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("存在恢复记录时必须拒绝删除，实际 %v", err)
	}
	if len(inUse.RestoreRuns) != 2 {
		t.Fatalf("应报告 2 条恢复记录，实际 %d", len(inUse.RestoreRuns))
	}
	relations := map[string]string{}
	for _, item := range inUse.RestoreRuns {
		relations[item.Name] = item.Relation
	}
	if relations["回到目标实例"] != "target" || relations["已完成的恢复"] != "source" {
		t.Fatalf("恢复记录命中关系不对：%+v", inUse.RestoreRuns)
	}
	// 在途的恢复记录只在 RestoreRuns 里出现一次（避免弹窗重复展示），
	// 但聚合口径 ActiveBlockers 必须把它算进去
	active := inUse.ActiveBlockers()
	if len(active) != 1 || !inUse.ActiveBlockers()[0].Active {
		t.Fatalf("仅 RUNNING 的恢复记录算执行中，实际 %+v", active)
	}
	if !strings.Contains(inUse.Error(), "1 项执行中的备份/恢复") {
		t.Fatalf("在途恢复记录必须计入执行中文案：%s", inUse.Error())
	}
}

// 5) 无关联 → 删除成功，子表清空、权限自动回收并回传行数
func TestInstanceDeleteAllowedAndRevokesPermissions(t *testing.T) {
	db := setupInstanceDeleteGuardDB(t)
	const instanceID = "ds-clean0000001"
	pk := seedInstanceWithRelatedData(t, db, instanceID)

	service, err := runInstanceDelete(t, pk)
	if err != nil {
		t.Fatalf("无关联资源时应允许删除：%v", err)
	}

	checks := []struct {
		table string
		arg   any
	}{
		{"insight_instances", pk},
		{"insight_instance_sql_standard_bindings", instanceID},
		{"insight_instance_schemas", instanceID},
		{"insight_das_schema_perms", instanceID},
		{"insight_das_table_perms", instanceID},
		{"insight_das_data_grants", instanceID},
		{"insight_monitor_target_configs", instanceID},
		{"insight_performance_slowlog_collectors", instanceID},
		{"insight_inspect_instance_params", instanceID},
	}
	for _, check := range checks {
		column := "instance_id = ?"
		if check.table == "insight_instances" {
			column = "id = ?"
		}
		if got := countRows(t, db, check.table, column, check.arg); got != 0 {
			t.Fatalf("%s 应被清空，剩余 %d 行", check.table, got)
		}
	}

	if service.Summary.DasSchemaPerms != 2 || service.Summary.DasTablePerms != 1 || service.Summary.DasDataGrants != 2 {
		t.Fatalf("权限清理行数不对：%+v", service.Summary)
	}
	if service.Summary.RevokedPermissions() != 5 {
		t.Fatalf("自动回收的权限行数应为 5（库权限2 + 表权限1 + 细粒度授权2），实际 %d", service.Summary.RevokedPermissions())
	}
	if service.Summary.SQLStandardBindings != 1 || service.Summary.MonitorTargetConfigs != 1 {
		t.Fatalf("其它子表清理行数不对：%+v", service.Summary)
	}
}

// 6) 错误文案要能直接读：既说明拦了什么，也说明去哪处理
func TestInstanceInUseErrorMessageIsActionable(t *testing.T) {
	inUse := &InstanceInUseError{
		BackupTasks: []InstanceBlockingResource{
			{Kind: "backup_task", ID: "bk-44pwfd32nson", Name: "backup_JaySQL_生产系统"},
		},
	}
	msg := inUse.Error()
	for _, want := range []string{"该数据源", "1 个备份任务", "无法删除", "请先修改或删除对应任务", "bk-44pwfd32nson"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("拦截文案缺少 %q：%s", want, msg)
		}
	}

	active := &InstanceInUseError{
		ActiveItems: []InstanceBlockingResource{
			{Kind: "backup_run", ID: "42", Name: "bk-xxx", Status: "RUNNING", Active: true},
		},
		RestoreRuns: []InstanceBlockingResource{
			{Kind: "restore_run", ID: "7", Name: "恢复任务A", Status: "PENDING", Relation: "target", Active: true},
		},
	}
	for _, want := range []string{"执行中的备份/恢复", "先终止或等待其结束", "2 项"} {
		if !strings.Contains(active.Error(), want) {
			t.Fatalf("在途文案缺少 %q：%s", want, active.Error())
		}
	}
}
