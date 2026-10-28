package services

import (
	"testing"
)

// TestDetectOrderTargetMode 固化库层级/库内对象的目标层级判定。
//
// 这条判定直接决定审核引擎会不会被执行：
// syntax.go 中两处 `if targetMode != orderTargetSchema` 会整体短路审核，
// 因此 CREATE DATABASE（orderTargetNone）天然拿不到表级审核链路，
// 必须走 inspectDatabaseLevelDDL 静态审核，否则库级规则（如库字符集）永远不触发。
func TestDetectOrderTargetMode(t *testing.T) {
	cases := []struct {
		name   string
		dbType string
		sql    string
		want   orderTargetMode
	}{
		{"CREATE DATABASE 属库层级", "MySQL", "CREATE DATABASE testdb CHARACTER SET utf8", orderTargetNone},
		{"CREATE DATABASE IF NOT EXISTS 属库层级", "MySQL", "CREATE DATABASE IF NOT EXISTS testdb DEFAULT CHARACTER SET utf8", orderTargetNone},
		{"CREATE SCHEMA 属库层级", "MySQL", "create schema testdb character set utf8", orderTargetNone},
		{"ALTER DATABASE 归入库内对象(可走常规审核)", "MySQL", "ALTER DATABASE testdb CHARACTER SET utf8", orderTargetSchema},
		{"CREATE TABLE 属库内对象", "MySQL", "CREATE TABLE t1 (id int) CHARACTER SET utf8", orderTargetSchema},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := detectOrderTargetMode(c.sql, c.dbType)
			if err != nil {
				t.Fatalf("detectOrderTargetMode 返回错误: %v", err)
			}
			if got != c.want {
				t.Errorf("目标层级不符: 期望 %q 实际 %q", c.want, got)
			}
		})
	}
}

// TestInspectDatabaseLevelDDLSkipsPG PG 类语法 TiDB parser 解析不了，静态审核必须跳过而不是报错
func TestInspectDatabaseLevelDDLSkipsPG(t *testing.T) {
	got, err := inspectDatabaseLevelDDL("CREATE DATABASE testdb WITH ENCODING 'UTF8'", "inst-1", "PostgreSQL")
	if err != nil {
		t.Fatalf("PG 不应报错: %v", err)
	}
	if got != nil {
		t.Fatalf("PG 应返回 nil，实际 %+v", got)
	}
}
