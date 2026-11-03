package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// WebUI 保存账号走的是 PUT /api/accounts/:id，字段名必须与前端一致（loginTimeout）。
func TestAccountLoginTimeoutOverHTTP(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/accounts/"+acc.ID,
		strings.NewReader(`{"loginTimeout":"25m"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d (%s), want 200", resp.StatusCode, body)
	}

	// GET 回来应当是保存后的值（前端的编辑弹窗就靠它回填）
	got, err := http.Get(srv.URL + "/api/accounts/" + acc.ID) //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(body), `"loginTimeout":"25m"`) {
		t.Errorf("账号详情 = %s, want 含 \"loginTimeout\":\"25m\"", body)
	}
}

// 自动重登开关走专用端点：不经过 PUT /accounts（后者会重建运行态）。
func TestAutoReloginOverHTTP(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	put := func(body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut,
			srv.URL+"/api/accounts/"+acc.ID+"/auto-relogin", strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, string(data)
	}

	resp, body := put(`{"enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("关闭 autoRelogin status = %d (%s), want 200", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"autoRelogin":false`) {
		t.Fatalf("响应 = %s, want autoRelogin=false", body)
	}

	got, err := http.Get(srv.URL + "/api/accounts/" + acc.ID) //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer got.Body.Close()
	detail, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(detail), `"autoRelogin":false`) {
		t.Fatalf("账号详情 = %s, want autoRelogin=false", detail)
	}

	resp, body = put(`{"enabled":true}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"autoRelogin":true`) {
		t.Fatalf("开启 autoRelogin 失败: %d %s", resp.StatusCode, body)
	}

	resp, _ = put(`{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺少 enabled 时 status = %d, want 400", resp.StatusCode)
	}
}

// 自动选课邮件开关也走专用端点：账号级开关，不经过 PUT /accounts（后者会重建运行态）。
func TestAutoNotifyOverHTTP(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	put := func(body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut,
			srv.URL+"/api/accounts/"+acc.ID+"/auto-notify", strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, string(data)
	}

	resp, body := put(`{"enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("关闭 autoNotify status = %d (%s), want 200", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"autoNotify":false`) {
		t.Fatalf("响应 = %s, want autoNotify=false", body)
	}

	got, err := http.Get(srv.URL + "/api/accounts/" + acc.ID) //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer got.Body.Close()
	detail, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(detail), `"autoNotify":false`) {
		t.Fatalf("账号详情 = %s, want autoNotify=false", detail)
	}

	resp, body = put(`{"enabled":true}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"autoNotify":true`) {
		t.Fatalf("开启 autoNotify 失败: %d %s", resp.StatusCode, body)
	}

	resp, _ = put(`{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺少 enabled 时 status = %d, want 400", resp.StatusCode)
	}
}

// 登录方式走 HTTP 层也必须能往返：前端提交 loginMode，GET 回来可供编辑弹窗回填。
func TestAccountLoginModeOverHTTP(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/accounts/"+acc.ID,
		strings.NewReader(`{"loginMode":"wechat"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	got, err := http.Get(srv.URL + "/api/accounts/" + acc.ID) //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(body), `"loginMode":"wechat"`) {
		t.Errorf("账号详情 = %s, want 含 \"loginMode\":\"wechat\"", body)
	}
	if !strings.Contains(string(body), `"casLogin":false`) {
		t.Errorf("账号详情 = %s, want 含 \"casLogin\":false", body)
	}
}
