package cas2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"school_sdk/protocol/cas2/utils"
)

const (
	// wxQRMaxRefresh 是一次登录里最多生成几张二维码。
	// 一张二维码约 4 分钟，配合 10 分钟的登录超时足够覆盖 2~3 次过期。
	wxQRMaxRefresh = 3
	// wxQRTTL 是本地兜底过期时间：即使服务端一直回 408 不给 402，
	// 到点也会换一张，避免界面永远停在"请扫码"。
	wxQRTTL = 4 * time.Minute
)

// QRCode 是一次微信扫码登录的二维码快照。
// Image 是 PNG 字节；ExpiresAt 只用于 UI 倒计时，权威过期信号仍是服务端 402。
type QRCode struct {
	Image     []byte
	ExpiresAt time.Time
	Attempt   int
	// PageURL 是微信官方的扫码页面地址（open.weixin.qq.com/connect/qrconnect...），
	// 供宿主展示“复制链接”等入口；它不是二维码图片地址。
	PageURL string
}

// QRHandler 接收二维码变化；qr == nil 表示二维码已失效，宿主应清除显示。
// 回调在登录 goroutine 上同步执行，不得阻塞。
type QRHandler func(qr *QRCode)

// QRStatus 是扫码过程的机器可读状态。
type QRStatus string

const (
	QRWaiting QRStatus = "waiting" // 未扫描
	QRScanned QRStatus = "scanned" // 已扫描，等待手机确认
	QRExpired QRStatus = "expired" // 二维码过期
	QRRefused QRStatus = "refused" // 用户在手机上拒绝 / 取消
)

// QRStatusHandler 接收扫码状态变化与当前二维码轮次。
type QRStatusHandler func(status QRStatus, attempt int)

// wxEndpoints 把微信/CAS 端点集中起来，测试可以整体替换成 httptest。
// 默认值仍是 ycit 的固定地址；多校 profile 不在本次范围内。
type wxEndpoints struct {
	federatedRedirect string
	service           string
	qrImageBase       string
	qrPoll            string
	callback          string
	casLogin          string
}

func defaultWXEndpoints() wxEndpoints {
	return wxEndpoints{
		federatedRedirect: "https://cas2.ycit.edu.cn/cas/federatedRedirect",
		service:           "https://portal.ycit.edu.cn/?path=https://portal.ycit.edu.cn/main.html#/",
		qrImageBase:       "https://open.weixin.qq.com",
		qrPoll:            "https://lp.open.weixin.qq.com/connect/l/qrconnect",
		callback:          "https://cas2.ycit.edu.cn/cas/federation/federatedCallback/openweixin",
		casLogin:          "https://cas2.ycit.edu.cn/cas/login",
	}
}

// SetQRHandler 挂载二维码回调；传 nil 取消。回调在登录 goroutine 上同步执行。
func (c *Client) SetQRHandler(h QRHandler) {
	if c == nil {
		return
	}
	c.qrMu.Lock()
	c.qrHandler = h
	c.qrMu.Unlock()
}

// SetQRStatusHandler 挂载扫码状态回调；传 nil 取消。
func (c *Client) SetQRStatusHandler(h QRStatusHandler) {
	if c == nil {
		return
	}
	c.qrMu.Lock()
	c.qrStatus = h
	c.qrMu.Unlock()
}

func (c *Client) emitQR(qr *QRCode) {
	if c == nil {
		return
	}
	c.qrMu.RLock()
	h := c.qrHandler
	c.qrMu.RUnlock()
	if h != nil {
		h(qr)
	}
}

func (c *Client) emitQRStatus(status QRStatus, attempt int) {
	if c == nil {
		return
	}
	c.qrMu.RLock()
	h := c.qrStatus
	c.qrMu.RUnlock()
	if h != nil {
		h(status, attempt)
	}
}

