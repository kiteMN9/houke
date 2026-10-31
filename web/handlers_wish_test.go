package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// WebUI 的「高级选课」表单：PUT 保存、GET 回填，字段名必须与前端一致。
func TestWishOverHTTP(t *testing.T) {
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

	put := func(body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/accounts/"+acc.ID+"/wish",
			strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		return resp
	}

	resp := put(`{"wantClasses":[" 高等数学 ","高等数学",""]}`)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("PUT status = %d (%s), want 200", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"wantClasses":["高等数学"]`) {
		t.Errorf("PUT 响应 = %s, want 去空白去重后的 wantClasses", body)
	}

	got, err := http.Get(srv.URL + "/api/accounts/" + acc.ID + "/wish") //nolint:noctx // 测试不关心 ctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer got.Body.Close()
	body, _ = io.ReadAll(got.Body)
	if !strings.Contains(string(body), `"wantClasses":["高等数学"]`) {
		t.Errorf("GET 响应 = %s, want 含保存后的 wantClasses", body)
	}

	// 传空数组 = 清空；全空时三个字段序列化成 null，前端据此显示为空。
	resp = put(`{"wantClasses":[],"wantTeachers":[],"wantTypes":[]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT(clear) status = %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"wantClasses":null`) {
		t.Errorf("清空后响应 = %s, want \"wantClasses\":null", body)
	}

	// 不存在的账号：404，不能静默成功。
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/accounts/nope/wish",
		strings.NewReader(`{"wantClasses":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT(unknown): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("未知账号 status = %d, want 404", resp.StatusCode)
	}
}
