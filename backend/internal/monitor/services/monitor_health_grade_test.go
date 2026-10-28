package services

import (
	"encoding/json"
	"testing"
)

// ============================================================================
// 健康分级：配置自校验 + 评估口径 + 降级 + 可扩展性
//
// 这些用例盯的是"口径不在代码里"这件事本身：
// 内置默认必须自洽（否则新部署一上来就降级运行），
// 用户改配置后结论必须跟着变（否则等于把口径写回了代码）。
// ============================================================================

// TestBuiltinHealthGradeConfigSelfValid 内置默认配置必须自身合法且覆盖 P0~P3。
// 一旦这里挂了，说明默认策略有引用断裂，新部署会直接降级运行。
func TestBuiltinHealthGradeConfigSelfValid(t *testing.T) {
	cfg := builtinHealthGradeConfig()
	if err := ValidateHealthGradeConfig(cfg); err != nil {
		t.Fatalf("内置默认配置未通过校验: %v", err)
	}
	wantStates := map[string]string{
		"P0": HealthStateFault,
		"P1": HealthStateRisk,
		"P2": HealthStateRisk,
		"P3": HealthStateHealthy,
	}
	if len(cfg.Levels) != len(wantStates) {
		t.Fatalf("内置等级数应为 %d，实际 %d", len(wantStates), len(cfg.Levels))
	}
	for _, level := range cfg.Levels {
		if want, ok := wantStates[level.Code]; !ok {
			t.Errorf("出现未预期的等级 %s", level.Code)
		} else if level.HealthState != want {
			t.Errorf("等级 %s 的 health_state 应为 %s，实际 %s", level.Code, want, level.HealthState)
		}
	}
	// 每个等级都要有结果项落地，否则该等级永远不会被命中（配置成了摆设）
	used := map[string]int{}
	for _, result := range cfg.Results {
		used[result.Level]++
	}
	for code := range wantStates {
		if used[code] == 0 {
			t.Errorf("等级 %s 没有任何结果项归属，该等级不可达", code)
		}
	}
}

func healthGradeBaseCard() InstanceMonitorCard {
	return InstanceMonitorCard{
		CollectStatus: "success",
		Quality:       QualityOK,
		Status:        "healthy",
		DeployMode:    "standalone",
	}
}

func metricSample(key string, value float64) MetricSampleView {
	return MetricSampleView{Key: key, Available: true, Value: value, Quality: QualityOK}
}

