package checker

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// Oracle 语句准入闸门：与 MySQL（checker/stmt.go: statementAdmissionGate）和
// PG（pg_gate.go）对称——CTAS / CTL / 视图属「整类不允许执行」的语句，
// 命中即给出语句级结论、不再进入规则链（它们的表结构继承自 SELECT/源表，
// 后续逐列规则只能产出无法满足的伪结论）。

// pgForbidden 构造语句级禁止结论（通用助手，见 pg_gate.go）。

// gateOracleCreateTable CREATE TABLE 准入：CTAS / CTL。
// Oracle 原生没有 CREATE TABLE ... LIKE 语法（PG/MySQL 才有），命中 CTL 时
// 属于「语法不适用于 Oracle」——同样按 disable_create_table_like 给出结论。
func (c *oracleInspectContext) gateOracleCreateTable(stmt string) []controllers.SummaryItem {
	clean := strings.TrimSpace(stmt)
	rest, ok := trimOracleStmtPrefix(clean, "create", "table")
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem

	// CTAS：表定义之后有顶层 AS SELECT
	if findOracleCTASMark(rest) >= 0 {
		if !c.params.ENABLE_CREATE_TABLE_AS {
			return append(items, pgForbidden("disable_create_table_as", "",
				"① 改为显式列定义的 `CREATE TABLE`；② 如需复制表结构，先建表再用 `INSERT INTO ... SELECT` 迁移数据",
				"不允许使用 CREATE TABLE ... AS SELECT 语法"))
		}
		return items // 规范允许 CTAS：无逐列规则可查，直接放行
	}
	// CTL（括号内 LIKE）：Oracle 不支持该语法，语句执行必然失败
	if open, closeIdx := findTopLevelParen(rest); open >= 0 && closeIdx > open {
		body := strings.TrimSpace(stripPGStringsAndComments(rest[open+1 : closeIdx]))
		if strings.HasPrefix(strings.ToUpper(body), "LIKE ") {
			if !c.params.ENABLE_CREATE_TABLE_LIKE {
				return append(items, pgForbidden("disable_create_table_like", "",
					"① Oracle 不支持 CREATE TABLE ... LIKE 语法；② 复制表结构请改为显式列定义，或使用 `CREATE TABLE t AS SELECT * FROM src WHERE 1=0`（如规范允许 CTAS）",
					"不允许使用 CREATE TABLE ... LIKE 语法（Oracle 亦不支持该语法）"))
			}
		}
	}
	return items
}

// inspectOracleView 视图相关操作的准入判定（规则 disable_view_related_op）
func (c *oracleInspectContext) inspectOracleView(stmt string) []controllers.SummaryItem {
	op, ok := parseOracleViewStatement(stmt)
	if !ok {
		return nil
	}
	if c.params.ENABLE_CREATE_VIEW {
		return nil
	}
	return []controllers.SummaryItem{pgForbidden("disable_view_related_op", "",
		"① 视图会隐藏真实表结构、放大权限边界，平台统一禁止；② 如有查询需求，请在 SQL Workbench 中直接查询基表",
		fmt.Sprintf("不允许 %s 相关操作（视图/物化视图）", op))}
}
