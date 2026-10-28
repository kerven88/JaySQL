package tools

import (
	"testing"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// create_backup 是写工具，落库前的参数推导值得用真实（内存）库跑一遍：
// 实例定位、备份方式推导、范围校验、调度归一都在这个 Handler 里收口。
func setupBackupToolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:backup_tool_test?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	ddl := `DROP TABLE IF EXISTS insight_instances;
CREATE TABLE insight_instances (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  instance_id varchar(32) DEFAULT '',
  hostname varchar(128) DEFAULT '',
  port int DEFAULT 0,
  db_type varchar(32) DEFAULT '',
  environment int DEFAULT 0
);
DROP TABLE IF EXISTS insight_instance_environments;
CREATE TABLE insight_instance_environments (id INTEGER PRIMARY KEY AUTOINCREMENT, name varchar(64) DEFAULT '');`
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	seed := `INSERT INTO insight_instances (instance_id, hostname, port, db_type, environment) VALUES
	  ('inst-mysql01', '192.168.1.99', 3306, 'MySQL', 1),
	  ('inst-pg01', '192.168.1.99', 5432, 'PostgreSQL', 1),
	  ('inst-ora01', '192.168.1.99', 1521, 'Oracle', 1);
	INSERT INTO insight_instance_environments (id, name) VALUES (1, '生产');`
	if err := db.Exec(seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	prevDB, prevLog := global.App.DB, global.App.Log
	global.App.DB = db
	global.App.Log = logrus.New()
	t.Cleanup(func() {
		global.App.DB = prevDB
		global.App.Log = prevLog
	})
	return db
}

func runCreateBackup(t *testing.T, args map[string]any) (*DraftAction, error) {
	t.Helper()
	tool, ok := Get("create_backup")
	if !ok {
		t.Fatalf("create_backup 未注册")
	}
	value, err := tool.Handler(&Context{Username: "tester"}, args)
	if err != nil {
		return nil, err
	}
	draft, ok := value.(*DraftAction)
	if !ok {
		t.Fatalf("create_backup 未返回草稿：%#v", value)
	}
	return draft, nil
}

// 物理备份：未指定方式时按实例类型推导，范围为整实例，落库入口指向确认接口
func TestCreateBackupDraftPhysical(t *testing.T) {
	setupBackupToolTestDB(t)

	draft, err := runCreateBackup(t, map[string]any{"instance_id": "inst-mysql01"})
	if err != nil {
		t.Fatalf("生成草稿失败: %v", err)
	}
	if draft.Type != "create_backup" || draft.SubmitPath != "/api/v1/ai/backups/confirm" {
		t.Fatalf("草稿类型或确认入口不符：%#v", draft)
	}
	if draft.Prefill["driver_type"] != "XTRABACKUP" || draft.Prefill["object_mode"] != "ALL" {
		t.Fatalf("备份方式/范围推导不符：%v", draft.Prefill)
	}
	if draft.Prefill["instance_id"] != "inst-mysql01" {
		t.Fatalf("实例未定位到业务 ID：%v", draft.Prefill)
	}
	if days, _ := draft.Prefill["schedule_days"].([]string); len(days) != 7 {
		t.Fatalf("留空应表示每天，got %v", draft.Prefill["schedule_days"])
	}

	pgDraft, err := runCreateBackup(t, map[string]any{"instance_id": "inst-pg01"})
	if err != nil {
		t.Fatalf("生成 PG 草稿失败: %v", err)
	}
	if pgDraft.Prefill["driver_type"] != "PG_BACKREST" {
		t.Fatalf("PostgreSQL 应推导 pgBackRest，got %v", pgDraft.Prefill["driver_type"])
	}
}

// 给了库名 → 逻辑备份，范围 CUSTOM，调度星期归一成 MON..SUN
func TestCreateBackupDraftLogical(t *testing.T) {
	setupBackupToolTestDB(t)

	draft, err := runCreateBackup(t, map[string]any{
		"host":           "192.168.1.99",
		"port":           3306,
		"object_mode":    "CUSTOM",
		"databases":      []any{"order", "user"},
		"schedule_days":  []any{"周二", "SUNDAY", "周二"},
		"schedule_time":  "02:30",
		"retention_days": 30,
	})
	if err != nil {
		t.Fatalf("生成草稿失败: %v", err)
	}
	if draft.Prefill["driver_type"] != "LOGICAL" || draft.Prefill["object_mode"] != "CUSTOM" {
		t.Fatalf("给了库名应走逻辑备份：%v", draft.Prefill)
	}
	if dbs, _ := draft.Prefill["databases"].([]string); len(dbs) != 2 || dbs[0] != "order" {
		t.Fatalf("库名列表不符：%v", draft.Prefill["databases"])
	}
	if days, _ := draft.Prefill["schedule_days"].([]string); len(days) != 2 || days[0] != "TUE" || days[1] != "SUN" {
		t.Fatalf("调度星期归一/去重不符：%v", days)
	}
	if draft.Prefill["retention_days"] != 30 {
		t.Fatalf("保留天数不符：%v", draft.Prefill["retention_days"])
	}
}

// 异常场景：实例未登记、非 MySQL/PG 实例、物理备份指定库、非法调度
func TestCreateBackupDraftRejects(t *testing.T) {
	setupBackupToolTestDB(t)

	cases := []struct {
		name string
		args map[string]any
	}{
		{"实例未登记", map[string]any{"instance_id": "inst-none"}},
		{"不支持的数据库类型", map[string]any{"instance_id": "inst-ora01"}},
		{"物理备份指定库", map[string]any{"instance_id": "inst-mysql01", "driver_type": "XTRABACKUP", "object_mode": "CUSTOM", "databases": []any{"order"}}},
		{"CUSTOM 未给库名", map[string]any{"instance_id": "inst-mysql01", "object_mode": "CUSTOM"}},
		{"非法调度星期", map[string]any{"instance_id": "inst-mysql01", "schedule_days": []any{"周八"}}},
		{"非法调度时间", map[string]any{"instance_id": "inst-mysql01", "schedule_time": "25:00"}},
		{"非法备份方式", map[string]any{"instance_id": "inst-mysql01", "driver_type": "MYSQLDUMP"}},
		{"缺实例参数", map[string]any{"name": "无实例的备份"}},
	}
	for _, item := range cases {
		if _, err := runCreateBackup(t, item.args); err == nil {
			t.Fatalf("%s 应报错，实际通过：%v", item.name, item.args)
		}
	}
}
