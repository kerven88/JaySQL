package checker

import (
	"regexp"
	"strings"
)

// Oracle 语句轻量解析：与 PG 引擎同一设计原则——不依赖 AST，从 SQL 文本中
// 提取规范校验所需的最小信息；解析不出即返回 ok=false，由调用方跳过相关规则，
// 绝不猜测（避免误报）。
//
// 与 PG 的关键差异：
//   - 未加引号的标识符折叠为「大写」（PG 是小写），双引号包裹保留原样（不合规信号）；
//   - 列注释没有内联语法，只能通过独立的 COMMENT ON COLUMN 语句提供；
//   - 自增列是 IDENTITY 列（GENERATED ... AS IDENTITY [START WITH n]）；
//   - ALTER 的改列定义子句是 MODIFY 而不是 ALTER COLUMN；
//   - 通用文本助手（splitTopLevel / findTopLevelParen / indexOfKeyword /
//     stripPGStringsAndComments / pgWordTokens / pgInsertShapes 等）与 Oracle 的
//     单引号/双引号/括号语义一致，直接复用 pg_parse.go / pg_dml.go 的实现。

// oracleIdent 归一化 Oracle 标识符
type oracleIdent struct {
	Raw    string // 原始书写（去引号后）
	Quoted bool   // 是否被双引号包裹（Oracle 中引号包裹保留大小写，属不合规信号）
	Schema string // 限定 schema（可为空）
	Name   string // 对象名
}

// normalizeOracleIdent 归一化标识符用于比对：未加引号的标识符折叠为大写
func normalizeOracleIdent(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// parseOracleQualifiedName 解析 [schema.]name，支持双引号包裹
func parseOracleQualifiedName(s string) (oracleIdent, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" {
		return oracleIdent{}, false
	}
	parts, ok := splitPGIdentifierParts(s)
	if !ok || len(parts) == 0 || len(parts) > 2 {
		return oracleIdent{}, false
	}
	id := oracleIdent{Raw: s}
	if len(parts) == 2 {
		id.Schema = strings.ToUpper(strings.Trim(parts[0].text, `"`))
		id.Quoted = parts[1].quoted
		id.Name = strings.Trim(parts[1].text, `"`)
	} else {
		id.Quoted = parts[0].quoted
		id.Name = strings.Trim(parts[0].text, `"`)
	}
	if id.Name == "" {
		return oracleIdent{}, false
	}
	return id, true
}

// oracleReservedWords Oracle 保留字（官方 V$RESERVED_WORDS 中 reserved='R' 的常用子集）。
// 与 MySQL/PG 引擎口径一致：关键字范围来自规范配置（IDENTIFIER_KEYWORDS，未配置回退默认全集），
// 判定大小写不敏感（Oracle 未加引号的标识符本身折叠为大写）。
var oracleReservedWords = map[string]bool{
	"ACCESS": true, "ADD": true, "ALL": true, "ALTER": true, "AND": true, "ANY": true,
	"AS": true, "ASC": true, "AUDIT": true, "BETWEEN": true, "BY": true, "CHAR": true,
	"CHECK": true, "CLUSTER": true, "COLUMN": true, "COLUMN_VALUE": true, "COMMENT": true,
	"COMPRESS": true, "CONNECT": true, "CREATE": true, "CURRENT": true, "DATE": true,
	"DECIMAL": true, "DEFAULT": true, "DELETE": true, "DESC": true, "DISTINCT": true,
	"DROP": true, "ELSE": true, "EXCLUSIVE": true, "EXISTS": true, "FILE": true,
	"FLOAT": true, "FOR": true, "FROM": true, "GRANT": true, "GROUP": true, "HAVING": true,
	"IDENTIFIED": true, "IMMEDIATE": true, "IN": true, "INCREMENT": true, "INDEX": true,
	"INITIAL": true, "INSERT": true, "INTEGER": true, "INTERSECT": true, "INTO": true,
	"IS": true, "LEVEL": true, "LIKE": true, "LOCK": true, "LONG": true, "MAXEXTENTS": true,
	"MINUS": true, "MLSLABEL": true, "MODE": true, "MODIFY": true, "NOAUDIT": true,
	"NOCOMPRESS": true, "NOT": true, "NOWAIT": true, "NULL": true, "NUMBER": true,
	"OF": true, "OFFLINE": true, "ON": true, "ONLINE": true, "OPTION": true, "OR": true,
	"ORDER": true, "OVERLAPS": true, "PCTFREE": true, "PRIOR": true, "PUBLIC": true,
	"RAW": true, "RENAME": true, "RESOURCE": true, "REVOKE": true, "ROW": true,
	"ROWID": true, "ROWNUM": true, "ROWS": true, "SELECT": true, "SESSION": true,
	"SET": true, "SHARE": true, "SIZE": true, "SMALLINT": true, "START": true,
	"SUCCESSFUL": true, "SYNONYM": true, "SYSDATE": true, "TABLE": true, "THEN": true,
	"TO": true, "TRIGGER": true, "UID": true, "UNION": true, "UNIQUE": true, "UPDATE": true,
	"USER": true, "VALIDATE": true, "VALUES": true, "VARCHAR": true, "VARCHAR2": true,
	"VIEW": true, "WHENEVER": true, "WHERE": true, "WITH": true,
}

