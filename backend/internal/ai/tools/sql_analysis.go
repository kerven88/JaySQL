package tools

// ---------------------------------------------------------------------------
// SQL 类工具的纯函数层：解析、解释、索引分析、执行计划解读
//
// 这一层刻意不碰数据库、不连目标实例，也不做权限判定：
//   - 可单测（喂一段 SQL / 一段 DDL 就能断言输出）；
//   - 权限判定在 data_scope.go 统一收口，解析失败不会把「跳过鉴权」变成默认行为。
// ---------------------------------------------------------------------------

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/format"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"

	"github.com/simajiong/jaysql/internal/das/parser"
)

// ------------------------------------------------------------------
// 基础解析
// ------------------------------------------------------------------

// exprText 还原 AST 节点成 SQL 文本（还原失败返回空串，不把错误往上抛）
func exprText(node ast.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &builder)
	if err := node.Restore(ctx); err != nil {
		return ""
	}
	return strings.TrimSpace(builder.String())
}

// parseSingleStatement 解析单条 SQL，返回 AST 节点。
// 一次只解释一条：多条语句的表清单、执行计划都说不清，不如让模型拆开问。
func parseSingleStatement(sqlText string) (ast.StmtNode, error) {
	if strings.TrimSpace(sqlText) == "" {
		return nil, fmt.Errorf("SQL 不能为空")
	}
	stmt, _, err := parser.NewParse(sqlText, "", "")
	if err != nil {
		return nil, fmt.Errorf("SQL 语法解析失败：%s", strings.TrimSpace(err.Error()))
	}
	if stmt == nil || len(stmt.Stmts) == 0 {
		return nil, fmt.Errorf("未能从输入中解析出 SQL 语句")
	}
	if len(stmt.Stmts) > 1 {
		return nil, fmt.Errorf("一次只能解释一条语句，当前输入包含 %d 条", len(stmt.Stmts))
	}
	return stmt.Stmts[0], nil
}

