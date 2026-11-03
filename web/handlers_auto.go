package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// autoChooseReq 是自动选课的请求体。
//
// 愿望清单只允许"本次指定"或"沿用账号已保存的清单"，不允许两种都不给：
// 自动选课每次提交都在盲选，没有愿望就没有意义。
type autoChooseReq struct {
	WantClasses  []string `json:"wantClasses"`
	WantTeachers []string `json:"wantTeachers"`
	WantTypes    []string `json:"wantTypes"`

	// IntervalSeconds 是两轮之间的间隔（秒）；<=0 用 engine 的默认值。
	IntervalSeconds float64 `json:"intervalSeconds"`
	// RefreshSeconds 是重新拉课程列表的间隔（秒）；<=0 用 engine 的默认值。
	// 运行中靠它把新放出来的名额、变化的已选人数纳入候选，只对多轮运行有意义。
	RefreshSeconds float64 `json:"refreshSeconds"`
	// MaxRounds 是最大轮次；0 表示一直试到超时/取消。
	MaxRounds int `json:"maxRounds"`
	// Once 只试一轮，用于"确认有名额，点一下"的场景。
	Once bool `json:"once"`

	// DurationSeconds 是本次自动选课的最长运行时长（秒）；<=0 用默认值。
	// 自动选课是超长任务，允许几小时到几天，不再借用登录的 10 分钟预算。
	DurationSeconds float64 `json:"durationSeconds"`
	// EndAt 是本次自动选课的结束时间（Unix 毫秒，前端 Date.getTime() 直传）。
	// >0 时优先于 DurationSeconds，方便用户直接选"跑到几点"。
	EndAt int64 `json:"endAt"`
}

// defaultAutoTimeout 是请求没给运行时长/结束时间时的默认预算。
//
// 蹲名额经常要几小时甚至跨天，不能沿用登录/初始化那套 10 分钟默认值；
// 真要不设上限可以给一个很远的结束时间，由用户显式决定。
const defaultAutoTimeout = 24 * time.Hour

// autoTimeout 计算本次自动选课的运行预算（结束时间优先于时长）。
// 两个字段都没给时返回 defaultAutoTimeout。
func autoTimeout(req autoChooseReq, now time.Time) (time.Duration, error) {
	switch {
	case req.DurationSeconds < 0:
		return 0, errors.New("运行时长不能为负数")
	case req.EndAt < 0:
		return 0, errors.New("结束时间不能为负数")
	case req.EndAt > 0:
		d := time.UnixMilli(req.EndAt).Sub(now)
		if d <= 0 {
			return 0, errors.New("结束时间必须晚于当前时间")
		}
		return d, nil
	case req.DurationSeconds > 0:
		d := time.Duration(req.DurationSeconds * float64(time.Second))
		if d <= 0 {
			return 0, errors.New("运行时长太短")
		}
		return d, nil
	default:
		return defaultAutoTimeout, nil
	}
}

// autoTimeoutText 描述自动选课的运行预算。
//
// 预算可能跨小时、跨天，直接复用登录的 loginTimeoutText 会出现
// 「1503 分钟」这种难读的数字，所以这里按天/小时/分钟展开。
func autoTimeoutText(d time.Duration) string {
	if d < time.Minute {
		return loginTimeoutText(d)
	}
	d = d.Round(time.Minute)
	totalHours := int(d / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	days := totalHours / 24
	hours := totalHours % 24

	if days == 0 {
		switch {
		case hours == 0:
			return fmt.Sprintf("%d 分钟", minutes)
		case minutes == 0:
			return fmt.Sprintf("%d 小时", hours)
		default:
			return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
		}
	}
	switch {
	case hours == 0 && minutes == 0:
		return fmt.Sprintf("%d 天", days)
	case minutes == 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	default:
		return fmt.Sprintf("%d 天 %d 小时 %d 分钟", days, hours, minutes)
	}
}

// autoPlanText 把本次运行预算写进任务时间线，让用户知道什么时候会自动停。
func autoPlanText(now time.Time, timeout time.Duration) string {
	return fmt.Sprintf("本次最长运行 %s（至 %s）",
		autoTimeoutText(timeout), now.Add(timeout).Format("2006-01-02 15:04"))
}

// autoChoose 执行自动选课。
//
// 与 /choose 的区别是它跑的是 engine.Plan：候选（列表 + 详情里的 Do_jxb_id）
// 由 engine.CandidateSource 提供，运行中按 refreshSeconds 周期重拉，再按愿望
// 优先级反复尝试。整个过程写成任务事件，前端通过 /events 看进度；这个请求本身
// 会一直挂到选上、取消、超时或轮次用尽（与 /init 的模型一致）。
func (s *Server) autoChoose(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	var req autoChooseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 先校验运行预算再登记任务：参数不合法时不应该留下一条 running 的任务。
	startAt := time.Now()
	timeout, err := autoTimeout(req, startAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !rt.engine.LoggedIn() {
		writeUnauthorized(c, "尚未登录，请先登录")
		return
	}
	if !rt.engine.Inited() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先调用 /init 完成初始化"})
		return
	}

	wants := protocol.WishList{
		WantClassList:   req.WantClasses,
		WantTeacherList: req.WantTeachers,
		WantTypeList:    req.WantTypes,
	}
	if wants.Empty() {
		wants = rt.engine.Wish()
	}
	if wants.Empty() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少配置一项愿望（课程名 / 课程类型 / 教师）"})
		return
	}

	plan := engine.Plan{
		Wants:     wants,
		Interval:  time.Duration(req.IntervalSeconds * float64(time.Second)),
		Refresh:   time.Duration(req.RefreshSeconds * float64(time.Second)),
		MaxRounds: req.MaxRounds,
		Once:      req.Once,
	}

	// 与登录/初始化一致：独立于 HTTP 请求生命周期，靠 /auto-choose/cancel 取消；
	// 预算由本次请求的时长/结束时间决定（默认 24 小时，不再固定 10 分钟）。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 任务与取消函数原子登记：/status 一旦显示"自动选课中"，
	// /auto-choose/cancel 就一定真的能取消。
	if _, ok := s.tasks.startCancelable(id, taskKindAuto, cancel); !ok {
		writeAPIError(c, http.StatusConflict, codeBusy, "自动选课已在进行中")
		return
	}
	rt.autoMu.Lock()
	rt.autoCancel = cancel
	rt.autoMu.Unlock()
	defer func() {
		rt.autoMu.Lock()
		rt.autoCancel = nil
		rt.autoMu.Unlock()
	}()

	s.tasks.emit(id, taskEvent{Kind: "auto.plan", Stage: "auto.plan", Text: autoPlanText(startAt, timeout)})
	s.tasks.emit(id, taskEvent{Kind: "auto.collect", Stage: "auto.collect", Text: "正在组装候选课程"})

	// 候选交给 engine 的 Source：RunAuto 开跑前取一次，之后按 plan.Refresh 周期
	// 重拉课程列表，已选人数和新开的教学班都能在运行中生效。
	var chosen protocol.CustomCourseDic
	result, err := rt.engine.RunAuto(ctx, plan, rt.engine.CandidateSource(wants), func(ev engine.Event) {
		s.tasks.emit(id, taskEvent{
			Kind:   "auto." + ev.Kind,
			Stage:  "auto." + ev.Kind,
			Text:   ev.Message,
			Detail: ev.Course.Kcmc,
		})
		if ev.Kind == engine.EventSuccess {
			chosen = ev.Course
		}
	})
	if err != nil {
		s.finishAutoTask(id, ctx, err, timeout)
		s.notifyAutoChooseEnd(id, nil, err, ctx, startAt)
		s.autoError(c, ctx, err)
		return
	}

	s.tasks.finish(id, taskKindAuto, taskStatusDone,
		fmt.Sprintf("已选上《%s》", chosen.Kcmc), chosen.Jxbmc)
	s.notifyAutoChooseEnd(id, &chosen, nil, ctx, startAt)
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"flag":   result.Flag,
		"msg":    result.Msg,
		"course": chosen,
	})
}

