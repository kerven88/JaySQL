package checker

import (
	"strings"
	"unicode"

	"github.com/simajiong/jaysql/pkg/sqlsplit"
)

// PG 语句轻量解析：不依赖 AST，从 SQL 文本中提取规范校验所需的最小信息。
// 设计原则：解析不出即返回 ok=false，由调用方跳过相关规则，绝不猜测（避免误报）。

// pgIdent 归一化 PG 标识符：去掉双引号包裹，保留原文（未加引号时 PG 语义为小写）
type pgIdent struct {
	Raw    string // 原始书写（去引号后）
	Quoted bool   // 是否被双引号包裹（PG 中引号包裹保留大小写，属不合规信号）
	Schema string // 限定 schema（可为空）
	Name   string // 对象名
}

func normalizePGIdent(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// parsePGQualifiedName 解析 [schema.]name，支持双引号包裹
func parsePGQualifiedName(s string) (pgIdent, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" {
		return pgIdent{}, false
	}
	parts, ok := splitPGIdentifierParts(s)
	if !ok || len(parts) == 0 || len(parts) > 2 {
		return pgIdent{}, false
	}
	id := pgIdent{Raw: s}
	if len(parts) == 2 {
		id.Schema = strings.ToLower(strings.Trim(parts[0].text, `"`))
		id.Quoted = parts[1].quoted
		id.Name = strings.Trim(parts[1].text, `"`)
	} else {
		id.Quoted = parts[0].quoted
		id.Name = strings.Trim(parts[0].text, `"`)
	}
	if id.Name == "" {
		return pgIdent{}, false
	}
	return id, true
}

type pgNamePart struct {
	text   string
	quoted bool
}

// splitPGIdentifierParts 按点号拆分标识符，双引号内的点号不拆分
func splitPGIdentifierParts(s string) ([]pgNamePart, bool) {
	var parts []pgNamePart
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			if inQuote && i+1 < len(s) && s[i+1] == '"' {
				cur.WriteByte('"')
				i++
				continue
			}
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == '.' && !inQuote:
			parts = append(parts, pgNamePart{text: cur.String(), quoted: strings.Contains(cur.String(), `"`)})
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, false
	}
	parts = append(parts, pgNamePart{text: cur.String(), quoted: strings.Contains(cur.String(), `"`)})
	for i := range parts {
		parts[i].text = strings.TrimSpace(parts[i].text)
		if parts[i].text == "" {
			return nil, false
		}
	}
	return parts, true
}

// splitTopLevel 按顶层分隔符拆分（括号 / 单引号 / 双引号 / dollar-quote 感知）
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	var inSingle, inDouble, inDollar bool
	var dollarTag string
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inDollar:
			cur.WriteRune(c)
			if c == '$' {
				rest := string(runes[i:])
				if strings.HasPrefix(rest, "$"+dollarTag+"$") {
					for j := 1; j < len(dollarTag)+1 && i+j < len(runes); j++ {
						cur.WriteRune(runes[i+j])
					}
					i += len(dollarTag) + 1
					inDollar, dollarTag = false, ""
				}
			}
			continue
		case inSingle:
			cur.WriteRune(c)
			if c == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					cur.WriteRune('\'')
					i++
					continue
				}
				inSingle = false
			}
			continue
		case inDouble:
			cur.WriteRune(c)
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			cur.WriteRune(c)
		case '"':
			inDouble = true
			cur.WriteRune(c)
		case '$':
			cur.WriteRune(c)
			tagEnd := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '$' {
					tagEnd = j
					break
				}
				if !isPGWordRune(runes[j]) {
					break
				}
			}
			if tagEnd > i {
				inDollar = true
				dollarTag = string(runes[i+1 : tagEnd])
				for j := i + 1; j <= tagEnd && j < len(runes); j++ {
					cur.WriteRune(runes[j])
				}
				i = tagEnd
			}
		case '(':
			depth++
			cur.WriteRune(c)
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(c)
		default:
			if c == rune(sep) && depth == 0 {
				parts = append(parts, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteRune(c)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

func isPGWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// findTopLevelParen 返回第一个顶层左括号与其匹配右括号的下标（-1 表示未找到）
func findTopLevelParen(s string) (open, close int) {
	open, close = -1, -1
	depth := 0
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '(':
			if depth == 0 && open == -1 {
				open = i
			}
			depth++
		case ')':
			depth--
			if depth == 0 && open >= 0 && close == -1 {
				return open, i
			}
		}
	}
	return open, close
}

