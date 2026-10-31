package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"school_sdk/engine"

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
		c.JSON(http.StatusConflict, gin.H{"error": "初始化已在进行中"})
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
	// 注意：doLogin 完成后会 rt.engine.Reset()，下面 Initialize 会重新抓取参数，语义正确。
	if !rt.engine.LoggedIn() {
		s.tasks.emit(id, taskEvent{Kind: "init.login", Stage: "init.login", Text: "尚未登录，先执行登录"})
		// ownTask=false：登录事件直接写进本次初始化任务，前端只需看一条时间线
		if err := s.doLogin(id, ctx, rt, false); err != nil {
			s.finishInitTask(id, ctx, err, timeout)
			switch {
			case errors.Is(err, errLoginInProgress):
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			case errors.Is(err, context.Canceled):
				c.JSON(http.StatusOK, gin.H{"status": "cancelled", "stage": "login"})
			case errors.Is(err, context.DeadlineExceeded):
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": "登录超时（" + loginTimeoutText(timeout) + "）"})
			default:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "登录失败"})
			}
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
	if err := rt.engine.Initialize(ctx); err != nil {
		// ctx 被取消时，takeCancel 可能已经标记过 cancelled，finish 会自动幂等跳过
		s.finishInitTask(id, ctx, err, timeout)
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			// 被 /init/cancel 取消
			c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "初始化超时（" + loginTimeoutText(timeout) + "）"})
		case errors.Is(err, engine.ErrSessionReset):
			// 初始化期间登录态被重置（登出 / 重新登录），这次结果已作废
			c.JSON(http.StatusConflict, gin.H{"error": "会话已重置，请重新初始化"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	s.tasks.finish(id, taskKindInit, taskStatusDone, "初始化完成", rt.engine.ModeName())

	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"modeName": rt.engine.ModeName(),
	})
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
