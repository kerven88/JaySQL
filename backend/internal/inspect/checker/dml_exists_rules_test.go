package checker

// DELETE 表存在性 + INSERT 表/字段存在性 + UPDATE 不建议更新「创建时间」列 的端到端测试。
//
// 覆盖：
//  1. delete_stmt_check_table_exists            DELETE 检测表是否存在
//  2. insert_stmt_check_column_exists           INSERT 检测目标表 / 字段是否存在
//  3. update_stmt_avoid_update_create_time_column UPDATE 不建议更新创建时间列（默认 create_time，可配置）
//
// 历史：INSERT 字段值匹配 / INSERT 表字段存在 / UPDATE 表字段存在 三条规则曾于 2026-09-29 下线
// （seed 与引擎实现一并移除）。2026-10-19 仅恢复「INSERT 表字段存在」——
// 生产库的规则行与前端分类映射都还在，用户侧一直能看到并以为它在生效，属于残留的幽灵规则。

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

// ensureExistsRuleTables 固定 fixture：主表 + 关联表（主表带 create_time / update_time 时间列）
func ensureExistsRuleTables(t *testing.T) {
	skipIfNoMatrixMySQL(t)
	t.Helper()
	db := &dao.DB{
		User: matrixMySQLUser, Password: matrixMySQLPass(),
		Host: matrixMySQLHost, Port: matrixMySQLPort,
		Database: matrixMySQLDB, DbType: "MySQL",
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS t_exist_main (
			id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name varchar(64) NOT NULL DEFAULT '',
			status tinyint NOT NULL DEFAULT 0,
			create_time datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
			update_time datetime NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS t_exist_ref (
			rid bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			rname varchar(64) NOT NULL DEFAULT ''
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`DELETE FROM t_exist_main`,
		`DELETE FROM t_exist_ref`,
		`INSERT INTO t_exist_main (name, status) VALUES ('a', 0)`,
		`INSERT INTO t_exist_ref (rname) VALUES ('a')`,
	}
	for _, stmt := range stmts {
		if err := db.Execute(stmt); err != nil {
			t.Fatalf("准备存在性规则 fixture 失败: %v, sql: %s", err, stmt)
		}
	}
}

// TestDMLDeleteTableExistsRule DELETE 语句检测表是否存在
func TestDMLDeleteTableExistsRule(t *testing.T) {
	ensureExistsRuleTables(t)

	cases := []struct {
		name    string
		enable  bool
		sql     string
		wantKey string
	}{
		{
			name: "DELETE表不存在=>告警", enable: true,
			sql: "DELETE FROM t_exist_no_such WHERE id = 1;", wantKey: "delete_stmt_check_table_exists",
		},
		{
			name: "DELETE正常表=>不告警", enable: true,
			sql: "DELETE FROM t_exist_main WHERE id = 1;", wantKey: "",
		},
		{
			name: "DELETE多表语法-关联表不存在=>告警", enable: true,
			sql: "DELETE m FROM t_exist_main m JOIN t_exist_no_such r ON m.name = r.rname;", wantKey: "delete_stmt_check_table_exists",
		},
		{
			name: "规则关闭=>不告警", enable: false,
			sql: "DELETE FROM t_exist_no_such WHERE id = 1;", wantKey: "",
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		if c.enable {
			params.CHECK_DELETE_TABLE_EXISTS = true
		}
		data, err := runMySQLInspect(t, c.sql, params)
		if err != nil {
			t.Fatalf("%s: 审核执行失败: %v", c.name, err)
		}
		assertRuleHit(t, c.name, data, c.wantKey)
	}
}

// TestDMLAvoidUpdateCreateTimeColumnRule UPDATE 不建议更新「创建时间」列
//
// 规则默认列为 create_time，可在规范里配置（逗号分隔，如 gmt_create,created_at）。
// 不依赖表结构：列名由规范显式给出，不存在"读不到表结构就静默"的场景。
func TestDMLAvoidUpdateCreateTimeColumnRule(t *testing.T) {
	ensureExistsRuleTables(t)

	cases := []struct {
		name    string
		cols    string // 规则配置的「不建议更新的字段」，空表示不开启规则
		sql     string
		wantKey string
	}{
		{
			name: "更新create_time=>告警", cols: "create_time",
			sql: "UPDATE t_exist_main SET create_time = NOW() WHERE id = 1;", wantKey: "update_stmt_avoid_update_create_time_column",
		},
		{
			name: "带表前缀更新create_time=>告警", cols: "create_time",
			sql: "UPDATE t_exist_main SET t_exist_main.create_time = NOW() WHERE id = 1;", wantKey: "update_stmt_avoid_update_create_time_column",
		},
		{
			name: "与其它列一起更新=>告警", cols: "create_time",
			sql: "UPDATE t_exist_main SET status = 1, create_time = NOW() WHERE id = 1;", wantKey: "update_stmt_avoid_update_create_time_column",
		},
		{
			name: "配置为gmt_create时命中gmt_create", cols: "gmt_create",
			sql: "UPDATE t_exist_main SET gmt_create = NOW() WHERE id = 1;", wantKey: "update_stmt_avoid_update_create_time_column",
		},
		{
			name: "配置多列-命中其中一列", cols: "create_time,gmt_create",
			sql: "UPDATE t_exist_main SET gmt_create = NOW() WHERE id = 1;", wantKey: "update_stmt_avoid_update_create_time_column",
		},
		// 不误报
		{
			name: "更新update_time=>不告警", cols: "create_time",
			sql: "UPDATE t_exist_main SET update_time = NOW() WHERE id = 1;", wantKey: "",
		},
		{
			name: "更新普通列=>不告警", cols: "create_time",
			sql: "UPDATE t_exist_main SET status = 1 WHERE id = 1;", wantKey: "",
		},
		{
			name: "配置列与实际列不匹配=>不告警", cols: "gmt_create",
			sql: "UPDATE t_exist_main SET create_time = NOW() WHERE id = 1;", wantKey: "",
		},
		{
			name: "规则关闭=>不告警", cols: "",
			sql: "UPDATE t_exist_main SET create_time = NOW() WHERE id = 1;", wantKey: "",
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		if c.cols != "" {
			params.AVOID_UPDATE_COLUMNS = c.cols
		}
		data, err := runMySQLInspect(t, c.sql, params)
		if err != nil {
			t.Fatalf("%s: 审核执行失败: %v", c.name, err)
		}
		assertRuleHit(t, c.name, data, c.wantKey)
	}
}

// TestDMLInsertObjectExistsRule INSERT 语句检测目标表 / 字段是否存在
func TestDMLInsertObjectExistsRule(t *testing.T) {
	ensureExistsRuleTables(t)

	cases := []struct {
		name    string
		enable  bool
		sql     string
		wantKey string
	}{
		// 表不存在：无字段列表也要报（表都没有，字段判定无从谈起）
		{
			name: "INSERT表不存在-无字段列表=>告警", enable: true,
			sql: "INSERT INTO t_exist_no_such VALUES (10);", wantKey: "insert_stmt_check_column_exists",
		},
		{
			name: "INSERT表不存在-带字段列表=>告警", enable: true,
			sql: "INSERT INTO t_exist_no_such (id) VALUES (10);", wantKey: "insert_stmt_check_column_exists",
		},
		// 字段不存在
		{
			name: "INSERT字段不存在=>告警", enable: true,
			sql: "INSERT INTO t_exist_main (name, no_such_col) VALUES ('a', 1);", wantKey: "insert_stmt_check_column_exists",
		},
		{
			name: "INSERT多字段全部不存在=>告警", enable: true,
			sql: "INSERT INTO t_exist_main (bad_a, bad_b) VALUES (1, 2);", wantKey: "insert_stmt_check_column_exists",
		},
		// 不误报
		{
			name: "INSERT正常表与字段=>不告警", enable: true,
			sql: "INSERT INTO t_exist_main (name, status) VALUES ('a', 1);", wantKey: "",
		},
		{
			name: "INSERT大小写不同的字段=>不告警", enable: true,
			sql: "INSERT INTO t_exist_main (NAME, Status) VALUES ('a', 1);", wantKey: "",
		},
		{
			name: "INSERT无字段列表但表存在=>不告警", enable: true,
			sql: "INSERT INTO t_exist_main VALUES (NULL, 'a', 0, NOW(), NOW());", wantKey: "",
		},
		{
			name: "REPLACE表不存在=>告警", enable: true,
			sql: "REPLACE INTO t_exist_no_such VALUES (10);", wantKey: "insert_stmt_check_column_exists",
		},
		// 规则关闭
		{
			name: "规则关闭=>不告警", enable: false,
			sql: "INSERT INTO t_exist_no_such VALUES (10);", wantKey: "",
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		if c.enable {
			params.CHECK_INSERT_OBJECT_EXISTS = true
		}
		data, err := runMySQLInspect(t, c.sql, params)
		if err != nil {
			t.Fatalf("%s: 审核执行失败: %v", c.name, err)
		}
		assertRuleHit(t, c.name, data, c.wantKey)
	}
}

// assertRuleHit 断言审核结果是否命中指定 rule_key（wantKey 为空表示期望不命中任何带 rule_key 的告警）
func assertRuleHit(t *testing.T, name string, datas []ReturnData, wantKey string) {
	t.Helper()
	var msgs []string
	hit := false
	for _, item := range summariesOf(datas) {
		msgs = append(msgs, item.Message)
		if wantKey != "" && item.RuleKey == wantKey {
			hit = true
		}
		if wantKey == "" && item.RuleKey != "" {
			hit = true
		}
	}
	if wantKey != "" && !hit {
		t.Errorf("%s: 未命中规则 %s；实际告警: %v", name, wantKey, msgs)
	}
	if wantKey == "" && hit {
		t.Errorf("%s: 期望不告警，实际命中: %v", name, msgs)
	}
}
