package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	dbfamily "github.com/simajiong/jaysql/internal/common/dbfamily"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"github.com/simajiong/jaysql/pkg/secret"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

const (
	// defaultMySQLSlowLogPath 仅作为「探测拿不到任何路径」时的最后兜底，
	// 正常情况下应由 SHOW GLOBAL VARIABLES 的 slow_query_log_file 决定。
	defaultMySQLSlowLogPath       = "/var/log/mysql/slow.log"
	defaultMySQLSlowLogMaxRead    = 512 * 1024
	defaultSlowLogCollectSpec     = "@every 20s"
	slowLogCollectStatusIdle      = "IDLE"
	slowLogCollectStatusRunning   = "RUNNING"
	slowLogCollectStatusError     = "ERROR"
	slowLogCollectStatusDisabled  = "DISABLED"
	slowLogCollectStatusSkipped   = "SKIPPED"
	slowLogCollectStatusProbeFail = "PROBE_FAILED"
	slowLogParseStatusParsed      = "PARSED"
	slowLogParseStatusParseFailed = "PARSE_FAILED"

	// sourceFile 数据来源为「服务端日志文件」，与 Oracle 的 V$SQL 采样相对。
	sourceFile = "FILE"

	// 同一错误签名的告警抑制窗口：窗口内重复出现只记 Debug，避免刷屏
	slowLogErrorNotifyWindow  = 30 * time.Minute
	slowLogNotifySignatureMax = 255
)

var (
	mysqlSlowLogTimeRegexp   = regexp.MustCompile(`^# Time:\s+(.+?)\s*$`)
	mysqlSlowLogUserRegexp   = regexp.MustCompile(`^# User@Host:\s+([^\[]*)\[[^\]]*\]\s+@\s+([^\[]*)\s+\[([^\]]*)\]`)
	mysqlSlowLogStatsRegexp  = regexp.MustCompile(`^# Query_time:\s*([0-9.]+)\s+Lock_time:\s*([0-9.]+)\s+Rows_sent:\s*([0-9]+)\s+Rows_examined:\s*([0-9]+)`)
	mysqlSlowLogThreadRegexp = regexp.MustCompile(`^# Thread_id:\s*([0-9]+)\s+Schema:\s*([^\s]*)`)
	mysqlSlowLogUseRegexp    = regexp.MustCompile("(?i)^use\\s+`?([^`;\\s]+)`?;?\\s*$")
	sqlLiteralNumberRegexp   = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
	sqlLiteralStringRegexp   = regexp.MustCompile(`'([^'\\]|\\.)*'|"([^"\\]|\\.)*"`)
	mysqlSlowLogJobRunning   bool
)

type slowLogRemoteState struct {
	Inode        string
	Size         int64
	Offset       int64
	Tail         string
	ResolvedPath string
}

type parsedSlowLogEvent struct {
	OffsetStart    int64
	OffsetEnd      int64
	EventTime      time.Time
	Username       string
	ClientHost     string
	SchemaName     string
	QueryTimeMS    int
	LockTimeMS     int
	RowsSent       int
	RowsExamined   int
	ThreadID       string
	SQLText        string
	SQLFingerprint string
	SQLDigest      string
	ParseStatus    string
	ParseError     string
	RawText        string
}

func RegisterSlowLogCollectCronJobs() {
	if global.App.Cron == nil {
		return
	}
	if _, err := global.App.Cron.AddFunc(defaultSlowLogCollectSpec, func() {
		CollectMySQLSlowLogs()
	}); err != nil {
		global.App.Log.Errorf("register slowlog collect cron failed: %v", err)
	}
}

func CollectMySQLSlowLogs() {
	if mysqlSlowLogJobRunning {
		return
	}
	mysqlSlowLogJobRunning = true
	defer func() {
		mysqlSlowLogJobRunning = false
		if r := recover(); r != nil {
			global.App.Log.Errorf("CollectMySQLSlowLogs panic: %v", r)
		}
	}()

	if global.App.DB == nil {
		return
	}

	// 加载全部实例，由「引擎族」决定是否支持——不再写死 db_type='MySQL'。
	//
	// 之前这一行是硬过滤，PG / Oracle 实例连探测机会都没有；
	// 现在统一加载，不支持的引擎在下面用 UNSUPPORTED 明确表达，
	// 前端能给出"暂不支持"而不是让用户以为配置错了。
	var instances []commonModels.InsightInstances
	if err := global.App.DB.Find(&instances).Error; err != nil {
		global.App.Log.Errorf("load instances for slowlog failed: %v", err)
		return
	}

	for i := range instances {
		instance := instances[i]
		if !slowLogEngineSupported(instance.DbType, instance.ExtraConfig) {
			continue
		}
		collector, err := ensureSlowLogCollector(&instance)
		if err != nil {
			logSlowLogWarnOnce(&instance, fmt.Sprintf("ensure slowlog collector failed: %v", err))
			continue
		}
		if collector == nil || !collector.Enabled {
			continue
		}
		// 采集门控：只有「服务端开着慢日志」且「以文件方式记录」才进入采集链路。
		// 未开启 / 仅表格 / 探测失败属于预期内状态，直接跳过——不建 SSH、不读文件、不打 WARN。
		if !slowLogCollectable(collector) {
			noteSlowLogSkipOnce(collector)
			continue
		}
		collectSlowLogForInstance(&instance, collector)
	}
}

