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
// 与 CollectCandidates 的区别有两处：**每次调用都重拉课程列表**，以及第一次
// 调用如果愿望里填了课程名，先走"名字优先"——只抓课程名命中的课程号，命中就
// 立即返回。名字一门都没命中时才回落到整表扫描。
//
// 名字优先是为学校的两类选课设计的：板块课一个课程号下挂着全部教学班，一次
// 详情就能补齐，怎么算都不慢；通识选修课整页有几十个课程号、每个课程号一两个
// 教学班，全抓一遍要几十次详情（限流下约一分钟）。用户点名的课经常不在整页的
// 开头，名字优先把它从"扫完整页才能提交"变成"第一次请求就能提交"。
//
// 列表接口"成功"但没回数据（典型是加密串过期、参数被教务拒绝）时返回
// ErrNoCandidates：这不能当成"愿望没有命中"，否则 RunAuto 会把上一次的队列
// 丢掉，用户看到的就是"跑着跑着没候选了"。
func (e *Engine) CandidateSource(wants protocol.WishList) Source {
	var first atomic.Bool
	first.Store(true)
	return func(ctx context.Context) ([]protocol.CustomCourseDic, error) {
		if first.CompareAndSwap(true, false) && wants.HasCourseNames() {
			nameOnly, err := e.collect(ctx, wants, true, true)
			if err != nil {
				return nil, err
			}
			if len(nameOnly) > 0 {
				return nameOnly, nil
			}
			// 名字没有命中：列表刚刷新过，直接拿它做整表扫描。
			return e.collect(ctx, wants, false, false)
		}
		return e.collect(ctx, wants, true, false)
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
// 满员过滤与排序不在这里做：那是 RunAuto 的职责，CollectCandidates 只保证
// 候选带齐提交参数。需要"此刻最新人数"时用 CandidateSource。
func (e *Engine) CollectCandidates(ctx context.Context, wants protocol.WishList) ([]protocol.CustomCourseDic, error) {
	return e.collect(ctx, wants, false, false)
}

// collect 是 CollectCandidates / CandidateSource 的实现。
// refreshList 为 true 时无条件重拉课程列表（自动选课的周期性刷新走这条）。
// nameOnly 为 true 时只处理课程名命中的课程号，空结果不算错误——调用方要回落到整表扫描。
func (e *Engine) collect(ctx context.Context, wants protocol.WishList, refreshList, nameOnly bool) ([]protocol.CustomCourseDic, error) {
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
	if refreshList || len(cached) == 0 {
		list, err := e.GetCourseList(ctx)
		if err != nil {
			return nil, err
		}
		if refreshList && len(list) == 0 {
			// 列表查询没有失败、但也没给数据：保留旧缓存，让上层沿用上一次的队列。
			return nil, ErrNoCandidates
		}
		e.CacheCourseList(list)
		cached = e.Courses()
	}
	if len(cached) == 0 {
		return nil, ErrNoCandidates
	}

	out := make([]protocol.CustomCourseDic, 0, len(cached))
	var firstErr error
	for _, group := range courseNumberGroups(wants, cached, nameOnly) {
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
			}
		}
		// 详情补齐了教师与 Do_jxb_id，命中才收；这个课程号下一个教学班都没
		// 命中就整组跳过，继续下一个课程号。
		matched := matchedClasses(e.Courses(), wants, group.kchID, nameOnly)
		if len(matched) == 0 {
			continue
		}
		out = append(out, matched...)
	}
	if len(out) == 0 {
		if nameOnly {
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
