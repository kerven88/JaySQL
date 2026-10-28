package services

import (
	"testing"
)

// 规则类型是「规则类型」列的唯一权威来源，一旦种子里漏写/写错，
// 页面就会退回「待补充」。这里把两条硬约束锁死：
//  1. 规则目录里每一条都必须有合法 rule_type；
//  2. 每条目录规则都能被 ResolveRuleType 解析出同一个值（回填路径可用）。
func TestDefaultStandardRulesHaveRuleType(t *testing.T) {
	rules := defaultStandardRules()
	if len(rules) == 0 {
		t.Fatal("规则目录为空")
	}
	for _, r := range rules {
		if !isAllowedRuleType(r.RuleType) {
			t.Errorf("规则 %s(%s) 的 rule_type=%q 非法或为空", r.RuleKey, r.Name, r.RuleType)
			continue
		}
		if got := ResolveRuleType(r.RuleKey, r.Name); got != r.RuleType {
			t.Errorf("规则 %s 解析结果不一致：目录=%q 解析=%q", r.RuleKey, r.RuleType, got)
		}
	}
}

// 回填只允许动空值/非法值，已合法（运营手工修正过）的值必须原样保留。
func TestNormalizeRuleTypeKeepsExistingValue(t *testing.T) {
	if got := NormalizeRuleType("mysql.struct.limit_char_type_col_len", "限制 CHAR 类型字段的长度", "表"); got != "表" {
		t.Errorf("已合法的值被改写：got=%q want=表", got)
	}
	if got := NormalizeRuleType("mysql.struct.limit_char_type_col_len", "限制 CHAR 类型字段的长度", ""); got != "列" {
		t.Errorf("空值未被回填：got=%q want=列", got)
	}
	if got := NormalizeRuleType("mysql.struct.limit_char_type_col_len", "限制 CHAR 类型字段的长度", "unknown"); got != "列" {
		t.Errorf("非法值未被纠正：got=%q want=列", got)
	}
}

// rule_key 分类器需覆盖「目录里没有的自定义规则」，且不能被 limit_ 前缀带偏。
func TestClassifyRuleTypeByKey(t *testing.T) {
	cases := map[string]string{
		"mysql.struct.limit_char_type_col_len":                 "列",
		"mysql.struct.limit_insert_values_row_count":           "写入",
		"mysql.query_update.limit_update_delete_affected_rows": "更新",
		"postgres.query_update.where_or_condition_check":       "查询",
		"mysql.struct.obj_idx_must_exist":                      "索引",
		"mysql.struct.limit_database_charset":                  "库",
		"mysql.struct.disable_view_related_op":                 "对象",
		"mysql.query_update.delete_stmt_check_table_exists":    "删除",
		"mysql.struct.no_such_rule_convention_at_all":          "",
	}
	for key, want := range cases {
		if got := ClassifyRuleTypeByKey(key); got != want {
			t.Errorf("ClassifyRuleTypeByKey(%q)=%q want %q", key, got, want)
		}
	}
}
