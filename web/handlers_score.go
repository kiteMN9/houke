package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// scoreSuggest 返回按当前时间推算的建议学年 / 学期。
func (s *Server) scoreSuggest(c *gin.Context) {
	year, term := protocol.GetSuggestYearTerm()
	c.JSON(http.StatusOK, gin.H{
		"xnm": year,
		"xqm": protocol.TERM[term],
	})
}

// getScore 查询指定学年学期的考试成绩。
func (s *Server) getScore(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	// 未登录时直接失败：WebGetScore 内部会触发一次阻塞登录（最长到账号的登录
	// 超时），这次登录不在任务面板里、也无法取消，前端按钮会一直转圈。
	if !rt.engine.LoggedIn() {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "尚未登录，请先登录"})
		return
	}
	xnm := c.Query("xnm")
	xqm := c.Query("xqm")
	if xnm == "" || xqm == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 xnm / xqm 参数"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	list, err := rt.engine.WebGetScore(ctx, xnm, xqm)
	if err != nil {
		if errors.Is(err, protocol.ErrNoPermission) {
			c.JSON(http.StatusForbidden, gin.H{"error": "该账号无此功能权限", "code": "no_permission"})
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "查询超时"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []protocol.Score{}
	}
	c.JSON(http.StatusOK, list)
}

// shouldRelogin 判断一次 ping 结果是否值得自动重登：
//   - 必须是服务器真的回了响应（Status != 0，网络错误/超时不算掉线）；
//   - 会话已判失效，且是"明确掉线"的信号：302 到登录页，或 200 但页面里
//     已经没有当前账号。5xx/404 之类只是服务端异常，不拿它当掉线依据；
//   - 该账号已经有任务在跑（登录/初始化）时不重复触发。
func shouldRelogin(res *protocol.PingResult, taskRunning bool) bool {
	if res == nil || taskRunning || res.LoggedIn || res.Status == 0 {
		return false
	}
	return (res.Status >= 300 && res.Status < 400) || res.Status == http.StatusOK
}

// ping 探测当前账号会话是否仍然有效，返回延迟、HTTP 状态、是否登录。
//
// 探测到会话失效时会自动发起一次"可见的"重新登录：走任务面板的事件流，
// 可以取消；ping 本身立即返回（relogin=true），不等登录结束。
// 登录态仍由 client 自己维护，web 不额外改状态。
func (s *Server) ping(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	res := rt.engine.Ping(ctx)
	relogin := false
	hint := ""
	if shouldRelogin(res, s.tasks.running(c.Param("id"), "")) {
		if s.startBackgroundLogin(c.Param("id"), rt) {
			relogin = true
		} else {
			hint = "该账号用微信扫码登录，会话失效时需要手动点「微信扫码登录」"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":        res.OK,
		"loggedIn":  res.LoggedIn,
		"status":    res.Status,
		"latencyMs": res.LatencyMs,
		"message":   res.Message,
		"relogin":   relogin,
		"hint":      hint,
	})
}
