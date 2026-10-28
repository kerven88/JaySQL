package services

import "testing"

// TestNormalizeSlowLogErrorEngineCodes 守住「三引擎失败码 → 统一枚举」的映射。
//
// 这张表就是契约：新增引擎失败码时必须在 engineFailureCodeMap 登记，
// 否则会落进 UNKNOWN，前端就给不出明确原因。
func TestNormalizeSlowLogErrorEngineCodes(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		// 引擎级
		{"引擎不支持", failEngineUnsupported, errCodeVersionUnsupported},
		{"缺少账号", failEngineNoCredential, errCodeAuthFailed},
		{"SSH 不通", failEngineSSHFailed, errCodeUnreachable},
		{"文件缺失", failEngineFileMissing, errCodeLogFileUnreadable},
		{"解析失败", failEngineParseFailed, errCodeParseFailed},
		{"落库失败", failEnginePersistFailed, errCodePersistFailed},
		// MySQL
		{"MySQL 连不上", failMySQLConnectFailed, errCodeUnreachable},
		{"MySQL 未开慢日志", failMySQLSlowLogDisabled, errCodeSlowLogDisabled},
		{"MySQL 仅表输出", failMySQLLogOutputNoFile, errCodeParamIncompatible},
		{"MySQL 文件无权限", failMySQLFileNoRead, errCodePermissionDenied},
		// PostgreSQL
		{"PG 版本过低", failPGVersionUnsupported, errCodeVersionUnsupported},
		{"PG logging_collector 关闭", failPGCollectorOff, errCodeParamIncompatible},
		{"PG 未记录慢SQL", failPGMinDurationDisabled, errCodeSlowLogDisabled},
		{"PG 缺 pg_monitor", failPGNoMonitorRole, errCodePermissionDenied},
		// Oracle
		{"Oracle 无 V$SQL 权限", failOracleNoVSQLAccess, errCodePermissionDenied},
		{"Oracle statistics_level=BASIC", failOracleStatsBasic, errCodeParamIncompatible},
		{"Oracle 采样失败", failOracleSampleFailed, errCodeUnreachable},
		{"Oracle 落库失败", failOraclePersistFailed, errCodePersistFailed},
	}
	for _, item := range cases {
		got := NormalizeSlowLogError(capabilityProbeFailed, item.code, "", true)
		if got.Code != item.want {
			t.Errorf("%s：期望 %s，实际 %s", item.name, item.want, got.Code)
		}
	}
}

// TestNormalizeSlowLogErrorStatus 失败条目必须能拿到明确的「状态标识」：
// FAILED = 采集动作失败，NOT_READY = 条件不满足，两者都要留在列表里。
func TestNormalizeSlowLogErrorStatus(t *testing.T) {
	cases := []struct {
		name  string
		state string
		code  string
		text  string
		want  string
	}{
		{"正常采集", capabilityEnabledFile, "", "", collectStatusOK},
		{"不可达是故障", capabilityProbeFailed, failMySQLConnectFailed, "", collectStatusFailed},
		{"参数不兼容是条件不足", capabilityUnsupported, failPGCollectorOff, "", collectStatusNotReady},
		{"未开慢日志是条件不足", capabilityDisabled, "", "", collectStatusNotReady},
		{"仅表输出是条件不足", capabilityEnabledTable, "", "", collectStatusNotReady},
		{"文件不可读是故障", capabilityFileMissing, "", "", collectStatusFailed},
		{"没探测过", "", "", "", collectStatusNotReady},
	}
	for _, item := range cases {
		got := NormalizeSlowLogError(item.state, item.code, item.text, true)
		if got.Status != item.want {
			t.Errorf("%s：期望状态 %s，实际 %s（code=%s）", item.name, item.want, got.Status, got.Code)
		}
		if got.Message == "" {
			t.Errorf("%s：错误文案不能为空", item.name)
		}
	}
}

// TestNormalizeSlowLogErrorTextFallback 失败码缺失时靠原始错误文本兜底。
func TestNormalizeSlowLogErrorTextFallback(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"连接超时", "dial tcp 10.0.0.1:5432: i/o timeout", errCodeQueryTimeout},
		{"上下文超时", "context deadline exceeded", errCodeQueryTimeout},
		{"连接被拒", "dial tcp 10.0.0.1:3306: connect: connection refused", errCodeUnreachable},
		{"口令错误", "Access denied for user 'monitor'@'%'", errCodeAuthFailed},
		{"权限不足", "ERROR: permission denied for function pg_current_logfile", errCodePermissionDenied},
		{"ORA-01031", "ORA-01031: insufficient privileges", errCodePermissionDenied},
		{"文件不存在", "open /var/log/pg.log: no such file or directory", errCodeLogFileUnreadable},
	}
	for _, item := range cases {
		got := NormalizeSlowLogError(capabilityProbeFailed, "", item.text, true)
		if got.Code != item.want {
			t.Errorf("%s：期望 %s，实际 %s", item.name, item.want, got.Code)
		}
	}
}

// TestNormalizeSlowLogErrorUnknownTextDoesNotEscalate 文本看不出原因时停在粗分类，
// 绝不向上升级成"实例不可达"这类惊悚结论。
func TestNormalizeSlowLogErrorUnknownTextDoesNotEscalate(t *testing.T) {
	got := NormalizeSlowLogError(capabilityDisabled, "", "服务端参数未满足", true)
	if got.Code != errCodeSlowLogDisabled {
		t.Errorf("不可识别文本应保持状态兜底，实际 %s", got.Code)
	}
}

