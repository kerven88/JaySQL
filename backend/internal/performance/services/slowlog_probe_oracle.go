package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/pkg/secret"
)

// ============================================================================
// Oracle 慢 SQL 采集 —— 数据源为 V$SQL 增量采样
//
// 为什么是 V$SQL 采样，而不是像 MySQL / PG 那样读日志文件：
//   - **Oracle 没有"慢日志文件"这一标准产物**。alert log 只记实例级事件；
//     要拿到单条 SQL 级数据，官方路径是 AWR（`DBA_HIST_SQLSTAT`）或
//     SQL Trace（10046，需开 trace + tkprof 解析，运维成本极高）。
//   - `DBA_HIST_SQLSTAT` 依赖 **Diagnostics Pack 授权**。相当多客户没有买，
//     默认依赖它会导致"功能存在但大面积不可用"，属于产品事故级别的坑。
//   - `V$SQL` 是库缓存视图，**任何版本、任何授权版本都能查**，只要账号有
//     `SELECT_CATALOG_ROLE` 或显式 `V_$SQL` 权限。
//
//   代价是：V$SQL 里的 `ELAPSED_TIME` / `EXECUTIONS` 是**累计值**，
//   必须自己做增量差分才能得到"这段时间花了多久"。
//   如果直接用累计值当耗时，统计会完全失真——这是本方案最需要注意的点。
//
// 采样周期建议 60s：与监控采集同频，既能捕捉短时尖峰，又不至于对库造成压力。
// ============================================================================

// oracleSampleQuery 抓「采样窗口内活跃过」的 SQL。
//
// 关键：Oracle 的别名不带双引号会被折叠成大写，而 dao.OracleDB 直接用
// rows.Columns() 当 map key —— 所以**必须全部带双引号**，否则按小写取值永远取不到，
// toString(nil) 会得到字符串 "<nil>" 并写进库里（这个项目已经踩过一次）。
//
// LAST_ACTIVE_TIME 是 DATE 类型（秒级精度），增量判断用它即可，无需更高精度。
const oracleSampleQuery = `
SELECT
    s.sql_id                                   AS "sql_id",
    s.plan_hash_value                          AS "plan_hash_value",
    s.parsing_schema_name                      AS "parsing_schema_name",
    s.module                                   AS "module",
    s.executions                               AS "executions",
    s.elapsed_time                             AS "elapsed_time",
    s.cpu_time                                 AS "cpu_time",
    s.buffer_gets                              AS "buffer_gets",
    s.disk_reads                               AS "disk_reads",
    s.rows_processed                           AS "rows_processed",
    s.last_active_time                         AS "last_active_time",
    DBMS_LOB.SUBSTR(s.sql_fulltext, 4000, 1)   AS "sql_text"
FROM v$sql s
WHERE s.last_active_time > %s
  AND s.executions > 0
  AND s.sql_text NOT LIKE 'BEGIN%%'
ORDER BY s.elapsed_time DESC
FETCH FIRST %d ROWS ONLY`

// oracleProbeQuery 探测阶段：同时验证连通性、版本、关键参数与 V$SQL 权限。
//
// 为什么把权限校验和参数查询合并成一条 SQL：
//
//	探测每 10 分钟跑一遍，两条 SQL 就是两倍 RTT；更重要的是，
//	「能连上但查不了 V$SQL」和「连都连不上」是两个完全不同的故障，
//	必须分开判定，否则用户会一直去改账号密码却毫无效果。
const oracleProbeQuery = `
SELECT
    (SELECT version_full FROM v$instance) AS "db_version",
    (SELECT value FROM v$parameter WHERE name = 'statistics_level') AS "statistics_level",
    (SELECT value FROM v$parameter WHERE name = 'compatible') AS "compatible",
    (SELECT COUNT(*) FROM v$sql WHERE ROWNUM <= 1) AS "v_sql_reachable"
FROM dual`