// TestEvaluateInstanceHealthDefaultPolicy 内置默认口径的判定矩阵。
// 这里的期望值就是"最佳实践"的具象化，改默认值必须同步改这里。
func TestEvaluateInstanceHealthDefaultPolicy(t *testing.T) {
	bundle := newHealthGradeBundle(builtinHealthGradeConfig())
	cases := []struct {
		name       string
		mut        func(c *InstanceMonitorCard)
		wantState  string
		wantLevel  string
		wantResult string
	}{
		{"全绿判健康", func(c *InstanceMonitorCard) {}, HealthStateHealthy, "P3", ""},
		{"实例不可达判故障", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample(MetricAvailUp, 0)}
		}, HealthStateFault, "P0", "instance_down"},
		{"采集失败判故障", func(c *InstanceMonitorCard) {
			c.CollectStatus = "failed"
		}, HealthStateFault, "P0", "collect_failed"},
		{"数据过期判未采集", func(c *InstanceMonitorCard) {
			c.FreshnessSeconds = 999
		}, HealthStateUncollected, "P2", "data_stale"},
		{"有P2告警判风险", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{{Severity: "P2", MetricKey: "perf.qps"}}
		}, HealthStateRisk, "P2", "alert_active"},
		{"有P1告警判严重", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{{Severity: "P1", MetricKey: "repl.io_running"}}
		}, HealthStateRisk, "P1", "alert_p1_active"},
		// 单机实例没有复制，不应被复制类规则误判（"健康 0 个"的典型成因）
		{"单机复制指标为0不判故障", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample("repl.io_running", 0)}
		}, HealthStateHealthy, "P3", ""},
		{"主从复制线程停止判严重", func(c *InstanceMonitorCard) {
			c.DeployMode = "master_slave"
			c.Metrics = []MetricSampleView{metricSample("repl.io_running", 0)}
		}, HealthStateRisk, "P1", "replication_stopped"},
		{"磁盘将满判严重", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample("res.disk", 96)}
		}, HealthStateRisk, "P1", "capacity_exhausted"},
		{"主机资源压力判风险", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample("res.cpu", 90)}
		}, HealthStateRisk, "P2", "resource_pressure"},
		// 慢查询分档：少量只是提示（仍是健康），激增才是风险
		{"少量慢查询只判提示", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample("eng.slow_queries", 0.05)}
		}, HealthStateHealthy, "P3", "slow_queries_elevated"},
		{"慢查询激增判风险", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample("eng.slow_queries", 10)}
		}, HealthStateRisk, "P2", "slow_query_storm"},
		{"连接错误速率偏高判风险", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{metricSample("eng.aborted_conns", 60)}
		}, HealthStateRisk, "P2", "conn_error_high"},
		{"采集质量降级判风险", func(c *InstanceMonitorCard) {
			c.Quality = "partial"
		}, HealthStateRisk, "P2", "collect_warning"},
		{"连续失败但未彻底失败判风险", func(c *InstanceMonitorCard) {
			c.ConsecutiveFailures = 5
		}, HealthStateRisk, "P2", "collect_degraded"},
	}
	for _, tc := range cases {
		card := healthGradeBaseCard()
		tc.mut(&card)
		got := evaluateWithBundle(bundle, card)
		if got.HealthState != tc.wantState {
			t.Errorf("%s: 状态期望 %s，实际 %s", tc.name, tc.wantState, got.HealthState)
			continue
		}
		if got.Level != tc.wantLevel {
			t.Errorf("%s: 等级期望 %s，实际 %s", tc.name, tc.wantLevel, got.Level)
			continue
		}
		if tc.wantResult == "" {
			if len(got.Findings) != 0 {
				t.Errorf("%s: 期望无命中，实际 %+v", tc.name, got.Findings)
			}
			continue
		}
		found := false
		for _, item := range got.Findings {
			if item.ResultCode == tc.wantResult {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: 未命中结果项 %s，实际 %+v", tc.name, tc.wantResult, got.Findings)
		}
	}
}

// TestHealthGradeUnavailableMetricNeverFakesZero 采集不到的指标不产生事实，
// 因此不会被数值规则误命中 —— 0 是合法读数（如"磁盘 0%"），不能用来代表"没采到"。
func TestHealthGradeUnavailableMetricNeverFakesZero(t *testing.T) {
	bundle := newHealthGradeBundle(builtinHealthGradeConfig())
	card := healthGradeBaseCard()
	card.Metrics = []MetricSampleView{{Key: "res.disk", Available: false, Value: 0, Quality: QualityNoData}}
	got := evaluateWithBundle(bundle, card)
	if got.HealthState != HealthStateHealthy {
		t.Errorf("不可用的指标不应参与判定，实际状态 %s（%+v）", got.HealthState, got.Findings)
	}
}

// TestHealthGradeUserConfigOverridesDefault 用户配置必须覆盖内置默认：
// 把 P3 从"提示（健康）"改成"风险"后，少量慢查询的实例应立刻变成风险。
// 这条用例是"判定标准由配置决定、不写死在代码里"的直接证据。
func TestHealthGradeUserConfigOverridesDefault(t *testing.T) {
	cfg := builtinHealthGradeConfig()
	for i := range cfg.Levels {
		if cfg.Levels[i].Code == "P3" {
			cfg.Levels[i].HealthState = HealthStateRisk
			cfg.Levels[i].Name = "提示（已升级为风险）"
		}
	}
	card := healthGradeBaseCard()
	card.Metrics = []MetricSampleView{metricSample("eng.slow_queries", 0.05)}
	got := evaluateWithBundle(newHealthGradeBundle(cfg), card)
	if got.HealthState != HealthStateRisk {
		t.Errorf("用户把 P3 改成 risk 后应判风险，实际 %s", got.HealthState)
	}
	if got.Level != "P3" {
		t.Errorf("等级应仍为 P3（改的是状态映射不是等级归属），实际 %s", got.Level)
	}
}

