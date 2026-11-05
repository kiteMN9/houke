package cas2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"school_sdk/protocol/cas2/utils"
	"school_sdk/protocol/config"
	"school_sdk/protocol/internal"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"resty.dev/v3"
)

type Client struct {
	Account     string
	password    string
	fpVisitorId string
	http        *resty.Client
	portalHttp  *resty.Client
	//LoggedIn      bool
	enableWxLogin    bool
	nextLoginTimeExp time.Time
	fCfg             config.Settings
	// sess 是跨进程复用的会话产物（门户 JWT）。与 fCfg 分开：
	// 前者由上层持久化，后者是用户配置，cas2 只读。
	sess *config.Session

	// 微信扫码登录：端点、二维码回调与状态回调。
	// 回调在登录 goroutine 上同步调用，必须快速返回。
	wx        wxEndpoints
	qrMu      sync.RWMutex
	qrHandler QRHandler
	qrStatus  QRStatusHandler

	// 密码登录流程内部的步骤回调（见 steps.go）。
	// 同样在登录 goroutine 上同步调用，必须快速返回。
	stepMu      sync.RWMutex
	stepHandler StepHandler

	// 新增：WebUI 长任务可观测性用；nil 时不做任何上报。
	ProtocolHook internal.ProtocolHook

	// hookMu 保护 ProtocolHook 的读写与"补挂"过程。
	hookMu sync.Mutex
	// httpOnce / portalOnce 保证同一个 resty 客户端只挂一次观察中间件：
	// SetProtocolHook 允许被重复调用（构造后再挂、换回调），
	// 而每挂一次就会多发一轮事件，必须幂等。
	httpOnce   sync.Once
	portalOnce sync.Once
}

// attachHooks 把请求观察钩子挂到所有会用到的 resty 客户端上。
// 只做请求前上报，不改变请求本身的行为。
//
// 注意：本类型的请求分散在 http（cas2.ycit.edu.cn）与 portalHttp
// （portal.ycit.edu.cn）两个 resty 客户端上，且 portalHttp 是 Clone 出来的
// 独立客户端（只在部分版本里共享底层 transport，中间件绝不共享）。
// 因此只要新增一个 resty 客户端，就必须在这里补一行，否则那条链路的
// 请求会在 WebUI 时间线上彻底消失。
func attachHooks(c *Client) {
	if c == nil {
		return
	}
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	if c.ProtocolHook == nil {
		return
	}
	mw := internal.Middleware(c.ProtocolHook)
	if c.http != nil {
		c.httpOnce.Do(func() { c.http.AddRequestMiddleware(mw) })
	}
	if c.portalHttp != nil {
		c.portalOnce.Do(func() { c.portalHttp.AddRequestMiddleware(mw) })
	}
}

// SetProtocolHook 挂载请求观察钩子（挂载后再补钩子也能生效）。
func (c *Client) SetProtocolHook(h internal.ProtocolHook) {
	if c == nil {
		return
	}
	c.ProtocolHook = h
	attachHooks(c)
}

// cas2 门户链路的 transport 配置档。protocol 跨账号共享连接池时按这三个值
// 给 cas2 链路分组，因此它们必须与 NewCas 自建 transport 的参数一致；
// 改这里等于同时改共享池的分组。
const (
	TransportResponseHeaderTimeout = 31 * time.Second
	TransportIdleConnTimeout       = 68 * time.Second
	TransportMaxIdleConnsPerHost   = 8
)

func NewCas(account, password, UA string, wx bool, fCfg config.Settings, sess *config.Session) *Client {
	return NewCasWithTransport(account, password, UA, wx, fCfg, sess, nil)
}

