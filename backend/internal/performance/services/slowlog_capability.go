package services

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dbfamily "github.com/simajiong/jaysql/internal/common/dbfamily"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"github.com/simajiong/jaysql/pkg/secret"
)

// 慢日志采集能力五态（capability_state）
//
// 设计意图：把"这个实例能不能采"从一个隐含假设，变成显式状态。
// 只有 ENABLED_FILE 会进入采集链路；DISABLED / ENABLED_TABLE / PROBE_FAILED 属于
// 「预期内不可采集」，直接跳过、不建 SSH、不打 WARN；FILE_MISSING 才是真异常
// （服务端声明已开启文件方式，但文件读不到——磁盘满、权限、被手动删除等）。
const (
	capabilityEnabledFile  = "ENABLED_FILE"
	capabilityEnabledTable = "ENABLED_TABLE"
	capabilityDisabled     = "DISABLED"
	capabilityProbeFailed  = "PROBE_FAILED"
	capabilityFileMissing  = "FILE_MISSING"
	capabilityUnknown      = "UNKNOWN"
	// capabilityUnsupported 与 PROBE_FAILED 的区别：后者是"探测动作失败了"
	// （连不上、没权限），前者是"这个引擎压根不在支持范围内"。
	// 分开是为了让状态列能给出正确的指引——不支持就是不支持，让用户反复
	// 检查账号密码是没有意义的。
	capabilityUnsupported = "UNSUPPORTED"
)

// 探测结果状态（probe_status）
const (
	probeStatusOK              = "OK"
	probeStatusNoCredential    = "NO_CREDENTIAL"
	probeStatusConnectFailed   = "CONNECT_FAILED"
	probeStatusVariableMissing = "VARIABLE_MISSING"
	probeStatusUnsupported     = "UNSUPPORTED"
)

const (
	// 探测 TTL：可采集的实例用长 TTL，不可采集的用短 TTL 以便用户改完配置后快速自愈
	slowLogProbeTTLNormal = 10 * time.Minute
	slowLogProbeTTLRetry  = 2 * time.Minute
	slowLogProbeTimeout   = 5 * time.Second
)

// 探测所需的服务端变量。
// 注意 log_slow_queries 是旧版 MySQL / MariaDB 的开关变量名，与 slow_query_log 二者取一。
var slowLogCapabilityVariables = []string{
	"version",
	"hostname",
	"datadir",
	"log_output",
	"slow_query_log",
	"log_slow_queries",
	"slow_query_log_file",
	"long_query_time",
}

type slowLogCapability struct {
	InstanceID      string
	ProbeStatus     string
	ProbeError      string
	ServerVersion   string
	SlowLogEnabled  bool
	LogOutputMode   string
	FileCapable     bool
	TableCapable    bool
	RawLogFile      string
	Datadir         string
	ResolvedPath    string
	LongQueryTimeMS int
	State           string
	ProbedAt        time.Time
	// FailureCode 是可被前端映射到「前提条件项」的稳定失败码（见 slowlog_prerequisites.go）。
	// 与 ProbeError 的区别：ProbeError 是给人看的一句话（含 IP、路径等上下文），
	// FailureCode 是给程序用的枚举，前端靠它高亮对应的前提项并给出修复步骤。
	// 两者同时落库：前者排障用，后者做关联。
	FailureCode string
}

// probeSlowLogCapability 按引擎家族分派探测。
//
// 之前只有一条 MySQL 分支，因此 PG / Oracle 实例根本不会进入探测链路
// （连"不支持"这个结论都得不出来，直接被列表过滤掉了）。
// 这里用 dbfamily 收敛判定，新增引擎只要加一个分支 + 一个前提项清单。
func probeSlowLogCapability(instance *commonModels.InsightInstances) *slowLogCapability {
	dbType := strings.TrimSpace(string(instance.DbType))
	switch dbfamily.EffectiveFamily(dbType, instance.ExtraConfig) {
	case dbfamily.FamilyMySQL:
		return probeMySQLSlowLogCapability(instance)
	case dbfamily.FamilyPostgres:
		return probePostgresSlowLogCapability(instance)
	case dbfamily.FamilyOracle:
		return probeOracleSlowLogCapability(instance)
	default:
		return &slowLogCapability{
			InstanceID:  strings.TrimSpace(instance.InstanceID),
			ProbeStatus: probeStatusUnsupported,
			ProbeError:  fmt.Sprintf("引擎 %s 暂不支持慢日志采集", dbType),
			State:       capabilityUnsupported,
			FailureCode: failEngineUnsupported,
			ProbedAt:    time.Now(),
		}
	}
}

