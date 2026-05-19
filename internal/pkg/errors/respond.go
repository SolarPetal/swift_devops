package errors

import (
	stderrors "errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Respond 把 err 序列化为统一 JSON 写回 client。
// 已知 *Error 用其 HTTPStatus；其它一律视为 500 并落 slog.Error。
//
// 响应体格式：
//
//	{"error": {"code": "INVALID_CREDENTIALS", "message": "用户名或密码错误"}}
func Respond(c *gin.Context, err error) {
	if err == nil {
		return
	}
	var ae *Error
	if !stderrors.As(err, &ae) {
		ae = Wrap(err, "INTERNAL", "服务器内部错误", http.StatusInternalServerError)
	}
	if ae.HTTPStatus >= 500 {
		slog.Error("request failed",
			"code", ae.Code,
			"msg", ae.Message,
			"cause", ae.Cause,
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
		)
	}
	c.AbortWithStatusJSON(ae.HTTPStatus, gin.H{
		"error": gin.H{
			"code":    ae.Code,
			"message": ae.Message,
		},
	})
}
