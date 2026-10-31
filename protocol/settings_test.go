package protocol

import (
	"slices"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// Settings() 必须返回副本：web 会拿它做展示，改返回值不能影响正在跑的客户端。
func TestAPIClientSettingsIsCopy(t *testing.T) {
	cfg := config.Settings{
		BaseURL:   "https://jwglxt.ycit.edu.cn/",
		Account:   "u",
		UserAgent: config.ChromeUA,
		Routes:    []string{"user-1"},
		Timeout:   config.Duration(43 * time.Second),
	}
	c := mustNewAPIClient(t, cfg, nil, false, false, "")

	got := c.Settings()
	if got.Account != "u" {
		t.Fatalf("Settings().Account = %q, want u", got.Account)
	}
	got.Routes[0] = "mutated"
	if !slices.Equal(c.Settings().Routes, []string{"user-1"}) {
		t.Fatalf("Settings() 与内部配置共享了 Routes: %v", c.Settings().Routes)
	}

	var nilClient *APIClient
	if got := nilClient.Settings(); got.Account != "" {
		t.Errorf("nil 客户端应返回零值配置: %+v", got)
	}
}

// baseURL 非法时构造失败而不是退出进程；调用方（Web 请求、测试）有机会把错误展示给用户。
func TestNewAPIClientRejectsBadBaseURL(t *testing.T) {
	cfg := config.Default()
	cfg.Account = "u"

	bad := []string{
		"jwglxt.ycit.edu.cn/xtgl/login_slogin.html",
		"https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html",
	}
	for _, base := range bad {
		cfg.BaseURL = base
		if _, err := NewAPIClient(cfg, nil, false, false, ""); err == nil {
			t.Errorf("baseURL %q 应返回错误", base)
		}
	}
}

// 目录部署在 /jwglxt 这类前缀下也要能用：resty 把以 "/" 开头的请求路径
// 直接拼在前缀后面（TrimRight(baseURL,"/") + url），子路径会原样保留。
func TestNewAPIClientAcceptsSubPathBaseURL(t *testing.T) {
	cfg := config.Default()
	cfg.Account = "u"
	cfg.BaseURL = "https://jwglxt.ycit.edu.cn/jwglxt/"

	c, err := NewAPIClient(cfg, nil, false, false, "")
	if err != nil {
		t.Fatalf("子路径 baseURL 构造失败: %v", err)
	}
	if got, want := c.Settings().BaseURL, cfg.BaseURL; got != want {
		t.Errorf("settings.BaseURL = %q, want %q", got, want)
	}
	if got, want := c.Http.BaseURL(), cfg.BaseURL; got != want {
		t.Errorf("resty BaseURL = %q, want %q", got, want)
	}
}
