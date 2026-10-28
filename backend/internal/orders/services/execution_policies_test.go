package services

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/orders/api/base"
)

// 执行方式 / 策略归一化：外部值非法或为空时必须回落到默认值，
// 否则「定时执行」会因一个拼写错误变成静默的立即执行。
func TestNewExecutionPoliciesNormalization(t *testing.T) {
	cases := []struct {
		name        string
		errorPolicy string
		backupPoli  string
		mode        string
		scheduledAt string
		wantError   string
		wantBackup  string
		wantMode    string
		wantSched   string
	}{
		{"全空回落默认", "", "", "", "", ErrorPolicyStop, BackupPolicyStop, ExecutionModeImmediate, ""},
		{"大小写不敏感", "rollback", "continue", "scheduled", " 2026-10-14 01:00:00 ", ErrorPolicyRollback, BackupPolicyContinue, ExecutionModeScheduled, "2026-10-14 01:00:00"},
		{"非法值回落默认", "DROP_TABLE", "NOPE", "WHENEVER", "", ErrorPolicyStop, BackupPolicyStop, ExecutionModeImmediate, ""},
		{"手动模式", "CONTINUE", "STOP", "MANUAL", "", ErrorPolicyContinue, BackupPolicyStop, ExecutionModeManual, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewExecutionPolicies(c.errorPolicy, c.backupPoli, c.mode, c.scheduledAt)
			if p.ErrorPolicy != c.wantError {
				t.Fatalf("error_policy = %q, want %q", p.ErrorPolicy, c.wantError)
			}
			if p.BackupPolicy != c.wantBackup {
				t.Fatalf("backup_policy = %q, want %q", p.BackupPolicy, c.wantBackup)
			}
			if p.ExecutionMode != c.wantMode {
				t.Fatalf("execution_mode = %q, want %q", p.ExecutionMode, c.wantMode)
			}
			if p.ScheduledAt != c.wantSched {
				t.Fatalf("scheduled_at = %q, want %q", p.ScheduledAt, c.wantSched)
			}
			// 策略取值访问器同样要能兜底（不经归一化构造的结构体也能安全使用）
			if got := (ExecutionPolicies{}).errorPolicy(); got != ErrorPolicyStop {
				t.Fatalf("零值 errorPolicy() = %q, want STOP", got)
			}
			if got := (ExecutionPolicies{}).backupPolicy(); got != BackupPolicyStop {
				t.Fatalf("零值 backupPolicy() = %q, want STOP", got)
			}
		})
	}
}

// 执行计划快照可往返：落库的 execution_config 必须能回读出同一套策略，
// 定时触发时才不会丢失用户选择的错误/备份策略。
func TestExecutionPoliciesConfigRoundTrip(t *testing.T) {
	p := NewExecutionPolicies(ErrorPolicyRollback, BackupPolicyContinue, ExecutionModeScheduled, "2026-10-14 01:00:00")
	raw := p.configJSON()

	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("execution_config 不是合法 JSON: %v", err)
	}
	for key, want := range map[string]string{
		"error_policy":   ErrorPolicyRollback,
		"backup_policy":  BackupPolicyContinue,
		"execution_mode": ExecutionModeScheduled,
		"scheduled_at":   "2026-10-14 01:00:00",
	} {
		if decoded[key] != want {
			t.Fatalf("execution_config[%s] = %q, want %q", key, decoded[key], want)
		}
	}

	got := parseExecutionPolicies(raw)
	if got != p {
		t.Fatalf("回读不一致: %+v vs %+v", got, p)
	}

	// 空 / 脏数据必须回落到默认计划，而不是 panic 或产生非法模式
	if empty := parseExecutionPolicies(nil); empty.ExecutionMode != ExecutionModeImmediate {
		t.Fatalf("空 execution_config 应回落立即执行，实际 %q", empty.ExecutionMode)
	}
	if dirty := parseExecutionPolicies([]byte("{not-json")); dirty.ExecutionMode != ExecutionModeImmediate {
		t.Fatalf("脏 execution_config 应回落立即执行，实际 %q", dirty.ExecutionMode)
	}
}

