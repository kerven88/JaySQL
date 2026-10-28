package traverses

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers/process"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/opcode"
	driver "github.com/pingcap/tidb/pkg/types/parser_driver"
)

// TraverseDisableAuditDMLTables
type TraverseDisableAuditDMLTables struct {
	Tables []string
}

// TraverseDMLInsertIntoSelect
type TraverseDMLInsertIntoSelect struct {
	IsMatch           int // 是否匹配当前规则
	DMLType           string
	HasSelectSubQuery bool // 有 insert/replace into ... select ...
	HasOnDuplicate    bool // 有 OnDuplicate
}

func (c *TraverseDMLInsertIntoSelect) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.InsertStmt:
		c.IsMatch++
		c.DMLType = "INSERT"
		if stmt.IsReplace {
			c.DMLType = "REPLACE"
		}
		if stmt.Select != nil {
			c.HasSelectSubQuery = true
		}
		if !stmt.IsReplace && len(stmt.OnDuplicate) > 0 {
			c.HasOnDuplicate = true
		}
	}
	return in, false
}

func (c *TraverseDMLInsertIntoSelect) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLNoWhere
type TraverseDMLNoWhere struct {
	IsMatch  int // 是否匹配当前规则
	HasWhere bool
	DMLType  string
}

func (c *TraverseDMLNoWhere) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
		if stmt.Where != nil {
			c.HasWhere = true
		}
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
		if stmt.Where != nil {
			c.IsMatch++
			c.HasWhere = true
		}
	}
	return in, false
}

func (c *TraverseDMLNoWhere) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLInsertWithColumns
type TraverseDMLInsertWithColumns struct {
	Table             string
	Schema            string   // INSERT 目标表的库/schema 前缀（`db.t` 时为 db；为空表示当前库）
	IsMatch           int      // 是否匹配当前规则
	Columns           []string // 列名
	ColumnsCount      int      // 指定的列的数量
	ColsValuesIsMatch bool     // 列的数量是否和值的数量匹配
	RowsCount         int      // 一次insert行的数量
	// RowValueCounts 每行 VALUES 的值个数（与 RowsCount 等长；规则 insert_stmt_columns_match_values）
	RowValueCounts []int
	// FirstRowValues 第一行 VALUES 的值个数（无行时为 0）
	FirstRowValues   int
	DMLType          string
	DuplicateColumns []string // 字段列表中重复出现的列名（小写，已去重；规则 insert_stmt_col_name_no_dup）
	NullValueColumns []string // 被显式写入 NULL 值的列名（小写，已去重；仅指定了字段列表且行列数量对齐时可得）
	HasSysdateFunc   bool     // 是否使用了 SYSDATE() 函数（规则 insert_stmt_avoid_sysdate_func）
}

// insertTargetSchema 取 INSERT 目标表的库/schema 前缀（`INSERT INTO db.t` → db）。
//
// 表存在性判定要用它区分「当前库对象」与「跨库对象」：DESC 在连接库上执行，
// 对 db.t 直接 DESC `t` 会查错库、把存在的表误报成不存在。
func insertTargetSchema(stmt *ast.InsertStmt) string {
	if stmt == nil || stmt.Table == nil || stmt.Table.TableRefs == nil {
		return ""
	}
	join := stmt.Table.TableRefs
	if join == nil || join.Left == nil {
		return ""
	}
	source, ok := join.Left.(*ast.TableSource)
	if !ok {
		return ""
	}
	table, ok := source.Source.(*ast.TableName)
	if !ok {
		return ""
	}
	return strings.TrimSpace(table.Schema.O)
}

func (c *TraverseDMLInsertWithColumns) CheckSelectItem(node ast.ResultSetNode) {
	// 提取表名
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.Join:
		c.CheckSelectItem(n.Left)
		c.CheckSelectItem(n.Right)
	case *ast.TableSource:
		c.CheckSelectItem(n.Source)
	case *ast.TableName:
		c.Table = n.Name.String()
	}
}

