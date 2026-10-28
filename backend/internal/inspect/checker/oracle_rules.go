package checker

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"

	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"
)

// Oracle 规范审核引擎
//
// 判定来源与 MySQL / PG 引擎完全一致：实例绑定的 SQL 开发规范（经
// BuildInspectParamsForInstance 映射为 config.InspectParams）。差别只在实现方式
// ——Oracle 语句不使用 TiDB AST，与 PG 同样走「语法感知拆分 + 轻量文本解析」。
//
// Oracle 特有口径（与 MySQL/PG 的差异点）：
//   - 未加引号的标识符折叠为「大写」，因此表名/列名大小写规则默认 upper；
//   - 列/表注释没有内联语法，只能由工单内独立的 COMMENT ON 语句提供；
//   - 自增列是 IDENTITY 列，自增初值对应 START WITH；
//   - 索引名在 schema 内唯一（与 PG 一致，与 MySQL 表内唯一不同）。
//
// 规则 key 使用「去掉数据库前缀的后缀」形式（与两套既有引擎一致），
// 由 orders/services 的 resolveInspectSeverity 关联回规范中的具体规则级别。

// oracleInspectContext 单次预审的上下文
type oracleInspectContext struct {
	params  config.InspectParams
	sqlText string
	schema  string // 工单目标 schema（CURRENT_SCHEMA 语义）

	// 工单内 COMMENT ON 收集：table -> column -> true（column 为空表示表注释）
	colComments map[string]map[string]bool
	// 工单内新增索引：table -> 索引列表
	indexByTable map[string][]*oracleIndexDef
	// 元信息探针：nil 时走真实库查询；单测可注入桩实现（见 oracle_object_rules.go）
	meta oracleMetaResolver

	// conn 元信息探测的连接信息（service_name 缺失时对象类规则自动跳过，不阻断预审）
	conn *oracleConnMeta
	// dbConn 懒连接缓存（同一预审只建一次连接）
	dbConn *sql.DB

	// lastIdentityCols 当前建表语句中的 IDENTITY 列名（limit_pk_col_must_auto_inc 判定用），
	// 每条建表语句解析后重置
	lastIdentityCols []string
}

// oracleSummary 构造一条审核项
func oracleSummary(ruleKey, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:   LevelWarn,
		RuleKey: ruleKey,
		Message: fmt.Sprintf(format, args...),
	}
}

// runOracleInspect Oracle 工单规范预审入口
func (s *SyntaxInspectService) runOracleInspect() ([]ReturnData, error) {
	stmts, types, err := sqlsplit.SplitTyped(s.SqlText)
	if err != nil {
		return nil, err
	}
	ctx := &oracleInspectContext{
		params:       s.InspectParams,
		sqlText:      s.SqlText,
		schema:       s.InspectSchema,
		colComments:  map[string]map[string]bool{},
		indexByTable: map[string][]*oracleIndexDef{},
		meta:         s.oracleMeta,
	}
	// 连接信息：service_name 缺失时探针返回错误，对象类规则自动跳过（不阻断静态规则）
	if strings.TrimSpace(s.ServiceName) != "" {
		ctx.conn = &oracleConnMeta{
			User:        s.DbUser,
			Password:    s.DbPassword,
			Host:        s.DbHost,
			Port:        s.DbPort,
			ServiceName: s.ServiceName,
		}
	}
	ctx.collect(stmts)

	returnData := make([]ReturnData, 0, len(stmts))
	// 同表多条 ALTER：与两套既有引擎一致，在整段 SQL 层面汇总后给出合并建议
	var alterTables []string
	for i, stmt := range stmts {
		clean := strings.TrimSpace(sqlsplit.StripLeadingComments(stmt))
		data := ReturnData{
			FingerId: "",
			Query:    clean,
			Type:     types[i],
			Line:     s.computeLine(clean),
		}
		if act, ok := parseOracleAlterTableActions(clean); ok && strings.TrimSpace(act.Table) != "" {
			// 归并键：显式限定了「非工单 schema」时按 `schema.table` 区分（Oracle 对象名
			// 归一为大写，比较同样用大写）；限定到工单 schema 或未限定时一律用表名，
			// 避免 `APP.T` 与 `T` 被当成两张表而漏报。
			key := act.Table
			if act.Schema != "" && !strings.EqualFold(act.Schema, ctx.schema) {
				key = act.Schema + "." + act.Table
			}
			alterTables = append(alterTables, key)
		}
		data.Summary = ctx.inspectStatement(clean, types[i])
		returnData = append(returnData, data)
	}
	if data := ctx.mergeAlterSummary(alterTables); len(data.Summary) > 0 {
		returnData = append(returnData, data)
	}
	return returnData, nil
}

