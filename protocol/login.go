package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"school_sdk/protocol/config"
	"school_sdk/protocol/rsa"
	"strconv"
	"strings"
	"sync"
	"time"

	"school_sdk/check_code"
	"school_sdk/utils"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

var ExistVerify = fmt.Errorf("请先滑动图片进行验证！")
var InputYzmErr = fmt.Errorf("验证码输入错误！")
var IncorrectPassword = fmt.Errorf("用户名或密码不正确，请重新输入！")
var CsrfNotExist = fmt.Errorf("CSRF not exist")
var LoginFailSetCookie = fmt.Errorf("login fail set cookie")

//// ReLogin 在本账号会话失效时重新登录。
////
//// 并发约束（见 loginState.go）：
////   - 同一账号同一时刻只会有一个登录流程，重复调用会排队；
////   - 不同账号之间互不阻塞；
////   - 登录进行中，本账号的业务请求会等在 a.login.gate 上。
//func (a *APIClient) ReLogin() bool {
//	a.login.mu.Lock()
//	defer a.login.mu.Unlock()
//
//	// 冷静期只认本账号：并发请求同时发现掉线时，第一个真正重登，
//	// 其余的直接复用刚建立的会话。这里以前是包级的 lastSuccessTime，
//	// 于是 A 账号登录后，B 账号也会被误判成"刚登录成功"。
//	// 还要确认登录态确实还在：万一调用方刚因为 401 把状态清掉，
//	// 就不能拿着冷静期当成"已重登成功"。
//	if a.LoggedIn() && a.inLoginCooldown(time.Now()) {
//		return true
//	}
//	a.setLoggedIn(false)
//	if a.onlyCookieMethod {
//		// 只带 cookie 的客户端没法重新走登录流程：这里以前直接 os.Exit(0)，
//		// 留着整个 Web 进程被一次过期兜底带走。现在交给调用方决定。
//		log.Println("登录可能过期，需要更新 cookie")
//		time.Sleep(1 * time.Second)
//		return false
//	}
//	fmt.Println("\r重新登录")
//	reStartTime := time.Now()
//	if a.loginLocked(context.Background()) {
//		log.Println("重新登录用时:", time.Since(reStartTime))
//		return true
//	}
//	return false
//}

//// Login 无 ctx 版本，保持兼容
//func (a *APIClient) Login() bool {
//	return a.LoginWithContext(context.Background())
//}

// LoginWithContext 允许在登录过程中被取消 / 暂停，并且是唯一需要串行化的登录入口。
// —— 原来 Login() 里所有"阻塞 / 重试 / 请求"的位置，
//
//	都要换成通过 ctx 传递，并在每次循环开头做一次检查。
func (a *APIClient) LoginWithContext(ctx context.Context) bool {
	a.login.mu.Lock()
	defer a.login.mu.Unlock()

	return a.loginLocked(ctx)
}

func (a *APIClient) doLogin(ctx context.Context) bool {
	a.reportLoginStage(LoginPhasePrepare, 0, "")
	if a.enableCas2 {
		if a.cas2LoginCtl(ctx) {
			if a.cas2Client.Account != a.cfg.Account {
				a.cfg.Account = a.cas2Client.Account
			}
			return true
		}
		return false
	}
	var LoginExtend = generateLoginExtend(a.cfg.UserAgent)

	// 字符验证码"先自动、再人工"的策略按一次登录算：外层还有 15 次重试，
	// 把失败记在单次尝试上会让已经证明不适配的自动识别反复浪费次数。
	kaptcha := a.newKaptchaPlan()

	for i := range 15 {
		if !a.loginCtxOK(ctx) {
			return false
		}
		reqTime := strconv.FormatInt(time.Now().UnixMilli(), 10)
		a.reportLoginStage(LoginPhaseFetchForm, i+1, "")
		csrfToken, yzm, stat := a.getRawCsrfToken(ctx)
		if stat {
			return true
		}
		if yzm { // 字符验证码
			if a.kaptchaLogin(ctx, csrfToken, reqTime, kaptcha) {
				return true
			}
			if !a.loginCtxOK(ctx) {
				return false
			}
			continue
		}
		if a.hasSliderCaptcha() {
			if a.getCaptchaLogin(LoginExtend, csrfToken, reqTime, ctx) {
				return true
			}
			if !a.loginCtxOK(ctx) {
				return false
			}
			fmt.Println("重新开始登录流程")
			continue
		}
		// 无验证码分支
		var wg sync.WaitGroup
		var encryptedResult string
		wg.Add(1)
		a.reportLoginStage(LoginPhasePublicKey, i+1, "")
		go a.getRsaPublicKey(context.TODO(), &wg, &reqTime, &encryptedResult)
		wg.Wait()

		a.reportLoginStage(LoginPhaseSubmit, i+1, "")
		stat_, err := a.postLogin(csrfToken, reqTime, encryptedResult, "")
		if errors.Is(err, ExistVerify) {
			a.reportLoginStage(LoginPhaseCaptcha, i+1, CaptchaDetailNeedSlider)
			a.setSliderCaptcha(true)
			continue
		}
		if errors.Is(err, IncorrectPassword) {
			if !a.promptNewPassword(ctx) {
				return false
			}
			continue
		}
		if errors.Is(err, CsrfNotExist) {
			fmt.Println("未获取到CSRF")
		}
		return stat_
	}
	return false
}

