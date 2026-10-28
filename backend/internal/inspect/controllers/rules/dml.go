package rules

import (
	"github.com/simajiong/jaysql/internal/inspect/controllers/logics"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"

	"github.com/pingcap/tidb/pkg/parser/ast"
)

// DMLRules DML 语句的检查项。
//
// **执行顺序不再影响正确性**：checker 会逐条执行**全部**规则并把命中结果一并收集，
// 没有任何规则能把后面的规则短路掉（RuleHint 上已不存在 IsBreak 字段）。
// 因此这里允许按「可读性 + 成本」排序，而不是被迫把可能提前返回的规则挪到最前。
//
// 顺序上仍然保留两条经验性安排：
//   - 「必须使用索引」（RuleDMLMustUseIndex）依赖 EXPLAIN，先执行并把结果按 SQL 缓存进 KV，
//     后面的「更新影响行数」复用同一次 EXPLAIN，不再对目标库重复查询；
//   - 两条「影响行数」规则（更新/插入）留在最后，让 AffectedRows 在其它规则跑完后统一落定。
//
// 历史坑（已修）：这两条影响行数规则原排在第 22/23 位且无条件 IsBreak=true，
// 而第 18 位的「必须包含 WHERE」在无 WHERE 时也会 IsBreak —— 无 WHERE 的 UPDATE
// 直接短路掉「影响行数」这条 must 规则，导致全表 UPDATE 免检。详见 artifacts/ddl-breaker-shortcircuit-audit.md。
func DMLRules() []Rule {
	return []Rule{
		{
			Hint:      "DML#INSERT检测表/字段是否存在",
			CheckFunc: (*Rule).RuleDMLInsertObjectExists,
		},
		{
			Hint:      "DML#DELETE检测表是否存在",
			CheckFunc: (*Rule).RuleDMLDeleteTableExists,
		},
		{
			Hint:      "DML#UPDATE/DELETE必须使用索引",
			CheckFunc: (*Rule).RuleDMLMustUseIndex,
		},
		{
			Hint:      "DML#UPDATE/DELETE关联表数量",
			CheckFunc: (*Rule).RuleDMLJoinTableCount,
		},
		{
			Hint:      "DML#UPDATE建议更新修改时间列",
			CheckFunc: (*Rule).RuleDMLUpdateTimeColumn,
		},
		{
			Hint:      "DML#UPDATE不建议更新创建时间列",
			CheckFunc: (*Rule).RuleDMLAvoidUpdateColumns,
		},
		{
			Hint:      "DML#WHERE条件IN元素个数",
			CheckFunc: (*Rule).RuleDMLWhereInElements,
		},
		{
			Hint:      "DML#UPDATE多个列之间的分隔符",
			CheckFunc: (*Rule).RuleDMLUpdateSetSeparator,
		},
		{
			Hint:      "DML#UPDATE多表时SET列的表前缀",
			CheckFunc: (*Rule).RuleDMLUpdateMultiTableColumnPrefix,
		},
		{
			Hint:      "DML#UPDATE是否更新主键",
			CheckFunc: (*Rule).RuleDMLUpdatePrimaryKey,
		},
		{
			Hint:      "DML#UPDATE是否更新唯一键",
			CheckFunc: (*Rule).RuleDMLUpdateUniqueKey,
		},
		{
			Hint:      "DML#WHERE条件LIKE前通配符",
			CheckFunc: (*Rule).RuleDMLWhereLeadingWildcard,
		},
		{
			Hint:      "DML#WHERE条件LIKE无通配符",
			CheckFunc: (*Rule).RuleDMLWhereLikeWithoutWildcard,
		},
		{
			Hint:      "DML#WHERE条件反向查询NOT IN/NOT LIKE",
			CheckFunc: (*Rule).RuleDMLWhereNotInNotLike,
		},
		{
			Hint:      "DML#WHERE条件OR连接",
			CheckFunc: (*Rule).RuleDMLWhereOrCondition,
		},
		{
			Hint:      "DML#WHERE条件索引列运算",
			CheckFunc: (*Rule).RuleDMLWhereIndexColCalc,
		},
		{
			Hint:      "DML#WHERE条件隐式类型转换",
			CheckFunc: (*Rule).RuleDMLWhereImplicitCast,
		},
		{
			Hint:      "DML#是否允许INSERT INTO SELECT语法",
			CheckFunc: (*Rule).RuleDMLInsertIntoSelect,
		},
		{
			Hint:      "DML#必须要有WHERE条件",
			CheckFunc: (*Rule).RuleDMLNoWhere,
		},
		{
			Hint:      "DML#INSERT必须指定列名",
			CheckFunc: (*Rule).RuleDMLInsertWithColumns,
		},
		{
			Hint:      "DML#不能有LIMIT/ORDERBY/SubQuery",
			CheckFunc: (*Rule).RuleDMLHasConstraint,
		},
		{
			Hint:      "DML#JOIN操作必须要有ON语句",
			CheckFunc: (*Rule).RuleDMLJoinWithOn,
		},
		{
			Hint:      "DML#更新影响行数",
			CheckFunc: (*Rule).RuleDMLMaxUpdateRows,
		},
		{
			Hint:      "DML#插入影响行数",
			CheckFunc: (*Rule).RuleDMLMaxInsertRows,
		},
	}
}

