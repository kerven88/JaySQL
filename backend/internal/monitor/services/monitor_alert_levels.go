package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 告警等级模型（P0–P3）与档位校验
//
// 背景：旧模型是"一个指标一条规则"，等级用中文 高/中/低 表示（评估时映射成
// P1/P2/P3，见 monitor_alerts.go）。业务侧需要按运维语言直接表达 P0–P3 四档，
// 且允许同一指标只配其中若干档（阶梯阈值，越严重阈值越极端）。
//
// 本文件是等级语义的唯一事实来源：
//   - 等级取值与排序（P0 最高）；
//   - 中文旧值与 P 码的双向归一化（存量数据兼容）；
//   - 档位集合的硬校验（阻断保存）与软校验（提示不阻断）；
//   - 每个指标的推荐档位（默认模板播种 + 前端「恢复推荐值」共用同一份数据）。
// ---------------------------------------------------------------------------

const (
	AlertLevelP0 = "P0" // 紧急：服务已不可用或即将不可用
	AlertLevelP1 = "P1" // 严重：核心能力受损
	AlertLevelP2 = "P2" // 一般：有风险需关注
	AlertLevelP3 = "P3" // 提示：趋势提醒
)

// AlertLevelOrder 由高到低。评估时按此顺序匹配并短路（命中最高档）。
var AlertLevelOrder = []string{AlertLevelP0, AlertLevelP1, AlertLevelP2, AlertLevelP3}

var alertLevelRank = map[string]int{
	AlertLevelP0: 0,
	AlertLevelP1: 1,
	AlertLevelP2: 2,
	AlertLevelP3: 3,
}

var alertLevelLabels = map[string]string{
	AlertLevelP0: "紧急",
	AlertLevelP1: "严重",
	AlertLevelP2: "一般",
	AlertLevelP3: "提示",
}

// alertLevelRankSentinel 未知等级的排序哨兵。
//
// 与 monitor 侧 qualityWeight 同理：绝不能用 map 零值（0 = 与 P0 同权）
// 兜底未知等级，否则脏数据会被当成最高优先级参与短路判定。
const alertLevelRankSentinel = 99

// alertLevelRankOf 返回等级排序，未知等级返回哨兵值（排在所有已知等级之后）。
func alertLevelRankOf(level string) int {
	if rank, ok := alertLevelRank[normalizeAlertLevelStrict(level)]; ok {
		return rank
	}
	return alertLevelRankSentinel
}

// normalizeAlertLevel 把外部输入归一化为 P 码。第二个返回值为 false 表示非法等级。
// 中文旧值（高/中/低/紧急）一律映射到对应 P 码，保证存量规则与旧客户端不改也能用。
func normalizeAlertLevel(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "p0", "紧急", "灾难", "critical":
		return AlertLevelP0, true
	case "p1", "高", "严重", "error":
		return AlertLevelP1, true
	case "p2", "中", "一般", "warning":
		return AlertLevelP2, true
	case "p3", "低", "提示", "info":
		return AlertLevelP3, true
	default:
		return "", false
	}
}

// normalizeAlertLevelStrict 与 normalizeAlertLevel 相同，但非法输入返回空串（不做兜底）。
func normalizeAlertLevelStrict(raw string) string {
	level, ok := normalizeAlertLevel(raw)
	if !ok {
		return ""
	}
	return level
}

// AlertLevelLabel 返回等级的中文标签（用于通知文案与列表展示）。
func AlertLevelLabel(level string) string {
	normalized := normalizeAlertLevelStrict(level)
	if normalized == "" {
		return level
	}
	return alertLevelLabels[normalized]
}

// AlertLevelOfDefaultLevel 把字典里的"建议等级"（高/中/低）映射成 P 码。
// 字典是给运维看的粗粒度建议，不含 P0 —— P0 只应由人工或推荐表显式授予。
func AlertLevelOfDefaultLevel(defaultLevel string) string {
	if level := normalizeAlertLevelStrict(defaultLevel); level != "" {
		return level
	}
	return AlertLevelP2
}

// ---------------------------------------------------------------------------
// 档位规格与校验
// ---------------------------------------------------------------------------

