package protocol

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"school_sdk/protocol/cas2"
	"school_sdk/protocol/config"
)

// transportProfile 是共享连接池的分组键：只有这一组参数完全相同的账号，
// 才能共用同一个 *http.Transport。
//
// 身份不在传输层：cookie 是每请求的头、由各账号自己的 cookie jar 产生，
// 而 Go 的 stdlib 连接池按 (proxy, scheme, host) 分桶。因此同一档的账号
// 共用 transport 不会串号；反过来，代理 / TLS / 超时策略不同的账号不能共用。
type transportProfile struct {
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConnsPerHost   int // 0 表示沿用默认值（GOMAXPROCS+1）
	ProxyURL              string
	InsecureSkipVerify    bool
}

// TransportPool 按 transportProfile 惰性创建并复用底层 *http.Transport。
//
// 复用的只有传输层：resty.Client / http.Client / cookie jar 仍然每账号一份
// （见 newBasicClient）。因此共享池不会让 A 账号的 cookie 出现在 B 账号的
// 请求里——jar 才是身份。
//
// 空闲连接上限也随之下沉为"每档一份"：主链路 profile 的
// MaxIdleConnsPerHost 留空时用默认值（GOMAXPROCS+1），即所有账号合计
// 而不是每个账号各一份。单机少量账号（个位数）串行操作足够；若将来大量
// 账号并发抢课，再调大 mainTransportProfile 里的这个值。
type TransportPool struct {
	mu sync.Mutex
	m  map[transportProfile]*http.Transport
}

// NewTransportPool 返回一个空的连接池；零值 TransportPool 也可用（惰性初始化）。
func NewTransportPool() *TransportPool {
	return &TransportPool{m: make(map[transportProfile]*http.Transport)}
}

// transport 返回 profile 对应的共享 transport，第一次访问时创建。
func (p *TransportPool) transport(profile transportProfile) *http.Transport {
	if p == nil {
		return buildTransport(profile)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[transportProfile]*http.Transport)
	}
	if t, ok := p.m[profile]; ok {
		return t
	}
	t := buildTransport(profile)
	p.m[profile] = t
	return t
}

// CloseIdleConnections 关闭池内所有 transport 的空闲连接，释放 fd。
//
// 只应在进程 / 服务退出时调用：按账号调用会误伤其他账号正在复用的连接，
// 账号配置变更（dropRuntime）也不要关池。
func (p *TransportPool) CloseIdleConnections() {
	if p == nil {
		return
	}
	p.mu.Lock()
	transports := make([]*http.Transport, 0, len(p.m))
	for _, t := range p.m {
		transports = append(transports, t)
	}
	p.mu.Unlock()
	for _, t := range transports {
		t.CloseIdleConnections()
	}
}

// buildTransport 按配置档造一个 *http.Transport。
//
// 以 net/http 的 DefaultTransport 为模板（dialer 30s、代理取环境变量、
// ForceAttemptHTTP2、MaxIdleConns=100、IdleConnTimeout=90s 等），再按档
// 覆盖需要调整的字段。这是重构前 resty.NewWithTransportSettings 建出的
// transport 的同款默认值，但不必为了拿到 transport 先造一个用完即弃的
// resty.Client。
func buildTransport(profile transportProfile) *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()

	// resty 自行处理响应解压，这里保持 DisableCompression=true 与旧实现一致。
	t.DisableCompression = true

	if profile.MaxIdleConnsPerHost > 0 {
		t.MaxIdleConnsPerHost = profile.MaxIdleConnsPerHost
	} else {
		// 与旧实现（resty 默认）一致，而不是 stdlib 的 DefaultMaxIdleConnsPerHost=2。
		t.MaxIdleConnsPerHost = runtime.GOMAXPROCS(0) + 1
	}
	if profile.IdleConnTimeout > 0 {
		t.IdleConnTimeout = profile.IdleConnTimeout
	}
	t.ResponseHeaderTimeout = profile.ResponseHeaderTimeout
	if profile.ProxyURL != "" {
		if u, err := url.Parse(profile.ProxyURL); err == nil {
			t.Proxy = http.ProxyURL(u)
		}
	}
	if profile.InsecureSkipVerify {
		tlsCfg := t.TLSClientConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{}
		} else {
			tlsCfg = tlsCfg.Clone()
		}
		tlsCfg.InsecureSkipVerify = true
		t.TLSClientConfig = tlsCfg
	}
	return t
}

// proxyFromEnv 返回本地调试代理配置，与旧 NewBasicClient / NewCas 里
// proxy=1 的分支保持一致。
func proxyFromEnv() (proxyURL string, insecureSkipVerify bool) {
	if os.Getenv("proxy") == "1" {
		return "http://127.0.0.1:8866", true
	}
	return "", false
}

// mainTransportProfile 是普通 Web 客户端（NewBasicClient）的 transport 档，
// 参数与重构前 NewBasicClient 内联创建的 transport 一致。
func mainTransportProfile(cfg config.Settings) transportProfile {
	proxyURL, insecure := proxyFromEnv()
	p := transportProfile{
		ResponseHeaderTimeout: 35 * time.Second,
		ProxyURL:              proxyURL,
		InsecureSkipVerify:    insecure,
	}
	// http 沿用 90s 默认；https 缩短到 68s（原 NewBasicClient 行为）。
	if strings.HasPrefix(cfg.BaseURL, "https://") {
		p.IdleConnTimeout = 68 * time.Second
	}
	return p
}

// cas2TransportProfile 是门户登录链路的 transport 档，必须与 cas2.NewCas
// 自建 transport 的参数保持一致（见 cas2 包的 Transport* 常量）。
func cas2TransportProfile() transportProfile {
	proxyURL, insecure := proxyFromEnv()
	return transportProfile{
		ResponseHeaderTimeout: cas2.TransportResponseHeaderTimeout,
		IdleConnTimeout:       cas2.TransportIdleConnTimeout,
		MaxIdleConnsPerHost:   cas2.TransportMaxIdleConnsPerHost,
		ProxyURL:              proxyURL,
		InsecureSkipVerify:    insecure,
	}
}
