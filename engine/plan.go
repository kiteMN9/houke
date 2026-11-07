package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"school_sdk/protocol"
)

// 自动选课的默认节奏：CLI 的 JL 用 1~2 秒一轮，这里取中间值。
// 太快会把教务系统的选课接口打成 5xx，太慢又会错过放出来的名额。
const defaultInterval = 1500 * time.Millisecond

// 自动选课默认的候选刷新间隔。
//
// 队列里的人数是某一刻的快照；刷新 = 重新拉一遍课程列表（教学班 + 已选人数），
// 把"刚放出来的名额"和"新开的教学班"纳入队列。CLI 的 JL 每 440ms 就拉一次列表，
// 但自动选课的主力动作是提交选课，没必要让查询抢带宽：30 秒足够在前几轮内
// 感知名额释放，又不会让课程列表接口变成主要流量。
const defaultRefresh = 30 * time.Second

// Plan 是一次自动选课的描述。
//
// 它只描述"想要什么、多久试一次、多久刷新一次、最多试几轮"，不含任何会话状态：
// 会话（登录态、Index 参数、课程缓存）仍由 Engine 持有。这样同一个 Plan
// 可以被 Web 的高级选课、TUI 的 JL、以后可能的定时任务复用。
type Plan struct {
	// Wants 是愿望清单（课程名 / 课程类型 / 教师）。
	// 为空时沿用 Session 里已经设置过的清单（见 Engine.SetWish）。
	Wants protocol.WishList

	// Interval 是两轮之间的间隔；<=0 时用 defaultInterval。
	Interval time.Duration

	// Refresh 是重新取一次候选的间隔；<=0 时用 defaultRefresh。
	//
	// 抢课要跑几小时，而课程列表里的人数是会变的（有人退课、教务加容量、
	// 新开一个教学班）。Refresh 决定多久把这些人重新看一遍；提交本身仍按
	// Interval 的节奏走。只对多轮运行有意义：Once / MaxRounds=1 拿到候选
	// 之后就结束了，不会再刷新。
	Refresh time.Duration

	// MaxRounds 是最多尝试几轮；0 表示一直重试到 ctx 结束或选上。
	MaxRounds int

	// Once 表示只尝试一次（等价 MaxRounds=1），给"确定有名额、点一下"的场景用。
	Once bool
}

// 自动选课的失败原因。调用方可以用 errors.Is 区分"没配愿望清单"和"抢不到"。
var (
	// ErrNoCandidates 表示调用方没有给出任何候选课程。
	ErrNoCandidates = errors.New("engine: 没有候选课程")
	// ErrNoWantedCourse 表示候选里没有命中愿望清单、且能提交的课程。
	// 命中了但教学班已满**不算**这个错误：满员的教学班仍是候选（见 candidatesByPriority）。
	ErrNoWantedCourse = errors.New("engine: 愿望清单没有命中可选的课程")
	// ErrNotChosen 表示轮次用尽仍未选上。
	ErrNotChosen = errors.New("engine: 重试用尽仍未选上")
)

// Event 是自动选课过程中的进度事件。
//
// Kind 见下面的事件类型；Message 是给人看的一句话，Course 是当前操作的教学班
// （进度界面用它显示课程名/教师/时间）。刷新类事件没有具体课程，Course 为零值。
type Event struct {
	Round   int
	Kind    string
	Message string
	Course  protocol.CustomCourseDic
}

// 事件类型。
const (
	// EventBegin 表示候选已就绪、开始提交（发生在第一轮之前，Round 为 0）。
	EventBegin   = "begin"
	EventPick    = "pick"
	EventFailed  = "failed"
	EventSuccess = "success"
	// EventRefresh 表示候选刷新成功，Message 带着新的候选数与满员数。
	EventRefresh = "refresh"
	// EventRefreshFailed 表示这次刷新没拿到可用名单（查询失败、没有命中的
	// 教学班），本轮沿用上一次的队列继续提交。
	EventRefreshFailed = "refresh_failed"
)

// Sink 接收进度事件；传 nil 表示不关心进度。
// 它会被同步调用，实现里不要做慢操作（Web 侧只往 taskStore 塞一条事件）。
type Sink func(Event)

