package checker

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// Oracle DML 规则实现：UPDATE / DELETE / INSERT 的文本级结构分析。
// 通用助手（stripPGStringsAndComments / pgWordTokens / pgInsertShapes /
// pgJoinMissingOn / pgDMLJoinTableCount / pgWhereInMaxElements 等）与 Oracle 的
// 单引号/双引号/括号语义一致，直接复用（见 pg_dml.go 文件头说明）。
//
// Oracle 特有口径：
//   - UPDATE/DELETE 不支持 LIMIT 语法（命中即报告，语句执行必然失败）；
//   - 标识符归一为大写（列名比对按 UPPER）。

// oracleSetItem UPDATE SET 子句的单列赋值
type oracleSetItem struct {
	Column   string // 列名（大写，已去前缀）
	Prefixed bool   // 是否带表前缀（t.col）
	Raw      string // 原始左侧文本
}

// oracleDMLAnalysis DML 语句的文本级结构分析
type oracleDMLAnalysis struct {
	Kind               string // insert / update / delete
	Table              string
	TableSchema        string
	HasWhere           bool
	WhereClause        string // WHERE 之后的条件文本（供 COUNT；无 WHERE 为空）
	JoinMissingOn      bool
	JoinTableCount     int
	HasLimit           bool // 是否出现 LIMIT 子句（Oracle 不支持）
	MaxInElements      int
	UpdateSet          []oracleSetItem
	UpdateMultiTable   bool
	InsertValueRows    int
	InsertExplicitCols int
	InsertValueCols    []int
	InsertColNames     []string // 显式字段列表（大写；未指定为 nil）
	OK                 bool
}

// analyzeOracleDML 解析 UPDATE/DELETE/INSERT 的关键结构（不解析表达式）
func analyzeOracleDML(stmt string) oracleDMLAnalysis {
	res := oracleDMLAnalysis{}
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(clean)
	trimmed := strings.TrimSpace(lower)

	var tablePart string
	switch {
	case strings.HasPrefix(trimmed, "update"):
		res.Kind = "update"
		tablePart = strings.TrimSpace(clean[len("update"):])
	case strings.HasPrefix(trimmed, "delete"):
		res.Kind = "delete"
		rest := strings.TrimSpace(clean[len("delete"):])
		if !strings.HasPrefix(strings.ToLower(rest), "from") {
			return res
		}
		tablePart = strings.TrimSpace(rest[len("from"):])
	case strings.HasPrefix(trimmed, "insert"):
		res.Kind = "insert"
		rest := strings.TrimSpace(clean[len("insert"):])
		if !strings.HasPrefix(strings.ToLower(rest), "into") {
			return res
		}
		tablePart = strings.TrimSpace(rest[len("into"):])
	default:
		return res
	}

	// 表名：取到第一个空白或左括号为止
	fields := strings.Fields(tablePart)
	if len(fields) == 0 {
		return res
	}
	nameToken := fields[0]
	if i := strings.IndexByte(nameToken, '('); i > 0 {
		nameToken = nameToken[:i]
	}
	if id, ok := parseOracleQualifiedName(strings.Trim(nameToken, `"`)); ok {
		res.Table = id.Name
		res.TableSchema = id.Schema
	}

	whereIdx := indexOfKeyword(clean, "where")
	res.HasWhere = whereIdx >= 0
	if res.HasWhere {
		// WHERE 条件原文：到 order by 为止（COUNT 用；供预审阶段做影响行数估算）
		after := clean[whereIdx+len("where"):]
		if obIdx := indexOfKeyword(after, "order by"); obIdx >= 0 {
			after = after[:obIdx]
		}
		res.WhereClause = strings.TrimSpace(after)
	}

	if res.Kind != "insert" {
		res.JoinMissingOn = pgJoinMissingOn(clean)
		res.JoinTableCount = pgDMLJoinTableCount(clean, res.Kind)
		res.MaxInElements = pgWhereInMaxElements(clean)
		res.HasLimit = pgLimitRowsRe.MatchString(clean)
		if res.Kind == "update" {
			res.UpdateSet = oracleUpdateSetItems(clean)
			// 多表判定：主表 + JOIN/USING/逗号
			res.UpdateMultiTable = res.JoinTableCount > 1
		}
	} else {
		res.InsertValueRows = pgInsertValueRows(clean)
		res.InsertExplicitCols, res.InsertValueCols = pgInsertShapes(clean)
		res.InsertColNames = oracleInsertColumnNames(clean)
	}
	res.OK = true
	return res
}

