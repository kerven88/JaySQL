package services

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"
)

// monitor_risk.go —— 风险实例的"为什么"与"都有谁"。
//
// 大屏把实例归入 risk 只给了一个结论，值班同学还需要两层信息：
//  1. 下钻：这个实例为什么被判为风险 —— 每一条原因 + 判定依据（阈值/观测值/质量状态）；
//  2. 总览：当前系统里全部风险实例及其概要。
//
// 口径与 classifyScreenCard 严格一致：screen_class == "risk" 的实例才会出现在这里，
// 原因只来自卡片上已有的真实数据（待处理/认领告警 + 采集质量降级），不做任何推断。
//
// 硬规则沿用本包约定：取不到的字段保持 nil / 空串，前端显示「未获取」；
// 绝不用 0 冒充"没算出来"。

// RiskReasonKind 原因类别。
const (
	RiskReasonAlert   = "alert"   // 告警触发（待处理 / 处理中，未忽略）
	RiskReasonCollect = "collect" // 采集质量降级（部分指标不可用 / 连续失败 / 引擎不支持）
)

// RiskReason 单条风险原因。
type RiskReason struct {
	Kind string `json:"kind"` // alert / collect
	// 告警类：等级（P1/P2/P3，空表示无等级）；采集类为空。
	Severity string `json:"severity"`
	// 指标键与字典展示名（采集类可能是空——如"连续失败"不对应单个指标）。
	MetricKey  string `json:"metric_key"`
	MetricName string `json:"metric_name"`
	// 一句话说明（告警消息 / 降级说明）。
	Message string `json:"message"`
	// Basis 判定依据：阈值 vs 观测值，或质量状态说明。纯文字，直接可读。
	Basis string `json:"basis"`
	// 观测值 / 阈值。指针为 nil 表示该原因没有对应的数值依据（如引擎不支持）。
	Observed  *float64 `json:"observed_value"`
	Threshold *float64 `json:"threshold"`
	Unit      string   `json:"unit"`
	// 已持续秒数（告警类）；采集类为 0。
	DurationSec int64  `json:"duration_sec"`
	FirstSeenAt string `json:"first_seen_at"`
}

// RiskInstance 风险实例及其全部原因。
type RiskInstance struct {
	InstanceID      string       `json:"instance_id"`
	Hostname        string       `json:"hostname"`
	Port            int          `json:"port"`
	Target          string       `json:"target"`
	Remark          string       `json:"remark"`
	DbType          string       `json:"db_type"`
	EnvironmentName string       `json:"environment_name"`
	Business        string       `json:"business"`
	GroupName       string       `json:"group_name"`
	ScreenClass     string       `json:"screen_class"`
	StatusMessage   string       `json:"status_message"`
	MaxSeverity     string       `json:"max_severity"` // P1 > P2 > P3；无告警为空
	ReasonCount     int          `json:"reason_count"`
	Reasons         []RiskReason `json:"reasons"`
	// GradeLevel / GradeFindings 健康分级评估的产物：最终等级与命中的结果项清单。
	// 与 Reasons 的区别：Reasons 只覆盖"告警 + 采集降级"两类老口径原因，
	// 这里是配置驱动评估的全量命中项（含慢查询、容量等纯指标类命中）。
	GradeLevel    string          `json:"grade_level"`
	GradeFindings []HealthFinding `json:"grade_findings"`
}

// buildRiskInstances 从已分类的实例卡片中提取全部风险实例及原因。
// 纯函数：只消费卡片上已有的告警与指标样本，不查库、不发外部请求。
func buildRiskInstances(cards []InstanceMonitorCard) []RiskInstance {
	instances := make([]RiskInstance, 0, 8)
	for i := range cards {
		card := cards[i]
		if card.ScreenClass != "risk" {
			continue
		}
		item := RiskInstance{
			InstanceID:      card.InstanceID,
			Hostname:        card.Hostname,
			Port:            card.Port,
			Target:          card.Target,
			Remark:          card.Remark,
			DbType:          card.DbType,
			EnvironmentName: card.EnvironmentName,
			Business:        card.Business,
			GroupName:       card.GroupName,
			ScreenClass:     card.ScreenClass,
			StatusMessage:   card.StatusMessage,
			Reasons:         make([]RiskReason, 0),
		}
		item.Reasons = append(item.Reasons, riskReasonsFromAlerts(card)...)
		item.Reasons = append(item.Reasons, riskReasonsFromCollect(card)...)
		item.ReasonCount = len(item.Reasons)
		item.MaxSeverity = maxSeverityOf(item.Reasons)
		instances = append(instances, item)
	}

	sort.SliceStable(instances, func(left, right int) bool {
		lw, rw := severityWeight(instances[left].MaxSeverity), severityWeight(instances[right].MaxSeverity)
		if lw != rw {
			return lw < rw
		}
		if instances[left].ReasonCount != instances[right].ReasonCount {
			return instances[left].ReasonCount > instances[right].ReasonCount
		}
		return instances[left].Target < instances[right].Target
	})
	return instances
}