// slowLogEngineSupported 判断某数据库类型是否具备慢 SQL 采集实现。
//
// 与 db_type 白名单的区别：这里按**引擎族**判定，因此
// MariaDB / TiDB / Doris / PolarDB 会自动归入 MySQL 族，
// OceanBase 按租户模式归入 MySQL 或 Oracle 族，无需逐个列举。
func slowLogEngineSupported(dbType commonModels.EnumType, extraConfig []byte) bool {
	switch dbfamily.EffectiveFamily(string(dbType), extraConfig) {
	case dbfamily.FamilyMySQL, dbfamily.FamilyPostgres, dbfamily.FamilyOracle:
		return true
	default:
		return false
	}
}

// collectSlowLogForInstance 按引擎族分派到具体的采集实现。
//
// 为什么要分派：MySQL 与 PG 虽然都走「SSH 读文件 + 增量 offset」，
// 但日志格式完全不同（`# Query_time:` vs `duration: ... ms`），
// 解析器不可共用；Oracle 更彻底——它根本没有日志文件，只能采样。
func collectSlowLogForInstance(instance *commonModels.InsightInstances,
	collector *performanceModels.InsightPerformanceSlowLogCollectors) {
	if instance == nil || collector == nil {
		return
	}
	dbType := strings.TrimSpace(string(instance.DbType))
	switch dbfamily.EffectiveFamily(dbType, instance.ExtraConfig) {
	case dbfamily.FamilyMySQL:
		// 传实际 db_type 而不是硬编码 "MySQL"：
		// 兼容引擎（TiDB / MariaDB / PolarDB…）的明细行要带上自己的类型，
		// 否则汇总页按 db_type 关联明细时会匹配不上。
		collectFileSlowLogForInstance(instance, collector, dbType, parseMySQLSlowLogChunk)
	case dbfamily.FamilyPostgres:
		collectPostgresSlowLogForInstance(instance, collector)
	case dbfamily.FamilyOracle:
		collectOracleSlowLogForInstance(instance, collector)
	}
}

func ensureSlowLogCollector(instance *commonModels.InsightInstances) (*performanceModels.InsightPerformanceSlowLogCollectors, error) {
	if instance == nil || strings.TrimSpace(instance.InstanceID) == "" {
		return nil, fmt.Errorf("instance empty")
	}
	instanceID := strings.TrimSpace(instance.InstanceID)
	manualEnabled := readSlowLogEnabledFromExtra(instance)

	var collector performanceModels.InsightPerformanceSlowLogCollectors
	loadErr := global.App.DB.Where("instance_id = ?", instanceID).Take(&collector).Error
	isNew := errors.Is(loadErr, gorm.ErrRecordNotFound)
	if loadErr != nil && !isNew {
		return nil, loadErr
	}

	// 能力探测：按 TTL 触发，避免每 20 秒都去连一次实例库
	if isNew || slowLogProbeDue(collector.CapabilityState, collector.LastProbeAt) {
		applySlowLogCapability(&collector, probeSlowLogCapability(instance))
	}

	// 路径优先级：人工覆盖(extra_config.slow_log_path) > 探测结果 > 最后兜底
	logPath := resolveCollectorLogPath(instance, &collector)

	collector.InstanceID = instanceID
	collector.DBType = commonModels.EnumType(strings.TrimSpace(string(instance.DbType)))
	collector.LogPath = logPath
	collector.Enabled = manualEnabled

	if isNew {
		collector.CollectStatus = decideInitialCollectStatus(manualEnabled, &collector)
		if createErr := global.App.DB.Create(&collector).Error; createErr != nil {
			return nil, createErr
		}
		return &collector, nil
	}

	updates := map[string]any{
		"db_type":                strings.TrimSpace(string(instance.DbType)),
		"log_path":               logPath,
		"enabled":                manualEnabled,
		"capability_state":       collector.CapabilityState,
		"slow_query_log_enabled": collector.SlowQueryLogEnabled,
		"log_output_mode":        collector.LogOutputMode,
		"file_capable":           collector.FileCapable,
		"server_log_file":        collector.ServerLogFile,
		"resolved_log_path":      collector.ResolvedLogPath,
		"long_query_time_ms":     collector.LongQueryTimeMS,
		"server_version":         collector.ServerVersion,
		"probe_status":           collector.ProbeStatus,
		"probe_error":            collector.ProbeError,
		"last_probe_at":          collector.LastProbeAt,
	}
	// 人工停用优先；其次落「预期内不可采集」的终态；
	// 可采集时保留现有 collect_status，交由采集流程写 RUNNING/IDLE/ERROR。
	if !manualEnabled {
		collector.CollectStatus = slowLogCollectStatusDisabled
		updates["collect_status"] = collector.CollectStatus
	} else if skipped := skippedCollectStatus(&collector); skipped != "" {
		collector.CollectStatus = skipped
		updates["collect_status"] = skipped
	}

	if updateErr := global.App.DB.Model(&collector).Updates(updates).Error; updateErr != nil {
		return nil, updateErr
	}
	return &collector, nil
}

