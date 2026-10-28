-- =============================================================================
-- 细粒度数据授权改造：建表 + 存量授权迁移
--
-- 背景
--   改造前：insight_das_schema_perms 只有「主体 + 实例 + 库」三维，
--           且读取路径会对 code='admin' 的角色隐式补齐「全部实例」授权
--           （das/services/principal.go 的 ensureSelfServiceGrantsForScope），
--           结果是管理员默认可见全部数据、且查不到「谁把权限授给了谁」。
--   改造后：insight_das_data_grants 为唯一权威，授权维度 =
--           主体（user/role） × 资源（instance/database/table） × 操作（select/export/dml），
--           附带授权人、生效时间、失效时间、状态；每次变更写 insight_das_grant_audit 流水。
--
-- 执行顺序（务必按 STEP 顺序，STEP 3/4 是这次改造的关键）
--   STEP 1  建表（幂等：IF NOT EXISTS；也可由服务启动时的 AutoMigrate 自动完成）
--   STEP 2  备份旧授权表
--   STEP 3  存量迁移：旧表 → 新表（幂等，可重复执行）
--   STEP 4  复核
--
-- 注意
--   * 迁移完成后新表非空 → 服务自动切到新模型（data_grant.go:dataGrantEnabled 判据）。
--     因此 STEP 3 是「切换开关」，执行即生效，30s 内缓存自然过期。
--   * 迁移不删除旧表数据（保留回退能力）；旧表不再参与判定。
--     若需要回退：DROP 新表数据即可让 dataGrantEnabled 变回 false。
-- =============================================================================

-- ========================== STEP 1：建表 ==========================

CREATE TABLE IF NOT EXISTS `insight_das_data_grants` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `grant_no`       VARCHAR(32)      NOT NULL DEFAULT '' COMMENT '授权单号',
  `principal_type` VARCHAR(16)      NOT NULL DEFAULT 'user' COMMENT '被授权主体类型 user/role',
  `principal_id`   BIGINT UNSIGNED  NOT NULL DEFAULT 0 COMMENT '被授权主体ID',
  `principal_name` VARCHAR(128)     NOT NULL DEFAULT '' COMMENT '被授权主体名称快照',
  `resource_level` VARCHAR(16)      NOT NULL DEFAULT 'instance' COMMENT '资源层级 instance/database/table',
  `instance_id`    CHAR(15)         NOT NULL DEFAULT '' COMMENT '数据源实例ID',
  `schema_name`    VARCHAR(255)     NOT NULL DEFAULT '' COMMENT '库/模式名',
  `table_name`     VARCHAR(128)     NOT NULL DEFAULT '' COMMENT '表名',
  `resource_type`  VARCHAR(32)      NOT NULL DEFAULT 'database' COMMENT '资源类型',
  `actions`        VARCHAR(255)     NOT NULL DEFAULT 'select' COMMENT '允许的操作类型,逗号分隔',
  `effect`         VARCHAR(16)      NOT NULL DEFAULT 'allow' COMMENT '效力 allow/deny',
  `effective_at`   DATETIME(3)      NULL COMMENT '生效时间',
  `expire_at`      DATETIME(3)      NULL COMMENT '失效时间,为空表示长期有效',
  `grantor_username` VARCHAR(64)    NOT NULL DEFAULT '' COMMENT '授权人账号',
  `grantor_name`     VARCHAR(128)   NOT NULL DEFAULT '' COMMENT '授权人名称快照',
  `grant_source`   VARCHAR(32)      NOT NULL DEFAULT 'manual' COMMENT '来源 manual/self/migrate/system',
  `status`         VARCHAR(16)      NOT NULL DEFAULT 'active' COMMENT '状态 active/revoked/expired',
  `reason`         VARCHAR(512)     NOT NULL DEFAULT '' COMMENT '授权事由',
  `revoked_at`     DATETIME(3)      NULL COMMENT '撤销时间',
  `revoked_by`     VARCHAR(64)      NOT NULL DEFAULT '' COMMENT '撤销人账号',
  `created_at`     DATETIME(3)      NULL COMMENT '创建时间',
  `updated_at`     DATETIME(3)      NULL COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_grant_no` (`grant_no`),
  KEY `idx_grant_principal` (`principal_type`, `principal_id`),
  KEY `idx_grant_resource` (`resource_level`, `instance_id`, `schema_name`, `table_name`),
  KEY `idx_grant_status` (`status`),
  KEY `idx_grant_expire` (`expire_at`),
  KEY `idx_created_at` (`created_at`),
  KEY `idx_updated_at` (`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='细粒度数据授权';

