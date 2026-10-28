package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
	"gorm.io/datatypes"
)

type slowSQLInstanceSummaryRow struct {
	InstanceID string `json:"instance_id"`
	Remark     string `json:"remark"`
	Hostname   string `json:"hostname"`
	Port       int    `json:"port"`
	DBType     string `json:"db_type"`
	// DbVersion 实例画像采集的版本串（如 8.0.43），可能为空——前端拼成「MySQL 8.0.43」
	DbVersion        string  `json:"db_version"`
	EnvironmentName  string  `json:"environment_name"`
	SlowSQLCount     int64   `json:"slow_sql_count"`
	AvgDurationMS    float64 `json:"avg_duration_ms"`
	FullScanCount    int64   `json:"full_scan_count"`
	SystemScore      int     `json:"system_score"`
	SystemScoreLevel string  `json:"system_score_level"`
	SystemScoreText  string  `json:"system_score_text"`
	Target           string  `json:"target"`
	Supported        bool    `json:"supported"`

	// ---- 采集状态与结构化失败原因（三引擎统一口径）----
	//
	// 关键约束：实例一旦进入这个列表，就不会因为「采集失败」被剔除。
	// 失败信息只影响状态列的展示，不影响是否出现——否则用户看到的是
	// "少了几台"，既看不出原因也没有修复入口。
	CollectStatus string `json:"collect_status"`
	ErrorCode     string `json:"error_code"`
	ErrorMessage  string `json:"error_message"`
	ErrorCategory string `json:"error_category"`
	ErrorTone     string `json:"error_tone"`
	ErrorDetail   string `json:"error_detail"`
	// FailureCode 引擎原始失败码（如 PG_LOGGING_COLLECTOR_OFF），保留用于
	// 反查前提条件清单与日志排查；归一化不丢原始信息。
	FailureCode string `json:"failure_code"`
	// CapabilityState 采集能力探测状态（ENABLED_FILE / DISABLED / ...）
	CapabilityState string `json:"capability_state"`
	// Source 数据来源：FILE（慢日志文件增量）或 V$SQL（Oracle 定时采样）
	Source string `json:"source"`
	// LastProbeAt 最近一次探测时间，用于判断失败信息是否已过期
	LastProbeAt string `json:"last_probe_at"`
	// LastCollectMessage 最近一次采集消息（新增 N 条 / 跳过 M 条协议阶段记录）
	LastCollectMessage string `json:"last_collect_message"`
}

type GetSlowSQLInstanceSummaryService struct {
	Form     *forms.GetSlowSQLInstanceSummaryForm
	C        *gin.Context
	Username string
}

