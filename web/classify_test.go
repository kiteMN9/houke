package web

import (
	"strings"
	"testing"

	"school_sdk/protocol/config"
)

// 用例里的地址一律由 endpoints.go 的常量拼出来：路径写死的话，教务换一次地址
// 实现和测试会一起漂——要么测不到真实路径，要么测试先红。
func TestClassifyRequest(t *testing.T) {
	const base = "https://jwglxt.ycit.edu.cn"
	cases := []struct {
		url     string
		stage   string
		logWant bool
	}{
		{base + config.ChooseCourseIndex + "?gnmkdm=" + config.GndmmkXsxk, "init.index", true},
		{base + config.ChooseCourseTabParams, "init.prepare", true},
		{base + config.ChooseCourseCourseList, "init.courses", true},
		{base + config.ChooseCourseCourseDetail, "init.detail", true},
		{base + config.CourseSelectedList, "init.selected", true},
		{base + config.ChooseCourse, "course.choose", true},
		{base + config.QuitCourse, "course.quit", true},
		// 登录端点不再由 URL 分类：阶段由 protocol 上报，这里只当未知请求计数
		{base + "/xtgl/login_slogin.html", "http:/xtgl/login_slogin.html", false},
		// 未知端点：不应该写日志，但要有阶段文案
		{base + "/some/unknown_thing.html", "http:/some/unknown_thing.html", false},
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
		"https://jwglxt.ycit.edu.cn" + config.ChooseCourseIndex + "?token=SECRET&password=PW")
	for _, s := range []string{stage, text, detail} {
		if strings.Contains(s, "SECRET") || strings.Contains(s, "PW") || strings.Contains(s, "?") {
			t.Errorf("阶段信息泄漏了查询串: %q", s)
		}
	}
}
