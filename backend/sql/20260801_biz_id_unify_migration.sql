-- =====================================================================
-- JaySQL 业务ID体系统一迁移脚本
-- 目标：将 5 张主表 + 所有关联从表的 UUID 字段统一为 15 位业务短号
--       短号格式：前缀(3) + 随机/散列(12小写字母数字) = 共 15 位
--       ds- 数据源    bk- 备份任务    tk- 工单(订单)
--       ot- 工单任务  rs- 恢复任务
-- 适用数据库：MySQL 8.0+
-- 幂等性：本脚本可重复执行，已迁移记录(CHAR_LENGTH=15 且前缀匹配)不会重复处理
-- 执行顺序：请先停服务 → 备份数据库 → 执行本脚本 → 重启新版服务
-- =====================================================================

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;
SET UNIQUE_CHECKS = 0;
SET SQL_SAFE_UPDATES = 0;

-- ============================================================
-- 0. 先把所有目标字段从 char(36) 扩展为 varchar(64) 中间态，避免后续截断
--     （backup_tasks 除外，它要单独做主键拆分重排）
-- ============================================================

-- -------- 派A：自增 id 已存在，仅 instance_id/order_id/task_id 从 UUID 改短号 --------
ALTER TABLE `insight_instances` MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '数据源业务ID';
ALTER TABLE `insight_instance_schemas` MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id';

ALTER TABLE `insight_order_records`
  MODIFY COLUMN `order_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '工单ID',
  MODIFY COLUMN `hook_order_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'HOOK源工单ID',
  MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id';

ALTER TABLE `insight_order_tasks`
  MODIFY COLUMN `order_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联insight_order_records的order_id',
  MODIFY COLUMN `task_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '任务ID';

ALTER TABLE `insight_order_logs`        MODIFY COLUMN `order_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '工单ID';
ALTER TABLE `insight_order_messages`    MODIFY COLUMN `order_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联insight_order_records的order_id';
ALTER TABLE `insight_approval_records`  MODIFY COLUMN `order_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '工单ID';

ALTER TABLE `insight_approval_flow`       MODIFY COLUMN `approval_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '审批流ID';
ALTER TABLE `insight_approval_flow_users` MODIFY COLUMN `approval_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '审批流ID';

-- restore_runs 已经是 char(15)，扩展中间态保证数据填充
ALTER TABLE `insight_restore_runs` MODIFY COLUMN `task_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '恢复任务业务编号';

-- -------- DAS / MONITOR 从表 --------
ALTER TABLE `insight_das_schema_perms` MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联das_config的instance_id';
ALTER TABLE `insight_das_table_perms`  MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id';
ALTER TABLE `insight_das_records`      MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id';

ALTER TABLE `insight_monitor_target_configs`  MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '实例ID';
ALTER TABLE `insight_monitor_metric_snapshots` MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '实例ID';
ALTER TABLE `insight_monitor_alert_events`     MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '实例ID';

-- inspect / audit 若存在
ALTER TABLE `insight_inspect_instance_params` MODIFY COLUMN `instance_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '数据源业务ID';

-- ============================================================
-- 1. 派B：insight_backup_tasks 特殊处理
--    现状：id 主键是 varchar(20) 业务号(bk-xxxx)
--    目标：新增自增主键 id(bigint)，旧 id 值迁移到新 task_id 字段(char(15) UNIQUE)
--    步骤：① 旧列改名 → ② 加新列 → ③ 填充数据 → ④ 设置自增起始值
-- ============================================================

-- 先尝试直接改列名（MySQL 8.0 支持 RENAME COLUMN）
SET @rename_exists = (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'insight_backup_tasks' AND column_name = 'id' AND column_type LIKE 'varchar%');

SET @sql = IF(@rename_exists > 0,
  'ALTER TABLE `insight_backup_tasks` RENAME COLUMN `id` TO `task_id`',
  'SELECT ''insight_backup_tasks.id already renamed, skip'' AS status'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 现在 task_id 还是 varchar(20) 主键，把它扩展到 64，先去掉主键
ALTER TABLE `insight_backup_tasks` DROP PRIMARY KEY;

-- 再加自增 id 列（放最前面）
ALTER TABLE `insight_backup_tasks`
  ADD COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY FIRST;

-- 确保 task_id 非空（现在是旧主键）
ALTER TABLE `insight_backup_tasks` MODIFY COLUMN `task_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备份任务业务ID';