// AlertLevelSpec 一个指标下的单个告警档位（提交与推荐共用）。
type AlertLevelSpec struct {
	Level          string  `json:"level"`
	Threshold      float64 `json:"threshold"`
	ForSeconds     int     `json:"for_seconds"`
	SilenceMinutes int     `json:"silence_minutes"`
	Enabled        bool    `json:"enabled"`
}

// isGreaterDirection 判断比较符是否属于"越大越糟"方向。
func isGreaterDirection(operator string) bool {
	switch operator {
	case ">", ">=":
		return true
	default:
		return false
	}
}

// isLesserDirection 判断比较符是否属于"越小越糟"方向。
func isLesserDirection(operator string) bool {
	switch operator {
	case "<", "<=":
		return true
	default:
		return false
	}
}

// AlertLevelValidation 档位校验结果。
type AlertLevelValidation struct {
	// Operator 归一化后的比较符（与字典建议一致；字典无建议时保持原值）
	Operator string
	// Levels 归一化并排序后的档位（按 P0→P3）
	Levels []AlertLevelSpec
	// Errors 硬错误：出现即拒绝保存
	Errors []string
	// Warnings 软提示：不阻断保存，前端黄色展示
	Warnings []string
}

// ValidateMetricAlertLevels 校验单个指标的档位集合。
//
// 硬规则（Errors）：
//  1. 等级必须合法且不重复；
//  2. 比较符方向必须与字典建议一致（字典有建议时）——方向选反会让规则永不触发；
//  3. 阈值阶梯必须严格单调：>= 方向 P0 > P1 > P2 > P3，<= 方向反向；
//  4. 布尔类（!=／=／无方向）指标只允许一档。
//
// 软规则（Warnings）：
//  5. 持续秒数随等级降低而放宽（P0 最快触发），且 >= 30s 才有判定意义；
//  6. 静默时长随等级降低而放宽（P0 最少打扰）。
func ValidateMetricAlertLevels(metricLabel, operator string, defaultOperator string, levels []AlertLevelSpec) AlertLevelValidation {
	result := AlertLevelValidation{Operator: operator, Levels: levels}
	if len(levels) == 0 {
		// 未配置档位 = 该指标不告警，合法
		return result
	}

	// 1) 比较符：方向以字典建议为准（方向一致时形式差异自动归一化）
	operator = strings.TrimSpace(operator)
	if operator == "" {
		operator = defaultOperator
	}
	if defaultOperator != "" && operator != defaultOperator {
		sameDirection := (isGreaterDirection(operator) && isGreaterDirection(defaultOperator)) ||
			(isLesserDirection(operator) && isLesserDirection(defaultOperator))
		if sameDirection {
			operator = defaultOperator
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("%s 的比较符 %s 与指标方向（%s）不一致", metricLabel, operator, defaultOperator))
		}
	}
	switch operator {
	case ">=", "<=", ">", "<", "=", "!=":
	case "":
		operator = ">="
	default:
		result.Errors = append(result.Errors, fmt.Sprintf("%s 的比较符 %s 不合法", metricLabel, operator))
	}
	result.Operator = operator

	// 2) 等级合法性 + 去重 + 排序
	normalized := make([]AlertLevelSpec, 0, len(levels))
	seen := map[string]struct{}{}
	for _, raw := range levels {
		level, ok := normalizeAlertLevel(raw.Level)
		if !ok {
			result.Errors = append(result.Errors, fmt.Sprintf("%s 的告警等级 %s 不合法（仅支持 P0/P1/P2/P3）", metricLabel, raw.Level))
			continue
		}
		if _, dup := seen[level]; dup {
			result.Errors = append(result.Errors, fmt.Sprintf("%s 的告警等级 %s 重复配置", metricLabel, level))
			continue
		}
		seen[level] = struct{}{}
		item := raw
		item.Level = level
		if item.SilenceMinutes < 0 {
			item.SilenceMinutes = 0
		}
		if item.ForSeconds < 0 {
			item.ForSeconds = 0
		}
		normalized = append(normalized, item)
	}
	sortByAlertLevel(normalized)
	result.Levels = normalized

	// 3) 布尔类指标只允许一档（!= / = 没有"更严重"的语义，无法构成阶梯）
	if operator == "!=" || operator == "=" {
		if len(normalized) > 1 {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s 使用 %s 判定，只能配置一个告警档位（不存在严重程度阶梯）", metricLabel, operator))
		}
		return result
	}

	// 4) 阈值阶梯单调性
	greater := isGreaterDirection(operator)
	for i := 0; i+1 < len(normalized); i++ {
		current, next := normalized[i], normalized[i+1]
		if greater {
			if current.Threshold <= next.Threshold {
				result.Errors = append(result.Errors, fmt.Sprintf("%s 阈值倒挂：%s(%s) 必须高于 %s(%s)",
					metricLabel, current.Level, formatAlertThreshold(current.Threshold), next.Level, formatAlertThreshold(next.Threshold)))
			}
		} else {
			if current.Threshold >= next.Threshold {
				result.Errors = append(result.Errors, fmt.Sprintf("%s 阈值倒挂：%s(%s) 必须低于 %s(%s)",
					metricLabel, current.Level, formatAlertThreshold(current.Threshold), next.Level, formatAlertThreshold(next.Threshold)))
			}
		}
	}

	// 5) 软提示：持续窗口与静默时长
	for i := 0; i+1 < len(normalized); i++ {
		current, next := normalized[i], normalized[i+1]
		if current.ForSeconds > next.ForSeconds {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 的 %s 持续 %ds 比 %s 的 %ds 更长，建议越严重越快触发",
				metricLabel, current.Level, current.ForSeconds, next.Level, next.ForSeconds))
		}
		if current.SilenceMinutes > next.SilenceMinutes {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 的 %s 静默 %d 分钟比 %s 的 %d 分钟更长，建议越严重打扰越少",
				metricLabel, current.Level, current.SilenceMinutes, next.Level, next.SilenceMinutes))
		}
	}
	for _, item := range normalized {
		if item.ForSeconds > 0 && item.ForSeconds < 30 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s 的 %s 持续 %ds 小于 2 倍采集周期（30s），可能因采样间隔而永不触发",
				metricLabel, item.Level, item.ForSeconds))
		}
	}
	return result
}

