package traverses

// INSERT / UPDATE 语法语义类规则的 AST 采集测试（2026-09-29 新增实现）。
// 只验证「遍历器拿到的信息是否正确」，判定与告警文案由 logics 层负责。

import (
	"testing"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

func parseStmt(t *testing.T, sql string) ast.StmtNode {
	t.Helper()
	p := parser.New()
	stmts, _, err := p.Parse(sql, "", "")
	if err != nil {
		t.Fatalf("解析失败 %s: %v", sql, err)
	}
	if len(stmts) == 0 {
		t.Fatalf("无语句: %s", sql)
	}
	return stmts[0]
}

func TestTraverseDMLInsertWithColumnsExtras(t *testing.T) {
	cases := []struct {
		name     string
		sql      string
		wantDup  []string
		wantNull []string
		wantSys  bool
	}{
		{
			name: "重复字段", sql: "INSERT INTO t(a, A, b) VALUES (1,2,3)",
			wantDup: []string{"a"},
		},
		{
			name: "无重复字段", sql: "INSERT INTO t(a, b) VALUES (1,2)",
		},
		{
			name: "显式插入NULL", sql: "INSERT INTO t(a, b) VALUES (NULL, 1)",
			wantNull: []string{"a"},
		},
		{
			name: "多行插入NULL只报一次", sql: "INSERT INTO t(a, b) VALUES (NULL, 1), (NULL, 2)",
			wantNull: []string{"a"},
		},
		{
			name: "使用SYSDATE", sql: "INSERT INTO t(a) VALUES (SYSDATE())",
			wantSys: true,
		},
		{
			name: "使用NOW不算", sql: "INSERT INTO t(a) VALUES (NOW())",
		},
	}

	for _, c := range cases {
		stmt := parseStmt(t, c.sql)
		v := &TraverseDMLInsertWithColumns{}
		stmt.Accept(v)
		if v.IsMatch == 0 {
			t.Fatalf("%s: 未匹配 INSERT", c.name)
		}
		if !equalStrings(v.DuplicateColumns, c.wantDup) {
			t.Errorf("%s: 重复列 got=%v want=%v", c.name, v.DuplicateColumns, c.wantDup)
		}
		if !equalStrings(v.NullValueColumns, c.wantNull) {
			t.Errorf("%s: NULL值列 got=%v want=%v", c.name, v.NullValueColumns, c.wantNull)
		}
		if v.HasSysdateFunc != c.wantSys {
			t.Errorf("%s: SYSDATE got=%v want=%v", c.name, v.HasSysdateFunc, c.wantSys)
		}
	}
}

func TestTraverseDMLUpdateSetColumnsExtras(t *testing.T) {
	cases := []struct {
		name         string
		sql          string
		wantTables   int
		wantNoPrefix []string
		wantAndSep   []string
		wantOwner    string
	}{
		{
			name: "单表UPDATE-无前缀不记录", sql: "UPDATE t SET a = 1 WHERE id = 1",
			wantTables: 1, wantOwner: "t",
		},
		{
			name: "多表UPDATE-无前缀", sql: "UPDATE t1 JOIN t2 ON t1.id = t2.id SET a = 1",
			wantTables: 2, wantNoPrefix: []string{"a"},
		},
		{
			name: "多表UPDATE-带前缀", sql: "UPDATE t1 JOIN t2 ON t1.id = t2.id SET t1.a = 1",
			wantTables: 2, wantOwner: "t1",
		},
		{
			name: "AND分隔多个SET列", sql: "UPDATE t SET a = 1 AND b = 2 WHERE id = 1",
			wantTables: 1, wantAndSep: []string{"a"}, wantOwner: "t",
		},
		{
			name: "合法布尔表达式不算AND分隔", sql: "UPDATE t SET a = (b > 1 AND c < 2) WHERE id = 1",
			wantTables: 1, wantOwner: "t",
		},
	}

	for _, c := range cases {
		stmt := parseStmt(t, c.sql)
		v := &TraverseDMLUpdateSetColumns{}
		stmt.Accept(v)
		if v.IsMatch == 0 {
			t.Fatalf("%s: 未匹配 UPDATE", c.name)
		}
		if v.TableCount != c.wantTables {
			t.Errorf("%s: 表数量 got=%d want=%d", c.name, v.TableCount, c.wantTables)
		}
		if !equalStrings(v.NoPrefixCols, c.wantNoPrefix) {
			t.Errorf("%s: 无前缀列 got=%v want=%v", c.name, v.NoPrefixCols, c.wantNoPrefix)
		}
		if !equalStrings(v.AndSepCols, c.wantAndSep) {
			t.Errorf("%s: AND分隔列 got=%v want=%v", c.name, v.AndSepCols, c.wantAndSep)
		}
		if len(v.SetItems) > 0 && v.SetItems[0].Table != c.wantOwner {
			t.Errorf("%s: SET项归属表 got=%q want=%q", c.name, v.SetItems[0].Table, c.wantOwner)
		}
	}
}

// equalStrings 比较两个字符串切片（nil 与空切片视为相等）
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
