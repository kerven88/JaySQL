package services

import (
	"testing"

	usersModels "github.com/simajiong/jaysql/internal/users/models"
)

// ============================================================================
// 系统库可见性收口的回归测试
//
// 覆盖四条硬约束：
//   1. 系统库清单覆盖平台支持的全部数据库类型，且按引擎各自的口径判定；
//   2. 全实例授权 + 非 admin  => 系统库不可见；
//   3. 具备 admin 角色        => 系统库仍可见；
//   4. 显式授权单个库          => 不受影响（哪怕是系统库，也要按授权人的点名放行）。
// ============================================================================

func TestIsSystemSchemaAcrossEngines(t *testing.T) {
	cases := []struct {
		name   string
		dbType string
		schema string
		want   bool
	}{
		// MySQL 系
		{"mysql-sys", "MySQL", "sys", true},
		{"mysql-information-schema", "MySQL", "information_schema", true},
		{"mysql-performance-schema", "MySQL", "performance_schema", true},
		{"mysql-mysql", "MySQL", "mysql", true},
		{"mysql-business-db", "MySQL", "appdb", false},
		{"mysql-test-db", "MySQL", "test", false},
		{"mysql-uppercase-business-db", "MySQL", "ORDER_DB", false},
		{"tidb-metrics-schema", "TiDB", "metrics_schema", true},
		{"tidb-business-db", "TiDB", "appdb", false},
		{"doris-information-schema", "Doris", "information_schema", true},
		// PostgreSQL 系
		{"pg-pg-catalog", "PostgreSQL", "pg_catalog", true},
		{"pg-information-schema", "PostgreSQL", "information_schema", true},
		{"pg-template0", "PostgreSQL", "template0", true},
		{"pg-toast-prefix", "PostgreSQL", "pg_toast_temp_1", true},
		{"pg-public", "PostgreSQL", "public", false},
		{"pg-business-db", "PostgreSQL", "appdb", false},
		// Oracle 系（Oracle 的对象名全大写，判定必须大小写不敏感）
		{"oracle-sys", "Oracle", "SYS", true},
		{"oracle-system", "Oracle", "SYSTEM", true},
		{"oracle-sysbackup-prefix", "Oracle", "SYSBACKUP", true},
		{"oracle-wmsys-suffix", "Oracle", "WMSYS", true},
		{"oracle-mdsys-suffix", "Oracle", "MDSYS", true},
		{"oracle-business-user", "Oracle", "APPUSER", false},
		{"oracle-business-user-2", "Oracle", "JAYSQL", false},
		// Oracle：1.99 环境实测同步进来的内置账号，逐个钉死
		{"oracle-anonymous", "Oracle", "ANONYMOUS", true},
		{"oracle-sysdollar-umf", "Oracle", "SYS$UMF", true},
		{"oracle-dbsfwuser", "Oracle", "DBSFWUSER", true},
		{"oracle-oracle-ocm", "Oracle", "ORACLE_OCM", true},
		{"oracle-si-informtn-schema", "Oracle", "SI_INFORMTN_SCHEMA", true},
		{"oracle-xs-null", "Oracle", "XS$NULL", true},
		{"oracle-dba1-stays", "Oracle", "DBA1", false},
		{"oracle-hr-stays", "Oracle", "HR", false},
		{"oracle-pdbadmin-stays", "Oracle", "PDBADMIN", false},
		// SQLServer
		{"sqlserver-master", "SQLServer", "master", true},
		{"sqlserver-tempdb", "SQLServer", "tempdb", true},
		{"sqlserver-sys", "SQLServer", "sys", true},
		{"sqlserver-dbo", "SQLServer", "dbo", false},
		// ClickHouse
		{"clickhouse-system", "ClickHouse", "system", true},
		{"clickhouse-default", "ClickHouse", "default", false},
		// MongoDB
		{"mongodb-admin", "MongoDB", "admin", true},
		{"mongodb-local", "MongoDB", "local", true},
		{"mongodb-config", "MongoDB", "config", true},
		{"mongodb-business-db", "MongoDB", "appdb", false},
		// OceanBase：MySQL / Oracle 两种租户模式的系统库都要盖住
		{"ob-oceanbase", "OceanBase", "oceanbase", true},
		{"ob-mysql-mode-sys", "OceanBase", "sys", true},
		{"ob-oracle-mode-system", "OceanBase", "SYSTEM", true},
		{"ob-mysql", "OceanBase", "mysql", true},
		{"ob-business-db", "OceanBase", "appdb", false},
		// 未知引擎：不做任何隐藏，宁可不收敛也绝不误伤业务库
		{"unknown-engine", "SomeNewEngine", "sys", false},
		// 空名不参与判定
		{"empty-name", "MySQL", "", false},
		{"blank-name", "MySQL", "   ", false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := IsSystemSchema(item.dbType, item.schema); got != item.want {
				t.Fatalf("IsSystemSchema(%q, %q) = %v, want %v", item.dbType, item.schema, got, item.want)
			}
		})
	}
}

