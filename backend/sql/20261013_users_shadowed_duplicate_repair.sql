-- =============================================================================
-- 「同名软删除历史行」清理（insight_users）
--
-- 现场（2026-10-13，1.99）：
--   insight_users 里有两条 username='hongjie'：
--     uid=245（2026-08-17 建，后被软删 is_deleted=1 / is_active=0，角色绑定已清空）
--     uid=262（2026-10-13 用同名重建，绑定「普通成员」role_id=11，10 项权限全启用）
--   username 上**故意**不设唯一索引（见 20260925_users_uniq_uid_only.sql），
--   而历史上多处按 username 裸查且不带排序：InnoDB 全表扫描按主键序先读到 uid=245，
--   GORM 的 Scan/Take 又只取第一行 —— 于是「用户绑了角色，页面无权限项、接口 403、
--   首页统计全 0」。旧 uid=245 的 last_login 甚至被登录回写刷成了当天。
--
-- 代码侧修复（本次同步提交）：
--   所有 username → 用户行的解析统一收敛到 users/services.identity.go 的
--   ResolveCanonicalUser，口径与登录一致：未删除 → 已激活 → uid 最小；
--   登录回写、改密、改头像、权限解析、用户信息接口全部改按 uid 读写。
--
-- 本脚本的作用（数据侧卫生 + 现场即时缓解，不改任何业务数据）：
--   把「已被软删、且与一个正常同名账号并存」的历史行改名，
--   username → '原名#del#uid'，使任何仍按 username 裸查的老代码/老接口
--   都不再可能命中历史行（对**尚未重新发版**的环境，这是一条即时止血手段）。
--
-- 安全性：
--   1) 只改 is_deleted=1 的行的 username 字段，不动其它列、不动任何业务表；
--   2) 幂等：已带 '#del#' 后缀的行会被跳过，可重复执行；
--   3) 可回滚（见文末 STEP 4）；
--   4) 用户名列 varchar(32)，改写时按长度截断，不会因超长报错（1265）。
--
-- 执行记录：
--   [2026-10-13] 1.99（jaysql）已执行：uid=245 hongjie → hongjie#del#245
-- =============================================================================

-- ===================== STEP 1：执行前预览（只读） =====================
-- 1.1 当前所有同名记录（count > 1）
SELECT username, COUNT(*) AS total,
       SUM(is_deleted = 1) AS deleted_cnt,
       SUM(is_deleted = 0 AND is_active = 1) AS effective_cnt,
       GROUP_CONCAT(uid ORDER BY uid) AS uids
FROM insight_users
GROUP BY username
HAVING COUNT(*) > 1
ORDER BY username;

-- 1.2 将被改名的目标行（已被软删 + 存在正常同名账号 + 尚未带 '#del#' 后缀）
SELECT d.uid, d.username AS old_username, d.is_deleted, d.is_active,
       CONCAT(LEFT(d.username, GREATEST(1, 32 - CHAR_LENGTH(CONCAT('#del#', d.uid)))), '#del#', d.uid) AS new_username,
       a.uid AS effective_uid
FROM insight_users d
JOIN insight_users a
  ON a.username = d.username
 AND a.uid <> d.uid
 AND a.is_deleted = 0
 AND a.is_active = 1
WHERE d.is_deleted = 1
  AND d.username NOT LIKE '%#del#%';

-- ===================== STEP 2：改名（幂等） =====================
UPDATE insight_users d
JOIN insight_users a
  ON a.username = d.username
 AND a.uid <> d.uid
 AND a.is_deleted = 0
 AND a.is_active = 1
SET d.username = CONCAT(
        LEFT(d.username, GREATEST(1, 32 - CHAR_LENGTH(CONCAT('#del#', d.uid)))),
        '#del#',
        d.uid
    )
WHERE d.is_deleted = 1
  AND d.username NOT LIKE '%#del#%';

-- ===================== STEP 3：执行后复核 =====================
-- 3.1 应不再有「软删行与正常行同名」的组合（结果为空即达标）
SELECT d.uid, d.username, a.uid AS effective_uid, a.username AS effective_username
FROM insight_users d
JOIN insight_users a
  ON a.username = d.username
 AND a.uid <> d.uid
 AND a.is_deleted = 0
 AND a.is_active = 1
WHERE d.is_deleted = 1;

-- 3.2 目标用户的归属复核：uid / 是否超级管理员 / 角色绑定
SELECT u.uid, u.username, u.is_superuser, u.is_active, u.is_deleted,
       r.id AS role_id, r.name AS role_name, r.code AS role_code, r.is_builtin
FROM insight_users u
LEFT JOIN insight_user_roles ur ON ur.uid = u.uid
LEFT JOIN insight_roles r ON r.id = ur.role_id AND r.is_deleted = 0
WHERE u.username = 'hongjie';

-- 3.3 生效权限复核（应与角色在「角色管理」里勾选的一致；含后端虚拟补的 system.access）
SELECT DISTINCT p.code
FROM insight_permissions p
WHERE p.is_enabled = 1
  AND (
    EXISTS (SELECT 1 FROM insight_user_permissions up
             WHERE up.uid = (SELECT u.uid FROM insight_users u WHERE u.username = 'hongjie'
                              ORDER BY u.is_deleted ASC, u.is_active DESC, u.uid ASC LIMIT 1)
               AND up.permission_id = p.id)
    OR EXISTS (SELECT 1 FROM insight_user_roles ur
                 JOIN insight_roles r ON ur.role_id = r.id
                 JOIN insight_role_permissions rp ON ur.role_id = rp.role_id
                WHERE ur.uid = (SELECT u.uid FROM insight_users u WHERE u.username = 'hongjie'
                                 ORDER BY u.is_deleted ASC, u.is_active DESC, u.uid ASC LIMIT 1)
                  AND r.is_deleted = 0 AND rp.permission_id = p.id)
  )
ORDER BY p.code;

-- ===================== STEP 4：回滚（按需手工执行） =====================
-- 把被改名的历史行还原（示例：uid=245）
-- UPDATE insight_users
-- SET username = SUBSTRING_INDEX(username, '#del#', 1)
-- WHERE uid = 245 AND username LIKE '%#del#%';