// applySlowLogCapability 把探测结果合并进采集器。
// 探测失败时保留上一次成功结论（避免网络抖动把可采集实例误判为不可采集）。
func applySlowLogCapability(collector *performanceModels.InsightPerformanceSlowLogCollectors, capability *slowLogCapability) {
	if collector == nil || capability == nil {
		return
	}
	collector.ProbeStatus = capability.ProbeStatus
	collector.ProbeError = capability.ProbeError
	collector.LastProbeAt = commonModels.LocalTime(capability.ProbedAt)

	if capability.ProbeStatus != probeStatusOK {
		// 引擎不在支持范围内：与「探测失败」分开落库，前端据此给出
		// 「暂不支持」而不是让用户反复检查账号密码。
		if capability.ProbeStatus == probeStatusUnsupported {
			collector.CapabilityState = capabilityUnsupported
			collector.SlowQueryLogEnabled = false
			collector.FileCapable = false
			collector.LogOutputMode = ""
			collector.ResolvedLogPath = ""
			collector.ResolvedLogPath = ""
			collector.FailureCode = capability.FailureCode
			collector.Source = ""
			collector.ServerVersion = strings.TrimSpace(capability.ServerVersion)
			return
		}
		if !hasCapabilityConclusion(collector.CapabilityState) {
			collector.CapabilityState = capabilityProbeFailed
			collector.SlowQueryLogEnabled = false
			collector.FileCapable = false
			collector.LogOutputMode = ""
			collector.ResolvedLogPath = ""
		}
		collector.FailureCode = capability.FailureCode
		collector.Source = resolveSlowLogSource(capability)
		return
	}

	collector.CapabilityState = capability.State
	collector.SlowQueryLogEnabled = capability.SlowLogEnabled
	collector.LogOutputMode = capability.LogOutputMode
	collector.FileCapable = capability.FileCapable
	collector.ServerLogFile = capability.RawLogFile
	collector.ResolvedLogPath = capability.ResolvedPath
	collector.LongQueryTimeMS = capability.LongQueryTimeMS
	collector.ServerVersion = capability.ServerVersion
	collector.FailureCode = capability.FailureCode
	collector.Source = resolveSlowLogSource(capability)
}

// resolveSlowLogSource 判定该实例慢 SQL 的**数据来源形态**，写进 collector.source：
//
//	FILE  —— 服务端写日志文件，平台走 SSH 增量读（MySQL / PostgreSQL）
//	V$SQL —— 无日志文件，平台定时采样动态性能视图（Oracle）
//
// 前端据此决定展示哪些列：Oracle 不展示「日志文件路径」，
// 而展示「采样周期 / 采样时刻」，避免拿文件口径去套采样口径。
func resolveSlowLogSource(capability *slowLogCapability) string {
	if capability == nil {
		return ""
	}
	if strings.TrimSpace(capability.RawLogFile) == oracleSlowLogSource {
		return oracleSlowLogSource
	}
	if capability.ProbeStatus == probeStatusOK &&
		(capability.FileCapable || strings.TrimSpace(capability.ResolvedPath) != "") {
		return sourceFile
	}
	return ""
}

func hasCapabilityConclusion(state string) bool {
	text := strings.TrimSpace(state)
	return text != "" && text != capabilityUnknown
}

// skippedCollectStatus 返回「预期内不可采集」对应的采集器状态；可采集时返回空串表示不干预。
func skippedCollectStatus(collector *performanceModels.InsightPerformanceSlowLogCollectors) string {
	if collector == nil {
		return ""
	}
	switch strings.TrimSpace(collector.CapabilityState) {
	case capabilityEnabledTable, capabilityDisabled:
		return slowLogCollectStatusSkipped
	case capabilityProbeFailed, capabilityUnknown, "":
		return slowLogCollectStatusProbeFail
	default:
		return ""
	}
}

