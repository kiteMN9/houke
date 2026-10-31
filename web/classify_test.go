package web

import (
	"strings"
	"testing"
)

func TestClassifyRequest(t *testing.T) {
	cases := []struct {
		url     string
		stage   string
		logWant bool
	}{
		{"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzb_cxZzxkYzbIndex.html?gnmkdm=N253512", "init.index", true},
		{"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzb_cxZzxkYzbDisplay.html", "init.prepare", true},
		{"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzb_cxJxbWithKchZzxkYzb.html", "init.detail", true},
		{"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzb_cxZzxkYzbChoosedDisplay.html", "init.selected", true},
		{"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzbjk_xkBcZyZzxkYzb.html", "course.choose", true},
		{"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzb_tuikBcZzxkYzb.html", "course.quit", true},
		// 登录端点不再由 URL 分类：阶段由 protocol 上报，这里只当未知请求计数
		{"https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html", "http:/xtgl/login_slogin.html", false},
		// 未知端点：不应该写日志，但要有阶段文案
		{"https://jwglxt.ycit.edu.cn/some/unknown_thing.html", "http:/some/unknown_thing.html", false},
		{"", "", false},
	}

	for _, c := range cases {
		stage, text, _, logEvent := classifyRequest(c.url)
		if stage != c.stage {
			t.Errorf("classifyRequest(%q).stage = %q, want %q", c.url, stage, c.stage)
		}
		if logEvent != c.logWant {
			t.Errorf("classifyRequest(%q).logEvent = %v, want %v", c.url, logEvent, c.logWant)
		}
		if stage != "" && text == "" {
			t.Errorf("classifyRequest(%q) 返回了阶段但没有文案", c.url)
		}
	}
}

// 阶段文案里不能出现查询串，避免把敏感参数带进事件流。
func TestClassifyRequestDoesNotLeakQuery(t *testing.T) {
	stage, text, detail, _ := classifyRequest(
		"https://jwglxt.ycit.edu.cn/xsxk/zzxkyzb_cxZzxkYzbIndex.html?token=SECRET&password=PW")
	for _, s := range []string{stage, text, detail} {
		if strings.Contains(s, "SECRET") || strings.Contains(s, "PW") || strings.Contains(s, "?") {
			t.Errorf("阶段信息泄漏了查询串: %q", s)
		}
	}
}