// oracleUpdateSetItems 提取 UPDATE SET 子句的列赋值（大写、保留前缀信息）
func oracleUpdateSetItems(clean string) []oracleSetItem {
	lower := strings.ToLower(clean)
	setIdx := strings.Index(lower, " set ")
	if setIdx < 0 {
		return nil
	}
	rest := clean[setIdx+len(" set "):]
	end := len(rest)
	for _, kw := range []string{" where ", " order by "} {
		if i := strings.Index(strings.ToLower(rest), kw); i >= 0 && i < end {
			end = i
		}
	}
	var items []oracleSetItem
	depth := 0
	var cur strings.Builder
	flush := func() {
		item := strings.TrimSpace(cur.String())
		cur.Reset()
		if item == "" {
			return
		}
		if i := strings.Index(item, "="); i >= 0 {
			left := strings.TrimSpace(item[:i])
			prefixed := strings.Contains(left, ".")
			if i := strings.LastIndex(left, "."); i >= 0 {
				left = left[i+1:]
			}
			left = strings.Trim(left, `"`)
			if left != "" {
				items = append(items, oracleSetItem{Column: strings.ToUpper(left), Prefixed: prefixed, Raw: left})
			}
		}
	}
	for i := 0; i < len(rest[:end]); i++ {
		switch rest[i] {
		case '(':
			depth++
			cur.WriteByte(rest[i])
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(rest[i])
		case ',':
			if depth == 0 {
				flush()
				continue
			}
			cur.WriteByte(rest[i])
		default:
			cur.WriteByte(rest[i])
		}
	}
	flush()
	return items
}

// oracleInsertColumnNames 提取 INSERT 的显式字段列表（大写；未指定返回 nil）
func oracleInsertColumnNames(clean string) []string {
	valIdx := indexOfKeyword(clean, "values")
	if valIdx < 0 {
		return nil
	}
	head := clean[:valIdx]
	lowerHead := strings.ToLower(head)
	if intoIdx := strings.Index(lowerHead, "into"); intoIdx >= 0 {
		i := intoIdx + len("into")
		for i < len(head) && head[i] != '(' && head[i] != ' ' && head[i] != '\t' && head[i] != '\n' && head[i] != '\r' {
			i++
		}
		for i < len(head) && (head[i] == ' ' || head[i] == '\t' || head[i] == '\n' || head[i] == '\r') {
			i++
		}
		if i < len(head) && head[i] == '(' {
			if closeIdx := pgMatchingParen(head, i); closeIdx > 0 {
				var cols []string
				for _, item := range splitTopLevel(head[i+1:closeIdx], ',') {
					item = strings.TrimSpace(item)
					if item == "" {
						continue
					}
					cols = append(cols, strings.ToUpper(strings.Trim(item, `"`)))
				}
				return cols
			}
		}
	}
	return nil
}

// oracleInsertNullValueColumns 找出显式字段列表中、值被写为 NULL 的列（大写）。
// 仅分析第一行 VALUES（多行场景下后续行以第一行口径代表——与「建议指定字段列表」
// 等规则同一粒度，静态分析不做逐行深挖）。
func oracleInsertNullValueColumns(clean string) []string {
	valIdx := indexOfKeyword(clean, "values")
	if valIdx < 0 {
		return nil
	}
	cols := oracleInsertColumnNames(clean)
	if len(cols) == 0 {
		return nil // 未指定字段列表时无法定位列，跳过
	}
	rest := clean[valIdx+len("values"):]
	for i := 0; i < len(rest); i++ {
		if rest[i] != '(' {
			continue
		}
		closeIdx := pgMatchingParen(rest, i)
		if closeIdx < 0 {
			return nil
		}
		var nullCols []string
		items := splitTopLevel(rest[i+1:closeIdx], ',')
		for pos, item := range items {
			if pos >= len(cols) {
				break
			}
			if strings.EqualFold(strings.TrimSpace(item), "NULL") {
				nullCols = append(nullCols, cols[pos])
			}
		}
		return nullCols
	}
	return nil
}

