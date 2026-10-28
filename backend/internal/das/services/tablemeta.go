package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GetTableInfoService struct {
	*forms.GetTableInfoForm
	C        *gin.Context
	Username string
}

func (s *GetTableInfoService) parserUUID() (id string, err error) {
	id = strings.TrimSpace(s.InstanceID)
	if id == "" {
		return "", fmt.Errorf("instance id is empty")
	}
	return id, nil
}

// 验证用户是否有指定schema的权限
func (s *GetTableInfoService) validatePerms(uuid string) error {
	// 检查库表权限
	var tables []parser.Table
	tables = append(tables, parser.Table{Schema: s.Schema})
	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     tables,
	}
	if err := checker.HasSchemaPerms(); err != nil {
		return err
	}
	return nil
}

func (s *GetTableInfoService) getInstanceCfg() (instance models.InsightInstances, err error) {
	// 获取DB配置
	r := global.App.DB.Table("`insight_instances` a").
		Select("a.`hostname`, a.`port`, a.`user`, a.`password`, a.`db_type`, a.`extra_config`").
		Where("a.instance_id=?", s.InstanceID).
		Take(&instance)
	// 判断记录是否存在
	if errors.Is(r.Error, gorm.ErrRecordNotFound) {
		return instance, fmt.Errorf("指定DB配置的记录不存在,错误的信息:%s", r.Error.Error())
	}
	return instance, nil
}

