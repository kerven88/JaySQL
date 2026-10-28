package logics

import (
	"fmt"
	"sort"
	"strings"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/parser"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// LogicDMLInsertIntoSelect
func LogicDMLInsertIntoSelect(v *traverses.TraverseDMLInsertIntoSelect, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if r.InspectParams.DISABLE_INSERT_INTO_SELECT && v.HasSelectSubQuery {
		r.Warn(fmt.Sprintf("禁止使用 `%s INTO SELECT` 语法", v.DMLType))
	}
	if r.InspectParams.DISABLE_ON_DUPLICATE && v.HasOnDuplicate {
		r.Warn(fmt.Sprintf("禁止使用 `%s ... ON DUPLICATE KEY UPDATE` 语法", v.DMLType))
	}
}

// LogicDMLNoWhere
func LogicDMLNoWhere(v *traverses.TraverseDMLNoWhere, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if !v.HasWhere && r.InspectParams.DML_MUST_HAVE_WHERE {
		r.WarnWithRule("update_delete_stmt_suggest_where", fmt.Sprintf("`%s` 语句必须包含 `WHERE` 条件（避免全表扫描/全表更新）", v.DMLType))
	}
}

// LogicDMLInsertWithColumns
func LogicDMLInsertWithColumns(v *traverses.TraverseDMLInsertWithColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	// 禁止 REPLACE：只加一条提示，不再提前 return —— 否则本函数随后的字段列表/行数/NULL 检查
	// 会跟着一起被跳过（一条规则的违规不该掩盖同一规则内的其它违规）。
	if v.DMLType == "REPLACE" && r.InspectParams.DISABLE_REPLACE {
		r.Warn("禁止使用 `REPLACE` 语句（可能导致隐式删除+插入）")
	}
	// 1）字段列表重复：`INSERT INTO t(a,a) VALUES(...)`（不依赖表结构）
	if r.InspectParams.CHECK_INSERT_COLUMN_NO_DUP && len(v.DuplicateColumns) > 0 {
		r.WarnWithRule("insert_stmt_col_name_no_dup", fmt.Sprintf("`%s` 语句字段列表中存在重复字段：%s；请删除重复项", v.DMLType, strings.Join(v.DuplicateColumns, " / ")))
	}
	// 2）必须显式指定字段列表：避免表结构变更后出现列错位
	if r.InspectParams.CHECK_INSERT_SPECIFY_COLUMNS && v.ColumnsCount == 0 {
		r.WarnWithRule("insert_stmt_specify_columns", fmt.Sprintf("`%s` 语句必须显式指定字段列表（如：`%s INTO t(col1,col2) VALUES ...`）", v.DMLType, v.DMLType))
	}
	// 3）不建议使用 SYSDATE()：它取的是函数执行时刻而非语句开始时刻，会导致主从数据不一致
	if r.InspectParams.CHECK_INSERT_AVOID_SYSDATE && v.HasSysdateFunc {
		r.WarnWithRule("insert_stmt_avoid_sysdate_func", fmt.Sprintf("`%s` 语句不建议使用 `SYSDATE()` 函数（返回执行时刻而非语句开始时间，主从/回放会产生不一致）；请改用 `NOW()` 或 `CURRENT_TIMESTAMP`", v.DMLType))
	}
	// MAX_INSERT_ROWS=0 表示不限制（规则关闭时的取值）
	if r.InspectParams.MAX_INSERT_ROWS > 0 && v.RowsCount > r.InspectParams.MAX_INSERT_ROWS {
		r.WarnWithRule("limit_insert_values_row_count", fmt.Sprintf("`%s` 单次写入行数过多：最多允许 %d 行，当前 %d 行；建议拆分为多条执行", v.DMLType, r.InspectParams.MAX_INSERT_ROWS, v.RowsCount))
	}
	// 5）字段列表和值列表数量要匹配（规则 insert_stmt_columns_match_values）
	//    有字段列表：值个数与字段个数逐行比对（traverse 已算好）；
	//    无字段列表：与真实表列数比对，表结构读不到时跳过（由「表是否存在」规则负责兜底），
	//    `VALUES ()` 是合法的全默认值插入，不算不匹配。
	if r.InspectParams.CHECK_INSERT_COLUMNS_MATCH_VALUES && len(v.RowValueCounts) > 0 {
		if v.ColumnsCount > 0 {
			for i, got := range v.RowValueCounts {
				if got == v.ColumnsCount {
					continue
				}
				r.WarnWithRule("insert_stmt_columns_match_values", fmt.Sprintf("`%s` 语句字段列表有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配；请核对字段与值一一对应", v.DMLType, v.ColumnsCount, i+1, got))
				break
			}
		} else if schema := loadTableSchema(v.Table, r); schema != nil && len(schema.Cols) > 0 {
			want := len(schema.Cols)
			for i, got := range v.RowValueCounts {
				if got == want || got == 0 {
					continue
				}
				r.WarnWithRule("insert_stmt_columns_match_values", fmt.Sprintf("`%s` 语句未指定字段列表，表`%s`有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配；建议显式指定字段列表", v.DMLType, v.Table, want, i+1, got))
				break
			}
		}
	}
	// 6）NOT NULL 列不允许显式插入 NULL 值：依赖目标表结构，读不到时静默跳过
	if !r.InspectParams.CHECK_INSERT_NOT_NULL_COLUMN_NULL || len(v.NullValueColumns) == 0 {
		return
	}
	// 解析目标表结构。不可读时（PostgreSQL 没有 SHOW CREATE TABLE、目标对象是视图等）
	// 依赖表结构的检查静默跳过，不把「读不到结构」当成「字段不存在」报出来。
	schema := loadTableSchema(v.Table, r)
	if schema == nil {
		return
	}
	var bad []string
	for _, col := range v.NullValueColumns {
		if utils.IsContain(schema.NotNullCols, col) {
			bad = appendUniqueName(bad, col)
		}
	}
	if len(bad) > 0 {
		r.WarnWithRule("insert_stmt_not_null_column_null_value", fmt.Sprintf("表`%s`中字段`%s`为 `NOT NULL`，不允许插入 `NULL` 值", v.Table, strings.Join(bad, " / ")))
	}
}

// LogicDMLHasConstraint
//
// 三个子项（LIMIT / ORDER BY / 子查询）互不排斥：一条语句同时踩中多个时全部报出，
// 不再有一个命中就让后面的检查消失。
func LogicDMLHasConstraint(v *traverses.TraverseDMLHasConstraint, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if v.HasLimit && r.InspectParams.DML_DISABLE_LIMIT {
		// 历史参数兜底（当前无规则再设置该参数，保留以防回退）
		r.Warn(fmt.Sprintf("`%s` 语句禁止使用 `LIMIT` 子句", v.DMLType))
	}
	// LIMIT 数据量阈值：行数超过 DML_MAX_LIMIT_ROWS 才告警（规则 limit_update_delete_data_size）
	if v.HasLimit && r.InspectParams.DML_MAX_LIMIT_ROWS > 0 && (v.LimitCount <= 0 || v.LimitCount > r.InspectParams.DML_MAX_LIMIT_ROWS) {
		if v.LimitCount > 0 {
			r.WarnWithRule("limit_update_delete_data_size", fmt.Sprintf("`%s` 语句 LIMIT 行数超过上限：最多允许 %d 行，当前 %d 行；建议缩小 LIMIT 或分批处理", v.DMLType, r.InspectParams.DML_MAX_LIMIT_ROWS, v.LimitCount))
		} else {
			r.WarnWithRule("limit_update_delete_data_size", fmt.Sprintf("`%s` 语句使用了无法确定行数的 `LIMIT` 子句，请确认单次操作不超过 %d 行", v.DMLType, r.InspectParams.DML_MAX_LIMIT_ROWS))
		}
	}
	if v.HasOrderBy && r.InspectParams.DML_DISABLE_ORDERBY {
		r.WarnWithRule("update_delete_stmt_no_order_by_clause", fmt.Sprintf("`%s` 语句禁止使用 `ORDER BY` 子句", v.DMLType))
	}
	if v.HasSubQuery && r.InspectParams.DML_DISABLE_SUBQUERY {
		r.WarnWithRule("update_delete_stmt_where_subquery_check", fmt.Sprintf("`%s` 语句禁止包含子查询（请改写为 JOIN 或分步执行）", v.DMLType))
	}
}

// LogicDMLJoinWithOn
func LogicDMLJoinWithOn(v *traverses.TraverseDMLJoinWithOn, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if v.HasJoin && r.InspectParams.CHECK_DML_JOIN_WITH_ON && !v.IsJoinWithOn {
		r.WarnWithRule("update_delete_stmt_join_on_check", fmt.Sprintf("`%s` 的 JOIN 必须包含 `ON` 条件（禁止笛卡尔积）", v.DMLType))
	}
}

// LogicDMLMustUseIndex UPDATE/DELETE 必须使用索引（规则 update_delete_stmt_must_use_idx）
//
// 判定依据是 EXPLAIN：任一张被访问的表 key 为空（未走索引）即告警。
// 只对你真的要改的那张表的访问路径负责，不做语法层面的猜测。
// TiDB / PG 等没有 type/key 列的数据源会返回 Checked=false，此处自动跳过、不误报。
func LogicDMLMustUseIndex(v *traverses.TraverseDMLMaxUpdateRows, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if !r.InspectParams.CHECK_DML_MUST_USE_INDEX {
		return
	}
	usage, err := (&process.Explain{DB: r.DB, SQL: r.Query, KV: r.KV}).GetIndexUsage()
	if err != nil {
		// EXPLAIN 执行失败（表不存在、方言不支持等）时不做判定：
		// 由「更新影响行数」规则统一报错，避免同一问题重复告警。
		return
	}
	if !usage.Checked || usage.Used {
		return
	}
	accessType := strings.TrimSpace(usage.AccessType)
	if accessType == "" {
		accessType = "未知"
	}
	msg := fmt.Sprintf("`%s` 语句未使用索引（表`%s`，访问类型 %s）；建议为 WHERE 条件补充可用索引",
		v.DMLType, usage.Table, accessType)
	possible := strings.TrimSpace(usage.PossibleKeys)
	if possible != "" && !strings.EqualFold(possible, "NULL") {
		msg += fmt.Sprintf("（可选索引：%s；若未被选中，请检查条件是否满足最左前缀）", possible)
	}
	r.WarnWithRule("update_delete_stmt_must_use_idx", msg)
}

// LogicDMLUpdateSetSeparator UPDATE 的 SET 子句用 AND 分隔多个列（规则 update_stmt_set_separator_check）
//
// `UPDATE t SET a=1 AND b=2 WHERE ...` 会被 MySQL 静默解析成 `a = (1 AND (b=2))`，
// 语义完全不是「同时更新两列」，是明确的事故源。这里按 AST 判定：
// SET 右值是 AND/OR/XOR 且其右操作数为另一个 `列 = 值` 表达式才告警；
// 合法的布尔表达式 `SET a = (b>1 AND c<2)` 外层是括号表达式，不会误报。
func LogicDMLUpdateSetSeparator(v *traverses.TraverseDMLUpdateSetColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_UPDATE_SET_SEPARATOR {
		return
	}
	if len(v.AndSepCols) == 0 {
		return
	}
	r.WarnWithRule("update_stmt_set_separator_check", fmt.Sprintf("`UPDATE` 语句 SET 多个列之间使用了 `AND` 作为分隔符（%s）；多个列的赋值必须使用英文逗号 `,` 分隔", strings.Join(v.AndSepCols, " / ")))
}

// LogicDMLUpdateMultiTableColumnPrefix 多表 UPDATE 的 SET 列必须指定表前缀（规则 update_multi_table_column_prefix_check）
func LogicDMLUpdateMultiTableColumnPrefix(v *traverses.TraverseDMLUpdateSetColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX {
		return
	}
	if v.TableCount <= 1 || len(v.NoPrefixCols) == 0 {
		return
	}
	r.WarnWithRule("update_multi_table_column_prefix_check", fmt.Sprintf("多表 `UPDATE` 语句 SET 子句中的列必须指定表前缀（如 `t1.col = 1`）；未指定前缀的列：%s", strings.Join(v.NoPrefixCols, " / ")))
}

// LogicDMLUpdatePrimaryKey UPDATE 是否更新了主键（规则 update_stmt_check_updates_pk）
//
// 依赖目标表结构（SHOW CREATE TABLE）。表结构读不到时跳过，不做猜测、不误报。
func LogicDMLUpdatePrimaryKey(v *traverses.TraverseDMLUpdateSetColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_UPDATE_UPDATES_PK {
		return
	}
	for _, item := range v.SetItems {
		if item.Table == "" {
			continue
		}
		schema := loadTableSchema(item.Table, r)
		if schema == nil || len(schema.PrimaryKeys) == 0 {
			continue
		}
		if utils.IsContain(schema.PrimaryKeys, item.Column) {
			r.WarnWithRule("update_stmt_check_updates_pk", fmt.Sprintf("`UPDATE` 语句不建议更新表`%s`的主键列（`%s`）；更新主键会导致行迁移、二级索引重构与主从延迟", item.Table, item.Column))
		}
	}
}

// LogicDMLUpdateUniqueKey UPDATE 是否更新了唯一键（规则 update_stmt_check_updates_unique_key）
//
// 依赖目标表结构（SHOW CREATE TABLE）。表结构读不到时跳过，不做猜测、不误报。
// 主键由 update_stmt_check_updates_pk 单独覆盖，此处只在唯一键集合里匹配，避免重复告警。
func LogicDMLUpdateUniqueKey(v *traverses.TraverseDMLUpdateSetColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_UPDATE_UPDATES_UNIQUE_KEY {
		return
	}
	for _, item := range v.SetItems {
		if item.Table == "" {
			continue
		}
		schema := loadTableSchema(item.Table, r)
		if schema == nil || len(schema.UniqueCols) == 0 {
			continue
		}
		if utils.IsContain(schema.UniqueCols, item.Column) {
			r.WarnWithRule("update_stmt_check_updates_unique_key", fmt.Sprintf("`UPDATE` 语句不建议更新表`%s`的唯一键列（`%s`）；更新唯一键会触发唯一性校验与行迁移，代价高于普通列", item.Table, item.Column))
		}
	}
}

// collectWhereInfo 采集 WHERE 子句的检查特征。
// 表结构不可读时只做语法层判定（LIKE / NOT IN / OR），依赖表结构的判定自动落空。
func collectWhereInfo(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) *process.WhereConditions {
	if v.WhereExpr == nil {
		return &process.WhereConditions{}
	}
	meta := process.TableColumnMeta{TableName: strings.ToLower(strings.TrimSpace(v.Table))}
	if schema := loadTableSchema(v.Table, r); schema != nil {
		meta.IndexCols = toLowerSet(schema.IndexCols)
		meta.UniqueCols = toLowerSet(schema.UniqueCols)
		meta.ColTypes = toLowerTypeMap(schema.ColTypes)
	}
	return process.CollectWhereConditions(v.WhereExpr, meta)
}

// toLowerSet 列集合转小写集合
func toLowerSet(cols []string) map[string]struct{} {
	set := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		if key := strings.ToLower(strings.TrimSpace(c)); key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}

// toLowerTypeMap 列类型映射的 key 统一为小写
func toLowerTypeMap(types map[string]byte) map[string]byte {
	out := make(map[string]byte, len(types))
	for name, tp := range types {
		out[strings.ToLower(strings.TrimSpace(name))] = tp
	}
	return out
}

// LogicDMLWhereLeadingWildcard WHERE 条件中的 LIKE 前通配符（规则 where_avoid_leading_wildcard）
func LogicDMLWhereLeadingWildcard(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_WHERE_LEADING_WILDCARD {
		return
	}
	info := collectWhereInfo(v, r)
	if len(info.LeadingWildcards) == 0 {
		return
	}
	r.WarnWithRule("where_avoid_leading_wildcard", fmt.Sprintf("`%s` 语句 WHERE 条件中 `LIKE` 使用了前通配符（%s）：无法使用索引，会退化为全表扫描；建议改为后缀匹配或使用全文检索", v.DMLType, strings.Join(info.LeadingWildcards, " / ")))
}

// LogicDMLWhereLikeWithoutWildcard WHERE 条件中没有通配符的 LIKE（规则 where_like_without_wildcard）
func LogicDMLWhereLikeWithoutWildcard(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_WHERE_LIKE_WITHOUT_WILDCARD {
		return
	}
	info := collectWhereInfo(v, r)
	if len(info.NoWildcardLikes) == 0 {
		return
	}
	r.WarnWithRule("where_like_without_wildcard", fmt.Sprintf("`%s` 语句 WHERE 条件中的 `LIKE` 没有通配符（%s）：语义等同 `=`，建议直接改用 `=`", v.DMLType, strings.Join(info.NoWildcardLikes, " / ")))
}

// LogicDMLWhereNotInNotLike WHERE 条件中的反向查询（规则 where_avoid_not_in_not_like）
func LogicDMLWhereNotInNotLike(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_WHERE_NOT_IN_NOT_LIKE {
		return
	}
	info := collectWhereInfo(v, r)
	var parts []string
	for _, e := range info.NotInExprs {
		parts = append(parts, fmt.Sprintf("`NOT IN`（%s）", e))
	}
	for _, e := range info.NotLikeExprs {
		parts = append(parts, fmt.Sprintf("`NOT LIKE`（%s）", e))
	}
	if len(parts) == 0 {
		return
	}
	r.WarnWithRule("where_avoid_not_in_not_like", fmt.Sprintf("`%s` 语句 WHERE 条件中不建议使用反向查询 %s：无法有效利用索引，且 `NOT IN` 遇到 NULL 会导致结果集为空；建议改写为正向条件或 `LEFT JOIN ... IS NULL`", v.DMLType, strings.Join(parts, " / ")))
}

// LogicDMLWhereOrCondition WHERE 条件中通过 OR 连接过滤条件（规则 where_or_condition_check）
func LogicDMLWhereOrCondition(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_WHERE_OR_CONDITION {
		return
	}
	info := collectWhereInfo(v, r)
	if len(info.OrConditions) == 0 {
		return
	}
	r.WarnWithRule("where_or_condition_check", fmt.Sprintf("`%s` 语句 WHERE 条件中使用 `OR` 连接过滤条件（%s）：容易导致索引失效；建议拆分为多条语句或改写为 `UNION ALL` / `IN`", v.DMLType, strings.Join(info.OrConditions, " / ")))
}

// LogicDMLWhereIndexColCalc WHERE 条件中索引列参与数学运算或函数运算（规则 where_avoid_index_col_arithmetic_or_func）
func LogicDMLWhereIndexColCalc(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_WHERE_INDEX_COL_CALC {
		return
	}
	info := collectWhereInfo(v, r)
	if len(info.IndexColCalcs) == 0 {
		return
	}
	r.WarnWithRule("where_avoid_index_col_arithmetic_or_func", fmt.Sprintf("`%s` 语句 WHERE 条件中字段参与了数学运算或函数运算（%s）：该字段上的索引将失效；建议改写为对常量求值，如 `col = 1 + 2` 而非 `col - 1 = 2`、`col >= '2023-10-01' AND col < '2023-10-02'` 而非 `DATE(col) = '2023-10-01'`", v.DMLType, strings.Join(info.IndexColCalcs, " / ")))
}

// LogicDMLWhereImplicitCast WHERE 条件中字段存在隐式类型转换（规则 where_implicit_type_cast_check）
func LogicDMLWhereImplicitCast(v *traverses.TraverseDMLWhereConditions, r *controllers.RuleHint) {
	if v.IsMatch == 0 || !r.InspectParams.CHECK_WHERE_IMPLICIT_CAST {
		return
	}
	info := collectWhereInfo(v, r)
	if len(info.ImplicitCasts) == 0 {
		return
	}
	r.WarnWithRule("where_implicit_type_cast_check", fmt.Sprintf("`%s` 语句 WHERE 条件中存在隐式类型转换（%s）：字段与比较值的类型不一致会导致索引失效；请让比较值与字段类型一致", v.DMLType, strings.Join(info.ImplicitCasts, " / ")))
}

// LogicDMLDeleteTableExists DELETE 语句检测表是否存在（规则 delete_stmt_check_column_exists）
//
// 复用 UPDATE 侧的表存在性判定（MySQL 走 DESC、PostgreSQL 走 information_schema）。
// 查询异常（连不上库 / 权限不足）时跳过，不误报。
func LogicDMLDeleteTableExists(v *traverses.TraverseDMLDeleteTables, r *controllers.RuleHint) {
	if v.IsMatch == 0 || r.InspectParams == nil || !r.InspectParams.CHECK_DELETE_TABLE_EXISTS {
		return
	}
	var missing []string
	for _, table := range v.Tables {
		if res := checkTableExists(table, r); res.Checked && !res.Exists {
			missing = appendUniqueName(missing, table)
		}
	}
	if len(missing) > 0 {
		// DELETE 目标表不存在时执行必然失败（MySQL 1146 / PG 42P01），归为对象缺失
		r.WarnFatal(
			"delete_stmt_check_table_exists",
			controllers.CategoryObjectMissing,
			strings.Join(missing, " / "),
			"① 核对表名拼写与大小写；② 确认是否选错了目标库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
			fmt.Sprintf("表`%s`不存在", strings.Join(missing, " / ")),
		)
	}
}

// LogicDMLInsertObjectExists INSERT 语句检测目标表 / 字段是否存在（规则 insert_stmt_check_column_exists）
//
// 判定口径：
//  1. 表存在性：MySQL 走 DESC、PostgreSQL 走 information_schema，两种数据源都覆盖；
//     目标表带库前缀且不是当前库时走全库探测（宁可漏报，不把跨库表误报成不存在）；
//  2. 字段存在性：仅 MySQL（依赖 SHOW CREATE TABLE）。`INSERT INTO t VALUES(...)`
//     没有字段列表时无从判定，只报表；
//  3. 表不存在时直接返回，不再判字段 —— 表都没有，列判定必然误报；
//  4. 查询异常（连不上库 / 权限不足 / 标识符非法）一律跳过，不猜测。
func LogicDMLInsertObjectExists(v *traverses.TraverseDMLInsertWithColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || r.InspectParams == nil || !r.InspectParams.CHECK_INSERT_OBJECT_EXISTS {
		return
	}
	table := strings.TrimSpace(v.Table)
	if table == "" || r.DB == nil {
		return
	}

	// 1) 目标表是否存在
	res := checkTableExists(table, r)
	if v.Schema != "" && !strings.EqualFold(v.Schema, r.DB.Database) {
		res = checkTableExistsCrossDB(table, r)
	}
	if !res.Checked {
		return
	}
	if !res.Exists {
		r.WarnFatal(
			"insert_stmt_check_column_exists",
			controllers.CategoryObjectMissing,
			table,
			"① 核对表名拼写与大小写；② 确认是否选错了目标库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
			fmt.Sprintf("表`%s`不存在", table),
		)
		return
	}

	// 2) 字段是否存在（未显式指定字段列表时跳过；PG 读不到 SHOW CREATE TABLE 时自动跳过）
	if len(v.Columns) == 0 {
		return
	}
	schema := loadTableSchema(table, r)
	if schema == nil || len(schema.Cols) == 0 {
		return
	}
	existing := make(map[string]struct{}, len(schema.Cols))
	for _, col := range schema.Cols {
		existing[strings.ToLower(strings.TrimSpace(col))] = struct{}{}
	}
	var missing []string
	for _, col := range v.Columns {
		name := strings.ToLower(strings.TrimSpace(col))
		if name == "" {
			continue
		}
		if _, ok := existing[name]; !ok {
			missing = appendUniqueName(missing, col)
		}
	}
	if len(missing) > 0 {
		r.WarnFatal(
			"insert_stmt_check_column_exists",
			controllers.CategoryObjectMissing,
			strings.Join(missing, " / "),
			"① 核对字段名拼写；② 执行 DESC 表名 查看真实字段列表；③ 字段已改名时同步修改 INSERT 列表",
			fmt.Sprintf("表`%s`中字段`%s`不存在", table, strings.Join(missing, " / ")),
		)
	}
}

// loadTableSchema 读取并解析目标表结构。表结构不可读（表不存在 / 数据源不支持）时返回 nil，
// 表示调用方应跳过依赖表结构的检查，而不是把它当成违规。
func loadTableSchema(table string, r *controllers.RuleHint) *traverses.TraverseAlterTableShowCreateTableGetCols {
	if r == nil || r.DB == nil || strings.TrimSpace(table) == "" {
		return nil
	}
	audit, err := dao.ShowCreateTable(table, r.DB, r.KV)
	if err != nil {
		return nil
	}
	v := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	switch a := audit.(type) {
	case *parser.Audit:
		if len(a.TiStmt) == 0 {
			return nil
		}
		a.TiStmt[0].Accept(v)
	default:
		return nil
	}
	return v
}

// LogicDMLMaxUpdateRows UPDATE/DELETE 影响行数上限（规则 limit_update_delete_affected_rows）
//
// 依赖 EXPLAIN 取「预计影响/扫描行数」。它是 must 级规则，也曾经是整条链上最大的受害者：
// 原实现排在「必须包含 WHERE」之后，而无 WHERE 会置 IsBreak 短路整条链，
// 导致「无条件全表 UPDATE」永远取不到这个判定 —— 风险最高的场景反而免检。
// 现在链路不再短路，本规则对每条 UPDATE/DELETE 都必然执行。
func LogicDMLMaxUpdateRows(v *traverses.TraverseDMLMaxUpdateRows, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	explain := process.Explain{DB: r.DB, SQL: r.Query, KV: r.KV}
	affectedRows, err := explain.Get(r.InspectParams.EXPLAIN_RULE)
	if err != nil {
		r.AffectedRows = 0
		r.Warn(err.Error())
		return
	}
	// MAX_AFFECTED_ROWS=0 表示不限制（规则关闭时的取值）
	if r.InspectParams.MAX_AFFECTED_ROWS > 0 && affectedRows > r.InspectParams.MAX_AFFECTED_ROWS {
		r.AffectedRows = affectedRows
		r.WarnWithRule("limit_update_delete_affected_rows", fmt.Sprintf("`%s` 预计影响/扫描行数 %d，超过上限 %d；建议拆分语句或缩小 WHERE 范围", v.DMLType, affectedRows, r.InspectParams.MAX_AFFECTED_ROWS))
		return
	}
	r.AffectedRows = affectedRows
}

// LogicDMLMaxInsertRows INSERT 影响行数（把一次写入的行数回填到 AffectedRows）
func LogicDMLMaxInsertRows(v *traverses.TraverseDMLMaxInsertRows, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.AffectedRows = v.RowsCount
}

// LogicDMLJoinTableCount UPDATE/DELETE 多表关联数量检查（规则 limit_update_delete_join_table_count）
func LogicDMLJoinTableCount(v *traverses.TraverseDMLJoinTableCount, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	// MAX_UPDATE_DELETE_JOIN_TABLES=0 表示不限制（规则关闭时的取值）
	if r.InspectParams.MAX_UPDATE_DELETE_JOIN_TABLES > 0 && v.TableCount > r.InspectParams.MAX_UPDATE_DELETE_JOIN_TABLES {
		r.WarnWithRule("limit_update_delete_join_table_count", fmt.Sprintf("`%s` 语句关联表数过多：最多允许 %d 张表（含主表），当前 %d 张；建议拆分语句或缩小关联范围", v.DMLType, r.InspectParams.MAX_UPDATE_DELETE_JOIN_TABLES, v.TableCount))
	}
}

// LogicDMLUpdateTimeColumn UPDATE 建议同步更新「修改时间」列（规则 update_stmt_suggest_update_time_column）
func LogicDMLUpdateTimeColumn(v *traverses.TraverseDMLUpdateSetColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || v.DMLType != "UPDATE" {
		return
	}
	cols := strings.TrimSpace(r.InspectParams.UPDATE_TIME_COLUMNS)
	if cols == "" {
		return
	}
	want := map[string]struct{}{}
	for _, c := range strings.Split(cols, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			want[c] = struct{}{}
		}
	}
	if len(want) == 0 {
		return
	}
	for _, col := range v.SetColumns {
		// SET 列带表前缀（如 t.update_time）时按后缀匹配
		if i := strings.LastIndex(col, "."); i >= 0 {
			col = col[i+1:]
		}
		if _, ok := want[col]; ok {
			return
		}
	}
	r.WarnWithRule("update_stmt_suggest_update_time_column", fmt.Sprintf("`UPDATE` 语句建议同步更新表上的修改时间列（%s），便于数据审计与变更追踪", strings.Join(mapKeysSorted(want), " / ")))
}

