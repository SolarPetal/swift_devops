package errors

import "net/http"

// 预定义错误码。Handler 里直接 return 或 errors.Is 比较。
var (
	// 4xx
	ErrBadRequest         = New("BAD_REQUEST", "请求参数错误", http.StatusBadRequest)
	ErrUnauthorized       = New("UNAUTHORIZED", "未授权", http.StatusUnauthorized)
	ErrInvalidCredentials = New("INVALID_CREDENTIALS", "用户名或密码错误", http.StatusUnauthorized)
	ErrForbidden          = New("FORBIDDEN", "无权访问", http.StatusForbidden)
	ErrNotFound           = New("NOT_FOUND", "资源不存在", http.StatusNotFound)
	ErrConflict           = New("CONFLICT", "资源已存在或状态冲突", http.StatusConflict)
	ErrTooManyRequests    = New("TOO_MANY_REQUESTS", "请求过于频繁", http.StatusTooManyRequests)

	// 5xx
	ErrInternal    = New("INTERNAL", "服务器内部错误", http.StatusInternalServerError)
	ErrUnavailable = New("UNAVAILABLE", "服务暂不可用", http.StatusServiceUnavailable)
)
