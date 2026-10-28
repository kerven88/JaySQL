package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ============================================================================
// 告警中心：告警事件的检索与统计
//
// 背景：改造前告警只有"大屏上那一屏"——大屏是"此刻"的切片，且只返回未恢复的
// 事件，值班同学想查"昨天下午那条 P1 什么时候恢复的""谁认领了""有没有被忽略"
// 全都查不到。告警中心补的就是这条时间线。
//
// 三条硬口径（与既有链路保持一致，不另起炉灶）：
//   1. 模板外指标的历史事件照样过滤 —— 复用 alertMetricExcludedByTemplate，
//      与 loadActiveEventMap / evaluateInstanceAlerts 同一份门控，避免"没在采却还在列"；
//   2. 实例已下线的孤儿事件不展示 —— 与 RunMonitorMaintenance 的清理口径一致，
//      否则统计会被删掉的实例污染（实测 71 条事件里 69 条是孤儿）；
//   3. 算不出的字段留空串，绝不填模拟数。
// ============================================================================

const (
	alertCenterDefaultPageSize = 20
	alertCenterMaxPageSize     = 200
	// alertCenterTimeLayout 与页面时间控件一致的解析格式。
	alertCenterTimeLayout = "2006-01-02 15:04:05"
	// alertCenterInstanceStatLimit 实例聚合榜的返回上限：榜单是"看谁最吵"的
	// 态势视图，不是明细列表，超出部分由前端跳历史 Tab 查看。
	alertCenterInstanceStatLimit = 50
)

// AlertCenterItem 单条告警事件（含实例维度信息，前端无需二次查询）。
type AlertCenterItem struct {
	RowKey      string `json:"row_key"`
	Fingerprint string `json:"fingerprint"`
	RuleID      uint64 `json:"rule_id"`
	// AlertID 对外告警 ID（alm-xxxxxxxxxxxx）。存量事件由启动迁移回填，
	// 极老数据可能为空串——前端按空值显示 —，不要前端现算补假 ID。
	AlertID string `json:"alert_id"`

	// 实例维度
	InstanceID string `json:"instance_id"`
	Instance   string `json:"instance"`
	Hostname   string `json:"hostname"`
	Port       int    `json:"port"`
	DbType     string `json:"db_type"`
	Remark     string `json:"remark"`
	Business   string `json:"business"`
	GroupName  string `json:"group_name"`

	// 告警本体
	Name          string  `json:"name"`
	Level         string  `json:"level"`
	Severity      string  `json:"severity"`
	Status        string  `json:"status"`
	StatusText    string  `json:"status_text"`
	MetricKey     string  `json:"metric_key"`
	MetricName    string  `json:"metric_name"`
	Unit          string  `json:"unit"`
	Message       string  `json:"message"`
	Advice        string  `json:"advice"`
	Threshold     float64 `json:"threshold"`
	ObservedValue float64 `json:"observed_value"`
	PeakValue     float64 `json:"peak_value"`
	Occurrences   int     `json:"occurrences"`
	DurationSec   int64   `json:"duration_sec"`

	// 时间线
	FirstSeenAt string `json:"first_seen_at"`
	LastSeenAt  string `json:"last_seen_at"`
	TriggeredAt string `json:"triggered_at"`
	RecoveredAt string `json:"recovered_at"`

	// 处置
	Suppressed bool   `json:"suppressed"`
	Ignored    bool   `json:"ignored"`
	IgnoredBy  string `json:"ignored_by"`
	IgnoredAt  string `json:"ignored_at"`
	AckedBy    string `json:"acked_by"`
	AckedAt    string `json:"acked_at"`
}

// AlertCenterSummary 当前筛选条件下的概览（不受分页影响）。
type AlertCenterSummary struct {
	Total        int `json:"total"`
	Active       int `json:"active"`
	Acknowledged int `json:"acknowledged"`
	Recovered    int `json:"recovered"`
	Ignored      int `json:"ignored"`
	Suppressed   int `json:"suppressed"`
	// P0 必须单独计数：平台等级是 P0–P3（紧急/严重/一般/提示），把 P0 混进
	// P2 桶会让最紧急的一类告警在概览里"降级隐身"。
	P0        int `json:"p0"`
	P1        int `json:"p1"`
	P2        int `json:"p2"`
	P3        int `json:"p3"`
	Instances int `json:"instances"`
}

