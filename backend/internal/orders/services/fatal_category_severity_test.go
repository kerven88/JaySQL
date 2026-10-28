package services

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/checker"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
)

// 构造一条语法错误审核结果：模拟引擎在 SQL 无法解析时的输出
func buildSyntaxErrorReturnDataForTest() []checker.ReturnData {
	syntaxErr := &checker.SyntaxError{
		Message: `line 1 column 15 near "talbe" `,
		Line:    1,
		Column:  15,
		Near:    "talbe",
	}
	return syntaxErr.ToReturnData("create talbe t1 (id int)")
}

/* 致命类别（语法错误 / 对象已存在 / 对象不存在）的级别判定规则：
   这类错误在执行阶段必然失败，不能因为「引擎没给它 rule_key」或
   「规范里没有对应规则」就被降级成「建议改进」放行。 */

var fatalRuleLevels = []inspectServices.StandardRuleLevel{
	{RuleKey: "mysql.struct.table_must_have_pk", Name: "表必须要有主键", Level: "suggest"},
	{RuleKey: "mysql.struct.limit_tbl_must_comment", Name: "限制表必须注释", Level: "suggest"},
}

func TestFatalCategoryForcesMustWhenNoRuleMatched(t *testing.T) {
	cases := []struct {
		name     string
		category string
		message  string
	}{
		{"对象已存在", "object_conflict", "表或视图`t_1011`已存在"},
		{"对象不存在", "object_missing", "表或视图`t_order`不存在"},
		{"语法错误", "syntax_error", "第 1 行第 15 列 附近存在语法错误"},
	}
	for _, c := range cases {
		// 规范默认级别为 suggest，且消息无法匹配到任何规则 —— 历史实现会判成 suggest 并放行
		got := resolveInspectSeverityWithCategory(fatalRuleLevels, "suggest", "", c.message, c.category, nil)
		if got != "must" {
			t.Errorf("%s: 级别 = %q, want must（执行必然失败的错误不得降级放行）", c.name, got)
		}
	}
}

// 非致命类别仍按原逻辑走规范默认级别，避免把普通规范建议也升级成必须改进
func TestNonFatalCategoryKeepsDefaultSeverity(t *testing.T) {
	got := resolveInspectSeverityWithCategory(fatalRuleLevels, "suggest", "", "表`t1`建议添加注释", "rule_violation", nil)
	if got != "suggest" {
		t.Errorf("级别 = %q, want suggest（普通规范问题不应被升级）", got)
	}
}

// 规则被规范显式关闭时不再报出 —— 这是「可配置」的另一半能力（能关掉）
func TestDisabledRuleIsNotReported(t *testing.T) {
	disabled := map[string]bool{"create_tbl_must_not_exist": true}
	got := resolveInspectSeverityWithCategory(
		fatalRuleLevels, "suggest", "create_tbl_must_not_exist", "表或视图`t_1011`已存在",
		"object_conflict", disabled,
	)
	if got != "" {
		t.Errorf("级别 = %q, want 空（规则已被关闭，不应报出）", got)
	}
}

// 规则存在且启用时，尊重规则自身级别
func TestEnabledRuleLevelIsRespected(t *testing.T) {
	rules := append([]inspectServices.StandardRuleLevel{}, fatalRuleLevels...)
	rules = append(rules, inspectServices.StandardRuleLevel{
		RuleKey: "mysql.struct.create_tbl_must_not_exist",
		Name:    "限制建表目标表不能已存在",
		Level:   "must",
	})
	got := resolveInspectSeverityWithCategory(
		rules, "suggest", "create_tbl_must_not_exist", "表或视图`t_1011`已存在",
		"object_conflict", nil,
	)
	if got != "must" {
		t.Errorf("级别 = %q, want must", got)
	}
}

// 预审结果应把致命错误标记出来，供前端区分文案与呈现
func TestBuildPreInspectResultMarksFatalItems(t *testing.T) {
	returnData := buildSyntaxErrorReturnDataForTest()
	result := buildPreInspectResultWithVersion(returnData, fatalRuleLevels, "suggest", inspectServices.StandardRuleVersion{})
	if result["has_syntax_error"] != true {
		t.Error("has_syntax_error 应为 true")
	}
	if result["status"] != "must_fix" {
		t.Errorf("status = %v, want must_fix（语法错误必须阻断）", result["status"])
	}
	items, ok := result["items"].([]preInspectItem)
	if !ok || len(items) != 1 {
		t.Fatalf("items 应有 1 条，实际 %v", result["items"])
	}
	item := items[0]
	if item.Category != "syntax_error" {
		t.Errorf("Category = %q, want syntax_error", item.Category)
	}
	// 四要素必须齐全：原因 + 建议
	if item.Reason == "" || item.Advice == "" || item.Position == "" {
		t.Errorf("结构化字段缺失：reason=%q advice=%q position=%q", item.Reason, item.Advice, item.Position)
	}
}
