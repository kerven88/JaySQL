package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/config"
	"github.com/simajiong/jaysql/internal/global"
)

// instance_probe_test.go
//
// 这里锁定的是「后台探活到底挑哪些实例、挑多少、按什么顺序」以及
// 「轻量探活绝不打肿脸冒充完整体检结论」两件事。
//
// 背景：实例页面上那个开关只是浏览器定时器，没人开页面就不探测，
// 而首页「实例连接异常」风险口径直接读 last_check_status。挑选逻辑一旦跑偏，
// 首页数字就会失真 —— 而且是静默失真，所以用测试钉死。

func probeRow(id uint64, status string, checkedAgo time.Duration) probeCandidate {
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)
	return probeCandidate{
		ID:            id,
		Status:        status,
		LastCheckedAt: base.Add(-checkedAgo),
	}
}

var probeBaseNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)

func TestSelectDueProbeCandidatesTieredInterval(t *testing.T) {
	const (
		errInterval = 60 * time.Second
		okInterval  = 600 * time.Second
	)

	cases := []struct {
		name string
		rows []probeCandidate
		want bool
	}{
		{"空列表不选", nil, false},
		{"异常实例未到期（30s < 60s）", []probeCandidate{probeRow(1, "error", 30*time.Second)}, false},
		{"异常实例到期（61s >= 60s）", []probeCandidate{probeRow(1, "error", 61*time.Second)}, true},
		{"待检测（unknown）按异常档", []probeCandidate{probeRow(2, "unknown", 61*time.Second)}, true},
		{"状态为空按异常档", []probeCandidate{probeRow(3, "", 61*time.Second)}, true},
		{"可用实例未到期（300s < 600s）", []probeCandidate{probeRow(4, "success", 300*time.Second)}, false},
		{"可用实例到期（601s >= 600s）", []probeCandidate{probeRow(4, "success", 601*time.Second)}, true},
		{"状态大小写与空格容错（SUCCESS）", []probeCandidate{probeRow(5, " SUCCESS ", 300*time.Second)}, false},
		// 零值 last_checked_at = 新增实例从未检测过，不受间隔约束，立即探测。
		{"从未检测过（零值）立即探测", []probeCandidate{{ID: 6, Status: "success"}}, true},
		{"从未检测过的异常实例也立即探测", []probeCandidate{{ID: 7, Status: "error"}}, true},
		{"ID 为 0 跳过", []probeCandidate{{ID: 0, Status: "error"}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := selectDueProbeCandidates(tc.rows, probeBaseNow, errInterval, okInterval, 30)
			if got := len(ids) == 1; got != tc.want {
				t.Fatalf("期望入选=%v，实际 ids=%v", tc.want, ids)
			}
		})
	}
}

func TestSelectDueProbeCandidatesPriorityAndLimit(t *testing.T) {
	const (
		errInterval = 60 * time.Second
		okInterval  = 600 * time.Second
	)

	rows := []probeCandidate{
		probeRow(10, "error", 70*time.Second),    // 最久未测（同样异常档）
		probeRow(11, "error", 90*time.Second),    // 比 10 更久
		probeRow(12, "success", 601*time.Second), // 刚到期
		{ID: 13, Status: "unknown"},              // 从未检测，最优先
		probeRow(14, "error", 30*time.Second),    // 未到期
	}

	ids := selectDueProbeCandidates(rows, probeBaseNow, errInterval, okInterval, 3)
	if len(ids) != 3 {
		t.Fatalf("应按 limit 截断为 3 条，实际 %v", ids)
	}
	// 期望顺序：从未检测(13) → 异常档里最久未测(11, 90s) → 异常档次久(10, 70s)
	// 注意 12（可用档、601s 未测）虽然更"久"，但异常档优先 —— 单轮限流时要先补测
	// 首页风险口径关心的那批，所以它被挤出本轮。
	wantOrder := []uint64{13, 11, 10}
	for i, want := range wantOrder {
		if ids[i] != want {
			t.Fatalf("第 %d 位应为 %d，实际顺序 %v", i, want, ids)
		}
	}
}

