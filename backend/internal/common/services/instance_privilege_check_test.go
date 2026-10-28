package services

import (
	"strings"
	"testing"
)

// instance_privilege_check_test.go
//
// 这里的断言针对的是"体检结论是否如实反映到用户眼前"。
// 体检本身的价值不在于算对，而在于**不让缺口沉默**。

func TestConnectionSuccessMessageSSHNotConfigured(t *testing.T) {
	report := newConnectionTestReport()
	report.SSHStatus = sshStatusNotConfigured
	msg := connectionSuccessMessage(report)
	if !strings.Contains(msg, "数据库连接正常") {
		t.Fatalf("未配置 SSH 时数据库仍应判定成功：%s", msg)
	}
	if !strings.Contains(msg, "未配置主机 SSH") {
		t.Fatalf("未配置 SSH 的事实必须出现在结论里：%s", msg)
	}
}

func TestConnectionSuccessMessageSSHFailedDoesNotBlockDB(t *testing.T) {
	// 关键回归：SSH 失败曾经让整次连通性检测失败，
	// 导致"只想接入监控"的用户被迫先开通主机 SSH。
	report := newConnectionTestReport()
	report.SSHStatus = sshStatusFailed
	report.SSHMessage = "SSH 登录失败"
	msg := connectionSuccessMessage(report)
	if !strings.Contains(msg, "数据库连接正常") {
		t.Fatalf("SSH 失败不应掩盖数据库可达的事实：%s", msg)
	}
	if !strings.Contains(msg, "SSH 登录失败") || !strings.Contains(msg, "不影响监控采集") {
		t.Fatalf("SSH 失败原因与影响范围都要说清：%s", msg)
	}
}

func TestConnectionSuccessMessageReportsPrivilegeGaps(t *testing.T) {
	report := newConnectionTestReport()
	report.add(PrivilegeCheckItem{Key: "a", Name: "查看全部会话", Status: PrivilegeStatusPass})
	report.add(PrivilegeCheckItem{Key: "b", Name: "读取会话明细", Status: PrivilegeStatusFail})
	report.add(PrivilegeCheckItem{Key: "c", Name: "语句执行统计", Status: PrivilegeStatusFail})
	if report.FailedCount != 2 {
		t.Fatalf("FailedCount = %d, want 2", report.FailedCount)
	}
	msg := connectionSuccessMessage(report)
	if !strings.Contains(msg, "采集权限缺口 2 项") {
		t.Fatalf("缺口数量必须写进结论：%s", msg)
	}
	for _, name := range []string{"读取会话明细", "语句执行统计"} {
		if !strings.Contains(msg, name) {
			t.Fatalf("结论里应列出缺口名称 %q：%s", name, msg)
		}
	}
	if strings.Contains(msg, "查看全部会话（PROCESS）") {
		t.Fatalf("通过项不应占用结论篇幅：%s", msg)
	}
}

func TestAsBool(t *testing.T) {
	// MySQL 驱动把 SUM() 的结果可能以任意形态送达，归约必须稳。
	truthy := []any{[]byte("1"), "1", int64(1), int32(1), float64(1), true, []byte("2"), "100"}
	falsy := []any{nil, []byte("0"), "0", []byte(""), "", int64(0), float64(0), false, []byte("0.0")}
	for _, v := range truthy {
		if !asBool(v) {
			t.Errorf("asBool(%#v) = false, want true", v)
		}
	}
	for _, v := range falsy {
		if asBool(v) {
			t.Errorf("asBool(%#v) = true, want false", v)
		}
	}
}

func TestTrimErrorText(t *testing.T) {
	if got := trimErrorText("short", 10); got != "short" {
		t.Fatalf("短文本不应被改写：%q", got)
	}
	// 省略号是 3 字节 UTF-8 字符，长度断言必须走 rune 计数。
	got := trimErrorText(strings.Repeat("x", 20), 5)
	if got != "xxxxx…" {
		t.Fatalf("超长文本应截断并带省略号：%q", got)
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent(`mon"itor`); got != `"mon""itor"` {
		t.Fatalf("标识符内的双引号必须转义：%q", got)
	}
}
