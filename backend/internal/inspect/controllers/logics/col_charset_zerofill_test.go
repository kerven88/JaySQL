package logics

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
	"github.com/simajiong/jaysql/pkg/parser"
)

// runCreateChecks 解析建表语句并跑「列字符集/校对集 + ZEROFILL」三条规则
func runCreateChecks(t *testing.T, sql string, params config.InspectParams) []controllers.SummaryItem {
	t.Helper()
	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	vc := &traverses.TraverseCreateTableColsCharset{}
	stmt.Accept(vc)
	vz := &traverses.TraverseCreateTableColsZerofill{}
	stmt.Accept(vz)
	r := &controllers.RuleHint{InspectParams: &params}
	LogicCreateTableColsCharset(vc, r)
	LogicCreateTableColsZerofill(vz, r)
	return r.Summary
}

// runAlterChecks 解析 ALTER 语句并跑同一组规则
func runAlterChecks(t *testing.T, sql string, params config.InspectParams) []controllers.SummaryItem {
	t.Helper()
	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	vc := &traverses.TraverseAlterTableColCharset{}
	stmt.Accept(vc)
	vz := &traverses.TraverseAlterTableColZerofill{}
	stmt.Accept(vz)
	r := &controllers.RuleHint{InspectParams: &params}
	LogicAlterTableColCharset(vc, r)
	LogicAlterTableColZerofill(vz, r)
	return r.Summary
}

func allRulesOn() config.InspectParams {
	params := config.DefaultInspectParams()
	params.CHECK_COLUMN_CHARSET = true
	params.CHECK_COLUMN_COLLATION = true
	params.DISABLE_COLUMN_ZEROFILL = true
	return params
}

func countByKey(items []controllers.SummaryItem, ruleKey string) []string {
	var out []string
	for _, it := range items {
		if it.RuleKey == ruleKey {
			out = append(out, it.Message)
		}
	}
	return out
}

// 规则 no_set_col_的charset：不能显式设置列的字符集
func TestLogicNoSetColCharset(t *testing.T) {
	const ruleKey = "no_column_charset"
	const sql = "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, c1 varchar(10) CHARACTER SET utf8mb4, PRIMARY KEY (id));"

	params := allRulesOn()
	got := countByKey(runCreateChecks(t, sql, params), ruleKey)
	if len(got) != 1 {
		t.Fatalf("显式指定列字符集应命中 1 条，实际 %d 条: %v", len(got), got)
	}
	if !strings.Contains(got[0], "`c1`") || !strings.Contains(got[0], "utf8mb4") {
		t.Errorf("告警文案应回显列名与字符集，实际: %s", got[0])
	}

	// 合规：未显式指定字符集
	clean := "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, c1 varchar(10), PRIMARY KEY (id));"
	if got := countByKey(runCreateChecks(t, clean, params), ruleKey); len(got) != 0 {
		t.Errorf("未显式指定字符集不应告警，实际: %v", got)
	}

	// 规则关闭：不告警
	params.CHECK_COLUMN_CHARSET = false
	if got := countByKey(runCreateChecks(t, sql, params), ruleKey); len(got) != 0 {
		t.Errorf("规则关闭不应告警，实际: %v", got)
	}
}

// 规则 no_set_col_的collate：不能显式设置列的校对集
func TestLogicNoSetColCollate(t *testing.T) {
	const ruleKey = "no_column_collate"
	const sql = "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, c1 varchar(10) COLLATE utf8mb4_bin, PRIMARY KEY (id));"

	params := allRulesOn()
	got := countByKey(runCreateChecks(t, sql, params), ruleKey)
	if len(got) != 1 {
		t.Fatalf("显式指定列校对集应命中 1 条，实际 %d 条: %v", len(got), got)
	}
	if !strings.Contains(got[0], "`c1`") || !strings.Contains(got[0], "utf8mb4_bin") {
		t.Errorf("告警文案应回显列名与校对集，实际: %s", got[0])
	}

	// 合规：未显式指定校对集
	clean := "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, c1 varchar(10), PRIMARY KEY (id));"
	if got := countByKey(runCreateChecks(t, clean, params), ruleKey); len(got) != 0 {
		t.Errorf("未显式指定校对集不应告警，实际: %v", got)
	}

	// 规则关闭：不告警
	params.CHECK_COLUMN_COLLATION = false
	if got := countByKey(runCreateChecks(t, sql, params), ruleKey); len(got) != 0 {
		t.Errorf("规则关闭不应告警，实际: %v", got)
	}
}

// 规则 col_禁用_zerofill_属性：字段禁用 ZEROFILL 属性
func TestLogicDisableColZerofill(t *testing.T) {
	const ruleKey = "column_zerofill_forbidden"
	const sql = "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, c1 int(5) UNSIGNED ZEROFILL, PRIMARY KEY (id));"

	params := allRulesOn()
	got := countByKey(runCreateChecks(t, sql, params), ruleKey)
	if len(got) != 1 {
		t.Fatalf("使用 ZEROFILL 应命中 1 条，实际 %d 条: %v", len(got), got)
	}
	if !strings.Contains(got[0], "`c1`") {
		t.Errorf("告警文案应回显列名，实际: %s", got[0])
	}

	// 合规：未使用 ZEROFILL
	clean := "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, c1 int(5) UNSIGNED, PRIMARY KEY (id));"
	if got := countByKey(runCreateChecks(t, clean, params), ruleKey); len(got) != 0 {
		t.Errorf("未使用 ZEROFILL 不应告警，实际: %v", got)
	}

	// 规则关闭：不告警
	params.DISABLE_COLUMN_ZEROFILL = false
	if got := countByKey(runCreateChecks(t, sql, params), ruleKey); len(got) != 0 {
		t.Errorf("规则关闭不应告警，实际: %v", got)
	}
}

// ALTER TABLE 路径：三条规则同样生效
func TestLogicAlterTableColCharsetAndZerofill(t *testing.T) {
	params := allRulesOn()

	// MODIFY：同时显式指定字符集与校对集 → 两条各命中 1 条
	alter1 := "ALTER TABLE t_demo MODIFY COLUMN c1 varchar(10) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;"
	summary := runAlterChecks(t, alter1, params)
	if got := countByKey(summary, "no_column_charset"); len(got) != 1 {
		t.Errorf("ALTER MODIFY 显式字符集应命中 1 条，实际: %v", got)
	}
	if got := countByKey(summary, "no_column_collate"); len(got) != 1 {
		t.Errorf("ALTER MODIFY 显式校对集应命中 1 条，实际: %v", got)
	}

	// ADD COLUMN 带 ZEROFILL → 命中
	alter2 := "ALTER TABLE t_demo ADD COLUMN c4 int(5) UNSIGNED ZEROFILL;"
	if got := countByKey(runAlterChecks(t, alter2, params), "column_zerofill_forbidden"); len(got) != 1 {
		t.Errorf("ALTER ADD 带 ZEROFILL 应命中 1 条，实际: %v", got)
	}

	// 与三条规则无关的 ALTER（加索引）→ 0 告警
	alter3 := "ALTER TABLE t_demo ADD INDEX idx_c1 (c1);"
	if got := runAlterChecks(t, alter3, params); len(got) != 0 {
		t.Errorf("与规则无关的 ALTER 不应告警，实际: %v", got)
	}

	// 合规的 MODIFY → 0 告警
	alter4 := "ALTER TABLE t_demo MODIFY COLUMN c1 varchar(10) NOT NULL DEFAULT '';"
	if got := runAlterChecks(t, alter4, params); len(got) != 0 {
		t.Errorf("合规 ALTER 不应告警，实际: %v", got)
	}
}
