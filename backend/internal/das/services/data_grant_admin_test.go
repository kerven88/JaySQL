package services

import (
	"testing"

	"github.com/simajiong/jaysql/internal/das/models"
)

// ============================================================================
// 「数据访问」管理页切换到新授权表之后的回归测试
//
// 锁死三条容易回退的约束：
//   1. resource_type 推导：instance / table 层级就按层级给，不能一律被
//      insight_instance_schemas 的原始 resource_type 覆盖成 database
//      （覆盖会让表级授权在页面上显示成「数据库」，撤销时看不出撤的是哪张表）
//   2. 被授权主体已删除 / 已停用时要给页面灰标
//   3. 撤销新表授权必须级联清掉旧表同源行，否则旧页对账会把刚撤销的授权补回来
// ============================================================================

func TestFillDataGrantDisplayFieldsDerivesResourceType(t *testing.T) {
	db := setupDataGrantTestDB(t)
	if err := db.Exec(`INSERT INTO insight_users (uid, username, is_deleted, is_active) VALUES (1, 'admin', 0, 1)`).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	rows := []dataGrantListRow{
		// 表级：即使库里同名的 schema 元数据是 database，也必须显示成 table
		{GrantNo: "g-table", PrincipalType: "user", PrincipalID: 1,
			ResourceLevel: DataResourceLevelTable, InstanceID: "i1", SchemaName: "jaysql",
			TableName: "t1", SchemaResourceType: "database"},
		// 库级：Schema 元数据说是 schema（PostgreSQL）时要跟着变成 schema
		{GrantNo: "g-schema", PrincipalType: "user", PrincipalID: 1,
			ResourceLevel: DataResourceLevelDatabase, InstanceID: "i1", SchemaName: "appdb.public",
			SchemaResourceType: "schema"},
		// 库级但没有元数据：回落到 database
		{GrantNo: "g-db", PrincipalType: "user", PrincipalID: 1,
			ResourceLevel: DataResourceLevelDatabase, InstanceID: "i1", SchemaName: "shop"},
		// 实例级
		{GrantNo: "g-inst", PrincipalType: "user", PrincipalID: 1,
			ResourceLevel: DataResourceLevelInstance, InstanceID: "i1"},
	}

	fillDataGrantDisplayFields(rows)

	want := map[string]string{
		"g-table":  "table",
		"g-schema": "schema",
		"g-db":     "database",
		"g-inst":   "instance",
	}
	for _, row := range rows {
		if got := row.ResourceType; got != want[row.GrantNo] {
			t.Fatalf("%s 的 resource_type 期望 %q，实际 %q", row.GrantNo, want[row.GrantNo], got)
		}
	}
	// 展示名兜底：没有元数据展示名时用 schema_name，避免页面出现空白资源列
	if rows[2].DisplayName != "shop" {
		t.Fatalf("display_name 未回落到 schema_name：%q", rows[2].DisplayName)
	}
}

func TestFillDataGrantDisplayFieldsMarksInvalidPrincipal(t *testing.T) {
	db := setupDataGrantTestDB(t)
	if err := db.Exec(`INSERT INTO insight_users (uid, username, is_deleted, is_active) VALUES (1, 'alive', 0, 1), (2, 'gone', 1, 1), (3, 'off', 0, 0)`).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := db.Exec(`INSERT INTO insight_roles (id, code, name, is_deleted) VALUES (7, 'member', '普通成员', 1)`).Error; err != nil {
		t.Fatalf("seed role: %v", err)
	}

	rows := []dataGrantListRow{
		{GrantNo: "u-ok", PrincipalType: "user", PrincipalID: 1, ResourceLevel: DataResourceLevelInstance},
		{GrantNo: "u-del", PrincipalType: "user", PrincipalID: 2, ResourceLevel: DataResourceLevelInstance},
		{GrantNo: "u-off", PrincipalType: "user", PrincipalID: 3, ResourceLevel: DataResourceLevelInstance},
		{GrantNo: "u-404", PrincipalType: "user", PrincipalID: 999, ResourceLevel: DataResourceLevelInstance},
		{GrantNo: "r-del", PrincipalType: "role", PrincipalID: 7, ResourceLevel: DataResourceLevelInstance},
		{GrantNo: "r-404", PrincipalType: "role", PrincipalID: 888, ResourceLevel: DataResourceLevelInstance},
	}

	fillDataGrantDisplayFields(rows)

	want := map[string]string{
		"u-ok":  "",
		"u-del": "已删除",
		"u-off": "已停用",
		"u-404": "主体已不存在",
		"r-del": "已删除",
		"r-404": "角色已不存在",
	}
	for _, row := range rows {
		if row.PrincipalHint != want[row.GrantNo] {
			t.Fatalf("%s 的灰标期望 %q，实际 %q", row.GrantNo, want[row.GrantNo], row.PrincipalHint)
		}
	}
}

