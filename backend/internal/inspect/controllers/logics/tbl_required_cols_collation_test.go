package logics

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
	"github.com/simajiong/jaysql/pkg/parser"
)

// runRequiredColsChecks 解析建表语句并执行「表需要包含某些列」规则（tbl_需要include_某些col）
func runRequiredColsChecks(t *testing.T, sql string, params config.InspectParams) []controllers.SummaryItem {
	t.Helper()
	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v := &traverses.TraverseCreateTableColsRepeatDefine{}
	stmt.Accept(v)
	r := &controllers.RuleHint{InspectParams: &params}
	LogicCreateTableColsRepeatDefine(v, r)
	return r.Summary
}

// 规则 tbl_需要include_某些col：建表语句必须包含配置的列
func TestLogicTableRequiredColumns(t *testing.T) {
	const ruleKey = "table_must_include_required_columns"
	const sql = "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, name varchar(10), PRIMARY KEY (id));"

	params := config.DefaultInspectParams()
	params.TABLE_REQUIRED_COLUMNS = []string{"id", "created_at"}

	got := countByKey(runRequiredColsChecks(t, sql, params), ruleKey)
	if len(got) != 1 {
		t.Fatalf("缺少 created_at 应命中 1 条，实际 %d 条: %v", len(got), got)
	}
	if !strings.Contains(got[0], "created_at") {
		t.Errorf("告警文案应回显缺失列名，实际: %s", got[0])
	}

	// 合规：包含全部必需列
	full := "CREATE TABLE t_demo (id bigint NOT NULL AUTO_INCREMENT, name varchar(10), created_at datetime, PRIMARY KEY (id));"
	if got := countByKey(runRequiredColsChecks(t, full, params), ruleKey); len(got) != 0 {
		t.Errorf("包含全部必需列不应告警，实际: %v", got)
	}

	// 列名大小写不敏感
	mixed := "CREATE TABLE t_demo (ID bigint NOT NULL AUTO_INCREMENT, Created_At datetime, PRIMARY KEY (ID));"
	if got := countByKey(runRequiredColsChecks(t, mixed, params), ruleKey); len(got) != 0 {
		t.Errorf("列名大小写不同但语义一致不应告警，实际: %v", got)
	}

	// 规则关闭（列表为空）：不告警
	params.TABLE_REQUIRED_COLUMNS = nil
	if got := countByKey(runRequiredColsChecks(t, sql, params), ruleKey); len(got) != 0 {
		t.Errorf("规则关闭不应告警，实际: %v", got)
	}
}

// 规则 limit_tbl_collation：显式指定的表排序规则必须在白名单内
func TestProcessTableCollation(t *testing.T) {
	base := config.DefaultInspectParams()
	base.CHECK_TABLE_COLLATION = true
	base.TABLE_SUPPORT_COLLATION = []string{"utf8mb4_general_ci", "utf8mb4_unicode_ci"}

	// create：排序规则在白名单内（大小写不敏感）→ 通过
	t1 := process.TableOptions{
		Table: "t_demo", Type: "create", Charset: "utf8mb4", Collate: "UTF8MB4_GENERAL_CI", InspectParams: &base,
	}
	if err := t1.CheckTableCollation(); err != nil {
		t.Errorf("白名单内排序规则不应报错: %v", err)
	}

	// create：排序规则不在白名单 → 报错并回显可选值
	t2 := process.TableOptions{
		Table: "t_demo", Type: "create", Charset: "utf8mb4", Collate: "utf8mb4_bin", InspectParams: &base,
	}
	err := t2.CheckTableCollation()
	if err == nil {
		t.Fatalf("白名单外排序规则应报错")
	}
	if !strings.Contains(err.Error(), "utf8mb4_bin") || !strings.Contains(err.Error(), "utf8mb4_general_ci") {
		t.Errorf("报错文案应回显当前排序规则与可选值，实际: %s", err.Error())
	}

	// 未显式指定排序规则 → 不拦截（字符集匹配由 CheckTableCharset 负责）
	t3 := process.TableOptions{
		Table: "t_demo", Type: "create", Charset: "utf8mb4", InspectParams: &base,
	}
	if err := t3.CheckTableCollation(); err != nil {
		t.Errorf("未指定排序规则不应报错: %v", err)
	}

	// alter：显式改排序规则到白名单外 → 报错；未指定 → 不报错
	t4 := process.TableOptions{
		Table: "t_demo", Type: "alter", Collate: "latin1_swedish_ci", InspectParams: &base,
	}
	if err := t4.CheckTableCollation(); err == nil {
		t.Errorf("ALTER 到白名单外排序规则应报错")
	}
	t5 := process.TableOptions{
		Table: "t_demo", Type: "alter", InspectParams: &base,
	}
	if err := t5.CheckTableCollation(); err != nil {
		t.Errorf("ALTER 未指定排序规则不应报错: %v", err)
	}

	// 规则关闭 → 不检查
	off := base
	off.CHECK_TABLE_COLLATION = false
	t6 := process.TableOptions{
		Table: "t_demo", Type: "create", Charset: "utf8mb4", Collate: "utf8mb4_bin", InspectParams: &off,
	}
	if err := t6.CheckTableCollation(); err != nil {
		t.Errorf("规则关闭不应报错: %v", err)
	}
}