type AlertCenterData struct {
	Summary AlertCenterSummary `json:"summary"`
	Items   []AlertCenterItem  `json:"items"`
	// InstanceStats 当前筛选条件下的实例聚合榜（服务端一次遍历产出）。
	// 存在的意义：态势视图要回答"哪个实例最吵"，让前端拉全量自行聚合会把
	// 千级实例的列表整份搬给浏览器。口径与 Summary 完全一致（同一份过滤）。
	InstanceStats []AlertCenterInstanceStat `json:"instance_stats"`
	Page          int                       `json:"page"`
	PageSize      int                       `json:"page_size"`
}

// AlertCenterInstanceStat 单个实例在当前筛选条件下的告警聚合。
type AlertCenterInstanceStat struct {
	InstanceID string `json:"instance_id"`
	Instance   string `json:"instance"`
	Hostname   string `json:"hostname"`
	Port       int    `json:"port"`
	DbType     string `json:"db_type"`
	Remark     string `json:"remark"`
	Business   string `json:"business"`
	GroupName  string `json:"group_name"`

	Total  int `json:"total"`
	P0     int `json:"p0"`
	P1     int `json:"p1"`
	P2     int `json:"p2"`
	P3     int `json:"p3"`
	Active int `json:"active"`
	Acked  int `json:"acked"`
	// Ignored / Suppressed 保留在聚合里，便于前端做"是否全是已忽略"的降级显示。
	Ignored    int `json:"ignored"`
	Suppressed int `json:"suppressed"`
	// LatestAt 该实例最近一次命中时间（last_seen_at 回落到 triggered_at）。
	LatestAt string `json:"latest_at"`
}

type GetAlertCenterService struct {
	*forms.GetAlertCenterForm
	C *gin.Context
}

