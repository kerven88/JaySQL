package checker

// INSERT / UPDATE 语法语义类规则端到端测试（2026-09-29 新增实现的 7 条规则）。
//
// 覆盖：
//   1. insert_stmt_col_name_no_dup            字段列表重复
//   2. insert_stmt_specify_columns            未指定字段列表
//   3. insert_stmt_not_null_column_null_value 为 NOT NULL 列插入 NULL
//   4. insert_stmt_avoid_sysdate_func         使用 SYSDATE()
//   5. update_multi_table_column_prefix_check 多表 UPDATE 的 SET 列缺表前缀
//   6. update_stmt_check_updates_pk           更新主键
//   7. update_stmt_set_separator_check        SET 多列之间用 AND 分隔
//
// 直连本机 MySQL 真机执行，锁住两件事：
//   - 规则开启 => 必须报出对应 rule_key（此前这些规则在引擎里根本没有实现）；
//   - 规则关闭 => 绝不报出（开关必须真正受规范控制，而不是无条件告警）。

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

// ensureDMLRuleTables 固定 fixture：
//   - t_dml_rules：id 主键、name NOT NULL 且有索引、status 允许 NULL、update_time NOT NULL
//   - t_dml_rules_ref：多表 UPDATE 的关联表
func ensureDMLRuleTables(t *testing.T) {
	skipIfNoMatrixMySQL(t)
	t.Helper()
	db := &dao.DB{
		User: matrixMySQLUser, Password: matrixMySQLPass(),
		Host: matrixMySQLHost, Port: matrixMySQLPort,
		Database: matrixMySQLDB, DbType: "MySQL",
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS t_dml_rules (
			id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name varchar(64) NOT NULL DEFAULT '',
			status tinyint DEFAULT NULL,
			update_time datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
			KEY idx_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS t_dml_rules_ref (
			id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			rname varchar(64) NOT NULL DEFAULT ''
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`DELETE FROM t_dml_rules`,
		`DELETE FROM t_dml_rules_ref`,
		`INSERT INTO t_dml_rules (name, status) VALUES ('a', 0), ('b', 1)`,
		`INSERT INTO t_dml_rules_ref (rname) VALUES ('a')`,
	}
	for _, stmt := range stmts {
		if err := db.Execute(stmt); err != nil {
			t.Fatalf("准备 t_dml_rules 失败: %v, sql: %s", err, stmt)
		}
	}
}

func TestDMLInsertUpdateRules(t *testing.T) {
	ensureDMLRuleTables(t)

	cases := []struct {
		name    string
		enable  bool
		sql     string
		wantKey string // 期望命中的 rule_key（空表示不期望命中任何一条被测规则）
	}{
		// 1）字段列表重复
		{
			name: "INSERT字段重复=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, name) VALUES ('a', 'b');", wantKey: "insert_stmt_col_name_no_dup",
		},
		{
			name: "INSERT字段不重复=>不告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, status) VALUES ('a', 0);", wantKey: "",
		},
		// 2）未指定字段列表
		{
			name: "INSERT未指定字段列表=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules VALUES (1, 'a', 0, NOW());", wantKey: "insert_stmt_specify_columns",
		},
		// 3）NOT NULL 列插入 NULL（name 为 NOT NULL；status 允许 NULL，不应命中）
		{
			name: "INSERT为NOT NULL列插入NULL=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name) VALUES (NULL);", wantKey: "insert_stmt_not_null_column_null_value",
		},
		{
			name: "INSERT为可空列插入NULL=>不告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, status) VALUES ('a', NULL);", wantKey: "",
		},
		// 4）SYSDATE()
		{
			name: "INSERT使用SYSDATE=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, update_time) VALUES ('a', SYSDATE());", wantKey: "insert_stmt_avoid_sysdate_func",
		},
		{
			name: "INSERT使用NOW=>不告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, update_time) VALUES ('a', NOW());", wantKey: "",
		},
		// 4.5）字段列表和值列表数量匹配（规则 insert_stmt_columns_match_values）
		{
			name: "INSERT字段列表值数不足=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, status) VALUES ('a');", wantKey: "insert_stmt_columns_match_values",
		},
		{
			name: "INSERT字段列表值数过多=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name) VALUES ('a', 1);", wantKey: "insert_stmt_columns_match_values",
		},
		{
			name: "INSERT多行其一值数不符=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, status) VALUES ('a', 0), ('b');", wantKey: "insert_stmt_columns_match_values",
		},
		{
			name: "INSERT裸插入值数不符=>告警", enable: true,
			sql: "INSERT INTO t_dml_rules VALUES (1, 'a');", wantKey: "insert_stmt_columns_match_values",
		},
		{
			name: "INSERT字段列表值数匹配=>不告警", enable: true,
			sql: "INSERT INTO t_dml_rules(name, status) VALUES ('a', 0);", wantKey: "",
		},
		{
			name: "规则关闭-INSERT值数不匹配=>不告警", enable: false,
			sql: "INSERT INTO t_dml_rules(name) VALUES ('a', 1);", wantKey: "",
		},
		// 5）多表 UPDATE 的 SET 列缺表前缀
		{
			name: "多表UPDATE的SET列无表前缀=>告警", enable: true,
			sql:     "UPDATE t_dml_rules d JOIN t_dml_rules_ref r ON d.name = r.rname SET status = 1 WHERE r.rname = 'a';",
			wantKey: "update_multi_table_column_prefix_check",
		},
		{
			name: "多表UPDATE的SET列带表前缀=>不告警", enable: true,
			sql:     "UPDATE t_dml_rules d JOIN t_dml_rules_ref r ON d.name = r.rname SET d.status = 1 WHERE r.rname = 'a';",
			wantKey: "",
		},
		{
			name: "单表UPDATE的SET列不带前缀=>不告警", enable: true,
			sql: "UPDATE t_dml_rules SET status = 1 WHERE id = 1;", wantKey: "",
		},
		// 6）更新主键
		{
			name: "UPDATE更新主键列=>告警", enable: true,
			sql: "UPDATE t_dml_rules SET id = id + 100 WHERE id = 1;", wantKey: "update_stmt_check_updates_pk",
		},
		{
			name: "UPDATE更新非主键列=>不告警", enable: true,
			sql: "UPDATE t_dml_rules SET status = 2 WHERE id = 1;", wantKey: "",
		},
		// 7）SET 多列之间用 AND 分隔
		{
			name: "UPDATE用AND分隔多个SET列=>告警", enable: true,
			sql: "UPDATE t_dml_rules SET name = 'x' AND status = 1 WHERE id = 1;", wantKey: "update_stmt_set_separator_check",
		},
		{
			name: "UPDATE合法布尔表达式=>不误报", enable: true,
			sql: "UPDATE t_dml_rules SET status = (status > 0 AND status < 2) WHERE id = 1;", wantKey: "",
		},
		{
			name: "UPDATE用逗号分隔多个SET列=>不告警", enable: true,
			sql: "UPDATE t_dml_rules SET name = 'x', status = 1 WHERE id = 1;", wantKey: "",
		},
		// 规则关闭（level=off）=> 全部不告警：开关必须真正受规范控制
		{
			name: "规则关闭-INSERT字段重复=>不告警", enable: false,
			sql: "INSERT INTO t_dml_rules(name, name) VALUES ('a', 'b');", wantKey: "",
		},
		{
			name: "规则关闭-INSERT未指定字段列表=>不告警", enable: false,
			sql: "INSERT INTO t_dml_rules VALUES (1, 'a', 0, NOW());", wantKey: "",
		},
		{
			name: "规则关闭-INSERT为NOT NULL列插入NULL=>不告警", enable: false,
			sql: "INSERT INTO t_dml_rules(name) VALUES (NULL);", wantKey: "",
		},
		{
			name: "规则关闭-INSERT使用SYSDATE=>不告警", enable: false,
			sql: "INSERT INTO t_dml_rules(name, update_time) VALUES ('a', SYSDATE());", wantKey: "",
		},
		{
			name: "规则关闭-多表UPDATE无前缀=>不告警", enable: false,
			sql:     "UPDATE t_dml_rules d JOIN t_dml_rules_ref r ON d.name = r.rname SET status = 1 WHERE r.rname = 'a';",
			wantKey: "",
		},
		{
			name: "规则关闭-UPDATE更新主键=>不告警", enable: false,
			sql: "UPDATE t_dml_rules SET id = id + 100 WHERE id = 1;", wantKey: "",
		},
		{
			name: "规则关闭-UPDATE用AND分隔=>不告警", enable: false,
			sql: "UPDATE t_dml_rules SET name = 'x' AND status = 1 WHERE id = 1;", wantKey: "",
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		if c.enable {
			params.CHECK_INSERT_COLUMN_NO_DUP = true
			params.CHECK_INSERT_SPECIFY_COLUMNS = true
			params.CHECK_INSERT_NOT_NULL_COLUMN_NULL = true
			params.CHECK_INSERT_AVOID_SYSDATE = true
			params.CHECK_INSERT_COLUMNS_MATCH_VALUES = true
			params.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX = true
			params.CHECK_UPDATE_UPDATES_PK = true
			params.CHECK_UPDATE_SET_SEPARATOR = true
		}
		data, err := runMySQLInspect(t, c.sql, params)
		if err != nil {
			t.Fatalf("%s: 审核执行失败: %v", c.name, err)
		}
		var msgs []string
		hit := false
		for _, item := range summariesOf(data) {
			msgs = append(msgs, item.Message)
			if c.wantKey != "" && item.RuleKey == c.wantKey {
				hit = true
			}
			if c.wantKey == "" && item.RuleKey != "" {
				// 期望不命中被测规则：出现任何带 rule_key 的告警都算失败
				hit = true
			}
		}
		if c.wantKey != "" && !hit {
			t.Errorf("%s: 未命中规则 %s；实际告警: %v", c.name, c.wantKey, msgs)
		}
		if c.wantKey == "" && hit {
			t.Errorf("%s: 不应命中任何被测规则；实际告警: %v", c.name, msgs)
		}
	}
}
