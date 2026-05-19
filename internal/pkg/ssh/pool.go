package ssh

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Conn 是连接池能管理的连接类型。*Client 实现该接口。
type Conn interface {
	io.Closer
}

// PoolFactory 按需建立新连接。生产代码注入 Dial 的包装版本，测试时可注入 mock。
type PoolFactory func(target HostTarget) (Conn, error)

// PoolConfig 连接池配置
type PoolConfig struct {
	SizePerHost int
	IdleTimeout time.Duration
}

// ErrPoolExhausted 同主机活动连接数达到 SizePerHost 上限。
var ErrPoolExhausted = errors.New("pool exhausted")

// Pool 多主机 SSH 连接池
type Pool struct {
	cfg     PoolConfig
	factory PoolFactory

	mu    sync.Mutex
	hosts map[string]*hostBucket

	stop chan struct{}
}

type pooledConn struct {
	c       Conn
	lastUse time.Time
}

type hostBucket struct {
	mu     sync.Mutex
	idle   []*pooledConn
	active int
	target HostTarget
}

// NewPool 构造连接池。启动 reaper goroutine 定期回收空闲连接。
func NewPool(cfg PoolConfig, factory PoolFactory) *Pool {
	if cfg.SizePerHost <= 0 {
		cfg.SizePerHost = 2
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	p := &Pool{
		cfg:     cfg,
		factory: factory,
		hosts:   map[string]*hostBucket{},
		stop:    make(chan struct{}),
	}
	go p.reaper()
	return p
}

// Stop 关闭池：停止 reaper、关掉所有空闲连接。
// 已经借出未归还的连接不影响。
func (p *Pool) Stop() {
	close(p.stop)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.hosts {
		b.mu.Lock()
		for _, pc := range b.idle {
			_ = pc.c.Close()
		}
		b.idle = nil
		b.mu.Unlock()
	}
}

func bucketKey(t HostTarget) string {
	return fmt.Sprintf("%s:%d/%s", t.IP, t.Port, t.User)
}

// Get 借连接。若没有空闲且未到上限，调 factory 新建。
// 用完必须 Put（健康）或 Discard（损坏）。
func (p *Pool) Get(target HostTarget) (Conn, error) {
	p.mu.Lock()
	b, ok := p.hosts[bucketKey(target)]
	if !ok {
		b = &hostBucket{target: target}
		p.hosts[bucketKey(target)] = b
	}
	p.mu.Unlock()

	b.mu.Lock()
	if len(b.idle) > 0 {
		n := len(b.idle) - 1
		pc := b.idle[n]
		b.idle = b.idle[:n]
		b.active++
		b.mu.Unlock()
		return pc.c, nil
	}
	if b.active >= p.cfg.SizePerHost {
		b.mu.Unlock()
		return nil, ErrPoolExhausted
	}
	b.active++
	b.mu.Unlock()

	c, err := p.factory(target)
	if err != nil {
		b.mu.Lock()
		b.active--
		b.mu.Unlock()
		return nil, err
	}
	return c, nil
}

// Put 归还健康连接进入空闲池。
func (p *Pool) Put(target HostTarget, c Conn) {
	b := p.bucketOrClose(target, c)
	if b == nil {
		return
	}
	b.mu.Lock()
	b.active--
	b.idle = append(b.idle, &pooledConn{c: c, lastUse: time.Now()})
	b.mu.Unlock()
}

// Discard 归还不可复用的连接（已断、出错等），直接 Close。
func (p *Pool) Discard(target HostTarget, c Conn) {
	b := p.bucketOrClose(target, c)
	if b == nil {
		return
	}
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	_ = c.Close()
}

func (p *Pool) bucketOrClose(target HostTarget, c Conn) *hostBucket {
	p.mu.Lock()
	b, ok := p.hosts[bucketKey(target)]
	p.mu.Unlock()
	if !ok {
		_ = c.Close()
		return nil
	}
	return b
}

// Stats 用于运维观察
type Stats struct {
	Hosts  int
	Idle   int
	Active int
}

func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := Stats{Hosts: len(p.hosts)}
	for _, b := range p.hosts {
		b.mu.Lock()
		s.Idle += len(b.idle)
		s.Active += b.active
		b.mu.Unlock()
	}
	return s
}

func (p *Pool) reaper() {
	interval := p.cfg.IdleTimeout / 2
	if interval < time.Minute {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.sweep()
		}
	}
}

func (p *Pool) sweep() {
	cutoff := time.Now().Add(-p.cfg.IdleTimeout)
	p.mu.Lock()
	buckets := make([]*hostBucket, 0, len(p.hosts))
	for _, b := range p.hosts {
		buckets = append(buckets, b)
	}
	p.mu.Unlock()
	for _, b := range buckets {
		b.mu.Lock()
		keep := b.idle[:0]
		for _, pc := range b.idle {
			if pc.lastUse.Before(cutoff) {
				_ = pc.c.Close()
			} else {
				keep = append(keep, pc)
			}
		}
		b.idle = keep
		b.mu.Unlock()
	}
}
