package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/protocol"
)

func newToolsTestServer(t *testing.T, upstream http.HandlerFunc) (*Server, *Account, *accountRuntime) {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:        "n",
		Username:    "u",
		BaseURL:     srv.URL + "/",
		Password:    "pw-secret",
		CasPassword: "cas-secret",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	return mustNewServer(t, store), acc, rt
}

func doToolsGET(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// 未登录时个人信息 / 大头照必须快速失败，不能触发不可见的阻塞登录。
func TestToolsInfoPhotoRequireLogin(t *testing.T) {
	var hits atomic.Int32
	srv, acc, _ := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})

	for _, suffix := range []string{"/tools/info", "/tools/photo"} {
		rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+suffix)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d, body = %s", suffix, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "尚未登录") {
			t.Errorf("%s body = %s, want 含「尚未登录」", suffix, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", suffix, got)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("未登录请求仍访问了上游 %d 次", n)
	}
}

func TestToolsInfo(t *testing.T) {
	srv, acc, rt := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xsxxxggl/xsxxwh_cxCkDgxsxx.html" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("gnmkdm"); got != "N100801" {
			t.Errorf("gnmkdm = %q, want N100801", got)
		}
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_, _ = w.Write([]byte(`{"xm":"张三","xh":"20230001","xh_id":"20230001","jg_id":"计算机学院","xbm":"1"}`))
	})
	rt.engine.TestSetLoggedIn(true)

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/info")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var info protocol.UserInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v, body = %s", err, rec.Body.String())
	}
	if info.Xm != "张三" || info.Xh != "20230001" || info.JgId != "计算机学院" {
		t.Errorf("info = %+v", info)
	}
}

func TestToolsInfoNoPermission(t *testing.T) {
	srv, acc, rt := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html;charset=UTF-8")
		_, _ = w.Write([]byte("<html><body>无功能权限</body></html>"))
	})
	rt.engine.TestSetLoggedIn(true)

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/info")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "no_permission" {
		t.Errorf("code = %q, want no_permission", body.Code)
	}
}

func TestToolsPhoto(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13}
	srv, acc, rt := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xtgl/photo_cxXszp.html" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("xh_id"); got != "u" {
			t.Errorf("xh_id = %q, want u", got)
		}
		if got := r.URL.Query().Get("zplx"); got != "rxqzp" {
			t.Errorf("zplx = %q, want rxqzp", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	rt.engine.TestSetLoggedIn(true)

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/photo")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Errorf("body = %v, want %v", rec.Body.Bytes(), png)
	}
}

// Cookie 是本地运行态，不要求登录；route cookie 由 client 初始化时写入，
// 所以这里只校验响应结构稳定，不假定数量一定为 0。
func TestToolsCookiesWithoutLogin(t *testing.T) {
	srv, acc, _ := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/cookies")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		Cookies  []protocol.CookieSnapshot `json:"cookies"`
		Header   string                    `json:"header"`
		Count    int                       `json:"count"`
		LoggedIn bool                      `json:"loggedIn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v, body = %s", err, rec.Body.String())
	}
	if body.Cookies == nil {
		t.Fatalf("cookies = null, want JSON 数组")
	}
	if body.Count != len(body.Cookies) {
		t.Errorf("count = %d, len = %d", body.Count, len(body.Cookies))
	}
	if body.LoggedIn {
		t.Errorf("loggedIn = true, want false")
	}
	if len(body.Cookies) > 0 && body.Header == "" {
		t.Errorf("有 %d 条 Cookie 但 header 为空", len(body.Cookies))
	}
}

// 诊断信息可以给前端展示，但绝不能把密码/JWT 明文带出去。
func TestToolsDebugRedactsSecrets(t *testing.T) {
	srv, acc, _ := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/debug")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	raw := rec.Body.String()
	for _, secret := range []string{"pw-secret", "cas-secret"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("debug 响应泄露了密码 %q: %s", secret, raw)
		}
	}
	var body struct {
		GeneratedAt string `json:"generatedAt"`
		Account     struct {
			Username       string `json:"username"`
			HasPassword    bool   `json:"hasPassword"`
			HasCasPassword bool   `json:"hasCasPassword"`
		} `json:"account"`
		Engine struct {
			LoggedIn bool `json:"loggedIn"`
		} `json:"engine"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v, body = %s", err, raw)
	}
	if body.GeneratedAt == "" {
		t.Errorf("generatedAt 为空")
	}
	if body.Account.Username != "u" || !body.Account.HasPassword || !body.Account.HasCasPassword {
		t.Errorf("account = %+v", body.Account)
	}
	if body.Engine.LoggedIn {
		t.Errorf("engine.loggedIn = true, want false")
	}
}