// TestHealthGradeResultLevelReassign 改结果项的等级归属即可改判：
// 把"存在慢查询"从 P3 挪到 P1，无需改任何规则。
func TestHealthGradeResultLevelReassign(t *testing.T) {
	cfg := builtinHealthGradeConfig()
	for i := range cfg.Results {
		if cfg.Results[i].Code == "slow_queries_elevated" {
			cfg.Results[i].Level = "P1"
		}
	}
	if err := ValidateHealthGradeConfig(cfg); err != nil {
		t.Fatalf("改结果项等级后配置应仍合法: %v", err)
	}
	card := healthGradeBaseCard()
	card.Metrics = []MetricSampleView{metricSample("eng.slow_queries", 0.05)}
	got := evaluateWithBundle(newHealthGradeBundle(cfg), card)
	if got.Level != "P1" || got.HealthState != HealthStateRisk {
		t.Errorf("期望 P1/risk，实际 %s/%s", got.Level, got.HealthState)
	}
}

// TestHealthGradeConfigSupportsExtraLevel 新增等级 P4 不需要改代码：
// 加等级 + 加结果项 + 加规则三步即可，校验与评估都认。
func TestHealthGradeConfigSupportsExtraLevel(t *testing.T) {
	cfg := builtinHealthGradeConfig()
	cfg.Levels = append(cfg.Levels, HealthLevelDef{
		Code: "P4", Name: "观察", Rank: 4, HealthState: HealthStateHealthy, Description: "仅记录，不参与处置",
	})
	cfg.Results = append(cfg.Results, HealthResultDef{
		Code: "qps_observed", Name: "QPS 进入观察区", Level: "P4", Category: "performance", Enabled: true,
	})
	cfg.Rules = append(cfg.Rules, HealthRuleDef{
		Code: "qps_observed", Result: "qps_observed", Enabled: true, Priority: 200,
		Message: "QPS {observed} 已进入观察区",
		Any:     []HealthCondition{{Fact: "metric:perf.qps", Op: "gte", Value: 5000, Label: "QPS"}},
	})
	if err := ValidateHealthGradeConfig(cfg); err != nil {
		t.Fatalf("扩展等级后配置应合法: %v", err)
	}
	card := healthGradeBaseCard()
	card.Metrics = []MetricSampleView{metricSample("perf.qps", 8000)}
	got := evaluateWithBundle(newHealthGradeBundle(cfg), card)
	if got.Level != "P4" {
		t.Errorf("期望命中新增等级 P4，实际 %s（%+v）", got.Level, got.Findings)
	}
}

// TestValidateHealthGradeConfigRejectsBrokenConfig 写入路径严格校验：
// 引用断裂、rank 重复、事实/操作符不匹配都要报错，不能静默收下。
func TestValidateHealthGradeConfigRejectsBrokenConfig(t *testing.T) {
	valid := builtinHealthGradeConfig()

	t.Run("等级rank重复", func(t *testing.T) {
		cfg := valid
		cfg.Levels = append([]HealthLevelDef{}, valid.Levels...)
		cfg.Levels[1].Rank = cfg.Levels[0].Rank
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("rank 重复应报错")
		}
	})
	t.Run("结果项引用不存在的等级", func(t *testing.T) {
		cfg := valid
		cfg.Results = append([]HealthResultDef{}, valid.Results...)
		cfg.Results[0].Level = "P9"
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("结果项引用不存在等级应报错")
		}
	})
	t.Run("规则引用不存在的结果项", func(t *testing.T) {
		cfg := valid
		cfg.Rules = append([]HealthRuleDef{}, valid.Rules...)
		cfg.Rules[0].Result = "not_exists"
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("规则引用不存在结果项应报错")
		}
	})
	t.Run("fallback不存在", func(t *testing.T) {
		cfg := valid
		cfg.Fallback = "not_exists"
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("fallback 指向不存在的结果项应报错")
		}
	})
	t.Run("规则无条件", func(t *testing.T) {
		cfg := valid
		cfg.Rules = append([]HealthRuleDef{}, valid.Rules...)
		cfg.Rules[0].Any = nil
		cfg.Rules[0].All = nil
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("规则没有任何条件应报错")
		}
	})
	t.Run("未知事实", func(t *testing.T) {
		cfg := valid
		cfg.Rules = append([]HealthRuleDef{}, valid.Rules...)
		cfg.Rules[0].Any = []HealthCondition{{Fact: "unknown_fact", Op: "eq", Value: 1}}
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("引用未知事实应报错")
		}
	})
	t.Run("事实与操作符不匹配", func(t *testing.T) {
		cfg := valid
		cfg.Rules = append([]HealthRuleDef{}, valid.Rules...)
		cfg.Rules[0].Any = []HealthCondition{{Fact: "metric:perf.qps", Op: "contains", Value: "x"}}
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("数值事实不支持 contains 应报错")
		}
	})
	t.Run("between需要两个数字", func(t *testing.T) {
		cfg := valid
		cfg.Rules = append([]HealthRuleDef{}, valid.Rules...)
		cfg.Rules[0].Any = []HealthCondition{{Fact: "metric:perf.qps", Op: "between", Value: []any{1}}}
		if err := ValidateHealthGradeConfig(cfg); err == nil {
			t.Error("between 只给一个数字应报错")
		}
	})
}

