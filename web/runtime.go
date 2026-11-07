package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"
)

// accountRuntime 是账号对应的运行态（engine.Engine）。
// 这里不保存登录态，也不保存配置副本：两者都由 engine 持有，
// web 只按需读取（rt.engine.LoggedIn() / rt.engine.Settings()）。
type accountRuntime struct {
	id     string
	engine *engine.Engine

	// 登录 / 初始化任务的最长等待时间（账号级配置，默认 10 分钟）
	taskTimeout time.Duration

	// 登录专用锁，避免长时间持锁阻塞 status/logout
	loginMu     sync.Mutex
	loginCancel context.CancelFunc

	// 初始化专用锁（与登录互相独立，互不阻塞）
	initMu     sync.Mutex
	initCancel context.CancelFunc

	// Web 模式下等待用户输入的挂起请求
	inputMu      sync.Mutex
	pendingInput *pendingInput

	// 微信扫码登录的当前二维码。图片只在这里存一份，SSE 只发元数据，
	// 前端按 rev 通过 GET /login/qr 取图片。
	qrMu  sync.RWMutex
	qr    *loginQR
	qrSeq int64

	// 长任务可观测性：登录/初始化的阶段与事件流
	tasks *taskStore
}

// longTaskTimeout 返回该运行态下登录/初始化任务的超时时间（永远为正数）。
func (rt *accountRuntime) longTaskTimeout() time.Duration {
	if rt.taskTimeout > 0 {
		return rt.taskTimeout
	}
	return fallbackLoginTimeout
}

// requestStage 把一次即将发出的 HTTP 请求翻译成"当前阶段"，并写入任务事件流。
// 由 protocol.ProtocolHook 在请求前同步调用，必须轻量。
//
// 登录 / 门户登录的请求不再经过这里：它们的阶段由 protocol 的 LoginObserver
// 上报（见 loginStage），无论 hook 有没有挂都能看到。
func (rt *accountRuntime) requestStage(_, rawURL string) {
	if rt.tasks == nil {
		return
	}
	stage, text, detail, logEvent := classifyRequest(rawURL)
	if stage == "" {
		// 连路径都没解析出来：只让"请求计数"动起来
		rt.tasks.touchStage(rt.id, "", "")
		return
	}
	if !logEvent {
		// 未知端点：只更新阶段与计数，不写日志，避免把日志刷爆
		rt.tasks.touchStage(rt.id, stage, text)
		return
	}
	rt.tasks.emitRequest(rt.id, taskEvent{
		Kind:   stage,
		Stage:  stage,
		Text:   text,
		Detail: detail,
	})
}

// loginStage 接收 protocol 上报的登录阶段，翻译成文案写进任务阶段。
// 它不依赖任何一次 HTTP 请求：hook 没挂时这里仍然有值。
func (rt *accountRuntime) loginStage(st protocol.LoginStage) {
	if rt.tasks == nil {
		return
	}
	text := loginStageText(st)
	if text == "" {
		return
	}
	rt.tasks.setLoginStage(rt.id, string(st.Phase), text)
}

// initStage 接收 protocol 上报的初始化阶段，翻译成文案写进任务阶段。
func (rt *accountRuntime) initStage(st protocol.InitStage) {
	if rt.tasks == nil {
		return
	}
	text := initStageText(st)
	if text == "" {
		return
	}
	rt.tasks.setInitStage(rt.id, string(st.Phase), text)
}

// pendingInput 一次待用户输入的请求。
type pendingInput struct {
	kind      string
	prompt    string
	imageData []byte
	respCh    chan string
	createdAt time.Time
}

// requestInput 由 protocol.InputHandler 调用。会阻塞直到：
//   - 前端通过 submitInput 提交了内容；
//   - ctx 被取消（登录超时 / /login/cancel）。
func (rt *accountRuntime) requestInput(ctx context.Context, req *protocol.InputRequest) (string, error) {
	p := &pendingInput{
		kind:      req.Kind,
		prompt:    req.Prompt,
		imageData: req.ImageData,
		respCh:    make(chan string, 1),
		createdAt: time.Now(),
	}
	rt.inputMu.Lock()
	if rt.pendingInput != nil {
		rt.inputMu.Unlock()
		return "", fmt.Errorf("已有待处理的输入请求")
	}
	rt.pendingInput = p
	rt.inputMu.Unlock()

	// 让任务时间线里出现"正在等待人工输入"，用户一眼就知道卡在自己身上
	if rt.tasks != nil {
		text := "需要验证码，等待输入"
		if req.Kind == "password" {
			text = "密码不正确，等待输入新密码"
		}
		rt.tasks.emit(rt.id, taskEvent{
			Kind:   "task.input." + req.Kind,
			Stage:  "task.input." + req.Kind,
			Text:   text,
			Detail: req.Prompt,
		})
	}

	defer func() {
		rt.inputMu.Lock()
		if rt.pendingInput == p {
			rt.pendingInput = nil
		}
		rt.inputMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case v := <-p.respCh:
		if rt.tasks != nil {
			rt.tasks.emit(rt.id, taskEvent{
				Kind:  "task.input.submitted",
				Stage: "task.input.submitted",
				Text:  "已提交输入，继续登录",
			})
		}
		return v, nil
	}
}

// getPendingInput 返回当前等待用户输入的请求（没有待处理请求时为 nil）。
func (rt *accountRuntime) getPendingInput() *pendingInput {
	rt.inputMu.Lock()
	defer rt.inputMu.Unlock()
	return rt.pendingInput
}

