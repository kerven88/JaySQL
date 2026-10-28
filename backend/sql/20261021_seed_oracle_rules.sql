-- =============================================================================
-- 补种「Oracle SQL 规范规则」（存量规范专用，共 57 条 = DDL 37 + DML 20）
--
-- 背景：
--   Oracle 工单此前完全不支持 SQL 规范预审：inspect 引擎 Run() 只分发 MySQL/PG，
--   orders 侧也不放行 Oracle 数据源的工单类型。本次补齐后：
--     · 引擎（checker/oracle_parse.go / oracle_gate.go / oracle_rules.go /
--       oracle_dml.go / oracle_object_rules.go）按「语法感知拆分 + 轻量文本解析」
--       实现 Oracle 判定，连库规则走 all_* 数据字典（go-ora 驱动）；
--     · 标准播种逻辑（InitializeDefaultSQLStandards）对已有规范是「有规则就跳过」，
--       存量环境必须跑本脚本，规则才会出现在规范页面并可配置级别。
--
-- 为什么必须整目录补种（而不是只补缺失的）：
--   Oracle 引擎产出的每个 rule_key 都必须在规范里有对应规则，否则预审结论会走
--   「致命类别兜底」，被挂到同致命类别里任意一条规则上（PG 轮实测踩坑）。
--
-- 未收录的两条目录规则（避免幽灵规则）：
--   · update_delete_stmt_must_use_idx：需要 EXPLAIN PLAN 连库判定，暂不支持；
--   · update_delete_stmt_no_order_by_clause：Oracle 引擎未实现该检查。
--
-- Oracle 特有口径：
--   · 未加引号的标识符折叠为大写 ⇒ 大小写两条规则默认要求大写（引擎按
--     rule.DbType 分支；规范里可配 name_case=lower 降级）；
--   · 列/表注释无内联语法 ⇒ 必须在同工单内提供 COMMENT ON 语句；
--   · 自增列是 IDENTITY 列，自增初值对应 START WITH；
--   · 无 UPDATE/DELETE 的 LIMIT 语法（出现即必然失败，ORA-00933）。
--
-- 安全：
--   STEP 1 只读预览；STEP 2 为写入，整段可重复执行（NOT EXISTS 幂等）。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

-- 1.1 当前规范列表
SELECT id, name, env_type, enabled FROM insight_sql_standards ORDER BY id;