// pgColumn PG 列定义
type pgColumn struct {
	Name       string
	Type       string // 规范化基础类型名（小写，去掉精度后缀）
	Len        int    // varchar(n)/char(n) 的 n，0 表示未指定
	NotNull    bool
	HasDefault bool
	IsIdentity bool
	Raw        string
	Line       int
}

// pgIndexDef 索引/唯一约束定义
type pgIndexDef struct {
	Name     string
	Table    string
	Columns  []string
	Unique   bool
	Explicit bool // 是否显式命名
	Raw      string
}

// pgTableDef 建表定义
type pgTableDef struct {
	Ident         pgIdent
	Columns       []pgColumn
	PrimaryKeys   []string
	UniqueKeys    [][]string
	UniqueNames   []string
	HasForeignKey bool
	Raw           string
}

// parseCreateTable 解析 CREATE TABLE（含 IF NOT EXISTS、表级约束、列级约束）
func parseCreateTable(stmt string) (*pgTableDef, bool) {
	body := strings.TrimSpace(stmt)
	lower := strings.ToLower(body)
	idx := strings.Index(lower, "create table")
	if idx < 0 {
		return nil, false
	}
	rest := strings.TrimSpace(body[idx+len("create table"):])
	// 去掉 IF NOT EXISTS
	if m := pgIfNotExistsRe.FindString(rest); m != "" {
		rest = strings.TrimSpace(rest[len(m):])
	}
	open, closeIdx := findTopLevelParen(rest)
	if open < 0 || closeIdx < 0 || closeIdx <= open {
		return nil, false
	}
	// 处理 CREATE TABLE ... PARTITION OF / ... AS SELECT：无列定义，跳过
	head := strings.TrimSpace(rest[:open])
	if strings.Contains(strings.ToLower(head), " as ") || strings.Contains(strings.ToLower(head), " of ") {
		return nil, false
	}
	ident, ok := parsePGQualifiedName(head)
	if !ok {
		return nil, false
	}
	def := &pgTableDef{Ident: ident, Raw: stmt}
	body = rest[open+1 : closeIdx]
	for _, item := range splitTopLevel(body, ',') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		upper := strings.ToUpper(item)
		switch {
		case strings.HasPrefix(upper, "CONSTRAINT ") || strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CHECK") || strings.HasPrefix(upper, "EXCLUDE"):
			parsePGTableLevelConstraint(item, def)
			continue
		case strings.HasPrefix(upper, "LIKE ") || strings.HasPrefix(upper, "INHERITS"):
			continue
		}
		col, ok := parsePGColumnDef(item)
		if !ok {
			continue
		}
		def.Columns = append(def.Columns, col)
		if strings.Contains(strings.ToUpper(item), "PRIMARY KEY") {
			def.PrimaryKeys = append(def.PrimaryKeys, col.Name)
		}
		if strings.Contains(strings.ToUpper(item), "REFERENCES") {
			def.HasForeignKey = true
		}
		if strings.Contains(strings.ToUpper(item), "UNIQUE") {
			def.UniqueKeys = append(def.UniqueKeys, []string{col.Name})
		}
	}
	return def, true
}

// parsePGTableLevelConstraint 解析表级约束
func parsePGTableLevelConstraint(item string, def *pgTableDef) {
	upper := strings.ToUpper(item)
	rest := item
	if strings.HasPrefix(upper, "CONSTRAINT ") {
		// CONSTRAINT name PRIMARY KEY (...) / UNIQUE (...) / FOREIGN KEY ...
		fields := strings.Fields(item)
		if len(fields) > 2 {
			rest = strings.Join(fields[2:], " ")
		}
		upper = strings.ToUpper(rest)
	}
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		def.PrimaryKeys = append(def.PrimaryKeys, extractPGColumnList(rest)...)
	case strings.HasPrefix(upper, "UNIQUE"):
		cols := extractPGColumnList(rest)
		if len(cols) > 0 {
			def.UniqueKeys = append(def.UniqueKeys, cols)
			def.UniqueNames = append(def.UniqueNames, extractPGConstraintName(item))
		}
	case strings.HasPrefix(upper, "FOREIGN KEY"):
		def.HasForeignKey = true
	}
}

