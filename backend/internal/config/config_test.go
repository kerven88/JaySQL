package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// TestViperReadsRepositoryConfig 完整复现启动路径：viper 读真实 config.yaml + Unmarshal。
// 顶层键重复时 ReadInConfig 会直接返回 "mapping key ... already defined"，
// 与启动 panic 的报错一一对应，这条用例就是该故障的回归防线。
func TestViperReadsRepositoryConfig(t *testing.T) {
	v := viper.New()
	v.SetConfigFile(filepath.Join("..", "..", "config.yaml"))
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("viper 读取 config.yaml 失败（顶层键重复会在此暴露）: %v", err)
	}
	var cfg Configuration
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatalf("viper 反序列化失败: %v", err)
	}
	got := cfg.AISettings()
	if !got.Enabled {
		t.Fatalf("config.yaml 中 ai.enabled 应为 true，实际 %+v", got)
	}
	if got.RequestTimeoutSecond != 60 {
		t.Fatalf("ai.request_timeout_seconds 应为 60，实际 %d", got.RequestTimeoutSecond)
	}
}

// TestConfigFilesParseable 校验仓库里的配置文件语法合法。
//
// 这条用例存在的理由：配置文件里出现重复的 mapping key（例如连续两段 `ai:`）
// 会让 viper 在启动阶段直接 panic "mapping key already defined"，
// 而 go build 完全发现不了。这里用与 viper 同源的 yaml 解析器做静态把关。
func TestConfigFilesParseable(t *testing.T) {
	files := []string{"config.yaml", "config-linux.yaml", "config.yaml.template"}
	for _, name := range files {
		path := filepath.Join("..", "..", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		var out map[string]any
		if err := yaml.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s 解析失败（重复键或缩进错误会在此暴露）: %v", name, err)
		}
		if _, ok := out["ai"]; !ok {
			t.Fatalf("%s 缺少 ai 节点", name)
		}
	}
}

// TestAISettingsDefaults 老配置文件无 ai 节点时，零值必须被安全默认值兜底，
// 否则 request_timeout_seconds=0 会让 HTTP 客户端立刻超时。
func TestAISettingsDefaults(t *testing.T) {
	cfg := Configuration{}
	got := cfg.AISettings()
	if got.RequestTimeoutSecond != 60 {
		t.Fatalf("超时默认值应为 60，实际 %d", got.RequestTimeoutSecond)
	}
	if got.HealthCheckInterval != "*/5 * * * *" {
		t.Fatalf("健康检查周期默认值应为 */5 * * * *，实际 %q", got.HealthCheckInterval)
	}
}

// TestAISettingsKeepsExplicitValues 配置里显式写的值不能被默认值覆盖。
func TestAISettingsKeepsExplicitValues(t *testing.T) {
	var cfg Configuration
	src := []byte(`
ai:
  enabled: true
  allow_insecure_http: true
  failover_enabled: false
  degrade_to_rule_engine: true
  health_check_interval: "*/10 * * * *"
  request_timeout_seconds: 120
`)
	if err := yaml.Unmarshal(src, &cfg); err != nil {
		t.Fatalf("解析 ai 节点失败: %v", err)
	}
	got := cfg.AISettings()
	if !got.Enabled || !got.AllowInsecureHTTP || !got.DegradeToRuleEngine {
		t.Fatalf("布尔开关未正确解析: %+v", got)
	}
	if got.FailoverEnabled {
		t.Fatalf("failover_enabled=false 应保持 false")
	}
	if got.RequestTimeoutSecond != 120 {
		t.Fatalf("显式超时 120 被覆盖为 %d", got.RequestTimeoutSecond)
	}
	if got.HealthCheckInterval != "*/10 * * * *" {
		t.Fatalf("显式周期被覆盖为 %q", got.HealthCheckInterval)
	}
}

// TestRepositoryConfigHasAIKeysKept 防止再次出现重复插入：
// 顶层 ai 键在每份配置中必须恰好出现一次。
func TestRepositoryConfigHasAIKeysKept(t *testing.T) {
	for _, name := range []string{"config.yaml", "config-linux.yaml", "config.yaml.template"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		junk := map[string]any{}
		if err := yaml.Unmarshal(raw, &junk); err != nil {
			t.Fatalf("%s 存在重复的顶层键: %v", name, err)
		}
	}
}
