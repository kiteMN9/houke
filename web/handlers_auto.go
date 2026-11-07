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

// autoWants 把请求体里的愿望清单清洗成 engine.Plan 用的形式。
//
// 与保存路径（applyWish / store.SetWish）用同一套 cleanWishItems：去首尾空格、
// 丢空行、去重。不清洗的话，wants 里混进一个空串就会让类型 / 教师条件失效
// （Contains 的第二个参数为空串时恒为真），"只要艺术类"会变成"什么课都要"。
func autoWants(req autoChooseReq) protocol.WishList {
	return protocol.WishList{
		WantClassList:   cleanWishItems(req.WantClasses),
		WantTeacherList: cleanWishItems(req.WantTeachers),
		WantTypeList:    cleanWishItems(req.WantTypes),
	}
}

// autoChoose 启动自动选课任务。
//
// 这个接口只负责校验和启动：参数、登录态、初始化、愿望清单、同账号任务冲突
// 都在返回前处理；真正的 RunAuto 放到后台 goroutine，接口立即返回 202。
// 运行进度/结果通过 /status 和 /events 推送，不再把 24 小时挂在 HTTP 响应上。
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

	// 请求体里的清单按保存路径的同一套规则清洗，再决定是否回落到账号里保存的清单。
	wants := autoWants(req)
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

	// 与登录/初始化一致：任务独立于 HTTP 请求生命周期，靠 /auto-choose/cancel 取消。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// 任务与取消函数原子登记：/status 一旦显示"自动选课中"，
	// /auto-choose/cancel 就一定真的能取消。
	task, ok := s.tasks.startCancelable(id, taskKindAuto, cancel)
	if !ok {
		cancel()
		writeAPIError(c, http.StatusConflict, codeBusy, "自动选课已在进行中")
		return
	}

	// 先把启动事件写进任务，再返回 202，前端首帧就能看到"计划/组装候选"。
	s.tasks.emit(id, taskEvent{Kind: "auto.plan", Stage: "auto.plan", Text: autoPlanText(startAt, timeout)})
	s.tasks.emit(id, taskEvent{Kind: "auto.collect", Stage: "auto.collect", Text: "正在组装候选课程"})

	// 关键：defer cancel() 必须跟着 goroutine 走。如果留在 handler 里，
	// 接口一返回就会把刚启动的任务取消掉。
	go s.runAutoChoose(rt, id, task.id, ctx, cancel, plan, wants, timeout, startAt)

	c.JSON(http.StatusAccepted, gin.H{
		"status": "running",
		"task":   s.tasks.snapshot(id),
	})
}

// runAutoChoose 是自动选课的后台执行体。所有运行期结果都写进任务快照，
// 不再通过 HTTP 响应表达。
func (s *Server) runAutoChoose(
	rt *accountRuntime,
	id string,
	taskID uint64,
	ctx context.Context,
	cancel context.CancelFunc,
	plan engine.Plan,
	wants protocol.WishList,
	timeout time.Duration,
	startAt time.Time,
) {
	defer cancel()

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
		outcome := autoOutcome(ctx, err, timeout)
		s.tasks.finishWithResult(id, taskKindAuto, taskID, outcome.taskStatus, outcome.text, outcome.detail, outcome.result)
		s.notifyAutoChooseEnd(id, nil, err, ctx, startAt)
		return
	}

	s.tasks.finishWithResult(id, taskKindAuto, taskID, taskStatusDone,
		fmt.Sprintf("已选上《%s》", chosen.Kcmc), chosen.Jxbmc,
		&taskResult{
			Status: "ok",
			Flag:   result.Flag,
			Msg:    result.Msg,
			Course: &chosen,
		})
	s.notifyAutoChooseEnd(id, &chosen, nil, ctx, startAt)
}

// autoOutcome 把 RunAuto 的结束原因翻译成任务状态 + 结构化结果。
//
// 这里刻意不再返回 HTTP 状态码：异步化之后，运行期错误属于任务结果，
// 不属于"启动请求"的响应。参数/权限错误仍在 handler 里同步返回 4xx。
type autoTaskOutcome struct {
	taskStatus string
	text       string
	detail     string
	result     *taskResult
}

func autoOutcome(ctx context.Context, err error, timeout time.Duration) autoTaskOutcome {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return autoTaskOutcome{
			taskStatus: taskStatusCancelled,
			text:       "自动选课已取消",
			result:     &taskResult{Status: taskStatusCancelled},
		}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		text := "自动选课超时（" + autoTimeoutText(timeout) + "）"
		return autoTaskOutcome{
			taskStatus: taskStatusTimeout,
			text:       text,
			result:     &taskResult{Status: taskStatusTimeout, Error: text},
		}
	case errors.Is(err, engine.ErrSessionChanged):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "自动选课中断：会话已变更",
			detail:     err.Error(),
			result:     &taskResult{Status: "session_changed", Error: err.Error()},
		}
	case errors.Is(err, engine.ErrSessionReset):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "自动选课中断：会话已重置",
			detail:     err.Error(),
			result:     &taskResult{Status: "session_reset", Error: err.Error()},
		}
	case errors.Is(err, engine.ErrNotChosen):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "重试用尽仍未选上",
			detail:     err.Error(),
			result:     &taskResult{Status: "not_chosen", Error: err.Error()},
		}
	case errors.Is(err, engine.ErrNoWantedCourse):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "愿望清单没有命中可选的课程",
			detail:     err.Error(),
			result:     &taskResult{Status: "no_wanted", Error: err.Error()},
		}
	case errors.Is(err, engine.ErrNoCandidates):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "没有可用的候选课程",
			detail:     err.Error(),
			result:     &taskResult{Status: "no_candidates", Error: err.Error()},
		}
	case errors.Is(err, engine.ErrNotInitialized):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "自动选课中断：会话尚未初始化",
			result:     &taskResult{Status: "not_initialized"},
		}
	case errors.Is(err, protocol.ErrUnauthorized):
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "自动选课中断：登录已失效",
			result:     &taskResult{Status: "unauthorized"},
		}
	default:
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		return autoTaskOutcome{
			taskStatus: taskStatusFailed,
			text:       "自动选课失败",
			detail:     detail,
			result:     &taskResult{Status: "failed", Error: detail},
		}
	}
}

// cancelAutoChoose 取消正在进行的自动选课。
func (s *Server) cancelAutoChoose(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.store.runtime(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 取消函数登记在任务上而不是运行态里：运行态被重建（例如自动选课期间
	// 改了账号配置）也不会丢；takeCancel 会先 force 标记再调用 ctx cancel，
	// 避免前端连点取消时看到任务仍然是 running。
	if s.tasks.takeCancel(id, taskKindAuto, true) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "noop"})
}