// oracleColumn Oracle 列定义
type oracleColumn struct {
	Name       string
	Type       string // 规范化基础类型名（小写，去掉精度后缀）
	Len        int    // char/varchar2/nchar/nvarchar2/raw 的 n，0 表示未指定
	NotNull    bool
	HasDefault bool
	IsIdentity bool // GENERATED ... AS IDENTITY
	Quoted     bool // 列名是否被双引号包裹（Oracle 中引号包裹保留大小写，属不合规信号）
	// IdentityStart IDENTITY 的 START WITH n；0 表示未指定（默认 1）
	IdentityStart int
	Raw           string
}

// oracleIndexDef 索引/唯一约束定义
type oracleIndexDef struct {
	Name     string
	Table    string
	Schema   string // 语句中显式限定的 schema（ON sch.t）
	Columns  []string
	Unique   bool
	Explicit bool // 是否显式命名
	Raw      string
}

// oracleTableDef 建表定义
type oracleTableDef struct {
	Ident         oracleIdent
	Columns       []oracleColumn
	PrimaryKeys   []string
	UniqueKeys    [][]string
	UniqueNames   []string
	HasForeignKey bool
	Raw           string
}

// oracleCreateTablePrefixRe 匹配 CREATE [global temporary|private temporary|...] TABLE 前缀
var oracleCreateTablePrefixRe = regexp.MustCompile(`(?is)^\s*create\s+(?:(?:global|private|sharded|duplicated|immutable)\s+)?(?:temporary\s+)?table\s+`)

// oracleIdentityStartRe 提取 IDENTITY 的 START WITH 常量
var oracleIdentityStartRe = regexp.MustCompile(`(?is)start\s+with\s+(\d+)`)

