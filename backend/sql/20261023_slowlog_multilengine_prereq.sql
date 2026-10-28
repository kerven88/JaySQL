-- ============================================================================
-- 慢 SQL 采集：多引擎支持 + 前提条件可视化 所需的结构变更
--
-- 【重要】本脚本现在是**可选的手动执行项**，不是发版前置条件。
--   列（failure_code / source / event_key / sample_time / exec_count）由
--   服务启动时的 AutoMigrate 自动补齐；event_key 的回填与唯一索引由
--   EnsureSlowLogSchema 在启动期完成（见 slowlog_schema.go）。
--   只有以下情况才需要手动跑本脚本：
--     · DBA 想在发版前先把结构准备好，避免启动期 DDL 锁表
--     · 启动日志出现 "skip creating uniq_perf_slowlog_event_key: N duplicated"
--       需要先清理重复数据
--     · 想提前建好 Oracle 采样基线表
--
-- 兼容性：目标库为 MySQL 8.0，不支持 MariaDB 的
--   `ADD COLUMN IF NOT EXISTS` / `ADD INDEX IF NOT EXISTS` 语法。
--   因此下面用「先查 information_schema 判断、不存在才 ALTER」的
--   预处理语句写法，重复执行安全。
--
-- 修订记录：
--   初版把「建唯一索引」放在「回填 event_key」之前，存量库执行时必然报
--   Error 1062: Duplicate entry '' for key 'uniq_perf_slowlog_event_key'
--   ——存量行加完列后 event_key 全是 ''。已调整为先回填、后建索引。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 1) 采集器表：记录失败原因码与数据源类型
-- ---------------------------------------------------------------------------
SET @db := DATABASE();

