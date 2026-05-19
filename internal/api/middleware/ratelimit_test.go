package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"swift-devops/internal/api/middleware"
)

func TestRateLimit_AllowUntilMax(t *testing.T) {
	rl := middleware.NewRateLimiter(time.Second, 3)
	defer rl.Stop()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LoginRateLimit(rl))
	r.POST("/login", func(c *gin.Context) { c.Status(200) })

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/login", nil))
		if w.Code != 200 {
			t.Fatalf("第 %d 次应放行，得 %d", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/login", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("第 4 次应 429，得 %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("应带 Retry-After 头")
	}
}

func TestRateLimit_WindowReset(t *testing.T) {
	rl := middleware.NewRateLimiter(120*time.Millisecond, 1)
	defer rl.Stop()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LoginRateLimit(rl))
	r.POST("/login", func(c *gin.Context) { c.Status(200) })

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest("POST", "/login", nil))
	if w1.Code != 200 {
		t.Fatalf("首次应放行，得 %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("POST", "/login", nil))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("第二次应 429，得 %d", w2.Code)
	}

	time.Sleep(150 * time.Millisecond)

	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequest("POST", "/login", nil))
	if w3.Code != 200 {
		t.Fatalf("窗口重置后应放行，得 %d", w3.Code)
	}
}

func TestRateLimit_PerKeyIsolation(t *testing.T) {
	rl := middleware.NewRateLimiter(time.Second, 1)
	defer rl.Stop()

	if ok, _ := rl.Allow("a"); !ok {
		t.Fatal("a 首次应放行")
	}
	if ok, _ := rl.Allow("a"); ok {
		t.Fatal("a 第二次应拒绝")
	}
	if ok, _ := rl.Allow("b"); !ok {
		t.Fatal("b 首次应放行（与 a 独立）")
	}
}
