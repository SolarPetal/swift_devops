package service_test

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

func setupAppSvc(t *testing.T) (*service.AppService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.Application{}, &model.Deployment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewAppService(db), db
}

func setupAppSvcWithServices(t *testing.T) (*service.AppService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Application{},
		&model.AppService{},
		&model.DockerfileTemplate{},
		&model.Deployment{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewAppService(db), db
}

func validInput() service.AppInput {
	return service.AppInput{
		AppCode:    "user-service",
		Name:       "用户服务",
		DeployPath: "/opt/apps/user-service",
		Port:       8080,
	}
}

func TestAppService_CreateAndGet(t *testing.T) {
	svc, _ := setupAppSvc(t)
	out, err := svc.Create(validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.AppCode != "user-service" {
		t.Fatalf("app_code: %s", out.AppCode)
	}
	if out.AppType != "jar" {
		t.Fatalf("默认 app_type 应为 jar，得 %s", out.AppType)
	}
	if out.HealthCheckURL != "/actuator/health" {
		t.Fatalf("默认 health URL 不对：%s", out.HealthCheckURL)
	}

	got, err := svc.Get(out.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "用户服务" {
		t.Fatalf("name: %s", got.Name)
	}
}

func TestAppService_CreateDoesNotCreateDefaultService(t *testing.T) {
	svc, db := setupAppSvcWithServices(t)
	out, err := svc.Create(validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var n int64
	if err := db.Model(&model.AppService{}).Where("app_id = ?", out.ID).Count(&n).Error; err != nil {
		t.Fatalf("count app_services: %v", err)
	}
	if n != 0 {
		t.Fatalf("新增应用后不应自动生成 default service，got %d", n)
	}
}

func TestAppService_CreateDefaultsPortWhenOmitted(t *testing.T) {
	svc, _ := setupAppSvc(t)
	in := validInput()
	in.Port = 0
	out, err := svc.Create(in)
	if err != nil {
		t.Fatalf("create without port: %v", err)
	}
	if out.Port != 8080 {
		t.Fatalf("omitted app port should default to 8080 for service initialization, got %d", out.Port)
	}
}

func TestAppService_AppCodeValidation(t *testing.T) {
	svc, _ := setupAppSvc(t)
	bad := []string{
		"User-Service",  // 大写
		"1-leading-num", // 数字开头
		"x",             // 太短
		"-leading-dash", // 连字符开头
		"with_underscore",
		"too" + string(make([]byte, 60)), // 太长
		"",
	}
	for _, code := range bad {
		t.Run("bad:"+code, func(t *testing.T) {
			in := validInput()
			in.AppCode = code
			if _, err := svc.Create(in); err == nil {
				t.Fatalf("应失败但成功了：%q", code)
			}
		})
	}
}

func TestAppService_UniqueAppCode(t *testing.T) {
	svc, _ := setupAppSvc(t)
	_, err := svc.Create(validInput())
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	_, err = svc.Create(validInput())
	if err == nil {
		t.Fatal("应失败：app_code 重复")
	}
	if !stderrors.Is(err, apperr.ErrConflict) {
		// ErrConflict.Code == "CONFLICT"
		ae, ok := apperr.As(err)
		if !ok || ae.Code != "CONFLICT" {
			t.Fatalf("应 CONFLICT，得 %v", err)
		}
	}
}

func TestAppService_PortValidation(t *testing.T) {
	// service 层不强制 port 范围（binding tag 在 handler 层做）
	// 这里只验证 service 不爆
	svc, _ := setupAppSvc(t)
	in := validInput()
	in.Port = 8080
	if _, err := svc.Create(in); err != nil {
		t.Fatalf("正常端口应通过：%v", err)
	}
}

func TestAppService_EnvVarsJSONValidation(t *testing.T) {
	svc, _ := setupAppSvc(t)
	cases := []struct {
		name    string
		envVars string
		ok      bool
	}{
		{"空字符串放行", "", true},
		{"合法 JSON 对象", `{"K":"v"}`, true},
		{"空对象", `{}`, true},
		{"非 JSON", `not-json`, false},
		{"JSON 数组（非对象）", `["x"]`, false},
		{"值非字符串", `{"K":123}`, false},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validInput()
			in.AppCode = fmt.Sprintf("app-env-%d", i)
			in.EnvVars = c.envVars
			_, err := svc.Create(in)
			if c.ok && err != nil {
				t.Fatalf("应通过但失败：%v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("应失败但通过")
			}
		})
	}
}

func TestAppService_UpdateAndDelete(t *testing.T) {
	svc, _ := setupAppSvc(t)
	a, _ := svc.Create(validInput())

	in := validInput()
	in.Name = "用户服务（改名）"
	in.JvmArgs = "-Xms512m -Xmx512m"
	updated, err := svc.Update(a.ID, in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "用户服务（改名）" {
		t.Fatalf("name: %s", updated.Name)
	}
	if updated.JvmArgs != "-Xms512m -Xmx512m" {
		t.Fatalf("jvm_args: %s", updated.JvmArgs)
	}

	if err := svc.Delete(a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.Delete(a.ID); err == nil {
		t.Fatal("二次删除应失败")
	}
}

func TestAppService_AppTypeValidation(t *testing.T) {
	svc, _ := setupAppSvc(t)
	in := validInput()
	in.AppType = "invalid"
	if _, err := svc.Create(in); err == nil {
		t.Fatal("应失败：未知 app_type")
	}
}

func TestAppService_GetNotFound(t *testing.T) {
	svc, _ := setupAppSvc(t)
	_, err := svc.Get(9999)
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND，得 %v", err)
	}
}

func TestAppService_SystemdUserValidation(t *testing.T) {
	svc, _ := setupAppSvc(t)
	cases := []struct {
		name string
		user string
		ok   bool
	}{
		{"空值放行 (沿用 SSH 账号)", "", true},
		{"小写字母开头", "deployer", true},
		{"下划线开头", "_svc", true},
		{"含数字", "app1", true},
		{"含连字符", "spring-user", true},
		{"两边空格被裁剪后空值", "   ", true},
		{"大写非法", "Root", false},
		{"数字开头非法", "1user", false},
		{"超过 32 位非法", "u" + string(make([]byte, 33)), false},
		{"含非法字符", "user@host", false},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validInput()
			in.AppCode = fmt.Sprintf("app-su-%d", i)
			in.SystemdUser = c.user
			_, err := svc.Create(in)
			if c.ok && err != nil {
				t.Fatalf("应通过但失败：%v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("应失败但通过")
			}
		})
	}
}

func TestAppService_SystemdUserRoundtrip(t *testing.T) {
	svc, _ := setupAppSvc(t)
	in := validInput()
	in.SystemdUser = "deployer"
	a, err := svc.Create(in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.SystemdUser != "deployer" {
		t.Fatalf("create 返回 systemd_user 应为 deployer，得 %q", a.SystemdUser)
	}
	got, err := svc.Get(a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SystemdUser != "deployer" {
		t.Fatalf("get 返回 systemd_user 应为 deployer，得 %q", got.SystemdUser)
	}

	// 更新清空：留空 ⇒ 沿用 SSH 账号
	in2 := validInput()
	in2.SystemdUser = ""
	updated, err := svc.Update(a.ID, in2)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.SystemdUser != "" {
		t.Fatalf("update 清空 systemd_user 失败：%q", updated.SystemdUser)
	}
}

// 分组切流配置已下线：兼容接收旧字段，但写入时清空，避免继续产生隐式配置。
func TestAppService_LegacyTrafficSwitchFieldsAreCleared(t *testing.T) {
	svc, _ := setupAppSvc(t)

	in := validInput()
	in.NginxHostID = 7
	in.NginxUpstreamName = "user-svc-backend"
	in.ActiveGroup = "blue"
	created, err := svc.Create(in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.NginxHostID != 0 || created.NginxUpstreamName != "" || created.ActiveGroup != "" {
		t.Fatalf("legacy traffic-switch fields should be cleared on create: %+v", created)
	}

	in.ActiveGroup = "green"
	updated, err := svc.Update(created.ID, in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.NginxHostID != 0 || updated.NginxUpstreamName != "" || updated.ActiveGroup != "" {
		t.Fatalf("legacy traffic-switch fields should be cleared on update: %+v", updated)
	}
}