// Run 返回 (数据, 总数, 错误)。总数单独返回是为了让 view 走 PaginationSuccess。
func (s *GetAlertCenterService) Run() (*AlertCenterData, int64, error) {
	data := &AlertCenterData{
		Items:         make([]AlertCenterItem, 0),
		InstanceStats: make([]AlertCenterInstanceStat, 0),
		Page:          1,
		PageSize:      alertCenterDefaultPageSize,
	}
	if global.App.DB == nil {
		return data, 0, nil
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
		return nil, 0, err
	}
	if len(rows) == 0 {
		return data, 0, nil
	}

	metaByID := make(map[string]monitorInstanceRow, len(rows))
	excludeByInstance := make(map[string]func(string) bool, len(rows))
	for _, row := range rows {
		metaByID[row.InstanceID] = row
		excludeByInstance[row.InstanceID] = alertMetricExcludedByTemplate(row)
	}
	// 查询条件构造抽成独立函数：「告警列表」与「告警实例」下钻聚合共用
	// 同一套可见域 / 关键字匹配 / 过滤条件，避免两处逻辑漂移导致数字对不上。
	build := buildAlertCenterQuery(s.GetAlertCenterForm, rows)

	// total 初值是未剔除"模板外指标"的 Count（该门控是每实例一份集合，无法用
	// 单条 SQL 表达），仅用于空数据快速短路；下方门控过滤后会以过滤结果重写
	// total，保证总条数、概览统计与列表数据永远同口径。
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	data.Page, data.PageSize = normalizeAlertCenterPage(s.Page, s.PageSize)

	// 默认排序：状态优先 active → acknowledged（处理中）→ recovered（已恢复），
	// 保证"处理中"告警无论时间、等级如何都排在"已恢复"之前。
	// 同一状态内部：严重度 P0→P3 → 最近命中；last_seen_at 可能为空，回落到触发时间。
	// field() 必须列全 P0–P3：漏写的等级会命中 field() 的 0 值而被误排到最前。
	orderClause := "field(status, 'active', 'acknowledged', 'recovered') asc, " +
		"field(severity, 'P0', 'P1', 'P2', 'P3') asc, " +
		"COALESCE(last_seen_at, triggered_at) desc"

	// 告警历史 Tab 传 sort_by=triggered_at：纯时间倒序（后发生在前），
	// 历史是"流水账"视图，状态/级别优先会把几天前的未恢复事件顶到最新记录前面，不符合直觉。
	if s.SortBy == "triggered_at" {
		orderClause = "triggered_at desc, id desc"
	}

	// 一次性取全量匹配行，模板门控过滤后再做统计与分页。此前是
	// "SQL 分页 + 应用层剔除"两段式：total 用剔除前的 Count，而 Limit/Offset
	// 窗口内的行可能被门控整页剔除——页码显示有数据、表体却渲染为空
	// （下钻弹窗点"下一页"看不到数据的直接根因），页内条数也会无声缩水。
	// 改为过滤后统一口径：total = 过滤后总数，再内存切片分页，概览统计、
	// 总条数与页内数据永远同源。
	// 量级：原实现每次请求本就做一次全量 stats 扫描，此处合并为一次明细
	// 全量扫描，开销同量级。
	events := make([]monitorModels.InsightMonitorAlertEvents, 0, total)
	if total > 0 {
		if err := build().
			Order(orderClause).
			Find(&events).Error; err != nil {
			return nil, 0, err
		}
	}
	kept := make([]monitorModels.InsightMonitorAlertEvents, 0, len(events))
	for _, event := range events {
		// 模板外指标的历史事件同样过滤，口径与采集侧、大屏侧完全一致。
		if exclude := excludeByInstance[event.InstanceID]; exclude != nil && exclude(event.MetricKey) {
			continue
		}
		kept = append(kept, event)
	}
	total = int64(len(kept))

	instances := make(map[string]struct{}, len(kept))
	statByInstance := make(map[string]*AlertCenterInstanceStat)
	latestByInstance := make(map[string]time.Time)
	for _, item := range kept {
		switch item.Status {
		case "recovered":
			data.Summary.Recovered++
		case "acknowledged":
			data.Summary.Acknowledged++
		default:
			data.Summary.Active++
		}
		switch strings.ToUpper(item.Severity) {
		case "P0":
			data.Summary.P0++
		case "P1":
			data.Summary.P1++
		case "P3":
			data.Summary.P3++
		default:
			data.Summary.P2++
		}
		if item.Ignored {
			data.Summary.Ignored++
		}
		if item.Suppressed {
			data.Summary.Suppressed++
		}
		instances[item.InstanceID] = struct{}{}

		// 实例聚合：同一份遍历、同一套过滤，不做第二遍查询。
		stat := statByInstance[item.InstanceID]
		if stat == nil {
			stat = &AlertCenterInstanceStat{InstanceID: item.InstanceID, Instance: item.InstanceID}
			statByInstance[item.InstanceID] = stat
		}
		stat.Total++
		switch strings.ToUpper(item.Severity) {
		case "P0":
			stat.P0++
		case "P1":
			stat.P1++
		case "P3":
			stat.P3++
		default:
			stat.P2++
		}
		if item.Status == "acknowledged" {
			stat.Acked++
		} else if item.Status != "recovered" {
			stat.Active++
		}
		if item.Ignored {
			stat.Ignored++
		}
		if item.Suppressed {
			stat.Suppressed++
		}
		// 最近命中：last_seen_at 可能为空，回落到 triggered_at。
		seenAt := time.Time(item.TriggeredAt)
		if item.LastSeenAt != nil {
			seenAt = time.Time(*item.LastSeenAt)
		}
		if prev, ok := latestByInstance[item.InstanceID]; !ok || seenAt.After(prev) {
			latestByInstance[item.InstanceID] = seenAt
		}
	}
	data.Summary.Total = int(total)
	data.Summary.Instances = len(instances)

	// 补实例元信息 + 排序：有 P1 的优先 → 未恢复条数 → 最近命中。
	for instanceID, stat := range statByInstance {
		if meta, ok := metaByID[instanceID]; ok {
			stat.Hostname = meta.Hostname
			stat.Port = meta.Port
			stat.DbType = meta.DbType
			stat.Remark = meta.Remark
			stat.Business = meta.Business
			stat.GroupName = meta.GroupName
			if meta.Hostname != "" {
				stat.Instance = fmt.Sprintf("%s:%d", meta.Hostname, meta.Port)
			}
		}
		if latest, ok := latestByInstance[instanceID]; ok && !latest.IsZero() {
			stat.LatestAt = latest.Format(time.DateTime)
		}
		data.InstanceStats = append(data.InstanceStats, *stat)
	}
	data.InstanceStats = sortInstanceStats(data.InstanceStats)

	if total == 0 {
		return data, 0, nil
	}

	// 内存分页：只有过滤后的总条数才能用来计算页窗口。
	start := (data.Page - 1) * data.PageSize
	if start >= len(kept) {
		kept = nil
	} else {
		end := start + data.PageSize
		if end > len(kept) {
			end = len(kept)
		}
		kept = kept[start:end]
	}
	for _, event := range kept {
		data.Items = append(data.Items, buildAlertCenterItem(event, metaByID[event.InstanceID]))
	}
	return data, total, nil
}

