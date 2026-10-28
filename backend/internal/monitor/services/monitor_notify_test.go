package services

import (
	"strings"
	"testing"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
	"gorm.io/datatypes"
)

// ---------- 策略匹配矩阵 ----------

func TestNotifyPolicyMatchesMatrix(t *testing.T) {
	base := monitorModels.InsightMonitorNotifyPolicies{
		Enabled: true,
	}
	event := notifyMatchContext{
		Level:           AlertLevelP1,
		DbType:          "MySQL",
		Business:        "支付中心",
		Environment:     "生产",
		InstanceID:      "abc123",
		Layer:           LayerEngine,
		AlertTemplateID: 7,
	}

	if !notifyPolicyMatches(base, event) {
		t.Fatal("空条件策略应匹配任意告警（空数组 = 不限）")
	}

	cases := []struct {
		name   string
		mutate func(*monitorModels.InsightMonitorNotifyPolicies)
		want   bool
	}{
		{"级别命中", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchLevels = jsonList("P0", "P1")
		}, true},
		{"级别未命中", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchLevels = jsonList("P0")
		}, false},
		{"中文旧级别等价于 P1", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchLevels = jsonList("高")
		}, true},
		{"数据库类型大小写无关", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchDbTypes = jsonList("mysql")
		}, true},
		{"业务精确匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchBusiness = jsonList("支付中心")
		}, true},
		{"业务不匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchBusiness = jsonList("订单中心")
		}, false},
		{"环境匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchEnvs = jsonList("生产", "预发")
		}, true},
		{"实例匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchInstances = jsonList("abc123")
		}, true},
		{"层级匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchLayers = jsonList(LayerEngine)
		}, true},
		{"层级不匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchLayers = jsonList(LayerKernel)
		}, false},
		{"模板匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchTemplateIDs = jsonUintList(7)
		}, true},
		{"模板不匹配", func(p *monitorModels.InsightMonitorNotifyPolicies) {
			p.MatchTemplateIDs = jsonUintList(9)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := base
			tc.mutate(&policy)
			if got := notifyPolicyMatches(policy, event); got != tc.want {
				t.Fatalf("匹配结果不符：want=%v got=%v", tc.want, got)
			}
		})
	}

	// 停用策略一律不匹配（哪怕条件全中）
	disabled := base
	disabled.Enabled = false
	if notifyPolicyMatches(disabled, event) {
		t.Fatal("停用策略不应命中")
	}
}

// 层级判定不出来时按"不匹配"处理：宁可漏发，也不能把只该发核心库的告警发给测试库。
func TestNotifyPolicyLayerUnknownNotMatched(t *testing.T) {
	policy := monitorModels.InsightMonitorNotifyPolicies{
		Enabled:     true,
		MatchLayers: jsonList(LayerEngine),
	}
	event := notifyMatchContext{Level: AlertLevelP2, DbType: "MySQL", Layer: ""}
	if notifyPolicyMatches(policy, event) {
		t.Fatal("层级未知时不应命中限定层级的策略")
	}
}

func TestNormalizeNotifyLevels(t *testing.T) {
	got := normalizeNotifyLevels([]string{"低", "p0", "P0", " 高 ", ""})
	want := []string{AlertLevelP0, AlertLevelP1, AlertLevelP3}
	if len(got) != len(want) {
		t.Fatalf("归一化结果长度不符：%v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("归一化结果不符：want=%v got=%v", want, got)
		}
	}
	if normalizeNotifyLevels(nil) != nil {
		t.Fatal("空输入应返回 nil（表示不限）")
	}
}

// ---------- 静默与退避 ----------

func TestNotifyRetryDelay(t *testing.T) {
	cases := map[int]time.Duration{
		1: 10 * time.Second,
		2: time.Minute,
		3: 5 * time.Minute,
		4: 30 * time.Minute,
		9: 30 * time.Minute,
	}
	for attempt, want := range cases {
		if got := notifyRetryDelay(attempt); got != want {
			t.Fatalf("第 %d 次重试间隔不符：want=%v got=%v", attempt, want, got)
		}
	}
}

// ---------- 渠道寻址与降级 ----------

