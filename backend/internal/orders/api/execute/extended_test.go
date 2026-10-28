package execute

import "testing"

// 回归：工单 tk-2dp3ws0bszqh 执行报「no schema has been selected to create in (SQLSTATE 3F000)」。
// 根因是上游把「database.schema」路径二次拼接成「postgres.postgres.public」，
// parse 后 schema 名不存在，SET search_path 静默成功但 CREATE 无有效 schema。
func TestParseDatabaseAndSchema(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantDatabase string
		wantSchema   string
	}{
		{"标准资源路径", "postgres.public", "postgres", "public"},
		{"历史双拼路径应拆出首段", "postgres.postgres.public", "postgres", "postgres.public"},
		{"仅 database 单段不再报错", "postgres", "postgres", ""},
		{"schema 名可含点", "db.my.schema", "db", "my.schema"},
		{"前后空白修剪", " postgres.public ", "postgres", "public"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, sc, err := parseDatabaseAndSchema(tc.in)
			if err != nil {
				t.Fatalf("不应返回错误: %v", err)
			}
			if db != tc.wantDatabase || sc != tc.wantSchema {
				t.Fatalf("parse(%q) = (%q,%q), want (%q,%q)", tc.in, db, sc, tc.wantDatabase, tc.wantSchema)
			}
		})
	}
}