// LogicDMLAvoidUpdateColumns UPDATE 不建议更新「创建时间」列（规则 update_stmt_avoid_update_create_time_column）
//
// 判定：SET 子句中出现配置列的任一项即告警（默认 create_time，可在规范里配置 gmt_create / created_at 等，
// 逗号分隔多个）。创建时间属于数据元信息、具备不可变更属性，更新会破坏审计追溯能力。
//
// 不依赖表结构：列名由规范显式配置，读不到表结构也能判定，不存在"配了不生效"的静默场景。
func LogicDMLAvoidUpdateColumns(v *traverses.TraverseDMLUpdateSetColumns, r *controllers.RuleHint) {
	if v.IsMatch == 0 || v.DMLType != "UPDATE" {
		return
	}
	cols := strings.TrimSpace(r.InspectParams.AVOID_UPDATE_COLUMNS)
	if cols == "" {
		return
	}
	avoid := map[string]struct{}{}
	for _, c := range strings.Split(cols, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			avoid[c] = struct{}{}
		}
	}
	if len(avoid) == 0 {
		return
	}
	var hit []string
	for _, col := range v.SetColumns {
		// SET 列带表前缀（如 t.create_time）时按后缀匹配
		if i := strings.LastIndex(col, "."); i >= 0 {
			col = col[i+1:]
		}
		if _, ok := avoid[strings.ToLower(col)]; ok {
			hit = appendUniqueName(hit, col)
		}
	}
	if len(hit) == 0 {
		return
	}
	r.WarnWithRule("update_stmt_avoid_update_create_time_column",
		fmt.Sprintf("`UPDATE` 语句不建议更新表上的创建时间列（%s）：创建时间属于数据元信息、具备不可变更属性，更新会破坏数据审计追溯能力", strings.Join(hit, " / ")))
}

// mapKeysSorted 返回集合的键并排序（保证告警文案稳定）
func mapKeysSorted(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LogicDMLWhereInElements WHERE 条件中 IN 子句元素个数检查（规则 limit_where_in_element_count）
func LogicDMLWhereInElements(v *traverses.TraverseDMLWhereInElements, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	// MAX_WHERE_IN_ELEMENTS=0 表示不限制（规则关闭时的取值）
	if r.InspectParams.MAX_WHERE_IN_ELEMENTS > 0 && v.MaxInList > r.InspectParams.MAX_WHERE_IN_ELEMENTS {
		r.WarnWithRule("limit_where_in_element_count", fmt.Sprintf("`%s` 语句 WHERE 条件中 `IN` 子句元素过多：最多允许 %d 个，当前 %d 个；建议改用临时表关联或分批查询", v.DMLType, r.InspectParams.MAX_WHERE_IN_ELEMENTS, v.MaxInList))
	}
}
