package services

import (
	"context"
	"strings"
	"sync"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 实例连通性探活（后台定时调度）
//
// 为什么必须有服务端调度：实例配置页右上角那个「自动探测」开关是纯浏览器
// 定时器（www/src/views/admin/system/instances/InstanceList.vue），只在页面
// 打开期间生效 —— 没人开页面就没有探测；而首页「实例连接异常」风险口径
// （common/services/home_dashboard.go）与列表状态列都直接读 last_check_status。
// 这与监控模块当初"采集挂在页面请求上、快照表连续 7 天 0 条"是同一个反模式。
//
// 探测深度：走 ProbeModeLightweight（只验数据库连通性），不做主机 SSH 探测与
// 采集权限体检 —— 那两项是纳管/手动「测试」该干的重活，每分钟对每个实例
// 跑一遍纯属浪费，还会把单轮探活耗时放大一个量级。
//
// 连接判定与状态回写复用 ProbeInstanceConnection（唯一实现），
// 与手动「测试」口径完全一致，不另起一套。
// ============================================================================

const (
	// 默认 tick。30 秒一轮只是"扫描节奏"，真正决定是否探测的是每个实例的
	// last_checked_at 与分层间隔（见 selectDueProbeCandidates），
	// 所以 tick 调小不会线性放大探测量。
	defaultInstanceProbeSpec = "@every 30s"
	// 异常/待检测实例的复测间隔：故障恢复要尽快感知，这正是首页风险口径关心的集合。
	defaultInstanceProbeErrorIntervalSec = 60
	// 可用实例的兜底复测间隔：已可用只需低频确认，避免无谓建连。
	defaultInstanceProbeOKIntervalSec = 600
	// 单轮最多探测条数：防止首次部署（全部实例 last_checked_at 为空）时一次性打满。
	defaultInstanceProbeBatchSize = 30
	// 并发池。单实例探活 = 一次建连 + 一条 select 1，8 并发足够且留有余量。
	instanceProbeWorkers = 8
	// 单实例探测的总预算（连接超时在内层另算 4s，这里兜住整体）。
	instanceProbeTimeout = 15 * time.Second
)

var (
	instanceProbeRunning bool
	instanceProbeMutex   sync.Mutex
)

// RegisterInstanceProbeCronJobs 注册实例连通性探活任务。
//
// 放在 common/services 包内（而不是 common/tasks）：连接测试的单一实现就在本包，
// 跨包会引入 tasks ← services 的反向依赖（instance_meta_sync.go 已经 services → tasks）。
func RegisterInstanceProbeCronJobs() {
	if global.App == nil || global.App.Cron == nil {
		return
	}
	spec := strings.TrimSpace(global.App.Config.Crontab.InstanceProbe)
	if spec == "" {
		spec = defaultInstanceProbeSpec
	}
	if _, err := global.App.Cron.AddFunc(spec, func() {
		RunInstanceProbeOnce()
	}); err != nil {
		global.App.Log.Warnf("register instance probe cron failed: %v", err)
		return
	}
	// 启动引导：进程起来后立刻补一轮，避免部署/重启后要等一个 tick 才刷新状态。
	go RunInstanceProbeOnce()
}

// probeCandidate 是探活挑选阶段的输入：只取判定所需的三个字段。
//
// 刻意不直接用 models.InsightInstances，让挑选逻辑保持纯函数、可单测，
// 不依赖数据库与模型的全字段。
type probeCandidate struct {
	ID            uint64
	Status        string
	LastCheckedAt time.Time
}

// selectDueProbeCandidates 按分层间隔挑选本轮要探测的实例，返回实例 ID。
//
// 口径：
//   - last_checked_at 为空（新增实例从未测过）→ 立即纳入；
//   - 异常/待检测（status != success，含 error / unknown / 空）→ errorInterval；
//   - 可用（status == success）→ okInterval。
//
// 排序按分层优先级：从未检测 > 异常/待检测 > 可用；同层内越久未测越靠前。最多 limit 条。
// now 由调用方注入，便于测试构造边界。
func selectDueProbeCandidates(rows []probeCandidate, now time.Time, errorInterval, okInterval time.Duration, limit int) []uint64 {
	if len(rows) == 0 || limit <= 0 {
		return nil
	}
	due := make([]probeCandidate, 0, len(rows))
	for _, row := range rows {
		if row.ID == 0 {
			continue
		}
		// 从未检测过：立即探测，不受间隔约束。
		if row.LastCheckedAt.IsZero() {
			due = append(due, row)
			continue
		}
		interval := okInterval
		if !strings.EqualFold(strings.TrimSpace(row.Status), "success") {
			interval = errorInterval
		}
		if now.Sub(row.LastCheckedAt) >= interval {
			due = append(due, row)
		}
	}
	if len(due) == 0 {
		return nil
	}
	// 排序 + 截断：优先级见 probePriority。
	sortProbeCandidatesByPriority(due)
	if len(due) > limit {
		due = due[:limit]
	}
	ids := make([]uint64, 0, len(due))
	for _, item := range due {
		ids = append(ids, item.ID)
	}
	return ids
}

// probePriority 分层优先级，越小越优先：
//  0. 从未检测过（last_checked_at 为零值，新增实例）；
//  1. 异常/待检测（status != success）；
//  2. 可用。
//
// 异常档排在可用档前面是有意的：单轮有条数上限，被截断时该优先补测的
// 正是首页「实例连接异常」口径关心的那一批，而不是"顺手最久没测的那个"。
func probePriority(item probeCandidate) int {
	if item.LastCheckedAt.IsZero() {
		return 0
	}
	if !strings.EqualFold(strings.TrimSpace(item.Status), "success") {
		return 1
	}
	return 2
}

// sortProbeCandidatesByPriority 排序：先按分层优先级，同层内越久未检测越靠前，最后按 ID 升序。
// 用插入排序：候选集规模受单轮上限约束，且避免为此引入 sort.Slice 的闭包开销。
func sortProbeCandidatesByPriority(items []probeCandidate) {
	less := func(a, b probeCandidate) bool {
		if pa, pb := probePriority(a), probePriority(b); pa != pb {
			return pa < pb
		}
		if !a.LastCheckedAt.Equal(b.LastCheckedAt) {
			return a.LastCheckedAt.Before(b.LastCheckedAt)
		}
		return a.ID < b.ID
	}
	for i := 1; i < len(items); i++ {
		cur := items[i]
		j := i - 1
		for j >= 0 && less(cur, items[j]) {
			items[j+1] = items[j]
			j--
		}
		items[j+1] = cur
	}
}

// RunInstanceProbeOnce 跑一轮实例探活。单飞：上一轮没结束就直接跳过本次 tick。
func RunInstanceProbeOnce() {
	instanceProbeMutex.Lock()
	if instanceProbeRunning {
		instanceProbeMutex.Unlock()
		return
	}
	instanceProbeRunning = true
	instanceProbeMutex.Unlock()
	defer func() {
		instanceProbeMutex.Lock()
		instanceProbeRunning = false
		instanceProbeMutex.Unlock()
	}()

	if global.App == nil || global.App.DB == nil {
		return
	}

	rows := make([]commonModels.InsightInstances, 0, 64)
	if err := global.App.DB.
		Table("insight_instances").
		Select("id, last_check_status, last_checked_at").
		Find(&rows).Error; err != nil {
		global.App.Log.Warnf("instance probe: 读取实例列表失败: %v", err)
		return
	}

	candidates := make([]probeCandidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, probeCandidate{
			ID:            row.ID,
			Status:        row.LastCheckStatus,
			LastCheckedAt: time.Time(row.LastCheckedAt),
		})
	}

	probeCfg := resolveInstanceProbeConfig()
	ids := selectDueProbeCandidates(candidates, time.Now(), probeCfg.errorInterval, probeCfg.okInterval, probeCfg.batchSize)
	if len(ids) == 0 {
		return
	}

	okCount, failCount := probeInstancesConcurrently(ids)
	if failCount > 0 {
		global.App.Log.Warnf("instance probe: 本轮探测 %d 个实例，可用 %d，异常 %d", len(ids), okCount, failCount)
		return
	}
	global.App.Log.Infof("instance probe: 本轮探测 %d 个实例，全部可用", len(ids))
}

