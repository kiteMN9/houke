// Package engine 是选课引擎：它持有一个账号的会话状态，负责编排 protocol 层的请求。
//
// 分层目标（见根目录 deps_test.go）：
//
//	web ──┐
//	tui ──┴──► engine ──► protocol
//
// 适配层（web / tui）不应该直接操作 protocol.Client：登录态、初始化、课程缓存
// 这些"会话语义"都在这里收口。protocol 现在只负责"发请求 + 解析"：会话失效时
// 它返回 protocol.ErrUnauthorized，由 Engine 的 authorize/retryOnUnauthorized
// 决定是否重登，适配层再把最终错误翻译成 HTTP/终端语义。
package engine

import (
	"context"
	"errors"
	"sync/atomic"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// Options 是 Engine 的装配参数。
//
// Ask / OnStage / OnInitStage / OnRequest 是 protocol 与上层之间的端口：
//   - Ask：登录过程中需要人工输入时（验证码 / 新密码）由上层阻塞等待；
//     Web 走 SSE + pendingInput，tui 走 stdin。
//   - OnStage：protocol 自己记账的登录阶段变化（可选）。它不依赖 HTTP 请求，
//     所以 hook 缺席时上层仍能显示"正在取登录页 / 正在换票"。
//   - OnInitStage：protocol 自己记账的初始化阶段变化（可选），抓选课参数时
//     上报"取首页 / 等待开放 / 解析页签 / 取列表参数 / 解析规则"。
//   - OnRequest：每次 HTTP 请求发出前的轻量通知（可选），只适合做明细/兜底；
//     Web 用它补任务时间线，tui 可用来打印进度。
type Options struct {
	Ask         func(ctx context.Context, req *protocol.InputRequest) (string, error)
	OnStage     func(stage protocol.LoginStage)
	OnInitStage func(stage protocol.InitStage)
	OnRequest   func(method, rawURL string)
	// OnQRCode 接收微信扫码登录的二维码快照（可选）。qr == nil 表示二维码
	// 已失效，宿主应清除显示。回调在登录 goroutine 上同步执行，不得阻塞。
	OnQRCode func(qr *protocol.QRCode)
	// Kaptcha 是字符验证码的自动识别器（可选）。注入后登录先识别、识别不了
	// 再走 Ask 等人工输入；nil 就是纯人工。
	Kaptcha protocol.KaptchaRecognizer
	// Transports 是跨账号共享的底层连接池（可选）。nil 表示每个账号各建
	// 一份 transport；Web 宿主传一份，账号切换 / 重登时复用 TCP + TLS 连接。
	// 共享的只有 transport：cookie jar 与 resty 客户端仍每账号独立。
	Transports *protocol.TransportPool
}

// LoginKind 是账号的登录方式。
//
// engine 只把用户的选择翻译成 protocol 的两个构造参数（isCas2 / WX）；
// 具体怎么登录仍然由 protocol/cas2 决定。放在 engine 而不是 protocol，
// 是因为 tui 目前直接调 protocol，保留它原来的 bool 参数可以少改一处。
type LoginKind uint8

const (
	// LoginPassword 是教务系统账号密码登录。
	LoginPassword LoginKind = iota
	// LoginCAS 是 CAS2 统一身份认证（门户密码）登录。
	LoginCAS
	// LoginWeChat 是 CAS2 微信扫码登录。
	LoginWeChat
)

// Engine 绑定一个账号：一个 protocol.Client + 一份会话状态。
//
// 并发约定见 session：初始化只串行化自己，不阻塞只读查询；
// 登录态由 protocol.Client 记账，Engine 只代理读取。
type Engine struct {
	client  *protocol.APIClient
	session *session

	settings config.Settings
	kind     LoginKind

	// autoRelogin 控制"业务请求发现登录失效时，是否自动重登一次再重试"。
	// 默认开启；关闭后 authorize 直接返回 false，调用方拿到 ErrUnauthorized，
	// 由 Web 提示用户手动登录。用 atomic 是因为它会在运行中由 WebUI 热切换。
	autoRelogin atomic.Bool
}

// New 构造一个账号的引擎。
//
// settings/sess 来自 Account -> Settings/Session 的显式映射；
// opts 里的回调会在构造期间注入 protocol.Client。
func New(settings config.Settings, sess *config.Session, kind LoginKind, opts Options) (*Engine, error) {
	// 未知值按默认方式处理：宁可走账号密码，也不要误开 CAS。
	switch kind {
	case LoginCAS, LoginWeChat:
	default:
		kind = LoginPassword
	}
	isCas2 := kind == LoginCAS || kind == LoginWeChat
	wx := kind == LoginWeChat

	var clientOpts []protocol.Option
	if opts.Transports != nil {
		clientOpts = append(clientOpts, protocol.WithTransportPool(opts.Transports))
	}
	cli, err := protocol.NewAPIClient(settings, sess, isCas2, wx, "", clientOpts...)
	if err != nil {
		return nil, err
	}
	if opts.Ask != nil {
		cli.InputHandler = opts.Ask
	}
	cli.Kaptcha = opts.Kaptcha
	if opts.OnStage != nil {
		cli.SetLoginObserver(opts.OnStage)
	}
	if opts.OnInitStage != nil {
		cli.SetInitObserver(opts.OnInitStage)
	}
	if opts.OnRequest != nil {
		cli.SetProtocolHook(opts.OnRequest)
	}
	if opts.OnQRCode != nil {
		cli.SetQRHandler(opts.OnQRCode)
	}

	eng := &Engine{
		client:   cli,
		session:  newSession(cli),
		settings: settings,
		kind:     kind,
	}
	// 默认开启：保持"发现掉线就重登一次"的既有行为；需要关闭的账号
	// 在 WebUI 里显式关掉（见 Account.autoReloginEnabled）。
	eng.autoRelogin.Store(true)
	return eng, nil
}

// LoginKind 返回构造时选定的登录方式。
func (e *Engine) LoginKind() LoginKind {
	return e.kind
}

// Settings 返回构造时的配置快照（值拷贝 + Routes 深拷贝，调用方改不到 Engine 内部）。
func (e *Engine) Settings() config.Settings {
	return e.settings.Clone()
}

// Session 返回持久化会话产物（JWT / route / 修正过的 baseURL）。
// 登录成功、初始化前后由上层取快照写回账号记录。
func (e *Engine) Session() config.Session {
	return e.client.Session()
}

// ---------- 登录态 ----------

// LoggedIn 返回当前会话是否已登录。
func (e *Engine) LoggedIn() bool {
	return e.client.LoggedIn()
}

// Login 执行一次完整登录，返回是否成功。
// ctx 取消 / 超时会中断登录并返回 false。
func (e *Engine) Login(ctx context.Context) bool {
	return e.client.LoginWithContext(ctx)
}

// Logout 显式登出并清空登录态。
func (e *Engine) Logout() {
	e.client.Logout()
}

// Ping 探测会话是否仍然有效。
func (e *Engine) Ping(ctx context.Context) *protocol.PingResult {
	return e.client.Ping(ctx)
}

// TestSetLoggedIn 仅供测试显式写入登录态。
func (e *Engine) TestSetLoggedIn(v bool) {
	e.client.TestSetLoggedIn(v)
}

// Identity 返回登录证明的身份，身份未知时返回 nil。
// 需要它的只有扫码登录：界面在登录成功之后才知道该把这次会话记到哪个账号上。
func (e *Engine) Identity() *config.Identity {
	return e.client.Identity()
}

// ---------- 初始化与页签 ----------

// Initialize 拉取选课参数（公开参数 + 课程列表参数）。
func (e *Engine) Initialize(ctx context.Context) error {
	return e.session.Initialize(ctx)
}

// Inited 返回是否已经初始化。
func (e *Engine) Inited() bool {
	return e.session.Inited()
}

// ModeName 返回当前页签名。
func (e *Engine) ModeName() string {
	return e.session.ModeName()
}

// Tabs 返回所有可选页签。
func (e *Engine) Tabs() []protocol.TabInfo {
	return e.session.Tabs()
}

// Window 返回当前选课时间窗（未初始化时为零值）。
func (e *Engine) Window() protocol.WindowInfo {
	return e.session.Window()
}

// SelectTab 切换页签并重新拉取该页签的参数。
// 和课程查询一样：会话失效时按开关重登一次再重试一次。
func (e *Engine) SelectTab(ctx context.Context, idx int) error {
	_, err := retryOnUnauthorized(ctx, e, func() (struct{}, error) {
		return struct{}{}, e.session.SelectTab(ctx, idx)
	})
	return err
}

// Reset 清空初始化状态与课程缓存（登出 / force 刷新时调用）。
func (e *Engine) Reset() {
	e.session.Reset()
}

// ---------- 课程查询 ----------

// Courses 返回内存缓存的课程列表副本。
func (e *Engine) Courses() []protocol.CustomCourseDic {
	return e.session.Courses()
}

// CourseCacheLen 返回缓存条目数。
func (e *Engine) CourseCacheLen() int {
	return e.session.CourseCacheLen()
}

// SetAutoRelogin 热切换"会话失效时是否自动重登一次"。
// 只影响之后发生的 authorize；已经建立的会话与正在跑的任务不受影响。
func (e *Engine) SetAutoRelogin(v bool) {
	if e == nil {
		return
	}
	e.autoRelogin.Store(v)
}

// AutoRelogin 返回当前自动重登开关。
func (e *Engine) AutoRelogin() bool {
	return e != nil && e.autoRelogin.Load()
}

// authorize 确保会话可用：已登录直接放行；允许自动重登时尝试重登一次。
// 重登策略属于引擎（上层），protocol 只负责把"登录已失效"报回来。
func (e *Engine) authorize(ctx context.Context) bool {
	if e.client.LoggedIn() {
		return true
	}
	// 开关关闭：把"未登录"原样交回调用方，由 Web 提示手动登录，
	// 不在业务请求里偷偷跑一次可能弹验证码的登录。
	if !e.AutoRelogin() {
		return false
	}
	// 微信扫码必须由用户显式发起：业务请求失败时静默弹一张二维码，
	// 既没人扫，也会把正在展示的登录界面顶掉。这里只把未登录报回去。
	if e.kind == LoginWeChat {
		return false
	}
	return e.client.LoginWithContext(ctx)
}

// retryOnUnauthorized 执行 fn；遇到 protocol.ErrUnauthorized 时重登一次再重试一次。
// 只重试一次：宁可把错误交给上层，也不在会话彻底失效时无限重登。
func retryOnUnauthorized[T any](ctx context.Context, e *Engine, fn func() (T, error)) (T, error) {
	v, err := fn()
	if !errors.Is(err, protocol.ErrUnauthorized) {
		return v, err
	}
	if !e.authorize(ctx) {
		var zero T
		return zero, protocol.ErrUnauthorized
	}
	return fn()
}

// call 执行一次协议请求，并就地处理两类"重试一次就能好"的会话问题：
//
//   - 登录失效：重登一次再重试（见 retryOnUnauthorized）；
//   - 选课参数过期（正方叫"加密串错误，请刷新网页重试"）：重抓首页参数再重试一次。
//
// 参数过期信号由 protocol 记在 client 上（见 protocol/paramRefresh.go）：engine
// 每个请求拿的是 cfg 拷贝，protocol 写进 cfg 的 needInit 传不回来，只能走这条
// 通道。只重试一次——重抓参数后还失败，说明问题不在参数，把结果原样交回上层。
func call[T any](ctx context.Context, e *Engine, fn func() (T, error)) (T, error) {
	v, err := retryOnUnauthorized(ctx, e, fn)
	if !e.client.TakeParamRefresh() {
		return v, err
	}
	// 这次失败不是参数过期引起的（取消 / 网络 / 登录失效）：标记已经消费掉，
	// 下一次请求会重新发现并触发重抓。
	if err != nil || ctx.Err() != nil {
		return v, err
	}
	if rerr := e.session.Renew(ctx); rerr != nil {
		return v, err
	}
	return retryOnUnauthorized(ctx, e, fn)
}

// GetCourseList 拉取当前页签下的课程列表。
func (e *Engine) GetCourseList(ctx context.Context) ([]protocol.CourseListDic, error) {
	return call(ctx, e, func() ([]protocol.CourseListDic, error) {
		return e.session.GetCourseList(ctx)
	})
}

// GetCourseDetail 拉取某门课程的教学班详情。
func (e *Engine) GetCourseDetail(ctx context.Context, kchId string) ([]protocol.CourseDetail, error) {
	return call(ctx, e, func() ([]protocol.CourseDetail, error) {
		return e.session.GetCourseDetail(ctx, kchId)
	})
}

// CacheCourseList 合并课程列表到内存缓存。
func (e *Engine) CacheCourseList(list []protocol.CourseListDic) {
	e.session.CacheCourseList(list)
}

// CacheCourseDetail 合并教学班详情到内存缓存。
func (e *Engine) CacheCourseDetail(list []protocol.CourseDetail) {
	e.session.CacheCourseDetail(list)
}

// ClearCourseCache 清空课程缓存。
func (e *Engine) ClearCourseCache() {
	e.session.ClearCourseCache()
}

// ---------- 选课 / 退课 ----------

// Choose 执行一次选课。
//
// 选课接口没有 needInit 那样的隐式通道，参数过期只能从返回文案里看出来
// （"加密串错误…刷新网页重试"）。命中就重抓一次参数再重试：抢课要跑几小时，
// 参数在半夜被轮换掉时，不能指望用户第二天早上手动重开。
func (e *Engine) Choose(ctx context.Context, co *protocol.CustomCourseDic) (protocol.ChooseCourseResult, error) {
	r, err := call(ctx, e, func() (protocol.ChooseCourseResult, error) {
		return e.session.Choose(ctx, co)
	})
	if err != nil || !protocol.NeedsParamRefresh(r.Msg) {
		return r, err
	}
	if rerr := e.session.Renew(ctx); rerr != nil {
		return r, nil
	}
	return call(ctx, e, func() (protocol.ChooseCourseResult, error) {
		return e.session.Choose(ctx, co)
	})
}

// Quit 执行一次退课。
func (e *Engine) Quit(ctx context.Context, jxbId, kchId string) (bool, string, error) {
	type quitResult struct {
		ok  bool
		msg string
	}
	r, err := retryOnUnauthorized(ctx, e, func() (quitResult, error) {
		ok, msg, err := e.session.Quit(ctx, jxbId, kchId)
		return quitResult{ok: ok, msg: msg}, err
	})
	if err != nil {
		return false, "", err
	}
	return r.ok, r.msg, nil
}

// Selected 返回已选课程列表。
func (e *Engine) Selected(ctx context.Context, xkxnm, xkxqm string) ([]protocol.ChosenDic, error) {
	return retryOnUnauthorized(ctx, e, func() ([]protocol.ChosenDic, error) {
		return e.session.Selected(ctx, xkxnm, xkxqm)
	})
}

// ---------- 成绩 ----------

// WebGetScore 查询指定学期的成绩。
// 成绩接口原先直接调 protocol，不经过 engine 的重试；这里补上同样的
// "会话失效按开关重登一次再重试一次"，与课程/已选课程保持一致。
func (e *Engine) WebGetScore(ctx context.Context, xnm, xqm string) ([]protocol.Score, error) {
	return retryOnUnauthorized(ctx, e, func() ([]protocol.Score, error) {
		return e.client.WebGetScore(ctx, xnm, xqm)
	})
}

// ---------- 其他工具 ----------

// UserInfo 查询当前账号的个人信息。
// 登录态失效时按 AutoRelogin 开关决定是否重登一次；关闭时返回
// protocol.ErrUnauthorized，由 Web 层提示用户重新登录。
func (e *Engine) UserInfo(ctx context.Context) (protocol.UserInfo, error) {
	return retryOnUnauthorized(ctx, e, func() (protocol.UserInfo, error) {
		return e.client.WebGetUserInfo(ctx)
	})
}

// UserPhoto 下载当前账号的学生大头照，返回图片字节与 MIME 类型。
// 重登策略与 UserInfo 相同。
func (e *Engine) UserPhoto(ctx context.Context) ([]byte, string, error) {
	type photo struct {
		body        []byte
		contentType string
	}
	r, err := retryOnUnauthorized(ctx, e, func() (photo, error) {
		body, contentType, err := e.client.WebGetUserPhoto(ctx)
		return photo{body: body, contentType: contentType}, err
	})
	if err != nil {
		return nil, "", err
	}
	return r.body, r.contentType, nil
}

// Cookies 返回当前会话 Cookie 快照和拼好的请求头，不会发起网络请求。
func (e *Engine) Cookies() ([]protocol.CookieSnapshot, string) {
	return e.client.Cookies()
}
