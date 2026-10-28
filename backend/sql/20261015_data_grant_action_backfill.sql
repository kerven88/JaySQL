-- ---------------------------------------------------------------------------
-- 存量数据授权：补齐「操作维度」（select / export / dml）
--
-- 背景：
--   20261015_data_grant_fine_grained.sql 把旧授权表搬进 insight_das_data_grants 时，
--   actions 一律写成了 'select'。旧模型（schema_perms / table_perms）根本没有操作维度——
--   一条库授权同时蕴含「能查、能导出、能改」，所以那次迁移只搬了可见性，
--   把「能不能改 / 能不能导出」这两条腿落在了地上。
--
--   本次改造给工单链路接上了操作维度（DDL/DML → dml，EXPORT → export），
--   如果不做这次补齐，所有存量用户会立刻无法提交和执行 DML/DDL/导出工单 ——
--   那是事故，不是收紧权限。
--
-- 本脚本做什么：
--   把「由旧模型等价搬过来的」授权（grant_source IN migrate/manual/system）的 actions
--   补齐为 'select,export,dml'，并写一条审计流水说明原因。
--   不动 grant_source='self' 的记录 —— 那是用户在自助授权页显式勾选的，必须尊重原意。
--
-- 幂等：可重复执行（第二起的每一轮只会命中尚未补齐的记录）。
-- 回滚：见文件末尾「回滚」一节。
-- ---------------------------------------------------------------------------

-- 0. 先看一眼影响面（只读，不写）
SELECT grant_source,
       actions,
       COUNT(*) AS cnt
FROM `insight_das_data_grants`
WHERE `status` = 'active'
GROUP BY grant_source, actions
ORDER BY grant_source, actions;

-- 1. 补齐操作维度
UPDATE `insight_das_data_grants`
SET `actions`    = 'select,export,dml',
    `updated_at` = NOW(3)
WHERE `status` = 'active'
  AND `grant_source` IN ('migrate', 'manual', 'system')
  AND `actions` <> 'select,export,dml'
  -- 已经显式包含 dml 的不动（避免把用户后来手工调整过的记录又拍平）
  AND CONCAT(',', `actions`, ',') NOT LIKE '%,dml,%';

-- 2. 审计流水：说明这是一次「语义等价补齐」，不是新授权
INSERT INTO `insight_das_grant_audit`
  (`grant_no`, `grant_id`, `action`, `operator_username`, `operator_name`,
   `principal_type`, `principal_id`, `principal_name`, `resource_desc`, `actions`,
   `before_snapshot`, `after_snapshot`, `reason`, `request_id`, `source_ip`,
   `created_at`, `updated_at`)
SELECT
  g.`grant_no`,
  g.`id`,
  'backfill',
  'system',
  'system(action-backfill)',
  g.`principal_type`,
  g.`principal_id`,
  g.`principal_name`,
  CASE g.`resource_level`
    WHEN 'instance' THEN CONCAT('实例 ', g.`instance_id`)
    WHEN 'database' THEN CONCAT('库 ', g.`instance_id`, '.', g.`schema_name`)
    ELSE CONCAT('表 ', g.`instance_id`, '.', g.`schema_name`, '.', g.`table_name`)
  END,
  g.`actions`,
  CONCAT('{"actions":"select"}'),
  CONCAT('{"actions":"', g.`actions`, '"}'),
  '存量补齐：旧授权模型无操作维度，一条授权等价 select+export+dml，本次补齐以保持语义不变',
  '',
  '',
  NOW(3),
  NOW(3)
FROM `insight_das_data_grants` g
WHERE g.`status` = 'active'
  AND g.`grant_source` IN ('migrate', 'manual', 'system')
  AND g.`actions` = 'select,export,dml'
  AND NOT EXISTS (
    SELECT 1 FROM `insight_das_grant_audit` a
    WHERE a.`grant_no` = g.`grant_no` AND a.`action` = 'backfill'
  );

-- 3. 清掉启用状态缓存（服务侧缓存 30s，重启或等 30s 也会自然生效）
--    无需 SQL 操作，这里只做提示。

-- ---------------------------------------------------------------------------
-- 回滚（仅在需要恢复到「只授 select」时使用）
--   UPDATE `insight_das_data_grants`
--   SET `actions` = 'select', `updated_at` = NOW(3)
--   WHERE `grant_source` IN ('migrate','manual','system') AND `actions` = 'select,export,dml';
--
--   DELETE FROM `insight_das_grant_audit` WHERE `action` = 'backfill';
-- ---------------------------------------------------------------------------