// mergeAlterSummary 同一张表出现多条 ALTER 语句时提示合并（规则 merge_multiple_alter_stmt_same_table）。
// Oracle 每条 ALTER TABLE 同样要单独获取表锁，合并可显著缩短持锁时间。
func (c *oracleInspectContext) mergeAlterSummary(tables []string) ReturnData {
	data := ReturnData{FingerId: utils.GenerateSimpleRandomString(16)}
	if !c.params.ENABLE_MERGE_ALTER_TABLE || len(tables) < 2 {
		return data
	}
	ok, repeated := utils.IsRepeat(tables)
	if !ok {
		return data
	}
	for _, table := range repeated {
		data.Summary = append(data.Summary, oracleSummary("merge_multiple_alter_stmt_same_table",
			"[Oracle数据库]表`%s`的多条ALTER操作，请合并为一条ALTER语句", table))
	}
	return data
}

// collect 预处理：收集 COMMENT ON 与 CREATE INDEX，供后续规则引用
func (c *oracleInspectContext) collect(stmts []string) {
	for _, stmt := range stmts {
		clean := strings.TrimSpace(sqlsplit.StripLeadingComments(stmt))
		switch {
		case hasPrefixKeyword(clean, "comment on"):
			table, column, ok := parseOracleCommentOn(clean)
			if !ok {
				continue
			}
			key := normalizeOracleIdent(table)
			if c.colComments[key] == nil {
				c.colComments[key] = map[string]bool{}
			}
			c.colComments[key][normalizeOracleIdent(column)] = true
		case hasPrefixKeyword(clean, "create"):
			if head := strings.ToLower(stripPGStringsAndComments(clean)); strings.Contains(head, " index ") || strings.HasPrefix(head, "create unique index") {
				def, ok := parseOracleCreateIndex(clean)
				if !ok {
					continue
				}
				key := normalizeOracleIdent(def.Table)
				c.indexByTable[key] = append(c.indexByTable[key], def)
			}
		}
	}
}

// hasPrefixKeyword 语句前缀关键字判断（大小写不敏感、词边界）
func hasPrefixKeyword(stmt, kw string) bool {
	return hasPGKeywordPrefix(strings.TrimSpace(stmt), kw)
}

// inspectStatement 对单条语句执行全部适用规则
func (c *oracleInspectContext) inspectStatement(stmt string, sqlType string) []controllers.SummaryItem {
	head := strings.ToLower(stripPGStringsAndComments(strings.TrimSpace(stmt)))
	switch {
	case strings.HasPrefix(head, "create table"):
		// 准入闸门：CTAS / CTL 命中即给出语句级结论
		if rejected := c.gateOracleCreateTable(stmt); len(rejected) > 0 {
			return rejected
		}
		return c.inspectCreateTable(stmt)
	case strings.HasPrefix(head, "alter table"):
		return c.inspectAlterTable(stmt)
	case strings.HasPrefix(head, "create") && strings.Contains(head, " index "):
		return c.inspectCreateIndex(stmt)
	case strings.HasPrefix(head, "drop index"), strings.HasPrefix(head, "alter index"):
		// 索引对象的存在性/冲突（Oracle 的改索引名是 ALTER INDEX ... RENAME TO）
		return c.inspectIndexObject(stmt)
	case isOracleViewStatement(stmt):
		return c.inspectOracleView(stmt)
	case strings.HasPrefix(head, "comment on"):
		return nil // 已在 collect 阶段收集
	case sqlType == "DML":
		return c.inspectDML(stmt)
	default:
		return nil
	}
}

// ---------- CREATE TABLE ----------