func (s *GetTableInfoService) getTableStruc(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Table, "table"); err != nil {
		return nil, err
	}

	// 解密密码
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	// 获取DB类型
	dbType := string(instanceCfg.DbType)
	if IsMySQLFamilyCfg(dbType, instanceCfg.ExtraConfig) {
		if err := validateIdentifier(s.Schema, "schema"); err != nil {
			return nil, err
		}
		db := dao.DB{
			User:     instanceCfg.User,
			Password: plainPassword,
			Host:     instanceCfg.Hostname,
			Port:     instanceCfg.Port,
			Params:   map[string]string{"group_concat_max_len": "1073741824"},
			Ctx:      s.C.Request.Context(),
		}

		_, data, err = db.Query(fmt.Sprintf("show create table %s.%s", quoteIdentifier(s.Schema), quoteIdentifier(s.Table)))
		if err != nil {
			global.App.Log.Error(err.Error())
		}
	}
	if IsPostgresFamilyCfg(dbType, instanceCfg.ExtraConfig) {
		parts := strings.SplitN(s.Schema, ".", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("PostgreSQL 资源路径不合法")
		}
		databaseName := parts[0]
		schemaName := parts[1]
		if err := validateIdentifier(databaseName, "database"); err != nil {
			return nil, err
		}
		if err := validateIdentifier(schemaName, "schema"); err != nil {
			return nil, err
		}

		quotedSchema := quotePostgresIdentifier(schemaName)
		quotedTable := quotePostgresIdentifier(s.Table)
		qualifiedName := quotedSchema + "." + quotedTable

		query := fmt.Sprintf(`
			WITH
				-- 1. 获取目标表的 OID
				base AS (
					SELECT c.oid AS rel_oid
					FROM pg_catalog.pg_namespace n
					JOIN pg_catalog.pg_class c ON c.relnamespace = n.oid
					WHERE n.nspname = %s
					  AND c.relname = %s
					  AND c.relkind IN ('r','p','v','m','f')
					LIMIT 1
				),

				-- 2. 构建列定义（列名 + 数据类型 + NOT NULL + 默认值）
				columns AS (
					SELECT
						pg_catalog.quote_ident(a.attname) AS col_name,
						pg_catalog.format_type(a.atttypid, a.atttypmod) AS col_type,
						a.attnotnull,
						pg_catalog.pg_get_expr(d.adbin, d.adrelid) AS default_expr
					FROM base b
					JOIN pg_catalog.pg_attribute a ON a.attrelid = b.rel_oid
					LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
					WHERE a.attnum > 0
					  AND NOT a.attisdropped
					ORDER BY a.attnum
				),

				-- 3. 拼接列定义的字符串（用逗号和换行分隔）
				col_defs AS (
					SELECT string_agg(
						'    ' || col_name || ' ' || col_type ||
						CASE WHEN attnotnull THEN ' NOT NULL' ELSE '' END ||
						CASE WHEN default_expr IS NOT NULL THEN ' DEFAULT ' || default_expr ELSE '' END,
						E',\n'
					) AS col_list
					FROM columns
				),

				-- 4. 构造 CREATE TABLE 语句（若表不存在则生成空表）
				create_sql AS (
					SELECT
						CASE WHEN b.rel_oid IS NOT NULL THEN
							'CREATE TABLE ' || %s || ' (' || E'\n' ||
							COALESCE(cd.col_list, '') || E'\n' || ');'
						ELSE
							'CREATE TABLE ' || %s || ' ();'
						END AS ddl
					FROM base b
					CROSS JOIN col_defs cd
				),

				-- 5. 获取表的所有者（若存在）
				owner_sql AS (
					SELECT
						CASE WHEN r.rolname IS NOT NULL
						THEN 'ALTER TABLE ' || %s || ' OWNER TO ' || pg_catalog.quote_ident(r.rolname)
						ELSE '' END AS ddl
					FROM base b
					LEFT JOIN pg_catalog.pg_class c ON c.oid = b.rel_oid
					LEFT JOIN pg_catalog.pg_roles r ON r.oid = c.relowner
					LIMIT 1
				),

				-- 6. 列注释（来自 pg_description）
				column_comments AS (
					SELECT
						string_agg(
							'COMMENT ON COLUMN ' || %s || '.' ||
							pg_catalog.quote_ident(a.attname) || ' IS ' ||
							pg_catalog.quote_literal(pg_catalog.col_description(b.rel_oid, a.attnum)),
							E';\n' ORDER BY a.attnum
						) || CASE WHEN count(*) > 0 THEN ';' ELSE '' END AS ddl
					FROM base b
					JOIN pg_catalog.pg_attribute a ON a.attrelid = b.rel_oid
					WHERE a.attnum > 0
					  AND NOT a.attisdropped
					  AND pg_catalog.col_description(b.rel_oid, a.attnum) IS NOT NULL
					  AND length(btrim(pg_catalog.col_description(b.rel_oid, a.attnum))) > 0
				),

				-- 7. 表注释
				table_comment AS (
					SELECT
						CASE WHEN pg_catalog.obj_description(b.rel_oid, 'pg_class') IS NOT NULL
						  AND length(btrim(pg_catalog.obj_description(b.rel_oid, 'pg_class'))) > 0
						THEN 'COMMENT ON TABLE ' || %s || ' IS ' ||
							pg_catalog.quote_literal(pg_catalog.obj_description(b.rel_oid, 'pg_class'))
						ELSE '' END AS ddl
					FROM base b
					LIMIT 1
				)

				-- 8. 组装最终输出
				SELECT
					trim(trailing E'; \t\n\r' from c.ddl)
					|| CASE WHEN length(o.ddl) > 0 THEN E';\n\n' || o.ddl ELSE '' END
					|| CASE WHEN length(cc.ddl) > 0 THEN E';\n\n' || cc.ddl ELSE '' END
					|| CASE WHEN length(tc.ddl) > 0 THEN E';\n\n' || tc.ddl ELSE '' END
					|| E';\n'
				AS statement
				FROM create_sql c
				CROSS JOIN (SELECT 1) s
				LEFT JOIN owner_sql o ON true
				LEFT JOIN column_comments cc ON true
				LEFT JOIN table_comment tc ON true
		`,
			quoteStringLiteral(schemaName),
			quoteStringLiteral(s.Table),
			quoteStringLiteral(qualifiedName),
			quoteStringLiteral(qualifiedName),
			quoteStringLiteral(qualifiedName),
			quoteStringLiteral(qualifiedName),
			quoteStringLiteral(qualifiedName),
		)

		cfg := postgresExtraConfig{}
		if len(instanceCfg.ExtraConfig) > 0 {
			_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
		}
		db := dao.PostgresDB{
			User:     instanceCfg.User,
			Password: plainPassword,
			Host:     instanceCfg.Hostname,
			Port:     instanceCfg.Port,
			Database: databaseName,
			SSLMode:  cfg.SSLMode,
			Ctx:      s.C.Request.Context(),
		}

		_, data, err = db.Query(query)
		if err != nil {
			global.App.Log.Error(err.Error())
		}
	}
	if strings.EqualFold(dbType, "clickhouse") {
		if err := validateIdentifier(s.Schema, "schema"); err != nil {
			return nil, err
		}
		db := dao.ClickhouseDB{
			User:     instanceCfg.User,
			Password: plainPassword,
			Host:     instanceCfg.Hostname,
			Port:     instanceCfg.Port,
			Ctx:      s.C.Request.Context(),
		}
		_, data, err = db.Query(fmt.Sprintf("show create table %s.%s", quoteIdentifier(s.Schema), quoteIdentifier(s.Table)))
		if err != nil {
			global.App.Log.Error(err.Error())
		}
	}
	// SQL Server 资源路径是「库.模式」，不能用上面的 validateIdentifier（含点会被判非法），
	// 放在最后并由实现内部自行拆分校验。
	if strings.EqualFold(dbType, "sqlserver") {
		return s.getTableStrucSQLServer(instanceCfg)
	}
	return data, err
}

