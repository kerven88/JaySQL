package services

import (
	"strings"
	"testing"
)

// TiDB 与 MySQL 的取数口径差异必须锁死在这里。
//
// 背景：TiDB 的 SHOW GLOBAL STATUS 只有 Uptime / Ssl_* / server_id / ddl_schema_version /
// tidb_gc_*（v7.5.1 实测 9 个变量），MySQL 那套 Com_* / Select_scan / Threads_* /
// Slow_queries / Bytes_* 一个都没有。一旦有人把 isTiDB 门控去掉，这些指标会重新变成
// "ok + 0" 的假数据，页面画出一条贴着 0 的真实曲线 —— 这种回归最难被发现。
func TestMySQLStatusDerivedSkipsTiDB(t *testing.T) {
	// 即便状态变量"齐备"（模拟有人误传了 MySQL 口径的 values），TiDB 也不能产出 Com_*
	values := map[string]float64{
		"Com_delete": 10, "Com_insert": 20, "Com_insert_select": 30,
		"Com_replace": 40, "Com_replace_select": 50, "Com_select": 60,
		"Com_update": 70, "Select_scan": 80,
	}
	rates := map[string]float64{
		MetricEngComDelete: 1, MetricEngComInsert: 1, MetricEngComInsertSelect: 1,
		MetricEngComReplace: 1, MetricEngComReplaceSelect: 1, MetricEngComSelect: 1,
		MetricEngComUpdate: 1, MetricEngSelectScan: 1,
	}
	keys := []string{
		MetricEngComDelete, MetricEngComInsert, MetricEngComInsertSelect,
		MetricEngComReplace, MetricEngComReplaceSelect, MetricEngComSelect,
		MetricEngComUpdate, MetricEngSelectScan,
	}

	builder := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder, values, rates, keys, true)
	if got := len(builder.list()); got != 0 {
		t.Fatalf("TiDB 不应产出 MySQL 状态变量口径的采样点，实际 %d 个：%+v", got, builder.list())
	}

	// MySQL 侧行为保持不变
	builder2 := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder2, values, rates, keys, false)
	if got := len(builder2.list()); got != len(keys) {
		t.Fatalf("MySQL 应产出 %d 个采样点，实际 %d", len(keys), got)
	}
}

// 窗口差分：首轮 / 换窗口 / 计数回退都必须报 no_data，不能算成 0 速率。
func TestTiDBStmtRateWindowSemantics(t *testing.T) {
	if _, reason := tidbStmtRate(1000, 0, false, 15); reason == "" {
		t.Fatal("首轮（窗口未对齐）必须返回原因，不能直接给速率")
	}
	if _, reason := tidbStmtRate(50, 1000, true, 15); reason == "" {
		t.Fatal("计数回退（窗口重置）必须返回原因，不能给出负速率")
	}

	rate, reason := tidbStmtRate(1300, 1000, true, 15)
	if reason != "" {
		t.Fatalf("同窗口正常增长不应有原因，实际 %q", reason)
	}
	if diff := rate - 20; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("(1300-1000)/15 应为 20，实际 %v", rate)
	}

	// elapsed <= 0（同秒内两轮）不算速率
	if _, reason := tidbStmtRate(100, 90, true, 0); reason == "" {
		t.Fatal("采样间隔为 0 时必须返回原因")
	}
}

// tikv_store_status 的容量字段是 varchar，两种形态都要能换算。
func TestParseTiKVSize(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"1073741824", 1073741824},
		{"100GiB", 100 * (1 << 30)},
		{"1.5TiB", 1.5 * (1 << 40)},
		{"512MiB", 512 * (1 << 20)},
		{"200GB", 200e9},
		{"", 0},
		{"  ", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := parseTiKVSize(c.in); got != c.want {
			t.Fatalf("parseTiKVSize(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// TiKV / PD 侧三种失败要给出三种不同的排查方向，不能一律说"7.5+ 已移除"。
func TestTiKVUnavailableReason(t *testing.T) {
	pdReason := tiKVUnavailableReason(errString("Error 1105 (HY000): pd unavailable"))
	if !strings.Contains(pdReason, "PD 不可达") {
		t.Fatalf("pd unavailable 应提示 PD 不可达，实际 %q", pdReason)
	}
	missingReason := tiKVUnavailableReason(errString("Error 1146 (42S02): Table 'information_schema.tikv_store_status' doesn't exist"))
	if !strings.Contains(missingReason, "7.5+ 已移除") {
		t.Fatalf("表不存在应提示版本移除，实际 %q", missingReason)
	}
	otherReason := tiKVUnavailableReason(errString("Error 1045: Access denied"))
	if !strings.Contains(otherReason, "Access denied") {
		t.Fatalf("其他错误应保留原文，实际 %q", otherReason)
	}
}