func (s *GetSlowSQLInstanceSummaryService) Run() (map[string]any, error) {
	startAt, endAt := resolveInstanceSummaryRange(s.Form.StartAt, s.Form.EndAt)

	type queryRow struct {
		InstanceID      string
		Remark          string
		Hostname        string
		Port            int
		DBType          string
		DbVersion       string
		EnvironmentName string
		SlowSQLCount    int64
		AvgDurationMS   float64
		FullScanCount   int64
		ExtraConfig     datatypes.JSON
		// 采集器侧：enabled 决定是否平台停用，capability_state / failure_code /
		// probe_error / last_error 用于归一出结构化失败原因
		Enabled         bool
		CapabilityState string
		FailureCode     string
		ProbeError      string
		LastError       string
		CollectStatus   string
		Source          string
		LastProbeAt     string
		// 最近一次采集消息：包含「新增 N 条 / 跳过 M 条协议阶段记录」，
		// 用于解释「日志里 duration 涨了几百条，页面只多几条」的统计口径差异
		LastCollectMessage string
	}

	var rows []queryRow
	// 采集器一律 left join，且**不再按 capability_state 过滤**：
	//
	// 之前的口径是「只返回 ENABLED_FILE / PROBE_FAILED / FILE_MISSING」，
	// 结果 DISABLED、ENABLED_TABLE、UNSUPPORTED、未探测的实例直接从列表消失，
	// 用户只知道"少了几台"却看不出原因。现在三类引擎（MySQL / PostgreSQL /
	// Oracle）的实例全部进列表，采集失败的照常展示，只是状态列标出原因与
	// 「采集失败」标识——可见但不必惊慌，比静默丢弃有用得多。
	tx := global.App.DB.Table("insight_instances i").
		Select(`
                        i.instance_id,
                        i.remark,
                        i.hostname,
                        i.port,
                        i.db_type,
                        ifnull(i.db_version, '') as db_version,
                        ifnull(env.name, '') as environment_name,
                        count(r.id) as slow_sql_count,
                        ifnull(avg(r.query_time_ms), 0) as avg_duration_ms,
                        ifnull(sum(case
                                when r.rows_examined >= greatest(ifnull(r.rows_sent, 0) * 20, 10000) then 1
                                else 0
                        end), 0) as full_scan_count,
                        i.extra_config,
                        ifnull(sc.enabled, 1) as enabled,
                        ifnull(sc.capability_state, '') as capability_state,
                        ifnull(sc.failure_code, '') as failure_code,
                        ifnull(sc.probe_error, '') as probe_error,
                        ifnull(sc.last_error, '') as last_error,
                        ifnull(sc.collect_status, '') as collect_status,
                        ifnull(sc.source, '') as source,
                        ifnull(sc.last_collect_message, '') as last_collect_message,
                        ifnull(DATE_FORMAT(sc.last_probe_at, '%Y-%m-%d %H:%i:%s'), '') as last_probe_at
                `).
		Joins("left join insight_instance_environments env on env.id = i.environment").
		Joins("left join insight_performance_slowlog_collectors sc on sc.instance_id = i.instance_id").
		Joins("left join insight_performance_slowlog_entries r on r.instance_id = i.instance_id and r.event_time >= ? and r.event_time <= ?", startAt, endAt).
		Group("i.instance_id, i.remark, i.hostname, i.port, i.db_type, i.db_version, env.name, i.extra_config, sc.enabled, sc.capability_state, sc.failure_code, sc.probe_error, sc.last_error, sc.collect_status, sc.source, sc.last_collect_message, sc.last_probe_at").
		Where("i.db_type in ?", supportedDBTypes).
		Order("slow_sql_count desc, avg_duration_ms desc, i.remark asc, i.hostname asc")

	if keyword := strings.TrimSpace(s.Form.Search); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where("i.instance_id like ? or i.remark like ? or i.hostname like ? or ifnull(env.name,'') like ?", like, like, like, like)
	}

	// 可见性只由功能权限决定（路由层 performance.view），不再叠加数据授权过滤：
	// 实例清单、明细、统计、详情四处口径一致，避免出现「列表里有、点进去说不存在」。

	if err := tx.Scan(&rows).Error; err != nil {
		return nil, err
	}

	list := make([]slowSQLInstanceSummaryRow, 0, len(rows))
	for _, item := range rows {
		score, level, text := calcSlowSQLSystemScore(item.SlowSQLCount, item.AvgDurationMS, item.FullScanCount)
		// 归一出统一错误口径：优先 probe_error，其次 last_error（最近一次采集的报错）。
		// 两者都带上下文，取第一个非空的即可，不必拼接成长句。
		rawError := strings.TrimSpace(item.ProbeError)
		if rawError == "" {
			rawError = strings.TrimSpace(item.LastError)
		}
		errView := NormalizeSlowLogError(item.CapabilityState, item.FailureCode, rawError, item.Enabled)

		row := slowSQLInstanceSummaryRow{
			InstanceID:         item.InstanceID,
			Remark:             item.Remark,
			Hostname:           item.Hostname,
			Port:               item.Port,
			DBType:             item.DBType,
			DbVersion:          item.DbVersion,
			EnvironmentName:    item.EnvironmentName,
			SlowSQLCount:       item.SlowSQLCount,
			AvgDurationMS:      item.AvgDurationMS,
			FullScanCount:      item.FullScanCount,
			SystemScore:        score,
			SystemScoreLevel:   level,
			SystemScoreText:    text,
			Target:             strings.TrimSpace(item.Hostname),
			Supported:          slowLogEngineSupported(commonModels.EnumType(item.DBType), []byte(item.ExtraConfig)),
			CollectStatus:      errView.Status,
			ErrorCode:          errView.Code,
			ErrorMessage:       errView.Message,
			ErrorCategory:      errView.Category,
			ErrorTone:          errView.Tone,
			ErrorDetail:        errView.Detail,
			FailureCode:        strings.TrimSpace(item.FailureCode),
			CapabilityState:    strings.TrimSpace(item.CapabilityState),
			Source:             strings.TrimSpace(item.Source),
			LastProbeAt:        strings.TrimSpace(item.LastProbeAt),
			LastCollectMessage: strings.TrimSpace(item.LastCollectMessage),
		}
		if item.Port > 0 {
			row.Target = strings.TrimSpace(item.Hostname) + ":" + strconv.Itoa(item.Port)
		}
		if !statusFilterMatched(s.Form.StatusFilter, row.CollectStatus) {
			continue
		}
		if scoreLevelMatched(s.Form.ScoreLevel, row.SystemScoreLevel) {
			list = append(list, row)
		}
	}

	// 排序只决定"谁在前"，绝不决定"谁在列表里"：
	// 正常采集的排前面，采集未就绪、采集失败依次靠后，组内保持 SQL 的计数倒序。
	sort.SliceStable(list, func(i, j int) bool {
		return slowLogStatusRank(list[i].CollectStatus) < slowLogStatusRank(list[j].CollectStatus)
	})

	// 统计各状态的实例数，供页面提示「还有哪些实例不具备采集条件」
	slowLogCounts, _ := splitSlowLogAttentionBySearch(s.Form.Search)

	return map[string]any{
		"instances": list,
		"status_summary": map[string]any{
			"ok":        countByCollectStatus(list, collectStatusOK),
			"failed":    countByCollectStatus(list, collectStatusFailed),
			"not_ready": countByCollectStatus(list, collectStatusNotReady),
			"total":     len(list),
		},
		// excluded_summary / excluded_hint 保留旧字段名，避免前端旧版本取不到值：
		// 现在列表不再排除任何实例，它们退化为"不具备采集条件的实例有多少"的提示。
		"excluded_summary": map[string]any{
			"table_only":   slowLogCounts[capabilityEnabledTable],
			"disabled":     slowLogCounts[capabilityDisabled],
			"probe_failed": slowLogCounts[capabilityProbeFailed],
			"file_missing": slowLogCounts[capabilityFileMissing],
			"unsupported":  slowLogCounts[capabilityUnsupported],
			"total": slowLogCounts[capabilityEnabledTable] + slowLogCounts[capabilityDisabled] +
				slowLogCounts[capabilityProbeFailed] + slowLogCounts[capabilityFileMissing] +
				slowLogCounts[capabilityUnsupported],
		},
		"excluded_hint": buildExcludedHint(slowLogCounts),
	}, nil
}

