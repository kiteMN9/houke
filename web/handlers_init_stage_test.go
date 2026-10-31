package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 初始化任务的阶段必须来自 protocol 的 InitStage：抓首页参数时 /status 能
// 看到"正在获取选课首页参数"，完成后收尾为"初始化完成"。
// 这条链路不依赖 ProtocolHook——hook 的端点事件在初始化任务里被丢弃。
func TestInitTaskShowsProtocolStages(t *testing.T) {
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

	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+"/init", nil))
		done <- rec.Code
	}()

	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("初始化没有发出请求")
	}

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"stageText":"正在获取选课首页参数"`) {
		t.Fatalf("初始化阶段没有进 /status: %s", body)
	}

	release()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("init = %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("初始化未在超时内完成")
	}

	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"stageText":"初始化完成"`) {
		t.Fatalf("初始化收尾阶段不对: %s", body)
	}
}