func (a *APIClient) getCaptchaLogin(LoginExtend []byte, csrfToken, reqTime string, ctxl context.Context) bool {
	// 控制整个滑块验证码登录
	var rtk string
	var encryptedResult string
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctxl)
	defer cancel()

	//Eb := strconv.FormatInt(time.Now().UnixMilli(), 10)
	rtk = a.getRTK(ctxl)

	// fmt.Println(encryptedResult)
	// 发起登录请求
	for range 3 {
		wg.Add(1)
		// 验证码获取与识别，顺便获取公钥
		if !a.captchaControl(ctx, &wg, LoginExtend, &csrfToken, &rtk, &encryptedResult, &reqTime, ctxl) {
			return false
		}
		// 公钥和验证码识别是并发的：识别完成后再等公钥，阶段上先显示"加密密码"。
		a.reportLoginStage(LoginPhasePublicKey, 0, "")
		wg.Wait()
		a.reportLoginStage(LoginPhaseSubmit, 0, "")

		stat, err := a.postLogin(csrfToken, reqTime, encryptedResult, "")
		if errors.Is(err, LoginFailSetCookie) {
			rtk = a.getRTK(ctxl)
		}
		if errors.Is(err, ExistVerify) {
			if a.hasSliderCaptcha() {
				log.Println("重试验证码")
				continue
			}
			a.setSliderCaptcha(true)
			return false
		}
		if errors.Is(err, IncorrectPassword) {
			if !a.promptNewPassword(ctxl) {
				return false // 用户取消或输入为空，终止登录
			}
			continue
		} else {
			// set cookie empty?
			return stat
		}
	}
	return false
}

func (a *APIClient) captchaControl(ctx context.Context, wg *sync.WaitGroup, LoginExtend []byte, csrfToken, rtk, encryptedResult, t *string, ctxl context.Context) bool {
	// 控制除了RTK的整个验证码识别过程
	defer wg.Done()
	captchaStartTime := time.Now()
	for range 4 {
		if !a.loginCtxOK(ctx) {
			return false
		}
		*t = strconv.FormatInt(time.Now().UnixMilli(), 10)
		captchaParams := a.getCaptchaParams(ctxl, *rtk, *t)
		if captchaParams.VS == "verified" {
			log.Println("验证码已通过验证")
			return true
		}
		for captchaParams.Msg != "" {
			//log.Println("验证码已通过验证")
			log.Println(a.Http.Cookies())
			fmt.Println("清空cookie")
			log.Println("清空cookie")
			u, _ := url.Parse(a.Http.BaseURL())
			a.Http.CookieJar().SetCookies(u, []*http.Cookie{})
			*csrfToken, _, _ = a.getRawCsrfToken(ctxl)
			*rtk = a.getRTK(ctxl)
			if *rtk == "" && !a.loginCtxOK(ctx) {
				return false
			}
			*t = strconv.FormatInt(time.Now().UnixMilli(), 10)
			captchaParams = a.getCaptchaParams(ctxl, *rtk, *t)
		}
		imgStream, err := a.getCaptchaImage(ctxl, captchaParams.Imtk, captchaParams.Mi, captchaParams.T)
		if err != nil {
			continue
		}
		wg.Add(1)
		// 将公钥获取放在这里以节省时间，并确保公钥是新鲜的
		go a.getRsaPublicKey(ctx, wg, t, encryptedResult)
		a.reportLoginStage(LoginPhaseCaptcha, 0, CaptchaDetailSlider)
		capStartTime := time.Now()
		x := check_code.FindBestMatch(imgStream)
		fmt.Println("识别用时:", time.Since(capStartTime))
		verResult := a.captchaVerify(ctxl, *rtk, LoginExtend, x)
		if verResult {
			fmt.Println("验证用时:", time.Since(captchaStartTime))
			fmt.Println("cap verfied")
			return true
		}

		fmt.Println(":( 滑块验证失败")
		log.Println(":( 滑块验证失败")
		check_code.SaveFile("fail/", "fail_"+strconv.Itoa(x)+"_"+strconv.FormatInt(time.Now().UnixMilli(), 10)+".png", imgStream)
		return false // 一般来说出现验证失败是cookie问题，所以要重新登录流程而不是重试验证码
	}
	return false
}

func (a *APIClient) kaptchaLogin(ctx context.Context, csrfToken, reqTime string, plan *kaptchaPlan) bool {
	var encryptedResult string
	var wg sync.WaitGroup
	wg.Add(1)
	// 放在这里以保证公钥新鲜
	go a.getRsaPublicKey(ctx, &wg, &reqTime, &encryptedResult)

	for range 3 {
		if !a.loginCtxOK(ctx) {
			return false
		}
		yzm, auto := a.getKaptchaImage(ctx, plan)
		if yzm == "" {
			continue
		}
		a.reportLoginStage(LoginPhasePublicKey, 0, "")
		wg.Wait()
		a.reportLoginStage(LoginPhaseSubmit, 0, "")
		stat, err := a.postLogin(csrfToken, reqTime, encryptedResult, yzm)
		if errors.Is(err, InputYzmErr) {
			// 只有自动识别的结果才计入失败次数：用户手输错了不该连累自动识别。
			if auto {
				plan.failed()
			}
			fmt.Println("验证码输入错误！")
			// 重新获取公钥，其实没必要，但保持 fresh
			wg.Add(1)
			go a.getRsaPublicKey(ctx, &wg, &reqTime, &encryptedResult)
			continue
		}
		if errors.Is(err, IncorrectPassword) {
			// 让外层循环重新开始时用新密码重新加密
			if !a.promptNewPassword(ctx) {
				return false
			}
			return false
		}
		return stat
	}
	return false
}

