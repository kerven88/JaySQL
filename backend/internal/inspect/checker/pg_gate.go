package checker

// PG 引擎的「语句准入闸门」
//
// 与 MySQL 引擎（stmt.go: statementAdmissionGate）同一层语义：判定「这类语句能不能执行」，
// 命中即给出**语句级**结论，不再进入规则链 —— CTAS / CTL / 视图的表结构继承自别处，
// 字段/索引/约束/字符集规范既无法满足，也不是用户该处理的问题。
//
// 覆盖的语句与对应规范规则（开关与级别全部来自实例绑定的规范，闸门只做执行者）：
//
//	语句                        规范规则（rule_key）                       引擎参数                 引擎默认
//	CTAS                        postgres.struct.disable_create_table_as     ENABLE_CREATE_TABLE_AS   false（禁止）
//	CTL                         postgres.struct.disable_create_table_like   ENABLE_CREATE_TABLE_LIKE false（禁止）
//	CREATE/ALTER/DROP VIEW      postgres.struct.disable_view_related_op     ENABLE_CREATE_VIEW       true（允许）
//
// 与 MySQL 的语法差异：
//   - CTAS：PG 写作 `CREATE TABLE t AS SELECT ...`（MySQL 是 `CREATE TABLE t AS SELECT`，写法相同，
//     但 PG 还允许 `CREATE TABLE t (a, b) AS SELECT ...` 的列名清单形式）；
//   - CTL：PG 没有 `CREATE TABLE t LIKE src`，等价能力是括号内的 `LIKE` 子句
//     `CREATE TABLE t (LIKE src INCLUDING ALL)`，对外语义一致，归到同一条规则；
//   - 视图：PG 还有 MATERIALIZED VIEW，一并纳入「禁止视图相关操作」。

import (
	"fmt"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// pgForbidden 构造「整类语句被禁止执行」的语句级结论（与 MySQL 的 controllers.ForbiddenStatement 同构）。
func pgForbidden(ruleKey, object, advice, message string) controllers.SummaryItem {
	return controllers.ForbiddenStatement(ruleKey, object, advice, message)
}

// gateCreateTable 判定 `CREATE TABLE ... AS SELECT` / `CREATE TABLE ... (LIKE src)`。
func (c *pgInspectContext) gateCreateTable(stmt string) []controllers.SummaryItem {
	if !c.params.ENABLE_CREATE_TABLE_AS {
		if table, ok := parseCreateTableAs(stmt); ok {
			return []controllers.SummaryItem{pgForbidden(
				"disable_create_table_as",
				table,
				"① 改为两步执行：先 `CREATE TABLE 表名 (列定义…)`，再 `INSERT INTO 表名 SELECT …`；"+
					"② 若确需一次性建表并灌数据，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止 CREATE TABLE ... AS SELECT」规则",
				fmt.Sprintf("语句不允许执行：`CREATE TABLE ... AS SELECT`（表`%s`），该语法会绕过字段/索引/约束等建表规范", table),
			)}
		}
	}
	if !c.params.ENABLE_CREATE_TABLE_LIKE {
		if table, source, ok := parseCreateTableLike(stmt); ok {
			src := source
			if src == "" {
				src = "源表"
			}
			return []controllers.SummaryItem{pgForbidden(
				"disable_create_table_like",
				table,
				"① 改为显式列定义建表（可先查看源表结构再补齐字段定义）；"+
					"② 若确需复制表结构，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止 CREATE TABLE ... LIKE」规则",
				fmt.Sprintf("语句不允许执行：`CREATE TABLE ... (LIKE %s)`（表`%s`），表结构完全继承自源表，字段/索引/约束规范无法逐项校验", src, table),
			)}
		}
	}
	return nil
}

// inspectView 视图相关操作（CREATE / ALTER / DROP / REFRESH [MATERIALIZED] VIEW）。
//
// 平台禁止视图时给出语句级结论（同 MySQL 的准入闸门）；视图被允许时 PG 侧暂无后续规则，
// 直接放行（视图存在性、依赖表存在性等尚未纳入 PG 引擎）。
func (c *pgInspectContext) inspectView(stmt string) []controllers.SummaryItem {
	if c.params.ENABLE_CREATE_VIEW {
		return nil
	}
	op, view, ok := parseViewStatement(stmt)
	if !ok {
		return nil
	}
	object := view
	desc := fmt.Sprintf("`%s`", op)
	if view != "" {
		desc = fmt.Sprintf("`%s`（视图`%s`）", op, view)
	}
	return []controllers.SummaryItem{pgForbidden(
		"disable_view_related_op",
		object,
		"① 改为在应用层封装查询，或使用普通表承载数据；"+
			"② 若确需视图，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止视图相关操作」规则",
		fmt.Sprintf("语句不允许执行：%s，平台规范禁止使用视图对象", desc),
	)}
}
