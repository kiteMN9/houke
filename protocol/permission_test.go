package protocol

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 学校拒绝功能权限时返回的是 HTTP 200 + 这张 HTML 错误页，不是 JSON；
// 页面里没有 #tips，所以 LoginCheck 会认为会话仍在，必须单独识别。
const noPermissionPage = `<!doctype html><html><head><title>错误提示</title>` +
	`<link rel="stylesheet" href="/zftal-ui-v5-1.0.2/assets/css/error.css"></head>` +
	`<body><div class="error_v5"><p class="error_title">无功能权限</p></div></body></html>`

func TestIsNoPermission(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"学校无权限页", noPermissionPage, true},
		{"空 JSON 数组", `[]`, false},
		{"成绩 JSON", `{"items":[]}`, false},
		{"登录页", `<html><body><div id="tips">用户名或密码不正确</div></body></html>`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isNoPermission(c.body); got != c.want {
				t.Fatalf("isNoPermission(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

// newNoPermissionServer 起一个永远返回“无功能权限”页的服务器并统计请求数。
func newNoPermissionServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, noPermissionPage)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// 成绩无功能权限：报 ErrNoPermission（不是解析失败、不是掉线），且不重试。
func TestWebGetScoreReportsNoPermission(t *testing.T) {
	srv, hits := newNoPermissionServer(t)
	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	list, err := api.WebGetScore(ctx, "2026", "3")
	if !errors.Is(err, ErrNoPermission) {
		t.Fatalf("WebGetScore 错误 = %v，期望 ErrNoPermission", err)
	}
	if list != nil {
		t.Fatalf("无权限时不应返回成绩，得到 %d 条", len(list))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("无权限不该重试，共 %d 次请求", got)
	}
}

// 已选课程同样要能报 ErrNoPermission（虽然当前 ZJUT 账号不触发）。
func TestGetSelectedReportsNoPermission(t *testing.T) {
	srv, hits := newNoPermissionServer(t)
	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)

	list, err := api.GetSelected(context.Background(), "2026", "3")
	if !errors.Is(err, ErrNoPermission) {
		t.Fatalf("GetSelected 错误 = %v，期望 ErrNoPermission", err)
	}
	if list != nil {
		t.Fatalf("无权限时不应返回已选课程，得到 %d 条", len(list))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("无权限不该重试，共 %d 次请求", got)
	}
}