func TestNotifySubjectsByChannel(t *testing.T) {
	resolution := notifySubjectResolution{
		Recipients: []notifyResolvedRecipient{
			{UserID: 1, Name: "张三", Email: "zhangsan@example.com", Mobile: "13800000001"},
			{UserID: 2, Name: "李四", Email: "", Mobile: "13800000002"},
		},
	}

	// 个人渠道（邮件）：没有邮箱的人被跳过，但不影响其他人
	emailTargets, skipped := notifySubjectsByChannel(notifier.Capabilities{Personalizable: true}, resolution)
	if len(emailTargets) != 1 || emailTargets[0].Email != "zhangsan@example.com" {
		t.Fatalf("邮件渠道应只投递有邮箱的接收人，实际 %+v", emailTargets)
	}
	if len(skipped) != 1 || skipped[0] != "李四" {
		t.Fatalf("缺地址的接收人应被显式跳过，实际 %+v", skipped)
	}

	// 群渠道：目标是群本身，不按人拆分
	groupTargets, groupSkipped := notifySubjectsByChannel(notifier.Capabilities{Personalizable: false}, resolution)
	if len(groupTargets) != 1 || len(groupSkipped) != 0 {
		t.Fatalf("群渠道应产出单条群任务，实际 %+v / %+v", groupTargets, groupSkipped)
	}

	// 需要绑定的渠道（短信/电话/飞书）：按手机号寻址
	bound, _ := notifySubjectsByChannel(notifier.Capabilities{Personalizable: true, RequiresBinding: true}, resolution)
	if len(bound) != 2 {
		t.Fatalf("需要绑定的渠道应按手机号寻址，实际 %+v", bound)
	}
}

// ---------- 消息渲染 ----------

func TestRenderAlertPayloadContent(t *testing.T) {
	triggered := commonModels.LocalTime(time.Date(2026, 9, 20, 10, 30, 0, 0, time.Local))
	event := monitorModels.InsightMonitorAlertEvents{
		Model:         &commonModels.Model{ID: 42},
		InstanceID:    "abc123",
		DBType:        "MySQL",
		Name:          "活跃连接占比过高",
		MetricKey:     MetricCapConnUsedPct,
		Level:         AlertLevelP1,
		Severity:      AlertLevelP1,
		Threshold:     80,
		ObservedValue: 92.5,
		Message:       "活跃连接占比 92.50% 超过阈值 80.00%",
		Advice:        "检查慢查询与连接泄漏",
		Occurrences:   6,
		DurationSec:   1500,
		TriggeredAt:   triggered,
	}
	instance := notifyInstanceContext{
		InstanceID:  "abc123",
		Name:        "db-01:3306",
		DbType:      "MySQL",
		Environment: "生产",
		Business:    "支付中心",
	}

	payload := renderAlertPayload(event, instance, "firing", "", notifier.Mentions{}, nil, "https://insight.example.com")
	for _, want := range []string{
		// 标题不再出现告警等级标识（外层 [P1] 前缀与规则名内嵌 P1 段都已剥掉）
		"### 活跃连接占比过高",
		"db-01:3306",
		"MySQL / 生产 / 支付中心",
		"92.50",
		"25 分钟",
		"累计：6 次",
		"建议：检查慢查询与连接泄漏",
		"https://insight.example.com/monitor/detail?instance_id=abc123",
	} {
		if !strings.Contains(payload.Markdown, want) {
			t.Fatalf("Markdown 缺少内容 %q，实际：\n%s", want, payload.Markdown)
		}
	}
	// 标题不应残留任何等级标识
	if alertLevelTokenRe.MatchString(payload.Title) {
		t.Fatalf("标题不应包含等级标识 P0-P3，实际 %s", payload.Title)
	}
	if payload.Level != AlertLevelP1 {
		t.Fatalf("payload 级别应为 P1，实际 %s", payload.Level)
	}
	if payload.Meta["event_id"] != "42" {
		t.Fatalf("payload 元信息缺少事件号，实际 %+v", payload.Meta)
	}

	// 恢复通知：标题带（已恢复），且不再展示"当前值/建议"
	recovered := renderAlertPayload(event, instance, "recovered", "", notifier.Mentions{}, nil, "")
	if !strings.Contains(recovered.Title, "已恢复") {
		t.Fatalf("恢复通知标题应标记已恢复，实际 %s", recovered.Title)
	}
	if strings.Contains(recovered.PlainText, "建议：") {
		t.Fatalf("恢复通知不应带处置建议，实际：\n%s", recovered.PlainText)
	}
}