// parseOracleCreateTable 解析 CREATE TABLE（列定义 + 表级约束）。
// CTAS（含「列定义 + AS SELECT」混合形态）返回 ok=false，由闸门统一处理。
func parseOracleCreateTable(stmt string) (*oracleTableDef, bool) {
	m := oracleCreateTablePrefixRe.FindStringIndex(stmt)
	if m == nil {
		return nil, false
	}
	rest := strings.TrimSpace(stmt[m[1]:])
	// CTAS 判定：表名与列定义之后存在顶层 AS SELECT / AS ( / AS WITH —— 该形态的列结构
	// 继承自 SELECT，逐列规范检查只能产出无法满足的伪结论，交由闸门统一给出结论
	if findOracleCTASMark(rest) >= 0 {
		return nil, false
	}
	open, closeIdx := findTopLevelParen(rest)
	if open < 0 || closeIdx < 0 || closeIdx <= open {
		return nil, false
	}
	head := strings.TrimSpace(rest[:open])
	ident, ok := parseOracleQualifiedName(head)
	if !ok {
		return nil, false
	}
	def := &oracleTableDef{Ident: ident, Raw: stmt}
	body := rest[open+1 : closeIdx]
	for _, item := range splitTopLevel(body, ',') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		upper := strings.ToUpper(stripPGStringsAndComments(item))
		switch {
		case strings.HasPrefix(upper, "CONSTRAINT ") || strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CHECK") || strings.HasPrefix(upper, "SUPPLEMENTAL LOG"):
			parseOracleTableLevelConstraint(item, def)
			continue
		}
		col, ok := parseOracleColumnDef(item)
		if !ok {
			continue
		}
		def.Columns = append(def.Columns, col)
		if strings.Contains(upper, "PRIMARY KEY") {
			def.PrimaryKeys = append(def.PrimaryKeys, col.Name)
		}
		if strings.Contains(upper, "REFERENCES") {
			def.HasForeignKey = true
		}
		if strings.Contains(upper, "UNIQUE") {
			def.UniqueKeys = append(def.UniqueKeys, []string{col.Name})
		}
	}
	return def, true
}

// findOracleCTASMark 在建表语句中查找顶层「AS SELECT / AS (」标记的下标（未找到返回 -1）。
// 字符串/注释已剔除后再扫描，避免列 DEFAULT 表达式里的字面量干扰。
func findOracleCTASMark(rest string) int {
	clean := stripPGStringsAndComments(rest)
	lower := strings.ToLower(clean)
	// 先去掉首对顶层括号（列定义区），再找 AS
	open, closeIdx := findTopLevelParen(clean)
	searchFrom := 0
	if open >= 0 && closeIdx > open {
		searchFrom = closeIdx + 1
	}
	tail := lower[searchFrom:]
	for i := 0; i+3 <= len(tail); i++ {
		if tail[i:i+2] != "as" {
			continue
		}
		if i > 0 && isPGWordRune(rune(tail[i-1])) {
			continue
		}
		if i+2 < len(tail) && isPGWordRune(rune(tail[i+2])) {
			continue
		}
		after := strings.TrimSpace(tail[i+2:])
		if strings.HasPrefix(after, "select") || strings.HasPrefix(after, "(") || strings.HasPrefix(after, "with") {
			return searchFrom + i
		}
	}
	return -1
}

// parseOracleTableLevelConstraint 解析表级约束
func parseOracleTableLevelConstraint(item string, def *oracleTableDef) {
	clean := stripPGStringsAndComments(item)
	upper := strings.ToUpper(clean)
	rest := item
	if strings.HasPrefix(upper, "CONSTRAINT ") {
		// CONSTRAINT name PRIMARY KEY (...) / UNIQUE (...) / FOREIGN KEY ...
		fields := strings.Fields(clean)
		if len(fields) > 2 {
			rest = joinWordsFrom(item, clean, 3) // 跳过 CONSTRAINT 与约束名两个字段
		}
		upper = strings.ToUpper(stripPGStringsAndComments(rest))
		name := ""
		if fields := strings.Fields(clean); len(fields) > 1 {
			name = strings.Trim(fields[1], `"`)
		}
		switch {
		case strings.HasPrefix(upper, "UNIQUE"):
			def.UniqueNames = append(def.UniqueNames, name)
		case strings.HasPrefix(upper, "FOREIGN KEY") || strings.Contains(upper, " REFERENCES "):
			def.HasForeignKey = true
		}
		// 命名/匿名的 PK/UNIQUE 列集由下方统一提取
	}
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"), strings.HasPrefix(upper, "UNIQUE"):
		cols := extractOracleParenList(rest)
		if len(cols) > 0 {
			if strings.HasPrefix(upper, "PRIMARY KEY") {
				def.PrimaryKeys = append(def.PrimaryKeys, cols...)
			} else {
				def.UniqueKeys = append(def.UniqueKeys, cols)
			}
		}
	case strings.HasPrefix(upper, "FOREIGN KEY") || strings.Contains(upper, " REFERENCES "):
		def.HasForeignKey = true
	}
}

