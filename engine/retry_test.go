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
		_, _ = io.WriteString(w, unauthorizedPage)
	}))
	defer srv.Close()

	eng := newEngineForServer(t, srv)
	eng.TestSetLoggedIn(true)

	_, err := eng.GetCourseList(context.Background())
	if !errors.Is(err, protocol.ErrUnauthorized) {
		t.Fatalf("错误 = %v，期望 ErrUnauthorized", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("engine 应只重试一次（首轮 + 重试 = 2 次请求），实际 %d 次", got)
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
