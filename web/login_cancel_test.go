package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// freeAddr 占一个端口再立刻释放，得到一个"保证被拒绝连接"的地址。
// 登录流程会快速失败并重试，既不用等 DNS，也不用挂起服务器。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// 任务一出现就必须带着真取消函数：startCancelable 把"运行中"和"可取消"
// 登记在同一次加锁里，不存在"任务在跑却取不到取消函数"的中间态。
func TestStartCancelableInstallsCancelAtomically(t *testing.T) {
	ts := newTaskStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, ok := ts.startCancelable("a", taskKindLogin, cancel); !ok {
		t.Fatal("startCancelable 应当成功")
	}
	if !ts.running("a", taskKindLogin) {
		t.Fatal("任务应当处于运行中")
	}
	if !ts.takeCancel("a", taskKindLogin, false) {
		t.Fatal("任务在跑却取不到取消函数：运行中与可取消必须同时成立")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("取出的取消函数没有被调用")
	}
}

// 被拒绝的启动不能碰任务表，更不能把调用方的取消函数登记进去——否则
// /login/cancel 会把别人的任务标成"已取消"，而它的 ctx 其实还活着。
func TestStartCancelableRejectedLeavesRunningTaskAlone(t *testing.T) {
	ts := newTaskStore()
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	if _, ok := ts.startCancelable("a", taskKindInit, cancelFirst); !ok {
		t.Fatal("首个任务应当启动成功")
	}

	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if _, ok := ts.startCancelable("a", taskKindLogin, cancelSecond); ok {
		t.Fatal("同账号已有任务时 startCancelable 应当返回 false")
	}

	if !ts.running("a", taskKindInit) {
		t.Error("原有任务被顶掉了")
	}
	if ts.running("a", taskKindLogin) {
		t.Error("被拒绝的启动却留下了一条登录任务")
	}
	select {
	case <-second.Done():
		t.Fatal("被拒绝的 startCancelable 不该取消调用方的 ctx")
	default:
	}

	// 任务表里的取消函数仍是最初登记的那个
	if !ts.takeCancel("a", taskKindInit, true) {
		t.Fatal("原有任务的取消函数丢了")
	}
	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("takeCancel 没有调用唤醒任务用的取消函数")
	}
	select {
	case <-second.Done():
		t.Fatal("takeCancel 误调用了被拒绝的取消函数")
	default:
	}
}

// 409 不能留下"幽灵任务"：旧实现先 start 再检查登录所有权，所有权检查
// 失败时新任务没人收尾，会一直显示"登录中"，并让后续登录永远 409。
func TestLoginConflictLeavesNoPhantomTask(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:         "dead",
		Username:     "u",
		BaseURL:      "http://" + freeAddr(t) + "/",
		Timeout:      "300ms",
		LoginTimeout: "5s",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	// 模拟"另一个登录仍持有运行态"：典型场景是 /init 顺带登录还在收尾，
	// 此时 rt.loginCancel 非 nil，但任务表里没有跑着的登录任务。
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	defer ownerCancel()
	rt.loginMu.Lock()
	rt.loginCancel = ownerCancel
	rt.loginMu.Unlock()

	resp, err := http.Post(srv.URL+"/api/accounts/"+acc.ID+"/login", "application/json", nil) //nolint:noctx // 测试不关心 ctx
	if err != nil {
		t.Fatalf("POST login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d (%s), want 409", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "登录已在进行中") {
		t.Errorf("body = %s, want 含「登录已在进行中」", body)
	}
	// 关键断言：被拒绝的这次登录没有在任务表里留下任何东西
	if snap := store.tasks.snapshot(acc.ID); snap != nil {
		t.Fatalf("409 之后不应留下登录任务，却得到 %+v", snap)
	}
	// 别人的登录所有权也不能被清掉
	rt.loginMu.Lock()
	left := rt.loginCancel
	rt.loginMu.Unlock()
	if left == nil {
		t.Error("别人的登录所有权被清掉了")
	}
	select {
	case <-ownerCtx.Done():
		t.Error("别人的登录 ctx 被取消了")
	default:
	}
}

// /status 能看到"登录中"之后，/login/cancel 必须真的取消，而不是只把任务
// 标成已取消、等它自己跑完（旧实现用空 func(){} 占位，曾经就是这个症状）。
func TestLoginCancelStopsVisibleLogin(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:         "dead",
		Username:     "u",
		BaseURL:      "http://" + freeAddr(t) + "/",
		Timeout:      "300ms",
		LoginTimeout: "10s",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	type result struct {
		code int
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Post(srv.URL+"/api/accounts/"+acc.ID+"/login", "application/json", nil) //nolint:noctx // 测试不关心 ctx
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		done <- result{code: resp.StatusCode, body: string(b)}
	}()

	// 等到任务可见："登录中"一旦对外公布，取消就必须是真的能取消
	deadline := time.Now().Add(10 * time.Second)
	for !store.tasks.running(acc.ID, taskKindLogin) {
		if time.Now().After(deadline) {
			t.Fatal("登录任务一直没有出现在 /status 里")
		}
		time.Sleep(5 * time.Millisecond)
	}

	resp, err := http.Post(srv.URL+"/api/accounts/"+acc.ID+"/login/cancel", "application/json", nil) //nolint:noctx // 测试不关心 ctx
	if err != nil {
		t.Fatalf("POST login/cancel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("login/cancel = %d %s, want 200 ok", resp.StatusCode, body)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("登录请求失败: %v", r.err)
		}
		if r.code != http.StatusOK || !strings.Contains(r.body, "cancelled") {
			t.Fatalf("取消后 POST /login = %d %s, want 200 cancelled", r.code, r.body)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("取消之后登录请求没有返回：ctx 没有被真正取消")
	}

	snap := store.tasks.snapshot(acc.ID)
	if snap == nil {
		t.Fatal("没有任务快照")
	}
	if snap.Status != taskStatusCancelled {
		t.Errorf("任务状态 = %q, want %q", snap.Status, taskStatusCancelled)
	}
	if snap.Running {
		t.Error("取消后任务不应仍在运行")
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.loginMu.Lock()
	left := rt.loginCancel
	rt.loginMu.Unlock()
	if left != nil {
		t.Error("登录结束后所有权没有释放")
	}
}
