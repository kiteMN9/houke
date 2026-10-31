package web

import (
	"reflect"
	"testing"
	"time"

	"school_sdk/engine"
	baseCfg "school_sdk/protocol/config"
)

// 运行态必须带上账号配置的登录超时；改配置后重建运行态应拿到新值。
func TestBuildRuntimeUsesAccountLoginTimeout(t *testing.T) {
	s := newTestStore(t)

	created, err := s.Create(&Account{
		Name:         "n",
		Username:     "u",
		BaseURL:      "https://jwglxt.ycit.edu.cn/",
		LoginTimeout: "7m",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rt, err := s.runtime(created.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	if got := rt.longTaskTimeout(); got != 7*time.Minute {
		t.Errorf("longTaskTimeout() = %v, want 7m", got)
	}

	if _, err := s.Update(created.ID, &Account{LoginTimeout: "20m"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rt2, err := s.runtime(created.ID)
	if err != nil {
		t.Fatalf("runtime(重建): %v", err)
	}
	if rt2 == rt {
		t.Error("改配置后应重建运行态")
	}
	if got := rt2.longTaskTimeout(); got != 20*time.Minute {
		t.Errorf("重建后 longTaskTimeout() = %v, want 20m", got)
	}
}

func TestBuildRuntimeHonoursAccountConfig(t *testing.T) {
	s := newTestStore(t)

	created, err := s.Create(&Account{
		Name:         "n",
		Username:     "u",
		BaseURL:      "https://jwglxt.ycit.edu.cn/",
		UserAgent:    "MyApp/9.9",
		Hedging:      true,
		HedgingDelay: "7s",
		Routes:       []string{"route-a", "route-b"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := s.getRaw(created.ID)

	rt := mustBuildRuntime(t, s, a)
	cfg := rt.engine.Settings()

	if !cfg.Hedging {
		t.Error("settings.Hedging = false, want true")
	}
	if got, want := cfg.HedgingDelay.Std(), 7*time.Second; got != want {
		t.Errorf("settings.HedgingDelay = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(cfg.Routes, []string{"route-a", "route-b"}) {
		t.Errorf("settings.Routes = %v, want [route-a route-b]", cfg.Routes)
	}
	if cfg.UserAgent != "MyApp/9.9" {
		t.Errorf("settings.UserAgent = %q, want MyApp/9.9", cfg.UserAgent)
	}
}

func TestBuildRuntimeFallsBackToDefaultUA(t *testing.T) {
	s := newTestStore(t)

	// 绕过 Create 的归一化，模拟磁盘上遗留的非法 UA
	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := s.getRaw(created.ID)
	a.UserAgent = "legacy-garbage-ua"

	rt := mustBuildRuntime(t, s, a)
	if got, want := rt.engine.Settings().UserAgent, baseCfg.DefaultUA; got != want {
		t.Errorf("settings.UserAgent = %q, want %q", got, want)
	}
}

// 绕过 normalize 的路径也不能把非法 timeout 带进 client（旧代码会得到 0 → 各调用点各自兜底）。
func TestBuildRuntimeRepairsBadTimeout(t *testing.T) {
	s := newTestStore(t)

	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := s.getRaw(created.ID)
	a.Timeout = "not-a-duration"

	rt := mustBuildRuntime(t, s, a)
	if got, want := rt.engine.Settings().Timeout, baseCfg.Default().Timeout; got != want {
		t.Errorf("settings.Timeout = %v, want %v", got, want)
	}
}

// 绕过 normalize 的脏数据也不能进 client：toSettings 会再归一化一次。
func TestBuildRuntimeNormalizesBaseURL(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := s.getRaw(created.ID)
	a.BaseURL = "https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html"

	rt := mustBuildRuntime(t, s, a)
	if got, want := rt.engine.Settings().BaseURL, "https://jwglxt.ycit.edu.cn/"; got != want {
		t.Errorf("settings.BaseURL = %q, want %q", got, want)
	}
}

// buildRuntime 也不能抹掉子路径：toSettings 只去登录页尾巴，不重写前缀。
func TestBuildRuntimeKeepsSubPathBaseURL(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/jwglxt/xtgl/login_slogin.html",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := s.getRaw(created.ID)

	rt := mustBuildRuntime(t, s, a)
	if got, want := rt.engine.Settings().BaseURL, "https://jwglxt.ycit.edu.cn/jwglxt/"; got != want {
		t.Errorf("settings.BaseURL = %q, want %q", got, want)
	}
}

// 登录方式必须从 Account 一路传到 engine 构造：wechat 的二维码由
// OnQRCode 回调交给 web 运行态，见 login_qr_test.go。
func TestBuildRuntimeMapsLoginMode(t *testing.T) {
	cases := []struct {
		mode string
		want engine.LoginKind
	}{
		{LoginModePassword, engine.LoginPassword},
		{LoginModeCAS, engine.LoginCAS},
		{LoginModeWeChat, engine.LoginWeChat},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			s := newTestStore(t)
			created, err := s.Create(&Account{
				Name:      "n",
				Username:  "u",
				BaseURL:   "https://jwglxt.ycit.edu.cn/",
				LoginMode: tc.mode,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			a, _ := s.getRaw(created.ID)
			rt := mustBuildRuntime(t, s, a)
			if got := rt.engine.LoginKind(); got != tc.want {
				t.Errorf("LoginKind() = %v, want %v", got, tc.want)
			}
		})
	}
}

// 配置不合法时 buildRuntime 返回错误，由路由层变成 500，而不是带着坏配置发请求。
func TestBuildRuntimeRejectsInvalidConfig(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := s.getRaw(created.ID)
	a.BaseURL = "" // 模拟手改 accounts.json 后的脏数据

	if _, err := buildRuntime(s, a.ID, a); err == nil {
		t.Fatal("空 baseURL 应返回错误")
	}
	// 错误不能缓存成运行时
	if _, err := s.runtime(created.ID); err == nil {
		t.Fatal("getRuntime 应透传配置错误")
	}
}
