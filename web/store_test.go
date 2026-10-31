package web

import (
	"reflect"
	"testing"
	"time"

	baseCfg "school_sdk/protocol/config"
)

// 登录超时是账号级配置：默认 10 分钟，非法值回落默认值，未提交时不覆盖旧值。
func TestLoginTimeoutConfigurable(t *testing.T) {
	s := newTestStore(t)

	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, bad := range []string{"abc", "-5m", "0"} {
		got, err := s.Update(created.ID, &Account{LoginTimeout: bad})
		if err != nil {
			t.Fatalf("Update(%q): %v", bad, err)
		}
		if got.LoginTimeout != defaultLoginTimeout {
			t.Errorf("Update(%q) 后 LoginTimeout = %q, want %q（非法值应回落默认）",
				bad, got.LoginTimeout, defaultLoginTimeout)
		}
	}

	got, err := s.Update(created.ID, &Account{LoginTimeout: "25m"})
	if err != nil {
		t.Fatalf("Update(25m): %v", err)
	}
	if got.LoginTimeout != "25m" {
		t.Errorf("LoginTimeout = %q, want 25m", got.LoginTimeout)
	}
	if d := got.loginTimeoutDuration(); d != 25*time.Minute {
		t.Errorf("loginTimeoutDuration() = %v, want 25m", d)
	}

	// 前端未提交该字段（空字符串）时保持原值
	got, err = s.Update(created.ID, &Account{Name: "n2"})
	if err != nil {
		t.Fatalf("Update(name only): %v", err)
	}
	if got.LoginTimeout != "25m" {
		t.Errorf("只改名字后 LoginTimeout = %q, want 25m", got.LoginTimeout)
	}
}

func TestUpdateHedgingAndRoutes(t *testing.T) {
	s := newTestStore(t)

	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := s.Update(created.ID, &Account{
		Hedging:      true,
		HedgingDelay: "12s",
		Routes:       []string{"r1", "r2"},
		UserAgent:    "no-slash",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, ok := s.Get(created.ID)
	if !ok {
		t.Fatal("账号丢失")
	}
	if !got.Hedging {
		t.Error("Hedging 未生效")
	}
	if got.HedgingDelay != "12s" {
		t.Errorf("HedgingDelay = %q, want 12s", got.HedgingDelay)
	}
	if !reflect.DeepEqual(got.Routes, []string{"r1", "r2"}) {
		t.Errorf("Routes = %v, want [r1 r2]", got.Routes)
	}
	// 更新时的非法 UA 也应被归一化
	if got.UserAgent != baseCfg.DefaultUA {
		t.Errorf("UserAgent = %q, want %q", got.UserAgent, baseCfg.DefaultUA)
	}

	// 显式传空数组应清空 routes
	if _, err := s.Update(created.ID, &Account{Routes: []string{}}); err != nil {
		t.Fatalf("Update(clear routes): %v", err)
	}
	got, _ = s.Get(created.ID)
	if len(got.Routes) != 0 {
		t.Errorf("Routes = %v, want 空", got.Routes)
	}

	// Routes 为 nil（前端未提交）时不应覆盖已有值
	if _, err := s.Update(created.ID, &Account{Routes: []string{"keep"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := s.Update(created.ID, &Account{Name: "n2"}); err != nil {
		t.Fatalf("Update(name only): %v", err)
	}
	got, _ = s.Get(created.ID)
	if !reflect.DeepEqual(got.Routes, []string{"keep"}) {
		t.Errorf("Routes = %v, want [keep]", got.Routes)
	}
}

// 登录方式的两条更新路径：新前端传 loginMode，旧前端只传 casLogin。
// 关键约束：旧前端不认识 wechat，不能用它默认的 casLogin=false 把扫码账号降级。
func TestUpdateLoginMode(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	get := func() *Account {
		t.Helper()
		a, ok := s.Get(created.ID)
		if !ok {
			t.Fatal("账号丢失")
		}
		return a
	}

	if got := get(); got.LoginMode != LoginModePassword || got.CasLogin {
		t.Fatalf("默认 = %+v，want password / casLogin=false", got)
	}

	// 旧前端只传 casLogin=true
	if _, err := s.Update(created.ID, &Account{CasLogin: true}); err != nil {
		t.Fatalf("Update(casLogin=true): %v", err)
	}
	if got := get(); got.LoginMode != LoginModeCAS || !got.CasLogin {
		t.Errorf("casLogin=true 后 = %+v，want cas / CasLogin=true", got)
	}

	// 旧前端取消 CAS
	if _, err := s.Update(created.ID, &Account{CasLogin: false}); err != nil {
		t.Fatalf("Update(casLogin=false): %v", err)
	}
	if got := get(); got.LoginMode != LoginModePassword || got.CasLogin {
		t.Errorf("casLogin=false 后 = %+v，want password / CasLogin=false", got)
	}

	// 新前端显式 wechat（同时带的 casLogin=false 不应反向覆盖）
	if _, err := s.Update(created.ID, &Account{LoginMode: LoginModeWeChat, CasLogin: false}); err != nil {
		t.Fatalf("Update(wechat): %v", err)
	}
	if got := get(); got.LoginMode != LoginModeWeChat || got.CasLogin {
		t.Errorf("wechat 后 = %+v，want wechat / CasLogin=false", got)
	}

	// 旧前端编辑扫码账号时只传 name，casLogin 是零值 false：不能降级
	if _, err := s.Update(created.ID, &Account{Name: "n2"}); err != nil {
		t.Fatalf("Update(name only): %v", err)
	}
	if got := get(); got.LoginMode != LoginModeWeChat {
		t.Errorf("旧前端只改 name 后 LoginMode = %q，want wechat", got.LoginMode)
	}
}
