package services

import (
	"os"
	"testing"

	"github.com/simajiong/jaysql/internal/global"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// ============================================================================
// 这条用例盯的是一次真实的线上事故：
// event_key 上的 uniqueIndex 标签让 AutoMigrate 在启动时给存量表建唯一索引，
// 而存量行 event_key 全是 '' —— Duplicate entry ''，
// bootstrap 对 AutoMigrate 的 error 是 Fatal，服务直接起不来。
//
// 需要真实 MySQL（索引语义与 SQLite 不同），因此默认跳过：
//   GOINSIGHT_TEST_MYSQL_DSN='root:pwd@tcp(127.0.0.1:3306)/?charset=utf8mb4&parseTime=True' \
//     go test ./internal/performance/services/ -run TestEnsureSlowLogSchemaOnLegacyTable -v
// ============================================================================

func TestEnsureSlowLogSchemaOnLegacyTable(t *testing.T) {
	dsn := os.Getenv("GOINSIGHT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GOINSIGHT_TEST_MYSQL_DSN to run this test against a real MySQL")
	}
	global.App.Log = logrus.New()

	admin, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}
	const schema = "goinsight_slowlog_ddltest"
	_ = admin.Exec("DROP DATABASE IF EXISTS " + schema)
	if err := admin.Exec("CREATE DATABASE " + schema + " DEFAULT CHARSET utf8mb4").Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer admin.Exec("DROP DATABASE IF EXISTS " + schema)

	// 复用同一套账号，只是把库名换成临时 schema
	testDB, err := gorm.Open(mysql.Open(replaceDSNDatabase(dsn, schema)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test schema: %v", err)
	}

	// 1) 复现存量表：只有老三列唯一键，没有 event_key
	if err := testDB.Exec(`CREATE TABLE insight_performance_slowlog_entries (
		id bigint unsigned NOT NULL AUTO_INCREMENT,
		instance_id char(15) NOT NULL DEFAULT '',
		db_type varchar(32) NOT NULL DEFAULT 'MySQL',
		log_path varchar(512) NOT NULL DEFAULT '',
		file_inode varchar(64) NOT NULL DEFAULT '',
		offset_start bigint NOT NULL DEFAULT 0,
		offset_end bigint NOT NULL DEFAULT 0,
		event_time datetime DEFAULT NULL,
		collect_time datetime DEFAULT NULL,
		username varchar(128) NOT NULL DEFAULT '',
		client_host varchar(255) NOT NULL DEFAULT '',
		schema_name varchar(255) NOT NULL DEFAULT '',
		query_time_ms int NOT NULL DEFAULT 0,
		lock_time_ms int NOT NULL DEFAULT 0,
		rows_sent int NOT NULL DEFAULT 0,
		rows_examined int NOT NULL DEFAULT 0,
		thread_id varchar(64) NOT NULL DEFAULT '',
		sql_text longtext,
		sql_fingerprint varchar(255) NOT NULL DEFAULT '',
		sql_digest char(64) NOT NULL DEFAULT '',
		parse_status varchar(32) NOT NULL DEFAULT 'PARSED',
		parse_error text,
		raw_text longtext,
		created_at datetime(3) DEFAULT NULL,
		updated_at datetime(3) DEFAULT NULL,
		deleted_at datetime(3) DEFAULT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uniq_perf_slowlog_entry (instance_id, file_inode, offset_start)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := testDB.Exec(`INSERT INTO insight_performance_slowlog_entries
		(instance_id, file_inode, offset_start, event_time) VALUES
		('i0000000000001','12345',100,NOW()),
		('i0000000000001','12345',200,NOW()),
		('i0000000000002','',0,NOW())`).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// 2) AutoMigrate 必须成功——事故就发生在这里
	if err := testDB.AutoMigrate(&performanceModels.InsightPerformanceSlowLogEntries{}); err != nil {
		t.Fatalf("AutoMigrate on legacy table failed: %v", err)
	}

	// 3) 首次 ensure：回填 + 建索引
	EnsureSlowLogSchema(testDB)
	if !hasIndex(testDB, slowLogEntriesTable, slowLogEventKeyIndex) {
		t.Fatalf("unique index %s was not created", slowLogEventKeyIndex)
	}
	var empty int64
	testDB.Raw("SELECT COUNT(*) FROM insight_performance_slowlog_entries WHERE event_key = ''").Scan(&empty)
	if empty != 0 {
		t.Fatalf("still %d rows with empty event_key", empty)
	}

	// 4) 重复执行幂等
	EnsureSlowLogSchema(testDB)

	// 5) 真有重复值时跳过建索引（宁可不建，也不能让启动失败）
	testDB.Exec("ALTER TABLE insight_performance_slowlog_entries DROP INDEX " + slowLogEventKeyIndex)
	testDB.Exec("UPDATE insight_performance_slowlog_entries SET event_key = 'dup' WHERE id IN (1, 2)")
	EnsureSlowLogSchema(testDB)
	if hasIndex(testDB, slowLogEntriesTable, slowLogEventKeyIndex) {
		t.Fatalf("index must be skipped when duplicates exist")
	}
}

// replaceDSNDatabase 把 DSN 里的库名换成指定值（形如 ...tcp(host:3306)/db?param）。
func replaceDSNDatabase(dsn, database string) string {
	start := -1
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '/' && i+1 < len(dsn) && dsn[i+1] != '/' {
			start = i
			break
		}
	}
	if start < 0 {
		return dsn
	}
	end := len(dsn)
	for i := start + 1; i < len(dsn); i++ {
		if dsn[i] == '?' || dsn[i] == '/' {
			end = i
			break
		}
	}
	return dsn[:start+1] + database + dsn[end:]
}