func buildAlertCenterItem(event monitorModels.InsightMonitorAlertEvents, row monitorInstanceRow) AlertCenterItem {
	name, _, unit, _ := metricDisplayName(canonicalMetricKey(event.MetricKey))
	item := AlertCenterItem{
		Fingerprint:   event.Fingerprint,
		RuleID:        event.RuleID,
		AlertID:       event.AlertID,
		InstanceID:    event.InstanceID,
		Instance:      event.InstanceID,
		Name:          event.Name,
		Level:         event.Level,
		Severity:      defaultString(event.Severity, severityFromLevel(event.Level)),
		Status:        event.Status,
		StatusText:    alertStatusText(event.Status),
		MetricKey:     event.MetricKey,
		MetricName:    name,
		Unit:          unit,
		Message:       event.Message,
		Advice:        defaultString(event.Advice, metricAdvice(canonicalMetricKey(event.MetricKey))),
		Threshold:     event.Threshold,
		ObservedValue: event.ObservedValue,
		PeakValue:     event.PeakValue,
		Occurrences:   event.Occurrences,
		DurationSec:   event.DurationSec,
		TriggeredAt:   time.Time(event.TriggeredAt).Format(time.DateTime),
		Suppressed:    event.Suppressed,
		Ignored:       event.Ignored,
		IgnoredBy:     event.IgnoredBy,
		AckedBy:       event.AckedBy,
		RowKey:        defaultString(event.Fingerprint, fmt.Sprintf("%s|%s", event.InstanceID, event.MetricKey)),
	}
	if row.InstanceID != "" {
		item.Hostname = row.Hostname
		item.Port = row.Port
		item.Instance = fmt.Sprintf("%s:%d", row.Hostname, row.Port)
		item.DbType = row.DbType
		item.Remark = row.Remark
		item.Business = row.Business
		item.GroupName = row.GroupName
	}
	if event.FirstSeenAt != nil {
		item.FirstSeenAt = time.Time(*event.FirstSeenAt).Format(time.DateTime)
	}
	if event.LastSeenAt != nil {
		item.LastSeenAt = time.Time(*event.LastSeenAt).Format(time.DateTime)
	}
	if event.RecoveredAt != nil {
		item.RecoveredAt = time.Time(*event.RecoveredAt).Format(time.DateTime)
	}
	if event.IgnoredAt != nil {
		item.IgnoredAt = time.Time(*event.IgnoredAt).Format(time.DateTime)
	}
	if event.AckedAt != nil {
		item.AckedAt = time.Time(*event.AckedAt).Format(time.DateTime)
	}
	return item
}

// parseAlertCenterTime 解析页面传入的时间；空串与非法值一律视为"不过滤"
// （宁可多显示，也不要因为格式问题把整个列表查空）。
func parseAlertCenterTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation(alertCenterTimeLayout, trimmed, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func normalizeAlertCenterPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = alertCenterDefaultPageSize
	}
	if pageSize > alertCenterMaxPageSize {
		pageSize = alertCenterMaxPageSize
	}
	return page, pageSize
}