// NewCasWithTransport 用调用方提供的 transport 构造门户客户端。
//
// rt == nil 时自建 transport（与旧 NewCas 完全一致）；rt != nil 时使用共享
// transport：transport 级参数（代理 / TLS 跳过校验 / 空闲超时）由调用方在
// 池里一次性设好，这里不再覆盖。cookie jar 仍由 resty.New() 新建，
// portalHttp 依旧是 Clone，同账号的 cas2 / portal 两个域共享 transport + jar。
func NewCasWithTransport(account, password, UA string, wx bool, fCfg config.Settings, sess *config.Session, rt http.RoundTripper) *Client {
	if UA == "" {
		UA = config.EdgeUA
	}
	if sess == nil {
		sess = config.NewSession()
	}
	var client *resty.Client
	if rt != nil {
		client = resty.New().SetTransport(rt)
	} else {
		client = resty.NewWithTransportSettings(&resty.TransportSettings{
			IdleConnTimeout:       TransportIdleConnTimeout,
			ResponseHeaderTimeout: TransportResponseHeaderTimeout,
			MaxIdleConnsPerHost:   TransportMaxIdleConnsPerHost,
		})
		// 只有自建 transport 时才在这里设代理和跳过 TLS 校验；
		// 共享 transport 由 transportPool 建池时统一设置。
		if os.Getenv("proxy") == "1" {
			//.EnableInsecureSkipVerify()
			client.SetProxy("http://127.0.0.1:8866")
			tls_ := client.TLSClientConfig()
			tls_.InsecureSkipVerify = true
		}
	}
	client.SetBaseURL("https://cas2.ycit.edu.cn/").
		SetHeader("user-agent", UA).
		SetRedirectPolicy(resty.RedirectPolicyFunc(func(req *http.Request, via []*http.Request) error {
			if req.Response != nil {
				if req.Response.StatusCode == http.StatusMovedPermanently {
					return nil
				}
			}
			return http.ErrUseLastResponse
		}))
	client.AddContentDecompresser("br", internal.DecompressBrotli)
	client.SetTimeout(time.Second * 33)
	client.SetRetryCount(1).AddRetryConditions(resty.RetryConditionStatus5XX)
	client.SetRateLimiter(resty.NewRateLimitSlidingWindow(10, 4*time.Second))

	if os.Getenv("trace") == "1" {
		client.SetTrace(true)
		//client.SetLogger()
	}

	// 共享transport
	portalHttp := client.Clone(context.Background()).
		SetBaseURL("https://portal.ycit.edu.cn/")

	if sess.TicketJWT != "" {
		ticket, err1 := utils.ParseTicket(sess.TicketJWT)
		if err1 == nil && time.Now().Before(ticket.Exp) {
			portalHttp.SetHeader("x-id-token", ticket.IDToken)
			portalHttp.SetHeader("x-device-info", "PC")
			portalHttp.SetHeader("x-terminal-info", "PC")
			portalHttp.SetHeader("cookie", "isLogin=true")
			// 老账号可能只存了 JWT 没存身份（扫码上线前登录的），就地补上：
			// 否则"这个账号是谁"就只剩下用户名可猜了。
			if sess.Identity.Empty() {
				id := ticket.Identity
				sess.Identity = &id
			}
			hash := md5.Sum([]byte(ticket.Identity.Sub + "salt354waragthaswrg"))
			md5Str := hex.EncodeToString(hash[:])
			return newClient(account, password, md5Str, client, portalHttp, wx, fCfg, sess, ticket.Exp)
		}
		// 过期凭证就地作废；落盘由上层在登录结束后统一处理。
		sess.TicketJWT = ""
	}

	hash := md5.Sum([]byte(account + "salt354waragthaswrg"))
	md5Str := hex.EncodeToString(hash[:])
	//fmt.Println("MD5:", md5Str)

	return newClient(account, password, md5Str, client, portalHttp, wx, fCfg, sess, time.Time{})
}

