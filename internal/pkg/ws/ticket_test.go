package ws_test

import (
	"sync"
	"testing"
	"time"

	wspkg "swift-devops/internal/pkg/ws"
)

func TestTicketStore_IssueAndConsume(t *testing.T) {
	s := wspkg.NewTicketStore(2 * time.Second)
	defer s.Close()

	ticket, exp, err := s.Issue("alice", "pipeline:5")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ticket == "" {
		t.Fatal("ticket 不应为空")
	}
	if time.Until(exp) <= 0 || time.Until(exp) > 2*time.Second {
		t.Fatalf("过期时间不对: %v", exp)
	}
	if s.Len() != 1 {
		t.Fatalf("Len: %d", s.Len())
	}

	user, ok := s.Consume(ticket, "pipeline:5")
	if !ok || user != "alice" {
		t.Fatalf("consume: ok=%v user=%q", ok, user)
	}
	if s.Len() != 0 {
		t.Fatalf("consume 后 Len 应为 0：%d", s.Len())
	}

	// 二次 consume 必须失败（一次性）
	if _, ok := s.Consume(ticket, "pipeline:5"); ok {
		t.Fatal("二次消费应失败")
	}
}

func TestTicketStore_SubjectMismatch(t *testing.T) {
	s := wspkg.NewTicketStore(time.Second)
	defer s.Close()
	ticket, _, _ := s.Issue("bob", "pipeline:5")
	if _, ok := s.Consume(ticket, "pipeline:9"); ok {
		t.Fatal("subject 不匹配应失败")
	}
	// 不匹配的 ticket 已被移除，第二次拿正确 subject 也失败（防重放）
	if _, ok := s.Consume(ticket, "pipeline:5"); ok {
		t.Fatal("subject 不匹配后 ticket 应已失效")
	}
}

func TestTicketStore_Expired(t *testing.T) {
	s := wspkg.NewTicketStore(50 * time.Millisecond)
	defer s.Close()
	ticket, _, _ := s.Issue("carol", "metrics:1")
	time.Sleep(80 * time.Millisecond)
	if _, ok := s.Consume(ticket, "metrics:1"); ok {
		t.Fatal("过期 ticket 应失败")
	}
}

func TestTicketStore_UnknownTicket(t *testing.T) {
	s := wspkg.NewTicketStore(time.Second)
	defer s.Close()
	if _, ok := s.Consume("not-exists", "any"); ok {
		t.Fatal("未知 ticket 应失败")
	}
}

func TestTicketStore_ConcurrentIssueConsume(t *testing.T) {
	s := wspkg.NewTicketStore(2 * time.Second)
	defer s.Close()

	const n = 200
	tickets := make([]string, n)
	for i := 0; i < n; i++ {
		tk, _, err := s.Issue("u", "p:1")
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		tickets[i] = tk
	}
	if s.Len() != n {
		t.Fatalf("Len 应为 %d，得 %d", n, s.Len())
	}

	// 并发消费：每张票只允许成功一次
	var success int64
	var mu sync.Mutex
	wg := sync.WaitGroup{}
	for _, tk := range tickets {
		// 故意每张票两个 consumer 抢
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(t string) {
				defer wg.Done()
				if _, ok := s.Consume(t, "p:1"); ok {
					mu.Lock()
					success++
					mu.Unlock()
				}
			}(tk)
		}
	}
	wg.Wait()
	if success != int64(n) {
		t.Fatalf("应成功 %d 次（每票一次），得 %d", n, success)
	}
}
