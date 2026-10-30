package services

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
)

// ============================================================================
// 消息渲染：把告警事件渲染成各渠道可消费的消息
//
// 同一份信息渲染成三种形态，由渠道按能力取用：
//   - Markdown：钉钉 / 飞书 / 企业微信（带层级、加粗，可读性最好）
//   - PlainText：邮件 / 短信（短信再按长度上限截断）
//   - Meta：一级键值对，供后续自定义模板复用
// ============================================================================

// notifyTaskPayload 落在投递任务里的渲染结果（渠道层直接消费，不再回查事件）。
type notifyTaskPayload struct {
	Title      string               `json:"title"`
	Markdown   string               `json:"markdown"`
	PlainText  string               `json:"plain_text"`
	Level      string               `json:"level"`
	URL        string               `json:"url,omitempty"`
	Meta       map[string]string    `json:"meta,omitempty"`
	Mentions   notifier.Mentions    `json:"mentions,omitempty"`
	Recipients []notifier.Recipient `json:"recipients,omitempty"`
}

// toMessage 转成渠道消息结构。
func (p notifyTaskPayload) toMessage() notifier.Message {
	return notifier.Message{
		Title:      p.Title,
		Markdown:   p.Markdown,
		PlainText:  p.PlainText,
		Level:      p.Level,
		URL:        p.URL,
		Recipients: p.Recipients,
		Mentions:   p.Mentions,
		Meta:       p.Meta,
	}
}

// renderAlertPayload 渲染告警消息。trigger 为 firing / recovered / acknowledged（认领回执）。
// operator 仅在 trigger=acknowledged 时有意义（认领人用户名，用于回执文案）。
func renderAlertPayload(
	event monitorModels.InsightMonitorAlertEvents,
	instance notifyInstanceContext,
	trigger string,
	operator string,
	mentions notifier.Mentions,
	recipients []notifier.Recipient,
	noticeURL string,
) notifyTaskPayload {
	level := normalizeNotifyLevel(event.Level)
	levelLabel := alertLevelLabels[level]
	if levelLabel == "" {
		levelLabel = level
	}
	metricName := notifyMetricDisplayName(event.MetricKey)
	if metricName == "" {
		metricName = event.MetricKey
	}
	instanceLabel := instance.Name
	if instanceLabel == "" {
		instanceLabel = event.InstanceID
	}

	acked := strings.EqualFold(trigger, NotifyTriggerAcknowledged)
	recovered := strings.EqualFold(trigger, "recovered")
	stateText := "触发"
	titleSuffix := ""
	switch {
	case recovered:
		stateText = "恢复"
		titleSuffix = "（已恢复）"
	case acked:
		stateText = "认领"
		who := strings.TrimSpace(operator)
		if who == "" {
			who = "值班人员"
		}
		titleSuffix = fmt.Sprintf("（已被 %s 认领）", who)
	}
	// 告警名（规则名）里带比较符（如「MySQL 复制 IO 线程 高 != 1」），
	// 通知是给人看的终态文案，这里统一转中文（枚举值本身不动）。
	// 标题不再出现告警等级标识（2026-09-25）：外层「[P1]」前缀去掉，
	// 规则名内嵌的等级段（P0–P3）也剥掉，其余文字保持不变。
	// 等级信息仍保留在 payload.Level 与 Meta.level，供渠道侧按需使用。
	title := fmt.Sprintf("%s%s", operatorToChinese(stripAlertLevelToken(event.Name)), titleSuffix)

	triggeredAt := time.Time(event.TriggeredAt).Format("2006-01-02 15:04:05")
	occurredAt := triggeredAt
	if recovered {
		if event.RecoveredAt != nil {
			occurredAt = time.Time(*event.RecoveredAt).Format("2006-01-02 15:04:05")
		} else {
			occurredAt = time.Now().Format("2006-01-02 15:04:05")
		}
	} else if acked {
		occurredAt = time.Now().Format("2006-01-02 15:04:05")
	}

	durationText := formatNotifyDuration(event.DurationSec)

	var detail []string
	// 列布局（三渠道统一）：实例名称 → 实例IP → 告警等级 → 归属 → 指标 →
	// 当前值 → 持续 → 累计 → 触发时间 →（处理人/后续）→ 建议。
	// 名称+地址先定位"哪台实例"，等级紧随其后回答"多严重"，再往下读细节。
	detail = append(detail, fmt.Sprintf("实例名称：%s", instanceLabel))
	if strings.TrimSpace(instance.Target) != "" {
		detail = append(detail, fmt.Sprintf("实例IP：%s", instance.Target))
	}
	detail = append(detail, fmt.Sprintf("告警等级：%s · %s", strings.ToUpper(level), levelLabel))
	if instance.Environment != "" || instance.DbType != "" || instance.Business != "" {
		scope := make([]string, 0, 3)
		if instance.DbType != "" {
			scope = append(scope, instance.DbType)
		}
		if instance.Environment != "" {
			scope = append(scope, instance.Environment)
		}
		if instance.Business != "" {
			scope = append(scope, instance.Business)
		}
		detail = append(detail, fmt.Sprintf("归属：%s", strings.Join(scope, " / ")))
	}
	detail = append(detail, fmt.Sprintf("指标：%s", metricName))
	if !recovered {
		detail = append(detail, fmt.Sprintf("当前值：%s（阈值 %s %s）",
			formatNotifyValue(event.ObservedValue), notifyThresholdOperator(), formatNotifyValue(event.Threshold)))
	}
	detail = append(detail, fmt.Sprintf("持续：%s", durationText))
	detail = append(detail, fmt.Sprintf("累计：%d 次", event.Occurrences))
	detail = append(detail, fmt.Sprintf("%s时间：%s", stateText, occurredAt))
	if acked {
		who := strings.TrimSpace(operator)
		if who == "" {
			who = "值班人员"
		}
		detail = append(detail, fmt.Sprintf("处理人：%s", who))
		detail = append(detail, "后续：恢复前不再重复发送本条告警；指标恢复后会再次通知")
	}
	if strings.TrimSpace(event.Advice) != "" && !recovered {
		detail = append(detail, fmt.Sprintf("建议：%s", event.Advice))
	}

	url := ""
	if strings.TrimSpace(noticeURL) != "" && event.InstanceID != "" {
		url = fmt.Sprintf("%s/monitor/detail?instance_id=%s", strings.TrimRight(noticeURL, "/"), event.InstanceID)
	}

	var markdown strings.Builder
	fmt.Fprintf(&markdown, "### %s\n\n", title)
	for _, line := range detail {
		fmt.Fprintf(&markdown, "- %s\n", line)
	}
	if url != "" {
		fmt.Fprintf(&markdown, "\n[查看实例明细](%s)\n", url)
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n", title)
	for _, line := range detail {
		fmt.Fprintf(&plain, "%s\n", line)
	}
	if url != "" {
		fmt.Fprintf(&plain, "查看详情：%s\n", url)
	}

	return notifyTaskPayload{
		Title:      title,
		Markdown:   markdown.String(),
		PlainText:  strings.TrimRight(plain.String(), "\n"),
		Level:      level,
		URL:        url,
		Mentions:   mentions,
		Recipients: recipients,
		Meta: map[string]string{
			"event_id":        fmt.Sprintf("%d", event.ID),
			"title":           title,
			"instance_name":   instanceLabel,
			"instance_target": instance.Target,
			"db_type":         instance.DbType,
			"environment":     instance.Environment,
			"business":        instance.Business,
			"metric_name":     metricName,
			"observed_value":  formatNotifyValue(event.ObservedValue),
			"threshold":       formatNotifyValue(event.Threshold),
			"level_label":     levelLabel,
			"state":           stateText,
			"triggered_at":    triggeredAt,
			"occurred_at":     occurredAt,
			"duration":        durationText,
			"occurrences":     fmt.Sprintf("%d", event.Occurrences),
			"advice":          event.Advice,
			"operator":        operator,
			"url":             url,
			"instance_id":     event.InstanceID,
			"metric_key":      event.MetricKey,
			"level":           level,
			"trigger":         trigger,
		},
	}
}

func formatNotifyDuration(seconds int64) string {
	if seconds <= 0 {
		return "刚刚"
	}
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%d 分钟", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%.1f 小时", float64(seconds)/3600)
	}
	return fmt.Sprintf("%.1f 天", float64(seconds)/86400)
}