-- 1.2 每个规范缺失多少条 Oracle 规则（补种前）
SELECT s.id AS standard_id, s.name AS standard_name, COUNT(*) AS missing_count
FROM insight_sql_standards s
JOIN (
  SELECT 'oracle.struct.limit_auto_inc_col_name_is_id' AS rule_key
  UNION ALL SELECT 'oracle.struct.limit_create_table_auto_inc_init'
  UNION ALL SELECT 'oracle.struct.limit_char_type_col_len'
  UNION ALL SELECT 'oracle.struct.limit_col_name_case'
  UNION ALL SELECT 'oracle.struct.col_name_no_keyword'
  UNION ALL SELECT 'oracle.struct.limit_col_must_have_comment'
  UNION ALL SELECT 'oracle.struct.limit_table_column_count'
  UNION ALL SELECT 'oracle.struct.limit_tbl_must_comment'
  UNION ALL SELECT 'oracle.struct.table_must_have_pk'
  UNION ALL SELECT 'oracle.struct.limit_uniq_idx_name_fmt'
  UNION ALL SELECT 'oracle.struct.limit_varchar_type_col_len'
  UNION ALL SELECT 'oracle.struct.limit_col_must_have_default'
  UNION ALL SELECT 'oracle.struct.limit_col_not_null'
  UNION ALL SELECT 'oracle.struct.limit_col_no_partial_data_type'
  UNION ALL SELECT 'oracle.struct.disable_view_related_op'
  UNION ALL SELECT 'oracle.struct.disable_create_table_as'
  UNION ALL SELECT 'oracle.struct.disable_create_table_like'
  UNION ALL SELECT 'oracle.struct.limit_tbl_idx_count'
  UNION ALL SELECT 'oracle.struct.index_must_have_name'
  UNION ALL SELECT 'oracle.struct.limit_normal_idx_name_fmt'
  UNION ALL SELECT 'oracle.struct.limit_pk_col_must_auto_inc'
  UNION ALL SELECT 'oracle.struct.limit_pk_column_count'
  UNION ALL SELECT 'oracle.struct.limit_pk_col_type'
  UNION ALL SELECT 'oracle.struct.limit_single_index_column_count'
  UNION ALL SELECT 'oracle.struct.merge_multiple_alter_stmt_same_table'
  UNION ALL SELECT 'oracle.struct.limit_tbl_redundant_idx'
  UNION ALL SELECT 'oracle.struct.limit_tbl_name_case'
  UNION ALL SELECT 'oracle.struct.tbl_name_no_keyword'
  UNION ALL SELECT 'oracle.struct.limit_tbl_no_use_fk'
  UNION ALL SELECT 'oracle.struct.table_must_include_required_columns'
  UNION ALL SELECT 'oracle.struct.create_tbl_must_not_exist'
  UNION ALL SELECT 'oracle.struct.obj_tbl_must_exist'
  UNION ALL SELECT 'oracle.struct.obj_col_must_exist'
  UNION ALL SELECT 'oracle.struct.obj_idx_must_exist'
  UNION ALL SELECT 'oracle.struct.obj_col_must_not_exist'
  UNION ALL SELECT 'oracle.struct.obj_idx_must_not_exist'
  UNION ALL SELECT 'oracle.struct.obj_tbl_must_not_exist'
  UNION ALL SELECT 'oracle.query_update.limit_update_delete_affected_rows'
  UNION ALL SELECT 'oracle.query_update.delete_stmt_check_table_exists'
  UNION ALL SELECT 'oracle.query_update.limit_update_delete_join_table_count'
  UNION ALL SELECT 'oracle.query_update.update_delete_stmt_join_on_check'
  UNION ALL SELECT 'oracle.query_update.limit_update_delete_data_size'
  UNION ALL SELECT 'oracle.query_update.update_delete_stmt_suggest_where'
  UNION ALL SELECT 'oracle.query_update.insert_stmt_col_name_no_dup'
  UNION ALL SELECT 'oracle.query_update.insert_stmt_columns_match_values'
  UNION ALL SELECT 'oracle.query_update.insert_stmt_specify_columns'
  UNION ALL SELECT 'oracle.query_update.insert_stmt_not_null_column_null_value'
  UNION ALL SELECT 'oracle.query_update.insert_stmt_avoid_sysdate_func'
  UNION ALL SELECT 'oracle.query_update.limit_insert_values_row_count'
  UNION ALL SELECT 'oracle.query_update.insert_stmt_check_column_exists'
  UNION ALL SELECT 'oracle.query_update.limit_where_in_element_count'
  UNION ALL SELECT 'oracle.query_update.update_multi_table_column_prefix_check'
  UNION ALL SELECT 'oracle.query_update.update_stmt_avoid_update_create_time_column'
  UNION ALL SELECT 'oracle.query_update.update_stmt_check_updates_pk'
  UNION ALL SELECT 'oracle.query_update.update_stmt_suggest_update_time_column'
  UNION ALL SELECT 'oracle.query_update.update_stmt_set_separator_check'
  UNION ALL SELECT 'oracle.query_update.update_stmt_check_column_exists'
) r ON 1 = 1
LEFT JOIN insight_sql_standard_rules x
  ON x.standard_id = s.id AND x.rule_key = r.rule_key
WHERE x.id IS NULL
GROUP BY s.id, s.name
ORDER BY s.id;

-- ========================== STEP 2：执行补种 ==========================
-- 执行前先备份（幂等，重复执行不会重复插入）：
CREATE TABLE IF NOT EXISTS insight_sql_standard_rules_bak_20261021
  AS SELECT * FROM insight_sql_standard_rules;

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, db_type, name, description,
   rule_type, level, base_level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, r.category, 'oracle', r.name, r.description,
  r.rule_type, r.level, r.level, r.params, r.sort_order, 1
