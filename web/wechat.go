package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"school_sdk/engine"
	"school_sdk/protocol/config"
)

// guestWechatID 是"还没有账号"的扫码会话在任务表里的 id。
//
// 它只是一次登录的临时名字，不是账号：扫码成功、身份确定之后，运行态会被
// 搬到真正的账号 id 下（见 AccountStore.AdoptRuntime）。
const guestWechatID = "@wechat"

// wechatFlow 是一次从零开始的微信扫码登录。
//
// 它不挂在任何账号上，因为扫码之前根本没有账号可选——这正是它存在的理由：
// 二维码、登录阶段、任务状态都属于"这次登录"，而"这次登录是谁"要等 ticket
// 回来才知道。登录结束后它要么绑定到已有账号，要么新建一个。
type wechatFlow struct {
	rt   *accountRuntime
	done chan struct{}

	mu      sync.Mutex
	closed  bool
	acc     *Account // 绑定到的账号（完成前为 nil）
	created bool     // 是否新建了账号
	err     error    // 失败原因
}

type wechatFlowSnapshot struct {
	done    bool
	account *Account
	created bool
	err     string
}

func (f *wechatFlow) finished() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *wechatFlow) finish(acc *Account, created bool, err error) {
	f.mu.Lock()
	f.acc, f.created, f.err, f.closed = acc, created, err, true
	f.mu.Unlock()
	close(f.done)
}

func (f *wechatFlow) snapshot() wechatFlowSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := wechatFlowSnapshot{done: f.closed, account: f.acc, created: f.created}
	if f.err != nil {
		s.err = f.err.Error()
	}
	return s
}

// wechatLogin 发起一次无账号的扫码登录：只需要点一下，不需要任何输入。
func (s *Server) wechatLogin(c *gin.Context) {
	s.wechatMu.Lock()
	if old := s.wechat; old != nil && !old.finished() {
		s.wechatMu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "已有扫码登录在进行中"})
		return
	}
	rt, err := s.newGuestRuntime()
	if err != nil {
		s.wechatMu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "构造扫码客户端失败: " + err.Error()})
		return
	}
	f := &wechatFlow{rt: rt, done: make(chan struct{})}
	s.wechat = f
	s.wechatMu.Unlock()

	go s.runWechatFlow(f)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// newGuestRuntime 造一个"还没有账号"的运行态。
//
// 用默认配置建客户端：扫码的时候还不知道是哪所学校、哪个人，等 ticket 回来
// 才知道；而微信扫码走的门户地址是协议里写死的，配置里的地址此时不影响扫码。
func (s *Server) newGuestRuntime() (*accountRuntime, error) {
	rt := &accountRuntime{
		id:          guestWechatID,
		tasks:       s.tasks,
		taskTimeout: fallbackLoginTimeout,
	}
	eng, err := engine.New(config.Default(), config.NewSession(), engine.LoginWeChat, engine.Options{
		Ask:         rt.requestInput,
		OnStage:     rt.loginStage,
		OnInitStage: rt.initStage,
		OnRequest:   rt.requestStage,
		OnQRCode:    rt.setQRCode,
		// 扫码期间还没有账号，但走的是同一批门户地址：复用账号连接池。
		Transports: s.store.transports,
	})
	if err != nil {
		return nil, err
	}
	rt.engine = eng
	return rt, nil
}

// runWechatFlow 跑完一次扫码，并把它绑到人身上。
func (s *Server) runWechatFlow(f *wechatFlow) {
	ctx, cancel := context.WithTimeout(context.Background(), f.rt.longTaskTimeout())
	defer cancel()

	// persist 传 nil：这时候还不知道该把会话写进哪个账号。
	ok, err := s.runLogin(guestWechatID, ctx, f.rt, true, nil)
	if !ok {
		// ctx 的错误名对用户没有意义，翻成人话：取消和超时是两种不同的预期。
		switch {
		case err == nil:
			err = errLoginFailed
		case errors.Is(err, context.Canceled):
			err = errors.New("已取消")
		case errors.Is(err, context.DeadlineExceeded):
			err = fmt.Errorf("扫码超时（%s）", loginTimeoutText(f.rt.longTaskTimeout()))
		}
		f.finish(nil, false, err)
		return
	}

	// 扫码成功，身份此刻才出现：ticket 里写着是谁。这是整个流程的关键一步——
	// 登录先于账号发生，账号是这次登录的产物。
	id := f.rt.engine.Identity()
	if id.Empty() {
		f.finish(nil, false, errors.New("扫码成功，但 ticket 里没有身份标识"))
		return
	}
	acc, created, err := s.store.UpsertWechat(*id, f.rt.engine.Session())
	if err != nil {
		f.finish(nil, false, fmt.Errorf("登录成功但绑定账号失败: %w", err))
		return
	}
	// 运行态直接过继给账号：重建 runtime 会得到一个"有票但没登录"的引擎，
	// 而微信登录不允许静默重登，扫码的成功就白费了。
	s.store.AdoptRuntime(acc.ID, f.rt)
	f.finish(acc, created, nil)
}

// wechatStatus 是扫码登录的轮询入口：二维码、登录阶段、绑定结果都从这里出。
func (s *Server) wechatStatus(c *gin.Context) {
	s.wechatMu.Lock()
	f := s.wechat
	s.wechatMu.Unlock()

	if f == nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	snap := f.snapshot()
	out := gin.H{
		"active":  !snap.done,
		"qr":      f.rt.qrView(),
		"task":    s.tasks.snapshot(guestWechatID),
		"pending": pendingInputViewOf(f.rt.getPendingInput()),
	}
	if snap.account != nil {
		out["accountId"] = snap.account.ID
		out["created"] = snap.created
		out["account"] = snap.account
	}
	if snap.err != "" {
		out["error"] = snap.err
	}
	c.JSON(http.StatusOK, out)
}

// wechatQR 返回当前二维码图片（与账号登录共用同一套 rev 机制）。
func (s *Server) wechatQR(c *gin.Context) {
	s.wechatMu.Lock()
	f := s.wechat
	s.wechatMu.Unlock()
	if f == nil {
		c.Status(http.StatusNotFound)
		return
	}
	var rev int64
	if raw, ok := c.GetQuery("rev"); ok {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "rev 必须是正整数"})
			return
		}
		rev = parsed
	}
	img, ok := f.rt.qrImage(rev)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Data(http.StatusOK, "image/png", img)
}

// wechatCancel 取消整次扫码登录：任务、二维码、登录 ctx 一起回收。
func (s *Server) wechatCancel(c *gin.Context) {
	s.wechatMu.Lock()
	f := s.wechat
	s.wechatMu.Unlock()
	if f == nil || f.finished() {
		c.JSON(http.StatusOK, gin.H{"status": "noop"})
		return
	}
	// 先记录"用户主动取消"，再真正取消 ctx：任务快照里的收尾原因才准确
	s.tasks.takeCancel(guestWechatID, taskKindLogin, true)
	f.rt.clearQRCode()
	f.rt.loginMu.Lock()
	cancel := f.rt.loginCancel
	f.rt.loginMu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
