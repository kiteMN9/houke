package web

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// 关闭自动重登后，未登录时个人信息 / 大头照必须快速失败，不能偷偷跑登录。
func TestToolsInfoPhotoRequireLoginWhenAutoReloginOff(t *testing.T) {
	var hits atomic.Int32
	srv, acc, rt := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	rt.engine.SetAutoRelogin(false)

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

// withJSSnapshotDir 把本地快照目录指到临时目录：否则一次测试就往 repo 的
// html/ 里写三份脚本，工作区会脏。
func withJSSnapshotDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := jsSnapshotDir
	jsSnapshotDir = dir
	t.Cleanup(func() { jsSnapshotDir = old })
	return dir
}

func doToolsPOST(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

type toolsJSHashResp struct {
	GeneratedAt string `json:"generatedAt"`
	SnapshotDir string `json:"snapshotDir"`
	Saved       int    `json:"saved"`
	Files       []struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		OK         bool   `json:"ok"`
		Status     int    `json:"status"`
		Size       int    `json:"size"`
		SHA1       string `json:"sha1"`
		LocalFound bool   `json:"localFound"`
		LocalSHA1  string `json:"localSha1"`
		LocalMatch bool   `json:"localMatch"`
		Saved      bool   `json:"saved"`
		Error      string `json:"error"`
		SaveError  string `json:"saveError"`
	} `json:"files"`
}

func decodeJSHash(t *testing.T, rec *httptest.ResponseRecorder) toolsJSHashResp {
	t.Helper()
	var body toolsJSHashResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v, body = %s", err, rec.Body.String())
	}
	return body
}

// 静态脚本是公开资源：未登录也要能算，且本地没有副本时顺手存一份。
func TestToolsJSHashWithoutLogin(t *testing.T) {
	dir := withJSSnapshotDir(t)
	var hits atomic.Int32
	srv, acc, _ := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte("var a = 1;\n"))
	})

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/jshash")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if n := hits.Load(); n == 0 {
		t.Fatalf("未登录时没有请求上游静态资源")
	}
	body := decodeJSHash(t, rec)
	if body.Saved != len(body.Files) || body.Saved == 0 {
		t.Errorf("saved = %d, files = %d, want 全部自动存盘", body.Saved, len(body.Files))
	}
	for _, f := range body.Files {
		got, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			t.Fatalf("读取自动保存的快照 %s: %v", f.Name, err)
		}
		if string(got) != "var a = 1;\n" {
			t.Errorf("%s 内容 = %q", f.Name, got)
		}
	}
}

// 三份脚本都要走端点常量里的路径，哈希要覆盖解压后的全文。
func TestToolsJSHash(t *testing.T) {
	withJSSnapshotDir(t)
	bodies := map[string]string{
		"/js/comp/jwglxt/xkgl/xsxk/zzxkYzb.js":          "// zzxkYzb\nfunction loadJxbxxZzxk(obj){}\n",
		"/js/comp/jwglxt/xkgl/xsxk/zzxkYzbZy.js":        "var zy = 1;\n",
		"/js/comp/jwglxt/xkgl/xsxk/zzxkYzbChoosedZy.js": "/* choosed */\n",
	}
	srv, acc, rt := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			t.Errorf("意外的路径 %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript;charset=UTF-8")
		_, _ = w.Write([]byte(body))
	})
	rt.engine.TestSetLoggedIn(true)

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/jshash")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeJSHash(t, rec)
	if body.GeneratedAt == "" {
		t.Errorf("generatedAt 为空")
	}
	if body.SnapshotDir == "" {
		t.Errorf("snapshotDir 为空")
	}
	if len(body.Files) != len(bodies) {
		t.Fatalf("files = %d, want %d", len(body.Files), len(bodies))
	}
	for _, f := range body.Files {
		want, ok := bodies[f.Path]
		if !ok {
			t.Fatalf("返回了未请求的路径 %s", f.Path)
		}
		if !f.OK || f.Error != "" || f.SaveError != "" {
			t.Fatalf("%s ok = %v, error = %s, saveError = %s", f.Path, f.OK, f.Error, f.SaveError)
		}
		if f.Size != len(want) {
			t.Errorf("%s size = %d, want %d", f.Path, f.Size, len(want))
		}
		if wantSum := sha1.Sum([]byte(want)); f.SHA1 != hex.EncodeToString(wantSum[:]) {
			t.Errorf("%s sha1 = %s", f.Path, f.SHA1)
		}
		// 第一次跑没有本地副本：自动存一份，之后就是"一致"。
		if !f.Saved || !f.LocalFound || !f.LocalMatch {
			t.Errorf("%s saved/localFound/localMatch = %v/%v/%v", f.Name, f.Saved, f.LocalFound, f.LocalMatch)
		}
	}

	// 第二次跑：已经有快照，不该再写盘，也不该报 saved。
	rec = doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/jshash")
	body = decodeJSHash(t, rec)
	if body.Saved != 0 {
		t.Errorf("第二次 saved = %d, want 0", body.Saved)
	}
	for _, f := range body.Files {
		if f.Saved || !f.LocalMatch {
			t.Errorf("%s saved/localMatch = %v/%v", f.Name, f.Saved, f.LocalMatch)
		}
	}

	// 学校改了脚本：报告"已变化"，但**不**自动覆盖本地基准。
	bodies["/js/comp/jwglxt/xkgl/xsxk/zzxkYzbZy.js"] = "var zy = 2;\n"
	rec = doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/jshash")
	body = decodeJSHash(t, rec)
	var changed int
	for _, f := range body.Files {
		if f.Name == "zzxkYzbZy.js" {
			changed++
			if f.Saved || f.LocalMatch {
				t.Errorf("变化后 saved/localMatch = %v/%v, want false", f.Saved, f.LocalMatch)
			}
		}
	}
	if changed != 1 {
		t.Fatalf("没找到被改动的 zzxkYzbZy.js")
	}

	// 手动「存为快照」才覆盖基准。
	rec = doToolsPOST(t, srv, "/api/accounts/"+acc.ID+"/tools/jshash/save")
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body = decodeJSHash(t, rec)
	if body.Saved != len(bodies) {
		t.Errorf("save saved = %d, want %d", body.Saved, len(bodies))
	}
	for _, f := range body.Files {
		if !f.Saved || !f.LocalMatch {
			t.Errorf("%s 保存后 saved/localMatch = %v/%v", f.Name, f.Saved, f.LocalMatch)
		}
	}
}

// 学校把脚本请求拦到登录页时，整次请求仍应 200（静态资源不要求登录），
// 但这一份要记成失败，不能把登录页的哈希当成脚本哈希展示出去。
func TestToolsJSHashRejectsLoginPage(t *testing.T) {
	withJSSnapshotDir(t)
	srv, acc, _ := newToolsTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html;charset=UTF-8")
		_, _ = w.Write([]byte("<html><body>请先登录</body></html>"))
	})

	rec := doToolsGET(t, srv, "/api/accounts/"+acc.ID+"/tools/jshash")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeJSHash(t, rec)
	if len(body.Files) == 0 {
		t.Fatalf("files 为空")
	}
	for _, f := range body.Files {
		if f.OK || f.SHA1 != "" || f.Saved || f.Error == "" {
			t.Fatalf("登录页被当成了脚本: %+v", f)
		}
	}
	if body.Saved != 0 {
		t.Errorf("登录页不该存成快照，saved = %d", body.Saved)
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
