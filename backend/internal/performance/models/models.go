package models

import commonModels "github.com/simajiong/jaysql/internal/common/models"

type InsightPerformanceSnapshotParams struct {
	*commonModels.Model
	Name                  string                `gorm:"type:varchar(128);not null;default:'';comment:参数名称;index:idx_perf_param_name" json:"name"`
	DBType                commonModels.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';comment:数据库类型;index:idx_perf_param_db_type" json:"db_type"`
	ScopeType             string                `gorm:"type:varchar(16);not null;default:'global';comment:作用域类型;index:idx_perf_param_scope" json:"scope_type"`
	ScopeValue            string                `gorm:"type:char(15);not null;default:'';comment:作用域值，实例级时为instance_id;index:idx_perf_param_scope_value" json:"scope_value"`
	SlowSQLThresholdMS    int                   `gorm:"type:int;not null;default:1000;comment:慢SQL阈值毫秒" json:"slow_sql_threshold_ms"`
	SnapshotWindowMinutes int                   `gorm:"type:int;not null;default:60;comment:快照窗口分钟数" json:"snapshot_window_minutes"`
	TopSQLLimit           int                   `gorm:"type:int;not null;default:20;comment:Top SQL返回条数" json:"top_sql_limit"`
	RetentionDays         int                   `gorm:"type:int;not null;default:7;comment:报告保留天数" json:"retention_days"`
	Enabled               bool                  `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"enabled"`
	Remark                string                `gorm:"type:varchar(255);not null;default:'';comment:备注" json:"remark"`
}

func (InsightPerformanceSnapshotParams) TableName() string {
	return "insight_performance_snapshot_params"
}

// InsightPerformanceParamChanges 参数修改历史（只增不改，回滚也生成新记录）。
// 参数当前值不落平台库：列表实时查目标实例（SHOW VARIABLES / pg_settings 等），
// 这里只留存"谁在什么时候把什么参数从什么值改成了什么值"的审计链。
type InsightPerformanceParamChanges struct {
	*commonModels.Model
	InstanceID  string                `gorm:"type:char(15);not null;default:'';index:idx_perf_paramchg_instance,priority:1;comment:关联实例ID" json:"instance_id"`
	DBType      commonModels.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';comment:数据库类型" json:"db_type"`
	ParamName   string                `gorm:"type:varchar(128);not null;default:'';index:idx_perf_paramchg_instance,priority:2;comment:参数名" json:"param_name"`
	OldValue    string                `gorm:"type:varchar(512);not null;default:'';comment:修改前值" json:"old_value"`
	NewValue    string                `gorm:"type:varchar(512);not null;default:'';comment:修改后值" json:"new_value"`
	Scope       string                `gorm:"type:varchar(32);not null;default:'';comment:作用域 GLOBAL/SESSION/SPFILE 等" json:"scope"`
	Effective   string                `gorm:"type:varchar(32);not null;default:'';comment:生效方式 immediate/new_conn/restart/pending_restart" json:"effective"`
	Operator    string                `gorm:"type:varchar(64);not null;default:'';index:idx_perf_paramchg_operator;comment:操作人" json:"operator"`
	Reason      string                `gorm:"type:varchar(1024);not null;default:'';comment:变更原因（必填，审计要求）" json:"reason"`
	ExecResult  string                `gorm:"type:varchar(32);not null;default:'';comment:执行结果 success/failed/unknown" json:"exec_result"`
	ExecMessage string                `gorm:"type:text;comment:执行详情（含复核偏差提示）" json:"exec_message"`
	RollbackOf  uint64                `gorm:"type:bigint;not null;default:0;comment:回滚来源记录ID，0为普通修改" json:"rollback_of"`
}

func (InsightPerformanceParamChanges) TableName() string {
	return "insight_performance_param_changes"
}

