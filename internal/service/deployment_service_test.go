package service_test

import (
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
	if err := db.AutoMigrate(&model.Application{}, &model.Host{}, &model.Deployment{}); err != nil {
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

func TestDeployment_GroupTagValidation(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	_, err := svc.Bind(appID, service.DeploymentInput{HostID: h1, GroupTag: "invalid"})
	if err == nil {
		t.Fatal("应失败")
	}
}

func TestDeployment_UpdateGroup(t *testing.T) {
	svc, db := setupDeploySvc(t)
	appID, h1, _ := seed(t, db)
	d, _ := svc.Bind(appID, service.DeploymentInput{HostID: h1, GroupTag: "blue"})

	if err := svc.UpdateGroup(d.ID, "green"); err != nil {
		t.Fatalf("update: %v", err)
	}
	var got model.Deployment
	db.First(&got, d.ID)
	if got.GroupTag != "green" {
		t.Fatalf("group_tag: %s", got.GroupTag)
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
