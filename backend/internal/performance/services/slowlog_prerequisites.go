package services

import (
	dbfamily "github.com/simajiong/jaysql/internal/common/dbfamily"
)

// ============================================================================
// 慢 SQL 采集的「前提条件」声明库
//
// 为什么要把前提条件和采集代码分开：
//   采集代码只负责「连上去、读回来」，失败时给出的是一句技术性的 error；
//   但对使用者来说，真正需要的是「我该去改什么」。把每一类失败显式绑定到
//   一个前提项（版本 / 参数 / 权限 / 通道 / 文件），前端才能在异常旁边直接
//   给出自助修复步骤，而不是让用户拿着一句 error 去找运维。
//
//   同时，这份声明是**唯一事实来源**：前端的「采集前提条件」面板、状态列的
//   异常说明、以及自检 SQL 全部从这里出，不另外维护一份文案，避免改了代码
//   忘了改提示。
//
// 约定：
//   - Key 是稳定标识，前端用它做「失败 → 前提项」的关联与高亮，不要随意改名；
//   - Codes 是采集器给出的失败码，一个前提项可以对应多个失败码；
//   - Verify 是能直接在目标库上跑的只读语句，用于用户自检。
// ============================================================================

// 前提项分类（前端按 kind 分组展示 + 配色）
const (
	prereqKindVersion    = "version"
	prereqKindParameter  = "parameter"
	prereqKindPermission = "permission"
	prereqKindFeature    = "feature"
	prereqKindChannel    = "channel"
	prereqKindFile       = "file"
	// prereqKindCaliber 统计口径说明：不是"前提条件"，而是解释页面数字与日志行数的差异，
	// 单独成组，避免混进"必须满足才能采集"的清单里制造误解。
	prereqKindCaliber = "caliber"
)

// PrerequisiteItem 单个前提条件。
type PrerequisiteItem struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Requirement string   `json:"requirement"`
	HowTo       string   `json:"how_to"`
	Consequence string   `json:"consequence"`
	Verify      string   `json:"verify"`
	Codes       []string `json:"codes"`
}

// ---------- 失败码 ----------
//
// 命名规则：<引擎>_<问题>，采集器只产出这些常量，前端按码找前提项。
// 不要在这里放具体 error 文本——文本由采集器在 ProbeError 里带上下文。

// 引擎级失败码（跨引擎通用，与具体数据库无关）
const (
	failEngineUnsupported   = "ENGINE_UNSUPPORTED"
	failEngineNoCredential  = "ENGINE_NO_CREDENTIAL"
	failEngineSSHFailed     = "ENGINE_SSH_FAILED"
	failEngineFileMissing   = "ENGINE_FILE_MISSING"
	failEngineParseFailed   = "ENGINE_PARSE_FAILED"
	failEnginePersistFailed = "ENGINE_PERSIST_FAILED"
)

// MySQL 失败码
const (
	failMySQLNoCredential     = "MYSQL_NO_CREDENTIAL"
	failMySQLConnectFailed    = "MYSQL_CONNECT_FAILED"
	failMySQLVariableMissing  = "MYSQL_VARIABLE_MISSING"
	failMySQLSlowLogDisabled  = "MYSQL_SLOW_LOG_DISABLED"
	failMySQLLogOutputNoFile  = "MYSQL_LOG_OUTPUT_NO_FILE"
	failMySQLLongQueryTimeBad = "MYSQL_LONG_QUERY_TIME_INVALID"
	failMySQLSSHUnreachable   = "MYSQL_SSH_UNREACHABLE"
	failMySQLFileMissing      = "MYSQL_FILE_MISSING"
	failMySQLFileNoRead       = "MYSQL_FILE_NO_PERMISSION"
	failMySQLParseFailed      = "MYSQL_PARSE_FAILED"
)

// PostgreSQL 失败码
const (
	failPGNoCredential        = "PG_NO_CREDENTIAL"
	failPGConnectFailed       = "PG_CONNECT_FAILED"
	failPGVersionUnsupported  = "PG_VERSION_UNSUPPORTED"
	failPGCollectorOff        = "PG_LOGGING_COLLECTOR_OFF"
	failPGMinDurationDisabled = "PG_MIN_DURATION_DISABLED"
	failPGMinDurationZero     = "PG_MIN_DURATION_ZERO"
	failPGPrefixMissingUserDB = "PG_PREFIX_MISSING_USER_DB"
	failPGNoMonitorRole       = "PG_NO_MONITOR_ROLE"
	failPGLogFileUnreadable   = "PG_LOG_FILE_UNREADABLE"
	failPGParseFailed         = "PG_PARSE_FAILED"
	// failPGLogPathUnresolved 探测拿不到当前日志文件（pg_current_logfile() 返回空），
	// 连路径都没有，谈不上「文件缺失」——归到参数/权限未就绪，不报故障。
	failPGLogPathUnresolved = "PG_LOG_PATH_UNRESOLVED"
)

