package web

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"school_sdk/protocol/config"
)

// loginTimeoutText 是"超时"提示里用的可读时长（如「10 分钟」）。
func loginTimeoutText(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + " 小时"
	case d >= time.Minute && d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + " 分钟"
	case d >= time.Minute:
		return strconv.FormatFloat(d.Minutes(), 'f', 1, 64) + " 分钟"
	default:
		return strconv.Itoa(int(d.Seconds())) + " 秒"
	}
}

func (s *Server) login(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 超时按账号配置（WebUI「登录超时」，默认 10 分钟）：
	// 登录可能包含人工输入验证码/新密码，3 分钟经常不够。
	timeout := rt.longTaskTimeout()

	// 用 Background 作为父 ctx，让登录独立于本次 HTTP 请求的生命周期；
	// 通过 /login/cancel 显式取消，不再依赖连接断开
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := s.doLogin(id, ctx, rt, true); err != nil {
		switch {
		case errors.Is(err, errLoginInProgress):
			writeAPIError(c, http.StatusConflict, codeBusy, err.Error())
		case errors.Is(err, context.Canceled):
			c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
		case errors.Is(err, context.DeadlineExceeded):
			writeAPIError(c, http.StatusGatewayTimeout, codeTimeout,
				"登录超时（"+loginTimeoutText(timeout)+"）")
		default:
			// 登录动作失败（密码错 / 流程失败）也是 401，但不是"会话刚失效"：
			// 用 login_failed 区分，前端不会把输错密码显示成"登录已失效"。
			writeAPIError(c, http.StatusUnauthorized, codeLoginFailed, "登录失败")
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

var (
	errLoginInProgress = errors.New("登录已在进行中")
	errLoginFailed     = errors.New("登录失败")
)

// doLogin 执行一次账号登录（不检查是否已登录），并把会话产物写回账号。
// 调用方负责提供超时/取消的 ctx。返回的错误可能是：
//   - errLoginInProgress: 已有登录进行中
//   - context.Canceled / context.DeadlineExceeded: ctx 被取消/超时
//   - errLoginFailed: 登录失败
//
// 当 ownTask 为 true 时，本次调用自行负责开始/结束任务状态（有事件流）；
// 否则只是被 /init 顺带触发的登录步骤，阶段事件直接写进初始化任务里。
func (s *Server) doLogin(id string, ctx context.Context, rt *accountRuntime, ownTask bool) error {
	ok, err := s.runLogin(id, ctx, rt, ownTask, func(sess config.Session, ok bool) {
		_ = s.store.update(id, func(a *Account) {
			a.applySession(sess)
			if ok {
				a.LastLogin = time.Now()
			}
		})
	})
	if ok {
		return nil
	}
	return err
}

// runLogin 是登录的机制部分：任务状态、串行锁、二维码清理、调用 engine.Login，
// 最后把会话产物交给 persist 落盘。
//
// 账号登录和"还没有账号的扫码"共用这一段，区别只在 persist 写到哪里：
// 账号登录写回账号记录；扫码登录在拿到 ticket 之前没有账号可写（persist 为
// nil），由调用方在返回后用 engine.Identity() 决定这次登录属于谁。
func (s *Server) runLogin(id string, ctx context.Context, rt *accountRuntime, ownTask bool, persist func(sess config.Session, ok bool)) (bool, error) {
	// 顺序很重要，三步都不能换：
	//   1) 先建 ctx —— 纯内存对象，失败路径上直接 cancel() 就收干净，外部不可见；
	//   2) 再抢登录所有权（rt.loginCancel）—— 抢不到立刻返回，任务表一个字节不动；
	//   3) 最后才 start，并用 startCancelable 把取消函数和"任务运行中"登记在同一
	//      把锁里 —— /status 看到的第一个"登录中"就一定是可以取消的。
	//
	// 反过来"先 start、后在状态锁里检查"会漏出两个窗口：
	//   - 任务已可见、却没有真取消函数：此时 /login/cancel 会把任务标成"已取消"
	//     （旧代码用空 func(){} 占位，等于对用户撒谎）而 ctx 还活着，取消按钮
	//     却已经按 running=false 被隐藏；
	//   - 所有权检查失败时，start 出来的新任务没人收尾，变成一条永远 running 的
	//     "幽灵任务"，后续每次登录都被它顶成 409。
	loginCtx, cancel := context.WithCancel(ctx)

	rt.loginMu.Lock()
	if rt.loginCancel != nil {
		rt.loginMu.Unlock()
		cancel()
		return false, errLoginInProgress
	}
	rt.loginCancel = cancel
	rt.loginMu.Unlock()
	// 上一轮（如果有）留下的二维码不能带进新登录
	rt.clearQRCode()

	// 跑的是别的任务（如初始化）时沿用旧行为：本次登录不单独开任务，事件写进
	// 那个任务里。已有登录任务在跑则拒绝，并且不留下任何新任务。
	if ownTask {
		if _, ok := s.tasks.startCancelable(id, taskKindLogin, cancel); !ok && s.tasks.running(id, taskKindLogin) {
			// 撤销刚登记的所有权：任务是别人的，不能把它的取消函数清了
			rt.loginMu.Lock()
			rt.loginCancel = nil
			rt.loginMu.Unlock()
			cancel()
			return false, errLoginInProgress
		}
	}

	if ownTask {
		s.tasks.emit(id, taskEvent{Kind: "login.begin", Stage: "login.begin", Text: "正在准备登录"})
	}

	defer func() {
		rt.clearQRCode()
		rt.loginMu.Lock()
		rt.loginCancel = nil
		rt.loginMu.Unlock()
		cancel()
	}()

	// 注意：登录期间不持有运行态锁，status/logout 仍可访问
	ok := rt.engine.Login(loginCtx)

	// 会话产物（JWT / 身份 / 学到的 route / 修正后的地址）由 engine 在内存里维护，
	// 登录结束（无论成败）统一交给调用方落盘：失败时也可能刚清掉了过期 JWT。
	if persist != nil {
		persist(rt.engine.Session(), ok)
	}

	if !ok {
		err := loginCtx.Err()
		if ownTask {
			s.finishLoginTask(id, err, rt.longTaskTimeout())
		}
		if err != nil {
			return false, err
		}
		return false, errLoginFailed
	}

	// 登录态由 client 自己维护（LoginWithContext 内部已置为 true），web 不重复记录；
	// 这里只负责作废旧会话参数，下一次初始化重新抓。
	rt.engine.Reset()
	if ownTask {
		s.tasks.finish(id, taskKindLogin, taskStatusDone, "登录成功", "")
	}
	return true, nil
}

// startBackgroundLogin 在后台发起一次完整的登录任务（任务面板 + 事件流 +
// 可取消），立即返回。ping 探测到会话失效时用它自动重登。
//
// 用 Background 作为父 ctx：登录不能跟着 ping 的 HTTP 请求一起断；
// 取消仍然走 /login/cancel，与手动登录完全一致。
//
// 微信扫码登录没有"后台"可言：没有弹窗就没人扫码，任务只会一直挂在面板上
// 显示"登录进行中"，直到 10 分钟超时——而用户在网络页面里根本看不到那个
// 二维码。所以这类账号不自动重登，返回 false 交给界面提示手动扫码。
func (s *Server) startBackgroundLogin(id string, rt *accountRuntime) bool {
	if a, ok := s.store.Get(id); ok && a.effectiveLoginMode() == LoginModeWeChat {
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rt.longTaskTimeout())
		defer cancel()
		// 失败/取消/超时都已经由 doLogin 写进任务状态，这里没有额外要做的
		_ = s.doLogin(id, ctx, rt, true)
	}()
	return true
}

// finishLoginTask 按 ctx 的结束原因收尾登录任务。
// 已被 /login/cancel 提前标记为 cancelled 时不再覆盖。
func (s *Server) finishLoginTask(id string, err error, timeout time.Duration) {
	switch {
	case errors.Is(err, context.Canceled):
		if s.tasks.running(id, taskKindLogin) {
			s.tasks.finish(id, taskKindLogin, taskStatusCancelled, "登录已取消", "")
		}
	case errors.Is(err, context.DeadlineExceeded):
		s.tasks.finish(id, taskKindLogin, taskStatusTimeout,
			"登录超时（"+loginTimeoutText(timeout)+"）", "")
	default:
		s.tasks.finish(id, taskKindLogin, taskStatusFailed, "登录失败", "")
	}
}

// cancelLogin 取消正在进行的登录；运行态已重建时回退到任务上登记的取消函数。
func (s *Server) cancelLogin(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	rt.loginMu.Lock()
	cancel := rt.loginCancel
	rt.loginMu.Unlock()

	if cancel == nil {
		// 运行态可能已被重建（典型场景：登录期间改了账号配置），此时取消函数
		// 仍登记在任务上，直接用任务里的那份，避免"取消登录"变成无效按钮。
		// 同样先记录"用户主动取消"，再真正取消 ctx。
		if s.tasks.takeCancel(id, taskKindLogin, true) {
			rt.clearQRCode()
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
			return
		}
		rt.clearQRCode()
		c.JSON(http.StatusOK, gin.H{"status": "noop"})
		return
	}
	// 先记录"用户主动取消"，再真正取消 ctx；这样任务快照里的收尾原因准确
	s.tasks.takeCancel(id, taskKindLogin, true)
	rt.clearQRCode()
	cancel()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) clearCasJWT(c *gin.Context) {
	id := c.Param("id")
	if err := s.store.update(id, func(a *Account) { a.TicketJWT = "" }); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 顺便让 runtime 下次用新配置重建
	s.store.dropRuntime(id)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) logout(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	// 登录态由 client 自己维护：Logout 内部会把它置为 false
	rt.engine.Logout()
	rt.engine.Reset()
	rt.clearQRCode()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// status 返回账号运行态 + 长任务快照。
// 前端只靠这一个接口就能在刷新后恢复界面（含"正在登录 / 正在初始化 / 已等待多久"）。
func (s *Server) status(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 待输入（验证码 / 新密码）信息：让刷新后的页面也能立刻弹出输入框
	pending := pendingInputViewOf(rt.getPendingInput())

	c.JSON(http.StatusOK, gin.H{
		"loggedIn":    rt.engine.LoggedIn(), // 登录态由 client 维护，这里只读
		"initialized": rt.engine.Inited(),
		"modeName":    rt.engine.ModeName(),
		"window":      rt.engine.Window(),
		"task":        s.tasks.snapshot(id),
		"pending":     pending,
		"qr":          rt.qrView(),
	})
}

// loginQR 返回当前微信扫码登录的二维码图片。
// 图片走独立端点、不进 SSE：二维码每 4 分钟才换一张，没必要每帧重发几十 KB。
// rev 不匹配时返回 404，防止前端拿着旧 rev 的响应覆盖新二维码。
func (s *Server) loginQR(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	var rev int64
	if raw, ok := c.GetQuery("rev"); ok {
		rev, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || rev <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "rev 必须是正整数"})
			return
		}
	}
	img, ok := rt.qrImage(rev)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Data(http.StatusOK, "image/png", img)
}

