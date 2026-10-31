package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 初始化卡在学校接口上时，/status 必须立即返回。
// 回归点：以前 Initialize 跨网络调用持有 Session 的锁，/status 会一直等到
// 登录超时（最长 10 分钟），前端整个界面看起来像死掉。
func TestStatusDoesNotBlockDuringInitialize(t *testing.T) {
	arrived := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCh) }) }

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		select {
		case <-releaseCh:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `<html><body><input id="firstXkkzId" value="10"/></body></html>`)
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(release) // LIFO：先放行 handler，再关闭服务

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  upstream.URL + "/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- rt.engine.Initialize(ctx) }()

	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("初始化没有发出请求")
	}

	start := time.Now()
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("初始化期间 /status 被阻塞了 %v", d)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"initialized":false`) {
		t.Fatalf("初始化尚未完成，initialized 应为 false，body = %s", body)
	}

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Initialize: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("释放请求后 Initialize 仍未返回")
	}

	// 初始化完成后同一接口应立刻反映新状态
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"initialized":true`) {
		t.Fatalf("初始化完成后 initialized 应为 true，body = %s", body)
	}
}
