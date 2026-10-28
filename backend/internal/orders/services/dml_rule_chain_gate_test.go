package services

// 「规则链全量执行 → 预审闸门」闭环测试（2026-10-14）
//
// 引擎侧（internal/inspect/checker）已经保证：无 WHERE 的 UPDATE 会**同时**产出两条结论，
// 不再让「必须包含 WHERE」把后面的 must 级规则短路掉。这里锁住下游这一段：
//
//	update_delete_stmt_suggest_where   → 规范里是 suggest（提示放行）
//	limit_update_delete_affected_rows  → 规范里是 must（阻断提交）
//
// 两条都进预审结果，且 must 那条把整体状态抬到 must_fix。
// 反向用例复现修复前的后果：只剩 suggest 那条时状态停在 suggest、用户可提交 ——
// 这正是工单 tk-20ytmktike0m（`update test_01.user set email='100';`）当初能走到执行的原因。

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/checker"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
)

// dmlChainRuleLevels 模拟「默认生产环境规范」里的两条相关规则：
// 影响行数上限 = must（阻断），建议指定 WHERE = suggest（不阻断）
var dmlChainRuleLevels = []inspectServices.StandardRuleLevel{
	{
		RuleKey: "mysql.query_update.limit_update_delete_affected_rows",
		Name:    "限制 UPDATE/DELETE 单个语句总影响行数",
		Level:   "must",
	},
	{
		RuleKey: "mysql.query_update.update_delete_stmt_suggest_where",
		Name:    "UPDATE/DELETE 语句建议指定 WHERE 条件",
		Level:   "suggest",
	},
}

func whereSummary() controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:   "WARN",
		RuleKey: "update_delete_stmt_suggest_where",
		Message: "`UPDATE` 语句必须包含 `WHERE` 条件（避免全表扫描/全表更新）",
	}
}

func affectedRowsSummary() controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:   "WARN",
		RuleKey: "limit_update_delete_affected_rows",
		Message: "`UPDATE` 预计影响/扫描行数 100，超过上限 10；建议拆分语句或缩小 WHERE 范围",
	}
}

// TestUpdateWithoutWhereIsBlockedAfterFullChain 全链执行后：两条结论同时进入预审结果并阻断
func TestUpdateWithoutWhereIsBlockedAfterFullChain(t *testing.T) {
	returnData := []checker.ReturnData{{
		Type:         "DML",
		Query:        "update `test_01`.`user` set email='100';",
		AffectedRows: 100,
		Summary:      []controllers.SummaryItem{whereSummary(), affectedRowsSummary()},
	}}

	result := buildPreInspectResultWithVersion(returnData, dmlChainRuleLevels, "suggest", inspectServices.StandardRuleVersion{})

	if result["status"] != "must_fix" {
		t.Fatalf("状态应为 must_fix（阻断提交），实际 %v；must_count=%v suggest_count=%v",
			result["status"], result["must_count"], result["suggest_count"])
	}
	if result["must_count"] != 1 {
		t.Errorf("must_count 应为 1，实际 %v", result["must_count"])
	}
	if result["suggest_count"] != 1 {
		t.Errorf("suggest_count 应为 1，实际 %v", result["suggest_count"])
	}

	// 两条结论都必须出现在 items 里（不能只留一条）。
	// 注意：这里回写的是规范里的**完整 key**（带 mysql.query_update 前缀），
	// 引擎侧报的是去掉前缀的短 key，匹配由 matchRuleByKeyOrText 的后缀比对完成。
	items, _ := result["items"].([]preInspectItem)
	if len(items) != 2 {
		t.Fatalf("预审明细应为 2 条，实际 %d：%+v", len(items), items)
	}
	levels := map[string]string{}
	for _, it := range items {
		levels[it.RuleKey] = it.Level
	}
	if levels["mysql.query_update.limit_update_delete_affected_rows"] != "must" {
		t.Errorf("影响行数规则应映射为 must，实际 %q", levels["mysql.query_update.limit_update_delete_affected_rows"])
	}
	if levels["mysql.query_update.update_delete_stmt_suggest_where"] != "suggest" {
		t.Errorf("建议 WHERE 规则应映射为 suggest，实际 %q", levels["mysql.query_update.update_delete_stmt_suggest_where"])
	}

	// hit_rules 同样两条都要有
	hits, _ := result["hit_rules"].([]hitRuleItem)
	if len(hits) != 2 {
		t.Errorf("hit_rules 应为 2 条，实际 %d：%+v", len(hits), hits)
	}
}

// TestUpdateWithoutWhereOnlySuggestWouldPass 反向用例：复现修复前的效应
//
// 只在结果里给「建议指定 WHERE」这一条（= 修复前「影响行数」被短路吞掉的情形），
// 状态停在 suggest —— 不阻断提交。这条用例的作用是证明闭环测试的断言方向正确：
// 状态从 suggest 变 must_fix 完全来自「影响行数」那条结论被重新报出。
func TestUpdateWithoutWhereOnlySuggestWouldPass(t *testing.T) {
	returnData := []checker.ReturnData{{
		Type:    "DML",
		Query:   "update `test_01`.`user` set email='100';",
		Summary: []controllers.SummaryItem{whereSummary()},
	}}

	result := buildPreInspectResultWithVersion(returnData, dmlChainRuleLevels, "suggest", inspectServices.StandardRuleVersion{})

	if result["status"] != "suggest" {
		t.Fatalf("只报 suggest 级结论时状态应为 suggest，实际 %v", result["status"])
	}
	if result["must_count"] != 0 {
		t.Errorf("must_count 应为 0，实际 %v", result["must_count"])
	}
}
