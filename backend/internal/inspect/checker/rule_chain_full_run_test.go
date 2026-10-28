package checker

// 规则链「全量执行」回归测试（2026-10-14）
//
// 锁定一件事：**一条语句上的所有规则都必须执行完，命中结果一并收集**，
// 任何一条规则的命中（哪怕是 must 级结论）都不得让后续规则消失。
//
// 直接来自真实事故（工单 tk-20ytmktike0m，2026-10-14 生产 #267）：
// `update test_01.user set email='100';` 没有 WHERE，命中了「必须包含 WHERE」并置 IsBreak，
// 把排在其后的 must 级「限制 UPDATE/DELETE 单个语句总影响行数」整条短路掉 ——
// 风险最高的无条件全表 UPDATE 反倒免检、还能提交。旧的顺序契约测试只校验了
// 「横切规则排在可能 break 的规则之前」，没发现 breaker 之间也会互相截断，所以漏网很久。
//
// 因此这组用例断言的都是「两件事同时发生」，单边断言无法防住回归：
//   - 命中前序规则 AND 命中本应被其掩盖的后序规则；
//   - 单条规则内部的子检查也必须跑完（REPLACE 曾直接 return 跳过后续子检查）。
//
// 依赖本机 MySQL（127.0.0.1:3306，见 struct_matrix_test.go 的常量），
// 与其它 checker 端到端测试同源；不连库时这组用例整体失败（与既有测试行为一致）。

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

const chainFixtureTable = "t_chain_fix"

// ensureChainFixture 固定 fixture：100 行数据，保证 EXPLAIN 的扫描行数估算稳定大于阈值 1
func ensureChainFixture(t *testing.T) {
	skipIfNoMatrixMySQL(t)
	t.Helper()
	ensureMySQLFixtures(t)
	db := &dao.DB{
		User: matrixMySQLUser, Password: matrixMySQLPass(),
		Host: matrixMySQLHost, Port: matrixMySQLPort,
		Database: matrixMySQLDB, DbType: "MySQL",
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ` + chainFixtureTable + ` (
			id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name varchar(64) NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`DELETE FROM ` + chainFixtureTable,
	}
	for _, s := range stmts {
		if err := db.Execute(s); err != nil {
			t.Fatalf("准备 %s 失败: %v, sql: %s", chainFixtureTable, err, s)
		}
	}
	// 100 行：EXPLAIN 估算值随统计信息浮动，但只要 > 1 就能触发「影响行数超限」
	var values []string
	for i := 0; i < 100; i++ {
		values = append(values, "('n')")
	}
	if err := db.Execute(`INSERT INTO ` + chainFixtureTable + ` (name) VALUES ` + strings.Join(values, ",")); err != nil {
		t.Fatalf("灌入 %s 数据失败: %v", chainFixtureTable, err)
	}
	if err := db.Execute(`ANALYZE TABLE ` + chainFixtureTable); err != nil {
		t.Fatalf("ANALYZE %s 失败: %v", chainFixtureTable, err)
	}
}

// chainParams 本组用例共用的参数基线：EXPLAIN 取最大值、影响行数上限 1、表引擎限定 InnoDB
func chainParams() config.InspectParams {
	return config.InspectParams{
		EXPLAIN_RULE:            "max",
		MAX_AFFECTED_ROWS:       1,
		DML_MUST_HAVE_WHERE:     true,
		CHECK_TABLE_ENGINE:      true,
		TABLE_SUPPORT_ENGINE:    []string{"InnoDB"},
		CHECK_TABLE_PRIMARY_KEY: true,
	}
}

// keysOf 取出命中集合里的全部 rule key
func keysOf(items []controllers.SummaryItem) map[string]bool {
	keys := make(map[string]bool, len(items))
	for _, s := range items {
		if s.RuleKey != "" {
			keys[s.RuleKey] = true
		}
	}
	return keys
}

// messagesOf 取出命中集合里的全部文案
func messagesOf(items []controllers.SummaryItem) string {
	var sb strings.Builder
	for _, s := range items {
		sb.WriteString(s.Message)
		sb.WriteString("\n")
	}
	return sb.String()
}

// assertChain 执行一条语句并返回其唯一一条结果（本组用例都是单语句）
func assertChain(t *testing.T, sql string, params config.InspectParams) ReturnData {
	t.Helper()
	datas, err := runMySQLInspect(t, sql, params)
	if err != nil {
		t.Fatalf("审核失败: %v, sql: %s", err, sql)
	}
	if len(datas) != 1 {
		t.Fatalf("语句数应为 1，实际 %d，sql: %s", len(datas), sql)
	}
	return datas[0]
}

// TestUpdateWithoutWhereAlsoReportsAffectedRows 核心回归：无 WHERE 的 UPDATE
//
// 修复前：只报「必须包含 WHERE」，must 级的「影响行数超限」被短路吞掉。
// 修复后：两条同时报出 —— 这正是工单 tk-20ytmktike0m 的场景。
func TestUpdateWithoutWhereAlsoReportsAffectedRows(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t, "UPDATE "+chainFixtureTable+" SET name='z';", chainParams())

	keys := keysOf(data.Summary)
	if !keys["update_delete_stmt_suggest_where"] {
		t.Errorf("缺少「必须包含 WHERE」告警：%s", messagesOf(data.Summary))
	}
	if !keys["limit_update_delete_affected_rows"] {
		t.Fatalf("「影响行数超限」仍未执行（被前序规则短路）：%s", messagesOf(data.Summary))
	}
	if data.AffectedRows <= 0 {
		t.Errorf("AffectedRows 应被回填为正数，实际 %d", data.AffectedRows)
	}
}