func TestCascadeDeleteLegacyGrantRowsScopedByLevel(t *testing.T) {
	db := setupDataGrantTestDB(t)
	const inst = "ds-cascade001"
	seedLegacy := func(schema, table string) {
		t.Helper()
		if err := db.Exec("INSERT INTO insight_das_schema_perms (principal_type, principal_id, instance_id, `schema`, resource_type) VALUES ('user', 1, ?, ?, 'database')", inst, schema).Error; err != nil {
			t.Fatalf("seed schema perm: %v", err)
		}
		if err := db.Exec("INSERT INTO insight_das_table_perms (principal_type, principal_id, instance_id, `schema`, \"table\", rule) VALUES ('user', 1, ?, ?, ?, 'allow')", inst, schema, table).Error; err != nil {
			t.Fatalf("seed table perm: %v", err)
		}
	}
	countAll := func() (int64, int64) {
		var s, tb int64
		db.Table("insight_das_schema_perms").Where("principal_id = 1").Count(&s)
		db.Table("insight_das_table_perms").Where("principal_id = 1").Count(&tb)
		return s, tb
	}

	// --- 表级撤销：只删这一张表，库授权和别的表都不动 ---
	seedLegacy("jaysql", "t_keep")
	seedLegacy("jaysql", "t_drop")
	if err := cascadeDeleteLegacyGrantRows(db, dataGrantTestRow{
		level: DataResourceLevelTable, instance: inst, schema: "jaysql", table: "t_drop",
	}.model()); err != nil {
		t.Fatalf("cascade table: %v", err)
	}
	var remainTables int64
	db.Table("insight_das_table_perms").Where("principal_id = 1 AND \"table\" = ?", "t_keep").Count(&remainTables)
	if remainTables != 1 {
		t.Fatal("表级撤销误删了同一库下的其他表授权")
	}
	var droppedTables int64
	db.Table("insight_das_table_perms").Where("principal_id = 1 AND \"table\" = ?", "t_drop").Count(&droppedTables)
	if droppedTables != 0 {
		t.Fatal("表级撤销未删除目标表的授权行")
	}
	if s, _ := countAll(); s != 2 {
		t.Fatalf("表级撤销不应影响库级授权，实际剩余 %d 条", s)
	}

	// --- 库级撤销：该库的 schema 行与该库下全部 table 行一起清掉 ---
	if err := cascadeDeleteLegacyGrantRows(db, dataGrantTestRow{
		level: DataResourceLevelDatabase, instance: inst, schema: "jaysql",
	}.model()); err != nil {
		t.Fatalf("cascade database: %v", err)
	}
	if s, tb := countAll(); s != 0 || tb != 0 {
		t.Fatalf("库级撤销清理不干净：schema_perms=%d table_perms=%d", s, tb)
	}

	// --- 实例级撤销：只删 schema='*' 的实例级行 ---
	seedLegacy(dasInstanceGrantSchema, "t_should_stay")
	if err := cascadeDeleteLegacyGrantRows(db, dataGrantTestRow{
		level: DataResourceLevelInstance, instance: inst,
	}.model()); err != nil {
		t.Fatalf("cascade instance: %v", err)
	}
	var instRows int64
	db.Table("insight_das_schema_perms").Where("principal_id = 1 AND `schema` = ?", dasInstanceGrantSchema).Count(&instRows)
	if instRows != 0 {
		t.Fatal("实例级撤销未删除 schema='*' 的旧表行")
	}
	if _, tb := countAll(); tb == 0 {
		t.Fatal("实例级撤销不应牵连库/表级旧表行")
	}
}

// dataGrantTestRow 构造级联删除所需的授权模型，避免每个用例都写一长串结构体字面量
type dataGrantTestRow struct {
	level    string
	instance string
	schema   string
	table    string
}

func (r dataGrantTestRow) model() *models.InsightDasDataGrant {
	return &models.InsightDasDataGrant{
		PrincipalType: "user",
		PrincipalID:   1,
		ResourceLevel: r.level,
		InstanceID:    r.instance,
		SchemaName:    r.schema,
		Table:         r.table,
	}
}