// ---------- 规则执行 ----------

func (c *oracleInspectContext) inspectDML(stmt string) []controllers.SummaryItem {
	res := analyzeOracleDML(stmt)
	if !res.OK {
		return nil
	}
	var items []controllers.SummaryItem

	switch res.Kind {
	case "update", "delete":
		items = append(items, c.inspectUpdateDelete(res, stripPGStringsAndComments(stmt))...)
	case "insert":
		items = append(items, c.inspectInsert(res, stmt)...)
	}
	return items
}

// inspectUpdateDelete UPDATE/DELETE 规则（clean 为去字符串/注释后的语句文本）
func (c *oracleInspectContext) inspectUpdateDelete(res oracleDMLAnalysis, clean string) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	table := res.Table

	if c.params.DML_MUST_HAVE_WHERE && !res.HasWhere {
		items = append(items, oracleSummary("update_delete_stmt_suggest_where",
			"%s 语句必须指定 WHERE 条件（表`%s`）", strings.ToUpper(res.Kind), table))
	}
	if c.params.CHECK_DML_JOIN_WITH_ON && res.JoinMissingOn {
		items = append(items, oracleSummary("update_delete_stmt_join_on_check",
			"JOIN 语句必须包含 ON 子句（表`%s`）", table))
	}
	// Oracle 不支持 UPDATE/DELETE 的 LIMIT 语法（ORA-00933）：出现即报告，执行必然失败
	if res.HasLimit {
		items = append(items, oracleSummary("limit_update_delete_data_size",
			"Oracle 不支持 %s 语句的 LIMIT 子句（表`%s`），语句执行必然失败；如需限制处理量请使用 ROWNUM 过滤",
			strings.ToUpper(res.Kind), table))
	}
	if c.params.MAX_UPDATE_DELETE_JOIN_TABLES > 0 && res.JoinTableCount > c.params.MAX_UPDATE_DELETE_JOIN_TABLES {
		items = append(items, oracleSummary("limit_update_delete_join_table_count",
			"%s 语句关联表数过多：最多允许 %d 张表（含主表），当前 %d 张（表`%s`）",
			strings.ToUpper(res.Kind), c.params.MAX_UPDATE_DELETE_JOIN_TABLES, res.JoinTableCount, table))
	}
	if c.params.MAX_WHERE_IN_ELEMENTS > 0 && res.MaxInElements > c.params.MAX_WHERE_IN_ELEMENTS {
		items = append(items, oracleSummary("limit_where_in_element_count",
			"WHERE 条件中 `IN` 子句元素过多：最多允许 %d 个，当前 %d 个（表`%s`）",
			c.params.MAX_WHERE_IN_ELEMENTS, res.MaxInElements, table))
	}
	if res.Kind == "update" {
		items = append(items, c.inspectUpdateOnly(res, clean)...)
	}

	// 影响行数估算：对同条件做 COUNT(*)（规则 limit_update_delete_affected_rows）。
	// 连库失败 / 无 WHERE（无 WHERE 已被上一条规则拦截）时跳过。
	if c.params.MAX_AFFECTED_ROWS > 0 && res.HasWhere && strings.TrimSpace(res.WhereClause) != "" &&
		strings.TrimSpace(table) != "" {
		meta := c.metaResolverFor(res.TableSchema)
		if n, err := meta.CountWhere(table, res.WhereClause); err == nil && n > int64(c.params.MAX_AFFECTED_ROWS) {
			items = append(items, oracleSummary("limit_update_delete_affected_rows",
				"%s 语句影响行数（约 %d）超过上限 %d（表`%s`），请缩小 WHERE 条件范围或拆分批次",
				strings.ToUpper(res.Kind), n, c.params.MAX_AFFECTED_ROWS, table))
		}
	}

	// 目标表/字段存在性（连库，失败即跳过）
	items = append(items, c.inspectDMLObjectExists(res)...)
	return items
}

