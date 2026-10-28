-- =============================================================================
-- 补种「禁止语句执行」规范规则（存量规范专用）
--
-- 背景：
--   平台对这三类语句执行「语句级准入」判定（命中即整条语句不进入规则链，
--   只产出一条语句级结论，不再报主键/引擎/依赖表等无意义结论）：
--
--     rule_key（MySQL）                          语句
--     mysql.struct.disable_create_table_as       CTAS：CREATE TABLE ... AS SELECT
--     mysql.struct.disable_create_table_like     CTL ：CREATE TABLE ... LIKE
--     mysql.struct.disable_view_related_op       CREATE VIEW
--
--   判定入口：backend/internal/inspect/checker/stmt.go 的 statementAdmissionGate。
--
--   **开关与级别全部由规范规则驱动**，不是在引擎里写死的策略：
--     规则开启（must）    => 命中即阻断提交（status = must_fix）
--     规则开启（suggest） => 命中仅提示（status = suggest）
--     规则关闭（off）      => 放行，语句正常进入规则链
--
--   但标准播种逻辑（InitializeDefaultSQLStandards）对已有规范是「有规则就跳过」，
--   不会自动补种新规则 —— 因此存量环境里 **CTAS / CTL 两条规则并不存在**：
--   用户在规范页面看不到它们，也就无法关闭（此时引擎默认禁止 + 致命类别兜底
--   => 一律阻断，属于「关不掉的禁止」）。本脚本把它们补进已存在的规范。
--
--   CREATE VIEW 对应的 mysql.struct.disable_view_related_op 是规则目录中的既有条目，
--   存量规范通常已有；这里一并做幂等补齐，避免个别规范遗漏。
--
-- 级别说明：
--   与种子口径保持一致：生产规范（env_type='prod'）=> must，其它（开发）=> suggest。
--   若需要统一为 must（例如要求开发环境也不得执行），改 STEP 2 的 CASE 表达式，
--   或补种后按 STEP 3 的 UPDATE 示例逐条调整。
--
-- 安全：
--   STEP 1 为只读预览，可直接执行；STEP 2 为写入，整段可重复执行（NOT EXISTS 幂等）。
--   执行前自动建备份表 insight_sql_standard_rules_bak_20261014。
--
-- 执行记录：
--   [待执行] 测试环境 192.168.1.99（库 jaysql）与本机 dev 库各自执行；
--            生产环境需在该机本地执行（远程 root 无白名单）。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

-- 1.1 当前规范列表
SELECT id, name, env_type, enabled FROM insight_sql_standards ORDER BY id;

-- 1.2 每个规范缺失哪些规则（补种前）
SELECT s.id AS standard_id, s.name AS standard_name, s.env_type, r.rule_key
FROM insight_sql_standards s
CROSS JOIN (
            SELECT 'mysql.struct.disable_create_table_as' AS rule_key
  UNION ALL SELECT 'mysql.struct.disable_create_table_like'
  UNION ALL SELECT 'mysql.struct.disable_view_related_op'
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
)
ORDER BY s.id, r.rule_key;

-- ========================== STEP 2：执行补种 ==========================

CREATE TABLE IF NOT EXISTS insight_sql_standard_rules_bak_20261014
  AS SELECT * FROM insight_sql_standard_rules;

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, rule_type, db_type, name, description,
   level, base_level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, 'structure', r.rule_type, r.db_type, r.name, r.description,
  CASE WHEN s.env_type = 'prod' THEN 'must' ELSE 'suggest' END,
  CASE WHEN s.env_type = 'prod' THEN 'must' ELSE 'suggest' END,
  '{}', r.sort_order, 1
FROM insight_sql_standards s
CROSS JOIN (
            SELECT 'mysql.struct.disable_create_table_as' AS rule_key, 'mysql' AS db_type,
                   '禁止 CREATE TABLE ... AS SELECT' AS name,
                   '禁止使用 CTAS 语法建表：表结构来自 SELECT，字段/索引/约束/字符集等建表规范无法逐项校验' AS description,
                   '对象' AS rule_type, 25 AS sort_order
  UNION ALL SELECT 'mysql.struct.disable_create_table_like', 'mysql',
                   '禁止 CREATE TABLE ... LIKE',
                   '禁止使用 CTL 语法复制表结构：表结构完全继承源表，字段/索引/约束/字符集等建表规范无法逐项校验',
                   '对象', 25
  UNION ALL SELECT 'mysql.struct.disable_view_related_op', 'mysql',
                   '禁止视图相关操作',
                   '禁止视图的创建、更新、删除等变更',
                   '对象', 25
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);

-- ========================== STEP 3：复核 ==========================

-- 3.1 补种结果（每个规范应各有 3 条）
SELECT standard_id, rule_key, db_type, level, base_level, implemented, sort_order
FROM insight_sql_standard_rules
WHERE rule_key IN (
  'mysql.struct.disable_create_table_as',
  'mysql.struct.disable_create_table_like',
  'mysql.struct.disable_view_related_op'
)
ORDER BY standard_id, sort_order, rule_key;

-- 3.2 统计每个规范补齐了几条（应为 3）
SELECT standard_id, COUNT(*) AS rule_count
FROM insight_sql_standard_rules
WHERE rule_key IN (
  'mysql.struct.disable_create_table_as',
  'mysql.struct.disable_create_table_like',
  'mysql.struct.disable_view_related_op'
)
GROUP BY standard_id;

-- 3.3 按需调整级别 / 关闭（示例，默认不执行）
-- 开发规范也想强制阻断：
-- UPDATE insight_sql_standard_rules SET level='must', base_level='must'
--  WHERE rule_key='mysql.struct.disable_create_table_as' AND standard_id=1;
-- 允许某规范执行 CTAS（等价于在规范页面把规则关掉）：
-- UPDATE insight_sql_standard_rules SET level='off'
--  WHERE rule_key='mysql.struct.disable_create_table_as' AND standard_id=1;
