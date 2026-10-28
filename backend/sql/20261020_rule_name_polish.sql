-- SQL 规范【规则名称】文案统一（存量数据修正）
-- 背景：规则名称此前存在中英文空格不统一、全角/半角括号混用、语句不通顺等问题，本次统一润色。
--       只改 name 字段的展示文案，不动 rule_key / level / params 等任何逻辑字段。
-- 配套改动（发布时必须一起上）：
--   1. backend/internal/inspect/services/standard_seed.go      新建规范时的 Name 已同步
--   2. www/src/views/admin/system/sql-standards/ruleTypeMap.json 规则类型字典（键已同步改名 + 补齐类型）
-- 说明：按 rule_key 幂等设置，可重复执行；不需要重启服务。
-- 执行：mysql -h<host> -u<user> -p<pass> <db> < 20261020_rule_name_polish.sql

SET NAMES utf8mb4;

UPDATE insight_sql_standard_rules SET name = '限制自增列名为 ID' WHERE rule_key = 'mysql.struct.limit_auto_inc_col_name_is_id';
UPDATE insight_sql_standard_rules SET name = '限制建表自增列初始值' WHERE rule_key = 'mysql.struct.limit_create_table_auto_inc_init';
UPDATE insight_sql_standard_rules SET name = '限制 CHAR 类型字段的长度' WHERE rule_key = 'mysql.struct.limit_char_type_col_len';
UPDATE insight_sql_standard_rules SET name = '限制字段名的大小写' WHERE rule_key = 'mysql.struct.limit_col_name_case';
UPDATE insight_sql_standard_rules SET name = '限制字段名不能是关键字' WHERE rule_key = 'mysql.struct.col_name_no_keyword';
UPDATE insight_sql_standard_rules SET name = '限制列必须有注释' WHERE rule_key = 'mysql.struct.limit_col_must_have_comment';
UPDATE insight_sql_standard_rules SET name = '限制表字段数量上限' WHERE rule_key = 'mysql.struct.limit_table_column_count';
UPDATE insight_sql_standard_rules SET name = '限制表必须有注释' WHERE rule_key = 'mysql.struct.limit_tbl_must_comment';
UPDATE insight_sql_standard_rules SET name = '表必须有主键' WHERE rule_key = 'mysql.struct.table_must_have_pk';
UPDATE insight_sql_standard_rules SET name = '限制唯一索引命名格式' WHERE rule_key = 'mysql.struct.limit_uniq_idx_name_fmt';
UPDATE insight_sql_standard_rules SET name = '限制 VARCHAR 类型字段的长度' WHERE rule_key = 'mysql.struct.limit_varchar_type_col_len';
UPDATE insight_sql_standard_rules SET name = '限制列必须有默认值' WHERE rule_key = 'mysql.struct.limit_col_must_have_default';
UPDATE insight_sql_standard_rules SET name = '限制列不可空（NOT NULL）' WHERE rule_key = 'mysql.struct.limit_col_not_null';
UPDATE insight_sql_standard_rules SET name = '限制列不能使用指定的数据类型' WHERE rule_key = 'mysql.struct.limit_col_no_partial_data_type';
UPDATE insight_sql_standard_rules SET name = '限制列不能设置字符集' WHERE rule_key = 'mysql.struct.no_column_charset';
UPDATE insight_sql_standard_rules SET name = '限制列不能设置校对集' WHERE rule_key = 'mysql.struct.no_column_collate';
UPDATE insight_sql_standard_rules SET name = '限制字段使用 Zerofill 属性' WHERE rule_key = 'mysql.struct.column_zerofill_forbidden';
UPDATE insight_sql_standard_rules SET name = '限制索引名称不能重复' WHERE rule_key = 'mysql.struct.index_name_no_duplicate';
UPDATE insight_sql_standard_rules SET name = '限制表的索引数量' WHERE rule_key = 'mysql.struct.limit_tbl_idx_count';
UPDATE insight_sql_standard_rules SET name = '限制索引必须设置名称' WHERE rule_key = 'mysql.struct.index_must_have_name';
UPDATE insight_sql_standard_rules SET name = '限制普通索引命名格式' WHERE rule_key = 'mysql.struct.limit_normal_idx_name_fmt';
UPDATE insight_sql_standard_rules SET name = '限制主键包含的列数' WHERE rule_key = 'mysql.struct.limit_pk_column_count';
UPDATE insight_sql_standard_rules SET name = '限制主键列的数据类型' WHERE rule_key = 'mysql.struct.limit_pk_col_type';
UPDATE insight_sql_standard_rules SET name = '限制单个索引包含的列数' WHERE rule_key = 'mysql.struct.limit_single_index_column_count';
UPDATE insight_sql_standard_rules SET name = '限制表名的大小写' WHERE rule_key = 'mysql.struct.limit_tbl_name_case';
UPDATE insight_sql_standard_rules SET name = '限制表名不能是关键字' WHERE rule_key = 'mysql.struct.tbl_name_no_keyword';
UPDATE insight_sql_standard_rules SET name = '限制表必须包含指定列' WHERE rule_key = 'mysql.struct.table_must_include_required_columns';
UPDATE insight_sql_standard_rules SET name = '限制 UPDATE/DELETE 单语句影响行数' WHERE rule_key = 'mysql.query_update.limit_update_delete_affected_rows';
UPDATE insight_sql_standard_rules SET name = '限制 UPDATE/DELETE 语句关联表的数量' WHERE rule_key = 'mysql.query_update.limit_update_delete_join_table_count';
UPDATE insight_sql_standard_rules SET name = '限制 UPDATE/DELETE 语句 LIMIT 的行数' WHERE rule_key = 'mysql.query_update.limit_update_delete_data_size';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句字段列表与值列表要匹配' WHERE rule_key = 'mysql.query_update.insert_stmt_columns_match_values';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句建议指定字段列表' WHERE rule_key = 'mysql.query_update.insert_stmt_specify_columns';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句不建议使用 SYSDATE() 函数' WHERE rule_key = 'mysql.query_update.insert_stmt_avoid_sysdate_func';
UPDATE insight_sql_standard_rules SET name = '限制 INSERT 语句单条 VALUES 的行数' WHERE rule_key = 'mysql.query_update.limit_insert_values_row_count';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句检测目标表/字段是否存在' WHERE rule_key = 'mysql.query_update.insert_stmt_check_column_exists';
UPDATE insight_sql_standard_rules SET name = 'UPDATE 语句不建议更新表上的“创建时间”列' WHERE rule_key = 'mysql.query_update.update_stmt_avoid_update_create_time_column';
UPDATE insight_sql_standard_rules SET name = 'UPDATE 语句建议更新表上的“修改时间”列' WHERE rule_key = 'mysql.query_update.update_stmt_suggest_update_time_column';
UPDATE insight_sql_standard_rules SET name = 'UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法）' WHERE rule_key = 'mysql.query_update.update_stmt_set_separator_check';
UPDATE insight_sql_standard_rules SET name = '限制 WHERE 条件中 IN 子句的元素个数' WHERE rule_key = 'mysql.query_update.limit_where_in_element_count';
UPDATE insight_sql_standard_rules SET name = 'WHERE 条件中不建议使用反向查询（NOT IN/NOT LIKE）' WHERE rule_key = 'mysql.query_update.where_avoid_not_in_not_like';
UPDATE insight_sql_standard_rules SET name = 'WHERE 条件中检测是否使用 OR 连接过滤条件' WHERE rule_key = 'mysql.query_update.where_or_condition_check';
UPDATE insight_sql_standard_rules SET name = 'WHERE 条件中检测 LIKE 语句是否缺少通配符' WHERE rule_key = 'mysql.query_update.where_like_without_wildcard';
UPDATE insight_sql_standard_rules SET name = '限制 CHAR 类型字段的长度' WHERE rule_key = 'postgres.struct.limit_char_type_col_len';
UPDATE insight_sql_standard_rules SET name = '限制字段名的大小写' WHERE rule_key = 'postgres.struct.limit_col_name_case';
UPDATE insight_sql_standard_rules SET name = '限制字段名不能是关键字' WHERE rule_key = 'postgres.struct.col_name_no_keyword';
UPDATE insight_sql_standard_rules SET name = '限制列必须有注释' WHERE rule_key = 'postgres.struct.limit_col_must_have_comment';
UPDATE insight_sql_standard_rules SET name = '限制表字段数量上限' WHERE rule_key = 'postgres.struct.limit_table_column_count';
UPDATE insight_sql_standard_rules SET name = '表必须有主键' WHERE rule_key = 'postgres.struct.table_must_have_pk';
UPDATE insight_sql_standard_rules SET name = '限制唯一索引命名格式' WHERE rule_key = 'postgres.struct.limit_uniq_idx_name_fmt';
UPDATE insight_sql_standard_rules SET name = '限制 VARCHAR 类型字段的长度' WHERE rule_key = 'postgres.struct.limit_varchar_type_col_len';
UPDATE insight_sql_standard_rules SET name = '限制列必须有默认值' WHERE rule_key = 'postgres.struct.limit_col_must_have_default';
UPDATE insight_sql_standard_rules SET name = '限制列不可空（NOT NULL）' WHERE rule_key = 'postgres.struct.limit_col_not_null';
UPDATE insight_sql_standard_rules SET name = '限制列不能使用指定的数据类型' WHERE rule_key = 'postgres.struct.limit_col_no_partial_data_type';
UPDATE insight_sql_standard_rules SET name = '限制表的索引数量' WHERE rule_key = 'postgres.struct.limit_tbl_idx_count';
UPDATE insight_sql_standard_rules SET name = '限制索引必须设置名称' WHERE rule_key = 'postgres.struct.index_must_have_name';
UPDATE insight_sql_standard_rules SET name = '限制普通索引命名格式' WHERE rule_key = 'postgres.struct.limit_normal_idx_name_fmt';
UPDATE insight_sql_standard_rules SET name = '限制主键包含的列数' WHERE rule_key = 'postgres.struct.limit_pk_column_count';
UPDATE insight_sql_standard_rules SET name = '限制主键列的数据类型' WHERE rule_key = 'postgres.struct.limit_pk_col_type';
UPDATE insight_sql_standard_rules SET name = '限制单个索引包含的列数' WHERE rule_key = 'postgres.struct.limit_single_index_column_count';
UPDATE insight_sql_standard_rules SET name = '限制表名的大小写' WHERE rule_key = 'postgres.struct.limit_tbl_name_case';
UPDATE insight_sql_standard_rules SET name = '限制表名不能是关键字' WHERE rule_key = 'postgres.struct.tbl_name_no_keyword';
UPDATE insight_sql_standard_rules SET name = '限制表必须包含指定列' WHERE rule_key = 'postgres.struct.table_must_include_required_columns';
UPDATE insight_sql_standard_rules SET name = 'DELETE 语句检测表是否存在' WHERE rule_key = 'postgres.query_update.delete_stmt_check_table_exists';
UPDATE insight_sql_standard_rules SET name = '限制 UPDATE/DELETE 语句关联表的数量' WHERE rule_key = 'postgres.query_update.limit_update_delete_join_table_count';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句字段列表与值列表要匹配' WHERE rule_key = 'postgres.query_update.insert_stmt_columns_match_values';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句建议指定字段列表' WHERE rule_key = 'postgres.query_update.insert_stmt_specify_columns';
UPDATE insight_sql_standard_rules SET name = '限制 INSERT 语句单条 VALUES 的行数' WHERE rule_key = 'postgres.query_update.limit_insert_values_row_count';
UPDATE insight_sql_standard_rules SET name = 'INSERT 语句检测目标表/字段是否存在' WHERE rule_key = 'postgres.query_update.insert_stmt_check_column_exists';
UPDATE insight_sql_standard_rules SET name = 'UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法）' WHERE rule_key = 'postgres.query_update.update_stmt_set_separator_check';
UPDATE insight_sql_standard_rules SET name = '限制 WHERE 条件中 IN 子句的元素个数' WHERE rule_key = 'postgres.query_update.limit_where_in_element_count';
UPDATE insight_sql_standard_rules SET name = 'WHERE 条件中不建议使用反向查询（NOT IN/NOT LIKE）' WHERE rule_key = 'postgres.query_update.where_avoid_not_in_not_like';
UPDATE insight_sql_standard_rules SET name = 'WHERE 条件中检测是否使用 OR 连接过滤条件' WHERE rule_key = 'postgres.query_update.where_or_condition_check';
UPDATE insight_sql_standard_rules SET name = 'WHERE 条件中检测 LIKE 语句是否缺少通配符' WHERE rule_key = 'postgres.query_update.where_like_without_wildcard';
UPDATE insight_sql_standard_rules SET name = '限制表必须有注释' WHERE rule_key = 'postgres.struct.limit_tbl_must_comment';
UPDATE insight_sql_standard_rules SET name = '限制建表的目标表不能已存在' WHERE rule_key = 'mysql.struct.create_tbl_must_not_exist';
UPDATE insight_sql_standard_rules SET name = '限制操作的目标表必须存在' WHERE rule_key = 'mysql.struct.obj_tbl_must_exist';
UPDATE insight_sql_standard_rules SET name = '限制操作的目标列必须存在' WHERE rule_key = 'mysql.struct.obj_col_must_exist';
UPDATE insight_sql_standard_rules SET name = '限制操作的目标索引必须存在' WHERE rule_key = 'mysql.struct.obj_idx_must_exist';
UPDATE insight_sql_standard_rules SET name = '限制改名的目标列名不能已存在' WHERE rule_key = 'mysql.struct.obj_col_must_not_exist';
UPDATE insight_sql_standard_rules SET name = '限制改名的目标索引名不能已存在' WHERE rule_key = 'mysql.struct.obj_idx_must_not_exist';
UPDATE insight_sql_standard_rules SET name = '限制改名的目标表名不能已存在' WHERE rule_key = 'mysql.struct.obj_tbl_must_not_exist';
UPDATE insight_sql_standard_rules SET name = '限制创建视图的目标视图不能已存在' WHERE rule_key = 'mysql.struct.obj_view_must_not_exist';
UPDATE insight_sql_standard_rules SET name = '限制建表的目标表不能已存在' WHERE rule_key = 'postgres.struct.create_tbl_must_not_exist';
UPDATE insight_sql_standard_rules SET name = '限制操作的目标表必须存在' WHERE rule_key = 'postgres.struct.obj_tbl_must_exist';
UPDATE insight_sql_standard_rules SET name = 'UPDATE 语句检测表/字段是否存在' WHERE rule_key = 'mysql.query_update.update_stmt_check_column_exists';