func (c *oracleInspectContext) inspectCreateTable(stmt string) []controllers.SummaryItem {
	def, ok := parseOracleCreateTable(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	table := def.Ident.Name

	// 表名规范：Oracle 未加引号折叠为大写，大小写要求默认 upper（与 MySQL/PG 的 lower 相反），
	// 规范里配了 name_case=lower 时按 lower 检查（此时只有带双引号的小写名能通过）
	if c.params.CHECK_TABLE_IDENTIFIER {
		caseText := "大写"
		wantUpper := true
		if strings.EqualFold(strings.TrimSpace(c.params.TABLE_NAME_CASE), "lower") {
			wantUpper = false
			caseText = "小写"
		}
		if !oracleNameCaseOK(def.Ident.Name, def.Ident.Quoted, wantUpper) {
			items = append(items, oracleSummary("limit_tbl_name_case",
				"表`%s`命名不符合要求：必须为%s（Oracle 未加引号的标识符会折叠为大写，双引号包裹会保留原样）", def.Ident.Name, caseText))
		}
	}
	if c.params.CHECK_IDENTIFER_KEYWORD {
		// 与 MySQL/PG 引擎口径一致：关键字范围来自规范配置（未配置回退默认全集），
		// 判定大小写不敏感（Oracle 未加引号的标识符折叠为大写）
		if _, hit := c.params.IdentifierKeywordSet()[strings.ToUpper(table)]; hit {
			items = append(items, oracleSummary("tbl_name_no_keyword", "表`%s`命名不允许使用关键字", table))
		} else if oracleReservedWords[strings.ToUpper(table)] && len(c.params.IDENTIFIER_KEYWORDS) == 0 {
			items = append(items, oracleSummary("tbl_name_no_keyword", "表`%s`命名不允许使用关键字", table))
		}
	}

	// 执行前置：表已存在（Oracle ORA-00955，必然失败）。
	// 与 MySQL/PG 引擎一致归级到 create_tbl_must_not_exist 并标记「对象冲突」。
	if exists, err := c.metaResolverFor(def.Ident.Schema).TableExists(table); err == nil && exists {
		items = append(items, controllers.SummaryItem{
			Level:    LevelWarn,
			RuleKey:  "create_tbl_must_not_exist",
			Category: controllers.CategoryObjectConflict,
			Object:   table,
			Advice:   "① 更换表名（如 `" + table + "_NEW`）；② 确需覆盖请先单独提交 DROP TABLE 工单（数据将丢失，需另行评估）",
			Message:  fmt.Sprintf("表或视图`%s`已存在", table),
		})
	}

	// 主键（先收集 IDENTITY 列，供「主键列必须自增」判定）
	c.lastIdentityCols = c.lastIdentityCols[:0]
	for _, col := range def.Columns {
		if col.IsIdentity {
			c.lastIdentityCols = append(c.lastIdentityCols, col.Name)
		}
	}
	items = append(items, c.checkPrimaryKey(table, def.PrimaryKeys, def.Columns)...)

	// 列级规则
	for _, col := range def.Columns {
		items = append(items, c.checkColumn(table, col)...)
	}

	// 表字段数量上限（0 表示不限制）
	if c.params.MAX_TABLE_COLUMNS > 0 && len(def.Columns) > c.params.MAX_TABLE_COLUMNS {
		items = append(items, oracleSummary("limit_table_column_count",
			"表`%s`字段数量超出限制（max = %d，当前 %d），请拆分表或精简字段",
			table, c.params.MAX_TABLE_COLUMNS, len(def.Columns)))
	}

	// 表必须包含的列（列表为空表示规则未开启）
	if len(c.params.TABLE_REQUIRED_COLUMNS) > 0 {
		have := make(map[string]bool, len(def.Columns))
		for _, col := range def.Columns {
			have[normalizeOracleIdent(col.Name)] = true
		}
		var missing []string
		for _, need := range c.params.TABLE_REQUIRED_COLUMNS {
			name := normalizeOracleIdent(strings.TrimSpace(need))
			if name == "" {
				continue
			}
			if !have[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			items = append(items, oracleSummary("table_must_include_required_columns",
				"表`%s`缺少必需的列：%s", table, strings.Join(missing, ", ")))
		}
	}

	// 列注释：Oracle 无内联 COMMENT 语法，要求工单内为每列提供 COMMENT ON COLUMN
	items = append(items, c.checkColumnComments(table, def.Columns)...)

	// 表注释：同上，由独立的 COMMENT ON TABLE 语句提供
	items = append(items, c.checkTableComment(table)...)

	// 外键
	if def.HasForeignKey && !c.params.ENABLE_FOREIGN_KEY {
		items = append(items, oracleSummary("limit_tbl_no_use_fk", "表`%s`禁止定义外键", table))
	}

	// 表内唯一约束命名（判定口径与 CREATE UNIQUE INDEX 完全一致）
	for _, name := range def.UniqueNames {
		if name == "" {
			continue
		}
		if c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(name, c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, oracleSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					name, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
			}
		}
	}

	// 索引数量
	items = append(items, c.checkIndexCount(table)...)
	return items
}

// ---------- ALTER TABLE ----------

func (c *oracleInspectContext) inspectAlterTable(stmt string) []controllers.SummaryItem {
	var items []controllers.SummaryItem

	// 对象存在性/冲突（目标表、DROP/RENAME 列、改名表）：与 ADD 解析相互独立，
	// 不能因为「本语句没有 ADD 子句」就整条跳过
	items = append(items, c.checkAlterTableObjects(stmt)...)

	act, ok := parseOracleAlterTableActions(stmt)
	if !ok {
		return items
	}
	// 新增列的列级规则（与建表同口径）
	for _, col := range act.AddColumns {
		items = append(items, c.checkColumn(act.Table, col)...)
	}
	// 新增列同样需要注释（Oracle 只能通过 COMMENT ON COLUMN 提供）
	items = append(items, c.checkColumnComments(act.Table, act.AddColumns)...)
	// ADD 主键：列数限制（类型白名单需要与真实列比对，ADD 场景拿不到既有列类型，跳过）
	if len(act.AddPK) > 0 && c.params.PRIMARYKEY_MAX_KEY_PARTS > 0 && len(act.AddPK) > c.params.PRIMARYKEY_MAX_KEY_PARTS {
		items = append(items, oracleSummary("limit_pk_column_count",
			"表`%s`主键包含的列数超出限制（max = %d，当前 %d）",
			act.Table, c.params.PRIMARYKEY_MAX_KEY_PARTS, len(act.AddPK)))
	}
	// ADD UNIQUE 约束命名与列数
	for i, cols := range act.AddUniq {
		if c.params.SECONDARY_INDEX_MAX_KEY_PARTS > 0 && len(cols) > c.params.SECONDARY_INDEX_MAX_KEY_PARTS {
			items = append(items, oracleSummary("limit_single_index_column_count",
				"唯一约束包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
				c.params.SECONDARY_INDEX_MAX_KEY_PARTS, len(cols), act.Table))
		}
		if i < len(act.UniqNames) && act.UniqNames[i] != "" && c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(act.UniqNames[i], c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, oracleSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					act.UniqNames[i], utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), act.Table))
			}
		}
	}
	// 未命名的表级约束会生成系统名索引（SYS_C00xxxx），「索引必须显式命名」时提示
	if act.HasNewUnnamed && c.params.CHECK_INDEX_NAME_REQUIRED {
		items = append(items, oracleSummary("index_must_have_name",
			"索引必须显式命名（表`%s`，请使用 CONSTRAINT 指定约束名）", act.Table))
	}
	// ADD 外键
	if act.HasNewFK && !c.params.ENABLE_FOREIGN_KEY {
		items = append(items, oracleSummary("limit_tbl_no_use_fk", "表`%s`禁止定义外键", act.Table))
	}
	return items
}

