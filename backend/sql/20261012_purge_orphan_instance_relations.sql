-- =============================================================================
-- 一次性清理：删除数据源后残留的孤儿关联数据
--
-- 背景：AdminDeleteInstanceConfigsService 原先只删除 insight_instances 主记录，
--       不清理任何以 instance_id（char(15) 业务ID）关联的子表。
--       元库没有物理外键（FOREIGN KEY 数量 = 0），模型也没有 constraint:OnDelete，
--       所以「外键挂在子表侧」的关联全部变成孤儿。
--
--       代码侧已于本次修复（2026-10-12）改为事务内级联清理，本脚本只处理**存量**孤儿。
--
-- 实测存量（192.168.1.99 / jaysql，6 个实例）：
--   insight_instance_sql_standard_bindings  1 行   <- 本次报告的不一致问题
--   insight_instance_schemas              887 行
--   insight_monitor_target_configs          2 行
--   insight_das_schema_perms               10 行
--   insight_performance_slowlog_collectors  6 行
--   insight_inspect_instance_params         1 行
--
-- 使用方式：先执行 STEP 1 预览，确认列表符合预期后再打开 STEP 2 执行。
--   注意：STEP 2 不可回滚，务必先备份相关表。
-- =============================================================================

-- ---------------------------------------------------------------------------
-- STEP 1（只读）：预览各表的孤儿数据
-- ---------------------------------------------------------------------------

-- 1.1 全部子表的孤儿行数汇总
SELECT 'insight_instance_sql_standard_bindings' AS tbl, COUNT(*) AS orphan_rows
  FROM insight_instance_sql_standard_bindings b
  LEFT JOIN insight_instances i ON i.instance_id = b.instance_id
 WHERE i.instance_id IS NULL
UNION ALL
SELECT 'insight_instance_schemas', COUNT(*)
  FROM insight_instance_schemas s
  LEFT JOIN insight_instances i ON i.instance_id = s.instance_id
 WHERE i.instance_id IS NULL
UNION ALL
SELECT 'insight_monitor_target_configs', COUNT(*)
  FROM insight_monitor_target_configs c
  LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
 WHERE i.instance_id IS NULL
UNION ALL
SELECT 'insight_das_schema_perms', COUNT(*)
  FROM insight_das_schema_perms p
  LEFT JOIN insight_instances i ON i.instance_id = p.instance_id
 WHERE i.instance_id IS NULL
UNION ALL
SELECT 'insight_das_table_perms', COUNT(*)
  FROM insight_das_table_perms p
  LEFT JOIN insight_instances i ON i.instance_id = p.instance_id
 WHERE i.instance_id IS NULL
UNION ALL
SELECT 'insight_performance_slowlog_collectors', COUNT(*)
  FROM insight_performance_slowlog_collectors c
  LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
 WHERE i.instance_id IS NULL
UNION ALL
SELECT 'insight_inspect_instance_params', COUNT(*)
  FROM insight_inspect_instance_params p
  LEFT JOIN insight_instances i ON i.instance_id = p.instance_id
 WHERE i.instance_id IS NULL;

-- 1.2 逐条看清楚：SQL 规范绑定孤儿（本次问题主角）
SELECT b.id, b.instance_id, b.standard_id, s.name AS standard_name, b.created_at
  FROM insight_instance_sql_standard_bindings b
  LEFT JOIN insight_instances i ON i.instance_id = b.instance_id
  LEFT JOIN insight_sql_standards s ON s.id = b.standard_id
 WHERE i.instance_id IS NULL
 ORDER BY b.id;

-- 1.3 逐条看清楚：其余子表孤儿涉及的 instance_id（确认确实是不存在的实例）
SELECT DISTINCT s.instance_id FROM insight_instance_schemas s
  LEFT JOIN insight_instances i ON i.instance_id = s.instance_id
 WHERE i.instance_id IS NULL
UNION
SELECT DISTINCT c.instance_id FROM insight_monitor_target_configs c
  LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
 WHERE i.instance_id IS NULL
UNION
SELECT DISTINCT p.instance_id FROM insight_das_schema_perms p
  LEFT JOIN insight_instances i ON i.instance_id = p.instance_id
 WHERE i.instance_id IS NULL
UNION
SELECT DISTINCT c.instance_id FROM insight_performance_slowlog_collectors c
  LEFT JOIN insight_instances i ON i.instance_id = c.instance_id
 WHERE i.instance_id IS NULL
UNION
SELECT DISTINCT p.instance_id FROM insight_inspect_instance_params p
  LEFT JOIN insight_instances i ON i.instance_id = p.instance_id
 WHERE i.instance_id IS NULL;

-- ---------------------------------------------------------------------------
-- STEP 2（写操作）：删除孤儿数据
-- 说明：全部用 NOT EXISTS 关联删除，只命中「insight_instances 里已不存在」的
--       instance_id；正常实例的关联数据一行都不会动。可重复执行（幂等）。
-- ---------------------------------------------------------------------------

-- 2.1 SQL 规范绑定（核心）
-- DELETE b FROM insight_instance_sql_standard_bindings b
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = b.instance_id);

-- 2.2 实例库表元数据缓存（行数可能较多，必要时分批）
-- DELETE s FROM insight_instance_schemas s
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = s.instance_id);

-- 2.3 监控采集配置
-- DELETE c FROM insight_monitor_target_configs c
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = c.instance_id);

-- 2.4 DAS 库权限 / 表权限
-- DELETE p FROM insight_das_schema_perms p
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = p.instance_id);
-- DELETE p FROM insight_das_table_perms p
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = p.instance_id);

-- 2.5 慢日志采集器
-- DELETE c FROM insight_performance_slowlog_collectors c
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = c.instance_id);

-- 2.6 审核实例参数（无 GORM 模型的遗留表）
-- DELETE p FROM insight_inspect_instance_params p
--  WHERE NOT EXISTS (SELECT 1 FROM insight_instances i WHERE i.instance_id = p.instance_id);

-- ---------------------------------------------------------------------------
-- 附：以下「流水/审计类」表同样含 instance_id，但**不建议**随实例删除清理，
--     它们承载历史与合规价值，应由独立的保留期任务处理：
--       insight_order_records（工单留痕）
--       insight_das_records（执行记录）
--       insight_monitor_metric_points / metric_latest / metric_snapshots
--       insight_monitor_load_snapshots
--       insight_monitor_alert_events / alert_escalations / notify_tasks
--       insight_performance_slowlog_entries
-- ---------------------------------------------------------------------------
