package protocol

import "testing"

func TestNeedsParamRefresh(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"正方原文", "加密串错误，可以清除浏览器缓存后刷新网页重试！", true},
		{"只带刷新网页", "操作失败，请刷新网页重试", true},
		{"人数已满", "该教学班人数已满", false},
		{"频率限制", "选课频率过高，请稍后重试！", false},
		{"空文案", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsParamRefresh(tc.msg); got != tc.want {
				t.Fatalf("NeedsParamRefresh(%q) = %v，期望 %v", tc.msg, got, tc.want)
			}
		})
	}
}

// 并发请求同时撞上参数过期时，只有第一个 Take 能拿到 true：其余的直接返回，
// 等下一次请求自然用上新参数，避免同时抓两遍首页。
func TestParamRefreshFlagIsSingleConsumer(t *testing.T) {
	a := &APIClient{}
	if a.TakeParamRefresh() {
		t.Fatal("未标记时 Take 应为 false")
	}
	a.MarkParamRefresh()
	if !a.TakeParamRefresh() {
		t.Fatal("标记后第一次 Take 应为 true")
	}
	if a.TakeParamRefresh() {
		t.Fatal("取过一次之后应为 false")
	}

	// nil client 不 panic：调用方可能是测试里手搓的 Engine。
	var nilClient *APIClient
	nilClient.MarkParamRefresh()
	if nilClient.TakeParamRefresh() {
		t.Fatal("nil client 的 Take 应为 false")
	}
}
