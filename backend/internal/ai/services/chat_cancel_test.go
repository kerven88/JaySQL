package services

import (
	"context"
	"testing"
	"time"
)

// 等待 ctx 结束；超时返回 false（用于断言「确实没被取消」的反向场景）
func waitCtxDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

// 发起人取消：ctx 立刻结束
func TestRegisterChatRun_CancelByOwner(t *testing.T) {
	ctx, release := RegisterChatRun(context.Background(), "runTestOwner0001", "hongjie")
	defer release()

	if ctx.Err() != nil {
		t.Fatalf("刚注册的 ctx 不该已结束: %v", ctx.Err())
	}
	if !CancelChatRun("runTestOwner0001", "hongjie") {
		t.Fatal("发起人取消应返回 true")
	}
	if !waitCtxDone(ctx, time.Second) {
		t.Fatal("取消后 ctx 未结束")
	}
}

// 跨用户取消必须被拒绝：同权限不等于可以掐断别人的对话
func TestCancelChatRun_RejectOtherUser(t *testing.T) {
	ctx, release := RegisterChatRun(context.Background(), "runTestOther0001", "hongjie")
	defer release()

	if CancelChatRun("runTestOther0001", "someone-else") {
		t.Fatal("非发起人取消不应返回 true")
	}
	if waitCtxDone(ctx, 80*time.Millisecond) {
		t.Fatal("非发起人取消后 ctx 不该结束")
	}
	// 发起人自己仍能取消
	if !CancelChatRun("runTestOther0001", "hongjie") {
		t.Fatal("发起人自己取消应返回 true")
	}
}

// 不存在 / 空 run_id：一律 false，不 panic
func TestCancelChatRun_UnknownOrEmpty(t *testing.T) {
	if CancelChatRun("runNotExist00001", "hongjie") {
		t.Fatal("不存在的 run 应返回 false")
	}
	if CancelChatRun("", "hongjie") {
		t.Fatal("空 run_id 应返回 false")
	}
}

// 释放后注册项必须清干净（否则长跑进程会持续泄漏）
func TestRegisterChatRun_ReleaseRemovesEntry(t *testing.T) {
	before := ActiveChatRuns()

	_, release := RegisterChatRun(context.Background(), "runTestRelease001", "hongjie")
	if got := ActiveChatRuns(); got != before+1 {
		t.Fatalf("注册后应有 %d 条，实际 %d", before+1, got)
	}

	release()
	if got := ActiveChatRuns(); got != before {
		t.Fatalf("释放后应回到 %d 条，实际 %d", before, got)
	}
	if CancelChatRun("runTestRelease001", "hongjie") {
		t.Fatal("已释放的 run 不该还能取消")
	}
}

// 重复登记同一个 run_id：旧的那次必须被掐掉，否则它会变成「取消不掉」的后台孤儿
func TestRegisterChatRun_DuplicateCancelsOld(t *testing.T) {
	oldCtx, releaseOld := RegisterChatRun(context.Background(), "runTestDup0000001", "hongjie")
	defer releaseOld()

	newCtx, releaseNew := RegisterChatRun(context.Background(), "runTestDup0000001", "hongjie")
	defer releaseNew()

	if !waitCtxDone(oldCtx, time.Second) {
		t.Fatal("重复登记时旧的 ctx 应被取消")
	}
	if newCtx.Err() != nil {
		t.Fatal("新登记的 ctx 不该受影响")
	}
}

// 旧句柄延迟释放（旧请求收尾晚于新请求登记）时，不能把新注册项一起删掉
func TestRegisterChatRun_StaleReleaseKeepsNewEntry(t *testing.T) {
	_, releaseOld := RegisterChatRun(context.Background(), "runTestStale0001", "hongjie")
	newCtx, releaseNew := RegisterChatRun(context.Background(), "runTestStale0001", "hongjie")
	defer releaseNew()

	releaseOld()

	if !CancelChatRun("runTestStale0001", "hongjie") {
		t.Fatal("旧句柄的延迟释放不该把新注册项删掉")
	}
	if !waitCtxDone(newCtx, time.Second) {
		t.Fatal("新注册项应仍可被取消")
	}
}

// 空 run_id：不产生注册项（等价于老客户端，只依赖连接断开）
func TestRegisterChatRun_EmptyRunIDSkipsRegistry(t *testing.T) {
	before := ActiveChatRuns()
	_, release := RegisterChatRun(context.Background(), "", "hongjie")
	defer release()

	if got := ActiveChatRuns(); got != before {
		t.Fatalf("空 run_id 不该产生注册项，实际 %d 条（期望 %d）", got, before)
	}
}

// 请求 ctx 取消（客户端断开）时派生的 ctx 同样结束 ——
// 这是「双通道」的另一条，也是 ChatCompletion 放弃 context.Background() 后依赖的那条
func TestRegisterChatRun_ParentCancelPropagates(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, release := RegisterChatRun(parent, "runTestParent001", "hongjie")
	defer release()

	cancelParent()
	if !waitCtxDone(ctx, time.Second) {
		t.Fatal("父 ctx 取消后子 ctx 应结束")
	}
}