// ---------- CREATE INDEX ----------

func (c *oracleInspectContext) inspectCreateIndex(stmt string) []controllers.SummaryItem {
	def, ok := parseOracleCreateIndex(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	table := def.Table

	// 索引必须显式命名：CREATE INDEX 语法本身要求名称，这里兜底拦截解析异常的匿名形态；
	// 真正的「未命名索引」来自内联/表级约束（inspectCreateTable / inspectAlterTable 处理）
	if c.params.CHECK_INDEX_NAME_REQUIRED && (!def.Explicit || def.Name == "") {
		items = append(items, oracleSummary("index_must_have_name", "索引必须显式命名（表`%s`）", table))
	} else if def.Name != "" && def.Unique {
		// 唯一索引命名：与 MySQL/PG 引擎共用 utils.MatchIndexName，避免实现漂移
		if c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(def.Name, c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, oracleSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					def.Name, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
			}
		}
	} else if def.Name != "" && c.params.CHECK_SECONDARY_INDEX_PREFIX {
		prefix := c.secondaryIndexPrefix()
		if !utils.MatchIndexName(def.Name, c.params.SECONDARY_INDEX_REGEX, prefix) {
			items = append(items, oracleSummary("limit_normal_idx_name_fmt",
				"普通索引`%s`命名不符合要求，必须%s（表`%s`）",
				def.Name, utils.IndexNameRequirementText(c.params.SECONDARY_INDEX_REGEX, prefix), table))
		}
	}

	if c.params.SECONDARY_INDEX_MAX_KEY_PARTS > 0 && len(def.Columns) > c.params.SECONDARY_INDEX_MAX_KEY_PARTS {
		items = append(items, oracleSummary("limit_single_index_column_count",
			"索引`%s`包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
			def.Name, c.params.SECONDARY_INDEX_MAX_KEY_PARTS, len(def.Columns), table))
	}

	items = append(items, c.checkIndexCount(table)...)
	items = append(items, c.checkRedundantIndexes(def)...)
	return items
}

