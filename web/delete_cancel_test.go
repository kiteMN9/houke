package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 删除账号必须停掉正在跑的任务：账号一删，/login/cancel、/init/cancel、
// /auto-choose/cancel 都只会拿到「账号不存在」，再没有任何接口能取消它，
// 任务会一直跑到自己的预算（自动选课默认 24 小时），期间仍然登录、抓页面、
// 提交选课。取消函数只剩任务表里那一份，删任务时必须先把它摘出来调用。
func TestDeleteCancelsRunningTask(t *testing.T) {
	for _, kind := range []string{taskKindLogin, taskKindInit, taskKindAuto} {
		t.Run(kind, func(t *testing.T) {
			store := newTestStore(t)
			acc, err := store.Create(&Account{
				Name:     "n",
				Username: "u",
				BaseURL:  "https://jwglxt.ycit.edu.cn/",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// 故意不建运行态：删除路径靠任务表停任务，这也正是 /init 先登记
			// 任务、后写 rt.initCancel 的那个窗口（运行态里可能还没有副本）。
			if _, ok := store.tasks.startCancelable(acc.ID, kind, cancel); !ok {
				t.Fatal("startCancelable 应当成功")
			}

			if err := store.Delete(acc.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("删除账号后任务 ctx 仍然存活：任务会跑到自己的超时")
			}
			if snap := store.tasks.snapshot(acc.ID); snap != nil {
				t.Fatalf("删除后任务状态没有清理: %+v", snap)
			}
			if store.tasks.takeCancel(acc.ID, kind, true) {
				t.Fatal("删除后任务表里还留着取消函数")
			}
			if _, ok := store.Get(acc.ID); ok {
				t.Fatal("账号没有被删除")
			}
		})
	}
}

// 删号时正在进行的登录必须立刻收尾：DELETE 之后没有任何接口还能取消它，
// 旧实现会让这次登录挂到 LoginTimeout（默认 10 分钟）才结束。
func TestDeleteAccountStopsVisibleLogin(t *testing.T) {
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

	// 等任务真正可见：任务一出现，删除就必须能把它停掉。
	deadline := time.Now().Add(10 * time.Second)
	for !store.tasks.running(acc.ID, taskKindLogin) {
		if time.Now().After(deadline) {
			t.Fatal("登录任务一直没有出现在 /status 里")
		}
		time.Sleep(5 * time.Millisecond)
	}

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/accounts/"+acc.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE = %d %s, want 200", resp.StatusCode, body)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("登录请求失败: %v", r.err)
		}
		if r.code != http.StatusOK || !strings.Contains(r.body, "cancelled") {
			t.Fatalf("删除后 POST /login = %d %s, want 200 cancelled", r.code, r.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("删除账号后登录请求没有返回：任务 ctx 没有被取消")
	}
}

// 编辑账号会重建运行态（Update → dropRuntime），取消按钮不能因此变成 noop：
// 取消函数还登记在任务上，必须回退到任务里那份（与 cancelLogin 一致），
// 否则初始化/自动选课会继续跑到超时或预算用尽。
func TestCancelFallsBackToTaskAfterRuntimeRebuild(t *testing.T) {
	cases := []struct {
		name string
		kind string
		path string
	}{
		{"init", taskKindInit, "/init/cancel"},
		{"auto", taskKindAuto, "/auto-choose/cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			acc, err := store.Create(&Account{
				Name:     "n",
				Username: "u",
				BaseURL:  "https://jwglxt.ycit.edu.cn/",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := store.runtime(acc.ID); err != nil {
				t.Fatalf("runtime: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, ok := store.tasks.startCancelable(acc.ID, tc.kind, cancel); !ok {
				t.Fatal("startCancelable 应当成功")
			}

			// 等价于"任务运行期间改了一次账号配置"：运行态里那份 cancel
			// 随运行态一起消失，只剩任务表里登记的这一份。
			store.dropRuntime(acc.ID)

			srv := mustNewServer(t, store)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+tc.path, nil)
			srv.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
				t.Fatalf("POST %s = %d %s, want 200 ok（运行态重建后取消成了无效按钮）",
					tc.path, rec.Code, rec.Body.String())
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("取消函数没有被调用：任务会一直跑到超时")
			}
			snap := store.tasks.snapshot(acc.ID)
			if snap == nil {
				t.Fatal("没有任务快照")
			}
			if snap.Status != taskStatusCancelled || snap.Running {
				t.Fatalf("任务状态 = %q running=%v, want cancelled", snap.Status, snap.Running)
			}
		})
	}
}
