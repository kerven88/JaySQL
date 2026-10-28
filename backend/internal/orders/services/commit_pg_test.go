package services

import (
	"strings"
	"testing"
)

// 用户实际报错的 PG 建表工单（IDENTITY 主键 + COMMENT ON + 普通索引）
const pgOrderSQL = `-- 创建业务表，单主键标准模板
CREATE TABLE public.t_demo (
    id bigint GENERATED ALWAYS AS IDENTITY,
    name varchar(128) NOT NULL,
    status smallint NOT NULL DEFAULT 1,
    remark text,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz,
    CONSTRAINT pk_t_demo PRIMARY KEY (id)
);
-- 表注释
COMMENT ON TABLE public.t_demo IS '演示业务表';
-- 字段注释
COMMENT ON COLUMN public.t_demo.id IS '主键ID';
COMMENT ON COLUMN public.t_demo.name IS '名称';
COMMENT ON COLUMN public.t_demo.status IS '状态：0禁用，1正常，2删除';
COMMENT ON COLUMN public.t_demo.remark IS '备注信息';
COMMENT ON COLUMN public.t_demo.created_at IS '创建时间';
COMMENT ON COLUMN public.t_demo.updated_at IS '更新时间';
-- 业务普通索引（按需添加）
CREATE INDEX idx_t_demo_status ON public.t_demo(status);`

// 修复前：该 SQL 在提交阶段即被 TiDB parser 拦截（SQL解析错误 near "IDENTITY"）
func TestPGOrderSQLTypeInference(t *testing.T) {
	sqlType, err := inferOrderSQLType(pgOrderSQL, "PostgreSQL")
	if err != nil {
		t.Fatalf("PG 工单类型推断失败（修复前即在此报 SQL解析错误）: %v", err)
	}
	if sqlType != "DDL" {
		t.Fatalf("期望 DDL，实际 %s", sqlType)
	}
}

func TestPGOrderTargetMode(t *testing.T) {
	mode, err := detectOrderTargetMode(pgOrderSQL, "PostgreSQL")
	if err != nil {
		t.Fatalf("PG 工单目标模式判断失败: %v", err)
	}
	if mode != orderTargetSchema {
		t.Fatalf("期望 schema 模式，实际 %s", mode)
	}
}

func TestPGCreateSchemaTargetMode(t *testing.T) {
	mode, err := detectOrderTargetMode("CREATE SCHEMA hr;", "PostgreSQL")
	if err != nil {
		t.Fatalf("create schema 判断失败: %v", err)
	}
	if mode != orderTargetDatabase {
		t.Fatalf("create schema 应为库层级，实际 %s", mode)
	}
}

func TestPGMixedLevelRejected(t *testing.T) {
	_, err := detectOrderTargetMode("CREATE SCHEMA hr; CREATE TABLE t (id int);", "PostgreSQL")
	if err == nil || !strings.Contains(err.Error(), "不同目标层级") {
		t.Fatalf("混用层级应报错，实际: %v", err)
	}
}

// 查询/导出语句仍不允许与 DDL、DML 混在同一工单（导出走文件流、不落库，混排没有执行语义）；
// 但 DDL + DML 现在允许混用，整单类型归约为 MIXED。
func TestPGExportMixedWithDDLRejected(t *testing.T) {
	_, err := inferOrderSQLType("CREATE TABLE a (id int); SELECT 1;", "PostgreSQL")
	if err == nil || !strings.Contains(err.Error(), "混在同一个工单") {
		t.Fatalf("查询与 DDL 混用应报错，实际: %v", err)
	}
}

func TestPGMixedDDLDMLAllowed(t *testing.T) {
	sqlType, err := inferOrderSQLType("CREATE TABLE a (id int); INSERT INTO a (id) VALUES (1);", "PostgreSQL")
	if err != nil {
		t.Fatalf("DDL + DML 混用应放行，实际报错: %v", err)
	}
	if sqlType != "MIXED" {
		t.Fatalf("期望 MIXED，实际 %s", sqlType)
	}
}

// MySQL 链路回归：行为保持不变
func TestMySQLTypeInferenceUnchanged(t *testing.T) {
	sqlType, err := inferOrderSQLType("CREATE TABLE `t_demo` (`id` bigint PRIMARY KEY);", "MySQL")
	if err != nil {
		t.Fatalf("MySQL 类型推断失败: %v", err)
	}
	if sqlType != "DDL" {
		t.Fatalf("期望 DDL，实际 %s", sqlType)
	}
}
