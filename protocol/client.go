package protocol

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"school_sdk/protocol/cas2"
	"school_sdk/protocol/config"
	"school_sdk/protocol/hedge"
	"school_sdk/protocol/internal"
	"school_sdk/utils"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"resty.dev/v3"
)

// InputRequest 表示一次需要用户交互的请求。
type InputRequest struct {
	Kind      string // "password" | "captcha"
	Prompt    string // 给用户看的提示
	ImageData []byte // kind == "captcha" 时非空
}

// InputHandler 由上层注入；返回用户输入内容。
// ctx 取消时应尽快返回错误。
type InputHandler func(ctx context.Context, req *InputRequest) (string, error)

// QRCode 是一次微信扫码登录的二维码快照（定义在 cas2，见 cas2.QRCode）。
// 上层只读取 Image / ExpiresAt / Attempt，不需要知道轮询细节。
type QRCode = cas2.QRCode

// ProtocolHook 由上层注入：每个 HTTP 请求真正发出之前调用一次。
// 用于 WebUI 展示"当前卡在哪一步"，实现必须快速返回且不得阻塞、不得 panic。

// APIClient 包装Resty客户端，动态应用配置
type APIClient struct {
	// cfg 是构造时传入的用户配置拷贝：client 只读，改不到调用方那份。
	// 会话产物（JWT / 学到的 route / 修正过的地址）走 sess，持久化归上层。
	cfg              config.Settings
	Name             string
	Http             *resty.Client
	hedgeC           *resty.Client
	onlyCookieMethod bool
	enableCas2       bool
	cas2Client       *cas2.Client
	// 会话产物（JWT / 学到的 route / 修正过的地址）：上层注入，client 只改内存。
	sess            *config.Session
	lastRequestTime time.Time

	// 登录态由 client 自己维护（见 loginState.go）：登录成功 / 登出 /
	// 会话探测失效 / 业务响应里发现"未登录"都会更新它，上层只读，不再各存一份。
	isLoggedIn atomic.Bool

	// lastLoginSuccess 是最近一次登录成功的 UnixNano（0 表示还没成功过）。
	// 只用于判断"服务端说未登录"的响应是不是旧会话的迟到回包：早于最近一次
	// 登录成功的响应不能把新会话标成未登录。
	lastLoginSuccess atomic.Int64

	// 选课参数过期信号（见 paramRefresh.go）：列表/详情接口遇到“加密串错误”
	// 这类“刷新网页重试”的提示时置位。上层据此重抓首页参数再重试；
	// 用 Swap 取走，保证并发请求里只有一个去重新抓首页。
	paramRefresh atomic.Bool

	// 登录流程的串行锁 / 冷静期 / 滑块标记 / 并发请求闸门（见 loginState.go）。
	// 每个账号一份，互不干扰。
	login loginCoord

	// 登录阶段由 client 自己记账（见 login_stage.go）：hook 缺席时上层仍能
	// 读到"正在取登录页 / 正在换票"之类的语义，不必反推 URL。
	loginStage loginStageState

	// 初始化阶段同样由 client 记账（见 init_stage.go）：抓选课参数的过程
	// 不再依赖 hook 的 URL 分类。
	initStage initStageState

	// 新增：Web / 服务模式下需要人工输入时使用；未设置时登录会报错退出，
	// 协议层不再读取 stdin。
	InputHandler InputHandler

	// Kaptcha 是字符验证码的自动识别器（见 kaptcha.go）；nil 表示只走人工输入。
	// 它和 InputHandler 是互补关系：先自动识别，识别不了再问人。
	Kaptcha KaptchaRecognizer

	// 新增：WebUI 长任务可观测性用；nil 时不做任何上报。
	ProtocolHook internal.ProtocolHook
}

