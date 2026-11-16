package engine

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"

	"school_sdk/protocol"
)

// ErrNotInitialized 表示会话还没完成 Initialize：没有选课参数，也就没法抓课程。
// Web 层把它翻成 400「请先调用 /init 完成初始化」。
var ErrNotInitialized = errors.New("engine: 会话尚未初始化")

// CandidateSource 返回绑定在本引擎课程缓存上的候选源（见 Source）。
//
// 与 CollectCandidates 的区别有三处：**每次调用都重拉课程列表**；第一次调用
// 如果愿望里填了课程名，先走"名字优先"——只抓课程名命中的课程号，命中就立即
// 返回，一门都没命中时才回落到整表扫描；整表扫描走"快速首扫"——找到一个此刻
// 能提交的候选就先返回，让第一次提交尽快发出去。
//
// 名字优先是为学校的两类选课设计的：板块课一个课程号下挂着全部教学班，一次
// 详情就能补齐，怎么算都不慢；通识选修课整页有几十个课程号、每个课程号一两个
// 教学班，全抓一遍要几十次详情（限流下约一分钟）。用户点名的课经常不在整页的
// 开头，名字优先把它从"扫完整页才能提交"变成"第一次请求就能提交"。
//
// 快速首扫只发生在第一次调用：抢课的第一发请求不值得等整页详情抓完。之后的
// 调用（自动选课的周期性刷新）会把整页扫完——补过详情的课程号在缓存里带着
// Do_jxb_id、不会再发请求，所以整页扫描的代价只是一次列表请求加"还没抓过的
// 课程号"的详情；通识选修的全部课程和教学班在第一次刷新后就都在队列里，不再
// 只围着最先扫到的几个教学班反复尝试。
//
// 教师 / 类型愿望只能在整表扫描里命中（教师只出现在详情里），没有名字可以提前
// 定位；同理，扫到一个能提交的就先提交，不把整页详情拖到第一次提交之前。提前
// 收手不会丢掉低优先级的课：补过详情的课程号在缓存里带着 Do_jxb_id，下一次
// 刷新不会再为它们发请求，扫描自然从上次停下的地方继续，队列只会变长。
//
// 列表接口"成功"但没回数据（典型是加密串过期、参数被教务拒绝）时返回
// ErrNoCandidates：这不能当成"愿望没有命中"，否则 RunAuto 会把上一次的队列
// 丢掉，用户看到的就是"跑着跑着没候选了"。
func (e *Engine) CandidateSource(wants protocol.WishList) Source {
	var first atomic.Bool
	first.Store(true)
	return func(ctx context.Context) ([]protocol.CustomCourseDic, error) {
		if first.CompareAndSwap(true, false) {
			if wants.HasCourseNames() {
				nameOnly, err := e.collect(ctx, wants, collectOptions{refreshList: true, nameOnly: true})
				if err != nil {
					return nil, err
				}
				if len(nameOnly) > 0 {
					return nameOnly, nil
				}
				// 名字没有命中：列表刚刷新过，直接拿它做整表扫描。
				return e.collect(ctx, wants, collectOptions{quick: true})
			}
			return e.collect(ctx, wants, collectOptions{refreshList: true, quick: true})
		}
		return e.collect(ctx, wants, collectOptions{refreshList: true})
	}
}

// CollectCandidates 组装自动选课的候选课程。
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
// 它返回的是**整页**候选、不提前收手：调用方要完整名单时用它（自动选课走
// CandidateSource，找到一个能提交的就先提交）。
//
// 满员过滤与排序不在这里做：那是 RunAuto 的职责，CollectCandidates 只保证
// 候选带齐提交参数。需要"此刻最新人数"时用 CandidateSource。
func (e *Engine) CollectCandidates(ctx context.Context, wants protocol.WishList) ([]protocol.CustomCourseDic, error) {
	return e.collect(ctx, wants, collectOptions{})
}

// collectOptions 是一次候选收集的参数。
type collectOptions struct {
	// refreshList 为 true 时无条件重拉课程列表（自动选课的周期性刷新走这条）。
	refreshList bool
	// nameOnly 为 true 时只处理课程名命中的课程号，空结果不算错误——调用方要
	// 回落到整表扫描。
	nameOnly bool
	// quick 为 true 时走"快速首扫"：整表扫描找到一个此刻能提交的候选就先
	// 返回，不把整页详情拖到第一次提交之前。只用于自动选课的第一次加载；
	// 之后的刷新要把整页扫完，否则通识选修永远只在最先扫到的几个教学班里
	// 反复尝试。
	quick bool
}

