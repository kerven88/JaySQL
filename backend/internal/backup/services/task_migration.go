package services

import (
	"database/sql"
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/gorm"
)

func EnsureBackupTaskSchema(db *gorm.DB) {
	if db == nil {
		return
	}
	if !db.Migrator().HasTable("insight_backup_tasks") {
		return
	}
	idType := backupTaskIDColumnType(db)
	// 如果 id 列类型是 bigint 或 int (自增主键)，说明 backup_tasks 表已经完成双键改造
	// （即 id=自增主键，task_id=业务短号）。这意味着所有旧的 migrateBackupTaskSchema
	// （把 id 从自增 int 改回 varchar 字符串业务主键的那套迁移）已经过时，必须跳过。
	if strings.Contains(idType, "bigint") || strings.Contains(idType, "int") {
		// 已经是双键改造后的状态，直接跳过旧迁移，只做后续 ensure。
	} else {
		if !(strings.Contains(idType, "varchar") && db.Migrator().HasColumn("insight_backup_tasks", "deleted_at")) {
			migrateBackupTaskSchema(db)
		}
	}
	rebuildRestoreRunSchema(db)
	ensureBackupRunStatusColumn(db)
	ensureRestoreRunStatusColumn(db)
	ensureRestoreRunIdentityColumns(db)
	ensureBackupDriverTypeColumns(db)
	ensureBackupSetIdentityColumns(db)
	ensureBackupSetVerificationColumns(db)
	ensureBackupTaskPrecheckSchema(db)
	migrateLegacyPostgresPhysicalTasks(db)
	ensureBackupStorageTargetsSchema(db)
}

func ensureBackupTaskPrecheckSchema(db *gorm.DB) {
	if db == nil {
		return
	}
	if db.Migrator().HasTable("insight_backup_tasks") {
		if !db.Migrator().HasColumn("insight_backup_tasks", "last_precheck_status") {
			stmt := "ALTER TABLE insight_backup_tasks ADD COLUMN last_precheck_status varchar(32) NOT NULL DEFAULT 'NONE' COMMENT '最近一次预检查状态' AFTER driver_prepare_version"
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("ensure backup task last_precheck_status column failed: %v, sql: %s", err, stmt)
			}
		}
		if !db.Migrator().HasColumn("insight_backup_tasks", "last_precheck_at") {
			stmt := "ALTER TABLE insight_backup_tasks ADD COLUMN last_precheck_at datetime NULL COMMENT '最近一次预检查时间' AFTER last_precheck_status"
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("ensure backup task last_precheck_at column failed: %v, sql: %s", err, stmt)
			}
		}
		if !db.Migrator().HasColumn("insight_backup_tasks", "last_precheck_result") {
			stmt := "ALTER TABLE insight_backup_tasks ADD COLUMN last_precheck_result json NULL COMMENT '最近一次预检查结果' AFTER last_precheck_at"
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("ensure backup task last_precheck_result column failed: %v, sql: %s", err, stmt)
			}
		}
	}
	if db.Migrator().HasTable("insight_backup_runs") && !db.Migrator().HasColumn("insight_backup_runs", "precheck_summary") {
		stmt := "ALTER TABLE insight_backup_runs ADD COLUMN precheck_summary json NULL COMMENT '运行前预检查结果摘要' AFTER error_message"
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure backup run precheck_summary column failed: %v, sql: %s", err, stmt)
		}
	}
}

