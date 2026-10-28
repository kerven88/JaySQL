package process

// WHERE 条件类规则的表达式采集（规则 where_* 系列 + UPDATE 更新唯一键）。
//
// 设计要点：
//  1. 只分析**当前语句的 WHERE 子句**，不深入子查询（子查询内部的条件属于子查询自己的 WHERE）；
//  2. 依赖表结构的判定（索引列运算、隐式类型转换）在表结构不可用时静默跳过，不猜测、不误报；
//  3. 列带表前缀且前缀不是当前主表时，该列不参与依赖表结构的判定（无法确认归属）。

import (
	"regexp"
	"strings"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/format"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/opcode"
)

// TableColumnMeta 表的列元数据（由 SHOW CREATE TABLE 解析而来）
type TableColumnMeta struct {
	TableName  string              // 表名（小写）
	IndexCols  map[string]struct{} // 被索引覆盖的列（小写）：主键 + 唯一键 + 普通索引
	UniqueCols map[string]struct{} // 唯一索引（不含主键）覆盖的列（小写）
	ColTypes   map[string]byte     // 列名（小写）→ 类型字节码
}

// IsIndexCol 列是否被索引覆盖
func (m TableColumnMeta) IsIndexCol(col string) bool {
	if len(m.IndexCols) == 0 {
		return false
	}
	_, ok := m.IndexCols[strings.ToLower(col)]
	return ok
}

// IsUniqueCol 列是否属于唯一索引（不含主键）
func (m TableColumnMeta) IsUniqueCol(col string) bool {
	if len(m.UniqueCols) == 0 {
		return false
	}
	_, ok := m.UniqueCols[strings.ToLower(col)]
	return ok
}

// ColType 返回列的类型字节码
func (m TableColumnMeta) ColType(col string) (byte, bool) {
	tp, ok := m.ColTypes[strings.ToLower(col)]
	return tp, ok
}

// WhereConditions WHERE 子句中收集到的检查特征
type WhereConditions struct {
	LeadingWildcards []string // LIKE 模式以 `%` 或 `_` 开头（前通配符查找，索引失效）
	NoWildcardLikes  []string // LIKE 模式中没有通配符（应改用 `=`）
	NotInExprs       []string // NOT IN 表达式
	NotLikeExprs     []string // NOT LIKE 表达式
	OrConditions     []string // 用 OR 连接过滤条件的表达式
	IndexColCalcs    []string // 索引列参与数学运算或函数运算的表达式
	ImplicitCasts    []string // 存在隐式类型转换的「列 = 值」比较
}

// CollectWhereConditions 收集 WHERE 表达式中的检查特征
func CollectWhereConditions(expr ast.ExprNode, meta TableColumnMeta) *WhereConditions {
	c := &whereCollector{meta: meta, info: &WhereConditions{}}
	c.walk(expr)
	return c.info
}

type whereCollector struct {
	meta TableColumnMeta
	info *WhereConditions
}

func (c *whereCollector) walk(node ast.ExprNode) {
	switch e := node.(type) {
	case nil:
		return
	case *ast.BinaryOperationExpr:
		c.walkBinary(e)
	case *ast.FuncCallExpr:
		c.checkFuncWrappedCols(node)
		for _, arg := range e.Args {
			c.walk(arg)
		}
	case *ast.PatternLikeOrIlikeExpr:
		c.checkLike(node, e.Pattern, e.Not)
		c.walk(e.Expr)
	case *ast.PatternInExpr:
		if e.Not {
			c.info.NotInExprs = appendUnique(c.info.NotInExprs, exprText(e))
		}
		c.walk(e.Expr)
		// e.Sel 是子查询，不深入
	case *ast.BetweenExpr:
		c.checkImplicitCast(e.Expr, e.Left)
		c.checkImplicitCast(e.Expr, e.Right)
		c.walk(e.Expr)
		c.walk(e.Left)
		c.walk(e.Right)
	case *ast.ParenthesesExpr:
		c.walk(e.Expr)
	case *ast.UnaryOperationExpr:
		c.walk(e.V)
	case *ast.IsNullExpr:
		c.walk(e.Expr)
	case *ast.IsTruthExpr:
		c.walk(e.Expr)
	case *ast.CaseExpr:
		c.walk(e.Value)
		c.walk(e.ElseClause)
		for _, when := range e.WhenClauses {
			c.walk(when.Expr)
			c.walk(when.Result)
		}
	case *ast.PatternRegexpExpr:
		c.walk(e.Expr)
		// Pattern 是正则，不属于 LIKE 系列规则
	}
	// ColumnNameExpr / ValueExpr / SubqueryExpr 等叶子节点无需处理
}