CREATE TABLE IF NOT EXISTS `insight_das_grant_audit` (
  `id`                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `grant_no`          VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '授权单号',
  `grant_id`          BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '授权记录ID',
  `action`            VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '动作 create/revoke/renew/expire',
  `operator_username` VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '操作人账号',
  `operator_name`     VARCHAR(128)    NOT NULL DEFAULT '' COMMENT '操作人名称快照',
  `principal_type`    VARCHAR(16)     NOT NULL DEFAULT '' COMMENT '被授权主体类型',
  `principal_id`      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '被授权主体ID',
  `principal_name`    VARCHAR(128)    NOT NULL DEFAULT '' COMMENT '被授权主体名称快照',
  `resource_desc`     VARCHAR(512)    NOT NULL DEFAULT '' COMMENT '授权资源描述',
  `actions`           VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '操作类型',
  `before_snapshot`   TEXT            NULL COMMENT '变更前快照JSON',
  `after_snapshot`    TEXT            NULL COMMENT '变更后快照JSON',
  `reason`            VARCHAR(512)    NOT NULL DEFAULT '' COMMENT '操作事由',
  `request_id`        VARCHAR(128)    NOT NULL DEFAULT '' COMMENT '请求ID',
  `source_ip`         VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '来源IP',
  `created_at`        DATETIME(3)     NULL COMMENT '创建时间',
  `updated_at`        DATETIME(3)     NULL COMMENT '更新时间',
  PRIMARY KEY (`id`),
  KEY `idx_audit_grant_no` (`grant_no`),
  KEY `idx_audit_grant_id` (`grant_id`),
  KEY `idx_audit_operator` (`operator_username`),
  KEY `idx_created_at` (`created_at`),
  KEY `idx_updated_at` (`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='数据授权变更审计流水';

-- ========================== STEP 2：备份旧授权表 ==========================
-- 迁移前留一份可对照的快照，便于回滚与人工核对。

CREATE TABLE IF NOT EXISTS `insight_das_schema_perms_bak_dgrant` AS
SELECT * FROM `insight_das_schema_perms`;

CREATE TABLE IF NOT EXISTS `insight_das_table_perms_bak_dgrant` AS
SELECT * FROM `insight_das_table_perms`;

SELECT COUNT(*) AS legacy_schema_perms, (SELECT COUNT(*) FROM `insight_das_table_perms_bak_dgrant`) AS legacy_table_perms
FROM `insight_das_schema_perms_bak_dgrant`;

-- ========================== STEP 3：存量迁移 ==========================
-- 语义映射：
--   旧 schema_perms 中 schema='*'（实例级）        → 新表 resource_level='instance'，actions='select'
--   旧 schema_perms 中 schema=<具体库>             → 新表 resource_level='database'，actions='select'
--   旧 table_perms 中 rule='allow'                 → 新表 resource_level='table'，actions='select'
--   旧 table_perms 中 rule='deny'                  → 新表 effect='deny'（deny 优先，语义一致）
--
-- 授权人记为 system、来源标 migrate、长期有效、状态 active：
-- 这是「把既成事实落成可审计的显式授权」，不是新的授权行为。
-- 幂等：靠 (主体, 资源, 效力) 三元组去重，可重复执行。

-- 3.1 实例级授权（旧 schema='*'）
INSERT INTO `insight_das_data_grants`
  (`grant_no`, `principal_type`, `principal_id`, `principal_name`,
   `resource_level`, `instance_id`, `schema_name`, `table_name`, `resource_type`,
   `actions`, `effect`, `effective_at`, `expire_at`,
   `grantor_username`, `grantor_name`, `grant_source`, `status`, `reason`,
   `created_at`, `updated_at`)
SELECT
  CONCAT('dg-', LOWER(SUBSTRING(MD5(CONCAT('mig-inst-', p.principal_type, '-', p.principal_id, '-', p.instance_id)), 1, 12))),
  p.principal_type, p.principal_id, p.principal_name,
  'instance', p.instance_id, '', '', 'database',
  'select', 'allow', NOW(3), NULL,
  'system', 'system(migrate)', 'migrate', 'active', '存量迁移：旧实例级授权',
  NOW(3), NOW(3)
FROM `insight_das_schema_perms` p
WHERE p.schema = '*'
  AND NOT EXISTS (
    SELECT 1 FROM `insight_das_data_grants` g
    WHERE g.principal_type = p.principal_type AND g.principal_id = p.principal_id
      AND g.resource_level = 'instance' AND g.instance_id = p.instance_id
      AND g.effect = 'allow'
  );

-- 3.2 库级授权（旧 schema 为具体库）
INSERT INTO `insight_das_data_grants`
  (`grant_no`, `principal_type`, `principal_id`, `principal_name`,
   `resource_level`, `instance_id`, `schema_name`, `table_name`, `resource_type`,
   `actions`, `effect`, `effective_at`, `expire_at`,
   `grantor_username`, `grantor_name`, `grant_source`, `status`, `reason`,
   `created_at`, `updated_at`)
SELECT
  CONCAT('dg-', LOWER(SUBSTRING(MD5(CONCAT('mig-db-', p.principal_type, '-', p.principal_id, '-', p.instance_id, '-', p.schema)), 1, 12))),
  p.principal_type, p.principal_id, p.principal_name,
  'database', p.instance_id, p.schema, '', IFNULL(NULLIF(p.resource_type, ''), 'database'),
  'select', 'allow', NOW(3), NULL,
  'system', 'system(migrate)', 'migrate', 'active', '存量迁移：旧库级授权',
  NOW(3), NOW(3)
FROM `insight_das_schema_perms` p
WHERE p.schema <> '*'
  AND NOT EXISTS (
    SELECT 1 FROM `insight_das_data_grants` g
    WHERE g.principal_type = p.principal_type AND g.principal_id = p.principal_id
      AND g.resource_level = 'database' AND g.instance_id = p.instance_id
      AND g.schema_name = p.schema AND g.effect = 'allow'
  );

-- 3.3 表级授权（旧 table_perms，rule 为 allow 与 deny 都迁）
INSERT INTO `insight_das_data_grants`
  (`grant_no`, `principal_type`, `principal_id`, `principal_name`,
   `resource_level`, `instance_id`, `schema_name`, `table_name`, `resource_type`,
   `actions`, `effect`, `effective_at`, `expire_at`,
   `grantor_username`, `grantor_name`, `grant_source`, `status`, `reason`,
   `created_at`, `updated_at`)
SELECT
  CONCAT('dg-', LOWER(SUBSTRING(MD5(CONCAT('mig-tbl-', t.principal_type, '-', t.principal_id, '-', t.instance_id, '-', t.schema, '-', t.`table`, '-', t.rule)), 1, 12))),
  t.principal_type, t.principal_id, t.principal_name,
  'table', t.instance_id, t.schema, t.`table`, 'table',
  'select', IF(t.rule = 'deny', 'deny', 'allow'), NOW(3), NULL,
  'system', 'system(migrate)', 'migrate', 'active', CONCAT('存量迁移：旧表级授权(', t.rule, ')'),
  NOW(3), NOW(3)
FROM `insight_das_table_perms` t
WHERE NOT EXISTS (
    SELECT 1 FROM `insight_das_data_grants` g
    WHERE g.principal_type = t.principal_type AND g.principal_id = t.principal_id
      AND g.resource_level = 'table' AND g.instance_id = t.instance_id
      AND g.schema_name = t.schema AND g.table_name = t.`table`
      AND g.effect = IF(t.rule = 'deny', 'deny', 'allow')
  );

-- 3.4 为迁移动作本身补一条审计流水（一次性，仅当还没有 migrate 流水时）
INSERT INTO `insight_das_grant_audit`
  (`grant_no`, `grant_id`, `action`, `operator_username`, `operator_name`,
   `principal_type`, `principal_id`, `principal_name`, `resource_desc`, `actions`,
   `before_snapshot`, `after_snapshot`, `reason`, `request_id`, `source_ip`,
   `created_at`, `updated_at`)
SELECT
  g.grant_no, g.id, 'create', 'system', 'system(migrate)',
  g.principal_type, g.principal_id, g.principal_name,
  CONCAT_WS(' / ', g.instance_id, NULLIF(g.schema_name, ''), NULLIF(g.table_name, '')),
  g.actions, '', '', '存量迁移：由旧授权表转为显式授权记录', '', '',
  NOW(3), NOW(3)
FROM `insight_das_data_grants` g
WHERE g.grant_source = 'migrate'
  AND NOT EXISTS (
    SELECT 1 FROM `insight_das_grant_audit` a WHERE a.grant_no = g.grant_no
  );

-- ========================== STEP 4：复核 ==========================

-- 4.1 迁移结果汇总
SELECT resource_level, effect, status, COUNT(*) AS cnt
FROM `insight_das_data_grants`
GROUP BY resource_level, effect, status
ORDER BY resource_level, effect;

-- 4.2 每个主体的授权条数（迁移后应能逐一对照旧表）
SELECT principal_type, principal_id, principal_name, COUNT(*) AS grant_count
FROM `insight_das_data_grants`
GROUP BY principal_type, principal_id, principal_name
ORDER BY grant_count DESC;

-- 4.3 审计流水条数（应等于来源为 migrate 的授权条数）
SELECT COUNT(*) AS audit_rows FROM `insight_das_grant_audit` WHERE operator_username = 'system';

-- 4.4 逐主体可见实例（改造后 admin 也仅能看到被显式授权的实例）
SELECT g.principal_type, g.principal_id, g.principal_name,
       COUNT(DISTINCT g.instance_id) AS visible_instances,
       GROUP_CONCAT(DISTINCT g.instance_id ORDER BY g.instance_id) AS instances
FROM `insight_das_data_grants` g
WHERE g.status = 'active' AND g.effect = 'allow'
  AND (g.expire_at IS NULL OR g.expire_at > NOW())
GROUP BY g.principal_type, g.principal_id, g.principal_name
ORDER BY visible_instances DESC;
