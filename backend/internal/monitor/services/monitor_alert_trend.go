package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// ============================================================================
// 大屏「近 7 天告警趋势」卡：按预设窗口统计每段新增告警数。
//
// 与页面顶部时间范围刻意解耦（全局管实时、本卡管中长期），只接受预设窗口
// today / 3d / 7d / 14d。统计口径 = 每段「新增」告警数，落桶时间用
// COALESCE(first_seen_at, triggered_at)（生命周期字段是后加的，老事件可能为
// NULL），与大屏 alerts_in_range 同一份口径，不给第三种解释空间。
// 模板外指标沿用同一份门控（excludeByInstance），失败不留半个口径。
// ============================================================================

// AlertTrendPoint 趋势单段：起止时间（本地时区 "2006-01-02 15:04:05"）+ 展示标签 + 计数。
// DrillLabel 供下钻弹窗标题使用（如「9月15日」「9月21日 14:00–15:00」），口径自洽。
// UnresolvedCount 该段新增中"仍未恢复"的条数（按当下回看的状态归类，
// 随修复进度变化），供前端柱体做 未恢复/已恢复 二分堆叠。
type AlertTrendPoint struct {
	Start           string `json:"start"`
	End             string `json:"end"`
	Label           string `json:"label"`
	DrillLabel      string `json:"drill_label"`
	Count           int    `json:"count"`
	UnresolvedCount int    `json:"unresolved_count"`
}

// AlertTrendData 趋势响应。Title 随预设联动（「近 7 天告警趋势」等），时间写进标题。
type AlertTrendData struct {
	Preset string            `json:"preset"`
	Title  string            `json:"title"`
	Points []AlertTrendPoint `json:"points"`
}

// alertTrendBucket 内部分桶结构。
type alertTrendBucket struct {
	start, end time.Time
	label      string
	drill      string
}

type GetAlertTrendService struct {
	*forms.GetAlertTrendForm
	C *gin.Context
}

var alertTrendPresetTitles = map[string]string{
	"today": "今日告警趋势",
	"3d":    "近3天告警趋势",
	"7d":    "近7天告警趋势",
	"14d":   "近14天告警趋势",
}

// Run 返回趋势序列。段数固定（今日=到当前小时的每小时、其余=按自然日），
// 无告警的段补 0——前端不补日期轴，补错时区比不补更难排查。
func (s *GetAlertTrendService) Run() (*AlertTrendData, error) {
	preset := s.Preset
	if _, ok := alertTrendPresetTitles[preset]; !ok {
		// 未知预设一律回 7d：宁可给默认口径，不给空图
		preset = "7d"
	}
	data := &AlertTrendData{Preset: preset, Title: alertTrendPresetTitles[preset], Points: make([]AlertTrendPoint, 0)}
	if global.App.DB == nil {
		return data, nil
	}

	now := time.Now()
	buckets := alertTrendBuckets(preset, now)
	if len(buckets) == 0 {
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

	// 轻量列扫描 + Go 侧分桶：门控是每实例一份集合，单条 SQL 表达不了（同 stats 页做法）
	type trendRow struct {
		Ts         time.Time
		InstanceID string
		MetricKey  string
		Status     string
	}
	eventRows := make([]trendRow, 0, 256)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("instance_id IN ?", instanceIDs).
		Where("COALESCE(first_seen_at, triggered_at) >= ? AND COALESCE(first_seen_at, triggered_at) < ?",
			commonLocalTime(buckets[0].start), commonLocalTime(buckets[len(buckets)-1].end)).
		Select("COALESCE(first_seen_at, triggered_at) AS ts, instance_id, metric_key, status").
		Find(&eventRows).Error; err != nil {
		return nil, err
	}

	counts := make([]int, len(buckets))
	unresolved := make([]int, len(buckets))
	for _, row := range eventRows {
		if exclude := excludeByInstance[row.InstanceID]; exclude != nil && exclude(row.MetricKey) {
			continue
		}
		for i := len(buckets) - 1; i >= 0; i-- {
			// 从最新段往回找：绝大多数事件落在末段附近，少走空循环
			if !row.Ts.Before(buckets[i].start) && row.Ts.Before(buckets[i].end) {
				counts[i]++
				if trendEventUnresolved(row.Status) {
					unresolved[i]++
				}
				break
			}
		}
	}

	for i, b := range buckets {
		data.Points = append(data.Points, AlertTrendPoint{
			Start:           b.start.Format("2006-01-02 15:04:05"),
			End:             b.end.Format("2006-01-02 15:04:05"),
			Label:           b.label,
			DrillLabel:      b.drill,
			Count:           counts[i],
			UnresolvedCount: unresolved[i],
		})
	}
	return data, nil
}

// trendEventUnresolved 判定趋势柱「未恢复」段：status != recovered。
// 刻意与下钻弹窗的分组口径（AlertDrillModal 的 isRecovered，仅看 status）
// 保持一致，而不是复用 isUnresolvedAlertEvent（它额外排除 ignored）——
// 弹窗把 ignored+未恢复的行放在主列表，柱体若把它们算进「已恢复」段，
// 点柱核对时两边的分组就会对不上；增量计数本身也不过滤 ignored（与既有 count 同口径）。
func trendEventUnresolved(status string) bool {
	return !strings.EqualFold(strings.TrimSpace(status), "recovered")
}

// alertTrendBuckets 生成分桶边界（纯函数，可测）：
//   - today：当天 00:00 起每小时一段，只到当前小时（未来时段不给"0"的错觉）；
//     最后一段 end 截到 now。
//   - 3d/7d/14d：按本地时区自然日分桶（含今天），最后一段 end 截到 now。
func alertTrendBuckets(preset string, now time.Time) []alertTrendBucket {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if preset == "today" {
		n := now.Hour() + 1
		buckets := make([]alertTrendBucket, 0, n)
		for i := 0; i < n; i++ {
			start := dayStart.Add(time.Duration(i) * time.Hour)
			end := start.Add(time.Hour)
			if end.After(now) {
				end = now
			}
			buckets = append(buckets, alertTrendBucket{
				start: start,
				end:   end,
				label: fmt.Sprintf("%d时", i),
				drill: fmt.Sprintf("%d月%d日 %02d:00–%02d:00", int(now.Month()), now.Day(), i, i+1),
			})
		}
		return buckets
	}
	days := map[string]int{"3d": 3, "7d": 7, "14d": 14}[preset]
	if days == 0 {
		return nil
	}
	firstStart := dayStart.AddDate(0, 0, -(days - 1))
	buckets := make([]alertTrendBucket, 0, days)
	for i := 0; i < days; i++ {
		start := firstStart.AddDate(0, 0, i)
		end := start.AddDate(0, 0, 1)
		if end.After(now) {
			end = now
		}
		buckets = append(buckets, alertTrendBucket{
			start: start,
			end:   end,
			label: fmt.Sprintf("%d/%d", int(start.Month()), start.Day()),
			drill: fmt.Sprintf("%d月%d日", int(start.Month()), start.Day()),
		})
	}
	return buckets
}