type InsightPerformanceSlowLogCollectors struct {
	*commonModels.Model
	InstanceID         string                 `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_perf_slowlog_instance;comment:关联实例ID" json:"instance_id"`
	DBType             commonModels.EnumType  `gorm:"type:varchar(32);not null;default:'MySQL';comment:数据库类型" json:"db_type"`
	LogPath            string                 `gorm:"type:varchar(512);not null;default:'/var/log/mysql/slow.log';comment:慢日志路径" json:"log_path"`
	FileInode          string                 `gorm:"type:varchar(64);not null;default:'';comment:远端文件inode" json:"file_inode"`
	FileSize           int64                  `gorm:"type:bigint;not null;default:0;comment:最近观测文件大小" json:"file_size"`
	ReadOffset         int64                  `gorm:"type:bigint;not null;default:0;comment:已读取偏移量" json:"read_offset"`
	PendingBuffer      string                 `gorm:"type:longtext;comment:待拼接的尾部缓冲" json:"pending_buffer"`
	CollectStatus      string                 `gorm:"type:varchar(32);not null;default:'IDLE';comment:采集状态" json:"collect_status"`
	LastCollectMessage string                 `gorm:"type:text;comment:最近采集消息" json:"last_collect_message"`
	LastError          string                 `gorm:"type:text;comment:最近错误" json:"last_error"`
	LastCollectAt      commonModels.LocalTime `gorm:"comment:最近采集时间" json:"last_collect_at"`
	LastSuccessAt      commonModels.LocalTime `gorm:"comment:最近成功采集时间" json:"last_success_at"`
	LastEventAt        commonModels.LocalTime `gorm:"comment:最近采集到的事件时间" json:"last_event_at"`
	Enabled            bool                   `gorm:"type:boolean;not null;default:true;comment:是否启用" json:"enabled"`

	// —— 慢日志能力探测（来源：SHOW GLOBAL VARIABLES，非猜测）——
	CapabilityState     string `gorm:"type:varchar(32);not null;default:'UNKNOWN';comment:采集能力五态 ENABLED_FILE/ENABLED_TABLE/DISABLED/PROBE_FAILED/FILE_MISSING;index:idx_perf_slowlog_capability" json:"capability_state"`
	SlowQueryLogEnabled bool   `gorm:"type:boolean;not null;default:false;comment:服务端 slow_query_log 开关" json:"slow_query_log_enabled"`
	LogOutputMode       string `gorm:"type:varchar(32);not null;default:'';comment:log_output 模式 FILE/TABLE/FILE,TABLE/NONE" json:"log_output_mode"`
	FileCapable         bool   `gorm:"type:boolean;not null;default:false;comment:是否文件方式（采集门控依据）" json:"file_capable"`
	ServerLogFile       string `gorm:"type:varchar(512);not null;default:'';comment:slow_query_log_file 变量原值" json:"server_log_file"`
	ResolvedLogPath     string `gorm:"type:varchar(512);not null;default:'';comment:解析后的慢日志绝对路径" json:"resolved_log_path"`
	LongQueryTimeMS     int    `gorm:"type:int;not null;default:0;comment:服务端 long_query_time 毫秒" json:"long_query_time_ms"`
	ServerVersion       string `gorm:"type:varchar(64);not null;default:'';comment:数据库版本" json:"server_version"`
	// FailureCode 对应 slowlog_prerequisites.go 里的前提项 Key 所绑定的失败码，
	// 前端据此高亮「采集前提条件」面板中那一项并展示修复步骤。为空=无失败。
	FailureCode string `gorm:"type:varchar(64);not null;default:'';comment:采集失败原因码;index:idx_perf_slowlog_failure" json:"failure_code"`
	// Source 标注该实例实际使用的慢 SQL 数据源：FILE（MySQL/PG 日志）或 V$SQL（Oracle）
	Source            string                 `gorm:"type:varchar(32);not null;default:'';comment:数据源 FILE/V$SQL" json:"source"`
	ProbeStatus       string                 `gorm:"type:varchar(32);not null;default:'';comment:探测结果 OK/NO_CREDENTIAL/CONNECT_FAILED/VARIABLE_MISSING/UNSUPPORTED" json:"probe_status"`
	ProbeError        string                 `gorm:"type:text;comment:探测失败原因" json:"probe_error"`
	LastProbeAt       commonModels.LocalTime `gorm:"comment:最近探测时间" json:"last_probe_at"`
	LastNotifiedError string                 `gorm:"type:varchar(255);not null;default:'';comment:最近已告警的错误签名（用于去重）" json:"last_notified_error"`
	LastNotifiedAt    commonModels.LocalTime `gorm:"comment:最近告警时间" json:"last_notified_at"`
}