// extractSQLTables 从 SQL 里提取涉及的库表。
// 解析失败时 ok=false：调用方必须退化为「按库级鉴权」，而不是跳过鉴权。
func extractSQLTables(sqlText, defaultSchema string) ([]parser.Table, bool) {
	stmt, _, err := parser.NewParse(sqlText, "", "")
	if err != nil || stmt == nil || len(stmt.Stmts) == 0 {
		return nil, false
	}
	out := make([]parser.Table, 0)
	seen := map[string]struct{}{}
	for _, node := range stmt.Stmts {
		extracter := parser.Extracter{Stmt: node, Schema: defaultSchema}
		for _, item := range extracter.Run() {
			key := strings.ToLower(item.Schema) + "|" + strings.ToLower(item.Table)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	return out, true
}

// statementTypeOf 取语句类型（SELECT / UPDATE / INSERT / DELETE / UNION …），解析失败返回空串
func statementTypeOf(sqlText string) string {
	stmt, _, err := parser.NewParse(sqlText, "", "")
	if err != nil || stmt == nil || len(stmt.Stmts) == 0 {
		return ""
	}
	extractor := parser.StatementType{}
	return extractor.Extract(stmt.Stmts[0])
}

// qualifySQLName 拼「库.表」（库名为空时只给表名）
func qualifySQLName(schema, table string) string {
	if strings.TrimSpace(schema) == "" {
		return table
	}
	return schema + "." + table
}

// ------------------------------------------------------------------
// SQL 解释
// ------------------------------------------------------------------

// interpretSQL 把一条 SQL 解释成结构化说明（纯解析，不查库、不执行）。
//
// 输出定位是「给模型当草稿、给用户当摘要」：字段能拿就给，拿不到就留空，
// 绝不为了凑字段去猜；读不懂的语句类型明确回一句「暂不支持解释」。
func interpretSQL(sqlText, defaultSchema string) (map[string]any, error) {
	node, err := parseSingleStatement(sqlText)
	if err != nil {
		return nil, err
	}
	extractor := parser.StatementType{}
	kind := extractor.Extract(node)

	out := map[string]any{"statement_type": kind}
	tables, _ := extractSQLTables(sqlText, defaultSchema)
	tableNames := make([]string, 0, len(tables))
	for _, item := range tables {
		tableNames = append(tableNames, qualifySQLName(item.Schema, item.Table))
	}
	sort.Strings(tableNames)
	if len(tableNames) > 0 {
		out["tables"] = tableNames
	}

	notes := make([]string, 0)
	switch stmt := node.(type) {
	case *ast.SelectStmt:
		notes = describeSelectStmt(node, stmt, out, notes)
	case *ast.SetOprStmt:
		describeSetOprStmt(node, stmt, out, notes)
	case *ast.InsertStmt:
		describeInsertStmt(stmt, out, notes)
	case *ast.UpdateStmt:
		describeUpdateStmt(stmt, out, notes)
	case *ast.DeleteStmt:
		describeDeleteStmt(stmt, out, notes)
	default:
		out["hint"] = fmt.Sprintf("暂不支持解释 %s 类型的语句，当前支持 SELECT / UNION / INSERT / UPDATE / DELETE", kind)
	}

	if len(notes) > 0 {
		out["risk_notes"] = notes
	}
	out["summary"] = summarizeInterpretation(kind, tableNames, out)
	return out, nil
}

func describeSelectStmt(root ast.Node, stmt *ast.SelectStmt, out map[string]any, notes []string) []string {
	if stmt.Fields != nil {
		columns := make([]string, 0, len(stmt.Fields.Fields))
		wildcard := false
		for _, field := range stmt.Fields.Fields {
			if field.WildCard != nil {
				wildcard = true
				columns = append(columns, "*")
				continue
			}
			text := exprText(field.Expr)
			if field.AsName.O != "" {
				text += " AS " + field.AsName.O
			}
			if text != "" {
				columns = append(columns, text)
			}
		}
		if len(columns) > 0 {
			out["columns"] = columns
		}
		if wildcard {
			notes = append(notes, "查询使用了 SELECT *，建议改为明确列出需要的字段")
		}
	}
	if stmt.Distinct {
		out["distinct"] = true
		notes = append(notes, "使用了 DISTINCT，可能带来额外的去重排序开销")
	}
	if stmt.Where != nil {
		if text := exprText(stmt.Where); text != "" {
			out["where"] = text
		}
	} else {
		notes = append(notes, "查询没有 WHERE 条件，可能触发全表扫描")
	}
	if stmt.GroupBy != nil {
		if items := byItemTexts(stmt.GroupBy.Items); len(items) > 0 {
			out["group_by"] = items
		}
	}
	if stmt.Having != nil {
		if text := exprText(stmt.Having.Expr); text != "" {
			out["having"] = text
		}
	}
	if stmt.OrderBy != nil {
		if items := byItemTexts(stmt.OrderBy.Items); len(items) > 0 {
			out["order_by"] = items
		}
	}
	if stmt.Limit != nil {
		out["limit"] = limitText(stmt.Limit)
	} else {
		notes = append(notes, "查询没有 LIMIT，结果集可能很大")
	}
	if joins := collectJoins(stmt.From); len(joins) > 0 {
		out["joins"] = joins
	}
	if count := countSelectNodes(root) - 1; count > 0 {
		out["subquery_count"] = count
	}
	return notes
}

func describeSetOprStmt(root ast.Node, stmt *ast.SetOprStmt, out map[string]any, notes []string) {
	if stmt.SelectList != nil {
		out["branch_count"] = len(stmt.SelectList.Selects)
	}
	if stmt.OrderBy != nil {
		if items := byItemTexts(stmt.OrderBy.Items); len(items) > 0 {
			out["order_by"] = items
		}
	}
	if stmt.Limit != nil {
		out["limit"] = limitText(stmt.Limit)
	}
	// UNION 的每个分支单独统计列与条件意义不大，这里只给分支数与整体约束
	notes = append(notes, "这是一条集合运算语句（UNION 类），逐分支的字段与条件请拆开单独解释")
	_ = root
}

func describeInsertStmt(stmt *ast.InsertStmt, out map[string]any, notes []string) {
	columns := make([]string, 0, len(stmt.Columns))
	for _, column := range stmt.Columns {
		columns = append(columns, column.Name.O)
	}
	if len(columns) > 0 {
		out["columns"] = columns
	}
	if stmt.IsReplace {
		out["replace"] = true
	}
	if len(stmt.Lists) > 0 {
		out["value_rows"] = len(stmt.Lists)
	}
	if stmt.Select != nil {
		out["insert_from_select"] = true
	}
	if len(stmt.Lists) == 0 && stmt.Select == nil {
		notes = append(notes, "未解析到待写入的数据行，可能是语法较为特殊")
	}
	notes = append(notes, "这是写入语句：执行前请确认影响范围，平台侧需走工单与审批，AI 不会代为执行")
}

func describeUpdateStmt(stmt *ast.UpdateStmt, out map[string]any, notes []string) {
	assignments := make([]string, 0, len(stmt.List))
	for _, item := range stmt.List {
		assignments = append(assignments, fmt.Sprintf("%s = %s", exprText(item.Column), exprText(item.Expr)))
	}
	if len(assignments) > 0 {
		out["set"] = assignments
	}
	if stmt.Where != nil {
		if text := exprText(stmt.Where); text != "" {
			out["where"] = text
		}
	} else {
		notes = append(notes, "UPDATE 没有 WHERE 条件，会更新整表，风险极高")
	}
	if stmt.Limit != nil {
		out["limit"] = limitText(stmt.Limit)
	}
	notes = append(notes, "这是写入语句：执行前请确认影响范围，平台侧需走工单与审批，AI 不会代为执行")
}

func describeDeleteStmt(stmt *ast.DeleteStmt, out map[string]any, notes []string) {
	if stmt.Where != nil {
		if text := exprText(stmt.Where); text != "" {
			out["where"] = text
		}
	} else {
		notes = append(notes, "DELETE 没有 WHERE 条件，会清空整表，风险极高")
	}
	if stmt.Limit != nil {
		out["limit"] = limitText(stmt.Limit)
	}
	notes = append(notes, "这是写入语句：执行前请确认影响范围，平台侧需走工单与审批，AI 不会代为执行")
}

// summarizeInterpretation 一句话摘要：把结构化字段串成「人能读」的说明
func summarizeInterpretation(kind string, tables []string, out map[string]any) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("这是一条 %s 语句", kind))
	if len(tables) > 0 {
		builder.WriteString("，涉及对象：" + strings.Join(tables, "、"))
	}
	if joins, ok := out["joins"].([]map[string]any); ok && len(joins) > 0 {
		builder.WriteString(fmt.Sprintf("，包含 %d 个关联（JOIN）", len(joins)))
	}
	if _, ok := out["where"]; ok {
		builder.WriteString("，带 WHERE 过滤条件")
	}
	if items, ok := out["group_by"].([]string); ok && len(items) > 0 {
		builder.WriteString("，按 " + strings.Join(items, "、") + " 分组")
	}
	if items, ok := out["order_by"].([]string); ok && len(items) > 0 {
		builder.WriteString("，按 " + strings.Join(items, "、") + " 排序")
	}
	if limit, ok := out["limit"].(string); ok && limit != "" {
		builder.WriteString("，最多返回 " + limit + " 行")
	}
	if count, ok := out["subquery_count"].(int); ok && count > 0 {
		builder.WriteString(fmt.Sprintf("，含 %d 个子查询", count))
	}
	builder.WriteString("。")
	return builder.String()
}

