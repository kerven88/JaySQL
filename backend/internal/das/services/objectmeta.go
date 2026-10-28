package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
)

// 对象树右键「查看X信息」的后端实现：视图 / 物化视图 / 存储过程 / 函数 / 触发器 / 事件。
// 与表共用 /das/table-info 接口（object_type 区分），DDL 统一以 statement 列返回，
// 概览字段由前端按 key-value 通用渲染，因此各引擎只管给出「能查到的真实字段」。

func normalizeObjectKind(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "table":
		return "table", nil
	case "view":
		return "view", nil
	case "materialized-view", "materialized_view", "mview":
		return "mview", nil
	case "procedure":
		return "procedure", nil
	case "function":
		return "function", nil
	case "trigger":
		return "trigger", nil
	case "event":
		return "event", nil
	}
	return "", fmt.Errorf("不支持的对象类型: %s", raw)
}

// wrapShowCreateStatement 把 SHOW CREATE xxx 的结果统一包装成 {"statement": ddl}，
// 屏蔽各语句返回列名差异（Create View / Create Procedure / SQL Original Statement ...）。
func wrapShowCreateStatement(rows *[]map[string]interface{}, keys ...string) *[]map[string]interface{} {
	if rows == nil {
		return rows
	}
	lowerKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		lowerKeys = append(lowerKeys, strings.ToLower(key))
	}
	pick := func(row map[string]interface{}) string {
		for _, key := range keys {
			if v, ok := row[key]; ok && v != nil {
				if str := strings.TrimSpace(fmt.Sprintf("%v", v)); str != "" {
					return str
				}
			}
		}
		// 大小写兜底（Oracle / 部分驱动会转大写列名）
		for k, v := range row {
			lk := strings.ToLower(k)
			for _, target := range lowerKeys {
				if lk == target && v != nil {
					return strings.TrimSpace(fmt.Sprintf("%v", v))
				}
			}
		}
		return ""
	}
	out := make([]map[string]interface{}, 0, len(*rows))
	for _, row := range *rows {
		if ddl := pick(row); ddl != "" {
			out = append(out, map[string]interface{}{"statement": ddl})
		}
	}
	return &out
}

// 拼接 all_source 的多行为完整源码（Oracle 的存储过程 / 函数 / 触发器源码按行存储）
func concatSourceLines(rows *[]map[string]interface{}) string {
	if rows == nil {
		return ""
	}
	var builder strings.Builder
	for _, row := range *rows {
		builder.WriteString(stringField(row, "text"))
	}
	return strings.TrimRight(builder.String(), "\r\n \t")
}

func (s *GetTableInfoService) objectTargetSchema() (databaseName, schemaName string, err error) {
	parts := strings.SplitN(s.Schema, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("PostgreSQL 资源路径不合法")
	}
	return parts[0], parts[1], nil
}

func (s *GetTableInfoService) postgresObjectDB(instanceCfg models.InsightInstances, databaseName string) (*dao.PostgresDB, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	cfg := postgresExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}
	return &dao.PostgresDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: databaseName,
		SSLMode:  cfg.SSLMode,
		Ctx:      s.C.Request.Context(),
	}, nil
}

// ---------------------------------------------------------------------------
// DDL（type=structure）
// ---------------------------------------------------------------------------

func (s *GetTableInfoService) getObjectDDL(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Table, "object"); err != nil {
		return nil, err
	}
	dbType := string(instanceCfg.DbType)

	switch {
	case IsMySQLFamilyCfg(dbType, instanceCfg.ExtraConfig):
		return s.objectDDLMySQL(instanceCfg, kind)
	case IsPostgresFamilyCfg(dbType, instanceCfg.ExtraConfig):
		return s.objectDDLPostgres(instanceCfg, kind)
	case IsOracleFamilyCfg(dbType, instanceCfg.ExtraConfig):
		return s.objectDDLOracle(instanceCfg, kind)
	case strings.EqualFold(dbType, "clickhouse"):
		// ClickHouse 视图与表同构，show create table 直接可用
		return s.getTableStruc(instanceCfg)
	case strings.EqualFold(dbType, "sqlserver"):
		return s.objectDDLSQLServer(instanceCfg)
	}
	return nil, fmt.Errorf("%s不支持获取对象信息", dbType)
}