// riskReasonsFromAlerts 告警类原因：卡片里的 Alerts 只含 active/acknowledged 且
// 未忽略的事件（见 loadActiveEventMap），本身就是把实例打进 risk 桶的那批告警。
func riskReasonsFromAlerts(card InstanceMonitorCard) []RiskReason {
	reasons := make([]RiskReason, 0, len(card.Alerts))
	for _, alert := range card.Alerts {
		if alert.Ignored {
			continue
		}
		reason := RiskReason{
			Kind:        RiskReasonAlert,
			Severity:    alert.Severity,
			MetricKey:   alert.MetricKey,
			MetricName:  alertMetricName(alert.MetricKey),
			Message:     alert.Message,
			Unit:        metricUnitOf(alert.MetricKey),
			DurationSec: alert.DurationSec,
			FirstSeenAt: alert.FirstSeenAt,
		}
		// 判定依据：规则阈值 vs 实际观测值。阈值/观测值为 0 不代表缺失
		// （如 repl.io_running 期望 ==1），照实给出，方向语义由规则名承载。
		threshold := alert.Threshold
		observed := alert.ObservedValue
		reason.Threshold = &threshold
		reason.Observed = &observed
		reason.Basis = fmt.Sprintf("规则「%s」阈值 %s，当前观测值 %s，已持续 %s",
			alert.Name,
			formatPlainValue(alert.Threshold, reason.Unit),
			formatPlainValue(alert.ObservedValue, reason.Unit),
			humanizeDuration(alert.DurationSec))
		reasons = append(reasons, reason)
	}
	return reasons
}

// riskReasonsFromCollect 采集质量降级类原因。这些状态让 classifyScreenCard
// 把卡片 Status 判为 warning（进而 risk），但不会产生告警事件 —— 必须单独列出，
// 否则"部分指标不可用"的实例在风险明细里一条原因都没有。
func riskReasonsFromCollect(card InstanceMonitorCard) []RiskReason {
	reasons := make([]RiskReason, 0, 4)
	// 采集连续失败：数据可能已过期
	if card.ConsecutiveFailures >= 3 && card.CollectStatus != "failed" {
		basis := fmt.Sprintf("采集已连续失败 %d 次", card.ConsecutiveFailures)
		if card.LastSuccessAt != "" {
			basis += fmt.Sprintf("，最后成功采集 %s", card.LastSuccessAt)
		}
		reasons = append(reasons, RiskReason{
			Kind:    RiskReasonCollect,
			Message: "数据来自上次成功采集，数值可能已过期",
			Basis:   basis,
		})
	}
	// 引擎能力不支持：整个引擎只有连接类指标
	if card.Quality == QualityUnsupported {
		reasons = append(reasons, RiskReason{
			Kind:        RiskReasonCollect,
			MetricKey:   "",
			MetricName:  "",
			Message:     "该引擎当前仅支持连接类指标，其余指标不适用",
			Basis:       "指标质量状态 unsupported：引擎能力注册表中没有该引擎对应指标的采集实现",
			DurationSec: 0,
		})
	}
	// 部分指标不可用：逐个列出 quality != ok 的样本（封顶 20 条防刷屏）
	if card.Quality == "partial" {
		count := 0
		for _, sample := range card.Metrics {
			if sample.Available && sample.Quality == QualityOK {
				continue
			}
			if count >= 20 {
				break
			}
			count++
			reasons = append(reasons, RiskReason{
				Kind:       RiskReasonCollect,
				MetricKey:  sample.Key,
				MetricName: sample.DisplayName,
				Message:    firstNonEmpty(sample.Reason, "该指标当前不可用"),
				Basis: fmt.Sprintf("指标质量状态 %s（%s），未产出可信读数",
					qualityStateLabel(sample.Quality),
					qualityStateBasis(sample.Quality)),
			})
		}
	}
	return reasons
}

