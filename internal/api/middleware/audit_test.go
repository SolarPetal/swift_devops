package middleware_test

import (
	"bytes"
	"io"
	"mime/multipart"
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

// TestAudit_MultipartBodyPreserved 回归：multipart/form-data 请求
// 不能被 Audit 读 body——一旦读就会截断到 4KB，handler 解析必败、上传巨量 body 客户端看到 Network Error。
// 这里造一个 ~8KB 的 multipart 请求（远超 auditPayloadMax=4096），断言 handler 能完整读到 file 字段。
func TestAudit_MultipartBodyPreserved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAuditDB(t)

	// 8KB 文件内容，远超 audit payload 阈值
	fileContent := bytes.Repeat([]byte("A"), 8*1024)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("app_id", "1")
	_ = mw.WriteField("version_tag", "v1.0.0")
	fw, err := mw.CreateFormFile("file", "demo.jar")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(fileContent); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}

	r := gin.New()
	r.Use(middleware.Audit(db))

	var gotSize int
	var gotVersion string
	r.POST("/api/v1/artifacts/upload", func(c *gin.Context) {
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("handler ParseMultipartForm: %v", err)
			c.Status(400)
			return
		}
		gotVersion = c.PostForm("version_tag")
		fh, err := c.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			c.Status(400)
			return
		}
		f, err := fh.Open()
		if err != nil {
			t.Errorf("open file: %v", err)
			c.Status(500)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		gotSize = len(data)
		c.Status(201)
	})

	req := httptest.NewRequest("POST", "/api/v1/artifacts/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("status: %d (handler 没能解析完整 multipart——audit 又把 body 截断了？)", w.Code)
	}
	if gotVersion != "v1.0.0" {
		t.Fatalf("version_tag 字段丢失: %q", gotVersion)
	}
	if gotSize != len(fileContent) {
		t.Fatalf("文件大小不匹配 want=%d got=%d（body 被中间件截断了）", len(fileContent), gotSize)
	}

	// 审计仍应有一条记录（payload 为空可接受）
	waitForCount(t, db, 1)
	var l model.AuditLog
	db.First(&l)
	if l.ResourceType != "artifacts" {
		t.Fatalf("resource_type: %s", l.ResourceType)
	}
	if l.Payload != "" {
		t.Fatalf("multipart 不应记录 payload，得：%q", l.Payload)
	}
}
