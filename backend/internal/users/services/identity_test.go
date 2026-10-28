package services

import (
	"slices"
	"testing"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 「同名软删除历史行」导致权限错位的回归
//
// 现场（2026-10-13，1.99）：
//   insight_users 里有两条 username='hongjie'——uid=245（2026-08-17 建，后被软删，
//   角色绑定已清空）与 uid=262（2026-10-13 同名重建，绑定「普通成员」）。
//   username 上无唯一索引（见 sql/20260925_users_uniq_uid_only.sql），而权限解析、
//   用户信息接口都按 username 裸查且不排序：InnoDB 全表扫描先读到 uid=245（主键序），
//   GORM 又只取第一行 —— 结果「绑了角色，菜单全无、接口 403、首页统计全 0」。
//
// 本测试用内存库固定这一场景，断言解析口径与登录一致（未删除→已激活→uid 最小）：
//   - 解析结果必须是 uid=262（活跃行），而不是 uid=245（历史行）；
//   - 权限解析必须拿到普通成员角色里的权限；
//   - 只剩历史行时不得再授予任何权限（避免幽灵账号放权）。
// ============================================================================

const identityTestSchemaSQL = `
DROP TABLE IF EXISTS insight_users;
CREATE TABLE insight_users (
    uid          INTEGER PRIMARY KEY AUTOINCREMENT,
    username     varchar(32) NOT NULL,
    password     varchar(128) NOT NULL DEFAULT '',
    email        varchar(254) NOT NULL DEFAULT '',
    mobile       varchar(11) NOT NULL DEFAULT '',
    avatar_file  varchar(254),
    is_superuser boolean DEFAULT 0,
    is_active    boolean DEFAULT 1,
    is_staff     boolean DEFAULT 0,
    last_login   datetime,
    is_deleted   boolean DEFAULT 0,
    date_joined  datetime,
    updated_at   datetime
);
DROP TABLE IF EXISTS insight_permissions;
CREATE TABLE insight_permissions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    code            varchar(128) NOT NULL,
    name            varchar(64) NOT NULL DEFAULT '',
    module          varchar(64) NOT NULL DEFAULT '',
    resource        varchar(64) NOT NULL DEFAULT '',
    action          varchar(64) NOT NULL DEFAULT '',
    permission_type varchar(32) NOT NULL DEFAULT 'action',
    description     varchar(255) NOT NULL DEFAULT '',
    is_builtin      boolean NOT NULL DEFAULT 1,
    is_enabled      boolean NOT NULL DEFAULT 1
);
DROP TABLE IF EXISTS insight_roles;
CREATE TABLE insight_roles (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at  datetime,
    updated_at  datetime,
    name        varchar(32) NOT NULL,
    description varchar(256),
    permissions text,
    code        varchar(64) NOT NULL,
    is_builtin  boolean DEFAULT 0,
    is_deleted  boolean DEFAULT 0
);
DROP TABLE IF EXISTS insight_role_permissions;
CREATE TABLE insight_role_permissions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at    datetime,
    updated_at    datetime,
    role_id       bigint NOT NULL,
    permission_id bigint NOT NULL
);
DROP TABLE IF EXISTS insight_user_roles;
CREATE TABLE insight_user_roles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at datetime,
    updated_at datetime,
    uid        bigint NOT NULL,
    role_id    bigint NOT NULL
);
DROP TABLE IF EXISTS insight_user_permissions;
CREATE TABLE insight_user_permissions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at    datetime,
    updated_at    datetime,
    uid           bigint NOT NULL,
    permission_id bigint NOT NULL
);`

func setupIdentityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:identity_test?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(identityTestSchemaSQL).Error; err != nil {
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

// seedDuplicateUsernameScenario 复刻线上现场：历史行（已删除/未激活/无绑定）+ 同名新行（活跃/普通成员）
func seedDuplicateUsernameScenario(t *testing.T, db *gorm.DB) {
	t.Helper()
	exec := func(sqlText string, args ...interface{}) {
		t.Helper()
		if err := db.Exec(sqlText, args...).Error; err != nil {
			t.Fatalf("seed %q: %v", sqlText, err)
		}
	}
	// 权限点
	exec(`INSERT INTO insight_permissions (id, code, name, module, is_enabled) VALUES
		(9,  'system.instance.view', '查看实例',    'system', 1),
		(48, 'ai.chat.use',          '使用 AI 助手', 'ai',     1)`)
	// 「普通成员」内置角色（role_id=11），绑定两个权限
	exec(`INSERT INTO insight_roles (id, name, code, is_builtin, is_deleted) VALUES
		(11, '普通成员', 'member', 1, 0)`)
	exec(`INSERT INTO insight_role_permissions (role_id, permission_id) VALUES (11, 9), (11, 48)`)
	// uid=245：同名历史行（is_deleted=1 / is_active=0 / 无角色绑定）
	exec(`INSERT INTO insight_users (uid, username, password, email, mobile, is_superuser, is_active, is_deleted, date_joined)
		VALUES (245, 'hongjie', 'old-hash', 'simaliaotaijie@gmail.com', '13637126371', 0, 0, 1, '2026-08-17 14:38:07')`)
	// uid=262：同名新行（活跃 + 绑定普通成员）
	exec(`INSERT INTO insight_users (uid, username, password, email, mobile, is_superuser, is_active, is_deleted, date_joined)
		VALUES (262, 'hongjie', 'new-hash', 'simaliaotaijie@gmail.com', '13637126371', 0, 1, 0, '2026-10-13 15:26:00')`)
	exec(`INSERT INTO insight_user_roles (uid, role_id) VALUES (262, 11)`)
}

func TestCanonicalUserByUsername_PicksActiveRowOverDeletedDuplicate(t *testing.T) {
	db := setupIdentityTestDB(t)
	seedDuplicateUsernameScenario(t, db)

	user, err := CanonicalUserByUsername("hongjie")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if user.Uid != 262 {
		t.Fatalf("期望解析到活跃行 uid=262，实际 uid=%d（is_deleted=%v is_active=%v）",
			user.Uid, user.IsDeleted, user.IsActive)
	}
	if !IsEffectiveUser(user) {
		t.Fatalf("解析结果应被判定为可用账号: %+v", user)
	}

	// 前导/尾随空白不应改变结果
	user, err = CanonicalUserByUsername("  hongjie  ")
	if err != nil || user.Uid != 262 {
		t.Fatalf("带空白用户名解析应仍命中 uid=262，got uid=%d err=%v", user.Uid, err)
	}
}

func TestGetPermissionCodesByUsername_UsesActiveDuplicateRowPermissions(t *testing.T) {
	db := setupIdentityTestDB(t)
	seedDuplicateUsernameScenario(t, db)

	codes, err := GetPermissionCodesByUsername("hongjie")
	if err != nil {
		t.Fatalf("permission codes: %v", err)
	}
	if !slices.Contains(codes, "ai.chat.use") {
		t.Fatalf("应拿到普通成员角色的 ai.chat.use，实际 %v", codes)
	}
	if !slices.Contains(codes, "system.instance.view") {
		t.Fatalf("应拿到普通成员角色的 system.instance.view，实际 %v", codes)
	}
	// 具备 system.<sub>.<action> 时后端会虚拟补 system.access，保证「系统管理」父菜单能露出
	if !slices.Contains(codes, "system.access") {
		t.Fatalf("应补出父级门栓权限 system.access，实际 %v", codes)
	}
}

func TestGetPermissionCodesByUsername_DeletedOnlyRowGrantsNothing(t *testing.T) {
	db := setupIdentityTestDB(t)
	seedDuplicateUsernameScenario(t, db)
	// 再插一条「只有已删除行」的用户名，模拟历史遗留账号
	if err := db.Exec(`INSERT INTO insight_users (uid, username, password, email, mobile, is_superuser, is_active, is_deleted, date_joined)
		VALUES (300, 'ghost', 'hash', 'ghost@example.com', '13600000000', 0, 0, 1, '2026-08-01 00:00:00')`).Error; err != nil {
		t.Fatalf("seed ghost: %v", err)
	}
	if err := db.Exec(`INSERT INTO insight_user_roles (uid, role_id) VALUES (300, 11)`).Error; err != nil {
		t.Fatalf("seed ghost role: %v", err)
	}

	codes, err := GetPermissionCodesByUsername("ghost")
	if err != nil {
		t.Fatalf("permission codes: %v", err)
	}
	if len(codes) != 0 {
		t.Fatalf("已删除账号不应再持任何权限，实际 %v", codes)
	}

	// 完全不存在的用户名仍返回错误，便于中间件区分 401/403 与 500
	if _, err := GetPermissionCodesByUsername("no-such-user"); err == nil {
		t.Fatalf("不存在的用户名应返回错误")
	}
}

func TestGetUserInfoServices_ReturnsActiveDuplicateRowAndHidesPassword(t *testing.T) {
	db := setupIdentityTestDB(t)
	seedDuplicateUsernameScenario(t, db)

	data, err := (&GetUserInfoServices{Username: "hongjie"}).Run()
	if err != nil {
		t.Fatalf("user info: %v", err)
	}
	user, ok := data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected payload type %T", data)
	}
	if uid, _ := user["uid"].(int64); uid != 262 {
		t.Fatalf("用户信息应来自 uid=262，实际 uid=%v", user["uid"])
	}
	if role, _ := user["role"].(string); role != "普通成员" {
		t.Fatalf("应返回绑定角色「普通成员」，实际 %q", user["role"])
	}
	perms, _ := user["effective_permissions"].([]string)
	if !slices.Contains(perms, "ai.chat.use") {
		t.Fatalf("生效权限应包含 ai.chat.use，实际 %v", perms)
	}
	if _, leaked := user["password"]; leaked {
		t.Fatalf("接口不应回传密码哈希")
	}
}

func TestIsSuperuserByUsername_IgnoresDeletedDuplicateSuperuser(t *testing.T) {
	db := setupIdentityTestDB(t)
	seedDuplicateUsernameScenario(t, db)
	seed := func(uid int, name string, isSuper, isActive, isDeleted int) {
		t.Helper()
		if err := db.Exec(`INSERT INTO insight_users (uid, username, password, email, mobile, is_superuser, is_active, is_deleted, date_joined)
			VALUES (?, ?, 'hash', 'x@example.com', '13600000000', ?, ?, ?, '2026-08-01 00:00:00')`,
			uid, name, isSuper, isActive, isDeleted).Error; err != nil {
			t.Fatalf("seed %s/%d: %v", name, uid, err)
		}
	}
	// 活跃超管 + 同名已删除超管历史行 → 仍应判为超管（但只认活跃那行）
	seed(400, "boss", 1, 1, 0)
	seed(401, "boss", 1, 0, 1)
	if !IsSuperuserByUsername("boss") {
		t.Fatalf("活跃超管 + 同名已删除历史行应判为超管")
	}
	// 只有已删除的超管历史行 → 不得再被当成超管
	seed(402, "exboss", 1, 0, 1)
	if IsSuperuserByUsername("exboss") {
		t.Fatalf("已删除历史行不应再被判定为超级管理员")
	}
	// 普通成员
	if IsSuperuserByUsername("hongjie") {
		t.Fatalf("普通成员不应被判定为超级管理员")
	}
}