// promptNewPassword 在 Web 模式下等待用户输入新密码；返回 true 表示密码已更新，调用方可重试登录。
//
// 没有 InputHandler 时不读 stdin：client 不再负责"交互式配置"，
// 密码的持久化由输入方负责（Web 走 /login/input）。
func (a *APIClient) promptNewPassword(ctx context.Context) bool {
	a.reportLoginStage(LoginPhasePassword, 0, "")
	if a.InputHandler != nil {
		pwd, err := a.InputHandler(ctx, &InputRequest{
			Kind:   "password",
			Prompt: "密码错误，请输入新的密码",
		})
		if err != nil || pwd == "" {
			return false
		}
		a.cfg.Password = pwd // 只改运行时；持久化由输入方（Web /login/input）负责
		return true
	}
	fmt.Println("密码重置:", strings.TrimRight(a.Http.BaseURL(), "/")+"/pwdmgr/retake/index.zf")
	return false
}

// getKaptchaImage 取一张字符验证码，返回要提交的验证码文本。
//
// 顺序是"能自动就自动"：plan 允许且 Kaptcha 非空时先识别；识别不出来
// （置信度低、坏图、实现报错）再走 InputHandler 等人工输入。
//
// 第二个返回值表示这次是不是自动识别的结果：被判错时只有自动的结果才计入
// plan 的失败次数，用户手输错了不该让自动识别提前放弃。
func (a *APIClient) getKaptchaImage(ctx context.Context, plan *kaptchaPlan) (string, bool) {
	a.reportLoginStage(LoginPhaseCaptchaImage, 0, CaptchaDetailKaptcha)
	if !a.loginCtxOK(ctx) {
		return "", false
	}
	resp, err := a.Http.R().
		SetContext(ctx).
		SetQueryParam("time", strconv.FormatInt(time.Now().UnixMilli(), 10)).
		Get(config.KAPTCHA)
	if err != nil {
		fmt.Println(err)
		return "", false
	}
	img := resp.Bytes()

	// ---- 自动识别：拿不准就往下走去人工输入 ----
	if plan.auto() {
		if text, ok := a.recognizeKaptcha(ctx, img); ok {
			return text, true
		}
	}

	// ---- 人工输入：把图片交给上层，等待前端提交 ----
	if a.InputHandler != nil {
		a.reportLoginStage(LoginPhaseCaptcha, 0, CaptchaDetailKaptchaManual)
		input, err := a.InputHandler(ctx, &InputRequest{
			Kind:      "captcha",
			Prompt:    "请输入验证码",
			ImageData: img,
		})
		if err != nil {
			fmt.Println("等待验证码输入失败:", err)
			return "", false
		}
		return strings.TrimSpace(input), false
	}

	// 没有输入通道（例如直接调用协议层）：无法询问验证码。
	// 返回空串让上层按「验证码错误」处理，协议层不再退回 stdin。
	log.Println("需要验证码输入，但没有可用的 InputHandler")
	return "", false
}

func (a *APIClient) getRawCsrfToken(ctx context.Context) (string, bool, bool) {
	// 获取CSRF令牌
	var failCount int
	var timeout int
	var csrfToken string
	var hasYzm bool // 字符验证码
	var existsCSRF bool
	for {
		if !a.loginCtxOK(ctx) {
			return "", false, false
		}
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			//SetRetryCount(1).
			//SetQueryParam("time", strconv.FormatInt(time.Now().UnixMilli(), 10)).
			//SetQueryParams(map[string]string{ // ?language=zh_CN&_t=MiniSecond
			//	"language": "zh_CN",
			//	"_t":       strconv.FormatInt(time.Now().UnixMilli(), 10),
			//}).
			Get(config.LoginIndex)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return "", false, false
			}
			if errors.Is(err, context.DeadlineExceeded) {
				timeout++
				fmt.Println("CSRF 请求超时", timeout, resp.Duration())
				continue
			}
			if errors.Is(err, io.EOF) {
				fmt.Println(err)
				time.Sleep(3 * time.Second)
				continue
			} else {
				log.Println("CSRF HTTP 请求失败:", failCount, err)
				failCount++
			}
			if failCount > 1 {
				fmt.Printf("\r%d %s", failCount, err.Error())
			}
			time.Sleep(600 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			if resp.StatusCode() == 404 {
				fmt.Println("url:", a.Http.BaseURL())
				fmt.Println("404, url 填的有问题吧，是不是少了 /jwglxt 或者多了")
				log.Println("404, url 填的有问题吧")
				time.Sleep(4 * time.Second)
				continue
			}
			failCount++
			log.Println("CSRF http:", resp.Status())
		}
		if failCount > 2 {
			fmt.Println()
		}

		if resp.IsStatusSuccess() {
			doc, err := goquery.NewDocumentFromReader(bytes.NewReader(resp.Bytes()))
			if err != nil {
				log.Println("CSRF 解析 HTML 失败:", err)
				time.Sleep(150 * time.Millisecond)
				continue
			}

			a.setSliderCaptcha(hasSliderCap(doc))

			if doc.Find("#yzmDiv").Text() != "" {
				hasYzm = true
			}

			// 使用 CSS 选择器提取元素属性 "input#csrftoken"
			csrfToken, existsCSRF = doc.Find("input#csrftoken").Attr("value")
			if existsCSRF {
				return csrfToken, hasYzm, false
			}
			if utils.UserIsLogin(a.cfg.Account, resp.String()) {
				return "nil", hasYzm, false
			}
			fmt.Println("未找到 #csrftoken 元素或其 value 属性")
			log.Println("未找到 #csrftoken 元素或其 value 属性")
			log.Println(resp.String())
		}

		if resp.StatusCode() == 302 {
			if strings.Contains(resp.Header().Get("Location"), config.MENU) {
				return "", hasYzm, true
			}
			fmt.Println(resp.Header().Get("Location"))
			return "", hasYzm, false
		}
		time.Sleep(1 * time.Second)
		continue
	}
}

