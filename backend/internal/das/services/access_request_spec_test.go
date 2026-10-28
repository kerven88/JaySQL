package services

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/das/forms"
)

// ============================================================================
// 多资源圈选（多库 / 多模式 / 表维度四种方式）的回归测试
//
// 口径锚点：
//   1. spec 构造：层级与圈选方式必须自洽（库级不带表、表级必须有容器、路径段数匹配）；
//   2. 通配符：* / ? 语义正确、字面字符不越权（order_1 不匹配 order_12）；
//   3. except：命中排除项不放行，清单外的表放行；
//   4. 展开：一条申请单展开成多条授权行；
//   5. 判定：wildcard / except 授权行经 matchDataGrants 取出 + Go 侧复核后生效。
// ============================================================================

// specFormStub 按字段直填，避免在单测里组装完整 forms.AccessRequestCreateForm
type specFormStub struct {
	DatabaseName  string
	SchemaName    string
	TableName     string
	DatabaseNames []string
	SchemaNames   []string
	TableMode     string
	TableNames    []string
	TablePattern  string
	ExceptTables  []string
}

func stubBuildSpec(level string, f *specFormStub, hasSchema bool) (*accessResourceSpec, error) {
	form := &forms.AccessRequestCreateForm{
		DatabaseName:  f.DatabaseName,
		SchemaName:    f.SchemaName,
		TableName:     f.TableName,
		DatabaseNames: f.DatabaseNames,
		SchemaNames:   f.SchemaNames,
		TableMode:     f.TableMode,
		TableNames:    f.TableNames,
		TablePattern:  f.TablePattern,
		ExceptTables:  f.ExceptTables,
	}
	return buildAccessResourceSpec(level, form, hasSchema)
}

func TestWildcardMatchTable(t *testing.T) {
	cases := []struct {
		pattern, table string
		want           bool
	}{
		{"order_*", "order_1", true},
		{"order_*", "order_", true},
		{"order_*", "orders_1", false},
		{"t?x", "t1x", true},
		{"t?x", "t12x", false},
		{"*", "anything", true},
		{"user_log_2026*", "user_log_2026_10", true},
		{"order_*", "refund_1", false},
		{"", "anything", false},
	}
	for _, tc := range cases {
		if got := wildcardMatchTable(tc.pattern, tc.table); got != tc.want {
			t.Fatalf("wildcardMatchTable(%q, %q) = %v, want %v", tc.pattern, tc.table, got, tc.want)
		}
	}
}