// inspectUpdateOnly 仅 UPDATE 执行的规则（clean 为去字符串/注释后的语句文本）
func (c *oracleInspectContext) inspectUpdateOnly(res oracleDMLAnalysis, clean string) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	table := res.Table
	bareCols := make([]string, 0, len(res.UpdateSet))
	prefixedCount := 0
	for _, item := range res.UpdateSet {
		bareCols = append(bareCols, item.Column)
		if item.Prefixed {
			prefixedCount++
		}
	}

	// SET 多个列之间的分隔符误用 AND（规则 update_stmt_set_separator_check）
	if c.params.CHECK_UPDATE_SET_SEPARATOR {
		if setIdx := strings.Index(strings.ToLower(clean), " set "); setIdx >= 0 {
			after := clean[setIdx+len(" set "):]
			if whereIdx := indexOfKeyword(after, "where"); whereIdx >= 0 {
				after = after[:whereIdx]
			}
			if oracleTopLevelKeyword(after, " and ") {
				items = append(items, oracleSummary("update_stmt_set_separator_check",
					"UPDATE 语句 SET 子句多个列之间应使用逗号分隔，不允许使用 AND（表`%s`）", table))
			}
		}
	}

	// 多表 UPDATE 时 SET 列必须带表前缀（规则 update_multi_table_column_prefix_check）
	if c.params.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX && res.UpdateMultiTable {
		for _, item := range res.UpdateSet {
			if !item.Prefixed {
				items = append(items, oracleSummary("update_multi_table_column_prefix_check",
					"UPDATE 多表关联时 SET 的列必须指定表前缀（列`%s`，表`%s`）", item.Column, table))
			}
		}
	}

	// 建议同步更新「修改时间」列（规则 update_stmt_suggest_update_time_column）
	if strings.TrimSpace(c.params.UPDATE_TIME_COLUMNS) != "" {
		updated := false
		for _, col := range bareCols {
			for _, want := range strings.Split(strings.ToLower(c.params.UPDATE_TIME_COLUMNS), ",") {
				if strings.TrimSpace(want) != "" && strings.EqualFold(col, strings.TrimSpace(want)) {
					updated = true
				}
			}
		}
		if !updated {
			items = append(items, oracleSummary("update_stmt_suggest_update_time_column",
				"UPDATE 语句建议同步更新表上的修改时间列（%s）（表`%s`）", c.params.UPDATE_TIME_COLUMNS, table))
		}
	}
	// 不建议更新「创建时间」列（规则 update_stmt_avoid_update_create_time_column）
	if strings.TrimSpace(c.params.AVOID_UPDATE_COLUMNS) != "" {
		for _, col := range bareCols {
			for _, avoid := range strings.Split(strings.ToLower(c.params.AVOID_UPDATE_COLUMNS), ",") {
				if strings.TrimSpace(avoid) != "" && strings.EqualFold(col, strings.TrimSpace(avoid)) {
					items = append(items, oracleSummary("update_stmt_avoid_update_create_time_column",
						"UPDATE 语句不建议更新表上的创建时间列（%s）：创建时间属于数据元信息、具备不可变更属性，更新会破坏数据审计追溯能力（表`%s`）",
						strings.TrimSpace(avoid), table))
				}
			}
		}
	}

	// 更新主键检测（规则 update_stmt_check_updates_pk）：连库取主键列，失败跳过
	if c.params.CHECK_UPDATE_UPDATES_PK && len(bareCols) > 0 {
		meta := c.metaResolverFor(res.TableSchema)
		if pkCols, err := meta.TablePKColumns(table); err == nil && len(pkCols) > 0 {
			for _, item := range res.UpdateSet {
				for _, pk := range pkCols {
					if strings.EqualFold(item.Column, pk) {
						items = append(items, oracleSummary("update_stmt_check_updates_pk",
							"UPDATE 语句不允许更新主键列`%s`（表`%s`）", item.Column, table))
						break
					}
				}
			}
		}
	}
	return items
}

