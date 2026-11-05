package cas2

import (
	"net/http"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// TestNewCasWithTransportUsesInjectedTransport 验证共享池注入后 cas2 链路
// 不再自建 transport，且同账号的 cas2 / portal 两个域仍共享 transport + jar。
func TestNewCasWithTransportUsesInjectedTransport(t *testing.T) {
	rt := &http.Transport{}
	c := NewCasWithTransport("u", "p", "", false, config.Settings{}, nil, rt)
	if got := c.http.Transport(); got != rt {
		t.Fatalf("cas2 transport = %v, want 注入的 transport", got)
	}
	if got := c.portalHttp.Transport(); got != rt {
		t.Fatalf("portal transport = %v, want 注入的 transport（Clone 共享底层 transport）", got)
	}
	if c.http.Client().Jar != c.portalHttp.Client().Jar {
		t.Fatal("同账号 cas2 / portal 应共享 cookie jar（既有行为）")
	}
}

// TestNewCasWithoutTransportKeepsDefaults 验证不注入时行为与旧版一致：
// transport 参数仍取自 Transport* 常量。
func TestNewCasWithoutTransportKeepsDefaults(t *testing.T) {
	c := NewCas("u", "p", "", false, config.Settings{}, nil)
	tr, ok := c.http.Transport().(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("自建 transport 类型不对: %T", c.http.Transport())
	}
	if tr.IdleConnTimeout != TransportIdleConnTimeout {
		t.Errorf("IdleConnTimeout = %v, want %v", tr.IdleConnTimeout, TransportIdleConnTimeout)
	}
	if tr.ResponseHeaderTimeout != TransportResponseHeaderTimeout {
		t.Errorf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, TransportResponseHeaderTimeout)
	}
	if tr.MaxIdleConnsPerHost != TransportMaxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", tr.MaxIdleConnsPerHost, TransportMaxIdleConnsPerHost)
	}
}

// 这些常量是 protocol.TransportPool 给 cas2 链路分组的依据，必须与
// NewCas 内联参数一致；写死期望值，防止以后只改一边。
func TestTransportConstants(t *testing.T) {
	// 放进表里比较，避免 IDE 把「常量 == 常量」当成恒真/恒假告警；
	// 常量被改动时这里仍会失败。
	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"TransportResponseHeaderTimeout", TransportResponseHeaderTimeout, 31 * time.Second},
		{"TransportIdleConnTimeout", TransportIdleConnTimeout, 68 * time.Second},
		{"TransportMaxIdleConnsPerHost", time.Duration(TransportMaxIdleConnsPerHost), 8},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
