package logics

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// LogicCreateTableIsExist
func LogicCreateTableIsExist(v *traverses.TraverseCreateTableIsExist, r *controllers.RuleHint) {
	// 建表前置校验：表已存在时，本次 CREATE 必然失败（MySQL 1050 / PG 42P07）。
	//
	// 这里不再设置 IsBreak —— 让后续规则（主键/字符集/索引等）照常执行，
	// 否则「表已存在」会掩盖同一条语句上的其他违规，用户改完表名才看到下一批问题。
	// 归级到 create_tbl_must_not_exist 规则，使它在规范中可配置，且默认按「必须改进」阻断。
	if msg, err := dao.CheckIfTableExists(v.Table, r.DB); err == nil {
		r.WarnFatal(
			"create_tbl_must_not_exist",
			controllers.CategoryObjectConflict,
			v.Table,
			"① 更换表名（如 `"+v.Table+"_new`）；② 需要重复执行请使用 `CREATE TABLE IF NOT EXISTS`；③ 确需覆盖请先单独提交 `DROP TABLE` 工单（数据将丢失，需另行评估）",
			msg,
		)
	}
}

// LogicCreateTableOptions
func LogicCreateTableOptions(v *traverses.TraverseCreateTableOptions, r *controllers.RuleHint) {
	v.Type = "create"
	v.TableOptions.InspectParams = r.InspectParams
	// 规则 key 使用「去掉数据库前缀的后缀」，便于在 MySQL/PostgreSQL 之间复用。
	// 没有对应标准规则的检查（如表名长度）不设置 key，由预审阶段的文案匹配兜底。
	ruleMap := map[string]string{
		"CheckTableEngine":                 "limit_table_storage_engine",
		"CheckTablePartition":              "limit_tbl_partition",
		"CheckTableComment":                "limit_tbl_must_comment",
		"CheckTableCharset":                "limit_tbl_charset",
		"CheckTableCollation":              "limit_tbl_collation",
		"CheckTableAutoIncrementInitValue": "limit_create_table_auto_inc_init",
	}
	fns := []struct {
		name string
		fn   func() error
	}{
		{"CheckTableLength", v.CheckTableLength},
		{"CheckTableIdentifer", v.CheckTableIdentifer},
		{"CheckTableIdentiferKeyword", v.CheckTableIdentiferKeyword},
		{"CheckTableEngine", v.CheckTableEngine},
		{"CheckTablePartition", v.CheckTablePartition},
		{"CheckTableComment", v.CheckTableComment},
		{"CheckTableCharset", v.CheckTableCharset},
		{"CheckTableCollation", v.CheckTableCollation},
		{"CheckTableAutoIncrementInitValue", v.CheckTableAutoIncrementInitValue},
	}
	for _, item := range fns {
		if err := item.fn(); err != nil {
			r.CurrentRuleKey = ruleMap[item.name]
			r.Warn(err.Error())
			r.CurrentRuleKey = ""
		}
	}
}

// LogicCreateTablePrimaryKey
func LogicCreateTablePrimaryKey(v *traverses.TraverseCreateTablePrimaryKey, r *controllers.RuleHint) {
	// 主键规则：没有主键会导致 UPDATE/DELETE 定位困难、复制/分库分表不友好。
	if r.InspectParams.CHECK_TABLE_PRIMARY_KEY {
		r.CurrentRuleKey = "table_must_have_pk"
		if len(v.PrimaryKeys) == 0 {
			r.Warn(fmt.Sprintf("表`%s`必须定义主键", v.Table))
		}
		if len(v.PrimaryKeys) > 1 {
			r.Warn(fmt.Sprintf("表`%s`主键定义异常：只能定义一个主键", v.Table))
		}
		r.CurrentRuleKey = ""
	}
	// 检查主键列的类型/属性是否符合规范。
	for _, item := range v.PrimaryKeys {
		var p process.PrimaryKey = item
		p.InspectParams = r.InspectParams
		fns := []struct {
			name string
			fn   func() error
		}{
			{"limit_pk_col_type", p.CheckBigint},
			{"limit_pk_col_must_auto_inc", p.CheckAutoIncrement},
			// 注：不检查主键列 NOT NULL——MySQL 主键列天然隐式 NOT NULL（即使建表语句
			// 未显式书写），此检查只会产生语义性误报，且无对应规则可控制。
		}
		for _, f := range fns {
			if err := f.fn(); err != nil {
				r.CurrentRuleKey = f.name
				r.Warn(err.Error())
				r.CurrentRuleKey = ""
			}
		}
	}
}

