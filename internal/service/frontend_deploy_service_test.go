package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service/pipeline/strategy"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeFrontendAppConfigDefaults(t *testing.T) {
	app := &model.Application{ID: 7, AppCode: "portal"}
	got, err := normalizeFrontendAppConfig(app, FrontendAppConfigInput{})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.ServiceCode != "web" || got.PackageManager != "auto" || got.BuildCommand != "npm run build" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	if got.ContainerName != "sd-fe-portal-web" {
		t.Fatalf("container=%q", got.ContainerName)
	}
	if !got.SPAFallback {
		t.Fatal("spa_fallback should default true")
	}
}

func TestRenderFrontendDockerfileAutoPackageManager(t *testing.T) {
	app := &model.Application{AppCode: "portal"}
	cfg, err := normalizeFrontendAppConfig(app, FrontendAppConfigInput{DistDir: "build"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got := renderFrontendDockerfile(app, cfg)
	needles := []string{
		"FROM node:20-alpine AS builder",
		"pnpm-lock.yaml",
		"yarn.lock",
		"RUN npm run build",
		"COPY --from=builder /app/build /usr/share/nginx/html",
		"FROM nginx:1.27-alpine",
	}
	for _, needle := range needles {
		if !strings.Contains(got, needle) {
			t.Fatalf("Dockerfile missing %q:\n%s", needle, got)
		}
	}
}

func TestRenderFrontendRuntimeNginxConfigSPAFallback(t *testing.T) {
	cfg := &model.FrontendAppConfig{SPAFallback: true}
	got := renderFrontendRuntimeNginxConfig(cfg)
	if !strings.Contains(got, "try_files $uri $uri/ /index.html;") {
		t.Fatalf("expected SPA fallback:\n%s", got)
	}
	cfg.SPAFallback = false
	got = renderFrontendRuntimeNginxConfig(cfg)
	if !strings.Contains(got, "try_files $uri $uri/ =404;") {
		t.Fatalf("expected no SPA fallback:\n%s", got)
	}
}

func TestCreateTarGzSkipsHeavyDirs(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("package.json", "{}")
	mustWrite("src/main.ts", "console.log(1)")
	mustWrite("node_modules/pkg/index.js", "skip")
	mustWrite("dist/index.html", "skip")
	archive := filepath.Join(t.TempDir(), "frontend.tar.gz")
	if err := createTarGz(dir, archive); err != nil {
		t.Fatalf("create tar: %v", err)
	}
	got := tarNames(t, archive)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "package.json") || !strings.Contains(joined, "src/main.ts") {
		t.Fatalf("archive missing expected files: %v", got)
	}
	if strings.Contains(joined, "node_modules") || strings.Contains(joined, "dist/index.html") {
		t.Fatalf("archive should skip heavy dirs: %v", got)
	}
}

func TestFrontendDeployRejectsNonFrontendApp(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "api", Name: "API", AppType: "jar", DeployPath: "/opt/api", Port: 8080}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	_, err := svc.GetConfig(app.ID, "web")
	if err == nil {
		t.Fatal("expected non-frontend app to be rejected")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendDeployRecordsFailedPipelineRun(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", GitURL: "https://example.com/repo.git", GitRef: "main", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11", Port: 22, AuthType: "password", Username: "root"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}

	// cred_id 非 0 但 GitCredentialService 未注入：会在创建 PipelineRun 后失败，
	// 用来稳定验证失败历史、状态与错误快照。
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	out, err := svc.Deploy(context.Background(), app.ID, FrontendDeployInput{
		HostID: host.ID,
		CredID: 1,
		Domain: "www.example.com",
	}, "tester")
	if err != nil {
		t.Fatalf("trigger deploy: %v", err)
	}
	if out.PipelineRunID == 0 || out.PipelineStatus != RunStatusRunning {
		t.Fatalf("unexpected trigger response: %+v", out)
	}

	run := waitFrontendRunStatus(t, db, out.PipelineRunID, RunStatusFailed)
	if run.Strategy != frontendPipelineStrategy || run.Status != RunStatusFailed || run.TriggeredBy != "tester" {
		t.Fatalf("unexpected run: %+v", run)
	}
	var snap strategy.RunSnapshot
	if err := json.Unmarshal([]byte(run.StateSnapshot), &snap); err != nil {
		t.Fatalf("snapshot json: %v\n%s", err, run.StateSnapshot)
	}
	if snap.Strategy != frontendPipelineStrategy || snap.Error == "" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Steps) != 1 || snap.Steps[0].Stage != FrontendStagePrepare || snap.Steps[0].OK {
		t.Fatalf("unexpected failed step: %+v", snap.Steps)
	}
	if !strings.Contains(snap.Error, "Git 凭证服务未初始化") {
		t.Fatalf("snapshot error missing credential message: %q", snap.Error)
	}

	var runHost model.PipelineRunHost
	if err := db.First(&runHost, "run_id = ?", run.ID).Error; err != nil {
		t.Fatalf("load run host: %v", err)
	}
	if runHost.Status != strategy.HostStatusFailed || runHost.CurrentStage != FrontendStagePrepare {
		t.Fatalf("unexpected run host: %+v", runHost)
	}
}

func TestFrontendDeployAcceptsEmptyDomainForContainerOnlyDeploy(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", GitURL: "https://example.com/repo.git", GitRef: "main", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11", Port: 22, AuthType: "password", Username: "root"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}

	// 无域名测试环境应允许触发部署；这里用缺失 GitCredentialService 让异步流水线
	// 稳定停在 prepare 阶段，验证 domain 不再是同步必填门槛。
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	out, err := svc.Deploy(context.Background(), app.ID, FrontendDeployInput{
		HostID: host.ID,
		CredID: 1,
	}, "tester")
	if err != nil {
		t.Fatalf("trigger deploy without domain: %v", err)
	}
	if out.Domain != "" || out.PipelineRunID == 0 {
		t.Fatalf("unexpected trigger response: %+v", out)
	}
	waitFrontendRunStatus(t, db, out.PipelineRunID, RunStatusFailed)
}

func TestFrontendPipelineRecorderSuccess(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	cfg := &model.FrontendAppConfig{AppID: app.ID, ServiceCode: "web"}
	rec, err := svc.startFrontendPipelineRun(&app, cfg, &host, "tester")
	if err != nil {
		t.Fatalf("start recorder: %v", err)
	}
	rec.Step(FrontendStageGitClone, time.Now(), true, "ref=main commit=abc1234", nil)
	rec.Finish(RunStatusSuccess, "")

	var run model.PipelineRun
	if err := db.First(&run, rec.RunID()).Error; err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != RunStatusSuccess || run.FinishedAt == nil {
		t.Fatalf("unexpected run finish: %+v", run)
	}
	var snap strategy.RunSnapshot
	if err := json.Unmarshal([]byte(run.StateSnapshot), &snap); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	if snap.FinishedAt == "" || len(snap.Steps) != 1 || !snap.Steps[0].OK {
		t.Fatalf("unexpected final snapshot: %+v", snap)
	}
	var runHost model.PipelineRunHost
	if err := db.First(&runHost, "run_id = ?", run.ID).Error; err != nil {
		t.Fatalf("load run host: %v", err)
	}
	if runHost.Status != strategy.HostStatusSuccess {
		t.Fatalf("unexpected host status: %+v", runHost)
	}
}

func TestFrontendDeploymentStateRotation(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	cfg := &model.FrontendAppConfig{AppID: app.ID, ServiceCode: "web", ContainerName: "sd-fe-portal-web", TargetPort: 80}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)

	if err := svc.markFrontendDeploySuccess(&app, cfg, &host, "www.example.com", "swift-devops/portal-web:aaa1111", "aaa11112222", "/opt/portal/frontend/web/release-a", 11); err != nil {
		t.Fatalf("mark first success: %v", err)
	}
	var state model.FrontendDeploymentState
	if err := db.First(&state, "app_id = ? AND host_id = ? AND service_code = ? AND domain = ?", app.ID, host.ID, "web", "www.example.com").Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.CurrentImage != "swift-devops/portal-web:aaa1111" || state.PreviousImage != "" || state.CurrentPipelineRunID != 11 {
		t.Fatalf("unexpected first state: %+v", state)
	}

	if err := svc.markFrontendDeploySuccess(&app, cfg, &host, "www.example.com", "swift-devops/portal-web:bbb2222", "bbb22223333", "/opt/portal/frontend/web/release-b", 12); err != nil {
		t.Fatalf("mark second success: %v", err)
	}
	if err := db.First(&state, state.ID).Error; err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if state.CurrentImage != "swift-devops/portal-web:bbb2222" || state.CurrentCommitSHA != "bbb22223333" || state.CurrentPipelineRunID != 12 {
		t.Fatalf("unexpected current after rotation: %+v", state)
	}
	if state.PreviousImage != "swift-devops/portal-web:aaa1111" || state.PreviousCommitSHA != "aaa11112222" || state.PreviousPipelineRunID != 11 {
		t.Fatalf("unexpected previous after rotation: %+v", state)
	}
}