// joinWordsFrom 取原文本中与 clean 对齐的第 n 个词起的片段。
// clean 是 item 去字符串/注释后的等长文本，二者按下标对齐，可以直接切片保留原文。
func joinWordsFrom(item, clean string, n int) string {
	fields := strings.Fields(clean)
	if len(fields) < n {
		return item
	}
	// 找第 n 个词在 clean 中的起始下标
	idx, count := 0, 0
	for idx < len(clean) {
		// 跳过空白
		for idx < len(clean) && clean[idx] == ' ' {
			idx++
		}
		if idx >= len(clean) {
			break
		}
		end := idx
		for end < len(clean) && clean[end] != ' ' {
			end++
		}
		count++
		if count == n {
			// 对齐回原文（stripPGStringsAndComments 等长替换）
			return strings.TrimSpace(item[idx:])
		}
		idx = end
	}
	return item
}

// parseOracleColumnDef 解析单列定义（ADD / MODIFY 子句复用）
func parseOracleColumnDef(item string) (oracleColumn, bool) {
	clean := stripPGStringsAndComments(item)
	fields := strings.Fields(clean)
	if len(fields) < 2 {
		return oracleColumn{}, false
	}
	name := strings.Trim(fields[0], `"`)
	if name == "" || strings.ContainsAny(name, "()") {
		return oracleColumn{}, false
	}
	col := oracleColumn{Name: name, Raw: item, Quoted: strings.HasPrefix(fields[0], `"`)}
	// 类型可能带精度：varchar2(128)、number(10,2)、char(10 byte)
	col.Type, col.Len = splitPGTypeLen(strings.ToLower(fields[1]))
	upper := strings.ToUpper(clean)
	col.NotNull = strings.Contains(upper, "NOT NULL")
	// DEFAULT 判定用带词边界的 indexOfKeyword：避免列名/类型名里包含 default 字样的误判
	col.HasDefault = indexOfKeyword(clean, "default") >= 0
	col.IsIdentity = strings.Contains(upper, "IDENTITY") && strings.Contains(upper, "GENERATED")
	if col.IsIdentity {
		col.HasDefault = true
		if m := oracleIdentityStartRe.FindStringSubmatch(clean); m != nil {
			col.IdentityStart = atoiSafe(m[1])
		}
	}
	return col, true
}

// extractOracleParenList 取文本中第一对顶层括号内的列名列表
func extractOracleParenList(s string) []string {
	open, closeIdx := findTopLevelParen(s)
	if open < 0 || closeIdx <= open {
		return nil
	}
	var cols []string
	for _, item := range splitTopLevel(s[open+1:closeIdx], ',') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		fields := strings.Fields(item)
		if len(fields) == 0 {
			continue
		}
		cols = append(cols, strings.Trim(fields[0], `"`))
	}
	return cols
}

// ---------- ALTER TABLE ----------

type oracleRenamePair struct{ Old, New string }

// oracleAlterActions ALTER TABLE 的动作拆解
type oracleAlterActions struct {
	Table  string
	Schema string // 语句中显式限定的 schema（ALTER TABLE sch.t ...）
	// 语句级开关：Oracle 无 IF EXISTS 语法，保留字段与 PG 对齐（恒为 false）
	IfExists bool

	AddColumns    []oracleColumn
	ModifyColumns []oracleColumn
	DropColumns   []string
	RenameColumns []oracleRenamePair
	RenameTable   string
	// 新增约束
	AddPK         []string
	AddUniq       [][]string
	UniqNames     []string // 显式命名的 UNIQUE 约束名
	HasNewFK      bool
	HasNewUnnamed bool // 是否存在未命名的表级约束（index_must_have_name 判定用）
}

