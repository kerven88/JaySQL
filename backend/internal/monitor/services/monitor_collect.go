package services

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// ============================================================================
// 后台采集调度
//
// 这是本次改造的根：原先采集挂在页面请求上（collectInstance 只在 dashboard/detail
// 两个接口里被调用），全平台没有任何 monitor 定时任务 —— 结果是"没人打开页面就没有
// 数据、没有告警"，实测快照表连续 7 天 0 条。
//
// 目标形态：后台调度器 7×24 跑，页面只读库。
// ============================================================================

const (
	// 采集 tick：10 秒一次。间隔 60s 的实例会在 6 个 tick 里按相位错峰落到不同 tick，
	// 避免 100 个实例在同一秒集中建连（网络与元数据库瞬时抖动）。
	monitorCollectSpec = "@every 10s"
	// 告警评估 tick 与采集 tick 分开：采集可能因网络阻塞耗时数秒，
	// 若与评估同批，会导致多个实例的告警在同一秒集中触发，持续时长判定也无法做准。
	monitorAlertSpec = "@every 1m"
	// 数据卫生维护
	monitorMaintenanceSpec = "@every 1h"

	// 并发池。原值 4 偏小；100 实例 × 60s 平均 1.7 次连接/秒，12 个 worker 足够且留有余量。
	monitorCollectWorkers = 12

	// 实例级采集周期的默认值是 0（跟随平台周期，见 monitor_templates.go 自动登记），
	// 不再物化 60 —— 那会让全局改小周期时实例永远跟不上。
	defaultTimeoutSeconds = 5
	defaultRetentionHours = 168
)

var (
	monitorCollectRunning bool
	monitorCollectMutex   sync.Mutex
	monitorAlertRunning   bool
)

// RegisterMonitorCronJobs 注册监控相关的三个后台任务。
func RegisterMonitorCronJobs() {
	if global.App == nil || global.App.Cron == nil {
		return
	}
	if _, err := global.App.Cron.AddFunc(monitorCollectSpec, func() {
		CollectMonitorInstances()
	}); err != nil {
		global.App.Log.Errorf("register monitor collect cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc(monitorAlertSpec, func() {
		EvaluateMonitorAlerts()
	}); err != nil {
		global.App.Log.Errorf("register monitor alert cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc(monitorNotifySpec, func() {
		RunMonitorNotifyWorker()
	}); err != nil {
		global.App.Log.Warnf("register monitor notify cron failed: %v", err)
	}
	// 升级调度：未认领超时升级 + 聚合窗口汇总补发。
	// 与投递 worker 分开注册：两者的节奏不同（升级以分钟计、投递 15 秒一轮），
	// 合在一起会让"升级判断"被投递的耗时拖慢。
	if _, err := global.App.Cron.AddFunc(monitorEscalationSpec, func() {
		RunMonitorAlertEscalation()
	}); err != nil {
		global.App.Log.Warnf("register monitor escalation cron failed: %v", err)
	}
	if _, err := global.App.Cron.AddFunc(monitorMaintenanceSpec, func() {
		RunMonitorMaintenance()
	}); err != nil {
		global.App.Log.Warnf("register monitor maintenance cron failed: %v", err)
	}
	// 负载快照 worker：单飞守卫在 RunLoadSnapshotWorker 内部由 CAS 抢占保证，
	// 这里 15s 一轮即可（与通知 worker 同节奏）。
	if _, err := global.App.Cron.AddFunc("@every 15s", func() {
		RunLoadSnapshotWorker()
	}); err != nil {
		global.App.Log.Warnf("register load snapshot cron failed: %v", err)
	}

	// 启动引导：为所有可监控实例补齐配置行 → 预热一轮采集与评估。
	// 这样部署完新版本无需等下一分钟就能在页面上看到数据。
	// （原「指标字典播种」步骤已随字典模块下线移除：指标定义固定在代码里，不再落库。）
	go bootstrapMonitor()
}