// ------------------------------------------------------------------
// AST 片段提取
// ------------------------------------------------------------------

func byItemTexts(items []*ast.ByItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		text := exprText(item.Expr)
		if text == "" {
			continue
		}
		if item.Desc {
			text += " DESC"
		}
		out = append(out, text)
	}
	return out
}

func limitText(limit *ast.Limit) string {
	if limit == nil {
		return ""
	}
	text := exprText(limit.Count)
	if offset := exprText(limit.Offset); offset != "" {
		text = offset + " OFFSET, " + text
	}
	return text
}

// joinTypeName TiDB 的 JoinType 只有三种，INNER JOIN 与 CROSS JOIN 都落在 CrossJoin 上，
// 靠是否带 ON/USING 区分「等值内连接」和「笛卡尔积」
func joinTypeName(tp ast.JoinType, hasCondition bool) string {
	switch tp {
	case ast.LeftJoin:
		return "LEFT JOIN"
	case ast.RightJoin:
		return "RIGHT JOIN"
	default:
		if hasCondition {
			return "INNER JOIN"
		}
		return "CROSS JOIN（笛卡尔积，缺少关联条件）"
	}
}

func collectJoins(from *ast.TableRefsClause) []map[string]any {
	if from == nil || from.TableRefs == nil {
		return nil
	}
	return collectJoinsInNode(from.TableRefs)
}

func collectJoinsInNode(node ast.ResultSetNode) []map[string]any {
	join, ok := node.(*ast.Join)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, 2)
	out = append(out, collectJoinsInNode(join.Left)...)
	if join.Right != nil {
		out = append(out, collectJoinsInNode(join.Right)...)
	}
	if join.On == nil && len(join.Using) == 0 {
		// 没有关联条件的 Join：只在显式写了 JOIN 关键字时才算一条记录，
		// 否则逗号连接（FROM a, b）也会被算进来
		if join.Tp != ast.CrossJoin {
			return out
		}
		return append(out, map[string]any{"type": joinTypeName(join.Tp, false)})
	}
	item := map[string]any{"type": joinTypeName(join.Tp, true)}
	if join.On != nil {
		if text := exprText(join.On.Expr); text != "" {
			item["condition"] = text
		}
	}
	if len(join.Using) > 0 {
		names := make([]string, 0, len(join.Using))
		for _, column := range join.Using {
			names = append(names, column.Name.O)
		}
		item["using"] = names
	}
	return append(out, item)
}

// selectCounter 统计语句里 SELECT 块的数量（顶层 + 子查询）
type selectCounter struct{ count int }

func (c *selectCounter) Enter(node ast.Node) (ast.Node, bool) {
	if _, ok := node.(*ast.SelectStmt); ok {
		c.count++
	}
	return node, false
}

func (c *selectCounter) Leave(node ast.Node) (ast.Node, bool) { return node, true }

func countSelectNodes(root ast.Node) int {
	if root == nil {
		return 0
	}
	counter := &selectCounter{}
	root.Accept(counter)
	return counter.count
}

// ------------------------------------------------------------------
// 建表语句解析（列与索引）
// ------------------------------------------------------------------

type tableColumn struct {
	Name string
	Type string
}

type tableIndex struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
}

