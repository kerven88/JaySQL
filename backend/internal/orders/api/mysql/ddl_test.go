package mysql

import "testing"

func TestParseMySQLVersion(t *testing.T) {
	cases := []struct {
		raw       string
		wantMajor int
		wantMinor int
		wantPatch int
		wantMaria bool
		wantErr   bool
	}{
		{"8.0.36", 8, 0, 36, false, false},
		{"5.7.44-log", 5, 7, 44, false, false},
		{"5.6.51", 5, 6, 51, false, false},
		{"8.0.36-tidb-v7.5.0", 8, 0, 36, false, false}, // TiDB 兼容串按 MySQL 语义取前三段
		{"10.11.6-MariaDB", 10, 11, 6, true, false},
		{"5.5.5-10.6.12-MariaDB", 10, 6, 12, true, false}, // 兼容协议伪装串
		{"garbage", 0, 0, 0, false, true},
		{"", 0, 0, 0, false, true},
	}
	for _, tc := range cases {
		info, err := parseMySQLVersion(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseMySQLVersion(%q) 期望报错，实际成功: %+v", tc.raw, info)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseMySQLVersion(%q) 报错: %v", tc.raw, err)
		}
		if info.Major != tc.wantMajor || info.Minor != tc.wantMinor || info.Patch != tc.wantPatch || info.IsMariaDB != tc.wantMaria {
			t.Fatalf("parseMySQLVersion(%q) = %+v, 期望 major=%d minor=%d patch=%d maria=%v",
				tc.raw, info, tc.wantMajor, tc.wantMinor, tc.wantPatch, tc.wantMaria)
		}
	}
}

func TestIsAlterTableOnlineDDL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		// 用户工单场景：DROP INDEX + ADD INDEX 应直连，不进 gh-ost
		{"drop_index", "ALTER TABLE `insight_users` DROP INDEX `uniq_username`;", true},
		{"add_index", "ALTER TABLE `insight_users` ADD INDEX `idx_email` (`email`);", true},
		{"add_unique_index", "ALTER TABLE t ADD UNIQUE INDEX uk(a, b)", true},
		{"add_key_no_name", "ALTER TABLE t ADD KEY (email)", true},
		{"multi_clause_index_only", "ALTER TABLE t ADD INDEX a(x), DROP INDEX b", true},

		// 重 DDL：仍走 gh-ost
		{"add_column", "ALTER TABLE t ADD COLUMN c INT NOT NULL DEFAULT 0", false},
		{"drop_column", "ALTER TABLE t DROP COLUMN c", false},
		{"drop_primary_key", "ALTER TABLE t DROP PRIMARY KEY", false},
		{"add_primary_key", "ALTER TABLE t ADD PRIMARY KEY (id)", false},
		{"fulltext_index", "ALTER TABLE t ADD FULLTEXT INDEX f(body)", false},
		{"engine_change", "ALTER TABLE t ENGINE=InnoDB", false},
		{"mixed_index_and_column", "ALTER TABLE t ADD INDEX a(x), ADD COLUMN c INT", false},

		// 既有瞬时 DDL：保持直连
		{"comment_only", "ALTER TABLE t COMMENT = 'demo'", true},
		{"charset_only", "ALTER TABLE t CONVERT TO CHARACTER SET utf8mb4", false},
		// 既有行为：ALGORITHM=INPLACE 后跟其他子句时 firstWord 分词不命中 algorithm case → 走 gh-ost（本次不动）
		{"algorithm_with_index", "ALTER TABLE t ALGORITHM=INPLACE, ADD INDEX a(x)", false},
		{"algorithm_alone", "ALTER TABLE t ALGORITHM=INPLACE", true},

		// 非 ALTER：不适用
		{"create_table", "CREATE TABLE t (id INT)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAlterTableOnlineDDL(tc.sql); got != tc.want {
				t.Fatalf("isAlterTableOnlineDDL(%q) = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}

// TestBuildGhostAlterClause 覆盖 gh-ost --alter 片段的还原。
// 回归背景：工单 tk-tx7e5vcqtnv0 第 2 条
//   ALTER TABLE insight_order_tasks MODIFY COLUMN `sql` mediumtext NULL COMMENT 'SQL语句';
// 旧实现会把反引号删掉 → `MODIFY COLUMN sql ...` → 目标库报 Error 1064（sql 是 MySQL 保留字）。
func TestBuildGhostAlterClause(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			// 保留字列名必须保留反引号，剥掉就会被 MySQL 判为语法错误
			name: "reserved_word_column_keeps_backticks",
			raw:  "MODIFY COLUMN `sql` mediumtext NULL COMMENT 'SQL语句';",
			want: "MODIFY COLUMN `sql` mediumtext NULL COMMENT 'SQL语句'",
		},
		{
			name: "normal_column",
			raw:  "MODIFY COLUMN content mediumtext NULL COMMENT '工单内容';",
			want: "MODIFY COLUMN content mediumtext NULL COMMENT '工单内容'",
		},
		{
			// COMMENT 里的双引号不能被改写成单引号，否则字面量被截断
			name: "double_quotes_preserved",
			raw:  "MODIFY COLUMN `key` varchar(64) NOT NULL DEFAULT \"a'b\";",
			want: "MODIFY COLUMN `key` varchar(64) NOT NULL DEFAULT \"a'b\"",
		},
		{
			name: "no_trailing_semicolon",
			raw:  "ADD COLUMN c INT NOT NULL DEFAULT 0",
			want: "ADD COLUMN c INT NOT NULL DEFAULT 0",
		},
		{
			// 多行 ALTER：只去首尾空白与末尾分号，中间换行保持原样
			name: "multiline",
			raw:  "  MODIFY COLUMN `order` int NOT NULL;\n",
			want: "MODIFY COLUMN `order` int NOT NULL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildGhostAlterClause(tc.raw); got != tc.want {
				t.Fatalf("buildGhostAlterClause(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
