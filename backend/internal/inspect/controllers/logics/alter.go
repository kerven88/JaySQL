package logics

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/parser"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// LogicAlterTableIsExist
func LogicAlterTableIsExist(v *traverses.TraverseAlterTableIsExist, r *controllers.RuleHint) {
	// 检查表是否存在。表不存在时 ALTER 必然失败（MySQL 1146 / PG 42P01），
	// 但后续规则仍照常执行（不再置 IsBreak），避免一条缺失结论掩盖其他违规。
	r.MergeAlter = v.Table
	if msg, err := dao.CheckIfTableExists(v.Table, r.DB); err != nil {
		r.WarnFatal(
			"obj_tbl_must_exist",
			controllers.CategoryObjectMissing,
			v.Table,
			"① 核对对象名拼写与大小写；② 确认是否选错了目标库/模式；③ 若该对象应由本工单创建，请调整语句顺序；④ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
			msg,
		)
	}
}

// LogicAlterTableTiDBMerge
func LogicAlterTableTiDBMerge(v *traverses.TraverseAlterTiDBMerge, r *controllers.RuleHint) {
	// 检查TiDBMergeAlter
	dbVersionIns := process.DbVersion{Version: r.KV.Get("dbVersion").(string)}
	if !r.InspectParams.ENABLE_TIDB_MERGE_ALTER_TABLE && dbVersionIns.IsTiDB() {
		if v.SpecsLen > 1 {
			r.Warn(fmt.Sprintf("表`%s`包含多个 ALTER 操作：TiDB 不支持单条 `ALTER TABLE` 执行多个更改，请拆分执行", v.Table))
		}
	}
}

// LogicAlterTableDropColsOrIndexes
func LogicAlterTableDropColsOrIndexes(v *traverses.TraverseAlterTableDropColsOrIndexes, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}

	if len(v.Cols) > 0 {
		if !r.InspectParams.ENABLE_DROP_COLS {
			// 不允许drop列
			r.Warn(fmt.Sprintf("表`%s`不允许执行 `DROP COLUMN`", v.Table))
		} else {
			// 检查drop的列是否存在（不存在时 DROP COLUMN 必然失败：MySQL 1091 / PG 42704）
			for _, col := range v.Cols {
				if !utils.IsContain(vAudit.Cols, col) {
					r.WarnFatal(
						"obj_col_must_exist",
						controllers.CategoryObjectMissing,
						col,
						"① 核对列名拼写与大小写；② 该列可能已被其他工单删除，请先同步实例元数据再重新检查；③ 若需幂等删除，请确认目标库当前真实结构",
						fmt.Sprintf("表`%s`中要 DROP 的列`%s`不存在", v.Table, col),
					)
				}
			}
		}
		if !r.InspectParams.ENABLE_DROP_PRIMARYKEY {
			// 不允许drop主键
			for _, pri := range vAudit.PrimaryKeys {
				if utils.IsContain(v.Cols, pri) {
					r.Warn(fmt.Sprintf("表`%s`不允许 DROP 主键列`%s`", v.Table, pri))
				}
			}
		}
	}
	if len(vAudit.Indexes) > 0 {
		if !r.InspectParams.ENABLE_DROP_INDEXES {
			// 不允许drop索引
			r.Warn(fmt.Sprintf("表`%s`不允许执行 `DROP INDEX`", v.Table))
		} else {
			// 检查drop的索引是否存在（不存在时 DROP INDEX 必然失败：MySQL 1091 / PG 42704）
			for _, index := range v.Indexes {
				if !utils.IsContain(vAudit.Indexes, index) {
					r.WarnFatal(
						"obj_idx_must_exist",
						controllers.CategoryObjectMissing,
						index,
						"① 核对索引名拼写与大小写；② 该索引可能已被其他工单删除，请先同步实例元数据再重新检查",
						fmt.Sprintf("表`%s`中要 DROP 的索引`%s`不存在", v.Table, index),
					)
				}
			}
		}
	}
}

