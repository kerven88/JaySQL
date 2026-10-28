package services

// ============================================================================
// 采集链路「降冲击」改造的回归测试
//
// 这一批测试守的不是"功能对不对"，而是**故障态下的行为边界**：
// 目标库已经出问题的时候，采集器不能再往它身上加压力。
// 这类约束一旦被后续改动悄悄放宽，线上表现是"监控把数据库压垮了"，
// 而根因在代码里几乎看不出来 —— 所以必须固化成测试。
// ============================================================================

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/das/dao"
)

// TestReserveCollectNowSlotCooldown 守住「立即采集」的单实例冷却与槽位释放。
//
// 手动采集是唯一不受后台周期约束的入口，用户连点 N 次就是对目标库连发 N 轮采集 SQL。
// 冷却缺失时，一个"实例卡死了快看看"的操作会变成压垮它的最后一根稻草。
func TestReserveCollectNowSlotCooldown(t *testing.T) {
	instanceID := "ds-collect-now-cooldown"
	collectNowLastAt.Delete(instanceID)
	defer collectNowLastAt.Delete(instanceID)

	release, reason := reserveCollectNowSlot(context.Background(), instanceID)
	if reason != "" {
		t.Fatalf("首次立即采集不应被限流：%s", reason)
	}
	if release == nil {
		t.Fatal("抢占成功时必须返回 release 函数，否则并发闸会永久泄漏槽位")
	}
	release()

	_, reason = reserveCollectNowSlot(context.Background(), instanceID)
	if reason == "" {
		t.Fatal("冷却期内的重复点击必须被拒绝")
	}
	if !strings.Contains(reason, "秒后重试") {
		t.Errorf("拒绝原因必须告诉用户还要等多久，实际：%s", reason)
	}

	// 冷却窗口过去之后必须恢复，不能把实例永久锁死
	collectNowLastAt.Store(instanceID, time.Now().Add(-collectNowCooldown-time.Second))
	release2, reason := reserveCollectNowSlot(context.Background(), instanceID)
	if reason != "" {
		t.Fatalf("冷却结束后应恢复采集：%s", reason)
	}
	if release2 != nil {
		release2()
	}
}

// TestReserveCollectNowSlotRespectsContext 客户端断开时不能卡在排队里。
func TestReserveCollectNowSlotRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// 占满全部槽位，逼后续请求进入等待分支
	for i := 0; i < collectNowMaxConcurrent; i++ {
		collectNowSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < collectNowMaxConcurrent; i++ {
			<-collectNowSem
		}
	}()

	if _, reason := reserveCollectNowSlot(ctx, "ds-collect-now-cancel"); reason == "" {
		t.Fatal("ctx 已取消时不应继续排队等待，应立即返回")
	}
}

// TestRateCacheEvictsStaleRecords 守住速率缓存的过期淘汰。
//
// 不淘汰的后果很隐蔽：实例下线后它的计数器快照永远留在内存里，
// 同一个 instance_id 重新接入时会拿几周前的旧值做差分，
// 表现是"刚接进来的实例速率低得离谱，过一轮才正常"。
func TestRateCacheEvictsStaleRecords(t *testing.T) {
	cache := &rateCacheStore{records: map[string]counterSnapshot{}}
	now := time.Now()
	cache.records["fresh"] = counterSnapshot{At: now, Counters: map[string]float64{"qps_total": 100}}
	cache.records["stale"] = counterSnapshot{
		At:       now.Add(-rateCacheMaxIdle - time.Minute),
		Counters: map[string]float64{"qps_total": 1},
	}

	cache.sweepLocked(now)
	if _, ok := cache.records["fresh"]; !ok {
		t.Error("刚更新的记录不应被淘汰")
	}
	if _, ok := cache.records["stale"]; ok {
		t.Error("超过 rateCacheMaxIdle 未更新的记录必须淘汰")
	}
}

// TestRateCacheSweepThrottled 淘汰扫描要摊薄，不能每次 compute 都做一次 O(n) 遍历。
func TestRateCacheSweepThrottled(t *testing.T) {
	cache := &rateCacheStore{records: map[string]counterSnapshot{}}
	now := time.Now()
	cache.lastSweep = now
	cache.records["stale"] = counterSnapshot{
		At:       now.Add(-rateCacheMaxIdle - time.Minute),
		Counters: map[string]float64{"qps_total": 1},
	}

	cache.sweepLocked(now.Add(time.Second))
	if _, ok := cache.records["stale"]; !ok {
		t.Error("未到扫描间隔时不应执行淘汰")
	}
}

// TestResourceCollectCtxHasIndependentBudget 资源层必须有自己的超时预算。
//
// 与引擎层共用 ctx 时，16 条 PromQL 只能吃引擎段剩下的时间，
// 超时后集体显示"未返回该主机的时间序列"——把"我们这边超时了"伪装成"exporter 没数据"。
func TestResourceCollectCtxHasIndependentBudget(t *testing.T) {
	ctx, cancel := newResourceCollectCtx(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("资源层必须有独立的超时预算，不能是无限期 ctx")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > resourceCollectBudget {
		t.Errorf("预算应落在 (0, %s] 之间，实际剩余 %s", resourceCollectBudget, remaining)
	}

	// 父 ctx 取消要能传递下去：HTTP 请求断开时不能留下空转的 PromQL。
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	child, cancelChild := newResourceCollectCtx(parent)
	defer cancelChild()
	if child.Err() == nil {
		t.Error("父 ctx 已取消时，派生的资源层 ctx 必须同步取消")
	}
}

// TestClickHouseSessionNilSafe CK 会话的 nil 安全与幂等关闭。
//
// 放在 monitor 包而不是 dao 包：dao 包的测试有会直连真实测试库的初始化逻辑，
// 本机跑不起来（与 mysql_session_test.go 同样的处理方式）。
func TestClickHouseSessionNilSafe(t *testing.T) {
	var session *dao.ClickHouseSession
	if _, _, err := session.Query("SELECT 1"); err == nil {
		t.Error("nil 会话执行查询必须返回错误，不能 panic")
	}
	if err := session.Close(); err != nil {
		t.Errorf("nil 会话 Close 必须幂等返回 nil，实际 %v", err)
	}
	if session.BrokenErr() != nil {
		t.Error("nil 会话不应报告 broken")
	}
}

// TestMonitorInstanceRowPasswordNotSerialized 守住加密口令不被序列化出去。
//
// monitorInstanceRow 会被整体塞进若干响应；口令虽是密文，但拿到 secret_key 就能还原，
// 密文泄露同样是泄露。这条测试让"忘了加 json:\"-\""变成测试失败而不是线上事故。
func TestMonitorInstanceRowPasswordNotSerialized(t *testing.T) {
	field, ok := reflect.ValueOf(monitorInstanceRow{}).Type().FieldByName("Password")
	if !ok {
		t.Fatal("monitorInstanceRow 缺少 Password 字段")
	}
	if tag := field.Tag.Get("json"); tag != "-" {
		t.Fatalf("monitorInstanceRow.Password 必须标记 json:\"-\"，实际为 %q", tag)
	}
}
