package checker

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

// Oracle 引擎规则测试：与 pg_rules_test.go 同一口径——
// 结构类规则（oracle.struct.*）在规范中为 must，DML 类为 suggest/must；
// 把对应参数全部打开，验证 Oracle 引擎的判定行为与规范参数一致。
// 连库规则通过 oracleMetaStub 桩注入（生产为 nil 走真实库）。

// oracleMetaStub 元信息探针桩：按表名（大写归一）返回预置结构。
type oracleMetaStub struct {
	tables      map[string]bool
	columns     map[string]bool // key: TABLE.COL（均大写）
	notNullable map[string]bool // key: TABLE.COL，true = NOT NULL
	indexes     map[string]bool
	pkCols      map[string][]string
	colCounts   map[string]int
	idxCounts   map[string]int
	whereCounts map[string]int64
}

func (s *oracleMetaStub) TableExists(table string) (bool, error) {
	return s.tables[strings.ToUpper(table)], nil
}

func (s *oracleMetaStub) ColumnExists(table, column string) (bool, error) {
	return s.columns[strings.ToUpper(table)+"."+strings.ToUpper(column)], nil
}

func (s *oracleMetaStub) ColumnNotNullable(table, column string) (bool, error) {
	return s.notNullable[strings.ToUpper(table)+"."+strings.ToUpper(column)], nil
}

func (s *oracleMetaStub) IndexExists(index string) (bool, error) {
	return s.indexes[strings.ToUpper(index)], nil
}

func (s *oracleMetaStub) TablePKColumns(table string) ([]string, error) {
	return s.pkCols[strings.ToUpper(table)], nil
}

func (s *oracleMetaStub) TableColumnCount(table string) (int, error) {
	if n, ok := s.colCounts[strings.ToUpper(table)]; ok {
		return n, nil
	}
	return 0, nil
}

func (s *oracleMetaStub) countExistingIndexes(table string) (int, error) {
	return s.idxCounts[strings.ToUpper(table)], nil
}

func (s *oracleMetaStub) CountWhere(table, whereClause string) (int64, error) {
	return s.whereCounts[strings.ToUpper(table)], nil
}

// oracleProdLikeParams 模拟「默认生产环境规范」映射出的 Oracle 审核参数：
// 大小写两条规则按 applyStandardRule 的 oracle 分支置为 upper（Oracle 未加引号
// 标识符折叠为大写；DefaultInspectParams 的 lower 是 MySQL/PG 口径）。
func oracleProdLikeParams() config.InspectParams {
	p := config.DefaultInspectParams()
	p.TABLE_NAME_CASE = "upper"
	p.COLUMN_NAME_CASE = "upper"
	p.CHECK_TABLE_PRIMARY_KEY = true
	p.CHECK_COLUMN_COMMENT = true
	p.CHECK_TABLE_COMMENT = true
	p.CHECK_COLUMN_DEFAULT_VALUE = true
	p.ENABLE_COLUMN_NOT_NULL = true
	p.CHECK_TABLE_IDENTIFIER = true
	p.CHECK_COLUMN_IDENTIFIER = true
	p.CHECK_IDENTIFER_KEYWORD = true
	p.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT = true
	p.PRIMARYKEY_SUPPORT_TYPES = []string{"number"}
	p.PRIMARYKEY_MAX_KEY_PARTS = 3
	p.SECONDARY_INDEX_MAX_KEY_PARTS = 5
	p.MAX_INDEX_KEYS = 8
	p.COLUMN_MAX_CHAR_LENGTH = 64
	p.MAX_VARCHAR_LENGTH = 2048
	p.CHECK_UNIQ_INDEX_PREFIX = true
	p.CHECK_SECONDARY_INDEX_PREFIX = true
	p.UNQI_INDEX_PREFIX = "UNIQ_"
	p.SECONDARY_INDEX_PREFIX = "IDX_"
	p.CHECK_INDEX_NAME_REQUIRED = true
	p.ENABLE_FOREIGN_KEY = false
	p.ENABLE_CREATE_VIEW = false
	p.ENABLE_CREATE_TABLE_AS = false
	p.ENABLE_CREATE_TABLE_LIKE = false
	p.ENABLE_MERGE_ALTER_TABLE = true
	p.DML_MUST_HAVE_WHERE = true
	p.CHECK_DML_JOIN_WITH_ON = true
	p.MAX_UPDATE_DELETE_JOIN_TABLES = 5
	p.MAX_WHERE_IN_ELEMENTS = 1000
	p.MAX_AFFECTED_ROWS = 10000
	p.MAX_INSERT_ROWS = 1000
	p.CHECK_INSERT_OBJECT_EXISTS = true
	p.CHECK_DELETE_TABLE_EXISTS = true
	p.CHECK_UPDATE_SET_SEPARATOR = true
	p.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX = true
	p.CHECK_UPDATE_UPDATES_PK = true
	p.CHECK_INSERT_COLUMNS_MATCH_VALUES = true
	p.CHECK_INSERT_SPECIFY_COLUMNS = true
	p.CHECK_INSERT_COLUMN_NO_DUP = true
	p.CHECK_INSERT_AVOID_SYSDATE = true
	return p
}

