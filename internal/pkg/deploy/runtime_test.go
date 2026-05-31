package deploy

import (
	"strings"
	"testing"
)

// TestRenderStartScript_Systemd 验证 systemd 模式下 unit 文件保持原行为。
// 不调用 nohup runtime，只对 RenderUnit 做回归（Sprint X.10 重构后行为应零变化）。
func TestRenderStartScript_BasicShape(t *testing.T) {
	spec := AppSpec{
		AppCode:    "demo-app",
		DeployPath: "/opt/demo",
		JvmArgs:    "-Xms256m -Xmx512m",
		Port:       8080,
		EnvVars:    map[string]string{"SPRING_PROFILES_ACTIVE": "prod"},
		JavaPath:   "/usr/local/jdk-17/bin/java",
	}
	got := renderStartScript(spec)

	// 必含关键字段
	musts := []string{
		"#!/bin/bash",
		"cd '/opt/demo'",
		"mkdir -p logs",
		"export SPRING_PROFILES_ACTIVE='prod'",
		"/usr/local/jdk-17/bin/java -Xms256m -Xmx512m -jar '/opt/demo/app.jar' --server.port=8080",
		"'/opt/demo/logs/stdout.log'",
		"'/opt/demo/app.pid'",
		"echo $! >",
	}
	for _, m := range musts {
		if !strings.Contains(got, m) {
			t.Errorf("renderStartScript missing %q\n--- got ---\n%s", m, got)
		}
	}
}

func TestRenderStartScript_DefaultJavaPath(t *testing.T) {
	spec := AppSpec{
		AppCode:    "demo",
		DeployPath: "/opt/demo",
		Port:       8080,
	}
	got := renderStartScript(spec)
	if !strings.Contains(got, "/usr/bin/java") {
		t.Errorf("expected default /usr/bin/java; got:\n%s", got)
	}
}

func TestRenderStartScript_SystemdUserSwitch(t *testing.T) {
	spec := AppSpec{
		AppCode:    "demo",
		DeployPath: "/opt/demo",
		Port:       8080,
		User:       "deployer",
	}
	got := renderStartScript(spec)
	if !strings.Contains(got, `runuser -u deployer --`) {
		t.Errorf("expected runuser switch for non-root user; got:\n%s", got)
	}
	if !strings.Contains(got, `$RUN_AS nohup`) {
		t.Errorf("expected $RUN_AS prefix on nohup command; got:\n%s", got)
	}
}

func TestRenderStartScript_NoUserSwitchForRoot(t *testing.T) {
	spec := AppSpec{
		AppCode:    "demo",
		DeployPath: "/opt/demo",
		Port:       8080,
		User:       "root",
	}
	got := renderStartScript(spec)
	if strings.Contains(got, "runuser") {
		t.Errorf("did not expect runuser when User=root; got:\n%s", got)
	}
}

func TestRenderStopScript_PidFlow(t *testing.T) {
	spec := AppSpec{AppCode: "demo", DeployPath: "/opt/demo", Port: 8080}
	got := renderStopScript(spec)
	musts := []string{
		"PID_FILE='/opt/demo/app.pid'",
		`kill -15 "$PID"`,
		`kill -9 "$PID"`,
		"rm -f \"$PID_FILE\"",
	}
	for _, m := range musts {
		if !strings.Contains(got, m) {
			t.Errorf("renderStopScript missing %q\n--- got ---\n%s", m, got)
		}
	}
}

func TestEnvVarsSortedOrder(t *testing.T) {
	spec := AppSpec{
		AppCode:    "demo",
		DeployPath: "/opt/demo",
		Port:       8080,
		EnvVars: map[string]string{
			"ZULU":    "1",
			"ALPHA":   "2",
			"BRAVO":   "3",
			"CHARLIE": "4",
		},
	}
	got := renderStartScript(spec)
	// 期望按字典序：ALPHA → BRAVO → CHARLIE → ZULU
	idxAlpha := strings.Index(got, "export ALPHA=")
	idxBravo := strings.Index(got, "export BRAVO=")
	idxCharlie := strings.Index(got, "export CHARLIE=")
	idxZulu := strings.Index(got, "export ZULU=")
	if !(idxAlpha < idxBravo && idxBravo < idxCharlie && idxCharlie < idxZulu) {
		t.Errorf("env vars not sorted: alpha=%d bravo=%d charlie=%d zulu=%d",
			idxAlpha, idxBravo, idxCharlie, idxZulu)
	}
}

func TestNormalizeDeployMode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", DeployModeSystemd},
		{"systemd", DeployModeSystemd},
		{"SYSTEMD", DeployModeSystemd},
		{" systemd ", DeployModeSystemd},
		{"nohup", DeployModeNohup},
		{"NoHup", DeployModeNohup},
		{"docker", DeployModeDocker},
		{"Docker", DeployModeDocker},
		{"unknown", DeployModeSystemd}, // 未知值兜底走 systemd
	}
	for _, c := range cases {
		if got := NormalizeDeployMode(c.in); got != c.want {
			t.Errorf("NormalizeDeployMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateDeployMode(t *testing.T) {
	good := []string{"", "systemd", "nohup", "docker", "SYSTEMD", " nohup ", " Docker "}
	for _, g := range good {
		if err := ValidateDeployMode(g); err != nil {
			t.Errorf("ValidateDeployMode(%q) unexpected error: %v", g, err)
		}
	}
	bad := []string{"k8s", "supervisor"}
	for _, b := range bad {
		if err := ValidateDeployMode(b); err == nil {
			t.Errorf("ValidateDeployMode(%q) should have failed", b)
		}
	}
}

func TestSystemdRuntime_HumanizeError(t *testing.T) {
	r := &systemdRuntime{}
	cases := []struct {
		name     string
		dump     string
		mustHave string
	}{
		{"203/EXEC", "Active: failed (Result: exit-code) status=203/EXEC", "Java 可执行文件不存在"},
		{"200/CHDIR", "status=200/CHDIR", "WorkingDirectory"},
		{"OOM", "Process killed by OOM-killer", "OOM"},
		{"port_busy", "java.net.BindException: Address already in use", "端口已被占用"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.HumanizeError(c.dump)
			if !strings.Contains(got, c.mustHave) {
				t.Errorf("HumanizeError missing %q, got: %q", c.mustHave, got)
			}
		})
	}
	if r.HumanizeError("") != "" {
		t.Errorf("empty dump should return empty hint")
	}
}

func TestNohupRuntime_HumanizeError(t *testing.T) {
	r := &nohupRuntime{}
	cases := []struct {
		name     string
		dump     string
		mustHave string
	}{
		{"port_busy", "java.net.BindException: Address already in use", "端口已被占用"},
		{"oom", "java.lang.OutOfMemoryError: Java heap space", "OOM"},
		{"class_not_found", "Caused by: java.lang.ClassNotFoundException", "Class not found"},
		{"early_exit", "(进程已退出，pid 文件残留)", "进程启动后立即退出"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.HumanizeError(c.dump)
			if !strings.Contains(got, c.mustHave) {
				t.Errorf("HumanizeError missing %q, got: %q", c.mustHave, got)
			}
		})
	}
}
