package services

import (
	"strings"
	"testing"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
)

// ============================================================================
// 升级链与聚合窗口的单测
//
// 全部是纯函数测试：判定逻辑（decideEscalation / escalationNextTarget /
// escalationChainHasCycle / 聚合键解析 / 渲染）都不碰数据库，
// 因此不需要测试库就能覆盖完整状态矩阵 —— 这也是把决策从调度器里抽出来的原因。
// ============================================================================

func escalationRowFixture(level int, anchor time.Time) monitorModels.InsightMonitorAlertEscalations {
	return monitorModels.InsightMonitorAlertEscalations{
		Model:        &commonModels.Model{ID: uint64(level)},
		EventID:      100,
		Fingerprint:  "inst01|cpu",
		InstanceID:   "inst01",
		RootPolicyID: 1,
		Level:        level,
		FromPolicyID: 1,
		PolicyID:     2,
		AnchorAt:     commonModels.LocalTime(anchor),
		Status:       monitorModels.EscalationStatusPending,
	}
}

func policyFixture(id uint64, escalateAfter int, escalateTo uint64) monitorModels.InsightMonitorNotifyPolicies {
	return monitorModels.InsightMonitorNotifyPolicies{
		Model:                &commonModels.Model{ID: id},
		Name:                 "策略",
		Enabled:              true,
		EscalateAfterMinutes: escalateAfter,
		EscalatePolicyID:     escalateTo,
		StopOnAck:            true,
	}
}

func eventFixture(status string, ignored bool) monitorModels.InsightMonitorAlertEvents {
	return monitorModels.InsightMonitorAlertEvents{
		Model:       &commonModels.Model{ID: 100},
		InstanceID:  "inst01",
		Fingerprint: "inst01|cpu",
		Status:      status,
		Ignored:     ignored,
	}
}

// ---------- 状态流转矩阵 ----------

func TestDecideEscalationStatusMatrix(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	anchor := now.Add(-10 * time.Minute) // 已活跃 10 分钟

	from := policyFixture(1, 5, 2)
	to := policyFixture(2, 0, 0)
	fromPtr := &from
	toPtr := &to

	cases := []struct {
		name    string
		status  string
		event   *monitorModels.InsightMonitorAlertEvents
		from    *monitorModels.InsightMonitorNotifyPolicies
		to      *monitorModels.InsightMonitorNotifyPolicies
		wantAct string
		wantIn  string
	}{
		{"未到期→等待", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", false)
			return &e
		}(), func() *monitorModels.InsightMonitorNotifyPolicies {
			p := policyFixture(1, 30, 2) // 30 分钟，才过 10 分钟
			return &p
		}(), toPtr, escalationActionWait, "未到升级时间"},
		{"已超时→升级", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", false)
			return &e
		}(), fromPtr, toPtr, escalationActionEscalate, "已超时未处理"},
		{"认领且开启停止升级→取消", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("acknowledged", false)
			return &e
		}(), fromPtr, toPtr, escalationActionCancel, "人工认领"},
		{"认领但关闭停止升级→继续升级", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("acknowledged", false)
			return &e
		}(), func() *monitorModels.InsightMonitorNotifyPolicies {
			p := policyFixture(1, 5, 2)
			p.StopOnAck = false
			return &p
		}(), toPtr, escalationActionEscalate, "已超时未处理"},
		{"已恢复→取消", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("recovered", false)
			return &e
		}(), fromPtr, toPtr, escalationActionCancel, "已恢复"},
		{"已忽略→取消", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", true)
			return &e
		}(), fromPtr, toPtr, escalationActionCancel, "人工忽略"},
		{"事件已消失→取消", monitorModels.EscalationStatusPending, nil, fromPtr, toPtr, escalationActionCancel, "已不存在"},
		{"等待时间改为0→取消", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", false)
			return &e
		}(), func() *monitorModels.InsightMonitorNotifyPolicies {
			p := policyFixture(1, 0, 2)
			return &p
		}(), toPtr, escalationActionCancel, "不启用"},
		{"目标策略删除→取消", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", false)
			return &e
		}(), fromPtr, nil, escalationActionCancel, "目标策略"},
		{"目标策略停用→取消", monitorModels.EscalationStatusPending, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", false)
			return &e
		}(), fromPtr, func() *monitorModels.InsightMonitorNotifyPolicies {
			p := policyFixture(2, 0, 0)
			p.Enabled = false
			return &p
		}(), escalationActionCancel, "已停用"},
		{"非待升级态→跳过", monitorModels.EscalationStatusEscalated, func() *monitorModels.InsightMonitorAlertEvents {
			e := eventFixture("active", false)
			return &e
		}(), fromPtr, toPtr, escalationActionSkip, "已结束"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := escalationRowFixture(1, anchor)
			row.Status = tc.status
			got := decideEscalation(row, now, tc.event, tc.from, tc.to)
			if got.Action != tc.wantAct {
				t.Fatalf("动作不符：want=%s got=%s（原因：%s）", tc.wantAct, got.Action, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantIn) {
				t.Fatalf("原因文案不符：want 包含「%s」，got「%s」", tc.wantIn, got.Reason)
			}
		})
	}
}

