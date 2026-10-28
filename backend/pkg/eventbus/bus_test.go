package eventbus

import (
	"sync"
	"testing"
)

func TestPublishDeliversToSubscriber(t *testing.T) {
	bus := New(0) // 0 → DefaultBufferSize
	_, ch := bus.Subscribe("order-1")

	if got := bus.Publish("order-1", "hello"); got != 1 {
		t.Errorf("期望投递给 1 个订阅者，实际 %d", got)
	}
	if msg := <-ch; msg != "hello" {
		t.Errorf("收到的消息不匹配: %q", msg)
	}
}

func TestPublishWithoutSubscriber(t *testing.T) {
	bus := New(8)
	// 没有订阅者时发布不应阻塞、不应报错
	if got := bus.Publish("nobody", "x"); got != 0 {
		t.Errorf("无订阅者时应投递 0 份，实际 %d", got)
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	bus := New(8)
	id, ch := bus.Subscribe("ch")
	bus.Unsubscribe("ch", id)

	// 通道应已关闭：读取立即返回零值 + ok=false
	if _, ok := <-ch; ok {
		t.Error("Unsubscribe 后通道应已关闭")
	}
	// 重复退订是安全的空操作
	bus.Unsubscribe("ch", id)
	// 退订后订阅者计数归零，topic 被清理
	if got := bus.SubscriberCount("ch"); got != 0 {
		t.Errorf("退订后 SubscriberCount 应为 0，实际 %d", got)
	}
}

func TestPublishNeverBlocksWhenBufferFull(t *testing.T) {
	bus := New(2) // 只有 2 个缓冲
	_, ch := bus.Subscribe("slow")

	// 灌 100 条，订阅者一个都不读。Publish 必须全部立即返回，
	// 这条性质是「执行器绝不被消费者拖住」的保证。
	for i := 0; i < 100; i++ {
		bus.Publish("slow", "msg")
	}
	// 缓冲里至少还应有前 2 条
	if len(ch) != 2 {
		t.Errorf("缓冲深度为 2，实际滞留 %d 条", len(ch))
	}
}

func TestMultipleSubscribers(t *testing.T) {
	bus := New(8)
	_, ch1 := bus.Subscribe("topic")
	_, ch2 := bus.Subscribe("topic")

	if got := bus.Publish("topic", "m"); got != 2 {
		t.Errorf("期望投递给 2 个订阅者，实际 %d", got)
	}
	<-ch1
	<-ch2
}

// 并发发布/退订的压测：跑 -race 时验证没有数据竞争与向已关闭通道写入的 panic。
// 这是总线最核心的安全性质——Publish（读锁）与 Unsubscribe（写锁 + close）交错。
func TestConcurrentPublishUnsubscribe(t *testing.T) {
	bus := New(64)
	var wg sync.WaitGroup

	stop := make(chan struct{})
	// 4 个订阅者不断进出场
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					id, ch := bus.Subscribe("hot")
					// 立即又退订，制造 close 与 publish 的最大交错压力
					bus.Unsubscribe("hot", id)
					// 顺手把通道排空，避免假阳性阻塞
					for range ch {
					}
				}
			}
		}()
	}
	// 2 个发布者持续广播
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				bus.Publish("hot", "payload")
			}
		}()
	}
	// 发布者先跑完
	time := make(chan struct{})
	go func() {
		for j := 0; j < 2000; j++ {
			bus.Publish("hot", "x")
		}
		close(time)
	}()
	<-time
	close(stop)
	wg.Wait()
}