// indexDefinitionPatterns 索引定义子句的识别前缀（MySQL 系 DDL）。
// 注：Go 的 regexp 是 RE2，不支持反向引用，引号的配对只能写成两侧各自可选。
var (
	primaryKeyPattern = regexp.MustCompile(`(?i)^\s*PRIMARY\s+KEY\s*\((.*?)\)`)
	uniqueKeyPattern  = regexp.MustCompile("(?i)^\\s*UNIQUE\\s+(?:KEY|INDEX)\\s+[`\"']?([\\w$]+)[`\"']?\\s*\\((.*?)\\)")
	normalKeyPattern  = regexp.MustCompile("(?i)^\\s*(?:KEY|INDEX)\\s+[`\"']?([\\w$]+)[`\"']?\\s*\\((.*?)\\)")
	fulltextPattern   = regexp.MustCompile("(?i)^\\s*FULLTEXT\\s+(?:KEY|INDEX)\\s+[`\"']?([\\w$]+)")
	ddlClausePrefix   = regexp.MustCompile(`(?i)^\s*(PRIMARY|UNIQUE|KEY|INDEX|FULLTEXT|CONSTRAINT|FOREIGN|CHECK|SPATIAL|PERIOD|PARTITION)\b`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
)

// parseCreateTable 解析建表语句，拆出列定义与索引定义。
//
// 只做「够用就好」的文本解析：按顶层括号深度切分，列与索引分别识别。
// 解析不出索引时返回空列表 —— 调用方据此提示「无法确认已有索引」，
// 而不是假装表上没有索引，那样会给出一堆重复建索引的错误建议。
func parseCreateTable(ddl string) ([]tableColumn, []tableIndex) {
	body := topLevelParenBody(ddl)
	if body == "" {
		return nil, nil
	}
	columns := make([]tableColumn, 0)
	indexes := make([]tableIndex, 0)
	for _, segment := range splitTopLevel(body) {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		if ddlClausePrefix.MatchString(segment) {
			if index := parseIndexClause(segment); index != nil {
				indexes = append(indexes, *index)
			}
			continue
		}
		if column := parseColumnClause(segment); column != nil {
			columns = append(columns, *column)
		}
	}
	return columns, indexes
}

// parseColumnClause 解析列定义：第一个标识符是列名，其后到行尾是类型与属性
func parseColumnClause(segment string) *tableColumn {
	name := ""
	rest := strings.TrimSpace(segment)
	if strings.HasPrefix(rest, "`") {
		end := strings.Index(rest[1:], "`")
		if end < 0 {
			return nil
		}
		name = rest[1 : end+1]
		rest = strings.TrimSpace(rest[end+2:])
	} else if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return nil
		}
		name = rest[1 : end+1]
		rest = strings.TrimSpace(rest[end+2:])
	} else {
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil
		}
		name = fields[0]
		rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
	}
	if !identifierPattern.MatchString(name) {
		return nil
	}
	// 类型取到第一个「列属性关键字」为止，避免把 COMMENT 'xxx' 也算进类型
	if stop := columnAttributePattern.FindStringIndex(rest); stop != nil {
		rest = rest[:stop[0]]
	}
	return &tableColumn{Name: name, Type: strings.TrimSpace(rest)}
}

var columnAttributePattern = regexp.MustCompile(
	`(?i)\b(NOT\s+NULL|NULL|DEFAULT|COMMENT|COLLATE|GENERATED|VIRTUAL|STORED|REFERENCES|CHECK|ON\s+UPDATE|PRIMARY|UNIQUE|AUTO_INCREMENT|IDENTITY)\b`)

func parseIndexClause(segment string) *tableIndex {
	if match := primaryKeyPattern.FindStringSubmatch(segment); len(match) > 1 {
		return &tableIndex{
			Name:    "PRIMARY",
			Columns: parseIndexColumns(match[1]),
			Unique:  true,
			Primary: true,
		}
	}
	if match := uniqueKeyPattern.FindStringSubmatch(segment); len(match) > 2 {
		return &tableIndex{Name: match[1], Columns: parseIndexColumns(match[2]), Unique: true}
	}
	if match := normalKeyPattern.FindStringSubmatch(segment); len(match) > 2 {
		return &tableIndex{Name: match[1], Columns: parseIndexColumns(match[2])}
	}
	if match := fulltextPattern.FindStringSubmatch(segment); len(match) > 1 {
		return &tableIndex{Name: match[1], Columns: nil}
	}
	return nil
}

// parseIndexColumns 解析索引列列表：`a`,`b` ASC → [a, b]；前缀索引 `c`(32) 只保留列名
var (
	indexDirectionPattern    = regexp.MustCompile(`(?i)\s+(ASC|DESC)\s*$`)
	indexPrefixLengthPattern = regexp.MustCompile(`\(\s*\d+\s*\)?\s*$`)
)

func parseIndexColumns(raw string) []string {
	out := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// 去掉 ASC/DESC 与前缀长度（`col`(32) 与 col(32) 两种写法都要能剥掉）
		part = indexDirectionPattern.ReplaceAllString(part, "")
		part = indexPrefixLengthPattern.ReplaceAllString(part, "")
		part = strings.Trim(part, "`\"' ")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// topLevelParenBody 取建表语句最外层括号里的内容
func topLevelParenBody(ddl string) string {
	start := strings.Index(ddl, "(")
	if start < 0 {
		return ""
	}
	depth := 0
	for index := start; index < len(ddl); index++ {
		switch ddl[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return ddl[start+1 : index]
			}
		}
	}
	return ""
}

