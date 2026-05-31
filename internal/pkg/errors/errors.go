// Package errors 提供统一的应用错误类型与 Gin 响应壳。
//
// 用法：
//
//	return errors.Wrap(err, "INVALID_INPUT", "邮箱格式错误", 400)
//	errors.Respond(c, err)  // 在 handler 里把任意 error 转成 JSON
package errors

import (
	stderrors "errors"
	"fmt"
)

// Error 是应用层错误。Code 用于客户端识别，Message 给人看，HTTPStatus 决定状态码。
type Error struct {
	Code       string
	Message    string
	HTTPStatus int
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// Is 让 errors.Is(x, ErrNotFound) 按 Code 匹配，便于 handler 写
//
//	if errors.Is(err, errors.ErrNotFound) { ... }
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// New 构造一个新的应用错误，不带 cause。
func New(code, msg string, status int) *Error {
	return &Error{Code: code, Message: msg, HTTPStatus: status}
}

// Wrap 包装底层错误。message 可为空，此时沿用原错误信息。
func Wrap(err error, code, msg string, status int) *Error {
	return &Error{Code: code, Message: msg, HTTPStatus: status, Cause: err}
}

// As 把 err 解为 *Error，若不是则返回 nil, false。
func As(err error) (*Error, bool) {
	var e *Error
	if stderrors.As(err, &e) {
		return e, true
	}
	return nil, false
}
