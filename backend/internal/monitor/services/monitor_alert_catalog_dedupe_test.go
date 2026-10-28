package services

import "testing"

// TestDedupeCatalogRowsByMetric 守住"一指标一组"的契约。
//
// 现场问题：监控明细目录里 perf.conn_count 同时挂在「会话连接」与「线程」两个明细项下，
// 逐条出清单会让配置抽屉里出现两个同 metric_key 的分组。前端一次提交全部监控项时
// 后端直接判"指标 perf.conn_count 重复提交"，配置页整个存不下去。
func TestDedupeCatalogRowsByMetric(t *testing.T) {
	t.Run("真实目录不再出现重复指标", func(t *testing.T) {
		rows := displayLayoutTemplateItems("MySQL")
		if len(rows) == 0 {
			t.Fatal("MySQL 展示目录不应为空")
		}
		seen := map[string]string{}
		dups := 0
		for _, row := range rows {
			key := canonicalMetricKey(row.MetricKey)
			if key == "" {
				continue
			}
			if prev, ok := seen[key]; ok {
				dups++
				t.Logf("目录里 %s 同时属于「%s」与「%s」", key, prev, row.ItemName)
				continue
			}
			seen[key] = row.ItemName
		}
		if dups == 0 {
			t.Skip("当前目录没有重复指标，用例失去意义（仍保留以防回归）")
		}

		// 归并后的清单不允许再有重复键
		meta := make([]MetricCatalogItem, 0, len(rows))
		for _, row := range rows {
			meta = append(meta, MetricCatalogItem{
				MetricKey:   row.MetricKey,
				DisplayName: row.SeriesKey,
				Layer:       row.Tab,
				Available:   row.MetricKey != "",
			})
		}
		merged := dedupeCatalogRowsByMetric(meta)
		again := map[string]int{}
		for _, row := range merged {
			again[canonicalMetricKey(row.MetricKey)]++
		}
		for key, count := range again {
			if count > 1 {
				t.Fatalf("归并后 %s 仍出现 %d 次", key, count)
			}
		}
		if len(merged) != len(seen) {
			t.Fatalf("归并后应剩 %d 条唯一指标，实际 %d 条", len(seen), len(merged))
		}
	})

	t.Run("可采集的重复项顶替不可采集的先出现项", func(t *testing.T) {
		rows := []MetricCatalogItem{
			{MetricKey: "perf.conn_count", DisplayName: "连接数", Available: false},
			{MetricKey: "perf.conn_count", DisplayName: "连接数", Available: true},
		}
		merged := dedupeCatalogRowsByMetric(rows)
		if len(merged) != 1 {
			t.Fatalf("应归并为 1 条，实际 %d 条", len(merged))
		}
		if !merged[0].Available {
			t.Fatal("可采集的那条应当顶替不可采集的先出现项，否则能配的指标会被标成不可配")
		}
	})

	t.Run("先出现的可采集项不被后出现的不可采集项顶掉", func(t *testing.T) {
		rows := []MetricCatalogItem{
			{MetricKey: "perf.conn_count", Available: true},
			{MetricKey: "perf.conn_count", Available: false},
		}
		merged := dedupeCatalogRowsByMetric(rows)
		if len(merged) != 1 || !merged[0].Available {
			t.Fatal("已可采集的行不应被不可采集的重复项降级")
		}
	})

	// 2026-09-25 现场问题：明细页「线程」卡片与「会话连接」卡片共享 perf.conn_count /
	// perf.active_conn_count，规则配置里同键条目只剩「会话连接」归属——
	// 用户在明细页看到「线程」、到规则配置搜「线程」一无所得。
	t.Run("共享键的归属明细项合并而非丢弃", func(t *testing.T) {
		rows := []MetricCatalogItem{
			{MetricKey: "perf.conn_count", DisplayName: "连接数", DetailItem: "会话连接", Available: true},
			{MetricKey: "perf.conn_count", DisplayName: "连接数", DetailItem: "线程", Available: true},
		}
		merged := dedupeCatalogRowsByMetric(rows)
		if len(merged) != 1 {
			t.Fatalf("应归并为 1 条，实际 %d 条", len(merged))
		}
		if merged[0].DetailItem != "会话连接、线程" {
			t.Fatalf("归属应合并为「会话连接、线程」，实际 %q", merged[0].DetailItem)
		}
	})

	t.Run("顶替发生时已累积的归属与说明不丢失", func(t *testing.T) {
		rows := []MetricCatalogItem{
			{MetricKey: "perf.conn_count", DetailItem: "会话连接", Available: false, Advice: "首行说明"},
			{MetricKey: "perf.conn_count", DetailItem: "线程", Available: true},
		}
		merged := dedupeCatalogRowsByMetric(rows)
		if len(merged) != 1 {
			t.Fatalf("应归并为 1 条，实际 %d 条", len(merged))
		}
		if merged[0].DetailItem != "会话连接、线程" {
			t.Fatalf("顶替后归属应保留，实际 %q", merged[0].DetailItem)
		}
		if merged[0].Advice != "首行说明" {
			t.Fatalf("顶替后说明应保留，实际 %q", merged[0].Advice)
		}
	})

	t.Run("首行说明为空时用后行补齐", func(t *testing.T) {
		rows := []MetricCatalogItem{
			{MetricKey: "perf.conn_count", DetailItem: "会话连接", Available: true},
			{MetricKey: "perf.conn_count", DetailItem: "线程", Available: true, Advice: "线程/连接概念边界说明"},
		}
		merged := dedupeCatalogRowsByMetric(rows)
		if len(merged) != 1 {
			t.Fatalf("应归并为 1 条，实际 %d 条", len(merged))
		}
		if merged[0].Advice != "线程/连接概念边界说明" {
			t.Fatalf("空说明应被后行补齐，实际 %q", merged[0].Advice)
		}
	})

	t.Run("空键跳过", func(t *testing.T) {
		rows := []MetricCatalogItem{
			{MetricKey: "", Available: true},
			{MetricKey: "res.cpu", Available: true},
		}
		merged := dedupeCatalogRowsByMetric(rows)
		if len(merged) != 1 || merged[0].MetricKey != "res.cpu" {
			t.Fatalf("空键应跳过，实际 %+v", merged)
		}
	})
}