// TestNormalizeHealthGradeConfigDegrades 读取路径宽容降级：
// 库里躺着脏数据时不能让大屏停摆，要降级成能跑的配置并留下原因。
func TestNormalizeHealthGradeConfigDegrades(t *testing.T) {
	t.Run("坏JSON回退内置默认", func(t *testing.T) {
		cfg, reason := NormalizeHealthGradeConfig([]byte("{ 这不是 JSON"))
		if !cfg.Degraded || reason == "" {
			t.Fatal("坏 JSON 应降级并给出原因")
		}
		if len(cfg.Levels) != 4 {
			t.Errorf("应回退内置四级，实际 %d 级", len(cfg.Levels))
		}
	})
	t.Run("空配置回退内置默认", func(t *testing.T) {
		cfg, reason := NormalizeHealthGradeConfig(nil)
		if reason != "" || cfg.Degraded {
			t.Error("空配置等同于未自定义，应直接用内置默认且不算降级")
		}
	})
	t.Run("结果项等级不存在挂到最轻等级", func(t *testing.T) {
		raw, _ := json.Marshal(HealthGradeConfig{
			Version: 1,
			Levels: []HealthLevelDef{
				{Code: "P0", Name: "致命", Rank: 0, HealthState: HealthStateFault},
				{Code: "P1", Name: "一般", Rank: 1, HealthState: HealthStateRisk},
			},
			Results: []HealthResultDef{{Code: "x", Name: "X", Level: "P9", Enabled: true}},
			Rules:   []HealthRuleDef{{Code: "r", Result: "x", Enabled: true, Any: []HealthCondition{{Fact: "active_alerts", Op: "gte", Value: 1}}}},
		})
		cfg, reason := NormalizeHealthGradeConfig(raw)
		if reason == "" {
			t.Fatal("应记录降级原因")
		}
		if cfg.Results[0].Level != "P1" {
			t.Errorf("结果项应挂到最轻等级 P1，实际 %s", cfg.Results[0].Level)
		}
	})
	t.Run("规则引用缺失结果项则停用", func(t *testing.T) {
		raw, _ := json.Marshal(HealthGradeConfig{
			Version:  1,
			Levels:   []HealthLevelDef{{Code: "P0", Name: "致命", Rank: 0, HealthState: HealthStateFault}},
			Results:  []HealthResultDef{{Code: "healthy", Name: "健康", Level: "P0", Enabled: true}},
			Fallback: "healthy",
			Rules: []HealthRuleDef{
				{Code: "bad", Result: "missing", Enabled: true, Any: []HealthCondition{{Fact: "active_alerts", Op: "gte", Value: 1}}},
			},
		})
		cfg, _ := NormalizeHealthGradeConfig(raw)
		if len(cfg.Rules) != 1 || cfg.Rules[0].Enabled {
			t.Error("引用缺失结果项的规则应被停用")
		}
	})
	t.Run("条件非法则停用整条规则", func(t *testing.T) {
		raw, _ := json.Marshal(HealthGradeConfig{
			Version:  1,
			Levels:   []HealthLevelDef{{Code: "P0", Name: "致命", Rank: 0, HealthState: HealthStateFault}},
			Results:  []HealthResultDef{{Code: "healthy", Name: "健康", Level: "P0", Enabled: true}},
			Fallback: "healthy",
			Rules: []HealthRuleDef{
				{Code: "bad", Result: "healthy", Enabled: true, Any: []HealthCondition{{Fact: "no_such_fact", Op: "eq", Value: 1}}},
			},
		})
		cfg, _ := NormalizeHealthGradeConfig(raw)
		if len(cfg.Rules) != 1 || cfg.Rules[0].Enabled {
			t.Error("条件非法的规则应被停用")
		}
	})
}