func bootstrapMonitor() {
	defer func() {
		if r := recover(); r != nil {
			global.App.Log.Errorf("bootstrap monitor panic: %v", r)
		}
	}()
	if global.App.DB == nil {
		return
	}
	if err := EnsureMetricTemplates(); err != nil {
		global.App.Log.Warnf("seed monitor metric templates failed: %v", err)
	}
	EnsureMonitorSchema(global.App.DB)
	if rows, err := ensureTargetConfigRows(); err == nil && rows > 0 {
		global.App.Log.Infof("[monitor] 已为 %d 个实例补齐监控配置行", rows)
	}
	// 启动自检：先把"三处周期是否同源"打成一行日志，再干活。
	// 部署完只要看这一行，就知道 Prometheus 抓取 / 落库 / PromQL 窗口有没有对上。
	promClient := newPrometheusClient()
	LogScrapeConfigSelfCheck(probePrometheus(context.Background(), promClient), promClient)
	// 健康状态判定自检：当前生效的故障/风险阈值与扣分表、数据过期阈值打成一行，
	// 配置被改坏（降级运行）时同位置会打出 WARN。
	LogHealthStateSelfCheck()
	// 通知配置一次性迁移：旧 insight_notify_settings 转成渠道实例 + 兜底策略。
	// 必须在评估之前跑，否则第一轮告警会因为"还没有渠道"而无处可发。
	EnsureNotifyBootstrap()

	RunMonitorMaintenance()
	CollectMonitorInstances()
	EvaluateMonitorAlerts()
	// 评估刚生成的投递任务立即处理一轮：部署后不必等 15 秒才看到通知
	RunMonitorNotifyWorker()
	// 启动即跑一轮升级调度：为已活跃的存量告警登记升级链，并补发上一窗口的聚合汇总
	RunMonitorAlertEscalation()
}

