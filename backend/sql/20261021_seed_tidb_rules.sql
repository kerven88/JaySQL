-- =============================================================================
-- 补种「TiDB SQL 规范规则」（存量规范专用，共 86 条 = DDL 55 + DML 31）
--
-- 背景：
--   TiDB 此前没有独立规范目录：实例 db_type=TiDB 被 normalizeRuleDbType 归一到
--   mysql，规范页的「数据库类型」列看不到 TiDB，也无法按 TiDB 单独配置级别。
--   本次补齐后：
--     · standard_seed.go 追加 db_type=tidb 规则段，与 mysql 段严格一一镜像
--       （条数、rule_key 后缀、类别、默认级别、参数、规则类型、implemented 逐字段一致）；
--     · normalizeRuleDbType("TiDB") 返回 "tidb"，TiDB 实例只加载 tidb 规则；
--     · applyStandardRule 的每个 mysql.* case 同步补齐 tidb.* 分支，参数可正常装配；
--     · 前端规范页「数据库类型」列按能力项聚合后同时展示 MySQL 与 TiDB 两个 logo。
--
-- 为什么整目录补种（而不是只补缺失的）：
--   种子写入逻辑（InitializeDefaultSQLStandards）对已有规范是「有规则就跳过」，
--   存量环境永远拿不到新增的 tidb 规则；而 TiDB 引擎产出的每个 rule_key 都必须在
--   规范里有对应规则，否则预审结论会走「致命类别兜底」，被挂到同类别任意一条规则上。
--
-- 引擎口径：
--   TiDB 复用 MySQL 审核引擎（TiDB parser 即 MySQL 方言），rule_key 后缀与 mysql
--   完全一致，因此本脚本内容等价于 mysql 段的镜像，仅 rule_key 前缀改为 tidb.。
--
-- 关于 implemented=0 的条目：
--   与 mysql 段保持一致的目录项，引擎尚未实现对应检测（如 UPDATE 目标表/字段
--   存在性），Implemented=0 只表示「暂未接线」，不影响规则出现在规范页面。
--
-- 安全：
--   STEP 1 只读预览；STEP 2 为写入，整段可重复执行（NOT EXISTS 幂等）。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

-- 1.1 当前规范列表
SELECT id, name, env_type, enabled FROM insight_sql_standards ORDER BY id;