// extractPGConstraintName 提取 CONSTRAINT 后的名字
func extractPGConstraintName(item string) string {
	fields := strings.Fields(item)
	if len(fields) >= 2 && strings.EqualFold(fields[0], "CONSTRAINT") {
		id, ok := parsePGQualifiedName(fields[1])
		if ok {
			return id.Name
		}
	}
	return ""
}

// extractPGColumnList 提取括号内的列名列表
func extractPGColumnList(s string) []string {
	open, closeIdx := findTopLevelParen(s)
	if open < 0 || closeIdx <= open {
		return nil
	}
	var cols []string
	for _, c := range splitTopLevel(s[open+1:closeIdx], ',') {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		// 去掉列选项（如 col opclass / col DESC / col NULLS LAST）
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		name := strings.Trim(fields[0], `"`)
		// 跳过表达式索引（如 lower(name)）
		if strings.ContainsAny(name, "()") {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// parsePGColumnDef 解析单列定义
func parsePGColumnDef(item string) (pgColumn, bool) {
	col := pgColumn{Raw: item}
	fields := strings.Fields(item)
	if len(fields) < 2 {
		return col, false
	}
	name := strings.Trim(fields[0], `"`)
	if name == "" || strings.ContainsAny(name, "()") {
		return col, false
	}
	col.Name = name
	// 类型可能带括号（varchar(128)、numeric(10,2)）或数组后缀
	typeText := strings.ToLower(fields[1])
	col.Type, col.Len = splitPGTypeLen(typeText)
	upper := strings.ToUpper(item)
	col.NotNull = strings.Contains(upper, "NOT NULL")
	col.HasDefault = strings.Contains(upper, " DEFAULT ")
	col.IsIdentity = strings.Contains(upper, "IDENTITY")
	if col.IsIdentity {
		col.HasDefault = true
	}
	return col, true
}

// splitPGTypeLen 拆分类型名与长度：varchar(128) -> ("varchar",128)
func splitPGTypeLen(t string) (string, int) {
	if strings.HasSuffix(t, "[]") {
		t = strings.TrimSuffix(t, "[]")
	}
	base := t
	if i := strings.IndexByte(t, '('); i > 0 {
		base = t[:i]
		numPart := strings.TrimSuffix(t[i+1:], ")")
		if j := strings.IndexByte(numPart, ','); j > 0 {
			numPart = numPart[:j]
		}
		return strings.TrimSpace(base), atoiSafe(strings.TrimSpace(numPart))
	}
	return strings.TrimSpace(base), 0
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// parseCreateIndex 解析 CREATE [UNIQUE] INDEX [CONCURRENTLY] [IF NOT EXISTS] name ON table (cols)
func parseCreateIndex(stmt string) (*pgIndexDef, bool) {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "create")
	if idx < 0 {
		return nil, false
	}
	rest := strings.TrimSpace(stmt[idx+len("create"):])
	def := &pgIndexDef{Raw: stmt}
	if strings.HasPrefix(strings.ToLower(rest), "unique") {
		def.Unique = true
		rest = strings.TrimSpace(rest[len("unique"):])
	}
	lowerRest := strings.ToLower(rest)
	if !strings.HasPrefix(lowerRest, "index") {
		return nil, false
	}
	rest = strings.TrimSpace(rest[len("index"):])
	for _, prefix := range []string{"concurrently", "if not exists"} {
		if strings.HasPrefix(strings.ToLower(rest), prefix) {
			rest = strings.TrimSpace(rest[len(prefix):])
		}
	}
	onIdx := indexOfKeyword(rest, "on")
	// onIdx == 0：未显式命名的索引（CREATE INDEX ON ...），同样合法
	if onIdx < 0 {
		return nil, false
	}
	namePart := strings.TrimSpace(rest[:onIdx])
	// 未显式命名（CREATE INDEX ON ...）：不再直接丢弃，返回 Explicit=false / Name=""，
	// 供「索引需要设置名称」（idx_需要set_name）规则与索引计数检查使用。
	def.Explicit = namePart != ""
	if def.Explicit {
		id, ok := parsePGQualifiedName(namePart)
		if !ok {
			return nil, false
		}
		def.Name = id.Name
	}
	after := strings.TrimSpace(rest[onIdx+len("on"):])
	// 表名：直到 "using" 或 "(" 或空格结尾
	tablePart := after
	if i := strings.Index(strings.ToLower(after), "using"); i >= 0 {
		tablePart = after[:i]
	}
	if i := strings.IndexByte(tablePart, '('); i >= 0 {
		tablePart = tablePart[:i]
	}
	tablePart = strings.TrimSpace(tablePart)
	tid, ok := parsePGQualifiedName(tablePart)
	if !ok {
		return nil, false
	}
	def.Table = tid.Name
	def.Columns = extractPGColumnList(after)
	return def, true
}

// indexOfKeyword 找关键字位置，要求前后为非标识符字符（避免 "on" 出现在列名中）
func indexOfKeyword(s, kw string) int {
	lower := strings.ToLower(s)
	for i := 0; i+len(kw) <= len(lower); i++ {
		if lower[i:i+len(kw)] != kw {
			continue
		}
		if i > 0 && isPGWordRune(rune(lower[i-1])) {
			continue
		}
		if i+len(kw) < len(lower) && isPGWordRune(rune(lower[i+len(kw)])) {
			continue
		}
		return i
	}
	return -1
}

// parseCommentOn 解析 COMMENT ON TABLE|COLUMN ... IS '...'
// 目标是 TABLE 时，标识符为 [catalog.][schema.]table（column 留空表示表注释）；
// 目标是 COLUMN 时，标识符为 [schema.]table.column。两者段数语义不同，必须区分处理。
func parseCommentOn(stmt string) (table string, column string, ok bool) {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "comment on")
	if idx < 0 {
		return "", "", false
	}
	rest := strings.TrimSpace(stmt[idx+len("comment on"):])
	lowerRest := strings.ToLower(rest)
	kind := ""
	var target string
	switch {
	case strings.HasPrefix(lowerRest, "table"):
		kind = "table"
		target = strings.TrimSpace(rest[len("table"):])
	case strings.HasPrefix(lowerRest, "column"):
		kind = "column"
		target = strings.TrimSpace(rest[len("column"):])
	default:
		return "", "", false
	}
	// 截断到 IS 之前
	isIdx := indexOfKeyword(target, "is")
	if isIdx < 0 {
		return "", "", false
	}
	target = strings.TrimSpace(target[:isIdx])
	parts, okParts := splitPGIdentifierParts(target)
	if !okParts {
		return "", "", false
	}
	for i := range parts {
		parts[i].text = strings.Trim(parts[i].text, `"`)
	}
	switch kind {
	case "table":
		// 最后一段是表名，前面的 schema/catalog 忽略
		return parts[len(parts)-1].text, "", true
	default: // column
		switch len(parts) {
		case 2: // table.column
			return parts[0].text, parts[1].text, true
		case 3: // schema.table.column
			return parts[1].text, parts[2].text, true
		case 4: // catalog.schema.table.column
			return parts[2].text, parts[3].text, true
		}
		return "", "", false
	}
}

// pgAlterAddColumn 解析 ALTER TABLE ... ADD COLUMN ...
type pgAlterAddColumn struct {
	Table string
	Cols  []pgColumn
	PK    []string
	FK    bool
}

// parseAlterTableAdd 解析 ALTER TABLE 的 ADD COLUMN / ADD CONSTRAINT
func parseAlterTableAdd(stmt string) (*pgAlterAddColumn, bool) {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "alter table")
	if idx < 0 {
		return nil, false
	}
	rest := strings.TrimSpace(stmt[idx+len("alter table"):])
	if m := pgIfExistsRe.FindString(rest); m != "" {
		rest = strings.TrimSpace(rest[len(m):])
	}
	addIdx := indexOfKeyword(rest, "add")
	if addIdx <= 0 {
		return nil, false
	}
	tablePart := strings.TrimSpace(rest[:addIdx])
	tid, ok := parsePGQualifiedName(tablePart)
	if !ok {
		return nil, false
	}
	res := &pgAlterAddColumn{Table: tid.Name}
	// ADD 子句可能有多个，用逗号拆分顶层
	for _, clause := range splitTopLevel(strings.TrimSpace(rest[addIdx+len("add"):]), ',') {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		upper := strings.ToUpper(clause)
		switch {
		case strings.HasPrefix(upper, "COLUMN"):
			col, ok := parsePGColumnDef(strings.TrimSpace(clause[len("column"):]))
			if ok {
				res.Cols = append(res.Cols, col)
			}
		case strings.HasPrefix(upper, "CONSTRAINT") || strings.HasPrefix(upper, "PRIMARY KEY"):
			def := &pgTableDef{}
			parsePGTableLevelConstraint(clause, def)
			res.PK = append(res.PK, def.PrimaryKeys...)
			if def.HasForeignKey {
				res.FK = true
			}
		case strings.HasPrefix(upper, "FOREIGN KEY"):
			res.FK = true
		}
	}
	return res, true
}

// ---------- 对象级语句解析（ALTER TABLE / ALTER INDEX / 视图 / CTAS / CTL） ----------

// hasPGKeywordPrefix 判断文本是否以指定关键字开头。
// 要求关键字后不是标识符字符，避免 `order` 命中 `ordered`、`as` 命中 `assign` 这类误判。
func hasPGKeywordPrefix(s, kw string) bool {
	s = strings.TrimSpace(s)
	if len(s) < len(kw) || !strings.EqualFold(s[:len(kw)], kw) {
		return false
	}
	return len(s) == len(kw) || !isPGWordRune(rune(s[len(kw)]))
}

// trimPGStmtPrefix 依次消费语句开头的关键字（容忍任意空白与大小写），返回剩余文本。
func trimPGStmtPrefix(stmt string, keywords ...string) (string, bool) {
	rest := strings.TrimSpace(sqlsplit.StripLeadingComments(stmt))
	for _, kw := range keywords {
		if !hasPGKeywordPrefix(rest, kw) {
			return "", false
		}
		rest = strings.TrimSpace(rest[len(kw):])
	}
	return rest, true
}

// stripPGIfExists 去掉开头的 `IF EXISTS`，返回剩余文本与是否出现过 IF EXISTS。
func stripPGIfExists(s string) (string, bool) {
	if m := pgIfExistsRe.FindString(s); m != "" {
		return strings.TrimSpace(s[len(m):]), true
	}
	return strings.TrimSpace(s), false
}

// consumePGQualified 从文本开头读出一个可带 schema 前缀的标识符，返回 schema、名字与剩余文本。
// schema 为空表示语句未限定（由调用方按当前 schema 处理）。
func consumePGQualified(s string) (schema string, name string, rest string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", "", false
	}
	var parts []string
	var cur strings.Builder
	inQuote := false
	i := 0
loop:
	for i < len(s) {
		c := s[i]
		switch {
		case inQuote:
			cur.WriteByte(c)
			if c == '"' {
				inQuote = false
			}
			i++
		case c == '"':
			inQuote = true
			cur.WriteByte(c)
			i++
		case c == '.':
			parts = append(parts, cur.String())
			cur.Reset()
			i++
		case isPGWordRune(rune(c)):
			cur.WriteByte(c)
			i++
		default:
			break loop
		}
	}
	if inQuote {
		return "", "", "", false
	}
	parts = append(parts, cur.String())
	for i := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(parts[i]), `"`)
	}
	name = parts[len(parts)-1]
	if name == "" {
		return "", "", "", false
	}
	if len(parts) > 1 {
		schema = parts[len(parts)-2]
	}
	return schema, name, strings.TrimSpace(s[i:]), true
}