func (c *TraverseDMLInsertWithColumns) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.InsertStmt:
		c.CheckSelectItem(stmt.Table.TableRefs)
		c.Schema = insertTargetSchema(stmt)
		c.IsMatch++
		c.DMLType = "INSERT"
		if stmt.IsReplace {
			c.DMLType = "REPLACE"
		}
		c.ColumnsCount = len(stmt.Columns)
		for _, item := range stmt.Columns {
			c.Columns = append(c.Columns, item.Name.O)
		}
		c.RowsCount = len(stmt.Lists)
		for _, row := range stmt.Lists {
			c.RowValueCounts = append(c.RowValueCounts, len(row))
		}
		if len(stmt.Lists) > 0 {
			c.FirstRowValues = len(stmt.Lists[0])
		}
		c.ColsValuesIsMatch = true
		for _, row := range stmt.Lists {
			if len(row) != c.ColumnsCount {
				c.ColsValuesIsMatch = false
				break
			}
		}
		// 重复字段：INSERT INTO t(a,a) VALUES(...) 属明确错误
		c.DuplicateColumns = duplicateLowerNames(c.Columns)
		// NULL 值列：只在「列名与值按位置一一对应」时才能定位到具体列
		if c.ColumnsCount > 0 && c.ColsValuesIsMatch {
			for _, row := range stmt.Lists {
				for i, expr := range row {
					if isNullLiteral(expr) {
						c.NullValueColumns = appendIfMissing(c.NullValueColumns, strings.ToLower(c.Columns[i]))
					}
				}
			}
		}
	case *ast.FuncCallExpr:
		// SYSDATE()：返回语句执行时刻的时间（非语句开始时间），主从/回放场景会不一致
		if c.IsMatch > 0 && strings.EqualFold(stmt.FnName.L, "sysdate") {
			c.HasSysdateFunc = true
		}
	}
	return in, false
}

// isNullLiteral 判断表达式是否为 NULL 字面量（IS NULL / 函数返回 NULL 不算）
func isNullLiteral(expr ast.ExprNode) bool {
	v, ok := expr.(ast.ValueExpr)
	return ok && v.GetValue() == nil
}

// duplicateLowerNames 返回列表中重复出现（忽略大小写）的名字，按首次重复的顺序去重
func duplicateLowerNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	var dup []string
	for _, n := range names {
		key := strings.ToLower(strings.TrimSpace(n))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			dup = appendIfMissing(dup, key)
			continue
		}
		seen[key] = struct{}{}
	}
	return dup
}

// appendIfMissing 去重追加（保持顺序稳定，便于告警文案可预期）
func appendIfMissing(list []string, item string) []string {
	for _, v := range list {
		if v == item {
			return list
		}
	}
	return append(list, item)
}

func (c *TraverseDMLInsertWithColumns) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLHasConstraint
type TraverseDMLHasConstraint struct {
	IsMatch     int    // 是否匹配当前规则
	DMLType     string // 语句类型，delete or update
	HasLimit    bool
	LimitCount  int // LIMIT 子句的常量行数（无法解析为常量时为 0）
	HasOrderBy  bool
	HasSubQuery bool
}

func (c *TraverseDMLHasConstraint) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		// DMLType 必须无条件赋值：告警文案依赖它区分 UPDATE/DELETE，
		// 历史实现只在「有 LIMIT」时赋值，导致无 LIMIT 的语句文案缺主语。
		c.DMLType = "DELETE"
		if stmt.Limit != nil {
			c.HasLimit = true
			c.LimitCount = limitRowCount(stmt.Limit)
		}
		if stmt.Order != nil {
			c.HasOrderBy = true
		}
		if stmt.Where != nil {
			check := process.CheckItem{}
			check.CheckExprItem(stmt.Where)
			mergeExprCheck(c, check)
		}
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
		if stmt.Limit != nil {
			c.HasLimit = true
			c.LimitCount = limitRowCount(stmt.Limit)
		}
		if stmt.Order != nil {
			c.HasOrderBy = true
		}
		// eg: update a set a.price = (subquery) where expr;
		for _, item := range stmt.List {
			if item.Expr != nil {
				check := process.CheckItem{}
				check.CheckExprItem(item.Expr)
				mergeExprCheck(c, check)
			}
		}
		// eg: update a set a.price=188 where a.id IN (subquery);
		if stmt.Where != nil {
			check := process.CheckItem{}
			check.CheckExprItem(stmt.Where)
			mergeExprCheck(c, check)
		}
	}
	return in, false
}

// mergeExprCheck 把「SET 项 / WHERE 表达式」的检查结果叠加进语句级标志。
//
// 历史 bug：这里曾是直接赋值（c.HasLimit = check.HasLimit），而子表达式检查通常检测不到
// 语句级 LIMIT/ORDER BY，于是带 SET 的 UPDATE、带 WHERE 的 DELETE 会把已识别到的
// 语句级标志覆盖成 false，导致「限制 LIMIT 数据量」「禁止 ORDER BY」等规则永不触发。
// 子表达式检查只做增量叠加（OR），绝不回退语句级结果。
func mergeExprCheck(c *TraverseDMLHasConstraint, check process.CheckItem) {
	c.HasLimit = c.HasLimit || check.HasLimit
	c.HasOrderBy = c.HasOrderBy || check.HasOrderBy
	c.HasSubQuery = c.HasSubQuery || check.HasSubQuery
}

