package services

import "testing"

// 模板绑定状态是本套改动的判定地基：页面标签、tooltip 文案都由它分叉，
// 一旦退化成单 bool，"未绑定"（健康）又会被标成橙色告警。用例必须钉死。
func TestClassifyTemplateState(t *testing.T) {
	cases := []struct {
		name      string
		boundName string
		fallback  bool
		want      string
	}{
		{"显式绑定且生效", "mysql-default", false, TemplateStateBound},
		{"显式绑定但失效（模板被删/停用）", "my-oltp", true, TemplateStateFallback},
		{"从未绑定（空值）", "", true, TemplateStateUnbound},
		{"旧版占位值 default", "default", true, TemplateStateUnbound},
		{"空值且解析不到默认模板", "", true, TemplateStateUnbound},
	}
	for _, c := range cases {
		if got := classifyTemplateState(c.boundName, c.fallback); got != c.want {
			t.Errorf("%s：classifyTemplateState(%q,%v) = %q，期望 %q", c.name, c.boundName, c.fallback, got, c.want)
		}
	}
}

// 状态枚举的三个取值是前后端契约（前端按字符串匹配渲染标签），改字面量等于破坏契约。
func TestTemplateStateConstantValues(t *testing.T) {
	want := map[string]string{
		TemplateStateBound:    "bound",
		TemplateStateUnbound:  "unbound",
		TemplateStateFallback: "fallback",
	}
	for got, expect := range want {
		if got != expect {
			t.Errorf("模板状态常量值变更：实际 %q，前端契约要求 %q", got, expect)
		}
	}
}

// 模板勾选清单的层级必须与监控明细页的展示目录一致：目录把
// 缓存命中率 / Buffer Pool 使用率 / 锁等待数 / 每秒读行数 这几个
// perf.*/cap.* 键归入 InnoDB 引擎层（kernel），清单若按 key 前缀推导
// 会错标成「数据库层」——正是这四个指标曾被用户指出归类错误。
func TestDisplayLayoutApplicableItemsLayerFollowsLayout(t *testing.T) {
	items := displayLayoutApplicableItems("mysql")
	if len(items) == 0 {
		t.Fatal("mysql 展示目录为空，无法校验层级")
	}
	byKey := make(map[string]string, len(items))
	for _, item := range items {
		byKey[item.MetricKey] = item.Layer
	}
	cases := []struct {
		key  string
		want string
	}{
		{MetricPerfCacheHitRatio, LayerKernel}, // 缓存命中率
		{MetricCapBufferPoolPct, LayerKernel},  // Buffer Pool 使用率
		{MetricPerfLocks, LayerKernel},         // 锁等待数
		{MetricPerfReadRows, LayerKernel},      // 每秒读行数
		{MetricPerfQPS, LayerEngine},           // QPS 仍在数据库层
		{MetricEngComSelect, LayerEngine},      // SELECT 次数仍在数据库层
		{MetricKernRowLockTimeMs, LayerKernel}, // kern.* 前缀不受影响
	}
	for _, c := range cases {
		if got := byKey[c.key]; got != c.want {
			t.Errorf("%s 层级 = %q，期望 %q（应与监控明细页目录一致）", c.key, got, c.want)
		}
	}
}

// PostgreSQL 目录只有主机层与数据库层两个 Tab，不应出现 kernel 行；
// 同时确认目录口径没有把 PG 的 perf.* 键误标成 kernel。
func TestDisplayLayoutApplicableItemsPGNoKernel(t *testing.T) {
	items := displayLayoutApplicableItems("postgresql")
	for _, item := range items {
		if item.Layer == LayerKernel {
			t.Errorf("postgresql 勾选清单出现 kernel 行：%s（%s）", item.MetricKey, item.DisplayName)
		}
	}
}

// 模板弹窗实际走的是指标目录接口（layout=1 → displayLayoutCatalogItems），
// 字典行（有采集源）的 Layer 也必须按展示目录 Tab 覆盖——9-18 那轮只修了
// applicable 路径，这条路径漏了，导致线上弹窗仍把四个指标标成「数据库层」。
func TestDisplayLayoutCatalogItemsLayerFollowsLayout(t *testing.T) {
	items := displayLayoutCatalogItems("mysql")
	if len(items) == 0 {
		t.Fatal("mysql layout=1 目录为空，无法校验层级")
	}
	byKey := make(map[string]string, len(items))
	for _, item := range items {
		byKey[item.MetricKey] = item.Layer
		if item.Layer == "" {
			t.Errorf("%s 的 layer 为空，前端会回退到前缀推导", item.MetricKey)
		}
	}
	cases := []struct {
		key  string
		want string
	}{
		{MetricPerfCacheHitRatio, LayerKernel}, // 缓存命中率
		{MetricCapBufferPoolPct, LayerKernel},  // Buffer Pool 使用率
		{MetricPerfLocks, LayerKernel},         // 锁等待数
		{MetricPerfReadRows, LayerKernel},      // 每秒读行数
		{MetricPerfWriteBytes, LayerHost},      // 协议流入流量（明细页在主机层网络流量）
		{MetricPerfReadBytes, LayerHost},       // 协议流出流量
		{MetricPerfQPS, LayerEngine},           // QPS 仍在数据库层
	}
	for _, c := range cases {
		if got := byKey[c.key]; got != c.want {
			t.Errorf("%s 层级 = %q，期望 %q（layout=1 应与监控明细页目录一致）", c.key, got, c.want)
		}
	}
}