// parseOracleAlterTableActions 解析 ALTER TABLE 的目标表与动作。
// Oracle 无 IF EXISTS；SET UNUSED / MOVE / ENABLE / DISABLE / DROP CONSTRAINT 等
// 不属于列动作，忽略（不产出告警，避免误报）。
func parseOracleAlterTableActions(stmt string) (*oracleAlterActions, bool) {
	clean := strings.TrimSpace(stmt)
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "alter table")
	if idx < 0 {
		return nil, false
	}
	rest := strings.TrimSpace(clean[idx+len("alter table"):])
	// 表名（可能带 schema 限定与双引号）
	schema, table, tail, ok := consumeOracleQualified(rest)
	if !ok {
		return nil, false
	}
	act := &oracleAlterActions{Table: table, Schema: schema}
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return act, true
	}
	upperTail := strings.ToUpper(stripPGStringsAndComments(tail))

	switch {
	case strings.HasPrefix(upperTail, "ADD"):
		body := strings.TrimSpace(tail[3:])
		parseOracleAddModifyClause(body, act, true)
	case strings.HasPrefix(upperTail, "MODIFY"):
		body := strings.TrimSpace(tail[6:])
		parseOracleAddModifyClause(body, act, false)
	case strings.HasPrefix(upperTail, "DROP"):
		parseOracleDropClause(strings.TrimSpace(tail[4:]), act)
	case strings.HasPrefix(upperTail, "RENAME"):
		parseOracleRenameClause(strings.TrimSpace(tail[6:]), act)
	}
	return act, true
}

// parseOracleAddModifyClause 解析 ADD / MODIFY 子句体。
// Oracle 语法：ADD (item, item, ...) / ADD item（单条可不带括号）；MODIFY 同构。
// isAdd 区分新增（列 + 约束都可能出现）与修改（只有列定义）。
func parseOracleAddModifyClause(body string, act *oracleAlterActions, isAdd bool) {
	if body == "" {
		return
	}
	var items []string
	if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "(") {
		open, closeIdx := findTopLevelParen(trimmed)
		if open < 0 || closeIdx <= open {
			return
		}
		for _, item := range splitTopLevel(trimmed[open+1:closeIdx], ',') {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
	} else {
		items = []string{strings.TrimSpace(body)}
	}
	for _, item := range items {
		upper := strings.ToUpper(stripPGStringsAndComments(item))
		switch {
		case strings.HasPrefix(upper, "CONSTRAINT ") || strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CHECK"):
			if !isAdd {
				continue
			}
			parseOracleAddConstraint(item, act)
		default:
			// MODIFY 的语义是「修改既有列」，列必然存在，不参与 obj_col_must_exist 判定
			col, ok := parseOracleColumnDef(item)
			if !ok {
				continue
			}
			if isAdd {
				act.AddColumns = append(act.AddColumns, col)
				if strings.Contains(upper, "REFERENCES") {
					act.HasNewFK = true
				}
			} else {
				act.ModifyColumns = append(act.ModifyColumns, col)
			}
		}
	}
}

// parseOracleAddConstraint 解析 ADD 子句中的表级约束
func parseOracleAddConstraint(item string, act *oracleAlterActions) {
	clean := stripPGStringsAndComments(item)
	upper := strings.ToUpper(clean)
	name := ""
	rest := item
	if strings.HasPrefix(upper, "CONSTRAINT ") {
		fields := strings.Fields(clean)
		if len(fields) > 1 {
			name = strings.Trim(fields[1], `"`)
		}
		if len(fields) > 2 {
			rest = joinWordsFrom(item, clean, 3) // 跳过 CONSTRAINT 与约束名两个字段
			upper = strings.ToUpper(stripPGStringsAndComments(rest))
		}
	}
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		act.AddPK = append(act.AddPK, extractOracleParenList(rest)...)
		if name == "" {
			act.HasNewUnnamed = true
		}
	case strings.HasPrefix(upper, "UNIQUE"):
		cols := extractOracleParenList(rest)
		if len(cols) > 0 {
			act.AddUniq = append(act.AddUniq, cols)
			if name != "" {
				act.UniqNames = append(act.UniqNames, name)
			} else {
				act.HasNewUnnamed = true
			}
		}
	case strings.HasPrefix(upper, "FOREIGN KEY") || strings.Contains(upper, " REFERENCES "):
		act.HasNewFK = true
	}
}

