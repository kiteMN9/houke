package protocol

import (
	"school_sdk/protocol/cas2"
	"sync"
	"time"
)

// =========================================================================
// 登录阶段（LoginStage）由 APIClient 自己记账
//
// 和 loginState.go 的登录态一样：谁在跑登录流程，就由谁记账；上层只读快照
// 或订阅变化。以前 WebUI 想在登录过程中显示"正在取登录页 / 正在识别验证码 /
// 正在换票"，只能靠 ProtocolHook 拿到原始 URL，再由 web/classify.go 猜。
// hook 是可以缺席的（tui、测试，或进程里只挂了 InputHandler），阶段不应该
// 跟着一起消失；而这些语义在 protocol 的每个分支里本来就是已知的。
//
// 约束：
//   - 只在登录主链路的 goroutine 上调用（doLogin / cas2LoginCtl / sso 流程），
//     不放进 getRsaPublicKey 之类的并发 goroutine，避免阶段来回跳；
//   - observer 在登录 goroutine 上同步调用，实现必须快速返回、不得阻塞；
//   - 阶段是运行态，不落盘，也不进 accounts.json。
// =========================================================================

// LoginPhase 是登录过程中"用户视角"的阶段标识。
// 取值是稳定的机器可读串；给用户看的中文文案由上层（web）决定。
type LoginPhase string

const (
	// LoginPhasePrepare 登录开始，还没发第一个请求。
	LoginPhasePrepare LoginPhase = "login.prepare"
	// LoginPhaseFetchForm 获取登录页 / CSRF token，并判断有没有验证码。
	LoginPhaseFetchForm LoginPhase = "login.fetch-form"
	// LoginPhaseRTK 获取滑块验证码的 rtk（zfdun_captcha.js 里的运行时密钥）。
	LoginPhaseRTK LoginPhase = "login.rtk"
	// LoginPhaseCaptchaImage 下载验证码图片（字符验证码或滑块背景图）。
	LoginPhaseCaptchaImage LoginPhase = "login.captcha-image"
	// LoginPhaseCaptcha 取验证码、等用户输入或自动识别。
	LoginPhaseCaptcha LoginPhase = "login.captcha"
	// LoginPhasePublicKey 获取 RSA 公钥并加密密码。
	LoginPhasePublicKey LoginPhase = "login.public-key"
	// LoginPhaseSubmit 提交账号密码。
	LoginPhaseSubmit LoginPhase = "login.submit"
	// LoginPhasePassword 密码错误，等待用户输入新密码。
	LoginPhasePassword LoginPhase = "login.password"
	// LoginPhasePasswordError 门户账号或密码错误（cas2 直接拒绝，没有输入入口）。
	// 与 LoginPhasePassword 区分：那个是"等用户改密码"，这个是"这轮已经失败"。
	LoginPhasePasswordError LoginPhase = "login.password-error"
	// LoginPhaseAccountLocked 门户账户已被锁定，重试没有意义。
	LoginPhaseAccountLocked LoginPhase = "login.account-locked"
	// LoginPhaseSessionValid 已有门户会话仍然有效，本次跳过密码登录。
	LoginPhaseSessionValid LoginPhase = "login.session-valid"
	// LoginPhaseSessionExpired 门户会话已失效，正在重新登录；微信模式需重新扫码。
	LoginPhaseSessionExpired LoginPhase = "login.session-expired"
	// LoginPhaseFailedAttempts 登录页带着历史失败次数，服务端可能要求验证码。
	LoginPhaseFailedAttempts LoginPhase = "login.failed-attempts"
	// LoginPhasePortalFailed 门户登录未成功（服务端拒绝 / 票据缺失 / 5xx）。
	LoginPhasePortalFailed LoginPhase = "login.portal-failed"
	// LoginPhasePortal 统一身份认证（cas2）登录。
	LoginPhasePortal LoginPhase = "login.portal"
	// LoginPhasePortalTicket 门户登录成功，准备换取教务系统票据。
	LoginPhasePortalTicket LoginPhase = "login.portal-ticket"
	// LoginPhaseSSO 跳转 SSO 入口、发现路由。
	LoginPhaseSSO LoginPhase = "login.sso"
	// LoginPhaseTicket 用 ticket 换取教务系统登录态。
	LoginPhaseTicket LoginPhase = "login.ticket"
	// LoginPhaseQRCode 微信扫码登录已生成二维码，等待用户扫描。
	LoginPhaseQRCode LoginPhase = "login.qr"
	// LoginPhaseQRScanned 用户已扫码，等待手机端确认。
	LoginPhaseQRScanned LoginPhase = "login.qr-scanned"
	// LoginPhaseQRExpired 二维码过期，正在刷新。
	LoginPhaseQRExpired LoginPhase = "login.qr-expired"
	// LoginPhaseQRRefused 用户在手机端拒绝或取消登录。
	LoginPhaseQRRefused LoginPhase = "login.qr-refused"
	// LoginPhaseDone 登录成功。
	LoginPhaseDone LoginPhase = "login.done"
)