// consumePGName 从文本开头读出一个标识符（可带 schema 前缀、可被双引号包裹），
// 返回去引号后的最后一段名称与剩余文本。
//
// 专门用于「名字之后紧跟动作关键字」的场景（如 `t ALTER COLUMN c TYPE ...`）：
// 不能按空白切词，否则带引号的名字与 `schema.table` 都会被切坏。
func consumePGName(s string) (name string, rest string, ok bool) {
	_, name, rest, ok = consumePGQualified(s)
	return name, rest, ok
}

// pgRenamePair 改名动作的「旧名 → 新名」
type pgRenamePair struct {
	Old string
	New string
}

// pgAlterActions ALTER TABLE 中与「对象存在性/冲突」相关的动作。
//
// 只收集需要判定的对象，其余动作（ADD / SET / OWNER TO / ENABLE TRIGGER 等）
// 由各自规则处理或不属于本组规则范围，这里一律忽略。
type pgAlterActions struct {
	Table    string
	Schema   string // 语句中显式限定的 schema（`ALTER TABLE sch.t ...`）；为空表示按当前 schema 处理
	IfExists bool   // ALTER TABLE IF EXISTS：目标表可以不存在，语句不会失败

	DropColumns   []string       // DROP [COLUMN] col（`IF EXISTS` 的不收集：目标允许不存在）
	AlterColumns  []string       // ALTER [COLUMN] col <SET TYPE / SET NOT NULL / ...>
	RenameColumns []pgRenamePair // RENAME [COLUMN] old TO new
	RenameTable   string         // RENAME TO new_table
}