func (s *GetTableInfoService) objectDDLMySQL(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Schema, "schema"); err != nil {
		return nil, err
	}
	verb := ""
	pickKeys := []string{}
	switch kind {
	case "view":
		verb, pickKeys = "view", []string{"Create View"}
	case "procedure":
		verb, pickKeys = "procedure", []string{"Create Procedure"}
	case "function":
		verb, pickKeys = "function", []string{"Create Function"}
	case "trigger":
		verb, pickKeys = "trigger", []string{"SQL Original Statement"}
	case "event":
		verb, pickKeys = "event", []string{"Create Event"}
	default:
		return nil, fmt.Errorf("MySQL 不支持获取该对象类型的 DDL: %s", kind)
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
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
	query := fmt.Sprintf("show create %s %s.%s", verb, quoteIdentifier(s.Schema), quoteIdentifier(s.Table))
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}
	return wrapShowCreateStatement(data, pickKeys...), nil
}

func (s *GetTableInfoService) objectDDLPostgres(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	databaseName, schemaName, err := s.objectTargetSchema()
	if err != nil {
		return nil, err
	}
	db, err := s.postgresObjectDB(instanceCfg, databaseName)
	if err != nil {
		return nil, err
	}
	qualified := quoteStringLiteral(schemaName + "." + s.Table)
	var query string
	switch kind {
	case "view":
		// 物化视图与普通视图按 relkind 自动分流，避免前端区分不了 PG 的两种「视图」
		query = fmt.Sprintf(`
			SELECT CASE c.relkind
					WHEN 'm' THEN 'CREATE MATERIALIZED VIEW ' || %s || ' AS ' || chr(10) || pg_get_viewdef(c.oid, true)
					ELSE 'CREATE OR REPLACE VIEW ' || %s || ' AS ' || chr(10) || pg_get_viewdef(c.oid, true)
				END AS statement
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = %s AND c.relname = %s AND c.relkind IN ('v','m') LIMIT 1`,
			qualified, qualified, quoteStringLiteral(schemaName), quoteStringLiteral(s.Table))
	case "mview":
		query = fmt.Sprintf(`
			SELECT 'CREATE MATERIALIZED VIEW ' || %s || ' AS ' || chr(10) || pg_get_viewdef(c.oid, true) AS statement
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = %s AND c.relname = %s AND c.relkind = 'm' LIMIT 1`,
			qualified, quoteStringLiteral(schemaName), quoteStringLiteral(s.Table))
	case "function", "procedure":
		prokind := "f"
		if kind == "procedure" {
			prokind = "p"
		}
		query = fmt.Sprintf(`
			SELECT pg_get_functiondef(c.oid) AS statement
			FROM pg_proc c JOIN pg_namespace n ON n.oid = c.pronamespace
			WHERE n.nspname = %s AND c.proname = %s AND c.prokind = '%s' LIMIT 1`,
			quoteStringLiteral(schemaName), quoteStringLiteral(s.Table), prokind)
	case "trigger":
		query = fmt.Sprintf(`
			SELECT pg_get_triggerdef(t.oid, true) AS statement
			FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = %s AND t.tgname = %s AND NOT t.tgisinternal LIMIT 1`,
			quoteStringLiteral(schemaName), quoteStringLiteral(s.Table))
	default:
		return nil, fmt.Errorf("PostgreSQL 不支持获取该对象类型的 DDL: %s", kind)
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetTableInfoService) oracleObjectDB(instanceCfg models.InsightInstances) (*dao.OracleDB, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	cfg := oracleTableExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}
	return &dao.OracleDB{
		User:        instanceCfg.User,
		Password:    plainPassword,
		Host:        instanceCfg.Hostname,
		Port:        instanceCfg.Port,
		ServiceName: cfg.ServiceName,
		Ctx:         s.C.Request.Context(),
	}, nil
}

