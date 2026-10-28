package logics

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
	"github.com/simajiong/jaysql/pkg/parser"

	"github.com/pingcap/tidb/pkg/parser/mysql"
)

// ALTER ADD COLUMN ... PRIMARY KEY：主键列类型应随遍历结构一起带出（用于类型白名单校验）
func TestAlterAddPrimaryKeyTraverseCollectsTypes(t *testing.T) {
	// 场景一：`ADD COLUMN id bigint ... PRIMARY KEY` —— 类型来自本次列定义
	sql1 := "ALTER TABLE t_order ADD COLUMN id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY"
	stmt, err := parser.NewParseOneStmt(sql1, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v1 := &traverses.TraverseAlterTableAddPrimaryKey{}
	stmt.Accept(v1)
	if len(v1.Cols) != 1 || v1.Cols[0] != "id" {
		t.Fatalf("应收集主键列 [id]，实际: %v", v1.Cols)
	}
	if tp, ok := v1.ColTypes["id"]; !ok || tp != mysql.TypeLonglong {
		t.Errorf("应带出列 id 的类型 bigint(%d)，实际: %v(ok=%v)", mysql.TypeLonglong, tp, ok)
	}

	// 场景二：`ADD PRIMARY KEY (id)` —— 列已存在，类型需由表结构反查，本次结构不带类型
	sql2 := "ALTER TABLE t_order ADD PRIMARY KEY (id)"
	stmt2, err := parser.NewParseOneStmt(sql2, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v2 := &traverses.TraverseAlterTableAddPrimaryKey{}
	stmt2.Accept(v2)
	if len(v2.Cols) != 1 || v2.Cols[0] != "id" {
		t.Fatalf("应收集主键列 [id]，实际: %v", v2.Cols)
	}
	if _, ok := v2.ColTypes["id"]; ok {
		t.Errorf("ADD PRIMARY KEY 不应带列类型（类型须由表结构反查），实际带出了")
	}
}

// 表结构遍历必须带出「列名 → 类型」，并同时识别表级主键与列级主键
func TestShowCreateTableTraverseCollectsColTypesAndPrimaryKeys(t *testing.T) {
	// 表级主键
	sql := "CREATE TABLE `t_order` (" +
		"`id` bigint(20) unsigned NOT NULL AUTO_INCREMENT, " +
		"`name` varchar(32) DEFAULT NULL, " +
		"`code` varchar(32) NOT NULL, " +
		"PRIMARY KEY (`id`), " +
		"UNIQUE KEY `uk_code` (`code`)" +
		") ENGINE=InnoDB"
	stmt, err := parser.NewParseOneStmt(sql, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	stmt.Accept(v)

	if tp, ok := v.ColTypes["id"]; !ok || tp != mysql.TypeLonglong {
		t.Errorf("列 id 类型应为 bigint，实际: %v(ok=%v)", tp, ok)
	}
	if tp, ok := v.ColTypes["code"]; !ok || tp != mysql.TypeVarchar {
		t.Errorf("列 code 类型应为 varchar，实际: %v(ok=%v)", tp, ok)
	}
	if len(v.PrimaryKeys) != 1 || v.PrimaryKeys[0] != "id" {
		t.Errorf("表级主键应为 [id]，实际: %v", v.PrimaryKeys)
	}

	// 列级主键（历史实现会漏掉）
	sql2 := "CREATE TABLE `t_x` (`id` bigint NOT NULL PRIMARY KEY, `c1` int DEFAULT NULL) ENGINE=InnoDB"
	stmt2, err := parser.NewParseOneStmt(sql2, "", "")
	if err != nil {
		t.Fatalf("SQL 解析失败: %v", err)
	}
	v2 := &traverses.TraverseAlterTableShowCreateTableGetCols{}
	stmt2.Accept(v2)
	if len(v2.PrimaryKeys) != 1 || v2.PrimaryKeys[0] != "id" {
		t.Errorf("列级主键应被识别为 [id]，实际: %v", v2.PrimaryKeys)
	}
}