// inspectDMLObjectExists UPDATE/DELETE/INSERT 的目标表与字段存在性（连库，失败即跳过）
func (c *oracleInspectContext) inspectDMLObjectExists(res oracleDMLAnalysis) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	table := strings.TrimSpace(res.Table)
	if table == "" {
		return items
	}
	meta := c.metaResolverFor(res.TableSchema)

	switch res.Kind {
	case "update":
		// 目标表存在性 + SET 列存在性（规则 update_stmt_check_column_exists）。
		// 与 INSERT 的存在性检查共用 CHECK_INSERT_OBJECT_EXISTS 开关（同一「目标表/字段存在性」能力族）。
		if !c.params.CHECK_INSERT_OBJECT_EXISTS {
			return items
		}
		if exists, err := meta.TableExists(table); err == nil && !exists {
			items = append(items, pgObjectMissing("update_stmt_check_column_exists", table,
				"① 核对表名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认表是否在其它 schema 下（需 schema.表名 限定）",
				"表`%s`不存在", table))
			return items // 表都不存在，列级检查没有意义
		}
		if len(res.UpdateSet) > 0 {
			if colCount, err := meta.TableColumnCount(table); err == nil && colCount > 0 {
				for _, item := range res.UpdateSet {
					if exists, err := meta.ColumnExists(table, item.Column); err == nil && !exists {
						items = append(items, pgObjectMissing("update_stmt_check_column_exists", item.Column,
							"① 核对列名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认列是否已删除",
							"表`%s`上不存在列`%s`", table, item.Column))
					}
				}
			}
		}
	case "delete":
		// 目标表存在性（规则 delete_stmt_check_table_exists）
		if !c.params.CHECK_DELETE_TABLE_EXISTS {
			return items
		}
		if exists, err := meta.TableExists(table); err == nil && !exists {
			items = append(items, pgObjectMissing("delete_stmt_check_table_exists", table,
				"① 核对表名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认表是否在其它 schema 下（需 schema.表名 限定）",
				"表`%s`不存在", table))
		}
	case "insert":
		// 目标表存在性 + 显式字段存在性（规则 insert_stmt_check_column_exists）
		if !c.params.CHECK_INSERT_OBJECT_EXISTS {
			return items
		}
		if exists, err := meta.TableExists(table); err == nil && !exists {
			items = append(items, pgObjectMissing("insert_stmt_check_column_exists", table,
				"① 核对表名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认表是否在其它 schema 下（需 schema.表名 限定）；③ 确认是否已被删除",
				"表`%s`不存在", table))
			return items
		}
		for _, col := range res.InsertColNames {
			if exists, err := meta.ColumnExists(table, col); err == nil && !exists {
				items = append(items, pgObjectMissing("insert_stmt_check_column_exists", col,
					"① 核对列名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认列是否已删除",
					"表`%s`上不存在列`%s`", table, col))
			}
		}
	}
	return items
}