// sortInstanceStats 就地为实例聚合榜排序并截断。抽成纯函数是为了可测：
// 排序规则一旦被写错（比如把"有 P1 优先"写成"P1 条数最多优先"），
// 排序错误本身不会报错，只会让最危险的实例沉到榜单底部——静默且致命。
//
// 规则（自上而下）：
//  1. 有 P0/P1 的实例一律排在其余实例之前 —— 值班必须先看到高危；
//  2. 未恢复条数多的靠前；
//  3. P0+P1 条数多的靠前；
//  4. 总条数多的靠前；
//  5. 最近命中时间新的靠前（字典序即时间序，"2006-01-02 15:04:05" 定长）。
//
// 返回值必须接收：切片长度是值语义，函数内截断不会反映到调用方的切片头上。
func sortInstanceStats(stats []AlertCenterInstanceStat) []AlertCenterInstanceStat {
	sort.SliceStable(stats, func(i, j int) bool {
		a, b := stats[i], stats[j]
		aHigh, bHigh := a.P0+a.P1 > 0, b.P0+b.P1 > 0
		if aHigh != bHigh {
			return aHigh
		}
		if a.Active != b.Active {
			return a.Active > b.Active
		}
		if a.P0+a.P1 != b.P0+b.P1 {
			return a.P0+a.P1 > b.P0+b.P1
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.LatestAt > b.LatestAt
	})
	if len(stats) > alertCenterInstanceStatLimit {
		stats = stats[:alertCenterInstanceStatLimit]
	}
	return stats
}

// buildAlertCenterQuery 构造告警事件检索的查询条件（实例可见域 + 关键字匹配 +
// 各过滤参数）。告警列表与「告警实例」下钻聚合共用，保证两处口径永远一致。
func buildAlertCenterQuery(s *forms.GetAlertCenterForm, rows []monitorInstanceRow) func() *gorm.DB {
	instanceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		instanceIDs = append(instanceIDs, row.InstanceID)
	}

	// Keyword 模糊匹配：告警ID、实例名称（remark）、IP:port。
	// 先按元数据把匹配的实例 ID 筛出来，再在 SQL 里用 OR 带上 alert_id LIKE。
	keyword := strings.TrimSpace(s.Keyword)
	var keywordMatchedInstanceIDs []string
	if keyword != "" {
		k := strings.ToLower(keyword)
		for _, row := range rows {
			texts := []string{
				strings.ToLower(row.InstanceID),
				strings.ToLower(row.Remark),
				strings.ToLower(row.Hostname),
			}
			if row.Port > 0 {
				texts = append(texts, fmt.Sprintf("%s:%d", strings.ToLower(row.Hostname), row.Port))
			}
			for _, t := range texts {
				if strings.Contains(t, k) {
					keywordMatchedInstanceIDs = append(keywordMatchedInstanceIDs, row.InstanceID)
					break
				}
			}
		}
	}

	return func() *gorm.DB {
		tx := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{})
		if keyword != "" {
			like := "%" + strings.ToLower(keyword) + "%"
			tx = tx.Where("(alert_id LIKE ? OR instance_id IN ?)", like, keywordMatchedInstanceIDs)
		} else {
			tx = tx.Where("instance_id IN ?", instanceIDs)
		}
		// 「未认领」优先且独占 status：它是比 status=active 更宽的语义（含观察期
		// pending），判定后不再叠加 Status —— 二者 AND 会把观察期事件排除，
		// 正是"未认领卡片 3 条、点开 0 条"的成因。
		if s.Unclaimed {
			tx = tx.Where("status NOT IN ?", []string{"acknowledged", "recovered"})
		} else if v := strings.TrimSpace(s.Status); v != "" {
			if strings.EqualFold(v, "unresolved") {
				// 状态筛选的聚合项「未恢复」：涵盖所有进行中的生命周期状态
				// （active + pending + acknowledged），即"还没恢复"。
				// 刻意不排除已忽略：告警历史是审计视角，需要能查到
				// "未恢复但已被人工忽略"的事实；是否只看忽略由独立开关
				// only_ignored 决定，状态维度与忽略维度不耦合。
				// 注意与 Unresolved=true（概览「当前告警」，排除已忽略）口径不同，
				// 二者不可互换，否则概览卡数字与筛选条数会不一致。
				tx = tx.Where("status <> ?", "recovered")
			} else {
				tx = tx.Where("status = ?", v)
			}
		}
		// 「当前告警」= 未恢复（active + pending + acknowledged），判定 status <> recovered。
		// 注意：这是 AND 叠加在 Status 之后的，并非"覆盖"—— 前端若同时传了
		// status=active，结果会被收窄成"已转正未认领"，观察期事件静默消失。
		// 需要这种收敛语义请用 Unclaimed，别依赖 status 与 unresolved 的组合。
		// 同时排除已人工忽略的事件：忽略的语义是"不再提醒"，把它算进"需要立即
		// 处理"的未恢复数会误导值班。大屏 loadScreenAlertCounts 用的是
		// status <> 'recovered' AND ignored = false，两处必须同口径，
		// 否则同一个系统里"大屏 11 / 告警中心 13"这类差额无法解释。
		// only_ignored 显式要求只看忽略事件时不做排除，避免两个条件互斥成空集。
		if s.Unresolved {
			tx = tx.Where("status <> ?", "recovered")
			if !s.OnlyIgnored {
				tx = tx.Where("ignored = ?", false)
			}
		}
		// 「今日已恢复」下钻：按恢复时间过滤，与概览 today_recovered 完全同口径
		// （触发可能在多天前，按触发时间的 range_hours 会漏）。隐含 recovered，覆盖 Status。
		if s.RecoveredToday {
			now := time.Now()
			todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			tx = tx.Where("status = ?", "recovered")
			tx = tx.Where("recovered_at >= ?", todayStart)
		}
		// 告警 ID 精确查询：ID 生成侧已统一小写，这里再兜一次底（用户可能手输大写）。
		if v := strings.ToLower(strings.TrimSpace(s.AlertID)); v != "" {
			tx = tx.Where("alert_id = ?", v)
		}
		if v := strings.ToUpper(strings.TrimSpace(s.Severity)); v != "" {
			// 支持逗号分隔多级别（概览「高危未处理」下钻 = P0,P1 一次查齐）
			tx = tx.Where("severity IN ?", strings.Split(v, ","))
		}
		if v := strings.TrimSpace(s.MetricKey); v != "" {
			tx = tx.Where("metric_key = ?", canonicalMetricKey(v))
		}
		if s.OnlyIgnored {
			tx = tx.Where("ignored = ?", true)
		}
		// 大屏圆饼下钻的排除口径（见 forms.ExcludeIgnored 注释）
		if s.ExcludeIgnored {
			tx = tx.Where("ignored = ?", false)
		}
	// 时间窗口落在哪个字段上由查询语义决定：
	//   - FirstSeen=true：趋势图下钻，口径是「新增告警」，用 first_seen_at；
	//   - status=recovered / RecoveredToday：已恢复列表，按 recovered_at；
	//   - 其余：默认按 triggered_at。
	// 三类字段互不兼容，混用会少/空数据（如圆饼图已恢复 1 条但列表 0 条）。
	statusIsRecovered := strings.TrimSpace(s.Status) == "recovered" || s.RecoveredToday
	var timeField string
	switch {
	case s.FirstSeen:
		timeField = "first_seen_at"
	case statusIsRecovered:
		timeField = "recovered_at"
	default:
		timeField = "triggered_at"
	}

	if s.RangeHours > 0 {
		tx = tx.Where(fmt.Sprintf("%s >= ?", timeField), time.Now().Add(-time.Duration(s.RangeHours)*time.Hour))
	}
	if start, ok := parseAlertCenterTime(s.StartTime); ok {
		tx = tx.Where(fmt.Sprintf("%s >= ?", timeField), start)
	}
	if end, ok := parseAlertCenterTime(s.EndTime); ok {
		tx = tx.Where(fmt.Sprintf("%s <= ?", timeField), end)
	}
	return tx
	}
}

