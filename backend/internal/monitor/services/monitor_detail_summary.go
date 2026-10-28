package services

import (
	"fmt"
	"sort"
	"strings"
)

// monitor_detail_summary.go —— 明细页的「结论先行」层。
//
// 明细页过去把 7 项元信息平铺在头部（采集来源、指标模板、Prometheus 标识、时间戳……），
// 值班同学进页 3 秒内仍然得不到答案。这里把答案压成三件东西：
//  1. screen_class：与 classifyScreenCard 共用同一份口径（故障 / 未采集 / 风险 / 健康）；
//  2. conclusion：一句话结论，逐字由真实原因拼出；
//  3. risk_reasons：风险原因明细，提取逻辑复用 monitor_risk.go，与大屏口径完全一致。
//
// 硬规则：结论必须能追溯到某条真实数据（告警的阈值 / 观测值、采集质量降级）。
// 不允许出现「建议关注」这类没有数据支撑的套话；取不到的字段保持空串 / nil，
// 由前端渲染成「未获取」——这是本包贯穿始终的约定。

// coreTrendKeys 首屏「核心指标带」的指标及展示顺序：资源水位 + 吞吐 + 连接。
// 复制类指标各引擎键名不统一（repl.* / eng.*），硬塞进来会在不支持的引擎上常驻空卡，
// 因此交给指标矩阵与风险原因承载。
var coreTrendKeys = []string{
	MetricResCPU,
	MetricResMemory,
	MetricResDisk,
	MetricPerfQPS,
	MetricPerfTPS,
	MetricPerfConnCount,
	MetricPerfActiveConn,
}

// isCoreTrendKey 判断某个指标是否进首屏核心指标带。
func isCoreTrendKey(key string) bool {
	for _, item := range coreTrendKeys {
		if item == key {
			return true
		}
	}
	return false
}

// coreRank 核心指标带内的排序位次（不在带内返回 -1）。
func coreRank(key string) int {
	for index, item := range coreTrendKeys {
		if item == key {
			return index
		}
	}
	return -1
}

// detailScreenClassWeight 实例列表排序权重：故障最先，健康最后。
//
// 口径说明：未采集（看不见）排在健康之前 —— 已知良好优于未知，
// 但排在风险之后 —— 风险是已经发生的影响，优先级更高。
// 未知分类落到哨兵值 9（思路同 qualityWeight / severityWeight），绝不抢在最前。
func detailScreenClassWeight(class string) int {
	switch class {
	case "fault":
		return 0
	case "risk":
		return 1
	case "uncollected":
		return 2
	case "healthy":
		return 3
	default:
		return 9
	}
}

// sortDetailInstancesByRisk 把「最需要看的」排到列表最前：
// 故障 > 风险 > 未采集 > 健康；同档内待处理告警数多的在前，最后用实例 ID 兜底保证顺序稳定
// （顺序抖动会让值班误以为列表在变）。
func sortDetailInstancesByRisk(items []MonitorDetailListItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		leftWeight := detailScreenClassWeight(left.ScreenClass)
		rightWeight := detailScreenClassWeight(right.ScreenClass)
		if leftWeight != rightWeight {
			return leftWeight < rightWeight
		}
		if left.AlertCount != right.AlertCount {
			return left.AlertCount > right.AlertCount
		}
		return left.InstanceID < right.InstanceID
	})
}

// detailRiskReasons 单实例的原因明细（告警 + 采集降级）。
// 直接从卡片提取，不经过 buildRiskInstances —— 后者只收 risk 实例，
// 而 fault 实例（P0/P1 告警触发）同样需要"为什么"。
func detailRiskReasons(card InstanceMonitorCard) []RiskReason {
	reasons := make([]RiskReason, 0, len(card.Alerts)+4)
	reasons = append(reasons, riskReasonsFromAlerts(card)...)
	reasons = append(reasons, riskReasonsFromCollect(card)...)
	return reasons
}

// topReason 取最严重的一条原因（P1 > P2 > P3 > 无等级），同级保留原有顺序。
func topReason(reasons []RiskReason) RiskReason {
	best := reasons[0]
	bestWeight := severityWeight(best.Severity)
	for _, reason := range reasons[1:] {
		if weight := severityWeight(reason.Severity); weight < bestWeight {
			best = reason
			bestWeight = weight
		}
	}
	return best
}

// countUnavailableMetrics 本轮采集里「没拿到可信读数」的指标数量。
// 直接数卡片上的样本，不用 riskReasons 的长度 —— 后者为防止刷屏封顶 20 条，
// 拿它当"共 N 项"会说少。
func countUnavailableMetrics(card InstanceMonitorCard) int {
	count := 0
	for _, sample := range card.Metrics {
		if sample.Available && sample.Quality == QualityOK {
			continue
		}
		count++
	}
	return count
}

