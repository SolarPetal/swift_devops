package service_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// 准备 pipeline service + 它依赖的 host/artifact svc
func setupPipeSvc(t *testing.T) (*service.PipelineService, *service.HostService, *service.ArtifactService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Host{}, &model.Application{}, &model.Artifact{},
		&model.Deployment{}, &model.PipelineRun{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	aes, _ := cryptopkg.NewAESGCM(key)
	hostSvc := service.NewHostService(db, aes)
	artSvc := service.NewArtifactService(db)
	pipeSvc := service.NewPipelineService(db, hostSvc, artSvc, 0)
	return pipeSvc, hostSvc, artSvc, db
}

func seedAppForPipe(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	a := &model.Application{
		AppCode: "demo-pipe", Name: "Demo", AppType: "jar",
		DeployPath: "/opt/apps/demo", Port: 8080,
		HealthCheckURL: "/actuator/health",
	}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return a.ID
}

func registerJar(t *testing.T, svc *service.ArtifactService, appID uint, ver string) uint {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "x.jar")
	if err := os.WriteFile(p, []byte("xx"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a, err := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: ver, FilePath: p,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return a.ID
}

func TestPipeline_Trigger_AppNotFound(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	_, err := svc.Trigger(9999, 1, "tester", "single")
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND：%v", err)
	}
}

func TestPipeline_Trigger_ArtifactMismatch(t *testing.T) {
	svc, _, artSvc, db := setupPipeSvc(t)
	app1 := seedAppForPipe(t, db)
	app2 := &model.Application{AppCode: "other", Name: "X", DeployPath: "/o", Port: 9}
	db.Create(app2)
	artID := registerJar(t, artSvc, app2.ID, "v1") // 制品属于 app2

	_, err := svc.Trigger(app1, artID, "tester", "single")
	if err == nil {
		t.Fatal("应失败")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
}

func TestPipeline_Trigger_NoDeployments(t *testing.T) {
	svc, _, artSvc, db := setupPipeSvc(t)
	appID := seedAppForPipe(t, db)
	artID := registerJar(t, artSvc, appID, "v1")
	_, err := svc.Trigger(appID, artID, "tester", "single")
	if err == nil {
		t.Fatal("没绑主机应失败")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
}

func TestPipeline_Trigger_StrategyValidation(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	_, err := svc.Trigger(1, 1, "tester", "rolling")
	if err == nil {
		t.Fatal("rolling 应拒")
	}
	ae, _ := apperr.As(err)
	if ae == nil || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
}

func TestPipeline_List_Empty(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	list, err := svc.List(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("应空")
	}
}

func TestPipeline_Get_NotFound(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	_, err := svc.Get(9999)
	if err == nil {
		t.Fatal("应失败")
	}
}
