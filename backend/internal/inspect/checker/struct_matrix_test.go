package checker

// 结构类规范规则全量矩阵测试（34 个能力项 × MySQL/PostgreSQL × 开关/限制维度）。
//
// 测试口径：
//   - 开关功能：规则参数开启时违反 SQL 触发告警；参数关闭（off 语义）时不触发。
//   - 级别设置：引擎层 must/suggest 触发行为一致（级别仅影响订单闸门严重度），
//     该维度在 internal/inspect/services（applyStandardRule）与
//     internal/orders/services（resolveInspectSeverity）的矩阵测试中覆盖。
//   - 规则限制：违反 SQL 触发 + 合规 SQL 通过。
//
// 测试直连引擎（不经过 BuildInspectParamsForInstance），参数按用例注入；
// MySQL 侧连接本机 127.0.0.1:3306（goinsight_struct_test 库），PG 侧不连库
// （依赖元信息的子检查按引擎设计静默跳过，不影响本矩阵断言）。
//
// 测试结果写入 /tmp/gi_struct_matrix.json，由报告脚本汇总为 Markdown。

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/pkg/kv"

	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"
)

const (
	matrixMySQLHost = "127.0.0.1"
	matrixMySQLPort = 3306
	matrixMySQLUser = "root"
	matrixMySQLDB   = "goinsight_struct_test"
	matrixOutPath   = "/tmp/gi_struct_matrix.json"
)

// 数据库密码不写进仓库，改从环境变量 MATRIX_MYSQL_PASS 读取。
// 未设置时 TestStructRuleMatrix 会自动跳过（见该函数开头），
// 需要跑这个矩阵测试时在本地导出该变量即可。
func matrixMySQLPass() string { return os.Getenv("MATRIX_MYSQL_PASS") }

// skipIfNoMatrixMySQL 在没有配置测试库密码时跳过用例。
//
// 这些用例需要一台真实的 MySQL 来建表并跑审核规则，密码由环境变量提供。
// 未配置时跳过而不是失败：开源仓库里不该硬编码任何真实凭据，
// 也不该让 clone 下来的人在没准备环境时看到一片红。
func skipIfNoMatrixMySQL(t *testing.T) {
	t.Helper()
	if matrixMySQLPass() == "" {
		t.Skip("未设置 MATRIX_MYSQL_PASS 环境变量，跳过需要真实 MySQL 的用例")
	}
}

type mxExpect struct {
	RuleKey     string // 期望出现的 summary rule key（引擎实际 key，允许与规范 key 不一致）
	MsgContains string // 或期望消息包含的文本（规则未关联 key 时用文案断言）
	Absent      bool   // true = 期望不触发（开关关闭 / 合规 SQL）
}

type mxCase struct {
	Item  string // 能力项名称
	Key   string // 规范 rule key 后缀（标准目录口径）
	DB    string // mysql / postgres
	Dim   string // 开关-启用 / 开关-关闭 / 限制-违反 / 限制-合规
	Name  string // 用例描述
	SQL   string
	Apply func(*config.InspectParams)
	Exp   mxExpect
	Note  string
}

type mxResult struct {
	Item     string   `json:"item"`
	Key      string   `json:"key"`
	DB       string   `json:"db"`
	Dim      string   `json:"dim"`
	Name     string   `json:"name"`
	SQL      string   `json:"sql"`
	Expected string   `json:"expected"`
	Actual   []string `json:"actual"`
	Pass     bool     `json:"pass"`
	Note     string   `json:"note"`
}

// ---------- 运行器 ----------

var mysqlOnce sync.Once

func ensureMySQLFixtures(t *testing.T) {
	t.Helper()
	mysqlOnce.Do(func() {
		db := &dao.DB{User: matrixMySQLUser, Password: matrixMySQLPass(), Host: matrixMySQLHost, Port: matrixMySQLPort, Database: matrixMySQLDB, DbType: "MySQL"}
		if err := db.Execute("CREATE TABLE IF NOT EXISTS t_fix (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, name varchar(64) NOT NULL DEFAULT '', status tinyint NOT NULL DEFAULT 1, created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"); err != nil {
			t.Fatalf("创建固定表失败: %v", err)
		}
	})
}