// RuleDMLInsertObjectExists INSERT 语句检测目标表 / 字段是否存在（规则 insert_stmt_check_column_exists）
func (r *Rule) RuleDMLInsertObjectExists(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLInsertWithColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLInsertObjectExists(v, r.RuleHint)
}

// RuleDMLDeleteTableExists DELETE 语句检测表是否存在（规则 delete_stmt_check_table_exists）
func (r *Rule) RuleDMLDeleteTableExists(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLDeleteTables{}
	(*tistmt).Accept(v)
	logics.LogicDMLDeleteTableExists(v, r.RuleHint)
}

// RuleDMLInsertIntoSelect
func (r *Rule) RuleDMLInsertIntoSelect(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLInsertIntoSelect{}
	(*tistmt).Accept(v)
	logics.LogicDMLInsertIntoSelect(v, r.RuleHint)
}

// RuleDMLNoWhere
func (r *Rule) RuleDMLNoWhere(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLNoWhere{}
	(*tistmt).Accept(v)
	logics.LogicDMLNoWhere(v, r.RuleHint)
}

// RuleDMLInsertWithColumns
func (r *Rule) RuleDMLInsertWithColumns(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLInsertWithColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLInsertWithColumns(v, r.RuleHint)
}

// RuleDMLHasConstraint
func (r *Rule) RuleDMLHasConstraint(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLHasConstraint{}
	(*tistmt).Accept(v)
	logics.LogicDMLHasConstraint(v, r.RuleHint)
}

// RuleDMLJoinWithOn
func (r *Rule) RuleDMLJoinWithOn(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLJoinWithOn{}
	(*tistmt).Accept(v)
	logics.LogicDMLJoinWithOn(v, r.RuleHint)
}

// RuleDMLMaxUpdateRows
func (r *Rule) RuleDMLMaxUpdateRows(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLMaxUpdateRows{}
	(*tistmt).Accept(v)
	logics.LogicDMLMaxUpdateRows(v, r.RuleHint)
}

// RuleDMLMaxInsertRows
func (r *Rule) RuleDMLMaxInsertRows(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLMaxInsertRows{}
	(*tistmt).Accept(v)
	logics.LogicDMLMaxInsertRows(v, r.RuleHint)
}

// RuleDMLMustUseIndex UPDATE/DELETE 必须使用索引（规则 update_delete_stmt_must_use_idx）
func (r *Rule) RuleDMLMustUseIndex(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLMaxUpdateRows{}
	(*tistmt).Accept(v)
	logics.LogicDMLMustUseIndex(v, r.RuleHint)
}