FROM insight_sql_standards s
CROSS JOIN (
  -- ---------- 结构：DDL（37 条） ----------
  SELECT 'oracle.struct.limit_auto_inc_col_name_is_id' AS rule_key,
         'structure' AS category, '限制自增列名为 ID' AS name,
         '限制自增列名字为 ID（GENERATED ... AS IDENTITY）' AS description,
         '列' AS rule_type, 'must' AS level, '{}' AS params, 0 AS sort_order
  UNION ALL SELECT 'oracle.struct.limit_create_table_auto_inc_init', 'structure', '限制建表自增列初始值',
         '限制建表自增初始值（IDENTITY 的 START WITH）', '表', 'must', '{}', 2
  UNION ALL SELECT 'oracle.struct.limit_char_type_col_len', 'structure', '限制 CHAR 类型字段的长度',
         '限制 CHAR 类型字段长度', '列', 'must', '{"max_length": 64}', 3
  UNION ALL SELECT 'oracle.struct.limit_col_name_case', 'structure', '限制字段名的大小写',
         '限制字段名大小写（Oracle 默认折叠为大写）', '列', 'must', '{}', 4
  UNION ALL SELECT 'oracle.struct.col_name_no_keyword', 'structure', '限制字段名不能是关键字',
         '字段名不能是关键字', '列', 'must', '{}', 5
  UNION ALL SELECT 'oracle.struct.limit_col_must_have_comment', 'structure', '限制列必须有注释',
         '列要有注释（Oracle 无内联 COMMENT 语法，需同工单内 COMMENT ON COLUMN 语句）', '列', 'must', '{}', 6
  UNION ALL SELECT 'oracle.struct.limit_table_column_count', 'structure', '限制表字段数量上限',
         '限制表字段的数量', '表', 'must', '{}', 7
  UNION ALL SELECT 'oracle.struct.limit_tbl_must_comment', 'structure', '限制表必须有注释',
         '表备注便于使用者快速了解业务（需同工单内 COMMENT ON TABLE 语句）', '表', 'must', '{}', 8
  UNION ALL SELECT 'oracle.struct.table_must_have_pk', 'structure', '表必须有主键',
         '表要有主键', '表', 'must', '{}', 9
  UNION ALL SELECT 'oracle.struct.limit_uniq_idx_name_fmt', 'structure', '限制唯一索引命名格式',
         '限制唯一索引名格式', '索引', 'must', '{}', 12
  UNION ALL SELECT 'oracle.struct.limit_varchar_type_col_len', 'structure', '限制 VARCHAR 类型字段的长度',
         '限制 VARCHAR 类型字段长度', '列', 'must', '{"max_length": 1024}', 13
  UNION ALL SELECT 'oracle.struct.limit_col_must_have_default', 'structure', '限制列必须有默认值',
         '除 CLOB/BLOB/NCLOB 外，列需有默认值', '列', 'suggest', '{}', 14
  UNION ALL SELECT 'oracle.struct.limit_col_not_null', 'structure', '限制列不可空（NOT NULL）',
         '除 CLOB/BLOB/NCLOB 外，列不可空（NOT NULL）', '列', 'suggest', '{}', 15
  UNION ALL SELECT 'oracle.struct.limit_col_no_partial_data_type', 'structure', '限制列不能使用指定的数据类型',
         '建表或改表时禁止使用指定数据类型（如 LONG / LONG RAW 等淘汰类型）', '列', 'suggest', '{"types": ["long", "long raw"]}', 16
  UNION ALL SELECT 'oracle.struct.disable_view_related_op', 'structure', '禁止视图相关操作',
         '禁止视图/物化视图的创建、更新、删除等变更', '对象', 'suggest', '{}', 25
  UNION ALL SELECT 'oracle.struct.disable_create_table_as', 'structure', '禁止 CREATE TABLE ... AS SELECT',
         '禁止使用 CTAS 语法建表：表结构来自 SELECT，字段/索引/约束等建表规范无法逐项校验', '对象', 'suggest', '{}', 25
  UNION ALL SELECT 'oracle.struct.disable_create_table_like', 'structure', '禁止 CREATE TABLE ... LIKE',
         'Oracle 不支持 CREATE TABLE ... LIKE 语法，该写法执行必然失败（ORA-00907）', '对象', 'suggest', '{}', 25
  UNION ALL SELECT 'oracle.struct.limit_tbl_idx_count', 'structure', '限制表的索引数量',
         '表上允许创建的最大索引数量', '索引', 'suggest', '{}', 28
  UNION ALL SELECT 'oracle.struct.index_must_have_name', 'structure', '限制索引必须设置名称',
         '限制索引要有名称', '索引', 'suggest', '{}', 29
  UNION ALL SELECT 'oracle.struct.limit_normal_idx_name_fmt', 'structure', '限制普通索引命名格式',
         '限制普通索引名格式', '索引', 'suggest', '{}', 31
  UNION ALL SELECT 'oracle.struct.limit_pk_col_must_auto_inc', 'structure', '限制主键列必须自增',
         '限制主键列必须自增（GENERATED ... AS IDENTITY）', '索引', 'suggest', '{}', 32
  UNION ALL SELECT 'oracle.struct.limit_pk_column_count', 'structure', '限制主键包含的列数',
         '限制主键包含列的个数', '索引', 'suggest', '{}', 33
  UNION ALL SELECT 'oracle.struct.limit_pk_col_type', 'structure', '限制主键列的数据类型',
         '限制主键列类型（Oracle 默认仅允许 NUMBER）', '索引', 'suggest', '{}', 34
  UNION ALL SELECT 'oracle.struct.limit_single_index_column_count', 'structure', '限制单个索引包含的列数',
         '限制单个索引包含列的个数', '索引', 'suggest', '{}', 35
  UNION ALL SELECT 'oracle.struct.merge_multiple_alter_stmt_same_table', 'structure', '同一张表的多条 ALTER 语句建议合为一条',
         '建议将同一张表的多条 DDL 合并，减少重复加锁与表重写开销', '表', 'suggest', '{}', 36
  UNION ALL SELECT 'oracle.struct.limit_tbl_redundant_idx', 'structure', '限制冗余索引',
         '不允许创建冗余索引（同列组合重复或已被最左前缀覆盖）', '索引', 'suggest', '{}', 37
  UNION ALL SELECT 'oracle.struct.limit_tbl_name_case', 'structure', '限制表名的大小写',
         '限制表名大小写（Oracle 默认折叠为大写）', '表', 'suggest', '{}', 40
  UNION ALL SELECT 'oracle.struct.tbl_name_no_keyword', 'structure', '限制表名不能是关键字',
         '表名不能是关键字', '表', 'suggest', '{}', 41
  UNION ALL SELECT 'oracle.struct.limit_tbl_no_use_fk', 'structure', '限制表不能使用外键',
         '表不能使用外键', '表', 'suggest', '{}', 42
  UNION ALL SELECT 'oracle.struct.table_must_include_required_columns', 'structure', '限制表必须包含指定列',
         '表需要包含某些列', '表', 'suggest', '{}', 43
  UNION ALL SELECT 'oracle.struct.create_tbl_must_not_exist', 'structure', '限制建表的目标表不能已存在',
         '目标表已存在时 CREATE TABLE 执行必然失败（ORA-00955）', '表', 'must', '{}', 175
  UNION ALL SELECT 'oracle.struct.obj_tbl_must_exist', 'structure', '限制操作的目标表必须存在',
         '表不存在时 ALTER/DROP/DELETE/UPDATE 执行必然失败（ORA-00942）', '表', 'must', '{}', 176
  UNION ALL SELECT 'oracle.struct.obj_col_must_exist', 'structure', '限制操作的目标列必须存在',
         '列不存在时 MODIFY/DROP COLUMN 执行必然失败（ORA-00904）', '列', 'must', '{}', 177
  UNION ALL SELECT 'oracle.struct.obj_idx_must_exist', 'structure', '限制操作的目标索引必须存在',
         '索引不存在时 DROP INDEX 执行必然失败（ORA-01418）', '索引', 'must', '{}', 178
  UNION ALL SELECT 'oracle.struct.obj_col_must_not_exist', 'structure', '限制改名的目标列名不能已存在',
         'RENAME COLUMN 的新列名已存在会产生列名冲突，执行必然失败（ORA-00957）', '列', 'must', '{}', 179
  UNION ALL SELECT 'oracle.struct.obj_idx_must_not_exist', 'structure', '限制改名的目标索引名不能已存在',
         'ALTER INDEX ... RENAME TO 的新索引名已存在会产生冲突，执行必然失败（ORA-00955）', '索引', 'must', '{}', 180
  UNION ALL SELECT 'oracle.struct.obj_tbl_must_not_exist', 'structure', '限制改名的目标表名不能已存在',
         'ALTER TABLE ... RENAME TO 的新表名已存在会失败（ORA-00955）', '表', 'must', '{}', 181
  -- ---------- DML（20 条） ----------
  UNION ALL SELECT 'oracle.query_update.limit_update_delete_affected_rows', 'query_update', '限制 UPDATE/DELETE 单语句影响行数',
         '大批量更新/删除会暴涨事务日志、长时间锁大量数据，回滚代价高', '更新', 'must', '{"max_affected_rows":10000}', 44
  UNION ALL SELECT 'oracle.query_update.delete_stmt_check_table_exists', 'query_update', 'DELETE 语句检测表是否存在',
         '检查 DELETE 涉及的表是否存在', '删除', 'suggest', '{}', 45
  UNION ALL SELECT 'oracle.query_update.limit_update_delete_join_table_count', 'query_update', '限制 UPDATE/DELETE 语句关联表的数量',
         '多表关联易致性能下降与锁竞争', '更新', 'suggest', '{"max_join_tables":5}', 46
  UNION ALL SELECT 'oracle.query_update.update_delete_stmt_join_on_check', 'query_update', 'UPDATE/DELETE 语句检测多表关联语法是否完整（JOIN 遗漏 ON 子句）',
         '缺少 ON 子句会产生笛卡尔积，可能误更新/删除大量数据', '更新', 'suggest', '{}', 47
  UNION ALL SELECT 'oracle.query_update.limit_update_delete_data_size', 'query_update', '限制 UPDATE/DELETE 语句 LIMIT 的行数',
         'Oracle 不支持 UPDATE/DELETE 的 LIMIT 语法，出现即必然失败（ORA-00933）', '更新', 'suggest', '{"max_limit_rows":5}', 48
  UNION ALL SELECT 'oracle.query_update.update_delete_stmt_suggest_where', 'query_update', 'UPDATE/DELETE 语句建议指定 WHERE 条件',
         '缺少 WHERE 会全表更新/删除，风险高；WHERE 可走索引减少数据量', '更新', 'suggest', '{}', 49
  UNION ALL SELECT 'oracle.query_update.insert_stmt_col_name_no_dup', 'query_update', 'INSERT 语句字段名不能重复',
         '字段名重复会导致歧义并使 SQL 执行失败（ORA-00957）', '写入', 'suggest', '{}', 53
  UNION ALL SELECT 'oracle.query_update.insert_stmt_columns_match_values', 'query_update', 'INSERT 语句字段列表与值列表要匹配',
         '字段与值数量不一致会直接报错（ORA-00947/00913），显式列表时逐行比对，无列表时与表结构比对', '写入', 'suggest', '{}', 54
  UNION ALL SELECT 'oracle.query_update.insert_stmt_specify_columns', 'query_update', 'INSERT 语句建议指定字段列表',
         '显式指定字段列表更清晰，且表结构变更时语句不易失效', '写入', 'suggest', '{}', 55
  UNION ALL SELECT 'oracle.query_update.insert_stmt_not_null_column_null_value', 'query_update', 'INSERT 语句不能为 NOT NULL 列插入 NULL 值',
         'NOT NULL 列必须给值，否则执行失败并易破坏数据完整性', '写入', 'suggest', '{}', 56
  UNION ALL SELECT 'oracle.query_update.insert_stmt_avoid_sysdate_func', 'query_update', 'INSERT 语句不建议使用 SYSDATE() 函数',
         'SYSDATE 取服务器当前时间，同事务多次调用结果不同，且易致主备不一致', '写入', 'suggest', '{}', 57
  UNION ALL SELECT 'oracle.query_update.limit_insert_values_row_count', 'query_update', '限制 INSERT 语句单条 VALUES 的行数',
         '大批量插入会暴涨事务日志、占用大量内存并加剧锁竞争', '写入', 'suggest', '{"max_insert_rows":1000}', 58
  UNION ALL SELECT 'oracle.query_update.insert_stmt_check_column_exists', 'query_update', 'INSERT 语句检测目标表/字段是否存在',
         '目标表或字段不存在时执行必然失败（ORA-00942 / ORA-00904），提交前必须拦截', '写入', 'suggest', '{}', 59
  UNION ALL SELECT 'oracle.query_update.limit_where_in_element_count', 'query_update', '限制 WHERE 条件中 IN 子句的元素个数',
         'IN 元素过多难以命中索引，易触发全表扫描', '查询', 'suggest', '{"max_in_elements":1000}', 83
  UNION ALL SELECT 'oracle.query_update.update_multi_table_column_prefix_check', 'query_update', 'UPDATE 多表时检测 SET 的列是否指定表前缀',
         '多表存在同名字段时，不指定表前缀易产生歧义', '更新', 'suggest', '{}', 140
  UNION ALL SELECT 'oracle.query_update.update_stmt_avoid_update_create_time_column', 'query_update', 'UPDATE 语句不建议更新表上的“创建时间”列',
         '创建时间属数据元信息，更新会破坏审计追溯', '更新', 'suggest', '{"create_time_columns":"create_time"}', 141
  UNION ALL SELECT 'oracle.query_update.update_stmt_check_updates_pk', 'query_update', 'UPDATE 语句检测是否更新了主键',
         '更新主键会触发索引重建并可能引发级联更新，影响性能', '更新', 'suggest', '{}', 142
  UNION ALL SELECT 'oracle.query_update.update_stmt_suggest_update_time_column', 'query_update', 'UPDATE 语句建议更新表上的“修改时间”列',
         '修改时间便于审计、变更追踪与增量同步', '更新', 'suggest', '{"update_time_columns":"update_time"}', 143
  UNION ALL SELECT 'oracle.query_update.update_stmt_set_separator_check', 'query_update', 'UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法）',
         'AND 是逻辑运算符，不能用作 SET 分隔符，否则语法异常', '更新', 'suggest', '{}', 144
  UNION ALL SELECT 'oracle.query_update.update_stmt_check_column_exists', 'query_update', 'UPDATE 语句检测表/字段是否存在',
         '目标表或 SET 的字段不存在时执行必然失败（ORA-00942 / ORA-00904），提交前必须拦截', '更新', 'suggest', '{}', 145
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);

-- ========================== STEP 3：复核 ==========================

-- 3.1 补种后每个规范应各持有 57 条
SELECT standard_id, COUNT(*) AS rule_count
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'oracle.%'
GROUP BY standard_id;

-- 3.2 明细核对（类别分布：structure 37 / query_update 20）
SELECT standard_id, category, COUNT(*) AS cnt
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'oracle.%'
GROUP BY standard_id, category
ORDER BY standard_id, category;

-- 3.3 规则类型（rule_type）不允许为空——「规则类型」列的唯一权威来源是库列
SELECT id, rule_key, rule_type
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'oracle.%' AND (rule_type IS NULL OR rule_type = '');

-- 3.4 需要关闭某条规则时按需改级别：
-- UPDATE insight_sql_standard_rules SET level='off'
--  WHERE rule_key='oracle.struct.obj_idx_must_exist' AND standard_id=1;
