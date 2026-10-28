-- =============================================================================
-- 补种「对象存在性」规范规则（存量规范专用）
--
-- 背景：
--   引擎侧的对象存在性检查（建表目标已存在 / 表不存在 / 列不存在 / 索引不存在等）
--   历史上走裸 Warn，摘要不带 rule_key，导致预审判级时无法关联到规范规则，
--   只能按规范默认级别兜底 —— 「默认开发环境规范」must=2/suggest=60，兜底结果为
--   suggest（告警），于是「表已存在」这种执行必然失败的错误不阻断提交审批。
--
--   代码侧已修复：引擎改为 WarnFatal 携带 category + rule_key 输出，
--   且致命类别（语法错误/对象已存在/对象不存在）在规范中无对应规则时强制按 must 阻断。
--
-- 本脚本的作用：
--   把这些规则补进**已存在的规范**，让它们在规范列表中可见、可配置（可改级别、可关闭）。
--   注意：标准播种逻辑（InitializeDefaultSQLStandards）对已有规范是「有规则就跳过」，
--   不会自动补种新规则，因此存量环境必须跑本脚本。
--
-- 级别说明：
--   统一为 must。这类错误属于「执行必然失败」，不是风格偏好，
--   开发环境也不应降级为 suggest（降级会导致放行后执行阶段失败）。
--
-- 安全：
--   STEP 1 为只读预览，可直接执行；STEP 2 为写入，整段可重复执行（NOT EXISTS 幂等）。
--   执行前建议先建备份表（见 STEP 2 顶部）。
--
-- 执行记录：
--   [2026-10-13] 测试环境 192.168.1.99（库 jaysql）已执行 STEP2，
--   两个规范（id=1 开发 / id=2 生产）各补种 10 条，共 20 行，level=base_level=must。
--   备份表：insight_sql_standard_rules_bak_20261013（226 行）。
--   生产环境需在该机本地执行（远程 root 无白名单，直连报 1045）。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

-- 1.1 当前规范列表
SELECT id, name, env_type, enabled FROM insight_sql_standards ORDER BY id;

-- 1.2 待补种的规则（对照清单）
SELECT 'mysql.struct.create_tbl_must_not_exist'   AS rule_key, 'mysql'    AS db_type, '限制建表目标表不能已存在' AS name, 155 AS sort_order
UNION ALL SELECT 'mysql.struct.obj_tbl_must_exist',      'mysql',    '限制操作目标表必须存在',     156
UNION ALL SELECT 'mysql.struct.obj_col_must_exist',      'mysql',    '限制操作目标列必须存在',     157
UNION ALL SELECT 'mysql.struct.obj_idx_must_exist',      'mysql',    '限制操作目标索引必须存在',   158
UNION ALL SELECT 'mysql.struct.obj_col_must_not_exist',  'mysql',    '限制改名目标列不能已存在',   159
UNION ALL SELECT 'mysql.struct.obj_idx_must_not_exist',  'mysql',    '限制改名目标索引不能已存在', 160
UNION ALL SELECT 'mysql.struct.obj_tbl_must_not_exist',  'mysql',    '限制改名目标表不能已存在',   161
UNION ALL SELECT 'mysql.struct.obj_view_must_not_exist', 'mysql',    '限制创建视图目标不能已存在', 162
UNION ALL SELECT 'postgres.struct.create_tbl_must_not_exist', 'postgres', '限制建表目标表不能已存在', 163
UNION ALL SELECT 'postgres.struct.obj_tbl_must_exist',   'postgres', '限制操作目标表必须存在',     164;

-- 1.3 每个规范缺失哪些规则（补种前）
SELECT s.id AS standard_id, s.name AS standard_name, r.rule_key
FROM insight_sql_standards s
CROSS JOIN (
  SELECT 'mysql.struct.create_tbl_must_not_exist' AS rule_key
  UNION ALL SELECT 'mysql.struct.obj_tbl_must_exist'
  UNION ALL SELECT 'mysql.struct.obj_col_must_exist'
  UNION ALL SELECT 'mysql.struct.obj_idx_must_exist'
  UNION ALL SELECT 'mysql.struct.obj_col_must_not_exist'
  UNION ALL SELECT 'mysql.struct.obj_idx_must_not_exist'
  UNION ALL SELECT 'mysql.struct.obj_tbl_must_not_exist'
  UNION ALL SELECT 'mysql.struct.obj_view_must_not_exist'
  UNION ALL SELECT 'postgres.struct.create_tbl_must_not_exist'
  UNION ALL SELECT 'postgres.struct.obj_tbl_must_exist'
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
)
ORDER BY s.id, r.rule_key;

