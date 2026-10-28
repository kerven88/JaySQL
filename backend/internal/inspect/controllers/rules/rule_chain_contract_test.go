package rules

import (
	"reflect"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/controllers"

	"github.com/pingcap/tidb/pkg/parser/ast"
)

// 规则链执行契约（2026-10-14 变更）
//
// 新约定：一条语句上的规则**必须全部执行**、命中结果**全部收集**，任何规则都不得短路后续规则。
//
// 历史 bug：RuleHint.IsBreak 被任一条规则置 true 时，checker 就 break 掉整条链。
// 直接后果是「UPDATE 缺 WHERE」短路掉排在其后的 must 级「影响行数上限」规则，
// 风险最高的无条件全表 UPDATE 反而免检、还能直接提交（工单 tk-20ytmktike0m）。
// 旧的顺序契约测试只校验了「横切规则排在可能 break 的规则之前」，
// 没发现 breaker 之间也会互相截断，所以长期漏网。
//
// 因此这里改为**从类型上**锁死：RuleHint 不允许再出现「跳过后续步骤」这类字段，
// 想重新引入短路必须先改这条测试 —— 让 regress 在编译期之外也过不去。
//
// 唯一的例外是**语句级**准入（闸门），它不在这条链上：
// 「这类语句能不能执行」由 checker.statementAdmissionGate 判定，
// 「这条语句写得对不对」才归规则链且必须全跑。两者别混为一谈（见下面的边界用例）。

// ruleFuncPtr 取检查函数的地址，用于在规则链中定位某条规则的位置。
func ruleFuncPtr(fn func(*Rule, *ast.StmtNode)) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

// TestRuleHintHasNoShortCircuitField RuleHint 不得再含短路开关
func TestRuleHintHasNoShortCircuitField(t *testing.T) {
	typ := reflect.TypeOf(controllers.RuleHint{})

	forbidden := []string{"isbreak", "skip_next_step", "break"}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := strings.ToLower(f.Name)
		tag := strings.ToLower(f.Tag.Get("json"))
		for _, bad := range forbidden {
			if name == bad {
				t.Fatalf("RuleHint 仍存在短路字段 %s：规则链必须全量执行，不允许任何规则跳过后续规则", f.Name)
			}
			// json tag 形如 "skip_next_step,omitempty"
			if seg := strings.Split(tag, ",")[0]; seg == bad {
				t.Fatalf("RuleHint 字段 %s 的 json tag 仍为 %q：短路开关已废弃，必须移除", f.Name, tag)
			}
		}
	}
}

// TestEveryRuleHasHintAndCheckFunc 规则链完整性：每条规则都要有说明与检查函数
//
// Hint 会出现在「规则执行异常、已跳过该规则」的排查日志里，缺失会让线上定位无从下手。
func TestEveryRuleHasHintAndCheckFunc(t *testing.T) {
	chains := map[string][]Rule{
		"CreateTable":    CreateTableRules(),
		"CreateView":     CreateViewRules(),
		"CreateDatabase": CreateDatabaseRules(),
		"AlterTable":     AlterTableRules(),
		"DropTable":      DropTableRules(),
		"RenameTable":    RenameTableRules(),
		"AnalyzeTable":   AnalyzeTableRules(),
		"DML":            DMLRules(),
	}

	for name, list := range chains {
		if len(list) == 0 {
			t.Errorf("%s 规则链为空", name)
			continue
		}
		seen := make(map[string]struct{}, len(list))
		for i, r := range list {
			if strings.TrimSpace(r.Hint) == "" {
				t.Errorf("%s 第 %d 条规则缺少 Hint", name, i+1)
			}
			if r.CheckFunc == nil {
				t.Errorf("%s 第 %d 条规则（%s）CheckFunc 为空", name, i+1, r.Hint)
			}
			// 同一条链上不允许重复注册同一个检查函数，否则会重复出告警
			if _, dup := seen[r.Hint]; dup {
				t.Errorf("%s 出现重复规则：%s", name, r.Hint)
			}
			seen[r.Hint] = struct{}{}
		}
	}
}

// TestComputeChainDoesNotOwnStatementAdmissionRules 语句准入项不得回流到 CreateTable 规则链
//
// `CREATE TABLE ... AS SELECT ...` / `LIKE ...` 的禁止是「整类语句不允许执行」，
// 由 checker 的语句准入闸门判定并终止该语句的规则链。若把它们放回链上，
// 会出现「规则命中却不终止 + 闸门又拦一次」的双份结论，或者反过来闸门拦下后
// 这些规则变成永不执行的死代码。这条用例把边界钉死在链的定义上。
func TestComputeChainDoesNotOwnStatementAdmissionRules(t *testing.T) {
	for _, r := range CreateTableRules() {
		if strings.Contains(r.Hint, "CreateTableAs") || strings.Contains(r.Hint, "CreateTableLike") {
			t.Fatalf("CTAS/CTL 应通过语句准入闸门判定，不得留在规则链上：%s", r.Hint)
		}
	}
}

// TestDMLRulesOrderKeepsExplainWarm 「必须使用索引」仍排在「更新影响行数」之前
//
// 顺序不再影响正确性，但这条安排有性能意义：两者都要 EXPLAIN，
// 前者先跑会把结果按 SQL 缓存进 KV，后者直接复用，避免对目标库重复查询。
func TestDMLRulesOrderKeepsExplainWarm(t *testing.T) {
	list := DMLRules()
	pos := make(map[uintptr]int, len(list))
	for i, r := range list {
		pos[ruleFuncPtr(r.CheckFunc)] = i
	}

	mustUseIdx, ok1 := pos[ruleFuncPtr((*Rule).RuleDMLMustUseIndex)]
	if !ok1 {
		t.Fatal("DML#UPDATE/DELETE必须使用索引 未注册")
	}
	maxRows, ok2 := pos[ruleFuncPtr((*Rule).RuleDMLMaxUpdateRows)]
	if !ok2 {
		t.Fatal("DML#更新影响行数 未注册")
	}
	if mustUseIdx > maxRows {
		t.Errorf("「必须使用索引」(%d) 应排在「更新影响行数」(%d) 之前，以复用同一次 EXPLAIN",
			mustUseIdx, maxRows)
	}
}
