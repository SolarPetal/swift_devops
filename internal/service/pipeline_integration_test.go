//go:build integration

// 集成测试：跑真实 SSH + SFTP + systemctl + health 全链路。
//
// 启用：
//
//	go test -tags=integration ./internal/service/...
//
// 依赖：本机能 docker run；首次运行会自动 docker build。
// 容器映射：12222 → sshd(22)；18080 → nginx(80) 提供 /actuator/health。
package service_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	"swift-devops/internal/service"
)

const (
	itImage     = "swift-devops-sshd:test"
	itContainer = "swift-devops-sshd-test"
	itSSHPort   = "12222"
	itHTTPPort  = "18080"
	itPassword  = "swift-devops-test"
)

// ensureImage 必要时 docker build。
func ensureImage(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "images", "-q", itImage).Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return
	}
	t.Logf("docker build %s …", itImage)
	cmd := exec.Command("docker", "build", "-t", itImage, "-f", "testdata/Dockerfile.sshd", "testdata")
	var berr bytes.Buffer
	cmd.Stderr = &berr
	cmd.Stdout = &berr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, berr.String())
	}
}

// startContainer 起容器，等 sshd 起来后返回 cleanup。
func startContainer(t *testing.T) func() {
	t.Helper()
	// 清残留
	_ = exec.Command("docker", "rm", "-f", itContainer).Run()
	cmd := exec.Command("docker", "run", "-d", "--rm", "--name", itContainer,
		"-p", itSSHPort+":22", "-p", itHTTPPort+":80", itImage)
	var berr bytes.Buffer
	cmd.Stderr = &berr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, berr.String())
	}
	cleanup := func() {
		out, _ := exec.Command("docker", "logs", "--tail", "50", itContainer).CombinedOutput()
		if t.Failed() {
			t.Logf("---container logs---\n%s\n---end---", out)
		}
		_ = exec.Command("docker", "rm", "-f", itContainer).Run()
	}
	// 等 sshd 起来：tcp 拨通 + 20s 超时
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := exec.Command("bash", "-c",
			fmt.Sprintf("nc -z -w 1 127.0.0.1 %s && echo OK", itSSHPort)).Output()
		if err == nil && strings.Contains(string(conn), "OK") {
			// 多等 500ms 让 sshd 完全 ready
			time.Sleep(500 * time.Millisecond)
			return cleanup
		}
		time.Sleep(300 * time.Millisecond)
	}
	cleanup()
	t.Fatalf("sshd 未在 20s 内就绪")
	return cleanup
}

func dockerExec(t *testing.T, cmd string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", itContainer, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Logf("docker exec %q err: %v\n%s", cmd, err, out)
	}
	return string(out)
}

