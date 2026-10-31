package config

import (
	"encoding/json/v2"
	"testing"
	"time"
)

func TestDefaultIsUsable(t *testing.T) {
	d := Default()
	// 凭据不属于默认值：Default() 只保证"除凭据外"的配置可用。
	d.Account = "u"
	if err := d.Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v，默认配置必须能直接用来发请求", err)
	}
	if d.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", d.BaseURL, DefaultBaseURL)
	}
	if d.Timeout != Duration(DefaultTimeout) {
		t.Errorf("Timeout = %v, want %v", d.Timeout, DefaultTimeout)
	}
	if d.HedgingDelay != Duration(DefaultHedgingDelay) {
		t.Errorf("HedgingDelay = %v, want %v", d.HedgingDelay, DefaultHedgingDelay)
	}
	if d.Want != DefaultWant {
		t.Errorf("Want = %q, want %q", d.Want, DefaultWant)
	}
	if d.UserAgent != DefaultUA {
		t.Errorf("UserAgent = %q, want %q", d.UserAgent, DefaultUA)
	}
}

func TestParseDuration(t *testing.T) {
	ok := []struct {
		in   string
		want time.Duration
	}{
		{"43s", 43 * time.Second},
		{"10m", 10 * time.Minute},
		{"1m30s", 90 * time.Second},
		{" 43s ", 43 * time.Second}, // 前后空白可容忍
	}
	for _, tc := range ok {
		got, err := ParseDuration(tc.in)
		if err != nil {
			t.Errorf("ParseDuration(%q) 返回错误: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	bad := []string{"", "   ", "43", "abc", "0s", "-1s", "43 s"}
	for _, in := range bad {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, 期望报错", in, got)
		}
	}
}

// Duration 在 JSON 里保持 "43s" 这种写法，解析失败必须报错而不是静默变 0。
func TestDurationJSON(t *testing.T) {
	b, err := json.Marshal(Settings{Timeout: Duration(35 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"timeout":"35s"`; !contains(string(b), want) {
		t.Errorf("Marshal = %s，缺少 %s", b, want)
	}

	// 预填充 + Unmarshal：没写的字段保持默认值，这就是"稀疏配置"的正确行为。
	got := Default()
	if err := json.Unmarshal([]byte(`{"account":"u","timeout":"35s","routes":["a"]}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Timeout != Duration(35*time.Second) {
		t.Errorf("Timeout = %v, want 35s", got.Timeout)
	}
	if got.BaseURL != DefaultBaseURL || got.HedgingDelay != Duration(DefaultHedgingDelay) {
		t.Errorf("缺省字段没有保持默认值: %+v", got)
	}
	if len(got.Routes) != 1 || got.Routes[0] != "a" {
		t.Errorf("Routes = %v, want [a]", got.Routes)
	}

	var d Duration
	if err := json.Unmarshal([]byte(`"43"`), &d); err == nil {
		t.Error(`"43"（缺单位）应报错`)
	}
	if err := json.Unmarshal([]byte(`43`), &d); err == nil {
		t.Error("数字应报错，必须是带单位的字符串")
	}
	if err := json.Unmarshal([]byte(`"-5s"`), &d); err == nil {
		t.Error("负数应报错")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestValidate(t *testing.T) {
	base := func() Settings {
		d := Default()
		d.Account = "u"
		return d
	}

	d := base()
	if err := d.Validate(); err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"account 为空", func(d *Settings) { d.Account = "  " }},
		{"url 缺 scheme", func(d *Settings) { d.BaseURL = "jwglxt.ycit.edu.cn/" }},
		{"url 带路径", func(d *Settings) { d.BaseURL = "https://jwglxt.ycit.edu.cn/jwglxt/xtgl/login_slogin.html" }},
		{"ua 非法", func(d *Settings) { d.UserAgent = "没有斜杠" }},
		{"timeout 为零", func(d *Settings) { d.Timeout = 0 }},
		{"timeout 为负", func(d *Settings) { d.Timeout = Duration(-5 * time.Second) }},
		{"开启对冲但间隔为零", func(d *Settings) { d.Hedging = true; d.HedgingDelay = 0 }},
	}
	for _, tc := range cases {
		d := base()
		tc.mutate(&d)
		if err := d.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil，期望报错", tc.name)
		}
	}

	// 未开启对冲时不校验 hedgingDelay：这个值根本不会被用到。
	d = base()
	d.HedgingDelay = 0
	if err := d.Validate(); err != nil {
		t.Errorf("未开启对冲时不应校验 hedgingDelay: %v", err)
	}
}
