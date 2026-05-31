// Package ws 提供 WebSocket 一次性 ticket 鉴权 + 简单广播 hub。
//
// 鉴权流程：
//  1. 客户端用 JWT 调 POST /api/v1/ws-tickets {subject: "pipeline:5"} 拿到 ticket。
//  2. 客户端 GET /api/v1/ws/...?ticket=<id> 升级；服务端 Consume(ticket, subject) 一次性核销。
//
// 为啥不复用 JWT 走 query：JWT 会进 nginx access log / 浏览器 history，撤销又只能靠 TTL。
// ticket 是 5s TTL + 一次性 + 主题绑定，安全边界小。
package ws

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// DefaultTicketTTL ticket 有效期，留 5s 足够浏览器拿到后立刻发起 WS 升级。
const DefaultTicketTTL = 5 * time.Second

// TicketStore 一次性 ticket 存储。线程安全。
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]ticketEntry
	ttl     time.Duration
	now     func() time.Time
	stopCh  chan struct{}
}

type ticketEntry struct {
	user      string // 持票人
	subject   string // 业务主题，比如 "pipeline:5"，升级时必须严格匹配
	expiresAt time.Time
}

// NewTicketStore 起一个 ticket 仓库；ttl 留 0 走默认。
// 调 Close 停止后台 GC。
func NewTicketStore(ttl time.Duration) *TicketStore {
	if ttl <= 0 {
		ttl = DefaultTicketTTL
	}
	s := &TicketStore{
		tickets: make(map[string]ticketEntry),
		ttl:     ttl,
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	go s.gcLoop()
	return s
}

// Issue 为一个 user + subject 发一张 ticket，返回 ticket 字符串与到期时间。
func (s *TicketStore) Issue(user, subject string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	exp := s.now().Add(s.ttl)
	s.mu.Lock()
	s.tickets[ticket] = ticketEntry{user: user, subject: subject, expiresAt: exp}
	s.mu.Unlock()
	return ticket, exp, nil
}

// Consume 一次性核销 ticket，校验 subject 与未过期，返回持票人。
// 不匹配 subject 视为非法，立即从仓库移除（避免被重放）。
func (s *TicketStore) Consume(ticket, subject string) (user string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, exists := s.tickets[ticket]
	if !exists {
		return "", false
	}
	delete(s.tickets, ticket) // 不论成功失败都一次性
	if s.now().After(e.expiresAt) {
		return "", false
	}
	if e.subject != subject {
		return "", false
	}
	return e.user, true
}

// Close 停 GC。重复调安全。
func (s *TicketStore) Close() {
	select {
	case <-s.stopCh:
		// 已关
	default:
		close(s.stopCh)
	}
}

func (s *TicketStore) gcLoop() {
	t := time.NewTicker(s.ttl) // 每个 ttl 周期清一次足够
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.gc()
		}
	}
}

func (s *TicketStore) gc() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.tickets {
		if now.After(e.expiresAt) {
			delete(s.tickets, k)
		}
	}
}

// Len 当前活跃 ticket 数，测试 / 监控用。
func (s *TicketStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tickets)
}
