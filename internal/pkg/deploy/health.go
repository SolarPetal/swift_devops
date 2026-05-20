package deploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// HealthOpts 探针参数
type HealthOpts struct {
	URL           string        // 完整 URL，如 http://10.0.0.1:8080/actuator/health
	Timeout       time.Duration // 单次请求超时；默认 3s
	MaxAttempts   int           // 总尝试次数；默认 30
	Interval      time.Duration // 失败后等待；默认 2s
	ExpectStatus  int           // 期望 HTTP 状态码；默认 200
	ExpectKeyword string        // 期望 body 子串；为空跳过 body 校验（如 "UP"）
}

// HealthResult 探针结果
type HealthResult struct {
	OK        bool
	Attempts  int
	LastError string
	LastCode  int
	LastBody  string
}

// BuildHealthURL 拼装 host_ip + port + path → 完整 URL。
//   - p 必须以 / 开头；自动加 http://
func BuildHealthURL(ip string, port int, p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return fmt.Sprintf("http://%s:%d%s", ip, port, p)
}

// Probe 反复发起 HTTP GET，直到成功或达到最大次数。
//   - ctx 取消立刻退出
//   - 期间所有重试都会 sleep Interval
func Probe(ctx context.Context, opts HealthOpts) HealthResult {
	if opts.Timeout == 0 {
		opts.Timeout = 3 * time.Second
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 30
	}
	if opts.Interval == 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.ExpectStatus == 0 {
		opts.ExpectStatus = http.StatusOK
	}

	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: opts.Timeout}).DialContext,
		TLSHandshakeTimeout:   opts.Timeout,
		ResponseHeaderTimeout: opts.Timeout,
		DisableKeepAlives:     true,
	}
	client := &http.Client{Transport: transport, Timeout: opts.Timeout}

	res := HealthResult{}
	for i := 1; i <= opts.MaxAttempts; i++ {
		res.Attempts = i
		if ctx.Err() != nil {
			res.LastError = ctx.Err().Error()
			return res
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
		if err != nil {
			res.LastError = err.Error()
			return res
		}
		resp, err := client.Do(req)
		if err != nil {
			res.LastError = err.Error()
			sleepOrCancel(ctx, opts.Interval)
			continue
		}
		body := readBodyLimited(resp, 4096)
		_ = resp.Body.Close()
		res.LastCode = resp.StatusCode
		res.LastBody = body
		if resp.StatusCode == opts.ExpectStatus &&
			(opts.ExpectKeyword == "" || strings.Contains(body, opts.ExpectKeyword)) {
			res.OK = true
			res.LastError = ""
			return res
		}
		res.LastError = fmt.Sprintf("unexpected status=%d body=%q", resp.StatusCode, truncate(body, 200))
		sleepOrCancel(ctx, opts.Interval)
	}
	return res
}

func sleepOrCancel(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func readBodyLimited(resp *http.Response, max int64) string {
	buf := make([]byte, max)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