// WXLogin 走完整的微信扫码登录：取二维码 → 轮询扫码 → 换 CAS/门户票据。
//
// 它不写文件、不打印二维码、不读 stdin；二维码字节交给 QRHandler，
// 状态交给 QRStatusHandler。ctx 取消时所有等待（网络、sleep）都会尽快返回。
func (c *Client) WXLogin(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	// 无论成功、失败还是取消，都不能让宿主继续显示一张已经无效的二维码。
	defer c.emitQR(nil)

	for attempt := 1; attempt <= wxQRMaxRefresh; attempt++ {
		if err := ctx.Err(); err != nil {
			return false
		}

		uuid, imgURL, pageURL, state, err := c.wxUUID(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Println("微信登录：获取二维码失败:", err)
			if attempt == wxQRMaxRefresh {
				break
			}
			if err := sleepCtx(ctx, time.Second); err != nil {
				return false
			}
			continue
		}
		img, err := c.getWXQrCode(ctx, imgURL)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Println("微信登录：下载二维码失败:", err)
			if attempt == wxQRMaxRefresh {
				break
			}
			if err := sleepCtx(ctx, time.Second); err != nil {
				return false
			}
			continue
		}

		expiresAt := time.Now().Add(wxQRTTL)
		c.emitQR(&QRCode{Image: img, ExpiresAt: expiresAt, Attempt: attempt, PageURL: pageURL})
		c.emitQRStatus(QRWaiting, attempt)

		code, status, err := c.waitScan(ctx, uuid, attempt, expiresAt)
		if err != nil {
			if ctx.Err() == nil {
				log.Println("微信登录：等待扫码失败:", err)
			}
			return false
		}
		switch status {
		case QRExpired:
			c.emitQR(nil)
			continue
		case QRRefused:
			return false
		}
		if code == "" {
			return false
		}
		return c.finishWXLogin(ctx, code, state)
	}

	log.Println("微信登录：二维码多次刷新仍失败")
	return false
}

// wxRedirect 发一次不允许自动跟随的 GET，取 302 的 Location。
func (c *Client) wxRedirect(ctx context.Context, rawURL string, query map[string]string) (string, error) {
	if rawURL == "" {
		return "", errors.New("微信登录：重定向地址为空")
	}
	req := c.http.R().SetContext(ctx).SetRetryCount(2)
	for k, v := range query {
		req.SetQueryParam(k, v)
	}
	resp, err := req.Get(rawURL)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != 302 {
		return "", fmt.Errorf("微信登录：%s 返回 %s", rawURL, resp.Status())
	}
	location := resp.Header().Get("Location")
	if location == "" {
		return "", fmt.Errorf("微信登录：%s 的 302 缺少 Location", rawURL)
	}
	return location, nil
}

// wxGet 只保证请求发出并拿到响应，不要求 302。
// 微信换票流程的第三步原本就只用于落 cookie，返回值不参与后续 URL。
func (c *Client) wxGet(ctx context.Context, rawURL string) error {
	if rawURL == "" {
		return errors.New("微信登录：跳转地址为空")
	}
	_, err := c.http.R().SetContext(ctx).SetRetryCount(2).Get(rawURL)
	return err
}

// wxUUID 取微信二维码页，解析 uuid、图片地址和 state。
func (c *Client) wxUUID(ctx context.Context) (uuid, imgURL, pageURL, state string, err error) {
	location, err := c.wxRedirect(ctx, c.wx.federatedRedirect, map[string]string{
		"service":       c.wx.service,
		"federatedName": "openweixin",
	})
	if err != nil {
		return "", "", "", "", err
	}

	wxLocation, err := c.wxRedirect(ctx, location, nil)
	if err != nil {
		return "", "", "", "", err
	}
	pageURL = wxLocation
	parsed, err := url.Parse(wxLocation)
	if err != nil {
		return "", "", "", "", fmt.Errorf("微信登录：解析微信跳转地址失败: %w", err)
	}
	state = parsed.Query().Get("state")
	if state == "" {
		return "", "", "", "", errors.New("微信登录：微信跳转地址缺少 state")
	}

	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", "", "", "", err
		}
		resp, reqErr := c.http.R().SetContext(ctx).Get(wxLocation)
		if reqErr != nil {
			if ctx.Err() != nil {
				return "", "", "", "", ctx.Err()
			}
			log.Println("微信登录：二维码页面请求失败:", reqErr)
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return "", "", "", "", sleepErr
			}
			continue
		}
		if resp.StatusCode() != 200 {
			log.Println("微信登录：二维码页面状态:", resp.Status())
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return "", "", "", "", sleepErr
			}
			continue
		}

		uuid, rawImg, ok := parseQRPage(resp.String())
		if !ok {
			log.Println("微信登录：二维码页面里没有 uuid 或图片")
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return "", "", "", "", sleepErr
			}
			continue
		}
		imgURL, err = resolveQRImageURL(c.wx.qrImageBase, rawImg)
		if err != nil {
			return "", "", "", "", err
		}
		return uuid, imgURL, pageURL, state, nil
	}
	return "", "", "", "", errors.New("微信登录：未能在二维码页面找到 uuid 和图片")
}

