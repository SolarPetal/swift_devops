package service_test

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

func setupDeploySvc(t *testing.T) (*service.DeploymentService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Application{}, &model.AppService{}, &model.Host{}, &model.Deployment{},
		&model.ArtifactBundle{}, &model.ArtifactItem{}, &model.PipelineRunHost{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewDeploymentService(db), db
}

// 准备一个 app + 两台 host
func seed(t *testing.T, db *gorm.DB) (appID, hostA, hostB uint) {
	t.Helper()
	a := &model.Application{AppCode: "user-svc", Name: "User", DeployPath: "/opt/u", Port: 8080}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := db.Create(&model.AppService{
		AppID:          a.ID,
		ServiceCode:    "default",
		Name:           "default",
		Port:           8080,
		HealthCheckURL: "/actuator/health",
		StartupOrder:   100,
		Enabled:        true,
	}).Error; err != nil {
		t.Fatalf("seed default service: %v", err)
	}
	h1 := &model.Host{Name: "h1", IP: "10.0.0.1", Port: 22, AuthType: "password", Username: "root", Status: "unknown"}
	h2 := &model.Host{Name: "h2", IP: "10.0.0.2", Port: 22, AuthType: "password", Username: "root", Status: "unknown"}
	if err := db.Create(h1).Error; err != nil {
		t.Fatalf("seed h1: %v", err)
	}
	if err := db.Create(h2).Error; err != nil {
		t.Fatalf("seed h2: %v", err)
	}
	return a.ID, h1.ID, h2.ID
}

func TestDeployment_BindRequiresConfiguredService(t *testing.T) {
	svc, db := setupDeploySvc(t)
	app := &model.Application{AppCode: "empty-svc", Name: "Empty", DeployPath: "/opt/empty", Port: 8080}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("seed app: %v", err)
	}
	host := &model.Host{Name: "h1", IP: "10.0.0.1", Port: 22, AuthType: "password", Username: "root"}
	if err := db.Create(host).Error; err != nil {
		t.Fatalf("seed host: %v", err)
	}
	_, err := svc.Bind(app.ID, service.DeploymentInput{HostID: host.ID})
	if err == nil {
		t.Fatal("未配置 service 时绑定主机应失败")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST，得 %v", err)
	}
}

func TestDeployment_BindSpringCloudServiceAware(t *testing.T) {
	svc, db := setupDeploySvc(t)
	app := &model.Application{AppCode: "suite", Name: "Suite", AppType: "spring-cloud", DeployPath: "/opt/suite", Port: 8080}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("seed app: %v", err)
	}
	h := &model.Host{Name: "h1", IP: "10.0.0.1", Port: 22, AuthType: "password", Username: "root"}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("seed host: %v", err)
	}
	for _, code := range []string{"auth-server", "admin-server"} {
		if err := db.Create(&model.AppService{
			AppID: app.ID, ServiceCode: code, Name: code, Port: 8080,
			HealthCheckURL: "/actuator/health", StartupOrder: 100, Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed service %s: %v", code, err)
		}
	}
	if _, err := svc.Bind(app.ID, service.DeploymentInput{HostID: h.ID}); err == nil {
		t.Fatal("多 service 应用未指定 service_code 应失败")
	}
	d1, err := svc.Bind(app.ID, service.DeploymentInput{HostID: h.ID, ServiceCode: "auth-server"})
	if err != nil {
		t.Fatalf("bind auth: %v", err)
	}
	d2, err := svc.Bind(app.ID, service.DeploymentInput{HostID: h.ID, ServiceCode: "admin-server"})
	if err != nil {
		t.Fatalf("bind admin same host: %v", err)
	}
	if d1.ServiceCode != "auth-server" || d2.ServiceCode != "admin-server" {
		t.Fatalf("service_code mismatch: %+v %+v", d1, d2)
	}
}

func TestDeployment_BindAndList(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, h2 := seed(t, db)

	d1, err := svc.Bind(appID, service.DeploymentInput{HostID: h1, GroupTag: "blue"})
	if err != nil {
		t.Fatalf("bind h1: %v", err)
	}
	if d1.HostName != "h1" || d1.HostIP != "10.0.0.1" {
		t.Fatalf("view 应含 host 信息：%+v", d1)
	}
	if d1.Status != "pending" {
		t.Fatalf("初始 status 应 pending，得 %s", d1.Status)
	}
	if d1.ServiceCode != "default" {
		t.Fatalf("单 service 应自动绑定 default，得 %q", d1.ServiceCode)
	}

	if _, err := svc.Bind(appID, service.DeploymentInput{HostID: h2, GroupTag: "green"}); err != nil {
		t.Fatalf("bind h2: %v", err)
	}

	list, err := svc.ListByApp(appID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应 2 条，得 %d", len(list))
	}
}

