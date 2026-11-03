package web

import (
	"context"
	"errors"
	"net/http"

	"school_sdk/engine"
	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// API 错误码是前后端之间的稳定协议：前端按 code 分支，不再靠 HTTP 状态码
// 猜语义。最典型的是 401——"登录失败"和"会话已失效"都是 401，但前者不能
// 触发"把徽标切成未登录 + 提示重新登录"，否则输错密码会被显示成掉线。
const (
	codeUnauthorized = "unauthorized" // 未登录 / 会话已失效，前端统一按"需要重新登录"处理
	codeLoginFailed  = "login_failed" // 这次登录动作失败（密码错、流程失败），不改会话失效语义
	codeNoPermission = "no_permission"
	codeBusy         = "busy"          // 已有任务在跑（登录 / 初始化 / 自动选课）
	codeSessionReset = "session_reset" // 初始化期间会话被登出 / 重登
	codeCancelled    = "cancelled"
	codeTimeout      = "timeout"
	codeInternal     = "internal"
)

// writeAPIError 是所有业务错误的统一出口。
// code 可以留空（参数校验之类前端只展示 error 的错误），但会话相关的错误
// 必须带 code，前端才不会把"登录失败"和"登录失效"混为一谈。
func writeAPIError(c *gin.Context, status int, code, msg string) {
	body := gin.H{"error": msg}
	if code != "" {
		body["code"] = code
	}
	c.JSON(status, body)
}

// writeUnauthorized 统一 401 + unauthorized：前端看到它就把账号标成未登录。
func writeUnauthorized(c *gin.Context, msg string) {
	if msg == "" {
		msg = "登录已失效，请重新登录"
	}
	writeAPIError(c, http.StatusUnauthorized, codeUnauthorized, msg)
}

// writeEngineError 把 engine 返回的错误映射成前端能处理的 HTTP 语义。
// 所有 handler 都走这里，避免某个入口把 ErrUnauthorized 当成 500 返回：
// 那样前端只能弹一条"初始化失败"，登录态却没有任何变化。
func writeEngineError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		writeAPIError(c, http.StatusRequestTimeout, codeCancelled, "请求已取消")
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(c, http.StatusGatewayTimeout, codeTimeout, "请求超时")
	case errors.Is(err, protocol.ErrUnauthorized):
		writeUnauthorized(c, "登录已失效，请重新登录")
	case errors.Is(err, protocol.ErrNoPermission):
		writeAPIError(c, http.StatusForbidden, codeNoPermission, "该账号无此功能权限")
	case errors.Is(err, engine.ErrSessionReset):
		writeAPIError(c, http.StatusConflict, codeSessionReset, "会话已重置，请重新初始化")
	default:
		writeAPIError(c, http.StatusInternalServerError, codeInternal, err.Error())
	}
}