// CollectMonitorInstances 一轮采集：挑出本轮到期的实例，放进并发池。
func CollectMonitorInstances() {
	monitorCollectMutex.Lock()
	if monitorCollectRunning {
		monitorCollectMutex.Unlock()
		return
	}
	monitorCollectRunning = true
	monitorCollectMutex.Unlock()
	defer func() {
		monitorCollectMutex.Lock()
		monitorCollectRunning = false
		monitorCollectMutex.Unlock()
		if r := recover(); r != nil {
			global.App.Log.Errorf("CollectMonitorInstances panic: %v", r)
		}
	}()

	if global.App.DB == nil {
		return
	}

	rows, err := listMonitorInstances(monitorListFilter{})
	if err != nil {
		global.App.Log.Errorf("load monitor instances failed: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	now := time.Now()
	tick := 10 * time.Second
	type dueItem struct {
		row    monitorInstanceRow
		slotTs time.Time
	}
	due := make([]dueItem, 0, len(rows))
	for _, row := range rows {
		if dueForCollect(row, now, tick) {
			// 落库时间用本轮槽位起点（相位对齐的 interval 网格点），
			// 而不是执行时刻：执行时刻含探测/排队/采集耗时（实测 +4s 左右），
			// 会让时序点漂离聚合桶边界，前端趋势图出现"每分钟固定缺一个点"的假缺口。
			due = append(due, dueItem{row: row, slotTs: collectSlotTime(now, row.InstanceID, effectiveCollectInterval(row))})
		}
	}
	if len(due) == 0 {
		return
	}

	started := time.Now()
	promClient := newPrometheusClient()
	promStatus := probePrometheus(context.Background(), promClient)

	var wg sync.WaitGroup
	sem := make(chan struct{}, monitorCollectWorkers)
	for _, item := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func(item dueItem) {
			defer wg.Done()
			defer func() {
				<-sem
				if r := recover(); r != nil {
					global.App.Log.Errorf("[monitor] 采集实例 %s 时 panic: %v", item.row.InstanceID, r)
				}
			}()
			collectAndPersist(item.row, promStatus, promClient, item.slotTs)
		}(item)
	}
	wg.Wait()

	elapsed := time.Since(started)
	global.App.Log.Infof("[monitor] 本轮采集 %d/%d 个实例，耗时 %dms", len(due), len(rows), elapsed.Milliseconds())
	// 全批耗时保护：一个 tick 超过周期的 80% 说明实例响应普遍变慢，需要关注。
	if elapsed > 48*time.Second {
		global.App.Log.Warnf("[monitor] 本轮采集耗时 %s，接近或超过采集周期，请检查实例响应速度与并发度设置", elapsed)
	}
}

// collectAndPersist 采集单个实例并落库。这里是采集链路唯一入口 ——
// 后台任务、页面首次补采、手动"立即采集"都走它，避免出现两套会各自漂移的实现。
//
// collectTs 是本轮采样点的归属时刻（槽位起点，相位对齐的 interval 网格）；
// 传零值时回落为 time.Now()（仅供极端场景兜底，正常调用方都应传槽位时间）。
//
// 指标范围来自 resolveInstanceMetricKeys：字典全局启用 ∩ 实例绑定模板 ∩ EngineScope。
// 模板表为空时退化为"按全局字典全采"，不会因为没配模板而停止采集。
func collectAndPersist(row monitorInstanceRow, promStatus PrometheusStatus, promClient *prometheusClient, collectTs time.Time) InstanceMonitorCard {
	keys, _, _ := resolveInstanceMetricKeys(row)
	// 引擎层预算：实例自配超时 + 3s 握手余量（历史口径，保持不变）。
	engineCtx, engineCancel := context.WithTimeout(context.Background(), instanceTimeoutSeconds(row)+3*time.Second)
	defer engineCancel()
	// 资源层预算：独立计时，不再吃引擎段的残羹（见 collectInstanceSamples 注释）。
	resourceCtx, resourceCancel := newResourceCollectCtx(context.Background())
	defer resourceCancel()

	started := time.Now()
	samples := collectInstanceSamples(engineCtx, resourceCtx, row, promStatus, promClient, keys)
	elapsedMs := int(time.Since(started).Milliseconds())
	// 采集耗时只有跑完才知道，所以由框架在这里补点 —— 各引擎采集器都不产出它。
	// 以前没人补：Oracle/SQLServer 被引擎层的"未实现"兜底标成 unsupported，
	// MySQL/PG 干脆没有这一行，明细页的"采集耗时"永远拿不到值。
	if wantMetric(keys, MetricAvailCollectMs) {
		samples = append(samples, MetricSample{
			Key: MetricAvailCollectMs, Value: float64(elapsedMs), Unit: "ms",
			Quality: QualityOK, Source: "direct",
		})
	}

	persistSamples(row, samples, elapsedMs, nil, collectTs)
	// 实例画像（版本 / OS / 规格 / 运行时长 / 角色）与指标分开采：
	// 指标每轮都采，画像按 profile_facts_at 节流（默认 1 小时），见 monitor_profile_collect.go。
	//
	// 画像必须用独立预算：上面的 ctx 是「指标 + 画像」共用的总超时（默认 5+3 秒），
	// 指标采完后常常只剩几百毫秒，而画像要新建连接再跑两次查询 —— 会被直接饿死。
	// 实例刚接入时指标采集偏慢，版本就永远采不到（2026-10-18 的 PG 15.19 实例即此）。
	profileCtx, profileCancel := context.WithTimeout(context.Background(),
		instanceTimeoutSeconds(row)+5*time.Second)
	defer profileCancel()
	refreshInstanceProfileFacts(profileCtx, row, promStatus, promClient, false)
	return buildCardFromStore(row, latestRowsFromSamples(row, samples), nil)
}

// collectInstanceSamples 采集一个实例的全部采样点（引擎层 + 资源层）。
//
// 设计要点：
//   - "该引擎不支持此指标"是正常状态（unsupported），不是采集失败 —— 这与慢日志改造
//     里"探不到路径被当成错误报警，每实例刷 4320 条/天"的教训一脉相承；
//   - 只有"连不上/认证失败"才算整体失败，此时不产出任何指标点；
//   - 网络类错误按实例配置重试（指数退避），认证类错误不重试；
//
// 引擎层与资源层各用一段独立预算（engineCtx / resourceCtx，见 newResourceCollectCtx）：
// 资源层要串行跑 16 条 PromQL，与引擎段共用同一个 ctx 时，引擎段用掉几秒后资源段
// 只剩残羹 —— 表现是引擎指标一切正常、16 项资源指标集体"无数据"，
// 而真实原因是超时不是没数据，排查方向会被彻底带偏。
func collectInstanceSamples(
	engineCtx context.Context,
	resourceCtx context.Context,
	row monitorInstanceRow,
	promStatus PrometheusStatus,
	promClient *prometheusClient,
	keys []string,
) []MetricSample {
	samples := make([]MetricSample, 0, 32)

	if !engineImplemented(row.DbType) {
		samples = append(samples, disabledEngineSamples(keys, "该引擎暂未实现监控采集，实例已在监控对象列表中标注")...)
		samples = append(samples, collectResourceSamples(resourceCtx, promClient, promStatus, row)...)
		return samples
	}

	if row.EngineSourceSetting == "none" {
		samples = append(samples, disabledEngineSamples(keys, "该实例采集策略中已关闭引擎指标采集")...)
		samples = append(samples, collectResourceSamples(resourceCtx, promClient, promStatus, row)...)
		return samples
	}

	engineSamples, err := collectEngineSamplesWithRetry(engineCtx, row, keys)
	if err != nil {
		reason := trimError(err)
		if isConnectionSaturated(err) {
			// 页面与告警里必须能一眼看出"是对方连接满了"而不是"我们采集坏了"，
			// 否则运维会一直查 JaySQL 而不是查目标库的 max_connections。
			reason = "目标数据库连接数已满，本轮跳过采集且未重试（避免加重负载）：" + reason
		}
		// 整体不可用：不产出指标点，由调用方回写采集失败状态。
		// 同时补上 avail.up=0，让"实例不可达"这条 P1 规则能命中。
		samples = append(samples, MetricSample{
			Key: MetricAvailUp, Quality: QualityError, Reason: reason, Source: "direct",
		})
		markCollectFailure(row.InstanceID, 0, err)
		return samples
	}
	samples = append(samples, MetricSample{Key: MetricAvailUp, Value: 1, Quality: QualityOK, Source: "direct"})
	samples = append(samples, engineSamples...)
	samples = append(samples, collectResourceSamples(resourceCtx, promClient, promStatus, row)...)
	return samples
}

// resourceCollectBudget 资源层（Prometheus）的独立超时预算。
//
// 取值依据：CollectHostResources 串行执行 16 条 PromQL，单条在健康 Prometheus 上
// 通常 20~200ms，但 irate + max by(device) 这类重查询在大节点上可能到 1~2s，
// 16 条留 15s 足够，又不至于让一个实例的整轮耗时无限膨胀。
//
// 它不是"越大越好"：预算过长会拖长整批采集（12 个 worker 并发），
// 反而放大 Prometheus 侧的排队。慢就是慢，标成超时让人看见，比静默 noData 强。
const resourceCollectBudget = 15 * time.Second

// newResourceCollectCtx 派生资源层采集的独立预算 ctx。
// parent 传 HTTP 请求 ctx 时，客户端断开会连带取消，不会留下空转的查询。
func newResourceCollectCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, resourceCollectBudget)
}

