package web

import (
	"net/http"
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