// parseQRPage 从微信二维码页 HTML 中取 uuid 与图片原始 src。
// 独立成纯函数，便于用固定样本覆盖页面结构变化。
func parseQRPage(html string) (uuid, imgURL string, ok bool) {
	const uuidMark = `"/connect/qrcode/`
	const imgMark = `web_qrcode_img" src="`

	i := strings.Index(html, uuidMark)
	if i < 0 {
		return "", "", false
	}
	rest := html[i+len(uuidMark):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", "", false
	}
	uuid = rest[:j]

	k := strings.Index(html, imgMark)
	if k < 0 {
		return "", "", false
	}
	rest = html[k+len(imgMark):]
	j = strings.IndexByte(rest, '"')
	if j < 0 {
		return "", "", false
	}
	imgURL = rest[:j]
	if uuid == "" || imgURL == "" {
		return "", "", false
	}
	return uuid, imgURL, true
}

// resolveQRImageURL 把微信页面里的相对/协议相对图片地址补成绝对地址。
func resolveQRImageURL(base, raw string) (string, error) {
	if raw == "" {
		return "", errors.New("微信登录：二维码图片地址为空")
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("微信登录：解析图片基地址失败: %w", err)
	}
	r, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("微信登录：解析二维码图片地址失败: %w", err)
	}
	return b.ResolveReference(r).String(), nil
}

// getWXQrCode 下载二维码图片；网络抖动时重试，ctx 取消立即返回。
func (c *Client) getWXQrCode(ctx context.Context, rawURL string) ([]byte, error) {
	for range 5 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := c.http.R().SetContext(ctx).Get(rawURL)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Println("微信登录：下载二维码失败:", err)
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		if resp.StatusCode() != 200 {
			log.Println("微信登录：二维码图片状态:", resp.Status())
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		if img := resp.Bytes(); len(img) > 0 {
			return img, nil
		}
		if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
			return nil, sleepErr
		}
	}
	return nil, errors.New("微信登录：二维码图片为空")
}

// waitScan 轮询扫码结果，直到拿到 code、过期、被拒绝或 ctx 取消。
// expiresAt 是本地兜底：服务端一直 408 时也不能永远等下去。
func (c *Client) waitScan(ctx context.Context, uuid string, attempt int, expiresAt time.Time) (string, QRStatus, error) {
	scanned := false
	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if time.Now().After(expiresAt) {
			c.emitQRStatus(QRExpired, attempt)
			return "", QRExpired, nil
		}

		resp, err := c.http.R().
			SetContext(ctx).
			SetQueryParam("uuid", uuid).
			Get(c.wx.qrPoll)
		if err != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			log.Println("微信登录：轮询扫码结果失败:", err)
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return "", "", sleepErr
			}
			continue
		}
		if resp.StatusCode() != 200 {
			log.Println("微信登录：轮询扫码结果状态:", resp.Status())
			if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
				return "", "", sleepErr
			}
			continue
		}

		code, status := parseScanResult(resp.String())
		switch status {
		case QRScanned:
			if !scanned {
				c.emitQRStatus(QRScanned, attempt)
				scanned = true
			}
			if code != "" {
				return code, QRScanned, nil
			}
		case QRExpired, QRRefused:
			c.emitQRStatus(status, attempt)
			return "", status, nil
		}

		if sleepErr := sleepCtx(ctx, time.Second); sleepErr != nil {
			return "", "", sleepErr
		}
	}
}