// 失败退避不能被"按配置重算"覆盖掉：否则重试会退化成每 30 秒打一次。
func TestEscalationRetryBackoffSurvivesConfigRecompute(t *testing.T) {
	anchor := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	now := anchor.Add(20 * time.Minute) // 早已超过 5 分钟的升级等待

	row := escalationRowFixture(1, anchor)
	row.Attempt = 1
	backoff := now.Add(2 * time.Minute) // 上次失败，退避到 2 分钟后
	row.DueAt = (*commonModels.LocalTime)(&backoff)

	got := decideEscalation(row, now, &monitorModels.InsightMonitorAlertEvents{Status: "active"},
		ptrPolicy(policyFixture(1, 5, 2)), ptrPolicy(policyFixture(2, 0, 0)))
	if got.Action != escalationActionWait {
		t.Fatalf("退避期内应继续等待，got=%s", got.Action)
	}
	if !got.DueAt.Equal(backoff) {
		t.Fatalf("退避时间被覆盖：want=%v got=%v", backoff, got.DueAt)
	}

	// 退避到期后立刻恢复升级
	got = decideEscalation(row, backoff.Add(time.Second), &monitorModels.InsightMonitorAlertEvents{Status: "active"},
		ptrPolicy(policyFixture(1, 5, 2)), ptrPolicy(policyFixture(2, 0, 0)))
	if got.Action != escalationActionEscalate {
		t.Fatalf("退避结束后应继续升级，got=%s", got.Action)
	}
}

// 配置变更实时生效：due_at 由"当前配置"算出，改配置的下一轮立即对齐。
func TestEscalationDueAtFollowsConfigChange(t *testing.T) {
	anchor := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	now := anchor.Add(6 * time.Minute)

	// 原配置 30 分钟：6 分钟时远远没到期
	row := escalationRowFixture(1, anchor)
	got := decideEscalation(row, now, &monitorModels.InsightMonitorAlertEvents{Status: "active"},
		ptrPolicy(policyFixture(1, 30, 2)), ptrPolicy(policyFixture(2, 0, 0)))
	if got.Action != escalationActionWait {
		t.Fatalf("30 分钟配置下应等待，got=%s", got.Action)
	}
	if !got.DueAt.Equal(anchor.Add(30 * time.Minute)) {
		t.Fatalf("到期时间不符：want=%v got=%v", anchor.Add(30*time.Minute), got.DueAt)
	}

	// 运维把「升级等待」改成 5 分钟：同一时刻立刻变为"已到期"
	got = decideEscalation(row, now, &monitorModels.InsightMonitorAlertEvents{Status: "active"},
		ptrPolicy(policyFixture(1, 5, 2)), ptrPolicy(policyFixture(2, 0, 0)))
	if got.Action != escalationActionEscalate {
		t.Fatalf("改成 5 分钟后应立即升级，got=%s", got.Action)
	}
	// 改成 60 分钟：又变回等待（改大立即生效，不会按旧倒计时提前触发）
	got = decideEscalation(row, now, &monitorModels.InsightMonitorAlertEvents{Status: "active"},
		ptrPolicy(policyFixture(1, 60, 2)), ptrPolicy(policyFixture(2, 0, 0)))
	if got.Action != escalationActionWait {
		t.Fatalf("改成 60 分钟后应继续等待，got=%s", got.Action)
	}
	if !got.DueAt.Equal(anchor.Add(60 * time.Minute)) {
		t.Fatalf("到期时间未跟随配置：want=%v got=%v", anchor.Add(60*time.Minute), got.DueAt)
	}
}

func ptrPolicy(p monitorModels.InsightMonitorNotifyPolicies) *monitorModels.InsightMonitorNotifyPolicies {
	return &p
}

// ---------- 升级链递进 ----------

