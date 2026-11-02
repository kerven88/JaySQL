package services

import (
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestStorageArtifactPathUpgradeFromOldRunTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Exec("CREATE TABLE insight_backup_runs (id integer primary key, file_path text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO insight_backup_runs (id, file_path) VALUES (1, 'legacy.sql')").Error; err != nil {
		t.Fatal(err)
	}
	// AutoMigrate uses this model field to add the missing column on existing installs.
	if err := db.Migrator().AddColumn(&backupModels.InsightBackupRuns{}, "StorageArtifactPath"); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.Raw("SELECT storage_artifact_path FROM insight_backup_runs WHERE id=1").Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	if value != "" {
		t.Fatal(value)
	}
	ref := "st:1:///backups/mysql/ds/task/run/backup.sql"
	if err := db.Table("insight_backup_runs").Where("id=1").Update("storage_artifact_path", ref).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("insight_backup_runs AS br").Select("br.storage_artifact_path").Where("br.id=1").Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	if value != ref {
		t.Fatalf("got %q", value)
	}
}
