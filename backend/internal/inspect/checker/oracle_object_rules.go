package checker

import (
	"database/sql"
	"fmt"
	"strings"

	go_ora "github.com/sijms/go-ora/v2"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// Oracle 元信息探针：规则 obj_*（目标对象存在性/改名冲突）与 DML 的表/字段
// 存在性检查需要连库取真实结构。
//
// 口径与 PG 引擎一致：查询异常一律返回 err，由调用方跳过该条检查——
// 宁可漏报也绝不误报（连不上库 ≠ 对象不存在）。
// 所有查询走 all_* 数据字典视图 + UPPER() 双侧归一（覆盖「双引号小写名」
// 与「未加引号折叠大写」两种书写）。

// oracleMetaResolver Oracle 元信息探针接口（生产走真实库；单测注入桩实现）
type oracleMetaResolver interface {
	TableExists(table string) (bool, error)
	ColumnExists(table, column string) (bool, error)
	// ColumnNotNullable 判断列是否定义为 NOT NULL（nullable='N'）
	ColumnNotNullable(table, column string) (bool, error)
	IndexExists(index string) (bool, error)
	TablePKColumns(table string) ([]string, error)
	TableColumnCount(table string) (int, error)
	countExistingIndexes(table string) (int, error)
	// CountWhere 对指定表按 WHERE 条件做 COUNT(*)（规则 limit_update_delete_affected_rows）
	CountWhere(table, whereClause string) (int64, error)
}

// oracleDBResolver 生产实现：按需懒连 Oracle（go-ora），失败返回 err
type oracleDBResolver struct {
	ctx    *oracleInspectContext
	schema string // 语句级 schema 覆盖（空 = 工单 InspectSchema）
}

// oracleConnMeta 生产探针持有连接信息的最小集合
type oracleConnMeta struct {
	User        string
	Password    string
	Host        string
	Port        int
	ServiceName string
}

func (r oracleDBResolver) owner() string {
	if s := strings.TrimSpace(r.schema); s != "" {
		return s
	}
	return strings.TrimSpace(r.ctx.schema)
}

// open 懒连接：同一上下文复用一个连接，避免一次预审反复建连。
// go-ora 纯 Go 实现，无需 Oracle Instant Client。
func (r oracleDBResolver) open() (*sql.DB, error) {
	if r.ctx.dbConn != nil {
		return r.ctx.dbConn, nil
	}
	m := r.ctx.conn
	if m == nil || strings.TrimSpace(m.ServiceName) == "" {
		return nil, fmt.Errorf("未初始化 Oracle 连接（service_name 缺失）")
	}
	dsn := go_ora.BuildUrl(m.Host, m.Port, m.ServiceName, m.User, m.Password, nil)
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	r.ctx.dbConn = db
	return db, nil
}

// queryCount 执行返回单个数值列的查询
func (r oracleDBResolver) queryCount(query string) (int, error) {
	db, err := r.open()
	if err != nil {
		return 0, err
	}
	var count int
	// 别名必须带双引号：Oracle 会把不带引号的别名折叠成大写，
	// db.QueryRow().Scan 按位置扫描不受影响，但保持与平台 Oracle 口径一致
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// ownerCond 返回 `owner = 'X'` 条件（未限定 schema 时不加条件，交给登录用户的
// 默认 schema——宁可漏报也不误报）
func (r oracleDBResolver) ownerCond() string {
	if o := r.owner(); o != "" {
		return fmt.Sprintf(" AND owner = %s", sqlQuoteLiteral(strings.ToUpper(o)))
	}
	return ""
}

func (r oracleDBResolver) TableExists(table string) (bool, error) {
	// all_objects 覆盖表/视图/物化视图（建表冲突判定三者都算，ORA-00955 对三者同名同样报错）
	count, err := r.queryCount(fmt.Sprintf(
		"SELECT COUNT(*) AS \"count\" FROM all_objects WHERE object_name = UPPER(%s) "+
			"AND object_type IN ('TABLE','VIEW','MATERIALIZED VIEW')%s",
		sqlQuoteLiteral(table), r.ownerCond()))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r oracleDBResolver) ColumnExists(table, column string) (bool, error) {
	count, err := r.queryCount(fmt.Sprintf(
		"SELECT COUNT(*) AS \"count\" FROM all_tab_columns WHERE table_name = UPPER(%s) "+
			"AND column_name = UPPER(%s)%s",
		sqlQuoteLiteral(table), sqlQuoteLiteral(column), r.ownerCond()))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r oracleDBResolver) ColumnNotNullable(table, column string) (bool, error) {
	count, err := r.queryCount(fmt.Sprintf(
		"SELECT COUNT(*) AS \"count\" FROM all_tab_columns WHERE table_name = UPPER(%s) "+
			"AND column_name = UPPER(%s) AND nullable = 'N'%s",
		sqlQuoteLiteral(table), sqlQuoteLiteral(column), r.ownerCond()))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r oracleDBResolver) IndexExists(index string) (bool, error) {
	count, err := r.queryCount(fmt.Sprintf(
		"SELECT COUNT(*) AS \"count\" FROM all_indexes WHERE index_name = UPPER(%s)%s",
		sqlQuoteLiteral(index), r.ownerCond()))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r oracleDBResolver) TablePKColumns(table string) ([]string, error) {
	db, err := r.open()
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(
		"SELECT cc.column_name FROM all_constraints c "+
			"JOIN all_cons_columns cc ON cc.owner = c.owner AND cc.constraint_name = c.constraint_name "+
			"WHERE c.constraint_type = 'P' AND c.table_name = UPPER(%s)%s "+
			"ORDER BY cc.position",
		sqlQuoteLiteral(table), r.ownerCond())
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func (r oracleDBResolver) TableColumnCount(table string) (int, error) {
	return r.queryCount(fmt.Sprintf(
		"SELECT COUNT(*) AS \"count\" FROM all_tab_columns WHERE table_name = UPPER(%s)%s",
		sqlQuoteLiteral(table), r.ownerCond()))
}

func (r oracleDBResolver) countExistingIndexes(table string) (int, error) {
	return r.queryCount(fmt.Sprintf(
		"SELECT COUNT(*) AS \"count\" FROM all_indexes WHERE table_name = UPPER(%s)%s",
		sqlQuoteLiteral(table), r.ownerCond()))
}

func (r oracleDBResolver) CountWhere(table, whereClause string) (int64, error) {
	db, err := r.open()
	if err != nil {
		return 0, err
	}
	qualified := sqlQuoteIdentifierForOracle(table, r.owner())
	query := fmt.Sprintf("SELECT COUNT(*) AS \"count\" FROM %s WHERE %s", qualified, whereClause)
	var count int64
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// sqlQuoteIdentifierForOracle 拼出 [schema.] 表名（schema/表名均按未加引号大写处理；
// 双引号包裹的小写名场景极少，且 UPPER() 判定口径一致，这里统一取大写）
func sqlQuoteIdentifierForOracle(table, schema string) string {
	name := strings.ToUpper(strings.TrimSpace(strings.Trim(table, `"`)))
	if s := strings.ToUpper(strings.TrimSpace(schema)); s != "" {
		return s + "." + name
	}
	return name
}

// metaResolver 取当前上下文的元信息探针（未注入桩实现时走真实库查询）
func (c *oracleInspectContext) metaResolver() oracleMetaResolver {
	if c.meta != nil {
		return c.meta
	}
	return oracleDBResolver{ctx: c}
}

// metaResolverFor 按语句级 schema 取探针（ALTER TABLE sch.t / DROP INDEX sch.idx 的
// schema 必须透传，否则会把「别的 schema 里的同名对象」误判——与 PG 引擎同一条红线）
func (c *oracleInspectContext) metaResolverFor(schema string) oracleMetaResolver {
	if c.meta != nil {
		return c.meta
	}
	return oracleDBResolver{ctx: c, schema: schema}
}

// ---------- ALTER TABLE 对象存在性/冲突 ----------

// checkAlterTableObjects ALTER TABLE 的对象级判定（规则 obj_tbl/obj_col/obj_idx 的 must 系列）。
//
// 保守原则与 PG 引擎一致：
//   - 目标表不存在 → 只报表级结论，不衍生列级噪音；
//   - 探测异常（连不上库/权限不足）→ 跳过该条检查，绝不误报。
func (c *oracleInspectContext) checkAlterTableObjects(stmt string) []controllers.SummaryItem {
	act, ok := parseOracleAlterTableActions(stmt)
	if !ok || strings.TrimSpace(act.Table) == "" {
		return nil
	}
	meta := c.metaResolverFor(act.Schema)
	var items []controllers.SummaryItem

	// ① 目标表必须存在
	exists, err := meta.TableExists(act.Table)
	if err != nil {
		return nil // 连不上库：对象类检查整体跳过，静态规则不受影响
	}
	if !exists {
		return []controllers.SummaryItem{pgObjectMissing("obj_tbl_must_exist", act.Table,
			"① 核对表名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认表是否在其它 schema 下（需 schema.表名 限定）；③ 确认是否已被删除",
			"表`%s`不存在，ALTER 语句执行必然失败", act.Table)}
	}

	// ② DROP / MODIFY 的目标列必须存在
	for _, col := range act.DropColumns {
		items = append(items, c.checkColumnExists(meta, act.Table, col)...)
	}
	for _, col := range act.ModifyColumns {
		items = append(items, c.checkColumnExists(meta, act.Table, col.Name)...)
	}

	// ③ 改名：旧列必须存在、新列名不能已存在（新旧同名视为无操作，跳过）
	for _, rc := range act.RenameColumns {
		if strings.EqualFold(rc.Old, rc.New) {
			continue
		}
		items = append(items, c.checkColumnExists(meta, act.Table, rc.Old)...)
		if exists, err := meta.ColumnExists(act.Table, rc.New); err == nil && exists {
			items = append(items, pgObjectConflict("obj_col_must_not_exist", rc.New,
				"① 更换新列名；② 确认同名列是否可删除",
				"改名后的列名`%s`已存在（改名后执行必然失败）", rc.New))
		}
	}

	// ④ 表改名：新表名不能已存在
	if act.RenameTable != "" && !strings.EqualFold(act.Table, act.RenameTable) {
		if exists, err := meta.TableExists(act.RenameTable); err == nil && exists {
			items = append(items, pgObjectConflict("obj_tbl_must_not_exist", act.RenameTable,
				"① 更换新表名；② 确认同名表是否可删除",
				"改名后的表名`%s`已存在（改名后执行必然失败）", act.RenameTable))
		}
	}
	return items
}

// checkColumnExists 单列存在性判定（规则 obj_col_must_exist）
func (c *oracleInspectContext) checkColumnExists(meta oracleMetaResolver, table, column string) []controllers.SummaryItem {
	exists, err := meta.ColumnExists(table, column)
	if err != nil || exists {
		return nil
	}
	return []controllers.SummaryItem{pgObjectMissing("obj_col_must_exist", column,
		"① 核对列名拼写（Oracle 未加引号的标识符折叠为大写）；② 确认列是否已删除；③ 如需新增请先提交 ADD COLUMN",
		"表`%s`上不存在列`%s`", table, column)}
}
