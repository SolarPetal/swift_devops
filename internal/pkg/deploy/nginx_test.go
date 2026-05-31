package deploy

import (
	"strings"
	"testing"
)

func TestUpstreamConfPath(t *testing.T) {
	got := UpstreamConfPath("user-service")
	want := "/etc/nginx/conf.d/swift-devops-user-service.conf"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestRenderUpstreamConf_OK(t *testing.T) {
	out, err := RenderUpstreamConf(NginxUpstream{
		AppCode:      "user-service",
		UpstreamName: "user-svc-backend",
		Servers: []NginxBackend{
			{IP: "10.0.0.1", Port: 8080},
			{IP: "10.0.0.2", Port: 8080},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"Managed by swift-devops",
		"App: user-service",
		"upstream user-svc-backend {",
		"least_conn;",
		"server 10.0.0.1:8080;",
		"server 10.0.0.2:8080;",
		"}\n",
	}
	for _, kw := range mustContain {
		if !strings.Contains(out, kw) {
			t.Errorf("缺少片段 %q\n---\n%s", kw, out)
		}
	}
}

func TestRenderUpstreamConf_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		u    NginxUpstream
	}{
		{"upstream name 空", NginxUpstream{AppCode: "a", Servers: []NginxBackend{{IP: "1.1.1.1", Port: 80}}}},
		{"servers 空", NginxUpstream{AppCode: "a", UpstreamName: "n", Servers: nil}},
		{"server.ip 空", NginxUpstream{
			AppCode: "a", UpstreamName: "n",
			Servers: []NginxBackend{{IP: "", Port: 80}},
		}},
		{"server.port 0", NginxUpstream{
			AppCode: "a", UpstreamName: "n",
			Servers: []NginxBackend{{IP: "1.1.1.1", Port: 0}},
		}},
		{"server.port 超界", NginxUpstream{
			AppCode: "a", UpstreamName: "n",
			Servers: []NginxBackend{{IP: "1.1.1.1", Port: 70000}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := RenderUpstreamConf(c.u); err == nil {
				t.Fatal("应报错")
			}
		})
	}
}

// ShellQuote 把含特殊字符的路径包成 shell 单引号安全格式
func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/tmp/x":        `'/tmp/x'`,
		"/tmp/a b":      `'/tmp/a b'`,
		"/tmp/has'apos": `'/tmp/has'"'"'apos'`,
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q)=%q, want %q", in, got, want)
		}
	}
}