// Source 提供"此刻能提交的候选课程"。
//
// RunAuto 在开跑前调用一次，之后每过 Plan.Refresh 再调用一次；返回的课程必须
// 带上 Do_jxb_id（提交选课的唯一凭据），过滤与排序由 RunAuto 负责。
// Engine.CandidateSource 是绑定在本引擎课程缓存上的实现（每次都重拉列表），
// 测试或"冻结一份名单"的场景也可以直接传一个返回固定切片的闭包。
//
// 第一次调用出错是致命的（没有候选就没法开始）；之后的调用出错只算"这次刷新
// 没拿到名单"，RunAuto 会沿用上一次的队列继续提交。
type Source func(ctx context.Context) ([]protocol.CustomCourseDic, error)

// SetWish 设置这个账号的愿望清单。清单跨 Reset/Initialize 保留。
func (e *Engine) SetWish(w protocol.WishList) {
	e.session.SetWish(w)
}

// Wish 返回当前愿望清单的副本。
func (e *Engine) Wish() protocol.WishList {
	return e.session.Wish()
}

// normalized 填默认值并校验明显写错的参数。
func (p Plan) normalized() (Plan, error) {
	if p.MaxRounds < 0 {
		return p, fmt.Errorf("engine: MaxRounds 不能为负: %d", p.MaxRounds)
	}
	if p.Once {
		p.MaxRounds = 1
	}
	if p.Interval <= 0 {
		p.Interval = defaultInterval
	}
	if p.Refresh <= 0 {
		p.Refresh = defaultRefresh
	}
	return p, nil
}

