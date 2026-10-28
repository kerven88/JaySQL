package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 负载快照服务：首页实例列表 / 快照 CRUD / 后台 worker（两点采样状态机）。
//
// worker 约定（与通知 worker 同款思路）：
//   - cron @every 15s tick，单轮限量，CAS 抢占（UPDATE ... WHERE status = 旧值），
//     不用 SELECT ... FOR UPDATE，天然多副本安全（多副本时重复抢占被 CAS 挡住）。
//   - 采集本体开 goroutine（信号量限并发 4），tick 不阻塞。
//   - 崩溃恢复：滞留行（collecting 超 15 分钟 / window 已过期 15 分钟）标 failed。
// ============================================================================

const (
	loadSnapshotStatusPending    = "pending"
	loadSnapshotStatusCollecting = "collecting"
	loadSnapshotStatusWindow     = "window"
	loadSnapshotStatusReady      = "ready"
	loadSnapshotStatusFailed     = "failed"

	loadSnapshotWorkerBatch  = 8
	loadSnapshotWorkerMax    = 4 // 全局并发上限
	loadSnapshotStaleMinutes = 15
)

// ListLoadSnapshotInstancesForm 首页实例列表入参。
type ListLoadSnapshotInstancesForm struct {
	Search      string
	DbType      string
	Environment string
}

// monitorInstanceDisplayName 实例展示名：remark 优先，回落 host:port
// （与告警中心 monitor_alert_center.go 的「实例名称」口径一致）。
func monitorInstanceDisplayName(row monitorInstanceRow) string {
	if name := strings.TrimSpace(row.Remark); name != "" {
		return name
	}
	return fmt.Sprintf("%s:%d", row.Hostname, row.Port)
}

// LoadSnapshotInstanceItem 首页实例行（demo 8 列）。
type LoadSnapshotInstanceItem struct {
	InstanceID      string  `json:"instance_id"`
	InstanceName    string  `json:"instance_name"` // 实例展示名：remark 优先，回落 host:port（与告警中心「实例名称」口径一致）
	Addr            string  `json:"addr"`          // host:port
	Hostname        string  `json:"hostname"`
	Port            int     `json:"port"`
	DbType          string  `json:"db_type"`
	DbVersion       string  `json:"db_version"`
	EnvironmentName string  `json:"environment_name"`
	QPS             *float64 `json:"qps"`               // latest perf.qps
	Quality         string  `json:"quality"`           // qps 的采集质量
	ConnUsedPct     *float64 `json:"conn_used_pct"`     // latest cap.conn_used_pct
	ConnQuality     string  `json:"conn_quality"`
	SlowQueries     *float64 `json:"slow_queries"`      // latest eng.slow_queries
	SlowQuality     string  `json:"slow_quality"`
	LastCollectAt   string  `json:"last_collect_at"`   // 从未采集为空
	SnapshotCount   int64   `json:"snapshot_count"`    // 保留期内快照份数
	LastSnapshotAt  string  `json:"last_snapshot_at"`  // 最近一次快照时间（相对时间前端算）
	LastSnapshotID  uint64  `json:"last_snapshot_id"`  // 最近一次快照（「查看」直达）
	Running         bool    `json:"running"`           // 是否有进行中的快照任务
}

// ListLoadSnapshotInstancesService 首页：实例清单 + 实时负载 + 快照概览。
type ListLoadSnapshotInstancesService struct {
	ListLoadSnapshotInstancesForm
}

