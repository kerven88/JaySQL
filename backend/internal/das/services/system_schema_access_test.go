package services

import (
	"reflect"
	"testing"
)

// ============================================================================
// 工单 / AI 统一入口的系统库过滤回归测试
//
// 口径（2026-10-24 定稿）：非 admin 一律隐藏系统库，不做「显式授权豁免」——
// 与工单提交校验（orders.rejectOrderSystemTarget）严格对齐，
// 避免「下拉看得到、提交被驳回」的半套规则。
// ============================================================================

// 严格过滤：系统库（含 PG 层级路径）剔除，业务库与空名保留。
func TestFilterAllSystemSchemasStrict(t *testing.T) {
	got := filterAllSystemSchemas("MySQL", []string{"appdb", "sys", "mysql", "performance_schema", "order_db", ""})
	want := []string{"appdb", "order_db", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MySQL 严格过滤结果不符：got %v want %v", got, want)
	}

	gotPG := filterAllSystemSchemas("PostgreSQL", []string{"appdb.public", "appdb.pg_catalog", "appdb.information_schema"})
	wantPG := []string{"appdb.public"}
	if !reflect.DeepEqual(gotPG, wantPG) {
		t.Fatalf("PG 层级路径过滤结果不符：got %v want %v", gotPG, wantPG)
	}
}

// SQL 下推用的清单：名称全小写、前缀/后缀按家族给出（工单下拉分页依赖它）。
func TestSystemSchemaFilterTerms(t *testing.T) {
	names, prefixes, suffixes := SystemSchemaFilterTerms("MySQL")
	wantNames := []string{"information_schema", "mysql", "performance_schema", "sys", "metrics_schema"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("MySQL names 不符：got %v", names)
	}
	if len(prefixes) != 0 || len(suffixes) != 0 {
		t.Fatalf("MySQL 不应有前缀/后缀规则：got %v / %v", prefixes, suffixes)
	}

	_, pgPrefixes, _ := SystemSchemaFilterTerms("PostgreSQL")
	if len(pgPrefixes) == 0 {
		t.Fatal("PG 应有 pg_toast/pg_temp_ 前缀规则")
	}

	_, oraPrefixes, oraSuffixes := SystemSchemaFilterTerms("Oracle")
	if len(oraPrefixes) == 0 || len(oraSuffixes) == 0 {
		t.Fatal("Oracle 应有 sys 前缀与后缀规则")
	}
}

// admin 与普通成员走同一入口，过滤行为必须相反；空用户名 fail-closed 按非 admin 处理。
func TestFilterSystemSchemasForUsernameAdminVsMember(t *testing.T) {
	db := setupDataGrantTestDB(t)
	if err := db.Exec(`
		DROP TABLE IF EXISTS insight_users;
		CREATE TABLE insight_users (
			uid          bigint DEFAULT 0,
			username     varchar(64) DEFAULT '',
			is_superuser boolean DEFAULT false,
			is_deleted   boolean DEFAULT false,
			is_active    boolean DEFAULT true
		);
		DROP TABLE IF EXISTS insight_user_roles;
		CREATE TABLE insight_user_roles (
			uid     bigint DEFAULT 0,
			role_id bigint DEFAULT 0
		);
		DROP TABLE IF EXISTS insight_roles;
		CREATE TABLE insight_roles (
			id         bigint PRIMARY KEY,
			code       varchar(32) DEFAULT '',
			is_deleted boolean DEFAULT false
		);
	`).Error; err != nil {
		t.Fatalf("create access tables: %v", err)
	}
	seed := func(username string, uid int64, roleID int64) {
		t.Helper()
		if err := db.Exec(`INSERT INTO insight_users (uid, username, is_active, is_deleted) VALUES (?, ?, true, false)`, uid, username).Error; err != nil {
			t.Fatalf("seed user %s: %v", username, err)
		}
		if roleID > 0 {
			if err := db.Exec(`INSERT INTO insight_user_roles (uid, role_id) VALUES (?, ?)`, uid, roleID).Error; err != nil {
				t.Fatalf("seed user_role %s: %v", username, err)
			}
		}
	}
	if err := db.Exec(`INSERT INTO insight_roles (id, code, is_deleted) VALUES (11, 'member', false), (12, 'admin', false)`).Error; err != nil {
		t.Fatalf("seed roles: %v", err)
	}
	seed("member_user", 101, 11)
	seed("admin_user", 102, 12)

	paths := []string{"appdb", "sys", "information_schema"}

	if got := FilterSystemSchemasForUsername("member_user", "MySQL", paths); len(got) != 1 || got[0] != "appdb" {
		t.Fatalf("普通成员应只看到业务库，got %v", got)
	}
	if got := FilterSystemSchemasForUsername("admin_user", "MySQL", paths); len(got) != 3 {
		t.Fatalf("admin 应原样看到全部清单，got %v", got)
	}
	// 空用户名 / 未知用户：fail-closed，按非 admin 处理
	if got := FilterSystemSchemasForUsername("", "MySQL", paths); len(got) != 1 {
		t.Fatalf("空用户名应按非 admin 过滤，got %v", got)
	}
	if got := FilterSystemSchemasForUsername("ghost_user", "MySQL", paths); len(got) != 1 {
		t.Fatalf("未知用户应按非 admin 过滤，got %v", got)
	}
	if HasBuiltinAdminRoleByUsername("ghost_user") {
		t.Fatal("未知用户不应被判定为 admin")
	}
}