func runOracle(t *testing.T, sql string, params config.InspectParams, stub *oracleMetaStub) []ReturnData {
	t.Helper()
	svc := &SyntaxInspectService{
		DbType:        "Oracle",
		SqlText:       sql,
		InspectSchema: "APP",
		InspectParams: params,
		oracleMeta:    stub,
	}
	data, err := svc.runOracleInspect()
	if err != nil {
		t.Fatalf("Oracle 预审执行失败: %v", err)
	}
	return data
}

const oracleCompliantSQL = `-- 业务表：IDENTITY 主键 + 全量注释 + 规范命名索引
CREATE TABLE t_demo (
    id NUMBER GENERATED ALWAYS AS IDENTITY,
    cust_name VARCHAR2(128) DEFAULT ' ' NOT NULL,
    state_flag NUMBER(3) DEFAULT 1 NOT NULL,
    remark_txt VARCHAR2(512) DEFAULT ' ' NOT NULL,
    created_at DATE DEFAULT SYSDATE NOT NULL,
    CONSTRAINT pk_t_demo PRIMARY KEY (id)
);
COMMENT ON TABLE t_demo IS '演示业务表';
COMMENT ON COLUMN t_demo.id IS '主键ID';
COMMENT ON COLUMN t_demo.cust_name IS '名称';
COMMENT ON COLUMN t_demo.state_flag IS '状态';
COMMENT ON COLUMN t_demo.remark_txt IS '备注';
COMMENT ON COLUMN t_demo.created_at IS '创建时间';
CREATE INDEX idx_t_demo_status ON t_demo(state_flag);`

// 合规建表工单：不该报的绝不报
func TestOracleEngineNoFalsePositiveOnCompliantSQL(t *testing.T) {
	keys := collectRuleKeys(runOracle(t, oracleCompliantSQL, oracleProdLikeParams(), &oracleMetaStub{}))
	if len(keys) != 0 {
		t.Fatalf("合规 SQL 不应产出告警，实际: %v", keys)
	}
}

// 准入闸门：CTAS / 视图 / 物化视图
func TestOracleEngineStatementGates(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{"CTAS 建表", "CREATE TABLE t_copy AS SELECT * FROM t_src;", "disable_create_table_as"},
		{"CTL 建表（Oracle 不支持）", "CREATE TABLE t_copy (LIKE t_src);", "disable_create_table_like"},
		{"创建视图", "CREATE VIEW v_demo AS SELECT id FROM t_demo;", "disable_view_related_op"},
		{"替换视图", "CREATE OR REPLACE VIEW v_demo AS SELECT id FROM t_demo;", "disable_view_related_op"},
		{"物化视图", "CREATE MATERIALIZED VIEW mv_demo AS SELECT id FROM t_demo;", "disable_view_related_op"},
		{"删除视图", "DROP VIEW v_demo;", "disable_view_related_op"},
	}
	for _, tc := range cases {
		keys := collectRuleKeys(runOracle(t, tc.sql, oracleProdLikeParams(), &oracleMetaStub{}))
		if !hasRuleKey(keys, tc.want) {
			t.Fatalf("%s: 期望命中 %s，实际: %v", tc.name, tc.want, keys)
		}
	}
}

