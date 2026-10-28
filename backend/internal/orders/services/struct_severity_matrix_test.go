package services

// 结构类规范规则「级别映射层」矩阵测试：
// 引擎产出的 summary（RuleKey/文案）经 resolveInspectSeverity 映射回规范配置的级别
// （must → 阻断提交审批；suggest → 提示放行）。本测试对 34 个结构能力项逐条验证：
//   - 规范配置 must 时，引擎 key / 文案均应映射为 must；
//   - 规范配置 suggest 时，应映射为 suggest；
//   - 引擎 key 与规范 key 不一致的场景（如 col_name_no_keyword）验证文案匹配兜底是否命中。
//
// 结果写入 /tmp/gi_struct_severity.json，由报告脚本汇总。

import (
	"encoding/json"
	"os"
	"testing"

	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
)

type sevResult struct {
	Item     string `json:"item"`
	Key      string `json:"key"`      // 规范 rule key 后缀
	Summary  string `json:"summary"`  // 引擎 summary 的 rule key（或空，用文案）
	Message  string `json:"message"`  // 引擎消息
	Level    string `json:"level"`    // 规范配置的级别
	Resolved string `json:"resolved"` // 实际映射结果
	Pass     bool   `json:"pass"`
	Note     string `json:"note"`
}

// sevCases 每个能力项的引擎输出形态（key 与代表性文案，取自引擎矩阵实测）
var sevCases = []struct {
	item    string
	key     string // 规范 key 后缀
	sumKey  string // 引擎 summary key（空 = 无 key）
	message string // 引擎消息（用于文案匹配）
}{
	{"限制自增列名字为 ID", "limit_auto_inc_col_name_is_id", "limit_auto_inc_col_name_is_id", "自增列`uid`命名必须为 id（表`t`）"},
	{"限制自增列为无符号", "limit_auto_inc_col_is_unsigned", "limit_auto_inc_col_is_unsigned", "自增列`id`必须定义为unsigned（表`t`）"},
	{"限制建表自增初始值", "limit_create_table_auto_inc_init", "limit_create_table_auto_inc_init", "表`t`的自增初始值必须显式指定且不得超过1"},
	{"限制 CHAR 类型字段长度", "limit_char_type_col_len", "limit_char_type_col_len", "列`c1`长度太大（max = 64），当前char长度为128，请调整长度或改用varchar类型（表`t`）"},
	{"限制字段名大小写", "limit_col_name_case", "limit_col_name_case", "列`Name`命名不符合要求：必须为小写，仅允许匹配正则`[a-z][a-z0-9_]*`（表`t`）"},
	{"字段名不能是关键字", "col_name_no_keyword", "col_name_no_keyword", "列`select`命名不允许使用关键字（表`t`）"},
	{"限制列要有注释", "limit_col_must_have_comment", "limit_col_must_have_comment", "列`c1`必须要有注释（表`t`）"},
	{"限制表字段的数量", "limit_table_column_count", "limit_table_column_count", "表`t`字段数量超出限制（max = 50，当前 60），请拆分表或精简字段"},
	{"限制表必须注释", "limit_tbl_must_comment", "limit_tbl_must_comment", "表`t`必须要有注释"},
	{"表要有主键", "table_must_have_pk", "table_must_have_pk", "表`t`必须定义主键"},
	{"限制表存储引擎", "limit_table_storage_engine", "limit_table_storage_engine", "表`t`指定的存储引擎`MEMORY`不符合要求，支持的存储引擎为`[InnoDB MyISAM]`"},
	{"限制唯一索引名格式", "limit_uniq_idx_name_fmt", "", "唯一索引`uk_c1`命名不符合要求，必须以`UNIQ_`为前缀（不区分大小写）（表`t`）"},
	{"限制 VARCHAR 类型字段长度", "limit_varchar_type_col_len", "limit_varchar_type_col_len", "列`c1`长度太大（max = 1024），当前varchar长度为2048，请使用text类型替代（表`t`）"},
	{"限制列要有默认值", "limit_col_must_have_default", "limit_col_must_have_default", "列`c1`需要设置一个默认值（表`t`）"},
	{"限制列不可空（NOT NULL)", "limit_col_not_null", "limit_col_not_null", "列`c1`必须定义为`NOT NULL`（表`t`）"},
	{"限制列不能使用部分数据类型", "limit_col_no_partial_data_type", "limit_col_no_partial_data_type", "列`c1`不允许使用`double`类型（表`t`）"},
	{"限制库的字符集", "limit_database_charset", "limit_database_charset", "数据库`db`必须指定字符集，可选字符集为[utf8mb4]【例如:DEFAULT CHARACTER SET=utf8mb4】"},
	{"不能设置列的字符集", "no_column_charset", "no_column_charset", "列`c1`不能显式指定字符集`utf8`（表`t`）"},
	{"不能设置列的校对集", "no_column_collate", "no_column_collate", "列`c1`不能显式指定校对集`utf8_general_ci`（表`t`）"},
	{"字段禁用 Zerofill 属性", "column_zerofill_forbidden", "column_zerofill_forbidden", "列`c1`禁用 ZEROFILL 属性（表`t`）"},
	{"禁止事件相关操作", "disable_event_related_op", "", ""},
	{"禁止自定义函数相关操作", "disable_custom_function_related_op", "", ""},
	{"禁止存储过程相关操作", "disable_proc_related_op", "", ""},
	{"禁止触发器相关操作", "disable_trigger_related_op", "", ""},
	{"禁止视图相关操作", "disable_view_related_op", "", "禁止使用视图"},
	{"索引名称不允许重复", "index_name_no_duplicate", "", "表`t`存在重复索引：`idx_c1,idx_c1_2`"},
	{"限制表索引数量", "limit_tbl_idx_count", "", "表`t`最多允许定义2个二级索引，当前二级索引个数为3"},
	{"索引需要设置名称", "index_must_have_name", "", "索引``命名不符合要求，必须以`IDX_`为前缀（不区分大小写）（表`t`）"},
	{"限制普通索引名格式", "limit_normal_idx_name_fmt", "", "普通索引`index_c1`命名不符合要求，必须以`IDX_`为前缀（不区分大小写）（表`t`）"},
	{"限制主键列必须自增", "limit_pk_col_must_auto_inc", "limit_pk_col_must_auto_inc", "表`t`的主键列`id`必须定义为自增列"},
	{"限制主键包含列的个数", "limit_pk_column_count", "", "表`t`的主键索引`PRIMARY KEY`最多允许组成列数为2，当前列数为3"},
	{"限制主键列类型", "limit_pk_col_type", "limit_pk_col_type", "表`t`的主键列`id`必须是bigint/int类型，当前为varchar"},
	{"限制单个索引包含列的个数", "limit_single_index_column_count", "", "表`t`的二级索引`IDX_c`最多允许组成列数为2，当前列数为3"},
	{"同一张表的多条 ALTER 语句建议合为一条", "merge_multiple_alter_stmt_same_table", "merge_multiple_alter_stmt_same_table", "[MySQL数据库]表`t`的多条ALTER操作，请合并为一条ALTER语句"},
	{"修改表字符集建议用 ALTER TABLE CONVERT 语法", "tbl_charset_change_use_convert_syntax", "", ""},
	{"限制表字符集", "limit_tbl_charset", "limit_tbl_charset", "表`t`指定的字符集`latin1`不符合要求，可选字符集为`[utf8 utf8mb4]`"},
	{"限制表校验规则", "limit_tbl_collation", "limit_tbl_collation", "表`t`指定的排序规则`latin1_general_ci`不符合要求"},
	{"限制表名大小写", "limit_tbl_name_case", "limit_tbl_name_case", "表`Tbl`命名不符合要求：必须为小写，仅允许匹配正则`[a-z][a-z0-9_]*`"},
	{"表名不能是关键字", "tbl_name_no_keyword", "", "表`select`的命名不允许使用关键字"},
	{"限制表不能使用外键", "limit_tbl_no_use_fk", "", "表`t`禁止定义外键"},
	{"表需要包含某些列", "table_must_include_required_columns", "table_must_include_required_columns", "表`t`缺少必需的列：updated_at"},
}