func decideInitialCollectStatus(manualEnabled bool, collector *performanceModels.InsightPerformanceSlowLogCollectors) string {
	if !manualEnabled {
		return slowLogCollectStatusDisabled
	}
	if skipped := skippedCollectStatus(collector); skipped != "" {
		return skipped
	}
	return slowLogCollectStatusIdle
}

// resolveCollectorLogPath 路径优先级：人工覆盖 > 探测结果 > 最后兜底。
//
// PG 不走 MySQL 的默认路径兜底：PG 没有独立的慢日志文件，日志文件名由
// log_filename（默认带日期）决定、每天轮转，只能由 pg_current_logfile() 给出。
// 强行塞 /var/log/mysql/slow.log 只会让采集去读一个跟 PG 无关的文件，
// 失败后还会被归一成「慢日志文件缺失」，把用户引向错误的排查方向。
func resolveCollectorLogPath(instance *commonModels.InsightInstances, collector *performanceModels.InsightPerformanceSlowLogCollectors) string {
	if override := readSlowLogPathOverride(instance); override != "" {
		return override
	}
	if collector != nil {
		if probed := strings.TrimSpace(collector.ResolvedLogPath); probed != "" {
			return probed
		}
	}
	if isPostgresFamilyInstance(instance) {
		return ""
	}
	return defaultMySQLSlowLogPath
}

// isPostgresFamilyInstance 判断实例是否属于 PG 引擎族（含兼容引擎）。
func isPostgresFamilyInstance(instance *commonModels.InsightInstances) bool {
	if instance == nil {
		return false
	}
	return dbfamily.EffectiveFamily(strings.TrimSpace(string(instance.DbType)), instance.ExtraConfig) == dbfamily.FamilyPostgres
}

// markCollectorError 记录真实采集异常（预期外的错误）。
// 告警去重：同一错误签名在 slowLogErrorNotifyWindow 内只 Warn 一次，其余降为 Debug，
// 避免「每 20 秒一条」把 app.log 刷爆。
func markCollectorError(collector *performanceModels.InsightPerformanceSlowLogCollectors, errMsg string) {
	markCollectorFailure(collector, errMsg, "")
}

// markCollectorFileMissing 用于「服务端声明已开启文件方式慢日志，但文件读不到」——
// 这是真异常（权限、磁盘、被清理），语义上单列为 FILE_MISSING 便于概览页凸显。
func markCollectorFileMissing(collector *performanceModels.InsightPerformanceSlowLogCollectors, errMsg string) {
	markCollectorFailure(collector, errMsg, capabilityFileMissing)
}

// markCollectorFailureWithCode 与 markCollectorFailure 的区别：多带一个 failure_code。
//
// failure_code 是给前端用的「稳定错误码」——前端拿它反查前提条件清单里的某一项，
// 直接把「为什么会失败」定位到「要改哪个配置」，用户不必再去找运维。
func markCollectorFailureWithCode(collector *performanceModels.InsightPerformanceSlowLogCollectors,
	errMsg, capabilityState, failureCode string) {
	if collector == nil {
		return
	}
	now := time.Now()
	notification := fmt.Sprintf("[slowlog_collect] instance=%s err=%s", strings.TrimSpace(collector.InstanceID), errMsg)
	signature := truncateNotifySignature(notification)

	previousSignature := strings.TrimSpace(collector.LastNotifiedError)
	previousAt := time.Time(collector.LastNotifiedAt)
	suppressed := signature == previousSignature &&
		!previousAt.IsZero() &&
		now.Sub(previousAt) < slowLogErrorNotifyWindow

	updates := map[string]any{
		"collect_status":       slowLogCollectStatusError,
		"last_error":           errMsg,
		"last_collect_message": errMsg,
		"last_collect_at":      commonModels.LocalTime(now),
		"failure_code":         strings.TrimSpace(failureCode),
	}
	collector.FailureCode = strings.TrimSpace(failureCode)
	if strings.TrimSpace(capabilityState) != "" {
		updates["capability_state"] = strings.TrimSpace(capabilityState)
		collector.CapabilityState = strings.TrimSpace(capabilityState)
	}
	if !suppressed {
		updates["last_notified_error"] = signature
		updates["last_notified_at"] = commonModels.LocalTime(now)
		collector.LastNotifiedError = signature
		collector.LastNotifiedAt = commonModels.LocalTime(now)
		global.App.Log.Warnf("%s", notification)
	} else {
		global.App.Log.Debugf("%s（同一错误已抑制，窗口 %s）", notification, slowLogErrorNotifyWindow)
	}

	_ = global.App.DB.Model(collector).Updates(updates).Error
}