func (a *APIClient) getRawCsrfTokenStream(ctx context.Context) (string, bool, bool) {
	var failCount int
	var timeout int
	type result struct {
		con       bool
		csrfToken string
		haveYZM   bool
		isLogin   bool
	}
	for {
		if !a.loginCtxOK(ctx) {
			return "", false, false
		}
		resultCh := make(chan result, 1)
		func() {
			resp, err := a.hedgeC.R().
				SetResponseDoNotParse(true).
				Get(config.LoginIndex)

			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					timeout++
					log.Println("CSRF 请求超时", timeout, resp.Duration(), err)
					fmt.Println("CSRF 请求超时", timeout, resp.Duration(), err)
					resultCh <- result{con: true}
					return
				}
				if errors.Is(err, io.EOF) {
					fmt.Println(err)
					time.Sleep(3 * time.Second)
					resultCh <- result{con: true}
					return
				}
				failCount++
				log.Println("CSRF HTTP 请求失败:", failCount, err)
				fmt.Printf("\r%d %s\n", failCount, err)
				time.Sleep(600 * time.Millisecond)
				resultCh <- result{con: true}
				return
			}
			defer func(Body io.ReadCloser) {
				if Body != nil {
					go func() {
						_, _ = io.Copy(io.Discard, Body)
						_ = Body.Close()
					}()
				}
			}(resp.Body)

			if resp.IsStatusFailure() {
				failCount++
				if resp.StatusCode() == 404 {
					fmt.Println("url:", a.Http.BaseURL())
					fmt.Println("404, url 填的有问题吧，是不是少了 /jwglxt 或者多了")
					log.Println("404, url 填的有问题吧")
					time.Sleep(4 * time.Second)
					resultCh <- result{con: true}
					return
				}
				if resp.StatusCode() == 403 {
					fmt.Println("url:", a.Http.BaseURL())
				}
				log.Println("CSRF http:", resp.Status())
				fmt.Println("CSRF http:", resp.Status())
				time.Sleep(1 * time.Second)
				resultCh <- result{con: true}
				return
			}

			if resp.IsStatusSuccess() {
				tokenizer := html.NewTokenizer(resp.Body)
				foundCSRF := false
				var tokenCsrf string
				for {
					tokenType := tokenizer.Next()
					if tokenType == html.ErrorToken {
						if tokenizer.Err() != io.EOF {
							log.Println("CSRF 解析 HTML 失败:", tokenizer.Err())
							fmt.Println("CSRF 解析 HTML 失败:", tokenizer.Err())
						}
						break
					}
					if tokenType == html.StartTagToken || tokenType == html.SelfClosingTagToken {
						token := tokenizer.Token()
						// 查找 csrftoken input
						if !foundCSRF {
							if token.Data == "input" {
								var id, value string
								for _, attr := range token.Attr {
									switch attr.Key {
									case "id":
										id = attr.Val
									case "value":
										value = attr.Val
									}
								}
								if id == "csrftoken" {
									tokenCsrf = value
									foundCSRF = true
								}
							}
							continue
						}
						// 查找 yzmDiv（但仅在未遇到登录按钮时记录）
						for _, attr := range token.Attr {
							if attr.Key == "id" && attr.Val == "yzmDiv" {
								resultCh <- result{con: false, csrfToken: tokenCsrf, haveYZM: true}
								return
							}
						}
						// 检查是否为 <button id="dl">
						if token.Data == "button" {
							for _, attr := range token.Attr {
								if attr.Key == "id" && attr.Val == "dl" {
									resultCh <- result{con: false, csrfToken: tokenCsrf, haveYZM: false}
									return
								}
							}
						}
					}
				}
				if foundCSRF {
					fmt.Println("未能完全解析html尝试风险登录 io.LimitReader")
					resultCh <- result{con: false, csrfToken: tokenCsrf, haveYZM: false}
					return
				}
				log.Println("未找到 #csrftoken 元素或其 value 属性")
				fmt.Println("未找到 #csrftoken 元素或其 value 属性")
				time.Sleep(1 * time.Second)
				resultCh <- result{con: true}
				return
			}

			if resp.StatusCode() == 302 {
				location := resp.Header().Get("Location")
				if strings.Contains(location, config.MENU) {
					resultCh <- result{con: false, csrfToken: "", isLogin: true}
					return
				}
				if strings.Contains(location, config.LoginIndex) {
					current := config.NormalizeBaseURL(a.cfg.BaseURL)
					base := correctedBaseURL(a.cfg.BaseURL, location)
					if base != "" && base != current {
						fmt.Println("当前路径:", current)
						fmt.Println("重定向路径:", location)
						fmt.Println("修正url:", base)
						a.Http.SetBaseURL(base)
						a.hedgeC.SetBaseURL(base)
						a.cfg.BaseURL = base
						// 地址修正属于会话产物：交给上层在登录结束后落到账号配置上。
						// client 不再写文件；这一轮先用新地址重试。
						a.sessionOrNew().CorrectedBaseURL = base
						//if transport, err := a.Http.HTTPTransport(); err == nil {
						//	transport.CloseIdleConnections()
						//}
					}
					time.Sleep(2 * time.Second)
					resultCh <- result{con: true}
					return
				}
				fmt.Println("302:", location)
				resultCh <- result{con: false, csrfToken: "", isLogin: true}
				return
			}
			if resp.StatusCode() == 307 {
				fmt.Println("Redirect:", resp.Status())
				fmt.Println("location:", resp.Header().Get("Location"))
			}
			fmt.Println("CSRF:", resp.Status())
			time.Sleep(1 * time.Second)
			resultCh <- result{con: true}
		}()

		res := <-resultCh
		if !res.con {
			fmt.Println("csrf index ok")
			return res.csrfToken, res.haveYZM, res.isLogin
		}
		continue
	}
}