func (c *whereCollector) walkBinary(e *ast.BinaryOperationExpr) {
	switch e.Op {
	case opcode.LogicOr:
		// OR 连接过滤条件：**任意一侧引用了列**即告警。
		//
		// 早期实现要求「两侧都引用列」，导致 `WHERE id = 1 OR 1 = 1` 这类最危险的写法漏检——
		// 常量恒真条件与列条件做 OR，等价于没有 WHERE（整表更新/删除），恰恰是最该拦的。
		// 只有两侧都是纯常量（如 `1 = 1 OR 2 = 2`，常见于拼接 SQL 的占位写法）才不告警。
		if containsColumn(e.L) || containsColumn(e.R) {
			c.info.OrConditions = appendUnique(c.info.OrConditions, exprText(e))
		}
	case opcode.LogicAnd, opcode.LogicXor:
		// 逻辑连接符，只做下钻
	default:
		if isArithmeticOp(e.Op) {
			c.checkWrappedIndexCols(e)
		} else if isCompareOp(e.Op) {
			c.checkImplicitCast(e.L, e.R)
			c.checkImplicitCast(e.R, e.L)
		}
	}
	c.walk(e.L)
	c.walk(e.R)
}

// checkLike 判定 LIKE 模式：前通配符 / 无通配符 / NOT LIKE
func (c *whereCollector) checkLike(node, pattern ast.ExprNode, not bool) {
	if !likeIsLike(node) {
		return
	}
	text, ok := likePatternText(pattern)
	if !ok {
		return
	}
	if not {
		c.info.NotLikeExprs = appendUnique(c.info.NotLikeExprs, exprText(node))
	}
	if strings.HasPrefix(text, "%") || strings.HasPrefix(text, "_") {
		c.info.LeadingWildcards = appendUnique(c.info.LeadingWildcards, text)
		return
	}
	// 「无通配符」只把 `%` 视为有效通配符。
	//
	// `_` 在 SQL 里确实是单字符通配符，但业务 SQL 中绝大多数是**字面下划线**
	// （如 `LIKE 'User_1'`），其真实语义就是 `=`。若把 `_` 也算通配符，这条规则在真实
	// 库表上几乎永远不会命中，形同虚设。转义写法（`\%` / `\_`）表示字面量，判定前先剥离。
	if !strings.Contains(stripLikeEscapes(text), "%") {
		c.info.NoWildcardLikes = appendUnique(c.info.NoWildcardLikes, text)
	}
}

// stripLikeEscapes 去掉 LIKE 模式中的转义序列（`\%` / `\_` / `\\` 整体移除），
// 只留下「真正起通配作用」的字符。
//
// 转义后的字符是字面量，不能算通配符：`LIKE 'User\%1'` 与 `LIKE 'User\_1'` 都是精确匹配，
// 应按「无通配符」提示改用 `=`；而 `LIKE 'User\\%1'` 里 `\\` 是字面反斜杠、后面的 `%` 仍是通配符。
// MySQL 默认转义符为 `\`，也支持 `ESCAPE` 子句（此处不感知，退化为按默认转义符处理）。
func stripLikeEscapes(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			i++ // 跳过被转义的字符本身
			continue
		}
		sb.WriteByte(text[i])
	}
	return sb.String()
}

