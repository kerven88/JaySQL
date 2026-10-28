package services

import "testing"

// v$parameter.value 是 VARCHAR2，"0" 表示未设上限。
// 这层解析错了会直接变成"使用率 0%"或"÷0"，所以逐类输入都要钉住。
func TestParseOracleLimitValue(t *testing.T) {
	cases := []struct {
		raw    string
		want   float64
		wantOK bool
		why    string
	}{
		{"300", 300, true, "正常上限"},
		{" 472 ", 472, true, "有空白也要能解析"},
		{"0", 0, false, "0 = 未设上限，不能当有效上限"},
		{"UNLIMITED", 0, false, "非数字"},
		{"", 0, false, "空串"},
		{"-1", 0, false, "负数非法"},
	}
	for _, c := range cases {
		got, ok := parseOracleLimitValue(c.raw)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Fatalf("parseOracleLimitValue(%q) = (%v,%v)，期望 (%v,%v)：%s",
				c.raw, got, ok, c.want, c.wantOK, c.why)
		}
	}
}

// 框架级指标（avail.up / avail.collect_ms）只能由 collectInstanceSamples 产出。
// 引擎层若也上报，同一批次 upsert 会覆盖框架写的 avail.up=1，页面变成"实例不可达"。
func TestIsFrameworkMetric(t *testing.T) {
	for _, key := range []string{MetricAvailUp, MetricAvailCollectMs} {
		if !isFrameworkMetric(key) {
			t.Fatalf("%s 应判定为框架级指标", key)
		}
	}
	for _, key := range []string{MetricPerfConnCount, MetricCapProcessUsedPct, MetricPerfMaxConn} {
		if isFrameworkMetric(key) {
			t.Fatalf("%s 不应判定为框架级指标", key)
		}
	}
}

// 部分实现的引擎会走 unsupportedEngineSamples 批量补"不可用"（SQL Server 已完成全量
// 采集改造、不再走这条路径，ClickHouse 等仍保留），这里同样不能碰框架级指标。
func TestUnsupportedEngineSamplesSkipsFrameworkMetrics(t *testing.T) {
	samples := unsupportedEngineSamples("direct", "SQLServer",
		map[string]float64{MetricPerfConnCount: 3})
	if len(samples) == 0 {
		t.Fatal("应产出占位采样点")
	}
	for _, sample := range samples {
		if isFrameworkMetric(sample.Key) {
			t.Fatalf("引擎层不应产出框架级指标 %s", sample.Key)
		}
	}
	found := false
	for _, sample := range samples {
		if sample.Key == MetricPerfConnCount {
			found = true
			if sample.Quality != QualityOK || sample.Value != 3 {
				t.Fatalf("已采集的连接数应为 ok/3，实际 %s/%v", sample.Quality, sample.Value)
			}
		}
	}
	if !found {
		t.Fatal("已采集的指标不应被丢弃")
	}
}
