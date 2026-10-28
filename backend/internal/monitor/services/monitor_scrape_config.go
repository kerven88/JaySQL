package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ============================================================================
// 采集频率：唯一数据源与三处派生
//
// 背景：改造前周期散落在四个地方各自硬编码 ——
//   1. Prometheus 抓取间隔：scripts/prometheus/prometheus.yml 里的 scrape_interval: 15s
//   2. JaySQL 落库周期：insight_monitor_target_configs.scrape_interval_seconds（默认 60）
//   3. PromQL 采样窗口：dashboard.go 里的 irate(...[5m])
//   4. 历史聚合桶宽：monitor_history.go 里 rangeWindows 的固定 bucket
// 四者互不知情，于是出现"Prometheus 15s 就刷了新数据、JaySQL 60s 才取一次、
// 但 rate 窗口按 5m 算"，口径对不上。
//
// 现在的形态：insight_monitor_scrape_settings（单行）是唯一数据源，
// 其余三处全部由它派生，派生规则集中在本文件，任何地方都不许再写死周期。
//
// 派生规则（改动这里等于改动全局口径，务必同步更新单测）：
//   Prometheus 抓取间隔 = 周期                 （两边必须同一个节奏，否则一边空转）
//   JaySQL 落库周期   = 周期 × N（N≥1 整数，实例级只允许放慢，不允许加快）
//   PromQL rate 窗口     = max(4 × 周期, 60s) （Prometheus 官方建议 ≥ 4 倍抓取间隔，
//                                              否则窗口内样本点不足，rate 会算空或跳变）
//   历史聚合桶宽         = 桶宽向上取整到周期的整数倍，且不低于"窗口/最大点数"
// ============================================================================

const (
	// DefaultScrapeIntervalSeconds 默认采集周期（1 分钟）。
	// 取值理由：既能覆盖绝大多数库的性能诊断粒度，又不至于让 100 个实例把元数据库写爆。
	DefaultScrapeIntervalSeconds = 60
	// MinScrapeIntervalSeconds 最小周期。低于 15s 时，一轮采集（含建连 + 16 项 PromQL）
	// 本身就可能超过周期，导致上一轮还没结束下一轮又起，最终堆积成雪崩。
	MinScrapeIntervalSeconds = 15
	// MaxScrapeIntervalSeconds 最大周期。再慢就失去监控意义了。
	MaxScrapeIntervalSeconds = 3600
	// scrapeIntervalGranularity 自定义值的粒度：必须是 15 秒的整数倍。
	// 这样桶边界、Prometheus 抓取、落库周期三者永远整除，不会出现窗口错位。
	scrapeIntervalGranularity = 15
	// promRateWindowFactor rate/increase 窗口相对抓取间隔的最小倍数。
	// Prometheus 官方建议至少 4 倍：窗口太窄时若恰巧丢了一个采样点，rate 会直接算空。
	promRateWindowFactor = 4
	// promRateWindowFloorSeconds 窗口下限。周期 15s 时 4 倍只有 60s，正好落在下限上。
	promRateWindowFloorSeconds = 60
	// maxHistoryPoints 单个时间窗口最多返回的点数。
	// 它是 DeriveBucketSeconds 里的兜底项：窗口期望桶宽算出的点数超过它时，
	// 桶宽会被撑大到刚好压回这个上限。
	//
	// 取值必须 ≥「7 天窗口 / 60s = 10080」，否则 7 天窗口的 60s 桶宽会被这条
	// 兜底撑大，用户看到的分档就名不副实。各窗口在新分档下的点数：
	//   15m/15s=60、1h/15s=240、6h/15s=1440、24h/15s=5760、7d/60s=10080。
	// 30 天（600s）=4320 点，也在上限内，所以 30 天以上的自定义区间走 3600s 档。
	maxHistoryPoints = 10080

	scrapeSettingsRowID = 1
	// promRateWindowPlaceholder PromQL 模板里的窗口占位符。
	// 表达式模板保留它是为了让 PromQL 保持可读；实际执行前会被统一替换成派生窗口
	// （见 dashboard.go CollectHostResources），所以这里不是"第二处硬编码周期"。
	promRateWindowPlaceholder = "[5m]"
	// scrapeConfigCacheTTL 进程内缓存时长。改配置后最多 10 秒全进程生效。
	// 采集是 10 秒一轮，缓存取 10 秒既能挡住高频读取，又不会让人觉得"改了没反应"。
	scrapeConfigCacheTTL = 10 * time.Second
)

