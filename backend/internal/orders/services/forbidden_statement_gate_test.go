package services

// 语句准入结论的阻断闭环测试（2026-10-14）
//
// 闸门（checker.statementAdmissionGate）判出「语句不允许执行」只是第一步 ——
// 它必须真的拦住提交，否则「不允许执行」就只是页面上的一句提示。
//
// 这里锁定的链路：forbidden_statement 类别 → 规范里没有对应规则条目（存量规范不回流种子）
// → 级别判定第 3 步「致命类别兜底」→ 强制 must → status = must_fix → 提交审批被阻断。
//
// 同时给一个反向对照：同一条消息只要不是致命类别，就会退回规范默认级别（suggest），
// 证明「阻断」确实来自类别而不是别的副作用。

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/checker"
	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
)

const forbiddenStatementMessage = "语句不允许执行：`CREATE TABLE ... AS SELECT`（表`t1`），该语法会绕过字段/索引/约束等建表规范"

func forbiddenStatementReturnData(category string) []checker.ReturnData {
	return []checker.ReturnData{{
		FingerId: "fp-ctas",
		Query:    "CREATE TABLE t1 AS SELECT * FROM t0",
		Type:     "CreateTable",
		Summary: []controllers.SummaryItem{{
			Level:    "WARN",
			RuleKey:  "disable_create_table_as",
			Category: category,
			Object:   "t1",
			Advice:   "① 改为两步执行：先建表再 INSERT ... SELECT",
			Message:  forbiddenStatementMessage,
		}},
	}}
}

// TestForbiddenStatementBlocksSubmit 规范里没有对应规则条目时也必须阻断提交
func TestForbiddenStatementBlocksSubmit(t *testing.T) {
	// 规则列表刻意留空、规范默认级别刻意设为 suggest —— 模拟存量规范（种子不回流）的真实处境
	result := buildPreInspectResultWithVersion(
		forbiddenStatementReturnData(controllers.CategoryForbiddenStatement),
		nil, "suggest", inspectServices.StandardRuleVersion{})

	if result["status"] != "must_fix" {
		t.Fatalf("status = %v，want must_fix（「语句不允许执行」必须阻断提交，否则等于没拦）", result["status"])
	}
	if result["must_count"] != 1 {
		t.Errorf("must_count = %v，want 1", result["must_count"])
	}

	items, ok := result["items"].([]preInspectItem)
	if !ok || len(items) != 1 {
		t.Fatalf("items 应有 1 条，实际 %v", result["items"])
	}
	item := items[0]
	if item.Category != controllers.CategoryForbiddenStatement {
		t.Errorf("Category = %q，want %q", item.Category, controllers.CategoryForbiddenStatement)
	}
	if item.Level != "must" {
		t.Errorf("Level = %q，want must", item.Level)
	}
	// 四要素：对象 / 原因 / 建议都要能给前端渲染
	if item.Object != "t1" {
		t.Errorf("Object = %q，want t1", item.Object)
	}
	if !strings.Contains(item.Reason, "不允许执行") {
		t.Errorf("Reason 应说明后果，实际 %q", item.Reason)
	}
	if strings.TrimSpace(item.Advice) == "" {
		t.Error("Advice 为空：用户只知道被拦，不知道该怎么改")
	}
}

// TestNonForbiddenStatementKeepsDefaultSeverity 反向对照：类别不是致命类就退回规范默认级别
//
// 证明「阻断」来自 forbidden_statement 这个致命类别，而不是消息文案碰巧触发了别的规则。
func TestNonForbiddenStatementKeepsDefaultSeverity(t *testing.T) {
	result := buildPreInspectResultWithVersion(
		forbiddenStatementReturnData(controllers.CategoryRuleViolation),
		nil, "suggest", inspectServices.StandardRuleVersion{})

	if result["status"] != "suggest" {
		t.Fatalf("status = %v，want suggest（非致命类别不得被升级为阻断）", result["status"])
	}
}

// TestForbiddenStatementParamsDefaultOff CTAS/CTL 能力开关默认必须关闭
//
// 闸门是「开关关闭 ⇒ 判定不允许执行」。若默认值被改成 open，闸门就形同虚设 ——
// 这条断言把默认口径钉在参数类型上。
func TestForbiddenStatementParamsDefaultOff(t *testing.T) {
	var p config.InspectParams
	if p.ENABLE_CREATE_TABLE_AS || p.ENABLE_CREATE_TABLE_LIKE {
		t.Fatalf("CTAS/CTL 能力开关默认应为关闭：AS=%v LIKE=%v", p.ENABLE_CREATE_TABLE_AS, p.ENABLE_CREATE_TABLE_LIKE)
	}
}

// TestForbiddenStatementRuleLevelDrivesSeverity 规范里存在对应规则时，阻断与否由**规则级别**决定
//
// 这是「语句类禁止也允许用户设等级」的落点：同一条 forbidden_statement 结论 ——
//   - 规则设为 must    ⇒ 阻断提交（见 TestForbiddenStatementBlocksSubmit 的兜底路径）
//   - 规则设为 suggest ⇒ 仅提示，不阻断
//
// 默认级别刻意设为 must，以证明结果来自规则本身，而不是规范默认级别兜底。
func TestForbiddenStatementRuleLevelDrivesSeverity(t *testing.T) {
	levels := []inspectServices.StandardRuleLevel{{
		RuleKey: "mysql.struct.disable_create_table_as",
		Name:    "禁止 CREATE TABLE ... AS SELECT",
		Level:   "suggest",
	}}
	result := buildPreInspectResultWithVersion(
		forbiddenStatementReturnData(controllers.CategoryForbiddenStatement),
		levels, "must", inspectServices.StandardRuleVersion{})

	if result["status"] != "suggest" {
		t.Fatalf("status = %v，want suggest（规则设为建议改进时不应阻断提交）", result["status"])
	}
	items, ok := result["items"].([]preInspectItem)
	if !ok || len(items) != 1 {
		t.Fatalf("items 应有 1 条，实际 %v", result["items"])
	}
	if items[0].Level != "suggest" {
		t.Errorf("Level = %q，want suggest", items[0].Level)
	}
	// 命中规则要能回填到规范条目上，前端才能显示「命中了哪条规则」
	if items[0].RuleKey != "mysql.struct.disable_create_table_as" {
		t.Errorf("RuleKey = %q，want mysql.struct.disable_create_table_as（引擎输出的是后缀 key，此处应关联回完整 key）",
			items[0].RuleKey)
	}
}

// TestForbiddenStatementRuleDisabledDropsItem 规则关闭（off）后该结论不再报出
//
// 用户把规则设为「关闭」即表示接受这类语句，闸门在引擎层就不会命中；
// 这里是判级层的第二道保险：即使结论被产出，也不得计入阻断。
func TestForbiddenStatementRuleDisabledDropsItem(t *testing.T) {
	result := buildPreInspectResultFull(
		forbiddenStatementReturnData(controllers.CategoryForbiddenStatement),
		nil, "must", inspectServices.StandardRuleVersion{},
		map[string]bool{"disable_create_table_as": true})

	if result["status"] == "must_fix" {
		t.Fatalf("规则已被关闭，不应阻断提交（status=%v）", result["status"])
	}
	items, _ := result["items"].([]preInspectItem)
	for _, it := range items {
		if it.Category == controllers.CategoryForbiddenStatement {
			t.Fatalf("规则已关闭，不应输出语句准入结论：%+v", it)
		}
	}
}