func (c *TraverseDMLHasConstraint) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLJoinWithOn
type TraverseDMLJoinWithOn struct {
	DMLType      string
	IsMatch      int  // 是否匹配当前规则
	HasJoin      bool // 是否为join语句
	IsJoinWithOn bool // join是否有ON语句
}

func (c *TraverseDMLJoinWithOn) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
		// 检查JOIN操作是否有ON语句
		// eg: delete a from a join b on a.id = b.id where expr;
		if stmt.TableRefs.TableRefs.Tp > 0 {
			c.HasJoin = true
			if stmt.TableRefs.TableRefs.On != nil {
				c.IsJoinWithOn = true
			}
		}
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
		// 检查JOIN操作是否有ON语句
		// eg: update a join b on a.id = b.id set a.is_deleted=1 where b.id is null;
		if stmt.TableRefs.TableRefs.Tp > 0 {
			c.HasJoin = true
			if stmt.TableRefs.TableRefs.On != nil {
				c.IsJoinWithOn = true
			}
		}
	}
	return in, false
}

func (c *TraverseDMLJoinWithOn) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLMaxUpdateRows
type TraverseDMLMaxUpdateRows struct {
	IsMatch int // 是否匹配当前规则
	DMLType string
}

func (c *TraverseDMLMaxUpdateRows) Enter(in ast.Node) (ast.Node, bool) {
	switch in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
	}
	return in, false
}

func (c *TraverseDMLMaxUpdateRows) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLMaxInsertRows
type TraverseDMLMaxInsertRows struct {
	IsMatch   int // 是否匹配当前规则
	DMLType   string
	RowsCount int // 一次insert行的数量
}

func (c *TraverseDMLMaxInsertRows) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.InsertStmt:
		c.IsMatch++
		c.DMLType = "INSERT"
		c.RowsCount = len(stmt.Lists)
	}
	return in, false
}

func (c *TraverseDMLMaxInsertRows) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// limitRowCount 从 LIMIT 子句提取常量行数（非字面量/无法解析时返回 0）
func limitRowCount(limit *ast.Limit) int {
	if limit == nil || limit.Count == nil {
		return 0
	}
	if v, ok := limit.Count.(*driver.ValueExpr); ok {
		switch d := v.GetValue().(type) {
		case int64:
			if d > 0 {
				return int(d)
			}
		case uint64:
			if d > 0 && d <= uint64(^uint(0)>>1) {
				return int(d)
			}
		}
	}
	return 0
}

// TraverseDMLJoinTableCount 统计 UPDATE/DELETE 参与操作的表数量（含主表，子查询按 1 张计）
type TraverseDMLJoinTableCount struct {
	IsMatch    int
	DMLType    string
	TableCount int // 参与操作的表数量
}

func (c *TraverseDMLJoinTableCount) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
		c.TableCount = countDMLTableSources(stmt.TableRefs.TableRefs)
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
		c.TableCount = countDMLTableSources(stmt.TableRefs.TableRefs)
	}
	return in, false
}

func (c *TraverseDMLJoinTableCount) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// countDMLTableSources 递归统计 JOIN 树中的表源数量（每个 TableSource 计 1）
func countDMLTableSources(node ast.ResultSetNode) int {
	switch n := node.(type) {
	case nil:
		return 0
	case *ast.Join:
		return countDMLTableSources(n.Left) + countDMLTableSources(n.Right)
	case *ast.TableSource:
		return 1
	}
	return 0
}

// TraverseDMLUpdateSetColumns 提取 UPDATE 语句 SET 子句的列名（小写）
type TraverseDMLUpdateSetColumns struct {
	IsMatch      int
	DMLType      string
	SetColumns   []string
	Table        string       // 主表名（UPDATE 涉及的首个表源）；多表时 SET 项需按表前缀区分
	TableCount   int          // 参与 UPDATE 的表数量（含主表）
	NoPrefixCols []string     // 多表 UPDATE 时未指定表前缀的 SET 列名（小写，规则 update_multi_table_column_prefix_check）
	AndSepCols   []string     // SET 子句中疑似用 AND 分隔多列赋值的列名（小写，规则 update_stmt_set_separator_check）
	SetItems     []SetItem    // SET 项与其所属的表的对应关系（主键判定用）
	WhereExpr    ast.ExprNode // WHERE 表达式（WHERE 条件类规则用，nil 表示无 WHERE）
}

