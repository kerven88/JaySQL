package services

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/pkg/secret"
)

// ---------------------------------------------------------------------------
// SQL Server 的表结构（DDL）与表概览
//
// SQL Server 没有 SHOW CREATE TABLE，也没有 PG 那种 pg_get_tabledef，
// 只能用系统视图把列定义重新拼出来——这是它过去拿不到「查看表信息」的根因。
//
// 路径口径（最容易踩）：SQL Server 是三层结构，前端传进来的 s.Schema 是
//「库.模式」（与 insight_instance_schemas.schema 的存法一致），必须拆成两段：
//   - 不能整段拿去 sys.schemas 匹配，没有任何 schema 叫 "msdb.dbo"；
//   - 连接 DSN 的默认库是 master，跨库必须写完全限定名 [库].[模式].[对象]，
//     本次连接是 Query 内临时建的，没有地方给你先 USE 切库。
// ---------------------------------------------------------------------------

func quoteBracketIdentifier(value string) string {
	return "[" + strings.ReplaceAll(value, "]", "]]") + "]"
}

// splitSQLServerResourcePath 拆分「库.模式」资源路径并逐段校验。
func splitSQLServerResourcePath(path string) (database, schema string, err error) {
	text := strings.TrimSpace(path)
	idx := strings.Index(text, ".")
	if idx <= 0 {
		return "", "", fmt.Errorf("SQL Server 资源路径不合法：%s（应为「库.模式」）", text)
	}
	database = strings.TrimSpace(text[:idx])
	schema = strings.TrimSpace(text[idx+1:])
	if strings.Contains(schema, ".") {
		return "", "", fmt.Errorf("SQL Server 资源路径不合法：%s（只应有两段）", text)
	}
	if err := validateIdentifier(database, "database"); err != nil {
		return "", "", err
	}
	if err := validateIdentifier(schema, "schema"); err != nil {
		return "", "", err
	}
	return database, schema, nil
}

// sqlServerQualifiedName 组装 [库].[模式].[对象] 完全限定名。
func sqlServerQualifiedName(database, schema, object string) string {
	return quoteBracketIdentifier(database) + "." + quoteBracketIdentifier(schema) + "." + quoteBracketIdentifier(object)
}

func (s *GetTableInfoService) sqlServerDB(instanceCfg models.InsightInstances, plainPassword string) *dao.SQLServerDB {
	return &dao.SQLServerDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Ctx:      s.C.Request.Context(),
	}
}

// getTableStrucSQLServer 用系统视图拼出 CREATE TABLE 语句。
//
// 输出刻意贴近 MySQL 的 SHOW CREATE TABLE 形态（列定义 + 括号内索引声明），
// 因为前端「查看表信息」抽屉是按 DDL 文本反解列 / 索引的——
// 换成 sp_help 那种多结果集，前端就得再写一套解析器。
func (s *GetTableInfoService) getTableStrucSQLServer(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	database, schemaName, err := splitSQLServerResourcePath(s.Schema)
	if err != nil {
		return nil, err
	}
	if err := validateIdentifier(s.Table, "table"); err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}

	db := s.sqlServerDB(instanceCfg, plainPassword)
	qualified := sqlServerQualifiedName(database, schemaName, s.Table)

	_, columnRows, qerr := db.Query(sqlServerColumnsQuery(database, schemaName, s.Table))
	if qerr != nil {
		global.App.Log.Errorf("getTableStrucSQLServer columns failed: %v", qerr)
		return nil, qerr
	}
	if len(*columnRows) == 0 {
		return nil, fmt.Errorf("在 %s.%s 下未找到表 %s", database, schemaName, s.Table)
	}

	// 索引：主键 / 唯一 / 普通索引都写成 MySQL 风格的括号声明，前端可直接反解
	indexLines, ierr := s.sqlServerIndexLines(db, database, qualified)
	if ierr != nil {
		global.App.Log.Warnf("getTableStrucSQLServer 读取索引失败（DDL 仅含列）: %v", ierr)
	}

	return &[]map[string]interface{}{
		{"Create Table": buildSQLServerTableDDL(qualified, columnRows, indexLines)},
	}, nil
}