// pendingInputView 是 /status 里内嵌的待输入描述（与 /login/pending 字段保持一致）。
type pendingInputView struct {
	Kind      string `json:"kind"`
	Prompt    string `json:"prompt"`
	AgeMs     int64  `json:"ageMs"`
	ImageB64  string `json:"imageBase64,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// pendingInputViewOf 把待输入请求转成前端结构（验证码图片转 base64）。
// 只在建立连接 / 显式查询时调用，不再随每次轮询重复编码。
func pendingInputViewOf(p *pendingInput) *pendingInputView {
	if p == nil {
		return nil
	}
	return &pendingInputView{
		Kind:      p.kind,
		Prompt:    p.prompt,
		AgeMs:     time.Since(p.createdAt).Milliseconds(),
		ImageB64:  base64.StdEncoding.EncodeToString(p.imageData),
		CreatedAt: p.createdAt.UnixMilli(),
	}
}

// pendingInput 返回当前是否有待用户输入的请求。
// 前端在 POST /login 之后轮询此接口即可（也可以直接看 /status 里的 pending 字段）。
func (s *Server) pendingInput(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	p := rt.getPendingInput()
	if p == nil {
		c.JSON(http.StatusOK, gin.H{"pending": false})
		return
	}
	resp := gin.H{
		"pending": true,
		"kind":    p.kind, // "password" | "captcha"
		"prompt":  p.prompt,
		"ageMs":   time.Since(p.createdAt).Milliseconds(),
	}
	if len(p.imageData) > 0 {
		resp["imageBase64"] = base64.StdEncoding.EncodeToString(p.imageData)
	}
	c.JSON(http.StatusOK, resp)
}

type loginInputReq struct {
	Value string `json:"value"`
}

// submitLoginInput 接收用户输入的验证码 / 新密码。
func (s *Server) submitLoginInput(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	var req loginInputReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 密码是"输入方收到的"，就由输入方持久化：client 只把新值交给运行时用，
	// 不再自己写文件。先取类型，submitInput 会清空待处理请求。
	p := rt.getPendingInput()
	if !rt.submitInput(req.Value) {
		c.JSON(http.StatusConflict, gin.H{"error": "当前没有待处理的输入请求"})
		return
	}
	if p != nil && p.kind == "password" {
		_ = s.store.update(c.Param("id"), func(a *Account) { a.Password = req.Value })
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