func (InsightPerformanceSlowLogCollectors) TableName() string {
	return "insight_performance_slowlog_collectors"
}

type InsightPerformanceSlowLogEntries struct {
	*commonModels.Model
	InstanceID     string                 `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_perf_slowlog_entry,priority:1;index:idx_perf_slowlog_instance_time,priority:1;comment:关联实例ID" json:"instance_id"`
	DBType         commonModels.EnumType  `gorm:"type:varchar(32);not null;default:'MySQL';index:idx_perf_slowlog_db_type;comment:数据库类型" json:"db_type"`
	LogPath        string                 `gorm:"type:varchar(512);not null;default:'';comment:来源日志路径" json:"log_path"`
	FileInode      string                 `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_perf_slowlog_entry,priority:2;comment:来源文件inode" json:"file_inode"`
	OffsetStart    int64                  `gorm:"type:bigint;not null;default:0;uniqueIndex:uniq_perf_slowlog_entry,priority:3;comment:事件起始偏移量" json:"offset_start"`
	OffsetEnd      int64                  `gorm:"type:bigint;not null;default:0;comment:事件结束偏移量" json:"offset_end"`
	EventTime      commonModels.LocalTime `gorm:"index:idx_perf_slowlog_instance_time,priority:2;comment:慢日志事件时间" json:"event_time"`
	CollectTime    commonModels.LocalTime `gorm:"comment:采集入库时间" json:"collect_time"`
	Username       string                 `gorm:"type:varchar(128);not null;default:'';index:idx_perf_slowlog_username;comment:执行用户" json:"username"`
	ClientHost     string                 `gorm:"type:varchar(255);not null;default:'';comment:客户端地址" json:"client_host"`
	SchemaName     string                 `gorm:"column:schema_name;type:varchar(255);not null;default:'';index:idx_perf_slowlog_schema;comment:数据库名" json:"schema"`
	QueryTimeMS    int                    `gorm:"type:int;not null;default:0;index:idx_perf_slowlog_duration;comment:查询耗时毫秒" json:"query_time_ms"`
	LockTimeMS     int                    `gorm:"type:int;not null;default:0;comment:锁等待耗时毫秒" json:"lock_time_ms"`
	RowsSent       int                    `gorm:"type:int;not null;default:0;comment:返回行数" json:"rows_sent"`
	RowsExamined   int                    `gorm:"type:int;not null;default:0;comment:扫描行数" json:"rows_examined"`
	ThreadID       string                 `gorm:"type:varchar(64);not null;default:'';comment:线程ID" json:"thread_id"`
	SQLText        string                 `gorm:"type:longtext;comment:原始SQL文本" json:"sql_text"`
	SQLFingerprint string                 `gorm:"type:varchar(255);not null;default:'';index:idx_perf_slowlog_fingerprint;comment:归一化SQL" json:"sql_fingerprint"`
	SQLDigest      string                 `gorm:"type:char(64);not null;default:'';index:idx_perf_slowlog_digest;comment:SQL摘要" json:"sql_digest"`
	ParseStatus    string                 `gorm:"type:varchar(32);not null;default:'PARSED';comment:解析状态" json:"parse_status"`
	ParseError     string                 `gorm:"type:text;comment:解析错误信息" json:"parse_error"`
	RawText        string                 `gorm:"type:longtext;comment:原始慢日志片段" json:"raw_text"`
	// EventKey 是跨引擎统一的「事件身份」，用来做幂等入库。
	// 之所以不能只用 (file_inode, offset_start)：Oracle 走 V$SQL 采样，
	// 没有文件、没有偏移量，所有行都会是空值 → 第二条就撞唯一键。
	//
	// 这里刻意**不写 uniqueIndex 标签**：AutoMigrate 是「先加列、再建索引」，
	// 存量行加完列全是 ''，唯一索引当场 Duplicate entry '' 失败，
	// 而 initializeTables 对 AutoMigrate 的 error 是 Fatal——服务直接起不来。
	// 唯一索引改由 EnsureSlowLogSchema 在回填之后再建（见 slowlog_schema.go）。
	EventKey   string                 `gorm:"type:varchar(160);not null;default:'';comment:事件身份(跨引擎)" json:"event_key"`
	SampleTime commonModels.LocalTime `gorm:"comment:采样时刻(Oracle)" json:"sample_time"`
	// ExecCount 仅采样类引擎有意义：本轮采样窗口内的新增执行次数。
	// 文件类引擎每条日志就是一次执行，固定为 1（见入库处赋值）。
	ExecCount int64 `gorm:"type:bigint;not null;default:0;comment:本轮新增执行次数(采样类引擎)" json:"exec_count"`
}