// parseAlterTableActions 解析 ALTER TABLE 的对象级动作。
// 解析失败返回 ok=false，调用方跳过相关规则（绝不猜测）。
func parseAlterTableActions(stmt string) (*pgAlterActions, bool) {
	rest, ok := trimPGStmtPrefix(stmt, "alter", "table")
	if !ok {
		return nil, false
	}
	act := &pgAlterActions{}
	if stripped, ifExists := stripPGIfExists(rest); ifExists {
		act.IfExists = true
		rest = stripped
	}
	// ALTER TABLE [ONLY] name [*]
	if hasPGKeywordPrefix(rest, "only") {
		rest = strings.TrimSpace(rest[len("only"):])
	}
	// `ALTER TABLE [schema.]table`：显式限定的 schema 必须保留，
	// 否则后续元信息查询会退化到当前 schema，把「别的模式里的同名表」误报成不存在。
	schema, table, tail, okName := consumePGQualified(rest)
	if !okName {
		return nil, false
	}
	act.Table = table
	act.Schema = schema
	tail = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tail), "*"))

	for _, clause := range splitTopLevel(tail, ',') {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		upper := strings.ToUpper(clause)
		switch {
		case strings.HasPrefix(upper, "DROP "):
			body := strings.TrimSpace(clause[len("DROP "):])
			// DROP CONSTRAINT 操作的是约束对象，不在本组规则范围内
			if hasPGKeywordPrefix(body, "constraint") {
				continue
			}
			if hasPGKeywordPrefix(body, "column") {
				body = strings.TrimSpace(body[len("column"):])
			}
			// `DROP COLUMN IF EXISTS c`：列可以不存在，语句不会失败，不判定
			if stripped, ifExists := stripPGIfExists(body); ifExists {
				continue
			} else {
				body = stripped
			}
			if name, _, okCol := consumePGName(body); okCol {
				act.DropColumns = append(act.DropColumns, name)
			}
		case strings.HasPrefix(upper, "ALTER "):
			body := strings.TrimSpace(clause[len("ALTER "):])
			// ALTER CONSTRAINT 不是列动作
			if hasPGKeywordPrefix(body, "constraint") {
				continue
			}
			if hasPGKeywordPrefix(body, "column") {
				body = strings.TrimSpace(body[len("column"):])
			}
			if name, _, okCol := consumePGName(body); okCol {
				act.AlterColumns = append(act.AlterColumns, name)
			}
		case strings.HasPrefix(upper, "RENAME "):
			body := strings.TrimSpace(clause[len("RENAME "):])
			if hasPGKeywordPrefix(body, "constraint") {
				continue
			}
			if hasPGKeywordPrefix(body, "to") {
				if name, _, okTbl := consumePGName(strings.TrimSpace(body[len("to"):])); okTbl {
					act.RenameTable = name
				}
				continue
			}
			if hasPGKeywordPrefix(body, "column") {
				body = strings.TrimSpace(body[len("column"):])
			}
			oldName, tail, okOld := consumePGName(body)
			if !okOld || !hasPGKeywordPrefix(tail, "to") {
				continue
			}
			newName, _, okNew := consumePGName(strings.TrimSpace(tail[len("to"):]))
			if !okNew {
				continue
			}
			act.RenameColumns = append(act.RenameColumns, pgRenamePair{Old: oldName, New: newName})
		}
	}
	return act, true
}