func (a *APIClient) getRTK(ctx context.Context) string {
	// 获取 cookie rtk
	a.reportLoginStage(LoginPhaseRTK, 0, "")
	for {
		if !a.loginCtxOK(ctx) {
			return ""
		}
		resp, err := a.hedgeC.R().
			SetQueryParams(map[string]string{
				"type":       "resource",
				"instanceId": "zfcaptchaLogin",
				"name":       "zfdun_captcha.js",
			}).
			SetResponseDoNotParse(true).
			Get(config.CAPTCHA)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ""
			}
			if errors.Is(err, context.DeadlineExceeded) {
				time.Sleep(275 * time.Millisecond)
				fmt.Println("rtk 请求超时", resp.Duration())
				continue
			} else {
				fmt.Println("rtk http:", err)
			}
			time.Sleep(1475 * time.Millisecond)
			continue
		}
		if resp.Body == nil {
			fmt.Println("rtk: resp.Body == nil")
			continue
		}

		if resp.IsStatusFailure() {
			if resp.StatusCode() == 404 {
				fmt.Println(a.Http.BaseURL())
				fmt.Println("404, url 填的有问题，是不是少了 /jwglxt")
				fmt.Println("请填写baseURL，后面的部分如:" + config.LoginIndex + " 是不必要的")
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				time.Sleep(4 * time.Second)
				continue
			}
			fmt.Println("rtk HTTP 错误: 状态码 ", resp.Status())
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			time.Sleep(275 * time.Millisecond)
			continue
		}

		rtk, err := getRTKFromResponse(resp.Body)
		go func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		if err != nil {
			fmt.Println("rtk http:", err)
			time.Sleep(4 * time.Second)
		} else {
			fmt.Println("rtk ok")
			return rtk
		}
	}
}

// getRTKFromResponse 从 HTTP 响应中流式读取并提取 rtk 值
func getRTKFromResponse(body io.Reader) (string, error) {
	// 使用带缓冲的 Reader 按行读取
	// rtk在前192字节中
	reader := bufio.NewReaderSize(body, 192)
	prefix := "rtk:'" // 目标字段前缀

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read error: %w", err)
		}

		// 在当前行中查找前缀
		if idx := strings.Index(line, prefix); idx != -1 {
			start := idx + len(prefix)
			// 寻找结束单引号
			end := strings.Index(line[start:], "'")
			if end == -1 {
				// 如果跨行，可继续读取直到找到闭合引号（此处简化，通常在同一行）
				return "", errors.New("rtk value spans multiple lines, not supported")
			}
			rtk := line[start : start+end]
			return rtk, nil
		}

		if err == io.EOF {
			break // 已读完所有行仍未找到
		}
	}

	return "", errors.New("rtk not found in response")
}

type captchaData struct {
	Msg    string `json:"msg"`
	T      int64  `json:"t"`
	Si     string `json:"si"`
	Imtk   string `json:"imtk"`
	Mi     string `json:"mi"`
	VS     string `json:"vs"`     // not_verify
	Status string `json:"status"` // success
}