// PostgreSQL / SQLServer 的库与 schema 是父子关系，资源路径带点号，
// 系统库判定必须只看最后一段，否则 `appdb.pg_catalog` 会被当成业务库放过去。
func TestIsSystemSchemaPathForHierarchicalEngines(t *testing.T) {
	cases := []struct {
		dbType string
		path   string
		want   bool
	}{
		{"PostgreSQL", "appdb", false},
		{"PostgreSQL", "appdb.public", false},
		{"PostgreSQL", "appdb.pg_catalog", true},
		{"PostgreSQL", "appdb.information_schema", true},
		{"SQLServer", "appdb.dbo", false},
		{"SQLServer", "appdb.sys", true},
		{"MySQL", "appdb", false},
		{"MySQL", "sys", true},
		{"Oracle", "SYS", true},
	}
	for _, item := range cases {
		if got := IsSystemSchemaPath(item.dbType, item.path); got != item.want {
			t.Fatalf("IsSystemSchemaPath(%q, %q) = %v, want %v", item.dbType, item.path, got, item.want)
		}
	}
}

// 全实例授权 + 非 admin：系统库从可见列表里消失，业务库不受影响。
func TestSystemSchemaGateHidesSystemSchemasForInstanceGrant(t *testing.T) {
	db := setupDataGrantTestDB(t)
	instanceID := "ds-target000001"
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: instanceID})

	gate := newSystemSchemaGate(testScope())
	if gate == nil {
		t.Fatal("非 admin 主体应当拿到非空的判定上下文")
	}
	if !gate.hidden(instanceID, "MySQL", "sys") {
		t.Error("全实例授权 + 非 admin：MySQL sys 应当被隐藏")
	}
	if !gate.hidden(instanceID, "MySQL", "performance_schema") {
		t.Error("全实例授权 + 非 admin：performance_schema 应当被隐藏")
	}
	if gate.hidden(instanceID, "MySQL", "appdb") {
		t.Error("业务库不应被隐藏")
	}
	// 没有全实例授权的实例：可见性来自显式授权，本规则不介入
	if gate.hidden("ds-other0000001", "MySQL", "sys") {
		t.Error("未持有全实例授权的实例不应触发系统库隐藏")
	}
}

// 显式点名授权的库不受影响——哪怕是系统库，授权人点了名就按点的算。
func TestSystemSchemaGateKeepsExplicitGrantedSchema(t *testing.T) {
	db := setupDataGrantTestDB(t)
	instanceID := "ds-target000001"
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: instanceID})
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: instanceID, Schema: "sys"})

	gate := newSystemSchemaGate(testScope())
	if gate.hidden(instanceID, "MySQL", "sys") {
		t.Error("显式授权 sys 后，sys 不应被隐藏")
	}
	if !gate.hidden(instanceID, "MySQL", "performance_schema") {
		t.Error("未显式授权的系统库仍应被隐藏")
	}
}

// 只有库级授权：gate 里没有全实例授权实例，任何库都不隐藏。
func TestSystemSchemaGateNoInstanceGrantKeepsEverything(t *testing.T) {
	db := setupDataGrantTestDB(t)
	instanceID := "ds-target000001"
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: instanceID, Schema: "appdb"})

	gate := newSystemSchemaGate(testScope())
	for _, schema := range []string{"sys", "mysql", "performance_schema", "appdb"} {
		if gate.hidden(instanceID, "MySQL", schema) {
			t.Errorf("仅库级授权时 %s 不应被隐藏", schema)
		}
	}
}

// admin 角色：拿到的 gate 为 nil，等价于「不做任何系统库收敛」。
func TestSystemSchemaGateAdminKeepsSystemSchemas(t *testing.T) {
	db := setupDataGrantTestDB(t)
	instanceID := "ds-target000001"
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: instanceID})

	scope := testScope()
	scope.HasAdminRole = true
	gate := newSystemSchemaGate(scope)
	if gate != nil {
		t.Fatal("admin 主体不应构造系统库判定上下文")
	}
	if gate.hidden(instanceID, "MySQL", "sys") {
		t.Error("admin 应当仍能看到系统库")
	}
}