// checkRedundantIndexes 冗余索引检查（规则 limit_tbl_redundant_idx）。
// 判定口径与 MySQL/PG 引擎对齐：重复索引 / 最左前缀覆盖。
// Oracle 的索引来自工单内的 CREATE INDEX 语句（collect 阶段按表聚合），
// 是「同一工单内」的比对，不涉及数据库已有索引（避免预审阶段连库）。
func (c *oracleInspectContext) checkRedundantIndexes(cur *oracleIndexDef) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if c.params.ENABLE_REDUNDANT_INDEX {
		return items
	}
	key := normalizeOracleIdent(cur.Table)
	group := c.indexByTable[key]
	curPos := -1
	for i, def := range group {
		if def == cur {
			curPos = i
			break
		}
	}
	if curPos < 0 {
		return items // 防御：当前语句的索引不在收集结果中，跳过
	}
	curCols := oracleNormCols(cur.Columns)
	for i, other := range group {
		if i == curPos {
			continue
		}
		otherCols := oracleNormCols(other.Columns)
		if len(otherCols) == 0 || len(curCols) == 0 {
			continue
		}
		switch {
		case len(otherCols) < len(curCols) && pgColsPrefix(curCols, otherCols):
			// other 是 cur 的最左前缀：由 cur（更宽）一侧报告
		case len(otherCols) == len(curCols) && pgColsEqual(curCols, otherCols) && i > curPos:
			// 重复索引：两语句各会比对一次，只由位置靠后的一方报告
		default:
			continue
		}
		items = append(items, oracleSummary("limit_tbl_redundant_idx",
			"表`%s`发现了冗余索引，冗余索引的字段组合为%s/%s",
			other.Table, oracleIndexLabel(cur), oracleIndexLabel(other)))
	}
	return items
}

// oracleIndexLabel 索引展示标签
func oracleIndexLabel(def *oracleIndexDef) string {
	name := def.Name
	if strings.TrimSpace(name) == "" {
		name = "(匿名)"
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(oracleNormCols(def.Columns), ","))
}

// oracleNormCols 归一化索引列名（统一大写——Oracle 未加引号标识符折叠为大写）
func oracleNormCols(cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, col := range cols {
		out = append(out, strings.ToUpper(strings.TrimSpace(col)))
	}
	return out
}

// ---------- 索引对象（DROP INDEX / ALTER INDEX RENAME） ----------