-- 1.2 每个规范缺失多少条 TiDB 规则（补种前）
SELECT s.id AS standard_id, s.name AS standard_name, COUNT(*) AS missing_count
FROM insight_sql_standards s
CROSS JOIN (

  SELECT 'tidb.struct.limit_auto_inc_col_name_is_id' AS rule_key
  UNION ALL SELECT 'tidb.struct.limit_auto_inc_col_is_unsigned'
  UNION ALL SELECT 'tidb.struct.limit_create_table_auto_inc_init'
  UNION ALL SELECT 'tidb.struct.limit_char_type_col_len'
  UNION ALL SELECT 'tidb.struct.limit_col_name_case'
  UNION ALL SELECT 'tidb.struct.col_name_no_keyword'
  UNION ALL SELECT 'tidb.struct.limit_col_must_have_comment'
  UNION ALL SELECT 'tidb.struct.limit_table_column_count'
  UNION ALL SELECT 'tidb.struct.limit_tbl_must_comment'
  UNION ALL SELECT 'tidb.struct.table_must_have_pk'
  UNION ALL SELECT 'tidb.struct.onlineddl_big_table_ddl_risk_detect'
  UNION ALL SELECT 'tidb.struct.limit_table_storage_engine'
  UNION ALL SELECT 'tidb.struct.limit_uniq_idx_name_fmt'
  UNION ALL SELECT 'tidb.struct.limit_varchar_type_col_len'
  UNION ALL SELECT 'tidb.struct.limit_col_must_have_default'
  UNION ALL SELECT 'tidb.struct.limit_col_not_null'
  UNION ALL SELECT 'tidb.struct.limit_col_no_partial_data_type'
  UNION ALL SELECT 'tidb.struct.limit_database_charset'
  UNION ALL SELECT 'tidb.struct.no_column_charset'
  UNION ALL SELECT 'tidb.struct.no_column_collate'
  UNION ALL SELECT 'tidb.struct.column_zerofill_forbidden'
  UNION ALL SELECT 'tidb.struct.disable_event_related_op'
  UNION ALL SELECT 'tidb.struct.disable_custom_function_related_op'
  UNION ALL SELECT 'tidb.struct.disable_proc_related_op'
  UNION ALL SELECT 'tidb.struct.disable_trigger_related_op'
  UNION ALL SELECT 'tidb.struct.disable_view_related_op'
  UNION ALL SELECT 'tidb.struct.disable_create_table_as'
  UNION ALL SELECT 'tidb.struct.disable_create_table_like'
  UNION ALL SELECT 'tidb.struct.sql_type_risk_control'
  UNION ALL SELECT 'tidb.struct.index_name_no_duplicate'
  UNION ALL SELECT 'tidb.struct.limit_tbl_idx_count'
  UNION ALL SELECT 'tidb.struct.limit_tbl_redundant_idx'
  UNION ALL SELECT 'tidb.struct.index_must_have_name'
  UNION ALL SELECT 'tidb.struct.dangerous_column_op_control'
  UNION ALL SELECT 'tidb.struct.limit_normal_idx_name_fmt'
  UNION ALL SELECT 'tidb.struct.limit_pk_col_must_auto_inc'
  UNION ALL SELECT 'tidb.struct.limit_pk_column_count'
  UNION ALL SELECT 'tidb.struct.limit_pk_col_type'
  UNION ALL SELECT 'tidb.struct.limit_single_index_column_count'
  UNION ALL SELECT 'tidb.struct.merge_multiple_alter_stmt_same_table'
  UNION ALL SELECT 'tidb.struct.tbl_charset_change_use_convert_syntax'
  UNION ALL SELECT 'tidb.struct.limit_tbl_charset'
  UNION ALL SELECT 'tidb.struct.limit_tbl_collation'
  UNION ALL SELECT 'tidb.struct.limit_tbl_name_case'
  UNION ALL SELECT 'tidb.struct.tbl_name_no_keyword'
  UNION ALL SELECT 'tidb.struct.limit_tbl_no_use_fk'
  UNION ALL SELECT 'tidb.struct.table_must_include_required_columns'
  UNION ALL SELECT 'tidb.query_update.limit_update_delete_affected_rows'
  UNION ALL SELECT 'tidb.query_update.delete_stmt_check_table_exists'
  UNION ALL SELECT 'tidb.query_update.limit_update_delete_join_table_count'
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_join_on_check'
  UNION ALL SELECT 'tidb.query_update.limit_update_delete_data_size'
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_suggest_where'
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_must_use_idx'
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_no_order_by_clause'
  UNION ALL SELECT 'tidb.query_update.insert_stmt_col_name_no_dup'
  UNION ALL SELECT 'tidb.query_update.insert_stmt_columns_match_values'
  UNION ALL SELECT 'tidb.query_update.insert_stmt_specify_columns'
  UNION ALL SELECT 'tidb.query_update.insert_stmt_not_null_column_null_value'
  UNION ALL SELECT 'tidb.query_update.insert_stmt_avoid_sysdate_func'
  UNION ALL SELECT 'tidb.query_update.limit_insert_values_row_count'
  UNION ALL SELECT 'tidb.query_update.insert_stmt_check_column_exists'
  UNION ALL SELECT 'tidb.query_update.update_multi_table_column_prefix_check'
  UNION ALL SELECT 'tidb.query_update.update_stmt_avoid_update_create_time_column'
  UNION ALL SELECT 'tidb.query_update.update_stmt_check_updates_pk'
  UNION ALL SELECT 'tidb.query_update.update_stmt_suggest_update_time_column'
  UNION ALL SELECT 'tidb.query_update.update_stmt_set_separator_check'
  UNION ALL SELECT 'tidb.query_update.update_stmt_check_column_exists'
  UNION ALL SELECT 'tidb.query_update.update_stmt_check_updates_unique_key'
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_where_subquery_check'
  UNION ALL SELECT 'tidb.query_update.limit_where_in_element_count'
  UNION ALL SELECT 'tidb.query_update.where_avoid_leading_wildcard'
  UNION ALL SELECT 'tidb.query_update.where_avoid_index_col_arithmetic_or_func'
  UNION ALL SELECT 'tidb.query_update.where_avoid_not_in_not_like'
  UNION ALL SELECT 'tidb.query_update.where_or_condition_check'
  UNION ALL SELECT 'tidb.query_update.where_implicit_type_cast_check'
  UNION ALL SELECT 'tidb.query_update.where_like_without_wildcard'
  UNION ALL SELECT 'tidb.struct.create_tbl_must_not_exist'
  UNION ALL SELECT 'tidb.struct.obj_tbl_must_exist'
  UNION ALL SELECT 'tidb.struct.obj_col_must_exist'
  UNION ALL SELECT 'tidb.struct.obj_idx_must_exist'
  UNION ALL SELECT 'tidb.struct.obj_col_must_not_exist'
  UNION ALL SELECT 'tidb.struct.obj_idx_must_not_exist'
  UNION ALL SELECT 'tidb.struct.obj_tbl_must_not_exist'
  UNION ALL SELECT 'tidb.struct.obj_view_must_not_exist'
  
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
)
GROUP BY s.id, s.name
ORDER BY s.id;

