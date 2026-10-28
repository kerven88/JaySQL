package services

// ============================================================================
// 慢 SQL 采集失败的「统一错误枚举」
//
// 为什么需要这一层：
//   采集链路原本直接把引擎自己的失败码（MYSQL_SSH_UNREACHABLE、
//   PG_LOGGING_COLLECTOR_OFF、ORACLE_NO_VSQL_ACCESS...）透给前端，
//   三个引擎加起来三十多个码，且命名规则各不相同。前端要渲染状态列，
//   就得在每个分支里 if mysql / if pg / if oracle，加一个新引擎等于改一遍前端。
//
//   这里把三引擎的失败统一收敛到十几个**语义枚举**：不可达 / 认证失败 /
//   权限不足 / 超时 / 参数不兼容 / 版本不支持 ... 前端只认枚举，
//   新增引擎只需在下面的映射表补一行，前端零改动。
//
// 设计约定：
//   - Code 是对外契约，稳定不改名，前端按它配色与分组；
//   - Status 决定列表状态列的主标识：OK / FAILED / NOT_READY；
//     FAILED = 采集动作发起了但失败（故障，需要人处理）；
//     NOT_READY = 条件不满足（未开启、不支持、未探测），不是故障但要让用户看见；
//   - 引擎原始失败码保留在 FailureCode 字段一起返回，排查时仍可定位到具体引擎原因，
//     归一化不会丢信息。
// ============================================================================

import "strings"

// ---------- 采集状态（列表状态列取值，前后端共用）----------
const (
	collectStatusOK       = "OK"
	collectStatusFailed   = "FAILED"
	collectStatusNotReady = "NOT_READY"
)

// ---------- 渲染色调 ----------
const (
	errorToneOK      = "ok"
	errorToneError   = "error"
	errorToneWarning = "warning"
)

// ---------- 错误类别（前端按类分组/聚合用）----------
const (
	errCategoryNone       = ""
	errCategoryConnect    = "connect"
	errCategoryAuth       = "auth"
	errCategoryPermission = "permission"
	errCategoryTimeout    = "timeout"
	errCategoryParameter  = "parameter"
	errCategoryVersion    = "version"
	errCategoryFile       = "file"
	errCategoryParse      = "parse"
	errCategoryPersist    = "persist"
	errCategoryPlatform   = "platform"
	errCategoryUnknown    = "unknown"
)

// ---------- 统一错误码 ----------
const (
	// errCodeNone 采集正常，没有错误。
	errCodeNone = "OK"
	// errCodeUnreachable 实例不可达：连接超时、网络异常、主机不可达、SSH 不通。
	errCodeUnreachable = "UNREACHABLE"
	// errCodeAuthFailed 账号或口令错误、或压根没配账号。
	errCodeAuthFailed = "AUTH_FAILED"
	// errCodePermissionDenied 账号权限不足（缺角色、缺视图授权、文件不可读权限）。
	errCodePermissionDenied = "PERMISSION_DENIED"
	// errCodeQueryTimeout 查询或探测超时。
	errCodeQueryTimeout = "QUERY_TIMEOUT"
	// errCodeParamIncompatible 慢日志相关参数未开启或取值不兼容。
	errCodeParamIncompatible = "PARAM_INCOMPATIBLE"
	// errCodeVersionUnsupported 数据库版本过低或驱动不支持。
	errCodeVersionUnsupported = "VERSION_UNSUPPORTED"
	// errCodeSlowLogDisabled 服务端未开启慢SQL记录（参数层面直接关闭）。
	errCodeSlowLogDisabled = "SLOW_LOG_DISABLED"
	// errCodeLogFileUnreadable 慢日志文件缺失、被轮转清理或权限不足。
	errCodeLogFileUnreadable = "LOG_FILE_UNREADABLE"
	// errCodeParseFailed 日志文件读到了，但内容解析不出慢SQL条目。
	errCodeParseFailed = "PARSE_FAILED"
	// errCodePersistFailed 采集/采样数据写入元库失败。
	errCodePersistFailed = "PERSIST_FAILED"
	// errCodeNotProbed 尚未完成采集能力探测（新接入、或探测还没跑过）。
	errCodeNotProbed = "NOT_PROBED"
	// errCodeCollectDisabled 平台侧已停用该实例的慢日志采集。
	errCodeCollectDisabled = "COLLECT_DISABLED"
	// errCodeUnknown 采集失败但无法归类到上述任何一类。
	errCodeUnknown = "UNKNOWN"
)

type slowLogErrorMeta struct {
	Code     string
	Label    string
	Category string
	Status   string
	Tone     string
}