func (InsightPerformanceSlowLogEntries) TableName() string {
	return "insight_performance_slowlog_entries"
}

// InsightPerformanceSlowLogSamples Oracle V$SQL 采样基线。
//
// V$SQL 里的 EXECUTIONS / ELAPSED_TIME 是**累计值**，单次查询只能拿到
// "自实例启动以来的总和"。要算出"这一轮采样窗口内实际发生了什么"，
// 必须记住上一轮的累计值并做差——这张表就是那个"上一轮"。
//
// 唯一键 (instance_id, sql_id, plan_hash_value)：同一条 SQL 若换了执行计划，
// 应作为新的一行重新建立基线，否则差分会出现负数（旧计划被老化出共享池时）。
type InsightPerformanceSlowLogSamples struct {
	*commonModels.Model
	InstanceID         string                 `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_slowlog_sample,priority:1;comment:关联实例ID" json:"instance_id"`
	SQLID              string                 `gorm:"type:varchar(32);not null;default:'';uniqueIndex:uniq_slowlog_sample,priority:2;comment:Oracle SQL_ID" json:"sql_id"`
	PlanHashValue      string                 `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_slowlog_sample,priority:3;comment:执行计划指纹" json:"plan_hash_value"`
	ParsingSchemaName  string                 `gorm:"type:varchar(128);not null;default:'';comment:解析用户" json:"parsing_schema_name"`
	ExecutionsTotal    int64                  `gorm:"type:bigint;not null;default:0;comment:累计执行次数" json:"executions_total"`
	ElapsedUSTotal     int64                  `gorm:"type:bigint;not null;default:0;comment:累计耗时(微秒)" json:"elapsed_us_total"`
	CPUUSTotal         int64                  `gorm:"type:bigint;not null;default:0;comment:累计CPU时间(微秒)" json:"cpu_us_total"`
	BufferGetsTotal    int64                  `gorm:"type:bigint;not null;default:0;comment:累计逻辑读" json:"buffer_gets_total"`
	DiskReadsTotal     int64                  `gorm:"type:bigint;not null;default:0;comment:累计物理读" json:"disk_reads_total"`
	RowsProcessedTotal int64                  `gorm:"type:bigint;not null;default:0;comment:累计处理行数" json:"rows_processed_total"`
	LastActiveTime     commonModels.LocalTime `gorm:"comment:服务端最后活跃时间" json:"last_active_time"`
	SampleTime         commonModels.LocalTime `gorm:"comment:本次采样时刻" json:"sample_time"`
}

func (InsightPerformanceSlowLogSamples) TableName() string {
	return "insight_performance_slowlog_samples"
}