// parseOracleDropClause 解析 DROP 子句（只关心列动作；
// DROP CONSTRAINT / DROP PRIMARY KEY / DROP UNIQUE 属约束操作，不产出列级告警）
func parseOracleDropClause(body string, act *oracleAlterActions) {
	clean := strings.TrimSpace(body)
	upper := strings.ToUpper(stripPGStringsAndComments(clean))
	switch {
	case strings.HasPrefix(upper, "COLUMN"):
		name := strings.TrimSpace(clean[len("COLUMN"):])
		// Oracle 无 DROP COLUMN IF EXISTS；排除 CASCADE CONSTRAINTS 等尾巴上的非列名 token
		fields := strings.Fields(stripPGStringsAndComments(name))
		if len(fields) > 0 && !isOracleDropTail(fields[0]) {
			act.DropColumns = append(act.DropColumns, strings.Trim(fields[0], `"`))
		}
	case strings.HasPrefix(upper, "("):
		open, closeIdx := findTopLevelParen(clean)
		if open < 0 || closeIdx <= open {
			return
		}
		for _, item := range splitTopLevel(clean[open+1:closeIdx], ',') {
			if item = strings.TrimSpace(item); item != "" {
				act.DropColumns = append(act.DropColumns, strings.Trim(item, `"`))
			}
		}
	}
}

// isOracleDropTail DROP COLUMN 后的修饰词（CASCADE/CONSTRAINTS/INVALIDATE 等）不是列名
func isOracleDropTail(word string) bool {
	switch strings.ToUpper(strings.Trim(word, `"`)) {
	case "CASCADE", "CONSTRAINTS", "INVALIDATE", "CHECKPOINT":
		return true
	}
	return false
}

// parseOracleRenameClause 解析 RENAME 子句：RENAME TO new_name / RENAME COLUMN old TO new
func parseOracleRenameClause(body string, act *oracleAlterActions) {
	clean := strings.TrimSpace(body)
	upper := strings.ToUpper(stripPGStringsAndComments(clean))
	switch {
	case strings.HasPrefix(upper, "TO "):
		_, name, _, ok := consumeOracleQualified(strings.TrimSpace(clean[2:]))
		if ok {
			act.RenameTable = name
		}
	case strings.HasPrefix(upper, "COLUMN "):
		rest := strings.TrimSpace(clean[len("COLUMN"):])
		parts := splitOracleRenameParts(rest)
		if len(parts) == 3 && strings.EqualFold(parts[1], "TO") {
			old := strings.Trim(parts[0], `"`)
			_, newName, _, ok := consumeOracleQualified(parts[2])
			if old != "" && ok {
				act.RenameColumns = append(act.RenameColumns, oracleRenamePair{Old: old, New: newName})
			}
		}
	}
}