// buildSQLServerTableDDL 把列 / 索引查询结果拼成 CREATE TABLE 语句。
//
// 输出形态刻意贴近 MySQL 的 SHOW CREATE TABLE：列定义在括号内逐行列出，
// 索引也写成 MySQL 风格的括号声明。前端「查看表信息」是按 DDL 文本反解列与索引的，
// 换成 SQL Server 原生那套（sp_help 多结果集、SSMS 脚本）前端就得再写一套解析器。
func buildSQLServerTableDDL(qualified string, columnRows *[]map[string]interface{}, indexLines []string) string {
	if columnRows == nil || len(*columnRows) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("CREATE TABLE " + qualified + " (\n")
	for i, row := range *columnRows {
		builder.WriteString("  " + quoteBracketIdentifier(stringValue(row, "COLUMN_NAME")))
		builder.WriteString(" " + buildSQLServerColumnType(row))
		if stringValue(row, "IS_IDENTITY") == "1" {
			builder.WriteString(" IDENTITY")
		}
		if strings.EqualFold(stringValue(row, "IS_NULLABLE"), "NO") {
			builder.WriteString(" NOT NULL")
		}
		if def := strings.TrimSpace(stringValue(row, "COLUMN_DEFAULT")); def != "" {
			builder.WriteString(" DEFAULT " + def)
		}
		// 最后一列后不写逗号：索引行自带前置逗号，两者都写会出现 ", ,"
		if i < len(*columnRows)-1 || len(indexLines) > 0 {
			builder.WriteString(",")
		}
		builder.WriteString("\n")
	}
	for _, line := range indexLines {
		builder.WriteString("  " + line + "\n")
	}
	builder.WriteString(");")
	return builder.String()
}

// sqlServerColumnsQuery 列元数据查询。
//
// IsIdentity 用 COLUMNPROPERTY(OBJECT_ID(...)) 而不是 sys.columns.is_identity：
// 后者需要连到目标库才能读，而连接默认库是 master，跨库场景拿不到。
// 传进去的 qualified 是完全限定名，OBJECT_ID 在任意库上下文都能解析。
func sqlServerColumnsQuery(database, schemaName, table string) string {
	qualified := sqlServerQualifiedName(database, schemaName, table)
	return fmt.Sprintf(`
		SELECT
			c.COLUMN_NAME                          AS COLUMN_NAME,
			c.DATA_TYPE                            AS DATA_TYPE,
			CAST(c.CHARACTER_MAXIMUM_LENGTH AS int) AS CHAR_MAX_LEN,
			CAST(c.NUMERIC_PRECISION AS int)        AS NUM_PRECISION,
			CAST(c.NUMERIC_SCALE AS int)            AS NUM_SCALE,
			CAST(c.DATETIME_PRECISION AS int)       AS DT_PRECISION,
			c.IS_NULLABLE                          AS IS_NULLABLE,
			c.COLUMN_DEFAULT                       AS COLUMN_DEFAULT,
			CAST(ISNULL(COLUMNPROPERTY(OBJECT_ID(N%s), c.COLUMN_NAME, 'IsIdentity'), 0) AS int) AS IS_IDENTITY
		FROM %s.INFORMATION_SCHEMA.COLUMNS c
		WHERE c.TABLE_SCHEMA = %s AND c.TABLE_NAME = %s
		ORDER BY c.ORDINAL_POSITION`,
		quoteStringLiteral(qualified), quoteBracketIdentifier(database),
		quoteStringLiteral(schemaName), quoteStringLiteral(table))
}