// isUnresolvedAlertEvent 「未恢复告警」的统一语义锚点：既未恢复、也未被人工忽略。
//
// 「忽略」的产品含义是"这条告警我看过了，别再提醒"，因此它不应继续计入
// 「未恢复告警 / 需要立即处理」这类待办口径。SQL 层（buildAlertCenterQuery 的
// Unresolved 分支）与内存聚合层（实例下钻 unresolved）都以此为准；监控大屏的
// loadScreenAlertCounts 同样用 status <> 'recovered' AND ignored = false（含观察期
// pending），loadActiveEventMap 也已对齐 —— 任何一侧单独改动都会让
// "大屏 N 条 / 告警中心 M 条 / 明细页 K 条"重新对不上。
//
// 判定刻意与 SQL 的 `status <> 'recovered'` 保持同构（不做空值特判、大小写不敏感
// 同 utf8mb4 默认排序规则），避免内存层与 SQL 层在脏数据上悄悄分叉。
func isUnresolvedAlertEvent(status string, ignored bool) bool {
	if ignored {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(status), "recovered")
}

// ============================================================================
// 「告警实例总数」下钻：触发告警的 DB 实例列表（告警概览计数卡专用）。
// 与 GetAlertCenterService 共用同一套可见域 / 过滤 / 模板门控，保证实例数
// 与概览卡片数字（summary.instances）同口径；实例聚合榜（instance_stats）
// 有 50 条截断且按"吵闹度"排序，不适合直接当完整列表用，这里单独返回全量。
// ============================================================================

