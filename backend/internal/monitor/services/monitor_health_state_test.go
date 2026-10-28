package services

import (
	"testing"
	"time"
)

// TestValidateHealthStateConfig 写入路径校验：阈值范围与次序、扣分表完整性。
func TestValidateHealthStateConfig(t *testing.T) {
	valid := builtinHealthStateConfig()
	if err := ValidateHealthStateConfig(valid); err != nil {
		t.Fatalf("内置默认应通过校验，实际 %v", err)
	}

	cases := []struct {
		name string
		mut  func(c *HealthStateConfig)
	}{
		{"故障阈值超上限", func(c *HealthStateConfig) { c.FaultThreshold = 101 }},
		{"故障阈值为负", func(c *HealthStateConfig) { c.FaultThreshold = -1 }},
		{"风险阈值超上限", func(c *HealthStateConfig) { c.RiskThreshold = 101 }},
		{"风险阈值为零", func(c *HealthStateConfig) { c.RiskThreshold = 0 }},
		{"故障阈值不小于风险阈值", func(c *HealthStateConfig) { c.FaultThreshold = 90; c.RiskThreshold = 90 }},
		{"扣分表为空", func(c *HealthStateConfig) { c.Deductions = map[string]int{} }},
		{"扣分表缺等级", func(c *HealthStateConfig) { delete(c.Deductions, "P2") }},
		{"扣分为负", func(c *HealthStateConfig) { c.Deductions["P1"] = -1 }},
		{"扣分超上限", func(c *HealthStateConfig) { c.Deductions["P3"] = 101 }},
	}
	for _, tc := range cases {
		cfg := valid
		tc.mut(&cfg)
		if err := ValidateHealthStateConfig(cfg); err == nil {
			t.Errorf("%s: 应报错", tc.name)
		}
	}
}

// TestEvaluateInstanceStateScoreMatrix 分数 → 状态矩阵（内置默认阈值 60/90，扣分 40/10/5/3）。
func TestEvaluateInstanceStateScoreMatrix(t *testing.T) {
	card := InstanceMonitorCard{CollectStatus: "success", Quality: QualityOK}
	alert := func(level string) AlertRecord {
		return AlertRecord{RowKey: "r", Severity: level, StatusKey: "active"}
	}

	cases := []struct {
		name       string
		alerts     []AlertRecord
		wantClass  string
		wantScore  int
		wantTop    string
	}{
		{"无告警满分健康", nil, "healthy", 100, ""},
		{"单条P0恰好触达故障档", []AlertRecord{alert("P0")}, "fault", 60, "P0"},
		{"单条P1恰好触达风险档", []AlertRecord{alert("P1")}, "risk", 90, "P1"},
		{"单条P2不改变状态", []AlertRecord{alert("P2")}, "healthy", 95, "P2"},
		{"单条P3不改变状态", []AlertRecord{alert("P3")}, "healthy", 97, "P3"},
		{"混合扣分落风险区间", []AlertRecord{alert("P1"), alert("P3"), alert("P3")}, "risk", 84, "P1"},
		{"四条P1触达故障档", []AlertRecord{alert("P1"), alert("P1"), alert("P1"), alert("P1")}, "fault", 60, "P1"},
		{"超额扣分钳零仍故障", []AlertRecord{alert("P0"), alert("P0"), alert("P0")}, "fault", 0, "P0"},
		{"空severity按P3兜底", []AlertRecord{alert("")}, "healthy", 97, "P3"},
	}
	for _, tc := range cases {
		card.Alerts = tc.alerts
		got := evaluateInstanceState(card)
		if got.Class != tc.wantClass {
			t.Errorf("%s: 状态期望 %s，实际 %s", tc.name, tc.wantClass, got.Class)
		}
		if got.Score == nil || *got.Score != tc.wantScore {
			t.Errorf("%s: 分数期望 %d，实际 %v", tc.name, tc.wantScore, got.Score)
		}
		if got.TopSeverity != tc.wantTop {
			t.Errorf("%s: 最高等级期望 %q，实际 %q", tc.name, tc.wantTop, got.TopSeverity)
		}
	}
}

// TestEvaluateInstanceStateCustomThresholds 自定义阈值生效：阈值改了，同一分数落不同档。
func TestEvaluateInstanceStateCustomThresholds(t *testing.T) {
	cfg := builtinHealthStateConfig()
	cfg.FaultThreshold = 95
	cfg.RiskThreshold = 98
	injectHealthStateCache(cfg)
	t.Cleanup(clearHealthStateCache)

	card := InstanceMonitorCard{CollectStatus: "success", Quality: QualityOK}
	card.Alerts = []AlertRecord{{RowKey: "r", Severity: "P2", StatusKey: "active"}}
	got := evaluateInstanceState(card)
	if got.Class != "fault" {
		t.Errorf("故障阈值 95：单条 P2（95 分）应判故障，实际 %s", got.Class)
	}
}

