package ws

import (
	"sync"
	"time"
)

// Hub 按 topic 广播消息给订阅者。
//
// 关键设计：
//   - 每个 subscriber 自带一条带缓冲的发送 channel；Hub 不阻塞 Publish。
//   - subscriber 跟不上（channel 满）就直接丢消息——日志流"丢一帧"远比把生产端拖死好。
//   - Subscribe 返回 unsubscribe 函数；调一次幂等。
type Hub struct {
	mu     sync.RWMutex
	topics map[string]map[*subscription]struct{}
}

type subscription struct {
	topic string
	send  chan []byte
}

// NewHub 创建空 hub。
func NewHub() *Hub {
	return &Hub{topics: make(map[string]map[*subscription]struct{})}
}

// Subscribe 订阅 topic，bufSize 是 subscriber 自带 channel 的容量（建议 32~64）。
// 返回值：
//   - recv：只读 channel，收消息
//   - cancel：取消订阅，幂等
func (h *Hub) Subscribe(topic string, bufSize int) (recv <-chan []byte, cancel func()) {
	if bufSize <= 0 {
		bufSize = 32
	}
	sub := &subscription{topic: topic, send: make(chan []byte, bufSize)}
	h.mu.Lock()
	subs, ok := h.topics[topic]
	if !ok {
		subs = make(map[*subscription]struct{})
		h.topics[topic] = subs
	}
	subs[sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			h.mu.Lock()
			if ss, ok := h.topics[topic]; ok {
				delete(ss, sub)
				if len(ss) == 0 {
					delete(h.topics, topic)
				}
			}
			h.mu.Unlock()
			close(sub.send)
		})
	}
	return sub.send, cancel
}

// Publish 向 topic 广播 msg。subscriber channel 满则跳过（避免阻塞生产端）。
// 返回成功推送的订阅者数（含丢弃的）。
func (h *Hub) Publish(topic string, msg []byte) int {
	h.mu.RLock()
	subs := h.topics[topic]
	// 拷一份 slice 出来再放锁，避免 Publish 期间持锁导致 Subscribe 阻塞
	local := make([]*subscription, 0, len(subs))
	for s := range subs {
		local = append(local, s)
	}
	h.mu.RUnlock()
	for _, s := range local {
		select {
		case s.send <- msg:
		default:
			// channel 满，跳过这一帧（subscriber 太慢）
		}
	}
	return len(local)
}

// SubscriberCount 当前 topic 的订阅者数；0 表示该 topic 无人订阅。
func (h *Hub) SubscriberCount(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.topics[topic])
}

// TopicCount 当前 hub 内 topic 总数；测试 / 监控用。
func (h *Hub) TopicCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.topics)
}

// PublishJSON 是 Publish 的便捷版本，调用方传可序列化对象。
// 如果序列化失败返回 0；不污染调用栈。
func (h *Hub) PublishJSON(topic string, v any, marshal func(any) ([]byte, error)) int {
	b, err := marshal(v)
	if err != nil {
		return 0
	}
	return h.Publish(topic, b)
}

// Closed 用静态时间常量构造 deadline。
// 仅在内部默认 ws read/write 超时时使用。
var (
	defaultReadDeadline  = 60 * time.Second
	defaultWriteDeadline = 10 * time.Second
	defaultPingInterval  = 30 * time.Second
)