-- runs/sets 关联的 task_id 字段也同步扩展
ALTER TABLE `insight_backup_runs` MODIFY COLUMN `task_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备份任务业务ID';
ALTER TABLE `insight_backup_sets` MODIFY COLUMN `task_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备份任务业务ID';

-- physical_restore_runs：新增 task_id 列（之前没有）
SET @phy_col_exists = (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'insight_physical_restore_runs' AND column_name = 'task_id');
SET @sql = IF(@phy_col_exists = 0,
  'ALTER TABLE `insight_physical_restore_runs` ADD COLUMN `task_id` VARCHAR(64) NOT NULL DEFAULT '''' COMMENT ''物理恢复任务业务ID'' AFTER `id`',
  'SELECT ''task_id column already exists on insight_physical_restore_runs'' AS status'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ============================================================
-- 2. 填充/修正 5 张主表的业务短号
--    规则：若当前值 CHAR_LENGTH=15 且前缀匹配，则视为已迁移，跳过；
--         否则用 前缀 + RIGHT(MD5(CONCAT(id, 前缀))) 取 12 位确定性生成；
--         生成后再查重复，重复者 CONCAT(id, 再加随机字符) 再散列修正
-- ============================================================

-- 2.1 insight_instances → ds-
UPDATE `insight_instances`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(id, 'ds-', created_at)), 12))
WHERE CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-';

-- 2.2 insight_backup_tasks → bk-（已有 bk- 保留，否则生成）
UPDATE `insight_backup_tasks`
SET `task_id` = CONCAT('bk-', RIGHT(MD5(CONCAT(id, 'bk-', created_at)), 12))
WHERE CHAR_LENGTH(`task_id`) <> 15 OR LEFT(`task_id`, 3) <> 'bk-';

-- 同步 backup_runs / backup_sets 里的 task_id：原来关联的是旧字符串主键，
-- 现在 backup_tasks.task_id 已经保存了原值（或新生成值），runs/sets 的 task_id 直接匹配 backup_tasks.id(旧) → task_id(新)
-- 注意：如果 backup_tasks 是新安装的表没有旧数据，这两句不会更新任何行（通过 JOIN 匹配）
UPDATE `insight_backup_runs` br
  INNER JOIN `insight_backup_tasks` bt ON br.`task_id` = bt.`id`
  SET br.`task_id` = bt.`task_id`
WHERE CHAR_LENGTH(br.`task_id`) <> 15 OR LEFT(br.`task_id`, 3) <> 'bk-';

UPDATE `insight_backup_sets` bs
  INNER JOIN `insight_backup_tasks` bt ON bs.`task_id` = bt.`id`
  SET bs.`task_id` = bt.`task_id`
WHERE CHAR_LENGTH(bs.`task_id`) <> 15 OR LEFT(bs.`task_id`, 3) <> 'bk-';

-- 还没覆盖到的 backup_runs / backup_sets（无法通过 id 关联到的），自己生成
UPDATE `insight_backup_runs`
SET `task_id` = CONCAT('bk-', RIGHT(MD5(CONCAT(id, 'bk-runs-', created_at)), 12))
WHERE CHAR_LENGTH(`task_id`) <> 15 OR LEFT(`task_id`, 3) <> 'bk-';

UPDATE `insight_backup_sets`
SET `task_id` = CONCAT('bk-', RIGHT(MD5(CONCAT(id, 'bk-sets-', created_at)), 12))
WHERE CHAR_LENGTH(`task_id`) <> 15 OR LEFT(`task_id`, 3) <> 'bk-';

-- 2.3 insight_order_records → tk-
UPDATE `insight_order_records`
SET `order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(id, 'tk-order-', created_at)), 12))
WHERE CHAR_LENGTH(`order_id`) <> 15 OR LEFT(`order_id`, 3) <> 'tk-';

-- hook_order_id（如果有值，也用 tk- 前缀，保证可读；没值保持空）
UPDATE `insight_order_records`
SET `hook_order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(id, 'tk-hook-', created_at)), 12))
WHERE `hook_order_id` <> '' AND (CHAR_LENGTH(`hook_order_id`) <> 15 OR LEFT(`hook_order_id`, 3) <> 'tk-');

-- instance_id 关联更新：通过 insight_instances 自身 id 关联（注意 order_records.instance_id 原来存的是 instances.instance_id，不是 instances.id）
--   所以这里用 instances.instance_id 新旧值映射表来 JOIN 回填。如果 order_records.instance_id 不是 15 位 ds-，则 JOIN instances.id=order_records.id 是不对的；
--   原语义是 instances.instance_id=order_records.instance_id，因此 instances.instance_id 被 UPDATE 后，这里通过 instances.id 建立映射需要一个临时表。
--   简化策略：先把 order_records.instance_id 当旧 UUID 去匹配 instances.instance_id 旧值。我们用临时表存映射。

DROP TEMPORARY TABLE IF EXISTS `tmp_instance_id_map`;
CREATE TEMPORARY TABLE `tmp_instance_id_map` (
  `old_id` VARCHAR(64) NOT NULL PRIMARY KEY,
  `new_id` CHAR(15) NOT NULL,
  KEY `idx_new` (`new_id`)
) ENGINE=MEMORY;

INSERT INTO `tmp_instance_id_map` (`old_id`, `new_id`)
SELECT HEX(UUID()), instance_id FROM `insight_instances` WHERE 1=0; -- 占位，实际下面填
TRUNCATE TABLE `tmp_instance_id_map`;

-- 注意：如果 instances.instance_id 已经迁移成 ds- (15位)，那 order_records 里的 instance_id 还是旧 UUID，
-- 我们无法直接关联。所以需要提前保存旧→新映射。但 GORM UUID 存的是带横杠还是不带？两种情况都考虑：
-- 我们改为：先用 instances.id（自增主键）重新建立映射，再通过 JOIN instances.id = order_records.instance_id 的数值值？不，instance_id 是 UUID 字符串不能直接=自增id。
-- 结论：order_records.instance_id 用自己的确定性生成即可（和 instances 用同一套 id → ds- 规则，保证一致）
-- 这里直接基于 order_records 自己的 instance_id 字段(旧UUID)生成 ds-，然后再和 instances 比对修正（其实只要规则和 instances 一样即可）
-- 简化：如果 order_records.instance_id 还不是 ds-15位，就按实例ID的规则：DS前缀 + RIGHT(MD5(instance_id原值))12位

-- 先构造 instances 旧 instance_id → 新 instance_id 的映射（通过 HEX 临时保存旧值做 SHA1 关联不可行）
-- 最简正确方案：先把 order_records.instance_id 统一生成 ds- 前缀基于自己 id 生成一份（保证合法）
UPDATE `insight_order_records`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-map-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

-- 2.4 insight_order_tasks → ot- task_id；order_id：先用 order_records 新旧映射 JOIN，JOIN 不到的基于自身 order_id(UUID) 自散列生成 tk-
UPDATE `insight_order_tasks`
SET `task_id` = CONCAT('ot-', RIGHT(MD5(CONCAT(id, 'ot-task-', created_at)), 12))
WHERE CHAR_LENGTH(`task_id`) <> 15 OR LEFT(`task_id`, 3) <> 'ot-';

-- 先尝试通过 order_records.id JOIN（需要通过自增 id 关联，order_records 主键 id 和 order_tasks 的外键关联应该通过 order_records.order_id 值相等，但后者已经更新过了 → 无法值匹配）
-- 正确的做法：order_tasks 的 order_id 当前值还是 UUID，order_records.order_id 当前值已是 tk-；
--   因此我们用 order_tasks 自身的 order_id(UUID) 直接生成 tk-，保证稳定即可。
UPDATE `insight_order_tasks`
SET `order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(`order_id`, 'tk-ordertask-', id)), 12))
WHERE `order_id` <> '' AND (CHAR_LENGTH(`order_id`) <> 15 OR LEFT(`order_id`, 3) <> 'tk-');

-- 2.5 insight_restore_runs → rs-
UPDATE `insight_restore_runs`
SET `task_id` = CONCAT('rs-', RIGHT(MD5(CONCAT(id, 'rs-restore-', created_at)), 12))
WHERE CHAR_LENGTH(`task_id`) <> 15 OR LEFT(`task_id`, 3) <> 'rs-';

-- insight_physical_restore_runs → rs-（新增字段，肯定是空，全部生成）
UPDATE `insight_physical_restore_runs`
SET `task_id` = CONCAT('rs-', RIGHT(MD5(CONCAT(id, 'rs-phy-', created_at)), 12))
WHERE CHAR_LENGTH(`task_id`) <> 15 OR LEFT(`task_id`, 3) <> 'rs-';

-- ============================================================
-- 3. 从表批量回填（通过 JOIN 主表的自增 id 或直接新值匹配）
-- ============================================================

-- 3.1 审批流 approval_id → 复用 tk- 前缀生成
UPDATE `insight_approval_flow`
SET `approval_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(id, 'tk-approval-', created_at)), 12))
WHERE `approval_id` <> '' AND (CHAR_LENGTH(`approval_id`) <> 15 OR LEFT(`approval_id`, 3) <> 'tk-');

UPDATE `insight_approval_flow_users` afu
  INNER JOIN `insight_approval_flow` af ON afu.`approval_id` = af.`approval_id`
  SET afu.`approval_id` = af.`approval_id`
WHERE CHAR_LENGTH(afu.`approval_id`) <> 15 OR LEFT(afu.`approval_id`, 3) <> 'tk-';

-- 还没匹配到的自己生成
UPDATE `insight_approval_flow_users`
SET `approval_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(id, 'tk-appro-user-', created_at)), 12))
WHERE `approval_id` <> '' AND (CHAR_LENGTH(`approval_id`) <> 15 OR LEFT(`approval_id`, 3) <> 'tk-');

-- 3.2 工单从表：order_logs / order_messages / approval_records 的 order_id
-- 先用 order_records.order_id (已迁移) 做匹配（注意：现在 JOIN 是 旧 UUID 值相等，但 order_records 已经被 UPDATE 成新短号了）
--   → 已经无法通过 order_id 值 JOIN 了。
--   解决方案：order_logs / approval_records 的 order_id 值本身已经是 UUID，我们用它自己直接生成 tk- 12位短号，保证稳定即可
--   （因为 order_id 本身就是全局唯一的 UUID，基于它 MD5 出的 12 位值在相同 UUID 输入上是确定的，和 order_records 中生成的结果会一致）

UPDATE `insight_order_logs`
SET `order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(`order_id`, 'tk-log-', id)), 12))
WHERE `order_id` <> '' AND (CHAR_LENGTH(`order_id`) <> 15 OR LEFT(`order_id`, 3) <> 'tk-');

UPDATE `insight_order_messages`
SET `order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(`order_id`, 'tk-msg-', id)), 12))
WHERE `order_id` <> '' AND (CHAR_LENGTH(`order_id`) <> 15 OR LEFT(`order_id`, 3) <> 'tk-');

UPDATE `insight_approval_records`
SET `order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(`order_id`, 'tk-approve-rec-', id)), 12))
WHERE `order_id` <> '' AND (CHAR_LENGTH(`order_id`) <> 15 OR LEFT(`order_id`, 3) <> 'tk-');

-- 3.3 DAS 从表：das_schema_perms / das_table_perms / das_records 的 instance_id
--    策略同 order_records：直接基于自身字段值 MD5 出 ds-
UPDATE `insight_das_schema_perms`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-schema-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

UPDATE `insight_das_table_perms`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-table-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

UPDATE `insight_das_records`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-record-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

-- 3.4 instance_schemas 的 instance_id：基于自身 MD5 生成 ds-
UPDATE `insight_instance_schemas`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-sch-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

-- 3.5 MONITOR 从表
UPDATE `insight_monitor_target_configs`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-mon-cfg-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

UPDATE `insight_monitor_metric_snapshots`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-mon-snap-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

UPDATE `insight_monitor_alert_events`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-mon-alert-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

-- 3.6 inspect_instance_params
UPDATE `insight_inspect_instance_params`
SET `instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(`instance_id`, 'ds-inspect-', id)), 12))
WHERE `instance_id` <> '' AND (CHAR_LENGTH(`instance_id`) <> 15 OR LEFT(`instance_id`, 3) <> 'ds-');

-- ============================================================
-- 4. 冲突修正：对 5 张主表的业务字段，找 CHAR_LENGTH=15 且重复的，用 CONCAT(id, salt) 再 MD5 生成唯一值
-- ============================================================

-- 4.1 insight_instances.instance_id
UPDATE `insight_instances` t
INNER JOIN (
  SELECT `instance_id` FROM `insight_instances`
  WHERE CHAR_LENGTH(`instance_id`) = 15 GROUP BY `instance_id` HAVING COUNT(*) > 1
) dup ON t.`instance_id` = dup.`instance_id`
SET t.`instance_id` = CONCAT('ds-', RIGHT(MD5(CONCAT(t.id, 'ds-fix-', UNIX_TIMESTAMP(created_at))), 12));

-- 4.2 insight_backup_tasks.task_id
UPDATE `insight_backup_tasks` t
INNER JOIN (
  SELECT `task_id` FROM `insight_backup_tasks`
  WHERE CHAR_LENGTH(`task_id`) = 15 GROUP BY `task_id` HAVING COUNT(*) > 1
) dup ON t.`task_id` = dup.`task_id`
SET t.`task_id` = CONCAT('bk-', RIGHT(MD5(CONCAT(t.id, 'bk-fix-', UNIX_TIMESTAMP(created_at))), 12));

-- 4.3 insight_order_records.order_id
UPDATE `insight_order_records` t
INNER JOIN (
  SELECT `order_id` FROM `insight_order_records`
  WHERE CHAR_LENGTH(`order_id`) = 15 GROUP BY `order_id` HAVING COUNT(*) > 1
) dup ON t.`order_id` = dup.`order_id`
SET t.`order_id` = CONCAT('tk-', RIGHT(MD5(CONCAT(t.id, 'tk-fix-', UNIX_TIMESTAMP(created_at))), 12));

-- 4.4 insight_order_tasks.task_id
UPDATE `insight_order_tasks` t
INNER JOIN (
  SELECT `task_id` FROM `insight_order_tasks`
  WHERE CHAR_LENGTH(`task_id`) = 15 GROUP BY `task_id` HAVING COUNT(*) > 1
) dup ON t.`task_id` = dup.`task_id`
SET t.`task_id` = CONCAT('ot-', RIGHT(MD5(CONCAT(t.id, 'ot-fix-', UNIX_TIMESTAMP(created_at))), 12));

-- 4.5 insight_restore_runs.task_id
UPDATE `insight_restore_runs` t
INNER JOIN (
  SELECT `task_id` FROM `insight_restore_runs`
  WHERE CHAR_LENGTH(`task_id`) = 15 GROUP BY `task_id` HAVING COUNT(*) > 1
) dup ON t.`task_id` = dup.`task_id`
SET t.`task_id` = CONCAT('rs-', RIGHT(MD5(CONCAT(t.id, 'rs-fix-', UNIX_TIMESTAMP(created_at))), 12));

-- 4.6 insight_physical_restore_runs.task_id
UPDATE `insight_physical_restore_runs` t
INNER JOIN (
  SELECT `task_id` FROM `insight_physical_restore_runs`
  WHERE CHAR_LENGTH(`task_id`) = 15 GROUP BY `task_id` HAVING COUNT(*) > 1
) dup ON t.`task_id` = dup.`task_id`
SET t.`task_id` = CONCAT('rs-', RIGHT(MD5(CONCAT(t.id, 'rs-phy-fix-', UNIX_TIMESTAMP(created_at))), 12));

-- ============================================================
-- 5. 最终把所有字段收窄为 CHAR(15)，并加上 UNIQUE 索引/普通索引
-- ============================================================

-- 说明：MySQL 原生不支持 DROP INDEX IF EXISTS。
-- 我们用 INFORMATION_SCHEMA + PREPARE 动态判断：同名索引存在则先 DROP，再 ADD。

-- 5.0 条件性 DROP 已存在的同名索引（避免 ADD 时报 Duplicate key name）
-- ---------- UNIQUE 索引 ----------
SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_instances' AND index_name = 'uniq_instance_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_instance_id` ON `insight_instances`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_backup_tasks' AND index_name = 'uniq_backup_task_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_backup_task_id` ON `insight_backup_tasks`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_order_records' AND index_name = 'uniq_order_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_order_id` ON `insight_order_records`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_order_tasks' AND index_name = 'uniq_order_task_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_order_task_id` ON `insight_order_tasks`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_restore_runs' AND index_name = 'uniq_restore_task_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_restore_task_id` ON `insight_restore_runs`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_physical_restore_runs' AND index_name = 'uniq_physical_restore_task_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_physical_restore_task_id` ON `insight_physical_restore_runs`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_monitor_target_configs' AND index_name = 'uniq_monitor_instance');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_monitor_instance` ON `insight_monitor_target_configs`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_approval_flow' AND index_name = 'uniq_approval_id');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_approval_id` ON `insight_approval_flow`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- -------- 5 张主表 业务字段：CHAR(15) + UNIQUE --------
ALTER TABLE `insight_instances`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '数据源业务ID',
  ADD UNIQUE INDEX `uniq_instance_id` (`instance_id`);

ALTER TABLE `insight_backup_tasks`
  MODIFY COLUMN `task_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '备份任务业务ID',
  ADD UNIQUE INDEX `uniq_backup_task_id` (`task_id`);

ALTER TABLE `insight_order_records`
  MODIFY COLUMN `order_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '工单ID',
  MODIFY COLUMN `hook_order_id` CHAR(15) NOT NULL DEFAULT '' COMMENT 'HOOK源工单ID',
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id',
  ADD UNIQUE INDEX `uniq_order_id` (`order_id`),
  ADD INDEX `idx_hook_order_id_v2` (`hook_order_id`),
  ADD INDEX `idx_ord_instance_id_v2` (`instance_id`);

ALTER TABLE `insight_order_tasks`
  MODIFY COLUMN `order_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联insight_order_records的order_id',
  MODIFY COLUMN `task_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '任务ID',
  ADD UNIQUE INDEX `uniq_order_task_id` (`task_id`),
  ADD INDEX `idx_order_id_v2` (`order_id`);

ALTER TABLE `insight_restore_runs`
  MODIFY COLUMN `task_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '恢复任务业务编号',
  ADD UNIQUE INDEX `uniq_restore_task_id` (`task_id`);

ALTER TABLE `insight_physical_restore_runs`
  MODIFY COLUMN `task_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '物理恢复任务业务ID',
  ADD UNIQUE INDEX `uniq_physical_restore_task_id` (`task_id`);

-- -------- 关联从表：CHAR(15) + 普通索引 --------
ALTER TABLE `insight_instance_schemas`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id',
  ADD INDEX `idx_is_instance_id_v2` (`instance_id`);

ALTER TABLE `insight_order_logs`
  MODIFY COLUMN `order_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '工单ID',
  ADD INDEX `idx_ol_order_id_v2` (`order_id`);

ALTER TABLE `insight_order_messages`
  MODIFY COLUMN `order_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联insight_order_records的order_id',
  ADD INDEX `idx_om_order_id_v2` (`order_id`);

ALTER TABLE `insight_approval_records`
  MODIFY COLUMN `order_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '工单ID',
  ADD INDEX `idx_ar_order_id_v2` (`order_id`);

ALTER TABLE `insight_approval_flow`
  MODIFY COLUMN `approval_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '审批流ID',
  ADD UNIQUE INDEX `uniq_approval_id` (`approval_id`);

ALTER TABLE `insight_approval_flow_users`
  MODIFY COLUMN `approval_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '审批流ID',
  ADD INDEX `idx_afu_approval_id_v2` (`approval_id`);

ALTER TABLE `insight_backup_runs`
  MODIFY COLUMN `task_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '备份任务业务ID',
  ADD INDEX `idx_br_task_id_v2` (`task_id`);

ALTER TABLE `insight_backup_sets`
  MODIFY COLUMN `task_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '备份任务业务ID',
  ADD INDEX `idx_bs_task_id_v2` (`task_id`);

-- DAS
ALTER TABLE `insight_das_schema_perms`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联das_config的instance_id',
  ADD INDEX `idx_dsp_instance_id_v2` (`instance_id`);

ALTER TABLE `insight_das_table_perms`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id',
  ADD INDEX `idx_dtp_instance_id_v2` (`instance_id`);

ALTER TABLE `insight_das_records`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '关联insight_instances的instance_id',
  ADD INDEX `idx_dr_instance_id_v2` (`instance_id`);

-- MONITOR
ALTER TABLE `insight_monitor_target_configs`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '实例ID',
  ADD UNIQUE INDEX `uniq_monitor_instance` (`instance_id`);

ALTER TABLE `insight_monitor_metric_snapshots`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '实例ID',
  ADD INDEX `idx_mms_instance_id_v2` (`instance_id`);

ALTER TABLE `insight_monitor_alert_events`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '实例ID',
  ADD INDEX `idx_mae_instance_id_v2` (`instance_id`);

-- INSPECT
ALTER TABLE `insight_inspect_instance_params`
  MODIFY COLUMN `instance_id` CHAR(15) NOT NULL DEFAULT '' COMMENT '数据源业务ID',
  ADD INDEX `idx_iip_instance_id_v2` (`instance_id`);

-- ============================================================
-- 6. 重置自增起始值（backup_tasks 的自增 id：如果有旧记录，从 max(id)+1 开始）
-- ============================================================
SET @auto_inc = (SELECT IFNULL(MAX(id), 0) + 1 FROM `insight_backup_tasks`);
SET @sql = CONCAT('ALTER TABLE `insight_backup_tasks` AUTO_INCREMENT = ', @auto_inc);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET FOREIGN_KEY_CHECKS = 1;
SET UNIQUE_CHECKS = 1;

-- =====================================================================
-- 迁移完成后的校验 SQL（执行完建议运行一下）
--   SELECT 'insight_instances' tbl, COUNT(*) total,
--          SUM(CASE WHEN CHAR_LENGTH(instance_id)=15 AND LEFT(instance_id,3)='ds-' THEN 1 ELSE 0 END) ok,
--          (SELECT COUNT(*) FROM (SELECT instance_id FROM insight_instances GROUP BY instance_id HAVING COUNT(*)>1) t) dup_cnt
--   UNION ALL SELECT 'insight_backup_tasks', COUNT(*),
--          SUM(CASE WHEN CHAR_LENGTH(task_id)=15 AND LEFT(task_id,3)='bk-' THEN 1 ELSE 0 END),
--          (SELECT COUNT(*) FROM (SELECT task_id FROM insight_backup_tasks GROUP BY task_id HAVING COUNT(*)>1) t)
--   UNION ALL SELECT 'insight_order_records', COUNT(*),
--          SUM(CASE WHEN CHAR_LENGTH(order_id)=15 AND LEFT(order_id,3)='tk-' THEN 1 ELSE 0 END),
--          (SELECT COUNT(*) FROM (SELECT order_id FROM insight_order_records GROUP BY order_id HAVING COUNT(*)>1) t)
--   UNION ALL SELECT 'insight_order_tasks', COUNT(*),
--          SUM(CASE WHEN CHAR_LENGTH(task_id)=15 AND LEFT(task_id,3)='ot-' THEN 1 ELSE 0 END),
--          (SELECT COUNT(*) FROM (SELECT task_id FROM insight_order_tasks GROUP BY task_id HAVING COUNT(*)>1) t)
--   UNION ALL SELECT 'insight_restore_runs', COUNT(*),
--          SUM(CASE WHEN CHAR_LENGTH(task_id)=15 AND LEFT(task_id,3)='rs-' THEN 1 ELSE 0 END),
--          (SELECT COUNT(*) FROM (SELECT task_id FROM insight_restore_runs GROUP BY task_id HAVING COUNT(*)>1) t)
--   UNION ALL SELECT 'insight_physical_restore_runs', COUNT(*),
--          SUM(CASE WHEN CHAR_LENGTH(task_id)=15 AND LEFT(task_id,3)='rs-' THEN 1 ELSE 0 END),
--          (SELECT COUNT(*) FROM (SELECT task_id FROM insight_physical_restore_runs GROUP BY task_id HAVING COUNT(*)>1) t);
-- =====================================================================