// sqlServerIndexLines 读取主键与索引，产出 DDL 内的索引声明行。
// 包含列（is_included_column=1）不参与展示，否则会把覆盖列误报成索引列。
func (s *GetTableInfoService) sqlServerIndexLines(db *dao.SQLServerDB, database, qualified string) ([]string, error) {
	_, rows, err := db.Query(sqlServerIndexQuery(database, qualified))
	if err != nil {
		return nil, err
	}

	type indexRow struct {
		name    string
		kind    string
		columns []string
	}
	order := make([]string, 0, 4)
	indexes := make(map[string]*indexRow, 4)
	for _, row := range *rows {
		name := stringValue(row, "INDEX_NAME")
		kind := "KEY"
		if stringValue(row, "IS_PRIMARY_KEY") == "1" {
			kind = "PRIMARY KEY"
		} else if stringValue(row, "IS_UNIQUE") == "1" {
			kind = "UNIQUE KEY"
		}
		existing, ok := indexes[name]
		if !ok {
			existing = &indexRow{name: name, kind: kind}
			indexes[name] = existing
			order = append(order, name)
		}
		existing.columns = append(existing.columns, quoteBracketIdentifier(stringValue(row, "COLUMN_NAME")))
	}

	lines := make([]string, 0, len(order))
	for _, name := range order {
		item := indexes[name]
		head := item.kind
		if item.kind != "PRIMARY KEY" {
			head += " " + quoteBracketIdentifier(item.name)
		}
		lines = append(lines, fmt.Sprintf("%s (%s)", head, strings.Join(item.columns, ", ")))
	}
	return lines, nil
}

// buildSQLServerColumnType 按 INFORMATION_SCHEMA 的元数据还原列类型字面值。
func buildSQLServerColumnType(row map[string]interface{}) string {
	dataType := strings.ToLower(strings.TrimSpace(stringValue(row, "DATA_TYPE")))
	switch dataType {
	case "varchar", "char", "nvarchar", "nchar", "varbinary", "binary":
		length := stringValue(row, "CHAR_MAX_LEN")
		if length == "-1" {
			return dataType + "(max)"
		}
		if length != "" && length != "0" {
			return fmt.Sprintf("%s(%s)", dataType, length)
		}
		return dataType
	case "decimal", "numeric":
		precision := stringValue(row, "NUM_PRECISION")
		scale := stringValue(row, "NUM_SCALE")
		if precision != "" && scale != "" {
			return fmt.Sprintf("%s(%s,%s)", dataType, precision, scale)
		}
		return dataType
	case "datetime2", "datetimeoffset", "time":
		precision := stringValue(row, "DT_PRECISION")
		if precision != "" && precision != "0" {
			return fmt.Sprintf("%s(%s)", dataType, precision)
		}
		return dataType
	}
	return dataType
}

// getTableBaseSQLServer 表概览，字段口径对齐 MySQL 的 information_schema.tables，
// 前端「表信息」页按同一套标签渲染（行数 / 数据大小 / 索引大小 / 创建时间…）。
func (s *GetTableInfoService) getTableBaseSQLServer(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	database, schemaName, err := splitSQLServerResourcePath(s.Schema)
	if err != nil {
		return nil, err
	}
	if err := validateIdentifier(s.Table, "table"); err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}

	_, data, err = s.sqlServerDB(instanceCfg, plainPassword).
		Query(sqlServerTableBaseQuery(database, schemaName, s.Table))
	if err != nil {
		global.App.Log.Errorf("getTableBaseSQLServer query failed: %v", err)
		return nil, err
	}
	return data, nil
}

