package protocol

import (
	"errors"
	"fmt"
)

// ErrUnauthorized 表示教务系统认为当前会话已经失效：请求被重定向到登录页、
// 返回未登录提示，或者 CSRF/票据失效。
//
// 协议层不再自己偷偷重登：要不要重登、重登几次、失败后怎么提示用户，
// 是 engine / tui / web 的策略。protocol 只负责把"登录已失效"这件事
// 用一个可判断的错误告诉调用方。
var ErrUnauthorized = errors.New("protocol: 登录已失效")

// ErrNoPermission 表示教务系统拒绝了这个功能：HTTP 200，但返回的是一张
// “无功能权限”的 HTML 错误页（不是 JSON 数据，也不是登录页）。
//
// 这不是会话失效，重试也没用；公共账号、没有开通的模块都会出现这种情况。
// 协议层把它单独报出来，Web 端据此提示“该账号无此功能权限”，
// 而不是当成解析失败弹一个红色错误。
var ErrNoPermission = errors.New("protocol: 无功能权限")

// StageError 是一次协议操作在某个阶段失败时返回的错误。
//
// Message 是可以直接展示给用户的中文原因。以前这些原因只能通过返回值是
// string 来传递（空串=成功），调用方既没法 errors.Is，也没法区分取消/超时。
type StageError struct {
	Op      string // 阶段：index / listPre / list / detail / choose / quit / score ...
	Message string
	Err     error
}

func (e *StageError) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return e.Op + ": " + e.Message + ": " + e.Err.Error()
	case e.Message != "":
		return e.Op + ": " + e.Message
	case e.Err != nil:
		return e.Op + ": " + e.Err.Error()
	default:
		return e.Op
	}
}

func (e *StageError) Unwrap() error { return e.Err }

// stagef 构造一个只带消息的 StageError。
func stagef(op, format string, args ...any) error {
	return &StageError{Op: op, Message: fmt.Sprintf(format, args...)}
}
