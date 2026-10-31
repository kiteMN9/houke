package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// writeEngineError 是 web 与 engine 的唯一错误出口：状态码决定前端
// 是弹登录框（401）、提示取消（408）还是超时重试（504）。
func TestWriteEngineError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"canceled", context.Canceled, http.StatusRequestTimeout, "请求已取消"},
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, "请求超时"},
		{"unauthorized", fmt.Errorf("course list: %w", protocol.ErrUnauthorized), http.StatusUnauthorized, "登录已失效，请重新登录"},
		{"no_permission", fmt.Errorf("score: %w", protocol.ErrNoPermission), http.StatusForbidden, "no_permission"},
		{"other", errors.New("boom"), http.StatusInternalServerError, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			writeEngineError(c, tc.err)

			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("body = %s, want contains %q", rec.Body.String(), tc.body)
			}
		})
	}
}

// 端到端：学校侧返回登录页时 /quit 必须是 401，而不是 200 + 空结果，
// 也不能卡在请求里（旧实现没有 ctx/超时）。
func TestQuitMapsUnauthorized(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `<html><body><div id="tips">请先登录</div></body></html>`)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  upstream.URL + "/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)

	srv := mustNewServer(t, store)
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+"/quit",
		strings.NewReader(`{"kchId":"kch-1","jxbId":"jxb-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "登录已失效") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	// engine 的"重登一次再重试一次"：本轮已是登录态，故恰好 2 次上游请求。
	if got := hits.Load(); got != 2 {
		t.Fatalf("上游请求数 = %d, want 2", got)
	}
}

// 端到端：学校返回“无功能权限”页时，/score 与 /selected 都要给 403 +
// code=no_permission，前端据此在面板里说明原因，而不是弹红色错误。
func TestQueriesMapNoPermission(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><body><p class="error_title">无功能权限</p></body></html>`)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  upstream.URL + "/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)
	srv := mustNewServer(t, store)

	for _, path := range []string{
		"/api/accounts/" + acc.ID + "/score?xnm=2026&xqm=3",
		"/api/accounts/" + acc.ID + "/selected?xnm=2026&xqm=3",
	} {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403, body = %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "no_permission") {
			t.Fatalf("%s body = %s, want code no_permission", path, rec.Body.String())
		}
	}
}
