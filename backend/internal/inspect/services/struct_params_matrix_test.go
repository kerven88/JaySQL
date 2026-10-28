package services

// 结构类规范规则「参数映射层」矩阵测试：
// 对结构目录中全部能力项的规范规则，逐条验证 applyStandardRule 在
// must / suggest / off 三种级别下的参数状态：
//   - must 与 suggest 的引擎参数必须完全一致（级别只影响订单闸门严重度，不影响引擎触发）；
//   - off 时参数回落到关闭状态；
//   - 未接入 applyStandardRule 的规则（params 无任何变化）单独记录，作为规范开关/级别不生效的依据。
//
// 结果写入 /tmp/gi_struct_params.json，由报告脚本汇总。

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/models"
)

type pmResult struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	DB      string `json:"db"`
	MustOK  bool   `json:"must_ok"`  // must 级别下参数为开启态
	EqualOK bool   `json:"equal_ok"` // must 与 suggest 参数完全一致
	OffOK   bool   `json:"off_ok"`   // off 级别下参数为关闭态
	Mapped  bool   `json:"mapped"`   // 是否接入 applyStandardRule
	Detail  string `json:"detail"`   // 失败说明 / 未映射说明
}

// mappedChecks 已接入 applyStandardRule 的规则 → 开启态断言与关闭态断言
var mappedChecks = []struct {
	key      string
	name     string
	db       string
	checkOn  func(*config.InspectParams) bool // 规则开启（must/suggest）时应满足
	checkOff func(*config.InspectParams) bool // 规则关闭（off）时应满足
}{
	{"table_must_have_pk", "表要有主键", "both",
		func(p *config.InspectParams) bool { return p.CHECK_TABLE_PRIMARY_KEY },
		func(p *config.InspectParams) bool { return !p.CHECK_TABLE_PRIMARY_KEY }},
	{"limit_tbl_must_comment", "限制表必须注释", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_TABLE_COMMENT },
		func(p *config.InspectParams) bool { return !p.CHECK_TABLE_COMMENT }},
	{"limit_create_table_auto_inc_init", "限制建表自增初始值", "mysql",
		func(p *config.InspectParams) bool {
			return p.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE && p.TABLE_AUTOINCREMENT_INIT_VALUE == 1
		},
		func(p *config.InspectParams) bool {
			return !p.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE && p.TABLE_AUTOINCREMENT_INIT_VALUE == 0
		}},
	{"limit_auto_inc_col_name_is_id", "限制自增列名字为 ID", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID },
		func(p *config.InspectParams) bool { return !p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID }},
	{"limit_auto_inc_col_is_unsigned", "限制自增列为无符号", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_AUTOINCREMENT_COL_UNSIGNED },
		func(p *config.InspectParams) bool { return !p.CHECK_AUTOINCREMENT_COL_UNSIGNED }},
	{"limit_table_storage_engine", "限制表存储引擎", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_TABLE_ENGINE },
		func(p *config.InspectParams) bool { return !p.CHECK_TABLE_ENGINE }},
	{"limit_tbl_charset", "限制表字符集", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_TABLE_CHARSET },
		func(p *config.InspectParams) bool { return !p.CHECK_TABLE_CHARSET }},
	{"limit_tbl_name_case", "限制表名大小写", "both",
		func(p *config.InspectParams) bool { return p.CHECK_TABLE_IDENTIFIER && p.TABLE_NAME_CASE == "lower" },
		func(p *config.InspectParams) bool { return !p.CHECK_TABLE_IDENTIFIER }},
	{"limit_col_name_case", "限制字段名大小写", "both",
		func(p *config.InspectParams) bool { return p.CHECK_COLUMN_IDENTIFIER && p.COLUMN_NAME_CASE == "lower" },
		func(p *config.InspectParams) bool { return !p.CHECK_COLUMN_IDENTIFIER }},
	{"tbl_name_no_keyword", "表名不能是关键字", "both",
		func(p *config.InspectParams) bool { return p.CHECK_IDENTIFER_KEYWORD },
		func(p *config.InspectParams) bool { return !p.CHECK_IDENTIFER_KEYWORD }},
	{"col_name_no_keyword", "字段名不能是关键字", "both",
		func(p *config.InspectParams) bool { return p.CHECK_IDENTIFER_KEYWORD },
		func(p *config.InspectParams) bool { return !p.CHECK_IDENTIFER_KEYWORD }},
	{"limit_col_must_have_comment", "限制列要有注释", "both",
		func(p *config.InspectParams) bool { return p.CHECK_COLUMN_COMMENT },
		func(p *config.InspectParams) bool { return !p.CHECK_COLUMN_COMMENT }},
	{"limit_col_must_have_default", "限制列要有默认值", "both",
		func(p *config.InspectParams) bool { return p.CHECK_COLUMN_DEFAULT_VALUE },
		func(p *config.InspectParams) bool { return !p.CHECK_COLUMN_DEFAULT_VALUE }},
	{"limit_col_not_null", "限制列不可空（NOT NULL)", "both",
		func(p *config.InspectParams) bool { return p.ENABLE_COLUMN_NOT_NULL },
		func(p *config.InspectParams) bool { return !p.ENABLE_COLUMN_NOT_NULL }},
	{"no_column_charset", "不能设置列的字符集", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_COLUMN_CHARSET },
		func(p *config.InspectParams) bool { return !p.CHECK_COLUMN_CHARSET }},
	{"no_column_collate", "不能设置列的校对集", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_COLUMN_COLLATION },
		func(p *config.InspectParams) bool { return !p.CHECK_COLUMN_COLLATION }},
	{"column_zerofill_forbidden", "字段禁用 Zerofill 属性", "mysql",
		func(p *config.InspectParams) bool { return p.DISABLE_COLUMN_ZEROFILL },
		func(p *config.InspectParams) bool { return !p.DISABLE_COLUMN_ZEROFILL }},
	{"limit_char_type_col_len", "限制 CHAR 类型字段长度", "both",
		func(p *config.InspectParams) bool { return p.COLUMN_MAX_CHAR_LENGTH == 64 },
		func(p *config.InspectParams) bool { return p.COLUMN_MAX_CHAR_LENGTH == 0 }},
	{"limit_varchar_type_col_len", "限制 VARCHAR 类型字段长度", "both",
		func(p *config.InspectParams) bool { return p.MAX_VARCHAR_LENGTH == 1024 },
		func(p *config.InspectParams) bool { return p.MAX_VARCHAR_LENGTH == 0 }},
	{"limit_col_no_partial_data_type", "限制列不能使用部分数据类型", "both",
		func(p *config.InspectParams) bool {
			return p.CHECK_COLUMN_FORBIDDEN_TYPES && len(p.COLUMN_FORBIDDEN_TYPES) > 0
		},
		func(p *config.InspectParams) bool {
			return !p.CHECK_COLUMN_FORBIDDEN_TYPES && p.COLUMN_FORBIDDEN_TYPES == nil
		}},
	{"limit_pk_col_type", "限制主键列类型", "both",
		func(p *config.InspectParams) bool { return p.CHECK_PRIMARYKEY_USE_BIGINT },
		func(p *config.InspectParams) bool {
			return !p.CHECK_PRIMARYKEY_USE_BIGINT && p.PRIMARYKEY_SUPPORT_TYPES == nil
		}},
	{"limit_pk_col_must_auto_inc", "限制主键列必须自增", "mysql",
		func(p *config.InspectParams) bool { return p.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT },
		func(p *config.InspectParams) bool { return !p.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT }},
	{"limit_pk_column_count", "限制主键包含列的个数", "both",
		func(p *config.InspectParams) bool { return p.PRIMARYKEY_MAX_KEY_PARTS == 2 },
		func(p *config.InspectParams) bool { return p.PRIMARYKEY_MAX_KEY_PARTS == 0 }},
	{"limit_single_index_column_count", "限制单个索引包含列的个数", "both",
		func(p *config.InspectParams) bool { return p.SECONDARY_INDEX_MAX_KEY_PARTS == 2 },
		func(p *config.InspectParams) bool { return p.SECONDARY_INDEX_MAX_KEY_PARTS == 0 }},
	{"limit_tbl_idx_count", "限制表索引数量", "both",
		func(p *config.InspectParams) bool { return p.MAX_INDEX_KEYS == 5 },
		func(p *config.InspectParams) bool { return p.MAX_INDEX_KEYS == 0 }},
	{"limit_uniq_idx_name_fmt", "限制唯一索引名格式", "both",
		func(p *config.InspectParams) bool { return p.CHECK_UNIQ_INDEX_PREFIX && p.UNQI_INDEX_PREFIX == "UNIQ_" },
		func(p *config.InspectParams) bool { return !p.CHECK_UNIQ_INDEX_PREFIX }},
	{"limit_normal_idx_name_fmt", "限制普通索引名格式", "both",
		func(p *config.InspectParams) bool {
			return p.CHECK_SECONDARY_INDEX_PREFIX && p.SECONDARY_INDEX_PREFIX == "IDX_"
		},
		func(p *config.InspectParams) bool { return !p.CHECK_SECONDARY_INDEX_PREFIX }},
	{"index_must_have_name", "索引需要设置名称", "both",
		func(p *config.InspectParams) bool {
			return p.CHECK_INDEX_NAME_REQUIRED && p.CHECK_UNIQ_INDEX_PREFIX && p.CHECK_SECONDARY_INDEX_PREFIX
		},
		func(p *config.InspectParams) bool { return !p.CHECK_INDEX_NAME_REQUIRED }},
	{"limit_tbl_no_use_fk", "限制表不能使用外键", "both",
		func(p *config.InspectParams) bool { return !p.ENABLE_FOREIGN_KEY },
		func(p *config.InspectParams) bool { return p.ENABLE_FOREIGN_KEY }},
	{"merge_multiple_alter_stmt_same_table", "同一张表的多条 ALTER 语句建议合为一条", "mysql",
		func(p *config.InspectParams) bool { return p.ENABLE_MERGE_ALTER_TABLE },
		func(p *config.InspectParams) bool { return !p.ENABLE_MERGE_ALTER_TABLE }},
	{"disable_view_related_op", "禁止视图相关操作", "both",
		func(p *config.InspectParams) bool { return !p.ENABLE_CREATE_VIEW },
		func(p *config.InspectParams) bool { return p.ENABLE_CREATE_VIEW }},
	{"limit_table_column_count", "限制表字段的数量", "both",
		func(p *config.InspectParams) bool { return p.MAX_TABLE_COLUMNS == 50 },
		func(p *config.InspectParams) bool { return p.MAX_TABLE_COLUMNS == 0 }},
	{"limit_database_charset", "限制库的字符集", "mysql",
		func(p *config.InspectParams) bool {
			return p.CHECK_DATABASE_CHARSET && len(p.DATABASE_SUPPORT_CHARSET) > 0
		},
		func(p *config.InspectParams) bool { return !p.CHECK_DATABASE_CHARSET }},
	{"limit_tbl_collation", "限制表校验规则", "mysql",
		func(p *config.InspectParams) bool {
			return p.CHECK_TABLE_COLLATION && len(p.TABLE_SUPPORT_COLLATION) > 0
		},
		func(p *config.InspectParams) bool {
			return !p.CHECK_TABLE_COLLATION && p.TABLE_SUPPORT_COLLATION == nil
		}},
	{"table_must_include_required_columns", "表需要包含某些列", "both",
		func(p *config.InspectParams) bool { return len(p.TABLE_REQUIRED_COLUMNS) == 2 },
		func(p *config.InspectParams) bool { return p.TABLE_REQUIRED_COLUMNS == nil }},
}