func formatNotifyValue(value float64) string {
	// 通知面向人，不做科学计数：大数用千分位更易读
	text := fmt.Sprintf("%.2f", value)
	parts := strings.Split(text, ".")
	if len(parts) == 2 {
		integer := parts[0]
		sign := ""
		if strings.HasPrefix(integer, "-") {
			sign = "-"
			integer = integer[1:]
		}
		var builder strings.Builder
		for index, char := range integer {
			if index > 0 && (len(integer)-index)%3 == 0 {
				builder.WriteString(",")
			}
			builder.WriteRune(char)
		}
		text = sign + builder.String() + "." + parts[1]
	}
	return text
}

func notifyMetricDisplayName(metricKey string) string {
	normalized := canonicalMetricKey(metricKey)
	if normalized == "" {
		return ""
	}
	for _, metric := range builtinMetricCatalog {
		if canonicalMetricKey(metric.MetricKey) == normalized {
			return metric.DisplayName
		}
	}
	return ""
}

// notifyThresholdOperator 阈值比较符。
//
// 事件表当前没有比较符列（比较符只存在于规则里），通知正文统一按"达到或超过"表达，
// 这是平台绝大多数规则的语义；比较符细化属于二期（事件表加列后由规则带入）。
func notifyThresholdOperator() string {
	return "大于等于"
}

// alertLevelTokenRe 匹配独立的告警等级标识（P0–P3）。
// 规则保存时等级段被拼进规则名（如「MySQL 复制 SQL 线程 P1 != 1」），
// 通知标题不再展示等级，这里把它剥掉；\b 边界保证不误伤 P99 这类词。
var alertLevelTokenRe = regexp.MustCompile(`\s*\bP[0-3]\b`)

// stripAlertLevelToken 去掉文本中的告警等级标识及其紧邻空白。
func stripAlertLevelToken(text string) string {
	return strings.TrimSpace(alertLevelTokenRe.ReplaceAllString(text, ""))
}

// operatorToChinese 把自由文本里的比较符转成中文，用于通知这类「生成即人读」的文案。
//
// 只替换多字符符号 >= <= !=：单字符 > < = 不替换 ——
// 指标名与采集 SQL 里有「空闲事务数(>1s)」「state=active」这类原文，
// 一起替换会扭曲原意。与前端 utils.js 的 operatorTextInText 保持同一套规则。
func operatorToChinese(text string) string {
	if text == "" {
		return text
	}
	replacer := strings.NewReplacer(
		">=", "大于等于",
		"<=", "小于等于",
		"!=", "不等于",
	)
	return replacer.Replace(text)
}
