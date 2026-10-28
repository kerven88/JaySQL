package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/datatypes"
)

// monitor_health_state.go —— 实例健康状态判定（健康分驱动，2026-09-24 第二版）。
//
// 口径：**先算分、再定级**，分数是唯一主线，状态与分数天然一致：
//   - 健康分 = 100 − Σ(每条未恢复告警按 P 级扣分)，下限 0；
//   - 健康分 ≤ fault_threshold → 故障（默认 60）；
//   - 健康分 ≤ risk_threshold  → 风险（默认 90）；
//   - 其余 → 健康。
//
// 默认扣分 P0=40 / P1=10 / P2=5 / P3=3，与默认阈值配合的语义：
// 单条 P0（60 分）即故障；单条 P1（90 分）即风险；单条 P2/P3（95/97 分）不影响状态。
//
// 两类**固定前置**不参与配置（它们回答的是"能不能看见这台实例"，分数表达不了）：
//   - 未采集：从未采集 / 数据过期 / 新鲜度超限 → uncollected，不给分（防"假健康"，见 99 从库停更事故）；
//   - 采集失败 / 质量错误 / avail.up=0 → 故障且 0 分（状态与分数必须自洽）；
//   - 采集降级（部分指标不可用 / 引擎不支持 / 连续失败≥3）→ 至少风险（分数高也抬升，此时分数仍反映告警）。
//
// 配置存储复用 insight_monitor_health_grade_settings 同一行（payload 为 v2 schema）。
// v1（按等级判定 fault_min_level/risk_min_level）与更早的健康分级 JSON 均不含阈值字段：
// 读取时识别为旧数据，回退内置默认，绝不停摆。

// ---------------------------------------------------------------------------
// 配置定义
// ---------------------------------------------------------------------------

// 健康状态配置内置版本。调整内置默认值时递增，未自定义的行会自动跟随。
const builtinHealthStateVersion = 2

// healthStateCacheTTL 配置缓存时长：保存后最迟这个时间全平台生效（与大屏轮询节奏匹配）。
const healthStateCacheTTL = 10 * time.Second

