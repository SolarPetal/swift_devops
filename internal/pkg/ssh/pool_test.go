package ssh

import (
	"errors"
	"sync"
	"testing"
)

type fakeConn struct {
	id     int
	closed bool
	mu     sync.Mutex
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// newFakeFactory 返回工厂和它发出的连接计数器。
func newFakeFactory() (PoolFactory, func() int) {
	var counter int
	var mu sync.Mutex
	f := func(_ HostTarget) (Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return &fakeConn{id: counter}, nil
	}
	return f, func() int {
		mu.Lock()
		defer mu.Unlock()
		return counter
	}
}

func TestPool_GetCreatesUntilLimit(t *testing.T) {
	factory, count := newFakeFactory()
	p := NewPool(PoolConfig{SizePerHost: 2}, factory)
	defer p.Stop()
	tg := HostTarget{IP: "1.1.1.1", Port: 22, User: "x"}

	c1, err := p.Get(tg)
	if err != nil {
		t.Fatalf("get 1: %v", err)
	}
	c2, err := p.Get(tg)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if c1 == c2 {
		t.Fatal("两次 Get 应得到不同连接")
	}
	if _, err := p.Get(tg); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("第 3 次应 ErrPoolExhausted，得 %v", err)
	}
	if count() != 2 {
		t.Fatalf("factory 应被调 2 次，实际 %d", count())
	}
}

func TestPool_PutReusesIdle(t *testing.T) {
	factory, count := newFakeFactory()
	p := NewPool(PoolConfig{SizePerHost: 2}, factory)
	defer p.Stop()
	tg := HostTarget{IP: "1.1.1.1", Port: 22, User: "x"}

	c, _ := p.Get(tg)
	p.Put(tg, c)
	c2, err := p.Get(tg)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if c2 != c {
		t.Fatal("Put 后再 Get 应复用原连接")
	}
	if count() != 1 {
		t.Fatalf("factory 应只被调 1 次（已复用），实际 %d", count())
	}
}

func TestPool_DiscardClosesAndReleasesSlot(t *testing.T) {
	factory, _ := newFakeFactory()
	p := NewPool(PoolConfig{SizePerHost: 1}, factory)
	defer p.Stop()
	tg := HostTarget{IP: "1.1.1.1", Port: 22, User: "x"}

	c, _ := p.Get(tg)
	fc := c.(*fakeConn)
	p.Discard(tg, c)
	if !fc.isClosed() {
		t.Fatal("Discard 应关闭连接")
	}
	if _, err := p.Get(tg); err != nil {
		t.Fatalf("槽位应释放，得 %v", err)
	}
}

func TestPool_PerHostIsolation(t *testing.T) {
	factory, _ := newFakeFactory()
	p := NewPool(PoolConfig{SizePerHost: 1}, factory)
	defer p.Stop()
	h1 := HostTarget{IP: "1.1.1.1", Port: 22, User: "x"}
	h2 := HostTarget{IP: "2.2.2.2", Port: 22, User: "x"}

	if _, err := p.Get(h1); err != nil {
		t.Fatalf("h1 get: %v", err)
	}
	if _, err := p.Get(h2); err != nil {
		t.Fatalf("h2 get: %v", err)
	}
	if _, err := p.Get(h1); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("h1 应满，得 %v", err)
	}
}

func TestPool_StatsAccurate(t *testing.T) {
	factory, _ := newFakeFactory()
	p := NewPool(PoolConfig{SizePerHost: 3}, factory)
	defer p.Stop()
	tg := HostTarget{IP: "1.1.1.1", Port: 22, User: "x"}

	c1, _ := p.Get(tg)
	_, _ = p.Get(tg) // c2 借走不归还
	p.Put(tg, c1)

	s := p.Stats()
	if s.Hosts != 1 || s.Active != 1 || s.Idle != 1 {
		t.Fatalf("stats 不对: %+v", s)
	}
}

func TestPool_FactoryErrorReleasesSlot(t *testing.T) {
	bad := func(_ HostTarget) (Conn, error) {
		return nil, errors.New("dial failed")
	}
	p := NewPool(PoolConfig{SizePerHost: 1}, bad)
	defer p.Stop()
	tg := HostTarget{IP: "1.1.1.1", Port: 22, User: "x"}

	if _, err := p.Get(tg); err == nil {
		t.Fatal("factory 失败应透传")
	}
	// 槽位必须释放（不然重试就被 Exhausted 卡死）
	good, _ := newFakeFactory()
	p2 := NewPool(PoolConfig{SizePerHost: 1}, good)
	defer p2.Stop()
	if _, err := p.Get(tg); err == nil {
		// 这里 p 仍然是 bad，应继续报错而非 Exhausted
		t.Fatal("应仍报 dial 错")
	} else if errors.Is(err, ErrPoolExhausted) {
		t.Fatal("槽位泄漏：dial 失败后被 Exhausted")
	}
}
