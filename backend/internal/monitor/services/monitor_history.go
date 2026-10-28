package services

// ============================================================================
// 指标历史（趋势图）取数
//
// 页面「时间范围」下拉框选中 15m / 1h / 6h / 24h / 7d 后，调用本服务取回每个指标的序列，
// 前端据此重绘趋势图 —— 曲线必须来自真实采集点，不允许前端造数据。
//
// 窗口与分桶在后端定义的原因：
//  1. 前端自己算起止时间会导致"同一范围在不同端画出不同点数"；
//  2. 分桶聚合（AVG）必须在服务端做，否则 7 天范围要把几十万原始点传到浏览器。
// ============================================================================

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
)

// MetricHistoryPoint 趋势图上的一个点。
// Value 为 nil 表示该桶没有有效采集值（不是 0），前端渲染成断点而不是跌到 0。
type MetricHistoryPoint struct {
	Ts    string   `json:"ts"`
	Value *float64 `json:"value"`
}

type MetricHistorySeries struct {
	Key    string               `json:"key"`
	Points []MetricHistoryPoint `json:"points"`
}

type MetricHistoryData struct {
	InstanceID string `json:"instance_id"`
	// Range 实际生效的范围（前端下拉值）；自定义区间时是 custom
	Range string `json:"range"`
	// RangeLabel 中文标签，直接用于页面展示，避免前端再维护一份映射
	RangeLabel    string `json:"range_label"`
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	BucketSeconds int    `json:"bucket_seconds"`
	// PointCount 每个序列应有的点数（前端据此判断断点）
	PointCount int                   `json:"point_count"`
	Series     []MetricHistorySeries `json:"series"`
}

// rangeWindow 时间窗口配置：与页面下拉一一对应。
type rangeWindow struct {
	Key           string
	Label         string
	Duration      time.Duration
	BucketSeconds int
}

// BucketSeconds 是该窗口的「期望桶宽」，不是最终桶宽：实际桶宽由
// DeriveBucketSeconds 在「期望桶宽 / 采集周期 / 上限点数」三者间取大者，
// 再向上对齐到采集周期的整数倍。
//
// 分档口径（与 bucketForSpan 共用同一套边界，避免预设窗口与自定义区间打架）：
//
//	≤ 24h → 15s   不做聚合，直接呈现原始采样密度（周期 15s 时即 1:1 还原每个采样点）
//	≤ 7d  → 60s
//	≤ 30d → 600s（10 分钟）
//	> 30d → 3600s（1 小时）
//
// 24h 以内保持 15s 是为了让短周期窗口看到真实抖动，而不是被 AVG 抹平的平均线。
var rangeWindows = []rangeWindow{
	{"15m", "最近15分钟", 15 * time.Minute, 15},
	{"1h", "最近1小时", time.Hour, 15},
	{"6h", "最近6小时", 6 * time.Hour, 15},
	{"24h", "最近24小时", 24 * time.Hour, 15},
	{"7d", "最近7天", 7 * 24 * time.Hour, 60},
}

const defaultRangeKey = "1h"

func rangeWindowOf(key string) rangeWindow {
	for _, item := range rangeWindows {
		if item.Key == key {
			return item
		}
	}
	for _, item := range rangeWindows {
		if item.Key == defaultRangeKey {
			return item
		}
	}
	return rangeWindow{defaultRangeKey, "最近1小时", time.Hour, 15}
}

// GetMetricHistoryService 取指标历史序列。
type GetMetricHistoryService struct {
	*forms.GetMetricHistoryForm
	C *gin.Context
}

