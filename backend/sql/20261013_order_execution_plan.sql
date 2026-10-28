-- =============================================================================
-- 工单「执行方式」落库（insight_order_records 补列）
--
-- 背景：
--   执行弹窗里的「执行方式」（立即/定时/手动）与「错误处理」「备份失败处理」
--   历史上只停留在表单层：forms 做了 binding 校验、前端也正确传参，但
--   ExecuteTaskService.Run / ExecuteBatchTasksService.Run 从未读取这些字段，
--   buildExecutionConfig() 也从未被调用（死代码）。三条链路无条件
--   「置 EXECUTING → 同步串行执行 → 收尾」，因此三个选项行为完全一致。
--
-- 本次改动：
--   1) export execution_mode / scheduled_at 两个可索引列——
--      定时执行调度器（每分钟）需要按 `execution_mode='SCHEDULED' AND scheduled_at <= NOW()`
--      扫描到期工单，若只在 execution_config(JSON) 里存则无法走索引。
--   2) error_policy / backup_policy 继续存在既有的 execution_config(JSON) 列里
--      （该列本就是为此预留，buildExecutionConfig 现在被真正调用）。
--
-- 幂等：
--   通过 information_schema 判存在后再 ALTER，整段可重复执行。
--   注意：GORM 启动时的 AutoMigrate 也会补这两列（见 internal/bootstrap/db.go），
--   本脚本用于「需要显式记录 / 不想依赖启动迁移」的环境。
--
-- 执行记录：
--   [2026-10-13] 测试环境 192.168.1.99（库 jaysql）已执行：execution_mode / scheduled_at
--   两个列与索引此前已由启动 AutoMigrate 补齐（脚本提示 already exists），
--   本次实际新增 started_at 列。生产环境需在该机本地执行（远程 root 无白名单）。
-- =============================================================================

-- ========================== STEP 1：只读预览（执行前） ==========================
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_COMMENT
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = 'insight_order_records'
  AND COLUMN_NAME IN ('execution_mode', 'scheduled_at', 'execution_config');

-- ========================== STEP 2：补列（幂等） ==========================
-- 2.1 execution_mode
SET @db := DATABASE();
SET @has_mode := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_order_records' AND COLUMN_NAME = 'execution_mode'
);
SET @sql := IF(@has_mode = 0,
  'ALTER TABLE `insight_order_records` ADD COLUMN `execution_mode` varchar(16) NOT NULL DEFAULT ''IMMEDIATE'' COMMENT ''执行方式(IMMEDIATE/SCHEDULED/MANUAL)'' AFTER `execution_config`',
  'SELECT ''execution_mode already exists'' AS skipped');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 2.2 scheduled_at
SET @has_sched := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_order_records' AND COLUMN_NAME = 'scheduled_at'
);
SET @sql := IF(@has_sched = 0,
  'ALTER TABLE `insight_order_records` ADD COLUMN `scheduled_at` datetime NULL DEFAULT NULL COMMENT ''定时执行时间'' AFTER `execution_mode`',
  'SELECT ''scheduled_at already exists'' AS skipped');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 2.3 索引：调度器扫描依据（EXPLAIN 应命中 idx_execution_mode / idx_scheduled_at）
SET @has_idx_mode := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_order_records' AND INDEX_NAME = 'idx_execution_mode'
);
SET @sql := IF(@has_idx_mode = 0,
  'ALTER TABLE `insight_order_records` ADD INDEX `idx_execution_mode` (`execution_mode`)',
  'SELECT ''idx_execution_mode already exists'' AS skipped');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @has_idx_sched := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_order_records' AND INDEX_NAME = 'idx_scheduled_at'
);
SET @sql := IF(@has_idx_sched = 0,
  'ALTER TABLE `insight_order_records` ADD INDEX `idx_scheduled_at` (`scheduled_at`)',
  'SELECT ''idx_scheduled_at already exists'' AS skipped');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 2.4 started_at（2026-10-13 追加）
--   背景：工单只有 executed_at（完成时刻），没有开始时刻，页面只能拿完成时间当"执行时间"；
--   定时工单的触发延迟（排队等待）与 SQL 实际执行耗时混在一起，无法解释
--   「计划 16:31:51、实际 16:32:46 完成，页面却显示耗时 57ms」这类现象。
--   落 started_at 后，等待时长 = started_at - scheduled_at，执行时长 = executed_at - started_at。
SET @has_started := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_order_records' AND COLUMN_NAME = 'started_at'
);
SET @sql := IF(@has_started = 0,
  'ALTER TABLE `insight_order_records` ADD COLUMN `started_at` datetime NULL DEFAULT NULL COMMENT ''开始执行时间'' AFTER `executed_at`',
  'SELECT ''started_at already exists'' AS skipped');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 存量数据说明：历史工单的 execution_mode 由 DEFAULT 'IMMEDIATE' 补齐，
-- 与它们「提交即执行」的实际行为一致，无需回填；scheduled_at 保持 NULL。

-- ========================== STEP 3：复核 ==========================
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_COMMENT
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = 'insight_order_records'
  AND COLUMN_NAME IN ('execution_mode', 'scheduled_at', 'started_at');

SELECT INDEX_NAME, COLUMN_NAME
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = 'insight_order_records'
  AND INDEX_NAME IN ('idx_execution_mode', 'idx_scheduled_at');

-- 待执行的定时工单（调度器的扫描目标）
SELECT order_id, title, progress, execution_mode, scheduled_at, started_at, executed_at
FROM insight_order_records
WHERE execution_mode = 'SCHEDULED' AND scheduled_at IS NOT NULL
ORDER BY scheduled_at ASC
LIMIT 20;