func TestFrontendDeploymentStateRotationWithoutDomain(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	cfg := &model.FrontendAppConfig{AppID: app.ID, ServiceCode: "web", ContainerName: "sd-fe-portal-web", TargetPort: 80}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)

	if err := svc.markFrontendDeploySuccess(&app, cfg, &host, "", "swift-devops/portal-web:aaa1111", "aaa11112222", "/opt/portal/frontend/web/release-a", 11); err != nil {
		t.Fatalf("mark first no-domain success: %v", err)
	}
	if err := svc.markFrontendDeploySuccess(&app, cfg, &host, "", "swift-devops/portal-web:bbb2222", "bbb22223333", "/opt/portal/frontend/web/release-b", 12); err != nil {
		t.Fatalf("mark second no-domain success: %v", err)
	}

	var state model.FrontendDeploymentState
	if err := db.First(&state, "app_id = ? AND host_id = ? AND service_code = ? AND domain = ?", app.ID, host.ID, "web", "").Error; err != nil {
		t.Fatalf("load no-domain state: %v", err)
	}
	if state.Domain != "" || state.CurrentImage != "swift-devops/portal-web:bbb2222" || state.PreviousImage != "swift-devops/portal-web:aaa1111" {
		t.Fatalf("unexpected no-domain state: %+v", state)
	}
}