func TestEscalationNextTargetChain(t *testing.T) {
	// 正常多级递进：A(5min)→B(10min)→C
	a := policyFixture(1, 5, 2)
	b := policyFixture(2, 10, 3)
	c := policyFixture(3, 0, 0)

	next, reason := escalationNextTarget(a, 1, map[uint64]bool{1: true})
	if next != 2 || reason != "" {
		t.Fatalf("A 的下一级应为 B：got=%d reason=%s", next, reason)
	}
	next, reason = escalationNextTarget(b, 2, map[uint64]bool{1: true, 2: true})
	if next != 3 || reason != "" {
		t.Fatalf("B 的下一级应为 C：got=%d reason=%s", next, reason)
	}
	// C 是链尾：没配下一级，正常终止
	next, reason = escalationNextTarget(c, 3, map[uint64]bool{1: true, 2: true, 3: true})
	if next != 0 || reason == "" {
		t.Fatalf("C 应终止升级链：got=%d reason=%q", next, reason)
	}

	// 自环
	selfLoop := policyFixture(4, 5, 4)
	if next, _ := escalationNextTarget(selfLoop, 1, map[uint64]bool{4: true}); next != 0 {
		t.Fatalf("自环应被拦下：got=%d", next)
	}
	// 环：A→B→A（B 的目标 A 已在链上）
	backToA := policyFixture(2, 5, 1)
	next, reason = escalationNextTarget(backToA, 2, map[uint64]bool{1: true, 2: true})
	if next != 0 || !strings.Contains(reason, "循环") {
		t.Fatalf("环路应被拦下：got=%d reason=%s", next, reason)
	}
	// 深度上限：第 maxEscalationDepth 级之后不再往下
	deep := policyFixture(9, 5, 10)
	next, reason = escalationNextTarget(deep, maxEscalationDepth, map[uint64]bool{9: true})
	if next != 0 || !strings.Contains(reason, "最大深度") {
		t.Fatalf("应受深度上限约束：got=%d reason=%s", next, reason)
	}
	// 未配置等待时间：链到此终止（不是错误）
	noWait := policyFixture(11, 0, 12)
	if next, _ := escalationNextTarget(noWait, 1, map[uint64]bool{11: true}); next != 0 {
		t.Fatalf("未配置升级等待应终止：got=%d", next)
	}
}

// ---------- 升级链环路校验（保存时） ----------

func TestEscalationChainHasCycle(t *testing.T) {
	next := map[uint64]uint64{1: 2, 2: 3, 3: 0}

	if !escalationChainHasCycle(1, 1, next) {
		t.Fatal("自环应判定为环")
	}
	if escalationChainHasCycle(1, 2, next) {
		t.Fatal("A→B 不成环（B 后面是 C，C 后面没了）")
	}
	// 把 C 指回 A：A→B→C→A
	next[3] = 1
	if !escalationChainHasCycle(1, 2, next) {
		t.Fatal("A→B→C→A 应判定为环")
	}
	// 环不含起始策略：B→C→B，从 A 出发配置 A→B 时仍然成环（链会兜圈子）
	next[3] = 2
	if !escalationChainHasCycle(1, 2, next) {
		t.Fatal("B→C→B 也应判定为环（链上兜圈子）")
	}
}

func TestValidateEscalationChainRejectsSelfLoop(t *testing.T) {
	// 自环不需要查库就能拦下（global.App.DB 为 nil 时也会校验）
	if err := validateEscalationChain(7, 7); err == nil || !strings.Contains(err.Error(), "自身") {
		t.Fatalf("自环应被拦下：%v", err)
	}
	if err := validateEscalationChain(7, 0); err != nil {
		t.Fatalf("不配置升级目标时不应报错：%v", err)
	}
}

// ---------- 聚合窗口 ----------

func TestAggregateBucketAlignsWindow(t *testing.T) {
	window := 300
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	start := aggregateWindowStart(aggregateBucket(base, window), window)

	// 同一窗口内的任意时刻桶号相同 —— 这是"窗口内多次评估幂等"的根据
	for _, offset := range []int{0, 1, 99, 299} {
		got := aggregateBucket(base.Add(time.Duration(offset)*time.Second), window)
		if got != aggregateBucket(base, window) {
			t.Fatalf("偏移 %d 秒后桶号应不变：want=%d got=%d", offset, aggregateBucket(base, window), got)
		}
	}
	// 跨过窗口边界必须换桶
	if aggregateBucket(base.Add(time.Duration(window)*time.Second), window) == aggregateBucket(base, window) {
		t.Fatal("跨窗口边界应换桶")
	}
	if !start.Equal(base) {
		t.Fatalf("窗口起点应对齐到桶边界：want=%v got=%v", base, start)
	}
	// 窗口为 0（不聚合）时不产生有效桶
	if aggregateBucket(base, 0) != 0 {
		t.Fatal("未配置聚合窗口时桶号应为 0")
	}
}