// Oracle 失败码
const (
	failOracleNoCredential  = "ORACLE_NO_CREDENTIAL"
	failOracleConnectFailed = "ORACLE_CONNECT_FAILED"
	failOracleNoVSQLAccess  = "ORACLE_NO_VSQL_ACCESS"
	failOracleStatsBasic    = "ORACLE_STATISTICS_LEVEL_BASIC"
	failOracleVersionTooOld = "ORACLE_VERSION_UNSUPPORTED"
	failOracleSampleFailed  = "ORACLE_SAMPLE_FAILED"
	failOracleAWRNoLicense  = "ORACLE_AWR_NO_LICENSE"
	// failOraclePersistFailed 采样数据写库失败。与 failOracleSampleFailed 分开：
	// 前者说明 Oracle 侧正常、平台侧有故障，两者的排查方向完全不同。
	failOraclePersistFailed = "ORACLE_PERSIST_FAILED"
)

// ---------- 对外：按引擎取前提项 ----------

// SlowLogPrerequisites 返回指定数据库类型的采集前提清单。
// 未知引擎返回空列表——前端据此显示"暂不支持"，而不是编造一份前提项。
func SlowLogPrerequisites(dbType string, extraConfig []byte) []PrerequisiteItem {
	switch dbfamily.EffectiveFamily(dbType, extraConfig) {
	case dbfamily.FamilyMySQL:
		return mysqlSlowLogPrerequisites()
	case dbfamily.FamilyPostgres:
		return postgresSlowLogPrerequisites()
	case dbfamily.FamilyOracle:
		return oracleSlowLogPrerequisites()
	default:
		return nil
	}
}

func normalizeFailureCode(raw string) string {
	out := make([]rune, 0, len(raw))
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else if r >= 'A' && r <= 'Z' {
			out = append(out, r+32)
		}
	}
	return string(out)
}

// ---------- MySQL ----------

