package engine

import (
	"context"
	"errors"
	"sort"
	"strings"

	"school_sdk/protocol"
)

// ErrNotInitialized 表示会话还没完成 Initialize：没有选课参数，也就没法抓课程。
// Web 层把它翻成 400「请先调用 /init 完成初始化」。
var ErrNotInitialized = errors.New("engine: 会话尚未初始化")

// CandidateSource 返回绑定在本引擎课程缓存上的候选源（见 Source）。
//
// 与 CollectCandidates 的区别只有一点：**每次调用都重拉课程列表**。自动选课
// 一跑几小时，已选人数和新开的教学班只有重新查列表才看得到（详情缓存里只有
// Do_jxb_id，人数以列表为准）。拉回来的列表会合并进缓存，所以课程页也跟着更新。
//
// 列表接口"成功"但没回数据（典型是加密串过期、参数被教务拒绝）时返回
// ErrNoCandidates：这不能当成"愿望没有命中"，否则 RunAuto 会把上一次的队列
// 丢掉，用户看到的就是"跑着跑着没候选了"。
func (e *Engine) CandidateSource(wants protocol.WishList) Source {
	return func(ctx context.Context) ([]protocol.CustomCourseDic, error) {
		return e.collectCandidates(ctx, wants, true)
	}
}

// CollectCandidates 组装自动选课的候选课程。
//
// 课程列表只带教学班与人数，提交选课需要的 Do_jxb_id 必须从课程详情里拿
// （详情按 kch_id 请求，返回该课程下所有教学班）。因此这里做三件事：
//
//  1. 缓存为空时先拉一次列表（Web 的列表页通常已经缓过，直接复用）；
//  2. 对可能命中愿望、且还没拿到 Do_jxb_id 的课程，按优先级逐个拉详情并合并进缓存；
//  3. 回读缓存，按愿望清单过滤出"能提交"的候选。
//
// 满员过滤与排序不在这里做：那是 RunAuto 的职责，CollectCandidates 只保证
// 候选带齐提交参数。需要"此刻最新人数"时用 CandidateSource。
func (e *Engine) CollectCandidates(ctx context.Context, wants protocol.WishList) ([]protocol.CustomCourseDic, error) {
	return e.collectCandidates(ctx, wants, false)
}

// collectCandidates 是 CollectCandidates / CandidateSource 的实现。
// refreshList 为 true 时无条件重拉课程列表（自动选课的周期性刷新走这条）。
func (e *Engine) collectCandidates(ctx context.Context, wants protocol.WishList, refreshList bool) ([]protocol.CustomCourseDic, error) {
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

	var firstErr error
	for _, kchID := range detailTargets(wants, cached) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		detail, err := e.GetCourseDetail(ctx, kchID)
		if err != nil {
			// 取消/登录失效是全局性问题，不能当成"这门课没拿到详情"跳过。
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, protocol.ErrUnauthorized) {
				return nil, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		e.CacheCourseDetail(detail)
	}

	out := make([]protocol.CustomCourseDic, 0, len(cached))
	for _, course := range e.Courses() {
		if course.Do_jxb_id == "" || !wants.Match(course) {
			continue
		}
		out = append(out, course)
	}
	if len(out) == 0 {
		// 详情请求全失败时，把第一个错误报出去，比"没有命中"更好排查。
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, ErrNoWantedCourse
	}
	return out, nil
}

// detailTargets 返回需要拉详情的 kch_id，按愿望优先级排序。
//
// 先抓最想要的课：自动选课经常在详情阶段被取消/超时，优先保证第一志愿可用。
// 同一门课只请求一次，且已经有 Do_jxb_id 的跳过。
func detailTargets(wants protocol.WishList, list []protocol.CustomCourseDic) []string {
	type target struct {
		kchID string
		rank  int
	}
	seen := make(map[string]bool, len(list))
	targets := make([]target, 0, len(list))
	for i := range list {
		course := list[i]
		if course.Kch_id == "" || course.Do_jxb_id != "" || seen[course.Kch_id] {
			continue
		}
		if !matchesLoosely(wants, course) {
			continue
		}
		seen[course.Kch_id] = true
		targets = append(targets, target{kchID: course.Kch_id, rank: wants.Rank(course.Jxbmc)})
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].rank < targets[j].rank })

	out := make([]string, len(targets))
	for i := range targets {
		out[i] = targets[i].kchID
	}
	return out
}

// matchesLoosely 是抓详情之前的粗筛。
//
// 列表里还没有教师字段（Jsxx 来自详情），所以只填教师/类型的愿望必须放行；
// 填了课程名时先按名字筛，避免为了几门课把整张课表都请求一遍详情。
func matchesLoosely(wants protocol.WishList, course protocol.CustomCourseDic) bool {
	if len(wants.WantClassList) == 0 {
		return true
	}
	for _, class := range wants.WantClassList {
		class = strings.TrimSpace(class)
		if class == "" {
			continue
		}
		if strings.Contains(course.Jxbmc, class) || strings.Contains(course.Kcmc, class) {
			return true
		}
	}
	return false
}
