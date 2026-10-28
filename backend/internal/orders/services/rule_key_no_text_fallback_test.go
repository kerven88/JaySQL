package services

import (
	"testing"

	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
)

// 回归（2026-10-22）：引擎显式携带 rule_key 但规范里没有对应条目时，
// 不得退化到文案相似度匹配 —— 否则「列`content`不允许变更数据类型」会被
// 字符重叠度误挂到「限制主键列的数据类型」上，展示出完全无关的规则名。
func TestExplicitRuleKeyWithoutEntrySkipsTextMatch(t *testing.T) {
	// 规范里只有「限制主键列的数据类型」，与列类型变更消息文案高度相似
	ruleLevels := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.limit_pk_col_type", Name: "限制主键列的数据类型", Level: "must"},
	}

	msg := "列`content`不允许变更数据类型（表`insight_order_records`）"

	level, rule := resolveInspectRule(ruleLevels, "suggest", "column_type_change", msg, "rule_violation", nil)
	if rule.RuleKey != "" {
		t.Fatalf("规范无 column_type_change 条目时不应误挂到其他规则，实际挂到: %s (%s)", rule.RuleKey, rule.Name)
	}
	if level != "suggest" {
		t.Fatalf("未命中具体规则时应回退规范默认级别 suggest，实际: %q", level)
	}

	// 规范默认级别为 must 时同样不误挂
	level, rule = resolveInspectRule(ruleLevels, "must", "column_type_change", msg, "rule_violation", nil)
	if rule.RuleKey != "" {
		t.Fatalf("规范无条目时不应误挂到其他规则，实际挂到: %s (%s)", rule.RuleKey, rule.Name)
	}
	if level != "must" {
		t.Fatalf("规范默认级别 must 应回传 must，实际: %q", level)
	}
}

// 对照组：显式 key 在规范中有条目（全名或后缀匹配）时仍精确命中，行为不变。
func TestExplicitRuleKeyWithEntryStillMatches(t *testing.T) {
	ruleLevels := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.table_must_have_pk", Name: "表要有主键", Level: "must"},
	}

	level, rule := resolveInspectRule(ruleLevels, "suggest", "table_must_have_pk", "任意文案", "rule_violation", nil)
	if rule.RuleKey != "mysql.struct.table_must_have_pk" || level != "must" {
		t.Fatalf("后缀匹配应精确命中，实际: level=%q rule=%+v", level, rule)
	}
}