func ensureBackupStorageTargetsSchema(db *gorm.DB) {
	if db == nil {
		return
	}
	// 1) insight_backup_tasks / runs / sets 加 storage_target_id 列
	targets := []struct {
		table string
		after string
	}{
		{table: "insight_backup_tasks", after: "repository_node_id"},
		{table: "insight_backup_runs", after: "repository_node_id"},
		{table: "insight_backup_sets", after: "repository_node_id"},
	}
	for _, item := range targets {
		if !db.Migrator().HasTable(item.table) {
			continue
		}
		if !db.Migrator().HasColumn(item.table, "storage_target_id") {
			stmt := fmt.Sprintf(
				"ALTER TABLE %s ADD COLUMN storage_target_id bigint NOT NULL DEFAULT 0 COMMENT '存储目标ID' AFTER %s",
				item.table, item.after,
			)
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("ensure storage_target_id column failed: %v, sql: %s", err, stmt)
			}
		}
		if !db.Migrator().HasIndex(item.table, "idx_storage_target_id") {
			stmt := fmt.Sprintf(
				"ALTER TABLE %s ADD INDEX idx_storage_target_id (storage_target_id)",
				item.table,
			)
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("ensure idx_storage_target_id index failed: %v, sql: %s", err, stmt)
			}
		}
	}
	// 2) 扩展 storage_backend ENUM 加 STORAGE_TARGET_REF
	for _, item := range []string{"insight_backup_tasks", "insight_backup_runs", "insight_backup_sets"} {
		if !db.Migrator().HasTable(item) {
			continue
		}
		stmt := fmt.Sprintf(
			"ALTER TABLE %s MODIFY COLUMN storage_backend ENUM('LOCAL','OSS','MINIO','STORAGE_TARGET_REF') NOT NULL DEFAULT 'LOCAL' COMMENT '存储后端'",
			item,
		)
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure storage_backend enum extension failed: %v, sql: %s", err, stmt)
		}
	}
}

func backupTaskIDColumnType(db *gorm.DB) string {
	return backupColumnType(db, "insight_backup_tasks", "id")
}

func backupColumnType(db *gorm.DB, table string, column string) string {
	var value sql.NullString
	err := db.Raw(`
		SELECT DATA_TYPE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?
		LIMIT 1`, table, column).Scan(&value).Error
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value.String))
}

func ensureBackupRunStatusColumn(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("insight_backup_runs") {
		return
	}
	if strings.Contains(backupColumnType(db, "insight_backup_runs", "status"), "varchar") {
		return
	}
	stmt := "ALTER TABLE insight_backup_runs MODIFY COLUMN status varchar(32) NOT NULL DEFAULT 'PENDING' COMMENT '执行状态'"
	if err := db.Exec(stmt).Error; err != nil {
		global.App.Log.Warnf("ensure backup run status column failed: %v, sql: %s", err, stmt)
		panic(fmt.Sprintf("ensure backup run status column failed: %v", err))
	}
}

func ensureRestoreRunStatusColumn(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("insight_restore_runs") {
		return
	}
	if strings.Contains(backupColumnType(db, "insight_restore_runs", "status"), "varchar") {
		return
	}
	stmt := "ALTER TABLE insight_restore_runs MODIFY COLUMN status varchar(32) NOT NULL DEFAULT 'PENDING' COMMENT '执行状态'"
	if err := db.Exec(stmt).Error; err != nil {
		global.App.Log.Warnf("ensure restore run status column failed: %v, sql: %s", err, stmt)
		panic(fmt.Sprintf("ensure restore run status column failed: %v", err))
	}
}

func ensureRestoreRunIdentityColumns(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("insight_restore_runs") {
		return
	}
	if db.Migrator().HasColumn("insight_restore_runs", "task_id") {
		stmt := `UPDATE insight_restore_runs
			SET task_id = CONCAT('rs-', SUBSTR(MD5(CONCAT('restore-task:', id)), 1, 12))
			WHERE COALESCE(task_id, '') = ''`
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("backfill restore run task_id failed: %v, sql: %s", err, stmt)
		}
	} else {
		stmt := `ALTER TABLE insight_restore_runs
			ADD COLUMN task_id char(15) NOT NULL DEFAULT '' COMMENT '任务编号' AFTER updated_at`
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure restore run task_id column failed: %v, sql: %s", err, stmt)
			panic(fmt.Sprintf("ensure restore run task_id column failed: %v", err))
		}
		backfill := `UPDATE insight_restore_runs
			SET task_id = CONCAT('rs-', SUBSTR(MD5(CONCAT('restore-task:', id)), 1, 12))
			WHERE COALESCE(task_id, '') = ''`
		if err := db.Exec(backfill).Error; err != nil {
			global.App.Log.Warnf("backfill restore run task_id failed: %v, sql: %s", err, backfill)
		}
	}
	if !db.Migrator().HasColumn("insight_restore_runs", "name") {
		stmt := `ALTER TABLE insight_restore_runs
			ADD COLUMN name varchar(128) NOT NULL DEFAULT '' COMMENT '任务名称' AFTER task_id`
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure restore run name column failed: %v, sql: %s", err, stmt)
			panic(fmt.Sprintf("ensure restore run name column failed: %v", err))
		}
	}
}

