package checker

// WHERE 条件类规则 + UPDATE 更新唯一键 的端到端测试（2026-09-29 新增实现）。
//
// 覆盖：
//   1. update_stmt_check_updates_unique_key      UPDATE 更新唯一键
//   2. where_avoid_leading_wildcard              LIKE 前通配符
//   3. where_like_without_wildcard               LIKE 无通配符
//   4. where_avoid_not_in_not_like               NOT IN / NOT LIKE
//   5. where_or_condition_check                  OR 连接过滤条件
//   6. where_avoid_index_col_arithmetic_or_func  索引列参与数学运算/函数运算
//   7. where_implicit_type_cast_check            隐式类型转换
//
// 直连本机 MySQL 真机执行，锁住两件事：
//   - 规则开启 => 必须报出对应 rule_key（此前这些规则在引擎里根本没有实现）；
//   - 合法写法不误报、规则关闭不报。

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

// ensureWhereRuleTable 固定 fixture：
//   - id 主键、code 唯一键、name 普通索引、status 无索引（用于验证「只有索引列参与运算才报」）
func ensureWhereRuleTable(t *testing.T) {
	skipIfNoMatrixMySQL(t)
	t.Helper()
	db := &dao.DB{
		User: matrixMySQLUser, Password: matrixMySQLPass(),
		Host: matrixMySQLHost, Port: matrixMySQLPort,
		Database: matrixMySQLDB, DbType: "MySQL",
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS t_where_rules (
			id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name varchar(64) NOT NULL DEFAULT '',
			code varchar(32) NOT NULL DEFAULT '',
			status tinyint NOT NULL DEFAULT 0,
			UNIQUE KEY uk_code (code),
			KEY idx_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`DELETE FROM t_where_rules`,
		`INSERT INTO t_where_rules (name, code, status) VALUES ('a','c1',0), ('b','c2',1)`,
	}
	for _, stmt := range stmts {
		if err := db.Execute(stmt); err != nil {
			t.Fatalf("准备 t_where_rules 失败: %v, sql: %s", err, stmt)
		}
	}
}

func TestDMLWhereRules(t *testing.T) {
	ensureWhereRuleTable(t)

	cases := []struct {
		name    string
		enable  bool
		sql     string
		wantKey string // 期望命中的 rule_key（空表示不期望命中任何带 rule_key 的告警）
	}{
		// 1）UPDATE 更新唯一键
		{
			name: "UPDATE更新唯一键=>告警", enable: true,
			sql: "UPDATE t_where_rules SET code = 'x' WHERE id = 1;", wantKey: "update_stmt_check_updates_unique_key",
		},
		{
			name: "UPDATE更新普通索引列=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET name = 'x' WHERE id = 1;", wantKey: "",
		},
		{
			name: "UPDATE更新主键=>不归唯一键规则", enable: true,
			sql: "UPDATE t_where_rules SET id = id + 100 WHERE id = 1;", wantKey: "",
		},
		// 2）LIKE 前通配符
		{
			name: "LIKE前通配符=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name LIKE '%a';", wantKey: "where_avoid_leading_wildcard",
		},
		{
			name: "LIKE后缀通配符=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name LIKE 'a%';", wantKey: "",
		},
		{
			name: "DELETE的LIKE前通配符=>告警", enable: true,
			sql: "DELETE FROM t_where_rules WHERE name LIKE '_a';", wantKey: "where_avoid_leading_wildcard",
		},
		// 3）LIKE 无通配符
		{
			name: "LIKE无通配符=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name LIKE 'a';", wantKey: "where_like_without_wildcard",
		},
		{
			name: "LIKE含通配符=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name LIKE 'a%';", wantKey: "",
		},
		// 4）NOT IN / NOT LIKE
		{
			name: "NOT IN=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id NOT IN (1, 2);", wantKey: "where_avoid_not_in_not_like",
		},
		{
			name: "IN=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id IN (1, 2);", wantKey: "",
		},
		{
			name: "NOT LIKE=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name NOT LIKE 'a%';", wantKey: "where_avoid_not_in_not_like",
		},
		// 5）OR 连接过滤条件
		{
			name: "OR连接过滤条件=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name = 'a' OR code = 'c1';", wantKey: "where_or_condition_check",
		},
		{
			name: "AND连接=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name = 'a' AND code = 'c1';", wantKey: "",
		},
		// 6）索引列参与数学运算/函数运算
		{
			name: "索引列算术运算=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id + 1 = 2;", wantKey: "where_avoid_index_col_arithmetic_or_func",
		},
		{
			name: "索引列函数运算=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE UPPER(name) = 'A';", wantKey: "where_avoid_index_col_arithmetic_or_func",
		},
		{
			name: "索引列直接比较=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id = 1;", wantKey: "",
		},
		{
			name: "非索引列运算=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE status + 1 = 2;", wantKey: "",
		},
		// 7）隐式类型转换
		{
			name: "字符列与数字常量比较=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name = 1;", wantKey: "where_implicit_type_cast_check",
		},
		{
			name: "数字列与字符串常量比较=>告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id = '1';", wantKey: "where_implicit_type_cast_check",
		},
		{
			name: "字符列与字符串常量=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name = 'a';", wantKey: "",
		},
		{
			name: "数字列与数字常量=>不告警", enable: true,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id = 1;", wantKey: "",
		},
		// 规则关闭（level=off）=> 全部不告警
		{
			name: "规则关闭-LIKE前通配符=>不告警", enable: false,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name LIKE '%a';", wantKey: "",
		},
		{
			name: "规则关闭-NOT IN=>不告警", enable: false,
			sql: "UPDATE t_where_rules SET status = 1 WHERE id NOT IN (1, 2);", wantKey: "",
		},
		{
			name: "规则关闭-OR=>不告警", enable: false,
			sql: "UPDATE t_where_rules SET status = 1 WHERE name = 'a' OR code = 'c1';", wantKey: "",
		},
		{
			name: "规则关闭-更新唯一键=>不告警", enable: false,
			sql: "UPDATE t_where_rules SET code = 'x' WHERE id = 1;", wantKey: "",
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		if c.enable {
			params.CHECK_UPDATE_UPDATES_UNIQUE_KEY = true
			params.CHECK_WHERE_LEADING_WILDCARD = true
			params.CHECK_WHERE_LIKE_WITHOUT_WILDCARD = true
			params.CHECK_WHERE_NOT_IN_NOT_LIKE = true
			params.CHECK_WHERE_OR_CONDITION = true
			params.CHECK_WHERE_INDEX_COL_CALC = true
			params.CHECK_WHERE_IMPLICIT_CAST = true
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

// ensureWhereRuleUserTable 复刻用户反馈场景的 fixture：
//   - id 主键、name 普通索引、status 字符列、**created_at 无索引**（时间列最常见没有索引，
//     正是"写了函数运算却因为不是索引列而整体漏检"的重灾区）
func ensureWhereRuleUserTable(t *testing.T) {
	skipIfNoMatrixMySQL(t)
	t.Helper()
	db := &dao.DB{
		User: matrixMySQLUser, Password: matrixMySQLPass(),
		Host: matrixMySQLHost, Port: matrixMySQLPort,
		Database: matrixMySQLDB, DbType: "MySQL",
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name varchar(64) NOT NULL DEFAULT '',
			status varchar(16) NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
			KEY idx_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`DELETE FROM users`,
		`INSERT INTO users (name, status, created_at) VALUES ('User_1','0','2023-10-01 00:00:00')`,
	}
	for _, stmt := range stmts {
		if err := db.Execute(stmt); err != nil {
			t.Fatalf("准备 users 失败: %v, sql: %s", err, stmt)
		}
	}
}

// TestDMLWhereRulesUserCases 用户实测漏检的三条用例 + 变体与误报边界。
//
// 背景：这三条规则此前在 UPDATE/DELETE 上判不出，根因是判定口径过严：
//  1. 索引列运算 —— 强依赖索引元数据，非索引列（如 created_at）上的函数运算整体静默；
//  2. OR 连接    —— 要求两侧都引用列，`id = 1 OR 1 = 1` 这类恒真条件被漏掉；
//  3. LIKE 无通配符 —— 把 `_` 也算通配符，`LIKE 'User_1'` 这种字面下划线写法永远命中不了。
func TestDMLWhereRulesUserCases(t *testing.T) {
	ensureWhereRuleUserTable(t)

	cases := []struct {
		name    string
		sql     string
		wantKey string
	}{
		// 用户反馈的三条原始用例
		{
			name: "用例1-DATE包裹非索引列", sql: "update users set status='1111' WHERE DATE(created_at) = '2023-10-01';",
			wantKey: "where_avoid_index_col_arithmetic_or_func",
		},
		{
			name: "用例2-列条件OR恒真常量", sql: "update users set status='1111' WHERE id = 1 OR 1=1;",
			wantKey: "where_or_condition_check",
		},
		{
			name: "用例3-LIKE字面下划线", sql: "update users set status='1111' WHERE name LIKE 'User_1';",
			wantKey: "where_like_without_wildcard",
		},
		// 变体：大小写 / 前置注释 / 行注释 / 结尾分号 / 多空格 / DELETE
		{
			name: "变体-全大写", sql: "UPDATE users SET status='1111' WHERE DATE(created_at) = '2023-10-01'",
			wantKey: "where_avoid_index_col_arithmetic_or_func",
		},
		{
			name: "变体-块注释前置", sql: "/* 清理历史数据 */\nupdate users set status='1111' WHERE DATE(created_at) = '2023-10-01';",
			wantKey: "where_avoid_index_col_arithmetic_or_func",
		},
		{
			name: "变体-行注释与多空格", sql: "-- 清理\nupdate users set status='1111' WHERE  id  =  1  OR  1 = 1 ;",
			wantKey: "where_or_condition_check",
		},
		{
			name: "变体-DELETE的LIKE", sql: "delete from users where name like 'User_1';",
			wantKey: "where_like_without_wildcard",
		},
		{
			name: "变体-DELETE的函数运算", sql: "DELETE FROM users WHERE DATE(created_at) = '2023-10-01';",
			wantKey: "where_avoid_index_col_arithmetic_or_func",
		},
		// 误报边界
		{name: "边界-时间列直接比较", sql: "update users set status='1111' WHERE created_at = '2023-10-01';", wantKey: ""},
		{name: "边界-LIKE含百分号", sql: "update users set status='1111' WHERE name LIKE 'User_1%';", wantKey: ""},
		{name: "边界-纯常量OR", sql: "update users set status='1111' WHERE 1 = 1 OR 2 = 2;", wantKey: ""},
		{name: "边界-主键等值", sql: "update users set status='1111' WHERE id = 1;", wantKey: ""},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		params.CHECK_WHERE_LEADING_WILDCARD = true
		params.CHECK_WHERE_LIKE_WITHOUT_WILDCARD = true
		params.CHECK_WHERE_NOT_IN_NOT_LIKE = true
		params.CHECK_WHERE_OR_CONDITION = true
		params.CHECK_WHERE_INDEX_COL_CALC = true
		params.CHECK_WHERE_IMPLICIT_CAST = true
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