// SetItem UPDATE 语句中的一个 SET 赋值项
type SetItem struct {
	Table  string // 所属表名（SET 列带前缀时取前缀；单表且无前缀时取主表名；无法确定时为空）
	Column string // 列名（原始大小写）
}

func (c *TraverseDMLUpdateSetColumns) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
		c.Table = firstDMLTableName(stmt.TableRefs.TableRefs)
		c.TableCount = countDMLTableSources(stmt.TableRefs.TableRefs)
		c.WhereExpr = stmt.Where
		for _, item := range stmt.List {
			if item == nil || item.Column == nil {
				continue
			}
			col := strings.TrimSpace(item.Column.Name.O)
			if col != "" {
				c.SetColumns = append(c.SetColumns, strings.ToLower(col))
			}
			// 多表 UPDATE：`SET col=1` 无法判断属于哪张表，要求写成 `t.col=1`
			if c.TableCount > 1 && strings.TrimSpace(item.Column.Table.O) == "" && col != "" {
				c.NoPrefixCols = appendIfMissing(c.NoPrefixCols, strings.ToLower(col))
			}
			// SET 多个列之间使用 AND 分隔：`SET a=1 AND b=2` 会被解析成
			// a = (1 AND (b=2))，即 Expr 为 LogicAnd 且右操作数是另一个赋值表达式。
			// 合法的布尔表达式 `SET a = (b>1 AND c<2)` 外层是 ParenthesesExpr，不会命中。
			if isAndSeparatedAssignment(item.Expr) && col != "" {
				c.AndSepCols = appendIfMissing(c.AndSepCols, strings.ToLower(col))
			}
			// 记录 SET 项归属的表：带前缀用前缀，单表无前缀用主表，多表无前缀留空（无法判定，不参与主键检查）
			owner := strings.TrimSpace(item.Column.Table.O)
			if owner == "" && c.TableCount == 1 {
				owner = c.Table
			}
			if col != "" {
				c.SetItems = append(c.SetItems, SetItem{Table: owner, Column: col})
			}
		}
	}
	return in, false
}

// isAndSeparatedAssignment 判断 SET 的右值是否为「用 AND/OR/XOR 连接的另一个赋值」，
// 即把多个 `col = value` 用 AND 串起来写（MySQL 会静默按布尔表达式处理，结果非预期）。
func isAndSeparatedAssignment(expr ast.ExprNode) bool {
	bin, ok := expr.(*ast.BinaryOperationExpr)
	if !ok {
		return false
	}
	switch bin.Op {
	case opcode.LogicAnd, opcode.LogicOr, opcode.LogicXor:
	default:
		return false
	}
	right, ok := bin.R.(*ast.BinaryOperationExpr)
	if !ok || right.Op != opcode.EQ {
		return false
	}
	_, ok = right.L.(*ast.ColumnNameExpr)
	return ok
}

// dmlTableNames 递归收集 JOIN 树中的真实表名（去重，保持顺序）。
// 派生表 / 子查询（TableSource.Source 非 TableName）没有可判定的表名，跳过。
func dmlTableNames(node ast.ResultSetNode) []string {
	var names []string
	collectDMLTableNames(node, &names)
	return names
}

func collectDMLTableNames(node ast.ResultSetNode, names *[]string) {
	switch n := node.(type) {
	case nil:
		return
	case *ast.Join:
		collectDMLTableNames(n.Left, names)
		collectDMLTableNames(n.Right, names)
	case *ast.TableSource:
		collectDMLTableNames(n.Source, names)
	case *ast.TableName:
		name := strings.TrimSpace(n.Name.O)
		if name != "" {
			*names = appendIfMissing(*names, name)
		}
	}
}

// dmlTableAliasMap 递归收集 JOIN 树中的「引用名 → 真实表名」映射：
// 有别名的表以别名为键；无别名的表以自身表名为键（便于统一查表还原）。
func collectDMLTableAliasMap(node ast.ResultSetNode, m map[string]string) {
	switch n := node.(type) {
	case nil:
		return
	case *ast.Join:
		collectDMLTableAliasMap(n.Left, m)
		collectDMLTableAliasMap(n.Right, m)
	case *ast.TableSource:
		tn, ok := n.Source.(*ast.TableName)
		if !ok {
			return // 派生表/子查询没有可还原的表名
		}
		real := strings.TrimSpace(tn.Name.O)
		if real == "" {
			return
		}
		m[strings.ToLower(real)] = real
		if alias := strings.TrimSpace(n.AsName.O); alias != "" {
			m[strings.ToLower(alias)] = real
		}
	}
}