func mysqlSlowLogPrerequisites() []PrerequisiteItem {
	return []PrerequisiteItem{
		{
			Key:         "mysql.runtime.version",
			Title:       "数据库版本",
			Kind:        prereqKindVersion,
			Requirement: "MySQL 5.6 及以上，推荐 8.0（8.0 支持毫秒级 long_query_time 与更完整的日志时间格式）",
			HowTo:       "查看实例配置中的版本信息，或在目标库执行 SELECT VERSION();",
			Consequence: "低于 5.6 时慢日志字段不完整，平台无法保证解析出的耗时口径",
			Verify:      "SELECT VERSION();",
			Codes:       []string{failMySQLVariableMissing},
		},
		{
			Key:         "mysql.runtime.account",
			Title:       "实例账号与密码",
			Kind:        prereqKindPermission,
			Requirement: "需要配置一个能登录目标实例、且能执行 SHOW GLOBAL VARIABLES 的账号",
			HowTo:       "进入「实例配置」，填写数据库账号，并在密码框中填入密码（系统加密存储）",
			Consequence: "未配置账号或密码时，平台连不上实例，无法判断慢日志是否已开启",
			Verify:      "在实例配置页点击「测试连接」",
			Codes:       []string{failMySQLNoCredential, failMySQLConnectFailed, failEngineNoCredential},
		},
		{
			Key:         "mysql.param.slow_query_log",
			Title:       "参数 slow_query_log",
			Kind:        prereqKindParameter,
			Requirement: "必须为 ON",
			HowTo:       "SET GLOBAL slow_query_log = ON; 并写入 my.cnf 的 [mysqld] 段（slow_query_log = 1）以免重启失效",
			Consequence: "为 OFF 时服务端完全不记录慢SQL，平台采不到任何数据",
			Verify:      "SHOW GLOBAL VARIABLES LIKE 'slow_query_log';",
			Codes:       []string{failMySQLSlowLogDisabled},
		},
		{
			Key:         "mysql.param.log_output",
			Title:       "参数 log_output",
			Kind:        prereqKindParameter,
			Requirement: "必须包含 FILE（FILE 或 FILE,TABLE）",
			HowTo:       "SET GLOBAL log_output = 'FILE'; 同时建议确认 log_output 不要设为 NONE",
			Consequence: "只写 TABLE 时慢SQL 进 mysql.slow_log 表，平台当前只支持文件方式采集",
			Verify:      "SHOW GLOBAL VARIABLES LIKE 'log_output';",
			Codes:       []string{failMySQLLogOutputNoFile},
		},
		{
			Key:         "mysql.param.long_query_time",
			Title:       "参数 long_query_time",
			Kind:        prereqKindParameter,
			Requirement: "大于 0，建议 1（秒）",
			HowTo:       "SET GLOBAL long_query_time = 1;",
			Consequence: "设为 0 会记录全部语句，日志量暴涨且没有分析价值",
			Verify:      "SHOW GLOBAL VARIABLES LIKE 'long_query_time';",
			Codes:       []string{failMySQLLongQueryTimeBad},
		},
		{
			Key:         "mysql.feature.performance_schema",
			Title:       "performance_schema（可选增强）",
			Kind:        prereqKindFeature,
			Requirement: "开启后可补充执行计划与更细的等待事件统计，非必需",
			HowTo:       "确认 my.cnf 中 performance_schema = ON（默认开启，重启生效）",
			Consequence: "不开启也能采集慢日志；仅缺失增强指标，不影响基础能力",
			Verify:      "SHOW GLOBAL VARIABLES LIKE 'performance_schema';",
			Codes:       []string{},
		},
		{
			Key:         "mysql.channel.ssh",
			Title:       "SSH 通道与文件权限",
			Kind:        prereqKindChannel,
			Requirement: "平台需能通过 SSH 登录数据库主机，且账号有权读取慢日志文件",
			HowTo:       "在实例配置的 SSH 区块填写主机账号与认证方式；确认慢日志文件对 SSH 账号可读",
			Consequence: "SSH 不通或文件不可读时，即使服务端已开启慢日志也无法采集",
			Verify:      "SSH 登录后执行 head -1 <慢日志路径>",
			Codes: []string{failMySQLSSHUnreachable, failMySQLFileMissing, failMySQLFileNoRead,
				failEngineSSHFailed, failEngineFileMissing, failEngineParseFailed, failEnginePersistFailed},
		},
		{
			Key:         "mysql.channel.logformat",
			Title:       "日志格式与时间字段",
			Kind:        prereqKindFile,
			Requirement: "慢日志需为 MySQL 原生文本格式（以 `# Time:` / `# Query_time:` 开头的段落结构）",
			HowTo:       "确认未开启 log_slow_extra 之外的第三方改写；日志中必须能解析出 Query_time 与 Time 字段",
			Consequence: "格式被改写时平台无法切分事件，表现为「有日志文件但慢SQL数量始终为 0」",
			Verify:      "SSH 登录后执行 grep -m1 -A3 'Query_time' <慢日志路径>",
			Codes:       []string{failMySQLParseFailed},
		},
	}
}

// ---------- PostgreSQL ----------