// splitOracleRenameParts 按 RENAME COLUMN old TO new 拆 token（双引号感知，括号不感知——该语法无括号）
func splitOracleRenameParts(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case (c == ' ' || c == '\t' || c == '\n' || c == '\r') && !inQuote:
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// ---------- 索引 / 注释 / 视图 ----------

// parseOracleCreateIndex 解析 CREATE [UNIQUE] INDEX name ON [schema.]table (cols)
func parseOracleCreateIndex(stmt string) (*oracleIndexDef, bool) {
	clean := strings.TrimSpace(stmt)
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "create")
	if idx < 0 {
		return nil, false
	}
	rest := strings.TrimSpace(clean[idx+len("create"):])
	def := &oracleIndexDef{Raw: stmt}
	if strings.HasPrefix(strings.ToLower(rest), "unique") {
		def.Unique = true
		rest = strings.TrimSpace(rest[len("unique"):])
	}
	if !strings.HasPrefix(strings.ToLower(rest), "index") {
		return nil, false
	}
	rest = strings.TrimSpace(rest[len("index"):])
	onIdx := indexOfKeyword(rest, "on")
	if onIdx < 0 {
		return nil, false
	}
	namePart := strings.TrimSpace(rest[:onIdx])
	def.Explicit = namePart != ""
	if def.Explicit {
		id, ok := parseOracleQualifiedName(namePart)
		if !ok {
			return nil, false
		}
		def.Name = id.Name
	}
	after := strings.TrimSpace(rest[onIdx+len("on"):])
	// 表名：直到 "(" 或空白结尾（Oracle 无 USING）
	tablePart := after
	if i := strings.IndexByte(tablePart, '('); i >= 0 {
		tablePart = tablePart[:i]
	}
	tablePart = strings.TrimSpace(tablePart)
	tid, ok := parseOracleQualifiedName(tablePart)
	if !ok {
		return nil, false
	}
	def.Table = tid.Name
	def.Schema = tid.Schema
	def.Columns = extractPGColumnList(after)
	return def, true
}

type oracleIndexObjectOp struct {
	Op       string   // "DROP INDEX" / "ALTER INDEX"
	Names    []string // DROP INDEX 允许一次列出多个索引
	RenameTo string   // ALTER INDEX ... RENAME TO new_name
}

// parseOracleIndexObjectOp 解析 DROP INDEX / ALTER INDEX 的目标索引与动作。
// ALTER INDEX 仅处理 RENAME TO（REBUILD / UNUSABLE / MONITORING 等不产出告警）。
func parseOracleIndexObjectOp(stmt string) (*oracleIndexObjectOp, bool) {
	clean := strings.TrimSpace(stmt)
	if rest, ok := trimOracleStmtPrefix(clean, "drop", "index"); ok {
		op := &oracleIndexObjectOp{Op: "DROP INDEX"}
		for _, item := range splitTopLevel(rest, ',') {
			if _, name, _, okName := consumeOracleQualified(strings.TrimSpace(item)); okName {
				op.Names = append(op.Names, name)
			}
		}
		return op, len(op.Names) > 0
	}
	rest, ok := trimOracleStmtPrefix(clean, "alter", "index")
	if !ok {
		return nil, false
	}
	op := &oracleIndexObjectOp{Op: "ALTER INDEX"}
	_, name, tail, okName := consumeOracleQualified(rest)
	if !okName {
		return nil, false
	}
	tail = strings.TrimSpace(tail)
	if upper := strings.ToUpper(stripPGStringsAndComments(tail)); strings.HasPrefix(upper, "RENAME TO ") {
		_, newName, _, okNew := consumeOracleQualified(strings.TrimSpace(tail[len("RENAME TO"):]))
		if !okNew {
			return nil, false
		}
		op.RenameTo = newName
	} else {
		return nil, false // REBUILD / UNUSABLE 等非改名动作不进入规则链
	}
	op.Names = []string{name}
	return op, true
}

// parseOracleCommentOn 解析 COMMENT ON TABLE / COLUMN / MATERIALIZED VIEW。
// Oracle 列名可能是三段式 schema.table.column，返回其原始书写（调用方按段归一）。
func parseOracleCommentOn(stmt string) (table, column string, ok bool) {
	clean := strings.TrimSpace(stmt)
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "comment on")
	if idx < 0 {
		return "", "", false
	}
	rest := strings.TrimSpace(clean[idx+len("comment on"):])
	upper := strings.ToUpper(rest)
	kind := ""
	switch {
	case strings.HasPrefix(upper, "COLUMN "):
		kind = "column"
		rest = strings.TrimSpace(rest[len("COLUMN"):])
	case strings.HasPrefix(upper, "TABLE "):
		kind = "table"
		rest = strings.TrimSpace(rest[len("TABLE"):])
	case strings.HasPrefix(upper, "MATERIALIZED VIEW "):
		kind = "table"
		rest = strings.TrimSpace(rest[len("MATERIALIZED VIEW"):])
	default:
		return "", "", false
	}
	// 目标名：到 IS 关键字为止
	isIdx := indexOfKeyword(rest, "is")
	if isIdx < 0 {
		return "", "", false
	}
	namePart := strings.TrimSpace(rest[:isIdx])
	parts, okParts := splitPGIdentifierParts(namePart)
	if !okParts || len(parts) == 0 || len(parts) > 3 {
		return "", "", false
	}
	name := strings.Trim(parts[len(parts)-1].text, `"`)
	if kind == "column" {
		column = name
		if len(parts) >= 2 {
			table = strings.Trim(parts[len(parts)-2].text, `"`)
		}
	} else {
		table = name
	}
	if table == "" {
		return "", "", false
	}
	return table, column, true
}

