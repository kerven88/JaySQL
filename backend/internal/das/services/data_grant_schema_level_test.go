package services

import "testing"

// ============================================================================
// 模式级授权（PostgreSQL / SQL Server 的「库 → 模式 → 表」三层结构）
//
// 三条口径必须同时成立，缺一条就会表现成「授了用不了」或「越权」：
//   1. 库级授权（schema_name=库名）覆盖该库下所有模式 —— 否则授了整个库却查不了表；
//   2. 模式级授权（schema_name=库.模式）只覆盖本模式，不越到同库其他模式；
//   3. 单层结构（MySQL）的库名不含点，库级授权的精确匹配不能退化成前缀匹配。
// ============================================================================

func TestHasDataGrantSchemaLevelCoverage(t *testing.T) {
	db := setupDataGrantTestDB(t)
	const inst = "ds-pgschema0001"

	// 库级授权：schema_name 只写库名
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: inst, Schema: "appdb"})

	cases := []struct {
		name   string
		schema string
		table  string
		want   bool
	}{
		{"库级授权覆盖库下任意模式（库级判定）", "appdb.public", "", true},
		{"库级授权覆盖库下任意模式的表", "appdb.public", "orders", true},
		{"库级授权覆盖库本身", "appdb", "", true},
		{"库级授权不越到别的库", "otherdb.public", "", false},
	}
	for _, tc := range cases {
		ok, err := HasDataGrant(testScope(), inst, tc.schema, tc.table, DataActionSelect)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ok != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, ok, tc.want)
		}
	}
}

func TestHasDataGrantSchemaLevelIsolation(t *testing.T) {
	db := setupDataGrantTestDB(t)
	const inst = "ds-pgschema0002"

	// 模式级授权：只给 appdb.public
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelSchema, InstanceID: inst, Schema: "appdb.public"})

	if ok, _ := HasDataGrant(testScope(), inst, "appdb.public", "", DataActionSelect); !ok {
		t.Fatal("模式级授权应覆盖本模式")
	}
	if ok, _ := HasDataGrant(testScope(), inst, "appdb.public", "orders", DataActionSelect); !ok {
		t.Fatal("模式级授权应覆盖模式内的表")
	}
	if ok, _ := HasDataGrant(testScope(), inst, "appdb.audit", "", DataActionSelect); ok {
		t.Fatal("模式级授权不能越到同库的其他模式")
	}
	if ok, _ := HasDataGrant(testScope(), inst, "appdb", "", DataActionSelect); ok {
		t.Fatal("模式级授权不能当成库级授权用")
	}
}

// 单层结构（MySQL）回归：库级授权只覆盖同名库，不能被前缀规则带偏
func TestHasDataGrantDatabaseLevelStaysExactForSingleLayer(t *testing.T) {
	db := setupDataGrantTestDB(t)
	const inst = "ds-mysql00000001"

	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: inst, Schema: "appdb"})

	if ok, _ := HasDataGrant(testScope(), inst, "appdb", "", DataActionSelect); !ok {
		t.Fatal("库级授权应覆盖同名库")
	}
	if ok, _ := HasDataGrant(testScope(), inst, "appdb2", "", DataActionSelect); ok {
		t.Fatal("库级授权不能覆盖名字相近的另一个库")
	}
	if ok, _ := HasDataGrant(testScope(), inst, "appdb", "orders", DataActionSelect); !ok {
		t.Fatal("库级授权应覆盖库内的表")
	}
}
