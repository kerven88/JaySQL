package services

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
)

const maxNotifyTemplateBytes = 16384
const defaultNotifyMessageTemplate = "## {{title}}\n\n- 实例：{{instance_name}}\n- 地址：{{instance_target}}\n- 等级：{{level}} · {{level_label}}\n- 状态：{{state}}\n- 指标：{{metric_name}}\n- 当前值：{{observed_value}}\n- 阈值：{{threshold}}\n- 持续：{{duration}}\n- 时间：{{occurred_at}}\n\n{{advice}}\n\n[查看实例明细]({{url}})"

type notifyTemplateVariable struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var notifyTemplateVariables = []notifyTemplateVariable{
	{"title", "告警标题"}, {"event_id", "告警事件 ID"}, {"instance_id", "实例 ID"}, {"instance_name", "实例名称"},
	{"instance_target", "实例地址"}, {"db_type", "数据库类型"}, {"environment", "环境"}, {"business", "所属业务"},
	{"metric_key", "指标标识"}, {"metric_name", "指标名称"}, {"observed_value", "当前值"}, {"threshold", "阈值"},
	{"level", "告警等级"}, {"level_label", "等级说明"}, {"trigger", "触发阶段"}, {"state", "状态说明"},
	{"triggered_at", "首次触发时间"}, {"occurred_at", "本次通知时间"}, {"duration", "持续时间"}, {"occurrences", "累计次数"},
	{"advice", "处理建议"}, {"operator", "处理人"}, {"url", "实例详情链接"},
}
var notifyVariablePattern = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

func validateNotifyMessageTemplate(value string) error {
	if len(value) > maxNotifyTemplateBytes {
		return fmt.Errorf("消息模板不能超过 16384 字节")
	}
	known := map[string]bool{}
	for _, v := range notifyTemplateVariables {
		known[v.Key] = true
	}
	var invalid string
	rest := notifyVariablePattern.ReplaceAllStringFunc(value, func(match string) string {
		key := notifyVariablePattern.FindStringSubmatch(match)[1]
		if !known[key] {
			invalid = key
		}
		return ""
	})
	if invalid != "" {
		return fmt.Errorf("不支持的消息模板变量：%s", invalid)
	}
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return fmt.Errorf("消息模板变量格式错误，请使用 {{instance_name}} 这样的变量")
	}
	return nil
}

// Variables are literal values, never template instructions or additional Markdown.
func escapeNotifyMarkdown(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r < 128 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
func applyNotifyMessageTemplate(payload notifyTaskPayload, template string) (notifyTaskPayload, error) {
	if strings.TrimSpace(template) == "" || payload.Meta["trigger"] == NotifyTriggerAggregated {
		return payload, nil
	}
	if err := validateNotifyMessageTemplate(template); err != nil {
		return payload, err
	}
	values := map[string]string{"title": payload.Title, "level": payload.Level, "url": payload.URL}
	for k, v := range payload.Meta {
		values[k] = v
	}
	values["title"] = payload.Title
	rendered := interpolateNotifyTemplate(template, values)
	if len(rendered) > 65536 {
		return payload, fmt.Errorf("消息模板渲染结果超过 65536 字节")
	}
	payload.Markdown = rendered
	_, payload.PlainText = notifier.RenderMessageMarkdown(rendered)
	if strings.TrimSpace(payload.PlainText) == "" {
		return payload, fmt.Errorf("消息模板渲染结果不能为空")
	}
	return payload, nil
}

type notifyTemplatePreview struct {
	Markdown        string                   `json:"markdown"`
	PlainText       string                   `json:"plain_text"`
	TelegramHTML    string                   `json:"telegram_html"`
	DefaultTemplate string                   `json:"default_template"`
	Variables       []notifyTemplateVariable `json:"variables"`
}

func notifyTemplateSample(trigger string) notifyTaskPayload {
	now := commonModels.LocalTime(time.Date(2026, 10, 30, 10, 0, 0, 0, time.Local))
	event := monitorModels.InsightMonitorAlertEvents{
		Model: &commonModels.Model{ID: 1}, InstanceID: "demo-instance", Name: "MySQL CPU 使用率告警", MetricKey: "cpu_usage",
		Level: AlertLevelP1, ObservedValue: 92, Threshold: 80, TriggeredAt: now, DurationSec: 300, Occurrences: 3, Advice: "检查高负载进程和慢 SQL。",
	}
	if trigger == "recovered" {
		event.RecoveredAt = &now
	}
	return renderAlertPayload(event, notifyInstanceContext{Name: "dev-mysql", Target: "192.168.50.137:3306", DbType: "MySQL", Environment: "测试环境", Business: "支付中心"}, trigger, "dba", notifier.Mentions{}, nil, "https://jaysql.example.com")
}

// Preview uses the same render function as queued sends and never calls a channel.
func PreviewNotifyMessageTemplate(template, trigger string) (notifyTemplatePreview, error) {
	if trigger == "" {
		trigger = "firing"
	}
	if trigger != "firing" && trigger != "recovered" && trigger != NotifyTriggerAcknowledged {
		return notifyTemplatePreview{}, fmt.Errorf("不支持的预览阶段")
	}
	payload, err := applyNotifyMessageTemplate(notifyTemplateSample(trigger), template)
	if err != nil {
		return notifyTemplatePreview{}, err
	}
	rich, _ := notifier.RenderMessageMarkdown(payload.Markdown)
	return notifyTemplatePreview{Markdown: payload.Markdown, PlainText: payload.PlainText, TelegramHTML: rich, DefaultTemplate: defaultNotifyMessageTemplate, Variables: notifyTemplateVariables}, nil
}