// checkFuncWrappedCols 列被函数包裹（如 `DATE(created_at) = '2023-10-01'`）：该列上的索引必然失效。
//
// 与「算术运算」的判定不同，函数包裹**不依赖索引元数据**——只要列出现在函数参数中就告警：
//  1. 表结构读不到时（PostgreSQL 无 SHOW CREATE TABLE、表不存在、临时表）无从判断该列是否有索引，
//     若强要求「必须是索引列」，规则在这些场景会整体静默，用户看到的就是"配了不生效"；
//  2. 把列写进函数本身就是不可索引的写法，与它当前有没有索引无关（将来加索引也一样失效）。
func (c *whereCollector) checkFuncWrappedCols(node ast.ExprNode) {
	for range columnsIn(node) {
		c.info.IndexColCalcs = appendUnique(c.info.IndexColCalcs, exprText(node))
		break
	}
}

// checkWrappedIndexCols 索引列参与**数学运算**（如 `id + 1 = 2`）：索引失效。
//
// 数学运算只针对索引列告警：非索引列上的算术运算（如 `status + 1 = 2`，status 无索引）
// 本来就不会走索引，告警无意义且会放大误报面。表结构不可读时同样静默跳过。
func (c *whereCollector) checkWrappedIndexCols(node ast.ExprNode) {
	for _, col := range columnsIn(node) {
		if !c.meta.IsIndexCol(col) {
			continue
		}
		c.info.IndexColCalcs = appendUnique(c.info.IndexColCalcs, exprText(node))
		break
	}
}

// checkImplicitCast 列与常量的类型不匹配（隐式类型转换，索引失效）
func (c *whereCollector) checkImplicitCast(left, right ast.ExprNode) {
	colExpr, ok := left.(*ast.ColumnNameExpr)
	if !ok {
		return
	}
	valExpr, ok := right.(ast.ValueExpr)
	if !ok {
		return
	}
	// 列带表前缀且不是当前主表时无法确认归属，跳过（不误报）
	if t := strings.ToLower(strings.TrimSpace(colExpr.Name.Table.L)); t != "" && t != c.meta.TableName {
		return
	}
	colType, ok := c.meta.ColType(colExpr.Name.Name.L)
	if !ok {
		return
	}
	valKind := literalKind(valExpr)
	if valKind == "" || valKind == "null" {
		return
	}
	desc := ""
	switch {
	case isStringType(colType) && valKind == "number":
		desc = "字符类型的列与数字常量比较"
	case isNumericType(colType) && valKind == "string":
		desc = "数字类型的列与字符串常量比较"
	}
	if desc == "" {
		return
	}
	msg := colExpr.Name.Name.O + "：" + desc
	c.info.ImplicitCasts = appendUnique(c.info.ImplicitCasts, msg)
}

// literalKind 常量字面量的种类：string / number / null / ""（时间、二进制等不参与判定）。
//
// 注意：tidb 的 ast.ValueExpr **不暴露字段类型**（没有 GetType 方法），只能按 GetValue()
// 返回的 Go 类型判断。'1' 解析为 string、1 解析为 int64，正好覆盖「列与值类型不一致」这个场景。
func literalKind(v ast.ValueExpr) string {
	switch val := v.GetValue().(type) {
	case nil:
		return "null"
	case string, []byte:
		return "string"
	case int64, uint64, int, int32, float32, float64:
		return "number"
	default:
		_ = val
		return ""
	}
}

func isArithmeticOp(op opcode.Op) bool {
	switch op {
	case opcode.Plus, opcode.Minus, opcode.Mul, opcode.Div, opcode.Mod, opcode.IntDiv:
		return true
	}
	return false
}

