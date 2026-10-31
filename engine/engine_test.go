package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"school_sdk/protocol/config"
)

func validSettings(baseURL string) config.Settings {
	cfg := config.Default()
	cfg.BaseURL = baseURL
	cfg.Account = "u"
	cfg.Password = "p"
	return cfg
}

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	if _, err := New(validSettings(""), config.NewSession(), LoginPassword, Options{}); err == nil {
		t.Fatal("空 BaseURL 应该被拒绝")
	}
}

func TestNewAcceptsValidSettings(t *testing.T) {
	eng, err := New(validSettings("https://example.com/"), config.NewSession(), LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := eng.Settings().BaseURL; got != "https://example.com/" {
		t.Fatalf("BaseURL = %q", got)
	}
	if eng.LoggedIn() {
		t.Fatal("新引擎不应默认已登录")
	}
	if eng.Inited() {
		t.Fatal("新引擎不应默认已初始化")
	}
	if got := eng.Tabs(); got != nil {
		t.Fatalf("未初始化时 Tabs = %v, want nil", got)
	}
}

// 微信模式下未登录时 authorize 不能自动发起扫码登录：业务请求失败
// 只应把未登录报回去，二维码必须由用户显式点击登录产生。
func TestAuthorizeWeChatDoesNotStartLogin(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	eng, err := New(validSettings(srv.URL+"/"), config.NewSession(), LoginWeChat, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if eng.LoginKind() != LoginWeChat {
		t.Fatalf("LoginKind = %v, want LoginWeChat", eng.LoginKind())
	}
	if eng.authorize(context.Background()) {
		t.Fatal("未登录的微信账号 authorize 应返回 false")
	}
	if hits.Load() != 0 {
		t.Fatalf("authorize 不应发请求，实际 %d 次", hits.Load())
	}
}

// TestSettingsIsIsolated 防止调用方通过返回值的 Routes 反向修改引擎内部状态。
func TestSettingsIsIsolated(t *testing.T) {
	cfg := validSettings("https://example.com/")
	cfg.Routes = []string{"route-a"}

	eng, err := New(cfg, config.NewSession(), LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := eng.Settings()
	got.Routes[0] = "route-hacked"

	if again := eng.Settings(); again.Routes[0] != "route-a" {
		t.Fatalf("Engine.Settings 泄露了内部切片: Routes = %v", again.Routes)
	}
}

// TestLoginStateDelegates 确认登录态只认 protocol.Client 那一份，不在 Engine 里另存。
func TestLoginStateDelegates(t *testing.T) {
	eng, err := New(validSettings("https://example.com/"), config.NewSession(), LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	eng.TestSetLoggedIn(true)
	if !eng.LoggedIn() {
		t.Fatal("TestSetLoggedIn(true) 之后 LoggedIn 应为 true")
	}
	eng.TestSetLoggedIn(false)
	if eng.LoggedIn() {
		t.Fatal("TestSetLoggedIn(false) 之后 LoggedIn 应为 false")
	}
}