func countByCollectStatus(list []slowSQLInstanceSummaryRow, status string) int {
	total := 0
	for _, item := range list {
		if item.CollectStatus == status {
			total++
		}
	}
	return total
}

// statusFilterMatched 状态筛选：all/空 = 全部；ok = 采集正常；
// failed = 采集失败；not_ready = 不具备采集条件。
// 筛掉的是"用户当前不想看的"，不是"平台认为不该出现的"——两者性质不同。
func statusFilterMatched(filter string, status string) bool {
	value := strings.TrimSpace(strings.ToLower(filter))
	if value == "" || value == "all" {
		return true
	}
	return value == strings.TrimSpace(strings.ToLower(status))
}

// splitSlowLogAttentionBySearch 复用实例查询，把可采集/需提醒的实例分桶计数。
func splitSlowLogAttentionBySearch(search string) (map[string]int, []instanceOptionRow) {
	instances, err := queryPerformanceInstances(search)
	if err != nil {
		return map[string]int{}, nil
	}
	return splitSlowLogAttention(instances)
}

// buildExcludedHint 生成给用户看的一句话说明。
func buildExcludedHint(counts map[string]int) string {
	parts := make([]string, 0, 3)
	if counts[capabilityDisabled] > 0 {
		parts = append(parts, fmt.Sprintf("%d 个未开启慢日志", counts[capabilityDisabled]))
	}
	if counts[capabilityEnabledTable] > 0 {
		parts = append(parts, fmt.Sprintf("%d 个仅以表格方式记录", counts[capabilityEnabledTable]))
	}
	if counts[capabilityProbeFailed] > 0 {
		parts = append(parts, fmt.Sprintf("%d 个无法探测慢日志配置", counts[capabilityProbeFailed]))
	}
	if counts[capabilityFileMissing] > 0 {
		parts = append(parts, fmt.Sprintf("%d 个慢日志文件不可读", counts[capabilityFileMissing]))
	}
	if counts[capabilityUnsupported] > 0 {
		parts = append(parts, fmt.Sprintf("%d 个引擎当前不具备采集条件", counts[capabilityUnsupported]))
	}
	if len(parts) == 0 {
		return ""
	}
	// 注意措辞：这些实例**在列表里仍然可见**，这里只是说明它们为什么没有慢SQL数据，
	// 不再是"被隐藏"的意思。
	return "其中 " + strings.Join(parts, "、") + "，慢SQL数量为 0，已在列表中标注原因。"
}

func resolveInstanceSummaryRange(startAt, endAt string) (time.Time, time.Time) {
	now := time.Now()
	end := now
	start := now.Add(-24 * time.Hour)
	if parsed, ok := parseAnalysisTime(startAt); ok {
		start = parsed
	}
	if parsed, ok := parseAnalysisTime(endAt); ok {
		end = parsed
	}
	if !end.After(start) {
		end = start.Add(24 * time.Hour)
	}
	return start, end
}

func calcSlowSQLSystemScore(_ int64, avgDurationMS float64, fullScanCount int64) (int, string, string) {
	score := 100
	score -= int(minInt64(fullScanCount*6, 40))
	score -= int(minFloat64(avgDurationMS/300, 25))
	if score < 0 {
		score = 0
	}
	switch {
	case score >= 90:
		return score, "excellent", "优"
	case score >= 75:
		return score, "good", "良"
	case score >= 60:
		return score, "medium", "中"
	default:
		return score, "poor", "差"
	}
}

func scoreLevelMatched(filter, level string) bool {
	value := strings.TrimSpace(strings.ToLower(filter))
	if value == "" || value == "all" {
		return true
	}
	return value == strings.TrimSpace(strings.ToLower(level))
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
