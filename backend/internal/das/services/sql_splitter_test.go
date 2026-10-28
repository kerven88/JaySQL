package services

import (
	"reflect"
	"testing"
)

func TestSplitSQLStatements(t *testing.T) {
	type testCase struct {
		name          string
		input         string
		expected      []string
		expectedCount int
	}
	testCases := []testCase{
		{
			name:     "empty",
			input:    "",
			expected: nil,
		},
		{
			name:     "blank only",
			input:    "   \n\t\n  ",
			expected: nil,
		},
		{
			name:     "semicolons only",
			input:    ";;;;",
			expected: nil,
		},
		{
			name:  "single select with leading/trailing semis and whitespace",
			input: "  ;  ;  SELECT 1  ;  ;  \n  ;",
			expected: []string{
				"SELECT 1",
			},
		},
		{
			name: "pg CTE with literal semicolons (用户实际 DDL 拉取 SQL)",
			input: `
				WITH base AS (
					SELECT c.oid AS rel_oid
					FROM pg_catalog.pg_namespace n
					JOIN pg_catalog.pg_class c ON c.relnamespace = n.oid
					WHERE n.nspname = 'dwd' AND c.relname = 't' AND c.relkind IN ('r','p','v','m','f')
					LIMIT 1
				),
				create_sql AS (
					SELECT 'CREATE TABLE ' || '"dwd"."t"' || ' (' || E'\n' || ');' AS ddl FROM base
				),
				column_comments AS (
					SELECT string_agg(
						'COMMENT ON COLUMN ' || '"dwd"."t"' || '.' || a.attname || ' IS ' || quote_literal(col_description(b.rel_oid, a.attnum)),
						E';\n' ORDER BY a.attnum
					) || CASE WHEN count(*) > 0 THEN ';' ELSE '' END AS ddl
					FROM base b
					JOIN pg_attribute a ON a.attrelid = b.rel_oid
					WHERE col_description(b.rel_oid, a.attnum) IS NOT NULL
				)
				SELECT trim(trailing E'; \t\n\r' from c.ddl)
				FROM create_sql c
				LEFT JOIN column_comments cc ON true;
			`,
			expectedCount: 1,
		},
		{
			name: "two ddl with dash/pound/block comments containing semicolons",
			input: `
				-- ; leading dash comment with ;;;;;
				# another ; comment style
				/* block; comment /* nested; */ still; comment */
				CREATE TABLE a (id int not null);
				COMMENT ON TABLE a IS 'this; that; the other thing';
			`,
			expectedCount: 2,
		},
		{
			name: "dollar-quoted pg function body with semicolons inside",
			input: `
				CREATE OR REPLACE FUNCTION hello() RETURNS void AS $body$
				BEGIN
					RAISE NOTICE 'hello; world; !';
					PERFORM 1;
				END;
				$body$ LANGUAGE plpgsql;
				SELECT 1;
			`,
			expected: []string{
				"CREATE OR REPLACE FUNCTION hello() RETURNS void AS $body$\n\t\t\t\tBEGIN\n\t\t\t\t\tRAISE NOTICE 'hello; world; !';\n\t\t\t\t\tPERFORM 1;\n\t\t\t\tEND;\n\t\t\t\t$body$ LANGUAGE plpgsql",
				"SELECT 1",
			},
		},
		{
			name:          "E-escaped string with backslash-semicolon",
			input:         `SELECT E';\\n\\n;' AS t;;; SELECT E'abc\\'def';`,
			expectedCount: 2,
		},
		{
			name:  "double quote identifier + backtick identifier contains semicolons (looks like but inside ident)",
			input: "CREATE TABLE `weird;table` (\"col;name\" int); SELECT * FROM t;",
			expected: []string{
				"CREATE TABLE `weird;table` (\"col;name\" int)",
				"SELECT * FROM t",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitSQLStatements(tc.input)
			if tc.expectedCount > 0 {
				if len(got) != tc.expectedCount {
					t.Fatalf("unexpected statement count: got %d want %d\ngot=%#v", len(got), tc.expectedCount, got)
				}
			} else if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("unexpected split result: got=%#v want=%#v", got, tc.expected)
			}
		})
	}
}
