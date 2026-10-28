package services

import (
	"strings"
	"testing"
)

// Oracle 种子一致性守门测试（红线：致命类别兜底会张冠李戴）。
//
//   1. TestOracleSeedCoversEngineKeys：Oracle 引擎产出的每个 rule_key 后缀
//      （checker/oracle_*.go 中 WarnXxx 携带的 key）都必须在种子目录里有对应规则，
//      否则预审结论会挂到同致命类别的任意规则上（PG 轮实测踩坑）。
//   2. TestOracleSeedNoGhostRules：种子里全部 oracle.* 规则都必须是引擎确实会
//      产出的 key，否则规范页面照常显示、引擎永不触发（幽灵规则）。

// oracleEngineEmittedKeys Oracle 引擎产出的 rule_key 后缀全集
// （来源：checker/oracle_gate.go / oracle_rules.go / oracle_dml.go / oracle_object_rules.go）。
var oracleEngineEmittedKeys = []string{
	// 准入闸门
	"disable_create_table_as",
	"disable_create_table_like",
	"disable_view_related_op",
	// 结构：表
	"table_must_have_pk",
	"limit_tbl_must_comment",
	"limit_table_column_count",
	"table_must_include_required_columns",
	"limit_tbl_name_case",
	"tbl_name_no_keyword",
	"limit_tbl_no_use_fk",
	"merge_multiple_alter_stmt_same_table",
	"create_tbl_must_not_exist",
	"obj_tbl_must_exist",
	"obj_tbl_must_not_exist",
	// 结构：列
	"limit_auto_inc_col_name_is_id",
	"limit_create_table_auto_inc_init",
	"limit_char_type_col_len",
	"limit_col_name_case",
	"col_name_no_keyword",
	"limit_col_must_have_comment",
	"limit_col_must_have_default",
	"limit_col_not_null",
	"limit_col_no_partial_data_type",
	"limit_varchar_type_col_len",
	"obj_col_must_exist",
	"obj_col_must_not_exist",
	// 结构：索引
	"limit_uniq_idx_name_fmt",
	"limit_normal_idx_name_fmt",
	"index_must_have_name",
	"limit_pk_col_must_auto_inc",
	"limit_pk_column_count",
	"limit_pk_col_type",
	"limit_single_index_column_count",
	"limit_tbl_idx_count",
	"limit_tbl_redundant_idx",
	"obj_idx_must_exist",
	"obj_idx_must_not_exist",
	// DML
	"limit_update_delete_affected_rows",
	"delete_stmt_check_table_exists",
	"limit_update_delete_join_table_count",
	"update_delete_stmt_join_on_check",
	"limit_update_delete_data_size",
	"update_delete_stmt_suggest_where",
	"insert_stmt_col_name_no_dup",
	"insert_stmt_columns_match_values",
	"insert_stmt_specify_columns",
	"insert_stmt_not_null_column_null_value",
	"insert_stmt_avoid_sysdate_func",
	"limit_insert_values_row_count",
	"insert_stmt_check_column_exists",
	"limit_where_in_element_count",
	"update_multi_table_column_prefix_check",
	"update_stmt_avoid_update_create_time_column",
	"update_stmt_check_updates_pk",
	"update_stmt_suggest_update_time_column",
	"update_stmt_set_separator_check",
	"update_stmt_check_column_exists",
}

func oracleSeedIndex() map[string]bool {
	idx := make(map[string]bool)
	for _, r := range defaultStandardRules() {
		if strings.HasPrefix(r.RuleKey, "oracle.") {
			idx[strings.TrimPrefix(r.RuleKey, "oracle.struct.")] = true
			idx[strings.TrimPrefix(r.RuleKey, "oracle.query_update.")] = true
		}
	}
	return idx
}

func TestOracleSeedCoversEngineKeys(t *testing.T) {
	idx := oracleSeedIndex()
	for _, key := range oracleEngineEmittedKeys {
		if !idx[key] {
			t.Errorf("引擎产出 key %q 不在 Oracle 种子目录中：预审结论会走致命类别兜底（张冠李戴），请补种子", key)
		}
	}
}

func TestOracleSeedNoGhostRules(t *testing.T) {
	emitted := make(map[string]bool, len(oracleEngineEmittedKeys))
	for _, key := range oracleEngineEmittedKeys {
		emitted[key] = true
	}
	for _, r := range defaultStandardRules() {
		if !strings.HasPrefix(r.RuleKey, "oracle.") {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(r.RuleKey, "oracle.struct."), "oracle.query_update.")
		_ = suffix
		key := strings.TrimPrefix(r.RuleKey, "oracle.struct.")
		key = strings.TrimPrefix(key, "oracle.query_update.")
		if !emitted[key] {
			t.Errorf("种子里的 %q 不是引擎产出的 key：属于幽灵规则（页面可配但永不触发），请删除或实现引擎", r.RuleKey)
		}
	}
}

// TestOracleSeedRuleTypeValid 种子里的 oracle 规则类型必须是合法取值
func TestOracleSeedRuleTypeValid(t *testing.T) {
	for _, r := range defaultStandardRules() {
		if !strings.HasPrefix(r.RuleKey, "oracle.") {
			continue
		}
		if !isAllowedRuleType(r.RuleType) {
			t.Errorf("种子 %q 的 RuleType %q 非法", r.RuleKey, r.RuleType)
		}
	}
}
