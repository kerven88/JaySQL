package checker

// 「UPDATE/DELETE 语句必须使用索引」（规则 update_delete_stmt_must_use_idx）端到端测试。
//
// 直连本机 MySQL（与 struct_matrix_test 同一套连接常量），真实执行 EXPLAIN 判定，
// 锁住 MySQL 8 的行为：EXPLAIN UPDATE 恒带 key=PRIMARY，只看 key 会漏判，必须看 type。

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

const mustUseIdxRuleKey = "update_delete_stmt_must_use_idx"

// ensureMustUseIdxTable 固定 fixture：id 主键、name 普通索引、status 无索引
func ensureMustUseIdxTable(t *testing.T) {
	skipIfNoMatrixMySQL(t)
	t.Helper()
	db := &dao.DB{
		User: matrixMySQLUser, Password: matrixMySQLPass(),
		Host: matrixMySQLHost, Port: matrixMySQLPort,
		Database: matrixMySQLDB, DbType: "MySQL",
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS t_must_idx (
			id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name varchar(64) NOT NULL DEFAULT '',
			status tinyint NOT NULL DEFAULT 0,
			KEY idx_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`DELETE FROM t_must_idx`,
		`INSERT INTO t_must_idx (name, status) VALUES ('a', 0), ('b', 0), ('c', 1), ('d', 1)`,
	}
	for _, stmt := range stmts {
		if err := db.Execute(stmt); err != nil {
			t.Fatalf("准备 t_must_idx 失败: %v, sql: %s", err, stmt)
		}
	}
}

func TestDMLMustUseIndexRule(t *testing.T) {
	ensureMustUseIdxTable(t)

	cases := []struct {
		name    string
		enable  bool
		sql     string
		wantHit bool
	}{
		{
			name: "UPDATE按无索引列过滤=>告警", enable: true,
			sql: "UPDATE t_must_idx SET status = 1 WHERE status = 0;", wantHit: true,
		},
		{
			name: "UPDATE无WHERE(全表更新)=>告警", enable: true,
			sql: "UPDATE t_must_idx SET status = 1;", wantHit: true,
		},
		{
			name: "UPDATE按主键过滤=>不告警", enable: true,
			sql: "UPDATE t_must_idx SET status = 1 WHERE id = 1;", wantHit: false,
		},
		{
			name: "UPDATE按索引列过滤=>不告警", enable: true,
			sql: "UPDATE t_must_idx SET status = 1 WHERE name = 'a';", wantHit: false,
		},
		{
			name: "DELETE按无索引列过滤=>告警", enable: true,
			sql: "DELETE FROM t_must_idx WHERE status = 0;", wantHit: true,
		},
		{
			name: "DELETE按索引列过滤=>不告警", enable: true,
			sql: "DELETE FROM t_must_idx WHERE name = 'a';", wantHit: false,
		},
		{
			name: "规则关闭=>不告警", enable: false,
			sql: "UPDATE t_must_idx SET status = 1 WHERE status = 0;", wantHit: false,
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		params.CHECK_DML_MUST_USE_INDEX = c.enable
		data, err := runMySQLInspect(t, c.sql, params)
		if err != nil {
			t.Fatalf("%s: 审核执行失败: %v", c.name, err)
		}
		hit := false
		var msgs []string
		for _, item := range summariesOf(data) {
			msgs = append(msgs, item.Message)
			if item.RuleKey == mustUseIdxRuleKey {
				hit = true
			}
		}
		if hit != c.wantHit {
			t.Errorf("%s: 命中=%v, 期望=%v；实际告警: %v", c.name, hit, c.wantHit, msgs)
		}
	}
}
