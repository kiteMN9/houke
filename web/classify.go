package web

import (
	"net/url"
	"strings"
)

// 这一组函数把 client.ProtocolHook 上报的原始请求 URL 翻译成人类可读的任务阶段。
// 只做纯字符串判断，不访问网络，便于单测覆盖。
//
// 登录 / 门户登录不在这里判断：它们的阶段由 protocol 自己上报
// （见 login_stage.go），这里只剩初始化、选课，以及未知端点的兜底。

// classifyRequest 把一次 HTTP 请求翻译成人类可读阶段。
// logEvent=false 表示"只更新当前阶段、不写日志"，用于高频/未知请求。
func classifyRequest(rawURL string) (stage, text, detail string, logEvent bool) {
	path := requestPath(rawURL)
	if stage, text, ok := classifyChoose(path); ok {
		return stage, text, "", true
	}
	if path == "" {
		// 连路径都没解析出来：只让请求计数动起来
		return "", "", "", false
	}
	return "http:" + path, "正在请求 " + path, "", false
}

// requestPath 只取 path，避免把查询串里的敏感信息带进事件流。
func requestPath(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		return u.Path
	}
	// 解析失败时退化为手工切分，至少不要原样透出整条 URL。
	s := rawURL
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.Index(s, "/"); j >= 0 {
			return s[j:]
		}
		return "/"
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// classifyChoose 选课/初始化相关端点（初始化与选课阶段会密集访问）。
func classifyChoose(path string) (stage, text string, ok bool) {
	switch {
	case strings.HasSuffix(path, "/zzxkyzb_cxZzxkYzbIndex.html"):
		return "init.index", "正在打开选课页", true
	case strings.HasSuffix(path, "/zzxkyzb_cxZzxkYzbDisplay.html"):
		return "init.prepare", "正在抓取选课参数", true
	case strings.HasSuffix(path, "/zzxkyzb_cxZzxkYzbPartDisplay.html"):
		return "init.courses", "正在加载课程列表", true
	case strings.HasSuffix(path, "/zzxkyzb_cxJxbWithKchZzxkYzb.html"):
		return "init.detail", "正在加载教学班详情", true
	case strings.HasSuffix(path, "/zzxkyzb_cxZzxkYzbChoosedDisplay.html"):
		return "init.selected", "正在查询已选课程", true
	case strings.HasSuffix(path, "/zzxkyzbjk_xkBcZyZzxkYzb.html"):
		return "course.choose", "正在提交选课请求", true
	case strings.HasSuffix(path, "/zzxkyzb_tuikBcZzxkYzb.html"):
		return "course.quit", "正在提交退课请求", true
	}
	return "", "", false
}
