package service_test

import (
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

func setupSvc(t *testing.T) (*service.HostService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.Host{}, &model.Deployment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	aes, err := cryptopkg.NewAESGCM(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	return service.NewHostService(db, aes), db
}

func TestHostService_CreateAndGet(t *testing.T) {
	svc, db := setupSvc(t)
	out, err := svc.Create(service.HostInput{
		Name: "h1", IP: "10.0.0.1", AuthType: "password",
		Username: "root", Password: "p4ss",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !out.HasSecret {
		t.Fatal("HasSecret 应为 true")
	}
	var h model.Host
	db.First(&h, out.ID)
	if h.Secret == "" {
		t.Fatal("DB 中 Secret 应非空")
	}
	if h.Secret == "p4ss" {
		t.Fatal("Secret 不应是明文")
	}
	got, err := svc.Get(out.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.IP != "10.0.0.1" {
		t.Fatalf("ip: %s", got.IP)
	}
}

func TestHostService_DefaultPort22(t *testing.T) {
	svc, _ := setupSvc(t)
	out, _ := svc.Create(service.HostInput{
		Name: "h1", IP: "1.1.1.1", AuthType: "password",
		Username: "root", Password: "p",
	})
	if out.Port != 22 {
		t.Fatalf("默认 port 应 22，得 %d", out.Port)
	}
}

func TestHostService_ValidationFailures(t *testing.T) {
	svc, _ := setupSvc(t)
	cases := []struct {
		name string
		in   service.HostInput
	}{
		{"password 类型缺密码", service.HostInput{
			Name: "h", IP: "1.1.1.1", AuthType: "password", Username: "u",
		}},
		{"key 类型缺私钥", service.HostInput{
			Name: "h", IP: "1.1.1.1", AuthType: "key", Username: "u",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.Create(c.in); err == nil {
				t.Fatal("应失败")
			}
		})
	}
}

func TestHostService_UpdatePreservesSecretWhenEmpty(t *testing.T) {
	svc, db := setupSvc(t)
	h, _ := svc.Create(service.HostInput{
		Name: "h1", IP: "1.1.1.1", AuthType: "password",
		Username: "root", Password: "old",
	})
	var pre model.Host
	db.First(&pre, h.ID)
	preSecret := pre.Secret

	_, err := svc.Update(h.ID, service.HostInput{
		Name: "h1-renamed", IP: "1.1.1.1", AuthType: "password", Username: "root",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	var post model.Host
	db.First(&post, h.ID)
	if post.Secret != preSecret {
		t.Fatal("Password 为空时 Secret 应保留")
	}
	if post.Name != "h1-renamed" {
		t.Fatalf("Name 应更新")
	}
}

func TestHostService_UpdateNewSecretReencryptsAndResetsHostKey(t *testing.T) {
	svc, db := setupSvc(t)
	h, _ := svc.Create(service.HostInput{
		Name: "h", IP: "1.1.1.1", AuthType: "password",
		Username: "root", Password: "old",
	})
	// 模拟已学到 host key
	db.Model(&model.Host{}).Where("id = ?", h.ID).Update("host_key", "FAKE_KEY")
	var pre model.Host
	db.First(&pre, h.ID)
	preSecret := pre.Secret

	_, err := svc.Update(h.ID, service.HostInput{
		Name: "h", IP: "1.1.1.1", AuthType: "password",
		Username: "root", Password: "newpass",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	var post model.Host
	db.First(&post, h.ID)
	if post.Secret == preSecret {
		t.Fatal("Secret 应被新加密")
	}
	if post.HostKey != "" {
		t.Fatal("凭证改了应清空 HostKey")
	}
}

func TestHostService_GetNotFound(t *testing.T) {
	svc, _ := setupSvc(t)
	_, err := svc.Get(9999)
	if err == nil {
		t.Fatal("应失败")
	}
	if !stderrors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("应 ErrNotFound，得 %v", err)
	}
}

func TestHostService_DeleteAndDeleteAgain(t *testing.T) {
	svc, _ := setupSvc(t)
	h, _ := svc.Create(service.HostInput{
		Name: "h", IP: "1.1.1.1", AuthType: "password",
		Username: "root", Password: "p",
	})
	if err := svc.Delete(h.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.Delete(h.ID); err == nil {
		t.Fatal("二次删除应失败")
	}
}

// 加密往返：手动用同 key 解密 Host.Secret，验证含 password 明文
func TestHostService_SecretRoundTrip(t *testing.T) {
	svc, db := setupSvc(t)
	_, _ = svc.Create(service.HostInput{
		Name: "h", IP: "1.1.1.1", AuthType: "password",
		Username: "root", Password: "secret123", Passphrase: "ph",
	})
	var h model.Host
	db.First(&h)

	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	aes, _ := cryptopkg.NewAESGCM(key)
	plain, err := aes.Decrypt(h.Secret)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var blob struct {
		V string `json:"v"`
		P string `json:"p,omitempty"`
	}
	if err := json.Unmarshal([]byte(plain), &blob); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if blob.V != "secret123" {
		t.Fatalf("V: %s", blob.V)
	}
	if blob.P != "ph" {
		t.Fatalf("P: %s", blob.P)
	}
}
