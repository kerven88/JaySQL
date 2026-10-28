package tools

import (
	"strings"
	"testing"
)

// ============================================================
// SQL 类工具的单测
//
// 只覆盖纯函数层（解析 / 推荐 / 采样构造 / 提示语）与注册契约：
// 真正连库的分支由 das 模块自己的测试兜底，这里不重复造数据库环境。
// ============================================================

// 5 个 SQL 类工具必须注册齐全，且全部只读 —— 它们只分析与采样，绝不落库
func TestSQLToolsRegistered(t *testing.T) {
	for _, name := range []string{
		"generate_sql", "interpret_sql", "explain_sql", "recommend_indexes", "sample_data",
	} {
		tool, ok := Get(name)
		if !ok {
			t.Fatalf("工具 %s 未注册", name)
		}
		if tool.Write {
			t.Fatalf("工具 %s 应为只读，不得标记写操作", name)
		}
		if tool.Permission == "" {
			t.Fatalf("工具 %s 缺少权限点声明", name)
		}
		properties, ok := tool.Parameters["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			t.Fatalf("工具 %s 参数 Schema 不合法", name)
		}
		if tool.Handler == nil {
			t.Fatalf("工具 %s 缺少处理函数", name)
		}
		// 只读工具必须出现在下发给模型的清单里，否则模型根本看不到它
		found := false
		for _, def := range Definitions(false) {
			if def.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("工具 %s 未出现在只读工具清单中", name)
		}
	}
}

// SQL 解释：结构化字段要能覆盖库表、字段、JOIN、WHERE、ORDER BY、LIMIT
func TestInterpretSQL(t *testing.T) {
	sqlText := "SELECT o.id, o.amount, u.name FROM orders.t_order o " +
		"JOIN orders.t_user u ON o.user_id = u.id WHERE o.status = 1 ORDER BY o.created_at DESC LIMIT 20"
	out, err := interpretSQL(sqlText, "")
	if err != nil {
		t.Fatalf("interpretSQL 失败：%v", err)
	}
	if out["statement_type"] != "SELECT" {
		t.Fatalf("statement_type = %v, want SELECT", out["statement_type"])
	}
	tables, _ := out["tables"].([]string)
	if len(tables) != 2 {
		t.Fatalf("tables = %v, want 2 张表", tables)
	}
	if !containsAll(tables, "orders.t_order", "orders.t_user") {
		t.Fatalf("tables = %v, 未包含期望的表名", tables)
	}
	if _, ok := out["where"]; !ok {
		t.Fatalf("缺少 where：%v", out)
	}
	if out["limit"] != "20" {
		t.Fatalf("limit = %v, want 20", out["limit"])
	}
	joins, ok := out["joins"].([]map[string]any)
	if !ok || len(joins) != 1 {
		t.Fatalf("joins = %v, want 1 个关联", out["joins"])
	}
	// 没有 GROUP BY 就不该出现这个字段（空值不输出，避免模型把「空」读成「有」）
	if _, ok := out["group_by"]; ok {
		t.Fatalf("不该出现 group_by：%v", out)
	}
	if summary, _ := out["summary"].(string); !strings.Contains(summary, "SELECT") {
		t.Fatalf("summary 不可读：%q", summary)
	}
}

// 缺失 WHERE / LIMIT 与 SELECT * 要能给出风险提示
func TestInterpretSQLRiskNotes(t *testing.T) {
	out, err := interpretSQL("SELECT * FROM t_order", "orders")
	if err != nil {
		t.Fatalf("interpretSQL 失败：%v", err)
	}
	notes, ok := out["risk_notes"].([]string)
	if !ok || len(notes) == 0 {
		t.Fatalf("缺少风险提示：%v", out)
	}
	joined := strings.Join(notes, "|")
	for _, keyword := range []string{"SELECT *", "WHERE", "LIMIT"} {
		if !strings.Contains(joined, keyword) {
			t.Fatalf("风险提示缺少 %s：%v", keyword, notes)
		}
	}
}

// 多条语句必须报错：执行计划与采样都是单条口径，混在一起说不清
func TestInterpretSQLRejectsMultipleStatements(t *testing.T) {
	if _, err := interpretSQL("SELECT * FROM a; SELECT * FROM b", ""); err == nil {
		t.Fatalf("多条语句应当报错")
	}
}

