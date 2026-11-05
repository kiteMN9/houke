package protocol

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// transportTestJar 造一个测试用的独立 cookie jar。
func transportTestJar(t *testing.T) *cookiejar.Jar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return jar
}

func TestTransportPoolReusesByProfile(t *testing.T) {
	pool := NewTransportPool()
	p := transportProfile{
		ResponseHeaderTimeout: 35 * time.Second,
		IdleConnTimeout:       68 * time.Second,
	}
	a := pool.transport(p)
	b := pool.transport(p)
	if a != b {
		t.Fatalf("同一配置档应返回同一个 transport")
	}
	if a.ResponseHeaderTimeout != 35*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 35s", a.ResponseHeaderTimeout)
	}
	if a.IdleConnTimeout != 68*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 68s", a.IdleConnTimeout)
	}
	// resty 的默认值应被继承：否则共享池会悄悄改变超时 / 连接数行为。
	if !a.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 应继承 resty 默认 true")
	}
	if a.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d, want 100", a.MaxIdleConns)
	}
	if a.MaxIdleConnsPerHost <= 0 {
		t.Errorf("MaxIdleConnsPerHost = %d, want resty 默认 > 0", a.MaxIdleConnsPerHost)
	}

	// 配置档不同（这里是 cas2 的 31s/8）不能共用。
	other := pool.transport(transportProfile{
		ResponseHeaderTimeout: 31 * time.Second,
		IdleConnTimeout:       68 * time.Second,
		MaxIdleConnsPerHost:   8,
	})
	if other == a {
		t.Fatalf("不同配置档不应共用 transport")
	}
	if other.MaxIdleConnsPerHost != 8 {
		t.Errorf("cas2 MaxIdleConnsPerHost = %d, want 8", other.MaxIdleConnsPerHost)
	}
}

func TestTransportPoolSharesConnectionsAcrossClients(t *testing.T) {
	var (
		mu    sync.Mutex
		addrs []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		addrs = append(addrs, r.RemoteAddr)
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := config.Settings{BaseURL: srv.URL + "/"}
	pool := NewTransportPool()
	rt := pool.transport(mainTransportProfile(cfg))

	// 两个"账号"：各自 http.Client / cookie jar，但共用池里的 transport。
	hc1 := &http.Client{Transport: rt, Jar: transportTestJar(t)}
	hc2 := &http.Client{Transport: rt, Jar: transportTestJar(t)}
	for _, hc := range []*http.Client{hc1, hc2} {
		resp, err := hc.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 2 {
		t.Fatalf("服务端收到 %d 个请求, want 2", len(addrs))
	}
	if addrs[0] != addrs[1] {
		t.Fatalf("共用 transport 时应复用同一条连接, RemoteAddr: %v", addrs)
	}
}

func TestTransportPoolKeepsCookieJarsSeparate(t *testing.T) {
	pool := NewTransportPool()
	cfg := config.Settings{BaseURL: "https://jwglxt.ycit.edu.cn/"}
	rt := pool.transport(mainTransportProfile(cfg))

	c1, _ := newBasicClient(cfg, rt)
	c2, _ := newBasicClient(cfg, rt)
	if c1.Client().Jar == c2.Client().Jar {
		t.Fatal("两个账号不能共享 cookie jar")
	}
	if c1.Transport() != c2.Transport() {
		t.Fatal("两个账号应共用同一个 transport")
	}

	u := mustParseURL(t, cfg.BaseURL)
	c1.CookieJar().SetCookies(u, []*http.Cookie{{Name: "route", Value: "a"}})
	if got := c2.CookieJar().Cookies(u); len(got) != 0 {
		t.Fatalf("A 账号的 cookie 泄漏到了 B 账号: %v", got)
	}
}

func TestNewAPIClientSharesTransportWhenPoolGiven(t *testing.T) {
	cfg := config.Settings{BaseURL: "https://jwglxt.ycit.edu.cn/"}
	pool := NewTransportPool()
	c1, err := NewAPIClient(cfg, nil, false, false, "", WithTransportPool(pool))
	if err != nil {
		t.Fatalf("NewAPIClient: %v", err)
	}
	c2, err := NewAPIClient(cfg, nil, false, false, "", WithTransportPool(pool))
	if err != nil {
		t.Fatalf("NewAPIClient: %v", err)
	}
	if c1.Http.Transport() == nil {
		t.Fatal("transport 不应为 nil")
	}
	if c1.Http.Transport() != c2.Http.Transport() {
		t.Fatal("传入同一个 TransportPool 时应共用 transport")
	}
	if want := pool.transport(mainTransportProfile(cfg)); c1.Http.Transport() != want {
		t.Fatal("transport 应来自 TransportPool")
	}
	if c1.Http.Client().Jar == c2.Http.Client().Jar {
		t.Fatal("cookie jar 仍必须每账号独立")
	}
}

func TestTransportPoolCloseIdleConnectionsIdempotent(t *testing.T) {
	pool := NewTransportPool()
	pool.transport(transportProfile{ResponseHeaderTimeout: time.Second})
	pool.CloseIdleConnections()
	pool.CloseIdleConnections()
	// 关空闲连接后池里的 transport 仍可复用（账号配置变更不该重建池）。
	if got := pool.transport(transportProfile{ResponseHeaderTimeout: time.Second}); got == nil {
		t.Fatal("CloseIdleConnections 不应清空池")
	}
}

// mustParseURL 解析测试里固定的 URL。
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}