func TestSelectDueProbeCandidatesNonPositiveLimit(t *testing.T) {
	rows := []probeCandidate{probeRow(1, "error", 100*time.Second)}
	if ids := selectDueProbeCandidates(rows, probeBaseNow, time.Minute, 10*time.Minute, 0); ids != nil {
		t.Fatalf("limit<=0 应返回 nil，实际 %v", ids)
	}
}

// 关键回归：轻量探活没有跑 SSH 与权限体检，它的成功摘要**不能**复用完整体检的文案，
// 否则会把"未配置主机 SSH"这类假结论写进 last_check_message，覆盖用户真实体检结果。
func TestConnectionSuccessMessageForModeLightweight(t *testing.T) {
	report := newConnectionTestReport()
	report.SSHStatus = sshStatusNotConfigured

	light := connectionSuccessMessageForMode(report, ProbeModeLightweight)
	if !strings.Contains(light, "数据库连接正常") {
		t.Fatalf("轻量探活摘要应说明数据库连接正常：%s", light)
	}
	if strings.Contains(light, "主机 SSH") {
		t.Fatalf("轻量探活未跑 SSH 体检，不得输出 SSH 结论：%s", light)
	}
	if strings.Contains(light, "采集权限") {
		t.Fatalf("轻量探活未跑权限体检，不得输出权限结论：%s", light)
	}

	full := connectionSuccessMessageForMode(report, ProbeModeFull)
	if full != connectionSuccessMessage(report) {
		t.Fatalf("full 模式必须与既有完整摘要逐字节一致：%s", full)
	}
	if !strings.Contains(full, "未配置主机 SSH") {
		t.Fatalf("full 模式仍应给出 SSH 提示：%s", full)
	}
}

// 轻量探活依然必须真实校验连通性：端口没人监听时返回"连接失败：..."。
func TestTestConnectionModeLightweightChecksConnectivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	_, err := testConnectionMode(ctx, ProbeModeLightweight, "mysql", "127.0.0.1", 1, "probe_user", "probe_pwd", nil)
	if err == nil {
		t.Fatal("端口未监听时应当返回连接失败")
	}
	if !strings.Contains(err.Error(), "连接失败") {
		t.Fatalf("错误文案应与手动测试口径一致：%v", err)
	}
	// 连不上必须快速失败，不能被外围体检拖满：这条断言守住"轻量"二字的成本含义。
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("轻量探活失败路径耗时过长（%s），疑似仍在跑外围体检", elapsed)
	}
}

func TestResolveInstanceProbeConfigDefaultsAndOverride(t *testing.T) {
	restore := global.App.Config
	defer func() { global.App.Config = restore }()

	global.App.Config = config.Configuration{}
	defaults := resolveInstanceProbeConfig()
	if defaults.errorInterval != time.Duration(defaultInstanceProbeErrorIntervalSec)*time.Second {
		t.Fatalf("异常档默认值应为 %ds，实际 %s", defaultInstanceProbeErrorIntervalSec, defaults.errorInterval)
	}
	if defaults.okInterval != time.Duration(defaultInstanceProbeOKIntervalSec)*time.Second {
		t.Fatalf("可用档默认值应为 %ds，实际 %s", defaultInstanceProbeOKIntervalSec, defaults.okInterval)
	}
	if defaults.batchSize != defaultInstanceProbeBatchSize {
		t.Fatalf("单轮上限默认值应为 %d，实际 %d", defaultInstanceProbeBatchSize, defaults.batchSize)
	}

	global.App.Config = config.Configuration{Crontab: config.Crontab{
		InstanceProbeErrorInterval: 15,
		InstanceProbeOKInterval:    120,
		InstanceProbeBatchSize:     5,
	}}
	overridden := resolveInstanceProbeConfig()
	if overridden.errorInterval != 15*time.Second || overridden.okInterval != 120*time.Second || overridden.batchSize != 5 {
		t.Fatalf("配置覆盖未生效：%+v", overridden)
	}
}