// collect 是 CollectCandidates / CandidateSource 的实现。
//
// 提前收手（quick）的规则：这次扫描已经抓过详情（didDetail）、收好的候选里
// 有一个此刻还有名额（hasVacancy）、后面的课程号还要再请求详情
// （hasDetailWork）——满足就带着已经收好的候选返回，剩下的留到下一次刷新。
// 补过详情的课程号在缓存里带着 Do_jxb_id，下次扫描到它们不会产生请求，所以
// 不需要额外记"扫到哪了"：自然从第一个还没详情的课程号接着扫。
func (e *Engine) collect(ctx context.Context, wants protocol.WishList, opt collectOptions) ([]protocol.CustomCourseDic, error) {
	if !e.Inited() {
		return nil, ErrNotInitialized
	}
	if wants.Empty() {
		wants = e.Wish()
	}
	if wants.Empty() {
		return nil, ErrNoWantedCourse
	}

	cached := e.Courses()
	if opt.refreshList || len(cached) == 0 {
		list, err := e.GetCourseList(ctx)
		if err != nil {
			return nil, err
		}
		if opt.refreshList && len(list) == 0 {
			// 列表查询没有失败、但也没给数据：保留旧缓存，让上层沿用上一次的队列。
			return nil, ErrNoCandidates
		}
		e.CacheCourseList(list)
		cached = e.Courses()
	}
	if len(cached) == 0 {
		return nil, ErrNoCandidates
	}

	groups := courseNumberGroups(wants, cached, opt.nameOnly)
	out := make([]protocol.CustomCourseDic, 0, len(cached))
	var firstErr error
	// didDetail 记录这次调用确实抓过详情：只有"省下请求"才值得提前收手。
	// 详情早就缓存好的课程号（课程页补过、上一次运行扫过）不产生请求，扫完
	// 整页也是瞬间，为了它们停下只会让新开的班晚一轮进队列。
	didDetail := false
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// 一个课程号只抓一次详情：接口返回该课程号下的全部教学班。
		if group.needsDetail {
			detail, err := e.GetCourseDetail(ctx, group.kchID)
			if err != nil {
				// 取消/登录失效/会话结束是全局性问题，不能当成"这门课没拿到详情"跳过。
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
					errors.Is(err, protocol.ErrUnauthorized) || isSessionEnd(err) {
					return nil, err
				}
				if firstErr == nil {
					firstErr = err
				}
			} else {
				e.CacheCourseDetail(detail)
				didDetail = true
			}
		}
		// 详情补齐了教师与 Do_jxb_id，命中才收；这个课程号下一个教学班都没
		// 命中就整组跳过，继续下一个课程号。
		if matched := matchedClasses(e.Courses(), wants, group.kchID, opt.nameOnly); len(matched) > 0 {
			out = append(out, matched...)
		}
		// 自动选课的快速首扫：这次扫描已经请求过详情，手里也有了一个此刻能
		// 提交的候选，后面的课程号还要再请求详情，留到下一次刷新接着扫——
		// 第一次提交不必等整页详情抓完。
		//
		// 只在整表扫描里提前收手：名字优先一次只抓点名的课程号（通常一两门），
		// 没有省头；而且这条路径已经"命中就返回"，再收手只会让同一次里本来就
		// 抓得完的第二门课白白晚一轮。周期性刷新（quick=false）把整页扫完：
		// 扫过的课程号不再产生请求，代价可控，而队列覆盖整页才能让通识选修
		// 的全部教学班都进入轮转。
		if !opt.nameOnly && opt.quick && didDetail && hasVacancy(out) && hasDetailWork(groups[i+1:]) {
			return out, nil
		}
	}
	if len(out) == 0 {
		if opt.nameOnly {
			// 名字命中的课可能一门都没拿到详情：交给调用方回落整表扫描，
			// 不能把"还没扫完"当成"选不上"。
			return nil, nil
		}
		// 详情请求全失败时，把第一个错误报出去，比"没有命中"更好排查。
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, ErrNoWantedCourse
	}
	return out, nil
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
// nameOnly 为 true 时只收课程名命中的课程号：名字优先的快速路径只抓用户点名
// 的那几门，不必为了它们把整张课表抓一遍。
func courseNumberGroups(wants protocol.WishList, list []protocol.CustomCourseDic, nameOnly bool) []courseNumber {
	index := make(map[string]int, len(list))
	groups := make([]courseNumber, 0, len(list))
	for i := range list {
		course := list[i]
		if course.Kch_id == "" || !wants.CouldMatch(course) {
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
func matchedClasses(list []protocol.CustomCourseDic, wants protocol.WishList, kchID string, nameOnly bool) []protocol.CustomCourseDic {
	out := make([]protocol.CustomCourseDic, 0, 2)
	for i := range list {
		course := list[i]
		if course.Kch_id != kchID || course.Do_jxb_id == "" || !wants.Match(course) {
			continue
		}
		if nameOnly && !wants.MatchesCourseName(course) {
			continue
		}
		out = append(out, course)
	}
	return out
}
