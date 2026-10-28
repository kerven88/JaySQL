package services

import "testing"

func TestValidateSingleSelectOnly(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		want    string
		wantErr string
	}{
		{
			name: "plain select",
			sql:  "select 1;",
			want: "select 1",
		},
		{
			name: "line comment before select",
			sql:  "-- 查询阻塞\nselect 1",
			want: "-- 查询阻塞\nselect 1",
		},
		{
			name: "block comment before with",
			sql:  "/* hint */ with cte as (select 1) select * from cte",
			want: "/* hint */ with cte as (select 1) select * from cte",
		},
		{
			name:    "reject non query statement",
			sql:     "-- note\nupdate t set a = 1",
			wantErr: "当前阶段仅允许执行SELECT语句",
		},
		{
			name: "let malformed numeric prefix go through",
			sql:  "11\n\nselect 1;",
			want: "11\n\nselect 1",
		},
		{
			name: "let malformed misspelled select go through",
			sql:  "selct 1",
			want: "selct 1",
		},
		{
			name:    "reject multiple statements",
			sql:     "select 1; select 2",
			wantErr: "每次仅允许执行一条语句",
		},
		{
			name: "pg DDL 拉取 CTE 字面量含分号 (CTE with literal semicolons)",
			sql: `
WITH base AS (
    SELECT c.oid AS rel_oid
    FROM pg_catalog.pg_namespace n
    JOIN pg_catalog.pg_class c ON c.relnamespace = n.oid
    WHERE n.nspname = 'dwd'
      AND c.relname = 'dw_lget_bidding_publish'
      AND c.relkind IN ('r','p','v','m','f')
    LIMIT 1
),
columns AS (
    SELECT
        pg_catalog.quote_ident(a.attname) AS col_name,
        pg_catalog.format_type(a.atttypid, a.atttypmod) AS col_type,
        a.attnotnull,
        pg_catalog.pg_get_expr(d.adbin, d.adrelid) AS default_expr
    FROM base b
    JOIN pg_catalog.pg_attribute a ON a.attrelid = b.rel_oid
    LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
    WHERE a.attnum > 0
      AND NOT a.attisdropped
    ORDER BY a.attnum
),
col_defs AS (
    SELECT string_agg(
        '    ' || col_name || ' ' || col_type ||
        CASE WHEN attnotnull THEN ' NOT NULL' ELSE '' END ||
        CASE WHEN default_expr IS NOT NULL THEN ' DEFAULT ' || default_expr ELSE '' END,
        E';\n'
    ) AS col_list
    FROM columns
),
create_sql AS (
    SELECT
        CASE WHEN b.rel_oid IS NOT NULL THEN
            'CREATE TABLE "dwd"."dw_lget_bidding_publish" (' || E'\n' ||
            COALESCE(cd.col_list, '') || E'\n' || ');'
        ELSE
            'CREATE TABLE "dwd"."dw_lget_bidding_publish" ();'
        END AS ddl
    FROM base b
    CROSS JOIN col_defs cd
),
owner_sql AS (
    SELECT
        CASE WHEN r.rolname IS NOT NULL
        THEN 'ALTER TABLE "dwd"."dw_lget_bidding_publish" OWNER TO ' || pg_catalog.quote_ident(r.rolname)
        ELSE '' END AS ddl
    FROM base b
    LEFT JOIN pg_catalog.pg_class c ON c.oid = b.rel_oid
    LEFT JOIN pg_catalog.pg_roles r ON r.oid = c.relowner
    LIMIT 1
),
column_comments AS (
    SELECT
        string_agg(
            'COMMENT ON COLUMN "dwd"."dw_lget_bidding_publish".' ||
            pg_catalog.quote_ident(a.attname) || ' IS ' ||
            pg_catalog.quote_literal(pg_catalog.col_description(b.rel_oid, a.attnum)),
            E';\n' ORDER BY a.attnum
        ) || CASE WHEN count(*) > 0 THEN ';' ELSE '' END AS ddl
    FROM base b
    JOIN pg_catalog.pg_attribute a ON a.attrelid = b.rel_oid
    WHERE a.attnum > 0
      AND NOT a.attisdropped
      AND pg_catalog.col_description(b.rel_oid, a.attnum) IS NOT NULL
      AND length(btrim(pg_catalog.col_description(b.rel_oid, a.attnum))) > 0
),
table_comment AS (
    SELECT
        CASE WHEN pg_catalog.obj_description(b.rel_oid, 'pg_class') IS NOT NULL
          AND length(btrim(pg_catalog.obj_description(b.rel_oid, 'pg_class'))) > 0
        THEN 'COMMENT ON TABLE "dwd"."dw_lget_bidding_publish" IS ' ||
            pg_catalog.quote_literal(pg_catalog.obj_description(b.rel_oid, 'pg_class'))
        ELSE '' END AS ddl
    FROM base b
    LIMIT 1
)
SELECT
    trim(trailing E'; \t\n\r' from c.ddl)
    || CASE WHEN length(o.ddl) > 0 THEN E';\n\n' || o.ddl ELSE '' END
    || CASE WHEN length(cc.ddl) > 0 THEN E';\n\n' || cc.ddl ELSE '' END
    || CASE WHEN length(tc.ddl) > 0 THEN E';\n\n' || tc.ddl ELSE '' END
    || E';\n'
AS statement
FROM create_sql c
CROSS JOIN (SELECT 1) s
LEFT JOIN owner_sql o ON true
LEFT JOIN column_comments cc ON true
LEFT JOIN table_comment tc ON true;
			`,
			wantErr: "",
		},
		{
			name:    "cte semicolons should not mask real multi-statements (真正 2 条 SELECT 仍然拦截)",
			sql:     "WITH t AS (SELECT ';' AS c) SELECT * FROM t; SELECT 2;",
			wantErr: "每次仅允许执行一条语句",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateSingleSelectOnly(tt.sql)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("validateSingleSelectOnly() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateSingleSelectOnly() error = %v", err)
			}
			if tt.want != "" && got != tt.want {
				t.Fatalf("validateSingleSelectOnly() = %q, want %q", got, tt.want)
			}
			if tt.want == "" && got == "" {
				t.Fatalf("validateSingleSelectOnly() returned empty normalized SQL, want non-empty")
			}
		})
	}
}
