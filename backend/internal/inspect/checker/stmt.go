package checker

import (
	"fmt"
	"regexp"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/kv"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/rules"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"

	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"
)

// 规则链执行约定
//
// 一条语句上的规则**必须全部执行**，命中结果**全部收集**，不允许「命中即中断」。
//
// 历史实现的坑：RuleHint.IsBreak 一旦被某条规则置为 true，checker 就 break 掉整条链，
// 后续规则对当前语句彻底不再执行。由此产生两类漏报（真实事故）：
//
//	1. 前序规则掩盖后续规则：UPDATE 缺 WHERE 会命中「必须包含 WHERE」并置 Break，
//	   排在其后的 must 级规则「限制 UPDATE/DELETE 单个语句总影响行数」永不执行 ——
//	   结果风险最高的「无条件全表 UPDATE」反倒免检，工单还能直接提交；
//	2. breaker 之间互相截断：契约测试只校验了「横切规则排在可能置 Break 的规则之前」，
//	   没有发现 breaker 内部互相吞掉，所以漏网很久。
//
// 因此这里不仅删掉了全部 break，还**从类型上删除了 IsBreak 字段本身**（见 controllers.RuleHint），
// 杜绝后续再引入短路。规则的执行顺序现在只影响告警的排列顺序与少量缓存预热，不再影响正确性。
//
// 唯一的例外是**语句级**的「准入闸门」（statementAdmissionGate）：判定的是「这类语句能不能执行」，
// 而不是「这条语句写得对不对」。两者不在一个层面，见该函数注释。

// 语句准入闸门（statement admission gate）—— 与 runRuleChain 的「规则链」是两件事
//
// 分界线在于「这句话本身能不能执行」：
//   - 规则链回答「语句写得对不对」：语句在平台上是被允许执行的，所以必须逐条检查它的表结构、索引、
//     字符集……这些结论合起来才构成一次完整审核，任何一条都不允许把后面的一起吞掉；
//   - 准入闸门回答「这类语句能不能执行」：命中即语句根本不会被下发，此时再去报「必须显式指定存储引擎」
//     「必须定义主键」毫无意义 —— CTAS/CTL 的表结构来自源表或 SELECT，这些结论既无法满足，
//     也不是用户该处理的问题（用户能做的只有一件事：换一种写法）。
//
// 所以闸门**可以**终止，而且必须终止；它产出的是语句级结论，不是规则级结论。
// 这也正是它与已删除的 RuleHint.IsBreak 的区别：IsBreak 是「规则之间互相截断」，会让前序规则的
// 结论掩盖后续规则的违规（典型：缺 WHERE 吞掉必须级的「影响行数上限」）；闸门不参与规则链。
//
// 闸门覆盖的语句（MySQL）与它们对应的**规范规则**——开关与级别全部由用户在
// 「SQL 开发规范」页面配置，闸门只是执行者，不在引擎里写死策略：
//
//	语句         规范规则（rule_key）                          引擎参数                 引擎默认
//	CTAS         mysql.struct.disable_create_table_as         ENABLE_CREATE_TABLE_AS   false（禁止）
//	CTL          mysql.struct.disable_create_table_like       ENABLE_CREATE_TABLE_LIKE false（禁止）
//	CREATE VIEW  mysql.struct.disable_view_related_op         ENABLE_CREATE_VIEW       true（允许）
//
// 三档行为（由 applyStandardRule 把规则映射到参数，见 inspect/services/standards.go）：
//   - 规则开启（must / suggest）：参数被置为「禁止」⇒ 闸门命中，产出 category=forbidden_statement
//     的语句级结论。最终是「阻断提交」还是「仅提示」由**规则级别**决定：
//     must ⇒ 阻断，suggest ⇒ 提示。判级在 orders/services 按 rule_key 关联到规则级别完成；
//   - 规则关闭（off）：参数被置为「放行」⇒ 闸门不命中，语句照常进入规则链；
//   - 规范中没有这条规则：参数保持引擎默认值。CTAS/CTL 默认禁止、VIEW 默认允许；
//     命中后走「致命类别兜底」强制 must —— 这是存量规范尚未补种时的安全网，
//     确保「补种脚本没跑」不会把「不允许执行」变成静默放行。
func (s *Stmt) statementAdmissionGate(stmt ast.StmtNode) []controllers.SummaryItem {
	switch node := stmt.(type) {
	case *ast.CreateTableStmt:
		return s.gateCreateTable(stmt)
	case *ast.CreateViewStmt:
		return s.gateCreateView(node)
	}
	// MySQL 引擎的准入项到此为止。
	// PostgreSQL 引擎是独立实现，它的准入闸门在 pg_gate.go（CTAS / CTL / 视图同样由规范规则控制，
	// 规则 key 为 postgres.* 版本），两边语义一致、实现各自贴合方言。
	return nil
}