// LogicAlterTableDropTiDBColWithCoveredIndex
func LogicAlterTableDropTiDBColWithCoveredIndex(v *traverses.TraverseAlterTableDropTiDBColWithCoveredIndex, r *controllers.RuleHint) {
	// TiDB目前不支持删除主键列或组合索引相关列。
	dbVersionIns := process.DbVersion{Version: r.KV.Get("dbVersion").(string)}
	if !dbVersionIns.IsTiDB() {
		return
	}
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableRedundantIndexes{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}

	for _, col := range v.Cols {
		for _, item := range vAudit.Redundant.IndexesCols {
			if len(item.Cols) > 1 {
				if utils.IsContain(item.Cols, col) {
					r.Warn(fmt.Sprintf("表`%s`删除列`%s`失败：该列被复合索引 %s(%s) 覆盖；TiDB 不支持直接删除（请先删除复合索引`%s`）", v.Table, col, item.Index, strings.Join(item.Cols, ","), item.Index))
				}
			}
		}
	}

}

// LogicAlterTableOptions
func LogicAlterTableOptions(v *traverses.TraverseAlterTableOptions, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table
	v.Type = "alter"
	v.TableOptions.InspectParams = r.InspectParams

	// 行格式
	var rowFormat string = v.RowFormat
	if v.RowFormat == "DEFAULT" {
		rowFormat = r.KV.Get("innodbDefaultRowFormat").(string)
	}
	v.TableOptions.RowFormat = rowFormat

	ruleMap := map[string]string{
		"CheckTableEngine":     "limit_table_storage_engine",
		"CheckTableComment":    "limit_tbl_must_comment",
		"CheckTableCharset":    "limit_tbl_charset",
		"CheckTableCollation":  "limit_tbl_collation",
		"CheckInnoDBRowFormat": "limit_innodb_row_format",
	}
	fns := []struct {
		name string
		fn   func() error
	}{
		{"CheckTableEngine", v.CheckTableEngine},
		{"CheckTableComment", v.CheckTableComment},
		{"CheckTableCharset", v.CheckTableCharset},
		{"CheckTableCollation", v.CheckTableCollation},
		{"CheckInnoDBRowFormat", v.CheckInnoDBRowFormat},
	}
	for _, item := range fns {
		if err := item.fn(); err != nil {
			r.CurrentRuleKey = ruleMap[item.name]
			r.Warn(err.Error())
			r.CurrentRuleKey = ""
		}
	}
}

// LogicAlterTableColCharset
// 规则 no_set_col_的charset / no_set_col_的collate：不能显式设置列的字符集/校对集。
// traverse 只收集显式设置了 charset 或 collate 的列，这里按设置项分别告警并归级。
func LogicAlterTableColCharset(v *traverses.TraverseAlterTableColCharset, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	for _, col := range v.Cols {
		if r.InspectParams.CHECK_COLUMN_CHARSET && col.Charset != "" {
			r.CurrentRuleKey = "no_column_charset"
			r.Warn(fmt.Sprintf("列`%s`不能显式指定字符集`%s`（表`%s`）", col.Column, col.Charset, col.Table))
			r.CurrentRuleKey = ""
		}
		if r.InspectParams.CHECK_COLUMN_COLLATION && col.Collate != "" {
			r.CurrentRuleKey = "no_column_collate"
			r.Warn(fmt.Sprintf("列`%s`不能显式指定校对集`%s`（表`%s`）", col.Column, col.Collate, col.Table))
			r.CurrentRuleKey = ""
		}
	}
}

// LogicAlterTableColZerofill
// 规则 col_禁用_zerofill_属性：字段禁用 ZEROFILL 属性。
func LogicAlterTableColZerofill(v *traverses.TraverseAlterTableColZerofill, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if !r.InspectParams.DISABLE_COLUMN_ZEROFILL {
		return
	}
	r.MergeAlter = v.Table

	for _, col := range v.Cols {
		r.CurrentRuleKey = "column_zerofill_forbidden"
		r.Warn(fmt.Sprintf("列`%s`禁用 ZEROFILL 属性（表`%s`）", col, v.Table))
		r.CurrentRuleKey = ""
	}
}