func (c *oracleInspectContext) inspectIndexObject(stmt string) []controllers.SummaryItem {
	op, ok := parseOracleIndexObjectOp(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	// 目标的 schema 限定（DROP INDEX sch.idx / ALTER INDEX sch.idx）：
	// 与 MySQL/PG 一致，语句级 schema 必须透传，否则会把别的 schema 里的同名索引判错
	schemas := oracleIndexSchemas(stmt, len(op.Names))
	for i, name := range op.Names {
		switch op.Op {
		case "DROP INDEX":
			// 目标索引必须存在（不存在则 ORA-01418，执行必然失败）
			meta := c.metaResolverFor(schemas[i])
			exists, err := meta.IndexExists(name)
			if err != nil {
				continue // 探测异常跳过，宁可漏报
			}
			if !exists {
				items = append(items, pgObjectMissing("obj_idx_must_exist", name,
					"① 核对索引名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认索引是否在其它 schema 下（需 schema.索引名 限定）；③ 确认是否已被删除",
					"索引`%s`不存在", name))
			}
		case "ALTER INDEX":
			if op.RenameTo == "" {
				continue
			}
			meta := c.metaResolverFor(schemas[0])
			exists, err := meta.IndexExists(op.RenameTo)
			if err != nil {
				continue
			}
			if exists {
				items = append(items, pgObjectConflict("obj_idx_must_not_exist", op.RenameTo,
					"① 更换索引名；② 确认同名索引是否可删除",
					"改名后的索引名`%s`已存在（改名后执行必然失败）", op.RenameTo))
			}
		}
	}
	return items
}

// oracleIndexSchemas 从 DROP INDEX / ALTER INDEX 语句中提取每个目标索引的 schema 限定
// （未限定时为空字符串，调用方按工单 schema 处理）。
func oracleIndexSchemas(stmt string, want int) []string {
	schemas := make([]string, want)
	clean := strings.TrimSpace(stmt)
	var rest string
	var ok bool
	if rest, ok = trimOracleStmtPrefix(clean, "drop", "index"); !ok {
		if rest, ok = trimOracleStmtPrefix(clean, "alter", "index"); !ok {
			return schemas
		}
	}
	i := 0
	for _, item := range splitTopLevel(rest, ',') {
		if i >= want {
			break
		}
		schema, _, _, okName := consumeOracleQualified(strings.TrimSpace(item))
		if !okName {
			continue
		}
		schemas[i] = schema
		i++
	}
	return schemas
}

// ---------- 主键 / 列级规则 ----------

// oracleTypeAliases Oracle ANSI/同义类型别名 → 归一化名称。
// 「限制列不能使用指定的数据类型」「限制主键列类型」两条规则按类型名字符串比对，
// 若不做归一，DOUBLE PRECISION / REAL / INT / DECIMAL 等别名写法会逃逸用户配置的
// 禁用列表（如禁 float 却写出 DOUBLE PRECISION）。归一设计对齐 PG 引擎的 pgTypeAliases：
// 双侧（列类型与用户配置项）都先过别名表再比对，配置里的别名同样收敛。
// 注意 binary_float / binary_double 是 Oracle 特有 IEEE 类型，不参与 FLOAT 归一。
var oracleTypeAliases = map[string]string{
	"double precision": "float", // ANSI：Oracle 中 DOUBLE PRECISION = FLOAT(126)
	"double":           "float", // 裸 DOUBLE 同属 FLOAT 家族
	"real":             "float", // ANSI：REAL = FLOAT(63)
	"integer":          "int",   // ANSI 整数写法
	"decimal":          "number", // ANSI：DECIMAL = NUMBER
	"numeric":          "number", // ANSI：NUMERIC = NUMBER
	"character":        "char",   // ANSI 字符写法
}

// oracleCanonicalType 返回类型名的归一化形态（小写、去空格、别名收敛）
func oracleCanonicalType(name string) string {
	t := strings.ToLower(strings.TrimSpace(name))
	if canon, ok := oracleTypeAliases[t]; ok {
		return canon
	}
	return t
}

func (c *oracleInspectContext) checkPrimaryKey(table string, keys []string, cols []oracleColumn) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if c.params.CHECK_TABLE_PRIMARY_KEY && len(keys) == 0 {
		items = append(items, oracleSummary("table_must_have_pk",
			"表`%s`必须定义主键（建议使用 NUMBER GENERATED ALWAYS AS IDENTITY）", table))
	}
	colTypes := make(map[string]string, len(cols))
	for _, col := range cols {
		colTypes[normalizeOracleIdent(col.Name)] = col.Type
	}
	items = append(items, c.checkPrimaryKeyType(table, keys, colTypes)...)
	return items
}

// checkPrimaryKeyType 主键列类型白名单与列数（建表 / ADD 约束共用）
func (c *oracleInspectContext) checkPrimaryKeyType(table string, keys []string, colTypes map[string]string) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if len(keys) == 0 {
		return items
	}
	if c.params.PRIMARYKEY_MAX_KEY_PARTS > 0 && len(keys) > c.params.PRIMARYKEY_MAX_KEY_PARTS {
		items = append(items, oracleSummary("limit_pk_column_count",
			"表`%s`主键包含的列数超出限制（max = %d，当前 %d）",
			table, c.params.PRIMARYKEY_MAX_KEY_PARTS, len(keys)))
	}
	// 主键列类型白名单：完全按用户配置；未配置(空) => 不限制类型。
	// 双侧先做别名归一（oracleCanonicalType）再比对，INTEGER/DOUBLE PRECISION 等
	// ANSI 写法不会逃逸白名单。
	if len(c.params.PRIMARYKEY_SUPPORT_TYPES) > 0 {
		allowed := make(map[string]bool, len(c.params.PRIMARYKEY_SUPPORT_TYPES))
		for _, t := range c.params.PRIMARYKEY_SUPPORT_TYPES {
			allowed[oracleCanonicalType(t)] = true
		}
		for _, key := range keys {
			colType := colTypes[normalizeOracleIdent(key)]
			if colType == "" {
				continue // 类型未知（如 ADD 约束场景）跳过，不做猜测
			}
			if !allowed[oracleCanonicalType(colType)] {
				items = append(items, oracleSummary("limit_pk_col_type",
					"主键列`%s`数据类型`%s`不在允许范围内（表`%s`，允许：%s）",
					key, colType, table, strings.Join(c.params.PRIMARYKEY_SUPPORT_TYPES, ",")))
			}
		}
	}
	// 主键列必须自增（Oracle：IDENTITY 列）
	if c.params.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT {
		for _, key := range keys {
			if !c.isOracleIdentityColumnByName(table, key) {
				items = append(items, oracleSummary("limit_pk_col_must_auto_inc",
					"主键列`%s`必须定义为 IDENTITY 自增列（GENERATED ALWAYS AS IDENTITY）（表`%s`）", key, table))
			}
		}
	}
	return items
}