// inspectInsert INSERT 规则
func (c *oracleInspectContext) inspectInsert(res oracleDMLAnalysis, stmt string) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	table := res.Table
	clean := stripPGStringsAndComments(stmt)

	if c.params.MAX_INSERT_ROWS > 0 && res.InsertValueRows > c.params.MAX_INSERT_ROWS {
		items = append(items, oracleSummary("limit_insert_values_row_count",
			"单条 INSERT 语句插入行数超出限制（max = %d，当前 %d）（表`%s`）",
			c.params.MAX_INSERT_ROWS, res.InsertValueRows, table))
	}
	if c.params.CHECK_INSERT_SPECIFY_COLUMNS && res.InsertExplicitCols == 0 && res.InsertValueRows > 0 {
		items = append(items, oracleSummary("insert_stmt_specify_columns",
			"INSERT 语句建议指定字段列表（表`%s`）", table))
	}
	// 字段名不能重复（规则 insert_stmt_col_name_no_dup）
	if c.params.CHECK_INSERT_COLUMN_NO_DUP {
		seen := make(map[string]bool, len(res.InsertColNames))
		for _, col := range res.InsertColNames {
			if seen[col] {
				items = append(items, oracleSummary("insert_stmt_col_name_no_dup",
					"INSERT 语句字段名`%s`重复（表`%s`）", col, table))
				break
			}
			seen[col] = true
		}
	}
	// 字段列表和值列表数量要匹配（规则 insert_stmt_columns_match_values）
	if c.params.CHECK_INSERT_COLUMNS_MATCH_VALUES && len(res.InsertValueCols) > 0 {
		if res.InsertExplicitCols > 0 {
			for i, got := range res.InsertValueCols {
				if got == res.InsertExplicitCols {
					continue
				}
				items = append(items, oracleSummary("insert_stmt_columns_match_values",
					"INSERT 语句字段列表有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配（表`%s`）",
					res.InsertExplicitCols, i+1, got, table))
				break
			}
		} else if cnt, err := c.metaResolverFor(res.TableSchema).TableColumnCount(table); err == nil && cnt > 0 {
			for i, got := range res.InsertValueCols {
				if got == cnt || got == 0 {
					continue
				}
				items = append(items, oracleSummary("insert_stmt_columns_match_values",
					"INSERT 语句未指定字段列表，表`%s`有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配",
					table, cnt, i+1, got))
				break
			}
		}
	}
	// 不建议使用 SYSDATE（规则 insert_stmt_avoid_sysdate_func）：
	// Oracle 的 SYSDATE 返回 DATE（含时分秒），写入 DATE 列会引入隐式截断歧义
	if c.params.CHECK_INSERT_AVOID_SYSDATE {
		if valIdx := indexOfKeyword(clean, "values"); valIdx >= 0 {
			tokens := pgWordTokens(strings.ToLower(clean[valIdx:]))
			for _, tk := range tokens {
				if tk == "sysdate" || tk == "current_date" {
					items = append(items, oracleSummary("insert_stmt_avoid_sysdate_func",
						"INSERT 语句不建议使用 SYSDATE/CURRENT_DATE 函数：返回 DATE 类型含时分秒，建议由应用层显式传入时间值（表`%s`）", table))
					break
				}
			}
		}
	}
	// 不允许为 NOT NULL 列插入 NULL 值（规则 insert_stmt_not_null_column_null_value）：
	// 静态判定「显式字段列表 + 显式 NULL 值」，再连库确认该列是否 NOT NULL（失败跳过）
	if c.params.CHECK_INSERT_NOT_NULL_COLUMN_NULL {
		for _, col := range oracleInsertNullValueColumns(clean) {
			if notNull, err := c.metaResolverFor(res.TableSchema).ColumnNotNullable(table, col); err == nil && notNull {
				items = append(items, oracleSummary("insert_stmt_not_null_column_null_value",
					"INSERT 语句不允许为 NOT NULL 列`%s`插入 NULL 值（表`%s`）", col, table))
			}
		}
	}
	return items
}

// ---------- 小助手 ----------

// oracleTopLevelKeyword 判断文本顶层（括号深度 0）是否出现关键字
func oracleTopLevelKeyword(s, kw string) bool {
	depth := 0
	lower := strings.ToLower(s)
	for i := 0; i+len(kw) <= len(lower); i++ {
		switch lower[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth != 0 {
			continue
		}
		if lower[i:i+len(kw)] != kw {
			continue
		}
		// 词边界：kw 的首/尾字符本身是词字符时才需要检查相邻字符
		// （kw 形如 " and " 时尾字符是空格，天然保证右边界，不能再检查后一字符）
		if isPGWordRune(rune(kw[0])) && i > 0 && isPGWordRune(rune(lower[i-1])) {
			continue
		}
		if last := kw[len(kw)-1]; isPGWordRune(rune(last)) && i+len(kw) < len(lower) && isPGWordRune(rune(lower[i+len(kw)])) {
			continue
		}
		return true
	}
	return false
}