// disabledEngineSamples 为"整批引擎层指标本轮不采集"的场景产出占位采样。
//
// 只覆盖 resolvedKeys（模板内的指标），而不是字典全集：否则实例明明只配了 10 个指标，
// 明细页却会因为"引擎未实现"冒出 40 条不适用记录 —— 这既吓人也掩盖真实配置。
func disabledEngineSamples(keys []string, reason string) []MetricSample {
	samples := make([]MetricSample, 0, len(keys))
	for _, key := range keys {
		unit := metricUnitOf(key)
		samples = append(samples, MetricSample{
			Key: key, Unit: unit, Quality: QualityUnsupported,
			Reason: reason, Source: "direct",
		})
	}
	return samples
}

// collectEngineSamplesWithRetry 仅在网络类错误上重试（指数退避 1s/2s/4s）。
// 认证失败、SQL 不支持等确定性错误重试无意义，只会拖长整批耗时。
//
// 连接饱和（isConnectionSaturated）单独短路：立即返回、一次都不重试。
// 详见该函数注释 —— 对已经打满的实例，重试是加压而不是容错。
func collectEngineSamplesWithRetry(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	saturated := func(err error) bool {
		if !isConnectionSaturated(err) {
			return false
		}
		global.App.Log.Warnf("[monitor] 实例 %s 连接数已满，本轮放弃且不再重试：%v", row.InstanceID, err)
		return true
	}
	attempts := row.RetryCount
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 3 {
		attempts = 3
	}
	var lastErr error
	for attempt := 0; attempt <= attempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, lastErr
			case <-time.After(backoff):
			}
		}
		samples, err := collectEngineSamples(ctx, row, keys)
		if err == nil {
			return samples, nil
		}
		lastErr = err
		if saturated(err) || !isRetryableCollectError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func isRetryableCollectError(err error) bool {
	if err == nil {
		return false
	}
	// 连接饱和必须一票否决：它同时命中下面的 "timeout" 之类的关键词，
	// 若先判可重试就会在已打满的实例上继续建连（见 isConnectionSaturated）。
	if isConnectionSaturated(err) {
		return false
	}
	message := strings.ToLower(err.Error())
	retryable := []string{
		"timeout", "i/o timeout", "connection refused", "connection reset",
		"broken pipe", "eof", "server closed", "dial tcp", "no route to host",
		"deadline exceeded", "the database system is starting up",
	}
	for _, item := range retryable {
		if strings.Contains(message, item) {
			return true
		}
	}
	return false
}