// splitTopLevel 按逗号切分，跳过括号内的逗号（decimal(10,2) 这类类型）
func splitTopLevel(body string) []string {
	out := make([]string, 0)
	depth := 0
	last := 0
	for index := 0; index < len(body); index++ {
		switch body[index] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, body[last:index])
				last = index + 1
			}
		}
	}
	out = append(out, body[last:])
	return out
}

// ------------------------------------------------------------------
// 索引推荐
// ------------------------------------------------------------------

// sqlExpressionKeywords 从表达式里挑列名时要排除的 SQL 关键字与常见函数名
var sqlExpressionKeywords = map[string]struct{}{
	"and": {}, "or": {}, "not": {}, "in": {}, "is": {}, "null": {}, "like": {}, "between": {},
	"select": {}, "from": {}, "where": {}, "order": {}, "group": {}, "by": {}, "as": {},
	"asc": {}, "desc": {}, "on": {}, "join": {}, "inner": {}, "left": {}, "right": {}, "union": {},
	"count": {}, "sum": {}, "avg": {}, "max": {}, "min": {}, "distinct": {}, "case": {}, "when": {},
	"then": {}, "else": {}, "end": {}, "interval": {}, "date": {}, "now": {}, "curdate": {},
}

// columnsInExpr 从表达式文本里挑出「确实属于该表」的列名。
// 先取标识符再与真实列做交集：宁可少推荐，也不要拿表达式里的字符串常量当列名。
func columnsInExpr(text string, columns []tableColumn) []string {
	if strings.TrimSpace(text) == "" || len(columns) == 0 {
		return nil
	}
	known := map[string]string{}
	for _, column := range columns {
		known[strings.ToLower(column.Name)] = column.Name
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, token := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_$]*`).FindAllString(text, -1) {
		// 带表别名前缀时取点号后的部分（o.created_at → created_at）
		if index := strings.LastIndex(token, "."); index >= 0 {
			token = token[index+1:]
		}
		lower := strings.ToLower(token)
		if _, skip := sqlExpressionKeywords[lower]; skip {
			continue
		}
		name, ok := known[lower]
		if !ok {
			continue
		}
		if _, duplicate := seen[lower]; duplicate {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, name)
	}
	return out
}

// indexCoversPrefix 判断已有索引是否已覆盖待建索引的「最左前缀」。
// 已有 (a,b,c) 时再建 (a,b) 是冗余；已有 (a) 时建 (a,b) 不算冗余。
func indexCoversPrefix(existing []tableIndex, candidate []string) bool {
	if len(candidate) == 0 {
		return true
	}
	for _, index := range existing {
		if len(index.Columns) < len(candidate) {
			continue
		}
		matched := true
		for position, column := range candidate {
			if !strings.EqualFold(index.Columns[position], column) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// columnsPrefixOf 判断 prefix 是否是 full 的左前缀（长度相等时视为完全相同）
func columnsPrefixOf(full, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(full) {
		return false
	}
	for position, column := range prefix {
		if !strings.EqualFold(full[position], column) {
			return false
		}
	}
	return true
}

// indexExactlyMatches 判断候选索引是否已存在（列顺序完全一致）
func indexExactlyMatches(existing []tableIndex, candidate []string) bool {
	for _, index := range existing {
		if len(index.Columns) != len(candidate) {
			continue
		}
		matched := true
		for position, column := range candidate {
			if !strings.EqualFold(index.Columns[position], column) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// recommendTableIndexes 基于「表结构 + 已有索引 + SQL 用到的列」给出索引建议。
//
// indexKnown=false 表示拿不到已有索引（例如 PG 侧建表语句不含索引定义），
// 此时只给出「待人工确认」的候选，绝不断言「缺失索引」。
func recommendTableIndexes(table string, columns []tableColumn, indexes []tableIndex, usedColumns []string, indexKnown bool) []map[string]any {
	out := make([]map[string]any, 0)
	hasPrimary := false
	for _, index := range indexes {
		if index.Primary {
			hasPrimary = true
			break
		}
	}
	if indexKnown && !hasPrimary {
		out = append(out, map[string]any{
			"priority":   "high",
			"reason":     "表上没有主键：InnoDB 会隐式生成 6 字节聚簇索引，且影响主从复制与数据订正",
			"suggestion": "建议补全主键（需结合业务唯一键评估，不可直接照搬）",
		})
	}

	// 1. SQL 用到的过滤/关联/排序列：还没有任何索引以它开头 → 缺失索引候选
	for _, column := range usedColumns {
		if indexKnown && indexCoversPrefix(indexes, []string{column}) {
			continue
		}
		if indexKnown && indexExactlyMatches(indexes, []string{column}) {
			continue
		}
		columnType := ""
		for _, item := range columns {
			if strings.EqualFold(item.Name, column) {
				columnType = item.Type
				break
			}
		}
		reason := fmt.Sprintf("SQL 的过滤 / 关联 / 排序条件用到了列 `%s`，但没有以该列开头的索引", column)
		suggestion := fmt.Sprintf("ALTER TABLE `%s` ADD INDEX idx_%s_%s (`%s`);", table, sanitizeIndexPart(table), sanitizeIndexPart(column), column)
		if isLowCardinalityType(columnType) {
			reason += "；不过该列类型偏「低区分度」（枚举 / 状态 / 布尔），单列索引过滤效果可能有限，建议与其它高区分度列组成联合索引"
			out = append(out, map[string]any{"priority": "low", "reason": reason, "suggestion": suggestion})
			continue
		}
		if isLongTextType(columnType) {
			reason += "；该列是长文本类型，直接建整列索引代价高，建议使用前缀索引"
			suggestion = fmt.Sprintf("ALTER TABLE `%s` ADD INDEX idx_%s_%s (`%s`(32));", table, sanitizeIndexPart(table), sanitizeIndexPart(column), column)
		}
		out = append(out, map[string]any{"priority": "medium", "reason": reason, "suggestion": suggestion})
	}

	// 2. 重复索引 / 冗余索引：
	//    - 列完全相同 = 重复索引；
	//    - 列是另一个索引的左前缀 = 冗余索引。
	//    两者都只增加写入成本与优化器选择成本，不带来任何查询收益。
	if indexKnown {
		for i := range indexes {
			left := indexes[i]
			if left.Primary || len(left.Columns) == 0 {
				continue
			}
			for j := range indexes {
				if i == j {
					continue
				}
				right := indexes[j]
				if len(right.Columns) == 0 {
					continue
				}
				if !columnsPrefixOf(right.Columns, left.Columns) {
					continue
				}
				// 重复/冗余时优先建议删掉「非唯一」的那个：唯一索引往往还有约束作用
				target := left
				if left.Unique && !right.Unique {
					target = right
				}
				if len(right.Columns) == len(left.Columns) {
					if j < i {
						continue // 完全相同的重复索引只报一次
					}
					out = append(out, map[string]any{
						"priority": "low",
						"reason": fmt.Sprintf("索引 `%s` 与 `%s` 的列完全相同（%s），属于重复索引",
							left.Name, right.Name, strings.Join(left.Columns, ",")),
						"suggestion": fmt.Sprintf("评估后删除其中一个：ALTER TABLE `%s` DROP INDEX `%s`;", table, target.Name),
					})
					continue
				}
				out = append(out, map[string]any{
					"priority": "low",
					"reason": fmt.Sprintf("索引 `%s`(%s) 是索引 `%s`(%s) 的左前缀，属于冗余索引，只会增加写入成本",
						left.Name, strings.Join(left.Columns, ","), right.Name, strings.Join(right.Columns, ",")),
					"suggestion": fmt.Sprintf("评估后删除冗余索引：ALTER TABLE `%s` DROP INDEX `%s`;", table, target.Name),
				})
			}
		}
		if len(indexes) > 8 {
			out = append(out, map[string]any{
				"priority":   "low",
				"reason":     fmt.Sprintf("表上已有 %d 个索引，索引越多写入越慢、优化器选择成本越高", len(indexes)),
				"suggestion": "结合慢查询与执行计划，清理长期未使用的索引",
			})
		}
	}
	return out
}

func isLowCardinalityType(columnType string) bool {
	lower := strings.ToLower(columnType)
	switch {
	case strings.HasPrefix(lower, "tinyint"),
		strings.HasPrefix(lower, "boolean"),
		strings.HasPrefix(lower, "bool"),
		strings.Contains(lower, "enum"):
		return true
	}
	for _, keyword := range []string{"status", "state", "type", "flag", "gender", "deleted", "is_"} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

var varcharLengthPattern = regexp.MustCompile(`(?i)varchar\s*\(\s*(\d+)\s*\)`)

func isLongTextType(columnType string) bool {
	lower := strings.ToLower(columnType)
	if strings.Contains(lower, "text") || strings.Contains(lower, "blob") || strings.Contains(lower, "json") {
		return true
	}
	// 短 varchar 直接建整列索引没问题，只有超过 255 才建议考虑前缀索引
	if match := varcharLengthPattern.FindStringSubmatch(lower); len(match) > 1 {
		if length, err := strconv.Atoi(match[1]); err == nil && length > 255 {
			return true
		}
	}
	return false
}

func sanitizeIndexPart(value string) string {
	value = regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(value, "")
	if len(value) > 16 {
		value = value[:16]
	}
	return strings.ToLower(value)
}

// ------------------------------------------------------------------
// 执行计划解读
// ------------------------------------------------------------------

// analyzeExplainPlan 把 EXPLAIN 结果压缩成「表 + 访问类型 + 用到/可用索引 + 行数 + Extra」，
// 再按 MySQL / PG 两种形态给出可读的风险提示。
//
// 输出同时给原始行（plan）与结论（findings）：原始行保证可复核，结论保证模型能直接说人话。
func analyzeExplainPlan(columns []string, rows []map[string]any, isPostgres bool) map[string]any {
	result := map[string]any{
		"row_count": len(rows),
	}
	findings := make([]map[string]any, 0)
	plan := make([]map[string]any, 0, len(rows))
	maxRows := int64(0)
	indexesUsed := make([]string, 0)
	accessTypes := make([]string, 0)
	seenIndex := map[string]struct{}{}
	seenType := map[string]struct{}{}

	for _, row := range rows {
		if isPostgres {
			text := cellString(pickRowValue(row, "QUERY PLAN", "query plan", "query_plan", "plan"))
			if strings.TrimSpace(text) == "" {
				continue
			}
			plan = append(plan, map[string]any{"plan": text})
			collectPostgresFindings(text, &findings)
			continue
		}
		table := cellString(pickRowValue(row, "table"))
		accessType := cellString(pickRowValue(row, "type"))
		possibleKeys := cellString(pickRowValue(row, "possible_keys"))
		usedKey := cellString(pickRowValue(row, "key"))
		extra := cellString(pickRowValue(row, "Extra", "extra"))
		rowCount := parseNumericCell(pickRowValue(row, "rows"))

		item := map[string]any{}
		if table != "" {
			item["table"] = table
		}
		if accessType != "" {
			item["access_type"] = accessType
			if _, ok := seenType[accessType]; !ok {
				seenType[accessType] = struct{}{}
				accessTypes = append(accessTypes, accessType)
			}
		}
		if possibleKeys != "" {
			item["possible_keys"] = possibleKeys
		}
		if usedKey != "" {
			item["key"] = usedKey
			if _, ok := seenIndex[usedKey]; !ok {
				seenIndex[usedKey] = struct{}{}
				indexesUsed = append(indexesUsed, usedKey)
			}
		}
		if extra != "" {
			item["extra"] = extra
		}
		if rowCount > 0 {
			item["rows"] = rowCount
			if rowCount > maxRows {
				maxRows = rowCount
			}
		}
		plan = append(plan, item)
		collectMySQLFindings(table, accessType, possibleKeys, usedKey, extra, rowCount, &findings)
	}

	result["plan"] = plan
	summary := map[string]any{}
	if len(accessTypes) > 0 {
		summary["access_types"] = accessTypes
	}
	if len(indexesUsed) > 0 {
		summary["indexes_used"] = indexesUsed
	}
	if maxRows > 0 {
		summary["max_estimated_rows"] = maxRows
	}
	if len(summary) > 0 {
		result["summary"] = summary
	}
	if len(findings) == 0 {
		findings = append(findings, map[string]any{"level": "info", "message": "未从执行计划中发现明显的风险点"})
	}
	result["findings"] = findings
	return result
}

func collectMySQLFindings(table, accessType, possibleKeys, usedKey, extra string, rowCount int64, findings *[]map[string]any) {
	target := table
	if target == "" {
		target = "该步骤"
	}
	switch strings.ToUpper(accessType) {
	case "ALL":
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": fmt.Sprintf("%s 走全表扫描（type=ALL），通常需要为过滤条件补索引或改写条件", target),
		})
	case "INDEX":
		*findings = append(*findings, map[string]any{
			"level":   "info",
			"message": fmt.Sprintf("%s 走全索引扫描（type=index），虽然没扫表，但仍遍历了整个索引", target),
		})
	}
	if possibleKeys != "" && usedKey == "" {
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": fmt.Sprintf("%s 有可用索引 %s 但实际未使用（key 为空），常见于隐式类型转换、函数包裹列或统计信息过期", target, possibleKeys),
		})
	}
	lowerExtra := strings.ToLower(extra)
	if strings.Contains(lowerExtra, "using filesort") {
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": fmt.Sprintf("%s 触发了 filesort（Using filesort），排序无法直接走索引顺序", target),
		})
	}
	if strings.Contains(lowerExtra, "using temporary") {
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": fmt.Sprintf("%s 使用了临时表（Using temporary），常见于 GROUP BY / DISTINCT 组合", target),
		})
	}
	if strings.Contains(lowerExtra, "using index") {
		*findings = append(*findings, map[string]any{
			"level":   "info",
			"message": fmt.Sprintf("%s 命中覆盖索引（Using index），无需回表", target),
		})
	}
	if rowCount >= 100000 {
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": fmt.Sprintf("%s 预估扫描 %s 行，行数偏大，建议缩小过滤范围或补索引", target, formatCount(rowCount)),
		})
	}
}

func collectPostgresFindings(text string, findings *[]map[string]any) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "seq scan"):
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": "计划中出现 Seq Scan（顺序扫描），通常是缺少可用索引或过滤性不足",
		})
	case strings.Contains(lower, "index only scan"):
		*findings = append(*findings, map[string]any{
			"level":   "info",
			"message": "计划中出现 Index Only Scan（仅索引扫描），无需回表",
		})
	case strings.Contains(lower, "index scan"):
		*findings = append(*findings, map[string]any{
			"level":   "info",
			"message": "计划中出现 Index Scan（索引扫描）",
		})
	}
	if strings.Contains(lower, "external merge") {
		*findings = append(*findings, map[string]any{
			"level":   "warn",
			"message": "排序落到磁盘（external merge Disk），说明 work_mem 不足或排序集过大",
		})
	}
	if strings.Contains(lower, "nested loop") {
		*findings = append(*findings, map[string]any{
			"level":   "info",
			"message": "使用 Nested Loop 关联，外层行数大时容易放大开销",
		})
	}
	if strings.Contains(lower, "hash join") || strings.Contains(lower, "merge join") {
		*findings = append(*findings, map[string]any{
			"level":   "info",
			"message": "使用 Hash/Join 或 Merge Join，适合大结果集关联",
		})
	}
}

// pickRowValue 大小写不敏感地取行里的字段（各驱动返回的列名大小写不一致）
func pickRowValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok && value != nil {
			return value
		}
	}
	lowerKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		lowerKeys = append(lowerKeys, strings.ToLower(key))
	}
	for name, value := range row {
		for _, key := range lowerKeys {
			if strings.ToLower(name) == key && value != nil {
				return value
			}
		}
	}
	return nil
}

func parseNumericCell(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func formatCount(value int64) string {
	return strconv.FormatInt(value, 10)
}

// ------------------------------------------------------------------
// 采样查询构造（只生成 SELECT，且入参先过白名单）
// ------------------------------------------------------------------

var (
	safeIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
	// unsafeWherePattern 采样 SQL 的 WHERE 里不允许出现的片段：
	// 语句分隔符、注释、子查询与其它语句类型，一律挡掉。
	unsafeWherePattern = regexp.MustCompile(`(?i)(;|--|/\*|\*/|\b(select|insert|update|delete|drop|alter|create|truncate|replace|union|into|outfile|dumpfile|load_file|sleep|benchmark|pg_sleep|information_schema|pg_catalog)\b)`)
)

// validateSampleIdentifier 校验库名 / 表名 / 列名，只放行常规标识符
func validateSampleIdentifier(value, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s 不能为空", field)
	}
	if !safeIdentifierPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%s 不合法（只允许字母、数字、下划线与 $，且不能以数字开头）：%s", field, trimmed)
	}
	return trimmed, nil
}

// validateSampleWhere 校验采样用的 WHERE 片段。
// 允许的就是「列 操作符 值」这类简单条件，任何带语句边界或子查询形态的一律拒绝。
func validateSampleWhere(where string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(where), ";"))
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > 500 {
		return "", fmt.Errorf("WHERE 条件过长（超过 500 字符），请精简条件后重试")
	}
	if match := unsafeWherePattern.FindString(trimmed); match != "" {
		return "", fmt.Errorf("WHERE 条件包含不允许的片段「%s」：采样仅支持简单的列比较条件，不支持子查询、注释与多语句", match)
	}
	return trimmed, nil
}

// quoteSQLIdentifier 按方言给标识符加引号
func quoteSQLIdentifier(value string, isPostgres bool) string {
	if isPostgres {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

// qualifiedTableName 拼出带引号的表全名。
// PG 的 schema 入参形如 database.schema，真正用于限定表名的是后半段。
func qualifiedTableName(schema, table string, isPostgres bool) (string, error) {
	cleanTable, err := validateSampleIdentifier(table, "table")
	if err != nil {
		return "", err
	}
	if !isPostgres {
		cleanSchema, err := validateSampleIdentifier(schema, "schema")
		if err != nil {
			return "", err
		}
		return quoteSQLIdentifier(cleanSchema, false) + "." + quoteSQLIdentifier(cleanTable, false), nil
	}
	parts := strings.SplitN(schema, ".", 2)
	schemaName := parts[len(parts)-1]
	cleanSchema, err := validateSampleIdentifier(schemaName, "schema")
	if err != nil {
		return "", err
	}
	return quoteSQLIdentifier(cleanSchema, true) + "." + quoteSQLIdentifier(cleanTable, true), nil
}

// buildSampleQuery 构造采样查询：SELECT 列 FROM 表 [WHERE 条件] LIMIT n
func buildSampleQuery(schema, table string, columns []string, where string, limit int, isPostgres bool) (string, error) {
	qualified, err := qualifiedTableName(schema, table, isPostgres)
	if err != nil {
		return "", err
	}
	selector := "*"
	if len(columns) > 0 {
		parts := make([]string, 0, len(columns))
		for _, column := range columns {
			cleanColumn, err := validateSampleIdentifier(column, "columns 中的列名")
			if err != nil {
				return "", err
			}
			parts = append(parts, quoteSQLIdentifier(cleanColumn, isPostgres))
		}
		selector = strings.Join(parts, ", ")
	}
	query := fmt.Sprintf("SELECT %s FROM %s", selector, qualified)
	if where != "" {
		query += " WHERE " + where
	}
	query += fmt.Sprintf(" LIMIT %d", limit)
	return query, nil
}