// newClient 是所有构造路径的唯一出口。
// 目前 ProtocolHook 由上层在构造之后注入（见 SetProtocolHook），这里只做兜底，
// 保证以后新增构造入口时不会漏掉"两个 resty 客户端"这一层。
func newClient(account, password, fpVisitorId string, httpClient, portalHttp *resty.Client, wx bool, fCfg config.Settings, sess *config.Session, nextLoginTimeExp time.Time) *Client {
	if sess == nil {
		sess = config.NewSession()
	}
	c := &Client{
		Account:          account,
		password:         password,
		fpVisitorId:      fpVisitorId,
		http:             httpClient,
		portalHttp:       portalHttp,
		enableWxLogin:    wx,
		nextLoginTimeExp: nextLoginTimeExp,
		fCfg:             fCfg,
		sess:             sess,
		wx:               defaultWXEndpoints(),
	}
	attachHooks(c)
	return c
}

func (c *Client) Login(ctx context.Context) bool {
	for attempt := 1; attempt <= 3; attempt++ {
		if c.netCheckIdToken(ctx) {
			// 会话还有效：这次登录没有发生任何密码交互，值得让用户看到。
			c.emitStep(StepSessionValid, attempt)
			return true
		}
		if c.enableWxLogin {
			return c.WXLogin(ctx)
		}
		// 三个步骤都带轮次上报：外层最多重试 3 轮，时间线上能看出
		// "第 2 次取登录页"而不是一条纹丝不动的"正在登录统一身份认证"。
		c.emitStep(StepFetchForm, attempt)
		execution := c.getHtml(ctx)
		if execution == "" {
			return false
		}
		fmt.Println("cas2 html ok")
		c.emitStep(StepPublicKey, attempt)
		encryptResult := c.getRsaPublicKey(ctx)
		if encryptResult == "" {
			return false
		}
		//check_code.SaveImgStream(c.getQrCode(), "./", "qrcode")
		c.emitStep(StepSubmit, attempt)
		if c.postLogin(encryptResult, execution, ctx) {
			//c.LoggedIn = true
			return true
		}

		//c.LoggedIn = false
		fmt.Println("清空cookie")
		log.Println("清空cookie")
		u, _ := url.Parse(c.http.BaseURL())
		c.http.CookieJar().SetCookies(u, []*http.Cookie{})
	}
	return false
}

func extractLoginParams(body io.ReadCloser) (execution, failN string, err error) {
	if body == nil {
		return "", "", errors.New("body is nil")
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(body)
	tokenizer := html.NewTokenizer(io.LimitReader(body, 34000))

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			// 遇到错误（如 EOF）则结束
			if tokenizer.Err() == io.EOF {
				return execution, failN, nil
			}
			return execution, failN, tokenizer.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			// 只处理 <input> 标签（自闭合或开始标签）
			tagName, _ := tokenizer.TagName()
			if !bytes.Equal(tagName, []byte("input")) {
				continue
			}
			// 遍历属性
			var name, value string
			for {
				key, val, moreAttr := tokenizer.TagAttr()
				if bytes.Equal(key, []byte("name")) {
					name = string(val)
				} else if bytes.Equal(key, []byte("value")) {
					value = string(val)
				}
				if !moreAttr {
					break
				}
			}
			if name == "execution" {
				execution = value
			} else if name == "failN" {
				failN = value
			}
			// 如果两个值都找到了，可以提前结束（但需注意 tokenizer 可能还有后续，但我们可以返回）
			if execution != "" && failN != "" {
				return execution, failN, nil
			}
		default:
			continue
		}
	}
}

