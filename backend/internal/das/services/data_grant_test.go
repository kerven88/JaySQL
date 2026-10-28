package services

import (
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 细粒度数据授权：统一判定的回归测试
//
// 覆盖改造的四条硬约束：
//   1. 没有任何授权 => 拒绝（未授权即不可见，admin 也不例外）
//   2. deny 优先于 allow
//   3. 时间窗口：未生效 / 已过期都不算
//   4. 资源层级：实例级授权覆盖库/表；库级不覆盖其他库
// ============================================================================

const createDataGrantTestSchemaSQL = `
DROP TABLE IF EXISTS insight_das_data_grants;
CREATE TABLE insight_das_data_grants (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at     datetime,
    updated_at     datetime,
    grant_no       varchar(32) DEFAULT '',
    principal_type varchar(16) DEFAULT 'user',
    principal_id   bigint DEFAULT 0,
    principal_name varchar(128) DEFAULT '',
    resource_level varchar(16) DEFAULT 'instance',
    instance_id    varchar(15) DEFAULT '',
    schema_name    varchar(255) DEFAULT '',
    table_name     varchar(128) DEFAULT '',
    table_mode     varchar(16) DEFAULT 'exact',
    request_no     varchar(15) DEFAULT '',
    resource_type  varchar(32) DEFAULT 'database',
    actions        varchar(255) DEFAULT 'select',
    effect         varchar(16) DEFAULT 'allow',
    effective_at   datetime,
    expire_at      datetime,
    grantor_username varchar(64) DEFAULT '',
    grantor_name   varchar(128) DEFAULT '',
    grant_source   varchar(32) DEFAULT 'manual',
    status         varchar(16) DEFAULT 'active',
    reason         varchar(512) DEFAULT '',
    revoked_at     datetime,
    revoked_by     varchar(64) DEFAULT ''
);
DROP TABLE IF EXISTS insight_das_grant_audit;
CREATE TABLE insight_das_grant_audit (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at     datetime,
    updated_at     datetime,
    grant_no       varchar(32) DEFAULT '',
    grant_id       bigint DEFAULT 0,
    action         varchar(32) DEFAULT '',
    operator_username varchar(64) DEFAULT '',
    operator_name  varchar(128) DEFAULT '',
    principal_type varchar(16) DEFAULT '',
    principal_id   bigint DEFAULT 0,
    principal_name varchar(128) DEFAULT '',
    resource_desc  varchar(512) DEFAULT '',
    actions        varchar(255) DEFAULT '',
    before_snapshot text,
    after_snapshot text,
    reason         varchar(512) DEFAULT '',
    request_id     varchar(128) DEFAULT '',
    source_ip      varchar(64) DEFAULT ''
);
DROP TABLE IF EXISTS insight_das_schema_perms;
CREATE TABLE insight_das_schema_perms (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at     datetime,
    updated_at     datetime,
    username       varchar(128) DEFAULT '',
    schema         varchar(255) DEFAULT '',
    instance_id    varchar(15) DEFAULT '',
    resource_type  varchar(32) DEFAULT 'database',
    principal_type varchar(16) DEFAULT 'user',
    principal_id   bigint DEFAULT 0,
    principal_name varchar(128) DEFAULT ''
);
DROP TABLE IF EXISTS insight_das_table_perms;
CREATE TABLE insight_das_table_perms (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at     datetime,
    updated_at     datetime,
    username       varchar(128) DEFAULT '',
    schema         varchar(255) DEFAULT '',
    "table"        varchar(128) DEFAULT '',
    instance_id    varchar(15) DEFAULT '',
    rule           varchar(16) DEFAULT 'allow',
    resource_type  varchar(32) DEFAULT 'database',
    principal_type varchar(16) DEFAULT 'user',
    principal_id   bigint DEFAULT 0,
    principal_name varchar(128) DEFAULT ''
);
DROP TABLE IF EXISTS insight_users;
CREATE TABLE insight_users (
    uid        INTEGER PRIMARY KEY AUTOINCREMENT,
    username   varchar(64) DEFAULT '',
    is_deleted boolean DEFAULT false,
    is_active  boolean DEFAULT true
);
DROP TABLE IF EXISTS insight_roles;
CREATE TABLE insight_roles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       varchar(64) DEFAULT '',
    name       varchar(128) DEFAULT '',
    is_deleted boolean DEFAULT false
);`

func setupDataGrantTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:data_grant_test?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(createDataGrantTestSchemaSQL).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	prevDB, prevLog := global.App.DB, global.App.Log
	global.App.DB = db
	global.App.Log = logrus.New()
	ResetDataGrantReadyCache()
	t.Cleanup(func() {
		global.App.DB = prevDB
		global.App.Log = prevLog
		ResetDataGrantReadyCache()
	})
	return db
}