// LogicCreateTableConstraint
func LogicCreateTableConstraint(v *traverses.TraverseCreateTableConstraint, r *controllers.RuleHint) {
	if !r.InspectParams.ENABLE_FOREIGN_KEY && v.IsForeignKey {
		// 外键会带来额外的锁/性能开销与跨系统耦合，默认不允许。
		r.Warn(fmt.Sprintf("表`%s`禁止定义外键", v.Table))
	}
}

// LogicCreateTableAuditCols
func LogicCreateTableAuditCols(v *traverses.TraverseCreateTableAuditCols, r *controllers.RuleHint) {
	if r.InspectParams.CHECK_TABLE_AUDIT_TYPE_COLUMNS {
		// 审计字段：建议至少包含“创建时间/更新时间”两类字段。
		// 这里不强制字段名，但会检查是否具备典型的默认值/自动更新时间语义。
		var colsOptionsArray []string
		for _, item := range v.AuditCols {
			for _, value := range item {
				colsOptionsArray = append(colsOptionsArray, value)
			}
		}
		if !utils.IsContain(colsOptionsArray, "DEFAULT CURRENT_TIMESTAMP") {
			r.Warn(fmt.Sprintf("表`%s`缺少创建时间类审计字段：建议包含 `DEFAULT CURRENT_TIMESTAMP`（如 `created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP`）", v.Table))
		}
		if !utils.IsContain(colsOptionsArray, "DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP") {
			r.Warn(fmt.Sprintf("表`%s`缺少更新时间类审计字段：建议包含 `DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP`（如 `updated_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP`）", v.Table))
		}
	}
}

// LogicCreateTableColsOptions
func LogicCreateTableColsOptions(v *traverses.TraverseCreateTableColsOptions, r *controllers.RuleHint) {
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

	// 表字段数量上限（规则 limit_tbl_col_的count，0 表示不限制）
	// 用 TotalCols（含主键列），不能用 len(v.Cols)——后者已剔除主键列，会少算。
	if limit := r.InspectParams.MAX_TABLE_COLUMNS; limit > 0 && v.TotalCols > limit {
		r.WarnWithRule("limit_table_column_count",
			fmt.Sprintf("表`%s`字段数量超出限制（max = %d，当前 %d），请拆分表或精简字段",
				v.Table, limit, v.TotalCols))
	}
}

