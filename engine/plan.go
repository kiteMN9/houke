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

// maxAttemptsPerRound 限制一轮里真正提交选课的次数。
//
// 通识选修一页愿望命中的教学班可能有几十个；候选队列扩到整页之后，若每轮都
// 把整个队列提交一遍，几秒内就是几十次提交，教务会回"选课频率过高"甚至 5xx。
// 每轮只按优先级试最前面的几个：排序已经把"有名额的"排在前面，名额的变化
// 也会在下次刷新后把刚放出名额的教学班重新排到前面，捡漏的机会丢不了。
const maxAttemptsPerRound = 5

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
	// ErrAllChosen 表示命中的候选教学班全部已经选上，继续运行没有意义。
	// 任务结束原因交给上层翻译（WebUI 显示为"愿望命中的课程都已选上"）。
	ErrAllChosen = errors.New("engine: 候选课程都已选上")
	// ErrChooseRejected 表示教务明确拒绝了这次选课，而且再试也不会变：
	// flag=0 且文案不是"选课频率过高"（见 protocol.ChooseCourseResult.Retryable）。
	// 运行会立即停止，不再按轮次重试——学分 / 门数上限、不在选课时间这类拒绝
	// 不会自己好，继续跑只是白耗请求。
	ErrChooseRejected = errors.New("engine: 选课被拒绝且重试没有意义")
)

// ChooseRejectError 带走教务返回的原始结果，供任务结果和邮件展示原因。
type ChooseRejectError struct {
	Result protocol.ChooseCourseResult
}

func (e *ChooseRejectError) Error() string {
	return ErrChooseRejected.Error() + ": " + e.Result.DisplayMsg()
}

// Unwrap 让 errors.Is(err, ErrChooseRejected) 成立。
func (e *ChooseRejectError) Unwrap() error { return ErrChooseRejected }

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
	// EventAlreadyChosen 表示提交后发现这门课此前已经选上：flag=6 是提交的
	// 教学班已选中，flag=0 的「一门课程只能选一个教学班」是同课程的另一个教学班
	// 已选中。本次运行把它连同同课程号的其它教学班从队列剔除，不再重试。
	EventAlreadyChosen = "already_chosen"
)

// Sink 接收进度事件；传 nil 表示不关心进度。
// 它会被同步调用，实现里不要做慢操作（Web 侧只往 taskStore 塞一条事件）。
type Sink func(Event)

// Batch 是一次候选获取的结果。
//
// Courses 是这一次能提交的候选，必须带上 Do_jxb_id（提交选课的唯一凭据）。
// 它可以是整页，也可以只是整页的一角：候选源不必为了凑齐名单，先把几十个课程号
// 的详情都抓完再交（那是"扫完整页才开始选"，通识选修最难受的就是这一段）。
//
// More 表示源里还有没扫完的课程号：马上再要一次会接着往下扫、给出新的候选。
// RunAuto 用它决定"这一轮没选上时立刻再要一批，而不是干等 Refresh"——扫到就
// 提交，提交不成接着扫，这才是"检索过程中命中就选"的节奏。
type Batch struct {
	Courses []protocol.CustomCourseDic
	More    bool
}