func (a *APIClient) getCaptchaParams(ctx context.Context, rtk, t string) captchaData {
	var jsonResult captchaData
	for {
		resp, err := a.Http.R().
			SetContext(ctx).
			//SetResult(&jsonResult).
			SetQueryParams(map[string]string{
				"type":       "refresh",
				"rtk":        rtk,
				"time":       t,
				"instanceId": "zfcaptchaLogin",
			}).Get(config.CAPTCHA)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return jsonResult
			}
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("capParams 请求超时:", resp.Duration())
			} else {
				fmt.Println("\ncapParams http:", err)
			}
			log.Println("getCaptchaParams:", err)
			time.Sleep(150 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println(resp.Status())
		}
		if resp.ResultError() != nil {
			log.Println(resp.ResultError())
		}
		if err := json.Unmarshal(resp.Bytes(), &jsonResult); err != nil {
			fmt.Println(err)
			log.Println(err, resp.String())
			time.Sleep(150 * time.Millisecond)
			continue
		}
		if jsonResult.Msg != "" {
			fmt.Println(jsonResult.Msg)
		}
		fmt.Println("captchaParams ok")
		return jsonResult
	}
}

var noImage = fmt.Errorf("未获取到 image")

func (a *APIClient) getCaptchaImage(ctx context.Context, imtk, id string, T int64) ([]byte, error) {
	a.reportLoginStage(LoginPhaseCaptchaImage, 0, CaptchaDetailSlider)
	if !a.loginCtxOK(ctx) {
		return nil, noImage
	}
	resp, err := a.Http.R().
		SetContext(ctx).
		SetRetryCount(1).
		SetTimeout(31*time.Second). // 不能睡到79秒 (76-78)
		SetHeader("Accept", "image/*,*/*").
		SetQueryParams(map[string]string{
			"type":       "image",
			"id":         id,
			"imtk":       imtk,
			"t":          strconv.FormatInt(T, 10),
			"instanceId": "zfcaptchaLogin",
		}).Get(config.CAPTCHA)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, noImage
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		fmt.Println("get image http error")
		log.Println(err)
		time.Sleep(150 * time.Millisecond)
		return nil, noImage
	}
	if resp.StatusCode() == 404 { // 过期了，重试也没用
		return nil, noImage
	}
	if resp.IsStatusFailure() {
		return nil, noImage
	}

	if len(resp.Bytes()) == 0 {
		log.Println("未获取到 image")
		return nil, noImage
	}
	fmt.Println("img ok")
	return resp.Bytes(), nil
}

type rsaResponseData struct {
	Modulus  string `json:"modulus"`
	Exponent string `json:"exponent"`
}

func (a *APIClient) getRsaPublicKey(ctx context.Context, wg *sync.WaitGroup, t *string, enResult *string) {
	// 获取RSA公钥信息
	// 注意：公钥会经常刷新
	var jsonResult rsaResponseData
	defer wg.Done()
	for range 3 {
		resp, err := a.Http.R().
			SetContext(ctx).
			SetHeader("Accept", "application/json, */*").
			//SetResult(&jsonResult).
			SetQueryParams(map[string]string{
				"time": strconv.FormatInt(time.Now().UnixMilli(), 10),
				"_":    *t,
			}).SetRetryCount(1).
			Get(config.PublicKey)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("pubkey 超时", resp.Duration())
			} else {
				fmt.Println("pubkey 获取错误:", err)
				log.Println("pubkey HTTP 请求失败:", err)
			}
			//continue
		}
		if resp.IsStatusFailure() {
			log.Println("pubkey HTTP 错误: 状态码 ", resp.Status())
			//continue
		}
		if err := json.Unmarshal(resp.Bytes(), &jsonResult); err != nil {
			fmt.Println(err)
			log.Println(err, resp.String())
		}
		if jsonResult.Modulus == "" || jsonResult.Exponent == "" {
			log.Println("pubkey 获取错误:", resp.Status(), resp.String(), *t)
			*t = strconv.FormatInt(time.Now().UnixMilli(), 10)
			time.Sleep(70 * time.Millisecond)
			continue
		}
		*enResult, err = rsa.EncryptRsa(jsonResult.Modulus, jsonResult.Exponent, a.cfg.Password)
		if err != nil {
			*t = strconv.FormatInt(time.Now().UnixMilli(), 10)
			continue
		}
		fmt.Println("rsa ok")
		return
	}
}

type captchaVerifyData struct {
	Msg    string `json:"msg"`    // 验证失败,请稍后重试
	VS     string `json:"vs"`     // verified, not_verify
	Status string `json:"status"` // success, fail
}

