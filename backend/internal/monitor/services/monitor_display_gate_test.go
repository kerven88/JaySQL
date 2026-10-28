package services

import (
	"testing"
)

// 门控规则本身是"模板勾选 → 明细展示"的契约，改坏了会让已发布模板的实例
// 明细页整页消失或幽灵指标复活，必须钉死。

func TestFilterDisplayByTemplate(t *testing.T) {
	layout := MonitorDisplayLayoutFor("mysql", "standalone")
	if layout == nil {
		t.Fatal("mysql 展示目录不应为 nil")
	}
	// 只勾选两个键：一个主机层、一个引擎层
	tmpl := &metricTemplate{
		Name:   "mini",
		DBType: "mysql",
		Keys:   []string{MetricResCPU, MetricPerfConnCount},
	}
	tmpl.KeySet = map[string]struct{}{}
	for _, k := range tmpl.Keys {
		tmpl.KeySet[k] = struct{}{}
	}

	got := filterDisplayByTemplate(layout, tmpl)
	if got == nil {
		t.Fatal("过滤结果不应为 nil")
	}
	// 全部剩余系列必须命中模板
	for _, item := range got.Items {
		for _, s := range item.Series {
			if !tmpl.contains(s.MetricKey) {
				t.Fatalf("系列 %s 未勾选却仍下发（监控项 %s）", s.MetricKey, item.Key)
			}
		}
	}
	// 剩余 Tab 必须都有监控项
	used := map[string]bool{}
	for _, item := range got.Items {
		used[item.Tab] = true
	}
	for _, tab := range got.Tabs {
		if !used[tab.Key] {
			t.Fatalf("Tab %s 没有监控项却仍下发", tab.Key)
		}
	}
	// CPU 使用率卡应保留、硬盘使用量卡应被剪掉
	foundCPU, foundDiskUsed := false, false
	for _, item := range got.Items {
		if item.Key == "mysql.host.cpu_usage" {
			foundCPU = true
		}
		if item.Key == "mysql.host.disk_used" {
			foundDiskUsed = true
		}
	}
	if !foundCPU {
		t.Fatal("已勾选的 CPU 使用率卡不应被剪掉")
	}
	if foundDiskUsed {
		t.Fatal("未勾选的硬盘使用量卡应被剪掉")
	}
}

func TestFilterDisplayByTemplateKeepsNoSourceSeries(t *testing.T) {
	layout := MonitorDisplayLayoutFor("mysql", "standalone")
	tmpl := &metricTemplate{DBType: "mysql", Keys: []string{}}
	tmpl.KeySet = map[string]struct{}{}
	got := filterDisplayByTemplate(layout, tmpl)
	// 空模板 → 常规系列全部剪掉；MetricKey 为空的行（无采集源）不属于模板治理，保留
	for _, item := range got.Items {
		for _, s := range item.Series {
			if s.MetricKey != "" {
				t.Fatalf("空模板下常规系列 %s 不应下发", s.MetricKey)
			}
		}
	}
}

func TestFilterKeysByTemplate(t *testing.T) {
	tmpl := &metricTemplate{DBType: "mysql"}
	tmpl.KeySet = map[string]struct{}{MetricResCPU: {}}
	got := filterKeysByTemplate([]string{MetricResCPU, MetricPerfQPS, " res.cpu "}, tmpl)
	if len(got) != 2 {
		t.Fatalf("应保留 2 个键（含重复请求），实际 %v", got)
	}
	// nil 模板 = 不治理，原样返回
	if all := filterKeysByTemplate([]string{MetricPerfQPS}, nil); len(all) != 1 {
		t.Fatalf("nil 模板不应过滤，实际 %v", all)
	}
}

func TestDisplayGateTemplateEngineScope(t *testing.T) {
	// TiDB / Doris 不受模板治理（不查库，直接看引擎分支）
	if gate := displayGateTemplate(monitorInstanceRow{InstanceID: "x", DbType: "TiDB"}); gate != nil {
		t.Fatal("TiDB 不应受模板门控")
	}
	if gate := displayGateTemplate(monitorInstanceRow{InstanceID: "x", DbType: "Doris"}); gate != nil {
		t.Fatal("Doris 不应受模板门控")
	}
	// MySQL 走模板解析；测试环境无 DB，模板缓存为空 → nil（不拦截），
	// 这里只锁定"不 panic 且引擎分支正确"
	if gate := displayGateTemplate(monitorInstanceRow{InstanceID: "x", DbType: "MySQL"}); gate != nil {
		t.Fatalf("无模板缓存时不应产生门控，实际 %+v", gate)
	}
}