// Source 提供"此刻能提交的候选课程"。
//
// RunAuto 在开跑前调用一次，之后每过 Plan.Refresh 再调用一次；上一批还没扫完
// （Batch.More）而这一轮一个都没选上时，还会立刻再要一次。返回的课程必须带上
// Do_jxb_id，过滤与排序由 RunAuto 负责。
//
// Engine.CandidateSource 是绑定在本引擎课程缓存上的实现（每一遍扫描重拉一次
// 列表）；测试或"冻结一份名单"的场景也可以直接传一个返回固定切片的闭包，
// More 留空即可——RunAuto 会退回按 Refresh 周期刷新。
//
// 第一次调用出错是致命的（没有候选就没法开始）；之后的调用出错只算"这次没拿到
// 名单"，RunAuto 会沿用上一次的队列继续提交。
type Source func(ctx context.Context) (Batch, error)

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
// 候选不必一次给全：source 说"这一遍还没扫完"（Batch.More）时，这一轮一个都没
// 选上就立刻再要一批，不等刷新周期。通识选修课整页几十个课程号、详情要一个个
// 抓，扫到第一个能选的就得先提交出去，剩下的边扫边试——"扫完整页才开始选"会让
// 用户白等一分钟。
//
// 期望按课型有两种口味（见 protocol.MatchMode，由当前页签的课程号形态判断）：
//
//   - 板块课（整页一个课程号）：用户在意的是教学班名，愿望顺序就是真实优先级
//     （足球13 排在瑜伽4 前面），排序先看志愿、再看余额；不查已选课程表——
//     选上任何一个班这门课就结束了，查一次最贵的接口只省一次提交。
//   - 通识选修课（整页几十个课程号）：用户按"教师 + 类型"挑课，命中哪门都算
//     达成，排序先看余额（有名额的就先试），再按志愿顺序；候选横跨多个课程号，
//     值得查一次已选课程表把整门已选的课剔除。
//
// 命中的教学班即使已经满员也会进队列（排在后面重试）：自动选课跑的是"抢名额"，
// 每轮重新提交一次，有人退课就有机会选上。只有压根没命中愿望清单才返回
// ErrNoWantedCourse，满员抢不到算 ErrNotChosen。
//
// 已经选上的教学班不进队列也不重试，但"要不要查已选课程表"看候选的形态
// （见 needChosenQuery）：通识选修课整页几十个课程号，值得查一次已选课表把
// 已选的整门课剔除；板块课整个页签只有一种课程号、下面挂着几十个教学班，
// 选上一个就是这门课结束，查一次最贵的接口换不来任何收益，不查——提交反馈
// 会把"这门课已经选上"告诉 RunAuto：flag=6 是提交的班已选中，flag=0 的
// "一门课程只能选一个教学班"是同课程的另一个班已选中（见
// protocol.ChooseCourseResult.AlreadyChosen）。整页命中的候选都已选上时返回
// ErrAllChosen。
//
// 一轮里真正提交的次数有上限（maxAttemptsPerRound）：队列扩到整页后，逐个
// 提交整个队列会把选课接口打进限流，每轮只试优先级最高的几个。通识选修课在这
// 几个名额里优先给"这一遍还没试过的"（见 tried）——扫描是一批批交候选的，新
// 扫到的课不能被刚被抢满的旧候选挡在后面。
//
// 刷新失败不是致命错误：教务抖一下、这次列表查询被"加密串"挡回来都很常见，
// 沿用上一次的队列继续提交，只在进度里留一条 EventRefreshFailed。
//
// 提交被教务业务拒绝时，只有"选课频率过高"值得再试；其它 flag=0 的原因
// （上课时间冲突、重复选课、学分 / 门数上限、不在选课时间……）不会因为重试而改变，
// RunAuto 立即停止并返回 ErrChooseRejected，原因在 ChooseRejectError 里。
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

	batch, err := source(ctx)
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
	candidates := batch.Courses
	// more 表示候选源这一遍还没扫完（见 Batch.More）：这批没选上就立刻接着扫。
	more := batch.More
	if len(candidates) == 0 {
		return zero, ErrNoCandidates
	}

	// 已选过滤：只在值得查的时候查（见 needChosenQuery）。查询失败不致命——
	// 这条只是辅助信息，拿不到就先不过滤，运行中提交撞上"已选"的反馈照样能把
	// 已选课程剔除。
	mode := e.matchMode()
	queue := candidatesByPriority(mode, wants, candidates)
	var chosen chosenFilter
	if needChosenQuery(queue) {
		if f, ferr := e.chosenFilterOf(ctx); ferr == nil {
			chosen = f
		}
		if e.SessionGeneration() != gen {
			return zero, ErrSessionChanged
		}
	}

	queue, dropped := dropChosen(queue, &chosen)
	if len(queue) == 0 && dropped > 0 {
		// 命中的候选全都已选：首次加载可能只是"扫到第一批"的局部名单（见
		// CandidateSource 的"命中即交"），立即再要一批确认，不拿半页的结论
		// 当"都已选上"。
		next, nextMore, nextDropped, rerr := e.loadQueue(ctx, source, wants, mode, &chosen, true)
		if e.SessionGeneration() != gen {
			return zero, asSessionChanged(rerr)
		}
		if rerr == nil {
			queue, dropped, more = next, nextDropped, nextMore
		}
	}
	if len(queue) == 0 {
		if dropped > 0 {
			return zero, ErrAllChosen
		}
		return zero, ErrNoWantedCourse
	}
	// 把本次使用的清单写回会话，后续 /status 或重试能看到同一份配置。
	e.session.SetWish(wants)

	emit(sink, Event{
		Kind:    EventBegin,
		Message: beginMessage(mode, queue, dropped, more),
	})

	loadedAt := time.Now()
	// tried 记录这一遍已经提交过的教学班（Do_jxb_id）。
	//
	// 通识选修课整页几十个课程号、"命中哪门都算达成"：扫描是一批批交候选的，
	// 队列里既可能有刚扫到、名额还新鲜的教学班，也可能有前几轮刚被抢满的旧
	// 候选。每轮只有 maxAttemptsPerRound 次机会，得先给没试过的——否则新扫到
	// 的课永远排在旧候选后面，扫描白扫。
	//
	// 板块课不用它：用户押的就是志愿顺序（足球13 排在瑜伽4 前面），每轮重试
	// 最靠前的几个才有意义，把队列从头到尾轮一遍反而让心仪的班晚被抢。
	// 定时刷新清空它：人数变了，该重新从头过一遍。
	tried := make(map[string]struct{}, 8)

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if e.SessionGeneration() != gen {
			return zero, ErrSessionChanged
		}

		// 先刷新再提交：这一轮的排序和"已满 x/y"都用刷新后的人数。
		//
		// 刷新有两种来由：到点了（Plan.Refresh），或者上一批候选源还没扫完
		// （more）而这一批已经试完、又没选上——后者就是"扫到就选、没选上接着
		// 扫"，不必为了下一批候选干等一个刷新周期。队列里还有没试过的候选时
		// 不续扫：先把它们试掉，扫到新的也挤不进这一轮的名额。第一轮同理，
		// 开跑时拿到的那批本身就是"命中即交"，先把它试掉。
		refresh := time.Since(loadedAt) >= p.Refresh
		rescan := more && round > 1 && !hasUntried(queue, tried)
		if refresh || rescan {
			if refresh {
				loadedAt = time.Now()
				// 人数变了，本遍的"试过了"记录作废，从头再过一遍。
				clear(tried)
			}
			next, nextMore, nextDropped, rerr := e.loadQueue(ctx, source, wants, mode, &chosen, refresh)
			if e.SessionGeneration() != gen {
				return zero, asSessionChanged(rerr)
			}
			switch {
			case rerr != nil:
				// 这一批没拿到：这一遍就当扫完了，回到 Refresh 周期再要，
				// 否则会按轮次反复打同一个必失败的请求。
				more = false
				if ctxErr := ctx.Err(); ctxErr != nil {
					return zero, ctxErr
				}
				// 会话已经结束（重置 / 页签消失）时沿用旧队列没有意义。
				if isSessionEnd(rerr) {
					return zero, asSessionChanged(rerr)
				}
				// 登录失效时提交也一定失败，交给上层重新登录。
				if errors.Is(rerr, protocol.ErrUnauthorized) {
					return zero, rerr
				}
				// 续扫失败只在进度里留一句，真正的动静还是随后的提交。
				msg := "刷新候选失败，沿用上一次的名单：" + rerr.Error()
				if !refresh {
					msg = "继续扫描失败，沿用上一次的名单：" + rerr.Error()
				}
				emit(sink, Event{Round: round, Kind: EventRefreshFailed, Message: msg})
			case len(next) == 0 && nextDropped > 0:
				// 命中的候选仍然全部已选，任务没有继续的意义。
				return zero, ErrAllChosen
			case len(next) == 0:
				msg := "刷新后没有可提交的教学班，沿用上一次的名单"
				if !refresh {
					msg = "这一批没有扫到可提交的教学班，沿用上一次的名单"
				}
				emit(sink, Event{Round: round, Kind: EventRefreshFailed, Message: msg})
			default:
				queue, more = next, nextMore
				// 续扫不报"刷新"：候选源一批批往下扫的时候，每轮一条刷新型
				// 事件只是噪音，真正的动静由随后的 EventPick 说明。
				if refresh {
					emit(sink, Event{Round: round, Kind: EventRefresh, Message: queueMessage(queue, nextDropped)})
				}
			}
		}

		// 通识选修先试这一遍还没试过的候选（见 tried）。
		order := queue
		if mode == protocol.MatchGeneral {
			order = untriedFirst(queue, tried)
		}

		attempts := 0
		for i := range order {
			course := order[i]
			// 本轮中发现已选的（提交反馈、刷新后的已选名单）不再提交。
			if chosen.matches(course) {
				continue
			}
			// 一轮只提交优先级最前面的几个候选（见 maxAttemptsPerRound）。
			if attempts >= maxAttemptsPerRound {
				break
			}
			attempts++
			tried[course.Do_jxb_id] = struct{}{}
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
			// flag=3（选课成功、免费学分已达上限）同样算落库，见 OK()。
			if result.OK() {
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
			// 这门课此前已经选上（用户手动选过，或同课程的其它教学班已选）：
			// 记进过滤器，本轮后续、之后的轮次与刷新都不再尝试它和同课程的
			// 其它教学班。既不算失败也不值得重试——重试只会白打请求。这条
			// 反馈是"不查已选课程表"的前提（见 needChosenQuery）。
			if result.AlreadyChosen() {
				if result.Flag == protocol.ChooseFlagAlreadyChosen {
					chosen.add(course.Jxb_id, course.Kch_id)
				} else {
					// flag=0"一门课程只能选一个教学班"：选上的是别的班，
					// 只知道课程号已满足，这个教学班本身没被选中。
					chosen.add("", course.Kch_id)
				}
				emit(sink, Event{
					Round:   round,
					Kind:    EventAlreadyChosen,
					Course:  course,
					Message: alreadyChosenMessage(course, result),
				})
				continue
			}
			// 教务的确定性拒绝（flag=0 且不是"选课频率过高"）：换轮次、换页面
			// 都不会变，立即停止，别把几小时耗在必然失败的请求上。
			if !result.Retryable() {
				emit(sink, Event{Round: round, Kind: EventFailed, Course: course, Message: result.DisplayMsg()})
				return zero, &ChooseRejectError{Result: result}
			}
			// flag=-1 的 msg 是人数机器串，展示前翻成人话，见 DisplayMsg。
			emit(sink, Event{Round: round, Kind: EventFailed, Course: course, Message: result.DisplayMsg()})
		}

		// 剔除本轮通过 flag=6 发现已选的候选。队列空了说明剩下的全已选：
		// 不空转等下个刷新周期，立即把 loadedAt 归零触发一次整页刷新确认，
		// 仍然全部已选就在刷新分支里返回 ErrAllChosen。
		queue, _ = dropChosen(queue, &chosen)
		if len(queue) == 0 {
			loadedAt = time.Time{}
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

// loadQueue 取一次最新候选：排序、（值得的时候）查询已选课程、剔除已选教学班。
//
// mode 是开跑时定下的课型（见 matchMode）：同一轮任务里匹配与排序口味必须
// 一致，不能因为刷新到的名单覆盖了多少课程号而变来变去。
//
// chosen 是运行中累积的过滤器：开跑前查到的名单、提交反馈学到的已选课程都在
// 里面；无论这次查不查已选课程表，都用它剔除队列。
//
// 返回的 more 是候选源"这一遍还没扫完"（见 Batch.More），原样交给 RunAuto；
// dropped 是这次被已选过滤器剔除的数量：队列空且 dropped>0 表示命中的候选
// 全部已选（区别于"压根没命中"的空队列）。
//
// withChosen 决定这次要不要重查已选课程表：只有定时刷新才查。续扫是按轮次
// 进行的（可能一两秒一次），把最贵的接口按这个频率打进去，比省下的几次提交
// 划算得多；续扫期间靠提交反馈（flag=6 / 兄弟班拒绝）剔除已选的课就够。
func (e *Engine) loadQueue(ctx context.Context, source Source, wants protocol.WishList, mode protocol.MatchMode, chosen *chosenFilter, withChosen bool) (queue []protocol.CustomCourseDic, more bool, dropped int, err error) {
	batch, err := source(ctx)
	if err != nil {
		return nil, false, 0, err
	}
	queue = candidatesByPriority(mode, wants, batch.Courses)
	if withChosen && needChosenQuery(queue) {
		// 已选名单跟着刷新一次：用户可能在别的窗口手动选了课，队列要同步剔除；
		// 查询失败或没拿到数据就沿用旧的过滤器。结果是并进而不是覆盖——提交
		// 反馈学到的已选信息不能被一次空查询抹掉。
		if f, ferr := e.chosenFilterOf(ctx); ferr == nil {
			chosen.merge(f)
		}
	}
	queue, dropped = dropChosen(queue, chosen)
	return queue, batch.More, dropped, nil
}

// hasUntried 报告队列里还有没有这一遍没提交过的候选。
//
// 有就不必急着扫下一批：每轮只有 maxAttemptsPerRound 次机会，先把手里这些试
// 完，扫到新的也挤不进这一轮。
func hasUntried(queue []protocol.CustomCourseDic, tried map[string]struct{}) bool {
	for i := range queue {
		if _, ok := tried[queue[i].Do_jxb_id]; !ok {
			return true
		}
	}
	return false
}

// untriedFirst 把这一遍还没提交过的候选排到前面，其余保持原顺序。
//
// 只在通识选修课里用（见 RunAuto 的 tried）：扫描是分批交候选的，队列里既有
// 刚扫到的新候选、也有刚被抢满的旧候选，而每轮只有 maxAttemptsPerRound 次机会。
// 不把没试过的提前，新扫到的课就会一直排在旧候选后面，扫描等于白跑。
func untriedFirst(queue []protocol.CustomCourseDic, tried map[string]struct{}) []protocol.CustomCourseDic {
	if len(tried) == 0 {
		return queue
	}
	out := make([]protocol.CustomCourseDic, 0, len(queue))
	for i := range queue {
		if _, ok := tried[queue[i].Do_jxb_id]; !ok {
			out = append(out, queue[i])
		}
	}
	for i := range queue {
		if _, ok := tried[queue[i].Do_jxb_id]; ok {
			out = append(out, queue[i])
		}
	}
	return out
}

// needChosenQuery 报告这批候选值不值得查询已选课程表。
//
// 已选课程查询比课程列表贵得多：它要把整张已选课表翻出来（分页、带全套用户
// 参数），而它换来的只是"少提交几次已选的课"。这个收益只在候选横跨多个课程号
// 时才存在——通识选修课整页几十个课程号，用户可能已经选掉其中若干门，每门都
// 白提交一次就得不偿失。
//
// 板块课正好相反，也是不查的最主要场景：整个页签只有一种课程号，下面挂着几十
// 个教学班（体育Ⅳ → 足球13 / 瑜伽4 / 舞龙4…）。选上其中任何一个，这门课就
// 结束了，查已选课表的唯一作用是省掉**一次**提交；而不查的代价也就那一次：
//
//   - 选的是同一个教学班 → flag=1，直接认"已选"；
//   - 选的是兄弟教学班 → flag=0「一门课程只能选一个教学班」，同样认"已选"
//     （见 protocol.ChooseCourseResult.AlreadyChosen）。
//
// 两条都在一次提交内闭环，比把最贵的请求打进每一轮划算得多。课型判断就是
// protocol.IsKchIdAllSame：候选全在同一个课程号下就是板块课形态。课程号缺失时
// 无法判断形态，按老办法查——查了顶多多花一次请求，不查则可能反复白提交。
func needChosenQuery(queue []protocol.CustomCourseDic) bool {
	if len(queue) < 2 {
		return false
	}
	if queue[0].Kch_id == "" {
		return true
	}
	same, _ := protocol.IsKchIdAllSame(queue)
	return !same
}

// chosenFilter 记录"已经选上"的教学班（jxb_id）与课程号（kch_id）。
//
// 教务规定一门课只能选一个教学班：课程号命中时，同课程的其它教学班再提交只会
// 收到 flag=0 的确定性拒绝（"一门课程只能选一个教学班"），必须跟着剔除。jxb_id /
// kch_id 为空的候选不参与匹配——空键会把没有 id 的条目全部误剔除。
type chosenFilter struct {
	jxb map[string]struct{}
	kch map[string]struct{}
}

// matches 报告这个教学班（或它所属的课程）是否已经选上。
func (f *chosenFilter) matches(c protocol.CustomCourseDic) bool {
	if f == nil {
		return false
	}
	if c.Jxb_id != "" {
		if _, ok := f.jxb[c.Jxb_id]; ok {
			return true
		}
	}
	if c.Kch_id != "" {
		_, ok := f.kch[c.Kch_id]
		return ok
	}
	return false
}

// add 把一个已选教学班记进过滤器（jxb_id / kch_id 空的忽略）。
func (f *chosenFilter) add(jxbID, kchID string) {
	if jxbID != "" {
		if f.jxb == nil {
			f.jxb = make(map[string]struct{}, 4)
		}
		f.jxb[jxbID] = struct{}{}
	}
	if kchID != "" {
		if f.kch == nil {
			f.kch = make(map[string]struct{}, 4)
		}
		f.kch[kchID] = struct{}{}
	}
}

// merge 把另一次已选查询的结果并进来。只用并集不用覆盖：运行中靠 flag=6
// 学到的已选信息，不能被一次"成功但为空"的查询抹掉（否则又会开始对已选的
// 课程反复提交）。代价是"用户退课后想靠正在跑的任务重新抢回这门课"做不到——
// 那种场景重新启动一次任务即可，开跑前的查询是全新名单。
func (f *chosenFilter) merge(other chosenFilter) {
	for k := range other.jxb {
		f.add(k, "")
	}
	for k := range other.kch {
		f.add("", k)
	}
}

// dropChosen 从队列里剔除已选的教学班，返回新队列与剔除数量。
// 剔除数量 > 0 且队列为空，说明这批命中的候选全部已选上。
func dropChosen(queue []protocol.CustomCourseDic, chosen *chosenFilter) ([]protocol.CustomCourseDic, int) {
	if chosen == nil || (len(chosen.jxb) == 0 && len(chosen.kch) == 0) {
		return queue, 0
	}
	out := make([]protocol.CustomCourseDic, 0, len(queue))
	for i := range queue {
		if !chosen.matches(queue[i]) {
			out = append(out, queue[i])
		}
	}
	return out, len(queue) - len(out)
}

// chosenFilterOf 查询当前已选课程，组装成 chosenFilter。
//
// 只在 needChosenQuery 说"值得"时才调用：这个接口要把整张已选课表翻出来，比
// 课程列表贵得多，板块课（单课程号、几十个教学班）不查它。
//
// 学年 / 学期取自选课首页参数（IndexInfo.Term），与当前选课轮次一致，不需要
// 调用方传参。查询失败时把错误交回调用方：自动选课把它当尽力而为的辅助信息，
// 失败就先不过滤，运行中靠"已选"的提交反馈照样能剔除已选课程。
func (e *Engine) chosenFilterOf(ctx context.Context) (chosenFilter, error) {
	xnm, xqm := e.session.snapshot().Index.Term()
	if xnm == "" || xqm == "" {
		return chosenFilter{}, errors.New("engine: 选课参数缺少学年/学期，无法查询已选课程")
	}
	list, err := e.Selected(ctx, xnm, xqm)
	if err != nil {
		return chosenFilter{}, err
	}
	var f chosenFilter
	for i := range list {
		f.add(list[i].JxbId, list[i].Kch_id)
	}
	return f, nil
}

// alreadyChosenMessage 是撞上"已选"之后的进度文案。
//
// 两种来源要分开说：flag=6 是这个教学班本身已选上；flag=0 的"只能选一个教学班"
// 是这门课的**另一个**教学班已选上（用户手动选的、或上一次运行选的），用户看到
// 文案才知道选上的是别的班，不用再去翻已选课表。
func alreadyChosenMessage(course protocol.CustomCourseDic, result protocol.ChooseCourseResult) string {
	if result.Flag == protocol.ChooseFlagAlreadyChosen {
		return fmt.Sprintf("《%s》%s 已选上，不再重复尝试", course.Kcmc, course.Jxbmc)
	}
	return fmt.Sprintf("《%s》已选过其它教学班，整门课不再重复尝试", course.Kcmc)
}

// queueMessage 描述刷新后的队列规模与满员情况。
//
// 刷新事件每隔 Refresh 才一条，文案里必须带数字：用户盯着任务面板时，能从中
// 看出"人数确实在变/名额确实放出来了"，而不是以为程序卡住了。
func queueMessage(queue []protocol.CustomCourseDic, dropped int) string {
	full := 0
	for i := range queue {
		if isFull(queue[i]) {
			full++
		}
	}
	msg := fmt.Sprintf("候选已刷新：%d 个可提交教学班", len(queue))
	if dropped > 0 {
		msg += fmt.Sprintf("（剔除已选 %d 个）", dropped)
	}
	if full > 0 {
		msg += fmt.Sprintf("，其中 %d 个已满", full)
	}
	return msg
}

// candidatesByPriority 过滤出"命中愿望 + 能提交"的课程，并按优先级排序。
//
// 排序规则分课型（见 protocol.MatchMode）：
//
//   - 板块课：先按愿望清单里的教学班顺序（用户在意的是"足球13 优先于瑜伽4"），
//     同一志愿下还有余额的排前面，再按已选人数少的优先。
//   - 通识选修课：先按有没有余额——用户按"教师 + 类型"挑课，命中哪门都算达成，
//     死守志愿顺序只会让有名额的备选排在一堆满员的点名课后面，白耗每轮的
//     提交次数；余额相同时再按志愿顺序，最后按已选人数。
//
// 满员的教学班只是排到最后，不会被丢掉：自动选课是抢名额，队列里留着它，等
// 某一轮有人退课就有机会选上。反过来若在这里把它们全过滤掉，明明命中了愿望
// 也会返回空队列，再报"愿望清单没有命中可选的课程"，排查起来根本看不出是
// 满员还是压根没这门课（真实数据见 html/ 下的抓包：定向越野6 就是 39/39 满员）。
func candidatesByPriority(mode protocol.MatchMode, wants protocol.WishList, list []protocol.CustomCourseDic) []protocol.CustomCourseDic {
	out := make([]protocol.CustomCourseDic, 0, len(list))
	for i := range list {
		course := list[i]
		if course.Do_jxb_id == "" || !wants.MatchIn(mode, course) {
			continue
		}
		out = append(out, course)
	}

	sort.SliceStable(out, func(i, j int) bool {
		fi, fj := isFull(out[i]), isFull(out[j])
		ri, rj := rankOf(wants, out[i]), rankOf(wants, out[j])
		if mode == protocol.MatchGeneral {
			if fi != fj {
				return !fi
			}
			if ri != rj {
				return ri < rj
			}
			return selected(out[i]) < selected(out[j])
		}
		if ri != rj {
			return ri < rj
		}
		if fi != fj {
			return !fi
		}
		return selected(out[i]) < selected(out[j])
	})
	return out
}

// rankOf 返回课程在愿望清单里的优先级：教学班名与课程名各算一次，取更靠前的。
// WishList.MatchIn 两种写法都认（板块课填教学班名，通识选修课填课程名），排序
// 也要一致，否则按课程名写的愿望会全部落到默认优先级，顺序只能是随机的。
func rankOf(wants protocol.WishList, course protocol.CustomCourseDic) int {
	rank := wants.Rank(course.Jxbmc)
	if byName := wants.Rank(course.Kcmc); byName < rank {
		rank = byName
	}
	return rank
}

// beginMessage 是开跑时的进度文案：写明这一轮按哪套规则挑课、队列规模，以及
// 剔除了多少已选课程——用户看到"通识选修课（按教师 / 类型匹配）"，才知道
// 课程类型确实生效了，而"板块课（按教学班名匹配）"能解释为什么类型没起作用。
func beginMessage(mode protocol.MatchMode, queue []protocol.CustomCourseDic, dropped int, more bool) string {
	msg := fmt.Sprintf("%s：候选 %d 个教学班，开始自动选课", modeText(mode), len(queue))
	if dropped > 0 {
		msg += fmt.Sprintf("（已剔除选上的 %d 个）", dropped)
	}
	if more {
		// 通识选修课整页几十个课程号：先交能选的那几个，剩下的边选边扫。
		msg += "，其余课程继续扫描"
	}
	return msg
}

// modeText 是课型的中文说法，用于进度与结果文案。
func modeText(mode protocol.MatchMode) string {
	if mode == protocol.MatchBlock {
		return "板块课（按教学班名匹配）"
	}
	return "通识选修课（按教师 / 类型匹配）"
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
