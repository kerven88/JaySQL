package logics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// filterSummaryByRule 过滤出指定规则 key 的审核摘要
func filterSummaryByRule(items []controllers.SummaryItem, ruleKey string) []controllers.SummaryItem {
	var out []controllers.SummaryItem
	for _, it := range items {
		if it.RuleKey == ruleKey {
			out = append(out, it)
		}
	}
	return out
}

// 表字段数量上限（规则 limit_tbl_col_的count）
func TestLogicCreateTableColumnCountLimit(t *testing.T) {
	const ruleKey = "limit_table_column_count"

	makeCols := func(n int) []process.ColOptions {
		out := make([]process.ColOptions, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, process.ColOptions{Column: fmt.Sprintf("c%d", i)})
		}
		return out
	}
	// totalCols 模拟建表语句里的「全部列数（含主键列）」，
	// optionsCols 模拟 traverse.Cols（真实场景已剔除主键列，因此比 totalCols 少 1）。
	run := func(limit, totalCols int) []controllers.SummaryItem {
		params := config.DefaultInspectParams()
		params.MAX_TABLE_COLUMNS = limit
		r := &controllers.RuleHint{InspectParams: &params}
		LogicCreateTableColsOptions(
			&traverses.TraverseCreateTableColsOptions{
				Table:     "t_demo",
				Cols:      makeCols(totalCols - 1), // 剔除主键列
				TotalCols: totalCols,
			}, r)
		return filterSummaryByRule(r.Summary, ruleKey)
	}

	// 8 列 > max 3：命中且文案回显阈值与实际列数
	got := run(3, 8)
	if len(got) != 1 {
		t.Fatalf("8 列应命中 1 条字段数量告警，实际 %d 条: %v", len(got), got)
	}
	if !strings.Contains(got[0].Message, "max = 3") || !strings.Contains(got[0].Message, "当前 8") {
		t.Errorf("告警文案须回显配置阈值与当前列数，实际: %s", got[0].Message)
	}

	// 恰好等于上限：不告警
	if got := run(8, 8); len(got) != 0 {
		t.Errorf("列数等于上限不应告警，实际: %v", got)
	}

	// 0 表示不限制（规则关闭时的取值）
	if got := run(0, 8); len(got) != 0 {
		t.Errorf("上限为 0（不限制）不应告警，实际: %v", got)
	}
}