func rebuildRestoreRunSchema(db *gorm.DB) {
	if db == nil {
		return
	}
	hasRestoreRuns := db.Migrator().HasTable("insight_restore_runs")
	hasLegacyPhysical := db.Migrator().HasTable("insight_physical_restore_runs")
	if !hasRestoreRuns && !hasLegacyPhysical {
		return
	}
	needsRebuild := hasLegacyPhysical
	if hasRestoreRuns {
		legacyColumns := []string{
			"target_pg_bin_dir",
			"target_pg_version",
			"target_data_dir",
			"stanza",
			"repo_host",
			"repo_path",
			"legacy_physical_run_id",
		}
		for _, column := range legacyColumns {
			if db.Migrator().HasColumn("insight_restore_runs", column) {
				needsRebuild = true
				break
			}
		}
	}
	if !needsRebuild {
		return
	}
	if db.Migrator().HasTable("insight_restore_run_items") {
		if err := db.Exec("DELETE FROM insight_restore_run_items").Error; err != nil {
			global.App.Log.Warnf("clear restore run items before rebuild failed: %v", err)
		}
	}
	if hasLegacyPhysical {
		if err := db.Migrator().DropTable("insight_physical_restore_runs"); err != nil {
			global.App.Log.Warnf("drop insight_physical_restore_runs failed: %v", err)
			panic(fmt.Sprintf("drop insight_physical_restore_runs failed: %v", err))
		}
	}
	if hasRestoreRuns {
		if err := db.Migrator().DropTable("insight_restore_runs"); err != nil {
			global.App.Log.Warnf("drop insight_restore_runs failed: %v", err)
			panic(fmt.Sprintf("drop insight_restore_runs failed: %v", err))
		}
	}
}

func ensureBackupDriverTypeColumns(db *gorm.DB) {
	if db == nil {
		return
	}
	targets := []struct {
		table  string
		column string
	}{
		{table: "insight_backup_tasks", column: "driver_type"},
		{table: "insight_backup_runs", column: "driver_type"},
		{table: "insight_backup_sets", column: "driver_type"},
	}
	for _, item := range targets {
		columnType := backupColumnType(db, item.table, item.column)
		if strings.Contains(columnType, "varchar") {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s varchar(32) NOT NULL DEFAULT 'LOGICAL' COMMENT '备份驱动'", item.table, item.column)
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure backup driver type column failed: %v, sql: %s", err, stmt)
			panic(fmt.Sprintf("ensure backup driver type column failed: %v", err))
		}
	}
}

// ensureBackupSetVerificationColumns 为备份集补齐「验证状态」三列。
//
// 背景：恢复预检原本靠现场 find metadata.json / checksums.sha256 判断备份集是否可验证，
// 而直传（DirectStream）模式下产物经 SSH 直写存储目标、附属文件不随行，导致两项恒为 WARN，
// 生产环境被 verified_backupset 一票否决。改为备份完成时把验证结果落库，恢复侧只读字段。
//
// 兼容性：三列新增后存量备份集均为 0 且 verified_at 为 NULL，表示「从未执行过验证流程」，
// 恢复侧据此回退到旧的现场查找逻辑，历史备份集的可用性与升级前完全一致，不会突然不可用。
func ensureBackupSetVerificationColumns(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("insight_backup_sets") {
		return
	}
	columns := []struct {
		name string
		stmt string
	}{
		{
			name: "metadata_verified",
			stmt: `ALTER TABLE insight_backup_sets
				ADD COLUMN metadata_verified tinyint(1) NOT NULL DEFAULT 0 COMMENT '元数据已验证' AFTER sha256`,
		},
		{
			name: "checksum_verified",
			stmt: `ALTER TABLE insight_backup_sets
				ADD COLUMN checksum_verified tinyint(1) NOT NULL DEFAULT 0 COMMENT '校验和已验证' AFTER metadata_verified`,
		},
		{
			name: "verified_at",
			stmt: `ALTER TABLE insight_backup_sets
				ADD COLUMN verified_at datetime NULL DEFAULT NULL COMMENT '验证时间(NULL=历史数据未验证)' AFTER checksum_verified`,
		},
	}
	for _, item := range columns {
		if db.Migrator().HasColumn("insight_backup_sets", item.name) {
			continue
		}
		if err := db.Exec(item.stmt).Error; err != nil {
			global.App.Log.Warnf("ensure backup set verification column %s failed: %v, sql: %s", item.name, err, item.stmt)
			panic(fmt.Sprintf("ensure backup set verification column %s failed: %v", item.name, err))
		}
	}
}

