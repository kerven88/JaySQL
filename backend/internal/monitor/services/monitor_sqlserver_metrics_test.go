package services

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// 日志文件使用率：Percent Log Used 没有 base 行，必须改用 KB 计数器相除
// ---------------------------------------------------------------------------

func TestSQLServerCounterMaxRatioByPair(t *testing.T) {
	counters := map[string]map[string]float64{
		"Log File(s) Used Size (KB)": {
			"_Total": 3540,
			"master": 1438,
			"model":  623,
			"msdb":   514,
			"tempdb": 489,
		},
		"Log File(s) Size (KB)": {
			"_Total": 20440,
			"master": 2040,
			"model":  8184,
			"msdb":   760,
			"tempdb": 8184,
		},
	}

	got, ok := sqlserverCounterMaxRatioByPair(counters, "Log File(s) Used Size (KB)", "Log File(s) Size (KB)")
	if !ok {
		t.Fatal("期望取到各库最大日志使用率")
	}
	// master 1438/2040 = 70.49%；_Total 的 17.3% 必须被跳过，否则最大值会被总计稀释。
	if math.Abs(got-70.49) > 0.02 {
		t.Errorf("期望 master 的 70.49%%，实际 %.2f%%", got)
	}

	// 缺任一侧计数器都应判"取不到"，而不是拿 0 当结果。
	if _, ok := sqlserverCounterMaxRatioByPair(counters, "Log File(s) Used Size (KB)", "不存在的计数器"); ok {
		t.Error("缺总量计数器时应返回 false")
	}
	if _, ok := sqlserverCounterMaxRatioByPair(counters, "不存在的计数器", "Log File(s) Size (KB)"); ok {
		t.Error("缺已用计数器时应返回 false")
	}

	// 总量为 0 的库必须跳过（除零会得到 +Inf 并污染最大值）。
	zero := map[string]map[string]float64{
		"Log File(s) Used Size (KB)": {"db1": 10},
		"Log File(s) Size (KB)":      {"db1": 0},
	}
	if _, ok := sqlserverCounterMaxRatioByPair(zero, "Log File(s) Used Size (KB)", "Log File(s) Size (KB)"); ok {
		t.Error("总量为 0 时应跳过该库并返回 false")
	}

	// 只有 _Total 时同样算取不到（与旧实现的语义一致）。
	onlyTotal := map[string]map[string]float64{
		"Log File(s) Used Size (KB)": {"_Total": 3540},
		"Log File(s) Size (KB)":      {"_Total": 20440},
	}
	if _, ok := sqlserverCounterMaxRatioByPair(onlyTotal, "Log File(s) Used Size (KB)", "Log File(s) Size (KB)"); ok {
		t.Error("只有 _Total 时应跳过并返回 false")
	}
}

func TestSQLServerCounterMaxPercentLogUsedFallback(t *testing.T) {
	// Percent Log Used 的 cntr_value 本身就是整数百分比，回退路径直接读它。
	counters := map[string]map[string]float64{
		"Percent Log Used": {
			"_Total":              17,
			"master":              70,
			"model":               7,
			"msdb":                67,
			"mssqlsystemresource": 37,
			"tempdb":              5,
		},
	}
	got, ok := sqlserverCounterMax(counters, "Percent Log Used")
	if !ok {
		t.Fatal("期望回退读到 Percent Log Used")
	}
	if got != 70 {
		t.Errorf("期望 70%%，实际 %.0f%%", got)
	}
	if _, ok := sqlserverCounterMax(counters, "Percent Log Used base"); ok {
		t.Error("SQL Server 不暴露 Percent Log Used base，应返回 false")
	}
}

// ---------------------------------------------------------------------------
// 数据文件 IO 延迟：本轮无增量时回退累计均值
// ---------------------------------------------------------------------------

func TestSQLServerIOLatency(t *testing.T) {
	cases := []struct {
		name        string
		deltaStall  float64
		deltaOps    float64
		totalStall  float64
		totalOps    float64
		wantHasData bool
		wantCumul   bool
		wantValue   float64
	}{
		{"本轮有增量：取增量均值", 500, 100, 1669, 316, true, false, 5},
		{"本轮无增量但有累计：回退累计均值", 0, 0, 1669, 316, true, true, 5.282278},
		{"本轮无增量且无累计：判无数据", 0, 0, 0, 0, false, false, 0},
		{"累计有 stall 但没有 ops：不能除零", 0, 0, 1669, 0, false, false, 0},
		{"增量回退成负值时按无增量处理", -3, 0, 879, 613, true, true, 1.433932},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sqlserverIOLatency(c.deltaStall, c.deltaOps, c.totalStall, c.totalOps)
			if got.HasData != c.wantHasData {
				t.Fatalf("HasData 期望 %v，实际 %v", c.wantHasData, got.HasData)
			}
			if got.Cumulative != c.wantCumul {
				t.Fatalf("Cumulative 期望 %v，实际 %v", c.wantCumul, got.Cumulative)
			}
			if !c.wantHasData {
				return
			}
			if math.Abs(got.Value-c.wantValue) > 0.001 {
				t.Errorf("延迟值期望 %.6f，实际 %.6f", c.wantValue, got.Value)
			}
		})
	}
}