func (s *GetTableInfoService) objectDDLOracle(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	db, err := s.oracleObjectDB(instanceCfg)
	if err != nil {
		return nil, err
	}
	owner := quoteStringLiteral(strings.ToUpper(s.Schema))
	name := quoteStringLiteral(strings.ToUpper(s.Table))
	switch kind {
	case "view":
		// all_views.text / all_mviews.query 是 LONG 类型：不能在 SQL 里与字符串拼接（ORA-00932），
		// 只能整列取回后由 Go 侧拼接；all_* 查不到（未授权给当前用户）时逐级降级 dba_*。
		fetchLong := func(view, column, nameColumn string) string {
			q := fmt.Sprintf(`SELECT %s AS "text" FROM %s WHERE owner = %s AND %s = %s`,
				column, view, owner, nameColumn, name)
			_, rows, qerr := db.Query(q)
			if qerr != nil || rows == nil || len(*rows) == 0 {
				return ""
			}
			return stringField((*rows)[0], "text")
		}
		qualified := fmt.Sprintf("%s.%s", strings.ToUpper(s.Schema), strings.ToUpper(s.Table))
		var statement string
		if text := fetchLong("all_views", "text", "view_name"); text != "" {
			statement = "CREATE OR REPLACE VIEW " + qualified + " AS" + "\n" + text
		} else if queryText := fetchLong("all_mviews", "query", "mview_name"); queryText != "" {
			statement = "CREATE MATERIALIZED VIEW " + qualified + " AS " + queryText
		} else if text := fetchLong("dba_views", "text", "view_name"); text != "" {
			statement = "CREATE OR REPLACE VIEW " + qualified + " AS" + "\n" + text
		} else if queryText := fetchLong("dba_mviews", "query", "mview_name"); queryText != "" {
			statement = "CREATE MATERIALIZED VIEW " + qualified + " AS " + queryText
		}
		if statement == "" {
			data = &[]map[string]interface{}{}
		} else {
			data = &[]map[string]interface{}{{"statement": statement}}
		}
	case "procedure", "function", "trigger":
		sourceType := strings.ToUpper(kind)
		fetchSource := func(view string) string {
			q := fmt.Sprintf(`SELECT text AS "text" FROM %s WHERE owner = %s AND name = %s AND type = %s ORDER BY line`,
				view, owner, name, quoteStringLiteral(sourceType))
			_, rows, qerr := db.Query(q)
			if qerr != nil {
				global.App.Log.Error(qerr.Error())
				return ""
			}
			return concatSourceLines(rows)
		}
		source := fetchSource("all_source")
		if source == "" {
			// DBA 账号可见面更大：all_source 没有时（对象未授权给当前用户）降级 dba_source
			source = fetchSource("dba_source")
		}
		if source == "" {
			data = &[]map[string]interface{}{}
		} else {
			data = &[]map[string]interface{}{{"statement": source}}
		}
	default:
		return nil, fmt.Errorf("Oracle 不支持获取该对象类型的 DDL: %s", kind)
	}
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetTableInfoService) objectDDLSQLServer(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	// 路径是「库.模式」：整段拼进 OBJECT_ID 会得到 N'msdb.dbo'.'view'，
	// SQL Server 不认这种形态，OBJECT_ID 返回 NULL → DDL 恒为空。
	database, schemaName, err := splitSQLServerResourcePath(s.Schema)
	if err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	// OBJECT_DEFINITION 覆盖视图 / 存储过程 / 函数 / 触发器
	qualified := sqlServerQualifiedName(database, schemaName, s.Table)
	query := fmt.Sprintf(`SELECT OBJECT_DEFINITION(OBJECT_ID(N%s)) AS statement`, quoteStringLiteral(qualified))
	_, data, err = s.sqlServerDB(instanceCfg, plainPassword).Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

// ---------------------------------------------------------------------------
// 概览（type=base）
// ---------------------------------------------------------------------------

func (s *GetTableInfoService) getObjectBase(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Table, "object"); err != nil {
		return nil, err
	}
	dbType := string(instanceCfg.DbType)

	switch {
	case IsMySQLFamilyCfg(dbType, instanceCfg.ExtraConfig):
		return s.objectBaseMySQL(instanceCfg, kind)
	case IsPostgresFamilyCfg(dbType, instanceCfg.ExtraConfig):
		return s.objectBasePostgres(instanceCfg, kind)
	case IsOracleFamilyCfg(dbType, instanceCfg.ExtraConfig):
		return s.objectBaseOracle(instanceCfg, kind)
	case strings.EqualFold(dbType, "clickhouse"):
		return s.objectBaseClickHouse(instanceCfg)
	case strings.EqualFold(dbType, "sqlserver"):
		return s.objectBaseSQLServer(instanceCfg)
	}
	return nil, fmt.Errorf("%s不支持获取对象信息", dbType)
}

