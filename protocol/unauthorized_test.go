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

// unauthorizedPage 是"需要重新登录"的最小页面：带 #tips 且不含当前账号，
// utils.UserIsLogin 会判为未登录（真实的登录页、被踢页也走这条判定）。
const unauthorizedPage = `<html><body><div id="tips">请先登录</div></body></html>`

// newUnauthorizedServer 起一个永远返回登录页的服务器，并统计请求数：
// 协议层现在应该直接报 ErrUnauthorized，而不是自己再发一次登录请求。
func newUnauthorizedServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, unauthorizedPage)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestCourseListReportsUnauthorizedWithoutRelogin(t *testing.T) {
	srv, hits := newUnauthorizedServer(t)
	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	list, err := api.GetCourseList(ctx, &APIConfig{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("GetCourseList 错误 = %v，期望 ErrUnauthorized", err)
	}
	if list != nil {
		t.Fatalf("未登录时不应返回课程列表，得到 %d 条", len(list))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("未登录应立即返回，实际耗时 %v", elapsed)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("未登录后不应再发请求（协议层不再自动重登），共 %d 次", got)
	}
}

func TestQuitCourseReportsUnauthorized(t *testing.T) {
	srv, hits := newUnauthorizedServer(t)
	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)

	ok, _, err := api.QuitCourse(context.Background(), IndexInfo{}, "jxb-1", "kch-1")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("QuitCourse 错误 = %v，期望 ErrUnauthorized", err)
	}
	if ok {
		t.Fatal("未登录时退课不应成功")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("退课遇到未登录后不应重试，共 %d 次请求", got)
	}
}

// GetIndexParams 的错误要能被 errors.As 成 StageError，同时 errors.Is 到
// ErrUnauthorized：Web 端据此区分"未登录"和"不在选课阶段"。
func TestIndexParamsReportsUnauthorizedAsStageError(t *testing.T) {
	srv, _ := newUnauthorizedServer(t)
	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)

	err := api.GetIndexParams(context.Background(), &APIConfig{}, IndexOptions{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("GetIndexParams 错误 = %v，期望 ErrUnauthorized", err)
	}
	var stage *StageError
	if !errors.As(err, &stage) {
		t.Fatalf("错误类型应为 *StageError，得到 %T", err)
	}
	if stage.Op != "index" {
		t.Fatalf("StageError.Op = %q，期望 index", stage.Op)
	}
}

// 探测类接口（CheckSession）只回答"还在线吗"，掉线时把登录态清掉，
// 但不能自己发起重登。
func TestCheckSessionProbeDoesNotRelogin(t *testing.T) {
	srv, hits := newUnauthorizedServer(t)
	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)

	if api.CheckSession(context.Background()) {
		t.Fatal("登录页应被判定为已掉线")
	}
	if api.LoggedIn() {
		t.Fatal("掉线后应清除登录态")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("探测接口不应重登，共 %d 次请求", got)
	}
}