// isConnectionSaturated 判定错误是否表示「目标库连接数已经打满」。
//
// 这类错误绝不能重试 —— 重试的动作本身就是"新建一条连接"，正好落在已经耗尽的库上：
//   - 重试 N 次 = 额外 N 次 TCP 握手 + N 次认证 + N 次 KILL QUERY 清理，
//     把一次瞬时饱和放大成持续饱和（常见 max_connections=151 的实例，
//     原本 10 秒后自然回落，被重试钉死在满负荷上）；
//   - 更糟的是它还会挤掉业务自己的连接，让"监控"变成故障的放大器。
//
// 正确处置是立即放弃本轮：consecutive_failures 递增会触发周期 ×5 退避
// （见 effectiveCollectInterval），等于天然的熔断，等目标库自己缓过来。
//
// 断言口径刻意保守：只认各引擎明确的"连接配额耗尽"文本与 Oracle 错误码，
// 不做模糊匹配 —— 误判成饱和只是少采一轮，误判成可重试则可能压垮实例。
func isConnectionSaturated(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	saturated := []string{
		// MySQL / TiDB / Doris（MySQL 协议兼容）
		"too many connections",
		// PostgreSQL / openGauss
		"too many clients already",
		"remaining connection slots are reserved",
		// Oracle（文本形式与错误码形式都要认，go-ora 两种都出现过）
		"ora-00020", // maximum number of processes (%s) exceeded
		"ora-12516", // TNS: listener could not find available handler with matching protocol stack
		"ora-12518", // TNS: listener could not hand off client connection
	}
	for _, item := range saturated {
		if strings.Contains(message, item) {
			return true
		}
	}
	return false
}

// dueForCollect 判断某实例本轮是否到期。
//
// 判定方式是「槽位跨越」而不是「经过秒数 ≥ 周期」：
//   - 槽位 = 相位对齐的 interval 网格（见 collectSlotTime），now 与 last 落在不同槽位即到期；
//   - 这样实际采样点严格落在相位网格上，周期是多少就采多少个点。旧判定
//     (now-last >= interval) 受 cron tick 量化：tick 10s + 周期 15s 时，15 秒的到期
//     时间只能被 20 秒后的下一个 tick 捕获 —— 配置 15s 实际跑成 20s，丢掉 1/3 样本，
//     且落点漂移使 15s 聚合桶每分钟固定缺一个（前端表现为时间戳 0/30/45/60 缺 15）。
//
// 错峰策略：相位 = hash(instance_id) % interval，不同实例的槽位网格彼此错开，
// 避免 100 个实例在同一 tick 里集中建连；同一实例的相位恒定，点始终落在自己的网格上。
//
// 退避策略：连续失败 3 次后把周期放大 5 倍（上限 10 分钟），
// 避免对一个已下线实例持续空转、持续刷日志；放大后的周期仍是原周期的整数倍，
// 恢复后采样点自动回到原网格，不会残留错位。
//
// 周期来源：只认 insight_monitor_scrape_settings 那一行（见 monitor_scrape_config.go）。
// 实例级 scrape_interval_seconds 只能在此基础上放慢（整数倍），不能加快 ——
// 快于 Prometheus 抓取只会反复取到同一个值。
func dueForCollect(row monitorInstanceRow, now time.Time, tick time.Duration) bool {
	interval := effectiveCollectInterval(row)
	phase := int64(instancePhase(row.InstanceID, interval))

	if row.LastCollectAt == nil {
		// 首次采集：等相位窗口（tick 宽度）到了再采，让各实例天然错开
		delta := int((now.Unix() - phase) % int64(interval))
		if delta < 0 {
			delta += interval
		}
		return delta < int(tick.Seconds())
	}
	lastSlot := (time.Time(*row.LastCollectAt).Unix() - phase) / int64(interval)
	nowSlot := (now.Unix() - phase) / int64(interval)
	return nowSlot > lastSlot
}

