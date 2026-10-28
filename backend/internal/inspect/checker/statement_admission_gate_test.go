package checker

// 语句准入闸门回归测试（2026-10-14）
//
// 锁定两件**互相牵制**的事，缺一不可：
//
//	1. CTAS / CTL 属于「整类不允许执行」的语句，命中即只给一条语句级结论，
//	   后续基于语句结构的检查（存储引擎 / 主键 / 注释…）一律不再产出；
//	2. 这种终止**只发生在闸门**，不能渗进规则链 —— 语句一旦被允许执行，
//	   链上规则仍必须全量执行（见 rule_chain_full_run_test.go）。
//
// 为什么必须分出这两层：CTAS/CTL 的表结构继承自源表或 SELECT，
// 「必须显式指定存储引擎」「必须定义主键」这类结论既无法满足、也不是用户能处理的问题
// （用户能做的只有换一种写法），继续报只会淹没真正的结论。
// 这与已删除的 IsBreak 有本质区别：IsBreak 是规则之间互相截断，会让前序规则的结论
// 掩盖后续规则的违规（典型：缺 WHERE 吞掉影响行数上限），那个必须永远消失。
//
// 依赖本机 MySQL（127.0.0.1:3306，见 struct_matrix_test.go 的常量）。

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// gateParams 闸门用例基线：「禁止执行」档 —— 三处参数都处于「禁止」状态。
//
// CTAS/CTL 是能力类参数，引擎默认 false（禁止，见 inspect/config/defaults.go）；
// CREATE VIEW 默认 true（允许），显式置 false 对应「规范里『禁止视图相关操作』规则已开启」。
// 用户改的其实是规范里那三条规则的级别/开关，参数只是规则的落地形式。
func gateParams() config.InspectParams {
	p := chainParams()
	p.ENABLE_CREATE_TABLE_AS = false
	p.ENABLE_CREATE_TABLE_LIKE = false
	p.ENABLE_CREATE_VIEW = false
	return p
}

// assertSingleRejection 断言结果里只有一条结论，且是「语句不允许执行」
func assertSingleRejection(t *testing.T, data ReturnData, wantKey, wantObject string) {
	t.Helper()
	if len(data.Summary) != 1 {
		t.Fatalf("闸门命中时应只输出 1 条语句级结论，实际 %d 条：%s", len(data.Summary), messagesOf(data.Summary))
	}
	item := data.Summary[0]
	if item.Category != controllers.CategoryForbiddenStatement {
		t.Errorf("Category = %q，应为 %q", item.Category, controllers.CategoryForbiddenStatement)
	}
	if item.RuleKey != wantKey {
		t.Errorf("RuleKey = %q，应为 %q", item.RuleKey, wantKey)
	}
	if item.Object != wantObject {
		t.Errorf("Object = %q，应为 %q", item.Object, wantObject)
	}
	if strings.TrimSpace(item.Advice) == "" {
		t.Error("缺少修复建议：用户只知道被禁止却不知道该怎么改")
	}
	// 结构继承类结论必须彻底消失，否则等于闸门没生效
	for _, forbidden := range []string{"存储引擎", "主键", "COMMENT", "注释"} {
		if strings.Contains(item.Message, forbidden) {
			t.Errorf("语句级结论里不应出现结构类结论（%s）：%s", forbidden, item.Message)
		}
	}
}

// TestCTASRejectedByGate CTAS 默认被闸门拦下：只给一条结论，且不再报引擎/主键
//
// 修复前：CTAS 提示与「必须显式指定存储引擎」「必须定义主键」同时报出 ——
// 后两条对 `CREATE TABLE ... AS SELECT` 是无法满足的伪结论。
func TestCTASRejectedByGate(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t,
		"CREATE TABLE probe_gate_ctas AS SELECT id FROM "+chainFixtureTable+";",
		gateParams())
	assertSingleRejection(t, data, "disable_create_table_as", "probe_gate_ctas")
}

// TestCTLRejectedByGate CTL 默认被闸门拦下：只给一条结论
//
// CTL 尤其明显：表结构完全继承源表，MySQL 语法上甚至不允许在 LIKE 语句里指定 ENGINE，
// 因此「必须显式指定存储引擎」永远是伪结论。
func TestCTLRejectedByGate(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t,
		"CREATE TABLE probe_gate_ctl LIKE "+chainFixtureTable+";",
		gateParams())
	assertSingleRejection(t, data, "disable_create_table_like", "probe_gate_ctl")
}

// TestCreateViewRejectedByGate CREATE VIEW 与 CTAS/CTL 同属语句级准入
//
// 视图是「平台不接受这类对象」，不是「这条语句写得不够好」：命中后不再产出
// 「视图已存在」「依赖表不存在」这些对被拒语句没有意义的后续结论。
func TestCreateViewRejectedByGate(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t,
		"CREATE VIEW probe_gate_view AS SELECT id FROM "+chainFixtureTable+";",
		gateParams())
	assertSingleRejection(t, data, "disable_view_related_op", "probe_gate_view")
}

