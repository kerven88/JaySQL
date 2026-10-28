package logics

// 表存在性判定的公共能力（规则 delete_stmt_check_table_exists）。
//
// 设计要点：
//  1. 表存在性对 MySQL 与 PostgreSQL 都可用（MySQL 走 DESC，PG 走 information_schema）；
//  2. 判定异常（连不上库、权限不足、SQL 执行失败）一律跳过，绝不猜测。

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

// tableExistence 表存在性判定结果
type tableExistence struct {
	Checked bool // 是否得到确定结论（查询异常时为 false，调用方应跳过告警）
	Exists  bool // 表是否存在（仅 Checked=true 时有意义）
}

// checkTableExists 判定表是否存在
func checkTableExists(table string, r *controllers.RuleHint) tableExistence {
	if r == nil || r.DB == nil || strings.TrimSpace(table) == "" {
		return tableExistence{}
	}
	msg, err := dao.CheckIfTableExists(table, r.DB)
	if err == nil {
		return tableExistence{Checked: true, Exists: true}
	}
	// dao.CheckIfTableExists 对「表不存在」与「查询异常」都返回 err，只能按文案区分：
	// 只有明确判定为不存在才告警，其余（连不上库 / 权限不足等）跳过，避免误报。
	if strings.Contains(msg, "不存在") {
		return tableExistence{Checked: true, Exists: false}
	}
	return tableExistence{}
}

// checkTableExistsCrossDB 跨库表存在性判定（目标表带库/schema 前缀、不是当前连接库时使用）。
//
// 语义刻意偏宽松：按 information_schema 全库按表名匹配，任一库同名即视为存在。
// 宁可漏报也不能误报 —— DESC 只能在当前连接库执行，跨库表一律查不到，
// 直接用它会把「db2.t 确实存在」误报成「表不存在」，这是比漏报更糟的结论。
func checkTableExistsCrossDB(table string, r *controllers.RuleHint) tableExistence {
	if r == nil || r.DB == nil || strings.TrimSpace(table) == "" {
		return tableExistence{}
	}
	msg, err := dao.CheckIfTableExistsCrossDB(table, r.DB)
	if err == nil {
		return tableExistence{Checked: true, Exists: true}
	}
	if strings.Contains(msg, "不存在") {
		return tableExistence{Checked: true, Exists: false}
	}
	return tableExistence{}
}

// appendUniqueName 去重追加（大小写不敏感，保持顺序稳定，便于告警文案可预期）
func appendUniqueName(list []string, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return list
	}
	for _, exist := range list {
		if strings.EqualFold(exist, name) {
			return list
		}
	}
	return append(list, name)
}