func (s *GetTableInfoService) getTableBasePostgres(instanceCfg models.InsightInstances, databaseName, schemaName string) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(databaseName, "database"); err != nil {
		return nil, err
	}
	if err := validateIdentifier(schemaName, "schema"); err != nil {
		return nil, err
	}
	if err := validateIdentifier(s.Table, "table"); err != nil {
		return nil, err
	}

	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT
			coalesce(cast(c.reltuples as bigint), 0)::bigint                                   AS "TABLE_ROWS",
			coalesce(cast(stat.n_dead_tup as bigint), 0)::bigint                                AS "DEAD_ROWS",
			CASE
				WHEN coalesce(c.reltuples, 0) > 0
				THEN GREATEST(1, round((
					coalesce(pg_relation_size(c.oid), 0)::float8
					/
					NULLIF(c.reltuples, 0)
				)::numeric, 0))::bigint
				ELSE 0::bigint
			END::bigint                                                                            AS "AVG_ROW_LENGTH",
			coalesce(pg_table_size(c.oid), 0)::bigint                                           AS "DATA_LENGTH",
			(coalesce(pg_relation_size(c.oid, 'main'), 0)
				+ coalesce(pg_relation_size(c.oid, 'fsm'), 0)
				+ coalesce(pg_relation_size(c.oid, 'vm'), 0)
				+ coalesce(pg_indexes_size(c.oid), 0))::bigint                                        AS "INDEX_LENGTH",
			coalesce(pg_total_relation_size(c.oid), 0)::bigint                                   AS "MAX_DATA_LENGTH",
			0::bigint                                                                             AS "DATA_FREE",
			(pg_stat_file(pg_relation_filepath(c.oid))).modification::timestamp without time zone  AS "CREATE_TIME",
			coalesce(
				stat.last_autoanalyze::timestamp without time zone,
				stat.last_analyze::timestamp without time zone,
				stat.last_autovacuum::timestamp without time zone,
				stat.last_vacuum::timestamp without time zone
			)                                                                                     AS "UPDATE_TIME",
			NULL::timestamp without time zone                                                     AS "CHECK_TIME",
			0::bigint                                                                             AS "VERSION",
			text 'Dynamic'                                                                        AS "ROW_FORMAT",
			c.relname                                                                             AS "TABLE_NAME",
			CASE c.relkind
				WHEN 'r' THEN 'BASE TABLE'
				WHEN 'p' THEN 'BASE TABLE'
				WHEN 'v' THEN 'VIEW'
				WHEN 'm' THEN 'MATERIALIZED VIEW'
				WHEN 'f' THEN 'FOREIGN TABLE'
				ELSE c.relkind::text
			END                                                                                   AS "TABLE_TYPE",
			''                                                                                    AS "ENGINE",
			coalesce(server_encoding.setting::text, '')                                           AS "CHARACTER_SET_NAME",
			coalesce(db.datcollate::text, '')                                                     AS "TABLE_COLLATION",
			coalesce(db.datctype::text, '')                                                       AS "COLLATION_NAME",
			0::bigint                                                                             AS "AUTO_INCREMENT",
			coalesce(obj_description(c.oid, 'pg_class')::text, '')                                AS "TABLE_COMMENT",
			coalesce(stat.last_vacuum, stat.last_autovacuum)::timestamp without time zone        AS "LAST_VACUUM",
			coalesce(stat.last_analyze, stat.last_autoanalyze)::timestamp without time zone      AS "LAST_ANALYZE"
		FROM pg_class c
		JOIN pg_namespace n
		  ON n.oid = c.relnamespace
		 AND n.nspname = %s
		 AND c.relname = %s
		 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
		LEFT JOIN pg_stat_user_tables stat
		  ON stat.relid = c.oid
		 AND stat.schemaname = n.nspname
		 AND stat.relname  = c.relname
		LEFT JOIN pg_database db
		  ON db.datname = current_database()
		LEFT JOIN (SELECT setting FROM pg_settings WHERE name = 'server_encoding') server_encoding ON true
		LIMIT 1
	`,
		quoteStringLiteral(schemaName),
		quoteStringLiteral(s.Table),
	)

	cfg := postgresExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}
	db := dao.PostgresDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: databaseName,
		SSLMode:  cfg.SSLMode,
		Ctx:      s.C.Request.Context(),
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Errorf("getTableBasePostgres query failed: %v", err)
		return nil, err
	}
	return data, nil
}

func (s *GetTableInfoService) getTableBase(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Table, "table"); err != nil {
		return nil, err
	}

	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	dbType := string(instanceCfg.DbType)

	if IsPostgresFamilyCfg(dbType, instanceCfg.ExtraConfig) {
		parts := strings.SplitN(s.Schema, ".", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("PostgreSQL 资源路径不合法")
		}
		databaseName := parts[0]
		schemaName := parts[1]
		if err := validateIdentifier(databaseName, "database"); err != nil {
			return nil, err
		}
		if err := validateIdentifier(schemaName, "schema"); err != nil {
			return nil, err
		}
		return s.getTableBasePostgres(instanceCfg, databaseName, schemaName)
	}

	// SQL Server 的 s.Schema 是「库.模式」，必须先拆分再校验，
	// 因此要排在下面那句通用 validateIdentifier(s.Schema) 之前。
	if strings.EqualFold(dbType, "sqlserver") {
		return s.getTableBaseSQLServer(instanceCfg)
	}

	if err := validateIdentifier(s.Schema, "schema"); err != nil {
		return nil, err
	}

	if IsMySQLFamilyCfg(dbType, instanceCfg.ExtraConfig) {
		query := fmt.Sprintf(`
					select
						*
					from 
						information_schema.tables 
						where 
							table_schema=%s and table_name=%s
					`, quoteStringLiteral(s.Schema), quoteStringLiteral(s.Table))

		db := dao.DB{
			User:     instanceCfg.User,
			Password: plainPassword,
			Host:     instanceCfg.Hostname,
			Port:     instanceCfg.Port,
			Params:   map[string]string{"group_concat_max_len": "1073741824"},
			Ctx:      s.C.Request.Context(),
		}

		_, data, err = db.Query(query)
		if err != nil {
			global.App.Log.Error(err.Error())
		}
		return data, err
	}

	return data, fmt.Errorf("%s不支持获取表信息", dbType)
}

func (s *GetTableInfoService) Run() (responseData *[]map[string]interface{}, err error) {
	// 获取DB配置
	instanceCfg, err := s.getInstanceCfg()
	if err != nil {
		return responseData, err
	}
	// 解析UUID
	uuid, err := s.parserUUID()
	if err != nil {
		return responseData, err
	}
	// 验证用户是否有指定schema的权限
	if err = s.validatePerms(uuid); err != nil {
		return responseData, err
	}

	// 非表对象（视图/存储过程/触发器等）：与表共用接口，object_type 区分
	objectKind, kerr := normalizeObjectKind(s.ObjectType)
	if kerr != nil {
		return nil, kerr
	}
	if objectKind != "table" {
		if s.Type == "structure" {
			return s.getObjectDDL(instanceCfg, objectKind)
		}
		return s.getObjectBase(instanceCfg, objectKind)
	}

	if s.Type == "structure" {
		// 获取表结构
		return s.getTableStruc(instanceCfg)
	} else {
		// 获取表基础信息
		return s.getTableBase(instanceCfg)
	}
}