func (c *Client) getHtml(ctx context.Context) string {
	for {
		if ctx.Err() != nil {
			return ""
		}
		resp, err := c.http.R().
			SetContext(ctx).
			SetQueryParam("service", "https://portal.ycit.edu.cn/?path=https://portal.ycit.edu.cn/main.html#/").
			SetRetryCount(1).
			SetResponseDoNotParse(true).
			Get(login)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ""
			}
			// 错误处理（注意 resp 可能为 nil，此时不能调用 resp.Body.Close）
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("cas getHtml 请求超时", resp.Duration())
				continue
			}
			if errors.Is(err, io.EOF) {
				fmt.Println(err)
				time.Sleep(3 * time.Second)
				continue
			}
			log.Println("cas getHtml 请求失败:", err)
			fmt.Println(err)
			time.Sleep(1 * time.Second)
			continue
		}
		if resp.IsStatusSuccess() {
			execution, failN, parseErr := extractLoginParams(resp.Body)
			if parseErr != nil {
				fmt.Println(parseErr)
				continue
			}

			if failN != "-1" && failN != "0" {
				fmt.Println("failN:", failN, "，有一定失败次数")
				// 失败次数不为零时 CAS 可能改出验证码：这条状态能解释
				// 后面为什么反复重试/被拒。
				c.emitStep(StepFailedAttempts, 0)
				time.Sleep(2 * time.Second)
			}
			if execution != "" {
				//fmt.Println(time.Since(start))
				return execution
			}
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		// 如果没有拿到 execution，继续循环
		time.Sleep(1 * time.Second)
	}
}

func (c *Client) getRsaPublicKey(ctx context.Context) string {
	publicKeyPEM := ""
	for {
		if ctx.Err() != nil {
			return ""
		}
		resp, err := c.http.R().
			SetContext(ctx).
			SetRetryCount(1).
			Get(pubKey)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ""
			}
			log.Println("getRsaPublicKey", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println("getRsaPublicKey", resp.Status())
			time.Sleep(1 * time.Second)
			continue
		}
		publicKeyPEM = resp.String()
		//fmt.Println(publicKeyPEM)
		// 从 PEM 创建加密器
		encryptor, rsaDecErr := NewRSAEncryptorFromPEM(publicKeyPEM)
		if rsaDecErr != nil {
			fmt.Printf("创建加密器失败: %v\n", rsaDecErr)
			log.Printf("创建加密器失败: %v\n", rsaDecErr)
			time.Sleep(1 * time.Second)
			continue
		}

		// 加密得到 Base64 结果
		base64Result, encErr := encryptor.EncryptWithBase64(c.password)
		if encErr != nil {
			fmt.Printf("加密失败: %v\n", encErr)
			continue
		}
		encryptResult := "__RSA__" + base64Result
		//fmt.Printf("结果: %s\n", encryptResult)
		//log.Println("encryptResult:", encryptResult)
		return encryptResult
	}
}

func (c *Client) getCaptchaImage() []byte {
	// set cookie
	resp, err := c.http.R().
		SetQueryParam("r", fmt.Sprint(time.Now().UnixMicro()/100)).
		SetRetryCount(2).
		Get(captcha)
	if err != nil {
		log.Println("getCaptchaImage:", err)
		return []byte{}
	}
	captchaImage := resp.Bytes()
	return captchaImage
}

func (c *Client) getQrCode() []byte {
	// if not set cookie SESSION
	resp, err := c.http.R().
		SetQueryParam("r", fmt.Sprint(time.Now().UnixMicro()/100)).
		SetRetryCount(2).
		Get(qrCode)
	if err != nil {
		log.Println("getCaptchaImage:", err)
		return []byte{}
	}
	captchaImage := resp.Bytes()
	return captchaImage
}