-- ========================== STEP 2：执行补种 ==========================
-- 已在测试环境验证通过。执行前先备份（幂等，重复执行不会重复插入）：
CREATE TABLE IF NOT EXISTS insight_sql_standard_rules_bak_20261013
  AS SELECT * FROM insight_sql_standard_rules;

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, db_type, name, description,
   rule_type, level, base_level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, 'structure', r.db_type, r.name, r.description,
  r.rule_type, 'must', 'must', '{}', r.sort_order, 1
FROM insight_sql_standards s
CROSS JOIN (
  SELECT 'mysql.struct.create_tbl_must_not_exist' AS rule_key, 'mysql' AS db_type,
         '限制建表目标表不能已存在' AS name,
         '目标表已存在时 CREATE TABLE 执行必然失败（错误码 1050）' AS description,
         '表' AS rule_type, 155 AS sort_order
  UNION ALL SELECT 'mysql.struct.obj_tbl_must_exist', 'mysql', '限制操作目标表必须存在',
         '表不存在时 ALTER/ANALYZE/RENAME/DELETE 执行必然失败（错误码 1146）', '表', 156
  UNION ALL SELECT 'mysql.struct.obj_col_must_exist', 'mysql', '限制操作目标列必须存在',
         '列不存在时 MODIFY/CHANGE/DROP COLUMN 执行必然失败（错误码 1091）', '列', 157
  UNION ALL SELECT 'mysql.struct.obj_idx_must_exist', 'mysql', '限制操作目标索引必须存在',
         '索引不存在时 DROP INDEX/RENAME INDEX 执行必然失败（错误码 1091）', '索引', 158
  UNION ALL SELECT 'mysql.struct.obj_col_must_not_exist', 'mysql', '限制改名目标列不能已存在',
         'CHANGE 的新列名已存在会产生列冲突，执行必然失败', '列', 159
  UNION ALL SELECT 'mysql.struct.obj_idx_must_not_exist', 'mysql', '限制改名目标索引不能已存在',
         'RENAME INDEX 的新索引名已存在会产生冲突，执行必然失败', '索引', 160
  UNION ALL SELECT 'mysql.struct.obj_tbl_must_not_exist', 'mysql', '限制改名目标表不能已存在',
         'RENAME TABLE 的新表名已存在会覆盖现有对象，执行必然失败', '表', 161
  UNION ALL SELECT 'mysql.struct.obj_view_must_not_exist', 'mysql', '限制创建视图目标不能已存在',
         '视图已存在时 CREATE VIEW 执行必然失败，建议改用 CREATE OR REPLACE VIEW', '表', 162
  UNION ALL SELECT 'postgres.struct.create_tbl_must_not_exist', 'postgres', '限制建表目标表不能已存在',
         '目标表已存在时 CREATE TABLE 执行必然失败（状态码 42P07）', '表', 163
  UNION ALL SELECT 'postgres.struct.obj_tbl_must_exist', 'postgres', '限制操作目标表必须存在',
         '表不存在时 ALTER/DELETE 执行必然失败（状态码 42P01）', '表', 164
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);

-- ========================== STEP 3：复核 ==========================

-- 3.1 补种后每个规范应各持有 10 条
SELECT standard_id, COUNT(*) AS rule_count
FROM insight_sql_standard_rules
WHERE rule_key IN (
  'mysql.struct.create_tbl_must_not_exist','mysql.struct.obj_tbl_must_exist',
  'mysql.struct.obj_col_must_exist','mysql.struct.obj_idx_must_exist',
  'mysql.struct.obj_col_must_not_exist','mysql.struct.obj_idx_must_not_exist',
  'mysql.struct.obj_tbl_must_not_exist','mysql.struct.obj_view_must_not_exist',
  'postgres.struct.create_tbl_must_not_exist','postgres.struct.obj_tbl_must_exist'
)
GROUP BY standard_id;

-- 3.2 明细核对
SELECT id, standard_id, rule_key, db_type, level, base_level, implemented, sort_order
FROM insight_sql_standard_rules
WHERE rule_key LIKE '%.obj_%' OR rule_key LIKE '%.create_tbl_must_not_exist'
ORDER BY standard_id, sort_order;

-- 3.2 若某条规则需要关闭（不再检查），可按需改为 off：
-- UPDATE insight_sql_standard_rules SET level='off'
--  WHERE rule_key='mysql.struct.create_tbl_must_not_exist' AND standard_id=1;
