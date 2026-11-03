package web

import (
	"testing"

	baseCfg "school_sdk/protocol/config"
)

// 自动重登默认开启：老账号没有 autoRelogin 字段时不能被当成"关闭"。
// 只有显式写入 false 才关闭；显式 true 原样保留。
func TestNormalizeAutoRelogin(t *testing.T) {
	a := &Account{}
	a.normalize()
	if !a.autoReloginEnabled() {
		t.Error("未设置 autoRelogin 时应默认开启")
	}

	off := false
	b := &Account{AutoRelogin: &off}
	b.normalize()
	if b.autoReloginEnabled() {
		t.Error("显式关闭后 autoReloginEnabled() = true")
	}

	on := true
	c := &Account{AutoRelogin: &on}
	c.normalize()
	if !c.autoReloginEnabled() {
		t.Error("显式开启后 autoReloginEnabled() = false")
	}
}

func TestNormalizeAutoNotify(t *testing.T) {
	a := &Account{}
	a.normalize()
	if !a.autoNotifyEnabled() {
		t.Error("未设置 autoNotify 时应默认开启")
	}

	off := false
	b := &Account{AutoNotify: &off}
	b.normalize()
	if b.autoNotifyEnabled() {
		t.Error("显式关闭后 autoNotifyEnabled() = true")
	}

	on := true
	c := &Account{AutoNotify: &on}
	c.normalize()
	if !c.autoNotifyEnabled() {
		t.Error("显式开启后 autoNotifyEnabled() = false")
	}
}

func TestAccountNormalizeOnCreate(t *testing.T) {
	s := newTestStore(t)

	a, err := s.Create(&Account{
		Name:      "n",
		Username:  "u",
		BaseURL:   "https://jwglxt.ycit.edu.cn/",
		UserAgent: "非法UA(没有斜杠)",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if a.UserAgent != baseCfg.DefaultUA {
		t.Errorf("UserAgent = %q, want 默认 UA %q", a.UserAgent, baseCfg.DefaultUA)
	}
	if a.Timeout != "43s" {
		t.Errorf("Timeout = %q, want 43s", a.Timeout)
	}
	if a.HedgingDelay != "21s" {
		t.Errorf("HedgingDelay = %q, want 21s", a.HedgingDelay)
	}
	if a.LoginTimeout != defaultLoginTimeout {
		t.Errorf("LoginTimeout = %q, want %q", a.LoginTimeout, defaultLoginTimeout)
	}
	if got := a.loginTimeoutDuration(); got != fallbackLoginTimeout {
		t.Errorf("loginTimeoutDuration() = %v, want %v", got, fallbackLoginTimeout)
	}
}

// 磁盘上的脏时长不能让登录秒超时，也不能被带进 client。
func TestNormalizeRepairsBadDurations(t *testing.T) {
	a := &Account{
		Timeout:      "abc",
		HedgingDelay: "0s",
		LoginTimeout: "nope",
		UserAgent:    "没有斜杠",
	}
	a.normalize()

	want := baseCfg.Default()
	if a.Timeout != want.Timeout.String() {
		t.Errorf("Timeout = %q, want %q", a.Timeout, want.Timeout.String())
	}
	if a.HedgingDelay != want.HedgingDelay.String() {
		t.Errorf("HedgingDelay = %q, want %q", a.HedgingDelay, want.HedgingDelay.String())
	}
	if a.LoginTimeout != defaultLoginTimeout {
		t.Errorf("LoginTimeout = %q, want %q", a.LoginTimeout, defaultLoginTimeout)
	}
	if a.UserAgent != baseCfg.DefaultUA {
		t.Errorf("UserAgent = %q, want %q", a.UserAgent, baseCfg.DefaultUA)
	}

	// 合法值必须原样保留
	b := &Account{Timeout: "35s", HedgingDelay: "7s", LoginTimeout: "25m", UserAgent: "MyApp/1.0"}
	b.normalize()
	if b.Timeout != "35s" || b.HedgingDelay != "7s" || b.LoginTimeout != "25m" {
		t.Errorf("合法时长被改写: %+v", b)
	}
}

// 历史账号里存着登录页地址：normalize 应该收敛成主机根，
// 而不是让 client 在发请求时悄悄截断（截断后的地址还和用户填的不一致）。
func TestNormalizeStripsPathFromBaseURL(t *testing.T) {
	a := &Account{BaseURL: " https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html?time=1 "}
	a.normalize()
	if want := "https://jwglxt.ycit.edu.cn/"; a.BaseURL != want {
		t.Errorf("BaseURL = %q, want %q", a.BaseURL, want)
	}
}

// 目录部署在 /jwglxt 这类子路径下：只剥掉登录页尾巴，前缀必须保留。
func TestNormalizeKeepsSubPathBaseURL(t *testing.T) {
	a := &Account{BaseURL: "https://jwglxt.ycit.edu.cn/jwglxt/xtgl/login_slogin.html"}
	a.normalize()
	if want := "https://jwglxt.ycit.edu.cn/jwglxt/"; a.BaseURL != want {
		t.Errorf("BaseURL = %q, want %q", a.BaseURL, want)
	}
}

// 登录方式：loginMode 优先，历史账号只有 casLogin 时按它推导；
// CasLogin 只是兼容镜像，wechat 不再写成 casLogin=true。
func TestNormalizeLoginMode(t *testing.T) {
	cases := []struct {
		name string
		in   Account
		want string
		cas  bool
	}{
		{"默认", Account{}, LoginModePassword, false},
		{"历史 casLogin", Account{CasLogin: true}, LoginModeCAS, true},
		{"显式 wechat", Account{LoginMode: LoginModeWeChat, CasLogin: true}, LoginModeWeChat, false},
		{"显式 password 覆盖 casLogin", Account{LoginMode: LoginModePassword, CasLogin: true}, LoginModePassword, false},
		{"非法值回落 casLogin", Account{LoginMode: "oops", CasLogin: true}, LoginModeCAS, true},
	}
	for _, tc := range cases {
		a := tc.in
		a.normalize()
		if a.LoginMode != tc.want {
			t.Errorf("%s: LoginMode = %q, want %q", tc.name, a.LoginMode, tc.want)
		}
		if a.CasLogin != tc.cas {
			t.Errorf("%s: CasLogin = %v, want %v", tc.name, a.CasLogin, tc.cas)
		}
	}
}