// TestDeleteWithoutWhereAlsoReportsAffectedRows DELETE 同样不得被短路
func TestDeleteWithoutWhereAlsoReportsAffectedRows(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t, "DELETE FROM "+chainFixtureTable+";", chainParams())

	keys := keysOf(data.Summary)
	if !keys["update_delete_stmt_suggest_where"] {
		t.Errorf("缺少「必须包含 WHERE」告警：%s", messagesOf(data.Summary))
	}
	if !keys["limit_update_delete_affected_rows"] {
		t.Fatalf("「影响行数超限」仍未执行：%s", messagesOf(data.Summary))
	}
}

// TestUpdateWithWhereSkipsWhereRuleOnly 对照组：带 WHERE 时不报「必须有 WHERE」，
// 但「影响行数」规则照常执行（说明规则是按条件各自判定，不是整链跳过）
func TestUpdateWithWhereSkipsWhereRuleOnly(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t, "UPDATE "+chainFixtureTable+" SET name='z' WHERE id=1;", chainParams())

	if keysOf(data.Summary)["update_delete_stmt_suggest_where"] {
		t.Errorf("带 WHERE 的语句不应报「必须包含 WHERE」：%s", messagesOf(data.Summary))
	}
	if data.AffectedRows <= 0 {
		t.Errorf("「影响行数」规则应照常回填 AffectedRows，实际 %d", data.AffectedRows)
	}
}

// TestUpdateWithMultipleConstraintsReportsAll 一条语句同时踩多个约束时全部报出
//
// 修复前：`DML#不能有LIMIT/ORDERBY/SubQuery` 命中 ORDER BY 即 IsBreak，
// 排在其后的「影响行数」同样被吞。
func TestUpdateWithMultipleConstraintsReportsAll(t *testing.T) {
	ensureChainFixture(t)
	params := chainParams()
	params.DML_DISABLE_ORDERBY = true
	data := assertChain(t, "UPDATE "+chainFixtureTable+" SET name='z' ORDER BY id LIMIT 3;", params)

	keys := keysOf(data.Summary)
	for _, want := range []string{
		"update_delete_stmt_suggest_where",
		"update_delete_stmt_no_order_by_clause",
		"limit_update_delete_affected_rows",
	} {
		if !keys[want] {
			t.Errorf("未命中规则 %s：%s", want, messagesOf(data.Summary))
		}
	}
}

// 关于 CTAS/CTL/CREATE VIEW：被禁止时它们**不在本组用例的范围内**。
//
// 曾经这里有一条 `TestCreateTableAsAlsoReportsEngine`，断言「CTAS 提示命中后，
// 后续的表结构类规则照常执行」。2026-10-14 口径变更后该断言被**有意推翻**：
// CTAS/CTL 属于「整类不允许执行」的语句（语句级判定），命中即由语句准入闸门
// 终止该语句的规则链 —— 它们的表结构继承自源表/SELECT，继续报「必须显式指定存储引擎」
// 「必须定义主键」只是无法满足的伪结论（见 statement_admission_gate_test.go）。
// CREATE VIEW 于同日并入同一层闸门。
//
// 边界很清楚：闸门管「这类语句能不能执行」，本文件管「语句允许执行时规则有没有跑全」。
// 后者才是不能退让的红线，所以这里保留「放行时链必须跑全」这一侧；
// 「被拒时只给一条语句级结论」那一侧见 statement_admission_gate_test.go 的
// TestCreateViewRejectedByGate。