// firstDMLTableName 返回 JOIN 树中的首个表名（多表 UPDATE 时的「主表」）
func firstDMLTableName(node ast.ResultSetNode) string {
	switch n := node.(type) {
	case nil:
		return ""
	case *ast.Join:
		if name := firstDMLTableName(n.Left); name != "" {
			return name
		}
		return firstDMLTableName(n.Right)
	case *ast.TableSource:
		return firstDMLTableName(n.Source)
	case *ast.TableName:
		return n.Name.String()
	}
	return ""
}

func (c *TraverseDMLUpdateSetColumns) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLDeleteTables 采集 DELETE 语句涉及的表名（规则 delete_stmt_check_table_exists）
type TraverseDMLDeleteTables struct {
	IsMatch int
	DMLType string
	Tables  []string // 参与 DELETE 的真实表名（含 JOIN 的表，排除派生表/子查询）
}

func (c *TraverseDMLDeleteTables) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
		c.Tables = dmlTableNames(stmt.TableRefs.TableRefs)
		// `DELETE t1 FROM t1 JOIN t2 ...` 的目标表不在 TableRefs 中时补进来。
		// 注意目标列表里写的可能是**别名**：`DELETE rp FROM insight_role_permissions rp JOIN ...`
		// 中 parser 只拿到名字 "rp"（表别名），直接当表名查存在性必然误报
		// 「表`rp`不存在」。因此先在 FROM 表引用里按别名还原成真实表名；
		// 还原不到的才视为真实表名（`DELETE t1, t2 FROM t1 JOIN t2` 的常规写法）。
		if stmt.Tables != nil {
			aliases := map[string]string{}
			collectDMLTableAliasMap(stmt.TableRefs.TableRefs, aliases)
			for _, t := range stmt.Tables.Tables {
				if t == nil {
					continue
				}
				name := strings.TrimSpace(t.Name.O)
				if name == "" {
					continue
				}
				if real := aliases[strings.ToLower(name)]; real != "" {
					name = real
				}
				c.Tables = appendIfMissing(c.Tables, name)
			}
		}
	}
	return in, false
}

func (c *TraverseDMLDeleteTables) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLWhereConditions 采集 UPDATE/DELETE 的 WHERE 子句特征（规则 where_* 系列）
//
// 只分析当前语句的 WHERE 表达式，不深入子查询；表结构相关的判定（索引列运算、隐式类型转换）
// 由 logics 层在拿到表结构后调用 process.CollectWhereConditions 完成。
type TraverseDMLWhereConditions struct {
	IsMatch    int
	DMLType    string
	Table      string       // 主表名
	TableCount int          // 参与的表数量（含主表）
	WhereExpr  ast.ExprNode // WHERE 表达式（nil 表示无 WHERE）
}

func (c *TraverseDMLWhereConditions) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
		c.Table = firstDMLTableName(stmt.TableRefs.TableRefs)
		c.TableCount = countDMLTableSources(stmt.TableRefs.TableRefs)
		c.WhereExpr = stmt.Where
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
		c.Table = firstDMLTableName(stmt.TableRefs.TableRefs)
		c.TableCount = countDMLTableSources(stmt.TableRefs.TableRefs)
		c.WhereExpr = stmt.Where
	}
	// 跳过子节点：WHERE 内部的子查询条件不属于本语句
	return in, true
}

func (c *TraverseDMLWhereConditions) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDMLWhereInElements 统计 UPDATE/DELETE 的 WHERE/条件中 IN 列表的最大元素个数
// （遍历整棵语句树，PatternInExpr 均被访问；只统计 UPDATE/DELETE 语句上下文）
type TraverseDMLWhereInElements struct {
	IsMatch   int
	DMLType   string
	MaxInList int // IN 列表的最大元素个数（无 IN 时为 0）
}

func (c *TraverseDMLWhereInElements) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.DeleteStmt:
		c.IsMatch++
		c.DMLType = "DELETE"
	case *ast.UpdateStmt:
		c.IsMatch++
		c.DMLType = "UPDATE"
	case *ast.PatternInExpr:
		if c.IsMatch > 0 && len(stmt.List) > c.MaxInList {
			c.MaxInList = len(stmt.List)
		}
	}
	return in, false
}

func (c *TraverseDMLWhereInElements) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}