func TestStripAlertLevelToken(t *testing.T) {
	cases := []struct{ in, want string }{
		// 规则保存时拼入的等级段：剥掉后其余文字与空白保持原样
		{"MySQL 复制 SQL 线程 P1 != 1", "MySQL 复制 SQL 线程 != 1"},
		{"MySQL 复制 IO 线程 P0 != 1", "MySQL 复制 IO 线程 != 1"},
		{"连接数 P2 >= 800", "连接数 >= 800"},
		{"活跃连接数 P3 > 50", "活跃连接数 > 50"},
		// P99 等非等级词不能误伤
		{"P99 延迟过高", "P99 延迟过高"},
		{"接口耗时 P95 > 100ms", "接口耗时 P95 > 100ms"},
		// 等级段在开头/结尾也不残留空白
		{"P1 磁盘空间不足", "磁盘空间不足"},
		{"磁盘空间不足 P1", "磁盘空间不足"},
	}
	for _, c := range cases {
		if got := stripAlertLevelToken(c.in); got != c.want {
			t.Fatalf("stripAlertLevelToken(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 认领回执：标题带认领人与处理人，正文明确"恢复前不再重复发送"。
func TestRenderAckPayload(t *testing.T) {
	event := monitorModels.InsightMonitorAlertEvents{
		Model:       &commonModels.Model{ID: 7},
		InstanceID:  "abc123",
		Name:        "活跃连接占比过高",
		MetricKey:   MetricCapConnUsedPct,
		Level:       AlertLevelP1,
		TriggeredAt: commonModels.LocalTime(time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)),
	}
	instance := notifyInstanceContext{InstanceID: "abc123", Name: "db-01:3306", DbType: "MySQL"}

	payload := renderAlertPayload(event, instance, NotifyTriggerAcknowledged, "hongjie", notifier.Mentions{}, nil, "")
	if !strings.Contains(payload.Title, "已被 hongjie 认领") {
		t.Fatalf("认领回执标题应含认领人，实际 %s", payload.Title)
	}
	for _, want := range []string{"处理人：hongjie", "恢复前不再重复发送本条告警", "恢复后会再次通知"} {
		if !strings.Contains(payload.Markdown, want) {
			t.Fatalf("认领回执缺少内容 %q，实际：\n%s", want, payload.Markdown)
		}
	}
	if payload.Meta["trigger"] != NotifyTriggerAcknowledged {
		t.Fatalf("认领回执 trigger 元信息应为 acknowledged，实际 %s", payload.Meta["trigger"])
	}
}

func TestNotifyMetricLayerFollowsDisplayLayout(t *testing.T) {
	cases := map[string]string{
		MetricCapConnUsedPct: LayerEngine,
		MetricResCPU:         LayerHost,
	}
	for metricKey, want := range cases {
		if got := notifyMetricLayer("mysql", metricKey); got != want {
			t.Fatalf("指标 %s 层级应为 %s，实际 %s", metricKey, want, got)
		}
	}
	// 平台自监控项不在展示目录里，回落指标字典的 scope
	if got := notifyMetricLayer("mysql", MetricAvailUp); got == "" {
		t.Fatalf("平台自监控项 %s 应能回落到字典 scope", MetricAvailUp)
	}
}

func TestFormatNotifyValueAndDuration(t *testing.T) {
	if got := formatNotifyValue(1234567.891); got != "1,234,567.89" {
		t.Fatalf("千分位格式不符：%s", got)
	}
	if got := formatNotifyValue(-1234.5); got != "-1,234.50" {
		t.Fatalf("负数千分位格式不符：%s", got)
	}
	durations := map[int64]string{
		0:      "刚刚",
		45:     "45 秒",
		600:    "10 分钟",
		7200:   "2.0 小时",
		172800: "2.0 天",
	}
	for seconds, want := range durations {
		if got := formatNotifyDuration(seconds); got != want {
			t.Fatalf("时长 %d 秒格式化不符：want=%s got=%s", seconds, want, got)
		}
	}
}

// ---------- 渠道能力与加签 ----------

func TestChannelRegistryAndCapabilities(t *testing.T) {
	for _, kind := range []string{"dingtalk", "feishu", "email", "wechat"} {
		channel, ok := notifier.Get(kind)
		if !ok {
			t.Fatalf("渠道 %s 未注册", kind)
		}
		if channel.Kind() != kind {
			t.Fatalf("渠道 %s 的 Kind() 返回 %s", kind, channel.Kind())
		}
	}
	// 群机器人不面向个人，邮件面向个人：寻址逻辑依赖这两个标记
	dingtalk, _ := notifier.Get("dingtalk")
	if dingtalk.Capabilities().Personalizable {
		t.Fatal("钉钉群机器人不应被标记为可发给个人")
	}
	email, _ := notifier.Get("email")
	if !email.Capabilities().Personalizable {
		t.Fatal("邮件应可发给个人")
	}
	// 密钥字段声明必须与表单字段一致，否则保存时会漏加密
	secretFields := notifier.SecretFieldsOf("dingtalk")
	if len(secretFields) != 1 || secretFields[0] != "secret" {
		t.Fatalf("钉钉需脱敏字段应为 [secret]，实际 %+v", secretFields)
	}
	if fields := notifier.SecretFieldsOf("email"); len(fields) != 1 || fields[0] != "password" {
		t.Fatalf("邮件需脱敏字段应为 [password]，实际 %+v", fields)
	}
}

// ---------- 接收人列表解码 ----------

func TestDecodeSubjectAndListHelpers(t *testing.T) {
	raw := datatypes.JSON([]byte(`[
		{"type":"USER","id":" 3 "},
		{"type":"org","id":"a/b"},
		{"type":"","id":"x"},
		{"type":"user","id":""}
	]`))
	subjects := decodeSubjects(raw)
	if len(subjects) != 2 {
		t.Fatalf("非法的接收人条目应被过滤，实际 %+v", subjects)
	}
	if subjects[0].Type != "user" || subjects[0].ID != "3" {
		t.Fatalf("接收人应被归一化，实际 %+v", subjects[0])
	}

	if got := decodeStringList(datatypes.JSON([]byte(`["P0",""," P1 "]`))); len(got) != 2 || got[1] != "P1" {
		t.Fatalf("字符串列表解码不符：%+v", got)
	}
	if got := decodeUint64List(datatypes.JSON([]byte(`[1,2,3]`))); len(got) != 3 {
		t.Fatalf("数字列表解码不符：%+v", got)
	}
	if got := decodeUint64List(datatypes.JSON([]byte(`["4","5"]`))); len(got) != 2 || got[0] != 4 {
		t.Fatalf("字符串形式的数字列表解码不符：%+v", got)
	}
}

// jsonList / jsonUintList 测试辅助：构造策略里的 JSON 条件列。
func jsonList(values ...string) datatypes.JSON {
	encoded := notifyEncodeJSON(values)
	return encoded
}

func jsonUintList(values ...uint64) datatypes.JSON {
	encoded := notifyEncodeJSON(values)
	return encoded
}

// TestPolicySampleEventRenders 样例告警必须能被正常渲染。
// 回归点：InsightMonitorAlertEvents 内嵌 *commonModels.Model，样例事件若忘了初始化，
// renderAlertPayload 读 event.ID 会直接 panic（曾把 /policies/:id/test 打成 502）。
func TestPolicySampleEventRenders(t *testing.T) {
	event := buildPolicyTestEvent(7, 1730000000)
	if event.ID != 0 {
		t.Fatalf("样例事件不应带真实主键，got %d", event.ID)
	}
	if event.Fingerprint == "" {
		t.Fatal("样例事件必须有 fingerprint（投递记录按它查询）")
	}
	payload := renderAlertPayload(event, policyTestInstance(), "firing", "", notifier.Mentions{}, nil, "")
	if !strings.Contains(payload.Title, "策略连通性测试") {
		t.Fatalf("标题缺少样例标识: %s", payload.Title)
	}
	if payload.Markdown == "" || payload.PlainText == "" {
		t.Fatal("渲染结果不能为空")
	}
	if payload.Meta["trigger"] != "firing" {
		t.Fatalf("trigger 元信息错误: %v", payload.Meta)
	}
}
