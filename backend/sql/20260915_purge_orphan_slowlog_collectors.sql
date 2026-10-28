-- =============================================================================
-- 一次性清理：孤儿慢日志采集器与明细
--
-- 背景：insight_performance_slowlog_collectors 以 instance_id 关联 insight_instances，
--       但实例被删除时不会级联清理采集器与 insight_performance_slowlog_entries，
--       于是留下「实例已不存在」的孤儿行（实测出现 5 行采集器 / 2 个 MySQL 实例）。
--
-- 在线维护任务（slowlog_maintenance.go）只把孤儿采集器标记为 ORPHAN，
-- 不删除数据；本脚本用于人工确认后真正移除。
--
-- 使用方式：先执行 STEP 1 预览，确认列表符合预期后再执行 STEP 2。
--   注意：STEP 2 不可回滚，务必先备份两张表。
-- =============================================================================

-- ---------------------------------------------------------------------------
-- STEP 1（只读）：预览将被清理的孤儿数据
-- ---------------------------------------------------------------------------
SELECT c.id,
       c.instance_id,
       c.collect_status,
       c.create_time,
       c.update_time,
       (SELECT COUNT(*) FROM insight_performance_slowlog_entries e
         WHERE e.instance_id = c.instance_id) AS entry_rows
FROM insight_performance_slowlog_collectors c
LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
WHERE i.instance_id IS NULL
ORDER BY c.instance_id;

-- 对应的明细条数汇总
SELECT COUNT(*) AS orphan_entry_rows
FROM insight_performance_slowlog_entries e
LEFT JOIN insight_instances i ON i.instance_id = e.instance_id
WHERE i.instance_id IS NULL;

-- ---------------------------------------------------------------------------
-- STEP 2（写操作）：删除孤儿明细与采集器
-- ---------------------------------------------------------------------------
-- 2.1 先删明细（大表，分批执行，避免长事务）
-- DELETE FROM insight_performance_slowlog_entries
--  WHERE instance_id IN (
--        SELECT c.instance_id
--          FROM insight_performance_slowlog_collectors c
--          LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
--         WHERE i.instance_id IS NULL
--  )
--  LIMIT 5000;   -- 反复执行直至 Rows affected = 0

-- 2.2 再删采集器
-- DELETE c FROM insight_performance_slowlog_collectors c
--  LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
-- WHERE i.instance_id IS NULL;

-- ---------------------------------------------------------------------------
-- 附：保留期清理（等价于在线定时任务，可选手动执行）
-- 保留天数取「启用中的全局快照参数」最大值；下方以 30 天为例。
-- ---------------------------------------------------------------------------
-- DELETE FROM insight_performance_slowlog_entries
--  WHERE IFNULL(event_time, collect_time) < DATE_SUB(NOW(), INTERVAL 30 DAY)
--  LIMIT 5000;   -- 反复执行直至 Rows affected = 0