func TestListFrontendDeploymentStates(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	state := model.FrontendDeploymentState{
		AppID:                 app.ID,
		HostID:                host.ID,
		ServiceCode:           "web",
		Domain:                "www.example.com",
		ContainerName:         "sd-fe-portal-web",
		TargetPort:            80,
		CurrentImage:          "swift-devops/portal-web:bbb2222",
		CurrentCommitSHA:      "bbb22223333",
		CurrentRemoteWorkDir:  "/opt/portal/frontend/web/release-b",
		CurrentPipelineRunID:  12,
		CurrentDeployedAt:     time.Now(),
		PreviousImage:         "swift-devops/portal-web:aaa1111",
		PreviousCommitSHA:     "aaa11112222",
		PreviousRemoteWorkDir: "/opt/portal/frontend/web/release-a",
		PreviousPipelineRunID: 11,
		PreviousDeployedAt:    time.Now().Add(-time.Hour),
		Status:                "active",
	}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	rows, err := svc.ListStates(app.ID, FrontendDeploymentStateFilter{
		HostID:      host.ID,
		ServiceCode: "web",
		Domain:      "WWW.Example.COM.",
	})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.HostName != "fe-1" || got.HostIP != "10.0.0.11" || got.AppCode != "portal" {
		t.Fatalf("missing joined labels: %+v", got)
	}
	if got.CurrentImage != state.CurrentImage || got.PreviousImage != state.PreviousImage {
		t.Fatalf("unexpected version state: %+v", got)
	}
	if got.CurrentDeployedAt == "" || got.PreviousDeployedAt == "" || got.UpdatedAt == "" {
		t.Fatalf("expected formatted timestamps: %+v", got)
	}
}