// AlertCenterInstanceItem 弹窗单行：实例 + 未恢复告警数 + 最近触发时间。
type AlertCenterInstanceItem struct {
	InstanceID string `json:"instance_id"`
	// Instance 展示名：remark 优先，回落 host:port（与设计稿"实例名称"列一致）。
	Instance string `json:"instance"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	DbType   string `json:"db_type"`
	// DbVersion 画像采集的版本串（如 8.0.43 / 16.4），可能为空——版本展示
	// 由前端拼接「MySQL 8.0」这类文案，空版本时只显示 db_type。
	DbVersion string `json:"db_version"`
	// Unresolved 未恢复告警数（active + acknowledged）。
	Unresolved int `json:"unresolved"`
	// Total 当前过滤条件下的全部事件数（含已恢复）。
	Total    int    `json:"total"`
	LatestAt string `json:"latest_at"`
}

type GetAlertCenterInstancesService struct {
	*forms.GetAlertCenterForm
	C *gin.Context
}

// normalizeAlertInstanceScope 实例下钻的集合口径：
// alert（默认）/ all / healthy / risk / fault / uncollected。
// 非法值一律回落 alert，宁可少显示也不因参数异常报错。
func normalizeAlertInstanceScope(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "all", "healthy", "risk", "fault", "uncollected":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "alert"
	}
}

// Run 返回（实例列表, 错误）。列表不做分页：行数 = 实例数，
// 量级远小于事件数，前端表格内分页即可。
//
// scope 口径：
//   - alert（默认）：有告警事件的实例（沿用 unresolved 等状态过滤）；
//   - all / healthy / risk / fault / uncollected：集合由大屏四分类口径圈定
//     （classifyScreenCard，见 monitor_health_state.go；
//     监控大屏「监控实例 / 健康实例 / 风险实例 / 故障实例」卡片与环形图图例下钻），
//     此时状态类过滤不参与圈定，告警聚合成"参考列"（无告警实例未恢复数为 0）。
func (s *GetAlertCenterInstancesService) Run() ([]AlertCenterInstanceItem, error) {
	items := make([]AlertCenterInstanceItem, 0)
	if global.App.DB == nil {
		return items, nil
	}
	scope := normalizeAlertInstanceScope(s.Scope)
	byHealthClass := scope != "alert"

	filter := monitorListFilter{
		Search:      s.Search,
		Environment: s.Environment,
		DbType:      s.DbType,
		Business:    s.Business,
		GroupName:   s.GroupName,
		InstanceID:  s.InstanceID,
	}
	rows, err := listMonitorInstances(filter)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return items, nil
	}

	// 实例四分类：与大屏卡片完全同一处口径（classifyScreenCard → 健康分驱动的
	// evaluateInstanceState）。此前残留的 EvaluateInstanceHealth 是已下线的旧分级
	// 引擎，判定结果与新口径不一致（如单条 P0 在新口径是故障、旧引擎可能判风险），
	// 会导致「风险实例」卡片数字与下钻弹窗行数对不上。
	// cards 与 rows 同序；这里只需要 instance_id → screen_class 映射。
	classByInstance := make(map[string]string, len(rows))
	if byHealthClass {
		requestCtx := context.Background()
		if s.C != nil && s.C.Request != nil {
			requestCtx = s.C.Request.Context()
		}
		_, cards, _, _, err := loadMonitorCards(requestCtx, filter)
		if err != nil {
			return nil, err
		}
		for i := range cards {
			classByInstance[cards[i].InstanceID] = classifyScreenCard(cards[i])
		}
	}
	classAllowed := func(instanceID string) bool {
		if scope == "all" {
			return true
		}
		return classByInstance[instanceID] == scope
	}

	excludeByInstance := make(map[string]func(string) bool, len(rows))
	for _, row := range rows {
		excludeByInstance[row.InstanceID] = alertMetricExcludedByTemplate(row)
	}
	// 状态类过滤只在 alert 口径下生效：scope 圈定集合后，聚合需要全事件
	// 才能算出每个实例的"未恢复告警总数"。
	qf := *s.GetAlertCenterForm
	if byHealthClass {
		qf.Unresolved = false
		qf.Status = ""
		qf.RecoveredToday = false
	}
	build := buildAlertCenterQuery(&qf, rows)

	// 全量扫描 + 模板门控，与列表/统计完全同一段口径。
	events := make([]monitorModels.InsightMonitorAlertEvents, 0, 64)
	if err := build().Order("COALESCE(last_seen_at, triggered_at) desc").Find(&events).Error; err != nil {
		return nil, err
	}

	type instAgg struct {
		unresolved int
		total      int
		latest     time.Time
	}
	aggByInstance := make(map[string]*instAgg)
	for _, event := range events {
		// 模板外指标的历史事件同样过滤，口径与采集侧、列表侧完全一致。
		if exclude := excludeByInstance[event.InstanceID]; exclude != nil && exclude(event.MetricKey) {
			continue
		}
		agg := aggByInstance[event.InstanceID]
		if agg == nil {
			agg = &instAgg{}
			aggByInstance[event.InstanceID] = agg
		}
		agg.total++
		// 未恢复口径与大屏、概览卡完全一致：排除已恢复，也排除已人工忽略。
		// 该分支在 byHealthClass（scope=all/healthy/...）下会把 Unresolved 关掉、
		// 拿全量事件在内存里算，所以必须在这里同样排掉 ignored，不能只依赖 SQL 条件。
		if isUnresolvedAlertEvent(event.Status, event.Ignored) {
			agg.unresolved++
		}
		seenAt := time.Time(event.TriggeredAt)
		if event.LastSeenAt != nil {
			seenAt = time.Time(*event.LastSeenAt)
		}
		if agg.latest.IsZero() || seenAt.After(agg.latest) {
			agg.latest = seenAt
		}
	}

	// 补实例元信息（展示名 / db_type）与画像版本（insight_instances.db_version，
	// 由采集侧回写，可能为空）。版本单独一把 IN 查询，不拖累事件扫描。
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.InstanceID)
	}
	versionByInstance := make(map[string]string, len(ids))
	type versionRow struct {
		InstanceID string
		DbVersion  string
	}
	var versionRows []versionRow
	if err := global.App.DB.Table("insight_instances").
		Select("instance_id, db_version").
		Where("instance_id IN ?", ids).
		Scan(&versionRows).Error; err == nil {
		for _, row := range versionRows {
			versionByInstance[row.InstanceID] = row.DbVersion
		}
	}
	// 版本查询失败不阻断主流程：列表照常返回，只是"实例类型"列少版本号。

	for _, row := range rows {
		// alert 口径：无事件的实例不出现（与卡片数字同口径）；
		// 健康分级口径：集合由 screen_class 决定，无告警实例未恢复数显示 0。
		agg, ok := aggByInstance[row.InstanceID]
		if !ok {
			if scope == "alert" || !classAllowed(row.InstanceID) {
				continue
			}
			agg = &instAgg{}
		} else if byHealthClass && !classAllowed(row.InstanceID) {
			continue
		}
		item := AlertCenterInstanceItem{
			InstanceID: row.InstanceID,
			Total:      agg.total,
			Unresolved: agg.unresolved,
		}
		item.Hostname = row.Hostname
		item.Port = row.Port
		item.DbType = row.DbType
		item.DbVersion = versionByInstance[row.InstanceID]
		// 展示名：业务备注优先，回落 host:port。
		if row.Remark != "" {
			item.Instance = row.Remark
		} else if row.Hostname != "" {
			item.Instance = fmt.Sprintf("%s:%d", row.Hostname, row.Port)
		} else {
			item.Instance = row.InstanceID
		}
		if !agg.latest.IsZero() {
			item.LatestAt = agg.latest.Format(time.DateTime)
		}
		items = append(items, item)
	}

	// 与设计稿一致：最近触发时间新的靠前；同时间未恢复多的靠前；最后按 ID 兜底稳定序。
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LatestAt != items[j].LatestAt {
			return items[i].LatestAt > items[j].LatestAt
		}
		if items[i].Unresolved != items[j].Unresolved {
			return items[i].Unresolved > items[j].Unresolved
		}
		return items[i].InstanceID < items[j].InstanceID
	})
	return items, nil
}