// 表级/列级结构规则（双引号小写名在 Oracle 默认 upper 口径下全部不合规）
func TestOracleEngineStructRules(t *testing.T) {
	sql := `CREATE TABLE "t_order" (
    "id" NUMBER,
    "name" VARCHAR2(3000),
    lock NUMBER DEFAULT 1,
    created_at DATE DEFAULT SYSDATE NOT NULL
);`
	keys := collectRuleKeys(runOracle(t, sql, oracleProdLikeParams(), &oracleMetaStub{}))
	for _, want := range []string{
		"table_must_have_pk",         // 无主键
		"limit_tbl_name_case",        // 双引号小写表名
		"limit_col_name_case",        // 双引号小写列名
		"limit_varchar_type_col_len", // VARCHAR2(3000) 超限
		"col_name_no_keyword",        // lock 是 Oracle 关键字
		"limit_col_must_have_comment",
		"limit_tbl_must_comment",
	} {
		if !hasRuleKey(keys, want) {
			t.Fatalf("期望命中 %s，实际: %v", want, keys)
		}
	}
}

// IDENTITY 主键初值 + 主键类型白名单
func TestOracleEnginePrimaryKeyRules(t *testing.T) {
	params := oracleProdLikeParams()
	params.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE = true
	params.TABLE_AUTOINCREMENT_INIT_VALUE = 200 // 要求 START WITH >= 200
	sql := `CREATE TABLE t_init (
    id NUMBER GENERATED ALWAYS AS IDENTITY (START WITH 100),
    CONSTRAINT pk_t_init PRIMARY KEY (id)
);
COMMENT ON TABLE t_init IS '初值测试';`
	keys := collectRuleKeys(runOracle(t, sql, params, &oracleMetaStub{}))
	if !hasRuleKey(keys, "limit_create_table_auto_inc_init") {
		t.Fatalf("START WITH 100 < 200 应命中 limit_create_table_auto_inc_init，实际: %v", keys)
	}

	// 主键类型不在白名单（VARCHAR2 主键）
	sql2 := `CREATE TABLE t_pk_type (
    code VARCHAR2(32) NOT NULL,
    CONSTRAINT pk_t_pk_type PRIMARY KEY (code)
);
COMMENT ON TABLE t_pk_type IS '主键类型测试';`
	keys2 := collectRuleKeys(runOracle(t, sql2, params, &oracleMetaStub{}))
	if !hasRuleKey(keys2, "limit_pk_col_type") {
		t.Fatalf("VARCHAR2 主键应命中 limit_pk_col_type，实际: %v", keys2)
	}
}

// 连库对象规则（桩注入）：建表冲突 / 目标表存在性 / DML 目标表
func TestOracleEngineObjectRules(t *testing.T) {
	stub := &oracleMetaStub{
		tables:    map[string]bool{"T_EXIST": true},
		pkCols:    map[string][]string{"T_EXIST": {"ID"}},
		colCounts: map[string]int{"T_EXIST": 4},
	}

	// 建表冲突
	sql := `CREATE TABLE t_exist (
    id NUMBER GENERATED ALWAYS AS IDENTITY,
    CONSTRAINT pk_t_exist PRIMARY KEY (id)
);
COMMENT ON TABLE t_exist IS '冲突测试';`
	keys := collectRuleKeys(runOracle(t, sql, oracleProdLikeParams(), stub))
	if !hasRuleKey(keys, "create_tbl_must_not_exist") {
		t.Fatalf("目标表已存在应命中 create_tbl_must_not_exist，实际: %v", keys)
	}

	// ALTER 目标表不存在（表都不存在时只报表级，不报列级误报）
	keys = collectRuleKeys(runOracle(t, "ALTER TABLE t_no_such DROP COLUMN c1;", oracleProdLikeParams(), stub))
	if !hasRuleKey(keys, "obj_tbl_must_exist") {
		t.Fatalf("目标表不存在应命中 obj_tbl_must_exist，实际: %v", keys)
	}
	if hasRuleKey(keys, "obj_col_must_exist") {
		t.Fatalf("表不存在时不应追加列级误报，实际: %v", keys)
	}

	// DML 目标表不存在
	keys = collectRuleKeys(runOracle(t, "UPDATE t_no_such SET name = 'x' WHERE id = 1;", oracleProdLikeParams(), stub))
	if !hasRuleKey(keys, "update_stmt_check_column_exists") {
		t.Fatalf("UPDATE 目标表不存在应命中 update_stmt_check_column_exists，实际: %v", keys)
	}
}