func postgresSlowLogPrerequisites() []PrerequisiteItem {
	return []PrerequisiteItem{
		{
			Key:         "pg.caliber.execution_only",
			Title:       "统计口径（为什么页面条数少于日志行数）",
			Kind:        prereqKindCaliber,
			Requirement: "只统计执行完成记录：`duration: X ms  statement:` 与 `duration: X ms  execute ...:`",
			HowTo:       "无需处理。扩展协议下同一条 SQL 会先输出 parse（含计划生成）和 bind，再输出 execute；前两者是协议阶段，不是一次执行",
			Consequence: "若把 parse/bind 计入，同一条 SQL 会被重复计 2~3 次，且混入纯计划开销；页面数量应少于日志里 grep duration 的行数",
			Verify:      "grep 'duration' <日志文件> | grep -c 'ms  statement:\\|ms  execute'，结果应与页面数量同量级",
			Codes:       nil,
		},
		{
			Key:         "pg.runtime.version",
			Title:       "数据库版本",
			Kind:        prereqKindVersion,
			Requirement: "PostgreSQL 10 及以上（低于 10 无 pg_current_logfile()，平台无法定位当前日志文件）",
			HowTo:       "查看实例配置中的版本信息，或在目标库执行 SHOW server_version;",
			Consequence: "低于 10 时无法自动定位日志文件，需要人工指定路径且轮转后失效",
			Verify:      "SHOW server_version;",
			Codes:       []string{failPGVersionUnsupported},
		},
		{
			Key:         "pg.runtime.account",
			Title:       "实例账号与密码",
			Kind:        prereqKindPermission,
			Requirement: "需要配置能登录目标库、且能执行 SHOW 参数与 pg_current_logfile() 的账号",
			HowTo:       "进入「实例配置」，填写数据库账号与密码",
			Consequence: "未配置时无法探测参数，也无法定位日志文件",
			Verify:      "在实例配置页点击「测试连接」",
			Codes:       []string{failPGNoCredential, failPGConnectFailed, failEngineNoCredential},
		},
		{
			Key:         "pg.param.logging_collector",
			Title:       "参数 logging_collector",
			Kind:        prereqKindParameter,
			Requirement: "必须为 on",
			HowTo:       "修改 postgresql.conf：logging_collector = on，然后重启或执行 pg_ctl reload",
			Consequence: "为 off 时日志只进 stderr，被 systemd/journald 接管，平台拿不到可读文件",
			Verify:      "SHOW logging_collector;",
			Codes:       []string{failPGCollectorOff},
		},
		{
			Key:         "pg.param.log_min_duration_statement",
			Title:       "参数 log_min_duration_statement",
			Kind:        prereqKindParameter,
			Requirement: "大于 0（毫秒），建议 1000",
			HowTo:       "ALTER SYSTEM SET log_min_duration_statement = 1000; 然后 SELECT pg_reload_conf();",
			Consequence: "为 -1 时完全不记录慢SQL；为 0 时记录全部语句，日志量暴涨",
			Verify:      "SHOW log_min_duration_statement;",
			Codes:       []string{failPGMinDurationDisabled, failPGMinDurationZero},
		},
		{
			Key:         "pg.param.log_line_prefix",
			Title:       "参数 log_line_prefix",
			Kind:        prereqKindParameter,
			Requirement: "必须包含 %m（时间）、%p（进程号）、%u（用户）、%d（库名），建议再加 %h（客户端 IP）",
			HowTo:       "ALTER SYSTEM SET log_line_prefix = '%m [%p] %u@%d %h '; 然后 SELECT pg_reload_conf();",
			Consequence: "缺少 %u %d 时，采上来的记录只有耗时、没有执行用户和库名，页面上看不出是谁拖慢了库",
			Verify:      "SHOW log_line_prefix;",
			Codes:       []string{failPGPrefixMissingUserDB},
		},
		{
			Key:         "pg.permission.pg_monitor",
			Title:       "账号需有 pg_monitor 角色",
			Kind:        prereqKindPermission,
			Requirement: "pg_current_logfile() 属于监控函数，普通账号默认无权限",
			HowTo:       "以超级用户执行：GRANT pg_monitor TO <平台账号>;",
			Consequence: "无该角色时探测会报权限错误，实例被判定为「无法探测」",
			Verify:      "SELECT pg_has_role(current_user, 'pg_monitor', 'member');",
			Codes:       []string{failPGNoMonitorRole, failPGLogPathUnresolved},
		},
		{
			Key:         "pg.channel.ssh",
			Title:       "SSH 通道与日志可读性",
			Kind:        prereqKindFile,
			Requirement: "平台需能通过 SSH 登录数据库主机，且能读取 PostgreSQL 日志目录",
			HowTo:       "在实例配置的 SSH 区块填写主机账号与认证方式；确认日志目录对 SSH 账号可读可执行",
			Consequence: "SSH 不通或文件不可读时，即使参数都对，也采不到任何数据",
			Verify:      "SSH 登录后执行 ls -l $(psql -tAc 'SHOW log_directory')",
			Codes: []string{failPGLogFileUnreadable, failPGParseFailed,
				failEngineSSHFailed, failEngineFileMissing, failEngineParseFailed, failEnginePersistFailed},
		},
	}
}

// ---------- Oracle ----------