// sortByAlertLevel 按 P0→P3 排序（未知等级排在末尾，保持稳定）。
func sortByAlertLevel(levels []AlertLevelSpec) {
	for i := 1; i < len(levels); i++ {
		for j := i; j > 0; j-- {
			if alertLevelRankOf(levels[j-1].Level) <= alertLevelRankOf(levels[j].Level) {
				break
			}
			levels[j-1], levels[j] = levels[j], levels[j-1]
		}
	}
}

// ---------------------------------------------------------------------------
// 推荐档位（默认模板播种 + 前端「恢复推荐值」单一数据源）
// ---------------------------------------------------------------------------

// recommendedAlertLevels 针对"值得开箱即告警"的指标给出多档推荐。
//
// 设计原则：默认集克制 —— 目录里几十个指标只对下面这些播种，其余保持"未配置"，
// 用户按需添加。否则默认模板一上线就是几十条规则，噪音淹没真问题。
//
// 阈值口径与字典 default_operator 方向一致（越大越糟 / 越小越糟），
// 具体数值按"业务可感知"设定，而不是字典里那个为了演示填的占位值。
var recommendedAlertLevels = map[string][]AlertLevelSpec{
	// 可用性：立刻要人，P0 且不等待
	MetricAvailUp: {
		{Level: AlertLevelP0, Threshold: 1, ForSeconds: 0, SilenceMinutes: 5, Enabled: true},
	},
	MetricResCPU: {
		{Level: AlertLevelP1, Threshold: 95, ForSeconds: 300, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 85, ForSeconds: 600, SilenceMinutes: 60, Enabled: true},
	},
	MetricResMemory: {
		{Level: AlertLevelP1, Threshold: 95, ForSeconds: 300, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 85, ForSeconds: 600, SilenceMinutes: 60, Enabled: true},
	},
	MetricResDisk: {
		{Level: AlertLevelP1, Threshold: 95, ForSeconds: 300, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 85, ForSeconds: 600, SilenceMinutes: 60, Enabled: true},
		{Level: AlertLevelP3, Threshold: 70, ForSeconds: 3600, SilenceMinutes: 240, Enabled: true},
	},
	MetricCapConnUsedPct: {
		{Level: AlertLevelP1, Threshold: 95, ForSeconds: 60, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 85, ForSeconds: 300, SilenceMinutes: 60, Enabled: true},
	},
	MetricCapBufferPoolPct: {
		{Level: AlertLevelP1, Threshold: 98, ForSeconds: 600, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 95, ForSeconds: 600, SilenceMinutes: 60, Enabled: true},
	},
	MetricPerfCacheHitRatio: {
		{Level: AlertLevelP2, Threshold: 90, ForSeconds: 600, SilenceMinutes: 60, Enabled: true},
	},
	MetricPerfLocks: {
		{Level: AlertLevelP1, Threshold: 5, ForSeconds: 0, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 1, ForSeconds: 60, SilenceMinutes: 60, Enabled: true},
	},
	MetricPerfLongTrx: {
		{Level: AlertLevelP2, Threshold: 1, ForSeconds: 300, SilenceMinutes: 60, Enabled: true},
	},
	MetricReplIORunning: {
		{Level: AlertLevelP0, Threshold: 1, ForSeconds: 0, SilenceMinutes: 5, Enabled: true},
	},
	MetricReplLagSeconds: {
		{Level: AlertLevelP1, Threshold: 300, ForSeconds: 60, SilenceMinutes: 15, Enabled: true},
		{Level: AlertLevelP2, Threshold: 60, ForSeconds: 300, SilenceMinutes: 60, Enabled: true},
	},
}