func (a *APIClient) captchaVerify(ctx context.Context, rtk string, LoginExtend []byte, x int) bool {
	captchaVerifyResult := check_code.GetTrackByte(x, 480)
	if captchaVerifyResult == nil {
		return false
	}
	var result captchaVerifyData
	for range 2 {
		formData := map[string]string{
			"type":       "verify",
			"rtk":        rtk,
			"time":       strconv.FormatInt(time.Now().UnixMilli(), 10),
			"mt":         base64.StdEncoding.EncodeToString(captchaVerifyResult),
			"instanceId": "zfcaptchaLogin",
			"extend":     base64.StdEncoding.EncodeToString(LoginExtend),
		}
		resp, err := a.Http.R().
			SetContext(ctx).
			//SetResult(&result).
			SetFormData(formData). // 这里不支持json
			Post(config.CAPTCHA)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false
			}
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("captcha_verify 超时", resp.Duration())
			} else {
				fmt.Println("captcha_verify http error:", err)
			}
			log.Println("captcha_verify HTTP 请求失败:", err)
			// fmt.Println(err)
			time.Sleep(150 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println("captcha_verify HTTP 错误: 状态码 ", resp.Status())
			fmt.Println("captcha_verify ", resp.Status())
			if resp.StatusCode() == 404 {
				return false
			}
		}
		if resp.StatusCode() != 200 {
			log.Println("captcha_verify HTTP 错误: 状态码 ", resp.Status())
		}
		// fmt.Println(resp)
		// {"msg":"","vs":"verified","status":"success"}
		if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
			fmt.Println(err)
			log.Println(err, resp.String())
			continue
		}

		if result.VS == "verified" && result.Status == "success" {
			return true
		} else if result.VS == "not_verify" {
			return false
		}
	}
	return false
}

func (a *APIClient) postLogin(csrf, t, mm, yzm string) (bool, error) {
	// fmt.Println("postLogin sleep 300")
	// time.Sleep(300 * time.Second)
	if csrf == "nil" || mm == "" {
		return false, CsrfNotExist
	}
	for range 6 {
		formData := map[string]string{
			"csrftoken": csrf, // 某些系统csrf就是空的
			"yhm":       a.cfg.Account,
			"mm":        mm, // TODO:使用后是否需要刷新公钥？
		}
		if len(yzm) != 0 {
			formData["yzm"] = yzm
		}
		resp, err := a.Http.R().
			SetQueryParam("time", t).
			SetFormData(formData).
			Post(config.LoginIndex)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("postLogin 超时", resp.Duration())
			} else {
				fmt.Println("postLogin http error")
			}
			log.Println("postLogin 请求失败:", err)
			fmt.Println(err)
			time.Sleep(170 * time.Millisecond)
			continue
		}
		//log.Println()
		if resp.IsStatusFailure() {
			log.Println("postLogin HTTP 错误: 状态码 ", resp.Status())
			continue
		}
		//log.Println(resp.String())
		stat, err1 := isLogin(a.cfg.Account, resp.String())
		if err1 != nil {
			if len(resp.Cookies()) != 0 {
				return false, errors.Join(LoginFailSetCookie, err1)
			}
			// CSRF 没必要重复获取，同cookie下是一样的
			return false, err1
		}
		if resp.StatusCode() == 302 || stat {
			//fmt.Println("postLogin", resp.Status())
			fmt.Println("登录成功✅")
			// 这个location 并不是很有参考意义
			log.Println("登录成功 Location:", resp.Header().Get("Location"))
			return true, nil
		}
	}
	return false, nil
}

func (a *APIClient) cas2LoginCtl(ctx context.Context) bool {
	log.Println("cas2Login=======")
	// 兜底补挂：APIClient.SetProtocolHook 已经会下发，
	// 这里再挂一次是幂等的，保证任何构造路径下门户请求都可见。
	a.cas2Client.SetProtocolHook(a.ProtocolHook)
	a.reportLoginStage(LoginPhasePortal, 0, "")
	if !a.cas2Client.Login(ctx) {
		return false
	}
	//if !a.cas2Client.GetJwCookie() {
	//	return false
	//}
	a.reportLoginStage(LoginPhasePortalTicket, 0, "")
	location := a.ssoLogin(ctx)
	if location == "" {
		return false
	}
	a.reportLoginStage(LoginPhaseTicket, 0, "")
	location = a.cas2Client.GetJwCookie2(location, ctx) // https://jwglxt.ycit.edu.cn/ticketlogin?uid=&timestamp=&verify=
	if location == "" {
		return false
	}
	return a.ssoTicketLogin(location, ctx)
}

func (a *APIClient) ssoLogin(ctx context.Context) string {
	a.reportLoginStage(LoginPhaseSSO, 0, "")
	log.Println("ssoLogin=======")
	targetURL, _ := url.Parse(a.Http.BaseURL())
	sRoute := ""
	for _, c := range a.Http.CookieJar().Cookies(targetURL) {
		if c.Name == "route" {
			sRoute = c.Value
		}
	}
	fmt.Printf("ssoLogin: (%s)\n", sRoute)
	for range 10 {
		if !a.loginCtxOK(ctx) {
			return ""
		}
		resp, err := a.Http.R().
			SetContext(ctx).
			SetHeader("Referer", "https://portal.ycit.edu.cn/main.html").
			SetRetryCount(0).
			Get("/sso/hnyyxyiotlogin")
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println(err)
				time.Sleep(2 * time.Second)
				continue
			}
			if errors.Is(err, context.Canceled) {
				return ""
			}
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("ssoLogin:", err, resp.Duration())
				log.Println("ssoLogin:", err, resp.Duration())
				a.cookie()
				time.Sleep(1 * time.Second)
				continue
			}
			fmt.Println("ssoLogin:", err)
			log.Println("ssoLogin:", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if resp.StatusCode() != 302 {
			fmt.Println("sso/hnyyxyiotlogin:", resp.Status())
			log.Println("sso/hnyyxyiotlogin:", resp.Status())
			time.Sleep(2 * time.Second)
			continue
		}
		for _, c := range resp.Cookies() {
			if c.Name == "route" {
				// 服务端确认可用的 route 记进会话产物；被取代的旧值同时清掉。
				// 落盘由上层（Web）在登录结束后统一处理。
				sess := a.sessionOrNew()
				sess.ForgetRoute(sRoute)
				sess.LearnRoute(c.Value)
				break
			}
		}
		location := resp.Header().Get("Location")
		log.Println(location) // https://cas2.ycit.edu.cn/cas/login?service=http://jwglxt.ycit.edu.cn/sso/hnyyxyiotlogin?targetUrl={base64}aHR0cDovL2p3Z2x4dC55Y2l0LmVkdS5jbi9zc28vc3NvL2luZGV4LmpzcA==
		return location
	}
	fmt.Println("===============================")
	fmt.Println("config routes:", a.cfg.Routes)
	fmt.Println("current cookie:")
	a.cookie()
	fmt.Println("===============================")
	return ""
}

