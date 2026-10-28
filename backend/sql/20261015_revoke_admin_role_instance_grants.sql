-- ===========================================================================
-- 撤销「管理员角色（code='admin'）」的实例级自动授权
--
-- 背景
--   细粒度授权改造后，管理员角色仍被自动授予每个实例的全量访问权，来源有两处：
--     1) 迁移脚本 20261015_data_grant_fine_grained.sql：把旧表 role#12 的
--        instance 级行原样迁到 insight_das_data_grants（grant_source='migrate'）；
--     2) 代码 CreateDefaultInstanceGrants / grantInstanceImmediateAccess：
--        新建、编辑、批量导入数据源时，额外给 code='admin' 的角色补一条实例级授权
--        （已于 2026-10-15 代码层移除，本脚本清理的是**存量**）。
--
--   这与「未授权的数据对 admin 同样不可见」冲突：任何被加进管理员角色的人
--   会立刻获得全部实例的全操作权限，且查不到授权人。
--
--   另有一条「复活路径」必须一起掐掉：旧表 insight_das_schema_perms 里的
--   role#12 '*'/instance 种子行。只要旧表还有种子，任何一次执行迁移脚本
--   （20261015_data_grant_fine_grained.sql 的 STEP 3）或管理页对账
--   （ReconcileLegacyGrantsForPrincipal）都会把角色授权重新搬回新表。
--   STEP 3 就是干这个的，不要跳过。
--
-- 状态：已于 2026-10-15 23:5x 在 192.168.1.99/jaysql 执行
--   撤销新表 7 条（全部置 revoked，含 3 条指向已删实例的孤儿）+ 删除旧表 4 行种子，
--   写 7 条 revoke 审计流水。备份表：
--     insight_das_data_grants_bak_20261015   (7 行)
--     insight_das_schema_perms_bak_20261015  (4 行)
--   再次执行是幂等的（只处理 status='active'），可安全重跑。
--
-- 影响面（执行前必读，已实测核对）
--   管理员角色 role#12 实际成员只有 admin(uid=1)
--     （maoti(uid=258) 是 is_deleted=1 的历史账号且不属于任何角色，此前一次
--       JOIN 查询误报，已复核推翻）
--     - admin 本人的 user#1 授权已覆盖全部存活实例 → 撤销后**可见范围完全不变**
--   ⚠️ 若之后把新成员加进 role#12，他不会自动获得任何数据访问权，
--      需要显式授权时走管理端接口（管理端列表/创建不受数据授权过滤，不会死锁）：
--      POST /api/v1/admin/das/data-grants  {"principal_type":"user","principal_id":<uid>, ...}
--
-- 安全性
--   - 幂等：只处理 status='active' 的行，重复执行不会重复写审计
--   - 不删数据：新表只置 status='revoked' + revoked_at/revoked_by，保留证据
--   - 有回滚段（文件末尾，默认注释掉）
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- STEP 0 执行前核对（先跑这段，确认行数符合预期）
-- ---------------------------------------------------------------------------
SELECT '待撤销的新表授权' AS item, COUNT(*) AS cnt
FROM insight_das_data_grants
WHERE principal_type = 'role'
  AND principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
  AND status = 'active';

SELECT g.grant_no, g.instance_id, g.resource_level, g.actions, g.effect, g.grant_source
FROM insight_das_data_grants g
WHERE g.principal_type = 'role'
  AND g.principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
  AND g.status = 'active';

-- ---------------------------------------------------------------------------
-- STEP 1 撤销新表授权（置 revoked，不物理删除）
-- ---------------------------------------------------------------------------
UPDATE insight_das_data_grants
SET status     = 'revoked',
    revoked_at = NOW(3),
    revoked_by = 'sql/20261015_revoke_admin_role_instance_grants.sql',
    updated_at = NOW(3)
WHERE principal_type = 'role'
  AND principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
  AND status = 'active';