// unmappedItems 结构目录中未接入 applyStandardRule 的能力项（规范开关/级别对引擎无影响）
var unmappedItems = []struct {
	key  string
	name string
}{
	{"index_name_no_duplicate", "索引名称不允许重复"},
	{"tbl_charset_change_use_convert_syntax", "修改表字符集建议用 ALTER TABLE CONVERT 语法"},
}

// dbPrefixes 生成规则 key：both → mysql/postgres 各一条
func dbPrefixes(db string) []string {
	if db == "both" {
		return []string{"mysql", "postgres"}
	}
	return []string{"mysql"}
}

func ruleWithLevel(suffix, prefix, level string) models.InsightSQLStandardRule {
	return models.InsightSQLStandardRule{
		RuleKey:  prefix + ".struct." + suffix,
		Category: "structure",
		DbType:   prefix,
		Level:    level,
		Params:   []byte(`{}`),
	}
}

func TestStructRuleParamsMatrix(t *testing.T) {
	results := make([]pmResult, 0, 96)
	for _, mc := range mappedChecks {
		for _, prefix := range dbPrefixes(mc.db) {
			res := pmResult{Key: mc.key, Name: mc.name, DB: prefix, Mapped: true}

			pMust := config.DefaultInspectParams()
			applyStandardRule(&pMust, ruleWithLevel(mc.key, prefix, "must"))
			pSuggest := config.DefaultInspectParams()
			applyStandardRule(&pSuggest, ruleWithLevel(mc.key, prefix, "suggest"))
			pOff := config.DefaultInspectParams()
			applyStandardRule(&pOff, ruleWithLevel(mc.key, prefix, "off"))

			res.MustOK = mc.checkOn(&pMust)
			res.EqualOK = reflect.DeepEqual(pMust, pSuggest)
			res.OffOK = mc.checkOff(&pOff)
			if !res.MustOK {
				res.Detail = "must 级别下参数未进入开启态"
			} else if !res.EqualOK {
				res.Detail = "must 与 suggest 的引擎参数不一致"
			} else if !res.OffOK {
				res.Detail = "off 级别下参数未回落关闭态"
			}
			results = append(results, res)
		}
	}
	for _, um := range unmappedItems {
		for _, prefix := range dbPrefixes("mysql") {
			pMust := config.DefaultInspectParams()
			applyStandardRule(&pMust, ruleWithLevel(um.key, prefix, "must"))
			pOff := config.DefaultInspectParams()
			applyStandardRule(&pOff, ruleWithLevel(um.key, prefix, "off"))
			mapped := !reflect.DeepEqual(pMust, config.DefaultInspectParams())
			results = append(results, pmResult{
				Key: um.key, Name: um.name, DB: prefix, Mapped: mapped,
				Detail: "未接入 applyStandardRule：规范的启用/禁用与级别切换对审核引擎无任何影响",
			})
		}
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile("/tmp/gi_struct_params.json", out, 0644); err != nil {
		t.Fatalf("写出结果失败: %v", err)
	}

	failed := 0
	for _, r := range results {
		if r.Mapped && !(r.MustOK && r.EqualOK && r.OffOK) {
			failed++
			t.Errorf("[参数映射] %s(%s): %s", r.Name, r.DB, r.Detail)
		}
	}
	t.Logf("参数映射用例共 %d 条，其中已映射 %d 条全部通过 = %v", len(results), len(results)-22, failed == 0)
}