func markCollectorFailure(collector *performanceModels.InsightPerformanceSlowLogCollectors, errMsg, capabilityState string) {
	markCollectorFailureWithCode(collector, errMsg, capabilityState, "")
}

// noteSlowLogSkipOnce 记录「预期内跳过」（未开启 / 仅表格 / 探测失败）。
// 只在状态跃迁时写一条 Info，不产生任何 Warn。
func noteSlowLogSkipOnce(collector *performanceModels.InsightPerformanceSlowLogCollectors) {
	if collector == nil {
		return
	}
	message := fmt.Sprintf("[slowlog_collect] instance=%s 慢日志不可采集（%s），已跳过：%s",
		strings.TrimSpace(collector.InstanceID),
		strings.TrimSpace(collector.CapabilityState),
		capabilityCause(collector.CapabilityState, collector.ProbeError),
	)
	signature := truncateNotifySignature(message)
	if signature == strings.TrimSpace(collector.LastNotifiedError) {
		return
	}
	global.App.Log.Info(message)
	collector.LastNotifiedError = signature
	collector.LastNotifiedAt = commonModels.LocalTime(time.Now())
	_ = global.App.DB.Model(collector).Updates(map[string]any{
		"last_notified_error": signature,
		"last_notified_at":    collector.LastNotifiedAt,
	}).Error
}

// logSlowLogWarnOnce 用于采集器准备阶段的异常（多为元数据库写入失败）。
// 这类错误不来自具体实例，无法挂在 collector 行上做去重，保持 Warn 以便及时暴露基础设施问题。
func logSlowLogWarnOnce(instance *commonModels.InsightInstances, message string) {
	instanceID := ""
	if instance != nil {
		instanceID = strings.TrimSpace(instance.InstanceID)
	}
	global.App.Log.Warnf("[slowlog_collect] instance=%s %s", instanceID, message)
}

func truncateNotifySignature(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= slowLogNotifySignatureMax {
		return string(runes)
	}
	return string(runes[:slowLogNotifySignatureMax])
}

// isSlowLogNotFoundError 识别远端脚本的 `__ERR__:slow log not found:<path>`。
func isSlowLogNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "slow log not found")
}

func readSlowLogPathFromExtra(instance *commonModels.InsightInstances, fallback string) string {
	if instance == nil || len(instance.ExtraConfig) == 0 {
		return fallback
	}
	var extra map[string]any
	if err := json.Unmarshal(instance.ExtraConfig, &extra); err != nil {
		return fallback
	}
	if v, ok := extra["slow_log_path"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

// readSlowLogPathOverride 返回人工配置的慢日志路径覆盖值；未配置时返回空串。
// 人工覆盖优先级最高，便于在探测失效时临时指定路径。
func readSlowLogPathOverride(instance *commonModels.InsightInstances) string {
	return readSlowLogPathFromExtra(instance, "")
}

func readSlowLogEnabledFromExtra(instance *commonModels.InsightInstances) bool {
	if instance == nil || len(instance.ExtraConfig) == 0 {
		return true
	}
	var extra map[string]any
	if err := json.Unmarshal(instance.ExtraConfig, &extra); err != nil {
		return true
	}
	if raw, ok := extra["slow_log_collect_enabled"]; ok {
		switch typed := raw.(type) {
		case bool:
			return typed
		case string:
			text := strings.TrimSpace(strings.ToLower(typed))
			return text != "false" && text != "0" && text != "off"
		case float64:
			return int(typed) != 0
		}
	}
	return true
}

func resolveSSHCredentialsFromPerformanceInstance(instance *commonModels.InsightInstances) (sshUser string, sshPassword string, sshPrivateKey string, sshPort int, ok bool, reason string) {
	if instance == nil {
		return "", "", "", 22, false, "实例信息不存在"
	}
	sshPort = 22
	var extra map[string]any
	if len(instance.ExtraConfig) > 0 {
		_ = json.Unmarshal(instance.ExtraConfig, &extra)
	}
	if extra != nil {
		if v, ok := extra["ssh_user"].(string); ok {
			sshUser = strings.TrimSpace(v)
		}
		if v, ok := extra["ssh_password"].(string); ok {
			sshPassword = strings.TrimSpace(v)
		}
		if v, ok := extra["ssh_private_key"].(string); ok {
			sshPrivateKey = strings.TrimSpace(v)
		}
		switch typed := extra["ssh_port"].(type) {
		case float64:
			if typed > 0 {
				sshPort = int(typed)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && n > 0 {
				sshPort = n
			}
		}
	}
	if sshUser == "" {
		sshUser = strings.TrimSpace(string(instance.User))
	}
	if sshPassword == "" && sshPrivateKey == "" {
		sshPassword = strings.TrimSpace(string(instance.Password))
	}
	if strings.TrimSpace(sshPassword) != "" {
		if plain, err := secret.Decrypt(strings.TrimSpace(sshPassword)); err == nil && plain != "" {
			sshPassword = plain
		}
	}
	if strings.TrimSpace(sshPrivateKey) != "" {
		if plain, err := secret.Decrypt(strings.TrimSpace(sshPrivateKey)); err == nil && plain != "" {
			sshPrivateKey = plain
		}
	}
	if strings.TrimSpace(sshPassword) == "" && strings.TrimSpace(sshPrivateKey) == "" {
		return sshUser, "", "", sshPort, false, "未配置 ssh_password 或 ssh_private_key"
	}
	return strings.TrimSpace(sshUser), strings.TrimSpace(sshPassword), strings.TrimSpace(sshPrivateKey), sshPort, true, ""
}

func openSSHClient(host string, port int, user, password, privateKey string) (*ssh.Client, error) {
	authMethods := []ssh.AuthMethod{}
	if strings.TrimSpace(privateKey) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(privateKey))
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}
	if strings.TrimSpace(password) != "" {
		authMethods = append(authMethods, ssh.Password(password))
		authMethods = append(authMethods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = password
			}
			return answers, nil
		}))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("缺少可用 SSH 认证方式")
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	return ssh.Dial("tcp", fmt.Sprintf("%s:%d", strings.TrimSpace(host), port), cfg)
}

