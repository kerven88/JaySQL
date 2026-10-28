package services

import (
	"testing"

	"gorm.io/datatypes"
)

// ---------------------------------------------------------------------------
// 采集策略（模板 - 实例两级）的核心判定。
//
// 这些用例钉的是"承诺"而不是实现细节：
//   - 策略项可扩展 = 注册表是唯一入口，新增类型不改表结构；
//   - 实时继承 = 解析结果里每一项都能说清来自模板 / 覆盖 / 平台默认；
//   - 未挂载不等于停采 = 任何情况下都能解析出完整策略。
// ---------------------------------------------------------------------------

func TestStrategyItemTypeSpecsComplete(t *testing.T) {
	if len(strategyItemTypeSpecs) == 0 {
		t.Fatal("策略项注册表为空：新增策略类型的扩展点就没了")
	}
	seen := map[string]struct{}{}
	for _, spec := range strategyItemTypeSpecs {
		if _, ok := seen[spec.Type]; ok {
			t.Errorf("策略项类型重复：%s", spec.Type)
		}
		seen[spec.Type] = struct{}{}

		if spec.Name == "" {
			t.Errorf("策略项 %s 缺少展示名", spec.Type)
		}
		switch spec.Kind {
		case StrategyValueInt:
			if spec.Min > spec.Max {
				t.Errorf("策略项 %s 的取值范围非法：%d > %d", spec.Type, spec.Min, spec.Max)
			}
			if _, ok := spec.Default.(int); !ok {
				t.Errorf("策略项 %s 是 int 类型，Default 必须是 int", spec.Type)
			}
		case StrategyValueEnum:
			if len(spec.Options) == 0 {
				t.Errorf("策略项 %s 是枚举但没给可选项", spec.Type)
			}
			inOptions := false
			for _, option := range spec.Options {
				if option.Value == spec.Default {
					inOptions = true
				}
			}
			if !inOptions {
				t.Errorf("策略项 %s 的默认值 %v 不在可选项里", spec.Type, spec.Default)
			}
		case StrategyValueText:
		default:
			t.Errorf("策略项 %s 使用了未定义的取值类型 %q", spec.Type, spec.Kind)
		}
	}
}

func TestParseStrategyValue(t *testing.T) {
	interval, _ := strategyItemSpec(StrategyItemInterval)
	source, _ := strategyItemSpec(StrategyItemResourceSource)

	if value, err := parseStrategyValue(interval, float64(300)); err != nil || value != 300 {
		t.Errorf("合法的采集周期应通过：value=%v err=%v", value, err)
	}
	if _, err := parseStrategyValue(interval, float64(9999)); err == nil {
		t.Error("超出范围的采集周期必须报错，不能静默截断")
	}
	if _, err := parseStrategyValue(interval, "abc"); err == nil {
		t.Error("非数字的采集周期必须报错")
	}
	if _, err := parseStrategyValue(source, "prometheus"); err != nil {
		t.Errorf("合法的枚举值应通过：%v", err)
	}
	if _, err := parseStrategyValue(source, "whatever"); err == nil {
		t.Error("枚举外的取值必须报错")
	}
}

func TestParseStrategyOverrides(t *testing.T) {
	// overrides 里出现注册表没有的键时直接丢弃：留着它只会显示一个没人消费的配置项
	raw := datatypes.JSON([]byte(`{"scrape_interval":300,"no_such_type":1,"timeout":999}`))
	parsed, types := parseStrategyOverrides(raw)
	if value, ok := parsed[StrategyItemInterval]; !ok || value != 300 {
		t.Errorf("合法覆盖项应保留：%v", parsed)
	}
	if _, ok := parsed["no_such_type"]; ok {
		t.Error("未注册的策略项类型必须丢弃")
	}
	if _, ok := parsed[StrategyItemTimeout]; ok {
		t.Error("值非法的覆盖项必须丢弃，不能带着越界值生效")
	}
	if len(types) != 1 || types[0] != StrategyItemInterval {
		t.Errorf("覆盖项类型清单应只含生效的那一项，实际 %v", types)
	}
}

// 未挂载 / 挂载失效时仍必须解析出一套完整策略 ——
// "没配策略"绝不能退化成"不采集"，这是两级配置最容易踩的坑。
func TestResolveInstanceStrategyFallbackKeepsComplete(t *testing.T) {
	resolved := ResolveInstanceStrategy("ins-1", "mysql", nil, nil)
	if len(resolved.Items) != len(strategyItemTypeSpecs) {
		t.Fatalf("解析结果应覆盖注册表全部策略项，实际 %d / %d", len(resolved.Items), len(strategyItemTypeSpecs))
	}
	if !resolved.Fallback {
		t.Error("未挂载模板时应标记 fallback，页面要据此提示用户")
	}
	if resolved.FallbackReason == "" {
		t.Error("fallback 必须给出原因，否则用户不知道为什么值是这个")
	}
	for _, item := range resolved.Items {
		if item.Source != StrategySourceDefault {
			t.Errorf("无模板时 %s 应回落平台默认，实际来源 %s", item.Type, item.Source)
		}
		if item.ValueText == "" {
			t.Errorf("策略项 %s 缺少可读值", item.Type)
		}
	}
}

// 覆盖优先于模板：这是"实例级特殊照顾"存在的唯一理由，顺序错了整套设计就白做。
func TestResolveInstanceStrategyOverridePrecedence(t *testing.T) {
	raw := datatypes.JSON([]byte(`{"scrape_interval":300}`))
	id := uint64(999999) // 不存在的模板，强制走回退分支，只为验证覆盖仍然生效
	resolved := ResolveInstanceStrategy("ins-1", "mysql", &id, raw)
	value, ok := strategyItemValue(resolved, StrategyItemInterval)
	if !ok || value != 300 {
		t.Fatalf("实例覆盖必须优先于模板，实际 %v", value)
	}
	for _, item := range resolved.Items {
		if item.Type != StrategyItemInterval {
			continue
		}
		if item.Source != StrategySourceOverride {
			t.Errorf("被覆盖的项来源应为 override，实际 %s", item.Source)
		}
	}
}

func TestStrategyDefaultTemplateName(t *testing.T) {
	if got := StrategyDefaultTemplateName("MySQL"); got != "default-mysql" {
		t.Errorf("默认模板标识应归一化到小写引擎键，实际 %q", got)
	}
}
