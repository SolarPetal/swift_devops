package errors

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestError_FormatAndUnwrap(t *testing.T) {
	base := stderrors.New("io err")
	e := Wrap(base, "INTERNAL", "x", 500)
	if !stderrors.Is(e, base) {
		t.Fatal("errors.Is 应能透过 Unwrap 找到 cause")
	}
	if !strings.Contains(e.Error(), "io err") {
		t.Fatalf("错误信息应包含 cause：%s", e.Error())
	}
}

func TestError_Is_ByCode(t *testing.T) {
	if !stderrors.Is(ErrNotFound, ErrNotFound) {
		t.Fatal("同一实例应匹配")
	}
	other := New("NOT_FOUND", "其他文案", 404)
	if !stderrors.Is(other, ErrNotFound) {
		t.Fatal("相同 Code 不同实例也应匹配")
	}
	if stderrors.Is(ErrBadRequest, ErrNotFound) {
		t.Fatal("不同 Code 不应匹配")
	}
}

func TestAs(t *testing.T) {
	if _, ok := As(ErrBadRequest); !ok {
		t.Fatal("As 应识别本类型")
	}
	if _, ok := As(stderrors.New("plain")); ok {
		t.Fatal("As 不应识别普通 error")
	}
}

func TestRespond_KnownError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/x", nil)

	Respond(c, ErrInvalidCredentials)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want %d", w.Code, http.StatusUnauthorized)
	}
	body := w.Body.String()
	if !strings.Contains(body, "INVALID_CREDENTIALS") {
		t.Fatalf("响应体应含 code，得 %s", body)
	}
	if !strings.Contains(body, "用户名或密码错误") {
		t.Fatalf("响应体应含 message，得 %s", body)
	}
}

func TestRespond_UnknownError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/x", nil)

	Respond(c, stderrors.New("random db error"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("unknown error 应映射 500，得 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "INTERNAL") {
		t.Fatalf("应含 INTERNAL: %s", w.Body.String())
	}
}

func TestRespond_NilNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	Respond(c, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("nil err 应不写响应，状态默认 200，得 %d", w.Code)
	}
}