// qualityStateLabel 质量状态的中文名。
func qualityStateLabel(quality string) string {
	switch quality {
	case QualityOK:
		return "可信"
	case QualityUnsupported:
		return "不适用"
	case QualityStale:
		return "过期"
	case QualityError:
		return "错误"
	case QualityNoData:
		return "无数据"
	default:
		return "未知"
	}
}

// qualityStateBasis 质量状态的判定口径说明（"依据"部分）。
func qualityStateBasis(quality string) string {
	switch quality {
	case QualityUnsupported:
		return "引擎能力注册表中没有该引擎对应指标的采集实现"
	case QualityStale:
		return "上一轮采集成功但本轮未取到新值，展示的是旧值"
	case QualityError:
		return "采集过程报错"
	case QualityNoData:
		return "从未采集到该指标"
	default:
		return "未登记的质量状态"
	}
}

// alertMetricName 告警关联指标的展示名；无指标或字典缺失时回退 key / 空串。
func alertMetricName(metricKey string) string {
	if metricKey == "" {
		return ""
	}
	name, _, _, _ := metricDisplayName(metricKey)
	if name == "" {
		return metricKey
	}
	return name
}

func maxSeverityOf(reasons []RiskReason) string {
	best := ""
	bestWeight := 99
	for _, reason := range reasons {
		if reason.Kind != RiskReasonAlert {
			continue
		}
		if w := severityWeight(reason.Severity); w < bestWeight {
			bestWeight = w
			best = reason.Severity
		}
	}
	return best
}

// severityWeight P1 最优先；空 / 未知排最后（与 qualityWeight 哨兵同思路）。
func severityWeight(severity string) int {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case "P1":
		return 0
	case "P2":
		return 1
	case "P3":
		return 2
	default:
		return 9
	}
}

// RiskOverviewData /monitor/risks 响应：全部风险实例 + 概要。
type RiskOverviewData struct {
	GeneratedAt string         `json:"generated_at"`
	Total       int            `json:"total"`        // 风险实例数
	ReasonTotal int            `json:"reason_total"` // 风险原因总数
	BySeverity  map[string]int `json:"by_severity"`  // 按 P1/P2/P3 计数（无告警类原因不计入）
	Instances   []RiskInstance `json:"instances"`    // 全部风险实例，已按严重度排序
}

// GetRiskOverviewService 风险总览：所有 screen_class=risk 的实例及其原因明细。
type GetRiskOverviewService struct {
	C interface{}
}

func (s GetRiskOverviewService) Run() (*RiskOverviewData, error) {
	// 总览不做业务线/机房过滤：值班视角是"全系统当前有哪些风险"。
	_, cards, _, _, err := loadMonitorCards(context.Background(), monitorListFilter{})
	if err != nil {
		return nil, err
	}
	for i := range cards {
		cards[i].ScreenClass = evaluateInstanceState(cards[i]).Class
	}
	instances := buildRiskInstances(cards)
	overview := &RiskOverviewData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Total:       len(instances),
		BySeverity:  map[string]int{"P1": 0, "P2": 0, "P3": 0},
		Instances:   instances,
	}
	for _, item := range instances {
		overview.ReasonTotal += item.ReasonCount
		if item.MaxSeverity != "" {
			overview.BySeverity[item.MaxSeverity]++
		}
	}
	if global.App.Log != nil {
		global.App.Log.Infof("risk overview: %d risk instances, %d reasons", overview.Total, overview.ReasonTotal)
	}
	return overview, nil
}

// humanizeDuration 秒数 → "3分05秒" / "9小时13分" / "2天4小时"。
func humanizeDuration(seconds int64) string {
	if seconds <= 0 {
		return "未知时长"
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d天%d小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d小时%d分", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%d分%02d秒", minutes, secs)
	default:
		return strconv.FormatInt(seconds, 10) + "秒"
	}
}