// LogicAlterTableAddColAfter
func LogicAlterTableAddColAfter(v *traverses.TraverseAlterTableAddColAfter, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}

	// 将add的列和原始表的列放到一起
	v.Cols = append(v.Cols, vAudit.Cols...)

	// 检查AFTER的列是否存在
	for _, pCol := range v.PositionCols {
		if !utils.IsContain(v.Cols, pCol) {
			r.WarnFatal(
				"obj_col_must_exist",
				controllers.CategoryObjectMissing,
				pCol,
				"① 核对 AFTER 后的列名拼写；② 若该列由本工单同一条 ALTER 新增，请把 ADD COLUMN 放在引用它的语句之前",
				fmt.Sprintf("表`%s`中 AFTER 指定的列`%s`不存在", v.Table, pCol),
			)
		}
	}
}

// LogicAlterTableAddColOptions
func LogicAlterTableAddColOptions(v *traverses.TraverseAlterTableAddColOptions, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	ruleMap := map[string]string{
		"CheckColumnLength":           "limit_col_name_len",
		"CheckColumnIdentifer":        "limit_col_name_case",
		"CheckColumnIdentiferKeyword": "col_name_no_keyword",
		"CheckColumnComment":          "limit_col_must_have_comment",
		"CheckColumnCharToVarchar":    "limit_char_type_col_len",
		"CheckColumnMaxVarcharLength": "limit_varchar_type_col_len",
		"CheckColumnFloatDouble":      "limit_col_no_partial_data_type",
		"CheckColumnNotAllowedType":   "limit_col_no_partial_data_type",
		"CheckColumnNotNull":          "limit_col_not_null",
		"CheckColumnDefaultValue":     "limit_col_must_have_default",
	}
	for _, col := range v.Cols {
		col.InspectParams = r.InspectParams
		fns := []struct {
			name string
			fn   func() error
		}{
			{"CheckColumnLength", col.CheckColumnLength},
			{"CheckColumnIdentifer", col.CheckColumnIdentifer},
			{"CheckColumnIdentiferKeyword", col.CheckColumnIdentiferKeyword},
			{"CheckColumnComment", col.CheckColumnComment},
			{"CheckColumnCharToVarchar", col.CheckColumnCharToVarchar},
			{"CheckColumnMaxVarcharLength", col.CheckColumnMaxVarcharLength},
			{"CheckColumnFloatDouble", col.CheckColumnFloatDouble},
			{"CheckColumnNotAllowedType", col.CheckColumnNotAllowedType},
			{"CheckColumnNotNull", col.CheckColumnNotNull},
			{"CheckColumnDefaultValue", col.CheckColumnDefaultValue},
		}
		for _, item := range fns {
			if err := item.fn(); err != nil {
				r.CurrentRuleKey = ruleMap[item.name]
				r.Warn(err.Error())
				r.CurrentRuleKey = ""
			}
		}
	}
}

// LogicAlterTableAddPrimaryKey
func LogicAlterTableAddPrimaryKey(v *traverses.TraverseAlterTableAddPrimaryKey, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}

	if len(vAudit.PrimaryKeys) > 0 && len(v.Cols) > 0 {
		var newPrimaryKeys []string
		for _, col := range v.Cols {
			newPrimaryKeys = append(newPrimaryKeys, fmt.Sprintf("`%s`", col))
		}
		r.Warn(fmt.Sprintf("表`%s`已经存在主键`%s`，增加主键%+s失败", v.Table, strings.Join(vAudit.PrimaryKeys, ","), strings.Join(newPrimaryKeys, ",")))
	}

	// 主键列类型白名单校验（规则 limit_pk_col_type）：新增主键列的类型必须落在白名单内。
	// 白名单未配置 => 不限制类型。类型来源优先级：本次 ADD COLUMN 的列定义 > 表结构中已存在的列；
	// 二者都取不到则无法判定，跳过不报（与 PG 引擎行为一致）。
	if r.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT && len(r.InspectParams.PRIMARYKEY_SUPPORT_TYPES) > 0 {
		for _, col := range v.Cols {
			tp, ok := v.ColTypes[col]
			if !ok {
				tp, ok = vAudit.ColTypes[col]
			}
			if !ok {
				continue
			}
			pk := process.PrimaryKey{Table: v.Table, Column: col, Tp: tp, InspectParams: r.InspectParams}
			if err := pk.CheckBigint(); err != nil {
				r.WarnWithRule("limit_pk_col_type", err.Error())
			}
		}
	}
}