func TestFrontendRollbackRejectsWithoutPrevious(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	state := model.FrontendDeploymentState{
		AppID:                app.ID,
		HostID:               host.ID,
		ServiceCode:          "web",
		Domain:               "www.example.com",
		ContainerName:        "sd-fe-portal-web",
		TargetPort:           80,
		CurrentImage:         "swift-devops/portal-web:aaa1111",
		CurrentCommitSHA:     "aaa11112222",
		CurrentRemoteWorkDir: "/opt/portal/frontend/web/release-a",
		CurrentPipelineRunID: 11,
		CurrentDeployedAt:    time.Now(),
		Status:               "active",
	}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	_, err := svc.Rollback(context.Background(), app.ID, FrontendRollbackInput{
		HostID:      host.ID,
		ServiceCode: "web",
		Domain:      "www.example.com",
	}, "tester")
	if err == nil {
		t.Fatal("expected rollback without previous to fail")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" || !strings.Contains(ae.Message, "没有可回滚") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendRollbackStateSwap(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	currentAt := time.Now().Add(-time.Hour)
	previousAt := time.Now().Add(-2 * time.Hour)
	state := model.FrontendDeploymentState{
		AppID:                 app.ID,
		HostID:                host.ID,
		ServiceCode:           "web",
		Domain:                "www.example.com",
		ContainerName:         "sd-fe-portal-web",
		TargetPort:            80,
		CurrentImage:          "swift-devops/portal-web:bbb2222",
		CurrentCommitSHA:      "bbb22223333",
		CurrentRemoteWorkDir:  "/opt/portal/frontend/web/release-b",
		CurrentPipelineRunID:  12,
		CurrentDeployedAt:     currentAt,
		PreviousImage:         "swift-devops/portal-web:aaa1111",
		PreviousCommitSHA:     "aaa11112222",
		PreviousRemoteWorkDir: "/opt/portal/frontend/web/release-a",
		PreviousPipelineRunID: 11,
		PreviousDeployedAt:    previousAt,
		Status:                "active",
	}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	cfg := &model.FrontendAppConfig{AppID: app.ID, ServiceCode: "web", ContainerName: "sd-fe-portal-web", TargetPort: 80}
	svc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	if err := svc.swapFrontendDeploymentStateOnRollback(app.ID, host.ID, "web", "www.example.com", cfg, 13); err != nil {
		t.Fatalf("swap rollback state: %v", err)
	}

	if err := db.First(&state, state.ID).Error; err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if state.CurrentImage != "swift-devops/portal-web:aaa1111" || state.CurrentCommitSHA != "aaa11112222" || state.CurrentPipelineRunID != 13 {
		t.Fatalf("unexpected current after rollback: %+v", state)
	}
	if state.PreviousImage != "swift-devops/portal-web:bbb2222" || state.PreviousCommitSHA != "bbb22223333" || state.PreviousPipelineRunID != 12 {
		t.Fatalf("unexpected previous after rollback: %+v", state)
	}
}

func TestFrontendDockerImageInspectCommandQuotesImage(t *testing.T) {
	cmd := frontendDockerImageInspectCommand("repo.example.com/team/web:abc123")
	if !strings.Contains(cmd, "docker image inspect 'repo.example.com/team/web:abc123'") {
		t.Fatalf("inspect command missing quoted image: %s", cmd)
	}
	if !strings.Contains(cmd, "image not found: repo.example.com/team/web:abc123") {
		t.Fatalf("inspect command missing human-readable miss message: %s", cmd)
	}
}

func TestFrontendPipelineCancelViaPipelineService(t *testing.T) {
	db := openFrontendDeployTestDB(t)
	app := model.Application{AppCode: "portal", Name: "Portal", AppType: "frontend", DeployPath: "/opt/portal", Port: 80}
	host := model.Host{Name: "fe-1", IP: "10.0.0.11"}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	pipeSvc := NewPipelineService(db, nil, nil, 0)
	frontendSvc := NewFrontendDeployService(db, nil, nil, nil, nil, t.TempDir(), 0)
	cfg := &model.FrontendAppConfig{AppID: app.ID, ServiceCode: "web"}
	rec, err := frontendSvc.startFrontendPipelineRun(&app, cfg, &host, "tester")
	if err != nil {
		t.Fatalf("start recorder: %v", err)
	}
	if err := pipeSvc.TryLockAppForExternalRun(app.ID); err != nil {
		t.Fatalf("lock app: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pipeSvc.RegisterCancelForExternalRun(rec.RunID(), cancel)
	defer pipeSvc.UnregisterCancelForExternalRun(rec.RunID())
	defer pipeSvc.ReleaseAppForExternalRun(app.ID)

	if err := pipeSvc.Cancel(rec.RunID()); err != nil {
		t.Fatalf("cancel frontend run: %v", err)
	}
	if !rec.CheckCancelled(ctx, FrontendStageGitClone, "测试取消") {
		t.Fatal("recorder should observe cancelled context")
	}

	run := waitFrontendRunStatus(t, db, rec.RunID(), RunStatusCancelled)
	var snap strategy.RunSnapshot
	if err := json.Unmarshal([]byte(run.StateSnapshot), &snap); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	if snap.Error != "前端部署已取消" || len(snap.Steps) != 1 || snap.Steps[0].Stage != FrontendStageGitClone {
		t.Fatalf("unexpected cancelled snapshot: %+v", snap)
	}
}

func tarNames(t *testing.T, archive string) []string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out []string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		out = append(out, hdr.Name)
	}
	return out
}

func waitFrontendRunStatus(t *testing.T, db *gorm.DB, runID uint, want string) model.PipelineRun {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var run model.PipelineRun
	for time.Now().Before(deadline) {
		if err := db.First(&run, runID).Error; err == nil && run.Status == want {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := db.First(&run, runID).Error; err != nil {
		t.Fatalf("load pipeline run %d: %v", runID, err)
	}
	t.Fatalf("run %d status=%s, want %s; snapshot=%s", runID, run.Status, want, run.StateSnapshot)
	return run
}

func openFrontendDeployTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Host{},
		&model.Application{},
		&model.FrontendGatewayInstance{},
		&model.FrontendGatewayRoute{},
		&model.FrontendAppConfig{},
		&model.FrontendDeploymentState{},
		&model.PipelineRun{},
		&model.PipelineRunHost{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