// Collectable 表示探测结论为「可走文件采集链路」。
func (c *slowLogCapability) Collectable() bool {
	if c == nil {
		return false
	}
	return c.ProbeStatus == probeStatusOK && c.SlowLogEnabled && c.FileCapable
}

// probeMySQLSlowLogCapability 通过 MySQL 协议直接询问服务端自身配置。
//
// 为什么不走 SSH 或解析 my.cnf：这两个实例是 systemd 托管的 MySQL 8.0，
// datadir 写在 my.cnf 里、命令行上没有 --datadir=，靠 ps/sed 猜路径会全部落空。
// SHOW GLOBAL VARIABLES 是唯一权威来源。
func probeMySQLSlowLogCapability(instance *commonModels.InsightInstances) *slowLogCapability {
	if instance == nil {
		return &slowLogCapability{
			ProbeStatus: probeStatusNoCredential,
			ProbeError:  "实例不存在",
			State:       capabilityProbeFailed,
			ProbedAt:    time.Now(),
		}
	}

	instanceID := strings.TrimSpace(instance.InstanceID)
	user := strings.TrimSpace(instance.User)
	password := strings.TrimSpace(instance.Password)
	if password != "" {
		if plain, err := secret.Decrypt(password); err == nil && strings.TrimSpace(plain) != "" {
			password = strings.TrimSpace(plain)
		}
	}
	if user == "" || password == "" {
		return &slowLogCapability{
			InstanceID:  instanceID,
			ProbeStatus: probeStatusNoCredential,
			ProbeError:  "未配置可用的实例账号或密码，无法探测服务端慢日志配置",
			State:       capabilityProbeFailed,
			ProbedAt:    time.Now(),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), slowLogProbeTimeout)
	defer cancel()

	db := dao.DB{
		User:     user,
		Password: password,
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: "",
		Params:   map[string]string{"charset": "utf8mb4"},
		Ctx:      ctx,
	}
	_, rows, err := db.Query(buildSlowLogVariableQuery())
	if err != nil {
		return &slowLogCapability{
			InstanceID:  instanceID,
			ProbeStatus: probeStatusConnectFailed,
			ProbeError:  fmt.Sprintf("连接实例查询慢日志变量失败：%v", err),
			State:       capabilityProbeFailed,
			ProbedAt:    time.Now(),
		}
	}

	vars := map[string]string{}
	if rows != nil {
		vars = parseSlowLogVariables(*rows)
	}
	return buildSlowLogCapability(instanceID, vars)
}

func buildSlowLogVariableQuery() string {
	quoted := make([]string, 0, len(slowLogCapabilityVariables))
	for _, name := range slowLogCapabilityVariables {
		quoted = append(quoted, "'"+name+"'")
	}
	return "SHOW GLOBAL VARIABLES WHERE Variable_name IN (" + strings.Join(quoted, ",") + ")"
}

// parseSlowLogVariables 把 SHOW GLOBAL VARIABLES 的结果行转成 变量名 -> 值 的小写映射。
func parseSlowLogVariables(rows []map[string]interface{}) map[string]string {
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		name, value := "", ""
		for column, raw := range row {
			switch strings.ToUpper(strings.TrimSpace(column)) {
			case "VARIABLE_NAME":
				name = stringifyVariableValue(raw)
			case "VALUE":
				value = stringifyVariableValue(raw)
			}
		}
		if name == "" {
			continue
		}
		out[strings.ToLower(name)] = value
	}
	return out
}

