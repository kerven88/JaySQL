package services

import "testing"

// ============================================================================
// SQL Server 表信息回归
//
// 覆盖三条硬约束：
//   1.「库.模式」路径必须拆成两段（整段拼进 SQL 会查不到任何 schema）
//   2. 类型还原要带上长度 / 精度 / max
//   3. DDL 形态要能被前端按 MySQL SHOW CREATE TABLE 的方式反解列与索引
// ============================================================================

func TestSplitSQLServerResourcePath(t *testing.T) {
	db, schema, err := splitSQLServerResourcePath("msdb.dbo")
	if err != nil {
		t.Fatalf("合法路径被拒：%v", err)
	}
	if db != "msdb" || schema != "dbo" {
		t.Fatalf("拆分结果不对：db=%q schema=%q", db, schema)
	}

	for _, bad := range []string{"", "msdb", "msdb.dbo.x", "msdb.", ".dbo", "ms;db.dbo"} {
		if _, _, err := splitSQLServerResourcePath(bad); err == nil {
			t.Fatalf("非法路径 %q 竟然通过了校验", bad)
		}
	}
	// 带空格的两段路径应被 trim 后接受（树节点来源不可控）
	if db, schema, err = splitSQLServerResourcePath(" msdb . dbo "); err != nil || db != "msdb" || schema != "dbo" {
		t.Fatalf("带空格路径应 trim 后通过：db=%q schema=%q err=%v", db, schema, err)
	}
}

func TestBuildSQLServerColumnType(t *testing.T) {
	cases := []struct {
		name string
		row  map[string]interface{}
		want string
	}{
		{"整型", map[string]interface{}{"DATA_TYPE": "int"}, "int"},
		{"定长(50)", map[string]interface{}{"DATA_TYPE": "nvarchar", "CHAR_MAX_LEN": "50"}, "nvarchar(50)"},
		{"max", map[string]interface{}{"DATA_TYPE": "varchar", "CHAR_MAX_LEN": "-1"}, "varchar(max)"},
		{"小数", map[string]interface{}{"DATA_TYPE": "decimal", "NUM_PRECISION": "18", "NUM_SCALE": "2"}, "decimal(18,2)"},
		{"datetime2(7)", map[string]interface{}{"DATA_TYPE": "datetime2", "DT_PRECISION": "7"}, "datetime2(7)"},
		{"datetime2 精度 0 不补", map[string]interface{}{"DATA_TYPE": "datetime2", "DT_PRECISION": "0"}, "datetime2"},
	}
	for _, tc := range cases {
		if got := buildSQLServerColumnType(tc.row); got != tc.want {
			t.Fatalf("%s：期望 %q，实际 %q", tc.name, tc.want, got)
		}
	}
}

// DDL 必须能被前端的 CREATE TABLE 解析器切开：列在前、索引在后、无多余空行卡住逗号。
func TestBuildSQLServerTableDDL(t *testing.T) {
	columns := []map[string]interface{}{
		{"COLUMN_NAME": "id", "DATA_TYPE": "int", "IS_NULLABLE": "NO", "IS_IDENTITY": "1"},
		{"COLUMN_NAME": "name", "DATA_TYPE": "nvarchar", "CHAR_MAX_LEN": "50", "IS_NULLABLE": "NO", "COLUMN_DEFAULT": "(N'')"},
		{"COLUMN_NAME": "created_at", "DATA_TYPE": "datetime2", "DT_PRECISION": "7", "IS_NULLABLE": "YES"},
	}
	indexes := []string{"PRIMARY KEY ([id])", "KEY [idx_name] ([name])"}
	lines := buildSQLServerTableDDL("[msdb].[dbo].[t]", &columns, indexes)

	want := `CREATE TABLE [msdb].[dbo].[t] (
  [id] int IDENTITY NOT NULL,
  [name] nvarchar(50) NOT NULL DEFAULT (N''),
  [created_at] datetime2(7),
  PRIMARY KEY ([id])
  KEY [idx_name] ([name])
);`
	if lines != want {
		t.Fatalf("DDL 不符：\n%s", lines)
	}

	// 索引查询失败时退化为纯列 DDL，末尾不能留下悬空逗号
	noIndex := buildSQLServerTableDDL("[msdb].[dbo].[t]", &columns, nil)
	if noIndex != `CREATE TABLE [msdb].[dbo].[t] (
  [id] int IDENTITY NOT NULL,
  [name] nvarchar(50) NOT NULL DEFAULT (N''),
  [created_at] datetime2(7)
);` {
		t.Fatalf("无索引时的 DDL 不符：\n%s", noIndex)
	}
}