// instanceProbeConfig 是探活的生效参数（配置缺省时回落代码默认值）。
type instanceProbeConfig struct {
	errorInterval time.Duration
	okInterval    time.Duration
	batchSize     int
}

func resolveInstanceProbeConfig() instanceProbeConfig {
	cfg := instanceProbeConfig{
		errorInterval: time.Duration(defaultInstanceProbeErrorIntervalSec) * time.Second,
		okInterval:    time.Duration(defaultInstanceProbeOKIntervalSec) * time.Second,
		batchSize:     defaultInstanceProbeBatchSize,
	}
	if global.App == nil {
		return cfg
	}
	crontab := global.App.Config.Crontab
	if crontab.InstanceProbeErrorInterval > 0 {
		cfg.errorInterval = time.Duration(crontab.InstanceProbeErrorInterval) * time.Second
	}
	if crontab.InstanceProbeOKInterval > 0 {
		cfg.okInterval = time.Duration(crontab.InstanceProbeOKInterval) * time.Second
	}
	if crontab.InstanceProbeBatchSize > 0 {
		cfg.batchSize = crontab.InstanceProbeBatchSize
	}
	return cfg
}

// probeInstancesConcurrently 并发探测一批实例，返回可用/异常条数。
// 每个实例的失败原因已由 ProbeInstanceConnection 写回 last_check_message，这里只做计数与汇总日志。
func probeInstancesConcurrently(ids []uint64) (okCount, failCount int) {
	workers := instanceProbeWorkers
	if workers > len(ids) {
		workers = len(ids)
	}
	outcomes := make([]error, len(ids))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(index int, instanceID uint64) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), instanceProbeTimeout)
			defer cancel()
			_, err := ProbeInstanceConnection(ctx, instanceID, ProbeModeLightweight)
			outcomes[index] = err
		}(i, id)
	}
	wg.Wait()
	for _, err := range outcomes {
		if err == nil {
			okCount++
			continue
		}
		failCount++
	}
	return okCount, failCount
}