// recommendedLevelsFor 返回某指标的推荐档位（仅供配置页「恢复推荐值」显式采纳）。
//
// 改造说明（随指标字典下线）：原先推荐表里没有的指标会退化成"字典单档" ——
// 拿字典的 default_threshold / default_level 现造一档。那等于"用户没配也有阈值可用"，
// 与"告警判定只看配置页规则"的原则冲突，已移除。
// 现在只有 recommendedAlertLevels 表里显式登记的指标才有推荐值，其余返回空：
// 明确"平台不建议开告警"，由用户在配置页自行设定，而不是硬塞一个默认值。
//
// 注意：推荐值只出现在配置页的「恢复推荐值」按钮里，评估链路不读它 ——
// 没有任何规则行就没有告警，不存在回退。
func recommendedLevelsFor(item MetricDescriptor) []AlertLevelSpec {
	if levels, ok := recommendedAlertLevels[canonicalMetricKey(item.MetricKey)]; ok {
		return cloneLevelSpecs(levels)
	}
	return nil
}

// recommendedSilenceMinutes 按等级给默认静默时长：越严重打扰越少。
func recommendedSilenceMinutes(level string) int {
	switch normalizeAlertLevelStrict(level) {
	case AlertLevelP0:
		return 5
	case AlertLevelP1:
		return 15
	case AlertLevelP2:
		return 60
	default:
		return 240
	}
}

func cloneLevelSpecs(source []AlertLevelSpec) []AlertLevelSpec {
	if len(source) == 0 {
		return nil
	}
	result := make([]AlertLevelSpec, len(source))
	copy(result, source)
	return result
}

// alertLevelIdentity 等级的身份键：合法等级归一化为 P 码；脏数据（既不是 P 码也不是
// 中文旧值）原样小写化参与比较 —— 保证"能读出来就能改掉"，不会与合法等级混为一谈。
func alertLevelIdentity(level string) string {
	if normalized := normalizeAlertLevelStrict(level); normalized != "" {
		return normalized
	}
	return strings.ToLower(strings.TrimSpace(level))
}

// ---------------------------------------------------------------------------
// 规则存储规范化（启动期，幂等）
// ---------------------------------------------------------------------------