// gateCreateTable 判定 `CREATE TABLE ... AS SELECT ...` / `CREATE TABLE ... LIKE ...`。
//
// 这两类语句的表结构来自源表或 SELECT，字段/索引/约束/字符集等规范无从逐项校验
// （判定结果只会是「继承来的」而非「用户写错的」），因此平台默认整类禁止。
func (s *Stmt) gateCreateTable(stmt ast.StmtNode) []controllers.SummaryItem {
	if !s.InspectParams.ENABLE_CREATE_TABLE_AS {
		v := &traverses.TraverseCreateTableAs{}
		stmt.Accept(v)
		if v.IsCreateAs {
			return []controllers.SummaryItem{controllers.ForbiddenStatement(
				"disable_create_table_as",
				v.Table,
				"① 改为两步执行：先 `CREATE TABLE 表名 (列定义…)`，再 `INSERT INTO 表名 SELECT …`；"+
					"② 若确需一次性建表并灌数据，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止 CREATE TABLE ... AS SELECT」规则",
				fmt.Sprintf("语句不允许执行：`CREATE TABLE ... AS SELECT`（表`%s`），该语法会绕过字段/索引/约束等建表规范", v.Table),
			)}
		}
	}

	if !s.InspectParams.ENABLE_CREATE_TABLE_LIKE {
		v := &traverses.TraverseCreateTableLike{}
		stmt.Accept(v)
		if v.IsCreateLike {
			return []controllers.SummaryItem{controllers.ForbiddenStatement(
				"disable_create_table_like",
				v.Table,
				"① 改为两步执行：先用 `SHOW CREATE TABLE 源表` 取出完整结构，再以显式列定义建表并按需灌数据；"+
					"② 若确需复制表结构，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止 CREATE TABLE ... LIKE」规则",
				fmt.Sprintf("语句不允许执行：`CREATE TABLE ... LIKE`（表`%s`），表结构完全继承自源表，字段/索引/约束规范无法逐项校验", v.Table),
			)}
		}
	}

	return nil
}

// gateCreateView 判定 `CREATE VIEW`（含 `CREATE OR REPLACE VIEW`）。
//
// 视图是「平台不接受这类对象」而不是「这条语句写得不够好」：视图的字段/索引/字符集等
// 同样无从按建表规范逐项校验，所以与 CTAS/CTL 归为同一层语句级准入，
// 命中后不再产出「视图已存在」「依赖表不存在」这类对用户无意义的后续结论。
//
// 开关来自规范规则 `mysql.struct.disable_view_related_op`（引擎参数 ENABLE_CREATE_VIEW，
// 默认 true=允许；规则开启时被置为 false=禁止）——该规则是「规则目录」中的既有条目，
// 存量规范里通常已经存在，因此用户在规范页面可直接改级别或关闭。
func (s *Stmt) gateCreateView(stmt *ast.CreateViewStmt) []controllers.SummaryItem {
	if s.InspectParams.ENABLE_CREATE_VIEW {
		return nil
	}
	view := ""
	if stmt != nil && stmt.ViewName != nil {
		view = stmt.ViewName.Name.O
	}
	return []controllers.SummaryItem{controllers.ForbiddenStatement(
		"disable_view_related_op",
		view,
		"① 改为在应用层封装查询，或使用普通表承载数据；"+
			"② 若确需视图，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止视图相关操作」规则",
		fmt.Sprintf("语句不允许执行：`CREATE VIEW`（视图`%s`），平台规范禁止使用视图对象", view),
	)}
}

// alterAddConstraintRe 命中即提示「禁止 ALTER TABLE ... ADD CONSTRAINT ...」。
//
// 历史实现命中该正则后直接 return，整条 ALTER 规则链（表存在性、列定义、索引、字符集……）
// 一条都不跑；现在只追加这一条提示，后续规则照常执行。
// 提到包级变量：原实现每次调用都 regexp.MustCompile 一次。
var alterAddConstraintRe = regexp.MustCompile(`(?is:.*alter.*table.*add.*constraint.*)`)