// isOracleIdentityColumnByName 判断指定列在最近解析的建表语句中是否为 IDENTITY 列。
// 由 inspectCreateTable 在解析后填充（oracleInspectContext.lastIdentityCols）。
func (c *oracleInspectContext) isOracleIdentityColumnByName(table, column string) bool {
	for _, name := range c.lastIdentityCols {
		if normalizeOracleIdent(name) == normalizeOracleIdent(column) {
			return true
		}
	}
	return false
}

// checkColumn 列级规则（建表 / ADD 列共用）
func (c *oracleInspectContext) checkColumn(table string, col oracleColumn) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	name := col.Name

	// 列名大小写：Oracle 默认要求大写（未加引号自动折叠为大写即满足，双引号包裹保留原样）
	if c.params.CHECK_COLUMN_IDENTIFIER {
		caseText := "大写"
		wantUpper := true
		if strings.EqualFold(strings.TrimSpace(c.params.COLUMN_NAME_CASE), "lower") {
			wantUpper = false
			caseText = "小写"
		}
		if !oracleNameCaseOK(col.Name, col.Quoted, wantUpper) {
			items = append(items, oracleSummary("limit_col_name_case",
				"列`%s`命名不符合要求：必须为%s（Oracle 未加引号的标识符会折叠为大写，双引号包裹会保留原样）（表`%s`）", name, caseText, table))
		}
	}
	if c.params.CHECK_IDENTIFER_KEYWORD {
		if _, hit := c.params.IdentifierKeywordSet()[strings.ToUpper(name)]; hit {
			items = append(items, oracleSummary("col_name_no_keyword", "列`%s`命名不允许使用关键字（表`%s`）", name, table))
		} else if oracleReservedWords[strings.ToUpper(name)] && len(c.params.IDENTIFIER_KEYWORDS) == 0 {
			items = append(items, oracleSummary("col_name_no_keyword", "列`%s`命名不允许使用关键字（表`%s`）", name, table))
		}
	}

	switch col.Type {
	case "char", "nchar", "character":
		if c.params.COLUMN_MAX_CHAR_LENGTH > 0 && col.Len > c.params.COLUMN_MAX_CHAR_LENGTH {
			items = append(items, oracleSummary("limit_char_type_col_len",
				"CHAR类型字段`%s`的长度超出限制（max = %d，当前 %d）（表`%s`）",
				name, c.params.COLUMN_MAX_CHAR_LENGTH, col.Len, table))
		}
	case "varchar2", "nvarchar2":
		if c.params.MAX_VARCHAR_LENGTH > 0 && col.Len > c.params.MAX_VARCHAR_LENGTH {
			items = append(items, oracleSummary("limit_varchar_type_col_len",
				"VARCHAR2类型字段`%s`的长度超出限制（max = %d，当前 %d）（表`%s`）",
				name, c.params.MAX_VARCHAR_LENGTH, col.Len, table))
		}
	}

	// IDENTITY 列由 Oracle 隐式保证 NOT NULL，不做不可空检查
	if c.params.ENABLE_COLUMN_NOT_NULL && !col.NotNull && !col.IsIdentity {
		items = append(items, oracleSummary("limit_col_not_null", "列`%s`必须定义为`NOT NULL`（表`%s`）", name, table))
	}
	if c.params.CHECK_COLUMN_DEFAULT_VALUE && !col.HasDefault {
		items = append(items, oracleSummary("limit_col_must_have_default", "列`%s`需要设置一个默认值（表`%s`）", name, table))
	}
	// 禁用数据类型：双侧先做别名归一（oracleCanonicalType）再比对，
	// DOUBLE PRECISION / REAL / INT / DECIMAL 等 ANSI 别名写法不会逃逸禁用列表
	if c.params.CHECK_COLUMN_FORBIDDEN_TYPES && len(c.params.COLUMN_FORBIDDEN_TYPES) > 0 {
		colCanon := oracleCanonicalType(col.Type)
		for _, t := range c.params.COLUMN_FORBIDDEN_TYPES {
			if oracleCanonicalType(t) == colCanon {
				items = append(items, oracleSummary("limit_col_no_partial_data_type",
					"列`%s`不允许使用数据类型`%s`（表`%s`）", name, col.Type, table))
				break
			}
		}
	}

	// 自增列：列名必须为 ID + 自增初值（Oracle IDENTITY）
	if col.IsIdentity {
		if c.params.CHECK_AUTOINCREMENT_COL_NAME_IS_ID && !strings.EqualFold(name, "ID") {
			items = append(items, oracleSummary("limit_auto_inc_col_name_is_id",
				"自增列`%s`必须命名为`ID`（表`%s`）", name, table))
		}
		if c.params.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE && c.params.TABLE_AUTOINCREMENT_INIT_VALUE > 0 &&
			col.IdentityStart > 0 && col.IdentityStart < c.params.TABLE_AUTOINCREMENT_INIT_VALUE {
			items = append(items, oracleSummary("limit_create_table_auto_inc_init",
				"表`%s`自增列`%s`初始值不能小于 %d（当前 START WITH %d）",
				table, name, c.params.TABLE_AUTOINCREMENT_INIT_VALUE, col.IdentityStart))
		}
	}
	return items
}