// attachHooks 把请求观察钩子挂到所有会用到的 resty 客户端上。
// 只做请求前上报，不改变请求本身的行为。
//
// cas2 门户登录链路由 cas2.Client 自己的 resty 客户端承担
// （cas2.ycit.edu.cn 与 portal.ycit.edu.cn 两个 base），
// 因此这里必须一并下发；否则门户登录期间 WebUI 会长时间空转无阶段。
func attachHooks(c *APIClient) {
	if c == nil {
		return
	}
	if c.cas2Client != nil {
		c.cas2Client.SetProtocolHook(c.ProtocolHook)
	}
	if c.ProtocolHook == nil {
		return
	}
	mw := internal.Middleware(c.ProtocolHook)
	if c.Http != nil {
		c.Http.AddRequestMiddleware(mw)
	}
	if c.hedgeC != nil && c.hedgeC != c.Http {
		c.hedgeC.AddRequestMiddleware(mw)
	}
}

// SetProtocolHook 挂载请求观察钩子（挂载后再补钩子也能生效）。
// 钩子会同时下发给 cas2 门户登录链路的 resty 客户端。
func (a *APIClient) SetProtocolHook(h internal.ProtocolHook) {
	if a == nil {
		return
	}
	a.ProtocolHook = h
	attachHooks(a)
}

// SetQRHandler 挂载微信扫码登录的二维码回调；传 nil 取消。
//
// 只有以 WX=true 构造的 client 才会产生二维码。回调在登录 goroutine 上
// 同步执行，必须快速返回；qr == nil 表示当前二维码已失效。
func (a *APIClient) SetQRHandler(h func(*QRCode)) {
	if a == nil || a.cas2Client == nil {
		return
	}
	a.cas2Client.SetQRHandler(h)
}

// NewBasicClient 用 Settings 构造 resty 客户端；请求超时取 cfg.Timeout。
//
// 它不校验 baseURL：调用方（NewAPIClient / NewClientWithCookieJar）已经用
// config.ValidateBaseURL 把过关，这里只负责组装。
func NewBasicClient(cfg config.Settings) (*resty.Client, *resty.Client) {
	baseURL := cfg.BaseURL
	client := resty.NewWithTransportSettings(&resty.TransportSettings{
		ResponseHeaderTimeout: 35 * time.Second,
		DisableKeepAlives:     false,
	}).
		//SetRedirectPolicy(resty.RedirectNoPolicy()).
		SetRedirectPolicy(resty.RedirectPolicyFunc(func(req *http.Request, via []*http.Request) error {
			if req.Response != nil {
				if req.Response.StatusCode == http.StatusMovedPermanently {
					return nil
				}
			}
			return http.ErrUseLastResponse
		})).
		SetBaseURL(baseURL)
	client.Client().Timeout = 55 * time.Second //

	if os.Getenv("proxy") == "1" {
		client.SetProxy("http://127.0.0.1:8866")
		if tls_ := client.TLSClientConfig(); tls_ != nil {
			tls_.InsecureSkipVerify = true
			//client.SetCloseConnection(true)
		}
		transport, err := client.HTTPTransport()
		if err == nil {
			transport.DisableKeepAlives = false
		}
	}

	if os.Getenv("trace") == "1" {
		client.SetTrace(true)
	}
	timeout := max(cfg.Timeout.Std(), 4*time.Second)
	client.SetTimeout(timeout) // ctx控制整个请求的超时时间包括读取响应体
	client.SetRateLimiter(resty.NewRateLimitSlidingWindow(10, 4*time.Second))
	client.SetRetryCount(3).
		AddRetryConditions(resty.RetryConditionStatus5XX)

	//refer, err := JoinURL(baseURL, baseCfg.LoginIndex)
	//if err != nil {
	//	log.Fatal(err)
	//}
	//client.SetHeader("Referer", refer)
	client.SetHeader("user-agent", cfg.UserAgent)

	client.AddContentDecompresser("br", internal.DecompressBrotli)

	if strings.HasPrefix(cfg.BaseURL, "https://") {
		if transport, _ := client.HTTPTransport(); transport != nil {
			transport.IdleConnTimeout = 68 * time.Second
		}
	}
	client.SetHeader("Connection", "keep-alive")

	if cfg.Hedging {
		delay := cfg.HedgingDelay.Std()
		if delay <= 0 {
			// 只可能来自绕过归一化的调用方：兜底但不写回，client 不持有可变配置。
			delay = config.DefaultHedgingDelay
		}
		ht := hedge.NewHedging().
			SetDelay(delay).
			SetMaxRequest(3).
			SetTransport(client.Transport())
		hedgedClient := &http.Client{
			Transport: ht,
			Jar:       client.Client().Jar,
			Timeout:   55 * time.Second,
		}
		htc := resty.NewWithClient(hedgedClient).SetBaseURL(baseURL).
			SetTimeout(timeout).SetRedirectPolicy(resty.RedirectNoPolicy())
		htc.SetRateLimiter(resty.NewRateLimitSlidingWindow(10, 4*time.Second))
		//htc.SetHeader("Referer", refer)
		htc.SetHeader("user-agent", cfg.UserAgent)
		htc.SetHeader("Connection", "keep-alive")
		htc.AddContentDecompresser("br", internal.DecompressBrotli)
		return client, htc
	}
	return client, client
}

