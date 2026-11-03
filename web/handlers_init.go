package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// initCourse 执行选课前置初始化（必要时先登录），并把过程写进任务时间线。
func (s *Server) initCourse(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 超时同样按账号配置（初始化可能先顺带做一次登录，不能比登录预算更短）
	timeout := rt.longTaskTimeout()

	// 用 Background 作为父 ctx，让初始化独立于本次 HTTP 请求的生命周期；
	// 通过 /init/cancel 显式取消，不再依赖连接断开
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 先登记任务，保证客户端立刻能从 /status 看到"初始化中"。
	// 取消函数与任务在同一把锁里登记：看到"初始化中"就一定取消得动。
	if _, ok := s.tasks.startCancelable(id, taskKindInit, cancel); !ok {
		writeAPIError(c, http.StatusConflict, codeBusy, "初始化已在进行中")
		return
	}
	rt.initMu.Lock()
	rt.initCancel = cancel
	rt.initMu.Unlock()

	defer func() {
		rt.initMu.Lock()
		rt.initCancel = nil
		rt.initMu.Unlock()
	}()

	// 初始化前若未登录，先登录（登录态问 client，web 不自己存）。
	// 自动重登关闭时不做这件事：否则用户刚关掉开关，点一次初始化又把登录跑起来了。
	// 未登录 = 没登录过或会话已失效，两种都交给界面提示手动登录。
	// 注意：doLogin 完成后会 rt.engine.Reset()，下面 Initialize 会重新抓取参数，语义正确。
	if !rt.engine.LoggedIn() {
		if !rt.engine.AutoRelogin() {
			s.tasks.emit(id, taskEvent{Kind: "init.login", Stage: "init.login", Text: "尚未登录，请先手动登录"})
			s.tasks.finish(id, taskKindInit, taskStatusFailed, "初始化中断：尚未登录，请先登录", "")
			writeUnauthorized(c, "尚未登录，请先登录")
			return
		}
		s.tasks.emit(id, taskEvent{Kind: "init.login", Stage: "init.login", Text: "尚未登录，先执行登录"})
		// ownTask=false：登录事件直接写进本次初始化任务，前端只需看一条时间线
		if err := s.doLogin(id, ctx, rt, false); err != nil {
			s.failInitLogin(c, id, ctx, err, timeout)
			return
		}
	}

	if c.Query("force") == "1" {
		rt.engine.Reset()
		s.tasks.emit(id, taskEvent{Kind: "init.reset", Stage: "init.reset", Text: "已清空旧参数，重新初始化"})
	}

	s.tasks.emit(id, taskEvent{Kind: "init.begin", Stage: "init.begin", Text: "正在抓取选课参数与页签"})

	// 注意：Initialize 不再持有 session 的读锁跨网络请求，抓参数期间
	// /status、/tabs 等只读接口不会被阻塞；发布结果时才短暂加写锁。
	err = rt.engine.Initialize(ctx)

	// 会话在初始化过程中失效：按账号开关自动重登一次，再重试一次初始化。
	// 走 doLogin 而不是 engine 的静默重登，原因有两个：
	//   - 登录事件要写进本次初始化任务，验证码/二维码仍能被界面接管；
	//   - 登录产物（JWT / 身份）要由 doLogin 落盘，不能只留在内存。
	// 只重试一次：重登后还失效，说明问题不在会话，交给上层报 401。
	if err != nil && errors.Is(err, protocol.ErrUnauthorized) &&
		rt.engine.AutoRelogin() && rt.engine.LoginKind() != engine.LoginWeChat {
		s.tasks.emit(id, taskEvent{
			Kind:  "init.relogin",
			Stage: "init.relogin",
			Text:  "登录已失效，正在自动重新登录（仅重试一次）",
		})
		if lerr := s.doLogin(id, ctx, rt, false); lerr != nil {
			s.failInitLogin(c, id, ctx, lerr, timeout)
			return
		}
		err = rt.engine.Initialize(ctx)
	}

	if err != nil {
		// ctx 被取消时，takeCancel 可能已经标记过 cancelled，finish 会自动幂等跳过
		s.finishInitTask(id, ctx, err, timeout)
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			// 被 /init/cancel 取消
			c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			writeAPIError(c, http.StatusGatewayTimeout, codeTimeout,
				"初始化超时（"+loginTimeoutText(timeout)+"）")
		default:
			// 和课程/成绩共用同一个错误出口：登录失效 → 401 + unauthorized，
			// 初始化期间会话被重置 → 409 + session_reset，不再一律 500。
			writeEngineError(c, err)
		}
		return
	}

	s.tasks.finish(id, taskKindInit, taskStatusDone, "初始化完成", rt.engine.ModeName())

	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"modeName": rt.engine.ModeName(),
	})
}

