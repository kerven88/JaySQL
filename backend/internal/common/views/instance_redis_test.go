package views

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestRedisPostWriteDoesNotSyncSQLMetadata(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE insight_instances (id INTEGER PRIMARY KEY, last_check_status TEXT, last_check_message TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO insight_instances (id,last_check_status,last_check_message) VALUES (1,'success','old')")
	oldDB := global.App.DB
	global.App.DB = db
	t.Cleanup(func() { global.App.DB = oldDB })
	row := commonModels.InsightInstances{Model: &commonModels.Model{ID: 1}, DbType: "Redis"}
	finalizeInstancePostWrite(&row, "实例创建成功，正在后台同步元数据", true)
	var result struct{ LastCheckStatus, LastCheckMessage string }
	if err := db.Table("insight_instances").First(&result).Error; err != nil {
		t.Fatal(err)
	}
	if result.LastCheckStatus != "unknown" || strings.Contains(result.LastCheckMessage, "元数据") {
		t.Fatalf("%+v", result)
	}
}