// pgIndexObjectOp DROP INDEX / ALTER INDEX 的目标索引
type pgIndexObjectOp struct {
	Op       string   // 语句前缀，用于告警文案（DROP INDEX / ALTER INDEX）
	Names    []string // DROP INDEX 允许一次列出多个索引
	IfExists bool     // IF EXISTS：索引可以不存在，语句不会失败
	RenameTo string   // ALTER INDEX ... RENAME TO new_name
	Schema   string   // 语句中显式限定的 schema（`DROP INDEX sch.idx`）；为空表示按当前 schema 处理
}

// parseIndexObjectOp 解析 DROP INDEX / ALTER INDEX 的目标索引与动作。
func parseIndexObjectOp(stmt string) (*pgIndexObjectOp, bool) {
	if rest, ok := trimPGStmtPrefix(stmt, "drop", "index"); ok {
		op := &pgIndexObjectOp{Op: "DROP INDEX"}
		if hasPGKeywordPrefix(rest, "concurrently") {
			rest = strings.TrimSpace(rest[len("concurrently"):])
		}
		rest, op.IfExists = stripPGIfExists(rest)
		for _, item := range splitTopLevel(rest, ',') {
			schema, name, _, okName := consumePGQualified(item)
			if !okName {
				continue
			}
			op.Names = append(op.Names, name)
			if schema != "" && op.Schema == "" {
				op.Schema = schema
			}
		}
		return op, len(op.Names) > 0
	}
	rest, ok := trimPGStmtPrefix(stmt, "alter", "index")
	if !ok {
		return nil, false
	}
	op := &pgIndexObjectOp{Op: "ALTER INDEX"}
	rest, op.IfExists = stripPGIfExists(rest)
	schema, name, tail, okName := consumePGQualified(rest)
	if !okName {
		return nil, false
	}
	op.Schema = schema
	op.Names = []string{name}
	if hasPGKeywordPrefix(tail, "rename") {
		tail = strings.TrimSpace(tail[len("rename"):])
		if hasPGKeywordPrefix(tail, "to") {
			if newName, _, okNew := consumePGName(strings.TrimSpace(tail[len("to"):])); okNew {
				op.RenameTo = newName
			}
		}
	}
	return op, true
}

