package services

import (
	"sort"
	"strings"
	"testing"
)

// 探测/断言三合一：打印并校验各引擎默认模板的开箱规则集（defaultAlertRuleSpecsForEngine）。
// 键集合变化时这里会失败 —— 更新期望列表前先确认是「字典/推荐表的有意变更」，
// 而不是目录与字典漂移（Available=false 会让指标静默消失）。
func TestDefaultAlertRuleSpecsForEngine(t *testing.T) {
	expect := map[string][]string{
		// oracle/sqlserver 与 postgresql 的推荐指标覆盖基本一致（资源层 + 连接 + 缓存/锁/长事务）
		"oracle": {
			"avail.up", "cap.conn_used_pct", "perf.cache_hit_ratio", "perf.locks",
			"perf.long_trx_count", "res.cpu", "res.disk", "res.memory",
		},
		"sqlserver": {
			"avail.up", "cap.conn_used_pct", "perf.cache_hit_ratio", "perf.locks",
			"perf.long_trx_count", "res.cpu", "res.disk", "res.memory",
		},
		// tidb 无缓存命中率/锁等待的推荐口径，只有资源层 + 长事务。
		// cap.conn_used_pct 已于 2026-10-27 从 TiDB 退役（max_connections 默认 0，
		// 分母不成立，采集侧恒定 unsupported），开箱规则集同步不再包含它。
		"tidb": {
			"avail.up", "perf.long_trx_count",
			"res.cpu", "res.disk", "res.memory",
		},
	}
	// oracle / sqlserver / tidb 与 mysql 至少共享资源层与命中的推荐指标；
	// 具体集合按目录实际情况断言下限（资源三件套 + 可用性必须有）。
	floor := []string{"avail.up", "res.cpu", "res.disk", "res.memory"}
	for _, engine := range []string{"mysql", "postgresql", "oracle", "sqlserver", "tidb"} {
		specs := defaultAlertRuleSpecsForEngine(engine)
		keys := make([]string, 0, len(specs))
		for _, spec := range specs {
			// 每个档位必须有等级与合法方向
			if len(spec.Levels) == 0 {
				t.Fatalf("%s: 指标 %s 无档位", engine, spec.Key)
			}
			if spec.Operator == "" {
				t.Fatalf("%s: 指标 %s 缺比较符", engine, spec.Key)
			}
			keys = append(keys, spec.Key)
		}
		sort.Strings(keys)
		t.Logf("%s => [%s]", engine, strings.Join(keys, ", "))

		present := map[string]bool{}
		for _, key := range keys {
			present[key] = true
		}
		for _, key := range floor {
			if !present[key] {
				t.Errorf("%s: 开箱规则集缺少必备指标 %s（检查目录 Available 与字典 EngineScope）", engine, key)
			}
		}
		if want, ok := expect[engine]; ok {
			if strings.Join(keys, ",") != strings.Join(want, ",") {
				t.Errorf("%s: 开箱规则集 = [%s]，期望 [%s]", engine, strings.Join(keys, ","), strings.Join(want, ","))
			}
		}
	}
}