// normalizeAlertRuleStorage 把规则表刷成"阶梯口径的规范形态"。
//
// 背景：多档阶梯上线后，历史数据里有三种会让配置页"看起来正常、保存必失败"的脏形态：
//  1. 同一指标同时存在裸名键与三段式键（cpu 与 res.cpu）——同一指标被拆成两行；
//  2. 布尔类指标（!= / =）被历史单档规则与新的推荐档位叠加出多个等级；
//  3. 历史上"同一阈值被播成 P1/P2 两行"——低等级那档永远不会被选中（评估取命中最高档），
//     却会让配置页一亮相就报"阈值倒挂"。
//
// 处理：① 按 (模板, 规范键, 等级) 去重（保留最小 id，事件外链不断）；
// ② 布尔类指标只保留最高一档；③ 阈值不严格更宽松的"死档"删除；
// ④ 裸名键就地改写成规范键。全程幂等，执行顺序必须是"先删、再改键"，否则会撞唯一索引。
func normalizeAlertRuleStorage(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	type ruleRow struct {
		ID         uint64
		TemplateID uint64
		MetricKey  string
		Operator   string
		AlertLevel string
		Threshold  float64
	}
	rows := make([]ruleRow, 0)
	if err := db.Model(&monitorModels.InsightMonitorAlertRules{}).
		Select("id, template_id, metric_key, operator, alert_level, threshold").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	deleting := map[uint64]struct{}{}
	markDelete := func(id uint64) { deleting[id] = struct{}{} }

	// ① 同 (模板, 规范键, 等级) 去重：保留 id 最小的一行
	minByIDentity := map[string]uint64{}
	for _, row := range rows {
		identity := fmt.Sprintf("%d|%s|%s", row.TemplateID, canonicalMetricKey(row.MetricKey), alertLevelIdentity(row.AlertLevel))
		kept, ok := minByIDentity[identity]
		switch {
		case !ok:
			minByIDentity[identity] = row.ID
		case row.ID < kept:
			minByIDentity[identity] = row.ID
			markDelete(kept)
		default:
			markDelete(row.ID)
		}
	}

	// ② + ③ 按指标聚合：布尔类只留最高档；其余按等级从高到低收敛成严格单调的阶梯
	byMetric := map[string][]ruleRow{}
	order := make([]string, 0)
	for _, row := range rows {
		if _, dropped := deleting[row.ID]; dropped {
			continue
		}
		group := fmt.Sprintf("%d|%s", row.TemplateID, canonicalMetricKey(row.MetricKey))
		if _, ok := byMetric[group]; !ok {
			order = append(order, group)
		}
		byMetric[group] = append(byMetric[group], row)
	}
	for _, group := range order {
		items := byMetric[group]
		sort.SliceStable(items, func(i, j int) bool {
			return alertLevelRankOf(items[i].AlertLevel) < alertLevelRankOf(items[j].AlertLevel)
		})
		if isBooleanOperator(items[0].Operator) {
			// 布尔类只能有一档：留最高档（排序后第一行）
			for _, item := range items[1:] {
				markDelete(item.ID)
			}
			continue
		}
		greater := isGreaterDirection(items[0].Operator)
		lastThreshold := 0.0
		for index, item := range items {
			if index == 0 {
				lastThreshold = item.Threshold
				continue
			}
			looser := greater && item.Threshold < lastThreshold
			if !greater {
				looser = item.Threshold > lastThreshold
			}
			if looser {
				lastThreshold = item.Threshold
				continue
			}
			// 不严格更宽松 = 永远不会被选中的死档（低等级同阈值或倒挂）
			markDelete(item.ID)
		}
	}

	deleteIDs := make([]uint64, 0, len(deleting))
	for id := range deleting {
		deleteIDs = append(deleteIDs, id)
	}
	if len(deleteIDs) > 0 {
		if err := db.Where("id IN ?", deleteIDs).Delete(&monitorModels.InsightMonitorAlertRules{}).Error; err != nil {
			return err
		}
		global.App.Log.Infof("[monitor] 已清理 %d 条重复/失效的告警档位", len(deleteIDs))
	}

	// ④ 裸名键就地改写为规范键（必须在删除之后，否则会撞唯一索引）
	for _, row := range rows {
		if _, dropped := deleting[row.ID]; dropped {
			continue
		}
		canonical := canonicalMetricKey(row.MetricKey)
		if row.MetricKey == canonical {
			continue
		}
		if err := db.Model(&monitorModels.InsightMonitorAlertRules{}).
			Where("id = ?", row.ID).Update("metric_key", canonical).Error; err != nil {
			return err
		}
	}
	return nil
}

// isBooleanOperator 布尔/相等类判定：没有"方向"也就没有"更严重"，只允许一档。
func isBooleanOperator(operator string) bool {
	switch strings.TrimSpace(operator) {
	case "!=", "=":
		return true
	default:
		return false
	}
}

// formatAlertThreshold 阈值文案：整数不带小数点，小数去掉多余的 0。
func formatAlertThreshold(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%.0f", value)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