// trimPGCreateTablePrefix 消费 `CREATE [OR REPLACE] [GLOBAL|LOCAL] [TEMP|TEMPORARY|UNLOGGED] TABLE` 前缀，
// 返回表名之前的剩余文本（调用方按需再剥离 IF NOT EXISTS）。
func trimPGCreateTablePrefix(stmt string) (string, bool) {
	rest, ok := trimPGStmtPrefix(stmt, "create")
	if !ok {
		return "", false
	}
	for {
		consumed := false
		if hasPGKeywordPrefix(rest, "or") {
			rest = strings.TrimSpace(rest[len("or"):])
			if hasPGKeywordPrefix(rest, "replace") {
				rest = strings.TrimSpace(rest[len("replace"):])
			}
			consumed = true
		}
		if !consumed {
			for _, kw := range []string{"global", "local", "temporary", "temp", "unlogged"} {
				if hasPGKeywordPrefix(rest, kw) {
					rest = strings.TrimSpace(rest[len(kw):])
					consumed = true
					break
				}
			}
		}
		if !consumed {
			break
		}
	}
	if !hasPGKeywordPrefix(rest, "table") {
		return "", false
	}
	return strings.TrimSpace(rest[len("table"):]), true
}

// parseCreateTableAs 解析 `CREATE TABLE t [ (columns) ] AS query`（CTAS），返回目标表名。
//
// 注意与 MySQL 的差异：PG 的 CTAS 是常规写法，`AS` 位于表名（或列名清单）之后；
// `CREATE TABLE t OF type` / `PARTITION OF` 不含 AS，不会误判。
func parseCreateTableAs(stmt string) (table string, ok bool) {
	rest, okPrefix := trimPGCreateTablePrefix(stmt)
	if !okPrefix {
		return "", false
	}
	rest = strings.TrimSpace(pgIfNotExistsRe.ReplaceAllString(rest, ""))
	name, tail, okName := consumePGName(rest)
	if !okName {
		return "", false
	}
	tail = strings.TrimSpace(tail)
	// 列名清单：`CREATE TABLE t (a, b) AS SELECT ...`
	if strings.HasPrefix(tail, "(") {
		open, closeIdx := findTopLevelParen(tail)
		if open != 0 || closeIdx < 0 {
			return "", false
		}
		tail = strings.TrimSpace(tail[closeIdx+1:])
	}
	if !hasPGKeywordPrefix(tail, "as") {
		return "", false
	}
	return name, true
}

