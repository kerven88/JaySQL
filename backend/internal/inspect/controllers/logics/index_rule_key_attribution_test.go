package logics

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
	"github.com/simajiong/jaysql/pkg/parser"
)

// 回归（2026-09-29）：索引数量 / 主键索引列数 / 二级索引列数三类告警必须各自携带 rule_key。
//
// 历史实现里这三条告警都走裸 Warn，摘要不带 rule_key；工单侧 resolveInspectSeverity
// 只能退化到「规范占优级别」兜底 —— 生产规范 must 占多数，于是规则页面上明明配成
// 「建议改进」的规则，在工单预审里被展示为「必须改进」。
func TestIndexCountRuleKeyAttribution(t *testing.T) {
	const sql = "CREATE TABLE t_rule_key (" +
		"id INT NOT NULL, name VARCHAR(64), col1 INT, col2 INT, col3 INT, " +
		"PRIMARY KEY (id, name), " +
		"KEY idx1_a (col1, col2, col3), " +
		"KEY idx1_b (col1)" +
		") ENGINE=InnoDB;"

	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v := &traverses.TraverseCreateTableIndexesCount{}
	stmt.Accept(v)

	params := config.DefaultInspectParams()
	params.PRIMARYKEY_MAX_KEY_PARTS = 1      // 主键 2 列 → 违规
	params.SECONDARY_INDEX_MAX_KEY_PARTS = 2 // idx1_a 3 列 → 违规
	params.MAX_INDEX_KEYS = 1                // 2 个二级索引 → 违规
	r := &controllers.RuleHint{InspectParams: &params}
	LogicCreateTableIndexesCount(v, r)

	want := map[string]bool{
		"limit_tbl_idx_count":             false,
		"limit_pk_column_count":           false,
		"limit_single_index_column_count": false,
	}
	for _, item := range r.Summary {
		if _, ok := want[item.RuleKey]; ok {
			want[item.RuleKey] = true
			continue
		}
		t.Errorf("告警未携带可归级的 rule_key: %q (%s)", item.RuleKey, item.Message)
	}
	for key, hit := range want {
		if !hit {
			t.Errorf("未命中期望的规则归级: %s（实际摘要 %+v）", key, r.Summary)
		}
	}
}

// 边界：主键与二级索引各自的列数限制互不串扰。
// 只放开二级索引列数限制时，主键列数超限不应被报出（反之亦然）。
func TestIndexCountLimitsAreIndependent(t *testing.T) {
	const sql = "CREATE TABLE t_rule_key2 (" +
		"id INT NOT NULL, name VARCHAR(64), col1 INT, col2 INT, col3 INT, " +
		"PRIMARY KEY (id, name), " +
		"KEY idx1_a (col1, col2, col3)" +
		") ENGINE=InnoDB;"

	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v := &traverses.TraverseCreateTableIndexesCount{}
	stmt.Accept(v)

	// 只开「限制单个索引包含列的个数」
	params := config.DefaultInspectParams()
	params.SECONDARY_INDEX_MAX_KEY_PARTS = 2
	r := &controllers.RuleHint{InspectParams: &params}
	LogicCreateTableIndexesCount(v, r)

	for _, item := range r.Summary {
		if item.RuleKey == "limit_pk_column_count" {
			t.Errorf("未开启主键列数规则时不应报主键列数: %s", item.Message)
		}
	}
	hit := false
	for _, item := range r.Summary {
		if item.RuleKey == "limit_single_index_column_count" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("二级索引列数超限应归级到 limit_single_index_column_count，实际: %+v", r.Summary)
	}
}