// sqlServerTableBaseQuery 表概览查询。
//
// 页数 → 字节：SQL Server 一页恒为 8 KB。
// 行数只看堆 / 聚集索引（index_id 0/1），否则每个非聚集索引都贡献一份行记数，
// 行数会被放大 N 倍——这是 SQL Server 取行数最常见的错法。
func sqlServerTableBaseQuery(database, schemaName, table string) string {
	return fmt.Sprintf(`
		SELECT TOP 1
			%s                                             AS TABLE_CATALOG,
			sch.name                                       AS TABLE_SCHEMA,
			t.name                                         AS TABLE_NAME,
			'BASE TABLE'                                   AS TABLE_TYPE,
			''                                             AS ENGINE,
			CAST(ISNULL(rc.rows_cnt, 0) AS bigint)         AS TABLE_ROWS,
			CAST(ISNULL(auc.total_pages, 0) * 8192 AS bigint) AS DATA_LENGTH,
			CAST(ISNULL(auc.used_pages - auc.data_pages, 0) * 8192 AS bigint) AS INDEX_LENGTH,
			CAST(CASE WHEN ISNULL(rc.rows_cnt, 0) > 0
				THEN ISNULL(auc.data_pages, 0) * 8192 / rc.rows_cnt ELSE 0 END AS bigint) AS AVG_ROW_LENGTH,
			CAST(0 AS bigint)                              AS DATA_FREE,
			CAST(0 AS bigint)                              AS MAX_DATA_LENGTH,
			CAST(0 AS bigint)                              AS VERSION,
			''                                             AS ROW_FORMAT,
			CAST(0 AS bigint)                              AS AUTO_INCREMENT,
			t.create_date                                  AS CREATE_TIME,
			t.modify_date                                  AS UPDATE_TIME,
			CAST(NULL AS datetime)                         AS CHECK_TIME,
			ISNULL(coll.collation_name, '')                AS TABLE_COLLATION,
			ISNULL(desc_prop.value, '')                    AS TABLE_COMMENT
		FROM %s.sys.tables t
		JOIN %[2]s.sys.schemas sch ON sch.schema_id = t.schema_id
		OUTER APPLY (
			SELECT SUM(p.rows) AS rows_cnt
			FROM %[2]s.sys.partitions p
			WHERE p.object_id = t.object_id AND p.index_id IN (0, 1)
		) rc
		OUTER APPLY (
			SELECT
				SUM(a.total_pages) AS total_pages,
				SUM(a.used_pages)  AS used_pages,
				SUM(a.data_pages)  AS data_pages
			FROM %[2]s.sys.partitions p
			JOIN %[2]s.sys.allocation_units a ON a.container_id = p.hobt_id
			WHERE p.object_id = t.object_id AND p.index_id IN (0, 1)
		) auc
		OUTER APPLY (
			SELECT TOP 1 c.collation_name
			FROM %[2]s.sys.columns c
			WHERE c.object_id = t.object_id AND c.collation_name IS NOT NULL
		) coll
		OUTER APPLY (
			SELECT TOP 1 CONVERT(nvarchar(4000), ep.value) AS value
			FROM %[2]s.sys.extended_properties ep
			WHERE ep.major_id = t.object_id AND ep.minor_id = 0 AND ep.name = N'MS_Description'
		) desc_prop
		WHERE sch.name = %s AND t.name = %s`,
		quoteStringLiteral(database), quoteBracketIdentifier(database),
		quoteStringLiteral(schemaName), quoteStringLiteral(table))
}

// sqlServerIndexQuery 索引元数据查询（主键 / 唯一 / 普通索引，含列顺序）。
func sqlServerIndexQuery(database, qualified string) string {
	return fmt.Sprintf(`
		SELECT
			i.name           AS INDEX_NAME,
			CAST(i.is_primary_key AS int) AS IS_PRIMARY_KEY,
			CAST(i.is_unique AS int)      AS IS_UNIQUE,
			c.name           AS COLUMN_NAME
		FROM %s.sys.indexes i
		JOIN %[1]s.sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN %[1]s.sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE i.object_id = OBJECT_ID(N%s)
		  AND ic.is_included_column = 0
		  AND i.name IS NOT NULL
		ORDER BY i.is_primary_key DESC, i.name, ic.key_ordinal`,
		quoteBracketIdentifier(database), quoteStringLiteral(qualified))
}

// stringValue 从查询结果行里取字符串值（dao 层把所有列都读成 string / nil）。
func stringValue(row map[string]interface{}, key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}
