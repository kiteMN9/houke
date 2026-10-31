package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// newStateTestClient 构造一个指向本地测试服务器、未启用 cas2 的 APIClient。
func newStateTestClient(t *testing.T, baseURL string) *APIClient {
	t.Helper()
	cfg := config.Settings{
		BaseURL:   baseURL,
		Account:   "u",
		Password:  "p",
		UserAgent: "Edg/142.0.0.0",
		Timeout:   config.Duration(5 * time.Second),
	}
	return mustNewAPIClient(t, cfg, nil, false, false, "")
}

// 登录态属于 APIClient：登出之后上层读到的必然是否。
func TestLogoutClearsLoggedInState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 登出后的页面：不含当前账号、带 #tips，判定为已退出
		_, _ = w.Write([]byte(`<html><body><div id="tips">已退出登录</div></body></html>`))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	if api.LoggedIn() {
		t.Fatal("新建客户端不该是已登录")
	}

	api.setLoggedIn(true)
	api.Logout()
	if api.LoggedIn() {
		t.Error("Logout() 之后 LoggedIn() = true, want false")
	}
}

// Ping 是会话探测器：探到在线 / 掉线都要写回 client 自己的登录态，
// 这样 web 只读 client 就能拿到最新状态。
func TestPingSyncsLoggedInState(t *testing.T) {
	var online atomic.Bool
	online.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if online.Load() {
			_, _ = w.Write([]byte(`<html><body><input id="yhm" value="u"></body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><body><div id="tips">用户名或密码不正确</div></body></html>`))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true) // 先假装手里有会话

	res := api.Ping(context.Background())
	if !res.OK || !res.LoggedIn {
		t.Fatalf("在线时 Ping = %+v, want ok+loggedIn", res)
	}
	if !api.LoggedIn() {
		t.Error("Ping 探到在线后 LoggedIn() = false, want true")
	}

	online.Store(false)
	res = api.Ping(context.Background())
	if res.LoggedIn {
		t.Errorf("掉线时 Ping.LoggedIn = true, want false: %+v", res)
	}
	if api.LoggedIn() {
		t.Error("Ping 探到掉线后 LoggedIn() = true, want false")
	}
}

// 请求本身失败（服务器不可达）时不能把登录态改成"已掉线"：
// 网络抖动不等于会话失效。
func TestPingKeepsStateOnRequestFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // 关掉：请求必然失败

	api := newStateTestClient(t, addr)
	api.setLoggedIn(true)

	if res := api.Ping(context.Background()); res.OK {
		t.Fatalf("服务器已关闭，Ping = %+v, want 请求失败", res)
	}
	if !api.LoggedIn() {
		t.Error("请求失败不该改动登录态，LoggedIn() = false, want true")
	}
}
