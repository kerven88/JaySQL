package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/datatypes"
)

// ============================================================================
// 通知链路的公共工具与上下文
// ============================================================================

// notifyInstanceContext 一条告警所归属实例的画像：策略匹配与消息渲染都靠它。
type notifyInstanceContext struct {
	InstanceID  string
	Name        string // 实例展示名称：Remark 优先，回落 hostname:port，用于消息正文与投递记录展示
	Target      string // 实例IP：hostname:port 连接地址
	DbType      string
	Environment string
	Business    string
	// AlertTemplateID 实例挂载的告警策略模板；策略可按模板限定生效范围
	AlertTemplateID uint64
}

// loadNotifyInstanceContext 按实例 ID 取通知所需的实例画像。
//
// 只取通知必需字段（不复用 dashboard 的大查询）：投递发生在后台 worker，
// 查询必须轻量且可缓存。取不到时返回零值，调用方按"未知"渲染，不阻断通知。
func loadNotifyInstanceContext(instanceID string) notifyInstanceContext {
	ctx := notifyInstanceContext{InstanceID: instanceID}
	if global.App.DB == nil || strings.TrimSpace(instanceID) == "" {
		return ctx
	}

	var row struct {
		InstanceID  string
		Hostname    string
		Port        int
		DbType      string
		Environment string
		Business    string
		Remark      string
	}
	err := global.App.DB.Table("insight_instances AS a").
		Select(`a.instance_id, a.hostname, a.port, a.db_type, a.remark,
			COALESCE(env.name, '') AS environment,
			COALESCE(mt.business, '') AS business`).
		Joins("left join insight_instance_environments env on a.environment = env.id").
		Joins("left join insight_monitor_target_configs mt on mt.instance_id = a.instance_id").
		Where("a.instance_id = ?", instanceID).
		Limit(1).
		Scan(&row).Error
	if err != nil {
		global.App.Log.Warnf("[monitor] 加载实例 %s 通知上下文失败: %v", instanceID, err)
		return ctx
	}

	// 实例IP（地址）与实例名称分离：
	//   Target = hostname:port，永远是连接地址；
	//   Name   = 实例展示名，Remark 优先、回落地址（与 runtime.go 实例展示口径一致）。
	endpoint := row.Hostname
	if row.Port > 0 {
		endpoint = fmt.Sprintf("%s:%d", row.Hostname, row.Port)
	}
	ctx.Target = endpoint
	ctx.Name = firstNonEmpty(strings.TrimSpace(row.Remark), endpoint)
	ctx.DbType = row.DbType
	ctx.Environment = row.Environment
	ctx.Business = row.Business
	return ctx
}

// notifyMetricLayer 判定指标层级（host / engine / kernel）。
//
// 展示目录是层级的唯一事实来源（历史教训：明细页层级与告警模板不一致），
// 因此优先查目录；目录里查不到（如平台自监控项 avail.*）再回落到指标字典的 Scope。
func notifyMetricLayer(dbType, metricKey string) string {
	normalized := canonicalMetricKey(metricKey)
	if normalized == "" {
		return ""
	}
	if entry, ok := displayLayouts[strings.ToLower(strings.TrimSpace(dbType))]; ok {
		for _, item := range entry.Items {
			for _, series := range item.Series {
				if series.MetricKey == "" {
					continue
				}
				if canonicalMetricKey(series.MetricKey) == normalized {
					return item.Tab
				}
			}
		}
	}
	for _, metric := range builtinMetricCatalog {
		if canonicalMetricKey(metric.MetricKey) == normalized {
			return metric.Scope
		}
	}
	return ""
}

// notifyLevelRank 级别序：P0 最高。用于"级别是否升级"的判断。
func notifyLevelRank(level string) int {
	return alertLevelRankOf(level)
}

// ---------- JSON 与基础类型小工具 ----------

func notifyEncodeJSON(value any) datatypes.JSON {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return datatypes.JSON(encoded)
}

// decodeStringList 解 JSON 字符串数组；空/非法一律返回 nil（视为"不限"）。
func decodeStringList(raw datatypes.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// decodeUint64List 解 JSON 数字数组（渠道 ID、模板 ID 等）。
func decodeUint64List(raw datatypes.JSON) []uint64 {
	if len(raw) == 0 {
		return nil
	}
	var values []uint64
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	// 兼容前端把 ID 当字符串提交
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err != nil {
		return nil
	}
	result := make([]uint64, 0, len(asStrings))
	for _, item := range asStrings {
		var parsed uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(item), "%d", &parsed); err == nil && parsed > 0 {
			result = append(result, parsed)
		}
	}
	return result
}

// decodeSubjects 解接收人列表。
func decodeSubjects(raw datatypes.JSON) []monitorModels.NotifySubject {
	if len(raw) == 0 {
		return nil
	}
	var subjects []monitorModels.NotifySubject
	if err := json.Unmarshal(raw, &subjects); err != nil {
		return nil
	}
	cleaned := make([]monitorModels.NotifySubject, 0, len(subjects))
	for _, subject := range subjects {
		subject.Type = strings.ToLower(strings.TrimSpace(subject.Type))
		subject.ID = strings.TrimSpace(subject.ID)
		if subject.Type == "" || subject.ID == "" {
			continue
		}
		cleaned = append(cleaned, subject)
	}
	return cleaned
}

// notifyCfgString 从渠道配置 map 里取字符串值（兼容 string / 数字 / bool）。
func notifyCfgString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%v", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func decodeBoolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateString(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// notifyNowLocal 便于统一注入时间（测试可控）。
func notifyNowLocal() commonModels.LocalTime {
	return commonModels.LocalTime(time.Now())
}