// TestCreateViewAllowedNotGated 规则关闭后视图放行，闸门不再命中
//
// 反向对照：证明闸门的三档行为确实由规范规则驱动（关闭即放行），而不是在引擎里写死禁止。
func TestCreateViewAllowedNotGated(t *testing.T) {
	ensureChainFixture(t)
	params := gateParams()
	params.ENABLE_CREATE_VIEW = true

	data := assertChain(t,
		"CREATE VIEW probe_gate_view_ok AS SELECT id FROM "+chainFixtureTable+";",
		params)

	for _, s := range data.Summary {
		if s.Category == controllers.CategoryForbiddenStatement {
			t.Fatalf("规则已关闭，视图不应触发语句准入闸门：%s", messagesOf(data.Summary))
		}
	}
}

// TestCTASAllowedRunsWholeChain 放开能力开关后，CTAS 走完整规则链
//
// 关键对照：同一个语句，唯一变量是开关 —— 闸门不再命中，链上的结构类规则照常给结论。
// 这也证明闸门没有把规则链本身改坏。
func TestCTASAllowedRunsWholeChain(t *testing.T) {
	ensureChainFixture(t)
	params := gateParams()
	params.ENABLE_CREATE_TABLE_AS = true

	data := assertChain(t,
		"CREATE TABLE probe_gate_ctas_ok ENGINE=MyISAM AS SELECT id FROM "+chainFixtureTable+";",
		params)

	if keysOf(data.Summary)["disable_create_table_as"] {
		t.Errorf("开关已放开，不应再报「语句不允许执行」：%s", messagesOf(data.Summary))
	}
	if !keysOf(data.Summary)["limit_table_storage_engine"] {
		t.Fatalf("放开后应继续检查存储引擎（显式指定的 MyISAM 不在白名单）：%s", messagesOf(data.Summary))
	}
}

// TestCTLAllowedRunsWholeChain 放开 CTL 开关后同样走完整规则链
//
// 顺带把一条**已知且被接受的**残留记录在案（而不是假装它不存在）：
// 放开后规则链会照常对 LIKE 语句报「必须显式指定存储引擎」「必须定义主键」，
// 而这两条对 `CREATE TABLE ... LIKE ...` 是伪结论 —— 表结构完全继承源表，
// MySQL 语法上也不允许在 LIKE 语句里指定 ENGINE。
//
// 之所以先不处理：CTAS/CTL 的能力开关（ENABLE_CREATE_TABLE_AS / _LIKE）在
// inspect/config/defaults.go 里恒为 false，且 standards.go 的 applyStandardRule
// 没有任何 case 能打开它们 —— 也就是说「放开」这条路径在生产中不可达，
// 默认路径一律由闸门拦下（见上面两条用例）。真要支持放开，正确做法是让各结构检查
// 自行区分「显式指定」与「继承而来」，那属于另一轮设计。
func TestCTLAllowedRunsWholeChain(t *testing.T) {
	ensureChainFixture(t)
	params := gateParams()
	params.ENABLE_CREATE_TABLE_LIKE = true

	data := assertChain(t,
		"CREATE TABLE probe_gate_ctl_ok LIKE "+chainFixtureTable+";",
		params)

	if keysOf(data.Summary)["disable_create_table_like"] {
		t.Errorf("开关已放开，不应再报「语句不允许执行」：%s", messagesOf(data.Summary))
	}
	if len(data.Summary) == 0 {
		t.Fatal("放开后应进入规则链并给出结论")
	}
}

// TestNormalCreateTableNotGated 普通建表不受闸门影响
func TestNormalCreateTableNotGated(t *testing.T) {
	ensureChainFixture(t)
	data := assertChain(t,
		"CREATE TABLE probe_gate_normal (id int) ENGINE=MyISAM;",
		gateParams())

	for _, s := range data.Summary {
		if s.Category == controllers.CategoryForbiddenStatement {
			t.Fatalf("普通建表不应触发语句准入闸门：%s", messagesOf(data.Summary))
		}
	}
	if !keysOf(data.Summary)["limit_table_storage_engine"] {
		t.Errorf("普通建表应照常检查存储引擎：%s", messagesOf(data.Summary))
	}
}

// TestForbiddenStatementCategoryIsFatal 类别本身必须被认定为「执行必然失败」
//
// 这是「不允许执行」能真正阻断的支点：规范里没有对应规则条目时，
// 级别判定会走到致命类别兜底并强制 must（见 orders/services 的级别闭环测试）。
func TestForbiddenStatementCategoryIsFatal(t *testing.T) {
	if !controllers.IsFatalCategory(controllers.CategoryForbiddenStatement) {
		t.Fatalf("类别 %q 必须属于致命类别，否则「不允许执行」会被降级为「建议改进」放行",
			controllers.CategoryForbiddenStatement)
	}
}