-- ========================== STEP 2：补种（幂等） ==========================

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, db_type, name, description,
   rule_type, level, base_level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, r.category, 'tidb', r.name, r.description,
  r.rule_type, r.level, r.level, r.params, r.sort_order, r.implemented
FROM insight_sql_standards s
CROSS JOIN (
  -- ---------- 结构：DDL（55 条） ----------
  SELECT 'tidb.struct.limit_auto_inc_col_name_is_id' AS rule_key,
         'structure' AS category, '限制自增列名为 ID' AS name,
         '限制自增列名字为 ID' AS description, '列' AS rule_type,
         'must' AS level, '{}' AS params, 0 AS sort_order, 1 AS implemented
  UNION ALL SELECT 'tidb.struct.limit_auto_inc_col_is_unsigned', 'structure', '限制自增列为无符号',
         '限制自增列为无符号', '列', 'must', '{}', 1, 1
  UNION ALL SELECT 'tidb.struct.limit_create_table_auto_inc_init', 'structure', '限制建表自增列初始值',
         '限制建表自增初始值', '表', 'must', '{}', 2, 0
  UNION ALL SELECT 'tidb.struct.limit_char_type_col_len', 'structure', '限制 CHAR 类型字段的长度',
         '限制 CHAR 类型字段长度', '列', 'must', '{"max_length": 64}', 3, 1
  UNION ALL SELECT 'tidb.struct.limit_col_name_case', 'structure', '限制字段名的大小写',
         '限制字段名大小写', '列', 'must', '{}', 4, 1
  UNION ALL SELECT 'tidb.struct.col_name_no_keyword', 'structure', '限制字段名不能是关键字',
         '字段名不能是关键字', '列', 'must', '{}', 5, 0
  UNION ALL SELECT 'tidb.struct.limit_col_must_have_comment', 'structure', '限制列必须有注释',
         '列要有注释', '列', 'must', '{}', 6, 1
  UNION ALL SELECT 'tidb.struct.limit_table_column_count', 'structure', '限制表字段数量上限',
         '限制表字段的数量', '表', 'must', '{}', 7, 1
  UNION ALL SELECT 'tidb.struct.limit_tbl_must_comment', 'structure', '限制表必须有注释',
         '表备注便于使用者快速了解业务', '表', 'must', '{}', 8, 0
  UNION ALL SELECT 'tidb.struct.table_must_have_pk', 'structure', '表必须有主键',
         '表要有主键', '表', 'must', '{}', 9, 1
  UNION ALL SELECT 'tidb.struct.limit_table_storage_engine', 'structure', '限制表存储引擎',
         '表引擎只支持在指定引擎中选择', '表', 'must', '{}', 11, 0
  UNION ALL SELECT 'tidb.struct.limit_uniq_idx_name_fmt', 'structure', '限制唯一索引命名格式',
         '限制唯一索引名格式', '索引', 'must', '{}', 12, 0
  UNION ALL SELECT 'tidb.struct.limit_varchar_type_col_len', 'structure', '限制 VARCHAR 类型字段的长度',
         '限制 VARCHAR 类型字段长度', '列', 'must', '{"max_length": 1024}', 13, 1
  UNION ALL SELECT 'tidb.struct.limit_col_must_have_default', 'structure', '限制列必须有默认值',
         '除 TEXT/BLOB/JSON/GEOMETRY 外，列需有默认值', '列', 'suggest', '{}', 14, 0
  UNION ALL SELECT 'tidb.struct.limit_col_not_null', 'structure', '限制列不可空（NOT NULL）',
         '除 TEXT/BLOB/JSON/GEOMETRY 外，列不可空（NOT NULL）', '列', 'suggest', '{}', 15, 1
  UNION ALL SELECT 'tidb.struct.limit_col_no_partial_data_type', 'structure', '限制列不能使用指定的数据类型',
         '建表或改表时禁止使用指定数据类型', '列', 'suggest', '{"types": ["double", "float", "enum", "bit", "set"]}', 16, 1
  UNION ALL SELECT 'tidb.struct.limit_database_charset', 'structure', '限制库的字符集',
         '限制库的字符集', '库', 'suggest', '{"charsets": ["utf8mb4", "utf8"]}', 17, 1
  UNION ALL SELECT 'tidb.struct.no_column_charset', 'structure', '限制列不能设置字符集',
         '不能显式设置列的字符集', '列', 'suggest', '{}', 18, 1
  UNION ALL SELECT 'tidb.struct.no_column_collate', 'structure', '限制列不能设置校对集',
         '不能显式设置列的校对集', '列', 'suggest', '{}', 19, 1
  UNION ALL SELECT 'tidb.struct.column_zerofill_forbidden', 'structure', '限制字段使用 Zerofill 属性',
         '字段禁用 Zerofill 属性', '列', 'suggest', '{}', 20, 1
  UNION ALL SELECT 'tidb.struct.disable_event_related_op', 'structure', '禁止事件相关操作',
         '禁止事件相关操作', '对象', 'suggest', '{}', 21, 0
  UNION ALL SELECT 'tidb.struct.disable_custom_function_related_op', 'structure', '禁止自定义函数相关操作',
         '禁止自定义函数相关操作', '对象', 'suggest', '{}', 22, 0
  UNION ALL SELECT 'tidb.struct.disable_proc_related_op', 'structure', '禁止存储过程相关操作',
         '禁止存储过程相关操作', '对象', 'suggest', '{}', 23, 0
  UNION ALL SELECT 'tidb.struct.disable_trigger_related_op', 'structure', '禁止触发器相关操作',
         '禁止触发器相关操作', '对象', 'suggest', '{}', 24, 0
  UNION ALL SELECT 'tidb.struct.disable_view_related_op', 'structure', '禁止视图相关操作',
         '禁止视图的创建、更新、删除等变更', '对象', 'suggest', '{}', 25, 1
  UNION ALL SELECT 'tidb.struct.disable_create_table_as', 'structure', '禁止 CREATE TABLE ... AS SELECT',
         '禁止使用 CTAS 语法建表：表结构来自 SELECT，字段/索引/约束/字符集等建表规范无法逐项校验', '对象', 'suggest', '{}', 25, 1
  UNION ALL SELECT 'tidb.struct.disable_create_table_like', 'structure', '禁止 CREATE TABLE ... LIKE',
         '禁止使用 CTL 语法复制表结构：表结构完全继承源表，字段/索引/约束/字符集等建表规范无法逐项校验', '对象', 'suggest', '{}', 25, 1
  UNION ALL SELECT 'tidb.struct.index_name_no_duplicate', 'structure', '限制索引名称不能重复',
         '单表内索引（普通/唯一/外键）名称不允许重复', '索引', 'suggest', '{}', 27, 0
  UNION ALL SELECT 'tidb.struct.limit_tbl_idx_count', 'structure', '限制表的索引数量',
         '表上允许创建的最大索引数量', '索引', 'suggest', '{}', 28, 0
  UNION ALL SELECT 'tidb.struct.limit_tbl_redundant_idx', 'structure', '限制冗余索引',
         '不允许创建冗余索引（同列组合重复或已被最左前缀覆盖）', '索引', 'suggest', '{}', 37, 1
  UNION ALL SELECT 'tidb.struct.index_must_have_name', 'structure', '限制索引必须设置名称',
         '限制索引要有名称', '索引', 'suggest', '{}', 29, 0
  UNION ALL SELECT 'tidb.struct.limit_normal_idx_name_fmt', 'structure', '限制普通索引命名格式',
         '限制普通索引名格式', '索引', 'suggest', '{}', 31, 0
  UNION ALL SELECT 'tidb.struct.limit_pk_col_must_auto_inc', 'structure', '限制主键列必须自增',
         '限制主键列必须自增', '索引', 'suggest', '{}', 32, 0
  UNION ALL SELECT 'tidb.struct.limit_pk_column_count', 'structure', '限制主键包含的列数',
         '限制主键包含列的个数', '索引', 'suggest', '{}', 33, 0
  UNION ALL SELECT 'tidb.struct.limit_pk_col_type', 'structure', '限制主键列的数据类型',
         '限制主键列类型', '索引', 'suggest', '{}', 34, 0
  UNION ALL SELECT 'tidb.struct.limit_single_index_column_count', 'structure', '限制单个索引包含的列数',
         '限制单个索引包含列的个数', '索引', 'suggest', '{}', 35, 0
  UNION ALL SELECT 'tidb.struct.merge_multiple_alter_stmt_same_table', 'structure', '同一张表的多条 ALTER 语句建议合为一条',
         '建议将同一张表的多条 DDL 合并，避免额外开销', '表', 'suggest', '{}', 36, 0
  UNION ALL SELECT 'tidb.struct.tbl_charset_change_use_convert_syntax', 'structure', '修改表字符集建议用 ALTER TABLE CONVERT 语法',
         '修改表字符集建议用 ALTER TABLE CONVERT 语法', '表', 'suggest', '{}', 37, 0
  UNION ALL SELECT 'tidb.struct.limit_tbl_charset', 'structure', '限制表字符集',
         '建表字符集只支持在指定字符集中选择', '表', 'suggest', '{}', 38, 0
  UNION ALL SELECT 'tidb.struct.limit_tbl_collation', 'structure', '限制表校验规则',
         '限制表校验规则', '表', 'suggest', '{}', 39, 0
  UNION ALL SELECT 'tidb.struct.limit_tbl_name_case', 'structure', '限制表名的大小写',
         '限制表名大小写', '表', 'suggest', '{}', 40, 1
  UNION ALL SELECT 'tidb.struct.tbl_name_no_keyword', 'structure', '限制表名不能是关键字',
         '表名不能是关键字', '表', 'suggest', '{}', 41, 0
  UNION ALL SELECT 'tidb.struct.limit_tbl_no_use_fk', 'structure', '限制表不能使用外键',
         '表不能使用外键', '表', 'suggest', '{}', 42, 0
  UNION ALL SELECT 'tidb.struct.table_must_include_required_columns', 'structure', '限制表必须包含指定列',
         '表需要包含某些列', '表', 'suggest', '{}', 43, 0
  UNION ALL SELECT 'tidb.struct.create_tbl_must_not_exist', 'structure', '限制建表的目标表不能已存在',
         '目标表已存在时 CREATE TABLE 执行必然失败（错误码 1050）', '表', 'must', '{}', 155, 1
  UNION ALL SELECT 'tidb.struct.obj_tbl_must_exist', 'structure', '限制操作的目标表必须存在',
         '表不存在时 ALTER/ANALYZE/RENAME/DELETE 执行必然失败（错误码 1146）', '表', 'must', '{}', 156, 1
  UNION ALL SELECT 'tidb.struct.obj_col_must_exist', 'structure', '限制操作的目标列必须存在',
         '列不存在时 MODIFY/CHANGE/DROP COLUMN 执行必然失败（错误码 1091）', '列', 'must', '{}', 157, 1
  UNION ALL SELECT 'tidb.struct.obj_idx_must_exist', 'structure', '限制操作的目标索引必须存在',
         '索引不存在时 DROP INDEX/RENAME INDEX 执行必然失败（错误码 1091）', '索引', 'must', '{}', 158, 1
  UNION ALL SELECT 'tidb.struct.obj_col_must_not_exist', 'structure', '限制改名的目标列名不能已存在',
         'CHANGE 的新列名已存在会产生列冲突，执行必然失败', '列', 'must', '{}', 159, 1
  UNION ALL SELECT 'tidb.struct.obj_idx_must_not_exist', 'structure', '限制改名的目标索引名不能已存在',
         'RENAME INDEX 的新索引名已存在会产生冲突，执行必然失败', '索引', 'must', '{}', 160, 1
  UNION ALL SELECT 'tidb.struct.obj_tbl_must_not_exist', 'structure', '限制改名的目标表名不能已存在',
         'RENAME TABLE 的新表名已存在会覆盖现有对象，执行必然失败', '表', 'must', '{}', 161, 1
  UNION ALL SELECT 'tidb.struct.obj_view_must_not_exist', 'structure', '限制创建视图的目标视图不能已存在',
         '视图已存在时 CREATE VIEW 执行必然失败，建议改用 CREATE OR REPLACE VIEW', '对象', 'must', '{}', 162, 1
  -- ---------- DML（31 条） ----------
  UNION ALL SELECT 'tidb.query_update.limit_update_delete_affected_rows', 'query_update', '限制 UPDATE/DELETE 单语句影响行数',
         '大批量更新/删除会暴涨事务日志、长时间锁大量数据，回滚代价高', '更新', 'must', '{"max_affected_rows":10000}', 44, 1
  UNION ALL SELECT 'tidb.query_update.delete_stmt_check_table_exists', 'query_update', 'DELETE 语句检测表是否存在',
         '检查 DELETE 涉及的表是否存在', '删除', 'suggest', '{}', 45, 1
  UNION ALL SELECT 'tidb.query_update.limit_update_delete_join_table_count', 'query_update', '限制 UPDATE/DELETE 语句关联表的数量',
         '多表关联易致性能下降与锁竞争', '更新', 'suggest', '{"max_join_tables":5}', 46, 1
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_join_on_check', 'query_update', 'UPDATE/DELETE 语句检测多表关联语法是否完整（JOIN 遗漏 ON 子句）',
         '缺少 ON 子句会产生笛卡尔积，可能误更新/删除大量数据', '更新', 'suggest', '{}', 47, 0
  UNION ALL SELECT 'tidb.query_update.limit_update_delete_data_size', 'query_update', '限制 UPDATE/DELETE 语句 LIMIT 的行数',
         '限制单次数据量可缩短锁持有时间，降低对性能的影响', '更新', 'suggest', '{"max_limit_rows":5}', 48, 1
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_suggest_where', 'query_update', 'UPDATE/DELETE 语句建议指定 WHERE 条件',
         '缺少 WHERE 会全表更新/删除，风险高；WHERE 可走索引减少数据量', '更新', 'suggest', '{}', 49, 0
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_must_use_idx', 'query_update', 'UPDATE/DELETE 语句必须使用索引',
         '走索引可加速定位，减少全表扫描与 I/O 开销', '更新', 'suggest', '{}', 50, 1
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_no_order_by_clause', 'query_update', 'UPDATE/DELETE 语句不能有 ORDER BY 子句',
         '排序对 UPDATE/DELETE 无意义，只会带来额外开销', '更新', 'suggest', '{}', 51, 0
  UNION ALL SELECT 'tidb.query_update.insert_stmt_col_name_no_dup', 'query_update', 'INSERT 语句字段名不能重复',
         '字段名重复会导致歧义并使 SQL 执行失败', '写入', 'suggest', '{}', 53, 1
  UNION ALL SELECT 'tidb.query_update.insert_stmt_columns_match_values', 'query_update', 'INSERT 语句字段列表与值列表要匹配',
         '字段与值数量不一致会直接报错（Error 1136），显式列表时逐行比对，无列表时与表结构比对', '写入', 'suggest', '{}', 54, 1
  UNION ALL SELECT 'tidb.query_update.insert_stmt_specify_columns', 'query_update', 'INSERT 语句建议指定字段列表',
         '显式指定字段列表更清晰，且表结构变更时语句不易失效', '写入', 'suggest', '{}', 55, 1
  UNION ALL SELECT 'tidb.query_update.insert_stmt_not_null_column_null_value', 'query_update', 'INSERT 语句不能为 NOT NULL 列插入 NULL 值',
         'NOT NULL 列必须给值，否则执行失败并易破坏数据完整性', '写入', 'suggest', '{}', 56, 1
  UNION ALL SELECT 'tidb.query_update.insert_stmt_avoid_sysdate_func', 'query_update', 'INSERT 语句不建议使用 SYSDATE() 函数',
         'SYSDATE 取服务器当前时间，同事务多次调用结果不同，且易致主备不一致', '写入', 'suggest', '{}', 57, 1
  UNION ALL SELECT 'tidb.query_update.limit_insert_values_row_count', 'query_update', '限制 INSERT 语句单条 VALUES 的行数',
         '大批量插入会暴涨事务日志、占用大量内存并加剧锁竞争', '写入', 'suggest', '{"max_insert_rows":1000}', 58, 1
  UNION ALL SELECT 'tidb.query_update.insert_stmt_check_column_exists', 'query_update', 'INSERT 语句检测目标表/字段是否存在',
         '目标表或字段不存在时执行必然失败（错误码 1146 / 1054），提交前必须拦截', '写入', 'suggest', '{}', 59, 1
  UNION ALL SELECT 'tidb.query_update.update_multi_table_column_prefix_check', 'query_update', 'UPDATE 多表时检测 SET 的列是否指定表前缀',
         '多表存在同名字段时，不指定表前缀易产生歧义', '更新', 'suggest', '{}', 75, 1
  UNION ALL SELECT 'tidb.query_update.update_stmt_avoid_update_create_time_column', 'query_update', 'UPDATE 语句不建议更新表上的“创建时间”列',
         '创建时间属数据元信息，更新会破坏审计追溯', '更新', 'suggest', '{"create_time_columns":"create_time"}', 76, 1
  UNION ALL SELECT 'tidb.query_update.update_stmt_check_updates_pk', 'query_update', 'UPDATE 语句检测是否更新了主键',
         '更新主键会触发索引重建并可能引发级联更新，影响性能', '更新', 'suggest', '{}', 77, 1
  UNION ALL SELECT 'tidb.query_update.update_stmt_suggest_update_time_column', 'query_update', 'UPDATE 语句建议更新表上的“修改时间”列',
         '修改时间便于审计、变更追踪与增量同步', '更新', 'suggest', '{"update_time_columns":"update_time"}', 78, 1
  UNION ALL SELECT 'tidb.query_update.update_stmt_set_separator_check', 'query_update', 'UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法）',
         'AND 是逻辑运算符，不能用作 SET 分隔符，否则语法异常', '更新', 'suggest', '{}', 79, 1
  UNION ALL SELECT 'tidb.query_update.update_stmt_check_column_exists', 'query_update', 'UPDATE 语句检测表/字段是否存在',
         'UPDATE 语句的目标表、字段不存在时执行必然失败（错误码 1146 / 1054）', '更新', 'suggest', '{}', 80, 0
  UNION ALL SELECT 'tidb.query_update.update_stmt_check_updates_unique_key', 'query_update', 'UPDATE 语句检测是否更新了唯一键',
         '更新唯一键会破坏外键关联与业务逻辑，易致数据不一致', '更新', 'suggest', '{}', 81, 1
  UNION ALL SELECT 'tidb.query_update.update_delete_stmt_where_subquery_check', 'query_update', 'UPDATE/DELETE 语句检测 WHERE 条件是否包含子查询',
         '子查询执行计划复杂、开销大，且拉长锁持有时间', '更新', 'suggest', '{}', 82, 0
  UNION ALL SELECT 'tidb.query_update.limit_where_in_element_count', 'query_update', '限制 WHERE 条件中 IN 子句的元素个数',
         'IN 元素过多难以命中索引，易触发全表扫描', '查询', 'suggest', '{"max_in_elements":1000}', 83, 1
  UNION ALL SELECT 'tidb.query_update.where_avoid_leading_wildcard', 'query_update', 'WHERE 条件中不建议使用前通配符查找',
         '前置 % 无法走前缀索引，必然全表扫描', '查询', 'suggest', '{}', 84, 1
  UNION ALL SELECT 'tidb.query_update.where_avoid_index_col_arithmetic_or_func', 'query_update', 'WHERE 条件中不建议索引字段包含数学运算或函数运算',
         '索引字段做函数/运算会失效，触发全表扫描', '查询', 'suggest', '{}', 85, 1
  UNION ALL SELECT 'tidb.query_update.where_avoid_not_in_not_like', 'query_update', 'WHERE 条件中不建议使用反向查询（NOT IN/NOT LIKE）',
         'NOT 类反向条件难命中索引，多为全表扫描', '查询', 'suggest', '{}', 86, 1
  UNION ALL SELECT 'tidb.query_update.where_or_condition_check', 'query_update', 'WHERE 条件中检测是否使用 OR 连接过滤条件',
         'OR 易致索引失效走全表扫描，条件恒真还有误操作风险', '查询', 'suggest', '{}', 87, 1
  UNION ALL SELECT 'tidb.query_update.where_implicit_type_cast_check', 'query_update', 'WHERE 条件中检测字段是否存在隐式类型转换',
         '隐式类型转换易致索引失效全表扫描，还可能比对出错', '查询', 'suggest', '{}', 88, 1
  UNION ALL SELECT 'tidb.query_update.where_like_without_wildcard', 'query_update', 'WHERE 条件中检测 LIKE 语句是否缺少通配符',
         '无通配符的 LIKE 等价于等值查询，用 = 更清晰且更易走索引', '查询', 'suggest', '{}', 89, 1
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);

