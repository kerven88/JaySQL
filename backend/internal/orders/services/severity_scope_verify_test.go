package services

import (
	"testing"

	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
)

// 验证：开发环境下把「表要有主键」单独改为 must 后，只有主键问题判为 must，其余仍为 suggest
func TestSeverityInheritanceDevEnv(t *testing.T) {
	// 模拟默认开发环境规范中已启用的规则（仅列出截图中触发项）
	ruleLevels := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.table_must_have_pk", Name: "表要有主键", Description: "表要有主键", Level: "must"},
		{RuleKey: "mysql.struct.limit_table_storage_engine", Name: "限制表存储引擎", Description: "表引擎只支持在指定引擎中选择", Level: "suggest"},
		{RuleKey: "mysql.struct.limit_tbl_must_comment", Name: "限制表必须注释", Description: "表备注有助于帮助使用者快速了解业务", Level: "suggest"},
		{RuleKey: "mysql.struct.limit_create_table_auto_inc_init", Name: "限制建表自增初始值", Description: "限制建表自增初始值", Level: "suggest"},
		{RuleKey: "mysql.struct.limit_col_must_have_comment", Name: "限制列要有注释", Description: "列要有注释", Level: "suggest"},
	}

	cases := []struct {
		message  string
		expected string
	}{
		{"表`t_0930`必须定义主键", "must"},
		{"表`t_0930`必须显式指定存储引擎，支持的存储引擎为[InnoDB MyISAM]", "suggest"},
		{"表`t_0930`必须要有注释", "suggest"},
		{"表`t_0930`的自增初始值必须显式指定且设置为1", "suggest"},
		{"列`id`必须要有注释（表`t_0930`）", "suggest"},
	}

	for _, c := range cases {
		got := resolveInspectSeverity(ruleLevels, "suggest", "", c.message)
		if got != c.expected {
			t.Errorf("message=%q got=%q want=%q", c.message, got, c.expected)
		}
	}
}

// 验证：只有被单独设为「必须改进」的规则会把对应审核消息判为 must，其余仍为 suggest
func TestMatchRuleSeverityScope(t *testing.T) {
	ruleLevels := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.table_must_have_pk", Name: "表要有主键", Description: "表要有主键", Level: "must"},
		{RuleKey: "mysql.struct.limit_tbl_must_comment", Name: "限制表必须注释", Description: "表备注有助于帮助使用者快速了解业务", Level: "suggest"},
		{RuleKey: "mysql.struct.limit_col_must_have_comment", Name: "限制列要有注释", Description: "列要有注释", Level: "suggest"},
	}

	cases := []struct {
		message  string
		expected string
	}{
		{"表`t_0930`必须定义主键", "must"},
		{"表`t_0930`必须要有注释", "suggest"},
		{"列`id`必须要有注释（表`t_0930`）", "suggest"},
		{"某项完全不相关的提示", ""},
	}

	for _, c := range cases {
		got := matchRuleSeverity(ruleLevels, c.message)
		if got != c.expected {
			t.Errorf("message=%q got=%q want=%q", c.message, got, c.expected)
		}
	}

	// 无法匹配到规则时，使用规范默认级别（suggest），不再一律判 must
	resolved := resolveInspectSeverity(ruleLevels, "suggest", "", "某项完全不相关的提示")
	if resolved != "suggest" {
		t.Errorf("unmatched message should fall back to defaultSeverity, got=%q", resolved)
	}

	// 全部规则都是 must 的规范：未匹配消息也应按默认级别 must
	allMust := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.table_must_have_pk", Name: "表要有主键", Description: "表要有主键", Level: "must"},
	}
	if got := resolveInspectSeverity(allMust, "must", "", "不相关提示"); got != "must" {
		t.Errorf("all-must standard should resolve to must, got=%q", got)
	}

	// 无规范绑定（ruleLevels 为空、默认级别为空）：规范中不存在该参数，
	// 不再按消息关键词臆断严重级别，统一按 suggest（建议改进）处理
	if got := resolveInspectSeverity(nil, "", "", "表必须要有主键"); got != "suggest" {
		t.Errorf("no standard binding should not judge by keyword, got=%q", got)
	}
	if got := resolveInspectSeverity(nil, "", "", "建议为表添加注释"); got != "suggest" {
		t.Errorf("no standard binding should resolve to suggest, got=%q", got)
	}

	// 引擎显式携带 rule_key 时，直接命中对应规则级别（不受文案影响）
	if got := resolveInspectSeverity(ruleLevels, "suggest", "mysql.struct.table_must_have_pk", "任意文案"); got != "must" {
		t.Errorf("explicit rule_key should win, got=%q", got)
	}
	// 引擎携带后缀 rule_key 时，同样能跨数据库命中
	if got := resolveInspectSeverity(ruleLevels, "suggest", "table_must_have_pk", "任意文案"); got != "must" {
		t.Errorf("explicit rule_key suffix should win, got=%q", got)
	}
}

// 验证：即使规则在规则目录中 implemented=false，只要规范中启用了该规则，
// 其级别变更也必须立刻在工单预审中生效（这是问题根因的回归用例）。
func TestSeverityReflectsCatalogRulesRegardlessImplemented(t *testing.T) {
	// 模拟把「限制表存储引擎」「限制表必须注释」两条 implemented=false 的规则改为 must
	ruleLevels := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.limit_table_storage_engine", Name: "限制表存储引擎", Description: "表引擎只支持在指定引擎中选择", Level: "must"},
		{RuleKey: "mysql.struct.limit_tbl_must_comment", Name: "限制表必须注释", Description: "表备注有助于帮助使用者快速了解业务", Level: "must"},
		{RuleKey: "mysql.struct.limit_col_must_have_comment", Name: "限制列要有注释", Description: "列要有注释", Level: "suggest"},
	}

	cases := []struct {
		message  string
		ruleKey  string
		expected string
	}{
		{"表`t_0930`必须显式指定存储引擎，支持的存储引擎为[InnoDB MyISAM]", "limit_table_storage_engine", "must"},
		{"表`t_0930`必须要有注释", "limit_tbl_must_comment", "must"},
		{"列`id`必须要有注释（表`t_0930`）", "limit_col_must_have_comment", "suggest"},
	}

	for _, c := range cases {
		// 使用默认级别 must，验证非实现规则仍能按自身级别覆盖默认值。
		// 引擎会携带 rule_key，所以这里同步传入 rule_key，模拟真实预审链路。
		got := resolveInspectSeverity(ruleLevels, "must", c.ruleKey, c.message)
		if got != c.expected {
			t.Errorf("message=%q ruleKey=%q got=%q want=%q", c.message, c.ruleKey, got, c.expected)
		}
	}
}