func testScope() *dasActorScope {
	return &dasActorScope{
		UID:      100,
		Username: "tester",
		Principals: []dasPrincipal{
			{PrincipalType: dasPrincipalUser, PrincipalID: 100},
			{PrincipalType: dasPrincipalRole, PrincipalID: 7},
		},
	}
}

type seedGrantOpt struct {
	PrincipalType string
	PrincipalID   uint64
	Level         string
	InstanceID    string
	Schema        string
	Table         string
	// TableMode 表级圈选方式：exact（默认）/ wildcard / except
	TableMode   string
	RequestNo   string
	Actions     string
	Effect      string
	Status      string
	EffectiveAt *time.Time
	ExpireAt    *time.Time
}

func seedGrant(t *testing.T, db *gorm.DB, opt seedGrantOpt) {
	t.Helper()
	if opt.PrincipalType == "" {
		opt.PrincipalType = dasPrincipalUser
	}
	if opt.PrincipalID == 0 {
		opt.PrincipalID = 100
	}
	if opt.Actions == "" {
		opt.Actions = DataActionSelect
	}
	if opt.Effect == "" {
		opt.Effect = DataEffectAllow
	}
	if opt.Status == "" {
		opt.Status = DataGrantStatusActive
	}
	if opt.TableMode == "" {
		opt.TableMode = "exact"
	}
	if err := db.Exec(`INSERT INTO insight_das_data_grants
		(principal_type, principal_id, resource_level, instance_id, schema_name, table_name,
		 table_mode, request_no, actions, effect, status, effective_at, expire_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		opt.PrincipalType, opt.PrincipalID, opt.Level, opt.InstanceID, opt.Schema, opt.Table,
		opt.TableMode, opt.RequestNo, opt.Actions, opt.Effect, opt.Status, opt.EffectiveAt, opt.ExpireAt).Error; err != nil {
		t.Fatalf("seed grant: %v", err)
	}
}

// 1. 无授权 => 拒绝（admin 不再默认可见全部数据的核心断言）
func TestHasDataGrantDeniesWithoutGrant(t *testing.T) {
	setupDataGrantTestDB(t)
	// 先插一条别的实例的授权，让新表"已启用"（否则会走旧表回退路径）
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-other0000001"})

	ok, err := HasDataGrant(testScope(), "ds-target000001", "", "", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if ok {
		t.Fatal("未授权实例竟然放行——「未授权即不可见」被破坏")
	}
}

// 2. 实例级授权覆盖该实例下的库与表
func TestInstanceGrantCoversSchemaAndTable(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-covered00001"
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: inst})

	for _, tc := range []struct {
		schema, table string
	}{
		{"", ""},
		{"appdb", ""},
		{"appdb", "t_user"},
	} {
		ok, err := HasDataGrant(testScope(), inst, tc.schema, tc.table, DataActionSelect)
		if err != nil {
			t.Fatalf("HasDataGrant(%s,%s): %v", tc.schema, tc.table, err)
		}
		if !ok {
			t.Fatalf("实例级授权未覆盖 %s.%s", tc.schema, tc.table)
		}
	}
}

// 3. deny 优先于 allow
func TestDenyTakesPrecedenceOverAllow(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-deny0000001"
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: inst})
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelTable, InstanceID: inst, Schema: "appdb", Table: "secret_tbl", Effect: DataEffectDeny})

	ok, err := HasDataGrant(testScope(), inst, "appdb", "secret_tbl", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if ok {
		t.Fatal("deny 未生效：表级 deny 被实例级 allow 覆盖")
	}
	// 同库其他表不受影响
	ok, err = HasDataGrant(testScope(), inst, "appdb", "normal_tbl", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if !ok {
		t.Fatal("deny 作用范围失控：同库其他表也被拒绝")
	}
}

// 4. 时间窗口：未生效 / 已过期 / 已撤销 都不算授权
func TestGrantTimeWindowAndStatus(t *testing.T) {
	db := setupDataGrantTestDB(t)
	future := time.Now().Add(1 * time.Hour)
	past := time.Now().Add(-1 * time.Hour)

	// 未生效
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-future000001", EffectiveAt: &future})
	// 已过期
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-expired00001", ExpireAt: &past})
	// 已撤销
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-revoked00001", Status: DataGrantStatusRevoked})
	// 正常
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-active000001"})

	for _, tc := range []struct {
		inst string
		want bool
	}{
		{"ds-future000001", false},
		{"ds-expired00001", false},
		{"ds-revoked00001", false},
		{"ds-active000001", true},
	} {
		ok, err := HasDataGrant(testScope(), tc.inst, "", "", DataActionSelect)
		if err != nil {
			t.Fatalf("HasDataGrant(%s): %v", tc.inst, err)
		}
		if ok != tc.want {
			t.Fatalf("实例 %s 判定 %v，期望 %v", tc.inst, ok, tc.want)
		}
	}
}

// 5. 角色主体授权对角色成员生效（自助/管理端给角色授权的主要用法）
func TestRoleGrantAppliesToMember(t *testing.T) {
	setupDataGrantTestDB(t)
	seedGrant(t, global.App.DB, seedGrantOpt{
		PrincipalType: dasPrincipalRole, PrincipalID: 7,
		Level: DataResourceLevelInstance, InstanceID: "ds-rolegrant001",
	})
	ok, err := HasDataGrant(testScope(), "ds-rolegrant001", "", "", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if !ok {
		t.Fatal("角色授权未对角色成员生效")
	}
}

// 6. 操作类型：只授 select 时，export / dml 必须被拒绝
func TestActionScopeIsHonored(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-action000001"
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: inst, Actions: DataActionSelect})

	for _, tc := range []struct {
		action string
		want   bool
	}{
		{DataActionSelect, true},
		{DataActionExport, false},
		{DataActionDML, false},
	} {
		ok, err := HasDataGrant(testScope(), inst, "", "", tc.action)
		if err != nil {
			t.Fatalf("HasDataGrant(%s): %v", tc.action, err)
		}
		if ok != tc.want {
			t.Fatalf("操作 %s 判定 %v，期望 %v", tc.action, ok, tc.want)
		}
	}
}

// 7. 库级授权只覆盖本库
func TestDatabaseGrantScope(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-dbscope00001"
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: inst, Schema: "appdb"})

	ok, err := HasDataGrant(testScope(), inst, "appdb", "", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if !ok {
		t.Fatal("库级授权未覆盖目标库")
	}
	ok, err = HasDataGrant(testScope(), inst, "otherdb", "", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if ok {
		t.Fatal("库级授权越界覆盖了其他库")
	}
}

// 8. 参数规范化：非法操作类型/层级与时间格式必须被拒绝
func TestNormalizeHelpers(t *testing.T) {
	if _, err := normalizeGrantActions("select,drop"); err == nil {
		t.Fatal("非法操作类型未被拒绝")
	}
	got, err := normalizeGrantActions("")
	if err != nil || got != DataActionSelect {
		t.Fatalf("空操作类型应回落 select，得到 %q err=%v", got, err)
	}
	if _, _, _, err := normalizeResourceLevel(DataResourceLevelTable, "appdb", ""); err == nil {
		t.Fatal("表级授权缺表名未被拒绝")
	}
	if _, _, _, err := normalizeResourceLevel("cluster", "", ""); err == nil {
		t.Fatal("非法资源层级未被拒绝")
	}
	if _, err := parseDataGrantTime("2026/10/15 10:00:00"); err == nil {
		t.Fatal("非法时间格式未被拒绝")
	}
	if v, err := parseDataGrantTime(""); err != nil || v != nil {
		t.Fatalf("空时间应返回 nil，得到 %v err=%v", v, err)
	}
	if v, err := parseDataGrantTime("2026-10-15 10:00:00"); err != nil || v == nil {
		t.Fatalf("标准时间解析失败：%v err=%v", v, err)
	}
}

// 9. 时间窗口纯函数
func TestGrantTimeWindowValid(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	if !grantTimeWindowValid(nil, nil, now) {
		t.Fatal("无时间限制应有效")
	}
	if grantTimeWindowValid(&future, nil, now) {
		t.Fatal("未生效的时间窗口被判为有效")
	}
	if grantTimeWindowValid(nil, &past, now) {
		t.Fatal("已过期的窗口被判为有效")
	}
	if !grantTimeWindowValid(&past, &future, now) {
		t.Fatal("落在窗口内被判为无效")
	}
}

// 11. 旧管理页对账：旧表新增 → 新表补齐；旧表删除 → 新表撤销；自助授权不被误撤
func TestReconcileLegacyGrantsForPrincipal(t *testing.T) {
	db := setupDataGrantTestDB(t)
	inst := "ds-reconcile001"
	if err := db.Exec(`INSERT INTO insight_users (uid, username, is_deleted, is_active) VALUES (1, 'admin', 0, 1)`).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// 旧表：一条实例级授权（schema='*'）
	if err := db.Exec("INSERT INTO insight_das_schema_perms (principal_type, principal_id, principal_name, instance_id, `schema`, resource_type) VALUES ('user', 1, 'admin', ?, '*', 'database')", inst).Error; err != nil {
		t.Fatalf("seed legacy schema perm: %v", err)
	}
	// 新表：一条自助授权（不应被对账撤销）
	seedGrant(t, db, seedGrantOpt{
		Level: DataResourceLevelInstance, InstanceID: "ds-selfgrant001",
	})
	if err := db.Exec("UPDATE insight_das_data_grants SET grant_source = 'self' WHERE instance_id = 'ds-selfgrant001'").Error; err != nil {
		t.Fatalf("mark self grant: %v", err)
	}

	if err := ReconcileLegacyGrantsForPrincipal(db, "user", 1, "admin"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var migrated int64
	db.Table("insight_das_data_grants").Where("instance_id = ? AND status = 'active'", inst).Count(&migrated)
	if migrated != 1 {
		t.Fatalf("旧表实例级授权未补齐到新表：%d", migrated)
	}
	var selfKept int64
	db.Table("insight_das_data_grants").Where("instance_id = 'ds-selfgrant001' AND status = 'active'").Count(&selfKept)
	if selfKept != 1 {
		t.Fatal("自助授权（grant_source=self）被对账误撤销")
	}
	var audits int64
	db.Table("insight_das_grant_audit").Where("action = 'create'").Count(&audits)
	if audits == 0 {
		t.Fatal("对账补齐未写审计流水")
	}

	// 删掉旧表授权后再对账 → 新表应变为 revoked
	if err := db.Exec("DELETE FROM insight_das_schema_perms WHERE instance_id = ?", inst).Error; err != nil {
		t.Fatalf("delete legacy perm: %v", err)
	}
	if err := ReconcileLegacyGrantsForPrincipal(db, "user", 1, "admin"); err != nil {
		t.Fatalf("reconcile after delete: %v", err)
	}
	var revoked int64
	db.Table("insight_das_data_grants").Where("instance_id = ? AND status = 'revoked'", inst).Count(&revoked)
	if revoked != 1 {
		t.Fatalf("旧表删除后新表未撤销：%d", revoked)
	}
	ok, err := HasDataGrant(testScope(), inst, "", "", DataActionSelect)
	if err != nil {
		t.Fatalf("HasDataGrant: %v", err)
	}
	if ok {
		t.Fatal("撤销后仍然放行——撤销未生效")
	}
}

// 12. 旧表授权写入幂等（多次对账不产生重复授权）
func TestReconcileIsIdempotent(t *testing.T) {
	db := setupDataGrantTestDB(t)
	inst := "ds-reconcile002"
	db.Exec(`INSERT INTO insight_users (uid, username, is_deleted, is_active) VALUES (2, 'alice', 0, 1)`)
	db.Exec("INSERT INTO insight_das_schema_perms (principal_type, principal_id, principal_name, instance_id, `schema`, resource_type) VALUES ('user', 2, 'alice', ?, 'appdb', 'database')", inst)
	db.Exec("INSERT INTO insight_das_table_perms (principal_type, principal_id, principal_name, instance_id, `schema`, `table`, rule, resource_type) VALUES ('user', 2, 'alice', ?, 'appdb', 't1', 'allow', 'database')", inst)

	for i := 0; i < 3; i++ {
		if err := ReconcileLegacyGrantsForPrincipal(db, "user", 2, "admin"); err != nil {
			t.Fatalf("reconcile #%d: %v", i, err)
		}
	}
	var total int64
	db.Table("insight_das_data_grants").Where("principal_id = 2").Count(&total)
	if total != 2 {
		t.Fatalf("对账不幂等：期望 2 条（1 库级 + 1 表级），实际 %d", total)
	}
}

// 10. 生效中的授权列表：撤销/过期不出现
func TestAccessibleInstanceIDsByGrantFiltersInvalid(t *testing.T) {
	db := setupDataGrantTestDB(t)
	past := time.Now().Add(-time.Hour)
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-ok0000000001"})
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-revoked00002", Status: DataGrantStatusRevoked})
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-expired00002", ExpireAt: &past})

	ids, err := AccessibleInstanceIDsByGrant(testScope())
	if err != nil {
		t.Fatalf("AccessibleInstanceIDsByGrant: %v", err)
	}
	if len(ids) != 1 || ids[0] != "ds-ok0000000001" {
		t.Fatalf("可见实例列表不正确：%v", ids)
	}
}