// LogicAlterTableAddColRepeatDefine
func LogicAlterTableAddColRepeatDefine(v *traverses.TraverseAlterTableAddColRepeatDefine, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 查找重复的列名
	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableColsRepeatDefine{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	v.Cols = append(v.Cols, vAudit.Cols...)

	if ok, data := utils.IsRepeat(v.Cols); ok {
		r.Warn(fmt.Sprintf("发现重复的列名`%s`（表`%s`）", strings.Join(data, ","), v.Table))
	}
}

// LogicAlterTableAddIndexPrefix
func LogicAlterTableAddIndexPrefix(v *traverses.TraverseAlterTableAddIndexPrefix, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 检查唯一索引前缀、如唯一索引必须以uniq_为前缀
	var indexPrefixCheck process.IndexPrefix = v.Prefix
	indexPrefixCheck.InspectParams = r.InspectParams
	if r.InspectParams.CHECK_UNIQ_INDEX_PREFIX {
		if err := indexPrefixCheck.CheckUniquePrefix(); err != nil {
			r.WarnWithRule("limit_uniq_idx_name_fmt", err.Error())
		}
	}
	// 检查二级索引前缀、如二级索引必须以idx_为前缀
	if r.InspectParams.CHECK_SECONDARY_INDEX_PREFIX {
		if err := indexPrefixCheck.CheckSecondaryPrefix(); err != nil {
			r.WarnWithRule("limit_normal_idx_name_fmt", err.Error())
		}
	}
	// 检查全文索引前缀、如全文索引必须以full_为前缀
	if r.InspectParams.CHECK_FULLTEXT_INDEX_PREFIX {
		if err := indexPrefixCheck.CheckFulltextPrefix(); err != nil {
			r.Warn(err.Error())
		}
	}
	// 索引必须显式命名（规则 index_must_have_name）
	if r.InspectParams.CHECK_INDEX_NAME_REQUIRED {
		if err := indexPrefixCheck.CheckUniqueIndexNameRequired(); err != nil {
			r.WarnWithRule("index_must_have_name", err.Error())
		}
		if err := indexPrefixCheck.CheckSecondaryIndexNameRequired(); err != nil {
			r.WarnWithRule("index_must_have_name", err.Error())
		}
		if err := indexPrefixCheck.CheckFulltextIndexNameRequired(); err != nil {
			r.WarnWithRule("index_must_have_name", err.Error())
		}
	}
}

// LogicAlterTableAddIndexCount
func LogicAlterTableAddIndexCount(v *traverses.TraverseAlterTableAddIndexCount, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableIndexesCount{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	v.Number.Number += vAudit.Number.Number
	// 检查二级索引数量
	var indexNumberCheck process.IndexNumber = v.Number
	indexNumberCheck.InspectParams = r.InspectParams
	if err := indexNumberCheck.CheckSecondaryIndexesNum(); err != nil {
		r.WarnWithRule("limit_tbl_idx_count", err.Error())
	}
	if err := indexNumberCheck.CheckPrimaryKeyColsNum(); err != nil {
		r.WarnWithRule("limit_pk_column_count", err.Error())
	}
	if err := indexNumberCheck.CheckSecondaryIndexColsNum(); err != nil {
		r.WarnWithRule("limit_single_index_column_count", err.Error())
	}
}

// LogicAlterTableAddConstraint
func LogicAlterTableAddConstraint(v *traverses.TraverseAlterTableAddConstraint, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table
	if !r.InspectParams.ENABLE_FOREIGN_KEY && v.IsForeignKey {
		// 禁止使用外键
		r.Warn(fmt.Sprintf("表`%s`禁止定义外键", v.Table))
	}
}

// LogicAlterTableAddIndexRepeatDefine
func LogicAlterTableAddIndexRepeatDefine(v *traverses.TraverseAlterTableAddIndexRepeatDefine, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableIndexesRepeatDefine{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	v.Indexes = append(v.Indexes, vAudit.Indexes...)
	if ok, data := utils.IsRepeat(v.Indexes); ok {
		r.Warn(fmt.Sprintf("发现重复的索引`%s`（表`%s`）", strings.Join(data, ","), v.Table))
	}
}

// LogicAlterTableRedundantIndexes
func LogicAlterTableRedundantIndexes(v *traverses.TraverseAlterTableRedundantIndexes, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}

	if r.InspectParams.ENABLE_REDUNDANT_INDEX {
		return
	}

	r.MergeAlter = v.Table

	// 检查索引,建索引时,指定的列必须存在、索引中的列,不能重复、索引名不能重复
	// 不能有重复的索引,包括(索引名不同,字段相同；冗余索引,如(a),(a,b))
	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableRedundantIndexes{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	v.Redundant.Cols = vAudit.Redundant.Cols
	// 用于检查alter table xxx add `col1` xxx,add index idx_col1(`col1`)
	v.Redundant.Cols = append(v.Redundant.Cols, v.AddCols...)
	// 用于检查alter table xxx drop `col2`,add index idx_col2(`col2`);
	v.Redundant.Cols = utils.RemoveElements(v.Redundant.Cols, v.DropCols)
	v.Redundant.Indexes = append(v.Redundant.Indexes, vAudit.Redundant.Indexes...)
	v.Redundant.IndexesCols = append(v.Redundant.IndexesCols, vAudit.Redundant.IndexesCols...)
	var redundantIndexCheck process.RedundantIndex = v.Redundant
	// 归级到「限制冗余索引」规则，使 must 级别能阻断提交审批
	r.CurrentRuleKey = "limit_tbl_redundant_idx"
	if err := redundantIndexCheck.CheckRepeatCols(); err != nil {
		r.Warn(err.Error())
	}
	if err := redundantIndexCheck.CheckRepeatColsWithDiffIndexes(); err != nil {
		r.Warn(err.Error())
	}
	if err := redundantIndexCheck.CheckRedundantColsWithDiffIndexes(); err != nil {
		r.Warn(err.Error())
	}
	r.CurrentRuleKey = ""
}

// LogicAlterTableDisabledIndexes
func LogicAlterTableDisabledIndexes(v *traverses.TraverseAlterTableDisabledIndexes, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableDisabledIndexes{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	v.DisabledIndexes.Cols = append(v.DisabledIndexes.Cols, vAudit.DisabledIndexes.Cols...)
	v.DisabledIndexes.IndexesCols = append(v.DisabledIndexes.IndexesCols, vAudit.DisabledIndexes.IndexesCols...)

	// BLOB/TEXT类型不能设置为索引
	var indexTypesCheck process.DisabledIndexes = v.DisabledIndexes
	if err := indexTypesCheck.Check(); err != nil {
		r.Warn(err.Error())
	}
}

// LogicAlterTableModifyColOptions
func LogicAlterTableModifyColOptions(v *traverses.TraverseAlterTableModifyColOptions, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableColsOptions{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	var vCols []string
	for _, vCol := range vAudit.Cols {
		vCols = append(vCols, vCol.Column)
	}
	// 检查modify的列是否存在
	for _, col := range v.Cols {
		if !utils.IsContain(vCols, col.Column) {
			r.WarnFatal(
				"obj_col_must_exist",
				controllers.CategoryObjectMissing,
				col.Column,
				"① 核对列名拼写与大小写；② 若是新增列请改用 ADD COLUMN；③ 该列可能已被其他工单删除，请先同步实例元数据再重新检查",
				fmt.Sprintf("列`%s`不存在（表`%s`）", col.Column, v.Table),
			)
		}
	}
	// 检查modify的列是否进行列类型变更
	for _, col := range v.Cols {
		for _, vCol := range vAudit.Cols {
			if err := process.CheckColsTypeChanged(col, vCol, r.InspectParams, r.KV, "modify", v.Table); err != nil {
				// 显式携带 rule_key：该检查在规范目录中没有独立规则条目（能力类参数控制），
				// 不带 key 会被工单侧文案相似度匹配误挂到「限制主键列的数据类型」等名字相近的规则上
				r.WarnWithRule("column_type_change", err.Error())
			}
		}
	}
	// 检查列
	for _, col := range v.Cols {
		col.InspectParams = r.InspectParams
		fns := []func() error{
			col.CheckColumnComment,
			col.CheckColumnCharToVarchar,
			col.CheckColumnMaxVarcharLength,
			col.CheckColumnNotAllowedType,
			col.CheckColumnNotNull,
			col.CheckColumnDefaultValue,
		}
		for _, fn := range fns {
			if err := fn(); err != nil {
				r.Warn(err.Error())
			}
		}
	}

	// 若被修改的列是表主键列，其新类型必须落在白名单内（规则 limit_pk_col_type）；空白名单不限制。
	if r.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT && len(r.InspectParams.PRIMARYKEY_SUPPORT_TYPES) > 0 {
		for _, col := range v.Cols {
			if !utils.IsContain(vAudit.PrimaryKeys, col.Column) {
				continue
			}
			pk := process.PrimaryKey{Table: v.Table, Column: col.Column, Tp: col.Tp, InspectParams: r.InspectParams}
			if err := pk.CheckBigint(); err != nil {
				r.WarnWithRule("limit_pk_col_type", err.Error())
			}
		}
	}
}

// LogicAlterTableChangeColOptions
func LogicAlterTableChangeColOptions(v *traverses.TraverseAlterTableChangeColOptions, r *controllers.RuleHint) {
	// CHANGE 既可以“改列定义”，也可以“改列名 + 改列定义”。
	// 这里对“改列名”做显式控制：默认不允许（需要打开 ENABLE_COLUMN_CHANGE_COLUMN_NAME）。
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table
	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableColsOptions{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	var vCols []string
	for _, vCol := range vAudit.Cols {
		vCols = append(vCols, vCol.Column)
	}
	// 判断change操作是否为修改列名操作
	for _, col := range v.Cols {
		if col.Column != col.OldColumn {
			// 不允许change列名操作
			if !r.InspectParams.ENABLE_COLUMN_CHANGE_COLUMN_NAME && len(v.Cols) > 0 {
				r.Warn(fmt.Sprintf("表`%s`禁止通过 `CHANGE` 修改列名：`%s` -> `%s`", v.Table, col.OldColumn, col.Column))
				return
			}
			// 允许change列名操作,检查change的列是否存在
			if !utils.IsContain(vCols, col.OldColumn) {
				r.WarnFatal(
					"obj_col_must_exist",
					controllers.CategoryObjectMissing,
					col.OldColumn,
					"① 核对原列名拼写与大小写；② 该列可能已被其他工单删除或改名，请先同步实例元数据再重新检查",
					fmt.Sprintf("表`%s`中原列`%s`不存在，无法执行 `CHANGE`", v.Table, col.OldColumn),
				)
			}
			if utils.IsContain(vCols, col.Column) {
				r.WarnFatal(
					"obj_col_must_not_exist",
					controllers.CategoryObjectConflict,
					col.Column,
					"① 换一个新列名；② 若目标就是覆盖，请先单独提交删除/改名的工单，避免 CHANGE 产生列冲突",
					fmt.Sprintf("表`%s`中新列`%s`已存在，`CHANGE` 可能导致列名冲突", v.Table, col.Column),
				)
			}
		} else {
			// 允许change列名操作,检查change的列是否存在
			if !utils.IsContain(vCols, col.OldColumn) {
				r.WarnFatal(
					"obj_col_must_exist",
					controllers.CategoryObjectMissing,
					col.OldColumn,
					"① 核对原列名拼写与大小写；② 该列可能已被其他工单删除或改名，请先同步实例元数据再重新检查",
					fmt.Sprintf("表`%s`中原列`%s`不存在，无法执行 `CHANGE`", v.Table, col.OldColumn),
				)
			}
		}
	}

	// 检查change的列是否进行列类型变更
	for _, col := range v.Cols {
		for _, vCol := range vAudit.Cols {
			if col.OldColumn == vCol.Column {
				if err := process.CheckColsTypeChanged(col, vCol, r.InspectParams, r.KV, "change", v.Table); err != nil {
					r.WarnWithRule("column_type_change", err.Error())
				}
			}
		}
	}

	// 检查列
	for _, col := range v.Cols {
		col.InspectParams = r.InspectParams
		fns := []func() error{
			col.CheckColumnComment,
			col.CheckColumnCharToVarchar,
			col.CheckColumnMaxVarcharLength,
			col.CheckColumnFloatDouble,
			col.CheckColumnNotAllowedType,
			col.CheckColumnNotNull,
			col.CheckColumnDefaultValue,
		}
		for _, fn := range fns {
			if err := fn(); err != nil {
				r.Warn(err.Error())
			}
		}
	}

	// 若被变更的列是表主键列，其新类型必须落在白名单内（规则 limit_pk_col_type）；空白名单不限制。
	// CHANGE 以旧列名判定主键归属（表结构里的主键仍是旧名），类型取本次语句给出的新类型。
	if r.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT && len(r.InspectParams.PRIMARYKEY_SUPPORT_TYPES) > 0 {
		for _, col := range v.Cols {
			if !utils.IsContain(vAudit.PrimaryKeys, col.OldColumn) {
				continue
			}
			pk := process.PrimaryKey{Table: v.Table, Column: col.Column, Tp: col.Tp, InspectParams: r.InspectParams}
			if err := pk.CheckBigint(); err != nil {
				r.WarnWithRule("limit_pk_col_type", err.Error())
			}
		}
	}
}

// LogicAlterTableRenameIndex
func LogicAlterTableRenameIndex(v *traverses.TraverseAlterTableRenameIndex, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	if !r.InspectParams.ENABLE_INDEX_RENAME {
		r.Warn(fmt.Sprintf("表`%s`禁止执行 `RENAME INDEX`", v.Table))
		return
	}
	// 判断是否重复定义
	var oldIndexes, newIndexes []string
	for _, item := range v.Indexes {
		oldIndexes = append(oldIndexes, item.OldIndex)
		newIndexes = append(newIndexes, item.NewIndex)
	}
	if ok, data := utils.IsRepeat(oldIndexes); ok {
		r.Warn(fmt.Sprintf("表`%s`的 `RENAME INDEX` 操作中发现重复的旧索引名：`%s`", v.Table, strings.Join(data, ",")))
	}
	if ok, data := utils.IsRepeat(newIndexes); ok {
		r.Warn(fmt.Sprintf("表`%s`的 `RENAME INDEX` 操作中发现重复的新索引名：`%s`", v.Table, strings.Join(data, ",")))
	}
	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	// 判断表是否存在
	if v.Table != vAudit.Table {
		r.WarnFatal(
			"obj_tbl_must_exist",
			controllers.CategoryObjectMissing,
			v.Table,
			"① 核对表名拼写与大小写；② 确认是否选错了目标库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
			fmt.Sprintf("表`%s`不存在，无法执行 `RENAME INDEX`", v.Table),
		)
	}
	for _, item := range v.Indexes {
		// 判断old_index_name是否存在
		if !utils.IsContain(vAudit.Indexes, item.OldIndex) {
			r.WarnFatal(
				"obj_idx_must_exist",
				controllers.CategoryObjectMissing,
				item.OldIndex,
				"① 核对原索引名拼写与大小写；② 该索引可能已被其他工单删除，请先同步实例元数据再重新检查",
				fmt.Sprintf("表`%s`中旧索引`%s`不存在，无法重命名", v.Table, item.OldIndex),
			)
		}
		// 判断new_index_name是否存在
		if utils.IsContain(vAudit.Indexes, item.NewIndex) {
			r.WarnFatal(
				"obj_idx_must_not_exist",
				controllers.CategoryObjectConflict,
				item.NewIndex,
				"① 换一个新索引名；② 若目标就是覆盖，请先单独提交 DROP INDEX 工单，避免重命名冲突",
				fmt.Sprintf("表`%s`中新索引名`%s`已存在，无法重命名为该名称", v.Table, item.NewIndex),
			)
		}
		// 检查索引名合法性（规范中无「索引重命名」专有规则，统一按普通索引名格式规则归级）
		if r.InspectParams.CHECK_INDEX_IDENTIFIER {
			if ok := utils.IsMatchPattern(utils.NamePattern, item.NewIndex); !ok {
				r.WarnWithRule("limit_normal_idx_name_fmt", fmt.Sprintf("表`%s`中索引名`%s`不符合命名规范：仅允许匹配正则`%s`", v.Table, item.NewIndex, utils.NamePattern))
			}
		}
	}
}

// LogicAlterTableRenameTblName
func LogicAlterTableRenameTblName(v *traverses.TraverseAlterTableRenameTblName, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table
	if !r.InspectParams.ENABLE_RENAME_TABLE_NAME {
		r.Warn(fmt.Sprintf("表`%s`禁止执行 `RENAME TABLE`", v.Table))
		return
	}
	// 判断新表是否存在
	if _, err := dao.CheckIfTableExists(v.NewTblName, r.DB); err == nil {
		r.Warn(fmt.Sprintf("目标表名`%s`已存在，无法将表`%s`重命名为该名称", v.NewTblName, v.Table))
		return
	}
}

// LogicAlterTableInnodbLargePrefix
func LogicAlterTableInnodbLargePrefix(v *traverses.TraverseAlterTableInnodbLargePrefix, r *controllers.RuleHint) {
	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.LargePrefix.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableColsTp{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	// 将提前到的字段类型复制给索引字段结构体
	var tmpLargePrefix process.LargePrefix
	tmpLargePrefix.Table = v.LargePrefix.Table
	tmpLargePrefix.Charset = vAudit.Charset
	for _, i := range v.LargePrefix.LargePrefixIndexColsMaps {
		var tmpKeys []process.LargePrefixIndexPartSpecification = i.Keys
		for index, ii := range i.Keys {
			for _, jj := range vAudit.Cols {
				if strings.EqualFold(jj.Column, ii.Column) {
					tmpKeys[index].Tp = jj.Tp
					tmpKeys[index].Flen = jj.Flen
					tmpKeys[index].Decimal = jj.Decimal
					tmpKeys[index].Charset = jj.Charset
				}
			}
		}
		tmpLargePrefix.LargePrefixIndexColsMaps = append(tmpLargePrefix.LargePrefixIndexColsMaps, process.LargePrefixIndexColsMap{Name: i.Name, Keys: tmpKeys})
	}

	var largePrefix process.LargePrefix = tmpLargePrefix
	if err := largePrefix.Check(r.KV); err != nil {
		r.Warn(err.Error())
	}
}

// LogicAlterTableInnoDBRowSize
func LogicAlterTableInnoDBRowSize(v *traverses.TraverseAlterTableInnoDBRowSize, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	r.MergeAlter = v.Table

	// 获取db表结构
	audit, err := dao.ShowCreateTable(v.Table, r.DB, r.KV)
	if err != nil {
		r.Warn(err.Error())
		return
	}
	// 解析获取的db表结构
	vAudit := &traverses.TraverseCreateTableInnoDBRowSize{}
	switch audit := audit.(type) {
	case *parser.Audit:
		(audit.TiStmt[0]).Accept(vAudit)
	}
	// 拷贝，如果Column不存在append，Column存在，重新赋值
	for _, v := range v.ColsMaps {
		if index, ok := func(v process.PartSpecification) (int, bool) {
			for i, vv := range vAudit.ColsMaps {
				if strings.EqualFold(v.Column, vv.Column) {
					return i, true
				}
			}
			return 0, false
		}(v); !ok {
			vAudit.InnoDBRowSize.ColsMaps = append(vAudit.ColsMaps, v)
		} else {
			vAudit.ColsMaps[index] = v
		}

	}
	var rowSizeTooLarge process.InnoDBRowSize = vAudit.InnoDBRowSize
	if err := rowSizeTooLarge.Check(r.KV); err != nil {
		r.Warn(err.Error())
	}
}
