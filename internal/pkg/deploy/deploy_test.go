package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderUnit_Basic(t *testing.T) {
	out, err := RenderUnit(AppSpec{
		AppCode:    "user-svc",
		DeployPath: "/opt/apps/user-svc",
		JvmArgs:    "-Xms512m -Xmx512m",
		Port:       8080,
		EnvVars:    map[string]string{"SPRING_PROFILES_ACTIVE": "prod"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wants := []string{
		"Description=swift-devops managed: user-svc",
		"WorkingDirectory=/opt/apps/user-svc",
		`Environment="SPRING_PROFILES_ACTIVE=prod"`,
		"ExecStart=/usr/bin/java -Xms512m -Xmx512m -jar /opt/apps/user-svc/app.jar --server.port=8080",
		"Restart=on-failure",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("缺少：%s\n---\n%s", w, out)
		}
	}
	// User 留空 → 不应出现 User= 行
	if strings.Contains(out, "User=") {
		t.Errorf("User 留空时不应渲染 User= 行")
	}
}

func TestRenderUnit_WithUserAndMultipleEnvs(t *testing.T) {
	out, err := RenderUnit(AppSpec{
		AppCode:    "app",
		DeployPath: "/opt/app",
		Port:       9000,
		User:       "deploy",
		EnvVars:    map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "User=deploy") {
		t.Error("User= 行缺失")
	}
	// 顺序稳定（按 key 排序）：A 在 B 前
	ia := strings.Index(out, `Environment="A=1"`)
	ib := strings.Index(out, `Environment="B=2"`)
	if ia < 0 || ib < 0 {
		t.Fatal("env 行缺失")
	}
	if ia > ib {
		t.Errorf("env 顺序不稳定")
	}
}

func TestParseEnvVarsJSON(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		count int
	}{
		{"", true, 0},
		{"  ", true, 0},
		{`{}`, true, 0},
		{`{"K":"v"}`, true, 1},
		{`not-json`, false, 0},
		{`["x"]`, false, 0},
	}
	for _, c := range cases {
		m, err := ParseEnvVarsJSON(c.in)
		if c.ok && err != nil {
			t.Errorf("%q 应通过：%v", c.in, err)
			continue
		}
		if !c.ok {
			if err == nil {
				t.Errorf("%q 应失败", c.in)
			}
			continue
		}
		if len(m) != c.count {
			t.Errorf("%q count=%d 期望 %d", c.in, len(m), c.count)
		}
	}
}

func TestUnitName_AndPaths(t *testing.T) {
	s := AppSpec{AppCode: "order-svc", DeployPath: "/srv/order"}
	if s.UnitName() != "devops-order-svc.service" {
		t.Errorf("UnitName: %s", s.UnitName())
	}
	if s.UnitPath() != "/etc/systemd/system/devops-order-svc.service" {
		t.Errorf("UnitPath: %s", s.UnitPath())
	}
	if s.JarPath() != "/srv/order/app.jar" {
		t.Errorf("JarPath: %s", s.JarPath())
	}
	s.JarFileName = "service.jar"
	if s.JarPath() != "/srv/order/service.jar" {
		t.Errorf("JarPath custom: %s", s.JarPath())
	}
}

func TestBuildHealthURL(t *testing.T) {
	cases := []struct {
		ip, p string
		port  int
		want  string
	}{
		{"10.0.0.1", "/actuator/health", 8080, "http://10.0.0.1:8080/actuator/health"},
		{"127.0.0.1", "actuator/health", 9000, "http://127.0.0.1:9000/actuator/health"},
	}
	for _, c := range cases {
		got := BuildHealthURL(c.ip, c.port, c.p)
		if got != c.want {
			t.Errorf("got %s want %s", got, c.want)
		}
	}
}

func TestProbe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), HealthOpts{
		URL: srv.URL + "/health", MaxAttempts: 1, ExpectKeyword: "UP",
	})
	if !res.OK {
		t.Fatalf("应成功：%+v", res)
	}
}

func TestProbe_WrongStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	res := Probe(context.Background(), HealthOpts{
		URL: srv.URL + "/h", MaxAttempts: 2, Interval: 10 * time.Millisecond,
	})
	if res.OK {
		t.Fatal("503 不应成功")
	}
	if res.LastCode != 503 {
		t.Errorf("LastCode=%d", res.LastCode)
	}
	if res.Attempts != 2 {
		t.Errorf("attempts=%d，应重试到 MaxAttempts", res.Attempts)
	}
}

func TestProbe_KeywordMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"DOWN"}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), HealthOpts{
		URL: srv.URL, MaxAttempts: 1, ExpectKeyword: "UP",
	})
	if res.OK {
		t.Fatal("DOWN 不应通过")
	}
}

func TestProbe_RetriesUntilOK(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&n, 1)
		if c < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("UP"))
	}))
	defer srv.Close()
	res := Probe(context.Background(), HealthOpts{
		URL: srv.URL, MaxAttempts: 5, Interval: 10 * time.Millisecond, ExpectKeyword: "UP",
	})
	if !res.OK {
		t.Fatalf("应在第 3 次成功：%+v", res)
	}
	if res.Attempts != 3 {
		t.Errorf("attempts=%d 期望 3", res.Attempts)
	}
}

func TestProbe_CtxCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	res := Probe(ctx, HealthOpts{
		URL: srv.URL, MaxAttempts: 10, Interval: 10 * time.Millisecond,
	})
	if res.OK {
		t.Fatal("ctx 取消不应成功")
	}
}

func TestLocalFileMD5(t *testing.T) {
	// 用 dispatcher.go 同包，直接调
	f, err := tempFileWith("hello")
	if err != nil {
		t.Fatalf("tmp: %v", err)
	}
	defer cleanupTemp(f)
	got, err := LocalFileMD5(f)
	if err != nil {
		t.Fatalf("md5: %v", err)
	}
	// md5("hello") = 5d41402abc4b2a76b9719d911017c592
	if got != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("md5 got %s", got)
	}
}
