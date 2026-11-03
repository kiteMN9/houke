package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/protocol"
)

func TestShouldRelogin(t *testing.T) {
	cases := []struct {
		name    string
		res     *protocol.PingResult
		busy    bool
		want    bool
		comment string
	}{
		{
			name: "网络错误不算掉线",
			res:  &protocol.PingResult{Status: 0, Message: "请求超时"},
			want: false,
		},
		{
			name: "会话有效",
			res:  &protocol.PingResult{OK: true, LoggedIn: true, Status: http.StatusOK},
			want: false,
		},
		{
			name: "200 但页面里已没有当前账号",
			res:  &protocol.PingResult{OK: true, Status: http.StatusOK, Message: "服务器可达，但当前未登录"},
			want: true,
		},
		{
			name: "302 到登录页",
			res:  &protocol.PingResult{Status: http.StatusFound, Message: "会话已失效（被重定向到登录页）"},
			want: true,
		},
		{
			name: "500 只是服务端异常",
			res:  &protocol.PingResult{OK: true, Status: http.StatusInternalServerError},
			want: false,
		},
		{
			name: "已有任务在跑时不重复登录",
			res:  &protocol.PingResult{OK: true, Status: http.StatusOK},
			busy: true,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldRelogin(c.res, c.busy); got != c.want {
				t.Errorf("shouldRelogin(%+v, busy=%v) = %v, want %v",
					c.res, c.busy, got, c.want)
			}
		})
	}
}

func TestShouldReloginNil(t *testing.T) {
	if shouldRelogin(nil, false) {
		t.Error("shouldRelogin(nil) = true, want false")
	}
}

// Ping 探到掉线时，开关关闭不应起后台重登。
func TestPingDoesNotReloginWhenDisabled(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `<html><body><div id="tips">请先登录</div></body></html>`)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	off := false
	acc, err := store.Create(&Account{
		Name:        "n",
		Username:    "u",
		BaseURL:     upstream.URL + "/",
		AutoRelogin: &off,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)

	srv := mustNewServer(t, store)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ping status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"relogin":false`) {
		t.Fatalf("关闭自动重登后 ping 仍返回 relogin=true: %s", body)
	}
	if !strings.Contains(body, "自动重登") {
		t.Fatalf("关闭自动重登后 hint 未说明原因: %s", body)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("ping 只应探测 1 次，实际 %d 次", got)
	}
}
