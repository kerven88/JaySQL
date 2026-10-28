package traverses

// INSERT 目标表/字段存在性规则的 AST 采集测试（规则 insert_stmt_check_column_exists）。
//
// 只验证遍历器拿到的信息是否正确：表名、库前缀、字段列表。
// 存在性判定与告警由 logics 层负责（见 logics/dml.go: LogicDMLInsertObjectExists）。

import (
	"testing"
)

func TestTraverseDMLInsertTarget(t *testing.T) {
	cases := []struct {
		name       string
		sql        string
		wantTable  string
		wantSchema string
		wantCols   []string
	}{
		{
			name: "无库前缀-无字段列表", sql: "INSERT INTO t_2026_1019 VALUES (10);",
			wantTable: "t_2026_1019",
		},
		{
			name: "带库前缀-带字段列表", sql: "INSERT INTO db2.t_order (id, name) VALUES (1, 'a');",
			wantTable: "t_order", wantSchema: "db2", wantCols: []string{"id", "name"},
		},
		{
			name: "反引号包裹", sql: "INSERT INTO `t_x` (`a`) VALUES (1);",
			wantTable: "t_x", wantCols: []string{"a"},
		},
		{
			name: "REPLACE语句同样采集", sql: "REPLACE INTO t_x (a) VALUES (1);",
			wantTable: "t_x", wantCols: []string{"a"},
		},
	}

	for _, c := range cases {
		stmt := parseStmt(t, c.sql)
		v := &TraverseDMLInsertWithColumns{}
		stmt.Accept(v)

		if v.IsMatch != 1 {
			t.Errorf("%s: IsMatch=%d，期望 1", c.name, v.IsMatch)
		}
		if v.Table != c.wantTable {
			t.Errorf("%s: Table=%q，期望 %q", c.name, v.Table, c.wantTable)
		}
		if v.Schema != c.wantSchema {
			t.Errorf("%s: Schema=%q，期望 %q", c.name, v.Schema, c.wantSchema)
		}
		if len(v.Columns) != len(c.wantCols) {
			t.Errorf("%s: Columns=%v，期望 %v", c.name, v.Columns, c.wantCols)
			continue
		}
		for i, col := range c.wantCols {
			if v.Columns[i] != col {
				t.Errorf("%s: Columns[%d]=%q，期望 %q", c.name, i, v.Columns[i], col)
			}
		}
	}
}