// LogicCreateTableColsRepeatDefine
func LogicCreateTableColsRepeatDefine(v *traverses.TraverseCreateTableColsRepeatDefine, r *controllers.RuleHint) {
	// 列名重复通常意味着建表语句拷贝/合并出错。
	if ok, data := utils.IsRepeat(v.Cols); ok {
		r.Warn(fmt.Sprintf("表`%s`存在重复列名：`%s`", v.Table, strings.Join(data, ",")))
	}
	// 规则 tbl_需要include_某些col：表必须包含指定的列（TABLE_REQUIRED_COLUMNS 为空表示规则未开启）。
	// 该 traverse 的 Cols 为建表语句的全量列名（小写），适合做必含列比对。
	if len(r.InspectParams.TABLE_REQUIRED_COLUMNS) > 0 {
		r.CurrentRuleKey = "table_must_include_required_columns"
		var missing []string
		for _, need := range r.InspectParams.TABLE_REQUIRED_COLUMNS {
			name := strings.ToLower(strings.TrimSpace(need))
			if name == "" {
				continue
			}
			if !utils.IsContain(v.Cols, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			r.Warn(fmt.Sprintf("表`%s`缺少必需的列：%s", v.Table, strings.Join(missing, ", ")))
		}
		r.CurrentRuleKey = ""
	}
}

// LogicCreateTableColsCharset
// 规则 no_set_col_的charset / no_set_col_的collate：不能显式设置列的字符集/校对集。
// traverse 只收集显式设置了 charset 或 collate 的列，这里按设置项分别告警并归级。
func LogicCreateTableColsCharset(v *traverses.TraverseCreateTableColsCharset, r *controllers.RuleHint) {
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

// LogicCreateTableColsZerofill
// 规则 col_禁用_zerofill_属性：字段禁用 ZEROFILL 属性。
func LogicCreateTableColsZerofill(v *traverses.TraverseCreateTableColsZerofill, r *controllers.RuleHint) {
	if !r.InspectParams.DISABLE_COLUMN_ZEROFILL {
		return
	}
	for _, col := range v.Cols {
		r.CurrentRuleKey = "column_zerofill_forbidden"
		r.Warn(fmt.Sprintf("列`%s`禁用 ZEROFILL 属性（表`%s`）", col, v.Table))
		r.CurrentRuleKey = ""
	}
}

// LogicCreateTableIndexesPrefix
func LogicCreateTableIndexesPrefix(v *traverses.TraverseCreateTableIndexesPrefix, r *controllers.RuleHint) {
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

// LogicCreateTableIndexesCount
func LogicCreateTableIndexesCount(v *traverses.TraverseCreateTableIndexesCount, r *controllers.RuleHint) {
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

// LogicCreateTableIndexesRepeatDefine
func LogicCreateTableIndexesRepeatDefine(v *traverses.TraverseCreateTableIndexesRepeatDefine, r *controllers.RuleHint) {
	// 索引名重复会导致建表失败；字段组合重复会造成冗余索引与写入成本上升。
	if ok, data := utils.IsRepeat(v.Indexes); ok {
		r.Warn(fmt.Sprintf("表`%s`存在重复索引：`%s`", v.Table, strings.Join(data, ",")))
	}
}

// LogicCreateTableRedundantIndexes
func LogicCreateTableRedundantIndexes(v *traverses.TraverseCreateTableRedundantIndexes, r *controllers.RuleHint) {
	if r.InspectParams.ENABLE_REDUNDANT_INDEX {
		return
	}
	// 冗余索引会增加写入成本、占用更多存储，且通常不会带来查询收益。
	// 典型场景：同列重复索引、(a) 与 (a,b) 这种前缀覆盖。
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

// LogicCreateTableDisabledIndexes
func LogicCreateTableDisabledIndexes(v *traverses.TraverseCreateTableDisabledIndexes, r *controllers.RuleHint) {
	// BLOB/TEXT 等大字段不允许建索引（存储/性能成本高，且大多场景不可用）。
	var indexTypesCheck process.DisabledIndexes = v.DisabledIndexes
	if err := indexTypesCheck.Check(); err != nil {
		r.Warn(err.Error())
	}
}

// LogicCreateTableInnodbLargePrefix
func LogicCreateTableInnodbLargePrefix(v *traverses.TraverseCreateTableInnodbLargePrefix, r *controllers.RuleHint) {
	var largePrefix process.LargePrefix = v.LargePrefix
	if err := largePrefix.Check(r.KV); err != nil {
		r.Warn(err.Error())
	}
}

// LogicCreateTableInnoDBRowSize
func LogicCreateTableInnoDBRowSize(v *traverses.TraverseCreateTableInnoDBRowSize, r *controllers.RuleHint) {
	var rowSize process.InnoDBRowSize = v.InnoDBRowSize
	if err := rowSize.Check(r.KV); err != nil {
		r.Warn(err.Error())
	}
}

// LogicCreateTableInnoDBRowFormat
func LogicCreateTableInnoDBRowFormat(v *traverses.TraverseCreateTableOptions, r *controllers.RuleHint) {
	// 行格式影响行存储与溢出页策略；这里只允许白名单中的 ROW_FORMAT。
	var rowFormat string = v.RowFormat
	if v.RowFormat == "DEFAULT" {
		rowFormat = r.KV.Get("innodbDefaultRowFormat").(string)
	}
	// 行格式白名单为空表示不检查
	if len(r.InspectParams.INNODB_ROW_FORMAT) > 0 && !utils.IsContain(r.InspectParams.INNODB_ROW_FORMAT, rowFormat) {
		r.Warn(fmt.Sprintf("表`%s`的 ROW_FORMAT=%s 不在允许列表中（允许：%s）", v.Table, rowFormat, strings.Join(r.InspectParams.INNODB_ROW_FORMAT, ",")))
	}
}