func isCompareOp(op opcode.Op) bool {
	switch op {
	case opcode.EQ, opcode.NE, opcode.GT, opcode.GE, opcode.LT, opcode.LE, opcode.NullEQ:
		return true
	}
	return false
}

// isStringType 字符类型（varchar/char/text/blob 等）
func isStringType(tp byte) bool {
	switch tp {
	case mysql.TypeVarchar, mysql.TypeString, mysql.TypeVarString,
		mysql.TypeTinyBlob, mysql.TypeBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob,
		mysql.TypeEnum, mysql.TypeSet:
		return true
	}
	return false
}

// isNumericType 数值类型
func isNumericType(tp byte) bool {
	switch tp {
	case mysql.TypeTiny, mysql.TypeShort, mysql.TypeLong, mysql.TypeLonglong,
		mysql.TypeInt24, mysql.TypeNewDecimal, mysql.TypeFloat, mysql.TypeDouble:
		return true
	}
	return false
}

// likePatternText 取 LIKE 模式的字面量文本
func likePatternText(pattern ast.ExprNode) (string, bool) {
	if pattern == nil {
		return "", false
	}
	v, ok := pattern.(ast.ValueExpr)
	if !ok {
		return "", false
	}
	s := v.GetString()
	return s, true
}

// columnsIn 递归收集表达式中引用的列名（小写，不深入子查询）
func columnsIn(node ast.ExprNode) []string {
	var cols []string
	collectColumns(node, &cols)
	return cols
}

func collectColumns(node ast.ExprNode, cols *[]string) {
	switch e := node.(type) {
	case nil:
		return
	case *ast.ColumnNameExpr:
		*cols = appendUnique(*cols, strings.ToLower(e.Name.Name.L))
	case *ast.BinaryOperationExpr:
		collectColumns(e.L, cols)
		collectColumns(e.R, cols)
	case *ast.FuncCallExpr:
		for _, arg := range e.Args {
			collectColumns(arg, cols)
		}
	case *ast.ParenthesesExpr:
		collectColumns(e.Expr, cols)
	case *ast.UnaryOperationExpr:
		collectColumns(e.V, cols)
	case *ast.PatternLikeOrIlikeExpr:
		collectColumns(e.Expr, cols)
	case *ast.PatternInExpr:
		collectColumns(e.Expr, cols)
	case *ast.BetweenExpr:
		collectColumns(e.Expr, cols)
		collectColumns(e.Left, cols)
		collectColumns(e.Right, cols)
	}
}

// containsColumn 表达式中是否引用了列
func containsColumn(node ast.ExprNode) bool {
	return len(columnsIn(node)) > 0
}

// likeIsLike 区分 LIKE 与 ILIKE（PatternLikeOrIlikeExpr 复用同一类型）
func likeIsLike(node ast.ExprNode) bool {
	e, ok := node.(*ast.PatternLikeOrIlikeExpr)
	if !ok {
		return false
	}
	return e.IsLike
}

// exprText 还原表达式文本（告警文案用，失败时返回空串）
func exprText(node ast.ExprNode) string {
	if node == nil {
		return ""
	}
	var sb strings.Builder
	ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)
	if err := node.Restore(ctx); err != nil {
		return ""
	}
	return cleanExprText(sb.String())
}

// charsetPrefixRegexp 常量字符串的字符集前缀（`_UTF8MB4'a'`），告警文案里去掉以提升可读性
var charsetPrefixRegexp = regexp.MustCompile(`(?i)_(utf8mb4|utf8|binary|latin1|gbk|ascii)'`)

// cleanExprText 去掉 Restore 输出中的字符集前缀与多余空白
func cleanExprText(text string) string {
	text = charsetPrefixRegexp.ReplaceAllString(text, "'")
	return strings.TrimSpace(text)
}

// appendUnique 去重追加（保持顺序稳定）
func appendUnique(list []string, item string) []string {
	if item == "" {
		return list
	}
	for _, v := range list {
		if v == item {
			return list
		}
	}
	return append(list, item)
}
