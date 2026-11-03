package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerSettingsAPI(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	get := func() serverView {
		t.Helper()
		res, err := http.Get(srv.URL + "/api/server")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/server = %d", res.StatusCode)
		}
		var view serverView
		if err := json.NewDecoder(res.Body).Decode(&view); err != nil {
			t.Fatal(err)
		}
		return view
	}

	view := get()
	if view.SMTP.Enabled || view.SMTP.HasPassword {
		t.Errorf("新目录默认配置应关闭且无密码: %+v", view.SMTP)
	}
	if view.SMTP.To == nil {
		t.Error("收件人为空时应返回 []，不能是 null")
	}
	if !strings.HasSuffix(view.SettingsFile, "server.json") {
		t.Errorf("settingsFile = %q，期望以 server.json 结尾", view.SettingsFile)
	}

	body := `{"smtp":{"enabled":true,"host":"smtp.example.com","port":587,"from":"sender@example.com","to":["one@example.com"],"password":"secret","notifyAutoChoose":true}}`
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/server", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/server = %d", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var updated serverView
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.SMTPReady || !updated.SMTP.HasPassword {
		t.Errorf("保存后应可发信且记录密码: %+v", updated)
	}
	if strings.Contains(string(raw), "secret") {
		t.Error("响应里出现了密码")
	}

	// 保存后重新 GET，确认落盘且不回显密码。
	view = get()
	if view.SMTP.Host != "smtp.example.com" || !view.SMTP.HasPassword {
		t.Errorf("重新读取配置不正确: %+v", view.SMTP)
	}
	if view.SMTP.Enabled != true || !view.SMTPReady {
		t.Errorf("重新读取后应处于可发信状态: %+v", view)
	}

	// 启用但缺主机：400，且错误响应里带当前状态供页面回滚。
	req, err = http.NewRequest(http.MethodPut, srv.URL+"/api/server",
		bytes.NewBufferString(`{"smtp":{"enabled":true,"host":""}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("非法配置应返回 400，实际 %d", res.StatusCode)
	}
}

func TestServerSettingsAPITestSMTPRequiresConfig(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	body := `{"smtp":{"enabled":false}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/server/smtp/test", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("没有 SMTP 配置时测试发信应返回 400，实际 %d", res.StatusCode)
	}
}
