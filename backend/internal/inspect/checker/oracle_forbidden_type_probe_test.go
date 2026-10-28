package checker

import (
	"testing"
)

// 「限制列不能使用指定的数据类型」（limit_col_no_partial_data_type）Oracle 引擎回归。
// 比对走 oracleCanonicalType 别名归一（双侧），设计对齐 PG 引擎 pgTypeAliases。
func TestOracleForbiddenColumnTypeRule(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_COLUMN_FORBIDDEN_TYPES = true
	params.COLUMN_FORBIDDEN_TYPES = []string{"long", "long raw"}

	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{
			name: "LONG 列应命中",
			sql:  "CREATE TABLE t_a (id NUMBER NOT NULL, c1 LONG);",
			want: true,
		},
		{
			name: "LONG RAW 列应命中",
			sql:  "CREATE TABLE t_b (id NUMBER NOT NULL, c1 LONG RAW);",
			want: true,
		},
		{
			name: "CHAR 列不在禁用列表不应命中",
			sql:  "CREATE TABLE t_c (id NUMBER NOT NULL, is_valid CHAR(1));",
			want: false,
		},
		{
			name: "VARCHAR2 列不在禁用列表不应命中",
			sql:  "CREATE TABLE t_d (id NUMBER NOT NULL, user_name VARCHAR2(500));",
			want: false,
		},
	}
	for _, tc := range cases {
		keys := collectRuleKeys(runOracle(t, tc.sql, params, &oracleMetaStub{}))
		got := hasRuleKey(keys, "limit_col_no_partial_data_type")
		if got != tc.want {
			t.Errorf("%s: 期望命中=%v，实际=%v（keys: %v）", tc.name, tc.want, got, keys)
		}
	}
}

// 自定义禁用列表：把 char 加进列表后应能命中（证明参数可配）
func TestOracleForbiddenColumnTypeCustomList(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_COLUMN_FORBIDDEN_TYPES = true
	params.COLUMN_FORBIDDEN_TYPES = []string{"char"}

	keys := collectRuleKeys(runOracle(t, "CREATE TABLE t_e (id NUMBER NOT NULL, is_valid CHAR(1));", params, &oracleMetaStub{}))
	if !hasRuleKey(keys, "limit_col_no_partial_data_type") {
		t.Fatalf("自定义禁用 char 应命中，实际: %v", keys)
	}
}

// 用户实测 SQL `CREATE TABLE ORDER1 (is_valid double);`：
// 裸 DOUBLE 解析为 col.Type="double"，归一后命中 float/double 任一配置。
func TestOracleForbiddenColumnDoubleType(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_COLUMN_FORBIDDEN_TYPES = true

	for _, list := range [][]string{{"double"}, {"float"}} {
		params.COLUMN_FORBIDDEN_TYPES = list
		keys := collectRuleKeys(runOracle(t, "CREATE TABLE ORDER1 (is_valid double);", params, &oracleMetaStub{}))
		if !hasRuleKey(keys, "limit_col_no_partial_data_type") {
			t.Errorf("禁用 %v 时 `is_valid double` 应命中，实际 keys=%v", list, keys)
		}
	}
}

// ANSI 别名归一后必须命中（历史上 Oracle 引擎按原文比对，别名写法全部漏检）：
//   DOUBLE PRECISION = FLOAT(126)、REAL = FLOAT(63) → 归一 float
//   DECIMAL/NUMERIC = NUMBER → 归一 number
//   INTEGER 与 int 同义收敛
func TestOracleForbiddenTypeAliasHit(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_COLUMN_FORBIDDEN_TYPES = true

	cases := []struct {
		name string
		list []string
		sql  string
	}{
		{"禁float应命中DOUBLE PRECISION", []string{"float"}, "CREATE TABLE t_f1 (id NUMBER NOT NULL, c1 DOUBLE PRECISION);"},
		{"禁float应命中REAL", []string{"float"}, "CREATE TABLE t_f2 (id NUMBER NOT NULL, c1 REAL);"},
		{"禁double应命中DOUBLE PRECISION", []string{"double"}, "CREATE TABLE t_f5 (id NUMBER NOT NULL, c1 DOUBLE PRECISION);"},
		{"禁number应命中DECIMAL", []string{"number"}, "CREATE TABLE t_f6 (id NUMBER NOT NULL, c1 DECIMAL(10,2));"},
		{"禁number应命中NUMERIC", []string{"number"}, "CREATE TABLE t_f7 (id NUMBER NOT NULL, c1 NUMERIC);"},
		{"禁int应命中INTEGER", []string{"int"}, "CREATE TABLE t_f8 (id NUMBER NOT NULL, c1 INTEGER);"},
	}
	for _, tc := range cases {
		params.COLUMN_FORBIDDEN_TYPES = tc.list
		keys := collectRuleKeys(runOracle(t, tc.sql, params, &oracleMetaStub{}))
		if !hasRuleKey(keys, "limit_col_no_partial_data_type") {
			t.Errorf("%s（%s）：应命中，实际 keys=%v", tc.name, tc.sql, keys)
		}
	}
}

// 反向：禁用列表之外的类型不应误伤
func TestOracleForbiddenTypeAliasNoFalsePositive(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_COLUMN_FORBIDDEN_TYPES = true
	params.COLUMN_FORBIDDEN_TYPES = []string{"float"}

	keys := collectRuleKeys(runOracle(t, "CREATE TABLE t_f9 (id NUMBER NOT NULL, c1 NUMBER(10));", params, &oracleMetaStub{}))
	if hasRuleKey(keys, "limit_col_no_partial_data_type") {
		t.Errorf("禁 float 不应命中 NUMBER 列（keys=%v）", keys)
	}
}

// 主键列类型白名单同样受益于别名归一：INTEGER 主键允许配置 int 放行
func TestOraclePrimaryKeyTypeAlias(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_PRIMARYKEY_USE_BIGINT = true
	params.PRIMARYKEY_SUPPORT_TYPES = []string{"int"}

	keys := collectRuleKeys(runOracle(t, "CREATE TABLE t_pk_int (id INTEGER NOT NULL, CONSTRAINT pk_t PRIMARY KEY (id));", params, &oracleMetaStub{}))
	if hasRuleKey(keys, "limit_pk_col_type") {
		t.Errorf("白名单 int 应放行 INTEGER 主键（别名归一），实际命中: %v", keys)
	}

	params.PRIMARYKEY_SUPPORT_TYPES = []string{"number"}
	keys = collectRuleKeys(runOracle(t, "CREATE TABLE t_pk_dec (id DECIMAL(10) NOT NULL, CONSTRAINT pk_t2 PRIMARY KEY (id));", params, &oracleMetaStub{}))
	if hasRuleKey(keys, "limit_pk_col_type") {
		t.Errorf("白名单 number 应放行 DECIMAL 主键（DECIMAL=NUMBER），实际命中: %v", keys)
	}
}