func (s *GetMetricHistoryService) Run() (*MetricHistoryData, error) {
	if s.GetMetricHistoryForm == nil {
		return nil, fmt.Errorf("参数缺失")
	}
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("实例ID不能为空")
	}

	end := time.Now()
	start := end
	window := rangeWindowOf(strings.TrimSpace(s.Range))
	rangeKey := window.Key

	// 自定义区间：起止都给了才生效，否则回落到预设窗口（不给半截区间制造歧义）。
	if strings.EqualFold(strings.TrimSpace(s.Range), "custom") || strings.TrimSpace(s.StartTime) != "" {
		layout := "2006-01-02 15:04:05"
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(s.StartTime), time.Local); err == nil {
			start = parsed
			rangeKey = "custom"
			if parsedEnd, err := time.ParseInLocation(layout, strings.TrimSpace(s.EndTime), time.Local); err == nil && parsedEnd.After(start) {
				end = parsedEnd
			}
			// 自定义区间按跨度自动选桶，保证曲线有足够采样密度（约 60～120 点），
			// 而不是旧版的十几个点的生硬折线
			window.BucketSeconds = bucketForSpan(end.Sub(start))
		} else {
			start = end.Add(-window.Duration)
		}
	} else {
		start = end.Add(-window.Duration)
	}

	// 桶宽由采集频率派生（唯一数据源）：必须是采集周期的整数倍。
	// 否则桶边界会在时间轴上缓慢漂移，出现"某分钟两个点、某分钟没有点"的假缺口，
	// 而实际上一个点都没丢。见 monitor_scrape_config.go 的 DeriveBucketSeconds。
	window.BucketSeconds = DeriveBucketSeconds(int(end.Sub(start).Seconds()), window.BucketSeconds, GetScrapeConfig().IntervalSeconds)

	data := &MetricHistoryData{
		InstanceID:    instanceID,
		Range:         rangeKey,
		RangeLabel:    window.Label,
		StartTime:     start.Format(time.DateTime),
		EndTime:       end.Format(time.DateTime),
		BucketSeconds: window.BucketSeconds,
		PointCount:    int(end.Sub(start).Seconds()/float64(window.BucketSeconds)) + 1,
		Series:        make([]MetricHistorySeries, 0, 16),
	}
	if global.App.DB == nil {
		return data, nil
	}

	keys := parseMetricKeys(s.Keys)

	// 模板勾选门控：MySQL / PostgreSQL 的实例只能取到生效模板已勾选的指标。
	// 客户端不传 keys 时同样收口到模板范围，避免绕过页面直接拉全量。
	if gate := displayHistoryGate(instanceID); gate != nil {
		if len(keys) == 0 {
			keys = gate.Keys
		} else {
			keys = filterKeysByTemplate(keys, gate)
		}
	}

	// 按桶聚合：FLOOR(UNIX_TIMESTAMP(ts)/bucket)*bucket 把时间对齐到桶起点。
	// 用 AVG 是因为看趋势时关心的是"这段时间的平均水平"，尖刺由告警负责发现。
	type row struct {
		MetricKey string
		BucketTs  time.Time
		Value     float64
	}
	query := global.App.DB.Table("insight_monitor_metric_points").
		Select("metric_key, FROM_UNIXTIME(FLOOR(UNIX_TIMESTAMP(ts) / ?) * ?) AS bucket_ts, AVG(value) AS value",
			window.BucketSeconds, window.BucketSeconds).
		Where("instance_id = ? AND ts >= ? AND ts <= ? AND value IS NOT NULL", instanceID, start, end)
	if len(keys) > 0 {
		query = query.Where("metric_key IN ?", keys)
	}
	rows := make([]row, 0, 256)
	if err := query.Group("metric_key, bucket_ts").Order("metric_key, bucket_ts").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取指标历史失败: %v", err)
	}

	byKey := make(map[string][]MetricHistoryPoint)
	for _, item := range rows {
		value := item.Value
		byKey[item.MetricKey] = append(byKey[item.MetricKey], MetricHistoryPoint{
			Ts:    item.BucketTs.Format(time.DateTime),
			Value: &value,
		})
	}
	for key, points := range byKey {
		data.Series = append(data.Series, MetricHistorySeries{Key: key, Points: points})
	}
	sort.SliceStable(data.Series, func(i, j int) bool { return data.Series[i].Key < data.Series[j].Key })
	return data, nil
}

// bucketForSpan 自定义区间按跨度选桶，与 rangeWindows 共用同一套分档边界：
//
//	≤ 24h → 15s；≤ 7d → 60s；≤ 30d → 600s；> 30d → 3600s
//
// 之后还会经 DeriveBucketSeconds 对齐到采集周期的整数倍（并受点数上限兜底）。
//
// 这里不再用「目标 60~120 点」的旧口径：那会把 24 小时压成 300s+ 的桶，
// 短时尖峰被 AVG 抹平，趋势图上只剩一条平均线。
func bucketForSpan(span time.Duration) int {
	seconds := int(span.Seconds())
	if seconds <= 0 {
		return 15
	}
	const (
		day   = 24 * 60 * 60
		week  = 7 * day
		month = 30 * day
	)
	switch {
	case seconds <= day:
		return 15
	case seconds <= week:
		return 60
	case seconds <= month:
		return 600
	default:
		return 3600
	}
}

func parseMetricKeys(raw string) []string {
	result := make([]string, 0, 8)
	for _, part := range strings.Split(raw, ",") {
		key := canonicalMetricKey(part)
		if key != "" {
			result = append(result, key)
		}
	}
	return result
}