func ensureBackupSetIdentityColumns(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("insight_backup_sets") {
		return
	}
	// columnJustAdded 标记 backup_sets_id 列是否为本次启动才补齐。
	// 该列的"历史快照回填"是一次性结构迁移，只能在新加列的那一次执行：
	// 回填的判定条件为「insight_backup_runs 存在、insight_backup_sets 缺失」，
	// 无法区分"历史数据尚未迁移"与"用户主动删除了备份集"。若每次启动都执行，
	// 被删除的备份集会因其关联的 run 记录仍然存在而被重新插入，
	// 表现为"删除后重启服务，已删除的备份集全部复活"。
	columnJustAdded := false
	if db.Migrator().HasColumn("insight_backup_sets", "backup_sets_id") {
		stmt := `UPDATE insight_backup_sets
			SET backup_sets_id = CONCAT('bks-', SUBSTR(MD5(CONCAT('backup-set:', id)), 1, 12))
			WHERE COALESCE(backup_sets_id, '') = ''`
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("backfill backup_sets_id failed: %v, sql: %s", err, stmt)
		}
	} else {
		columnJustAdded = true
		stmt := `ALTER TABLE insight_backup_sets
			ADD COLUMN backup_sets_id varchar(20) NOT NULL DEFAULT '' COMMENT '备份集ID' AFTER id`
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure backup_sets_id column failed: %v, sql: %s", err, stmt)
			panic(fmt.Sprintf("ensure backup_sets_id column failed: %v", err))
		}
		backfill := `UPDATE insight_backup_sets
			SET backup_sets_id = CONCAT('bks-', SUBSTR(MD5(CONCAT('backup-set:', id)), 1, 12))
			WHERE COALESCE(backup_sets_id, '') = ''`
		if err := db.Exec(backfill).Error; err != nil {
			global.App.Log.Warnf("backfill backup_sets_id failed: %v, sql: %s", err, backfill)
		}
	}
	if !db.Migrator().HasIndex(&backupModels.InsightBackupSets{}, "uniq_backup_sets_id") {
		stmt := `ALTER TABLE insight_backup_sets ADD UNIQUE INDEX uniq_backup_sets_id (backup_sets_id)`
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("ensure backup_sets_id unique index failed: %v, sql: %s", err, stmt)
			panic(fmt.Sprintf("ensure backup_sets_id unique index failed: %v", err))
		}
	}
	// 一次性迁移：仅当 backup_sets_id 列本次才被补齐时，才为历史执行记录回填备份集快照。
	// 已升级过的环境（列早已存在）不再执行，避免把用户已删除的备份集重新插回。
	if !columnJustAdded {
		return
	}
	if !db.Migrator().HasTable("insight_backup_runs") || !db.Migrator().HasTable("insight_backup_tasks") {
		return
	}
	insertMissingSets := `INSERT IGNORE INTO insight_backup_sets (
			backup_sets_id, run_id, task_id, db_type, source_instance_id, executor_node_id, repository_node_id, source_db_name,
			backup_type, object_mode, object_spec, object_summary, output_dir, driver_type, driver_config, storage_backend,
			storage_config, retention_days, expire_at, name, file_name, file_path, size_bytes, sha256, server_version, tool, tool_image,
			created_by, created_at, updated_at
		)
		SELECT
			CONCAT('bks-', SUBSTR(MD5(CONCAT('backup-run:', br.id)), 1, 12)) AS backup_sets_id,
			br.id,
			br.task_id,
			COALESCE(NULLIF(bt.db_type, ''), 'MySQL') AS db_type,
			COALESCE(bt.source_instance_id, 0) AS source_instance_id,
			br.executor_node_id,
			br.repository_node_id,
			COALESCE(bt.source_db_name, '') AS source_db_name,
			COALESCE(NULLIF(br.backup_type, ''), 'FULL') AS backup_type,
			COALESCE(NULLIF(br.object_mode, ''), 'CUSTOM') AS object_mode,
			br.object_spec,
			COALESCE(br.object_summary, '') AS object_summary,
			COALESCE(br.output_dir, '') AS output_dir,
			COALESCE(NULLIF(br.driver_type, ''), 'LOGICAL') AS driver_type,
			br.driver_config,
			COALESCE(NULLIF(br.storage_backend, ''), 'LOCAL') AS storage_backend,
			br.storage_config,
			COALESCE(br.retention_days, 30) AS retention_days,
			CASE
				WHEN COALESCE(br.retention_days, 0) > 0 THEN DATE_ADD(COALESCE(br.created_at, NOW()), INTERVAL COALESCE(br.retention_days, 30) DAY)
				ELSE NULL
			END AS expire_at,
			CONCAT(LEFT(COALESCE(NULLIF(bt.name, ''), CONCAT('backup_set_', br.task_id)), 240), '_', DATE_FORMAT(COALESCE(br.created_at, NOW()), '%Y%m%d%H%i%s')) AS name,
			COALESCE(br.file_name, '') AS file_name,
			COALESCE(br.file_path, '') AS file_path,
			COALESCE(br.size_bytes, 0) AS size_bytes,
			COALESCE(br.sha256, '') AS sha256,
			COALESCE(br.server_version, '') AS server_version,
			COALESCE(br.tool, '') AS tool,
			COALESCE(br.tool_image, '') AS tool_image,
			COALESCE(NULLIF(br.created_by, ''), COALESCE(bt.created_by, '')) AS created_by,
			COALESCE(br.created_at, NOW()) AS created_at,
			COALESCE(br.updated_at, NOW()) AS updated_at
		FROM insight_backup_runs br
		LEFT JOIN insight_backup_tasks bt ON bt.task_id = br.task_id
		LEFT JOIN insight_backup_sets bs ON bs.run_id = br.id
		WHERE bs.id IS NULL`
	if err := db.Exec(insertMissingSets).Error; err != nil {
		global.App.Log.Warnf("backfill backup set snapshots failed: %v, sql: %s", err, insertMissingSets)
	}
}