func (s *ListLoadSnapshotInstancesService) Run() ([]LoadSnapshotInstanceItem, error) {
	items := make([]LoadSnapshotInstanceItem, 0)
	if global.App.DB == nil {
		return items, nil
	}
	filter := monitorListFilter{
		Search:      s.Search,
		DbType:      s.DbType,
		Environment: s.Environment,
	}
	rows, err := listMonitorInstances(filter)
	if err != nil {
		return nil, err
	}

	// 版本号：listMonitorInstances 未带 db_version，单独补一把
	versions := map[string]string{}
	if len(rows) > 0 {
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.InstanceID)
		}
		type verRow struct {
			InstanceID string
			DbVersion  string
		}
		var vers []verRow
		if err := global.App.DB.Table("insight_instances").
			Select("instance_id, COALESCE(db_version, '') as db_version").
			Where("instance_id IN ?", ids).Find(&vers).Error; err == nil {
			for _, v := range vers {
				versions[v.InstanceID] = v.DbVersion
			}
		}
	}

	// 快照概览：份数 + 最近一次 + 是否有进行中
	type snapAgg struct {
		InstanceID string
		Cnt        int64
		LastID     uint64
		LastAt     *time.Time
		Running    int64
	}
	aggs := map[string]*snapAgg{}
	if len(rows) > 0 {
		var list []snapAgg
		if err := global.App.DB.Table("insight_monitor_load_snapshots").
			Select(`instance_id, COUNT(*) AS cnt, MAX(id) AS last_id, MAX(created_at) AS last_at,
				SUM(CASE WHEN status IN ('pending','collecting','window') THEN 1 ELSE 0 END) AS running`).
			Group("instance_id").Find(&list).Error; err == nil {
			for i := range list {
				aggs[list[i].InstanceID] = &list[i]
			}
		}
	}

	for _, row := range rows {
		engine := resolveEngine(row.DbType)
		if engine != "mysql" && engine != "tidb" && engine != "postgresql" {
			continue // 快照首期只覆盖 MySQL / PG
		}
		item := LoadSnapshotInstanceItem{
			InstanceID:      row.InstanceID,
			InstanceName:    monitorInstanceDisplayName(row),
			Addr:            fmt.Sprintf("%s:%d", row.Hostname, row.Port),
			Hostname:        row.Hostname,
			Port:            row.Port,
			DbType:          row.DbType,
			DbVersion:       versions[row.InstanceID],
			EnvironmentName: row.EnvironmentName,
			LastCollectAt:   "",
		}
		if row.LastCollectAt != nil {
			item.LastCollectAt = time.Time(*row.LastCollectAt).Format("2006-01-02 15:04:05")
		}
		// 实时负载：latest 表三个键（缺行 = 未采集，不是 0）
		type latestRow struct {
			MetricKey string
			Value     *float64
			Quality   string
		}
		var lat []latestRow
		if err := global.App.DB.Table("insight_monitor_metric_latest").
			Select("metric_key, value, quality").
			Where("instance_id = ? AND metric_key IN ?", row.InstanceID,
				[]string{MetricPerfQPS, MetricCapConnUsedPct, MetricEngSlowQueries}).
			Find(&lat).Error; err == nil {
			for _, lr := range lat {
				switch lr.MetricKey {
				case MetricPerfQPS:
					item.QPS = lr.Value
					item.Quality = lr.Quality
				case MetricCapConnUsedPct:
					item.ConnUsedPct = lr.Value
					item.ConnQuality = lr.Quality
				case MetricEngSlowQueries:
					item.SlowQueries = lr.Value
					item.SlowQuality = lr.Quality
				}
			}
		}
		if agg, ok := aggs[row.InstanceID]; ok {
			item.SnapshotCount = agg.Cnt
			item.LastSnapshotID = agg.LastID
			item.Running = agg.Running > 0
			if agg.LastAt != nil {
				item.LastSnapshotAt = agg.LastAt.Format("2006-01-02 15:04:05")
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// CreateLoadSnapshotService 生成快照任务（窗口 1-120 分钟，默认 15）。
type CreateLoadSnapshotService struct {
	InstanceID    string `json:"instance_id" binding:"required"`
	WindowMinutes int    `json:"window_minutes"`
	CreatedBy     string `json:"-"` // 由 JWT 操作人填充，不收客户端值
}

func (s *CreateLoadSnapshotService) Run() (uint64, error) {
	if global.App.DB == nil {
		return 0, fmt.Errorf("数据库不可用")
	}
	if strings.TrimSpace(s.InstanceID) == "" {
		return 0, fmt.Errorf("实例不能为空")
	}
	window := s.WindowMinutes
	if window <= 0 {
		window = 15
	}
	if window > 120 {
		window = 120
	}
	// 同实例进行中的任务只允许一个（前端禁用 + 后端兜底）
	var running int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("instance_id = ? AND status IN ?", s.InstanceID,
			[]string{loadSnapshotStatusPending, loadSnapshotStatusCollecting, loadSnapshotStatusWindow}).
		Count(&running).Error; err != nil {
		return 0, err
	}
	if running > 0 {
		return 0, fmt.Errorf("该实例已有进行中的快照任务，请等待完成后再生成")
	}
	rows, err := listMonitorInstances(monitorListFilter{InstanceID: s.InstanceID})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("实例不存在或未接入监控采集")
	}
	row := rows[0]
	engine := resolveEngine(row.DbType)
	if engine != "mysql" && engine != "tidb" && engine != "postgresql" {
		return 0, fmt.Errorf("负载快照暂不支持该引擎：%s", row.DbType)
	}
	record := monitorModels.InsightMonitorLoadSnapshots{
		// 嵌入 Model 必须显式初始化，否则 Create 时 BeforeCreate 写 ID 必崩
		Model:           &commonModels.Model{},
		InstanceID:      row.InstanceID,
		DbType:          row.DbType,
		InstanceName:    monitorInstanceDisplayName(row),
		EnvironmentName: row.EnvironmentName,
		Status:          loadSnapshotStatusPending,
		WindowMinutes:   window,
		CreatedBy:       s.CreatedBy,
	}
	if err := global.App.DB.Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

// GetLoadSnapshotDetailService 快照详情（含 payload）。
type GetLoadSnapshotDetailService struct {
	ID uint64
}

func (s *GetLoadSnapshotDetailService) Run() (*monitorModels.InsightMonitorLoadSnapshots, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库不可用")
	}
	var record monitorModels.InsightMonitorLoadSnapshots
	if err := global.App.DB.Where("id = ?", s.ID).Take(&record).Error; err != nil {
		return nil, fmt.Errorf("快照不存在")
	}
	return &record, nil
}

// DeleteLoadSnapshotService 删除快照。
type DeleteLoadSnapshotService struct {
	ID uint64
}

func (s *DeleteLoadSnapshotService) Run() error {
	if global.App.DB == nil {
		return fmt.Errorf("数据库不可用")
	}
	result := global.App.DB.Where("id = ?", s.ID).Delete(&monitorModels.InsightMonitorLoadSnapshots{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("快照不存在")
	}
	return nil
}

// ---------------------------------------------------------------------------
// worker
// ---------------------------------------------------------------------------

var loadSnapshotSem = make(chan struct{}, loadSnapshotWorkerMax)

// RunLoadSnapshotWorker 单轮 tick：恢复滞留 → 推进 pending → 处理到点的 window → 保留清理。
func RunLoadSnapshotWorker() {
	if global.App.DB == nil {
		return
	}
	recoverStaleLoadSnapshots()
	processPendingLoadSnapshots()
	processDueLoadSnapshots()
	cleanupLoadSnapshots()
}

// recoverStaleLoadSnapshots 崩溃恢复：滞留的进行中任务标 failed。
func recoverStaleLoadSnapshots() {
	deadline := time.Now().Add(-loadSnapshotStaleMinutes * time.Minute)
	global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("status IN ? AND updated_at < ?",
			[]string{loadSnapshotStatusPending, loadSnapshotStatusCollecting}, deadline).
		Updates(map[string]any{
			"status":        loadSnapshotStatusFailed,
			"error_message": "任务滞留（进程中断或采集超时），请重新生成",
		})
	global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("status = ? AND window_end_at < ?", loadSnapshotStatusWindow, deadline).
		Updates(map[string]any{
			"status":        loadSnapshotStatusFailed,
			"error_message": "观察窗口已过期但终点采样未执行（进程中断），请重新生成",
		})
}

// processPendingLoadSnapshots 抢占 pending → 基线采样 → 转 window。
func processPendingLoadSnapshots() {
	var rows []monitorModels.InsightMonitorLoadSnapshots
	if err := global.App.DB.
		Where("status = ?", loadSnapshotStatusPending).
		Order("id asc").Limit(loadSnapshotWorkerBatch).Find(&rows).Error; err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, row := range rows {
		wg.Add(1)
		go func(record monitorModels.InsightMonitorLoadSnapshots) {
			defer wg.Done()
			runBaselineLoadSnapshot(record)
		}(row)
	}
	wg.Wait()
}

func runBaselineLoadSnapshot(record monitorModels.InsightMonitorLoadSnapshots) {
	// CAS 抢占：pending → collecting（多副本时只有一个进程能成功）
	result := global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("id = ? AND status = ?", record.ID, loadSnapshotStatusPending).
		Updates(map[string]any{"status": loadSnapshotStatusCollecting, "progress": 10})
	if result.Error != nil || result.RowsAffected == 0 {
		return
	}
	loadSnapshotSem <- struct{}{}
	defer func() { <-loadSnapshotSem }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	q, err := connectLoadSnapshot(ctx, loadSnapshotInstanceRow(record.InstanceID))
	if err != nil {
		failLoadSnapshot(record.ID, err.Error())
		return
	}
	defer closeLoadSnapshotConn(q)

	baseline, _, err := captureLoadSnapshotBaseline(q, resolveEngine(record.DbType))
	if err != nil {
		failLoadSnapshot(record.ID, err.Error())
		return
	}
	payload, _ := json.Marshal(map[string]any{"baseline": baseline})
	now := time.Now()
	endAt := now.Add(time.Duration(record.WindowMinutes) * time.Minute)
	global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("id = ? AND status = ?", record.ID, loadSnapshotStatusCollecting).
		Updates(map[string]any{
			"status":        loadSnapshotStatusWindow,
			"progress":      40,
			"payload":       string(payload),
			"baseline_at":   now,
			"window_end_at": endAt,
		})
}

// processDueLoadSnapshots 窗口到点 → 终点采样 + 差分 → ready。
func processDueLoadSnapshots() {
	var rows []monitorModels.InsightMonitorLoadSnapshots
	if err := global.App.DB.
		Where("status = ? AND window_end_at <= ?", loadSnapshotStatusWindow, time.Now()).
		Order("window_end_at asc").Limit(loadSnapshotWorkerBatch).Find(&rows).Error; err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, row := range rows {
		wg.Add(1)
		go func(record monitorModels.InsightMonitorLoadSnapshots) {
			defer wg.Done()
			runEndLoadSnapshot(record)
		}(row)
	}
	wg.Wait()
}

func runEndLoadSnapshot(record monitorModels.InsightMonitorLoadSnapshots) {
	result := global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("id = ? AND status = ?", record.ID, loadSnapshotStatusWindow).
		Updates(map[string]any{"status": loadSnapshotStatusCollecting, "progress": 70})
	if result.Error != nil || result.RowsAffected == 0 {
		return
	}
	loadSnapshotSem <- struct{}{}
	defer func() { <-loadSnapshotSem }()

	if record.BaselineAt == nil {
		failLoadSnapshot(record.ID, "基线时间缺失，无法差分")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	q, err := connectLoadSnapshot(ctx, loadSnapshotInstanceRow(record.InstanceID))
	if err != nil {
		failLoadSnapshot(record.ID, err.Error())
		return
	}
	defer closeLoadSnapshotConn(q)

	engine := resolveEngine(record.DbType)
	// 从 payload 里还原基线
	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		failLoadSnapshot(record.ID, "基线数据损坏："+err.Error())
		return
	}
	baseline, _ := payload["baseline"].(map[string]any)
	if baseline == nil {
		failLoadSnapshot(record.ID, "基线数据缺失，无法差分")
		return
	}
	// baseline 里的 digest 行经 JSON 往返变成 []any(map[string]any)，还原为 []map[string]string
	baseline = normalizeBaselineRows(baseline)

	verNum := 0
	if engine == "postgresql" {
		// ver_num 在基线采集时写入 baseline（pgLoadStatementsSQL 列名分支依赖它）
		if v, ok := baseline["ver_num"].(float64); ok {
			verNum = int(v)
		}
	}
	windowSeconds := int(time.Since(time.Time(*record.BaselineAt)).Seconds())
	if windowSeconds <= 0 {
		windowSeconds = record.WindowMinutes * 60
	}
	sections, err := captureLoadSnapshotEnd(q, engine, baseline, windowSeconds, verNum)
	if err != nil {
		failLoadSnapshot(record.ID, err.Error())
		return
	}
	// 版本号在终点采样时一并落库（创建时 listMonitorInstances 不带 db_version）
	version := ""
	if basics, ok := sections["basics"].(*snapshotSection); ok && basics.Status == "ok" && basics.Data != nil {
		version = fmt.Sprintf("%v", basics.Data["version"])
	}
	finalPayload, _ := json.Marshal(map[string]any{
		"engine":   engine,
		"sections": sections,
		"window": map[string]any{
			"start":   time.Time(*record.BaselineAt).Format("2006-01-02 15:04:05"),
			"end":     time.Now().Format("2006-01-02 15:04:05"),
			"seconds": windowSeconds,
		},
	})
	global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("id = ? AND status = ?", record.ID, loadSnapshotStatusCollecting).
		Updates(map[string]any{
			"status":     loadSnapshotStatusReady,
			"progress":   100,
			"db_version": version,
			"payload":    string(finalPayload),
		})
}

// normalizeBaselineRows JSON 往返后 []map[string]string 退化为 []any(map[string]any)，还原。
func normalizeBaselineRows(baseline map[string]any) map[string]any {
	out := map[string]any{}
	for key, val := range baseline {
		switch v := val.(type) {
		case []any:
			rows := make([]map[string]string, 0, len(v))
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					converted := make(map[string]string, len(m))
					for k, value := range m {
						converted[k] = strings.TrimSpace(fmt.Sprintf("%v", value))
					}
					rows = append(rows, converted)
				}
			}
			out[key] = rows
		case map[string]any:
			// counters 数值保留原样
			out[key] = v
		default:
			out[key] = val
		}
	}
	return out
}

func failLoadSnapshot(id uint64, message string) {
	global.App.DB.Model(&monitorModels.InsightMonitorLoadSnapshots{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        loadSnapshotStatusFailed,
			"error_message": truncateLoadMessage(message, 500),
		})
}

func truncateLoadMessage(message string, limit int) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// loadSnapshotInstanceRow 快照场景按 instance_id 取实例行（含密码，用于解密建连）。
func loadSnapshotInstanceRow(instanceID string) monitorInstanceRow {
	rows, _ := listMonitorInstances(monitorListFilter{InstanceID: instanceID})
	if len(rows) == 0 {
		return monitorInstanceRow{InstanceID: instanceID}
	}
	return rows[0]
}

// closeLoadSnapshotConn 释放快照连接：PG 是显式短连接，MySQL 是会话。
//
// 两个分支都必须真关：MySQL 会话里那条连接是"整个快照共用"的，
// 漏关就等于在业务库上留一条连接直到 wait_timeout（默认 8 小时）。
func closeLoadSnapshotConn(q snapQuerier) {
	switch v := q.(type) {
	case *snapPgQ:
		_ = v.conn.Close(context.Background())
	case *snapMySQLQ:
		// 先 cancel 再 Close：cancel 会中止仍在跑的查询，
		// 否则 Close 要等驱动把手上的查询收尾，慢查询下会拖住整个快照流程。
		if v.cancel != nil {
			v.cancel()
		}
		_ = v.session.Close()
	}
}

// cleanupLoadSnapshots 保留策略：默认 90 天 + 每实例上限 200 份，每小时跑一次。
// 每实例超限按「删该实例最旧的」处理（两步走，兼容 MySQL 5.7 元库无窗口函数）。
var loadSnapshotLastCleanup time.Time

func cleanupLoadSnapshots() {
	if global.App.DB == nil {
		return
	}
	if time.Since(loadSnapshotLastCleanup) < time.Hour {
		return
	}
	loadSnapshotLastCleanup = time.Now()
	global.App.DB.Where("created_at < ?", time.Now().AddDate(0, 0, -90)).
		Delete(&monitorModels.InsightMonitorLoadSnapshots{})
	type cntRow struct {
		InstanceID string
		Cnt        int64
		CutID      uint64
	}
	var overs []cntRow
	if err := global.App.DB.Raw(`
SELECT instance_id, COUNT(*) AS cnt, (
  SELECT id FROM insight_monitor_load_snapshots s2
  WHERE s2.instance_id = s1.instance_id
  ORDER BY id DESC LIMIT 1 OFFSET 199
) AS cut_id
FROM insight_monitor_load_snapshots s1
GROUP BY instance_id HAVING COUNT(*) > 200`).Scan(&overs).Error; err != nil {
		return
	}
	for _, item := range overs {
		if item.CutID == 0 {
			continue
		}
		global.App.DB.Where("instance_id = ? AND id < ?", item.InstanceID, item.CutID).
			Delete(&monitorModels.InsightMonitorLoadSnapshots{})
	}
}
