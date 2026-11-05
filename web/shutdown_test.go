package web

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// 退出时必须把还在跑的长任务停掉：它们的 context 都派生自
// context.Background()，不主动取消就会继续跑到自己的预算（自动选课默认
// 24 小时）。旧实现里 http.Server.Shutdown 只会等连接，任务根本不看退出信号。
func TestShutdownCancelsRunningTasks(t *testing.T) {
	store := newTestStore(t)
	s := mustNewServer(t, store)

	taskCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := s.tasks.startCancelable("acc-shutdown", taskKindAuto, cancel); !ok {
		t.Fatal("startCancelable 应当成功")
	}
	if !s.tasks.running("acc-shutdown", taskKindAuto) {
		t.Fatal("任务应当处于运行中")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case <-taskCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("Shutdown 后任务 context 仍然存活：进程退了任务还在跑")
	}
	if s.tasks.running("acc-shutdown", taskKindAuto) {
		t.Fatal("Shutdown 后任务仍在运行")
	}
	snap := s.tasks.snapshot("acc-shutdown")
	if snap == nil || snap.Status != taskStatusCancelled {
		t.Fatalf("任务快照 = %+v，want status=%s", snap, taskStatusCancelled)
	}
}

// Serve 收到 ctx 取消后必须自己返回（而不是只能靠关窗口）：这是 main 里
// signal.NotifyContext 的退出路径。
func TestServeStopsOnContextCancel(t *testing.T) {
	store := newTestStore(t)
	s := mustNewServer(t, store)

	ln, err := s.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	// /healthz 是无副作用的现成探针，等它可用说明 Serve 已经接管 listener。
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务未就绪: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve 在 ctx 取消后返回 %v，want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 Serve 没有返回")
	}

	if resp, err := client.Get(base + "/healthz"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("Serve 返回后端口仍可连接")
	}
}

// SSE 长连接必须随退出结束。http.Server.Shutdown 不会取消已经建立的请求
// context，所以 Shutdown 里要显式 cancel 服务级 baseCtx；否则一条 SSE
// 连接就会把退出拖到 shutdownTimeout（5s）甚至更久。
func TestShutdownClosesSSE(t *testing.T) {
	store := newTestStore(t)
	s := mustNewServer(t, store)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ln, err := s.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	base := "http://" + ln.Addr().String()

	serveCtx, serveCancel := context.WithCancel(context.Background())
	defer serveCancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(serveCtx, ln) }()

	// 打开 SSE 并读掉首帧，确认连接真的建立了。
	req, err := http.NewRequest(http.MethodGet, base+"/api/accounts/"+acc.ID+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	resp, err := http.DefaultClient.Do(req.WithContext(reqCtx))
	if err != nil {
		t.Fatalf("打开 SSE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 状态码 = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 64)
	if _, err := io.ReadFull(resp.Body, buf[:1]); err != nil {
		t.Fatalf("读 SSE 首帧: %v", err)
	}

	start := time.Now()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("有 SSE 连接时 Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Shutdown 用了 %v：SSE 没有被 baseCtx 取消，只是等到超时", elapsed)
	}

	// SSE 服务端应当已收尾，再读会得到 EOF。
	_ = resp.Body.Close()
	serveCancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve 返回 %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve 没有返回")
	}
	_ = buf
}
