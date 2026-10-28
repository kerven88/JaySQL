package services

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// ============================================================================
// 告警概览统计：概览 Tab 的「今日已恢复」与「近 7 天趋势」数据源。
//
// 与告警中心列表同三条硬口径：模板外指标过滤、孤儿实例不展示、算不出留空。
// 故意不复用 /monitor/alerts 的 summary：概览要的两样东西（当日恢复数、按天分档
// 触发数）都是时间维度聚合，列表 summary 是状态维度切片，硬塞会互相污染口径。
// ============================================================================

// AlertStatsTrendPoint 近 7 天中某一天的触发分档计数。
type AlertStatsTrendPoint struct {
	// Date 展示用 "MM-DD"（本地时区）。Key 供前端对齐排序，格式 "2006-01-02"。
	Date string `json:"date"`
	Key  string `json:"key"`
	// Total 当日新增触发的事件数（含 pending；pending 未转正也是"当天响过一次铃"）。
	Total int `json:"total"`
	P0    int `json:"p0"`
	P1    int `json:"p1"`
	P2    int `json:"p2"`
	P3    int `json:"p3"`
}

// AlertStatsData 概览统计响应。
type AlertStatsData struct {
	// TodayRecovered 今日恢复的告警数（按恢复时间计，与触发时间无关——
	// 一条三周前触发、今天才恢复的 P1 算今天的"消化量"）。
	TodayRecovered int                    `json:"today_recovered"`
	Trend          []AlertStatsTrendPoint `json:"trend"`
}

// alertTrendRow 趋势聚合的轻量行：只扫聚合要用的四列。
type alertTrendRow struct {
	TriggeredAt time.Time
	Severity    string
	InstanceID  string
	MetricKey   string
}

type GetAlertStatsService struct {
	*forms.GetAlertStatsForm
	C *gin.Context
}

// 趋势窗口默认与上限：默认近 7 天，前端可选窗口最长 30 天（精度到日）。
const (
	alertStatsTrendDefaultDays = 7
	alertStatsTrendMaxDays     = 30
)

// trendDays 归一化趋势窗口：非法/超限值收敛到 [1, 30]，默认 7。
func trendDays(days int) int {
	if days <= 0 {
		return alertStatsTrendDefaultDays
	}
	if days > alertStatsTrendMaxDays {
		return alertStatsTrendMaxDays
	}
	return days
}

// Run 返回概览统计数据。趋势固定 span 个点（含今天，默认 7 天、最长 30 天），
// 无告警的日补 0，前端不需要自己补齐日期轴——补错时区比不补更难排查。
func (s *GetAlertStatsService) Run() (*AlertStatsData, error) {
	data := &AlertStatsData{Trend: make([]AlertStatsTrendPoint, 0)}
	if global.App.DB == nil {
		return data, nil
	}

	rows, err := listMonitorInstances(monitorListFilter{
		Search:      s.Search,
		Environment: s.Environment,
		DbType:      s.DbType,
		Business:    s.Business,
		GroupName:   s.GroupName,
		InstanceID:  s.InstanceID,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return data, nil
	}
	excludeByInstance := make(map[string]func(string) bool, len(rows))
	instanceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		instanceIDs = append(instanceIDs, row.InstanceID)
		excludeByInstance[row.InstanceID] = alertMetricExcludedByTemplate(row)
	}

	span := trendDays(s.Days)
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	windowStart := todayStart.AddDate(0, 0, -(span - 1))

	// 趋势：近 span 天触发的事件，轻量列扫描后在 Go 侧聚合（沿用模板外指标过滤的
	// 同一份门控——这条门控是每实例一份集合，单条 SQL 表达不了）。
	trendRows := make([]alertTrendRow, 0, 256)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("instance_id IN ?", instanceIDs).
		Where("triggered_at >= ?", windowStart).
		Select("triggered_at, severity, instance_id, metric_key").
		Find(&trendRows).Error; err != nil {
		return nil, err
	}
	kept := make([]alertTrendRow, 0, len(trendRows))
	for _, row := range trendRows {
		if exclude := excludeByInstance[row.InstanceID]; exclude != nil && exclude(row.MetricKey) {
			continue
		}
		kept = append(kept, row)
	}
	data.Trend = buildAlertTrend(kept, windowStart, now, span)

	// 今日恢复：按恢复时间窗口单独查（触发时间可能在 7 天窗口之外）。
	type recoveredRow struct {
		InstanceID string
		MetricKey  string
	}
	recoveredRows := make([]recoveredRow, 0, 64)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("instance_id IN ?", instanceIDs).
		Where("status = ?", "recovered").
		Where("recovered_at >= ?", todayStart).
		Select("instance_id, metric_key").
		Find(&recoveredRows).Error; err != nil {
		return nil, err
	}
	for _, row := range recoveredRows {
		if exclude := excludeByInstance[row.InstanceID]; exclude != nil && exclude(row.MetricKey) {
			continue
		}
		data.TodayRecovered++
	}
	return data, nil
}

// buildAlertTrend 把触发行聚合成固定 span 点的趋势序列（纯函数，可测）。
// 按本地时区的自然日分桶；补 0 点保证前端拿到的数组下标即日期序。
func buildAlertTrend(rows []alertTrendRow, windowStart, now time.Time, span int) []AlertStatsTrendPoint {
	if span <= 0 {
		span = alertStatsTrendDefaultDays
	}
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	idx := make([]AlertStatsTrendPoint, span)
	for i := 0; i < span; i++ {
		day := windowStart.AddDate(0, 0, i)
		idx[i] = AlertStatsTrendPoint{
			Date: day.Format("01-02"),
			Key:  day.Format("2006-01-02"),
		}
	}
	for _, row := range rows {
		ts := row.TriggeredAt
		days := int(todayStart.Sub(time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, ts.Location())).Hours() / 24)
		if days > span-1 {
			// 窗口外的旧事件直接丢弃（SQL 已限窗，这里防御时钟/时区脏数据），绝不挤进首日桶
			continue
		}
		if days < 0 {
			// 未来时间戳（时钟漂移）落到今天，不能越界
			days = 0
		}
		pos := span - 1 - days // 越新越靠后
		idx[pos].Total++
		switch strings.ToUpper(row.Severity) {
		case "P0":
			idx[pos].P0++
		case "P1":
			idx[pos].P1++
		case "P3":
			idx[pos].P3++
		default:
			idx[pos].P2++
		}
	}
	return idx
}