// runMySQLInspect 复刻 SyntaxInspectService.Run 的分发逻辑，但参数由用例注入（不走规范加载）
func runMySQLInspect(t *testing.T, sqlText string, params config.InspectParams) ([]ReturnData, error) {
	t.Helper()
	ensureMySQLFixtures(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)

	s := &SyntaxInspectService{
		C:             c,
		DbUser:        matrixMySQLUser,
		DbPassword: matrixMySQLPass(),
		DbHost:        matrixMySQLHost,
		DbPort:        matrixMySQLPort,
		DBSchema:      matrixMySQLDB,
		DbType:        "MySQL",
		SqlText:       sqlText,
		InspectParams: params,
	}
	s.initDB()

	requestID := ""
	cache := kv.NewKVCache(requestID)
	defer cache.Clear(requestID)

	dbVars, err := dao.GetDBVars(s.DB)
	if err != nil {
		return nil, fmt.Errorf("获取DB变量失败: %v", err)
	}
	for k, v := range dbVars {
		cache.Put(k, v)
	}
	s.Charset = dbVars["dbCharset"]

	if err := s.parser(); err != nil {
		return nil, err
	}

	st := Stmt{s}
	var returnData []ReturnData
	var mergeAlters []string
	for _, stmt := range s.Audit.TiStmt {
		fingerId := fmt.Sprintf("f%d", len(returnData))
		cache.Put(fingerId, true)
		line := s.computeLine(stmt.Text())
		switch stmt.(type) {
		case *ast.CreateTableStmt:
			data := st.CreateTableStmt(stmt, cache, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.CreateViewStmt:
			data := st.CreateViewStmt(stmt, cache, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.AlterTableStmt:
			data, mergeAlter := st.AlterTableStmt(stmt, cache, fingerId)
			data.Line = line
			mergeAlters = append(mergeAlters, mergeAlter)
			returnData = append(returnData, data)
		case *ast.DropTableStmt, *ast.TruncateTableStmt:
			data := st.DropTableStmt(stmt, cache, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.DeleteStmt, *ast.InsertStmt, *ast.UpdateStmt:
			data := st.DMLStmt(stmt, cache, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.CreateDatabaseStmt:
			data := st.CreateDatabaseStmt(stmt, cache, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.AlterDatabaseStmt:
			data := st.AlterDatabaseStmt(stmt, cache, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		default:
			data := ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "", Line: line}
			data.Summary = append(data.Summary, controllers.SummaryItem{Level: LevelWarn, Message: "未识别或禁止的审核语句，请联系数据库管理员"})
			returnData = append(returnData, data)
		}
	}
	if len(mergeAlters) > 1 {
		mergeData := s.mergeAlters(cache, mergeAlters)
		if len(mergeData.Summary) > 0 {
			returnData = append(returnData, mergeData)
		}
	}
	return returnData, nil
}

// runPGInspectForTest PG 侧不连库（db 为 nil，依赖元信息的子检查静默跳过）
func runPGInspectForTest(t *testing.T, sqlText string, params config.InspectParams) ([]ReturnData, error) {
	t.Helper()
	s := &SyntaxInspectService{
		DbType:        "PostgreSQL",
		SqlText:       sqlText,
		InspectParams: params,
	}
	return s.runPGInspect()
}

// ---------- 断言 ----------

func summariesOf(datas []ReturnData) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	for _, d := range datas {
		items = append(items, d.Summary...)
	}
	return items
}

func assertCase(t *testing.T, mc mxCase) mxResult {
	t.Helper()
	var (
		datas []ReturnData
		err   error
	)
	params := config.DefaultInspectParams()
	if mc.Apply != nil {
		mc.Apply(&params)
	}
	switch mc.DB {
	case "mysql":
		datas, err = runMySQLInspect(t, mc.SQL, params)
	case "postgres":
		datas, err = runPGInspectForTest(t, mc.SQL, params)
	}

	res := mxResult{Item: mc.Item, Key: mc.Key, DB: mc.DB, Dim: mc.Dim, Name: mc.Name, SQL: mc.SQL, Note: mc.Note}
	if err != nil {
		res.Actual = append(res.Actual, "引擎错误: "+err.Error())
		res.Expected = expectText(mc)
		res.Pass = false
		return res
	}

	items := summariesOf(datas)
	for _, it := range items {
		line := fmt.Sprintf("[%s] key=%q msg=%s", it.Level, it.RuleKey, it.Message)
		res.Actual = append(res.Actual, line)
	}

	expectKey := mc.Exp.RuleKey
	if expectKey == "" {
		expectKey = mc.Key
	}
	if mc.Exp.Absent {
		// 期望不触发：既不出现规范 key，也不出现引擎 key，也不命中文案
		for _, it := range items {
			if expectKey != "" && it.RuleKey == expectKey {
				res.Pass = false
				res.Expected = "不触发规则（key=" + expectKey + "）"
				return res
			}
			if mc.Exp.MsgContains != "" && strings.Contains(it.Message, mc.Exp.MsgContains) {
				res.Pass = false
				res.Expected = "不触发规则（文案含 " + mc.Exp.MsgContains + "）"
				return res
			}
			// Absent 且未给断言锚点时按 key 兜底
			if mc.Exp.RuleKey == "" && mc.Exp.MsgContains == "" && it.RuleKey == mc.Key {
				res.Pass = false
				res.Expected = "不触发规则（key=" + mc.Key + "）"
				return res
			}
		}
		res.Pass = true
		res.Expected = "不触发规则"
		return res
	}

	// 期望触发：命中 key 或文案任一即可
	res.Expected = "触发规则"
	if expectKey != "" {
		res.Expected += "（key=" + expectKey
		if mc.Exp.RuleKey != "" && mc.Exp.RuleKey != mc.Key {
			res.Expected += "，注意引擎 key 与规范 key 不一致"
		}
		res.Expected += "）"
	}
	if mc.Exp.MsgContains != "" {
		res.Expected += "（文案含 " + mc.Exp.MsgContains + "）"
	}
	for _, it := range items {
		if expectKey != "" && it.RuleKey == expectKey {
			res.Pass = true
			return res
		}
		if mc.Exp.MsgContains != "" && strings.Contains(it.Message, mc.Exp.MsgContains) {
			res.Pass = true
			return res
		}
	}
	res.Pass = false
	return res
}

func expectText(mc mxCase) string {
	if mc.Exp.Absent {
		return "不触发规则"
	}
	expectKey := mc.Exp.RuleKey
	if expectKey == "" {
		expectKey = mc.Key
	}
	if expectKey != "" {
		return "触发规则（key=" + expectKey + "）"
	}
	if mc.Exp.MsgContains != "" {
		return "触发规则（文案含 " + mc.Exp.MsgContains + "）"
	}
	return "触发规则"
}

// ---------- 用例表 ----------

func buildMatrixCases() []mxCase {
	cases := make([]mxCase, 0, 220)
	add := func(c mxCase) { cases = append(cases, c) }

	// 便捷构造：同一条规则的三类用例（开关-启用违反 / 开关-关闭 / 限制-合规）
	tri := func(item, key, db, violationSQL, passSQL string, on func(*config.InspectParams), off func(*config.InspectParams), expKey, expMsg, note string) {
		add(mxCase{Item: item, Key: key, DB: db, Dim: "开关-启用违反", Name: "规则启用时违反 SQL 触发", SQL: violationSQL, Apply: on, Exp: mxExpect{RuleKey: expKey, MsgContains: expMsg}, Note: note})
		add(mxCase{Item: item, Key: key, DB: db, Dim: "开关-关闭", Name: "规则关闭时不触发", SQL: violationSQL, Apply: off, Exp: mxExpect{Absent: true, RuleKey: expKey, MsgContains: expMsg}, Note: note})
		add(mxCase{Item: item, Key: key, DB: db, Dim: "限制-合规", Name: "规则启用时合规 SQL 通过", SQL: passSQL, Apply: on, Exp: mxExpect{Absent: true, RuleKey: expKey, MsgContains: expMsg}, Note: note})
	}

	// ================= MySQL 侧 =================

	// 1 限制自增列名字为 ID（引擎未实现）
	triMySQL := func(item, key, vSQL, pSQL string, on, off func(*config.InspectParams), expKey, expMsg, note string) {
		tri(item, key, "mysql", vSQL, pSQL, on, off, expKey, expMsg, note)
	}

	triMySQL("限制建表自增初始值", "limit_create_table_auto_inc_init",
		"CREATE TABLE t_autoinc_init (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB AUTO_INCREMENT=100",
		"CREATE TABLE t_autoinc_init (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB AUTO_INCREMENT=1",
		func(p *config.InspectParams) {
			p.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE = true
			p.TABLE_AUTOINCREMENT_INIT_VALUE = 1
		},
		func(p *config.InspectParams) { p.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE = false },
		"limit_create_table_auto_inc_init", "", "")

	triMySQL("限制自增列名字为 ID", "limit_auto_inc_col_name_is_id",
		"CREATE TABLE t_ai_name (uid bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_ai_name (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = true },
		func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = false },
		"limit_auto_inc_col_name_is_id", "", "")

	triMySQL("限制自增列为无符号", "limit_auto_inc_col_is_unsigned",
		"CREATE TABLE t_ai_unsigned (id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_ai_unsigned (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_UNSIGNED = true },
		func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_UNSIGNED = false },
		"limit_auto_inc_col_is_unsigned", "", "")

	// ALTER 语句路径：ADD / MODIFY 自增列
	add(mxCase{Item: "限制自增列名字为 ID", Key: "limit_auto_inc_col_name_is_id", DB: "mysql", Dim: "限制-违反", Name: "ALTER ADD 自增列名非 id",
		SQL:   "ALTER TABLE t_fix ADD COLUMN uid bigint unsigned NOT NULL AUTO_INCREMENT, ADD UNIQUE KEY uniq_uid (uid)",
		Apply: func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = true }, Exp: mxExpect{RuleKey: "limit_auto_inc_col_name_is_id"}, Note: ""})
	add(mxCase{Item: "限制自增列为无符号", Key: "limit_auto_inc_col_is_unsigned", DB: "mysql", Dim: "限制-违反", Name: "ALTER ADD 有符号自增列",
		SQL:   "ALTER TABLE t_fix ADD COLUMN seq bigint NOT NULL AUTO_INCREMENT, ADD UNIQUE KEY idx_seq (seq)",
		Apply: func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_UNSIGNED = true }, Exp: mxExpect{RuleKey: "limit_auto_inc_col_is_unsigned"}, Note: ""})
	add(mxCase{Item: "限制自增列为无符号", Key: "limit_auto_inc_col_is_unsigned", DB: "mysql", Dim: "限制-合规", Name: "ALTER MODIFY 为无符号自增",
		SQL:   "ALTER TABLE t_fix MODIFY COLUMN id bigint unsigned NOT NULL AUTO_INCREMENT",
		Apply: func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_UNSIGNED = true }, Exp: mxExpect{Absent: true, RuleKey: "limit_auto_inc_col_is_unsigned"}, Note: ""})
	add(mxCase{Item: "限制自增列名字为 ID", Key: "limit_auto_inc_col_name_is_id", DB: "mysql", Dim: "限制-合规", Name: "ALTER MODIFY 自增列名 id",
		SQL:   "ALTER TABLE t_fix MODIFY COLUMN id bigint unsigned NOT NULL AUTO_INCREMENT",
		Apply: func(p *config.InspectParams) { p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = true }, Exp: mxExpect{Absent: true, RuleKey: "limit_auto_inc_col_name_is_id"}, Note: ""})

	triMySQL("限制 CHAR 类型字段长度", "limit_char_type_col_len",
		"CREATE TABLE t_char_len (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 char(128)) ENGINE=InnoDB",
		"CREATE TABLE t_char_len (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 char(64)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.COLUMN_MAX_CHAR_LENGTH = 64 },
		func(p *config.InspectParams) { p.COLUMN_MAX_CHAR_LENGTH = 0 },
		"limit_char_type_col_len", "", "")

	triMySQL("限制字段名大小写", "limit_col_name_case",
		"CREATE TABLE t_col_case (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, Name varchar(10)) ENGINE=InnoDB",
		"CREATE TABLE t_col_case (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, name varchar(10)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_COLUMN_IDENTIFIER = true; p.COLUMN_NAME_CASE = "lower" },
		func(p *config.InspectParams) { p.CHECK_COLUMN_IDENTIFIER = false },
		"limit_col_name_case", "", "")

	// 字段名关键字：引擎 key 为 col_name_no_keyword（与规范 key 不一致，级别映射退化为文案匹配）
	triMySQL("字段名不能是关键字", "col_name_no_keyword",
		"CREATE TABLE t_col_keyword (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, `select` varchar(10)) ENGINE=InnoDB",
		"CREATE TABLE t_col_keyword (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, customer_name varchar(10)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = true },
		func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = false },
		"col_name_no_keyword", "", "发现：引擎 key=col_name_no_keyword 与规范 key 不一致")

	triMySQL("限制列要有注释", "limit_col_must_have_comment",
		"CREATE TABLE t_col_comment (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10)) ENGINE=InnoDB",
		"CREATE TABLE t_col_comment (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10) COMMENT 'c1') ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_COLUMN_COMMENT = true },
		func(p *config.InspectParams) { p.CHECK_COLUMN_COMMENT = false },
		"limit_col_must_have_comment", "", "")

	triMySQL("限制表字段的数量", "limit_table_column_count",
		"CREATE TABLE t_col_count (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int, c3 int, c4 int) ENGINE=InnoDB",
		"CREATE TABLE t_col_count (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.MAX_TABLE_COLUMNS = 3 },
		func(p *config.InspectParams) { p.MAX_TABLE_COLUMNS = 0 },
		"limit_table_column_count", "", "")

	triMySQL("限制表必须注释", "limit_tbl_must_comment",
		"CREATE TABLE t_tbl_comment (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_tbl_comment (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) COMMENT='demo' ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_TABLE_COMMENT = true },
		func(p *config.InspectParams) { p.CHECK_TABLE_COMMENT = false },
		"limit_tbl_must_comment", "", "")

	triMySQL("表要有主键", "table_must_have_pk",
		"CREATE TABLE t_tbl_pk (id bigint unsigned NOT NULL, c1 int) ENGINE=InnoDB",
		"CREATE TABLE t_tbl_pk (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_TABLE_PRIMARY_KEY = true },
		func(p *config.InspectParams) { p.CHECK_TABLE_PRIMARY_KEY = false },
		"table_must_have_pk", "", "")

	triMySQL("限制表存储引擎", "limit_table_storage_engine",
		"CREATE TABLE t_tbl_engine (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=MEMORY",
		"CREATE TABLE t_tbl_engine (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_TABLE_ENGINE = true },
		func(p *config.InspectParams) { p.CHECK_TABLE_ENGINE = false },
		"limit_table_storage_engine", "", "")

	triMySQL("限制唯一索引名格式", "limit_uniq_idx_name_fmt",
		"CREATE TABLE t_uniq_idx (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, UNIQUE KEY uk_c1 (c1)) ENGINE=InnoDB",
		"CREATE TABLE t_uniq_idx (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, UNIQUE KEY UNIQ_c1 (c1)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_UNIQ_INDEX_PREFIX = true },
		func(p *config.InspectParams) { p.CHECK_UNIQ_INDEX_PREFIX = false },
		"", "唯一索引", "发现：引擎未关联规则 key，级别映射退化为文案匹配")

	triMySQL("限制 VARCHAR 类型字段长度", "limit_varchar_type_col_len",
		"CREATE TABLE t_vc_len (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(2048)) ENGINE=InnoDB",
		"CREATE TABLE t_vc_len (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(256)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.MAX_VARCHAR_LENGTH = 1024 },
		func(p *config.InspectParams) { p.MAX_VARCHAR_LENGTH = 0 },
		"limit_varchar_type_col_len", "", "")

	triMySQL("限制列要有默认值", "limit_col_must_have_default",
		"CREATE TABLE t_col_default (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10)) ENGINE=InnoDB",
		"CREATE TABLE t_col_default (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10) NOT NULL DEFAULT '') ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_COLUMN_DEFAULT_VALUE = true },
		func(p *config.InspectParams) { p.CHECK_COLUMN_DEFAULT_VALUE = false },
		"limit_col_must_have_default", "", "")

	triMySQL("限制列不可空（NOT NULL)", "limit_col_not_null",
		"CREATE TABLE t_col_notnull (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10)) ENGINE=InnoDB",
		"CREATE TABLE t_col_notnull (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10) NOT NULL) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.ENABLE_COLUMN_NOT_NULL = true },
		func(p *config.InspectParams) { p.ENABLE_COLUMN_NOT_NULL = false },
		"limit_col_not_null", "", "")

	triMySQL("限制列不能使用部分数据类型", "limit_col_no_partial_data_type",
		"CREATE TABLE t_col_type (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 double) ENGINE=InnoDB",
		"CREATE TABLE t_col_type (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 decimal(10,2)) ENGINE=InnoDB",
		func(p *config.InspectParams) {
			p.CHECK_COLUMN_FORBIDDEN_TYPES = true
			p.COLUMN_FORBIDDEN_TYPES = []string{"double", "float", "enum", "bit", "set"}
		},
		func(p *config.InspectParams) { p.CHECK_COLUMN_FORBIDDEN_TYPES = false; p.COLUMN_FORBIDDEN_TYPES = nil },
		"limit_col_no_partial_data_type", "", "")

	add(mxCase{Item: "限制库的字符集", Key: "limit_database_charset", DB: "mysql", Dim: "限制-违反", Name: "建库未指定/指定非白名单字符集",
		SQL: "CREATE DATABASE struct_test_db_x DEFAULT CHARACTER SET=latin1",
		Apply: func(p *config.InspectParams) {
			p.CHECK_DATABASE_CHARSET = true
			p.DATABASE_SUPPORT_CHARSET = []map[string]string{{"charset": "utf8mb4"}}
		},
		Exp: mxExpect{RuleKey: "limit_database_charset"}, Note: ""})
	add(mxCase{Item: "限制库的字符集", Key: "limit_database_charset", DB: "mysql", Dim: "限制-合规", Name: "建库指定白名单字符集",
		SQL: "CREATE DATABASE struct_test_db_x DEFAULT CHARACTER SET=utf8mb4",
		Apply: func(p *config.InspectParams) {
			p.CHECK_DATABASE_CHARSET = true
			p.DATABASE_SUPPORT_CHARSET = []map[string]string{{"charset": "utf8mb4"}}
		},
		Exp: mxExpect{Absent: true, RuleKey: "limit_database_charset"}, Note: ""})

	add(mxCase{Item: "限制库的字符集", Key: "limit_database_charset", DB: "mysql", Dim: "开关-关闭", Name: "规则关闭后不触发",
		SQL:   "CREATE DATABASE struct_test_db_x DEFAULT CHARACTER SET=latin1",
		Apply: func(p *config.InspectParams) {},
		Exp:   mxExpect{Absent: true, RuleKey: "limit_database_charset"}, Note: ""})

	triMySQL("不能设置列的字符集", "no_column_charset",
		"CREATE TABLE t_col_charset (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10) CHARACTER SET utf8) ENGINE=InnoDB",
		"CREATE TABLE t_col_charset (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_COLUMN_CHARSET = true },
		func(p *config.InspectParams) { p.CHECK_COLUMN_CHARSET = false },
		"no_column_charset", "", "")

	triMySQL("不能设置列的校对集", "no_column_collate",
		"CREATE TABLE t_col_collate (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10) COLLATE utf8_general_ci) ENGINE=InnoDB",
		"CREATE TABLE t_col_collate (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 varchar(10)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_COLUMN_COLLATION = true },
		func(p *config.InspectParams) { p.CHECK_COLUMN_COLLATION = false },
		"no_column_collate", "", "")

	triMySQL("字段禁用 Zerofill 属性", "column_zerofill_forbidden",
		"CREATE TABLE t_col_zerofill (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int(4) ZEROFILL) ENGINE=InnoDB",
		"CREATE TABLE t_col_zerofill (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int(4)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.DISABLE_COLUMN_ZEROFILL = true },
		func(p *config.InspectParams) { p.DISABLE_COLUMN_ZEROFILL = false },
		"column_zerofill_forbidden", "", "")

	// disable_* 系列：引擎未实现，语句本身走「未识别语句」分支
	for _, it := range []struct{ item, key, sql string }{
		{"禁止事件相关操作", "disable_event_related_op", "CREATE EVENT ev_x ON SCHEDULE EVERY 1 DAY DO SELECT 1"},
		{"禁止自定义函数相关操作", "disable_custom_function_related_op", "CREATE FUNCTION fn_x() RETURNS int DETERMINISTIC RETURN 1"},
		{"禁止存储过程相关操作", "disable_proc_related_op", "CREATE PROCEDURE pr_x() BEGIN SELECT 1; END"},
		{"禁止触发器相关操作", "disable_trigger_related_op", "CREATE TRIGGER tg_x AFTER INSERT ON t_fix FOR EACH ROW SET @a=1"},
	} {
		add(mxCase{Item: it.item, Key: it.key, DB: "mysql", Dim: "限制-违反", Name: "违反规则的 DDL（引擎未实现）", SQL: it.sql, Apply: func(p *config.InspectParams) {}, Exp: mxExpect{Absent: true}, Note: "发现：引擎未实现该规则；该类语句由 TiDB parser 决定能否解析（预期解析失败或按未识别语句拦截，与规则无关）"})
	}

	triMySQL("禁止视图相关操作", "disable_view_related_op",
		"CREATE VIEW v_x AS SELECT 1",
		"CREATE TABLE t_view_ok (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.ENABLE_CREATE_VIEW = false },
		func(p *config.InspectParams) { p.ENABLE_CREATE_VIEW = true },
		"", "视图", "")

	add(mxCase{Item: "索引名称不允许重复", Key: "index_name_no_duplicate", DB: "mysql", Dim: "限制-违反", Name: "同表重复索引",
		SQL:   "CREATE TABLE t_idx_dup (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, KEY idx_c1 (c1), KEY idx_c1_2 (c1))",
		Apply: func(p *config.InspectParams) {}, Exp: mxExpect{MsgContains: "重复"}, Note: "发现：引擎内置检查但不关联规则 key 且规范未映射开关，规则 off 后仍会触发"})
	add(mxCase{Item: "索引名称不允许重复", Key: "index_name_no_duplicate", DB: "mysql", Dim: "限制-合规", Name: "不同列索引",
		SQL:   "CREATE TABLE t_idx_dup (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int, KEY idx_c1 (c1), KEY idx_c2 (c2))",
		Apply: func(p *config.InspectParams) {}, Exp: mxExpect{Absent: true, MsgContains: "重复"}, Note: ""})

	triMySQL("限制表索引数量", "limit_tbl_idx_count",
		"CREATE TABLE t_idx_count (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int, c3 int, KEY idx_c1 (c1), KEY idx_c2 (c2), KEY idx_c3 (c3)) ENGINE=InnoDB",
		"CREATE TABLE t_idx_count (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int, KEY idx_c1 (c1), KEY idx_c2 (c2)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.MAX_INDEX_KEYS = 2 },
		func(p *config.InspectParams) { p.MAX_INDEX_KEYS = 0 },
		"", "索引", "发现：引擎未关联规则 key，级别映射退化为文案匹配")

	triMySQL("索引需要设置名称", "index_must_have_name",
		"CREATE TABLE t_idx_name (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, KEY (c1)) ENGINE=InnoDB",
		"CREATE TABLE t_idx_name (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, KEY IDX_c1 (c1)) ENGINE=InnoDB",
		func(p *config.InspectParams) {
			p.CHECK_SECONDARY_INDEX_PREFIX = true
			p.SECONDARY_INDEX_PREFIX = "IDX_"
		},
		func(p *config.InspectParams) { p.CHECK_SECONDARY_INDEX_PREFIX = false },
		"", "索引", "发现：MySQL 侧通过索引前缀检查实现，未关联规则 key")

	triMySQL("限制普通索引名格式", "limit_normal_idx_name_fmt",		"CREATE TABLE t_norm_idx (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, KEY index_c1 (c1)) ENGINE=InnoDB",
		"CREATE TABLE t_norm_idx (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, KEY IDX_c1 (c1)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_SECONDARY_INDEX_PREFIX = true },
		func(p *config.InspectParams) { p.CHECK_SECONDARY_INDEX_PREFIX = false },
		"", "命名不符合要求", "发现：引擎未关联规则 key，级别映射退化为文案匹配")

	triMySQL("限制主键列必须自增", "limit_pk_col_must_auto_inc",
		"CREATE TABLE t_pk_autoinc (id bigint unsigned NOT NULL PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_pk_autoinc (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT = true },
		func(p *config.InspectParams) { p.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT = false },
		"limit_pk_col_must_auto_inc", "", "")

	triMySQL("限制主键包含列的个数", "limit_pk_column_count",
		"CREATE TABLE t_pk_parts (a bigint unsigned NOT NULL, b bigint unsigned NOT NULL, PRIMARY KEY (a, b)) ENGINE=InnoDB",
		"CREATE TABLE t_pk_parts (a bigint unsigned NOT NULL, PRIMARY KEY (a)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.PRIMARYKEY_MAX_KEY_PARTS = 1 },
		func(p *config.InspectParams) { p.PRIMARYKEY_MAX_KEY_PARTS = 0 },
		"", "主键", "发现：引擎未关联规则 key，级别映射退化为文案匹配")

	triMySQL("限制主键列类型", "limit_pk_col_type",
		"CREATE TABLE t_pk_type (id varchar(36) NOT NULL PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_pk_type (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) {
			p.CHECK_PRIMARYKEY_USE_BIGINT = true
			p.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint", "int"}
		},
		func(p *config.InspectParams) { p.CHECK_PRIMARYKEY_USE_BIGINT = false; p.PRIMARYKEY_SUPPORT_TYPES = nil },
		"limit_pk_col_type", "", "")

	triMySQL("限制单个索引包含列的个数", "limit_single_index_column_count",
		"CREATE TABLE t_idx_parts (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int, c3 int, KEY IDX_c (c1, c2, c3)) ENGINE=InnoDB",
		"CREATE TABLE t_idx_parts (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, c2 int, KEY IDX_c (c1, c2)) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.SECONDARY_INDEX_MAX_KEY_PARTS = 2 },
		func(p *config.InspectParams) { p.SECONDARY_INDEX_MAX_KEY_PARTS = 0 },
		"", "索引", "发现：引擎未关联规则 key，级别映射退化为文案匹配")

	add(mxCase{Item: "同一张表的多条 ALTER 语句建议合为一条", Key: "merge_multiple_alter_stmt_same_table", DB: "mysql", Dim: "限制-违反", Name: "两条同表 ALTER",
		SQL:   "ALTER TABLE t_fix ADD COLUMN c_m1 int;\nALTER TABLE t_fix ADD COLUMN c_m2 int",
		Apply: func(p *config.InspectParams) { p.ENABLE_MERGE_ALTER_TABLE = true }, Exp: mxExpect{RuleKey: "merge_multiple_alter_stmt_same_table"}, Note: ""})
	add(mxCase{Item: "同一张表的多条 ALTER 语句建议合为一条", Key: "merge_multiple_alter_stmt_same_table", DB: "mysql", Dim: "限制-合规", Name: "单条 ALTER",
		SQL:   "ALTER TABLE t_fix ADD COLUMN c_m3 int",
		Apply: func(p *config.InspectParams) { p.ENABLE_MERGE_ALTER_TABLE = true }, Exp: mxExpect{Absent: true, RuleKey: "merge_multiple_alter_stmt_same_table"}, Note: ""})
	add(mxCase{Item: "同一张表的多条 ALTER 语句建议合为一条", Key: "merge_multiple_alter_stmt_same_table", DB: "mysql", Dim: "开关-关闭", Name: "规则关闭后多条 ALTER 不提示",
		SQL:   "ALTER TABLE t_fix ADD COLUMN c_m4 int;\nALTER TABLE t_fix ADD COLUMN c_m5 int",
		Apply: func(p *config.InspectParams) { p.ENABLE_MERGE_ALTER_TABLE = false }, Exp: mxExpect{Absent: true, RuleKey: "merge_multiple_alter_stmt_same_table"}, Note: ""})

	add(mxCase{Item: "修改表字符集建议用 ALTER TABLE CONVERT 语法", Key: "tbl_charset_change_use_convert_syntax", DB: "mysql", Dim: "限制-违反", Name: "ALTER TABLE ... CHARSET（引擎未实现该规则）",
		SQL: "ALTER TABLE t_fix DEFAULT CHARSET=utf8", Apply: func(p *config.InspectParams) {}, Exp: mxExpect{Absent: true}, Note: "发现：引擎未实现该规则"})

	triMySQL("限制表字符集", "limit_tbl_charset",
		"CREATE TABLE t_tbl_charset (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=latin1",
		"CREATE TABLE t_tbl_charset (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		func(p *config.InspectParams) { p.CHECK_TABLE_CHARSET = true },
		func(p *config.InspectParams) { p.CHECK_TABLE_CHARSET = false },
		"limit_tbl_charset", "", "")

	add(mxCase{Item: "限制表校验规则", Key: "limit_tbl_collation", DB: "mysql", Dim: "限制-违反", Name: "建表指定非白名单 COLLATE",
		SQL: "CREATE TABLE t_tbl_collate (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=latin1_general_ci",
		Apply: func(p *config.InspectParams) {
			p.CHECK_TABLE_COLLATION = true
			p.TABLE_SUPPORT_COLLATION = []string{"utf8mb4_general_ci", "utf8mb4_0900_ai_ci"}
		},
		Exp: mxExpect{RuleKey: "limit_tbl_collation"}, Note: ""})
	add(mxCase{Item: "限制表校验规则", Key: "limit_tbl_collation", DB: "mysql", Dim: "限制-合规", Name: "建表指定白名单 COLLATE",
		SQL: "CREATE TABLE t_tbl_collate (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci",
		Apply: func(p *config.InspectParams) {
			p.CHECK_TABLE_COLLATION = true
			p.TABLE_SUPPORT_COLLATION = []string{"utf8mb4_general_ci", "utf8mb4_0900_ai_ci"}
		},
		Exp: mxExpect{Absent: true, RuleKey: "limit_tbl_collation"}, Note: ""})

	triMySQL("限制表名大小写", "limit_tbl_name_case",
		"CREATE TABLE Tbl_Case (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_tbl_case (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_TABLE_IDENTIFIER = true; p.TABLE_NAME_CASE = "lower" },
		func(p *config.InspectParams) { p.CHECK_TABLE_IDENTIFIER = false },
		"limit_tbl_name_case", "命名不符合要求", "发现：引擎未关联规则 key，级别映射退化为文案匹配")

	triMySQL("表名不能是关键字", "tbl_name_no_keyword",
		"CREATE TABLE `select` (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE t_user (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = true },
		func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = false },
		"", "关键字", "发现：引擎未关联规则 key；且表名检查仍读旧全局关键字表（未接配置参数）")

	triMySQL("限制表不能使用外键", "limit_tbl_no_use_fk",
		"CREATE TABLE t_fk (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, pid bigint unsigned, CONSTRAINT fk_pid FOREIGN KEY (pid) REFERENCES t_fix (id)) ENGINE=InnoDB",
		"CREATE TABLE t_fk (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, pid bigint unsigned) ENGINE=InnoDB",
		func(p *config.InspectParams) { p.ENABLE_FOREIGN_KEY = false },
		func(p *config.InspectParams) { p.ENABLE_FOREIGN_KEY = true },
		"", "外键", "")

	add(mxCase{Item: "表需要包含某些列", Key: "table_must_include_required_columns", DB: "mysql", Dim: "限制-违反", Name: "缺少必需列",
		SQL:   "CREATE TABLE t_req_cols (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int) ENGINE=InnoDB",
		Apply: func(p *config.InspectParams) { p.TABLE_REQUIRED_COLUMNS = []string{"created_at", "updated_at"} },
		Exp:   mxExpect{RuleKey: "table_must_include_required_columns"}, Note: ""})
	add(mxCase{Item: "表需要包含某些列", Key: "table_must_include_required_columns", DB: "mysql", Dim: "限制-合规", Name: "包含全部必需列",
		SQL:   "CREATE TABLE t_req_cols (id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY, c1 int, created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB",
		Apply: func(p *config.InspectParams) { p.TABLE_REQUIRED_COLUMNS = []string{"created_at", "updated_at"} },
		Exp:   mxExpect{Absent: true, RuleKey: "table_must_include_required_columns"}, Note: ""})

	// ================= PostgreSQL 侧 =================
	pgBase := "CREATE TABLE t_demo (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY"

	triPG := func(item, key, vTail, pTail string, on, off func(*config.InspectParams), expKey, expMsg, note string) {
		tri(item, key, "postgres", pgBase+vTail, pgBase+pTail, on, off, expKey, expMsg, note)
	}

	triPG("限制 CHAR 类型字段长度", "limit_char_type_col_len",
		", c1 char(128))", ", c1 char(64))",
		func(p *config.InspectParams) { p.COLUMN_MAX_CHAR_LENGTH = 64 },
		func(p *config.InspectParams) { p.COLUMN_MAX_CHAR_LENGTH = 0 },
		"limit_char_type_col_len", "", "")

	triPG("限制字段名大小写", "limit_col_name_case",
		", Name varchar(10))", ", name varchar(10))",
		func(p *config.InspectParams) { p.CHECK_COLUMN_IDENTIFIER = true; p.COLUMN_NAME_CASE = "lower" },
		func(p *config.InspectParams) { p.CHECK_COLUMN_IDENTIFIER = false },
		"limit_col_name_case", "", "")

	triPG("字段名不能是关键字", "col_name_no_keyword",
		", \"select\" varchar(10))", ", customer_name varchar(10))",
		func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = true },
		func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = false },
		"col_name_no_keyword", "", "")

	triPG("限制列要有注释", "limit_col_must_have_comment",
		", c1 varchar(10))", ", c1 varchar(10));\nCOMMENT ON COLUMN t_demo.id IS '主键';\nCOMMENT ON COLUMN t_demo.c1 IS 'c1'",
		func(p *config.InspectParams) { p.CHECK_COLUMN_COMMENT = true },
		func(p *config.InspectParams) { p.CHECK_COLUMN_COMMENT = false },
		"limit_col_must_have_comment", "", "")

	triPG("限制表字段的数量", "limit_table_column_count",
		", c1 int, c2 int, c3 int, c4 int)", ", c1 int, c2 int)",
		func(p *config.InspectParams) { p.MAX_TABLE_COLUMNS = 3 },
		func(p *config.InspectParams) { p.MAX_TABLE_COLUMNS = 0 },
		"limit_table_column_count", "", "")

	add(mxCase{Item: "表要有主键", Key: "table_must_have_pk", DB: "postgres", Dim: "限制-违反", Name: "无主键建表",
		SQL:   "CREATE TABLE t_no_pk (id bigint GENERATED ALWAYS AS IDENTITY, c1 int)",
		Apply: func(p *config.InspectParams) { p.CHECK_TABLE_PRIMARY_KEY = true }, Exp: mxExpect{RuleKey: "table_must_have_pk"}, Note: ""})
	add(mxCase{Item: "表要有主键", Key: "table_must_have_pk", DB: "postgres", Dim: "限制-合规", Name: "含主键建表",
		SQL:   "CREATE TABLE t_has_pk (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)",
		Apply: func(p *config.InspectParams) { p.CHECK_TABLE_PRIMARY_KEY = true }, Exp: mxExpect{Absent: true, RuleKey: "table_must_have_pk"}, Note: ""})
	add(mxCase{Item: "表要有主键", Key: "table_must_have_pk", DB: "postgres", Dim: "开关-关闭", Name: "规则关闭后无主键放行",
		SQL:   "CREATE TABLE t_no_pk (id bigint GENERATED ALWAYS AS IDENTITY, c1 int)",
		Apply: func(p *config.InspectParams) { p.CHECK_TABLE_PRIMARY_KEY = false }, Exp: mxExpect{Absent: true, RuleKey: "table_must_have_pk"}, Note: ""})

	triPG("限制唯一索引名格式", "limit_uniq_idx_name_fmt",
		", c1 int);\nCREATE UNIQUE INDEX uk_c1 ON t_demo (c1)", ", c1 int);\nCREATE UNIQUE INDEX UNIQ_c1 ON t_demo (c1)",
		func(p *config.InspectParams) { p.CHECK_UNIQ_INDEX_PREFIX = true },
		func(p *config.InspectParams) { p.CHECK_UNIQ_INDEX_PREFIX = false },
		"limit_uniq_idx_name_fmt", "", "")

	triPG("限制 VARCHAR 类型字段长度", "limit_varchar_type_col_len",
		", c1 varchar(2048))", ", c1 varchar(256))",
		func(p *config.InspectParams) { p.MAX_VARCHAR_LENGTH = 1024 },
		func(p *config.InspectParams) { p.MAX_VARCHAR_LENGTH = 0 },
		"limit_varchar_type_col_len", "", "")

	triPG("限制列要有默认值", "limit_col_must_have_default",
		", c1 varchar(10))", ", c1 varchar(10) NOT NULL DEFAULT '')",
		func(p *config.InspectParams) { p.CHECK_COLUMN_DEFAULT_VALUE = true },
		func(p *config.InspectParams) { p.CHECK_COLUMN_DEFAULT_VALUE = false },
		"limit_col_must_have_default", "", "")

	triPG("限制列不可空（NOT NULL)", "limit_col_not_null",
		", c1 varchar(10))", ", c1 varchar(10) NOT NULL)",
		func(p *config.InspectParams) { p.ENABLE_COLUMN_NOT_NULL = true },
		func(p *config.InspectParams) { p.ENABLE_COLUMN_NOT_NULL = false },
		"limit_col_not_null", "", "")

	triPG("限制列不能使用部分数据类型", "limit_col_no_partial_data_type",
		", c1 double precision)", ", c1 numeric(10,2))",
		func(p *config.InspectParams) {
			p.CHECK_COLUMN_FORBIDDEN_TYPES = true
			p.COLUMN_FORBIDDEN_TYPES = []string{"double", "float", "enum", "bit", "set"}
		},
		func(p *config.InspectParams) { p.CHECK_COLUMN_FORBIDDEN_TYPES = false; p.COLUMN_FORBIDDEN_TYPES = nil },
		"limit_col_no_partial_data_type", "", "")

	add(mxCase{Item: "禁止视图相关操作", Key: "disable_view_related_op", DB: "postgres", Dim: "限制-违反", Name: "CREATE VIEW 触发",
		SQL: "CREATE VIEW v_x AS SELECT 1", Apply: func(p *config.InspectParams) { p.ENABLE_CREATE_VIEW = false }, Exp: mxExpect{RuleKey: "disable_view_related_op"}, Note: ""})
	add(mxCase{Item: "禁止视图相关操作", Key: "disable_view_related_op", DB: "postgres", Dim: "开关-关闭", Name: "规则关闭后 CREATE VIEW 放行",
		SQL: "CREATE VIEW v_x AS SELECT 1", Apply: func(p *config.InspectParams) { p.ENABLE_CREATE_VIEW = true }, Exp: mxExpect{Absent: true, RuleKey: "disable_view_related_op"}, Note: ""})

	triPG("限制表索引数量", "limit_tbl_idx_count",
		", c1 int, c2 int, c3 int);\nCREATE INDEX idx_c1 ON t_demo (c1);\nCREATE INDEX idx_c2 ON t_demo (c2);\nCREATE INDEX idx_c3 ON t_demo (c3)",
		", c1 int, c2 int);\nCREATE INDEX idx_c1 ON t_demo (c1);\nCREATE INDEX idx_c2 ON t_demo (c2)",
		func(p *config.InspectParams) { p.MAX_INDEX_KEYS = 2 },
		func(p *config.InspectParams) { p.MAX_INDEX_KEYS = 0 },
		"limit_tbl_idx_count", "", "")

	triPG("索引需要设置名称", "index_must_have_name",
		", c1 int);\nCREATE INDEX ON t_demo (c1)", ", c1 int);\nCREATE INDEX IDX_c1 ON t_demo (c1)",
		func(p *config.InspectParams) { p.CHECK_INDEX_NAME_REQUIRED = true },
		func(p *config.InspectParams) { p.CHECK_INDEX_NAME_REQUIRED = false },
		"index_must_have_name", "", "")

	triPG("限制普通索引名格式", "limit_normal_idx_name_fmt",
		", c1 int);\nCREATE INDEX index_c1 ON t_demo (c1)", ", c1 int);\nCREATE INDEX IDX_c1 ON t_demo (c1)",
		func(p *config.InspectParams) { p.CHECK_SECONDARY_INDEX_PREFIX = true },
		func(p *config.InspectParams) { p.CHECK_SECONDARY_INDEX_PREFIX = false },
		"limit_normal_idx_name_fmt", "", "")

	triPG("限制主键列类型", "limit_pk_col_type",
		", name varchar(128) NOT NULL, PRIMARY KEY (name))", ", name bigint NOT NULL, PRIMARY KEY (name))",
		func(p *config.InspectParams) {
			p.CHECK_PRIMARYKEY_USE_BIGINT = true
			p.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint", "int"}
		},
		func(p *config.InspectParams) { p.CHECK_PRIMARYKEY_USE_BIGINT = false; p.PRIMARYKEY_SUPPORT_TYPES = nil },
		"limit_pk_col_type", "", "")

	triPG("限制单个索引包含列的个数", "limit_single_index_column_count",
		", c1 int, c2 int, c3 int);\nCREATE INDEX IDX_c ON t_demo (c1, c2, c3)", ", c1 int, c2 int);\nCREATE INDEX IDX_c ON t_demo (c1, c2)",
		func(p *config.InspectParams) { p.SECONDARY_INDEX_MAX_KEY_PARTS = 2 },
		func(p *config.InspectParams) { p.SECONDARY_INDEX_MAX_KEY_PARTS = 0 },
		"limit_single_index_column_count", "", "")

	add(mxCase{Item: "限制表名大小写", Key: "limit_tbl_name_case", DB: "postgres", Dim: "限制-违反", Name: "大写表名",
		SQL:   "CREATE TABLE Tbl_Case (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)",
		Apply: func(p *config.InspectParams) { p.CHECK_TABLE_IDENTIFIER = true; p.TABLE_NAME_CASE = "lower" }, Exp: mxExpect{RuleKey: "limit_tbl_name_case"}, Note: ""})
	add(mxCase{Item: "限制表名大小写", Key: "limit_tbl_name_case", DB: "postgres", Dim: "限制-合规", Name: "小写表名",
		SQL:   "CREATE TABLE t_tbl_case (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)",
		Apply: func(p *config.InspectParams) { p.CHECK_TABLE_IDENTIFIER = true; p.TABLE_NAME_CASE = "lower" }, Exp: mxExpect{Absent: true, RuleKey: "limit_tbl_name_case"}, Note: ""})

	add(mxCase{Item: "表名不能是关键字", Key: "tbl_name_no_keyword", DB: "postgres", Dim: "限制-违反", Name: "关键字表名 select",
		SQL:   "CREATE TABLE \"select\" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)",
		Apply: func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = true }, Exp: mxExpect{RuleKey: "tbl_name_no_keyword"}, Note: ""})
	add(mxCase{Item: "表名不能是关键字", Key: "tbl_name_no_keyword", DB: "postgres", Dim: "限制-合规", Name: "普通表名 t_user",
		SQL:   "CREATE TABLE t_user (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)",
		Apply: func(p *config.InspectParams) { p.CHECK_IDENTIFER_KEYWORD = true }, Exp: mxExpect{Absent: true, RuleKey: "tbl_name_no_keyword"}, Note: ""})

	triPG("限制表不能使用外键", "limit_tbl_no_use_fk",
		", pid bigint, CONSTRAINT fk_pid FOREIGN KEY (pid) REFERENCES t_other (id))", ", pid bigint)",
		func(p *config.InspectParams) { p.ENABLE_FOREIGN_KEY = false },
		func(p *config.InspectParams) { p.ENABLE_FOREIGN_KEY = true },
		"limit_tbl_no_use_fk", "", "")

	add(mxCase{Item: "表需要包含某些列", Key: "table_must_include_required_columns", DB: "postgres", Dim: "限制-违反", Name: "缺少必需列",
		SQL:   "CREATE TABLE t_req_cols (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, c1 int)",
		Apply: func(p *config.InspectParams) { p.TABLE_REQUIRED_COLUMNS = []string{"created_at", "updated_at"} },
		Exp:   mxExpect{RuleKey: "table_must_include_required_columns"}, Note: ""})
	add(mxCase{Item: "表需要包含某些列", Key: "table_must_include_required_columns", DB: "postgres", Dim: "限制-合规", Name: "包含全部必需列",
		SQL:   "CREATE TABLE t_req_cols (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, c1 int, created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at timestamptz)",
		Apply: func(p *config.InspectParams) { p.TABLE_REQUIRED_COLUMNS = []string{"created_at", "updated_at"} },
		Exp:   mxExpect{Absent: true, RuleKey: "table_must_include_required_columns"}, Note: ""})

	return cases
}

// ---------- 执行入口 ----------

func TestStructRuleMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过矩阵测试")
	}
	if matrixMySQLPass() == "" {
		t.Skip("未设置 MATRIX_MYSQL_PASS 环境变量，跳过需要真实 MySQL 的矩阵测试")
	}
	cases := buildMatrixCases()
	results := make([]mxResult, 0, len(cases))
	failed := 0
	for _, mc := range cases {
		mc := mc
		res := assertCase(t, mc)
		if !res.Pass {
			failed++
			t.Logf("FAIL [%s/%s/%s] %s\n  SQL: %s\n  实际: %s", mc.DB, mc.Item, mc.Dim, mc.Name, mc.SQL, strings.Join(res.Actual, " | "))
		}
		results = append(results, res)
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(matrixOutPath, out, 0644); err != nil {
		t.Fatalf("写出结果失败: %v", err)
	}
	t.Logf("矩阵用例共 %d 条，通过 %d 条，未通过 %d 条（未通过含「预期失败」的未实现规则）", len(results), len(results)-failed, failed)
}
