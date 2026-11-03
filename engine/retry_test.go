package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

const unauthorizedPage = `<html><body><div id="tips">请先登录</div></body></html>`

func newEngineForServer(t *testing.T, srv *httptest.Server) *Engine {
	t.Helper()
	eng, err := New(validSettings(srv.URL+"/"), config.NewSession(), LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng
}

// 会话仍标记为已登录、但学校侧已失效时，engine 只重登并重试一次：
// 不能无限重试，也不能把错误吞掉。
func TestGetCourseListRetriesOnceOnUnauthorized(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// 登录页请求回 302 到首页：protocol 据此判定"已经是登录态"，
		// 让 engine 的重登一次立刻成功，不用在测试里跑完整登录流程。
		if r.URL.Path == "/xtgl/login_slogin.html" {
			http.Redirect(w, r, "/xtgl/index_initMenu.html", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, unauthorizedPage)
	}))
	defer srv.Close()

	eng := newEngineForServer(t, srv)
	eng.TestSetLoggedIn(true)

	_, err := eng.GetCourseList(context.Background())
	if !errors.Is(err, protocol.ErrUnauthorized) {
		t.Fatalf("错误 = %v，期望 ErrUnauthorized", err)
	}
	// 首轮请求把登录态记成 false → authorize 真的重登一次 → 再重试一次。
	// 3 次 = 首轮 + 登录页 + 重试；仍然是"只重试一次"，不会无限重登。
	if got := hits.Load(); got != 3 {
		t.Fatalf("engine 应只重试一次（首轮 + 重登 + 重试 = 3 次请求），实际 %d 次", got)
	}
}

// 关闭自动重登后，业务请求发现 401 只把错误交回上层，不再偷偷登录、也不重试。
func TestAutoReloginOffDoesNotRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, unauthorizedPage)
	}))
	defer srv.Close()

	eng := newEngineForServer(t, srv)
	eng.SetAutoRelogin(false)
	eng.TestSetLoggedIn(true)

	_, err := eng.GetCourseList(context.Background())
	if !errors.Is(err, protocol.ErrUnauthorized) {
		t.Fatalf("错误 = %v，期望 ErrUnauthorized", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("关闭自动重登后不应重试/重登，实际 %d 次请求", got)
	}
}

// 非授权错误不该触发重登：ctx 已取消时一个请求都不该发。
func TestGetCourseListDoesNotRetryOnContextError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, unauthorizedPage)
	}))
	defer srv.Close()

	eng := newEngineForServer(t, srv)
	eng.TestSetLoggedIn(true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := eng.GetCourseList(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("错误 = %v，期望 context.Canceled", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("ctx 已取消时不应发请求，实际 %d 次", got)
	}
}

// 已登录时 authorize 不应额外发请求（否则每次未授权都会多打一次登录）。
func TestAuthorizeDoesNotLoginWhenLoggedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("已登录时不该有请求，收到 %s", r.URL.Path)
	}))
	defer srv.Close()

	eng := newEngineForServer(t, srv)
	eng.TestSetLoggedIn(true)

	if !eng.authorize(context.Background()) {
		t.Fatal("已登录时 authorize 应返回 true")
	}
}