func (s *GetTableInfoService) objectBaseMySQL(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Schema, "schema"); err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	db := dao.DB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Ctx:      s.C.Request.Context(),
	}
	schemaLit := quoteStringLiteral(s.Schema)
	nameLit := quoteStringLiteral(s.Table)
	var query string
	switch kind {
	case "view":
		query = fmt.Sprintf(`SELECT table_schema, table_name, view_definition, check_option, is_updatable,
				definer, security_type, character_set_client, collation_connection
			FROM information_schema.views WHERE table_schema = %s AND table_name = %s`, schemaLit, nameLit)
	case "procedure", "function":
		routineType := "PROCEDURE"
		if kind == "function" {
			routineType = "FUNCTION"
		}
		query = fmt.Sprintf(`SELECT routine_schema, routine_name, routine_type, definer, created, last_altered,
				sql_mode, routine_comment, character_set_client, collation_connection, routine_body
			FROM information_schema.routines WHERE routine_schema = %s AND routine_name = %s AND routine_type = '%s'`,
			schemaLit, nameLit, routineType)
	case "trigger":
		query = fmt.Sprintf(`SELECT trigger_schema, trigger_name, event_object_table AS table_name, action_timing AS timing,
				event_manipulation AS event, action_statement AS statement, definer, created, sql_mode
			FROM information_schema.triggers WHERE trigger_schema = %s AND trigger_name = %s`, schemaLit, nameLit)
	case "event":
		query = fmt.Sprintf(`SELECT event_schema, event_name, definer, event_type, interval_value, interval_field,
				status, on_completion, starts, ends, last_executed, event_comment, sql_mode
			FROM information_schema.events WHERE event_schema = %s AND event_name = %s`, schemaLit, nameLit)
	default:
		return nil, fmt.Errorf("MySQL 不支持获取该对象类型的信息: %s", kind)
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetTableInfoService) objectBasePostgres(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	// 视图 / 物化视图沿用表概览查询（已覆盖 relkind v/m）
	if kind == "view" || kind == "mview" {
		databaseName, schemaName, err := s.objectTargetSchema()
		if err != nil {
			return nil, err
		}
		return s.getTableBasePostgres(instanceCfg, databaseName, schemaName)
	}
	databaseName, schemaName, err := s.objectTargetSchema()
	if err != nil {
		return nil, err
	}
	db, err := s.postgresObjectDB(instanceCfg, databaseName)
	if err != nil {
		return nil, err
	}
	schemaLit := quoteStringLiteral(schemaName)
	nameLit := quoteStringLiteral(s.Table)
	var query string
	switch kind {
	case "function", "procedure":
		prokinds := "'f'"
		objectType := "FUNCTION"
		if kind == "procedure" {
			prokinds = "'p'"
			objectType = "PROCEDURE"
		}
		query = fmt.Sprintf(`
			SELECT c.proname AS "NAME", n.nspname AS "SCHEMA_NAME", '%s' AS "OBJECT_TYPE",
				pg_get_userbyid(c.proowner) AS "OWNER", l.lanname AS "LANGUAGE",
				pg_get_function_identity_arguments(c.oid) AS "ARGUMENTS",
				pg_get_function_result(c.oid) AS "RETURNS",
				CASE WHEN c.prosecdef THEN 'YES' ELSE 'NO' END AS "SECURITY_DEFINER",
				obj_description(c.oid, 'pg_proc') AS "COMMENT"
			FROM pg_proc c
			JOIN pg_namespace n ON n.oid = c.pronamespace
			LEFT JOIN pg_language l ON l.oid = c.prolang
			WHERE n.nspname = %s AND c.proname = %s AND c.prokind IN (%s) LIMIT 1`,
			objectType, schemaLit, nameLit, prokinds)
	case "trigger":
		query = fmt.Sprintf(`
			SELECT t.tgname AS "NAME", n.nspname AS "SCHEMA_NAME", c.relname AS "TABLE_NAME",
				CASE WHEN t.tgenabled IN ('O','o') THEN 'ENABLED' ELSE 'DISABLED' END AS "STATUS",
				obj_description(t.oid, 'pg_trigger') AS "COMMENT"
			FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = %s AND t.tgname = %s AND NOT t.tgisinternal LIMIT 1`,
			schemaLit, nameLit)
	default:
		return nil, fmt.Errorf("PostgreSQL 不支持获取该对象类型的信息: %s", kind)
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetTableInfoService) objectBaseOracle(instanceCfg models.InsightInstances, kind string) (data *[]map[string]interface{}, err error) {
	db, err := s.oracleObjectDB(instanceCfg)
	if err != nil {
		return nil, err
	}
	objectType := strings.ToUpper(kind)
	if objectType == "VIEW" || objectType == "MVIEW" {
		objectType = "VIEW"
	}
	// Oracle 的 object_type：VIEW / MATERIALIZED VIEW / PROCEDURE / FUNCTION / TRIGGER
	typeFilter := quoteStringLiteral(objectType)
	if objectType == "VIEW" {
		// 后端把物化视图也归到 Views；概览两种类型都查出来，object_type 列自会说明
		typeFilter = `'VIEW','MATERIALIZED VIEW'`
	}
	catalog := "all_objects"
	buildQuery := func(view string) string {
		return fmt.Sprintf(`
		SELECT o.owner AS "OWNER", o.object_name AS "NAME", o.object_type AS "OBJECT_TYPE",
			o.created AS "CREATED", o.last_ddl_time AS "LAST_DDL_TIME", o.status AS "STATUS"
		FROM %s o
		WHERE o.owner = %s AND o.object_name = %s AND o.object_type IN (%s) AND rownum <= 1`,
			view, quoteStringLiteral(strings.ToUpper(s.Schema)), quoteStringLiteral(strings.ToUpper(s.Table)), typeFilter)
	}
	query := buildQuery(catalog)
	_, data, err = db.Query(query)
	// all_objects 看不到（未授权给当前用户）时降级 dba_objects，DBA 账号可覆盖全部 schema
	if err == nil && (data == nil || len(*data) == 0) {
		query = buildQuery("dba_objects")
		_, data, err = db.Query(query)
	}
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetTableInfoService) objectBaseClickHouse(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Schema, "schema"); err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT database, name, engine, total_rows, total_bytes
		FROM system.tables WHERE database = %s AND name = %s`,
		quoteStringLiteral(s.Schema), quoteStringLiteral(s.Table))
	db := &dao.ClickhouseDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Ctx:      s.C.Request.Context(),
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetTableInfoService) objectBaseSQLServer(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	// 两段路径必须拆开：sys.schemas 里没有名为 "msdb.dbo" 的 schema，
	// 且连接的默认库是 master，跨库必须走 [库].sys.* 完全限定名。
	database, schemaName, err := splitSQLServerResourcePath(s.Schema)
	if err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	dbPrefix := quoteBracketIdentifier(database)
	query := fmt.Sprintf(`SELECT s.name AS "SCHEMA_NAME", o.name AS "NAME", o.type_desc AS "OBJECT_TYPE",
			o.create_date AS "CREATED", o.modify_date AS "LAST_DDL_TIME"
		FROM %s.sys.objects o JOIN %[1]s.sys.schemas s ON s.schema_id = o.schema_id
		WHERE s.name = %s AND o.name = %s`,
		dbPrefix, quoteStringLiteral(schemaName), quoteStringLiteral(s.Table))
	_, data, err = s.sqlServerDB(instanceCfg, plainPassword).Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}
