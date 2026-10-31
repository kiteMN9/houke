package cas2

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"school_sdk/protocol/config"
)

// cas2 的请求分散在 http / portalHttp 两个 resty 客户端上
// （portalHttp 是 Clone 出来的独立客户端，中间件不共享）。
// 这个测试锁住两件事：
//  1. 两个客户端上的请求都能被上层钩子看到；
//  2. 重复挂载钩子是幂等的，不会把同一请求上报两次。
func TestProtocolHookObservesBothClientsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewCas("u", "p", "Edg/142.0.0.0", false, config.Settings{}, nil)
	// NewCas 把两个客户端钉在真实门户域名上，这里换成本地测试服务器：
	// 既不触网，又能真实走一遍"请求 → 中间件"的完整路径。
	c.http.SetBaseURL(srv.URL)
	c.portalHttp.SetBaseURL(srv.URL)

	var (
		mu  sync.Mutex
		got []string
	)
	hook := func(method, url string) {
		mu.Lock()
		got = append(got, method+" "+url)
		mu.Unlock()
	}
	c.SetProtocolHook(hook)
	c.SetProtocolHook(hook) // 再挂一次：必须幂等

	if _, err := c.http.R().Get("/cas/login"); err != nil {
		t.Fatalf("cas2 客户端请求失败: %v", err)
	}
	if _, err := c.portalHttp.R().Get("/portal-api/v2/service/networkCheck"); err != nil {
		t.Fatalf("门户客户端请求失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("钩子收到 %d 次请求, want 2（两个客户端各一次且不重复）: %v", len(got), got)
	}
	for i, want := range []string{"GET " + srv.URL + "/cas/login", "GET " + srv.URL + "/portal-api/v2/service/networkCheck"} {
		if got[i] != want {
			t.Errorf("第 %d 次上报 = %q, want %q", i+1, got[i], want)
		}
	}
}

// 先挂 nil 再挂真实钩子：补挂要生效（构造顺序与 Web 层一致）。
func TestSetProtocolHookAfterNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewCas("u", "p", "Edg/142.0.0.0", false, config.Settings{}, nil)
	c.http.SetBaseURL(srv.URL)
	c.portalHttp.SetBaseURL(srv.URL)

	c.SetProtocolHook(nil) // 相当于构造后立刻被置空
	called := 0
	c.SetProtocolHook(func(method, url string) { called++ })

	if _, err := c.http.R().Get("/cas/jwt/publicKey"); err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if called != 1 {
		t.Errorf("钩子调用次数 = %d, want 1", called)
	}
}