// DML 规则：无 WHERE / LIMIT / SET 分隔符 / IN 元素数 / 影响行数
func TestOracleEngineDMLRules(t *testing.T) {
	params := oracleProdLikeParams()

	keys := collectRuleKeys(runOracle(t, "DELETE FROM t_demo;", params, &oracleMetaStub{}))
	if !hasRuleKey(keys, "update_delete_stmt_suggest_where") {
		t.Fatalf("DELETE 无 WHERE 应命中 update_delete_stmt_suggest_where，实际: %v", keys)
	}

	keys = collectRuleKeys(runOracle(t, "UPDATE t_demo SET name = 'a' WHERE id = 1 LIMIT 5;", params, &oracleMetaStub{}))
	if !hasRuleKey(keys, "limit_update_delete_data_size") {
		t.Fatalf("Oracle 不支持 LIMIT 应命中 limit_update_delete_data_size，实际: %v", keys)
	}

	keys = collectRuleKeys(runOracle(t, "UPDATE t_demo SET name = 'a' AND status = 1 WHERE id = 1;", params, &oracleMetaStub{}))
	if !hasRuleKey(keys, "update_stmt_set_separator_check") {
		t.Fatalf("SET 用 AND 分隔应命中 update_stmt_set_separator_check，实际: %v", keys)
	}

	// IN 元素数超限（1001 个）
	var sb strings.Builder
	sb.WriteString("DELETE FROM t_demo WHERE id IN (")
	for i := 0; i < 1001; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("1")
	}
	sb.WriteString(");")
	keys = collectRuleKeys(runOracle(t, sb.String(), params, &oracleMetaStub{}))
	if !hasRuleKey(keys, "limit_where_in_element_count") {
		t.Fatalf("IN 超限应命中 limit_where_in_element_count，实际: %v", keys)
	}

	// 影响行数（桩返回超限值）
	stub := &oracleMetaStub{
		whereCounts: map[string]int64{"T_DEMO": 50000},
	}
	keys = collectRuleKeys(runOracle(t, "UPDATE t_demo SET name = 'a' WHERE status = 1;", params, stub))
	if !hasRuleKey(keys, "limit_update_delete_affected_rows") {
		t.Fatalf("影响行数超限应命中 limit_update_delete_affected_rows，实际: %v", keys)
	}
}

// 同表多条 ALTER 合并建议
func TestOracleEngineMergeAlter(t *testing.T) {
	sql := `ALTER TABLE t_demo ADD remark2 VARCHAR2(100);
ALTER TABLE t_demo MODIFY status DEFAULT 0;`
	keys := collectRuleKeys(runOracle(t, sql, oracleProdLikeParams(), &oracleMetaStub{}))
	if !hasRuleKey(keys, "merge_multiple_alter_stmt_same_table") {
		t.Fatalf("同表多条 ALTER 应命中 merge_multiple_alter_stmt_same_table，实际: %v", keys)
	}
}

// 未绑定规范（参数全默认）：CTAS/CTL 闸门仍然生效（能力类参数默认禁止），其余不产出规范项
func TestOracleEngineDefaultParamsGateOnly(t *testing.T) {
	keys := collectRuleKeys(runOracle(t, "CREATE TABLE t_copy AS SELECT * FROM t_src;", config.DefaultInspectParams(), &oracleMetaStub{}))
	if !hasRuleKey(keys, "disable_create_table_as") {
		t.Fatalf("默认参数下 CTAS 仍应被闸门拦截，实际: %v", keys)
	}
	keys = collectRuleKeys(runOracle(t, oracleCompliantSQL, config.DefaultInspectParams(), &oracleMetaStub{}))
	if len(keys) != 0 {
		t.Fatalf("默认参数下合规 SQL 不应产出告警，实际: %v", keys)
	}
}
