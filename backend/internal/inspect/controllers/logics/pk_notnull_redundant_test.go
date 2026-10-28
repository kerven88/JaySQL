package logics

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
	"github.com/simajiong/jaysql/pkg/parser"
)

// runPKAndRedundantChecks 解析建表语句并跑「主键属性 + 冗余索引」检查
func runPKAndRedundantChecks(t *testing.T, sql string, params config.InspectParams) []controllers.SummaryItem {
	t.Helper()
	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	vp := &traverses.TraverseCreateTablePrimaryKey{}
	stmt.Accept(vp)
	vr := &traverses.TraverseCreateTableRedundantIndexes{}
	stmt.Accept(vr)
	r := &controllers.RuleHint{InspectParams: &params}
	LogicCreateTablePrimaryKey(vp, r)
	LogicCreateTableRedundantIndexes(vr, r)
	return r.Summary
}

// 场景复刻：规范只开启「限制主键列必须自增」，其余全关。
// 建表语句含：无自增主键、匿名前缀索引（KEY(tenant_id) 与 tenant_phone 构成冗余）。
// 期望：只报 auto_increment 一条；不再出现「必须定义NOT NULL」与「冗余索引」。
func TestOnlyAutoIncRuleNoExtraWarnings(t *testing.T) {
	const sql = "CREATE TABLE t_all_violate (" +
		"id INT ZEROFILL, " +
		"name VARCHAR(64), " +
		"tenant_id BIGINT NOT NULL, " +
		"phone VARCHAR(20), status TINYINT, " +
		"PRIMARY KEY (id), " +
		"KEY (tenant_id), " +
		"KEY tenant_phone (tenant_id, phone, status)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"

	params := config.DefaultInspectParams()
	params.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT = true
	// 修复后默认 ENABLE_REDUNDANT_INDEX=true（跳过检查）
	if !params.ENABLE_REDUNDANT_INDEX {
		t.Fatal("默认参数下冗余索引检查应处于关闭（ENABLE_REDUNDANT_INDEX=true）")
	}

	summary := runPKAndRedundantChecks(t, sql, params)
	if len(summary) != 1 {
		t.Fatalf("只开自增规则应恰好命中 1 条，实际 %d 条: %+v", len(summary), summary)
	}
	if summary[0].RuleKey != "limit_pk_col_must_auto_inc" {
		t.Errorf("命中的应为 limit_pk_col_must_auto_inc，实际: %s (%s)", summary[0].RuleKey, summary[0].Message)
	}
	for _, it := range summary {
		if strings.Contains(it.Message, "NOT NULL") {
			t.Errorf("不应再报主键 NOT NULL（已移除无守卫的孤儿检查）: %s", it.Message)
		}
		if strings.Contains(it.Message, "冗余索引") {
			t.Errorf("规范全关时不应报冗余索引: %s", it.Message)
		}
	}
}

// 兜底能力保留：显式将 ENABLE_REDUNDANT_INDEX 置 false（不允许冗余）时检查仍然生效，
// 匿名 KEY(tenant_id) 是 tenant_phone(tenant_id,phone,status) 的列前缀，应命中。
func TestRedundantIndexCheckStillWorksWhenDisabled(t *testing.T) {
	const sql = "CREATE TABLE t_redundant (" +
		"id bigint NOT NULL AUTO_INCREMENT, " +
		"tenant_id BIGINT NOT NULL, phone VARCHAR(20), status TINYINT, " +
		"PRIMARY KEY (id), " +
		"KEY (tenant_id), " +
		"KEY tenant_phone (tenant_id, phone, status)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"

	params := config.DefaultInspectParams()
	params.ENABLE_REDUNDANT_INDEX = false
	summary := runPKAndRedundantChecks(t, sql, params)

	found := false
	for _, it := range summary {
		if strings.Contains(it.Message, "冗余索引") && strings.Contains(it.Message, "tenant_phone") {
			found = true
			// 必须归级到「限制冗余索引」规则，否则 must 级别无法阻断提交审批
			if it.RuleKey != "limit_tbl_redundant_idx" {
				t.Errorf("冗余索引告警的 rule_key 应为 limit_tbl_redundant_idx，实际: %q", it.RuleKey)
			}
		}
	}
	if !found {
		t.Fatalf("显式关闭冗余索引容忍时应命中冗余索引告警，实际: %+v", summary)
	}
}

// 主键列未显式写 NOT NULL 时（MySQL 隐式 NOT NULL），不得产生非空误报
func TestPKImplicitNotNullNoFalsePositive(t *testing.T) {
	const sql = "CREATE TABLE t_pk_implicit (" +
		"id INT, name VARCHAR(32), " +
		"PRIMARY KEY (id)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"

	params := config.DefaultInspectParams()
	if got := runPKAndRedundantChecks(t, sql, params); len(got) != 0 {
		t.Errorf("主键列隐式 NOT NULL 不应产生任何告警，实际: %+v", got)
	}
}