type Stmt struct {
	*SyntaxInspectService
}

// ruleChainResult 规则链的聚合结果。
type ruleChainResult struct {
	Summary      []controllers.SummaryItem
	AffectedRows int
	MergeAlter   string
}

// runRuleChain 逐条执行规则链并收集全部命中结果。三条硬约定：
//
//  1. **不中断**：任何一条规则命中（哪怕是「执行必然失败」级的致命结论）都不影响后续规则执行；
//  2. **不静默**：单条规则内部 panic 时记日志后继续跑下一条 —— 一条规则的内部错误
//     不允许让同一条语句上的其他规则一起消失（历史实现下 break/panic 都会连累整条链）；
//  3. **不串味**：每条规则独占一个 RuleHint，规则之间除了 KV 请求级缓存外不共享可变状态。
func (s *Stmt) runRuleChain(ruleList []rules.Rule, stmt ast.StmtNode, kv *kv.KVCache) ruleChainResult {
	var res ruleChainResult
	for i := range ruleList {
		rule := ruleList[i]
		ruleHint := &controllers.RuleHint{
			DB:            s.DB,
			KV:            kv,
			Query:         stmt.Text(),
			InspectParams: &s.InspectParams,
		}
		rule.RuleHint = ruleHint

		s.execRule(rule, &stmt)

		// 收集命中结果，并跳过与已有结论完全相同的重复项。
		// 全链执行后，多条规则可能基于同一个失败前提得出**同一句话**（典型：ALTER 链上 6 条规则
		// 各自 EXPLAIN 一次，全部失败并吐出同一条 "Table ... doesn't exist"）。把这些逐条堆给用户
		// 只是噪音，不构成新信息 —— 去重后仍然是「全部命中结果一并收集」，只是同一结论只留一条。
		for _, item := range ruleHint.Summary {
			if res.hasSummary(item) {
				continue
			}
			res.Summary = append(res.Summary, item)
		}
		// 影响行数只有真正算出结果的规则会写非零值；后续规则的空零值不得把它覆盖掉
		// （原实现靠「更新影响行数那条规则无条件 break」来保住这个值，现在必须显式保住）。
		if ruleHint.AffectedRows > 0 {
			res.AffectedRows = ruleHint.AffectedRows
		}
		// 多条 ALTER 合并提示：保留首个非空值，与历史行为一致。
		if res.MergeAlter == "" && ruleHint.MergeAlter != "" {
			res.MergeAlter = ruleHint.MergeAlter
		}
	}
	return res
}

// hasSummary 判断该结论是否已经收集过（五个可见字段全部一致才算重复）。
func (r *ruleChainResult) hasSummary(item controllers.SummaryItem) bool {
	for _, exist := range r.Summary {
		if exist.Level == item.Level &&
			exist.RuleKey == item.RuleKey &&
			exist.Category == item.Category &&
			exist.Object == item.Object &&
			exist.Message == item.Message {
			return true
		}
	}
	return false
}

// execRule 执行单条规则，并用 recover 兜住规则内部的 panic。
//
// 规则的检查函数会访问目标库（EXPLAIN / SHOW CREATE TABLE / DESC）与请求级缓存，
// 任何一处未预期的空指针都不应该让「同一条语句上的其他规则」跟着一起消失。
// 这里只记录日志、不上抛：规则自身的实现缺陷属于服务端问题，不应把用户 SQL 的审核结果整体作废。
func (s *Stmt) execRule(rule rules.Rule, stmt *ast.StmtNode) {
	defer func() {
		if r := recover(); r != nil {
			if global.App != nil && global.App.Log != nil {
				global.App.Log.Warnf(
					"[inspect] 规则执行异常，已跳过该规则并继续执行后续规则：rule=%s query=%q err=%v",
					rule.Hint, (*stmt).Text(), r,
				)
			}
		}
	}()
	rule.CheckFunc(&rule, stmt)
}

// finishSummary 无任何规则命中/提示时注入一条默认通过信息。
func finishSummary(summary []controllers.SummaryItem) []controllers.SummaryItem {
	if len(summary) == 0 {
		return append(summary, controllers.SummaryItem{Level: LevelInfo, Message: "语句检查通过"})
	}
	return summary
}

