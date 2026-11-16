package engine

import (
	"context"
	"errors"
	"sort"

	"school_sdk/protocol"
)

// ErrNotInitialized 表示会话还没完成 Initialize：没有选课参数，也就没法抓课程。
// Web 层把它翻成 400「请先调用 /init 完成初始化」。
var ErrNotInitialized = errors.New("engine: 会话尚未初始化")

// matchMode 报告当前页签的课型（见 protocol.MatchModeOf）。
//
// 判断依据是页签缓存里课程号的形态，而不是某一次候选切片的形态：通识选修课的
// 首次加载可能只抓到点名的几个课程号，甚至只有一个，那是"还没扫完"，不是
// 板块课。缓存为空（还没拉过列表）时按通识选修处理——它要求教师 / 类型命中，
// 条件更严格，不会因为判断不出形态就把整页的课都收进队列。
func (e *Engine) matchMode() protocol.MatchMode {
	return protocol.MatchModeOf(e.Courses())
}

// CandidateSource 返回绑定在本引擎课程缓存上的候选源（见 Source）。
//
// 它按"一遍扫描"工作，每一遍从最新的课程列表出发，按愿望优先级逐个课程号补
// 详情，**扫到第一个"命中且有余额"的教学班就先交出去**（Batch.More 置真）：
// 用户要的是"检索过程中命中就选"，不是"把整页详情抓完再挑"。这一批没选上，
// RunAuto 会立刻再要一批，扫描从上次停下的地方接着走——补过详情的课程号在缓存
// 里带着 Do_jxb_id，不会再发请求，所以"接着扫"的代价只是下一个课程号的详情。
// 扫到整页末尾（没有详情要抓了）才 More=false，RunAuto 回到按 Refresh 周期
// 刷新的节奏；一遍扫描 = 一次列表请求。
//
// 两种课型的扫法不同（见 protocol.MatchModeOf）：
//
//   - 板块课（整页一个课程号）：一次详情就能补齐这门课的全部教学班，没有"先扫
//     哪门"的问题，一遍就扫完（More 恒为 false），提交节奏全交给 RunAuto 的
//     轮次重试。名字优先与提前收手在这里都不省请求，collect 里会关掉。
//   - 通识选修课（整页几十个课程号）：第一次如果填了课程名，先只抓点名的课程
//     号——点名的课经常不在整页开头，这一手把"扫完整页才能提交"变成"第一个
//     请求就能提交"。点名课全满或一门都没命中时，同一批里就接着整页扫，边扫
//     边交。教师 / 类型愿望只能在整页扫描里命中（教师只出现在详情里）。
//
// 列表接口"成功"但没回数据（典型是加密串过期、参数被教务拒绝）时返回
// ErrNoCandidates：这不能当成"愿望没有命中"，否则 RunAuto 会把上一次的队列
// 丢掉，用户看到的就是"跑着跑着没候选了"。
func (e *Engine) CandidateSource(wants protocol.WishList) Source {
	// started 记录第一批是否已经交过；more 记录上一批之后这一遍是否还没扫完。
	// RunAuto 串行调用 Source，不需要加锁。
	var started, more bool
	return func(ctx context.Context) (Batch, error) {
		if !started {
			started = true
			if wants.HasCourseNames() {
				named, namedMore, err := e.collect(ctx, wants, collectOptions{refreshList: true, nameOnly: true})
				if err != nil {
					return Batch{}, err
				}
				// 点名的课此刻就能提交：先交出去，剩下的课程号下一批再扫。
				// 板块课（namedMore=false）本来一遍就扫完，不必再要一批。
				if hasVacancy(named) {
					more = namedMore
					return Batch{Courses: named, More: namedMore}, nil
				}
				// 点名课没命中、或者命中的全满：同一批里接着整页扫。列表刚
				// 刷新过，不重复请求；点名的课也已经补过详情，扫描从它们
				// 后面的课程号开始花请求。
				courses, m, cerr := e.collect(ctx, wants, collectOptions{quick: true})
				more = m
				return Batch{Courses: courses, More: m}, cerr
			}
			courses, m, err := e.collect(ctx, wants, collectOptions{refreshList: true, quick: true})
			more = m
			return Batch{Courses: courses, More: m}, err
		}
		// 上一批没扫完就接着扫，不重拉列表：人数由刚抓到的详情刷新，列表留着
		// 下一遍再拉。整页扫完了（more=false）才拉新列表开新的一遍——那一遍
		// 的详情都缓存着，几乎不花请求。
		courses, m, err := e.collect(ctx, wants, collectOptions{refreshList: !more, quick: true})
		if err != nil {
			// 这一遍作废，等 RunAuto 的下一个刷新周期重开，别按轮次反复打
			// 同一个必失败的请求。
			more = false
			return Batch{}, err
		}
		more = m
		return Batch{Courses: courses, More: m}, nil
	}
}