// RuleDMLJoinTableCount
func (r *Rule) RuleDMLJoinTableCount(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLJoinTableCount{}
	(*tistmt).Accept(v)
	logics.LogicDMLJoinTableCount(v, r.RuleHint)
}

// RuleDMLUpdateTimeColumn
func (r *Rule) RuleDMLUpdateTimeColumn(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLUpdateSetColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLUpdateTimeColumn(v, r.RuleHint)
}

// RuleDMLAvoidUpdateColumns UPDATE 不建议更新「创建时间」列（规则 update_stmt_avoid_update_create_time_column）
func (r *Rule) RuleDMLAvoidUpdateColumns(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLUpdateSetColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLAvoidUpdateColumns(v, r.RuleHint)
}

// RuleDMLWhereInElements
func (r *Rule) RuleDMLWhereInElements(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereInElements{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereInElements(v, r.RuleHint)
}

// RuleDMLUpdateSetSeparator SET 多个列之间使用 AND 分隔（规则 update_stmt_set_separator_check）
func (r *Rule) RuleDMLUpdateSetSeparator(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLUpdateSetColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLUpdateSetSeparator(v, r.RuleHint)
}

// RuleDMLUpdateMultiTableColumnPrefix 多表 UPDATE 的 SET 列表前缀（规则 update_multi_table_column_prefix_check）
func (r *Rule) RuleDMLUpdateMultiTableColumnPrefix(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLUpdateSetColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLUpdateMultiTableColumnPrefix(v, r.RuleHint)
}

// RuleDMLUpdatePrimaryKey UPDATE 是否更新了主键（规则 update_stmt_check_updates_pk）
func (r *Rule) RuleDMLUpdatePrimaryKey(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLUpdateSetColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLUpdatePrimaryKey(v, r.RuleHint)
}

// RuleDMLUpdateUniqueKey UPDATE 是否更新了唯一键（规则 update_stmt_check_updates_unique_key）
func (r *Rule) RuleDMLUpdateUniqueKey(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLUpdateSetColumns{}
	(*tistmt).Accept(v)
	logics.LogicDMLUpdateUniqueKey(v, r.RuleHint)
}

// 以下 WHERE 条件类规则共用同一个遍历器：只分析当前语句的 WHERE 表达式，不深入子查询。

// RuleDMLWhereLeadingWildcard LIKE 前通配符（规则 where_avoid_leading_wildcard）
func (r *Rule) RuleDMLWhereLeadingWildcard(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereConditions{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereLeadingWildcard(v, r.RuleHint)
}

// RuleDMLWhereLikeWithoutWildcard 无通配符的 LIKE（规则 where_like_without_wildcard）
func (r *Rule) RuleDMLWhereLikeWithoutWildcard(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereConditions{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereLikeWithoutWildcard(v, r.RuleHint)
}

// RuleDMLWhereNotInNotLike 反向查询（规则 where_avoid_not_in_not_like）
func (r *Rule) RuleDMLWhereNotInNotLike(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereConditions{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereNotInNotLike(v, r.RuleHint)
}

// RuleDMLWhereOrCondition OR 连接过滤条件（规则 where_or_condition_check）
func (r *Rule) RuleDMLWhereOrCondition(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereConditions{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereOrCondition(v, r.RuleHint)
}

// RuleDMLWhereIndexColCalc 索引列参与运算（规则 where_avoid_index_col_arithmetic_or_func）
func (r *Rule) RuleDMLWhereIndexColCalc(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereConditions{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereIndexColCalc(v, r.RuleHint)
}

// RuleDMLWhereImplicitCast 隐式类型转换（规则 where_implicit_type_cast_check）
func (r *Rule) RuleDMLWhereImplicitCast(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDMLWhereConditions{}
	(*tistmt).Accept(v)
	logics.LogicDMLWhereImplicitCast(v, r.RuleHint)
}
