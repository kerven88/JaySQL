package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

// prodLikeParams 模拟「默认生产环境规范」映射出的审核参数：
// 结构类规则（postgres.struct.*）在规范中为 must，DML 类为 suggest/must；
// 此处把对应参数全部打开，用于验证 PG 引擎的判定行为与规范参数一致。
func prodLikeParams() config.InspectParams {
	p := config.DefaultInspectParams()
	p.CHECK_TABLE_PRIMARY_KEY = true
	p.CHECK_COLUMN_COMMENT = true
	p.CHECK_TABLE_COMMENT = true
	p.CHECK_COLUMN_DEFAULT_VALUE = true
	p.ENABLE_COLUMN_NOT_NULL = true
	p.CHECK_TABLE_IDENTIFIER = true
	p.CHECK_COLUMN_IDENTIFIER = true
	p.CHECK_IDENTIFER_KEYWORD = true
	p.CHECK_PRIMARYKEY_USE_BIGINT = true
	p.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint"}
	p.PRIMARYKEY_MAX_KEY_PARTS = 3
	p.SECONDARY_INDEX_MAX_KEY_PARTS = 5
	p.MAX_INDEX_KEYS = 8
	p.COLUMN_MAX_CHAR_LENGTH = 64
	p.MAX_VARCHAR_LENGTH = 2048
	p.CHECK_UNIQ_INDEX_PREFIX = true
	p.CHECK_SECONDARY_INDEX_PREFIX = true
	p.UNQI_INDEX_PREFIX = "UNIQ_"
	p.SECONDARY_INDEX_PREFIX = "IDX_"
	p.ENABLE_FOREIGN_KEY = false
	p.ENABLE_CREATE_VIEW = false
	p.DML_MUST_HAVE_WHERE = true
	p.DML_DISABLE_SUBQUERY = true
	p.DML_DISABLE_LIMIT = true
	p.CHECK_DML_JOIN_WITH_ON = true
	p.MAX_INSERT_ROWS = 1000
	return p
}

func runPG(t *testing.T, sql string, params config.InspectParams) []ReturnData {
	t.Helper()
	svc := &SyntaxInspectService{
		DbType:        "PostgreSQL",
		SqlText:       sql,
		InspectParams: params,
	}
	data, err := svc.runPGInspect()
	if err != nil {
		t.Fatalf("PG 预审执行失败: %v", err)
	}
	return data
}

// 收集全部审核项的 rule_key
func collectRuleKeys(data []ReturnData) []string {
	var keys []string
	for _, row := range data {
		for _, sum := range row.Summary {
			keys = append(keys, sum.RuleKey)
		}
	}
	return keys
}

func hasRuleKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

const userPGOrderSQL = `-- 创建业务表，单主键标准模板
CREATE TABLE public.t_demo (
    id bigint GENERATED ALWAYS AS IDENTITY,
    name varchar(128) NOT NULL,
    status smallint NOT NULL DEFAULT 1,
    remark text,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz,
    CONSTRAINT pk_t_demo PRIMARY KEY (id)
);
-- 表注释
COMMENT ON TABLE public.t_demo IS '演示业务表';
-- 字段注释
COMMENT ON COLUMN public.t_demo.id IS '主键ID';
COMMENT ON COLUMN public.t_demo.name IS '名称';
COMMENT ON COLUMN public.t_demo.status IS '状态：0禁用，1正常，2删除';
COMMENT ON COLUMN public.t_demo.remark IS '备注信息';
COMMENT ON COLUMN public.t_demo.created_at IS '创建时间';
COMMENT ON COLUMN public.t_demo.updated_at IS '更新时间';
-- 业务普通索引（按需添加）
CREATE INDEX idx_t_demo_status ON public.t_demo(status);`