// CollectCandidates 组装自动选课的候选课程（整页，不提前收手）。
//
// 课程列表只带教学班与人数，提交选课需要的 Do_jxb_id 必须从课程详情里拿
// （详情按 kch_id 请求，返回该课程号下的全部教学班）。因此这里按课程号
// （kch_id）逐个推进：
//
//  1. 缓存为空时先拉一次列表（Web 的列表页通常已经缓过，直接复用）；
//  2. 按愿望给课程号排序，逐个补详情：一个课程号只请求一次，覆盖它下面
//     的全部教学班；这个课程号下一个教学班都没命中就跳过它，继续下一个；
//  3. 把命中的教学班收进候选，满员过滤与最终排序留给 RunAuto。
//
// 匹配规则按课型选择（见 protocol.MatchModeOf）：板块课按教学班名（或教师），
// 通识选修课按教师 / 类型 / 课程名。它返回的是**整页**候选、不提前收手：
// 调用方要完整名单时用它（自动选课走 CandidateSource，扫到能提交的就先提交）。
//
// 满员过滤与排序不在这里做：那是 RunAuto 的职责，CollectCandidates 只保证
// 候选带齐提交参数。需要"此刻最新人数"时用 CandidateSource。
func (e *Engine) CollectCandidates(ctx context.Context, wants protocol.WishList) ([]protocol.CustomCourseDic, error) {
	courses, _, err := e.collect(ctx, wants, collectOptions{})
	return courses, err
}

// collectOptions 是一次候选收集的参数。
type collectOptions struct {
	// refreshList 为 true 时无条件重拉课程列表（每遍扫描的第一次调用走这条）。
	refreshList bool
	// nameOnly 为 true 时只处理课程名命中的课程号，空结果不算错误——调用方要
	// 回落到整表扫描。板块课用不上（整页只有一个课程号），collect 会关掉它。
	nameOnly bool
	// quick 为 true 时走"扫到就交"：这一遍扫到第一个命中且有余额的教学班就
	// 返回（and 后面还有课程号没扫，见返回的 more），让 RunAuto 立刻提交。
	// 自动选课走这条；要完整名单时（CollectCandidates）关掉它。
	quick bool
}

// collect 是 CollectCandidates / CandidateSource 的实现。
//
// 返回的 more 表示"这一遍还没扫完"：这次提前收手了，后面还有课程号要抓详情。
// 没有详情要抓时，多扫几个课程号不花请求，收手只会让队列莫名其妙地缺一截。
//
// 提前收手（quick）只认**这次抓到的详情**里的名额：缓存里那些早先抓好的教学班
// 哪怕还显示有名额，也不该拦住扫描——它们要么已经交出去过（RunAuto 正在重试），
// 要么就是人数过期，为它们停下会让扫描每轮只前进一个课程号。
func (e *Engine) collect(ctx context.Context, wants protocol.WishList, opt collectOptions) ([]protocol.CustomCourseDic, bool, error) {
	if !e.Inited() {
		return nil, false, ErrNotInitialized
	}
	if wants.Empty() {
		wants = e.Wish()
	}
	if wants.Empty() {
		return nil, false, ErrNoWantedCourse
	}

	cached := e.Courses()
	if opt.refreshList || len(cached) == 0 {
		list, err := e.GetCourseList(ctx)
		if err != nil {
			return nil, false, err
		}
		if opt.refreshList && len(list) == 0 {
			// 列表查询没有失败、但也没给数据：保留旧缓存，让上层沿用上一次的队列。
			return nil, false, ErrNoCandidates
		}
		e.CacheCourseList(list)
		cached = e.Courses()
	}
	if len(cached) == 0 {
		return nil, false, ErrNoCandidates
	}

	// 课型由课程号的形态决定（见 protocol.MatchModeOf）：板块课整页只有一个
	// 课程号，下面挂着全部教学班；通识选修课整页几十个课程号。
	mode := protocol.MatchModeOf(cached)
	if mode == protocol.MatchBlock {
		// 板块课只有一次详情请求，"名字优先"和"提前收手"都省不下请求：
		// 按课程名过滤课程号反而会把"只填了教师"的板块课挡在门外。
		opt.nameOnly = false
		opt.quick = false
	}

	groups := courseNumberGroups(wants, cached, mode, opt.nameOnly)
	out := make([]protocol.CustomCourseDic, 0, len(cached))
	var firstErr error
	// freshVacancy 记录这次抓到的详情里有没有"命中且有余额"的教学班：只有它
	// 才值得提前收手（见函数注释）。
	freshVacancy := false
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		// 一个课程号只抓一次详情：接口返回该课程号下的全部教学班。
		fetched := false
		if group.needsDetail {
			detail, err := e.GetCourseDetail(ctx, group.kchID)
			if err != nil {
				// 取消/登录失效/会话结束是全局性问题，不能当成"这门课没拿到详情"跳过。
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
					errors.Is(err, protocol.ErrUnauthorized) || isSessionEnd(err) {
					return nil, false, err
				}
				if firstErr == nil {
					firstErr = err
				}
			} else {
				e.CacheCourseDetail(detail)
				fetched = true
			}
		}
		// 详情补齐了教师与 Do_jxb_id，命中才收；这个课程号下一个教学班都没
		// 命中就整组跳过，继续下一个课程号。
		if matched := matchedClasses(e.Courses(), wants, mode, group.kchID, opt.nameOnly); len(matched) > 0 {
			out = append(out, matched...)
			// 只认这次真抓到详情的那组：拿不到详情时缓存里的人数可能是过期的。
			if fetched && hasVacancy(matched) {
				freshVacancy = true
			}
		}
		// 扫到就交：手里已经有了此刻能提交的候选（而且是真的刚抓到的详情），
		// 后面还要再请求详情，就先交给 RunAuto 去提交——第一次提交不必等整页
		// 详情抓完。剩下的课程号留到下一批：补过详情的不再产生请求，扫描自然
		// 从第一个还没详情的课程号接着扫。
		if opt.quick && freshVacancy && hasDetailWork(groups[i+1:]) {
			return out, true, nil
		}
	}
	if len(out) == 0 {
		if opt.nameOnly {
			// 名字命中的课可能一门都没拿到详情：交给调用方回落整表扫描，
			// 不能把"还没扫完"当成"选不上"。
			return nil, false, nil
		}
		// 详情请求全失败时，把第一个错误报出去，比"没有命中"更好排查。
		if firstErr != nil {
			return nil, false, firstErr
		}
		return nil, false, ErrNoWantedCourse
	}
	// 名字优先的一趟只看了点名的课程号，整页剩下的还没扫（more=true）；板块课
	// 的 nameOnly 在上面就被关掉了，所以这里只会是 false。
	return out, opt.nameOnly, nil
}

