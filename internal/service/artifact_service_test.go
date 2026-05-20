package service_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

func setupArtSvc(t *testing.T) (*service.ArtifactService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.Application{}, &model.Artifact{}, &model.Deployment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewArtifactService(db), db
}

// 写一个临时 jar 文件，返回绝对路径。测试结束自动清理。
func writeTempJar(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake.jar")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// 准备一个 app
func seedApp(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	app := &model.Application{
		AppCode: "demo", Name: "Demo", AppType: "jar",
		DeployPath: "/opt/apps/demo", Port: 8080,
		HealthCheckURL: "/actuator/health",
	}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	return app.ID
}

func TestArtifact_Register_OK(t *testing.T) {
	svc, db := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeTempJar(t, "hello-world")

	out, err := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1.0.0", FilePath: jar,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if out.FileMD5 == "" || out.FileSize == 0 {
		t.Fatalf("md5/size 应非空：%+v", out)
	}
	if out.FileName != "fake.jar" {
		t.Errorf("filename 默认应取 basename，得 %s", out.FileName)
	}
	if out.BuildStatus != "success" {
		t.Errorf("默认 BuildStatus 应 success")
	}
}

func TestArtifact_Register_AppNotFound(t *testing.T) {
	svc, _ := setupArtSvc(t)
	jar := writeTempJar(t, "x")
	_, err := svc.Register(service.ArtifactInput{
		AppID: 9999, VersionTag: "v1", FilePath: jar,
	})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND：%v", err)
	}
}

func TestArtifact_Register_PathValidation(t *testing.T) {
	svc, db := setupArtSvc(t)
	appID := seedApp(t, db)
	cases := []struct {
		name, path string
	}{
		{"相对路径", "relative/path.jar"},
		{"文件不存在", "/nonexistent/file.jar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Register(service.ArtifactInput{
				AppID: appID, VersionTag: "v1", FilePath: c.path,
			})
			if err == nil {
				t.Fatal("应失败")
			}
			ae, ok := apperr.As(err)
			if !ok || ae.Code != "BAD_REQUEST" {
				t.Fatalf("应 BAD_REQUEST：%v", err)
			}
		})
	}
}

func TestArtifact_Register_DirectoryRejected(t *testing.T) {
	svc, db := setupArtSvc(t)
	appID := seedApp(t, db)
	dir := t.TempDir()
	_, err := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1", FilePath: dir,
	})
	if err == nil {
		t.Fatal("目录应失败")
	}
}

func TestArtifact_ListByApp(t *testing.T) {
	svc, db := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeTempJar(t, "a")
	for _, v := range []string{"v1", "v2", "v3"} {
		_, err := svc.Register(service.ArtifactInput{
			AppID: appID, VersionTag: v, FilePath: jar,
		})
		if err != nil {
			t.Fatalf("register %s: %v", v, err)
		}
	}
	list, err := svc.ListByApp(appID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("应 3 条，得 %d", len(list))
	}
	// 最新优先
	if list[0].VersionTag != "v3" {
		t.Errorf("首条应 v3，得 %s", list[0].VersionTag)
	}
}

func TestArtifact_Delete_OK(t *testing.T) {
	svc, db := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeTempJar(t, "x")
	a, _ := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1", FilePath: jar,
	})
	if err := svc.Delete(a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestArtifact_Delete_ProtectedByDeployment(t *testing.T) {
	svc, db := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeTempJar(t, "x")
	a, _ := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1", FilePath: jar,
	})
	// 模拟 deployment 引用 current_artifact_id
	host := &model.Host{Name: "h", IP: "1.1.1.1", Username: "u", AuthType: "password"}
	db.Create(host)
	dep := &model.Deployment{AppID: appID, HostID: host.ID, CurrentArtifactID: a.ID, Status: "running"}
	db.Create(dep)

	err := svc.Delete(a.ID)
	if err == nil {
		t.Fatal("应被拒")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "CONFLICT" {
		t.Fatalf("应 CONFLICT：%v", err)
	}
}