// 建表语句解析：列、主键、唯一索引与普通索引都要能拆出来
func TestParseCreateTable(t *testing.T) {
	ddl := "CREATE TABLE `t_order` (\n" +
		"  `id` bigint(20) NOT NULL AUTO_INCREMENT,\n" +
		"  `uid` bigint(20) NOT NULL DEFAULT '0' COMMENT '用户ID',\n" +
		"  `amount` decimal(10,2) NOT NULL,\n" +
		"  `remark` varchar(512) DEFAULT NULL,\n" +
		"  PRIMARY KEY (`id`),\n" +
		"  UNIQUE KEY `uk_uid` (`uid`),\n" +
		"  KEY `idx_uid` (`uid`),\n" +
		"  KEY `idx_remark` (`remark`(32))\n" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	columns, indexes := parseCreateTable(ddl)
	if len(columns) != 4 {
		t.Fatalf("列数 = %d, want 4：%+v", len(columns), columns)
	}
	if columns[0].Name != "id" || columns[1].Name != "uid" {
		t.Fatalf("列名解析不正确：%+v", columns)
	}
	// 类型里不能带 COMMENT 等属性
	if strings.Contains(strings.ToUpper(columns[1].Type), "DEFAULT") {
		t.Fatalf("列类型不该包含列属性：%q", columns[1].Type)
	}
	if len(indexes) != 4 {
		t.Fatalf("索引数 = %d, want 4：%+v", len(indexes), indexes)
	}
	if !indexes[0].Primary {
		t.Fatalf("第一个索引应为主键：%+v", indexes[0])
	}
	if !indexes[1].Unique || indexes[1].Name != "uk_uid" {
		t.Fatalf("唯一索引解析不正确：%+v", indexes[1])
	}
	// 前缀索引 `remark`(32) 要保留列名、丢掉长度
	found := false
	for _, index := range indexes {
		if index.Name == "idx_remark" && len(index.Columns) == 1 && index.Columns[0] == "remark" {
			found = true
		}
	}
	if !found {
		t.Fatalf("前缀索引解析不正确：%+v", indexes)
	}
}

// 索引推荐：缺失索引要给建议，已有索引覆盖的列不能重复推荐，重复索引要被识别
func TestRecommendTableIndexes(t *testing.T) {
	columns := []tableColumn{
		{Name: "id", Type: "bigint(20)"},
		{Name: "uid", Type: "bigint(20)"},
		{Name: "status", Type: "tinyint(4)"},
		{Name: "created_at", Type: "datetime"},
	}
	indexes := []tableIndex{
		{Name: "PRIMARY", Columns: []string{"id"}, Primary: true, Unique: true},
		{Name: "uk_uid", Columns: []string{"uid"}, Unique: true},
		{Name: "idx_uid", Columns: []string{"uid"}},
		{Name: "idx_created_at", Columns: []string{"created_at"}},
	}
	suggestions := recommendTableIndexes("t_order", columns, indexes, []string{"status", "created_at"}, true)
	if len(suggestions) == 0 {
		t.Fatalf("应当给出建议")
	}
	joined := ""
	for _, item := range suggestions {
		joined += strings.Join([]string{
			item["priority"].(string), item["reason"].(string), item["suggestion"].(string),
		}, "|") + "\n"
	}
	// status 没有索引 → 应推荐
	if !strings.Contains(joined, "`status`") {
		t.Fatalf("未推荐缺失索引列 status：%s", joined)
	}
	// created_at 已有索引 → 不应推荐
	if strings.Contains(joined, "ADD INDEX idx_t_order_created_at") {
		t.Fatalf("created_at 已有索引，不该重复推荐：%s", joined)
	}
	// uk_uid / idx_uid 列完全相同 → 应识别为重复索引
	if !strings.Contains(joined, "重复索引") {
		t.Fatalf("未识别重复索引：%s", joined)
	}
}

// 拿不到已有索引时（如 PG 的建表语句不含索引）不能断言「缺失」，只能给待确认候选
func TestRecommendTableIndexesWithoutIndexInfo(t *testing.T) {
	columns := []tableColumn{{Name: "uid", Type: "bigint(20)"}}
	suggestions := recommendTableIndexes("t_order", columns, nil, []string{"uid"}, false)
	if len(suggestions) == 0 {
		t.Fatalf("仍应给出候选建议")
	}
	joined := ""
	for _, item := range suggestions {
		joined += item["reason"].(string)
	}
	// indexKnown=false 时不该出现「没有主键」这类基于索引清单的结论
	if strings.Contains(joined, "没有主键") {
		t.Fatalf("索引信息未知时不该断言主键情况：%s", joined)
	}
}

// 采样查询构造：方言引号、列白名单、LIMIT 都要正确
func TestBuildSampleQuery(t *testing.T) {
	query, err := buildSampleQuery("orders", "t_order", []string{"id", "status"}, "status = 1", 5, false)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	want := "SELECT `id`, `status` FROM `orders`.`t_order` WHERE status = 1 LIMIT 5"
	if query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}

	// PG 的 schema 形如 database.schema，限定表名只取后半段
	query, err = buildSampleQuery("app.public", "t_order", nil, "", 10, true)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	if query != `SELECT * FROM "public"."t_order" LIMIT 10` {
		t.Fatalf("query = %q", query)
	}
}

