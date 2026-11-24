// Package errs holds the errors a client is meant to see. Services return them;
// handlers pass any error to resp.Fail, which turns an *Error into its status,
// code and message and everything else into a logged 500.
package errs

import "net/http"

const (
	CodeInternal        = 1000
	CodeInvalid         = 1001
	CodeUnauthorized    = 1002
	CodeForbidden       = 1003
	CodeNotFound        = 1004
	CodeConflict        = 1005
	CodeTooLarge        = 1006
	CodeTooManyRequests = 1007
	CodeUnavailable     = 1008
	CodeNotImplemented  = 1009
)

type Error struct {
	Status int
	Code   int32
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func Invalid(msg string) *Error      { return &Error{http.StatusBadRequest, CodeInvalid, msg} }
func Unauthorized(msg string) *Error { return &Error{http.StatusUnauthorized, CodeUnauthorized, msg} }
func Forbidden(msg string) *Error    { return &Error{http.StatusForbidden, CodeForbidden, msg} }
func NotFound(msg string) *Error     { return &Error{http.StatusNotFound, CodeNotFound, msg} }
func Conflict(msg string) *Error     { return &Error{http.StatusConflict, CodeConflict, msg} }
func Unavailable(msg string) *Error {
	return &Error{http.StatusServiceUnavailable, CodeUnavailable, msg}
}

var (
	TooLarge        = &Error{http.StatusRequestEntityTooLarge, CodeTooLarge, "请求体过大"}
	TooManyRequests = &Error{http.StatusTooManyRequests, CodeTooManyRequests, "请求过于频繁，请稍后再试"}
	Internal        = &Error{http.StatusInternalServerError, CodeInternal, "服务器内部错误"}
	// NotImplemented marks a generated handler that no lane has filled yet.
	NotImplemented = &Error{http.StatusNotImplemented, CodeNotImplemented, "该功能尚未实现"}

	BadRequest   = Invalid("请求参数错误")
	NotLoggedIn  = Unauthorized("未登录或令牌无效")
	TokenInvalid = Unauthorized("令牌无效")
	HostDenied   = Forbidden("Host 不被允许")
	CrossSite    = Forbidden("拒绝跨站请求")
	NoSuchRoute  = NotFound("接口不存在")
	NoSession    = NotFound("整理任务不存在")
)
