package services

import "testing"

// TestRuleOutOfScopeGuardsPartialSubmit 守住"部分提交不得误删"这条底线。
//
// 背景：保存接口原本是无条件覆盖式——提交集里没有的档位一律删除。前端一次提交全部
// 监控项时没问题，但任何只提交一部分指标的调用方（脚本、局部保存、未来的按 Tab 保存）
// 都会把模板里其余指标的规则静默抹掉，且调用方完全无感。
// 现在默认走增量口径，只有显式 replace_all 才整模板覆盖。这个测试锁住该行为。
func TestRuleOutOfScopeGuardsPartialSubmit(t *testing.T) {
	submitted := map[string]struct{}{
		"res.cpu": {},
	}

	t.Run("增量口径下未提交的指标不受影响", func(t *testing.T) {
		if ruleOutOfScope(false, submitted, "res.memory") {
			t.Fatal("增量口径：未提交的 res.memory 不应进入删除范围")
		}
		if ruleOutOfScope(false, submitted, "perf.locks") {
			t.Fatal("增量口径：未提交的 perf.locks 不应进入删除范围")
		}
	})

	t.Run("增量口径下已提交的指标可以删档", func(t *testing.T) {
		if !ruleOutOfScope(false, submitted, "res.cpu") {
			t.Fatal("增量口径：已提交的 res.cpu 应进入删除范围（用于删除被移除的档位）")
		}
	})

	t.Run("裸名键按规范键对齐", func(t *testing.T) {
		// 库里可能存在历史裸名键 cpu，提交的是 res.cpu，二者必须视作同一指标
		if !ruleOutOfScope(false, submitted, "cpu") {
			t.Fatal("裸名键 cpu 应归一到 res.cpu 后判定为已提交")
		}
	})

	t.Run("全量口径下未提交的指标进入删除范围", func(t *testing.T) {
		if !ruleOutOfScope(true, submitted, "res.memory") {
			t.Fatal("全量口径：未提交的 res.memory 应被删除")
		}
	})
}

// TestTombstoneDiffForSave 锁住墓碑的立/撤判定。
//
// 默认模板的规则由平台播种并持续补齐；用户删掉的告警若不留凭证，下个周期会被
// 原样加回，删除形同虚设。墓碑只记"保存前有档、保存后没档"的指标——
// 本来就没配置的指标不立碑，保证平台新增的默认规则仍能播种到存量环境。
func TestTombstoneDiffForSave(t *testing.T) {
	catalog := map[string]MetricDescriptor{
		"res.cpu":    {MetricKey: "res.cpu", DisplayName: "CPU 使用率"},
		"res.memory": {MetricKey: "res.memory", DisplayName: "内存使用率"},
		"res.disk":   {MetricKey: "res.disk", DisplayName: "磁盘使用率"},
	}

	t.Run("删光的指标立碑", func(t *testing.T) {
		insert, remove := tombstoneDiffForSave(catalog,
			[]string{"res.cpu", "res.memory"},
			[]string{"res.memory"})
		if len(insert) != 1 || insert[0] != "res.cpu" {
			t.Fatalf("res.cpu 被删光应立碑，got insert=%v", insert)
		}
		// remove 的口径是"保存后仍有档的键"（撤碑在库层是幂等空操作），
		// 这里只要求 insert 精确，remove 不应包含被删光的 res.cpu。
		for _, key := range remove {
			if key == "res.cpu" {
				t.Fatal("res.cpu 已被删光，不应出现在撤碑列表")
			}
		}
	})

	t.Run("重新配置档位即撤碑", func(t *testing.T) {
		insert, remove := tombstoneDiffForSave(catalog,
			[]string{"res.memory"},
			[]string{"res.cpu", "res.memory"})
		if len(insert) != 0 {
			t.Fatalf("res.cpu 重新有档不应立碑，got insert=%v", insert)
		}
		found := false
		for _, key := range remove {
			if key == "res.cpu" {
				found = true
			}
		}
		if !found {
			t.Fatalf("res.cpu 重新有档应进入撤碑列表，got remove=%v", remove)
		}
	})

	t.Run("保存前后不变则不立碑", func(t *testing.T) {
		insert, _ := tombstoneDiffForSave(catalog,
			[]string{"res.cpu", "res.memory"},
			[]string{"res.memory", "res.cpu"})
		if len(insert) != 0 {
			t.Fatalf("无删除不应立碑，got insert=%v", insert)
		}
	})

	t.Run("字典外指标不立碑（平台清理不是用户删除）", func(t *testing.T) {
		insert, _ := tombstoneDiffForSave(catalog,
			[]string{"res.cpu", "legacy.gone"},
			[]string{"res.cpu"})
		if len(insert) != 0 {
			t.Fatalf("字典外键 legacy.gone 不应立碑，got insert=%v", insert)
		}
	})

	t.Run("重复键去重", func(t *testing.T) {
		insert, _ := tombstoneDiffForSave(catalog,
			[]string{"res.cpu", "res.cpu", "res.memory"},
			[]string{"res.memory"})
		if len(insert) != 1 {
			t.Fatalf("重复键应去重，got insert=%v", insert)
		}
	})
}