func TestAggregateKeyRoundTrip(t *testing.T) {
	trace := aggregateTraceKey(12, "inst01", 987654, 100)
	policyID, instanceID, bucket, tail, ok := parseAggregateKey(trace)
	if !ok || policyID != 12 || instanceID != "inst01" || bucket != 987654 || tail != "100" {
		t.Fatalf("留痕键解析失败：%v %d %s %d %s", ok, policyID, instanceID, bucket, tail)
	}

	digest := aggregateDigestKey(12, "inst01", 987654, 3, "user", "dba@example.com")
	policyID, instanceID, bucket, tail, ok = parseAggregateKey(digest)
	if !ok || policyID != 12 || instanceID != "inst01" || bucket != 987654 || tail != "3|user|dba@example.com" {
		t.Fatalf("汇总键解析失败：%v %d %s %d %q", ok, policyID, instanceID, bucket, tail)
	}

	// 两类键的前缀必须能区分：汇总不能被当成留痕统计进去，反之亦然
	if strings.HasPrefix(digest, aggregateTracePrefix) {
		t.Fatal("汇总键不应带留痕前缀")
	}
	if strings.HasPrefix(trace, aggregateDigestPrefix) {
		t.Fatal("留痕键不应带汇总前缀")
	}
	// 非聚合键（常规去重键）必须解析失败
	if _, _, _, _, ok := parseAggregateKey("100|12|3|user|a@b.c|firing|"); ok {
		t.Fatal("常规去重键不应被解析为聚合键")
	}
}

// ---------- 渲染 ----------

func TestRenderEscalationPayloadMarksLevel(t *testing.T) {
	event := monitorModels.InsightMonitorAlertEvents{
		Model:       &commonModels.Model{ID: 100},
		Name:        "CPU 使用率 高 >= 90",
		Level:       AlertLevelP1,
		DurationSec: 900,
	}
	instance := notifyInstanceContext{InstanceID: "inst01", Name: "db-01:3306", DbType: "MySQL"}
	payload := renderEscalationPayload(event, instance, 2, "基础策略", "值班经理", notifier.Mentions{}, nil, "")

	for _, want := range []string{"升级", "第 2 级", "基础策略", "值班经理"} {
		if !strings.Contains(payload.Title+payload.Markdown, want) {
			t.Fatalf("升级通知缺少关键信息「%s」：%s", want, payload.Title)
		}
	}
	if payload.Meta["escalation_level"] != "2" {
		t.Fatalf("meta 未记录升级级别：%v", payload.Meta)
	}
	if payload.Meta["trigger"] != NotifyTriggerEscalated {
		t.Fatalf("meta 的 trigger 不符：%v", payload.Meta)
	}
}

func TestRenderAggregatePayloadSortsAndTruncates(t *testing.T) {
	policy := policyFixture(1, 30, 0)
	policy.AggregateWindowSeconds = 300
	instance := notifyInstanceContext{InstanceID: "inst01", Name: "db-01:3306"}

	events := []monitorModels.InsightMonitorAlertEvents{
		{Level: AlertLevelP3, Name: "慢查询增长", ObservedValue: 12},
		{Level: AlertLevelP0, Name: "主库不可用", ObservedValue: 1},
	}
	payload := renderAggregatePayload(policy, instance, events, 300, notifier.Mentions{}, nil, "")
	// 严重的排前面
	if strings.Index(payload.PlainText, "主库不可用") > strings.Index(payload.PlainText, "慢查询增长") {
		t.Fatalf("汇总应按严重级别排序：\n%s", payload.PlainText)
	}
	if payload.Meta["aggregated_alerts"] != "2" {
		t.Fatalf("meta 未记录合并条数：%v", payload.Meta)
	}

	// 超过 10 条时截断并给出剩余数量
	many := make([]monitorModels.InsightMonitorAlertEvents, 0, 13)
	for i := 0; i < 13; i++ {
		many = append(many, monitorModels.InsightMonitorAlertEvents{Level: AlertLevelP2, Name: "指标告警", ObservedValue: float64(i)})
	}
	payload = renderAggregatePayload(policy, instance, many, 300, notifier.Mentions{}, nil, "")
	if !strings.Contains(payload.PlainText, "其余 3 条") {
		t.Fatalf("超过 10 条应提示剩余数量：\n%s", payload.PlainText)
	}
}