// TestEvaluateInstanceStateFixedPreconditions 固定前置：优先于分数，且状态与分数自洽。
func TestEvaluateInstanceStateFixedPreconditions(t *testing.T) {
	alert := func(level string) AlertRecord {
		return AlertRecord{RowKey: "r", Severity: level, StatusKey: "active"}
	}

	// 采集失败 → 故障 0 分（哪怕挂着一条 P1，分数也不许显示 90）
	failed := InstanceMonitorCard{CollectStatus: "failed", Quality: QualityOK}
	got := evaluateInstanceState(failed)
	if got.Class != "fault" || got.Score == nil || *got.Score != 0 {
		t.Errorf("采集失败应故障 0 分，实际 %s/%v", got.Class, got.Score)
	}

	// 质量错误 → 故障 0 分
	qualityErr := InstanceMonitorCard{CollectStatus: "success", Quality: QualityError}
	if got = evaluateInstanceState(qualityErr); got.Class != "fault" || *got.Score != 0 {
		t.Errorf("质量错误应故障 0 分，实际 %s/%v", got.Class, got.Score)
	}

	// 从未采集 / 数据过期 / 新鲜度超限 → 未采集且不给分
	never := InstanceMonitorCard{CollectStatus: "never"}
	if got = evaluateInstanceState(never); got.Class != "uncollected" || got.Score != nil {
		t.Errorf("从未采集应未采集无分数，实际 %s/%v", got.Class, got.Score)
	}
	stale := InstanceMonitorCard{CollectStatus: "success", Quality: QualityStale, Alerts: []AlertRecord{alert("P0")}}
	if got = evaluateInstanceState(stale); got.Class != "uncollected" || got.Score != nil {
		t.Errorf("数据过期应未采集无分数（告警不参与），实际 %s/%v", got.Class, got.Score)
	}
	freshness := InstanceMonitorCard{CollectStatus: "success", Quality: QualityOK, FreshnessSeconds: 99999}
	if got = evaluateInstanceState(freshness); got.Class != "uncollected" || got.Score != nil {
		t.Errorf("新鲜度超限应未采集无分数，实际 %s/%v", got.Class, got.Score)
	}

	// 采集降级：分数满分也至少判风险；分数同步下落到风险阈值，保持分数与状态一致。
	partial := InstanceMonitorCard{CollectStatus: "success", Quality: "partial"}
	if got = evaluateInstanceState(partial); got.Class != "risk" || got.Score == nil || *got.Score != 90 {
		t.Errorf("partial 应风险且分数下落到风险阈值 90，实际 %s/%v", got.Class, got.Score)
	}
	unsupported := InstanceMonitorCard{CollectStatus: "success", Quality: QualityUnsupported}
	if got = evaluateInstanceState(unsupported); got.Class != "risk" || got.Score == nil || *got.Score != 90 {
		t.Errorf("unsupported 应至少风险且分数下落到风险阈值 90，实际 %s/%v", got.Class, got.Score)
	}
	// 降级 + 分数本身落风险/故障档：按分数判，不额外处理
	degraded := InstanceMonitorCard{CollectStatus: "success", Quality: "partial", Alerts: []AlertRecord{alert("P0")}}
	if got = evaluateInstanceState(degraded); got.Class != "fault" || *got.Score != 60 {
		t.Errorf("partial+P0 应按分数判故障 60，实际 %s/%v", got.Class, got.Score)
	}

	// 已忽略的告警不参与算分与判定
	ignored := InstanceMonitorCard{CollectStatus: "success", Quality: QualityOK}
	ignored.Alerts = []AlertRecord{{RowKey: "r", Severity: "P0", StatusKey: "active", Ignored: true}}
	if got = evaluateInstanceState(ignored); got.Class != "healthy" || *got.Score != 100 {
		t.Errorf("已忽略告警应不影响，实际 %s/%v", got.Class, got.Score)
	}
}

// TestHealthStateClassFromScore 边界分归更严重一档：60 分在默认阈值下是故障不是风险。
func TestHealthStateClassFromScore(t *testing.T) {
	cfg := builtinHealthStateConfig()
	cases := []struct {
		score int
		want  string
	}{
		{0, "fault"}, {60, "fault"}, {61, "risk"}, {90, "risk"}, {91, "healthy"}, {100, "healthy"},
	}
	for _, tc := range cases {
		if got := healthStateClassFromScore(cfg, tc.score); got != tc.want {
			t.Errorf("score=%d: 期望 %s，实际 %s", tc.score, tc.want, got)
		}
	}
}

// TestScreenClassLabelOf 四分类中文名与前端口径一致。
func TestScreenClassLabelOf(t *testing.T) {
	cases := map[string]string{
		"healthy": "健康", "risk": "风险", "fault": "故障", "uncollected": "未采集", "other": "未获取",
	}
	for class, want := range cases {
		if got := screenClassLabelOf(class); got != want {
			t.Errorf("%s: 期望 %s，实际 %s", class, want, got)
		}
	}
}

// 注入缓存后立即失效重建，避免依赖真实时钟。
func injectHealthStateCache(cfg HealthStateConfig) {
	healthStateMutex.Lock()
	healthStateCache = &cfg
	healthStateLoadedAt = time.Now()
	healthStateMutex.Unlock()
}

func clearHealthStateCache() {
	healthStateMutex.Lock()
	healthStateCache = nil
	healthStateMutex.Unlock()
}