// hasVacancy 报告候选里是否至少有一个此刻还有名额。
//
// 提前收手只认"现在就能提交"的候选：满员的课可以留在队列里等名额释放，但没
// 必要为了它停下整页扫描——用户要的是"找到能选的直接选"。
func hasVacancy(list []protocol.CustomCourseDic) bool {
	for i := range list {
		if !isFull(list[i]) {
			return true
		}
	}
	return false
}

// hasDetailWork 报告这批课程号里还有没有要抓详情的。
// 没有详情要抓时继续扫不花请求，提前收手只会让队列莫名其妙地缺一截。
func hasDetailWork(groups []courseNumber) bool {
	for i := range groups {
		if groups[i].needsDetail {
			return true
		}
	}
	return false
}

// courseNumber 是同一个课程号（kch_id）下的候选。
//
// 详情接口按课程号返回该课程号下的全部教学班，所以一个课程号只请求一次；
// 这个课程号下一个教学班都没命中时整组跳过，继续下一个课程号。
type courseNumber struct {
	kchID       string
	rank        int  // 课程号在愿望清单里的最好名次（名字越靠前越优先）
	needsDetail bool // 还有教学班没拿到 Do_jxb_id
}

// courseNumberGroups 把课程列表按 kch_id 分组，只保留可能命中愿望的课程号，
// 并按愿望优先级排序。
//
// 粗筛按课型取规则（见 protocol.CouldMatchIn）：板块课整页只有一个课程号、
// 详情只请求一次，粗筛直接放行；通识选修课按名字 / 类型先筛，教师愿望则必须
// 全部放行（教师只在详情里）。
//
// nameOnly 为 true 时只收课程名命中的课程号：名字优先的快速路径只抓用户点名
// 的那几门，不必为了它们把整张课表抓一遍。
func courseNumberGroups(wants protocol.WishList, list []protocol.CustomCourseDic, mode protocol.MatchMode, nameOnly bool) []courseNumber {
	index := make(map[string]int, len(list))
	groups := make([]courseNumber, 0, len(list))
	for i := range list {
		course := list[i]
		if course.Kch_id == "" || !wants.CouldMatchIn(mode, course) {
			continue
		}
		if nameOnly && !wants.MatchesCourseName(course) {
			continue
		}
		pos, ok := index[course.Kch_id]
		if !ok {
			pos = len(groups)
			index[course.Kch_id] = pos
			groups = append(groups, courseNumber{kchID: course.Kch_id, rank: rankOf(wants, course)})
		}
		group := &groups[pos]
		if byName := rankOf(wants, course); byName < group.rank {
			group.rank = byName
		}
		if course.Do_jxb_id == "" {
			group.needsDetail = true
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].rank < groups[j].rank })
	return groups
}

// matchedClasses 返回某个课程号下"命中愿望、且拿得到 Do_jxb_id"的教学班。
func matchedClasses(list []protocol.CustomCourseDic, wants protocol.WishList, mode protocol.MatchMode, kchID string, nameOnly bool) []protocol.CustomCourseDic {
	out := make([]protocol.CustomCourseDic, 0, 2)
	for i := range list {
		course := list[i]
		if course.Kch_id != kchID || course.Do_jxb_id == "" || !wants.MatchIn(mode, course) {
			continue
		}
		if nameOnly && !wants.MatchesCourseName(course) {
			continue
		}
		out = append(out, course)
	}
	return out
}