func stringifyVariableValue(raw interface{}) string {
	if raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw))
}

// buildSlowLogCapability 是纯函数，不依赖数据库，便于单元测试。
func buildSlowLogCapability(instanceID string, vars map[string]string) *slowLogCapability {
	capability := &slowLogCapability{
		InstanceID:    strings.TrimSpace(instanceID),
		ProbeStatus:   probeStatusOK,
		ProbedAt:      time.Now(),
		ServerVersion: strings.TrimSpace(vars["version"]),
		RawLogFile:    strings.TrimSpace(vars["slow_query_log_file"]),
		Datadir:       normalizeSlowLogDir(vars["datadir"]),
	}

	enabled, ok := parseSlowLogSwitch(vars)
	if !ok {
		capability.ProbeStatus = probeStatusVariableMissing
		capability.ProbeError = "实例未返回可识别的 slow_query_log / log_slow_queries 变量"
		capability.State = capabilityProbeFailed
		return capability
	}

	capability.SlowLogEnabled = enabled
	capability.LogOutputMode = normalizeLogOutput(vars["log_output"])
	capability.FileCapable = slowLogOutputHasFile(capability.LogOutputMode)
	capability.TableCapable = slowLogOutputHasTable(capability.LogOutputMode)
	capability.LongQueryTimeMS = parseSlowLogLongQueryTimeMS(vars["long_query_time"])
	capability.ResolvedPath = resolveSlowLogPath(
		strings.TrimSpace(vars["hostname"]),
		capability.RawLogFile,
		capability.Datadir,
	)
	capability.State = decideSlowLogCapabilityState(capability)
	return capability
}

// parseSlowLogSwitch 读取慢日志开关，兼容 MariaDB / 旧版 MySQL 的 log_slow_queries。
func parseSlowLogSwitch(vars map[string]string) (bool, bool) {
	for _, name := range []string{"slow_query_log", "log_slow_queries"} {
		raw, exists := vars[name]
		if !exists {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(raw)) {
		case "ON", "1", "TRUE", "YES":
			return true, true
		case "OFF", "0", "FALSE", "NO":
			return false, true
		default:
			return false, false
		}
	}
	return false, false
}

// normalizeLogOutput 归一到 FILE / TABLE / FILE,TABLE / NONE 之一。
func normalizeLogOutput(raw string) string {
	text := strings.ToUpper(strings.TrimSpace(raw))
	if text == "" {
		return "NONE"
	}
	parts := strings.Split(text, ",")
	found := make([]string, 0, 2)
	for _, candidate := range []string{"FILE", "TABLE"} {
		for _, part := range parts {
			if strings.TrimSpace(part) == candidate {
				found = append(found, candidate)
				break
			}
		}
	}
	if len(found) == 0 {
		return text
	}
	return strings.Join(found, ",")
}

func slowLogOutputHasFile(mode string) bool {
	return strings.Contains(strings.ToUpper(strings.TrimSpace(mode)), "FILE")
}

func slowLogOutputHasTable(mode string) bool {
	return strings.Contains(strings.ToUpper(strings.TrimSpace(mode)), "TABLE")
}

func parseSlowLogLongQueryTimeMS(raw string) int {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 {
		return 0
	}
	return int(value * 1000)
}

func normalizeSlowLogDir(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	return filepath.Clean(text)
}

