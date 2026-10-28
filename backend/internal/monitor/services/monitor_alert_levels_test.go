package services

import (
	"testing"

	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// ---------------------------------------------------------------------------
// 告警等级与档位校验
// ---------------------------------------------------------------------------

func TestNormalizeAlertLevelLegacyAndPCodes(t *testing.T) {
	cases := map[string]string{
		"P0": "P0", "p0": "P0", "紧急": "P0",
		"P1": "P1", "高": "P1",
		"P2": "P2", "中": "P2",
		"P3": "P3", "低": "P3",
	}
	for input, want := range cases {
		if got := normalizeAlertLevelStrict(input); got != want {
			t.Errorf("归一化 %q 期望 %q，实际 %q", input, want, got)
		}
	}
	if normalizeAlertLevelStrict("") != "" || normalizeAlertLevelStrict("P9") != "" {
		t.Error("非法等级必须返回空串，不能兜底成某档")
	}
	// 排序哨兵：未知等级不能与 P0 同权
	if alertLevelRankOf("P0") != 0 || alertLevelRankOf("P0-unknown") != alertLevelRankSentinel {
		t.Errorf("未知等级必须走哨兵排序，实际 %d", alertLevelRankOf("P0-unknown"))
	}
}

// 单调性：>= 方向阈值必须严格递减，<= 方向必须严格递增，方向选反要拒绝。
func TestValidateMetricAlertLevelsMonotonicity(t *testing.T) {
	valid := []AlertLevelSpec{
		{Level: AlertLevelP1, Threshold: 95, ForSeconds: 300, Enabled: true},
		{Level: AlertLevelP2, Threshold: 85, ForSeconds: 600, Enabled: true},
		{Level: AlertLevelP3, Threshold: 70, ForSeconds: 3600, Enabled: true},
	}
	if result := ValidateMetricAlertLevels("磁盘使用率", ">=", ">=", valid); len(result.Errors) != 0 {
		t.Errorf("合法的递减阶梯不应报错，实际 %v", result.Errors)
	}

	inverted := []AlertLevelSpec{
		{Level: AlertLevelP1, Threshold: 70, ForSeconds: 300},
		{Level: AlertLevelP2, Threshold: 85, ForSeconds: 600},
	}
	if result := ValidateMetricAlertLevels("磁盘使用率", ">=", ">=", inverted); len(result.Errors) == 0 {
		t.Error(">= 方向阈值倒挂必须被拒绝")
	}

	// 越小越糟：缓存命中率 P1(70) 必须低于 P2(90)
	lesserValid := []AlertLevelSpec{
		{Level: AlertLevelP1, Threshold: 70, ForSeconds: 300},
		{Level: AlertLevelP2, Threshold: 90, ForSeconds: 600},
	}
	if result := ValidateMetricAlertLevels("缓存命中率", "<=", "<=", lesserValid); len(result.Errors) != 0 {
		t.Errorf("<= 方向的递增阶梯应合法，实际 %v", result.Errors)
	}
	lesserInverted := []AlertLevelSpec{
		{Level: AlertLevelP1, Threshold: 95, ForSeconds: 300},
		{Level: AlertLevelP2, Threshold: 90, ForSeconds: 600},
	}
	if result := ValidateMetricAlertLevels("缓存命中率", "<=", "<=", lesserInverted); len(result.Errors) == 0 {
		t.Error("<= 方向阈值倒挂必须被拒绝")
	}
}

func TestValidateMetricAlertLevelsOperatorDirection(t *testing.T) {
	levels := []AlertLevelSpec{{Level: AlertLevelP1, Threshold: 95, Enabled: true}}
	// 方向相反：字典建议 >=，提交 <= → 拒绝（这类错误会让规则永不触发）
	if result := ValidateMetricAlertLevels("CPU 使用率", "<=", ">=", levels); len(result.Errors) == 0 {
		t.Error("比较符方向与指标语义相反时必须拒绝")
	}
	// 方向相同但形式不同：自动归一化为字典口径，不报错
	result := ValidateMetricAlertLevels("CPU 使用率", ">", ">=", levels)
	if len(result.Errors) != 0 {
		t.Errorf("同方向的形式差异应自动归一化，实际 %v", result.Errors)
	}
	if result.Operator != ">=" {
		t.Errorf("比较符应归一化为字典建议 %q，实际 %q", ">=", result.Operator)
	}
}

func TestValidateMetricAlertLevelsBooleanSingleLevel(t *testing.T) {
	single := []AlertLevelSpec{{Level: AlertLevelP0, Threshold: 1, Enabled: true}}
	if result := ValidateMetricAlertLevels("复制 IO 线程", "!=", "!=", single); len(result.Errors) != 0 {
		t.Errorf("布尔类指标单档应合法，实际 %v", result.Errors)
	}
	pair := []AlertLevelSpec{
		{Level: AlertLevelP0, Threshold: 1, Enabled: true},
		{Level: AlertLevelP1, Threshold: 1, Enabled: true},
	}
	if result := ValidateMetricAlertLevels("复制 IO 线程", "!=", "!=", pair); len(result.Errors) == 0 {
		t.Error("布尔类指标配多档必须被拒绝（不存在严重程度阶梯）")
	}
}

func TestValidateMetricAlertLevelsDuplicateAndWarnings(t *testing.T) {
	dup := []AlertLevelSpec{
		{Level: AlertLevelP1, Threshold: 95, Enabled: true},
		{Level: "高", Threshold: 90, Enabled: true},
	}
	result := ValidateMetricAlertLevels("CPU 使用率", ">=", ">=", dup)
	if len(result.Errors) == 0 {
		t.Error("P1 与旧值「高」是同一档，必须判为重复")
	}

	// 软提示：P0 持续窗口比 P1 长、P0 静默比 P1 长、持续 < 30s
	soft := []AlertLevelSpec{
		{Level: AlertLevelP0, Threshold: 99, ForSeconds: 600, SilenceMinutes: 240, Enabled: true},
		{Level: AlertLevelP1, Threshold: 90, ForSeconds: 10, SilenceMinutes: 15, Enabled: true},
	}
	result = ValidateMetricAlertLevels("CPU 使用率", ">=", ">=", soft)
	if len(result.Errors) != 0 {
		t.Fatalf("软规则不应产生硬错误，实际 %v", result.Errors)
	}
	if len(result.Warnings) < 3 {
		t.Errorf("应给出持续窗口/静默时长/采样周期三类软提示，实际 %v", result.Warnings)
	}
}

// 推荐档位必须自洽：全部通过校验器（避免"平台自己的推荐值违规"）。
func TestRecommendedAlertLevelsSelfConsistent(t *testing.T) {
	dict := map[string]MetricDescriptor{}
	for _, item := range builtinMetricCatalog {
		dict[canonicalMetricKey(item.MetricKey)] = item
	}
	for key := range recommendedAlertLevels {
		item, ok := dict[key]
		if !ok {
			t.Errorf("推荐表里的 %s 不在指标字典中（推荐了一个采不到的指标）", key)
			continue
		}
		levels := recommendedLevelsFor(item)
		result := ValidateMetricAlertLevels(item.DisplayName, item.DefaultOperator, item.DefaultOperator, levels)
		if len(result.Errors) != 0 {
			t.Errorf("指标 %s 的推荐档位未通过校验：%v", key, result.Errors)
		}
		if len(result.Warnings) != 0 {
			t.Errorf("指标 %s 的推荐档位含软提示（默认值不该有反直觉配置）：%v", key, result.Warnings)
		}
	}
}

// ---------------------------------------------------------------------------
// 命中最高档（阶梯短路）
// ---------------------------------------------------------------------------

func TestChooseHighestHitRuleShortCircuits(t *testing.T) {
	rows := []monitorModels.InsightMonitorMetricLatest{
		{MetricKey: MetricResCPU, Quality: QualityOK, Value: floatPtr(96), Unit: "%"},
	}
	latest := latestByKey(rows)
	row := monitorInstanceRow{InstanceID: "ds-1", DbType: "MySQL"}
	group := []monitorModels.InsightMonitorAlertRules{
		{MetricKey: MetricResCPU, Operator: ">=", Threshold: 85, AlertLevel: AlertLevelP2},
		{MetricKey: MetricResCPU, Operator: ">=", Threshold: 95, AlertLevel: AlertLevelP1},
	}
	chosen, hit := chooseHighestHitRule(row, group, latest)
	if !hit {
		t.Fatal("96% 应命中阈值")
	}
	if chosen.Rule.AlertLevel != AlertLevelP1 {
		t.Errorf("P1(95) 与 P2(85) 同时命中时应只报最高档 P1，实际 %s", chosen.Rule.AlertLevel)
	}

	// 只命中低档：结果必须是低档，不能因为高threshold未命中就完全不报
	rows[0].Value = floatPtr(88)
	chosen, hit = chooseHighestHitRule(row, group, latestByKey(rows))
	if !hit || chosen.Rule.AlertLevel != AlertLevelP2 {
		t.Errorf("仅命中 P2 时应报 P2，实际 hit=%v level=%s", hit, chosen.Rule.AlertLevel)
	}

	// 全部未命中
	rows[0].Value = floatPtr(50)
	if _, hit = chooseHighestHitRule(row, group, latestByKey(rows)); hit {
		t.Error("未越限时不应命中")
	}
}

// 档位混乱（等级写入的是中文旧值）时，排序仍必须按 P 码语义 —— P0 优先短路。
func TestChooseHighestHitRuleToleratesLegacyLevelValues(t *testing.T) {
	latest := latestByKey([]monitorModels.InsightMonitorMetricLatest{
		{MetricKey: MetricResCPU, Quality: QualityOK, Value: floatPtr(99), Unit: "%"},
	})
	group := []monitorModels.InsightMonitorAlertRules{
		{MetricKey: MetricResCPU, Operator: ">=", Threshold: 90, AlertLevel: "中"},
		{MetricKey: MetricResCPU, Operator: ">=", Threshold: 98, AlertLevel: "高"},
	}
	chosen, hit := chooseHighestHitRule(monitorInstanceRow{InstanceID: "ds-1"}, group, latest)
	if !hit || chosen.Rule.AlertLevel != "高" {
		t.Errorf("中文旧等级「高」应等同于最高档并被优先命中，实际 hit=%v level=%q", hit, chosen.Rule.AlertLevel)
	}
}

// 数据质量是硬门槛：quality != ok 时不得命中（"没采到 ≠ 异常"）。
func TestChooseHighestHitRuleRespectsQualityGate(t *testing.T) {
	latest := latestByKey([]monitorModels.InsightMonitorMetricLatest{
		{MetricKey: MetricResCPU, Quality: QualityStale, Value: floatPtr(99), Unit: "%"},
	})
	group := []monitorModels.InsightMonitorAlertRules{
		{MetricKey: MetricResCPU, Operator: ">=", Threshold: 50, AlertLevel: AlertLevelP1},
	}
	if _, hit := chooseHighestHitRule(monitorInstanceRow{InstanceID: "ds-1"}, group, latest); hit {
		t.Error("quality != ok 时不应命中")
	}
}

func floatPtr(value float64) *float64 { return &value }