// parseCreateTableLike 解析 `CREATE TABLE t (LIKE source [INCLUDING ...])`，返回目标表名与源表名。
//
// PG 没有 MySQL 的 `CREATE TABLE t LIKE source` 写法，等价能力是括号内的 `LIKE` 子句；
// 对外规则（禁止 CREATE TABLE ... LIKE）语义一致，因此归到同一条规则。
func parseCreateTableLike(stmt string) (table string, source string, ok bool) {
	rest, okPrefix := trimPGCreateTablePrefix(stmt)
	if !okPrefix {
		return "", "", false
	}
	rest = strings.TrimSpace(pgIfNotExistsRe.ReplaceAllString(rest, ""))
	name, tail, okName := consumePGName(rest)
	if !okName {
		return "", "", false
	}
	tail = strings.TrimSpace(tail)
	open, closeIdx := findTopLevelParen(tail)
	if open != 0 || closeIdx <= open {
		return "", "", false
	}
	for _, item := range splitTopLevel(tail[open+1:closeIdx], ',') {
		item = strings.TrimSpace(item)
		if !hasPGKeywordPrefix(item, "like") {
			continue
		}
		src := strings.TrimSpace(item[len("like"):])
		// 去掉 INCLUDING / EXCLUDING 选项
		for _, kw := range []string{"including", "excluding"} {
			if i := indexOfKeyword(src, kw); i > 0 {
				src = strings.TrimSpace(src[:i])
			}
		}
		if srcName, _, okSrc := consumePGName(src); okSrc {
			return name, srcName, true
		}
		return name, "", true
	}
	return "", "", false
}

// parseViewStatement 解析视图相关语句：CREATE/ALTER/DROP [MATERIALIZED] VIEW、REFRESH MATERIALIZED VIEW。
// 返回操作名（用于告警文案）与目标视图名（解析不出名称时返回空串，规则仍照常命中）。
func parseViewStatement(stmt string) (op string, view string, ok bool) {
	words := strings.Fields(sqlsplit.NormalizeLeading(stmt))
	if len(words) < 2 {
		return "", "", false
	}
	switch words[0] {
	case "create", "alter", "drop", "refresh":
	default:
		return "", "", false
	}
	viewIdx := -1
	for i := 1; i < len(words); i++ {
		if words[i] == "view" {
			viewIdx = i
			break
		}
	}
	if viewIdx < 0 {
		return "", "", false
	}
	op = strings.ToUpper(strings.Join(words[:viewIdx+1], " "))
	rest, _ := stripPGIfExists(strings.Join(words[viewIdx+1:], " "))
	name, _, okName := consumePGName(rest)
	if !okName {
		return op, "", true
	}
	return op, name, true
}