// CreateTableStmt 检查 CreateTable 语句
func (s *Stmt) CreateTableStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "CreateTable"}
	// 先过语句准入闸门：CTAS/CTL 属于「整类不允许执行」的语句，直接给出语句级结论，
	// 不再进入规则链 —— 它们的表结构继承自源表/SELECT，后续规则只能产出无法满足的伪结论。
	if rejected := s.statementAdmissionGate(stmt); len(rejected) > 0 {
		data.Summary = rejected
		return data
	}
	data.Summary = finishSummary(s.runRuleChain(rules.CreateTableRules(), stmt, kv).Summary)
	return data
}

// CreateViewStmt 检查 CreateView 语句
func (s *Stmt) CreateViewStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "CreateView"}
	// 与 CTAS/CTL 同一层：平台禁止视图时直接给出语句级结论，不再进入规则链
	// （视图存在性/依赖表存在性这些结论对被拒的语句没有意义）。
	if rejected := s.statementAdmissionGate(stmt); len(rejected) > 0 {
		data.Summary = rejected
		return data
	}
	data.Summary = finishSummary(s.runRuleChain(rules.CreateViewRules(), stmt, kv).Summary)
	return data
}

// CreateDatabaseStmt 检查 CreateDatabase 语句
func (s *Stmt) CreateDatabaseStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	return s.checkDatabaseStmt(stmt, kv, fingerId, "CreateDatabase")
}

// AlterDatabaseStmt 检查 AlterDatabase 语句
func (s *Stmt) AlterDatabaseStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	return s.checkDatabaseStmt(stmt, kv, fingerId, "AlterDatabase")
}

func (s *Stmt) checkDatabaseStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string, stmtType string) ReturnData {
	data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: stmtType}
	data.Summary = finishSummary(s.runRuleChain(rules.CreateDatabaseRules(), stmt, kv).Summary)
	return data
}

// RenameTableStmt 检查 RenameTable 语句
func (s *Stmt) RenameTableStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "RenameTable"}
	data.Summary = finishSummary(s.runRuleChain(rules.RenameTableRules(), stmt, kv).Summary)
	return data
}

// AnalyzeTableStmt 检查 AnalyzeTable 语句
func (s *Stmt) AnalyzeTableStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "AnalyzeTable"}
	data.Summary = finishSummary(s.runRuleChain(rules.AnalyzeTableRules(), stmt, kv).Summary)
	return data
}

// DropTableStmt 检查 DropTable 语句
func (s *Stmt) DropTableStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "DropTable"}
	data.Summary = finishSummary(s.runRuleChain(rules.DropTableRules(), stmt, kv).Summary)
	return data
}

// AlterTableStmt 检查 AlterTable 语句
func (s *Stmt) AlterTableStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) (ReturnData, string) {
	var data ReturnData = ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "AlterTable"}
	// 禁止使用 ALTER TABLE...ADD CONSTRAINT... 语法。
	// 只追加这一条提示，不再提前 return —— 否则整条 ALTER 规则链一条都不执行。
	if alterAddConstraintRe.MatchString(stmt.Text()) {
		data.Summary = append(data.Summary, controllers.SummaryItem{Level: LevelWarn, Message: "禁止使用ALTER TABLE...ADD CONSTRAINT...语法"})
	}
	res := s.runRuleChain(rules.AlterTableRules(), stmt, kv)
	data.Summary = finishSummary(append(data.Summary, res.Summary...))
	return data, res.MergeAlter
}

// DMLStmt 检查 DML 语句
func (s *Stmt) DMLStmt(stmt ast.StmtNode, kv *kv.KVCache, fingerId string) ReturnData {
	// delete/update/insert语句
	/*
		DML语句真的需要对同一个指纹的SQL跳过校验？
		1. DML规则并不多，对实际校验性能影响不大
		2. 每条DML都需要进行Explain，由于考虑传值不一样，因此指纹一样并不能代表Explain的影响行数一样
		3. 实际测试1000条update校验仅需800ms,2000条update校验仅需1500ms
		finger := kv.Get(fingerId)
		var IsSkipAudit bool
		if finger != nil {
			IsSkipAudit = true
		}
	*/
	var data ReturnData = ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "DML"}
	res := s.runRuleChain(rules.DMLRules(), stmt, kv)
	data.Summary = finishSummary(res.Summary)
	// 当为DML语句时，赋值AffectedRows
	data.AffectedRows = res.AffectedRows
	return data
}
