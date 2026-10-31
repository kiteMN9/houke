package protocol

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// newTestAPIClient 构造一个指向本地测试服务器、启用了 cas2 的 APIClient。
func newTestAPIClient(t *testing.T, baseURL string) *APIClient {
	t.Helper()
	cfg := config.Settings{
		BaseURL:     baseURL,
		Account:     "u",
		Password:    "p",
		CasPassword: "cp",
		UserAgent:   "Edg/142.0.0.0",
		Timeout:     config.Duration(5 * time.Second),
	}
	return mustNewAPIClient(t, cfg, nil, true, false, "")
}

// APIClient 必须把钩子下发给 cas2 客户端（含置空）。
//
// 曾经的写法是在 NewAPIClient 里 SetProtocolHook(apiClient.ProtocolHook)——
// 那一刻字段必然还是 nil，于是 cas2 的请求全部静默丢失。
func TestAPIClientForwardsHookToCAS2(t *testing.T) {
	api := newTestAPIClient(t, "https://jwglxt.ycit.edu.cn/")
	if api.cas2Client == nil {
		t.Fatal("cas2 客户端未创建")
	}

	var mu sync.Mutex
	calls := 0
	api.SetProtocolHook(func(method, url string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	if api.cas2Client.ProtocolHook == nil {
		t.Fatal("cas2 客户端没有拿到 ProtocolHook")
	}
	api.cas2Client.ProtocolHook(http.MethodGet, "https://cas2.ycit.edu.cn/cas/login")

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("钩子调用次数 = %d, want 1", got)
	}

	api.SetProtocolHook(nil)
	if api.cas2Client.ProtocolHook != nil {
		t.Error("钩子置空后 cas2 客户端仍在回调")
	}
}

// 未启用 cas2 的账号不应因为没有 ProtocolHook 而 panic。
func TestSetProtocolHookWithoutCAS2(t *testing.T) {
	cfg := config.Settings{BaseURL: "https://jwglxt.ycit.edu.cn/", Account: "u", UserAgent: "Edg/142.0.0.0", Timeout: config.Duration(5 * time.Second)}
	api := mustNewAPIClient(t, cfg, nil, false, false, "")
	api.SetProtocolHook(func(method, url string) {})
	if api.cas2Client != nil {
		t.Error("未启用 cas2 却创建了 cas2 客户端")
	}
}

// 中间件拿到的必须是"补全 host 后的绝对 URL"：
// web.classifyCas 依赖 host 区分门户与教务，只有 path 会导致阶段判断退化成兜底分支。
func TestProtocolHookReceivesAbsoluteURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := config.Settings{BaseURL: srv.URL, Account: "u", UserAgent: "Edg/142.0.0.0", Timeout: config.Duration(5 * time.Second)}
	api := mustNewAPIClient(t, cfg, nil, false, false, "")

	var (
		mu  sync.Mutex
		got []string
	)
	api.SetProtocolHook(func(method, url string) {
		mu.Lock()
		got = append(got, method+" "+url)
		mu.Unlock()
	})

	if _, err := api.Http.R().Get("/xtgl/login_slogin.html"); err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("钩子调用次数 = %d, want 1: %v", len(got), got)
	}
	if want := "GET " + srv.URL + "/xtgl/login_slogin.html"; got[0] != want {
		t.Errorf("上报 URL = %q, want %q", got[0], want)
	}
}