-- ========================== STEP 3：复核 ==========================

-- 3.1 补种后每个规范应各持有 86 条 tidb 规则
SELECT standard_id, COUNT(*) AS rule_count
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'tidb.%'
GROUP BY standard_id;

-- 3.2 明细核对（类别分布：structure 55 / query_update 31）
SELECT standard_id, category, COUNT(*) AS cnt
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'tidb.%'
GROUP BY standard_id, category
ORDER BY standard_id, category;

-- 3.3 与 mysql 段逐条比对：不允许 tidb 独有条目（tidb_only_cnt 必须为 0）
-- 注意：若 mysql_cnt < 86，说明该存量规范的 mysql 段本身也不完整（早于相关规则
-- 加入的环境常见），此时 tidb_cnt 多于 mysql_cnt 属正常，需先补齐 mysql 段再复核本项。
SELECT
  t.standard_id,
  COALESCE(m.mysql_cnt, 0) AS mysql_cnt,
  t.tidb_cnt,
  COALESCE(o.tidb_only_cnt, 0) AS tidb_only_cnt
FROM (
  SELECT standard_id, COUNT(*) AS tidb_cnt
  FROM insight_sql_standard_rules
  WHERE rule_key LIKE 'tidb.%'
  GROUP BY standard_id
) t
LEFT JOIN (
  SELECT standard_id, COUNT(*) AS mysql_cnt
  FROM insight_sql_standard_rules
  WHERE rule_key LIKE 'mysql.%'
  GROUP BY standard_id
) m ON m.standard_id = t.standard_id
LEFT JOIN (
  SELECT t2.standard_id, COUNT(*) AS tidb_only_cnt
  FROM insight_sql_standard_rules t2
  WHERE t2.rule_key LIKE 'tidb.%'
    AND REPLACE(t2.rule_key, 'tidb.', 'mysql.') NOT IN (
      SELECT m2.rule_key FROM insight_sql_standard_rules m2
      WHERE m2.standard_id = t2.standard_id AND m2.rule_key LIKE 'mysql.%'
    )
  GROUP BY t2.standard_id
) o ON o.standard_id = t.standard_id
ORDER BY t.standard_id;

-- 3.4 规则类型（rule_type）不允许为空——「规则类型」列的唯一权威来源是库列
SELECT id, rule_key, rule_type
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'tidb.%' AND (rule_type IS NULL OR rule_type = '');