// ScrapePreset 一个可选的档位。档位表本身就是"配置驱动"的载体：
// 后续要加档位，只改这里，前端下拉、校验、派生全部自动跟随。
type ScrapePreset struct {
	Seconds           int    `json:"seconds"`
	Label             string `json:"label"`
	RateWindowSeconds int    `json:"rate_window_seconds"`
	StepSeconds       int    `json:"step_seconds"`
	Recommended       bool   `json:"recommended"`
	Hint              string `json:"hint"`
}

var scrapeIntervalPresets = []ScrapePreset{
	// 15s 档位不再附带「需同步改 Prometheus scrape_interval」的说明：
	// 页面已移除 Prometheus 侧同步片段，留着这句会指向一个不存在的操作入口。
	{Seconds: 15, Label: "15 秒", RateWindowSeconds: 60, StepSeconds: 15, Recommended: false,
		Hint: ""},
	{Seconds: 30, Label: "30 秒", RateWindowSeconds: 120, StepSeconds: 30, Recommended: false,
		Hint: "精度与开销折中"},
	{Seconds: 60, Label: "1 分钟", RateWindowSeconds: 240, StepSeconds: 60, Recommended: true,
		Hint: "推荐：覆盖绝大多数诊断场景，开销可控"},
	{Seconds: 300, Label: "5 分钟", RateWindowSeconds: 1200, StepSeconds: 300, Recommended: false,
		Hint: "适合大规模实例或只做趋势留档"},
	{Seconds: 600, Label: "10 分钟", RateWindowSeconds: 2400, StepSeconds: 600, Recommended: false,
		Hint: "极低开销，仅保留长期趋势"},
}