func TestDeployment_ListDecoratesCurrentItemAndLastDeployStatus(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)

	d, err := svc.Bind(appID, service.DeploymentInput{HostID: h1})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	bundle := &model.ArtifactBundle{
		AppID:       appID,
		VersionTag:  "v-test",
		BuildStatus: "success",
	}
	if err := db.Create(bundle).Error; err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	item := &model.ArtifactItem{
		BundleID:    bundle.ID,
		ServiceCode: d.ServiceCode,
		FileName:    "app.jar",
		FilePath:    "/tmp/app.jar",
		FileMD5:     "abc",
		FileSize:    123,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if err := db.Model(&model.Deployment{}).Where("id = ?", d.ID).Updates(map[string]any{
		"status":                   "running",
		"current_artifact_item_id": item.ID,
	}).Error; err != nil {
		t.Fatalf("update deployment: %v", err)
	}
	if err := db.Create(&model.PipelineRunHost{
		RunID:        1,
		HostID:       h1,
		ServiceCode:  d.ServiceCode,
		DeploymentID: d.ID,
		Status:       "failed",
		CurrentStage: "restart",
		Error:        "old failure",
	}).Error; err != nil {
		t.Fatalf("seed old run host: %v", err)
	}
	if err := db.Create(&model.PipelineRunHost{
		RunID:        2,
		HostID:       h1,
		ServiceCode:  d.ServiceCode,
		DeploymentID: d.ID,
		Status:       "success",
		CurrentStage: "health",
	}).Error; err != nil {
		t.Fatalf("seed latest run host: %v", err)
	}

	list, err := svc.ListByApp(appID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应 1 条，得 %d", len(list))
	}
	got := list[0]
	if got.CurrentArtifactID != 0 {
		t.Fatalf("bundle 链路不应伪造 current_artifact_id，得 %d", got.CurrentArtifactID)
	}
	if got.CurrentArtifactItemID != item.ID || got.CurrentBundleID != bundle.ID || got.CurrentBundleVersion != "v-test" {
		t.Fatalf("current item/bundle 未装饰正确：%+v", got)
	}
	if got.Status != "running" {
		t.Fatalf("runtime status 应保留 running，得 %s", got.Status)
	}
	if got.LastRunID != 2 || got.LastDeployStatus != "success" || got.LastDeployStage != "health" {
		t.Fatalf("latest deploy status 未装饰正确：%+v", got)
	}
}

func TestDeployment_DuplicateBindRejected(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)

	if _, err := svc.Bind(appID, service.DeploymentInput{HostID: h1}); err != nil {
		t.Fatalf("bind 1: %v", err)
	}
	_, err := svc.Bind(appID, service.DeploymentInput{HostID: h1})
	if err == nil {
		t.Fatal("重复绑定应失败")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "CONFLICT" {
		t.Fatalf("应 CONFLICT，得 %v", err)
	}
}

func TestDeployment_NotFoundOnMissingApp(t *testing.T) {
	svc, db := setupDeploySvc(t)
	_, h1, _ := seed(t, db)
	_, err := svc.Bind(9999, service.DeploymentInput{HostID: h1})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND，得 %v", err)
	}
}

func TestDeployment_NotFoundOnMissingHost(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, _, _ := seed(t, db)
	_, err := svc.Bind(appID, service.DeploymentInput{HostID: 9999})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND，得 %v", err)
	}
}

func TestDeployment_GroupTagIgnoredOnBind(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	d, err := svc.Bind(appID, service.DeploymentInput{HostID: h1, GroupTag: "blue"})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if d.GroupTag != "" {
		t.Fatalf("group_tag should be cleared, got %q", d.GroupTag)
	}
}

func TestDeployment_Unbind(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	d, _ := svc.Bind(appID, service.DeploymentInput{HostID: h1})

	if err := svc.Unbind(d.ID); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	// 二次 unbind 应失败
	if err := svc.Unbind(d.ID); err == nil {
		t.Fatal("二次解绑应失败")
	}
	// 解绑后允许重新绑定（UNIQUE 仅约束现存行）
	if _, err := svc.Bind(appID, service.DeploymentInput{HostID: h1}); err != nil {
		t.Fatalf("解绑后重新绑定应允许：%v", err)
	}
}

func TestDeployment_UnbindRejectsRunningDeployment(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	d, err := svc.Bind(appID, service.DeploymentInput{HostID: h1})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := db.Model(&model.Deployment{}).Where("id = ?", d.ID).Update("status", "running").Error; err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = svc.Unbind(d.ID)
	if err == nil {
		t.Fatal("运行中 deployment 不应允许解绑")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "CONFLICT" {
		t.Fatalf("应 CONFLICT，得 %v", err)
	}
	var n int64
	if err := db.Model(&model.Deployment{}).Where("id = ?", d.ID).Count(&n).Error; err != nil {
		t.Fatalf("count deployment: %v", err)
	}
	if n != 1 {
		t.Fatalf("运行中解绑失败后 deployment 应保留，得 %d", n)
	}
}

func TestDeployment_StopRuntimeRejectsNonDockerDeployment(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	d, err := svc.Bind(appID, service.DeploymentInput{HostID: h1})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	_, err = svc.StopRuntime(context.Background(), d.ID)
	if err == nil {
		t.Fatal("非 Docker deployment 不应允许停止容器")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST，得 %v", err)
	}
}

func TestDeployment_RestartRuntimeRequiresRuntimeActionService(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	if err := db.Model(&model.Application{}).Where("id = ?", appID).Update("deploy_mode", "docker").Error; err != nil {
		t.Fatalf("mark docker mode: %v", err)
	}
	d, err := svc.Bind(appID, service.DeploymentInput{HostID: h1})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	_, err = svc.RestartRuntime(context.Background(), d.ID)
	if err == nil {
		t.Fatal("未初始化 runtime action service 时不应继续重启容器")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "INTERNAL" {
		t.Fatalf("应 INTERNAL，得 %v", err)
	}
}

func TestDeployment_CountByAppAndHost(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, h2 := seed(t, db)
	_, _ = svc.Bind(appID, service.DeploymentInput{HostID: h1})
	_, _ = svc.Bind(appID, service.DeploymentInput{HostID: h2})

	if n, _ := svc.CountByApp(appID); n != 2 {
		t.Fatalf("CountByApp: %d", n)
	}
	if n, _ := svc.CountByHost(h1); n != 1 {
		t.Fatalf("CountByHost h1: %d", n)
	}
}
