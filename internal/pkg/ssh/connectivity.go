package ssh

import (
	"context"
	"time"
)

// TestResult 连通性测试结果
type TestResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	HostKey   string `json:"host_key,omitempty"` // TOFU 首次时返回，调用方应落库
	Error     string `json:"error,omitempty"`
}

// TestConnectivity 拨号 + 简单 ping，返回延迟和（首次时）学到的 host key。
// 用于 Host 录入页的"测试连接"按钮。
func TestConnectivity(target HostTarget, auth AuthMethod, opts DialOptions) TestResult {
	start := time.Now()
	c, err := Dial(target, auth, opts)
	if err != nil {
		return TestResult{Error: err.Error()}
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return TestResult{Error: "ping failed: " + err.Error()}
	}
	return TestResult{
		OK:        true,
		LatencyMs: time.Since(start).Milliseconds(),
		HostKey:   c.LearnedHostKey(),
	}
}