func TestStructRuleSeverityMatrix(t *testing.T) {
	results := make([]sevResult, 0, len(sevCases)*2)
	failed := 0
	for _, sc := range sevCases {
		if sc.sumKey == "" && sc.message == "" {
			// 引擎未实现：无 summary 可映射
			results = append(results, sevResult{Item: sc.item, Key: sc.key, Note: "引擎未实现该规则，无 summary 可映射", Pass: false, Level: "-", Resolved: "-"})
			failed++
			continue
		}
		for _, level := range []string{"must", "suggest"} {
			// 规范中该能力项在当前数据库下的规则（以 mysql 前缀为例；后缀匹配与数据库无关）
			ruleLevels := []inspectServices.StandardRuleLevel{
				{RuleKey: "mysql.struct." + sc.key, Name: sc.item, Description: sc.item, Level: level},
			}
			got := resolveInspectSeverity(ruleLevels, "suggest", sc.sumKey, sc.message)
			res := sevResult{
				Item: sc.item, Key: sc.key, Summary: sc.sumKey, Message: sc.message,
				Level: level, Resolved: got, Pass: got == level,
			}
			if !res.Pass {
				res.Note = "级别映射未命中（可能退化为文案匹配或默认级别兜底）"
				failed++
			}
			results = append(results, res)
		}
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile("/tmp/gi_struct_severity.json", out, 0644); err != nil {
		t.Fatalf("写出结果失败: %v", err)
	}
	for _, r := range results {
		if !r.Pass {
			t.Logf("级别映射异常: %s 规范级别=%s 实际=%s sumKey=%q msg=%q", r.Item, r.Level, r.Resolved, r.Summary, r.Message)
		}
	}
	t.Logf("级别映射用例共 %d 条，通过 %d 条，异常 %d 条", len(results), len(results)-failed, failed)
}