func (c *Client) postLogin(encryptResult, execution string, ctx context.Context) bool {
	for range 5 {
		if ctx.Err() != nil {
			return false
		}
		resp, err := c.http.R().
			SetContext(ctx).
			SetRetryCount(1).
			SetRetryAllowNonIdempotent(true).
			SetQueryParam("service", "https://portal.ycit.edu.cn/?path=https://portal.ycit.edu.cn/main.html#/").
			SetFormData(map[string]string{
				"username":    c.Account,
				"password":    encryptResult,
				"captcha":     "",
				"currentMenu": "1",
				"failN":       "0",
				"mfaState":    "",
				"execution":   execution,
				"_eventId":    "submit", // submitPasswordlessToken
				"geolocation": "",
				"fpVisitorId": c.fpVisitorId,
				"submit1":     "Login1",
			}).Post(login)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false
			}
			fmt.Println("postLogin error:", err)
			log.Println("postLogin err:", err)
			time.Sleep(1 * time.Second)
			continue
		}
		//log.Println("cas2 postLogin:", resp.Status()) // 401失败 302成功
		switch resp.StatusCode() {
		case 302:
			location := resp.Header().Get("Location")
			if location == "" {
				log.Println("cas2 postLogin 302 location is null")
				c.emitStep(StepLoginFailed, 0)
				return false
			}

			// 解析 location
			parsedUrl, _ := url.Parse(location)
			query := parsedUrl.Query()
			// 提取 ticket
			ticketJWT := query.Get("ticket")
			if ticketJWT == "" {
				log.Println("cas2 postLogin 302 缺少 ticket 参数:", location)
				c.emitStep(StepLoginFailed, 0)
				return false
			}
			//fmt.Println("ticketJWT:", ticketJWT)
			// 从 ticketJWT 提取 idToken 作为x-id-token
			// ticket分成三段，中间的base64解码后得到json里的idToken是结果
			ticket, err1 := utils.ParseTicket(ticketJWT)
			if err1 != nil {
				fmt.Printf("错误: %v\n", err1)
				log.Println("ticketJWT:", ticketJWT)
				log.Println("ticket解析失败:", err1)
				c.emitStep(StepLoginFailed, 0)
				return false
			}
			var idToken = ticket.IDToken
			c.nextLoginTimeExp = ticket.Exp
			c.Account = ticket.Identity.Sub
			if c.sess != nil {
				id := ticket.Identity
				c.sess.Identity = &id
			}
			fmt.Println("cas2登录成功✅")
			fmt.Println("====点击下方连接可访问门户=============")
			fmt.Println(location)
			fmt.Println("====点击上方连接可访问门户=============")

			log.Println("====点击下方连接可访问门户==============")
			log.Println("\n", location)
			log.Println("====点击上方连接可访问门户==============")

			// portal header
			c.portalHttp.SetHeader("x-id-token", idToken)
			c.portalHttp.SetHeader("x-device-info", "PC")
			c.portalHttp.SetHeader("x-terminal-info", "PC")
			c.portalHttp.SetHeader("cookie", "isLogin=true")
			c.sess.TicketJWT = ticketJWT
			return true
		case 200:
			if strings.Contains(resp.String(), "这个账户已经被锁住了。") {
				fmt.Println("这个账户已经被锁住了。")
				// 账户锁定不是"重试就好"的错误：必须让用户看到。
				c.emitStep(StepAccountLocked, 0)
			} else {
				fmt.Println("不成功，登录实现有问题", resp.Status())
				log.Println("不成功，登录实现有问题", resp.Status(), resp.String())
				c.emitStep(StepLoginFailed, 0)
				time.Sleep(time.Second * 12)
			}
		case 401:
			fmt.Println("cas2 账户或密码错误 401")
			// 不再在这里读 stdin / 改配置：Web 服务下会卡住整个登录任务。
			// client 只报告失败，密码修正交给上层（Web 编辑账号后重试）。
			log.Println("cas2 账户或密码错误 401")
			c.emitStep(StepPasswordError, 0)
			return false
		case 500:
			log.Println("postLogin status:", resp.Status())
			log.Println(resp.String())
			c.emitStep(StepLoginFailed, 0)
			time.Sleep(2 * time.Second)
			return false
		default:
			fmt.Println("cas2 postLogin:", resp.Status(), resp.String())
			log.Println("cas2 postLogin:", resp.Status(), resp.String())
			c.emitStep(StepLoginFailed, 0)
			time.Sleep(1 * time.Second)
			continue
		}
		break
		//fmt.Println(resp.String())
	}
	return false
}