// 用户实际工单：语法解析必须通过（修复前为 SQL解析错误 near "IDENTITY"）
func TestPGEngineParsesUserOrderSQL(t *testing.T) {
	data := runPG(t, userPGOrderSQL, config.DefaultInspectParams())
	if len(data) != 9 {
		t.Fatalf("期望拆出 9 条语句，实际 %d 条", len(data))
	}
	for _, row := range data {
		if row.Type != "DDL" {
			t.Fatalf("语句类型识别错误: %v", row.Query)
		}
	}
	// 未绑定规范（参数全默认关闭）→ 不应产出任何规范项
	if keys := collectRuleKeys(data); len(keys) != 0 {
		t.Fatalf("未绑定规范时不应产出规范项，实际: %v", keys)
	}
}

// 生产规范下的正向校验：不该报的绝不报
func TestPGEngineNoFalsePositiveOnCompliantSQL(t *testing.T) {
	keys := collectRuleKeys(runPG(t, userPGOrderSQL, prodLikeParams()))
	for _, unexpected := range []string{
		"table_must_have_pk",          // 已有主键
		"limit_col_must_have_comment", // 每列都有 COMMENT ON COLUMN
		"limit_tbl_must_comment",      // 工单内有 COMMENT ON TABLE
		"limit_uniq_idx_name_fmt",     // 无唯一索引
		"limit_normal_idx_name_fmt",   // idx_ 前缀符合 IDX_
		"limit_pk_col_type",           // bigint 主键
		"limit_pk_column_count",       // 单列主键
		"limit_char_type_col_len",     // 无 char 列
		"limit_varchar_type_col_len",  // varchar(128) 未超限
		"limit_tbl_name_case",         // t_demo 小写合法
		"limit_col_name_case",         // 列名小写合法
		"limit_tbl_no_use_fk",         // 无外键
		"limit_tbl_idx_count",         // 索引数量未超限
		"limit_col_not_null",          // identity/text/时间类型均豁免
	} {
		if hasRuleKey(keys, unexpected) {
			t.Errorf("合规 SQL 不应命中规则 %s，实际命中: %v", unexpected, keys)
		}
	}
	// 规范要求「列要有默认值」：name 与 updated_at 未定义默认值，应如实报出
	if !hasRuleKey(keys, "limit_col_must_have_default") {
		t.Errorf("应命中 limit_col_要有default（name/updated_at 无默认值），实际: %v", keys)
	}
}

// 反例：各类结构违规应逐条命中
func TestPGEngineViolations(t *testing.T) {
	sql := `CREATE TABLE t_bad (
    id int,
    "Name" char(200),
    c_at timestamptz,
    stat varchar(5000),
    "user" varchar(20),
    ref_id int REFERENCES t_demo(id)
);
CREATE UNIQUE INDEX idx_bad ON t_bad(stat);
CREATE INDEX t_bad_stat ON t_bad(stat);
CREATE VIEW v_bad AS SELECT 1;
UPDATE t_bad SET stat = 'x';
DELETE FROM t_bad WHERE id IN (SELECT id FROM t_demo);
UPDATE t_bad a JOIN t_demo b SET a.stat = b.stat;
INSERT INTO t_bad (id) VALUES (1),(2);`
	keys := collectRuleKeys(runPG(t, sql, prodLikeParams()))
	for _, want := range []string{
		"table_must_have_pk",                      // 无主键
		"limit_col_must_have_comment",             // 无 COMMENT ON COLUMN
		"limit_tbl_must_comment",                  // 无 COMMENT ON TABLE
		"limit_pk_col_type",                       // 主键列类型不适用（此处无 pk 列，由构造决定）
		"limit_col_name_case",                     // "Name" 大写
		"col_name_no_keyword",                     // user 保留字
		"limit_char_type_col_len",                 // char(200) > 64
		"limit_varchar_type_col_len",              // varchar(5000) > 2048
		"limit_uniq_idx_name_fmt",                 // 唯一索引 idx_bad 不满足 uniq_
		"limit_normal_idx_name_fmt",               // 普通索引 t_bad_stat 不满足 idx_
		"disable_view_related_op",                 // 创建视图
		"limit_tbl_no_use_fk",                     // 外键
		"update_delete_stmt_suggest_where",        // UPDATE 无 WHERE
		"update_delete_stmt_where_subquery_check", // WHERE 子查询
		"update_delete_stmt_join_on_check",        // JOIN 无 ON
		"limit_tbl_idx_count",                     // 索引数量超限（8 个上限内需更多索引才触发，见下）
	} {
		if want == "limit_pk_col_type" || want == "limit_tbl_idx_count" {
			continue // 该反例未构造对应场景，单独在其它用例覆盖
		}
		if !hasRuleKey(keys, want) {
			t.Errorf("应命中规则 %s，实际命中: %v", want, keys)
		}
	}
}