-- ---------------------------------------------------------------------------
-- STEP 2 写撤销审计流水（每条授权一行）
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
    'sql/20261015_revoke_admin_role_instance_grants.sql',
    g.principal_type,
    g.principal_id,
    g.principal_name,
    CONCAT('instance/', g.instance_id),
    g.actions,
    CONCAT('effect=', g.effect, ';status=active;source=', g.grant_source),
    'status=revoked',
    '收口管理员角色隐式全实例授权：未授权的数据对 admin 同样不可见',
    '',
    '',
    NOW(3), NOW(3)
FROM insight_das_data_grants g
WHERE g.principal_type = 'role'
  AND g.principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
  AND g.status = 'revoked'
  AND g.revoked_by = 'sql/20261015_revoke_admin_role_instance_grants.sql'
  -- 同一条授权只写一次流水（幂等）
  AND NOT EXISTS (
      SELECT 1 FROM insight_das_grant_audit a
      WHERE a.grant_no = g.grant_no AND a.action = 'revoke'
  );

-- ---------------------------------------------------------------------------
-- STEP 3 清理旧表兼容行
--   旧表 insight_das_schema_perms 没有 status 列，只能删除。
--   新模型已启用（dataGrantEnabled 判据 = 新表有记录），旧表不参与判定，
--   删除仅为避免后续再次迁移时被重新同步回来。
-- ---------------------------------------------------------------------------
DELETE FROM insight_das_schema_perms
WHERE principal_type = 'role'
  AND principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0);

-- ---------------------------------------------------------------------------
-- STEP 4 复核：管理员角色不应再有 active 授权；应 newly 写出 N 条 revoke 流水
-- ---------------------------------------------------------------------------
SELECT '撤销后仍 active' AS item, COUNT(*) AS cnt
FROM insight_das_data_grants
WHERE principal_type = 'role'
  AND principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
  AND status = 'active';

SELECT a.grant_no, a.principal_name, a.resource_desc, a.reason, a.created_at
FROM insight_das_grant_audit a
WHERE a.action = 'revoke'
  AND a.operator_name = 'sql/20261015_revoke_admin_role_instance_grants.sql';

-- 各成员在撤销后仍可见的实例数（确认 admin 不受影响、maoti 归零）
SELECT u.username,
       COUNT(DISTINCT g.instance_id) AS visible_instances
FROM insight_user_roles ur
JOIN insight_users u ON u.uid = ur.uid
JOIN insight_das_data_grants g
  ON (g.principal_type = 'user' AND g.principal_id = u.uid)
  OR (g.principal_type = 'role' AND g.principal_id = ur.role_id)
WHERE ur.role_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
  AND u.is_deleted = 0
  AND g.effect = 'allow'
  AND g.status = 'active'
  AND (g.expire_at IS NULL OR g.expire_at > NOW(3))
GROUP BY u.username;

-- ===========================================================================
-- 回滚段（默认注释；需要回退时单独执行）
--   仅恢复新表 status，审计流水保留不删（审计不可篡改是硬约束）
-- ===========================================================================
-- UPDATE insight_das_data_grants
-- SET status = 'active', revoked_at = NULL, revoked_by = '', updated_at = NOW(3)
-- WHERE principal_type = 'role'
--   AND principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
--   AND status = 'revoked'
--   AND revoked_by = 'sql/20261015_revoke_admin_role_instance_grants.sql';
--
-- INSERT IGNORE INTO insight_das_schema_perms
--   (principal_type, principal_id, principal_name, username, `schema`, resource_type, instance_id, created_at, updated_at)
-- SELECT 'role', g.principal_id, g.principal_name,
--        CONCAT('role#', g.principal_id), '*', 'instance', g.instance_id, NOW(3), NOW(3)
-- FROM insight_das_data_grants g
-- WHERE g.principal_type = 'role'
--   AND g.principal_id = (SELECT id FROM insight_roles WHERE code = 'admin' AND is_deleted = 0)
--   AND g.resource_level = 'instance';