// isOracleViewStatement 判断是否为视图相关语句（含 MATERIALIZED VIEW）
func isOracleViewStatement(stmt string) bool {
	_, ok := parseOracleViewStatement(stmt)
	return ok
}

// parseOracleViewStatement 识别视图相关语句，返回 (动作描述, ok)。
// 覆盖：CREATE [OR REPLACE] [FORCE|NO FORCE] [EDITIONABLE|NONEDITIONABLE] [MATERIALIZED] VIEW、
// ALTER VIEW / ALTER MATERIALIZED VIEW、DROP [MATERIALIZED] VIEW。
func parseOracleViewStatement(stmt string) (string, bool) {
	clean := strings.TrimSpace(stmt)
	lower := strings.ToLower(clean)
	if strings.HasPrefix(lower, "create ") {
		rest := strings.TrimSpace(clean[len("create"):])
		// 逐个消费修饰词
		for _, kw := range []string{"or replace", "force", "no force", "editioning", "editionable", "noneditionable"} {
			if strings.HasPrefix(strings.ToLower(rest), kw+" ") {
				rest = strings.TrimSpace(rest[len(kw)+1:])
			}
		}
		if strings.HasPrefix(strings.ToLower(rest), "materialized view") {
			return "CREATE MATERIALIZED VIEW", true
		}
		if strings.HasPrefix(strings.ToLower(rest), "view") {
			return "CREATE VIEW", true
		}
		return "", false
	}
	if strings.HasPrefix(lower, "alter view") || strings.HasPrefix(lower, "alter materialized view") {
		return "ALTER VIEW", true
	}
	if strings.HasPrefix(lower, "drop materialized view") {
		return "DROP MATERIALIZED VIEW", true
	}
	if strings.HasPrefix(lower, "drop view") {
		return "DROP VIEW", true
	}
	return "", false
}

// consumeOracleQualified 消费一个 [schema.]name（双引号感知），返回剩余文本。
// 语义与 PG 的 consumePGQualified 一致，区别在归一化为大写。
func consumeOracleQualified(s string) (schema, name, rest string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", s, false
	}
	// 标识符的结束位置：引号外的第一个空白或 '('
	end := len(s)
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case !inQuote && (c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '('):
			end = i
			i = len(s)
		}
	}
	if inQuote {
		return "", "", s, false
	}
	namePart := strings.TrimSpace(s[:end])
	if namePart == "" {
		return "", "", s, false
	}
	id, okID := parseOracleQualifiedName(namePart)
	if !okID {
		return "", "", s, false
	}
	return id.Schema, id.Name, strings.TrimSpace(s[end:]), true
}

// trimOracleStmtPrefix 判断语句是否以指定关键字序列开头（大小写不敏感），返回剩余文本。
// 与 trimPGStmtPrefix 语义一致：关键字后必须是非标识符字符。
func trimOracleStmtPrefix(stmt string, keywords ...string) (string, bool) {
	rest := strings.TrimSpace(stmt)
	for _, kw := range keywords {
		if !hasPGKeywordPrefix(rest, kw) {
			return "", false
		}
		rest = strings.TrimSpace(rest[len(kw):])
	}
	return rest, true
}