// 主键列类型与索引数量规则
// 表注释规则（limit_tbl_must_comment）：工单内提供 COMMENT ON TABLE 即视为满足
func TestPGEngineTableComment(t *testing.T) {
	withComment := `CREATE TABLE t_c (id bigint PRIMARY KEY, name varchar(32) NOT NULL DEFAULT '');
COMMENT ON TABLE t_c IS '测试表';`
	keys := collectRuleKeys(runPG(t, withComment, prodLikeParams()))
	if hasRuleKey(keys, "limit_tbl_must_comment") {
		t.Errorf("已提供 COMMENT ON TABLE 不应命中 limit_tbl_must_comment，实际: %v", keys)
	}

	withoutComment := `CREATE TABLE t_c (id bigint PRIMARY KEY, name varchar(32) NOT NULL DEFAULT '');`
	keys = collectRuleKeys(runPG(t, withoutComment, prodLikeParams()))
	if !hasRuleKey(keys, "limit_tbl_must_comment") {
		t.Errorf("未提供 COMMENT ON TABLE 应命中 limit_tbl_must_comment，实际: %v", keys)
	}

	// 规则关闭时不检查
	off := prodLikeParams()
	off.CHECK_TABLE_COMMENT = false
	keys = collectRuleKeys(runPG(t, withoutComment, off))
	if hasRuleKey(keys, "limit_tbl_must_comment") {
		t.Errorf("规则关闭时不应命中 limit_tbl_must_comment，实际: %v", keys)
	}
}

func TestPGEnginePKTypeAndIndexCount(t *testing.T) {
	pkSQL := `CREATE TABLE t_pk (id int PRIMARY KEY, name varchar(32) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
COMMENT ON COLUMN t_pk.id IS 'x'; COMMENT ON COLUMN t_pk.name IS 'y'; COMMENT ON COLUMN t_pk.created_at IS 'z';`
	keys := collectRuleKeys(runPG(t, pkSQL, prodLikeParams()))
	if !hasRuleKey(keys, "limit_pk_col_type") {
		t.Errorf("int 主键应命中 limit_pk_col_type，实际: %v", keys)
	}

	var idxSQL strings.Builder
	idxSQL.WriteString(`CREATE TABLE t_idx (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, c1 int NOT NULL DEFAULT 0);
COMMENT ON COLUMN t_idx.id IS 'x'; COMMENT ON COLUMN t_idx.c1 IS 'y';`)
	for i := 0; i < 9; i++ {
		idxSQL.WriteString("\nCREATE INDEX idx_t_idx_c")
		idxSQL.WriteString(string(rune('a' + i)))
		idxSQL.WriteString(" ON t_idx(c1);")
	}
	keys = collectRuleKeys(runPG(t, idxSQL.String(), prodLikeParams()))
	if !hasRuleKey(keys, "limit_tbl_idx_count") {
		t.Errorf("索引数量超限应命中 limit_tbl_idx_count，实际: %v", keys)
	}
}

