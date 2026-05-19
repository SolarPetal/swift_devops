package middleware_test

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"swift-devops/internal/api/middleware"
	"swift-devops/internal/model"
)

func setupAuditDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// waitForCount 轮询直到审计表中条数达到 want，或超时。
func waitForCount(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		db.Model(&model.AuditLog{}).Count(&n)
		if n == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got int64
	db.Model(&model.AuditLog{}).Count(&got)
	t.Fatalf("等待审计条数 want=%d got=%d", want, got)
}

func TestAudit_WriteOperationLogged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAuditDB(t)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user", "alice"); c.Next() })
	r.Use(middleware.Audit(db))
	r.POST("/api/v1/hosts", func(c *gin.Context) { c.Status(201) })

	body := []byte(`{"name":"h1","password":"secret123","private_key":"xxxxxx"}`)
	req := httptest.NewRequest("POST", "/api/v1/hosts", bytes.NewReader(body))
	r.ServeHTTP(httptest.NewRecorder(), req)

	waitForCount(t, db, 1)

	var l model.AuditLog
	db.First(&l)
	if l.Actor != "alice" {
		t.Fatalf("actor: %s", l.Actor)
	}
	if l.ResourceType != "hosts" {
		t.Fatalf("resource_type: %s", l.ResourceType)
	}
	if l.Action != "POST /api/v1/hosts" {
		t.Fatalf("action: %s", l.Action)
	}
	if !strings.Contains(l.Payload, `"password":"***"`) {
		t.Fatalf("password 应脱敏：%s", l.Payload)
	}
	if !strings.Contains(l.Payload, `"private_key":"***"`) {
		t.Fatalf("private_key 应脱敏：%s", l.Payload)
	}
	if !strings.Contains(l.Payload, `"name":"h1"`) {
		t.Fatalf("普通字段应保留：%s", l.Payload)
	}
}

func TestAudit_ResourceIDExtracted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAuditDB(t)

	r := gin.New()
	r.Use(middleware.Audit(db))
	r.PUT("/api/v1/hosts/:id", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("PUT", "/api/v1/hosts/42", bytes.NewReader([]byte(`{}`)))
	r.ServeHTTP(httptest.NewRecorder(), req)

	waitForCount(t, db, 1)
	var l model.AuditLog
	db.First(&l)
	if l.ResourceID != "42" {
		t.Fatalf("resource_id 应为 42，得 %s", l.ResourceID)
	}
}

func TestAudit_ReadOperationSkipped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAuditDB(t)

	r := gin.New()
	r.Use(middleware.Audit(db))
	r.GET("/api/v1/hosts", func(c *gin.Context) { c.Status(200) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/hosts", nil))

	time.Sleep(100 * time.Millisecond)
	var n int64
	db.Model(&model.AuditLog{}).Count(&n)
	if n != 0 {
		t.Fatalf("GET 不应触发审计，实际 %d", n)
	}
}