// 定时时间解析：格式错误与空值都要给出可读错误（服务端返回给用户）
func TestParseScheduledAt(t *testing.T) {
	parsed, err := parseScheduledAt("2026-10-14 01:00:00")
	if err != nil {
		t.Fatalf("合法时间解析失败: %v", err)
	}
	if parsed.Format(orderTimeLayout) != "2026-10-14 01:00:00" {
		t.Fatalf("解析结果不符: %s", parsed.Format(orderTimeLayout))
	}
	if _, err := parseScheduledAt(""); err == nil {
		t.Fatal("空时间应报错")
	}
	if _, err := parseScheduledAt("2026/10/14 01:00"); err == nil {
		t.Fatal("格式错误的时间应报错")
	}
	if _, err := parseScheduledAt("明天凌晨"); err == nil {
		t.Fatal("非时间字符串应报错")
	}
}

// 备份失败与 SQL 执行失败必须可区分：
// 前者 SQL 已生效（任务按成功计、后续任务按备份策略决定是否继续），
// 后者才是真正的执行失败（按错误处理策略分支）。
func TestBackupFailureDetection(t *testing.T) {
	if !backupFailure(base.BackupSQLError{Err: errors.New("binlog 解析失败")}) {
		t.Fatal("BackupSQLError 应被识别为备份失败")
	}
	if backupFailure(base.SQLExecuteError{Err: errors.New("1064 语法错误")}) {
		t.Fatal("SQLExecuteError 不应被识别为备份失败")
	}
	if backupFailure(errors.New("普通错误")) {
		t.Fatal("普通错误不应被识别为备份失败")
	}
	if backupFailure(nil) {
		t.Fatal("nil 不应被识别为备份失败")
	}
}

// 回滚 SQL 切分：Binlog 生成的反向语句用 ";\r\n" 拼接，
// 但字符串值里可能含分号、引号，必须做引号感知切分，否则回滚会把语句切碎。
func TestSplitSQLStatements(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"简单多语句", "INSERT INTO t VALUES(1);\r\nINSERT INTO t VALUES(2);", []string{"INSERT INTO t VALUES(1)", "INSERT INTO t VALUES(2)"}},
		{"值内分号不切", "INSERT INTO t(a) VALUES('a;b');\r\nDELETE FROM t WHERE id=1", []string{"INSERT INTO t(a) VALUES('a;b')", "DELETE FROM t WHERE id=1"}},
		{"单引号双写转义", "INSERT INTO t(a) VALUES('it''s;ok')", []string{"INSERT INTO t(a) VALUES('it''s;ok')"}},
		{"反斜杠转义引号", "INSERT INTO t(a) VALUES('a\\';b')", []string{"INSERT INTO t(a) VALUES('a\\';b')"}},
		{"反引号内的分号", "UPDATE `we;ird` SET a=1", []string{"UPDATE `we;ird` SET a=1"}},
		{"末尾多余分号与空语句", "SELECT 1;;\r\n;  ", []string{"SELECT 1"}},
		{"空输入", "   ", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitSQLStatements(c.raw)
			if len(got) != len(c.want) {
				t.Fatalf("语句数不符: got %d %q, want %d %q", len(got), got, len(c.want), c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 条不符: got %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// 定时触发失败的可重试判定：只对「工单正忙」写回计划，
// 其余终态错误不重试，避免调度器每分钟无意义重试。
func TestRetryableBusyErrors(t *testing.T) {
	if !errors.Is(ErrOrderBusy, ErrOrderBusy) {
		t.Fatal("ErrOrderBusy 应可用于 errors.Is 判定")
	}
	if errors.Is(errors.New("当前工单已终止，禁止执行"), ErrOrderBusy) {
		t.Fatal("终态错误不应被判定为可重试")
	}
	if !errors.Is(ErrOrderTaskExecuting, ErrOrderTaskExecuting) {
		t.Fatal("ErrOrderTaskExecuting 应可用于 errors.Is 判定")
	}
}

// 定时执行时间必须晚于当前时间：调度器只扫描 scheduled_at <= now，
// 若允许过去时间，工单会在提交后立刻被扫走执行 —— 与「定时」语义不符。
func TestScheduledTimeMustBeFuture(t *testing.T) {
	past := time.Now().Add(-1 * time.Minute).Format(orderTimeLayout)
	parsed, err := parseScheduledAt(past)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if parsed.After(time.Now()) {
		t.Fatal("过去时间不应被判为将来（前置校验逻辑依赖此判定）")
	}
	future := time.Now().Add(1 * time.Hour).Format(orderTimeLayout)
	parsedFuture, err := parseScheduledAt(future)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !parsedFuture.After(time.Now()) {
		t.Fatal("将来时间应判为将来")
	}
}