// ScrapeConfig 一次派生的完整结果。接口直接把它返回给前端，
// 页面上展示的"抓取 / 落库 / 窗口 / 聚合"四行就是这份结构，避免前端自己算、算错。
type ScrapeConfig struct {
	IntervalSeconds int  `json:"interval_seconds"`
	AllowCustom     bool `json:"allow_custom"`

	// --- 由 IntervalSeconds 派生的三处周期 ---
	PrometheusScrapeIntervalSeconds int `json:"prometheus_scrape_interval_seconds"`
	CollectIntervalSeconds          int `json:"collect_interval_seconds"`
	PromRateWindowSeconds           int `json:"prom_rate_window_seconds"`
	PromStepSeconds                 int `json:"prom_step_seconds"`

	// 派生后的历史聚合桶宽：key 为窗口，value 为该窗口实际使用的桶宽（秒）
	HistoryBuckets map[string]int `json:"history_buckets"`

	Presets []ScrapePreset `json:"presets"`
	// MinSeconds / MaxSeconds / Granularity 供前端做输入约束
	MinSeconds    int    `json:"min_seconds"`
	MaxSeconds    int    `json:"max_seconds"`
	Granularity   int    `json:"granularity_seconds"`
	Degraded      bool   `json:"degraded"`
	DegradeReason string `json:"degrade_reason,omitempty"`
	Source        string `json:"source"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

var (
	scrapeConfigCache    *ScrapeConfig
	scrapeConfigLoadedAt time.Time
	scrapeConfigMutex    sync.Mutex
	scrapeConfigLastWarn time.Time
)

// ScrapeIntervalPresets 暴露档位表（供接口返回给前端）。
func ScrapeIntervalPresets() []ScrapePreset {
	out := make([]ScrapePreset, len(scrapeIntervalPresets))
	copy(out, scrapeIntervalPresets)
	return out
}

func isPresetInterval(seconds int) bool {
	for _, item := range scrapeIntervalPresets {
		if item.Seconds == seconds {
			return true
		}
	}
	return false
}

// ValidateScrapeInterval 写入路径的严格校验：非法值直接返回错误，不静默改数。
// 规则优先级：档位 > （允许自定义时）范围 + 粒度。
func ValidateScrapeInterval(seconds int, allowCustom bool) error {
	if isPresetInterval(seconds) {
		return nil
	}
	if !allowCustom {
		return fmt.Errorf("采集周期 %d 秒不在可选档位内（档位以外的数值需开启自定义）", seconds)
	}
	if seconds < MinScrapeIntervalSeconds || seconds > MaxScrapeIntervalSeconds {
		return fmt.Errorf("采集周期需在 %d ~ %d 秒之间", MinScrapeIntervalSeconds, MaxScrapeIntervalSeconds)
	}
	if seconds%scrapeIntervalGranularity != 0 {
		return fmt.Errorf("采集周期需为 %d 秒的整数倍，保证与 Prometheus 抓取、聚合桶对齐", scrapeIntervalGranularity)
	}
	return nil
}

// NormalizeScrapeInterval 读取路径的宽容处理：非法值降级为默认值并给出原因。
// 与写入路径的区别在于：库里可能躺着历史脏数据，读的时候不能因此让整个监控停摆，
// 所以降级 + 告警，而不是报错。
func NormalizeScrapeInterval(seconds int, allowCustom bool) (int, string) {
	if isPresetInterval(seconds) {
		return seconds, ""
	}
	if seconds <= 0 {
		return DefaultScrapeIntervalSeconds, fmt.Sprintf("未配置采集周期，已按默认值 %d 秒运行", DefaultScrapeIntervalSeconds)
	}
	if !allowCustom {
		return DefaultScrapeIntervalSeconds, fmt.Sprintf("采集周期 %d 秒不在档位内且未开启自定义，已降级为 %d 秒", seconds, DefaultScrapeIntervalSeconds)
	}
	if seconds < MinScrapeIntervalSeconds || seconds > MaxScrapeIntervalSeconds {
		return DefaultScrapeIntervalSeconds, fmt.Sprintf("采集周期 %d 秒超出 %d~%d 秒范围，已降级为 %d 秒",
			seconds, MinScrapeIntervalSeconds, MaxScrapeIntervalSeconds, DefaultScrapeIntervalSeconds)
	}
	if seconds%scrapeIntervalGranularity != 0 {
		rounded := ((seconds + scrapeIntervalGranularity - 1) / scrapeIntervalGranularity) * scrapeIntervalGranularity
		if rounded > MaxScrapeIntervalSeconds {
			rounded = MaxScrapeIntervalSeconds
		}
		return rounded, fmt.Sprintf("采集周期 %d 秒不是 %d 秒的整数倍，已向上取整为 %d 秒",
			seconds, scrapeIntervalGranularity, rounded)
	}
	return seconds, ""
}

// deriveScrapeConfig 由周期派生出三处周期与聚合桶宽。新增派生项必须加在这里。
func deriveScrapeConfig(interval int, allowCustom bool, source, degradeReason, updatedAt, updatedBy string) ScrapeConfig {
	window := interval * promRateWindowFactor
	if window < promRateWindowFloorSeconds {
		window = promRateWindowFloorSeconds
	}
	cfg := ScrapeConfig{
		IntervalSeconds:                 interval,
		AllowCustom:                     allowCustom,
		PrometheusScrapeIntervalSeconds: interval,
		CollectIntervalSeconds:          interval,
		PromRateWindowSeconds:           window,
		PromStepSeconds:                 interval,
		HistoryBuckets:                  derivedHistoryBuckets(interval),
		Presets:                         ScrapeIntervalPresets(),
		MinSeconds:                      MinScrapeIntervalSeconds,
		MaxSeconds:                      MaxScrapeIntervalSeconds,
		Granularity:                     scrapeIntervalGranularity,
		Degraded:                        degradeReason != "",
		DegradeReason:                   degradeReason,
		Source:                          source,
		UpdatedAt:                       updatedAt,
		UpdatedBy:                       updatedBy,
	}
	return cfg
}

// derivedHistoryBuckets 由周期派生各时间窗口的聚合桶宽。
// 规则：桶宽 = max(窗口期望桶宽, 周期, 窗口/最大点数)，再向上取整到周期的整数倍。
// "向上取整到整数倍"是防止漏采与重复采的关键：桶边界与采样点严格对齐后，
// 一个采样点只会落进一个桶，不会横跨两个桶被算两次。
func derivedHistoryBuckets(interval int) map[string]int {
	out := make(map[string]int, len(rangeWindows))
	for _, w := range rangeWindows {
		out[w.Key] = DeriveBucketSeconds(int(w.Duration.Seconds()), w.BucketSeconds, interval)
	}
	return out
}

// DeriveBucketSeconds 计算某个窗口在本周期下实际使用的桶宽。
func DeriveBucketSeconds(spanSeconds, baseBucket, interval int) int {
	if interval <= 0 {
		interval = DefaultScrapeIntervalSeconds
	}
	minByPoints := (spanSeconds + maxHistoryPoints - 1) / maxHistoryPoints
	bucket := baseBucket
	if bucket < interval {
		bucket = interval
	}
	if bucket < minByPoints {
		bucket = minByPoints
	}
	return ceilToMultiple(bucket, interval)
}

func ceilToMultiple(value, unit int) int {
	if unit <= 0 {
		return value
	}
	if value%unit == 0 {
		return value
	}
	return ((value / unit) + 1) * unit
}

// GetScrapeConfig 获取当前生效的采集频率配置（带进程内缓存）。
// 这是全局唯一的读取入口：调度、PromQL、历史聚合都必须走它，不许再读各自的常量。
func GetScrapeConfig() ScrapeConfig {
	scrapeConfigMutex.Lock()
	defer scrapeConfigMutex.Unlock()
	if scrapeConfigCache != nil && time.Since(scrapeConfigLoadedAt) < scrapeConfigCacheTTL {
		return *scrapeConfigCache
	}
	cfg := loadScrapeConfig()
	scrapeConfigCache = &cfg
	scrapeConfigLoadedAt = time.Now()
	return cfg
}

// ReloadScrapeConfig 立刻失效缓存。保存配置后调用，让改动不等 TTL 就生效。
func ReloadScrapeConfig() ScrapeConfig {
	scrapeConfigMutex.Lock()
	scrapeConfigCache = nil
	scrapeConfigMutex.Unlock()
	return GetScrapeConfig()
}

func loadScrapeConfig() ScrapeConfig {
	if global.App == nil || global.App.DB == nil {
		return deriveScrapeConfig(DefaultScrapeIntervalSeconds, false, "default", "", "", "")
	}
	var row monitorModels.InsightMonitorScrapeSettings
	err := global.App.DB.Where("id = ?", scrapeSettingsRowID).Take(&row).Error
	if err != nil {
		// 表没建好或行被删了：按默认值跑，不让监控整体停摆
		return deriveScrapeConfig(DefaultScrapeIntervalSeconds, false, "default", "未读取到采集频率设置，已按默认值运行", "", "")
	}
	interval, reason := NormalizeScrapeInterval(row.IntervalSeconds, row.AllowCustom)
	if reason != "" {
		warnScrapeConfig(reason)
	}
	updatedAt := ""
	if row.Model != nil {
		updatedAt = time.Time(row.UpdatedAt).Format("2006-01-02 15:04:05")
	}
	return deriveScrapeConfig(interval, row.AllowCustom, "db", reason, updatedAt, row.UpdatedByName)
}

// warnScrapeConfig 降级告警做限频：配置错了很要紧，但每 10 秒刷一条日志会把日志淹掉。
func warnScrapeConfig(reason string) {
	scrapeConfigMutex.Lock()
	should := time.Since(scrapeConfigLastWarn) > 5*time.Minute
	if should {
		scrapeConfigLastWarn = time.Now()
	}
	scrapeConfigMutex.Unlock()
	if should && global.App != nil && global.App.Log != nil {
		global.App.Log.Warnf("[monitor] 采集频率配置降级：%s", reason)
	}
}

// SaveScrapeSettings 保存采集频率并立即生效。
// 校验放这里而不是只放前端：接口是唯一入口，绕过页面的调用也必须被拦住。
func SaveScrapeSettings(interval int, allowCustom bool, operator string) (ScrapeConfig, error) {
	if err := ValidateScrapeInterval(interval, allowCustom); err != nil {
		return ScrapeConfig{}, err
	}
	if global.App == nil || global.App.DB == nil {
		return ScrapeConfig{}, fmt.Errorf("数据库未就绪")
	}
	updates := map[string]any{
		"interval_seconds": interval,
		"allow_custom":     allowCustom,
		"updated_by_name":  strings.TrimSpace(operator),
	}
	var row monitorModels.InsightMonitorScrapeSettings
	err := global.App.DB.Where("id = ?", scrapeSettingsRowID).Take(&row).Error
	if err != nil {
		// 行不存在（比如手工清过库）：补一行，保证"唯一数据源"始终存在
		row = monitorModels.InsightMonitorScrapeSettings{
			Model:           &commonModels.Model{ID: scrapeSettingsRowID},
			IntervalSeconds: interval,
			AllowCustom:     allowCustom,
			UpdatedByName:   strings.TrimSpace(operator),
		}
		if err := global.App.DB.Create(&row).Error; err != nil {
			return ScrapeConfig{}, err
		}
	} else {
		if err := global.App.DB.Model(&monitorModels.InsightMonitorScrapeSettings{}).
			Where("id = ?", scrapeSettingsRowID).Updates(updates).Error; err != nil {
			return ScrapeConfig{}, err
		}
	}
	cfg := ReloadScrapeConfig()
	if global.App.Log != nil {
		global.App.Log.Infof("[monitor] 采集频率已更新为 %d 秒（Prometheus 抓取 %ds / 落库 %ds / PromQL 窗口 %ds），操作人 %s",
			cfg.IntervalSeconds, cfg.PrometheusScrapeIntervalSeconds, cfg.CollectIntervalSeconds,
			cfg.PromRateWindowSeconds, strings.TrimSpace(operator))
	}
	return cfg, nil
}

// EnsureScrapeSettings 建表后播种：库里没有设置行时，用 config.yaml 的值（若有）或默认值创建。
// config.yaml 只在这里被读一次，之后一律以数据行为准 —— 避免出现"文件一个值、库里一个值"。
func EnsureScrapeSettings(db *gorm.DB) {
	if db == nil {
		return
	}
	var count int64
	if err := db.Model(&monitorModels.InsightMonitorScrapeSettings{}).Count(&count).Error; err != nil {
		return
	}
	if count > 0 {
		return
	}
	seed := DefaultScrapeIntervalSeconds
	if global.App != nil && global.App.ConfigViper != nil {
		if raw := global.App.ConfigViper.GetInt("monitor.scrape_interval_seconds"); raw > 0 {
			if v, reason := NormalizeScrapeInterval(raw, false); reason == "" {
				seed = v
			}
		}
	}
	row := monitorModels.InsightMonitorScrapeSettings{
		Model:           &commonModels.Model{ID: scrapeSettingsRowID},
		IntervalSeconds: seed,
	}
	if err := db.Create(&row).Error; err != nil && global.App != nil && global.App.Log != nil {
		global.App.Log.Warnf("[monitor] 播种采集频率设置失败: %v", err)
	}
}

// ResolveInstanceScrapeInterval 由平台周期派生实例级周期。
//
// 约束：实例级只能"放慢"，不能"加快"，且必须是平台周期的整数倍。
// 原因：Prometheus 的抓取间隔是全局的，实例跑得比它快只会取到重复值（同一个抓取结果被存两遍），
// 而周期不是整数倍时，桶边界会在时间轴上缓慢漂移，最终出现"某分钟有 2 个点、某分钟 0 个点"。
func ResolveInstanceScrapeInterval(instanceValue int, cfg ScrapeConfig) (int, string) {
	base := cfg.IntervalSeconds
	if base <= 0 {
		base = DefaultScrapeIntervalSeconds
	}
	if instanceValue <= 0 || instanceValue == base {
		return base, ""
	}
	if instanceValue < base {
		return base, fmt.Sprintf("实例周期 %d 秒小于平台周期 %d 秒（快于 Prometheus 抓取只会采到重复值），已按 %d 秒执行",
			instanceValue, base, base)
	}
	if instanceValue%base != 0 {
		rounded := ceilToMultiple(instanceValue, base)
		return rounded, fmt.Sprintf("实例周期 %d 秒不是平台周期 %d 秒的整数倍（会造成聚合桶错位），已向上取整为 %d 秒",
			instanceValue, base, rounded)
	}
	return instanceValue, ""
}

// ---------------------------------------------------------------------------
// Prometheus 侧：配置片段生成与一致性校验
// ---------------------------------------------------------------------------

// OperatorFromContext 取当前操作人，用于配置变更留痕。
func OperatorFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	raw, ok := jwt.ExtractClaims(c)["id"]
	if !ok {
		return ""
	}
	name, _ := raw.(string)
	return strings.TrimSpace(name)
}

// ScrapeIntervalDiagnostics 统计实例级周期与平台周期的偏差。
// 返回：实例总数、被校正的实例数、若干条说明（最多 3 条，避免日志被刷屏）。
func ScrapeIntervalDiagnostics() (int, int, []string) {
	if global.App == nil || global.App.DB == nil {
		return 0, 0, nil
	}
	var rows []struct {
		InstanceID            string
		ScrapeIntervalSeconds int
	}
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("instance_id, scrape_interval_seconds").
		Where("enabled = ?", true).Scan(&rows).Error; err != nil {
		return 0, 0, nil
	}
	cfg := GetScrapeConfig()
	adjusted := 0
	samples := make([]string, 0, 3)
	for _, row := range rows {
		if _, note := ResolveInstanceScrapeInterval(row.ScrapeIntervalSeconds, cfg); note != "" {
			adjusted++
			if len(samples) < 3 {
				samples = append(samples, fmt.Sprintf("%s（%s）", row.InstanceID, note))
			}
		}
	}
	return len(rows), adjusted, samples
}

var promScrapeIntervalPattern = regexp.MustCompile(`scrape_interval:\s*"?(\d+)([smhd]?)"?`)

// CheckPrometheusScrapeInterval 读取 Prometheus 实际生效的 scrape_interval 并与配置比对。
// 走 /api/v1/status/config（Prometheus 原生接口，返回当前加载的 yml 全文）。
// 返回：实际间隔（秒）、是否一致、错误信息。
func CheckPrometheusScrapeInterval(ctx context.Context, client *prometheusClient) (int, bool, error) {
	if client == nil || !client.Configured() {
		return 0, true, fmt.Errorf("未配置 Prometheus")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/v1/status/config", nil)
	if err != nil {
		return 0, true, err
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return 0, true, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, true, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, true, fmt.Errorf("Prometheus 返回状态码 %d", resp.StatusCode)
	}
	actual := parsePrometheusScrapeInterval(string(body))
	if actual <= 0 {
		return 0, true, fmt.Errorf("未能从 Prometheus 配置中解析出 scrape_interval")
	}
	return actual, actual == GetScrapeConfig().PrometheusScrapeIntervalSeconds, nil
}

// parsePrometheusScrapeInterval 从 yml 全文里取 global.scrape_interval，支持 s/m/h/d 后缀。
func parsePrometheusScrapeInterval(content string) int {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "scrape_interval") {
			continue
		}
		match := promScrapeIntervalPattern.FindStringSubmatch(trimmed)
		if len(match) < 3 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		switch match[2] {
		case "m":
			value *= 60
		case "h":
			value *= 3600
		case "d":
			value *= 86400
		}
		return value
	}
	return 0
}

// LogScrapeConfigSelfCheck 启动自检：把三处周期打成一行日志，并比对 Prometheus 实际值。
// 这是"三者一致"最直接的证据 —— 部署完看这一行日志就知道口径对没对上。
func LogScrapeConfigSelfCheck(promStatus PrometheusStatus, promClient *prometheusClient) {
	cfg := GetScrapeConfig()
	if global.App == nil || global.App.Log == nil {
		return
	}
	global.App.Log.Infof("[monitor] 采集频率自检：周期 %ds | Prometheus 抓取 %ds | 落库周期 %ds | PromQL 窗口 %ds | step %ds | 历史聚合 %v",
		cfg.IntervalSeconds, cfg.PrometheusScrapeIntervalSeconds, cfg.CollectIntervalSeconds,
		cfg.PromRateWindowSeconds, cfg.PromStepSeconds, cfg.HistoryBuckets)
	if cfg.Degraded {
		global.App.Log.Warnf("[monitor] 采集频率已降级运行：%s", cfg.DegradeReason)
	}
	// 实例级周期是否被校正：这决定了"页面看到的周期"和"实际执行的周期"是否一致
	if total, adjusted, samples := ScrapeIntervalDiagnostics(); adjusted > 0 {
		global.App.Log.Warnf("[monitor] %d/%d 个实例的周期被校正为平台周期的整数倍：%v", adjusted, total, samples)
	}
	if !promStatus.Configured {
		global.App.Log.Infof("[monitor] 未配置 Prometheus，跳过抓取间隔一致性校验")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	actual, consistent, err := CheckPrometheusScrapeInterval(ctx, promClient)
	if err != nil {
		global.App.Log.Warnf("[monitor] 无法校验 Prometheus 抓取间隔：%v", err)
		return
	}
	if !consistent {
		global.App.Log.Warnf("[monitor] 周期不一致：JaySQL 配置 %ds，Prometheus 实际 %ds。"+
			"主机指标会按 Prometheus 的节奏刷新、按 JaySQL 的节奏落库，两者对不上。"+
			"请按 GET /api/v1/monitor/config/scrape 返回的片段更新 prometheus.yml 并 reload",
			cfg.PrometheusScrapeIntervalSeconds, actual)
		return
	}
	global.App.Log.Infof("[monitor] Prometheus 抓取间隔校验一致：%ds", actual)
}
