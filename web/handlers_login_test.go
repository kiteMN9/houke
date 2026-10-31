package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginTimeoutText(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{10 * time.Minute, "10 分钟"},
		{3 * time.Minute, "3 分钟"},
		{30 * time.Second, "30 秒"},
		{90 * time.Second, "1.5 分钟"},
		{2 * time.Hour, "2 小时"},
	}
	for _, c := range cases {
		if got := loginTimeoutText(c.in); got != c.want {
			t.Errorf("loginTimeoutText(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 超时收尾的文案必须带上账号实际配置的时长，不能再写死"3 分钟"。
func TestFinishLoginTaskUsesConfiguredTimeout(t *testing.T) {
	store := newTestStore(t)
	srv := mustNewServer(t, store)
	const id = "acc1"

	if _, ok := store.tasks.start(id, taskKindLogin); !ok {
		t.Fatal("start 应当成功")
	}
	srv.finishLoginTask(id, context.DeadlineExceeded, 25*time.Minute)

	snap := store.tasks.snapshot(id)
	if snap == nil {
		t.Fatal("没有任务快照")
	}
	if snap.Status != taskStatusTimeout {
		t.Errorf("任务状态 = %q, want %q", snap.Status, taskStatusTimeout)
	}
	last := snap.Events[len(snap.Events)-1]
	if !strings.Contains(last.Text, "25 分钟") {
		t.Errorf("收尾文案 = %q, want 含「25 分钟」", last.Text)
	}
}

// 登录超时必须来自账号配置：对着一个"没人监听"的地址（请求秒失败、客户端会
// 重试到超时），1 秒预算用尽后应当收到 504，且任务快照落到 timeout 状态。
//
// 不用 httptest 挂起服务器：客户端在超时后的收尾步骤里不检查 ctx，
// 挂起服务器会让用例多跑 20 多秒。连接被拒绝的地址既快又确定。
func TestLoginHandlerHonoursAccountLoginTimeout(t *testing.T) {
	// 占一个端口再立刻释放，得到一个"保证被拒绝"的地址
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:         "dead",
		Username:     "u",
		BaseURL:      "http://" + addr + "/",
		Timeout:      "300ms", // 单次请求超时，缩短用例耗时
		LoginTimeout: "1s",    // 整个登录任务的预算
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Post(srv.URL+"/api/accounts/"+acc.ID+"/login", "application/json", nil) //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("POST login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d (%s, 耗时 %v), want 504", resp.StatusCode, body, elapsed)
	}
	if !strings.Contains(string(body), "登录超时（1 秒）") {
		t.Errorf("body = %s, want 含「登录超时（1 秒）」", body)
	}
	// 预算 1s：留足余量，但绝不允许退化成"跑满硬编码的 3 分钟"
	if elapsed > 30*time.Second {
		t.Errorf("登录耗时 %v，登录超时没有生效", elapsed)
	}

	snap := store.tasks.snapshot(acc.ID)
	if snap == nil {
		t.Fatal("没有任务快照")
	}
	if snap.Status != taskStatusTimeout {
		t.Errorf("任务状态 = %q, want %q", snap.Status, taskStatusTimeout)
	}
}

// /status 的登录态直接读 client：web 不再自己存一份 loggedIn，
// 所以 client 说已登录，接口就必须说已登录（反之亦然）。
func TestStatusReadsLoggedInFromClient(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)

	statusLoggedIn := func() bool {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			LoggedIn bool `json:"loggedIn"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
		}
		return body.LoggedIn
	}

	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}

	if statusLoggedIn() {
		t.Error("新建运行态 loggedIn = true, want false")
	}

	rt.engine.TestSetLoggedIn(true)
	if !statusLoggedIn() {
		t.Error("client 已登录时 /status 仍报未登录")
	}

	rt.engine.TestSetLoggedIn(false)
	if statusLoggedIn() {
		t.Error("client 未登录时 /status 仍报已登录")
	}
}
