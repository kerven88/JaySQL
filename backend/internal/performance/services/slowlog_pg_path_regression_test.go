package services

import "testing"

// TestPGPathUnresolvedIsNotFileMissing 守住错误语义的边界：
//
//	「拿不到路径」≠「文件缺失」。
//	前者是 pg_monitor 角色 / log_directory 这类配置问题（NOT_READY，黄色提示），
//	后者才是真故障（FAILED，红色）。混为一谈会让用户对着一个天天在写的文件查权限。
func TestPGPathUnresolvedIsNotFileMissing(t *testing.T) {
	view := NormalizeSlowLogError("", failPGLogPathUnresolved, "未能定位 PG 当前日志文件", true)
	if view.Code != errCodeParamIncompatible {
		t.Fatalf("error_code = %q, want %q", view.Code, errCodeParamIncompatible)
	}
	if view.Status != collectStatusNotReady {
		t.Fatalf("collect_status = %q, want %q", view.Status, collectStatusNotReady)
	}
	if view.Tone != errorToneWarning {
		t.Fatalf("tone = %q, want %q", view.Tone, errorToneWarning)
	}

	// 真·文件缺失仍应报故障红字
	missing := NormalizeSlowLogError(capabilityFileMissing, failEngineFileMissing, "slow log not found", true)
	if missing.Code != errCodeLogFileUnreadable || missing.Status != collectStatusFailed {
		t.Fatalf("文件缺失语义被改动：code=%q status=%q", missing.Code, missing.Status)
	}
}
