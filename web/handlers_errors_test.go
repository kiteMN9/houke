package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/engine"
	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// writeEngineError 是 web 与 engine 的唯一错误出口：状态码决定前端的粗分类，
// code 决定具体语义（401 既可能是"登录失败"也可能是"会话失效"，不能只看状态码）。
func TestWriteEngineError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		err  error
		code int
		biz  string
		body string
	}{
		{"canceled", context.Canceled, http.StatusRequestTimeout, "cancelled", "请求已取消"},
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, "timeout", "请求超时"},
		{"unauthorized", fmt.Errorf("course list: %w", protocol.ErrUnauthorized), http.StatusUnauthorized, "unauthorized", "登录已失效，请重新登录"},
		{"no_permission", fmt.Errorf("score: %w", protocol.ErrNoPermission), http.StatusForbidden, "no_permission", "该账号无此功能权限"},
		{"session_reset", fmt.Errorf("init: %w", engine.ErrSessionReset), http.StatusConflict, "session_reset", "会话已重置"},
		{"other", errors.New("boom"), http.StatusInternalServerError, "internal", "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			writeEngineError(c, tc.err)

			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.code, rec.Body.String())
			}
			var got struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
			}
			if got.Code != tc.biz {
				t.Fatalf("code = %q, want %q", got.Code, tc.biz)
			}
			if !strings.Contains(got.Error, tc.body) {
				t.Fatalf("error = %q, want contains %q", got.Error, tc.body)
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
		// 登录页请求回 302 到首页：protocol 据此判定"已经是登录态"，
		// 让 engine 的重登一次立刻成功，不用在测试里跑完整登录流程。
		if r.URL.Path == "/xtgl/login_slogin.html" {
			http.Redirect(w, r, "/xtgl/index_initMenu.html", http.StatusFound)
			return
		}
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
	// engine 的"重登一次再重试一次"：第一次退课失败后登录态已被记成 false，
	// authorize 真的去重登（登录页 → 302 首页，1 次请求），再重试一次退课。
	// 3 次 = 退课 + 登录页 + 重试退课。
	if got := hits.Load(); got != 3 {
		t.Fatalf("上游请求数 = %d, want 3", got)
	}
}

// 端到端：初始化时学校返回登录页，必须回 401 + code=unauthorized，
// 并且 /status 的 loggedIn 立刻变成 false。
//
// 回归点：以前 init 只把 ErrUnauthorized 当普通错误回 500，protocol 也不会
// 因为业务响应更新登录态，前端弹出"初始化失败"后徽标仍停在"已登录"，
// 只能靠用户自己 Ping 或刷新页面才发现掉线。
func TestInitUnauthorizedRefreshesLoginState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><body><div id="tips">请先登录</div></body></html>`)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	// 这里只验证"发现失效后 401 且登录态刷新"，关闭自动重登避免测试去跑登录流程。
	off := false
	acc, err := store.Create(&Account{
		Name:        "n",
		Username:    "u",
		BaseURL:     upstream.URL + "/",
		AutoRelogin: &off,
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
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+"/init", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("init status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string
		Code  string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if body.Code != "unauthorized" {
		t.Fatalf("init code = %q, want unauthorized (body=%s)", body.Code, rec.Body.String())
	}

	// 协议层已经按响应把登录态记成"未登录"，/status 必须跟着变。
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"loggedIn":false`) {
		t.Fatalf("掉线后 /status 仍报已登录: %s", rec.Body.String())
	}
}

// 开启自动重登时，初始化发现会话失效会重登一次并重试一次初始化。
func TestInitAutoReloginRetriesOnce(t *testing.T) {
	var indexHits, loginHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xtgl/login_slogin.html":
			// 登录页 302 到首页：protocol 据此判定"已经是登录态"，登录立即成功。
			loginHits.Add(1)
			http.Redirect(w, r, "/xtgl/index_initMenu.html", http.StatusFound)
		case "/xsxk/zzxkyzb_cxZzxkYzbIndex.html":
			if indexHits.Add(1) == 1 {
				_, _ = io.WriteString(w, `<html><body><div id="tips">请先登录</div></body></html>`)
				return
			}
			_, _ = io.WriteString(w, `<html><body><input id="firstXkkzId" value="10"/></body></html>`)
		default:
			_, _ = io.WriteString(w, `<html><body></body></html>`)
		}
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
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+"/init", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("init status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := loginHits.Load(); got != 1 {
		t.Fatalf("自动重登应只登录 1 次，实际 %d 次", got)
	}
	if got := indexHits.Load(); got != 2 {
		t.Fatalf("初始化应重试 1 次（首轮 + 重试 = 2 次首页请求），实际 %d 次", got)
	}
	snap := store.tasks.snapshot(acc.ID)
	if snap == nil {
		t.Fatal("没有任务快照")
	}
	found := false
	for _, ev := range snap.Events {
		if strings.Contains(ev.Text, "正在自动重新登录") {
			found = true
		}
	}
	if !found {
		t.Fatalf("任务时间线缺少自动重登事件: %+v", snap.Events)
	}
}

// 关闭自动重登后，初始化发现会话失效只报 401，不应偷偷跑一次登录。
func TestInitUnauthorizedDoesNotReloginWhenDisabled(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `<html><body><div id="tips">请先登录</div></body></html>`)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	off := false
	acc, err := store.Create(&Account{
		Name:        "n",
		Username:    "u",
		BaseURL:     upstream.URL + "/",
		AutoRelogin: &off,
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
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+"/init", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("init status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("关闭自动重登后只应有 1 次上游请求（初始化首页），实际 %d 次", got)
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
