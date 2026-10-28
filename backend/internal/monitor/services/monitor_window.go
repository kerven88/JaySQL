package services

import (
	"fmt"
	"strings"
	"time"
)

// monitor_window.go —— 大屏统计窗口。
//
// 大屏的时间筛选有两类用法：
//  1. 预设区间：最近 5 分钟 / 30 分钟 / 1 小时 / 6 小时 / 1 天 / 7 天
//     （统一用分钟表达，分钟粒度比整数小时更细，整数小时表达不了 5 分钟）；
//  2. 自定义区间：用户任选起止时刻，例如排查昨晚 23:10 的一次抖动。
//
// 两类最终都归一成同一个 [Start, End] 窗口对象，后续所有区间查询
// （时段内新增告警、区间 QPS 均值/连接峰值、已恢复告警、环比快照）
// 只认这个窗口，避免各处各自算 cutoff 导致口径漂移。
//
// 硬规则：窗口解析失败直接返回错误给调用方（400），不做"猜一个差不多的区间"——
// 用错的时间范围算出来的数字比报错危险得多。

const (
	// defaultWindowMinutes 未指定区间时的默认窗口（下拉框默认选中「最近30分钟」）。
	defaultWindowMinutes = 30
	// maxWindowMinutes 窗口上限 30 天：防止手抖传入超大区间把聚合查询拖垮。
	maxWindowMinutes = 30 * 24 * 60
	// minWindowMinutes 窗口下限 1 分钟：起止几乎重叠时收敛为 1 分钟，而不是给空窗口。
	minWindowMinutes = 1
)

// customTimeLayouts 自定义起止时间支持的格式。
// 前端 a-range-picker 用 value-format="YYYY-MM-DD HH:mm:ss" 提交第一种。
var customTimeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05",
	time.RFC3339,
}

// monitorWindow 大屏统计窗口（左开右闭：(Start, End]）。
type monitorWindow struct {
	Start  time.Time
	End    time.Time
	Custom bool // true = 用户自定义起止，false = 预设相对区间
}

// Span 窗口跨度。
func (w monitorWindow) Span() time.Duration {
	return w.End.Sub(w.Start)
}

// Minutes 窗口跨度（分钟，向上取整，最小 1）。
func (w monitorWindow) Minutes() int {
	minutes := int(w.Span().Minutes())
	if w.Span()%time.Minute != 0 {
		minutes++
	}
	if minutes < minWindowMinutes {
		return minWindowMinutes
	}
	return minutes
}

// Label 区间文案：「最近30分钟」/「最近6小时」/「最近1天」/「09-16 07:00 ~ 09-16 08:30」。
//
// 取整口径自上而下：整天（含 1 天，即 1440 分钟）> 整小时 > 分钟。
// 1 天必须是「最近1天」而不是「最近24小时」——下拉框选项就叫「最近1天」，
// 两处文案不一致会让用户以为选的和看到的是两个区间。
func (w monitorWindow) Label() string {
	if w.Custom {
		return fmt.Sprintf("%s ~ %s",
			w.Start.Format("01-02 15:04"), w.End.Format("01-02 15:04"))
	}
	minutes := w.Minutes()
	switch {
	case minutes%1440 == 0:
		return fmt.Sprintf("最近%d天", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("最近%d小时", minutes/60)
	default:
		return fmt.Sprintf("最近%d分钟", minutes)
	}
}

// ShortLabel 表格列头等窄位文案：「30min」/「6h」/「7d」/「自定义」。
func (w monitorWindow) ShortLabel() string {
	if w.Custom {
		return "自定义"
	}
	minutes := w.Minutes()
	switch {
	case minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dmin", minutes)
	}
}

// resolveMonitorWindow 解析大屏统计窗口。
//
// 优先级：start_time/end_time（自定义）> range_minutes > range_hours（兼容旧参数）> 默认 30 分钟。
// 自定义区间规则：
//   - 起始时间必填，缺失报错（不猜）；
//   - 结束时间缺省为"当前时刻"；
//   - 结束时间落在未来 → 收敛到当前时刻（未来没有数据，给未来时刻只会让图空着）；
//   - 起始时间不早于结束时间 → 报错；
//   - 跨度收敛进 [1 分钟, 30 天]。
func resolveMonitorWindow(rangeMinutes, rangeHours int, startRaw, endRaw string) (monitorWindow, error) {
	startRaw = strings.TrimSpace(startRaw)
	endRaw = strings.TrimSpace(endRaw)
	now := time.Now()

	if startRaw != "" || endRaw != "" {
		if startRaw == "" {
			return monitorWindow{}, fmt.Errorf("自定义时间范围缺少起始时间")
		}
		start, err := parseCustomTime(startRaw)
		if err != nil {
			return monitorWindow{}, fmt.Errorf("起始时间格式无法识别：%s（支持 YYYY-MM-DD HH:mm:ss）", startRaw)
		}
		end := now
		if endRaw != "" {
			parsed, err := parseCustomTime(endRaw)
			if err != nil {
				return monitorWindow{}, fmt.Errorf("结束时间格式无法识别：%s（支持 YYYY-MM-DD HH:mm:ss）", endRaw)
			}
			end = parsed
		}
		if !end.After(start) {
			return monitorWindow{}, fmt.Errorf("自定义时间范围的起始时间必须早于结束时间")
		}
		if end.After(now) {
			end = now
		}
		if !end.After(start) {
			return monitorWindow{}, fmt.Errorf("自定义时间范围的起始时间必须早于当前时刻")
		}
		// 跨度收敛
		if span := end.Sub(start); span < minWindowMinutes*time.Minute {
			start = end.Add(-minWindowMinutes * time.Minute)
		} else if span > maxWindowMinutes*time.Minute {
			start = end.Add(-maxWindowMinutes * time.Minute)
		}
		return monitorWindow{Start: start, End: end, Custom: true}, nil
	}

	minutes := rangeMinutes
	if minutes <= 0 && rangeHours > 0 {
		minutes = rangeHours * 60
	}
	if minutes <= 0 {
		minutes = defaultWindowMinutes
	}
	if minutes > maxWindowMinutes {
		minutes = maxWindowMinutes
	}
	if minutes < minWindowMinutes {
		minutes = minWindowMinutes
	}
	return monitorWindow{
		Start: now.Add(-time.Duration(minutes) * time.Minute),
		End:   now,
	}, nil
}

func parseCustomTime(raw string) (time.Time, error) {
	for _, layout := range customTimeLayouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %s", raw)
}
