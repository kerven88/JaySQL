package traverses

import (
	"testing"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

// TestTraverseDMLDeleteTablesAliasTarget 多表删除的目标列表可能是表别名：
// `DELETE rp FROM t1 rp JOIN t2 p ...` 中 parser 拿到的目标是 "rp"，
// 必须还原成真实表名，否则存在性检查会误报「表`rp`不存在」。
// See also: https://dev.mysql.com/doc/refman/8.0/en/delete.html
func TestTraverseDMLDeleteTablesAliasTarget(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "别名目标还原为真实表",
			sql:  "DELETE rp FROM insight_role_permissions rp JOIN insight_permissions p ON p.id = rp.permission_id WHERE rp.role_id = 11",
			want: []string{"insight_role_permissions", "insight_permissions"},
		},
		{
			name: "常规多表删除目标为真实表名",
			sql:  "DELETE t1, t2 FROM t1 JOIN t2 ON t1.id = t2.id",
			want: []string{"t1", "t2"},
		},
		{
			name: "别名带库前缀目标",
			sql:  "DELETE a.* FROM db1.t1 AS a JOIN db1.t2 AS b ON a.id = b.tid",
			want: []string{"t1", "t2"},
		},
		{
			name: "单表删除不受影响",
			sql:  "DELETE FROM insight_role_permissions WHERE id = 1",
			want: []string{"insight_role_permissions"},
		},
	}
	p := parser.New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmts, _, err := p.ParseSQL(tc.sql)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			d, ok := stmts[0].(*ast.DeleteStmt)
			if !ok {
				t.Fatalf("not a delete stmt")
			}
			tr := &TraverseDMLDeleteTables{}
			tr.Enter(d)
			if len(tr.Tables) != len(tc.want) {
				t.Fatalf("tables = %v, want %v", tr.Tables, tc.want)
			}
			for i := range tc.want {
				if tr.Tables[i] != tc.want[i] {
					t.Fatalf("tables = %v, want %v", tr.Tables, tc.want)
				}
			}
		})
	}
}