// resolveSlowLogPath 复刻 MySQL 自身的解析规则：
//   - 绝对路径 → 原样使用
//   - 相对路径 → 相对 datadir 解析（例如 my.cnf 写 ../logs/slow.log）
//   - 变量为空 → 回退 MySQL 默认命名 $datadir/$hostname-slow.log
func resolveSlowLogPath(serverHostname, rawFile, datadir string) string {
	raw := strings.TrimSpace(rawFile)
	if raw == "" {
		if datadir == "" {
			return ""
		}
		name := "slow.log"
		if strings.TrimSpace(serverHostname) != "" {
			name = strings.TrimSpace(serverHostname) + "-slow.log"
		}
		return filepath.Clean(filepath.Join(datadir, name))
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if datadir == "" {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(datadir, raw))
}

func decideSlowLogCapabilityState(capability *slowLogCapability) string {
	if capability == nil || capability.ProbeStatus != probeStatusOK {
		return capabilityProbeFailed
	}
	if !capability.SlowLogEnabled {
		return capabilityDisabled
	}
	if !capability.FileCapable {
		return capabilityEnabledTable
	}
	return capabilityEnabledFile
}

// slowLogCollectable 是采集调度的唯一门控：只有「服务端开着慢日志」且「以文件方式记录」
// 且「采集器未被人工停用」时才进入采集链路。
func slowLogCollectable(collector *performanceModels.InsightPerformanceSlowLogCollectors) bool {
	if collector == nil || !collector.Enabled {
		return false
	}
	return collector.SlowQueryLogEnabled && collector.FileCapable
}

// slowLogCapabilityNeedsAttention 用于概览页提醒：五态中除 ENABLED_FILE 外都值得提示。
func slowLogCapabilityNeedsAttention(state string) bool {
	switch strings.TrimSpace(state) {
	case capabilityDisabled, capabilityEnabledTable, capabilityProbeFailed, capabilityFileMissing:
		return true
	default:
		return false
	}
}

// slowLogProbeDue 判断是否需要重新探测：不可采集状态用更短 TTL，便于用户改完配置后自愈。
func slowLogProbeDue(capabilityState string, lastProbeAt commonModels.LocalTime) bool {
	last := time.Time(lastProbeAt)
	if last.IsZero() {
		return true
	}
	ttl := slowLogProbeTTLNormal
	if strings.TrimSpace(capabilityState) != capabilityEnabledFile {
		ttl = slowLogProbeTTLRetry
	}
	return time.Since(last) >= ttl
}

// capabilityStateLabel / capabilityCause 供 API 与前端展示使用。
func capabilityStateLabel(state string) string {
	switch strings.TrimSpace(state) {
	case capabilityEnabledFile:
		return "已开启（文件方式）"
	case capabilityEnabledTable:
		return "已开启（仅表格方式）"
	case capabilityDisabled:
		return "未开启慢日志"
	case capabilityProbeFailed:
		return "无法探测"
	case capabilityFileMissing:
		return "慢日志文件不可读"
	case capabilityUnsupported:
		return "引擎暂不支持"
	default:
		return "未知"
	}
}

func capabilityCause(state, probeError string) string {
	switch strings.TrimSpace(state) {
	case capabilityEnabledFile:
		return ""
	case capabilityEnabledTable:
		return "服务端 log_output 未包含 FILE，慢SQL 仅写入 mysql.slow_log 表，平台暂不支持采集表方式"
	case capabilityDisabled:
		return "服务端 slow_query_log 为 OFF，慢SQL 未被记录"
	case capabilityProbeFailed:
		if strings.TrimSpace(probeError) != "" {
			return strings.TrimSpace(probeError)
		}
		return "无法探测实例的慢日志配置"
	case capabilityFileMissing:
		return "服务端已开启文件方式慢日志，但文件读取失败（权限、磁盘或文件被清理）"
	case capabilityUnsupported:
		if strings.TrimSpace(probeError) != "" {
			return strings.TrimSpace(probeError)
		}
		return "该数据库类型暂不支持慢SQL采集"
	default:
		return "尚未完成能力探测"
	}
}

func capabilityAdvice(state string) string {
	switch strings.TrimSpace(state) {
	case capabilityEnabledTable:
		return "SET GLOBAL log_output='FILE';"
	case capabilityDisabled:
		return "SET GLOBAL slow_query_log=ON;"
	case capabilityProbeFailed:
		return "请在实例配置中检查数据库账号密码与网络连通性"
	case capabilityFileMissing:
		return "请检查慢日志文件是否存在、权限是否可读"
	case capabilityUnsupported:
		return "如需支持该引擎，请提交需求并附上版本与错误码"
	default:
		return ""
	}
}