// NewAPIClient 构造客户端。sess 是会话产物（JWT / 学到的 route / 修正过的地址），
// 由上层持有并在登录结束后取走持久化；传 nil 表示这次不需要跨进程复用。
//
// cfg 是值拷贝：client 后续的读写都发生在这份拷贝上，调用方的 Settings 不会被改。
//
// baseURL 不合法时返回错误而不是退出进程：调用方可能是一个 Web 请求、
// 一个测试、或者将来别的宿主，它们都应该有机会把错误展示给用户。
func NewAPIClient(cfg config.Settings, sess *config.Session, isCas2, WX bool, route string) (*APIClient, error) {
	if sess == nil {
		sess = config.NewSession()
	}
	if err := config.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("baseURL 解析失败: %q: %w", cfg.BaseURL, err)
	}
	if utils.Is10PrivateIP(u.Hostname()) {
		fmt.Println("当前使用的是私网地址，请确保网络可达性")
		fmt.Println(cfg.BaseURL)
	}
	client, htc := NewBasicClient(cfg)
	if route != "" {
		cookie := &http.Cookie{ // 过 nginx有这个
			Name:  "route",
			Value: route, // 手动设置成一样的会有非常明显的挤号问题
		}
		client.CookieJar().SetCookies(u, []*http.Cookie{cookie})
	} else if strings.HasPrefix(cfg.BaseURL, "https://jwglxt.ycit") || strings.HasPrefix(cfg.BaseURL, "http://jwglxt.ycit") || strings.HasPrefix(cfg.BaseURL, "http://202.119.141") {
		if routes := routeCandidates(cfg, sess); len(routes) != 0 {
			selected := routes[rand.Intn(len(routes))]
			cookie := &http.Cookie{ // 过 nginx有这个
				Name:   "route",
				Value:  selected, // 手动设置成一样的会有非常明显的挤号问题
				Domain: "ycit.edu.cn",
				Path:   "/",
			}
			log.Println("route:", selected)
			fmt.Printf("route: (%s)\n", selected)
			client.CookieJar().SetCookies(u, []*http.Cookie{cookie})
		}
	}

	//transport, _ := client.HTTPTransport()
	//fmt.Println(transport.MaxConnsPerHost, transport.MaxIdleConnsPerHost, transport.MaxIdleConns)
	//transport.MaxIdleConns = 100
	//transport.MaxIdleConnsPerHost = 16 // 每个host最大空闲连接数
	//transport.MaxConnsPerHost = 30     // 每个host最大连接数

	apiClient := &APIClient{
		cfg:        cfg,
		Http:       client,
		hedgeC:     htc,
		enableCas2: isCas2 || WX,
		sess:       sess,
	}
	// 只有启用门户登录时才需要 cas2 客户端：它会额外构造两个 resty 客户端，
	// 未启用的账号不该为它付出代价（hook 下发对 nil 是安全的）。
	if apiClient.enableCas2 {
		apiClient.cas2Client = cas2.NewCas(cfg.Account, cfg.CasPassword, cfg.UserAgent, WX, cfg, sess)
		// 门户登录内部的取登录页 / 加密密码 / 提交 / 失败原因都翻译成
		// 统一的 LoginPhase 上报：否则 WebUI 在整个门户登录期间只能看到
		// 一条"正在登录统一身份认证"。
		apiClient.attachCas2Steps()
		if WX {
			// 把 cas2 的扫码状态翻译成统一的 LoginPhase：WebUI 只认阶段，
			// 不需要知道 cas2 的 QRStatus 枚举。
			apiClient.cas2Client.SetQRStatusHandler(func(status cas2.QRStatus, attempt int) {
				if phase := qrStatusPhase(status); phase != "" {
					apiClient.reportLoginStage(phase, attempt, "")
				}
			})
		}
	}
	attachHooks(apiClient)
	return apiClient, nil
}