// firstUnavailableSample 给一个好读的样例（第一条不可用指标的名称 + 原因）。
// 纯采集降级的实例，结论需要"规模 + 例子"，而不是把第一条指标的原因当成整句结论。
func firstUnavailableSample(card InstanceMonitorCard, maxRunes int) string {
	for _, sample := range card.Metrics {
		if sample.Available && sample.Quality == QualityOK {
			continue
		}
		name := firstNonEmpty(strings.TrimSpace(sample.DisplayName), strings.TrimSpace(sample.Key))
		reason := strings.TrimSpace(sample.Reason)
		text := name
		if reason != "" {
			if text != "" {
				text += "："
			}
			text += reason
		}
		if text == "" {
			continue
		}
		runes := []rune(text)
		if maxRunes > 0 && len(runes) > maxRunes {
			return string(runes[:maxRunes]) + "…"
		}
		return text
	}
	return ""
}

// reasonsOfKind 按类别筛原因（alert / collect）。
func reasonsOfKind(reasons []RiskReason, kind string) []RiskReason {
	filtered := make([]RiskReason, 0, len(reasons))
	for _, reason := range reasons {
		if reason.Kind == kind {
			filtered = append(filtered, reason)
		}
	}
	return filtered
}

// buildDetailConclusion 一句话结论：进页先给答案，再给依据。
//
// 四种分类各有一套话术，全部由真实字段拼装：
//   - fault：为什么判故障（采集失败次数 / avail.up=0 / 采集报错原文）；
//   - uncollected：为什么看不见（从未采集 / 数据过期 / 滞后超预期）；
//   - risk：有告警时以最严重告警为主线，纯采集降级时先说规模再给样例；
//   - healthy：无告警 + 采集正常（不追加任何没数据支撑的"建议"）。
func buildDetailConclusion(card InstanceMonitorCard, screenClass string, reasons []RiskReason) string {
	statusMessage := strings.TrimSpace(card.StatusMessage)
	switch screenClass {
	case "fault":
		// 告警驱动的故障（P0/P1 未恢复告警）：以最严重告警为主线（与 risk 分支同构）
		alertReasons := reasonsOfKind(reasons, RiskReasonAlert)
		if len(alertReasons) > 0 {
			top := topReason(alertReasons)
			text := firstNonEmpty(strings.TrimSpace(top.Message),
				strings.TrimSpace(top.MetricName), "存在高危告警")
			if top.DurationSec > 0 {
				text += "，已持续 " + humanizeDuration(top.DurationSec)
			}
			return text
		}
		if card.CollectStatus == "failed" {
			text := "采集失败，当前状态不可判"
			if card.ConsecutiveFailures > 0 {
				text = fmt.Sprintf("采集已连续失败 %d 次，当前状态不可判", card.ConsecutiveFailures)
			}
			if statusMessage != "" {
				text += "：" + statusMessage
			}
			return text
		}
		if up := sampleValue(card.Metrics, MetricAvailUp); up.Available && up.Value == 0 {
			return "实例不可达（avail.up = 0），采集无法建立连接"
		}
		return firstNonEmpty(statusMessage, "采集报错，指标不可判")
	case "uncollected":
		switch card.Quality {
		case QualityNoData:
			return "从未采集到数据，指标暂不可判"
		case QualityStale:
			return "数据已过期，展示的是上一次成功采集的旧值"
		}
		if card.CollectStatus == "never" {
			return "尚未执行过采集，指标暂不可判"
		}
		if card.FreshnessSeconds > 0 {
			return fmt.Sprintf("数据已滞后 %s，超出采集周期预期",
				humanizeDuration(int64(card.FreshnessSeconds)))
		}
		return firstNonEmpty(statusMessage, "当前无有效采集数据")
	case "risk":
		alertReasons := reasonsOfKind(reasons, RiskReasonAlert)
		unavailable := countUnavailableMetrics(card)
		if len(alertReasons) == 0 {
			if unavailable == 0 {
				// 兜底：既没有告警也没有不可用指标（理论上不该出现）。
				// 绝不能返回"0 项指标当前不可用"——那读起来像一切正常。
				if len(reasons) > 0 {
					return firstNonEmpty(strings.TrimSpace(reasons[0].Message), statusMessage, "存在风险项")
				}
				return firstNonEmpty(statusMessage, "存在风险项，原因见下方列表")
			}
			// 纯采集降级：先说规模（真实数量），再给一个好读的样例。
			text := fmt.Sprintf("采集质量降级：%d 项指标当前不可用", unavailable)
			if card.ConsecutiveFailures >= 3 {
				text = fmt.Sprintf("采集已连续失败 %d 次，%d 项指标当前不可用",
					card.ConsecutiveFailures, unavailable)
			}
			if sample := firstUnavailableSample(card, 36); sample != "" {
				text += "，如：" + sample
			}
			return text
		}
		top := topReason(alertReasons)
		text := firstNonEmpty(strings.TrimSpace(top.Message),
			strings.TrimSpace(top.MetricName), "存在待认领告警")
		if top.DurationSec > 0 {
			text += "，已持续 " + humanizeDuration(top.DurationSec)
		}
		if unavailable > 0 {
			text += fmt.Sprintf("；另有 %d 项指标不可用", unavailable)
		}
		return text
	}
	return "无待认领告警，采集正常"
}