// 采样的标识符与 WHERE 白名单：注入类输入一律挡掉
func TestSampleInputGuards(t *testing.T) {
	badIdentifiers := []string{"t_order;DROP TABLE x", "1abc", "a-b", ""}
	for _, value := range badIdentifiers {
		if _, err := validateSampleIdentifier(value, "table"); err == nil {
			t.Fatalf("标识符 %q 应当被拒绝", value)
		}
	}
	badWhere := []string{
		"status = 1; DROP TABLE t_order",
		"status = 1 --",
		"id IN (SELECT id FROM t_user)",
		"id = 1 UNION SELECT 1",
		strings.Repeat("a", 600),
	}
	for _, value := range badWhere {
		if _, err := validateSampleWhere(value); err == nil {
			t.Fatalf("WHERE %q 应当被拒绝", value)
		}
	}
	if _, err := validateSampleWhere("status = 1 AND created_at >= '2026-01-01'"); err != nil {
		t.Fatalf("正常条件不该被拒绝：%v", err)
	}
}

// 无权限提示语：5 个工具共用同一段措辞，必须说清缺哪张表、什么权限、怎么申请
func TestNoDataGrantMessage(t *testing.T) {
	message := noDataGrantMessage(SQLActionSelect, []string{"表 `order`.`t_order`"})
	for _, keyword := range []string{"表 `order`.`t_order`", "查询权限", "不执行任何查询"} {
		if !strings.Contains(message, keyword) {
			t.Fatalf("提示语缺少 %s：%s", keyword, message)
		}
	}
	result := noDataGrantResult(SQLActionSelect, []string{"库 `order`"})
	if result["allowed"] != false || result["code"] != "no_data_grant" {
		t.Fatalf("返回结果标记不正确：%v", result)
	}
	if how, _ := result["how_to_apply"].(string); !strings.Contains(how, "权限管理") {
		t.Fatalf("申请指引应指向权限管理：%v", result["how_to_apply"])
	}
	// 同一个权限问题在不同工具里必须是同一句话
	again := noDataGrantMessage(SQLActionSelect, []string{"表 `order`.`t_order`"})
	if again != message {
		t.Fatalf("提示语在不同调用间不一致")
	}
}

// 执行计划解读：全表扫描与「有索引未使用」要被识别为告警
func TestAnalyzeExplainPlanMySQL(t *testing.T) {
	rows := []map[string]any{
		{
			"id": "1", "select_type": "SIMPLE", "table": "t_order", "type": "ALL",
			"possible_keys": "idx_status", "key": nil, "rows": "120000",
			"Extra": "Using where; Using filesort",
		},
	}
	result := analyzeExplainPlan([]string{"id", "table", "type"}, rows, false)
	findings, ok := result["findings"].([]map[string]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("未产出风险提示：%v", result)
	}
	joined := ""
	for _, item := range findings {
		joined += item["level"].(string) + "|" + item["message"].(string) + "\n"
	}
	for _, keyword := range []string{"全表扫描", "未使用", "filesort"} {
		if !strings.Contains(joined, keyword) {
			t.Fatalf("缺少 %s 的提示：%s", keyword, joined)
		}
	}
	if summary, ok := result["summary"].(map[string]any); !ok || summary["max_estimated_rows"] != int64(120000) {
		t.Fatalf("预估行数未汇总：%v", result["summary"])
	}
}

// PG 的执行计划是单列文本，Seq Scan 要能被识别
func TestAnalyzeExplainPlanPostgres(t *testing.T) {
	rows := []map[string]any{
		{"QUERY PLAN": "Seq Scan on t_order  (cost=0.00..100.00 rows=5000 width=36)"},
	}
	result := analyzeExplainPlan([]string{"QUERY PLAN"}, rows, true)
	findings, ok := result["findings"].([]map[string]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("未产出风险提示：%v", result)
	}
	if !strings.Contains(findings[0]["message"].(string), "Seq Scan") {
		t.Fatalf("未识别 Seq Scan：%v", findings[0])
	}
}

// 关键词挑选候选表：表注释命中也要算数
func TestPickCandidateTables(t *testing.T) {
	tables := []map[string]any{
		{"table": "t_order", "comment": "订单表", "rows": "1000"},
		{"table": "t_user", "comment": "用户表", "rows": "900"},
		{"table": "t_log", "comment": "日志表", "rows": "99999"},
	}
	picked := pickCandidateTables(tables, "统计订单金额", 2)
	if len(picked) != 1 || picked[0]["table"] != "t_order" {
		t.Fatalf("候选表挑选不正确：%v", picked)
	}
	// 一个都没命中时退化为按数据量给出参考，不能空手而归
	fallback := pickCandidateTables(tables, "随便看看", 2)
	if len(fallback) == 0 {
		t.Fatalf("无命中时应给出参考表")
	}
}

func containsAll(values []string, wanted ...string) bool {
	for _, want := range wanted {
		found := false
		for _, value := range values {
			if value == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