// submitInput 由 HTTP handler 调用。返回 false 表示当前没有待处理请求。
func (rt *accountRuntime) submitInput(value string) bool {
	rt.inputMu.Lock()
	p := rt.pendingInput
	if p == nil {
		rt.inputMu.Unlock()
		return false
	}
	// 先清空，避免重复提交
	rt.pendingInput = nil
	rt.inputMu.Unlock()

	select {
	case p.respCh <- value:
		return true
	default:
		return false
	}
}

// loginQR 一张等待扫描的微信二维码及其元数据。
type loginQR struct {
	image     []byte
	pageURL   string
	rev       int64
	createdAt time.Time
	expiresAt time.Time
}

// loginQRView 是二维码的公开元数据：图片字节不进入 SSE，避免每帧全量重发。
type loginQRView struct {
	Rev       int64  `json:"rev"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	PageURL   string `json:"pageUrl,omitempty"`
}

// setQRCode 由 protocol.QRHandler 调用。nil 或空图片表示二维码已失效
// （过期 / 已扫描 / 登录结束），此时只清空，不递增 rev。
func (rt *accountRuntime) setQRCode(qr *protocol.QRCode) {
	if rt == nil {
		return
	}
	rt.qrMu.Lock()
	defer rt.qrMu.Unlock()
	if qr == nil || len(qr.Image) == 0 {
		rt.qr = nil
		return
	}
	rt.qrSeq++
	rt.qr = &loginQR{
		image:     qr.Image,
		pageURL:   qr.PageURL,
		rev:       rt.qrSeq,
		createdAt: time.Now(),
		expiresAt: qr.ExpiresAt,
	}
}

// clearQRCode 清空当前二维码（不影响 rev 序列，下一张一定换新 rev）。
func (rt *accountRuntime) clearQRCode() {
	if rt == nil {
		return
	}
	rt.qrMu.Lock()
	rt.qr = nil
	rt.qrMu.Unlock()
}

// qrView 返回当前二维码的元数据；没有二维码时为 nil。
func (rt *accountRuntime) qrView() *loginQRView {
	if rt == nil {
		return nil
	}
	rt.qrMu.RLock()
	defer rt.qrMu.RUnlock()
	if rt.qr == nil {
		return nil
	}
	return &loginQRView{
		Rev:       rt.qr.rev,
		CreatedAt: rt.qr.createdAt.UnixMilli(),
		ExpiresAt: rt.qr.expiresAt.UnixMilli(),
		PageURL:   rt.qr.pageURL,
	}
}

// qrImage 返回指定版本的二维码图片；rev<=0 表示"当前版本"。
// 没有二维码或版本已过期时返回 false，避免前端拿旧 rev 覆盖新图。
func (rt *accountRuntime) qrImage(rev int64) ([]byte, bool) {
	if rt == nil {
		return nil, false
	}
	rt.qrMu.RLock()
	defer rt.qrMu.RUnlock()
	if rt.qr == nil {
		return nil, false
	}
	if rev > 0 && rev != rt.qr.rev {
		return nil, false
	}
	out := make([]byte, len(rt.qr.image))
	copy(out, rt.qr.image)
	return out, true
}

// loginKindOf 把账号里的登录方式映射成 engine 的构造参数。
func loginKindOf(mode string) engine.LoginKind {
	switch mode {
	case LoginModeCAS:
		return engine.LoginCAS
	case LoginModeWeChat:
		return engine.LoginWeChat
	default:
		return engine.LoginPassword
	}
}

// buildRuntime 由 Account 构造运行时。
//
// 配置和账号记录之间只走 toSettings / applySession 两个显式映射，
// 不再有 map[string]any → json.Marshal 的 JSON 往返：字段写错是编译错误，
// 不会静默丢值（以前 perInfo 就是这样被硬编码成 true 的）。
//
// 会话产物（JWT / 学到的 route / 修正过的地址）走 config.Session，
// 和用户配置分开：client 只改内存，登录结束后由 doLogin 取快照写回 Account。
//
// 配置不合法时返回错误（例如 baseURL 带路径、timeout 为负），由路由层
// 变成 500 并附上原因，不再等第一次请求失败才发现。
func buildRuntime(s *AccountStore, id string, a *Account) (*accountRuntime, error) {
	settings := a.toSettings()
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("账号 %q 配置不合法: %w", a.Name, err)
	}

	rt := &accountRuntime{
		id:          id,
		tasks:       s.tasks,
		taskTimeout: a.loginTimeoutDuration(),
	}

	// 先建 rt 再建 Engine：回调都捕获 rt 指针。
	// Ask 把"等待人工输入"接到 SSE pendingInput；OnStage/OnInitStage 接
	// protocol 的语义阶段（hook 缺席时仍然可用）；OnRequest 补请求明细。
	// Kaptcha 是字符验证码的自动识别器（没配模型时为 nil，登录退回人工输入）。
	eng, err := engine.New(settings, a.session(), loginKindOf(a.effectiveLoginMode()), engine.Options{
		Ask:         rt.requestInput,
		OnStage:     rt.loginStage,
		OnInitStage: rt.initStage,
		OnRequest:   rt.requestStage,
		OnQRCode:    rt.setQRCode,
		Kaptcha:     s.kaptchaRecognizer(),
		// 所有账号共用同一份 transport 池：切换账号 / 重登时不再重新握 TCP+TLS。
		Transports: s.transports,
	})
	if err != nil {
		return nil, fmt.Errorf("账号 %q 构造客户端失败: %w", a.Name, err)
	}
	rt.engine = eng
	// 自动重登是账号级策略：构造时注入一次，之后由 PUT /auto-relogin 即时更新。
	eng.SetAutoRelogin(a.autoReloginEnabled())
	// 愿望清单是账号级配置：构造时注入一次，之后由 PUT /wish 即时更新。
	eng.SetWish(a.wish())

	return rt, nil
}