// 主键列数、索引列数上限
func TestPGEngineKeyPartsLimits(t *testing.T) {
	sql := `CREATE TABLE t_parts (a int NOT NULL, b int NOT NULL, c int NOT NULL, d int NOT NULL,
    CONSTRAINT pk_t_parts PRIMARY KEY (a, b, c, d));
COMMENT ON COLUMN t_parts.a IS '1'; COMMENT ON COLUMN t_parts.b IS '1';
COMMENT ON COLUMN t_parts.c IS '1'; COMMENT ON COLUMN t_parts.d IS '1';
CREATE INDEX idx_parts ON t_parts(a, b, c, d, a, b);`
	keys := collectRuleKeys(runPG(t, sql, prodLikeParams()))
	if !hasRuleKey(keys, "limit_pk_column_count") {
		t.Errorf("4 列主键应命中 limit_pk_include_col_的个数，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_single_index_column_count") {
		t.Errorf("6 列索引应命中 limit_单个idx_include_col_的个数，实际: %v", keys)
	}
}

// 索引必须显式命名 / INSERT 行数上限
func TestPGEngineMiscRules(t *testing.T) {
	ps := prodLikeParams()
	ps.MAX_INSERT_ROWS = 2
	sql := `CREATE TABLE t_misc (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, c1 int NOT NULL DEFAULT 0);
COMMENT ON COLUMN t_misc.id IS 'x'; COMMENT ON COLUMN t_misc.c1 IS 'y';
INSERT INTO t_misc (c1) VALUES (1),(2),(3);`
	keys := collectRuleKeys(runPG(t, sql, ps))
	if !hasRuleKey(keys, "limit_insert_values_row_count") {
		t.Errorf("3 行 INSERT 应命中 limit_insert_stmt_一条_insert_values_的总行数，实际: %v", keys)
	}
}

// 行号定位：多语句工单中每条语句的行号应正确
func TestPGEngineLineNumbers(t *testing.T) {
	data := runPG(t, userPGOrderSQL, config.DefaultInspectParams())
	if data[0].Line != 2 {
		t.Errorf("CREATE TABLE 应定位到第 2 行，实际 %d", data[0].Line)
	}
	if data[8].Line != 21 {
		t.Errorf("CREATE INDEX 应定位到第 21 行，实际 %d", data[8].Line)
	}
	if strings.Contains(data[0].Query, "--") {
		t.Errorf("Query 不应包含前导注释: %q", data[0].Query)
	}
}

// collectMessagesByRule 收集指定规则的审核消息
func collectMessagesByRule(data []ReturnData, want string) []string {
	var out []string
	for _, row := range data {
		for _, sum := range row.Summary {
			if sum.RuleKey == want {
				out = append(out, sum.Message)
			}
		}
	}
	return out
}

// 表字段数量上限（规则 limit_tbl_col_的count）
func TestPGEngineTableColumnCountLimit(t *testing.T) {
	build := func(cols int) string {
		var b strings.Builder
		b.WriteString("CREATE TABLE t_demo (\n")
		b.WriteString("    id bigint GENERATED ALWAYS AS IDENTITY,\n")
		for i := 1; i < cols; i++ {
			fmt.Fprintf(&b, "    c%d int NOT NULL DEFAULT 0,\n", i)
		}
		b.WriteString("    CONSTRAINT pk_t_demo PRIMARY KEY (id)\n);")
		return b.String()
	}

	// 8 列 > max 3：命中且文案回显阈值与实际列数
	ps := prodLikeParams()
	ps.MAX_TABLE_COLUMNS = 3
	msgs := collectMessagesByRule(runPG(t, build(8), ps), "limit_table_column_count")
	if len(msgs) != 1 {
		t.Fatalf("8 列应命中 1 条字段数量告警，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "max = 3") || !strings.Contains(msgs[0], "当前 8") {
		t.Errorf("告警文案须回显配置阈值与当前列数，实际: %s", msgs[0])
	}

	// 恰好等于上限：不告警
	ps = prodLikeParams()
	ps.MAX_TABLE_COLUMNS = 8
	if got := collectMessagesByRule(runPG(t, build(8), ps), "limit_table_column_count"); len(got) != 0 {
		t.Errorf("列数等于上限不应告警，实际: %v", got)
	}

	// 0 表示不限制（规则关闭时的取值）
	ps = prodLikeParams()
	ps.MAX_TABLE_COLUMNS = 0
	if got := collectMessagesByRule(runPG(t, build(8), ps), "limit_table_column_count"); len(got) != 0 {
		t.Errorf("上限为 0（不限制）不应告警，实际: %v", got)
	}
}

// 唯一索引命名：规范配置了正则（uk_\w+）时必须以「完整匹配正则」为准，
// 不能因为默认前缀 UNIQ_ 命中就放行 —— 对应线上 tk 工单里
// CREATE UNIQUE INDEX uniq_t_demo_status ON public.t_demo(status); 漏检。
func TestPGEngineUniqueIndexNameRegex(t *testing.T) {
	sql := `CREATE Unique INDEX uniq_t_demo_status ON public.t_demo(status);`

	// 1) 配了正则：uniq_t_demo_status 不匹配 uk_\w+，必须命中且回显正则
	ps := prodLikeParams()
	ps.UNIQ_INDEX_REGEX = `uk_\w+`
	msgs := collectMessagesByRule(runPG(t, sql, ps), "limit_uniq_idx_name_fmt")
	if len(msgs) != 1 {
		t.Fatalf("配了正则 uk_\\w+ 时 uniq_t_demo_status 应命中 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], `uk_\w+`) {
		t.Errorf("告警文案必须回显生效的正则，实际: %s", msgs[0])
	}

	// 2) 合规名字：不告警
	okSQL := `CREATE UNIQUE INDEX uk_t_demo_status ON public.t_demo(status);`
	if got := collectMessagesByRule(runPG(t, okSQL, ps), "limit_uniq_idx_name_fmt"); len(got) != 0 {
		t.Errorf("uk_t_demo_status 完整匹配 uk_\\w+，不应告警，实际: %v", got)
	}

	// 3) 完整匹配：子串命中不得放行（非锚定会误放行 xuk_abc）
	badSQL := `CREATE UNIQUE INDEX xuk_abc ON public.t_demo(status);`
	if got := collectMessagesByRule(runPG(t, badSQL, ps), "limit_uniq_idx_name_fmt"); len(got) != 1 {
		t.Errorf("xuk_abc 仅子串命中 uk_\\w+，必须告警，实际: %v", got)
	}

	// 4) 未配正则：回退前缀（保持既有行为不回退）
	ps2 := prodLikeParams()
	if got := collectMessagesByRule(runPG(t, sql, ps2), "limit_uniq_idx_name_fmt"); len(got) != 0 {
		t.Errorf("未配正则时 uniq_ 命中默认前缀 UNIQ_，不应告警，实际: %v", got)
	}
}

// 表必须包含某些列（tbl_需要include_某些col）：缺少必需列时命中，齐全时放行，规则关闭不检查
func TestPGEngineTableRequiredColumns(t *testing.T) {
	sql := `CREATE TABLE t_demo (
	    id bigint GENERATED ALWAYS AS IDENTITY,
	    name varchar(50) NOT NULL,
	    CONSTRAINT pk_t_demo PRIMARY KEY (id)
	);`

	// 1) 缺少 created_at：命中且文案回显缺失列名
	ps := prodLikeParams()
	ps.TABLE_REQUIRED_COLUMNS = []string{"id", "created_at"}
	msgs := collectMessagesByRule(runPG(t, sql, ps), "table_must_include_required_columns")
	if len(msgs) != 1 {
		t.Fatalf("缺少 created_at 应命中 1 条必含列告警，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "created_at") {
		t.Errorf("告警文案应回显缺失列名，实际: %s", msgs[0])
	}

	// 2) 列齐全：不告警
	fullSQL := `CREATE TABLE t_demo (
	    id bigint GENERATED ALWAYS AS IDENTITY,
	    name varchar(50) NOT NULL,
	    created_at timestamptz NOT NULL DEFAULT now(),
	    CONSTRAINT pk_t_demo PRIMARY KEY (id)
	);`
	if got := collectMessagesByRule(runPG(t, fullSQL, ps), "table_must_include_required_columns"); len(got) != 0 {
		t.Errorf("包含全部必需列不应告警，实际: %v", got)
	}

	// 3) 规则关闭（列表为空）：不告警
	ps = prodLikeParams()
	ps.TABLE_REQUIRED_COLUMNS = nil
	if got := collectMessagesByRule(runPG(t, sql, ps), "table_must_include_required_columns"); len(got) != 0 {
		t.Errorf("规则关闭不应告警，实际: %v", got)
	}
}

// 索引必须显式命名（idx_需要set_name）：规则开启时拦截未命名索引，规则关闭不再无条件报警
func TestPGEngineIndexNameRequired(t *testing.T) {
	sql := `CREATE INDEX ON public.t_demo(status);`

	// 1) 规则开启：命中
	ps := prodLikeParams()
	ps.CHECK_INDEX_NAME_REQUIRED = true
	msgs := collectMessagesByRule(runPG(t, sql, ps), "index_must_have_name")
	if len(msgs) != 1 {
		t.Fatalf("规则开启时未命名索引应命中 1 条，实际 %d 条: %v", len(msgs), msgs)
	}

	// 2) 规则关闭：不再报警（修复前该消息不受规则开关控制）
	ps = prodLikeParams()
	ps.CHECK_INDEX_NAME_REQUIRED = false
	if got := collectMessagesByRule(runPG(t, sql, ps), "index_must_have_name"); len(got) != 0 {
		t.Errorf("规则关闭不应告警，实际: %v", got)
	}
}

// 普通索引名格式（limit_normal_idx_name_fmt）：配正则 idx1_\w+ 时完整匹配，不匹配拒绝且回显正则
func TestPGEngineNormalIndexNameRegex(t *testing.T) {
	badSQL := `CREATE INDEX idx_t_demo_status ON public.t_demo(status);`

	// 1) idx_t_demo_status 不匹配 idx1_\w+：命中且回显正则
	ps := prodLikeParams()
	ps.CHECK_SECONDARY_INDEX_PREFIX = true
	ps.SECONDARY_INDEX_REGEX = `idx1_\w+`
	msgs := collectMessagesByRule(runPG(t, badSQL, ps), "limit_normal_idx_name_fmt")
	if len(msgs) != 1 {
		t.Fatalf("配了正则 idx1_\\w+ 时 idx_t_demo_status 应命中 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], `idx1_\w+`) {
		t.Errorf("告警文案必须回显生效的正则，实际: %s", msgs[0])
	}

	// 2) 合规名字 idx1_t_demo_status：完整匹配不告警
	okSQL := `CREATE INDEX idx1_t_demo_status ON public.t_demo(status);`
	if got := collectMessagesByRule(runPG(t, okSQL, ps), "limit_normal_idx_name_fmt"); len(got) != 0 {
		t.Errorf("idx1_t_demo_status 完整匹配 idx1_\\w+，不应告警，实际: %v", got)
	}

	// 3) 完整匹配：子串命中不得放行（xidx1_abc）
	if got := collectMessagesByRule(runPG(t, `CREATE INDEX xidx1_abc ON public.t_demo(status);`, ps), "limit_normal_idx_name_fmt"); len(got) != 1 {
		t.Errorf("xidx1_abc 仅子串命中 idx1_\\w+，必须告警，实际: %v", got)
	}
}

// 主键列类型白名单可配置：int 命中默认白名单 [bigint,int] 放行，varchar 仍拦截
func TestPGEnginePKTypeConfigurableWhitelist(t *testing.T) {
	intPK := `CREATE TABLE t_pk_int (id int PRIMARY KEY, name varchar(32) NOT NULL DEFAULT '');
COMMENT ON COLUMN t_pk_int.id IS 'x'; COMMENT ON COLUMN t_pk_int.name IS 'y';`

	// 1) 白名单 [bigint,int]：int 主键放行
	ps := prodLikeParams()
	ps.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint", "int"}
	if got := collectRuleKeys(runPG(t, intPK, ps)); hasRuleKey(got, "limit_pk_col_type") {
		t.Errorf("int 主键在白名单 [bigint,int] 内不应命中，实际: %v", got)
	}

	// 2) 白名单 [bigint]：int 主键命中且回显白名单
	ps = prodLikeParams()
	ps.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint"}
	msgs := collectMessagesByRule(runPG(t, intPK, ps), "limit_pk_col_type")
	if len(msgs) != 1 {
		t.Fatalf("int 主键不在白名单 [bigint] 内应命中 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "bigint") {
		t.Errorf("告警文案应回显白名单，实际: %s", msgs[0])
	}

	// 3) 白名单含别名归一化：int8 视同 bigint，integer 视同 int
	ps = prodLikeParams()
	ps.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint", "int"}
	aliasPK := `CREATE TABLE t_pk_alias (id integer PRIMARY KEY, name varchar(32) NOT NULL DEFAULT '');
COMMENT ON COLUMN t_pk_alias.id IS 'x'; COMMENT ON COLUMN t_pk_alias.name IS 'y';`
	if got := collectRuleKeys(runPG(t, aliasPK, ps)); hasRuleKey(got, "limit_pk_col_type") {
		t.Errorf("integer 主键（int 别名）在白名单内不应命中，实际: %v", got)
	}

	// 4) 非整型列进白名单也能正确匹配（varchar 主键、白名单含 varchar）
	ps = prodLikeParams()
	ps.PRIMARYKEY_SUPPORT_TYPES = []string{"varchar"}
	varcharPK := `CREATE TABLE t_pk_vc (code varchar(32) PRIMARY KEY, name varchar(32) NOT NULL DEFAULT '');
COMMENT ON COLUMN t_pk_vc.code IS 'x'; COMMENT ON COLUMN t_pk_vc.name IS 'y';`
	if got := collectRuleKeys(runPG(t, varcharPK, ps)); hasRuleKey(got, "limit_pk_col_type") {
		t.Errorf("varchar 主键在白名单 [varchar] 内不应命中，实际: %v", got)
	}
}

// 限制冗余索引（limit_tbl_redundant_idx）：前缀覆盖、同列重复命中；规则关闭/非冗余组合放行
func TestPGEngineRedundantIndex(t *testing.T) {
	const prefixCovered = `CREATE INDEX idx1_t_demo_a ON public.t_demo(a);
CREATE INDEX idx1_t_demo_ab ON public.t_demo(a, b);`

	// 1) 规则开启：窄索引 (a) 被宽索引 (a,b) 覆盖 → 由宽索引语句报 1 条，且回显两侧字段组合
	ps := prodLikeParams()
	ps.ENABLE_REDUNDANT_INDEX = false
	msgs := collectMessagesByRule(runPG(t, prefixCovered, ps), "limit_tbl_redundant_idx")
	if len(msgs) != 1 {
		t.Fatalf("前缀覆盖应命中 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "idx1_t_demo_ab") || !strings.Contains(msgs[0], "idx1_t_demo_a") {
		t.Errorf("告警文案应回显冗余双方，实际: %s", msgs[0])
	}

	// 2) 同列重复：两个索引列组合完全相同 → 只报 1 条（由位置靠后的语句报）
	dup := `CREATE INDEX idx1_dup1 ON public.t_demo(a, b);
CREATE INDEX idx1_dup2 ON public.t_demo(a, b);`
	if got := collectMessagesByRule(runPG(t, dup, ps), "limit_tbl_redundant_idx"); len(got) != 1 {
		t.Errorf("同列重复索引应命中 1 条，实际 %d 条: %v", len(got), got)
	}

	// 3) 非冗余：列顺序不同（(a,b) 与 (b,a)）不构成前缀覆盖 → 0 条
	diffOrder := `CREATE INDEX idx1_ord1 ON public.t_demo(a, b);
CREATE INDEX idx1_ord2 ON public.t_demo(b, a);`
	if got := collectMessagesByRule(runPG(t, diffOrder, ps), "limit_tbl_redundant_idx"); len(got) != 0 {
		t.Errorf("列顺序不同不构成冗余，不应命中，实际: %v", got)
	}

	// 4) 规则关闭（允许冗余）→ 0 条
	psOff := prodLikeParams()
	psOff.ENABLE_REDUNDANT_INDEX = true
	if got := collectMessagesByRule(runPG(t, prefixCovered, psOff), "limit_tbl_redundant_idx"); len(got) != 0 {
		t.Errorf("规则关闭不应命中，实际: %v", got)
	}
}