// oracleProbeQueryLegacy version_full 从 12.2 起才有，12.1 及更早报 ORA-00904。
// 这里回退到各版本都有的 version —— 只含主版本，但总比采不到强。
const oracleProbeQueryLegacy = `
SELECT
    (SELECT version FROM v$instance) AS "db_version",
    (SELECT value FROM v$parameter WHERE name = 'statistics_level') AS "statistics_level",
    (SELECT value FROM v$parameter WHERE name = 'compatible') AS "compatible",
    (SELECT COUNT(*) FROM v$sql WHERE ROWNUM <= 1) AS "v_sql_reachable"
FROM dual`

// oracleStatsLevelBasic statistics_level=BASIC 时 V$SQL 的耗时与等待类指标不更新。
const oracleStatsLevelBasic = "BASIC"

// oracleMaxSamples 单次采样抓取的最大 SQL 条数，避免库缓存很大时把请求拖垮。
const oracleMaxSamples = 500

// oracleSlowLogSource 是该引擎的数据源标识，写入 collector.source 供前端区分。
const oracleSlowLogSource = "V$SQL"

// probeOracleSlowLogCapability 探测 Oracle 实例的慢 SQL 可采集性。
func probeOracleSlowLogCapability(instance *commonModels.InsightInstances) *slowLogCapability {
	instanceID := strings.TrimSpace(instance.InstanceID)
	user := strings.TrimSpace(instance.User)

	password := strings.TrimSpace(instance.Password)
	if password != "" {
		if plain, err := secret.Decrypt(password); err == nil && strings.TrimSpace(plain) != "" {
			password = strings.TrimSpace(plain)
		}
	}
	if user == "" || password == "" {
		return oracleCapability(instanceID, "", "", "", failOracleNoCredential,
			"未配置可用的实例账号或密码，无法探测服务端配置")
	}

	serviceName := serviceNameFromExtra(instance.ExtraConfig)
	if strings.TrimSpace(serviceName) == "" {
		return oracleCapability(instanceID, "", "", "", failOracleNoCredential,
			"实例扩展参数未配置 service_name，Oracle 连接无法建立")
	}

	ctx, cancel := context.WithTimeout(context.Background(), slowLogProbeTimeout)
	defer cancel()

	db := dao.OracleDB{
		User:          user,
		Password:      password,
		Host:          strings.TrimSpace(instance.Hostname),
		Port:          instance.Port,
		ServiceName:   serviceName,
		CurrentSchema: strings.TrimSpace(instance.OrganizationKey),
		Ctx:           ctx,
	}

	_, rows, err := db.Query(oracleProbeQuery)
	if err != nil && strings.Contains(strings.ToUpper(err.Error()), "ORA-00904") {
		// version_full 列不存在 → 旧版 Oracle，换用 version
		_, rows, err = db.Query(oracleProbeQueryLegacy)
	}
	if err != nil {
		return oracleCapability(instanceID, "", oracleStatsLevelsAll, "", failOracleConnectFailed,
			fmt.Sprintf("连接实例查询配置失败：%v", err))
	}
	if rows == nil || len(*rows) == 0 {
		return oracleCapability(instanceID, "", oracleStatsLevelsAll, "", failOracleConnectFailed,
			"探测查询无返回行，疑似账号权限不足或实例异常")
	}

	// V$SQL 权限单独校验：连得上不代表能采
	reachable := pgText((*rows)[0], "v_sql_reachable")
	if strings.EqualFold(strings.TrimSpace(reachable), "0") {
		return oracleCapability(instanceID, "", oracleStatsLevelsAll, "", failOracleNoVSQLAccess,
			"账号已连上实例，但无 V$SQL 访问权限（需 SELECT_CATALOG_ROLE 或显式授予 V_$SQL）")
	}

	version := strings.TrimSpace(pgText((*rows)[0], "db_version"))
	statsLevel := strings.ToUpper(strings.TrimSpace(pgText((*rows)[0], "statistics_level")))
	if statsLevel == oracleStatsLevelBasic {
		return oracleCapability(instanceID, version, statsLevel, "", failOracleStatsBasic,
			"statistics_level=BASIC，V$SQL 的耗时与等待类指标不更新，采上来的数据全是 0")
	}

	// Oracle 走采样，不像文件方式那样有 ResolvedPath；
	// 这里用「V$SQL」回填，前端对 Oracle 不展示文件路径列。
	return oracleCapability(instanceID, version, statsLevel, oracleSlowLogSource, "", "")
}