// 验证码阶段（LoginPhaseCaptchaImage / LoginPhaseCaptcha）的 Detail 取值。
//
// 这些是稳定字符串：web 靠它们决定显示哪句文案，别随手改。
const (
	// CaptchaDetailKaptcha 下载的是字符验证码，还没开始识别。
	CaptchaDetailKaptcha = "字符验证码"
	// CaptchaDetailKaptchaAuto 正在自动识别字符验证码。
	CaptchaDetailKaptchaAuto = "正在自动识别字符验证码"
	// CaptchaDetailKaptchaManual 自动识别不可用，等待人工输入字符验证码。
	CaptchaDetailKaptchaManual = "等待输入字符验证码"
	// CaptchaDetailSlider 正在识别滑块验证码。
	CaptchaDetailSlider = "滑块验证码"
	// CaptchaDetailNeedSlider 服务端要求先滑动验证。
	CaptchaDetailNeedSlider = "需要滑动验证"
)

// LoginStage 是一次登录阶段快照，可直接放进状态查询或推送帧。
type LoginStage struct {
	Phase   LoginPhase // 阶段标识；空串表示这个 client 还没开始过登录
	Detail  string     // 可选补充说明（如验证码类型），不参与逻辑判断
	Attempt int        // 当前阶段的重试轮次；0 表示不适用
	At      time.Time  // 进入该阶段的时间
}

// loginStageState 是 APIClient 内的阶段记账。
type loginStageState struct {
	mu       sync.Mutex
	stage    LoginStage
	observer func(LoginStage)
}

// LoginStage 返回当前（或最近一次）登录阶段快照。
func (a *APIClient) LoginStage() LoginStage {
	if a == nil {
		return LoginStage{}
	}
	a.loginStage.mu.Lock()
	defer a.loginStage.mu.Unlock()
	return a.loginStage.stage
}

// SetLoginObserver 挂载阶段变化回调；传 nil 取消。
// 回调在登录流程的 goroutine 上同步执行，必须快速返回。
func (a *APIClient) SetLoginObserver(f func(LoginStage)) {
	if a == nil {
		return
	}
	a.loginStage.mu.Lock()
	a.loginStage.observer = f
	a.loginStage.mu.Unlock()
}

// reportLoginStage 记录一次阶段变化并通知 observer。
// 阶段、轮次、补充说明都没变时不重复通知：同一阶段的多个请求不该刷屏。
func (a *APIClient) reportLoginStage(phase LoginPhase, attempt int, detail string) {
	if a == nil {
		return
	}
	a.loginStage.mu.Lock()
	prev := a.loginStage.stage
	if prev.Phase == phase && prev.Attempt == attempt && prev.Detail == detail {
		a.loginStage.mu.Unlock()
		return
	}
	st := LoginStage{Phase: phase, Detail: detail, Attempt: attempt, At: time.Now()}
	a.loginStage.stage = st
	obs := a.loginStage.observer
	a.loginStage.mu.Unlock()

	// observer 在锁外调用：实现里可能要读 client 状态，不能和阶段锁嵌套。
	if obs != nil {
		obs(st)
	}
}

// qrStatusPhase 把 cas2 的扫码状态映射成统一的登录阶段。
// 未知状态返回空串，调用方据此跳过上报。
func qrStatusPhase(status cas2.QRStatus) LoginPhase {
	switch status {
	case cas2.QRWaiting:
		return LoginPhaseQRCode
	case cas2.QRScanned:
		return LoginPhaseQRScanned
	case cas2.QRExpired:
		return LoginPhaseQRExpired
	case cas2.QRRefused:
		return LoginPhaseQRRefused
	default:
		return ""
	}
}

// cas2StepPhase 把 cas2 密码登录流程内部的步骤映射成统一的登录阶段。
//
// cas2 的取登录页 / 加密密码 / 提交表单与直连教务系统的密码登录是同一件事，
// 因此复用 fetch-form / public-key / submit 三个阶段；失败原因则各有独立阶段，
// 让 WebUI 的时间线直接显示"账户被锁定"而不是一句笼统的"登录失败"。
// 未知步骤返回空串，调用方据此跳过上报。
func cas2StepPhase(step cas2.Step) LoginPhase {
	switch step {
	case cas2.StepSessionValid:
		return LoginPhaseSessionValid
	case cas2.StepSessionExpired:
		return LoginPhaseSessionExpired
	case cas2.StepFetchForm:
		return LoginPhaseFetchForm
	case cas2.StepFailedAttempts:
		return LoginPhaseFailedAttempts
	case cas2.StepPublicKey:
		return LoginPhasePublicKey
	case cas2.StepSubmit:
		return LoginPhaseSubmit
	case cas2.StepPasswordError:
		return LoginPhasePasswordError
	case cas2.StepAccountLocked:
		return LoginPhaseAccountLocked
	case cas2.StepLoginFailed:
		return LoginPhasePortalFailed
	default:
		return ""
	}
}

// attachCas2Steps 把 cas2 的登录步骤翻译成统一的 LoginPhase。
//
// 与扫码状态一样，WebUI 只认阶段，不需要知道 cas2 的 Step 枚举。
// 密码登录与微信登录都要挂：微信模式下的会话失效、换票失败同样走这些步骤。
func (a *APIClient) attachCas2Steps() {
	if a == nil || a.cas2Client == nil {
		return
	}
	a.cas2Client.SetStepHandler(func(step cas2.Step, attempt int) {
		if phase := cas2StepPhase(step); phase != "" {
			a.reportLoginStage(phase, attempt, "")
		}
	})
}

// resetLoginStage 在每次登录开始时清掉上一次的残留阶段。
func (a *APIClient) resetLoginStage() {
	if a == nil {
		return
	}
	a.loginStage.mu.Lock()
	a.loginStage.stage = LoginStage{}
	a.loginStage.mu.Unlock()
}