// RunAuto 按愿望清单的优先级反复提交候选，直到选上、ctx 结束或轮次用尽。
//
// 候选由 source 提供（见 Source / Engine.CandidateSource）：开跑前取一次，之后
// 每过 Plan.Refresh 再取一次，所以"已选人数变了""又放出来几个名额""新开了一个
// 教学班"都会在接下来的几轮里生效，不必等整个任务结束再重开。
//
// 命中的教学班即使已经满员也会进队列（排在后面重试）：自动选课跑的是"抢名额"，
// 每轮重新提交一次，有人退课就有机会选上。只有压根没命中愿望清单才返回
// ErrNoWantedCourse，满员抢不到算 ErrNotChosen。
//
// 刷新失败不是致命错误：教务抖一下、这次列表查询被"加密串"挡回来都很常见，
// 沿用上一次的队列继续提交，只在进度里留一条 EventRefreshFailed。
func (e *Engine) RunAuto(ctx context.Context, plan Plan, source Source, sink Sink) (protocol.ChooseCourseResult, error) {
	var zero protocol.ChooseCourseResult

	p, err := plan.normalized()
	if err != nil {
		return zero, err
	}
	if source == nil {
		return zero, ErrNoCandidates
	}

	wants := p.Wants
	if wants.Empty() {
		wants = e.Wish()
	}
	if wants.Empty() {
		return zero, ErrNoWantedCourse
	}

	// 会话租约：开跑时记下代次，之后每个可能改变会话的动作前后都比较。
	// Reset / 成功切换页签会递增代次，正常的参数刷新（Renew）不会。
	gen := e.SessionGeneration()

	candidates, err := source(ctx)
	if e.SessionGeneration() != gen {
		return zero, asSessionChanged(err)
	}
	if err != nil {
		// 会话在候选组装期间结束（Reset / 页签消失）时，不能把它当成
		// 一次普通的候选获取失败：自动选课已经没有可用的会话了。
		if isSessionEnd(err) && !errors.Is(err, ErrNotInitialized) {
			return zero, asSessionChanged(err)
		}
		return zero, err
	}
	if len(candidates) == 0 {
		return zero, ErrNoCandidates
	}
	queue := candidatesByPriority(wants, candidates)
	if len(queue) == 0 {
		return zero, ErrNoWantedCourse
	}
	// 把本次使用的清单写回会话，后续 /status 或重试能看到同一份配置。
	e.session.SetWish(wants)

	emit(sink, Event{
		Kind:    EventBegin,
		Message: fmt.Sprintf("候选 %d 个教学班，开始自动选课", len(queue)),
	})

	loadedAt := time.Now()

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if e.SessionGeneration() != gen {
			return zero, ErrSessionChanged
		}

		// 先刷新再提交：这一轮的排序和"已满 x/y"都用刷新后的人数。
		if time.Since(loadedAt) >= p.Refresh {
			loadedAt = time.Now()
			next, err := refreshQueue(ctx, source, wants)
			if e.SessionGeneration() != gen {
				return zero, asSessionChanged(err)
			}
			switch {
			case err != nil:
				if ctxErr := ctx.Err(); ctxErr != nil {
					return zero, ctxErr
				}
				// 会话已经结束（重置 / 页签消失）时沿用旧队列没有意义。
				if isSessionEnd(err) {
					return zero, asSessionChanged(err)
				}
				// 登录失效时提交也一定失败，交给上层重新登录。
				if errors.Is(err, protocol.ErrUnauthorized) {
					return zero, err
				}
				emit(sink, Event{
					Round:   round,
					Kind:    EventRefreshFailed,
					Message: fmt.Sprintf("刷新候选失败，沿用上一次的名单：%v", err),
				})
			case len(next) == 0:
				emit(sink, Event{
					Round:   round,
					Kind:    EventRefreshFailed,
					Message: "刷新后没有可提交的教学班，沿用上一次的名单",
				})
			default:
				queue = next
				emit(sink, Event{Round: round, Kind: EventRefresh, Message: queueMessage(queue)})
			}
		}

		for i := range queue {
			course := queue[i]
			emit(sink, Event{
				Round:   round,
				Kind:    EventPick,
				Course:  course,
				Message: pickMessage(round, course),
			})
			// sink 是同步调用的，可能刚在这里触发了 Reset / SelectTab。
			if e.SessionGeneration() != gen {
				return zero, ErrSessionChanged
			}

			result, err := e.Choose(ctx, &course)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return zero, ctxErr
			}
			// 已经选上就认成功：即使提交期间会话换了代，这门课也已经落库。
			if result.Flag == "1" {
				emit(sink, Event{
					Round:   round,
					Kind:    EventSuccess,
					Course:  course,
					Message: fmt.Sprintf("已选上《%s》%s", course.Kcmc, course.Jxbmc),
				})
				return result, nil
			}
			if e.SessionGeneration() != gen {
				return zero, asSessionChanged(err)
			}
			if err != nil {
				if isSessionEnd(err) {
					return zero, asSessionChanged(err)
				}
				// 会话已经彻底失效时重试没有意义，交给上层重新登录。
				if errors.Is(err, protocol.ErrUnauthorized) {
					return zero, err
				}
				emit(sink, Event{Round: round, Kind: EventFailed, Course: course, Message: err.Error()})
				continue
			}
			emit(sink, Event{Round: round, Kind: EventFailed, Course: course, Message: result.Msg})
		}

		if p.MaxRounds > 0 && round >= p.MaxRounds {
			if e.SessionGeneration() != gen {
				return zero, ErrSessionChanged
			}
			return zero, ErrNotChosen
		}
		if err := sleepCtx(ctx, p.Interval); err != nil {
			return zero, err
		}
	}
}

// asSessionChanged 把底层"会话已经结束"的错误归一成自动选课的租约错误，
// 同时保留原始原因，方便日志和任务详情定位是 Reset 还是页签消失。
func asSessionChanged(err error) error {
	if err == nil || errors.Is(err, ErrSessionChanged) {
		return ErrSessionChanged
	}
	return fmt.Errorf("%w: %v", ErrSessionChanged, err)
}

// refreshQueue 取一次最新候选并排成队列。返回空队列表示"这次刷新没有可提交
// 的教学班"，调用方应沿用上一次的队列。
func refreshQueue(ctx context.Context, source Source, wants protocol.WishList) ([]protocol.CustomCourseDic, error) {
	fresh, err := source(ctx)
	if err != nil {
		return nil, err
	}
	return candidatesByPriority(wants, fresh), nil
}