// oracleCapability 构造统一的采集能力结构。
//
// 语义对齐说明：Oracle 没有文件，但采集门控 slowLogCollectable() 依赖
// SlowQueryLogEnabled && FileCapable。这里把两者都置为 true，含义是
// 「数据源可达且有权限」，而非字面意义的"开了文件日志"。
// 前端展示时按 source=V$SQL 走 Oracle 专用文案，不会误导。
func oracleCapability(instanceID, version, statsLevel, resolvedPath, failureCode, detail string) *slowLogCapability {
	capability := &slowLogCapability{
		InstanceID:      instanceID,
		ProbeStatus:     probeStatusOK,
		ServerVersion:   version,
		SlowLogEnabled:  failureCode == "",
		LogOutputMode:   statsLevel,
		FileCapable:     failureCode == "",
		TableCapable:    false,
		RawLogFile:      oracleSlowLogSource,
		Datadir:         "",
		ResolvedPath:    resolvedPath,
		LongQueryTimeMS: 0,
		ProbedAt:        time.Now(),
		FailureCode:     failureCode,
		ProbeError:      detail,
	}
	if failureCode != "" {
		capability.ProbeStatus = probeStatusUnsupported
		capability.State = capabilityUnsupported
		return capability
	}
	capability.State = decideSlowLogCapabilityState(capability)
	return capability
}

// oracleVSQLSample 一次采样得到的单条 SQL。
type oracleVSQLSample struct {
	SQLID         string
	PlanHash      string
	SchemaName    string
	Module        string
	Executions    int64
	ElapsedUS     int64
	CPUUS         int64
	BufferGets    int64
	DiskReads     int64
	RowsProcessed int64
	LastActive    time.Time
	SQLText       string
}

// collectOracleSlowLogSample 执行一次 V$SQL 增量采样。
//
// 入参 lastSampleAt 是上一次采样时刻；首次为 0，此时取最近 oracleFirstSampleWindow 内的 SQL。
// 返回采样结果与本次采样时刻，由调用方落库为下一次的基线。
func collectOracleSlowLogSample(ctx context.Context, instance *commonModels.InsightInstances,
	password string, lastSampleAt time.Time, maxRows int) ([]oracleVSQLSample, time.Time, error) {
	if maxRows <= 0 {
		maxRows = oracleMaxSamples
	}
	base := lastSampleAt
	if base.IsZero() {
		base = time.Now().Add(-oracleFirstSampleWindow)
	}

	db := &dao.OracleDB{User: strings.TrimSpace(instance.User), Password: password,
		Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
		ServiceName:   serviceNameFromExtra(instance.ExtraConfig),
		CurrentSchema: strings.TrimSpace(instance.OrganizationKey), Ctx: ctx}

	_, rows, err := db.Query(fmt.Sprintf(oracleSampleQuery, oracleTimeLiteral(base), maxRows))
	if err != nil {
		return nil, time.Time{}, err
	}
	if rows == nil {
		return nil, time.Time{}, fmt.Errorf("采样返回为空")
	}

	out := make([]oracleVSQLSample, 0, len(*rows))
	for _, row := range *rows {
		out = append(out, oracleVSQLSample{
			SQLID:         pgText(row, "sql_id"),
			PlanHash:      pgText(row, "plan_hash_value"),
			SchemaName:    pgText(row, "parsing_schema_name"),
			Module:        pgText(row, "module"),
			Executions:    pgInt(row, "executions"),
			ElapsedUS:     pgInt(row, "elapsed_time"),
			CPUUS:         pgInt(row, "cpu_time"),
			BufferGets:    pgInt(row, "buffer_gets"),
			DiskReads:     pgInt(row, "disk_reads"),
			RowsProcessed: pgInt(row, "rows_processed"),
			SQLText:       pgText(row, "sql_text"),
			LastActive:    pgTime(row, "last_active_time"),
		})
	}
	return out, time.Now(), nil
}