// setupForIntegration 装配完整一套（host/app/artifact/deployment）。
func setupForIntegration(t *testing.T) (*service.PipelineService, uint, uint) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Host{}, &model.Application{}, &model.Artifact{},
		&model.Deployment{}, &model.PipelineRun{}, &model.PipelineRunHost{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := strings.Repeat("A", 44)[:44] // 任意 32B 的 base64 长度（44 chars + '='）
	// 用真随机更稳：直接生成
	key = "uoVrzdITJj+e1YqRXDnUAunQt4qgbU1PzjAMVT13SqA="
	aes, _ := cryptopkg.NewAESGCM(key)
	hostSvc := service.NewHostService(db, aes)
	artRoot := t.TempDir()
	artSvc := service.NewArtifactService(db, artRoot, 0)
	depSvc := service.NewDeploymentService(db)
	pipeSvc := service.NewPipelineService(db, hostSvc, artSvc, 10*time.Second)

	// host
	hv, err := hostSvc.Create(service.HostInput{
		Name: "container", IP: "127.0.0.1",
		Port: parsePortOr(itSSHPort, 12222),
		AuthType: "password", Username: "root", Password: itPassword,
	})
	if err != nil {
		t.Fatalf("host create: %v", err)
	}

	// app：deploy_path 用 /tmp/swift-devops-test，端口用 nginx 暴露的 18080（host 角度）
	// 这里 deployment.Port=0 → effectivePort=app.Port=18080
	// systemd_user=root：容器里 root 一定在；同时端到端验证 SystemdUser 透传到 unit
	appM := &model.Application{
		AppCode:        "demo-it",
		Name:           "Demo Integration",
		AppType:        "jar",
		DeployPath:     "/tmp/swift-devops-test",
		Port:           parsePortOr(itHTTPPort, 18080),
		HealthCheckURL: "/actuator/health",
		SystemdUser:    "root",
	}
	if err := db.Create(appM).Error; err != nil {
		t.Fatalf("app create: %v", err)
	}

	// 绑定
	if _, err := depSvc.Bind(appM.ID, service.DeploymentInput{HostID: hv.ID}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// 制品：写一个临时 jar（必须落在 artRoot 内，过白名单）
	jar := writeJarIn(t, artRoot, "INTEGRATION-FAKE-JAR-CONTENT")
	art, err := artSvc.Register(service.ArtifactInput{
		AppID: appM.ID, VersionTag: "vIT.1", FilePath: jar,
	})
	if err != nil {
		t.Fatalf("artifact register: %v", err)
	}

	return pipeSvc, appM.ID, art.ID
}

func parsePortOr(s string, fallback int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n == 0 {
		return fallback
	}
	return n
}

// waitForRun 轮询直到 status != running 或超时。
func waitForRun(t *testing.T, svc *service.PipelineService, runID uint, timeout time.Duration) service.PipelineRunView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		v, err := svc.Get(runID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if v.Status != "running" && v.Status != "pending" {
			return v
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("run %d 未在 %v 内结束", runID, timeout)
	return service.PipelineRunView{}
}

// TestPipeline_E2E_Integration 端到端：dial → sftp upload → write unit → fake systemctl restart → health probe。
func TestPipeline_E2E_Integration(t *testing.T) {
	if os.Getenv("SKIP_DOCKER") != "" {
		t.Skip("SKIP_DOCKER set")
	}
	// 检 docker 在
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	ensureImage(t)
	cleanup := startContainer(t)
	defer cleanup()

	pipe, appID, artID := setupForIntegration(t)
	_ = context.Background() // pipeline 内部已建 ctx

	out, err := pipe.Trigger(appID, artID, "integration", "single")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if out.Status != "running" {
		t.Fatalf("trigger 后应 running，得 %s", out.Status)
	}

	final := waitForRun(t, pipe, out.ID, 60*time.Second)
	if final.Status != "success" {
		t.Fatalf("应 success，得 %s\nstate_snapshot=%s", final.Status, final.StateSnapshot)
	}

	// 验证容器里 jar 真的到了
	lsOut := dockerExec(t, "ls -la /tmp/swift-devops-test/app.jar")
	if !strings.Contains(lsOut, "app.jar") {
		t.Fatalf("jar 应已到容器：\n%s", lsOut)
	}
	jarContent := dockerExec(t, "cat /tmp/swift-devops-test/app.jar")
	if !strings.Contains(jarContent, "INTEGRATION-FAKE-JAR-CONTENT") {
		t.Fatalf("jar 内容不一致：%s", jarContent)
	}

	// 验证 systemd unit 被写入
	unitOut := dockerExec(t, "cat /etc/systemd/system/devops-demo-it.service")
	for _, kw := range []string{"WorkingDirectory=/tmp/swift-devops-test", "ExecStart=/usr/bin/java", "--server.port=" + itHTTPPort, "User=root"} {
		if !strings.Contains(unitOut, kw) {
			t.Errorf("unit 缺少 %q\n---\n%s", kw, unitOut)
		}
	}

	// 验证 fake systemctl 被调用了 daemon-reload + restart
	logOut := dockerExec(t, "cat /tmp/systemctl.log")
	for _, kw := range []string{"daemon-reload", "enable devops-demo-it.service", "restart devops-demo-it.service"} {
		if !strings.Contains(logOut, kw) {
			t.Errorf("systemctl 未调用 %q\n---\n%s", kw, logOut)
		}
	}

	// 验证 MD5 / step 完整
	if !strings.Contains(final.StateSnapshot, `"stage":"health"`) ||
		!strings.Contains(final.StateSnapshot, `"ok":true`) {
		t.Errorf("snapshot 不完整：%s", final.StateSnapshot)
	}
}

// 让编译器看到 import filepath 用过（避免误删）
var _ = filepath.Base
