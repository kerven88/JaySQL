package services

import "testing"

// 回归：PG 工单 tk-2dp3ws0bszqh 执行报 3F000。
// schema 字段存的是「database.schema」完整资源路径（与前端资源树/DAS/执行器口径一致），
// executeTask 曾无条件再拼一次 database 前缀，产生「postgres.postgres.public」，
// 执行器拆分后 SET search_path 指向不存在的 schema，CREATE TABLE 报
// 「no schema has been selected to create in」。normalizeOrderTargetSchema 必须幂等。
func TestNormalizeOrderTargetSchema(t *testing.T) {
	cases := []struct {
		name   string
		dbType string
		db     string
		schema string
		want   string
	}{
		{"PG 完整资源路径原样保留", "PostgreSQL", "postgres", "postgres.public", "postgres.public"},
		{"PG 裸 schema 补前缀", "PostgreSQL", "postgres", "public", "postgres.public"},
		{"PG schema 为空回退 database", "PostgreSQL", "postgres", "", "postgres"},
		{"PG 两者皆空", "PostgreSQL", "", "", ""},
		{"PG database 为空保留 schema", "PostgreSQL", "", "postgres.public", "postgres.public"},
		{"SQLServer 同口径", "SQLServer", "db1", "db1.dbo", "db1.dbo"},
		{"MySQL schema 空回退 database", "MySQL", "mydb", "", "mydb"},
		{"MySQL schema 与 database 对齐", "MySQL", "mydb", "mydb", "mydb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeOrderTargetSchema(tc.dbType, tc.db, tc.schema)
			if got != tc.want {
				t.Fatalf("normalize(%s,%q,%q) = %q, want %q", tc.dbType, tc.db, tc.schema, got, tc.want)
			}
		})
	}
}

// 回归：inspectConnTarget 曾把「database.schema」整串透出给预审引擎，
// pg_rules 用它作 table_schema/schemaname 字面量永远匹配不上，存在性探测静默失效。
func TestInspectConnTargetStripsDatabasePrefix(t *testing.T) {
	cases := []struct {
		name              string
		dbType            string
		db                string
		schema            string
		wantConnDB        string
		wantInspectSchema string
	}{
		{"PG 剥离 database 前缀", "PostgreSQL", "postgres", "postgres.public", "postgres", "public"},
		{"PG 裸 schema 保留", "PostgreSQL", "postgres", "public", "postgres", "public"},
		{"PG database 为空不剥离", "PostgreSQL", "", "postgres.public", "postgres.public", "postgres.public"},
		{"MySQL 原样透出", "MySQL", "", "mydb", "mydb", "mydb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connDB, inspectSchema := inspectConnTarget(tc.dbType, tc.db, tc.schema)
			if connDB != tc.wantConnDB || inspectSchema != tc.wantInspectSchema {
				t.Fatalf("inspectConnTarget(%s,%q,%q) = (%q,%q), want (%q,%q)",
					tc.dbType, tc.db, tc.schema, connDB, inspectSchema, tc.wantConnDB, tc.wantInspectSchema)
			}
		})
	}
}