// TestCreateViewAllowedRunsWholeChain 放行视图后，存在性/依赖表检查照常执行
//
// 修复前：`LogicCreateViewIsExist` 在「禁止创建视图」后直接 return，
// 把同一条语句上的「依赖表不存在」一起吞掉。现在该禁止分支已上移到闸门，
// 规则链自身不再有任何提前 return —— 放行即全量执行。
func TestCreateViewAllowedRunsWholeChain(t *testing.T) {
	ensureChainFixture(t)
	params := chainParams()
	params.ENABLE_CREATE_VIEW = true

	data := assertChain(t, "CREATE VIEW v_chain_probe AS SELECT id FROM t_chain_fix_nonexistent;", params)

	if !keysOf(data.Summary)["obj_tbl_must_exist"] {
		t.Fatalf("「依赖表必须存在」未执行（链被提前 return 吞掉）：%s", messagesOf(data.Summary))
	}
}

// TestAlterAddConstraintRunsWholeChain ADD CONSTRAINT 正则在命中后不再提前 return
//
// 修复前：`checker/stmt.go` 用正则识别 ADD CONSTRAINT 后直接 return，
// 整条 ALTER 规则链一条都不跑（连表存在性都不查）。
func TestAlterAddConstraintRunsWholeChain(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t, "ALTER TABLE t_chain_fix_nonexistent ADD CONSTRAINT ck CHECK (id > 0);", chainParams())

	msg := messagesOf(data.Summary)
	if !strings.Contains(msg, "禁止使用ALTER TABLE...ADD CONSTRAINT...语法") {
		t.Errorf("缺少 ADD CONSTRAINT 提示：%s", msg)
	}
	if !keysOf(data.Summary)["obj_tbl_must_exist"] {
		t.Fatalf("ALTER 链未执行（被正则提前 return）：%s", msg)
	}
}

// TestReplaceRunsInsertSubChecks 单条规则内部的子检查也必须跑完
//
// 修复前：`LogicDMLInsertWithColumns` 遇到被禁的 REPLACE 时 `IsBreak=true; return`，
// 同一函数内随后的字段列表/NOT NULL/行数检查全部被跳过。
func TestReplaceRunsInsertSubChecks(t *testing.T) {
	ensureChainFixture(t)
	params := chainParams()
	params.DISABLE_REPLACE = true
	params.CHECK_INSERT_SPECIFY_COLUMNS = true

	data := assertChain(t, "REPLACE INTO "+chainFixtureTable+" VALUES (1,'x');", params)
	msg := messagesOf(data.Summary)

	if !strings.Contains(msg, "禁止使用 `REPLACE` 语句") {
		t.Errorf("缺少 REPLACE 禁用提示：%s", msg)
	}
	if !keysOf(data.Summary)["insert_stmt_specify_columns"] {
		t.Fatalf("REPLACE 分支的 return 仍在吞掉后续子检查：%s", msg)
	}
}

// TestInsertAffectedRowsPreserved INSERT 的行数回填不被后续规则覆盖
//
// 原实现靠「插入影响行数无条件 break」保住这个值；去掉短路后必须靠聚合逻辑保住。
func TestInsertAffectedRowsPreserved(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t, "INSERT INTO "+chainFixtureTable+" (name) VALUES ('a'),('b'),('c');", chainParams())

	if data.AffectedRows != 3 {
		t.Errorf("INSERT 的 AffectedRows 应为 3，实际 %d（被后续规则的零值覆盖？）", data.AffectedRows)
	}
}

// TestNoDuplicateIdenticalSummaries 同一结论不得重复堆叠
//
// 全链执行后，多条规则可能基于同一个失败前提得出同一句话（典型：ALTER 链上多条规则
// 各自 EXPLAIN 一次、全部失败并吐出同一条 "Table ... doesn't exist"）。
func TestNoDuplicateIdenticalSummaries(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t, "ALTER TABLE t_chain_fix_nonexistent ADD CONSTRAINT ck CHECK (id > 0);", chainParams())

	seen := make(map[string]int)
	for _, s := range data.Summary {
		key := s.Level + "|" + s.RuleKey + "|" + s.Category + "|" + s.Object + "|" + s.Message
		seen[key]++
	}
	for key, n := range seen {
		if n > 1 {
			t.Errorf("同一结论重复出现 %d 次：%s", n, key)
		}
	}
}