// errPGLogPathUnresolved PG 实例没有可用的日志文件路径（探测没拿到 pg_current_logfile()）。
// 单独成错，避免落到「文件不存在」把用户引向权限/磁盘排查。
var errPGLogPathUnresolved = errors.New("postgres slow log path unresolved")

// mysqlDatadirFallbackScript 仅在 MySQL 族使用：配置的慢日志路径不存在时，
// 从 mysqld 进程的 --datadir 里找 *-slow.log 兜底。
// PG 实例上绝不执行这段——同一台主机可能装了 MySQL，
// 误读到 MySQL 的 slow.log 会产出一批看似正常、实际无关的「慢SQL」。
const mysqlDatadirFallbackScript = `
  DATADIR=$($SUDO_BIN sh -c "ps -ef | grep '[m]ysqld' | sed -n 's/.*--datadir=\([^ ]*\).*/\1/p' | head -n 1" 2>/dev/null || true)
  if [ -n "$DATADIR" ]; then
    HOST_FULL=$(hostname 2>/dev/null || true)
    HOST_SHORT=$(hostname -s 2>/dev/null || true)
    for CAND in "$DATADIR/${HOST_FULL}-slow.log" "$DATADIR/${HOST_SHORT}-slow.log"; do
      if $SUDO_BIN test -f "$CAND" 2>/dev/null; then LOG_PATH="$CAND"; break; fi
    done
    if ! $SUDO_BIN test -f "$LOG_PATH" 2>/dev/null; then
      AUTO_PATH=$($SUDO_BIN sh -c "ls -1 \"$DATADIR\"/*-slow.log 2>/dev/null | head -n 1" || true)
      if [ -n "$AUTO_PATH" ]; then LOG_PATH="$AUTO_PATH"; fi
    fi
  fi`

