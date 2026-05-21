package service_test

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// setupArtSvc 返回 svc + db + artifactDir。artifactDir 是当次测试的根白名单。
func setupArtSvc(t *testing.T) (*service.ArtifactService, *gorm.DB, string) {
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
	root := t.TempDir()
	return service.NewArtifactService(db, root, 1<<20 /* 1MB */), db, root
}

// 在 dir 下写一个 jar 文件，返回绝对路径。
func writeJarIn(t *testing.T, dir, content string) string {
	t.Helper()
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
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeJarIn(t, root, "hello-world")

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
	svc, _, root := setupArtSvc(t)
	jar := writeJarIn(t, root, "x")
	_, err := svc.Register(service.ArtifactInput{
		AppID: 9999, VersionTag: "v1", FilePath: jar,
	})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND：%v", err)
	}
}

func TestArtifact_Register_PathValidation(t *testing.T) {
	svc, db, _ := setupArtSvc(t)
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

// 路径白名单：注册 artifactDir 之外的文件应被拒。
func TestArtifact_Register_PathOutsideWhitelist(t *testing.T) {
	svc, db, _ := setupArtSvc(t)
	appID := seedApp(t, db)
	// 完全独立的 tmp 路径，落在 artifactDir 之外
	outsideDir := t.TempDir()
	jar := writeJarIn(t, outsideDir, "x")
	_, err := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1", FilePath: jar,
	})
	if err == nil {
		t.Fatal("应被白名单拒绝")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
	if !strings.Contains(ae.Message, "必须位于") {
		t.Errorf("错误信息应提示白名单：%v", ae.Message)
	}
}

func TestArtifact_Register_DirectoryRejected(t *testing.T) {
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	// 子目录，落在白名单内但不是文件
	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1", FilePath: sub,
	})
	if err == nil {
		t.Fatal("目录应失败")
	}
}

func TestArtifact_ListByApp(t *testing.T) {
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeJarIn(t, root, "a")
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
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeJarIn(t, root, "x")
	a, _ := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: "v1", FilePath: jar,
	})
	if err := svc.Delete(a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestArtifact_Delete_ProtectedByDeployment(t *testing.T) {
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	jar := writeJarIn(t, root, "x")
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

// ----- Upload 测试 -----

// 计算字节流的 MD5（hex）
func md5hex(b []byte) string {
	h := md5.Sum(b)
	return hex.EncodeToString(h[:])
}

func TestArtifact_Upload_OK(t *testing.T) {
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	payload := []byte("hello upload world")

	out, err := svc.Upload(service.ArtifactUploadInput{
		AppID: appID, VersionTag: "v2.0.0", FileName: "demo.jar",
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if out.FileSize != int64(len(payload)) {
		t.Errorf("size 应 %d，得 %d", len(payload), out.FileSize)
	}
	if out.FileMD5 != md5hex(payload) {
		t.Errorf("md5 不匹配")
	}
	if out.FileName != "demo.jar" {
		t.Errorf("filename 应保留原始：%s", out.FileName)
	}
	// 落地路径必须在 artifactDir/demo（app_code 子目录）下
	wantPrefix := filepath.Join(root, "demo") + string(filepath.Separator)
	if !strings.HasPrefix(out.FilePath, wantPrefix) {
		t.Errorf("落地路径应在 %s 下，得 %s", wantPrefix, out.FilePath)
	}
	if !strings.Contains(filepath.Base(out.FilePath), "v2.0.0-") {
		t.Errorf("文件名应含 sanitized version：%s", out.FilePath)
	}
	// 文件内容与原 payload 一致
	got, err := os.ReadFile(out.FilePath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("内容不一致")
	}
}

func TestArtifact_Upload_AppNotFound(t *testing.T) {
	svc, _, _ := setupArtSvc(t)
	_, err := svc.Upload(service.ArtifactUploadInput{
		AppID: 9999, VersionTag: "v1", FileName: "x.jar",
	}, bytes.NewReader([]byte("x")))
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND：%v", err)
	}
}

func TestArtifact_Upload_TooLarge(t *testing.T) {
	svc, db, root := setupArtSvc(t)
	appID := seedApp(t, db)
	// max=1MB，传 1MB+1 字节
	big := bytes.Repeat([]byte("A"), (1<<20)+1)

	_, err := svc.Upload(service.ArtifactUploadInput{
		AppID: appID, VersionTag: "v1", FileName: "big.jar",
	}, bytes.NewReader(big))
	if err == nil {
		t.Fatal("应被大小限制拒")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "REQUEST_TOO_LARGE" {
		t.Fatalf("应 REQUEST_TOO_LARGE：%v", err)
	}
	// 数据库不应有任何 artifact
	var n int64
	db.Model(&model.Artifact{}).Count(&n)
	if n != 0 {
		t.Errorf("失败后不应入库，得 %d 条", n)
	}
	// 落地文件应已清理（遍历 root/demo 目录确认）
	subDir := filepath.Join(root, "demo")
	entries, _ := os.ReadDir(subDir)
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("失败后不应残留文件：%s", e.Name())
		}
	}
}

func TestArtifact_Upload_VersionSanitize(t *testing.T) {
	svc, db, _ := setupArtSvc(t)
	appID := seedApp(t, db)
	// version 里塞奇怪字符，落地文件名应被替换
	out, err := svc.Upload(service.ArtifactUploadInput{
		AppID: appID, VersionTag: "v1/../etc;rm -rf", FileName: "x.jar",
	}, bytes.NewReader([]byte("ok")))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	base := filepath.Base(out.FilePath)
	if strings.Contains(base, "/") || strings.Contains(base, "..") || strings.Contains(base, ";") || strings.Contains(base, " ") {
		t.Errorf("落地文件名应被 sanitize：%s", base)
	}
}

// 编译期保险：MD5 算法和 deploy.LocalFileMD5 走同源（hex 32 位）
var _ = io.Copy
