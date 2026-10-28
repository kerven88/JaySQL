-- ===========================================================================
-- 清理「指向已删除数据源」的孤儿数据授权
--
-- 背景
--   insight_das_data_grants 以 instance_id(char(15)) 关联数据源，但元库无物理外键，
--   删除数据源时若服务层没有显式清理子表，授权行就会残留成孤儿。
--   2026-10-15 已在 common/services/instance.go 的 deleteInstanceRelatedConfigs
--   补上清理（新删的实例不会再产生孤儿），本脚本用于清理**历史上已经留下**的部分。
--
--   孤儿的实际危害有限（实例已不存在，可见性过滤不会命中，不构成越权），
--   但会污染「数据访问」管理页的授权列表与审计对账——列表里会出现
--   一个永远选不到的 instance_id。
--
-- 当前扫描结果（192.168.1.99 / jaysql，7 条）
--   ds-xekxrn3j71ce  role#12 管理员 (migrate/active) + user#1 admin (migrate/revoked)
--   ds-6g6g3oqhmr92  role#12 管理员 (migrate/active) + user#1 admin (migrate/revoked)
--   ds-n62qx0gezm1i  role#12 管理员 (migrate/active) + user#1 admin (system/active)
--   ds-22j7xnas3ab3  user#1 admin (system/active)
--
-- 安全性
--   - 只删「insight_instances 里已无对应行」的授权，绝不会碰到存活实例的授权
--   - 幂等，可重复执行
--   - 先写 revoke 审计流水再删行：授权行没了，证据仍在（审计表自带
--     grant_no / 主体 / 资源描述，不依赖授权行存在）
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- STEP 0 扫描（先跑这段确认范围）
-- ---------------------------------------------------------------------------
SELECT g.grant_no, g.instance_id, g.principal_type, g.principal_id, g.principal_name,
       g.resource_level, g.actions, g.grant_source, g.status
FROM insight_das_data_grants g
LEFT JOIN insight_instances i ON i.instance_id = g.instance_id
WHERE i.instance_id IS NULL;

-- ---------------------------------------------------------------------------
-- STEP 1 为待清理的孤儿写撤销流水（幂等：同一 grant_no 只写一次）
-- ---------------------------------------------------------------------------
INSERT INTO insight_das_grant_audit (
    grant_no, grant_id, action,
    operator_username, operator_name,
    principal_type, principal_id, principal_name,
    resource_desc, actions,
    before_snapshot, after_snapshot,
    reason, request_id, source_ip,
    created_at, updated_at
)
SELECT
    g.grant_no,
    g.id,
    'revoke',
    'system',
    'sql/20261015_purge_orphan_data_grants.sql',
    g.principal_type,
    g.principal_id,
    g.principal_name,
    CONCAT('instance/', g.instance_id),
    g.actions,
    CONCAT('effect=', g.effect, ';status=', g.status, ';source=', g.grant_source),
    'deleted(orphan)',
    '清理指向已删除数据源的孤儿授权',
    '', '',
    NOW(3), NOW(3)
FROM insight_das_data_grants g
LEFT JOIN insight_instances i ON i.instance_id = g.instance_id
WHERE i.instance_id IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM insight_das_grant_audit a
      WHERE a.grant_no = g.grant_no AND a.action = 'revoke'
  );

-- ---------------------------------------------------------------------------
-- STEP 2 删除孤儿授权行
--     MySQL 不支持在子查询里直接改同一张表，这里用 JOIN 形式改写
-- ---------------------------------------------------------------------------
DELETE g
FROM insight_das_data_grants g
LEFT JOIN insight_instances i ON i.instance_id = g.instance_id
WHERE i.instance_id IS NULL;

-- ---------------------------------------------------------------------------
-- STEP 3 复核：应为 0 行
-- ---------------------------------------------------------------------------
SELECT COUNT(*) AS orphan_left
FROM insight_das_data_grants g
LEFT JOIN insight_instances i ON i.instance_id = g.instance_id
WHERE i.instance_id IS NULL;

-- 存活实例的授权不受影响，用于对照
SELECT COUNT(*) AS live_grants_kept
FROM insight_das_data_grants g
JOIN insight_instances i ON i.instance_id = g.instance_id;