// buildInspectRemoteScript 生成远端读取脚本。
//
// 与引擎无关的部分：sudo 自举、stat 取 inode/size、按 offset 增量 dd；
// 只有 MySQL 族才带上「从 mysqld --datadir 里找 *-slow.log」的兜底——
// PG 实例所在主机可能同时跑着 MySQL，误读会把无关的慢日志当 PG 数据入库。
func buildInspectRemoteScript(logPath, prevInode string, prevOffset int64, postgres bool) string {
	maxRead := defaultMySQLSlowLogMaxRead
	// fallback 必须包含完整的 `if ... then ... fi`：
	// 只把「块内语句」做成可选、外层 if/then/fi 恒定输出的话，
	// PG 分支会生成一个空的 then 块，bash 解析阶段就报
	// `syntax error near unexpected token 'fi'`——脚本根本跑不到读文件那一步。
	fallback := ""
	if !postgres {
		fallback = fmt.Sprintf(`
if ! $SUDO_BIN test -f "$LOG_PATH" 2>/dev/null; then%s
fi`, mysqlDatadirFallbackScript)
	}
	return fmt.Sprintf(`set -e
LOG_PATH=%q
PREV_INODE=%q
SUDO_BIN=""
if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  SUDO_BIN="sudo"
fi
%s
if ! $SUDO_BIN test -f "$LOG_PATH" 2>/dev/null; then
  echo "__ERR__:slow log not found:$LOG_PATH"
  exit 3
fi
META=$($SUDO_BIN stat -c '%%i|%%s' "$LOG_PATH")
INODE=$(printf '%%s' "$META" | cut -d'|' -f1)
SIZE=$(printf '%%s' "$META" | cut -d'|' -f2)
OFFSET=%d
if [ -n "$PREV_INODE" ] && [ "$INODE" != "$PREV_INODE" ]; then OFFSET=0; fi
if [ "$OFFSET" -gt "$SIZE" ]; then OFFSET=0; fi
READ_SIZE=$((SIZE - OFFSET))
if [ "$READ_SIZE" -gt %d ]; then OFFSET=$((SIZE - %d)); READ_SIZE=%d; fi
echo "__META__|$INODE|$SIZE|$OFFSET|$LOG_PATH"
if [ "$READ_SIZE" -gt 0 ]; then
  $SUDO_BIN dd if="$LOG_PATH" bs=1 skip="$OFFSET" count="$READ_SIZE" status=none
fi
`, logPath, strings.TrimSpace(prevInode), fallback, prevOffset, maxRead, maxRead, maxRead)
}

// inspectRemoteSlowLog 通过 SSH 读取远端日志的 stat 信息与增量内容。
//
// prevInode 用于识别日志轮转：文件被替换（PG 默认每天换新文件名、
// MySQL logrotate 也会重建）时 inode 变化，此时必须从 0 开始读，
// 否则会按旧文件的偏移去截新文件，丢掉开头一段（表现为「少采了若干条」）。
func inspectRemoteSlowLog(client *ssh.Client, logPath string, prevOffset int64, prevInode string, postgres bool) (*slowLogRemoteState, error) {
	if client == nil {
		return nil, fmt.Errorf("ssh client is nil")
	}
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		// MySQL 有约定俗成的 /var/log/mysql/slow.log 可以赌一把；
		// PG 没有「默认慢日志文件」，路径必须由 pg_current_logfile() 给出，
		// 猜一个路径只会读出无关文件或误报「文件缺失」。
		if postgres {
			return nil, errPGLogPathUnresolved
		}
		logPath = defaultMySQLSlowLogPath
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	cmd := buildInspectRemoteScript(logPath, prevInode, prevOffset, postgres)
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return nil, fmt.Errorf(text)
		}
		return nil, err
	}
	raw := string(output)
	lines := strings.SplitN(raw, "\n", 2)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "__META__|") {
		return nil, fmt.Errorf("unexpected remote slow log response")
	}
	parts := strings.Split(lines[0], "|")
	if len(parts) < 5 {
		return nil, fmt.Errorf("invalid remote slow log meta")
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	offset, _ := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
	tail := ""
	if len(lines) > 1 {
		tail = lines[1]
	}
	return &slowLogRemoteState{
		Inode:        strings.TrimSpace(parts[1]),
		Size:         size,
		Offset:       offset + int64(len(tail)),
		Tail:         tail,
		ResolvedPath: strings.TrimSpace(parts[4]),
	}, nil
}

func parseMySQLSlowLogChunk(content string, baseOffset int64) ([]parsedSlowLogEvent, string, error) {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, "", nil
	}

	type partialBlock struct {
		start    int64
		lines    []string
		rawLines []string
	}

	blocks := make([]partialBlock, 0)
	current := partialBlock{}
	currentOffset := baseOffset

	rawLines := strings.SplitAfter(text, "\n")
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	for _, rawLine := range rawLines {
		line := strings.TrimSuffix(rawLine, "\n")
		if strings.HasPrefix(line, "# Time:") {
			if len(current.rawLines) > 0 {
				blocks = append(blocks, current)
			}
			current = partialBlock{
				start:    currentOffset,
				lines:    []string{line},
				rawLines: []string{rawLine},
			}
		} else if len(current.rawLines) > 0 {
			current.lines = append(current.lines, line)
			current.rawLines = append(current.rawLines, rawLine)
		}
		currentOffset += int64(len(rawLine))
	}

	rest := ""
	if len(current.rawLines) > 0 {
		blocks = append(blocks, current)
		last := blocks[len(blocks)-1]
		if !strings.HasSuffix(strings.Join(last.rawLines, ""), "\n") || !hasMySQLSlowLogStats(last.lines) {
			rest = strings.Join(last.rawLines, "")
			blocks = blocks[:len(blocks)-1]
		}
	}

	events := make([]parsedSlowLogEvent, 0, len(blocks))
	for _, block := range blocks {
		event := parseMySQLSlowLogEvent(block)
		events = append(events, event)
	}
	return events, rest, nil
}

