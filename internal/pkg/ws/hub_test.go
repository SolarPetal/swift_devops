package ws_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wspkg "swift-devops/internal/pkg/ws"
)

func TestHub_SubscribeAndPublish(t *testing.T) {
	h := wspkg.NewHub()
	recv1, cancel1 := h.Subscribe("topic-a", 8)
	defer cancel1()
	recv2, cancel2 := h.Subscribe("topic-a", 8)
	defer cancel2()
	recv3, cancel3 := h.Subscribe("topic-b", 8)
	defer cancel3()

	if h.SubscriberCount("topic-a") != 2 {
		t.Fatalf("topic-a 订阅数: %d", h.SubscriberCount("topic-a"))
	}
	if h.SubscriberCount("topic-b") != 1 {
		t.Fatalf("topic-b 订阅数: %d", h.SubscriberCount("topic-b"))
	}

	n := h.Publish("topic-a", []byte("hello"))
	if n != 2 {
		t.Fatalf("发到 topic-a 的人数应为 2，得 %d", n)
	}

	for i, ch := range []<-chan []byte{recv1, recv2} {
		select {
		case msg := <-ch:
			if string(msg) != "hello" {
				t.Errorf("sub#%d 收到 %q", i, msg)
			}
		case <-time.After(time.Second):
			t.Errorf("sub#%d 没收到消息", i)
		}
	}

	// topic-b 不应收到 topic-a 的消息
	select {
	case msg := <-recv3:
		t.Errorf("topic-b 不应收到 %q", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHub_CancelRemovesSubscriber(t *testing.T) {
	h := wspkg.NewHub()
	_, cancel := h.Subscribe("t", 4)
	if h.SubscriberCount("t") != 1 {
		t.Fatalf("订阅数: %d", h.SubscriberCount("t"))
	}
	cancel()
	if h.SubscriberCount("t") != 0 {
		t.Fatalf("取消后订阅数应为 0：%d", h.SubscriberCount("t"))
	}
	if h.TopicCount() != 0 {
		t.Fatalf("topic 已空时应清掉条目：%d", h.TopicCount())
	}
	// 二次取消应安全（once）
	cancel()
}

func TestHub_FullBufferDropsMessage(t *testing.T) {
	h := wspkg.NewHub()
	// buffer 仅 1，发 5 条应只有 1 条留下，其余被丢
	recv, cancel := h.Subscribe("t", 1)
	defer cancel()

	for i := 0; i < 5; i++ {
		h.Publish("t", []byte{byte('0' + i)})
	}

	got := 0
	for {
		select {
		case <-recv:
			got++
		case <-time.After(50 * time.Millisecond):
			goto done
		}
	}
done:
	if got != 1 {
		t.Fatalf("缓冲满应丢消息，最多收 1 条，实收 %d", got)
	}
}

func TestHub_ConcurrentSubscribePublishCancel(t *testing.T) {
	h := wspkg.NewHub()
	const subs = 50
	const msgs = 100
	var recvCount int64

	wg := sync.WaitGroup{}
	cancels := make([]func(), 0, subs)
	mu := sync.Mutex{}

	for i := 0; i < subs; i++ {
		recv, cancel := h.Subscribe("t", 256)
		mu.Lock()
		cancels = append(cancels, cancel)
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range recv {
				atomic.AddInt64(&recvCount, 1)
			}
		}()
	}

	for i := 0; i < msgs; i++ {
		h.Publish("t", []byte("x"))
	}
	// 收一会儿
	time.Sleep(100 * time.Millisecond)
	for _, c := range cancels {
		c()
	}
	wg.Wait()

	if recvCount == 0 {
		t.Fatal("应收到一些消息")
	}
	if recvCount > int64(subs*msgs) {
		t.Fatalf("收到比发出还多？发 %d 收 %d", subs*msgs, recvCount)
	}
}

func TestHub_PublishToUnknownTopic(t *testing.T) {
	h := wspkg.NewHub()
	if n := h.Publish("ghost", []byte("x")); n != 0 {
		t.Fatalf("没人订阅时应返回 0，得 %d", n)
	}
}
