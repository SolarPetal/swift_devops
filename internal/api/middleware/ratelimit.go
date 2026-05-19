package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter 是 per-key 的固定窗口计数器。
// 适合登录这种低频接口，简单、内存可控。
// 高频场景可换 token bucket（golang.org/x/time/rate）。
type RateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	counts map[string]*windowCounter
	stop   chan struct{}
}

type windowCounter struct {
	n       int
	resetAt time.Time
}

// NewRateLimiter 构造限流器。window 是窗口长度，max 是窗口内最大次数。
// 内部启动 gc goroutine 定期回收过期 entry。
func NewRateLimiter(window time.Duration, max int) *RateLimiter {
	rl := &RateLimiter{
		window: window,
		max:    max,
		counts: make(map[string]*windowCounter),
		stop:   make(chan struct{}),
	}
	go rl.gc()
	return rl
}

// Stop 终止 gc goroutine。一般用于测试或优雅关闭。
func (rl *RateLimiter) Stop() {
	close(rl.stop)
}

// Allow 检查 key 是否在限额内。
// 返回 ok=true 时已经计入；ok=false 时附带剩余 retryAfter 时长。
func (rl *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	c, exists := rl.counts[key]
	if !exists || now.After(c.resetAt) {
		rl.counts[key] = &windowCounter{n: 1, resetAt: now.Add(rl.window)}
		return true, 0
	}
	if c.n >= rl.max {
		return false, c.resetAt.Sub(now)
	}
	c.n++
	return true, 0
}

func (rl *RateLimiter) gc() {
	t := time.NewTicker(rl.window)
	defer t.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-t.C:
			rl.mu.Lock()
			now := time.Now()
			for k, c := range rl.counts {
				if now.After(c.resetAt) {
					delete(rl.counts, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// LoginRateLimit 登录接口限流中间件。按 IP 计数。
// 触发时返回 429 + Retry-After 头 + 统一错误响应。
//
// 提示：按用户名维度的限流应在 handler 层 augment，
//
//	因为 username 来自 request body，中间件层尚未 bind。
func LoginRateLimit(rl *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, retry := rl.Allow("login:" + c.ClientIP())
		if !ok {
			secs := int(retry.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":    "TOO_MANY_REQUESTS",
					"message": "请求过于频繁，请稍后再试",
				},
			})
			return
		}
		c.Next()
	}
}