// NewClientWithCookieJar 用调用方提供的 cookie jar 构造客户端（超时取 cfg.Timeout）。
func NewClientWithCookieJar(cfg config.Settings, jar *cookiejar.Jar) (*APIClient, error) {
	if err := config.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, err
	}
	client, htc := NewBasicClient(cfg)
	client.SetCookieJar(jar)
	htc.SetCookieJar(jar)
	client.SetHeader("user-agent", cfg.UserAgent)
	//SetTLSFingerprintRandomized().
	//client.SetProxyURL("http://127.0.0.1:8866")
	client.SetTrace(true)

	return &APIClient{
		cfg:              cfg,
		Http:             client,
		hedgeC:           htc,
		onlyCookieMethod: true,
		sess:             config.NewSession(),
	}, nil
}

// routeCandidates 返回 route cookie 候选池：用户自定义优先，学到的补后去重。
func routeCandidates(cfg config.Settings, sess *config.Session) []string {
	routes := []string{
		"c80e782f5a3340e86274809ce311b6b4", // 快
		"8ed16c15842922decba77aa1ed63b61f", // 52.1706ms cas✅
	}
	if len(cfg.Routes) != 0 {
		routes = slices.Clone(cfg.Routes)
	}
	// 学到的 route 也进候选池，否则"重启后保留"只是记了个账：
	// 用户自定义的优先，学到的补在后面并去重。
	if sess != nil {
		for _, r := range sess.Routes() {
			if !slices.Contains(routes, r) {
				routes = append(routes, r)
			}
		}
	}
	return routes
}

// Session 返回会话产物快照。调用方（Web）在登录结束后取走并持久化；
// client 自己从不写文件。
func (a *APIClient) Session() config.Session {
	if a == nil || a.sess == nil {
		return config.Session{}
	}
	return a.sess.Snapshot()
}

// Identity 返回这次登录证明的身份，身份未知时返回 nil。
// 扫码登录在拿到 ticket 之后才知道登的是谁，所以这个值要在 Login 返回后读。
func (a *APIClient) Identity() *config.Identity {
	if a == nil || a.sess == nil {
		return nil
	}
	return a.sess.Identity.Clone()
}

// Settings 返回构造时配置的副本（Routes 已深拷贝）。client 内部不再改它，
// 上层需要"当前生效的配置"时读这里，不必自己再存一份。
func (a *APIClient) Settings() config.Settings {
	if a == nil {
		return config.Settings{}
	}
	return a.cfg.Clone()
}

// sessionOrNew 返回会话产物，零值构造的 APIClient 也能安全使用。
func (a *APIClient) sessionOrNew() *config.Session {
	if a.sess == nil {
		a.sess = config.NewSession()
	}
	return a.sess
}

//func JoinURL(base, endpoint string) (string, error) {
//	baseURL, err := url.Parse(base)
//	if err != nil {
//		return "", err
//	}
//	endpointURL, err1 := url.Parse(endpoint)
//	if err1 != nil {
//		return "", err1
//	}
//	fullURL := baseURL.ResolveReference(endpointURL)
//	return fullURL.String(), nil
//}

var TERM = map[int]string{0: "", 1: "3", 2: "12", 3: "16"}