// queueMessage 描述刷新后的队列规模与满员情况。
//
// 刷新事件每隔 Refresh 才一条，文案里必须带数字：用户盯着任务面板时，能从中
// 看出"人数确实在变/名额确实放出来了"，而不是以为程序卡住了。
func queueMessage(queue []protocol.CustomCourseDic) string {
	full := 0
	for i := range queue {
		if isFull(queue[i]) {
			full++
		}
	}
	msg := fmt.Sprintf("候选已刷新：%d 个可提交教学班", len(queue))
	if full > 0 {
		msg += fmt.Sprintf("，其中 %d 个已满", full)
	}
	return msg
}

// candidatesByPriority 过滤出"命中愿望 + 能提交"的课程，并按优先级排序。
//
// 排序规则：愿望清单里靠前的班级优先；同一优先级下还有余额的排在满员前面，
// 余额相同时已选人数少的优先，让每次尝试都落在最可能有名额的教学班上。
//
// 满员的教学班只是排到最后，不会被丢掉：自动选课是抢名额，队列里留着它，等
// 某一轮有人退课就有机会选上。反过来若在这里把它们全过滤掉，明明命中了愿望
// 也会返回空队列，再报"愿望清单没有命中可选的课程"，排查起来根本看不出是
// 满员还是压根没这门课（真实数据见 html/ 下的抓包：定向越野6 就是 39/39 满员）。
func candidatesByPriority(wants protocol.WishList, list []protocol.CustomCourseDic) []protocol.CustomCourseDic {
	out := make([]protocol.CustomCourseDic, 0, len(list))
	for i := range list {
		course := list[i]
		if course.Do_jxb_id == "" || !wants.Match(course) {
			continue
		}
		out = append(out, course)
	}

	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rankOf(wants, out[i]), rankOf(wants, out[j])
		if ri != rj {
			return ri < rj
		}
		fi, fj := isFull(out[i]), isFull(out[j])
		if fi != fj {
			return !fi
		}
		return selected(out[i]) < selected(out[j])
	})
	return out
}

// rankOf 返回课程在愿望清单里的优先级：教学班名与课程名各算一次，取更靠前的。
// WishList.Match 两种写法都认（CLI 填教学班名，WebUI 填课程名），排序也要一致，
// 否则按课程名写的愿望会全部落到默认优先级，顺序只能是随机的。
func rankOf(wants protocol.WishList, course protocol.CustomCourseDic) int {
	rank := wants.Rank(course.Jxbmc)
	if byName := wants.Rank(course.Kcmc); byName < rank {
		rank = byName
	}
	return rank
}

// pickMessage 是"准备提交哪个教学班"的进度文案。
// 满员的也会尝试（等别人退课），所以文案里点明当前人数，别让人以为漏了过滤。
func pickMessage(round int, course protocol.CustomCourseDic) string {
	msg := fmt.Sprintf("第 %d 轮：尝试《%s》%s", round, course.Kcmc, course.Jxbmc)
	if isFull(course) {
		msg += fmt.Sprintf("（已满 %s/%s，等名额释放）", course.Yxzrs, course.Jxbrl)
	}
	return msg
}

// isFull 判断教学班是否已经报满。
//
// 只有容量和已选人数都能解析时才下结论：字段缺失/是空串时按"没满"处理，
// 否则会漏掉教务系统还没回填人数的课程。
func isFull(course protocol.CustomCourseDic) bool {
	limit, err := strconv.Atoi(strings.TrimSpace(course.Jxbrl))
	if err != nil || limit <= 0 {
		return false
	}
	chosen, err := strconv.Atoi(strings.TrimSpace(course.Yxzrs))
	if err != nil {
		return false
	}
	return chosen >= limit
}

// selected 返回已选人数，解析失败时排到最后。
func selected(course protocol.CustomCourseDic) int {
	n, err := strconv.Atoi(strings.TrimSpace(course.Yxzrs))
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return n
}

func emit(sink Sink, event Event) {
	if sink != nil {
		sink(event)
	}
}

// sleepCtx 是可被 ctx 打断的 sleep。
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
