-- =============================================================================
-- 补种「PostgreSQL DDL 对象级规则」（存量规范专用）
--
-- 背景：
--   平台这批 DDL 能力项此前只有 MySQL 版本，PG 工单的 DDL 语句得不到对应判定：
--     · 操作目标列 / 索引 / 表必须存在
--     · 改名的目标列名 / 索引名 / 表名不能已存在
--     · 建表的目标表不能已存在
--     · 禁止 CREATE TABLE ... AS SELECT（CTAS）
--     · 禁止 CREATE TABLE ... LIKE（PG 写法为括号内 LIKE）
--     · 禁止视图相关操作（CREATE/ALTER/DROP/REFRESH [MATERIALIZED] VIEW）
--     · 同一张表的多条 ALTER 建议合并为一条
--
--   引擎侧（checker/pg_object_rules.go、checker/pg_gate.go、checker/pg_rules.go）已按
--   PG 语法实现判定，但标准播种逻辑（InitializeDefaultSQLStandards）对已有规范是
--   「有规则就跳过」，不会自动补种新规则，因此存量环境必须跑本脚本，
--   规则才会出现在规范页面并可配置级别。
--
-- 为什么连 create_tbl_must_not_exist / obj_tbl_must_exist / disable_view_related_op
-- 也一起补：
--   这三条在种子目录里一直有，但早期规范初始化时目录里还没有它们，
--   属于「引擎会产出、规范里查不到」的规则键。缺了它们，预审结论会走
--   「致命类别兜底」，被挂到同类别里任意一条 DML 规则上（例如
--   postgres.query_update.update_stmt_check_column_exists），规则名与级别都对不上。
--
-- 说明：
--   · 未补种也不等于不检查：对象缺失/冲突/禁止语句都属于「致命类别」，
--     即便规范里没有对应规则，预审也会按 must 阻断提交。补种是为了「可见、可配置」。
--   · 对象存在性类统一 must（执行必然失败，不是风格偏好）；CTAS/CTL/合并建议为 suggest，
--     与 mysql.* 同名规则口径一致。
--
-- 安全：
--   STEP 1 只读预览；STEP 2 为写入，整段可重复执行（NOT EXISTS 幂等）。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

-- 1.1 当前规范列表
SELECT id, name, env_type, enabled FROM insight_sql_standards ORDER BY id;

-- 1.2 待补种的规则（对照清单，共 11 条）
SELECT 'postgres.struct.create_tbl_must_not_exist'              AS rule_key, '表'    AS rule_type, 'must'    AS level, 163 AS sort_order
UNION ALL SELECT 'postgres.struct.obj_tbl_must_exist',                    '表',    'must',    164
UNION ALL SELECT 'postgres.struct.obj_col_must_exist',                    '列',    'must',    165
UNION ALL SELECT 'postgres.struct.obj_idx_must_exist',                    '索引',  'must',    166
UNION ALL SELECT 'postgres.struct.obj_col_must_not_exist',                '列',    'must',    167
UNION ALL SELECT 'postgres.struct.obj_idx_must_not_exist',                '索引',  'must',    168
UNION ALL SELECT 'postgres.struct.obj_tbl_must_not_exist',                '表',    'must',    169
UNION ALL SELECT 'postgres.struct.disable_view_related_op',               '对象',  'suggest', 101
UNION ALL SELECT 'postgres.struct.disable_create_table_as',               '对象',  'suggest', 25
UNION ALL SELECT 'postgres.struct.disable_create_table_like',             '对象',  'suggest', 25
UNION ALL SELECT 'postgres.struct.merge_multiple_alter_stmt_same_table',  '表',    'suggest', 36;

-- 1.3 每个规范缺失哪些规则（补种前）
SELECT s.id AS standard_id, s.name AS standard_name, r.rule_key
FROM insight_sql_standards s
CROSS JOIN (
  SELECT 'postgres.struct.create_tbl_must_not_exist' AS rule_key
  UNION ALL SELECT 'postgres.struct.obj_tbl_must_exist'
  UNION ALL SELECT 'postgres.struct.obj_col_must_exist'
  UNION ALL SELECT 'postgres.struct.obj_idx_must_exist'
  UNION ALL SELECT 'postgres.struct.obj_col_must_not_exist'
  UNION ALL SELECT 'postgres.struct.obj_idx_must_not_exist'
  UNION ALL SELECT 'postgres.struct.obj_tbl_must_not_exist'
  UNION ALL SELECT 'postgres.struct.disable_view_related_op'
  UNION ALL SELECT 'postgres.struct.disable_create_table_as'
  UNION ALL SELECT 'postgres.struct.disable_create_table_like'
  UNION ALL SELECT 'postgres.struct.merge_multiple_alter_stmt_same_table'
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
)
ORDER BY s.id, r.rule_key;