func migrateLegacyPostgresPhysicalTasks(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("insight_backup_tasks") {
		return
	}
	stmt := `UPDATE insight_backup_tasks
		SET driver_type = 'PG_BACKREST'
		WHERE db_type = 'PostgreSQL' AND driver_type = 'PHYSICAL'`
	if err := db.Exec(stmt).Error; err != nil {
		global.App.Log.Warnf("migrate legacy postgres physical tasks failed: %v, sql: %s", err, stmt)
	}
}

func migrateBackupTaskSchema(db *gorm.DB) {
	statements := []string{
		"DROP TABLE IF EXISTS insight_backup_sets_tmp",
		"DROP TABLE IF EXISTS insight_backup_runs_tmp",
		"DROP TABLE IF EXISTS insight_backup_tasks_tmp",
		`CREATE TABLE insight_backup_tasks_tmp LIKE insight_backup_tasks`,
		`ALTER TABLE insight_backup_tasks_tmp MODIFY COLUMN id varchar(20) NOT NULL COMMENT '任务ID'`,
		`ALTER TABLE insight_backup_tasks_tmp DROP PRIMARY KEY`,
		`ALTER TABLE insight_backup_tasks_tmp ADD PRIMARY KEY (id)`,
		`ALTER TABLE insight_backup_tasks_tmp MODIFY COLUMN created_at datetime NULL COMMENT '创建时间'`,
		`ALTER TABLE insight_backup_tasks_tmp MODIFY COLUMN updated_at datetime NULL COMMENT '更新时间'`,
		`ALTER TABLE insight_backup_tasks_tmp ADD COLUMN deleted_at datetime NULL COMMENT '逻辑删除时间' AFTER updated_at`,
		`INSERT INTO insight_backup_tasks_tmp (
			id, created_at, updated_at, deleted_at, name, db_type, source_instance_id, executor_node_id, source_db_name,
			object_mode, object_spec, object_summary, output_dir, driver_type, driver_config, storage_backend,
			storage_config, enabled, created_by, updated_by
		)
		SELECT
			CONCAT('bk-', SUBSTR(MD5(CONCAT('backup-task:', id)), 1, 12)) AS id,
			created_at, updated_at, NULL AS deleted_at, name, db_type, source_instance_id, executor_node_id, source_db_name,
			object_mode, object_spec, object_summary, output_dir, driver_type, driver_config, storage_backend,
			storage_config, enabled, created_by, updated_by
		FROM insight_backup_tasks`,
		`CREATE TABLE insight_backup_runs_tmp LIKE insight_backup_runs`,
		`ALTER TABLE insight_backup_runs_tmp MODIFY COLUMN task_id varchar(20) NOT NULL DEFAULT '' COMMENT '任务ID'`,
		`INSERT INTO insight_backup_runs_tmp (
			id, created_at, updated_at, task_id, status, start_at, end_at, executor_node_id,
			object_mode, object_spec, object_summary, output_dir, driver_type, driver_config,
			storage_backend, storage_config, tool, tool_image, server_version, file_name,
			file_path, size_bytes, sha256, log_path, log_excerpt, error_message, created_by
		)
		SELECT
			id, created_at, updated_at,
			CONCAT('bk-', SUBSTR(MD5(CONCAT('backup-task:', task_id)), 1, 12)) AS task_id,
			COALESCE(status, 'PENDING'), start_at, end_at, COALESCE(executor_node_id, 0), COALESCE(object_mode, 'CUSTOM'), object_spec, COALESCE(object_summary, ''),
			COALESCE(output_dir, ''), COALESCE(driver_type, 'LOGICAL'), driver_config, COALESCE(storage_backend, 'LOCAL'), storage_config, COALESCE(tool, ''), COALESCE(tool_image, ''),
			COALESCE(server_version, ''), COALESCE(file_name, ''), COALESCE(file_path, ''), COALESCE(size_bytes, 0), COALESCE(sha256, ''), COALESCE(log_path, ''), log_excerpt, COALESCE(error_message, ''), COALESCE(created_by, '')
		FROM insight_backup_runs`,
		`CREATE TABLE insight_backup_sets_tmp LIKE insight_backup_sets`,
		`ALTER TABLE insight_backup_sets_tmp MODIFY COLUMN task_id varchar(20) NOT NULL DEFAULT '' COMMENT '任务ID'`,
		`INSERT INTO insight_backup_sets_tmp (
			id, created_at, updated_at, run_id, task_id, db_type, source_instance_id,
			executor_node_id, source_db_name, object_mode, object_spec, object_summary,
			output_dir, driver_type, driver_config, storage_backend, storage_config, name,
			file_name, file_path, size_bytes, sha256, server_version, tool, tool_image, created_by
		)
		SELECT
			id, created_at, updated_at, run_id,
			CONCAT('bk-', SUBSTR(MD5(CONCAT('backup-task:', task_id)), 1, 12)) AS task_id,
			COALESCE(db_type, 'MySQL'), COALESCE(source_instance_id, 0), COALESCE(executor_node_id, 0), COALESCE(source_db_name, ''), COALESCE(object_mode, 'CUSTOM'), object_spec, COALESCE(object_summary, ''),
			COALESCE(output_dir, ''), COALESCE(driver_type, 'LOGICAL'), driver_config, COALESCE(storage_backend, 'LOCAL'), storage_config, COALESCE(name, ''), COALESCE(file_name, ''),
			COALESCE(file_path, ''), COALESCE(size_bytes, 0), COALESCE(sha256, ''), COALESCE(server_version, ''), COALESCE(tool, ''), COALESCE(tool_image, ''), COALESCE(created_by, '')
		FROM insight_backup_sets`,
		"RENAME TABLE insight_backup_tasks TO insight_backup_tasks_old, insight_backup_tasks_tmp TO insight_backup_tasks",
		"RENAME TABLE insight_backup_runs TO insight_backup_runs_old, insight_backup_runs_tmp TO insight_backup_runs",
		"RENAME TABLE insight_backup_sets TO insight_backup_sets_old, insight_backup_sets_tmp TO insight_backup_sets",
		"DROP TABLE insight_backup_tasks_old",
		"DROP TABLE insight_backup_runs_old",
		"DROP TABLE insight_backup_sets_old",
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("migrate backup task schema failed: %v, sql: %s", err, stmt)
			panic(fmt.Sprintf("migrate backup task schema failed: %v", err))
		}
	}
}
