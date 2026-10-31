package internal

import (
	"net/url"
	"strings"

	"resty.dev/v3"
)

// ProtocolHook 每个 HTTP 请求真正发出之前调用一次。
// method 为 HTTP 方法，url 为尽量补全 host 的绝对地址。
type ProtocolHook func(method, url string)

// Middleware 把 ProtocolHook 包装成 resty 请求中间件。
//
// 为什么不能只上报 r.URL：
// resty 的 AddRequestMiddleware 是"插在内置 MiddlewareRequestCreate 之前"执行的，
// 此刻 r.URL 还是调用方原样传进来的相对路径（如 "/cas/login"），host 只存在于
// 客户端自己的 BaseURL 里。上层（web/classifyRequest）要按 host 区分门户与教务，
// 只有 path 会把门户请求误判成教务兜底分支，所以这里按 resty 的规则补全绝对地址。
func Middleware(hook ProtocolHook) resty.RequestMiddleware {
	return func(c *resty.Client, r *resty.Request) error {
		defer func() { _ = recover() }()
		if r != nil {
			hook(r.Method, RequestURL(c, r))
		}
		return nil
	}
}

// RequestURL 取出本次请求的绝对地址，规则与 resty 内部保持一致：
// 绝对地址原样返回，相对路径拼在客户端 BaseURL 之后。
//
// 刻意不使用 r.RawRequest：它是上一次尝试留下的 http.Request，
// 重试时 r.URL 已被重置为最初传入的值，而 RawRequest 仍是旧的那一轮。
func RequestURL(c *resty.Client, r *resty.Request) string {
	if r == nil {
		return ""
	}
	raw := r.URL
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && (u.Scheme != "" || u.Host != "") {
		return raw
	}
	base := ""
	if c != nil {
		base = c.BaseURL()
	}
	if base == "" {
		return raw
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return strings.TrimRight(base, "/") + raw
}