// failInitLogin 把"初始化过程中顺带做的登录"失败翻译成 HTTP 响应。
// 自动重登与初始化前的预登录共用这一段，避免两处语义漂移。
func (s *Server) failInitLogin(c *gin.Context, id string, ctx context.Context, err error, timeout time.Duration) {
	s.finishInitTask(id, ctx, err, timeout)
	switch {
	case errors.Is(err, errLoginInProgress):
		writeAPIError(c, http.StatusConflict, codeBusy, err.Error())
	case errors.Is(err, context.Canceled):
		c.JSON(http.StatusOK, gin.H{"status": "cancelled", "stage": "login"})
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(c, http.StatusGatewayTimeout, codeTimeout,
			"登录超时（"+loginTimeoutText(timeout)+"）")
	default:
		// 这是"登录动作"失败，不是会话刚失效：
		// 用 login_failed，避免前端按 unauthorized 再提示一次"登录已失效"。
		writeAPIError(c, http.StatusUnauthorized, codeLoginFailed, "登录失败")
	}
}

// finishInitTask 按结束原因收尾初始化任务（已被取消标记时保持原状态）。
func (s *Server) finishInitTask(id string, ctx context.Context, err error, timeout time.Duration) {
	if !s.tasks.running(id, taskKindInit) {
		return
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		s.tasks.finish(id, taskKindInit, taskStatusCancelled, "初始化已取消", "")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.tasks.finish(id, taskKindInit, taskStatusTimeout,
			"初始化超时（"+loginTimeoutText(timeout)+"）", "")
	case errors.Is(err, errLoginInProgress):
		s.tasks.finish(id, taskKindInit, taskStatusFailed, "初始化中断：已有登录在进行中", "")
	case errors.Is(err, protocol.ErrUnauthorized):
		s.tasks.finish(id, taskKindInit, taskStatusFailed, "初始化中断：登录已失效，请重新登录", "")
	case errors.Is(err, engine.ErrSessionReset):
		s.tasks.finish(id, taskKindInit, taskStatusFailed, "初始化失效：会话已被重置", "")
	default:
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		s.tasks.finish(id, taskKindInit, taskStatusFailed, "初始化失败", detail)
	}
}

// cancelInitCourse 取消正在进行的初始化。
func (s *Server) cancelInitCourse(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	rt.initMu.Lock()
	cancel := rt.initCancel
	rt.initMu.Unlock()

	if cancel == nil {
		// 运行态可能已被重建（典型场景：初始化期间改了账号配置，Update 会
		// 丢掉 runtime），此时取消函数仍登记在任务上，直接用任务里的那份，
		// 避免"取消初始化"变成无效按钮、初始化一直跑到超时。
		// 同样先 force 标记为已取消，再真正取消 ctx。
		if s.tasks.takeCancel(id, taskKindInit, true) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "noop"})
		return
	}
	// 取消信号由 Initialize 内部消化，不会回到 handler 返回路径，
	// 因此这里直接 force 标记为已取消。
	s.tasks.takeCancel(id, taskKindInit, true)
	cancel()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