func oracleSlowLogPrerequisites() []PrerequisiteItem {
	return []PrerequisiteItem{
		{
			Key:         "oracle.runtime.version",
			Title:       "数据库版本",
			Kind:        prereqKindVersion,
			Requirement: "Oracle 10g 及以上（V$SQL 各版本均可用；V$SQLSTATS 需 11g 及以上）",
			HowTo:       "查看实例配置中的版本信息，或在目标库执行 SELECT * FROM v$version;",
			Consequence: "版本过低时会回退到 V$SQL 采集，指标口径与新版略有差异",
			Verify:      "SELECT version_full FROM v$instance;",
			Codes:       []string{failOracleVersionTooOld},
		},
		{
			Key:         "oracle.runtime.account",
			Title:       "实例账号与密码",
			Kind:        prereqKindPermission,
			Requirement: "需要配置能登录目标实例的账号",
			HowTo:       "进入「实例配置」，填写数据库账号与密码",
			Consequence: "未配置时无法建立连接，采集直接失败",
			Verify:      "在实例配置页点击「测试连接」",
			Codes:       []string{failOracleNoCredential, failOracleConnectFailed, failEngineNoCredential},
		},
		{
			Key:         "oracle.permission.v_sql",
			Title:       "V$SQL 视图访问权限",
			Kind:        prereqKindPermission,
			Requirement: "账号需有 SELECT_CATALOG_ROLE，或被显式授予 V_$SQL / V_$SESSION 的 SELECT 权限",
			HowTo:       "以 DBA 执行：GRANT SELECT_CATALOG_ROLE TO <平台账号>;  或 GRANT SELECT ON v_$sql TO <平台账号>;",
			Consequence: "无权限时报 ORA-00942 或 ORA-01031，平台采集不到任何 SQL",
			Verify:      "SELECT COUNT(*) FROM v$sql WHERE ROWNUM <= 1;",
			Codes:       []string{failOracleNoVSQLAccess},
		},
		{
			Key:         "oracle.param.statistics_level",
			Title:       "参数 statistics_level",
			Kind:        prereqKindParameter,
			Requirement: "不能为 BASIC，建议 TYPICAL 或 ALL",
			HowTo:       "ALTER SYSTEM SET statistics_level = TYPICAL;",
			Consequence: "为 BASIC 时 V$SQL 的耗时与等待类指标不更新，采上来的数据全是 0",
			Verify:      "SHOW PARAMETER statistics_level;",
			Codes:       []string{failOracleStatsBasic},
		},
		{
			Key:         "oracle.runtime.container",
			Title:       "多租户架构（12c 及以上）",
			Kind:        prereqKindFeature,
			Requirement: "需明确按 PDB 还是按 CDB 采集，两者不可混用",
			HowTo:       "若按 PDB 采集，实例连接串请直接指向 PDB 的 service_name；若按 CDB，需在账号上授予 CDB 视图权限",
			Consequence: "混用时同一条 SQL 会被重复统计，或不同 PDB 的同指纹 SQL 被错误合并",
			Verify:      "SELECT name, open_mode FROM v$pdbs;",
			Codes:       []string{},
		},
		{
			Key:         "oracle.channel.sample",
			Title:       "采样周期与连接可达性",
			Kind:        prereqKindChannel,
			Requirement: "平台默认每 60 秒对 V$SQL 做一次增量采样，要求实例在采样时刻可连通",
			HowTo:       "确认实例的主机、端口与 service_name 配置正确；确认平台到实例的网络与防火墙放行",
			Consequence: "采样时连不通会丢失该轮数据，表现为慢SQL曲线出现缺口",
			Verify:      "SELECT 1 FROM dual;",
			Codes:       []string{failOracleSampleFailed},
		},
		{
			Key:         "oracle.feature.awr",
			Title:       "AWR（可选，需授权）",
			Kind:        prereqKindFeature,
			Requirement: "DBA_HIST_SQLSTAT 依赖 Diagnostics Pack 授权；未授权时平台自动走 V$SQL 采样",
			HowTo:       "SELECT * FROM v$option WHERE parameter = 'Diagnostic Pack'; 为 FALSE 表示未授权",
			Consequence: "仅影响历史回溯能力；实时慢SQL采集不受影响",
			Verify:      "SELECT * FROM v$option WHERE parameter = 'Diagnostic Pack';",
			Codes:       []string{failOracleAWRNoLicense},
		},
		{
			Key:         "oracle.channel.persist",
			Title:       "采样数据落库",
			Kind:        prereqKindChannel,
			Requirement: "平台元数据库可写，且采样基线表有足够空间",
			HowTo:       "确认平台元数据库状态正常；必要时清理历史采样基线",
			Consequence: "写入失败时本轮采样作废，下一轮会重新差分，可能出现重复统计",
			Verify:      "在平台上查看该实例的「最近采样时间」是否持续更新",
			Codes:       []string{failOraclePersistFailed},
		},
	}
}

// ---------- 失败码 → 前提项 ----------

// MatchPrerequisiteKeys 把采集失败码翻译成前提项 Key 列表（可能命中多项）。
// 命中不了返回空，前端直接展示原始错误信息即可。
func MatchPrerequisiteKeys(dbType string, extraConfig []byte, code string) []string {
	target := normalizeFailureCode(code)
	if target == "" {
		return nil
	}
	keys := make([]string, 0, 2)
	for _, item := range SlowLogPrerequisites(dbType, extraConfig) {
		for _, candidate := range item.Codes {
			if normalizeFailureCode(candidate) == target {
				keys = append(keys, item.Key)
				break
			}
		}
	}
	return keys
}