func TestBuildSpecDatabaseLevelMulti(t *testing.T) {
	spec, err := stubBuildSpec("database", &specFormStub{
		DatabaseNames: []string{"db2", "db1", "db1", ""},
	}, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(spec.Databases) != 2 || spec.Databases[0] != "db1" || spec.Databases[1] != "db2" {
		t.Fatalf("应去重并排序为 [db1 db2], got %v", spec.Databases)
	}
	if len(spec.Schemas) != 0 {
		t.Fatalf("库级不应有 Schemas: %v", spec.Schemas)
	}
}

func TestBuildSpecRejectsCrossLevelFields(t *testing.T) {
	// 库级携带表 → 拒绝
	if _, err := stubBuildSpec("database", &specFormStub{
		DatabaseNames: []string{"db1"},
		TableMode:     "list",
		TableNames:    []string{"db1.t1"},
	}, false); err == nil || !strings.Contains(err.Error(), "不能携带") {
		t.Fatalf("库级携带表应被拒, got %v", err)
	}
	// 模式级未带「库.模式」两段路径 → 拒绝
	if _, err := stubBuildSpec("schema", &specFormStub{SchemaNames: []string{"public"}}, true); err == nil || !strings.Contains(err.Error(), "路径不合法") {
		t.Fatalf("模式级单段路径应被拒, got %v", err)
	}
	// 单层结构申请模式级 → 拒绝
	if _, err := stubBuildSpec("schema", &specFormStub{SchemaNames: []string{"db1.public"}}, false); err == nil || !strings.Contains(err.Error(), "没有独立的「模式」层级") {
		t.Fatalf("单层结构模式级应被拒, got %v", err)
	}
	// 表级 wildcard 缺模式 → 拒绝
	if _, err := stubBuildSpec("table", &specFormStub{TableMode: "wildcard", TablePattern: "t_*"}, true); err == nil {
		t.Fatal("wildcard 缺模式应被拒")
	}
	// 表级 except 缺排除清单 → 拒绝
	if _, err := stubBuildSpec("table", &specFormStub{
		TableMode:   "except",
		SchemaNames: []string{"db1.public"},
	}, true); err == nil || !strings.Contains(err.Error(), "排除") {
		t.Fatalf("except 缺排除清单应被拒, got %v", err)
	}
	// 旧单资源兜底：只传 DatabaseName 也能构造出库级 spec
	spec, err := stubBuildSpec("database", &specFormStub{DatabaseName: "legacy_db"}, false)
	if err != nil || len(spec.Databases) != 1 || spec.Databases[0] != "legacy_db" {
		t.Fatalf("旧字段兜底失败: %v %v", spec, err)
	}
}

func TestBuildSpecTableListThreeLayer(t *testing.T) {
	spec, err := stubBuildSpec("table", &specFormStub{
		TableMode:   "list",
		SchemaNames: []string{"db1.public", "db2.app", "db2.public"},
		TableNames:  []string{"db1.public.orders", "db1.public.users", "db2.app.logs", "db2.public.bad"},
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(spec.Schemas) != 3 {
		t.Fatalf("应为 3 个容器, got %v", spec.Schemas)
	}
	res := spec.expandGrantResources()
	if len(res) != 4 {
		t.Fatalf("应展开成 4 条授权行, got %d: %+v", len(res), res)
	}
	for _, r := range res {
		if r.Level != DataResourceLevelTable || r.TableMode != "exact" {
			t.Fatalf("list 展开应为 table/exact: %+v", r)
		}
	}
	// 表不在所选容器内 → 拒绝
	if _, err := stubBuildSpec("table", &specFormStub{
		TableMode:   "list",
		SchemaNames: []string{"db1.public"},
		TableNames:  []string{"db2.app.logs"},
	}, true); err == nil || !strings.Contains(err.Error(), "不在所选") {
		t.Fatalf("表越容器应被拒, got %v", err)
	}
	// 单层结构（MySQL）：容器是库名，表路径两段
	spec2, err := stubBuildSpec("table", &specFormStub{
		TableMode:     "list",
		DatabaseNames: []string{"jaysql"},
		TableNames:    []string{"jaysql.users"},
	}, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(spec2.Tables) != 1 || spec2.Schemas[0] != "jaysql" {
		t.Fatalf("单层结构 list 不正确: %+v", spec2)
	}
}

func TestBuildSpecTableWildcardAndExceptExpand(t *testing.T) {
	spec, err := stubBuildSpec("table", &specFormStub{
		TableMode:    "wildcard",
		TablePattern: "order_*",
		SchemaNames:  []string{"db1.public", "db1.app"},
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	res := spec.expandGrantResources()
	if len(res) != 2 {
		t.Fatalf("wildcard 应每模式一条, got %d", len(res))
	}
	for _, r := range res {
		if r.TableMode != "wildcard" || r.Table != "order_*" {
			t.Fatalf("wildcard 行不正确: %+v", r)
		}
	}

	spec2, err := stubBuildSpec("table", &specFormStub{
		TableMode:    "except",
		SchemaNames:  []string{"db1.public"},
		ExceptTables: []string{"db1.public.secret", "db1.public.tmp_log"},
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	res2 := spec2.expandGrantResources()
	if len(res2) != 1 {
		t.Fatalf("except 应每模式一条, got %d", len(res2))
	}
	if res2[0].TableMode != "except" || res2[0].Table != "secret,tmp_log" {
		t.Fatalf("except 行存储值不正确: %+v", res2[0])
	}
}

func TestMatchTableByGrantRowModes(t *testing.T) {
	if !matchTableByGrantRow("", "orders", "orders") {
		t.Fatal("空 mode 按 exact 处理")
	}
	if matchTableByGrantRow("exact", "orders", "orders2") {
		t.Fatal("exact 不能前缀匹配")
	}
	if !matchTableByGrantRow("wildcard", "order_*", "order_2026") {
		t.Fatal("wildcard 应命中")
	}
	if matchTableByGrantRow("wildcard", "order_*", "refund_1") {
		t.Fatal("wildcard 不应命中无关表")
	}
	if !matchTableByGrantRow("except", "secret,tmp_log", "orders") {
		t.Fatal("except 清单外的表应放行")
	}
	if matchTableByGrantRow("except", "secret,tmp_log", "secret") {
		t.Fatal("except 清单内的表必须拦下")
	}
}

// 判定端到端：wildcard / except 授权行经 matchDataGrants 取出 + Go 侧复核后生效
func TestHasDataGrantWildcardAndExceptRows(t *testing.T) {
	db := setupDataGrantTestDB(t)
	// 两个实例分开种：except 的语义是「除清单外全部表」，会和 wildcard 的负例互相污染
	const instW = "ds-wildcard00001"
	const instE = "ds-except0000001"
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelTable, InstanceID: instW, Schema: "appdb",
		Table: "order_*", TableMode: AccessTableModeWildcard})
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelTable, InstanceID: instE, Schema: "appdb",
		Table: "secret,tmp_log", TableMode: AccessTableModeExcept})

	cases := []struct {
		name     string
		instance string
		table    string
		want     bool
	}{
		{"通配符命中", instW, "order_2026", true},
		{"通配符不越权", instW, "refund_1", false},
		{"except 清单外放行", instE, "users", true},
		{"except 清单内拦截", instE, "secret", false},
	}
	for _, tc := range cases {
		ok, err := HasDataGrant(testScope(), tc.instance, "appdb", tc.table, DataActionSelect)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ok != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, ok, tc.want)
		}
	}
}

// ============================================================================
// 授权清单（entries，v2）：一单混搭「库/模式对象 + 各自的表圈选方式」
// ============================================================================

func stubEntry(kind, database, schema, tableMode string, tables []string, pattern string, except []string) forms.AccessRequestEntryForm {
	return forms.AccessRequestEntryForm{
		Kind: kind, Database: database, Schema: schema,
		TableMode: tableMode, Tables: tables, Pattern: pattern, Except: except,
	}
}

func TestBuildEntriesMixedKinds(t *testing.T) {
	// 三层结构：db1 整库 + db2.public 指定表 + db3.app 通配
	entries, err := buildAccessEntries([]forms.AccessRequestEntryForm{
		stubEntry("database", "db1", "", "", nil, "", nil),
		stubEntry("schema", "db2", "db2.public", "list", []string{"orders", "users"}, "", nil),
		stubEntry("schema", "db3", "db3.app", "wildcard", nil, "log_*", nil),
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("应 3 条条目, got %d", len(entries))
	}
	// 条目按 entryKey 排序，保证 resource_spec 归一化
	if entries[0].Database != "db1" || entries[1].Schema != "db2.public" || entries[2].Schema != "db3.app" {
		t.Fatalf("条目顺序不正确: %+v", entries)
	}
	if lv := derivedAccessResourceLevel(entries); lv != DataResourceLevelTable {
		t.Fatalf("混搭清单应派生为 table 级, got %s", lv)
	}

	// 展开：整库 1 条 database 级 + list 2 条 + wildcard 1 条 = 4 条授权行
	res := entriesExpandGrantResources(entries)
	if len(res) != 4 {
		t.Fatalf("应展开 4 条授权行, got %d: %+v", len(res), res)
	}
	if res[0].Level != DataResourceLevelDatabase || res[0].Schema != "db1" {
		t.Fatalf("整库条目应展开为 database 级: %+v", res[0])
	}
	if res[1].Level != DataResourceLevelTable || res[1].Schema != "db2.public" || res[1].Table != "orders" {
		t.Fatalf("list 条目展开不正确: %+v", res[1])
	}
	if res[3].TableMode != AccessTableModeWildcard || res[3].Table != "log_*" {
		t.Fatalf("wildcard 条目展开不正确: %+v", res[3])
	}

	// 旧三列取第一条：库条目 → schema_name=库名
	database, schemaName, tableName := entriesLegacyColumns(entries)
	if database != "db1" || schemaName != "db1" || tableName != "" {
		t.Fatalf("旧三列不正确: %s / %s / %s", database, schemaName, tableName)
	}

	// 摘要要把每条都说清
	summary := entriesResourceSummary(entries)
	if !strings.Contains(summary, "库 db1") || !strings.Contains(summary, "db2.public 指定表 orders、users") ||
		!strings.Contains(summary, "db3.app 通配表 log_*") {
		t.Fatalf("摘要不完整: %s", summary)
	}
}

func TestBuildEntriesSingleLayerAndSchemaLevel(t *testing.T) {
	// 单层结构（MySQL）：库条目可携带表圈选
	entries, err := buildAccessEntries([]forms.AccessRequestEntryForm{
		stubEntry("database", "jaysql", "", "except", nil, "", []string{"secret", "tmp_log"}),
		stubEntry("database", "otherdb", "", "", nil, "", nil),
	}, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if lv := derivedAccessResourceLevel(entries); lv != DataResourceLevelTable {
		t.Fatalf("单层带表圈选应派生 table 级, got %s", lv)
	}
	res := entriesExpandGrantResources(entries)
	if len(res) != 2 {
		t.Fatalf("应展开 2 条, got %d", len(res))
	}
	if res[0].TableMode != AccessTableModeExcept || res[0].Table != "secret,tmp_log" || res[0].Schema != "jaysql" {
		t.Fatalf("单层 except 条目展开不正确: %+v", res[0])
	}
	if res[1].Level != DataResourceLevelDatabase {
		t.Fatalf("整库条目应展开为 database 级: %+v", res[1])
	}

	// 三层结构：模式条目 mode 为空 → schema 级；派生 schema 级
	entries2, err := buildAccessEntries([]forms.AccessRequestEntryForm{
		stubEntry("schema", "db1", "db1.public", "", nil, "", nil),
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if lv := derivedAccessResourceLevel(entries2); lv != DataResourceLevelSchema {
		t.Fatalf("纯模式清单应派生 schema 级, got %s", lv)
	}
	if res2 := entriesExpandGrantResources(entries2); len(res2) != 1 || res2[0].Level != DataResourceLevelSchema {
		t.Fatalf("模式条目应展开 schema 级: %+v", res2)
	}
}

func TestBuildEntriesRejects(t *testing.T) {
	cases := []struct {
		name      string
		entries   []forms.AccessRequestEntryForm
		hasSchema bool
		want      string
	}{
		{"空清单", nil, false, "不能为空"},
		{"缺库名", []forms.AccessRequestEntryForm{stubEntry("database", "", "", "", nil, "", nil)}, false, "缺少库名"},
		{"库名带点号", []forms.AccessRequestEntryForm{stubEntry("database", "db.public", "", "", nil, "", nil)}, false, "点号"},
		{"三层整库带表圈选", []forms.AccessRequestEntryForm{stubEntry("database", "db1", "", "list", []string{"t1"}, "", nil)}, true, "不能携带表圈选"},
		{"单层结构加模式条目", []forms.AccessRequestEntryForm{stubEntry("schema", "db1", "db1.public", "", nil, "", nil)}, false, "没有独立的「模式」层级"},
		{"模式路径与库不一致", []forms.AccessRequestEntryForm{stubEntry("schema", "db2", "db1.public", "", nil, "", nil)}, true, "不一致"},
		{"模式路径单段", []forms.AccessRequestEntryForm{stubEntry("schema", "db1", "public", "", nil, "", nil)}, true, "路径不合法"},
		{"指定表为空", []forms.AccessRequestEntryForm{stubEntry("schema", "db1", "db1.public", "list", nil, "", nil)}, true, "至少选择一张表"},
		{"表名带前缀", []forms.AccessRequestEntryForm{stubEntry("schema", "db1", "db1.public", "list", []string{"db1.public.t1"}, "", nil)}, true, "前缀"},
		{"通配符为空", []forms.AccessRequestEntryForm{stubEntry("schema", "db1", "db1.public", "wildcard", nil, "", nil)}, true, "通配符"},
		{"排除表为空", []forms.AccessRequestEntryForm{stubEntry("schema", "db1", "db1.public", "except", nil, "", nil)}, true, "排除"},
		{"整对象残留表", []forms.AccessRequestEntryForm{stubEntry("database", "db1", "", "", []string{"t1"}, "", nil)}, false, "不能携带表圈选内容"},
	}
	for _, tc := range cases {
		if _, err := buildAccessEntries(tc.entries, tc.hasSchema); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want 含 %q", tc.name, err, tc.want)
		}
	}

	// 同对象重复加入只保留一条
	entries, err := buildAccessEntries([]forms.AccessRequestEntryForm{
		stubEntry("database", "db1", "", "", nil, "", nil),
		stubEntry("database", "db1", "", "list", []string{"t1"}, "", nil),
	}, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("同对象重复应去重, got %d", len(entries))
	}
}

func TestParseAccessEntriesRoundTrip(t *testing.T) {
	entries, err := buildAccessEntries([]forms.AccessRequestEntryForm{
		stubEntry("database", "db1", "", "", nil, "", nil),
		stubEntry("schema", "db2", "db2.public", "list", []string{"orders"}, "", nil),
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	raw, err := entriesCanonicalJSON(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, ok, err := parseAccessEntries(raw)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if len(got) != 2 || got[0].Database != "db1" || got[1].Schema != "db2.public" {
		t.Fatalf("round trip 不一致: %+v", got)
	}

	// v1 格式与空串都应回退旧解析
	if _, ok, err := parseAccessEntries(`{"databases":["db1"]}`); ok || err != nil {
		t.Fatalf("v1 格式应回退, ok=%v err=%v", ok, err)
	}
	if _, ok, _ := parseAccessEntries(""); ok {
		t.Fatal("空串应回退")
	}
}