// TestHealthGradeFactsCoveredByRules 配置里引用的事实必须都在事实字典里
// （动态事实按前缀匹配），否则前端下拉拿不到、文档也解释不了。
func TestHealthGradeFactsCoveredByRules(t *testing.T) {
	cfg := builtinHealthGradeConfig()
	for _, rule := range cfg.Rules {
		for _, cond := range append(append([]HealthCondition{}, rule.Any...), rule.All...) {
			if _, ok := factDef(cond.Fact); !ok {
				t.Errorf("规则 %s 引用了事实字典之外的事实：%s", rule.Code, cond.Fact)
			}
		}
	}
}

// TestHealthGradeSeverityOrdering 多条命中时取最严重的一条作为最终结论。
func TestHealthGradeSeverityOrdering(t *testing.T) {
	bundle := newHealthGradeBundle(builtinHealthGradeConfig())
	card := healthGradeBaseCard()
	// 同时命中 P1（磁盘将满）与 P2（慢查询激增）与 P3（长事务）
	card.Metrics = []MetricSampleView{
		metricSample("res.disk", 97),
		metricSample("eng.slow_queries", 20),
		metricSample("perf.long_trx_count", 3),
	}
	got := evaluateWithBundle(bundle, card)
	if got.Level != "P1" || got.HealthState != HealthStateRisk {
		t.Errorf("多命中应取最严重 P1，实际 %s/%s", got.Level, got.HealthState)
	}
	if len(got.Findings) < 3 {
		t.Errorf("应保留全部命中项（供前端解释），实际 %d 条", len(got.Findings))
	}
	if got.Findings[0].Rank > got.Findings[1].Rank {
		t.Error("命中项应按严重度排序（最严重在前）")
	}
}

// TestDataStaleLimitSecondsFromConfig 数据过期阈值必须来自配置，
// 与"卡片新鲜度判定"共用同一个值，不能各写一份。
func TestDataStaleLimitSecondsFromConfig(t *testing.T) {
	cfg := builtinHealthGradeConfig()
	if got := dataStaleLimitOf(cfg); got != defaultDataStaleSeconds {
		t.Errorf("默认阈值应为 %d，实际 %d", defaultDataStaleSeconds, got)
	}
	// 改配置里的值，阈值必须跟着变 —— 证明卡片新鲜度判定不再持有自己的常量
	for i := range cfg.Rules {
		if cfg.Rules[i].Code != "data_stale" {
			continue
		}
		for j := range cfg.Rules[i].Any {
			if cfg.Rules[i].Any[j].Fact == "freshness_seconds" {
				cfg.Rules[i].Any[j].Value = 300
			}
		}
	}
	if got := dataStaleLimitOf(cfg); got != 300 {
		t.Errorf("配置改成 300 后应读到 300，实际 %d", got)
	}
}

func TestHealthScoreOf(t *testing.T) {
	// 健康：无命中 → 满分
	healthy := HealthScoreOf(HealthGradeResult{HealthState: HealthStateHealthy})
	if healthy == nil || *healthy != 100 {
		t.Fatalf("健康实例应得 100 分，got %v", healthy)
	}
	// 单条 P2（rank 2）→ 88
	p2 := HealthScoreOf(HealthGradeResult{HealthState: HealthStateRisk, Findings: []HealthFinding{{Rank: 2}}})
	if p2 == nil || *p2 != 88 {
		t.Fatalf("单条 P2 应扣 12 得 88，got %v", p2)
	}
	// P1 + P2 → 63
	mixed := HealthScoreOf(HealthGradeResult{HealthState: HealthStateRisk, Findings: []HealthFinding{{Rank: 1}, {Rank: 2}}})
	if mixed == nil || *mixed != 63 {
		t.Fatalf("P1+P2 应扣 37 得 63，got %v", mixed)
	}
	// 多条累计下限 0
	heavy := HealthScoreOf(HealthGradeResult{HealthState: HealthStateFault, Findings: []HealthFinding{{Rank: 0}, {Rank: 0}, {Rank: 0}}})
	if heavy == nil || *heavy != 0 {
		t.Fatalf("多条 P0 应下限 0，got %v", heavy)
	}
	// uncollected → nil（数据不可信不给分，前端显示 —）
	uncollected := HealthScoreOf(HealthGradeResult{HealthState: HealthStateUncollected, Findings: []HealthFinding{{Rank: 2}}})
	if uncollected != nil {
		t.Fatalf("uncollected 应返回 nil，got %v", *uncollected)
	}
}