-- ========================== STEP 2：执行补种 ==========================
-- 执行前先备份（幂等，重复执行不会重复插入）：
CREATE TABLE IF NOT EXISTS insight_sql_standard_rules_bak_20261021
  AS SELECT * FROM insight_sql_standard_rules;

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, db_type, name, description,
   rule_type, level, base_level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, 'structure', 'postgres', r.name, r.description,
  r.rule_type, r.level, r.level, '{}', r.sort_order, 1
FROM insight_sql_standards s
CROSS JOIN (
  SELECT 'postgres.struct.create_tbl_must_not_exist' AS rule_key,
         '限制建表的目标表不能已存在' AS name,
         '目标表已存在时 CREATE TABLE 执行必然失败（状态码 42P07）' AS description,
         '表' AS rule_type, 'must' AS level, 163 AS sort_order
  UNION ALL SELECT 'postgres.struct.obj_tbl_must_exist', '限制操作的目标表必须存在',
         '表不存在时 ALTER TABLE 执行必然失败（状态码 42P01）', '表', 'must', 164
  UNION ALL SELECT 'postgres.struct.obj_col_must_exist', '限制操作的目标列必须存在',
         '列不存在时 DROP COLUMN/ALTER COLUMN/RENAME COLUMN 执行必然失败（状态码 42703）', '列', 'must', 165
  UNION ALL SELECT 'postgres.struct.obj_idx_must_exist', '限制操作的目标索引必须存在',
         '索引不存在时 DROP INDEX/ALTER INDEX 执行必然失败（状态码 42704）', '索引', 'must', 166
  UNION ALL SELECT 'postgres.struct.obj_col_must_not_exist', '限制改名的目标列名不能已存在',
         'RENAME COLUMN 的新列名已存在会产生列名冲突，执行必然失败（状态码 42701）', '列', 'must', 167
  UNION ALL SELECT 'postgres.struct.obj_idx_must_not_exist', '限制改名的目标索引名不能已存在',
         'ALTER INDEX ... RENAME TO 的新索引名已存在会产生冲突，执行必然失败（状态码 42710）', '索引', 'must', 168
  UNION ALL SELECT 'postgres.struct.obj_tbl_must_not_exist', '限制改名的目标表名不能已存在',
         'ALTER TABLE ... RENAME TO 的新表名已存在会失败（状态码 42P07）', '表', 'must', 169
  UNION ALL SELECT 'postgres.struct.disable_view_related_op', '禁止视图相关操作',
         '禁止视图的创建、更新、删除等变更', '对象', 'suggest', 101
  UNION ALL SELECT 'postgres.struct.disable_create_table_as', '禁止 CREATE TABLE ... AS SELECT',
         '禁止使用 CTAS 语法建表：表结构来自 SELECT，字段/索引/约束等建表规范无法逐项校验', '对象', 'suggest', 25
  UNION ALL SELECT 'postgres.struct.disable_create_table_like', '禁止 CREATE TABLE ... LIKE',
         '禁止通过 `CREATE TABLE t (LIKE 源表)` 复制表结构：表结构继承自源表，建表规范无法逐项校验', '对象', 'suggest', 25
  UNION ALL SELECT 'postgres.struct.merge_multiple_alter_stmt_same_table', '同一张表的多条 ALTER 语句建议合为一条',
         '建议将同一张表的多条 DDL 合并，减少重复加锁与表重写开销', '表', 'suggest', 36
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);

-- ========================== STEP 3：复核 ==========================

-- 3.1 补种后每个规范应各持有 11 条
SELECT standard_id, COUNT(*) AS rule_count
FROM insight_sql_standard_rules
WHERE rule_key IN (
  'postgres.struct.create_tbl_must_not_exist',
  'postgres.struct.obj_tbl_must_exist',
  'postgres.struct.obj_col_must_exist',
  'postgres.struct.obj_idx_must_exist',
  'postgres.struct.obj_col_must_not_exist',
  'postgres.struct.obj_idx_must_not_exist',
  'postgres.struct.obj_tbl_must_not_exist',
  'postgres.struct.disable_view_related_op',
  'postgres.struct.disable_create_table_as',
  'postgres.struct.disable_create_table_like',
  'postgres.struct.merge_multiple_alter_stmt_same_table'
)
GROUP BY standard_id;

-- 3.2 明细核对
SELECT id, standard_id, rule_key, db_type, rule_type, level, base_level, implemented, sort_order
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'postgres.struct.%'
ORDER BY standard_id, sort_order;

-- 3.3 需要关闭某条规则时按需改级别：
-- UPDATE insight_sql_standard_rules SET level='off'
--  WHERE rule_key='postgres.struct.obj_idx_must_exist' AND standard_id=1;