// HealthStateConfig 健康状态判定配置（v2 schema：分数阈值 + 扣分表）。
type HealthStateConfig struct {
	// FaultThreshold 健康分 ≤ 该值 → 故障。0 合法（仅 0 分判故障）。
	FaultThreshold int `json:"fault_threshold"`
	// RiskThreshold 健康分 ≤ 该值（且高于故障档）→ 风险。
	RiskThreshold int `json:"risk_threshold"`
	// Deductions 健康分扣分表：P0~P3 → 每条未恢复告警扣的分。
	Deductions map[string]int `json:"deductions"`

	Source        string `json:"source"`                    // builtin / db
	Customized    bool   `json:"customized"`
	Version       int    `json:"version"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
	Degraded      bool   `json:"degraded,omitempty"`
	DegradeReason string `json:"degrade_reason,omitempty"`
}

// builtinHealthStateConfig 内置默认模版（最佳实践参考）：
//   - 阈值：≤60 故障 / ≤90 风险 / >90 健康；
//   - 扣分：P0=40 / P1=10 / P2=5 / P3=3。
//
// 两组数字是配套设计的：单条 P0 恰好落 60 分触达故障档（延续"出现 P0 即故障"的运维直觉），
// 单条 P1 落 90 分触达风险档，单条 P2/P3 不改变状态。
func builtinHealthStateConfig() HealthStateConfig {
	return HealthStateConfig{
		FaultThreshold: 60,
		RiskThreshold:  90,
		Deductions:     map[string]int{"P0": 40, "P1": 10, "P2": 5, "P3": 3},
		Source:         "builtin",
		Version:        builtinHealthStateVersion,
	}
}

var healthStateLevels = []string{"P0", "P1", "P2", "P3"}

// healthStateLevelRank 等级 → 严重度序（越小越严重）。未知值落 9（哨兵，同 qualityWeight 思路）。
func healthStateLevelRank(level string) int {
	code := strings.ToUpper(strings.TrimSpace(level))
	for i, item := range healthStateLevels {
		if item == code {
			return i
		}
	}
	return 9
}

// healthStateSeverityOf 告警 → P 码。事件写入侧（severityFromLevel）已把未知值落到 P3，
// 这里再兜一层：空/未知按 P3 计，绝不让脏数据把告警"藏"出判定。
func healthStateSeverityOf(alert AlertRecord) string {
	code := strings.ToUpper(strings.TrimSpace(alert.Severity))
	for _, item := range healthStateLevels {
		if item == code {
			return item
		}
	}
	return "P3"
}

// ValidateHealthStateConfig 写入路径校验：不合法直接报错，不静默改配置。
func ValidateHealthStateConfig(cfg HealthStateConfig) error {
	if cfg.FaultThreshold < 0 || cfg.FaultThreshold > 100 {
		return fmt.Errorf("故障阈值超出范围（0~100）：%d", cfg.FaultThreshold)
	}
	if cfg.RiskThreshold < 1 || cfg.RiskThreshold > 100 {
		return fmt.Errorf("风险阈值超出范围（1~100）：%d", cfg.RiskThreshold)
	}
	// 故障档必须严格低于风险档，否则两档语义重叠（同分既故障又风险）
	if cfg.FaultThreshold >= cfg.RiskThreshold {
		return fmt.Errorf("故障阈值（%d）必须小于风险阈值（%d）", cfg.FaultThreshold, cfg.RiskThreshold)
	}
	if len(cfg.Deductions) == 0 {
		return fmt.Errorf("扣分表不能为空")
	}
	for _, level := range healthStateLevels {
		deduct, ok := cfg.Deductions[level]
		if !ok {
			return fmt.Errorf("扣分表缺少 %s", level)
		}
		if deduct < 0 || deduct > 100 {
			return fmt.Errorf("%s 扣分超出范围（0~100）：%d", level, deduct)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 配置读取 / 保存（复用 insight_monitor_health_grade_settings 同一行）
// ---------------------------------------------------------------------------

var (
	healthStateMutex    sync.Mutex
	healthStateCache    *HealthStateConfig
	healthStateLoadedAt time.Time
)

// GetHealthStateConfig 取当前生效配置（带缓存）。任何读取失败都降级为内置默认，绝不停摆。
func GetHealthStateConfig() HealthStateConfig {
	healthStateMutex.Lock()
	defer healthStateMutex.Unlock()
	if healthStateCache != nil && time.Since(healthStateLoadedAt) < healthStateCacheTTL {
		return *healthStateCache
	}
	cfg := loadHealthStateConfig()
	healthStateCache = &cfg
	healthStateLoadedAt = time.Now()
	return cfg
}

// ReloadHealthStateConfig 失效缓存；保存配置后调用，立即按新口径评估。
func ReloadHealthStateConfig() HealthStateConfig {
	healthStateMutex.Lock()
	healthStateCache = nil
	healthStateMutex.Unlock()
	return GetHealthStateConfig()
}

func loadHealthStateConfig() HealthStateConfig {
	builtin := builtinHealthStateConfig()
	if global.App == nil || global.App.DB == nil {
		return builtin
	}
	var row models.InsightMonitorHealthGradeSettings
	if err := global.App.DB.Where("id = ?", healthGradeRowID).Take(&row).Error; err != nil {
		builtin.Degraded = true
		builtin.DegradeReason = "未读取到健康状态设置，已按内置默认运行"
		warnHealthState(builtin.DegradeReason)
		return builtin
	}
	if !row.Customized || len(row.Payload) == 0 {
		return builtin
	}
	var cfg HealthStateConfig
	if err := json.Unmarshal(row.Payload, &cfg); err != nil {
		builtin.Degraded = true
		builtin.DegradeReason = fmt.Sprintf("健康状态配置解析失败（%v），已回退内置默认", err)
		warnHealthState(builtin.DegradeReason)
		return builtin
	}
	// v1（按等级判定）与更早的健康分级 JSON 都没有 fault_threshold/risk_threshold：
	// 识别为历史数据，回退默认。用户在面板重新保存后即落 v2 schema。
	if cfg.FaultThreshold == 0 && cfg.RiskThreshold == 0 {
		builtin.Degraded = true
		builtin.DegradeReason = "检测到旧版判定配置（按等级/分级），已切换为按健康分区间判定的内置默认（可在「告警配置 → 健康判定」重新保存）"
		warnHealthState(builtin.DegradeReason)
		return builtin
	}
	// 读取路径宽容：坏字段逐项修正，不让大屏停摆
	normalized := builtin
	normalized.Source = "db"
	normalized.Customized = true
	normalized.Version = row.Version
	if row.Model != nil {
		normalized.UpdatedAt = time.Time(row.UpdatedAt).Format("2006-01-02 15:04:05")
	}
	normalized.UpdatedBy = row.UpdatedByName
	if cfg.FaultThreshold >= 0 && cfg.FaultThreshold <= 100 {
		normalized.FaultThreshold = cfg.FaultThreshold
	}
	if cfg.RiskThreshold >= 1 && cfg.RiskThreshold <= 100 {
		normalized.RiskThreshold = cfg.RiskThreshold
	}
	if normalized.FaultThreshold >= normalized.RiskThreshold {
		normalized = builtin
		normalized.Source = "db"
		normalized.Customized = true
		normalized.Version = row.Version
		if row.Model != nil {
			normalized.UpdatedAt = time.Time(row.UpdatedAt).Format("2006-01-02 15:04:05")
		}
		normalized.UpdatedBy = row.UpdatedByName
		normalized.Degraded = true
		normalized.DegradeReason = "故障阈值不小于风险阈值，已回退内置默认（保存时会被校验拦截）"
	}
	for _, level := range healthStateLevels {
		if deduct, ok := cfg.Deductions[level]; ok && deduct >= 0 && deduct <= 100 {
			normalized.Deductions[level] = deduct
		}
	}
	return normalized
}

// SaveHealthStateConfig 校验并保存（写同一设置行，Customized=true）。
func SaveHealthStateConfig(raw []byte, operator string) (HealthStateConfig, error) {
	var cfg HealthStateConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return HealthStateConfig{}, fmt.Errorf("配置 JSON 解析失败: %v", err)
	}
	if err := ValidateHealthStateConfig(cfg); err != nil {
		return HealthStateConfig{}, err
	}
	if global.App == nil || global.App.DB == nil {
		return HealthStateConfig{}, fmt.Errorf("数据库未就绪")
	}
	toStore := builtinHealthStateConfig()
	toStore.FaultThreshold = cfg.FaultThreshold
	toStore.RiskThreshold = cfg.RiskThreshold
	toStore.Deductions = map[string]int{}
	for _, level := range healthStateLevels {
		toStore.Deductions[level] = cfg.Deductions[level]
	}
	payload, err := json.Marshal(toStore)
	if err != nil {
		return HealthStateConfig{}, err
	}
	updates := map[string]any{
		"version":         builtinHealthStateVersion,
		"customized":      true,
		"payload":         datatypes.JSON(payload),
		"updated_by_name": strings.TrimSpace(operator),
	}
	if err := global.App.DB.Model(&models.InsightMonitorHealthGradeSettings{}).
		Where("id = ?", healthGradeRowID).Updates(updates).Error; err != nil {
		return HealthStateConfig{}, err
	}
	result := ReloadHealthStateConfig()
	if global.App.Log != nil {
		global.App.Log.Infof("[monitor] 健康状态判定已更新：故障≤%d分 风险≤%d分 扣分 %v，操作人 %s",
			result.FaultThreshold, result.RiskThreshold, result.Deductions, strings.TrimSpace(operator))
	}
	return result, nil
}

// ResetHealthStateConfig 丢弃用户配置，回到内置默认判定策略。
func ResetHealthStateConfig(operator string) (HealthStateConfig, error) {
	if global.App == nil || global.App.DB == nil {
		return HealthStateConfig{}, fmt.Errorf("数据库未就绪")
	}
	payload, err := json.Marshal(builtinHealthStateConfig())
	if err != nil {
		return HealthStateConfig{}, err
	}
	if err := global.App.DB.Model(&models.InsightMonitorHealthGradeSettings{}).
		Where("id = ?", healthGradeRowID).
		Updates(map[string]any{
			"version":         builtinHealthStateVersion,
			"customized":      false,
			"payload":         datatypes.JSON(payload),
			"updated_by_name": strings.TrimSpace(operator),
		}).Error; err != nil {
		return HealthStateConfig{}, err
	}
	result := ReloadHealthStateConfig()
	if global.App.Log != nil {
		global.App.Log.Infof("[monitor] 健康状态判定已重置为内置默认，操作人 %s", strings.TrimSpace(operator))
	}
	return result, nil
}

func warnHealthState(reason string) {
	if global.App == nil || global.App.Log == nil {
		return
	}
	global.App.Log.Warnf("[monitor] 健康状态配置降级：%s", reason)
}

// LogHealthStateSelfCheck 采集轮自检：把当前生效口径打成一行日志，方便回溯"当时按什么规则判的"。
func LogHealthStateSelfCheck() {
	if global.App == nil || global.App.Log == nil {
		return
	}
	cfg := GetHealthStateConfig()
	global.App.Log.Infof("[monitor] 健康状态自检：来源 %s | 故障≤%d | 风险≤%d | 扣分 P0=%d P1=%d P2=%d P3=%d | 数据过期阈值 %ds",
		cfg.Source, cfg.FaultThreshold, cfg.RiskThreshold,
		cfg.Deductions["P0"], cfg.Deductions["P1"], cfg.Deductions["P2"], cfg.Deductions["P3"],
		DataStaleLimitSeconds())
	if cfg.Degraded {
		global.App.Log.Warnf("[monitor] 健康状态配置降级：%s", cfg.DegradeReason)
	}
}

// ---------------------------------------------------------------------------
// 实例状态判定（唯一入口，大屏 / 明细 / 风险总览共用）
// ---------------------------------------------------------------------------

// instanceStateEval 单实例判定结论：状态、健康分、最高未恢复告警等级三者同源一次算出，
// 调用方不许再自行拼装第二套口径。
type instanceStateEval struct {
	Class       string
	Score       *int // uncollected 为 nil（数据不可信不给分，前端显示「—」）
	TopSeverity string
}

// evaluateInstanceState 先算分、再定级：
//  1. 固定前置优先（从未采集 / 采集失败 / 数据过期）——它们决定"分数是否可信"；
//  2. 健康分 = 100 − Σ(未恢复告警按 P 级扣分)，下限 0；
//  3. 状态按分数区间推导（≤故障档故障 / ≤风险档风险 / 其余健康）；
//  4. 采集降级是唯一的"分数外抬升"：分数再高也至少判风险（分数本身不动，
//     留给"分数只反映告警"这条解释线）。
//
// 未恢复口径：卡片 Alerts 本身只含 active/acknowledged 且未忽略、模板白名单内的事件
// （见 loadActiveEventMap），这里不再重复过滤，但保留 isUnresolvedAlertEvent 兜底。
func evaluateInstanceState(card InstanceMonitorCard) instanceStateEval {
	// 1) 从未采集：还没有任何结论可给
	if card.CollectStatus == "never" {
		return instanceStateEval{Class: HealthStateUncollected}
	}
	// 2) 采集失败 / 质量错误 / 实例不可达：状态彻底不可判，直接故障且 0 分
	//   （原分级 collect_failed/instance_down P0 语义；0 分保证"状态故障 ⇔ 分数落故障档"自洽）
	if card.CollectStatus == "failed" || card.Quality == QualityError {
		zero := 0
		return instanceStateEval{Class: HealthStateFault, Score: &zero}
	}
	for _, sample := range card.Metrics {
		if sample.Key == MetricAvailUp && sample.Available && sample.Value == 0 {
			zero := 0
			return instanceStateEval{Class: HealthStateFault, Score: &zero}
		}
	}
	// 3) 数据过期 / 无数据 / 新鲜度超限：数据不可信，不给分也不给健康/风险结论（防假健康）
	if card.Quality == QualityStale || card.Quality == QualityNoData ||
		(card.FreshnessSeconds > 0 && card.FreshnessSeconds > DataStaleLimitSeconds()) {
		return instanceStateEval{Class: HealthStateUncollected}
	}
	// 4) 算分 + 取最高告警等级（同一轮循环，口径天然一致）
	cfg := GetHealthStateConfig()
	score := 100
	top := ""
	for _, alert := range card.Alerts {
		if alert.Ignored || !isUnresolvedAlertEvent(alert.StatusKey, alert.Ignored) {
			continue
		}
		code := healthStateSeverityOf(alert)
		score -= deductionOfLevel(cfg, code)
		if top == "" || healthStateLevelRank(code) < healthStateLevelRank(top) {
			top = code
		}
	}
	if score < 0 {
		score = 0
	}
	class := healthStateClassFromScore(cfg, score)
	// 5) 采集降级（固定项）：部分指标不支持 / 引擎不支持 / 连续失败，结论可信度下降 → 至少风险。
	//    为保证「分数与状态一致」的可解释性，状态被抬升时同步把分数下落到风险阈值：
	//    分数反映告警，但采集降级让结论可信度下降，落到风险阈值可让前端分数/标签/颜色三者同源。
	if class == HealthStateHealthy && (card.Quality == QualityUnsupported || card.Quality == "partial" ||
		(card.ConsecutiveFailures >= 3 && card.CollectStatus != "failed")) {
		class = HealthStateRisk
		if score > cfg.RiskThreshold {
			score = cfg.RiskThreshold
		}
	}
	return instanceStateEval{Class: class, Score: &score, TopSeverity: top}
}

// healthStateClassFromScore 分数 → 状态。全平台状态判定的唯一映射点。
// 阈值区间（与前端健康判定面板 scoreSegments 同源）：
//   故障：[0, fault_threshold]            （健康分 ≤ 故障阈值）
//   风险：(fault_threshold, risk_threshold] （健康分 ≤ 风险阈值 且 > 故障阈值）
//   健康：(risk_threshold, 100]              （健康分 > 风险阈值）
// 边界分归左侧更严重一档，宁可多提示。
func healthStateClassFromScore(cfg HealthStateConfig, score int) string {
	switch {
	case score <= cfg.FaultThreshold:
		return HealthStateFault
	case score <= cfg.RiskThreshold:
		return HealthStateRisk
	default:
		return HealthStateHealthy
	}
}

func deductionOfLevel(cfg HealthStateConfig, level string) int {
	if deduct, ok := cfg.Deductions[level]; ok && deduct >= 0 {
		return deduct
	}
	return 0
}

// screenClassLabelOf 四分类中文名（明细页健康分带 / 头部徽标共用）。
func screenClassLabelOf(class string) string {
	switch class {
	case HealthStateFault:
		return "故障"
	case HealthStateRisk:
		return "风险"
	case HealthStateUncollected:
		return "未采集"
	case HealthStateHealthy:
		return "健康"
	default:
		return "未获取"
	}
}