// TestNormalizeSlowLogErrorRemoteScriptSyntaxError 守住一次真实误报：
// PG 远端脚本生成了空 then 块，bash 报
// `line 8: syntax error near unexpected token 'fi'`，
// 归一化却按 ENGINE_SSH_FAILED 显示成「实例不可达（连接超时或网络异常）」，
// 运维会顺着网络方向白排查半天。平台脚本缺陷必须落到 UNKNOWN 并保留原文。
func TestNormalizeSlowLogErrorRemoteScriptSyntaxError(t *testing.T) {
	text := "读取慢日志状态失败：bash: -c: line 8: syntax error near unexpected token `fi'"
	got := NormalizeSlowLogError(capabilityEnabledFile, failEngineSSHFailed, text, true)
	if got.Code != errCodeUnknown {
		t.Fatalf("脚本语法错误不应归为 %s（网络类），实际 %s", errCodeUnreachable, got.Code)
	}
	if got.Detail != text {
		t.Errorf("原始错误文本必须保留，实际 %q", got.Detail)
	}
}

// TestNormalizeSlowLogErrorEOFFalsePositive 裸 "eof" 子串会误伤普通单词（如 thereof），
// 只有真正的连接 EOF 才应判为不可达。
func TestNormalizeSlowLogErrorEOFFalsePositive(t *testing.T) {
	if got := NormalizeSlowLogError(capabilityProbeFailed, "", "the composition thereof is invalid", true); got.Code == errCodeUnreachable {
		t.Errorf("普通单词 thereof 被误判为连接断开：%s", got.Code)
	}
	if got := NormalizeSlowLogError(capabilityProbeFailed, "", "ssh: handshake failed: EOF", true); got.Code != errCodeUnreachable {
		t.Errorf("真实 EOF 断连应判为 %s，实际 %s", errCodeUnreachable, got.Code)
	}
}

// TestNormalizeSlowLogErrorDisabled 平台侧停用时优先显示"已停用"，
// 而不是把它当成故障报红。
func TestNormalizeSlowLogErrorDisabled(t *testing.T) {
	got := NormalizeSlowLogError(capabilityEnabledFile, failMySQLConnectFailed, "connection refused", false)
	if got.Code != errCodeCollectDisabled || got.Status != collectStatusNotReady {
		t.Errorf("停用实例应归一为 %s/NOT_READY，实际 %s/%s", errCodeCollectDisabled, got.Code, got.Status)
	}
}

// TestNormalizeSlowLogErrorUnknownCodeKeepsRaw 未知失败码不能吞掉，要带进 Detail。
func TestNormalizeSlowLogErrorUnknownCodeKeepsRaw(t *testing.T) {
	got := NormalizeSlowLogError(capabilityProbeFailed, "SOME_NEW_CODE", "", true)
	if got.Code != errCodeUnknown {
		t.Errorf("未知码应落 UNKNOWN，实际 %s", got.Code)
	}
	if got.Detail == "" {
		t.Error("未知码需要把原始码留在 Detail 里，否则无法排查")
	}
}

// TestNormalizeSlowLogParseError 解析失败的明细条目要带结构化原因，
// 但绝不能被过滤掉——这里只验证原因的正确性。
func TestNormalizeSlowLogParseError(t *testing.T) {
	ok := NormalizeSlowLogParseError("PARSED", "")
	if ok.Code != errCodeNone || ok.Status != collectStatusOK {
		t.Errorf("解析成功应无错误，实际 %s/%s", ok.Code, ok.Status)
	}
	failed := NormalizeSlowLogParseError("FAILED", "无法解析 Query_time 字段")
	if failed.Code != errCodeParseFailed || failed.Status != collectStatusFailed {
		t.Errorf("解析失败应归一为 %s/FAILED，实际 %s/%s", errCodeParseFailed, failed.Code, failed.Status)
	}
	if failed.Detail != "无法解析 Query_time 字段" {
		t.Errorf("原始解析错误应保留在 Detail，实际 %q", failed.Detail)
	}
}

// TestSlowLogStatusRank 排序权重：正常在前，失败最后。
// 重点在"失败条目只是排得靠后"，排序逻辑本身不参与过滤。
func TestSlowLogStatusRank(t *testing.T) {
	if slowLogStatusRank(collectStatusOK) >= slowLogStatusRank(collectStatusNotReady) {
		t.Error("正常采集应排在未采集之前")
	}
	if slowLogStatusRank(collectStatusNotReady) >= slowLogStatusRank(collectStatusFailed) {
		t.Error("未采集应排在采集失败之前")
	}
}

// TestStatusFilterMatched 状态筛选不能把"全部"误判成过滤。
func TestStatusFilterMatched(t *testing.T) {
	cases := []struct {
		filter string
		status string
		want   bool
	}{
		{"", collectStatusFailed, true},
		{"all", collectStatusFailed, true},
		{"failed", collectStatusFailed, true},
		{"failed", collectStatusOK, false},
		{"not_ready", collectStatusNotReady, true},
		{"ok", collectStatusOK, true},
	}
	for _, item := range cases {
		if got := statusFilterMatched(item.filter, item.status); got != item.want {
			t.Errorf("filter=%s status=%s 期望 %v，实际 %v", item.filter, item.status, item.want, got)
		}
	}
}
