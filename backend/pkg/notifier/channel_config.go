package notifier

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/pkg/secret"
)

// ============================================================================
// 渠道实例配置的编解码（单一实现）
//
// 背景：渠道实例的「哪些字段是密钥」由 notifier.SecretFieldsOf(kind) 决定，
// 加密口径自然也应该由本包定义。原先这套逻辑只有一份，放在
// internal/monitor/services 里且未导出，导致工单模块要读同一张表时只能
// 复制一份 —— 两处加密口径一旦漂移，就会出现「监控能发、工单发不出去」
// 这类极难定位的问题。这里收敛成唯一实现，供监控与工单共用。
// ============================================================================

const (
	// MaskSentinel 密钥回显占位符。前端提交该值时表示"保持原值不变"。
	MaskSentinel = "******"

	// SecretPrefix 密文前缀：既能区分"加密存储的密文"与历史明文，也让迁移期可共存。
	SecretPrefix = "enc:aes:"
)

// DecodeChannelConfig 解密渠道配置里的敏感字段。
//
// 只对带密文前缀的值解密：历史数据里可能存在明文配置，
// 一律尝试解密会把它们变成乱码甚至报错。
func DecodeChannelConfig(raw []byte) (map[string]any, error) {
	cfg := map[string]any{}
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	for key, value := range cfg {
		text, ok := value.(string)
		if !ok || text == "" {
			continue
		}
		if !strings.HasPrefix(text, SecretPrefix) {
			continue
		}
		plain, err := secret.OpenRaw(strings.TrimPrefix(text, SecretPrefix))
		if err != nil {
			return nil, err
		}
		cfg[key] = plain
	}
	return cfg, nil
}

// EncodeChannelConfig 加密敏感字段后序列化。
func EncodeChannelConfig(kind string, cfg map[string]any) ([]byte, error) {
	encoded := map[string]any{}
	for key, value := range cfg {
		encoded[key] = value
	}
	for _, key := range SecretFieldsOf(kind) {
		text, ok := encoded[key].(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		cipherText, err := secret.SealRaw(text)
		if err != nil {
			return nil, fmt.Errorf("加密渠道密钥失败: %w", err)
		}
		encoded[key] = SecretPrefix + cipherText
	}
	return json.Marshal(encoded)
}

// MergeChannelSecrets 处理"留空/传掩码 = 保持原值"的语义。
//
// existingRaw 是数据库中已存的配置（密文形态）；为 nil 表示新建。
func MergeChannelSecrets(kind string, incoming map[string]any, existingRaw []byte) (map[string]any, error) {
	merged := map[string]any{}
	for key, value := range incoming {
		merged[key] = value
	}
	if len(existingRaw) == 0 {
		return merged, nil
	}
	// 前端回显的是掩码，提交时原样带回；此时应保留库里的密文（而不是加密掩码本身）
	if current, err := DecodeChannelConfig(existingRaw); err == nil {
		for _, key := range SecretFieldsOf(kind) {
			value := strings.TrimSpace(cfgText(merged, key))
			if value == "" || value == MaskSentinel {
				if oldValue, ok := current[key]; ok {
					merged[key] = oldValue
				}
			}
		}
	}
	return merged, nil
}

// MaskChannelConfig 按 kind 的密钥字段打码，返回脱敏后的配置与已配置的密钥键列表。
func MaskChannelConfig(kind string, cfg map[string]any) (map[string]any, []string) {
	masked := map[string]any{}
	for key, value := range cfg {
		masked[key] = value
	}
	configured := make([]string, 0)
	for _, key := range SecretFieldsOf(kind) {
		if strings.TrimSpace(cfgText(masked, key)) != "" {
			configured = append(configured, key)
			masked[key] = MaskSentinel
		}
	}
	return masked, configured
}

// cfgText 取配置里的字符串值（非字符串按原样格式化，取不到返回空串）。
func cfgText(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", value)
}