// filterSchemaPaths 在 gate 为 nil 时必须原样返回，避免 admin 走一次无谓的拷贝。
func TestFilterSchemaPathsWithoutGate(t *testing.T) {
	setupDataGrantTestDB(t)
	paths := []string{"appdb", "sys"}
	var gate *systemSchemaGate
	got := gate.filterSchemaPaths("ds-target000001", "MySQL", paths)
	if len(got) != 2 {
		t.Fatalf("gate 为空时应当原样返回，got %v", got)
	}
}

func TestFilterSchemaPathsDropsSystemSchemas(t *testing.T) {
	db := setupDataGrantTestDB(t)
	instanceID := "ds-target000001"
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: instanceID})

	gate := newSystemSchemaGate(testScope())
	got := gate.filterSchemaPaths(instanceID, "MySQL", []string{"appdb", "sys", "mysql", "order_db"})
	if len(got) != 2 || got[0] != "appdb" || got[1] != "order_db" {
		t.Fatalf("期望只保留业务库，got %v", got)
	}
}

// hasBuiltinAdminRole 只认 code='admin'，且查询失败时 fail-closed 返回 false。
func TestHasBuiltinAdminRole(t *testing.T) {
	db := setupDataGrantTestDB(t)
	if err := db.Exec(`
		DROP TABLE IF EXISTS insight_user_roles;
		CREATE TABLE insight_user_roles (
			uid     bigint DEFAULT 0,
			role_id bigint DEFAULT 0
		);
	`).Error; err != nil {
		t.Fatalf("create insight_user_roles: %v", err)
	}
	if err := db.Exec(`INSERT INTO insight_roles (id, code, name) VALUES (11, 'member', '普通成员'), (12, 'admin', '管理员')`).Error; err != nil {
		t.Fatalf("seed roles: %v", err)
	}

	cases := []struct {
		name string
		ids  []uint64
		want bool
	}{
		{"empty", nil, false},
		{"member-only", []uint64{11}, false},
		{"admin", []uint64{12}, true},
		{"mixed", []uint64{11, 12}, true},
		{"unknown-role", []uint64{99}, false},
		{"zero-id", []uint64{0}, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			roles := make([]usersModels.InsightUserRoles, 0, len(item.ids))
			for _, id := range item.ids {
				roles = append(roles, usersModels.InsightUserRoles{RoleID: id})
			}
			if got := hasBuiltinAdminRole(roles); got != item.want {
				t.Fatalf("hasBuiltinAdminRole(%v) = %v, want %v", item.ids, got, item.want)
			}
		})
	}
}

// visibleSchemasForInstanceGrant：全实例授权下把系统库从「全部放行」里扣掉。
func TestVisibleSchemasForInstanceGrant(t *testing.T) {
	db := setupDataGrantTestDB(t)
	instanceID := "ds-target000001"
	if err := db.Exec(`
		DROP TABLE IF EXISTS insight_instances;
		CREATE TABLE insight_instances (instance_id varchar(15) DEFAULT '', db_type varchar(32) DEFAULT '');
		DROP TABLE IF EXISTS insight_instance_schemas;
		CREATE TABLE insight_instance_schemas (
			instance_id varchar(15) DEFAULT '',
			"schema"    varchar(255) DEFAULT '',
			is_deleted  boolean DEFAULT false
		);
	`).Error; err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if err := db.Exec(`INSERT INTO insight_instances (instance_id, db_type) VALUES (?, ?)`, instanceID, "MySQL").Error; err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	for _, schema := range []string{"appdb", "order_db", "sys", "mysql"} {
		if err := db.Exec(`INSERT INTO insight_instance_schemas (instance_id, "schema", is_deleted) VALUES (?, ?, 0)`, instanceID, schema).Error; err != nil {
			t.Fatalf("seed schema: %v", err)
		}
	}
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: instanceID})

	got, err := visibleSchemasForInstanceGrant(testScope(), instanceID)
	if err != nil {
		t.Fatalf("visibleSchemasForInstanceGrant: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("期望只保留 2 个业务库，got %v", got)
	}
	for _, schema := range got {
		if IsSystemSchema("MySQL", schema) {
			t.Errorf("系统库 %s 不应出现在可见清单里", schema)
		}
	}
}