// effectiveCollectInterval 实例实际生效的采集周期：平台周期 → 实例级放慢 → 失败退避。
func effectiveCollectInterval(row monitorInstanceRow) int {
	interval, _ := ResolveInstanceScrapeInterval(row.ScrapeIntervalSeconds, GetScrapeConfig())
	if interval < MinScrapeIntervalSeconds {
		interval = MinScrapeIntervalSeconds
	}
	if row.ConsecutiveFailures >= 3 {
		interval *= 5
		if interval > 600 {
			interval = 600
		}
	}
	return interval
}

// collectSlotTime 计算此刻所属的采集槽位起点：phase + k*interval 中最大的一个（≤ now）。
// 采集落库的时间戳统一用它，保证时序点严格落在相位网格上、与历史聚合桶边界对齐。
func collectSlotTime(now time.Time, instanceID string, interval int) time.Time {
	if interval <= 0 {
		return now
	}
	phase := int64(instancePhase(instanceID, interval))
	slot := (now.Unix() - phase) / int64(interval)
	return time.Unix(phase+slot*int64(interval), 0)
}

func instancePhase(instanceID string, interval int) uint64 {
	if interval <= 0 {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(instanceID))
	return uint64(hasher.Sum32()) % uint64(interval)
}

// ---------------------------------------------------------------------------
// 实例清单加载（页面与后台共用同一份查询，避免入口不一致）
// ---------------------------------------------------------------------------

type monitorListFilter struct {
	Search      string
	Environment string
	DbType      string
	Business    string
	GroupName   string
	InstanceID  string
	// IncludeUnimplemented 为 true 时也返回"引擎未实现"的实例（监控对象页需要标注），
	// 为 false 时只返回可实际采集的引擎。
	IncludeUnimplemented bool
}

