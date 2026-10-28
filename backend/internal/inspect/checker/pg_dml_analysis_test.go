package checker

import "testing"

func TestPGDMLAnalysisTmp(t *testing.T) {
	cases := []struct {
		name       string
		sql        string
		kind       string
		joins      int
		limit      int
		setCols    int
		hasUpdTime bool
		maxIn      int
	}{
		{name: "update单表带limit", sql: "UPDATE t_order SET status = 1 WHERE id > 100 LIMIT 10", kind: "update", joins: 1, limit: 10, setCols: 1, hasUpdTime: false, maxIn: 0},
		{name: "update两表join", sql: "UPDATE a JOIN b ON a.id = b.id SET a.status = 1 WHERE b.x = 2", kind: "update", joins: 2, limit: 0, setCols: 1, hasUpdTime: false, maxIn: 0},
		{name: "update含update_time", sql: "UPDATE t SET status = 1, update_time = now() WHERE id IN (1,2,3)", kind: "update", joins: 1, limit: 0, setCols: 2, hasUpdTime: true, maxIn: 3},
		{name: "delete using两表", sql: "DELETE FROM a USING b WHERE a.id = b.id", kind: "delete", joins: 2, limit: 0, setCols: 0, hasUpdTime: false, maxIn: 0},
		{name: "delete where in大量", sql: "DELETE FROM t WHERE id IN (1,2,3,4,5,6,7)", kind: "delete", joins: 1, limit: 0, setCols: 0, hasUpdTime: false, maxIn: 7},
		{name: "in列表含字符串", sql: "UPDATE t SET status = 0 WHERE name IN ('a', 'b', 'c') AND id IN (1)", kind: "update", joins: 1, limit: 0, setCols: 1, hasUpdTime: false, maxIn: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := analyzePGDML(c.sql)
			if !res.OK || res.Kind != c.kind {
				t.Fatalf("kind=%q ok=%v, want %q", res.Kind, res.OK, c.kind)
			}
			if res.JoinTableCount != c.joins {
				t.Errorf("JoinTableCount=%d want %d", res.JoinTableCount, c.joins)
			}
			if res.LimitRows != c.limit {
				t.Errorf("LimitRows=%d want %d", res.LimitRows, c.limit)
			}
			if len(res.UpdateSetColumns) != c.setCols {
				t.Errorf("UpdateSetColumns=%v want %d cols", res.UpdateSetColumns, c.setCols)
			}
			hasTime := false
			for _, col := range res.UpdateSetColumns {
				if col == "update_time" {
					hasTime = true
				}
			}
			if hasTime != c.hasUpdTime {
				t.Errorf("hasUpdateTime=%v want %v (cols=%v)", hasTime, c.hasUpdTime, res.UpdateSetColumns)
			}
			if res.MaxInElements != c.maxIn {
				t.Errorf("MaxInElements=%d want %d", res.MaxInElements, c.maxIn)
			}
		})
	}
}
