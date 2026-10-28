package traverses

import (
	"testing"

	"github.com/simajiong/jaysql/pkg/parser"
)

// 语句级 LIMIT / ORDER BY 标志不得被 SET 项、WHERE 子表达式的检查结果覆盖。
//
// 历史 bug：TraverseDMLHasConstraint 曾直接赋值（c.HasLimit = check.HasLimit），
// 而子表达式检查通常检测不到语句级 LIMIT/ORDER BY，于是「带 SET 的 UPDATE」
// 「带 WHERE 的 DELETE」会把标志覆盖成 false，导致这两条规则永不触发：
//   - 规则：限制 UPDATE/DELETE 语句 LIMIT 数据量大小（limit_update_delete_data_size）
//   - 规则：UPDATE/DELETE 语句不能有 ORDER BY 子句（update_delete_stmt_no_order_by_clause）
//
// 同时锁定 DMLType 必须无条件赋值（告警文案依赖它区分 UPDATE/DELETE）。
func TestDMLHasConstraintFlagsSurviveExprCheck(t *testing.T) {
	cases := []struct {
		name           string
		sql            string
		wantType       string
		wantLimit      bool
		wantLimitCount int
		wantOrderBy    bool
	}{
		{
			name: "DELETE 无 WHERE + LIMIT", sql: "DELETE FROM users LIMIT 5000",
			wantType: "DELETE", wantLimit: true, wantLimitCount: 5000,
		},
		{
			name: "DELETE 带 WHERE + LIMIT", sql: "DELETE FROM users WHERE status = 0 LIMIT 5000",
			wantType: "DELETE", wantLimit: true, wantLimitCount: 5000,
		},
		{
			name: "UPDATE SET + LIMIT", sql: "UPDATE users SET status = 1 LIMIT 5000",
			wantType: "UPDATE", wantLimit: true, wantLimitCount: 5000,
		},
		{
			name: "UPDATE SET + WHERE + LIMIT", sql: "UPDATE users SET status = 1 WHERE status = 0 LIMIT 5000",
			wantType: "UPDATE", wantLimit: true, wantLimitCount: 5000,
		},
		{
			name: "DELETE 无 WHERE + ORDER BY", sql: "DELETE FROM users ORDER BY id DESC",
			wantType: "DELETE", wantOrderBy: true,
		},
		{
			name: "DELETE 带 WHERE + ORDER BY", sql: "DELETE FROM users WHERE department_id = 2 ORDER BY created_at ASC",
			wantType: "DELETE", wantOrderBy: true,
		},
		{
			name: "UPDATE SET + ORDER BY", sql: "UPDATE users SET status = 1 ORDER BY id DESC",
			wantType: "UPDATE", wantOrderBy: true,
		},
		{
			name: "UPDATE SET + WHERE + ORDER BY", sql: "UPDATE users SET status = 0 WHERE department_id = 1 ORDER BY id DESC",
			wantType: "UPDATE", wantOrderBy: true,
		},
		{
			name: "UPDATE 无 LIMIT 无 ORDER BY：类型仍需赋值", sql: "UPDATE users SET status = 0",
			wantType: "UPDATE",
		},
		{
			name: "DELETE 无 LIMIT 无 ORDER BY：类型仍需赋值", sql: "DELETE FROM users",
			wantType: "DELETE",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stmt, err := parser.NewParseOneStmt(c.sql, "", "")
			if err != nil {
				t.Fatalf("SQL 解析失败: %v", err)
			}
			v := &TraverseDMLHasConstraint{}
			stmt.Accept(v)
			if v.IsMatch == 0 {
				t.Fatalf("应匹配到 DML 语句")
			}
			if v.DMLType != c.wantType {
				t.Errorf("DMLType 应为 %s，实际 %q（告警文案缺主语）", c.wantType, v.DMLType)
			}
			if v.HasLimit != c.wantLimit {
				t.Errorf("HasLimit 应为 %v，实际 %v（被子表达式检查覆盖）", c.wantLimit, v.HasLimit)
			}
			if c.wantLimit && v.LimitCount != c.wantLimitCount {
				t.Errorf("LimitCount 应为 %d，实际 %d", c.wantLimitCount, v.LimitCount)
			}
			if v.HasOrderBy != c.wantOrderBy {
				t.Errorf("HasOrderBy 应为 %v，实际 %v（被子表达式检查覆盖）", c.wantOrderBy, v.HasOrderBy)
			}
		})
	}
}