// autoError 把自动选课的错误翻译成对前端有意义的状态码。
//
// "没选上"不是服务端错误：轮次用尽、愿望没命中都返回 200 + status，
// 让前端按"业务失败"展示；只有取消/超时/登录失效/内部错误才用非 200。
func (s *Server) autoError(c *gin.Context, ctx context.Context, err error) {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		writeAPIError(c, http.StatusGatewayTimeout, codeTimeout, "自动选课超时")
	case errors.Is(err, engine.ErrNotChosen):
		c.JSON(http.StatusOK, gin.H{"status": "not_chosen", "error": err.Error()})
	case errors.Is(err, engine.ErrNoWantedCourse), errors.Is(err, engine.ErrNoCandidates):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, engine.ErrNotInitialized):
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先调用 /init 完成初始化"})
	case errors.Is(err, protocol.ErrUnauthorized):
		writeUnauthorized(c, "登录已失效，请重新登录")
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// finishAutoTask 按结束原因收尾自动选课任务（已被取消标记时保持原状态）。
func (s *Server) finishAutoTask(id string, ctx context.Context, err error, timeout time.Duration) {
	if !s.tasks.running(id, taskKindAuto) {
		return
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		s.tasks.finish(id, taskKindAuto, taskStatusCancelled, "自动选课已取消", "")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.tasks.finish(id, taskKindAuto, taskStatusTimeout,
			"自动选课超时（"+autoTimeoutText(timeout)+"）", "")
	case errors.Is(err, engine.ErrNotChosen):
		s.tasks.finish(id, taskKindAuto, taskStatusFailed, "重试用尽仍未选上", err.Error())
	case errors.Is(err, engine.ErrNoWantedCourse), errors.Is(err, engine.ErrNoCandidates):
		s.tasks.finish(id, taskKindAuto, taskStatusFailed, "没有可用的候选课程", err.Error())
	case errors.Is(err, engine.ErrNotInitialized):
		s.tasks.finish(id, taskKindAuto, taskStatusFailed, "自动选课中断：会话尚未初始化", "")
	case errors.Is(err, protocol.ErrUnauthorized):
		s.tasks.finish(id, taskKindAuto, taskStatusFailed, "自动选课中断：登录已失效", "")
	default:
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		s.tasks.finish(id, taskKindAuto, taskStatusFailed, "自动选课失败", detail)
	}
}

// cancelAutoChoose 取消正在进行的自动选课。
func (s *Server) cancelAutoChoose(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	rt.autoMu.Lock()
	cancel := rt.autoCancel
	rt.autoMu.Unlock()

	if cancel == nil {
		// 运行态可能已被重建（典型场景：自动选课期间改了账号配置，Update 会
		// 丢掉 runtime），此时取消函数仍登记在任务上，直接用任务里的那份，
		// 避免"取消自动选课"变成无效按钮、任务继续跑到预算用尽（默认 24 小时）。
		// 同样先 force 标记为已取消，再真正取消 ctx。
		if s.tasks.takeCancel(id, taskKindAuto, true) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "noop"})
		return
	}
	// RunAuto 会在 ctx.Done 后返回，由 handler 自己收尾；这里先 force 标记，
	// 避免前端连点取消时看到任务仍然是 running。
	s.tasks.takeCancel(id, taskKindAuto, true)
	cancel()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
