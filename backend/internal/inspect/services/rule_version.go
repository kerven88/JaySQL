package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/inspect/models"
)

// ============================================================================
// SQL 规范「规则版本」
//
// 工单规范预审必须落在明确的规则版本上：规则随时可能被启用/停用/改级别/改参数，
// 若预审结果没有版本标记，就无法区分「这次预审是按哪一版规则算的」，
// 撤回工单后重新提交时也无法判断旧结论是否仍然有效。
//
// 版本指纹由规则内容计算（db_type + rule_key + level + params），
// 因此：任一规则变动 → 版本变化；规则改回原值 → 版本回落（内容相同即视为同一版）。
// ============================================================================

// StandardRuleVersion 实例绑定规范在某一时刻的规则版本信息
type StandardRuleVersion struct {
	// Bound 表示实例是否绑定了启用的规范；未绑定时版本为空串，预审不使用规范级别
	Bound        bool   `json:"bound"`
	StandardID   uint64 `json:"standard_id"`
	Version      string `json:"version"`
	RuleCount    int    `json:"rule_count"`
	EnabledCount int    `json:"enabled_count"`
	// CapturedAt 为本次读取规则的时间（不是规则的更新时间），用于追溯「这次预审发生在什么时候」
	CapturedAt string `json:"captured_at"`
}

// InstanceStandardSnapshot 一次加载得到的「规则级别 + 兜底级别 + 规则版本」
//
// 预审链路必须整体使用同一份快照：规则读取、校验执行、结果回写共用它，
// 否则会出现「按旧规则跑审核、按新规则判级」这类跨版本不一致。
type InstanceStandardSnapshot struct {
	Bound           bool
	StandardID      uint64
	Rules           []StandardRuleLevel
	DefaultSeverity string
	Version         StandardRuleVersion
	// DisabledRules 规范中显式关闭（level=off）的规则 key。
	//
	// 关闭的规则不参与级别判定，但「关闭」本身是有意义的用户意图：
	// 致命类错误（语法错误 / 对象已存在 / 对象不存在）在未能命中启用规则时会被强制判为
	// 必须改进，只有拿到这份集合才能区分「规范里没有这条规则」与「用户主动关掉了它」，
	// 从而让对象存在性检查真正可配置（可关闭），而不是永远强制开启。
	DisabledRules map[string]bool
}

// LoadInstanceStandardSnapshot 按实例绑定的规范一次性加载规则级别、兜底级别与规则版本。
// 只查一次库：三者同源，避免多次读取之间规则被修改导致结果不自洽。
func LoadInstanceStandardSnapshot(instanceID string) InstanceStandardSnapshot {
	standardID, dbType, ok := resolveInstanceStandard(instanceID)
	if !ok {
		return InstanceStandardSnapshot{}
	}
	rules := loadStandardRules(standardID, dbType)

	snapshot := InstanceStandardSnapshot{
		Bound:         true,
		StandardID:    standardID,
		Rules:         make([]StandardRuleLevel, 0, len(rules)),
		DisabledRules: make(map[string]bool),
	}
	mustCount, suggestCount := 0, 0
	for _, rule := range rules {
		switch rule.Level {
		case "must":
			mustCount++
		case "suggest":
			suggestCount++
		}
		if rule.Level == "off" {
			snapshot.DisabledRules[rule.RuleKey] = true
			// 同时记下去掉数据库前缀的后缀：引擎侧输出的 rule_key 可能只带后缀
			if idx := strings.LastIndex(rule.RuleKey, "."); idx >= 0 {
				snapshot.DisabledRules[rule.RuleKey[idx+1:]] = true
			}
			continue
		}
		snapshot.Rules = append(snapshot.Rules, StandardRuleLevel{
			RuleKey:     rule.RuleKey,
			Name:        rule.Name,
			Description: rule.Description,
			Level:       rule.Level,
		})
	}
	if len(rules) > 0 {
		switch {
		case mustCount > suggestCount:
			snapshot.DefaultSeverity = "must"
		default:
			snapshot.DefaultSeverity = "suggest"
		}
	}
	snapshot.Version = ComputeStandardRuleVersion(standardID, rules)
	return snapshot
}

// GetInstanceStandardRuleVersion 返回实例绑定规范当前的规则版本
func GetInstanceStandardRuleVersion(instanceID string) StandardRuleVersion {
	return LoadInstanceStandardSnapshot(instanceID).Version
}

// ComputeStandardRuleVersion 依据规则内容计算版本指纹。
// 入参顺序不影响结果：内部先按 (db_type, rule_key) 排序再拼接。
func ComputeStandardRuleVersion(standardID uint64, rules []models.InsightSQLStandardRule) StandardRuleVersion {
	version := StandardRuleVersion{
		Bound:      true,
		StandardID: standardID,
		RuleCount:  len(rules),
	}
	if len(rules) == 0 {
		// 无规则时版本仍需稳定：用「规范ID + 空集」标记，避免每次都算出不同值
		version.Version = fingerprintOf(fmtRuleFingerprintPart(standardID) + "|empty")
		version.CapturedAt = time.Now().Format("2006-01-02 15:04:05")
		return version
	}

	ordered := make([]models.InsightSQLStandardRule, len(rules))
	copy(ordered, rules)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].DbType != ordered[j].DbType {
			return ordered[i].DbType < ordered[j].DbType
		}
		return ordered[i].RuleKey < ordered[j].RuleKey
	})

	var builder strings.Builder
	builder.WriteString(fmtRuleFingerprintPart(standardID))
	for _, rule := range ordered {
		if rule.Level != "off" {
			version.EnabledCount++
		}
		builder.WriteString("|")
		builder.WriteString(rule.DbType)
		builder.WriteString("|")
		builder.WriteString(rule.RuleKey)
		builder.WriteString("|")
		builder.WriteString(rule.Level)
		builder.WriteString("|")
		builder.WriteString(normalizeRuleParams(rule.Params))
	}
	version.Version = fingerprintOf(builder.String())
	version.CapturedAt = time.Now().Format("2006-01-02 15:04:05")
	return version
}

// fmtRuleFingerprintPart 规范ID 参与指纹计算：换绑另一条规范时版本必须变化
func fmtRuleFingerprintPart(standardID uint64) string {
	return "std:" + hex.EncodeToString([]byte(strings.TrimSpace(itoa(standardID))))
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}

// normalizeRuleParams 规范化规则参数：空值统一为 `-`，有效 JSON 压缩掉空白差异
func normalizeRuleParams(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "-"
	}
	var compacted any
	if err := json.Unmarshal([]byte(trimmed), &compacted); err != nil {
		return trimmed
	}
	encoded, err := json.Marshal(compacted)
	if err != nil {
		return trimmed
	}
	return string(encoded)
}

// fingerprintOf 计算指纹（取 sha256 前 16 位十六进制，足够区分且便于日志/界面展示）
func fingerprintOf(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:16]
}
