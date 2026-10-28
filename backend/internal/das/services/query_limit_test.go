package services

import "testing"

func TestApplyQueryLimit(t *testing.T) {
	tests := []struct {
		name   string
		dbType string
		sql    string
		want   string
	}{
		{
			name:   "postgres append limit",
			dbType: "postgresql",
			sql:    "select * from users",
			want:   "select * from users LIMIT 200",
		},
		{
			name:   "postgres cap limit",
			dbType: "postgresql",
			sql:    "select * from users limit 1000",
			want:   "select * from users limit 1000",
		},
		{
			name:   "postgres clamp over max",
			dbType: "postgresql",
			sql:    "select * from users limit 1001",
			want:   "select * from users limit 1000",
		},
		{
			name:   "mysql no string rewrite",
			dbType: "mysql",
			sql:    "SELECT /*+ max_execution_time(600000)*/ * FROM `t1` LIMIT 100 /* req */",
			want:   "SELECT /*+ max_execution_time(600000)*/ * FROM `t1` LIMIT 100 /* req */",
		},
		{
			name:   "sqlserver inject top",
			dbType: "sqlserver",
			sql:    "select * from users",
			want:   "select TOP 200 * from users",
		},
		{
			name:   "sqlserver cap fetch next",
			dbType: "sqlserver",
			sql:    "select * from users order by id offset 0 rows fetch next 1000 rows only",
			want:   "select * from users order by id offset 0 rows fetch next 1000 rows only",
		},
		{
			name:   "sqlserver clamp top over max",
			dbType: "sqlserver",
			sql:    "select top 1001 * from users",
			want:   "select top 1000 * from users",
		},
		{
			name:   "oracle wrap rownum",
			dbType: "oracle",
			sql:    "select * from users",
			want:   "SELECT * FROM (select * from users) WHERE ROWNUM <= 200",
		},
		{
			name:   "oracle cap fetch first",
			dbType: "oracle",
			sql:    "select * from users fetch first 1000 rows only",
			want:   "select * from users fetch first 1000 rows only",
		},
		{
			name:   "oracle clamp fetch first over max",
			dbType: "oracle",
			sql:    "select * from users fetch first 1001 rows only",
			want:   "select * from users fetch first 1000 rows only",
		},
		{
			name:   "oracle keep rownum cap",
			dbType: "oracle",
			sql:    "select * from users where rownum <= 20",
			want:   "select * from users where rownum <= 20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyQueryLimit(tt.dbType, tt.sql)
			if err != nil {
				t.Fatalf("applyQueryLimit() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("applyQueryLimit() = %q, want %q", got, tt.want)
			}
		})
	}
}