// checkColumnComments 列注释检查（Oracle：工单内必须提供 COMMENT ON COLUMN）
func (c *oracleInspectContext) checkColumnComments(table string, cols []oracleColumn) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if !c.params.CHECK_COLUMN_COMMENT {
		return items
	}
	comments := c.colComments[normalizeOracleIdent(table)]
	for _, col := range cols {
		if len(comments) == 0 || !comments[normalizeOracleIdent(col.Name)] {
			items = append(items, oracleSummary("limit_col_must_have_comment",
				"列`%s`必须要有注释（表`%s`，请使用 COMMENT ON COLUMN %s.%s IS '...')", col.Name, table, table, col.Name))
		}
	}
	return items
}

// checkTableComment 表注释检查（Oracle：工单内必须提供 COMMENT ON TABLE）
func (c *oracleInspectContext) checkTableComment(table string) []controllers.SummaryItem {
	if !c.params.CHECK_TABLE_COMMENT {
		return nil
	}
	comments := c.colComments[normalizeOracleIdent(table)]
	if len(comments) == 0 || !comments[""] {
		return []controllers.SummaryItem{oracleSummary("limit_tbl_must_comment",
			"表`%s`必须要有注释（请使用 COMMENT ON TABLE %s IS '...')", table, table)}
	}
	return nil
}

// ---------- 索引数量 ----------

func (c *oracleInspectContext) checkIndexCount(table string) []controllers.SummaryItem {
	if c.params.MAX_INDEX_KEYS <= 0 {
		return nil
	}
	count := len(c.indexByTable[normalizeOracleIdent(table)])
	// 库内已有索引计数：连库失败时只用工单内索引数（宁可漏报）
	if existing, err := c.metaResolver().countExistingIndexes(table); err == nil {
		count += existing
	}
	if count > c.params.MAX_INDEX_KEYS {
		return []controllers.SummaryItem{oracleSummary("limit_tbl_idx_count",
			"表`%s`索引数量超出限制（max = %d，当前 %d）", table, c.params.MAX_INDEX_KEYS, count)}
	}
	return nil
}

func (c *oracleInspectContext) uniqueIndexPrefix() string {
	if p := strings.TrimSpace(c.params.UNQI_INDEX_PREFIX); p != "" {
		return p
	}
	return "UK_"
}

func (c *oracleInspectContext) secondaryIndexPrefix() string {
	if p := strings.TrimSpace(c.params.SECONDARY_INDEX_PREFIX); p != "" {
		return p
	}
	return "IDX_"
}

// Oracle 标识符命名正则
var (
	oracleUpperNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	oracleLowerNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// oracleNameCaseOK 标识符大小写是否符合要求（引号感知）：
//   - 要求大写：未加引号的标识符 Oracle 自动折叠为大写（天然合规）；
//     带双引号的保留原样，必须本身全大写才合规。
//   - 要求小写：未加引号的标识符必然被折叠为大写（必然违规）；
//     带双引号的保留原样，必须本身全小写才合规。
func oracleNameCaseOK(raw string, quoted bool, wantUpper bool) bool {
	if wantUpper {
		if !quoted {
			return true
		}
		return oracleUpperNamePattern.MatchString(raw)
	}
	if !quoted {
		return false
	}
	return oracleLowerNamePattern.MatchString(raw)
}