func (a *APIClient) ssoTicketLogin(location string, ctx context.Context) bool {
	a.reportLoginStage(LoginPhaseTicket, 0, "")
	if location == "" {
		log.Println("GetJwCookie2 location:", location)
		return false
	}
	fmt.Println("verify ticket")
	var location2 string
	for range 8 {
		if !a.loginCtxOK(ctx) {
			return false
		}
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetRetryCount(1).
			Get(location) // https://jwglxt.ycit.edu.cn/sso/hnyyxyiotlogin?targetUrl={base64}aHR0cDovL2p3Z2x4dC55Y2l0LmVkdS5jbi9zc28vc3NvL2luZGV4LmpzcA==&ticket=ST-529025-5R1TqFz
		if err != nil {
			if errors.Is(err, io.EOF) {
				log.Println("ssoTicketLogin EOF")
			}
			if errors.Is(err, context.Canceled) {
				return false
			}
			fmt.Println(err)
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.StatusCode() == 301 {
			location = resp.Header().Get("Location")
			continue
		}
		if resp.StatusCode() != 302 {
			fmt.Println(resp.Status())
			log.Println("resp2:", resp.Status(), resp.Header(), resp.String())
			time.Sleep(2 * time.Second)
			continue
		}
		location2 = resp.Header().Get("Location") // https://jwglxt.ycit.edu.cn/ticketlogin?uid=&timestamp=&verify=
		if location2 == "" {
			log.Println("location:", location2)
			continue
		}
		log.Println("location2:", location2)
		break
	}

	if location2 == "" {
		a.cookie()
		return false
	}
	if strings.HasPrefix(a.cfg.BaseURL, "https://") && strings.HasPrefix(location2, "http://") {
		location2 = strings.Replace(location2, "http://", "https://", -1)
	}
	fmt.Println("ticketlogin")
	for range 6 {
		if !a.loginCtxOK(ctx) {
			return false
		}
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetHeader("Referer", "https://portal.ycit.edu.cn/main.html").
			SetRetryCount(1).
			Get(location2)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false
			}
			fmt.Println(err)
			log.Println("ssoTicketLogin:", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if resp.StatusCode() == 301 {
			location2 = resp.Header().Get("Location")
			continue
		}
		if resp.StatusCode() != 302 {
			fmt.Println("ssoTicketLogin:", resp.Status())
			log.Println("ssoTicketLogin:", resp.Status())
			time.Sleep(2 * time.Second)
			continue
		}
		location2 = resp.Header().Get("Location") // /xtgl/login_slogin.html
		if location2 == "" {
			fmt.Println("location为空", resp.Status())
			time.Sleep(1 * time.Second)
			continue
		}
		if strings.Contains(location2, config.LoginIndex) {
			continue
		}
		if strings.Contains(location2, config.MENU) { // /xtgl/index_initMenu.html?jsdm=xs&_t=1785043153113&echarts=1
			// c80e782f5a3340e86274809ce311b6b4 1
			// 425b918000ed5b18d10afb85fbbf8ec7 1
			// 018f9ff65252ca4f51865070844ae0be ❌
			// 34ff17f478ebaa7e4063c9d5a95901d0 ❌
			fmt.Println("登录成功✅")
			return true
		}
		break
	}
	a.cookie()
	return false
}

// correctedBaseURL 从 302 的 Location 反推站点根地址，保留 /jwglxt 这类子路径。
//
// Location 既可能是绝对地址（https://host/jwglxt/xtgl/login_slogin.html），
// 也可能是相对地址（/jwglxt/xtgl/login_slogin.html），两种都只取 LoginIndex
// 之前的部分；取不到时返回空串，调用方保持原地址重试。
func correctedBaseURL(currentBase, location string) string {
	loc, err := url.Parse(location)
	if err != nil || loc.Path == "" {
		return ""
	}
	idx := strings.Index(loc.Path, config.LoginIndex)
	if idx < 0 {
		return ""
	}
	scheme, host := loc.Scheme, loc.Host
	if !loc.IsAbs() {
		cur, err := url.Parse(currentBase)
		if err != nil || cur.Host == "" {
			return ""
		}
		scheme, host = cur.Scheme, cur.Host
	}
	return config.NormalizeBaseURL(scheme + "://" + host + loc.Path[:idx])
}