func hasMySQLSlowLogStats(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, "# Query_time:") {
			return true
		}
	}
	return false
}

func parseMySQLSlowLogEvent(block struct {
	start    int64
	lines    []string
	rawLines []string
}) parsedSlowLogEvent {
	event := parsedSlowLogEvent{
		OffsetStart: block.start,
		OffsetEnd:   block.start + int64(len(strings.Join(block.rawLines, ""))),
		ParseStatus: slowLogParseStatusParsed,
		RawText:     strings.Join(block.rawLines, ""),
	}
	sqlLines := make([]string, 0)

	for _, line := range block.lines {
		trimmed := strings.TrimSpace(line)
		if matches := mysqlSlowLogTimeRegexp.FindStringSubmatch(line); len(matches) == 2 {
			if t, err := parseMySQLSlowLogTime(matches[1]); err == nil {
				event.EventTime = t
			}
			continue
		}
		if matches := mysqlSlowLogUserRegexp.FindStringSubmatch(line); len(matches) >= 4 {
			event.Username = strings.TrimSpace(matches[1])
			event.ClientHost = strings.TrimSpace(matches[3])
			continue
		}
		if matches := mysqlSlowLogStatsRegexp.FindStringSubmatch(line); len(matches) == 5 {
			event.QueryTimeMS = parseSlowSecondsToMS(matches[1])
			event.LockTimeMS = parseSlowSecondsToMS(matches[2])
			event.RowsSent, _ = strconv.Atoi(matches[3])
			event.RowsExamined, _ = strconv.Atoi(matches[4])
			continue
		}
		if matches := mysqlSlowLogThreadRegexp.FindStringSubmatch(line); len(matches) == 3 {
			event.ThreadID = strings.TrimSpace(matches[1])
			event.SchemaName = strings.TrimSpace(matches[2])
			continue
		}
		if strings.HasPrefix(line, "SET timestamp=") {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if matches := mysqlSlowLogUseRegexp.FindStringSubmatch(trimmed); len(matches) == 2 {
			if event.SchemaName == "" {
				event.SchemaName = strings.TrimSpace(matches[1])
			}
			continue
		}
		if trimmed != "" {
			sqlLines = append(sqlLines, line)
		}
	}

	event.SQLText = strings.TrimSpace(strings.Join(sqlLines, "\n"))
	event.SQLFingerprint = normalizeSQLFingerprint(event.SQLText)
	if event.SQLFingerprint != "" {
		sum := sha256.Sum256([]byte(event.SQLFingerprint))
		event.SQLDigest = hex.EncodeToString(sum[:])
	}
	if event.QueryTimeMS <= 0 || event.SQLText == "" {
		event.ParseStatus = slowLogParseStatusParseFailed
		event.ParseError = "缺少 Query_time 或 SQL 文本"
	}
	return event
}

func parseMySQLSlowLogTime(text string) (time.Time, error) {
	raw := strings.TrimSpace(text)
	layouts := []string{
		// MySQL 8.0 标准输出：2026-09-15T11:23:44.894523+08:00
		//
		// 关键坑：Go 的 layout 里写 `Z` 表示「字面量字母 Z」，只能匹配 UTC 的 'Z' 写法，
		// 匹配不了 +08:00 这类数值偏移 —— 必须写 `Z07:00`（它同时兼容 'Z' 与 +08:00）。
		// 原先误用字面量 Z，导致 MySQL 8.0 的 # Time 行全部解析失败、event_time 落成 NULL。
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		// 不带小数秒的 ISO 变体
		"2006-01-02T15:04:05",
		// MySQL 5.x / Percona 旧格式
		"060102 15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		// 必须用 ParseInLocation：layout 里不带时区时，time.Parse 会按 UTC 解释，
		// 再 .Local() 就白白平移了一个时区（实测旧格式 11:23:44 被解析成 19:23:44）。
		// 带 Z07:00 的 layout 由值自身的偏移决定，ParseInLocation 不会覆盖它。
		if ts, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported slow log time: %s", raw)
}

func parseSlowSecondsToMS(raw string) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return int(f * 1000)
}

func normalizeSQLFingerprint(sqlText string) string {
	text := strings.TrimSpace(sqlText)
	if text == "" {
		return ""
	}
	text = sqlLiteralStringRegexp.ReplaceAllString(text, "?")
	text = sqlLiteralNumberRegexp.ReplaceAllString(text, "?")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.ToLower(text)
	if len(text) > 255 {
		return text[:255]
	}
	return text
}