SET @exists := (SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_collectors' AND COLUMN_NAME = 'failure_code');
SET @sql := IF(@exists = 0,
  'ALTER TABLE `insight_performance_slowlog_collectors` ADD COLUMN `failure_code` varchar(64) NOT NULL DEFAULT '''' COMMENT ''采集失败原因码'' AFTER `probe_status`',
  'SELECT ''skip failure_code''');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @exists := (SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_collectors' AND COLUMN_NAME = 'source');
SET @sql := IF(@exists = 0,
  'ALTER TABLE `insight_performance_slowlog_collectors` ADD COLUMN `source` varchar(32) NOT NULL DEFAULT '''' COMMENT ''数据源 FILE/V$SQL'' AFTER `failure_code`',
  'SELECT ''skip source''');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @exists := (SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_collectors' AND INDEX_NAME = 'idx_perf_slowlog_failure');
SET @sql := IF(@exists = 0,
  'ALTER TABLE `insight_performance_slowlog_collectors` ADD INDEX `idx_perf_slowlog_failure` (`failure_code`)',
  'SELECT ''skip idx_perf_slowlog_failure''');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- 2) 慢日志明细表：新增「事件身份」与采样字段
--
-- 背景：现有唯一键是 (instance_id, file_inode, offset_start)，隐含了
--       "慢SQL 一定来自某个文件的某个偏移" 这个假设。
--       这个假设对 MySQL / PG 成立，但对 Oracle 不成立——V$SQL 采样
--       没有文件、没有偏移量，所有行都会是空值，第二条就撞唯一键。
--
-- 做法：新增 event_key 作为**跨引擎统一**的事件身份；
--       老数据按同一规则回填（含 instance_id，避免跨实例 inode 撞车）；
--       不动原有三列，历史记录保持原样可查。
-- ---------------------------------------------------------------------------
SET @exists := (SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_entries' AND COLUMN_NAME = 'event_key');
SET @sql := IF(@exists = 0,
  'ALTER TABLE `insight_performance_slowlog_entries` ADD COLUMN `event_key` varchar(160) NOT NULL DEFAULT '''' COMMENT ''事件身份(跨引擎)'' AFTER `file_inode`',
  'SELECT ''skip event_key''');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @exists := (SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_entries' AND COLUMN_NAME = 'sample_time');
SET @sql := IF(@exists = 0,
  'ALTER TABLE `insight_performance_slowlog_entries` ADD COLUMN `sample_time` datetime DEFAULT NULL COMMENT ''采样时刻(Oracle)'' AFTER `event_time`',
  'SELECT ''skip sample_time''');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @exists := (SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_entries' AND COLUMN_NAME = 'exec_count');
SET @sql := IF(@exists = 0,
  'ALTER TABLE `insight_performance_slowlog_entries` ADD COLUMN `exec_count` bigint NOT NULL DEFAULT 0 COMMENT ''本轮新增执行次数(采样类引擎)'' AFTER `sample_time`',
  'SELECT ''skip exec_count''');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- 3) 回填历史明细的 event_key（必须在建索引之前）
--
-- 规则必须与新版写入规则逐字符一致：
--   event_key = CONCAT(instance_id, '|', file_inode, '|', offset_start)
-- 带上 instance_id 是因为 inode 只在单台主机内唯一，
-- 不同实例完全可能拿到同一个 inode 号。
--
-- 分批执行（每批 5000）：明细表可能很大，一把 UPDATE 会长时间锁表。
-- ---------------------------------------------------------------------------
UPDATE `insight_performance_slowlog_entries`
SET `event_key` = CONCAT(`instance_id`, '|', `file_inode`, '|', `offset_start`)
WHERE `event_key` = '' AND `file_inode` <> ''
LIMIT 5000;
-- 若上方返回的行数等于 5000，重复执行本段直到返回 0 行。

-- 兜底：拿不到 inode 的历史行（理论上只有采集异常时才有）用主键补齐，
-- 保证 event_key 非空且唯一，否则唯一索引建不起来。
UPDATE `insight_performance_slowlog_entries`
SET `event_key` = CONCAT('legacy|', `id`)
WHERE `event_key` = ''
LIMIT 5000;

-- ---------------------------------------------------------------------------
-- 3.1) 清理重复值（手工改过数据的库才会出现）
--
-- 正常情况下不会出现：原有唯一键 (instance_id, file_inode, offset_start)
-- 已经保证了拼接结果唯一。若这里查出 N > 0，说明数据被人工干预过，
-- 先把除最小 id 之外的行打散再建索引。
-- ---------------------------------------------------------------------------
UPDATE `insight_performance_slowlog_entries` e
JOIN (
  SELECT `event_key`, MIN(`id`) AS keep_id
  FROM `insight_performance_slowlog_entries`
  GROUP BY `event_key` HAVING COUNT(*) > 1
) d ON e.`event_key` = d.`event_key` AND e.`id` <> d.keep_id
SET e.`event_key` = CONCAT(e.`event_key`, '|dup|', e.`id`);

-- 唯一索引：event_key（放在回填之后）
--
-- 再验一次残留：上面的 UPDATE 带 LIMIT 5000，若明细行数超过一批而没重复执行到
-- 返回 0 行，event_key 仍会留下 '' —— 此时建索引照样 1062。
-- 有残留就跳过索引而不是让 DDL 失败：索引只是加速器与最后一道防线，
-- 幂等由入库侧 FirstOrCreate 保证，且服务每次启动都会由 EnsureSlowLogSchema 重试。
SET @exists := (SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'insight_performance_slowlog_entries' AND INDEX_NAME = 'uniq_perf_slowlog_event_key');
SET @remaining := (SELECT COUNT(*) FROM `insight_performance_slowlog_entries` e
  JOIN (SELECT `event_key` FROM `insight_performance_slowlog_entries`
        GROUP BY `event_key` HAVING COUNT(*) > 1) d ON e.`event_key` = d.`event_key`);
SET @empties := (SELECT COUNT(*) FROM `insight_performance_slowlog_entries` WHERE `event_key` = '');
SET @sql := IF(@exists > 0,
  'SELECT ''skip uniq_perf_slowlog_event_key: already exists''',
  IF(@empties > 0 OR @remaining > 0,
    'SELECT ''skip uniq_perf_slowlog_event_key: empty or duplicated event_key remains, finish the UPDATE above first''',
    'ALTER TABLE `insight_performance_slowlog_entries` ADD UNIQUE KEY `uniq_perf_slowlog_event_key` (`event_key`)'));
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- 4) Oracle V$SQL 采样基线表
--
-- 为什么需要它：V$SQL 的 ELAPSED_TIME / EXECUTIONS 是**累计值**，
--   单次查询只能拿到"自实例启动以来的总和"。要算"这一轮花了多久"，
--   必须记住上一次的累计值并做差。没有这张表，采集出的耗时口径全错。
--
-- 唯一键 (instance_id, sql_id, plan_hash_value)：
--   同一条 SQL 换了执行计划应作为新的一行重新建基线，
--   否则旧计划被老化出共享池时差分会出现负数。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `insight_performance_slowlog_samples` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `instance_id` char(15) NOT NULL DEFAULT '' COMMENT '关联实例ID',
  `sql_id` varchar(32) NOT NULL DEFAULT '' COMMENT 'Oracle SQL_ID',
  `plan_hash_value` varchar(64) NOT NULL DEFAULT '' COMMENT '执行计划指纹',
  `parsing_schema_name` varchar(128) NOT NULL DEFAULT '' COMMENT '解析用户',
  `executions_total` bigint NOT NULL DEFAULT 0 COMMENT '累计执行次数',
  `elapsed_us_total` bigint NOT NULL DEFAULT 0 COMMENT '累计耗时(微秒)',
  `cpu_us_total` bigint NOT NULL DEFAULT 0 COMMENT '累计CPU时间(微秒)',
  `buffer_gets_total` bigint NOT NULL DEFAULT 0 COMMENT '累计逻辑读',
  `disk_reads_total` bigint NOT NULL DEFAULT 0 COMMENT '累计物理读',
  `rows_processed_total` bigint NOT NULL DEFAULT 0 COMMENT '累计处理行数',
  `last_active_time` datetime DEFAULT NULL COMMENT '服务端最后活跃时间',
  `sample_time` datetime NOT NULL COMMENT '本次采样时刻(平台侧)',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_slowlog_sample` (`instance_id`, `sql_id`, `plan_hash_value`),
  KEY `idx_slowlog_sample_time` (`sample_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Oracle V$SQL 慢SQL采样基线';