// slowLogErrorCatalog 统一错误码 → 展示元数据（唯一事实来源）。
//
// Label 是给用户看的一句话原因，要求「说清是什么问题、不必说怎么修」——
// 怎么修由前提条件清单（slowlog_prerequisites.go）按原始失败码给出。
var slowLogErrorCatalog = map[string]slowLogErrorMeta{
	errCodeNone: {
		Code: errCodeNone, Label: "采集正常", Category: errCategoryNone,
		Status: collectStatusOK, Tone: errorToneOK,
	},
	errCodeUnreachable: {
		Code: errCodeUnreachable, Label: "实例不可达（连接超时或网络异常）", Category: errCategoryConnect,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodeAuthFailed: {
		Code: errCodeAuthFailed, Label: "账号或口令错误，无法登录实例", Category: errCategoryAuth,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodePermissionDenied: {
		Code: errCodePermissionDenied, Label: "账号权限不足，无法读取慢日志数据", Category: errCategoryPermission,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodeQueryTimeout: {
		Code: errCodeQueryTimeout, Label: "查询或探测超时", Category: errCategoryTimeout,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodeParamIncompatible: {
		Code: errCodeParamIncompatible, Label: "慢日志参数未开启或取值不兼容", Category: errCategoryParameter,
		Status: collectStatusNotReady, Tone: errorToneWarning,
	},
	errCodeVersionUnsupported: {
		Code: errCodeVersionUnsupported, Label: "数据库版本或驱动不支持慢SQL采集", Category: errCategoryVersion,
		Status: collectStatusNotReady, Tone: errorToneWarning,
	},
	errCodeSlowLogDisabled: {
		Code: errCodeSlowLogDisabled, Label: "服务端未开启慢SQL记录", Category: errCategoryParameter,
		Status: collectStatusNotReady, Tone: errorToneWarning,
	},
	errCodeLogFileUnreadable: {
		Code: errCodeLogFileUnreadable, Label: "慢日志文件缺失或不可读", Category: errCategoryFile,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodeParseFailed: {
		Code: errCodeParseFailed, Label: "慢日志内容解析失败，无法提取指标", Category: errCategoryParse,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodePersistFailed: {
		Code: errCodePersistFailed, Label: "采集数据写入元库失败", Category: errCategoryPersist,
		Status: collectStatusFailed, Tone: errorToneError,
	},
	errCodeNotProbed: {
		Code: errCodeNotProbed, Label: "尚未完成采集能力探测", Category: errCategoryUnknown,
		Status: collectStatusNotReady, Tone: errorToneWarning,
	},
	errCodeCollectDisabled: {
		Code: errCodeCollectDisabled, Label: "该实例的慢日志采集已在平台侧停用", Category: errCategoryPlatform,
		Status: collectStatusNotReady, Tone: errorToneWarning,
	},
	errCodeUnknown: {
		Code: errCodeUnknown, Label: "采集失败，原因未能归类", Category: errCategoryUnknown,
		Status: collectStatusFailed, Tone: errorToneError,
	},
}

// ---------- 引擎失败码 → 统一错误码 ----------
//
// 三个引擎的失败码在这里对齐到同一套语义。新增引擎只需补这张表，
// 前端与前提条件面板都不用改。

var engineFailureCodeMap = map[string]string{
	// 引擎级（跨引擎通用）
	failEngineUnsupported:   errCodeVersionUnsupported,
	failEngineNoCredential:  errCodeAuthFailed,
	failEngineSSHFailed:     errCodeUnreachable,
	failEngineFileMissing:   errCodeLogFileUnreadable,
	failEngineParseFailed:   errCodeParseFailed,
	failEnginePersistFailed: errCodePersistFailed,

	// MySQL
	failMySQLNoCredential:     errCodeAuthFailed,
	failMySQLConnectFailed:    errCodeUnreachable,
	failMySQLVariableMissing:  errCodeVersionUnsupported,
	failMySQLSlowLogDisabled:  errCodeSlowLogDisabled,
	failMySQLLogOutputNoFile:  errCodeParamIncompatible,
	failMySQLLongQueryTimeBad: errCodeParamIncompatible,
	failMySQLSSHUnreachable:   errCodeUnreachable,
	failMySQLFileMissing:      errCodeLogFileUnreadable,
	failMySQLFileNoRead:       errCodePermissionDenied,
	failMySQLParseFailed:      errCodeParseFailed,

	// PostgreSQL
	failPGNoCredential:        errCodeAuthFailed,
	failPGConnectFailed:       errCodeUnreachable,
	failPGVersionUnsupported:  errCodeVersionUnsupported,
	failPGCollectorOff:        errCodeParamIncompatible,
	failPGMinDurationDisabled: errCodeSlowLogDisabled,
	failPGMinDurationZero:     errCodeParamIncompatible,
	failPGPrefixMissingUserDB: errCodeParamIncompatible,
	failPGNoMonitorRole:       errCodePermissionDenied,
	failPGLogFileUnreadable:   errCodeLogFileUnreadable,
	failPGLogPathUnresolved:   errCodeParamIncompatible,
	failPGParseFailed:         errCodeParseFailed,

	// Oracle
	failOracleNoCredential:  errCodeAuthFailed,
	failOracleConnectFailed: errCodeUnreachable,
	failOracleNoVSQLAccess:  errCodePermissionDenied,
	failOracleStatsBasic:    errCodeParamIncompatible,
	failOracleVersionTooOld: errCodeVersionUnsupported,
	failOracleSampleFailed:  errCodeUnreachable,
	failOracleAWRNoLicense:  errCodeParamIncompatible,
	failOraclePersistFailed: errCodePersistFailed,
}

// ---------- 对外结构 ----------

// SlowLogErrorView 列表与明细共用的结构化错误。
//
// 字段刻意保持精简：Code 给程序、Message 给人、Detail 给排查，
// 前端不需要再解析任何引擎特定的字符串。
type SlowLogErrorView struct {
	// Status 采集状态：OK / FAILED / NOT_READY
	Status string `json:"collect_status"`
	// Code 统一错误码，OK 表示无错误
	Code string `json:"error_code"`
	// Message 可直接展示的中文原因
	Message string `json:"error_message"`
	// Category 错误类别（connect / auth / permission ...），供前端分组与配色
	Category string `json:"error_category"`
	// Tone 展示色调：ok / error / warning
	Tone string `json:"error_tone"`
	// Detail 原始错误文本（引擎返回的带上下文信息），供 tooltip / 日志排查
	Detail string `json:"error_detail"`
}

// NormalizeSlowLogError 把「引擎失败码 + 能力状态 + 原始错误文本」归一成统一错误视图。
//
// 判定优先级（越靠前越可信）：
//  1. 平台侧停用（enabled=false）—— 用户主动关的，不该被故障文案吓到；
//  2. 引擎失败码映射表 —— 探测代码明确知道失败原因，最可信；
//  3. 能力状态兜底 —— 没失败码时按状态猜一个大类；
//  4. 原始错误文本关键词 —— 只用于把「不可达 / 超时 / 权限」这类能从文本
//     看出来的再精化一层；猜不出来就保持第 3 步的结果，绝不向上升级。
//
// dbType / capabilityState 为空时按「未探测」处理，不会误报成故障。
func NormalizeSlowLogError(capabilityState string, failureCode string, errorText string, enabled bool) SlowLogErrorView {
	if !enabled {
		return buildSlowLogErrorView(errCodeCollectDisabled, errorText)
	}

	state := strings.ToUpper(strings.TrimSpace(capabilityState))
	code := strings.TrimSpace(failureCode)
	text := strings.TrimSpace(errorText)

	// 1) 引擎失败码映射
	if code != "" {
		if unified, ok := engineFailureCodeMap[code]; ok {
			view := buildSlowLogErrorView(unified, text)
			// 失败码已能定性时，仍允许文本把它精化成「超时」——
			// 同一个码（如 CONNECT_FAILED）既可能是被拒也可能是超时，
			// 而这两者的处置完全不同。
			if refined, changed := refineByErrorText(unified, text); changed {
				view = buildSlowLogErrorView(refined, text)
			}
			return view
		}
		// 未知失败码：不吞掉，落 UNKNOWN 并把原始码带进 Detail
		view := buildSlowLogErrorView(errCodeUnknown, text)
		if view.Detail == "" {
			view.Detail = code
		} else if !strings.Contains(view.Detail, code) {
			view.Detail = code + "：" + view.Detail
		}
		return view
	}

	// 2) 按能力状态兜底
	var fallback string
	switch state {
	case capabilityEnabledFile:
		// 状态正常但可能最近一次采集报错（collect_status=ERROR），
		// 此时靠文本判定；文本也看不出就按 UNKNOWN。
		if text == "" {
			return buildSlowLogErrorView(errCodeNone, "")
		}
		fallback = errCodeUnknown
	case capabilityDisabled:
		fallback = errCodeSlowLogDisabled
	case capabilityEnabledTable:
		fallback = errCodeParamIncompatible
	case capabilityFileMissing:
		fallback = errCodeLogFileUnreadable
	case capabilityProbeFailed, capabilityUnsupported:
		// 探测失败 / 引擎不支持：多数是连不上或版本问题，文本能说清就说清
		fallback = errCodeUnknown
	default:
		// UNKNOWN / 空状态 = 还没探测过
		return buildSlowLogErrorView(errCodeNotProbed, text)
	}
	if refined, changed := refineByErrorText(fallback, text); changed {
		fallback = refined
	}
	return buildSlowLogErrorView(fallback, text)
}

// NormalizeSlowLogParseError 把单条慢日志的解析状态归一成统一错误视图。
//
// 明细列表里「解析失败」的条目必须保留（用户要看见平台确实采到了这条、
// 但没能完整解析），所以这里只负责给出结构化原因，不做任何过滤。
func NormalizeSlowLogParseError(parseStatus string, parseError string) SlowLogErrorView {
	status := strings.ToUpper(strings.TrimSpace(parseStatus))
	text := strings.TrimSpace(parseError)
	switch {
	case status == "" || status == "PARSED":
		if text == "" {
			return buildSlowLogErrorView(errCodeNone, "")
		}
		return buildSlowLogErrorView(errCodeNone, text)
	default:
		return buildSlowLogErrorView(errCodeParseFailed, text)
	}
}

func buildSlowLogErrorView(code string, detail string) SlowLogErrorView {
	meta, ok := slowLogErrorCatalog[code]
	if !ok {
		meta = slowLogErrorCatalog[errCodeUnknown]
	}
	return SlowLogErrorView{
		Status:   meta.Status,
		Code:     meta.Code,
		Message:  meta.Label,
		Category: meta.Category,
		Tone:     meta.Tone,
		Detail:   strings.TrimSpace(detail),
	}
}

// refineByErrorText 用原始错误文本把「粗分类」精化成更准确的一类。
//
// 只在文本里出现确定性关键词时才改判，否则返回 changed=false——
// 宁可停在粗分类，也不要靠模糊匹配乱升级（把参数问题报成实例宕机）。
func refineByErrorText(current string, text string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return current, false
	}

	// 平台侧脚本缺陷：远端 bash 解析失败属于平台内部错误，
	// 绝不能被网络类关键词或默认映射带偏成「实例不可达」，误导运维去查网络。
	if strings.Contains(lower, "syntax error") {
		return errCodeUnknown, true
	}
	// 超时：超时是明确的一类，且优先级高于"不可达"
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "context deadline") || strings.Contains(lower, "超时") {
		return errCodeQueryTimeout, true
	}
	// 认证：必须排在权限之前 —— MySQL 的 "Access denied for user 'x'@'y'"
	// 是账号口令错（1045），语义上属于认证失败，被权限分支先接走就判错了。
	if strings.Contains(lower, "authentication failed") || strings.Contains(lower, "unable to authenticate") ||
		strings.Contains(lower, "invalid password") || strings.Contains(lower, "access denied for user") ||
		strings.Contains(lower, "ora-01017") || strings.Contains(lower, "password authentication failed") ||
		strings.Contains(lower, "no password supplied") {
		return errCodeAuthFailed, true
	}
	// 权限
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "insufficient privilege") || strings.Contains(lower, "ora-01031") ||
		strings.Contains(lower, "ora-00942") || strings.Contains(lower, "must be superuser") ||
		strings.Contains(lower, "权限不足") {
		return errCodePermissionDenied, true
	}
	// 网络/主机不可达
	// 注意：不能裸匹配 "eof" 子串——"thereof" 之类的普通单词会被误判成网络断连。
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "no route to host") || strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "dial tcp") || strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "broken pipe") || strings.Contains(lower, "unexpected eof") ||
		strings.Contains(lower, ": eof") || strings.HasSuffix(lower, " eof") ||
		strings.Contains(lower, "无法连接") || strings.Contains(lower, "连接失败") {
		return errCodeUnreachable, true
	}
	// 版本/驱动
	if strings.Contains(lower, "version") && (strings.Contains(lower, "unsupported") || strings.Contains(lower, "过低")) {
		return errCodeVersionUnsupported, true
	}
	// 文件
	if strings.Contains(lower, "no such file") || strings.Contains(lower, "not exist") ||
		strings.Contains(lower, "文件不存在") {
		return errCodeLogFileUnreadable, true
	}
	return current, false
}

// slowLogStatusRank 列表排序用的状态权重：正常在前，异常在后。
// 失败条目只是排得靠后，绝不会被剔除——排序与过滤是两件事。
func slowLogStatusRank(status string) int {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case collectStatusOK:
		return 0
	case collectStatusNotReady:
		return 1
	default:
		return 2
	}
}