var (
	wxErrCodeRe = regexp.MustCompile(`wx_errcode[[:space:]]*=[[:space:]]*([0-9]+)`)
	wxCodeRe    = regexp.MustCompile(`wx_code[[:space:]]*=[[:space:]]*'([^']*)'`)
)

// parseScanResult 解析微信轮询返回的 JS 片段。
//
//	window.wx_errcode = 408; window.wx_code = '';       // 未扫描
//	window.wx_errcode=404;                              // 已扫描，等确认
//	window.wx_errcode=405;window.wx_code='...';         // 已确认
//	window.wx_errcode=402;                              // 过期
//	window.wx_errcode=403;                              // 用户拒绝
func parseScanResult(body string) (code string, status QRStatus) {
	errcode := 0
	if m := wxErrCodeRe.FindStringSubmatch(body); m != nil {
		errcode, _ = strconv.Atoi(m[1])
	}
	if m := wxCodeRe.FindStringSubmatch(body); m != nil {
		code = m[1]
	}

	switch errcode {
	case 402:
		return "", QRExpired
	case 403:
		return "", QRRefused
	}
	if code != "" {
		return code, QRScanned
	}
	switch errcode {
	case 404, 405:
		return "", QRScanned
	}
	return "", QRWaiting
}

// finishWXLogin 用微信 code 换 CAS/门户票据，并写入会话。
func (c *Client) finishWXLogin(ctx context.Context, code, state string) bool {
	location, err := c.wxRedirect(ctx, c.wx.callback, map[string]string{
		"code":  code,
		"state": state,
	})
	if err != nil {
		log.Println("微信登录：微信回调失败:", err)
		return false
	}

	location2, err := c.wxRedirect(ctx, location, nil)
	if err != nil {
		log.Println("微信登录：CAS 回调失败:", err)
		return false
	}

	// 第三步只用于让 CAS 种下会话 cookie，不要求 302。
	if err := c.wxGet(ctx, location2); err != nil {
		log.Println("微信登录：CAS 会话建立失败:", err)
		return false
	}

	// 和原实现保持一致：service 直接拼进 URL，不能整段转义。
	// 它本身带 #/，作为 URL 片段发送时会被浏览器/客户端截掉；CAS 只校验
	// 实际收到的 service，转义成 %23 反而可能对不上。
	location4, err := c.wxRedirect(ctx, c.wx.casLogin+"?service="+c.wx.service, nil)
	if err != nil {
		log.Println("微信登录：换取 CAS ticket 失败:", err)
		return false
	}

	parsed, err := url.Parse(location4)
	if err != nil {
		log.Println("微信登录：解析 CAS 跳转地址失败:", err)
		return false
	}
	ticketJWT := parsed.Query().Get("ticket")
	if ticketJWT == "" {
		log.Println("微信登录：CAS 跳转地址里缺少 ticket")
		return false
	}

	ticket, err := utils.ParseTicket(ticketJWT)
	if err != nil {
		log.Println("微信登录：ticket 解析失败:", err)
		return false
	}
	c.nextLoginTimeExp = ticket.Exp
	c.Account = ticket.Identity.Sub
	if c.portalHttp != nil {
		c.portalHttp.SetHeader("x-id-token", ticket.IDToken)
		c.portalHttp.SetHeader("x-device-info", "PC")
		c.portalHttp.SetHeader("x-terminal-info", "PC")
		c.portalHttp.SetHeader("cookie", "isLogin=true")
	}
	if c.sess != nil {
		c.sess.TicketJWT = ticketJWT
		// 扫码是唯一"登完才知道登的是谁"的登录方式：身份就在这里落进会话，
		// 由上层在登录结束后连同 JWT 一起写进账号记录。
		id := ticket.Identity
		c.sess.Identity = &id
	}
	return true
}

// sleepCtx 是可取消的 time.Sleep。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		time.Sleep(d)
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