// listMonitorInstances 加载监控实例清单。
// 关键修复：不再硬编码 db_type IN ('MySQL','PostgreSQL')，而是按引擎能力注册表取白名单，
// 使已经实现采集的 TiDB / Doris / ClickHouse / SQLServer / Oracle 能真正进入监控页。
func listMonitorInstances(filter monitorListFilter) ([]monitorInstanceRow, error) {
	rows := make([]monitorInstanceRow, 0)
	if global.App.DB == nil {
		return rows, nil
	}
	tx := global.App.DB.Table("insight_instances a").
		Select(`a.id, a.instance_id, a.hostname, a.port, a.user, a.password, a.db_type, a.extra_config,
			COALESCE(a.instance_role, '') as instance_role,
			COALESCE(env.name, '') as environment_name, a.remark,
			COALESCE(mt.business, '') as business,
			COALESCE(mt.group_name, '') as group_name,
			COALESCE(mt.enabled, 1) as enabled,
			COALESCE(mt.prometheus_target, '') as prometheus_target,
			COALESCE(mt.resource_source, 'prometheus') as resource_source_setting,
			COALESCE(mt.engine_source, 'direct') as engine_source_setting,
			COALESCE(mt.metrics_template, '') as metrics_template,
			mt.alert_template_id,
			COALESCE(mt.retention_hours, 168) as retention_hours,
			COALESCE(mt.scrape_interval_seconds, 0) as scrape_interval_seconds,
			COALESCE(mt.timeout_seconds, 5) as timeout_seconds,
			COALESCE(mt.retry_count, 2) as retry_count,
			COALESCE(mt.deploy_mode, 'standalone') as deploy_mode,
			mt.last_collect_at,
			COALESCE(mt.last_collect_status, 'never') as last_collect_status,
			COALESCE(mt.last_collect_message, '') as last_collect_message,
			COALESCE(mt.last_collect_ms, 0) as last_collect_ms,
			mt.last_success_at,
			COALESCE(mt.consecutive_failures, 0) as consecutive_failures,
			COALESCE(mt.collect_quality, 'no_data') as collect_quality`).
		Joins("left join insight_instance_environments env on a.environment = env.id").
		Joins("left join insight_monitor_target_configs mt on mt.instance_id = a.instance_id")

	tx = tx.Where("LOWER(a.db_type) IN ?", monitorableEngineKeys())
	tx = tx.Where("(mt.id IS NULL OR mt.enabled = ?)", true)

	if value := strings.TrimSpace(filter.Search); value != "" {
		like := "%" + value + "%"
		tx = tx.Where("(a.hostname like ? or a.remark like ? or a.instance_id like ?)", like, like, like)
	}
	if value := strings.TrimSpace(filter.Environment); value != "" {
		tx = tx.Where("env.name = ?", value)
	}
	if value := strings.TrimSpace(filter.DbType); value != "" {
		tx = tx.Where("LOWER(a.db_type) = ?", resolveEngine(value))
	}
	if value := strings.TrimSpace(filter.Business); value != "" {
		tx = tx.Where("mt.business = ?", value)
	}
	if value := strings.TrimSpace(filter.GroupName); value != "" {
		tx = tx.Where("mt.group_name = ?", value)
	}
	if value := strings.TrimSpace(filter.InstanceID); value != "" {
		// 支持逗号分隔多实例 ID（告警中心「其他」聚合下钻用）。
		ids := strings.Split(value, ",")
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
		}
		tx = tx.Where("a.instance_id IN ?", ids)
	}
	if err := tx.Order("a.id desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ensureTargetConfigRows 为所有可监控实例补齐监控配置行。
// 实测配置表 0 行 → business/group_name 恒为空 → "按业务筛选/按分组看"整体失效。
func ensureTargetConfigRows() (int, error) {
	if global.App.DB == nil {
		return 0, nil
	}
	type instanceBrief struct {
		InstanceID string
		DbType     string
	}
	var instances []instanceBrief
	if err := global.App.DB.Table("insight_instances").
		Select("instance_id, db_type").
		Where("LOWER(db_type) IN ?", monitorableEngineKeys()).
		Find(&instances).Error; err != nil {
		return 0, err
	}
	if len(instances) == 0 {
		return 0, nil
	}
	instanceIDs := make([]string, 0, len(instances))
	for _, item := range instances {
		instanceIDs = append(instanceIDs, item.InstanceID)
	}
	var existing []string
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Where("instance_id IN ?", instanceIDs).Pluck("instance_id", &existing).Error; err != nil {
		return 0, err
	}
	existingSet := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		existingSet[item] = struct{}{}
	}
	created := 0
	for _, item := range instances {
		if _, ok := existingSet[item.InstanceID]; ok {
			continue
		}
		record := monitorModels.InsightMonitorTargetConfigs{
			Model:          &commonModels.Model{},
			InstanceID:     item.InstanceID,
			Enabled:        true,
			DBType:         commonModels.EnumType(item.DbType),
			ResourceSource: "prometheus",
			EngineSource:   "direct",
			// 新实例的默认周期跟随平台采集频率，避免"配置说 15s、新实例却是 60s"
			ScrapeIntervalSeconds: GetScrapeConfig().IntervalSeconds,
			RetentionHours:        defaultRetentionHours,
			TimeoutSeconds:        defaultTimeoutSeconds,
			RetryCount:            2,
			MetricsTemplate:       defaultTemplateName(item.DbType),
			DeployMode:            "standalone",
			LastCollectStatus:     "never",
			CollectQuality:        QualityNoData,
		}
		if err := global.App.DB.Create(&record).Error; err != nil {
			global.App.Log.Warnf("[monitor] 创建实例 %s 监控配置失败: %v", item.InstanceID, err)
			continue
		}
		created++
	}
	return created, nil
}
