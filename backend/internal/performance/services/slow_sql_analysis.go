package services

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
)

type slowSQLTrendPoint struct {
	TimeLabel      string  `json:"time_label"`
	SlowSQLCount   int64   `json:"slow_sql_count"`
	AvgDurationMS  float64 `json:"avg_duration_ms"`
	FullScanCount  int64   `json:"full_scan_count"`
	BucketTimeText string  `json:"bucket_time_text"`
}

type GetSlowSQLAnalysisService struct {
	Form     *forms.GetSlowSQLAnalysisForm
	C        *gin.Context
	Username string
}

func (s *GetSlowSQLAnalysisService) Run() (map[string]any, error) {
	instanceID := strings.TrimSpace(s.Form.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("实例ID不能为空")
	}

	var instance instanceOptionRow
	instances, err := queryPerformanceInstances("")
	if err != nil {
		return nil, err
	}
	for _, item := range instances {
		if item.InstanceID == instanceID {
			instance = item
			break
		}
	}
	if strings.TrimSpace(instance.InstanceID) == "" {
		return nil, fmt.Errorf("实例不存在：%s 可能已被删除或尚未接入", instanceID)
	}

	startAt, endAt := resolveAnalysisRange(s.Form.StartAt, s.Form.EndAt)

	var summary struct {
		SlowSQLCount  int64
		AvgDurationMS float64
		MaxDurationMS int64
		FullScanCount int64
	}
	if err := global.App.DB.Table("insight_performance_slowlog_entries r").
		Select(`
                        count(*) as slow_sql_count,
                        ifnull(avg(r.query_time_ms), 0) as avg_duration_ms,
                        ifnull(max(r.query_time_ms), 0) as max_duration_ms,
                        ifnull(sum(case
                                when r.rows_examined >= greatest(ifnull(r.rows_sent, 0) * 20, 10000) then 1
                                else 0
                        end), 0) as full_scan_count
                `).
		Where("r.instance_id = ?", instanceID).
		Where("r.event_time >= ? and r.event_time <= ?", startAt, endAt).
		Scan(&summary).Error; err != nil {
		return nil, err
	}

	fullScanRatio := 0.0
	if summary.SlowSQLCount > 0 {
		fullScanRatio = float64(summary.FullScanCount) * 100 / float64(summary.SlowSQLCount)
	}

	trend, trendErr := querySlowSQLTrend(instanceID, startAt, endAt)
	if trendErr != nil {
		return nil, trendErr
	}

	return map[string]any{
		"instance": instance,
		"summary": map[string]any{
			"slow_sql_count":  summary.SlowSQLCount,
			"avg_duration_ms": summary.AvgDurationMS,
			"max_duration_ms": summary.MaxDurationMS,
			"full_scan_ratio": fullScanRatio,
			"full_scan_count": summary.FullScanCount,
			"range_start_at":  startAt.Format("2006-01-02 15:04:05"),
			"range_end_at":    endAt.Format("2006-01-02 15:04:05"),
			"range_label":     buildRangeLabel(startAt, endAt),
		},
		"trend": trend,
	}, nil
}

func resolveAnalysisRange(startAt, endAt string) (time.Time, time.Time) {
	now := time.Now()
	end := now
	start := now.Add(-time.Hour)
	if parsed, ok := parseAnalysisTime(startAt); ok {
		start = parsed
	}
	if parsed, ok := parseAnalysisTime(endAt); ok {
		end = parsed
	}
	if !end.After(start) {
		end = start.Add(time.Hour)
	}
	return start, end
}

func parseAnalysisTime(value string) (time.Time, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func buildRangeLabel(startAt, endAt time.Time) string {
	if endAt.Sub(startAt) <= time.Hour+time.Minute {
		return "最近1小时"
	}
	return fmt.Sprintf("%s - %s", startAt.Format("2006-01-02 15:04"), endAt.Format("2006-01-02 15:04"))
}

func querySlowSQLTrend(instanceID string, startAt, endAt time.Time) ([]slowSQLTrendPoint, error) {
	bucketSeconds := chooseTrendBucketSeconds(startAt, endAt)
	startText := startAt.Format("2006-01-02 15:04:05")
	bucketIndexExpr := fmt.Sprintf("FLOOR(TIMESTAMPDIFF(SECOND, '%s', r.event_time) / %d)", startText, bucketSeconds)
	var rows []struct {
		BucketIndex   int64
		SlowSQLCount  int64
		AvgDurationMS float64
		FullScanCount int64
	}
	if err := global.App.DB.Table("insight_performance_slowlog_entries r").
		Select(fmt.Sprintf(`
			%s as bucket_index,
                        count(*) as slow_sql_count,
                        ifnull(avg(r.query_time_ms), 0) as avg_duration_ms,
                        ifnull(sum(case
                                when r.rows_examined >= greatest(ifnull(r.rows_sent, 0) * 20, 10000) then 1
                                else 0
                        end), 0) as full_scan_count
		`, bucketIndexExpr)).
		Where("r.instance_id = ?", instanceID).
		Where("r.event_time >= ? and r.event_time < ?", startAt, endAt).
		Group(bucketIndexExpr).
		Order(fmt.Sprintf("%s asc", bucketIndexExpr)).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	rowMap := make(map[int64]struct {
		SlowSQLCount  int64
		AvgDurationMS float64
		FullScanCount int64
	}, len(rows))
	for _, item := range rows {
		rowMap[item.BucketIndex] = struct {
			SlowSQLCount  int64
			AvgDurationMS float64
			FullScanCount int64
		}{
			SlowSQLCount:  item.SlowSQLCount,
			AvgDurationMS: item.AvgDurationMS,
			FullScanCount: item.FullScanCount,
		}
	}

	totalBuckets := int(math.Ceil(endAt.Sub(startAt).Seconds() / float64(bucketSeconds)))
	if totalBuckets <= 0 {
		totalBuckets = 1
	}

	points := make([]slowSQLTrendPoint, 0, totalBuckets)
	for bucketIndex := 0; bucketIndex < totalBuckets; bucketIndex++ {
		bucketStart := startAt.Add(time.Duration(bucketIndex*bucketSeconds) * time.Second)
		aggregated := rowMap[int64(bucketIndex)]
		points = append(points, slowSQLTrendPoint{
			TimeLabel:      bucketStart.Format("15:04"),
			BucketTimeText: bucketStart.Format("2006-01-02 15:04:05"),
			SlowSQLCount:   aggregated.SlowSQLCount,
			AvgDurationMS:  aggregated.AvgDurationMS,
			FullScanCount:  aggregated.FullScanCount,
		})
	}
	return points, nil
}

func chooseTrendBucketSeconds(startAt, endAt time.Time) int {
	span := endAt.Sub(startAt)
	switch {
	case span <= 2*time.Hour:
		return 300
	case span <= 12*time.Hour:
		return 900
	case span <= 48*time.Hour:
		return 3600
	default:
		return 14400
	}
}
