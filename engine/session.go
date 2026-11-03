package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"school_sdk/protocol"
)

// 会话被 Reset（登出 / force 刷新）后，正在进行中的初始化结果作废。
var ErrSessionReset = errors.New("engine: 会话已重置")

// 页签下标越界。
var errBadTab = errors.New("engine: 无效的页签下标")

// errRenewTooSoon 表示冷静期内跳过了重抓参数。
var errRenewTooSoon = errors.New("engine: 选课参数刚刷新过")

// errTabGone 表示刷新参数后原来的页签不见了（教务撤掉了这一轮）。
// 此时不发布新配置：否则自动选课会拿另一个页签的课程集合继续抢。
var errTabGone = errors.New("engine: 原页签已不在刷新后的参数里")

// renewCooldown 是同一会话两次重抓选课参数之间的冷静期。
//
// 参数过期后重抓一次通常就好了；如果服务端持续回“加密串错误”，问题多半不在
// 参数上（账号被限制、轮次被关掉），这时候每个请求都去抓一次首页只会放大
// 请求量，还会把选课接口的提交节奏拖垮。
const renewCooldown = 10 * time.Second

// session 是"一次教务系统会话"的运行时状态：
//   - 一次初始化（GetIndexParams + GetCourseListPre）后的 APIConfig 快照
//   - 该快照下的选课/查询方法封装
//   - 课程内存缓存与愿望清单
//
// 它放在 engine 而不是 protocol 里，因为这些都是"会话语义"——选择什么时候
// 初始化、缓存什么、切换页签后保留什么，而不是"怎么发一次请求、怎么解析"。
// protocol 只提供无状态的请求函数和配置值对象。
//
// 并发约定（Web 多账号 + 多请求并发，这一层必须是"读永远不被写挡住"）：
//   - 初始化只串行化自己（initMu），并且在做网络 IO 时**不持有**状态锁；
//     抓完参数后，在最后一步把新的 *protocol.APIConfig 一次性发布出去。
//   - 只读接口（Inited / ModeName / Tabs）只做 RLock + 拷贝，
//     所以 /status 这种每几百毫秒调一次的接口不会被一次 10 分钟的初始化挂住。
//   - 每个请求拿到的是一份 cfg 拷贝，请求内部对 cfg 的写入（needInit 之类的
//     调试开关）不会回写 session，也不会和别的请求互相踩。
type session struct {
	// initMu 串行化 Initialize / Renew：同一会话同一时刻只允许一次参数抓取。
	// 它不保护任何被读取的状态，持有它期间可以做网络 IO。
	initMu sync.Mutex
	// lastRenew 是上一次重抓参数的时间，只由 initMu 保护（见 Renew 的冷静期）。
	lastRenew time.Time

	// mu 只保护下面已发布的快照；持锁路径不允许做网络 IO。
	mu         sync.RWMutex
	cfg        *protocol.APIConfig
	generation uint64 // Reset 递增，用来丢弃"重置之前发起"的初始化结果

	// wish 是高级选课的愿望清单：由上层（Web 配置 / Plan）设置，
	// 跨 Reset 保留，并随每次 snapshot 覆盖到 cfg 副本上。
	wish protocol.WishList

	// inited 用原子读，/status 这类热路径连 RLock 都不需要。
	inited atomic.Bool

	client *protocol.APIClient

	// 课程内存缓存（对应 CLI 里的 sCustL）。
	// 一把锁保护一个字段：合并时整段持写锁，读时持读锁拷贝一份再返回。
	// 不把底层切片交给外部，调用方拿到的永远是快照。
	cacheMu     sync.RWMutex
	courseCache []protocol.CustomCourseDic
}

func newSession(c *protocol.APIClient) *session {
	return &session{
		client:      c,
		cfg:         &protocol.APIConfig{},
		courseCache: make([]protocol.CustomCourseDic, 0),
	}
}

// Inited 返回是否已经初始化过。
// 初始化进行中时返回 false（旧参数已作废、新参数尚未发布）。
func (s *session) Inited() bool {
	return s.inited.Load()
}

// snapshot 返回当前已发布配置的副本。
// 请求方拿到后独占这份副本，整个请求期间都不需要再持有 session 的锁。
//
// 愿望清单不进快照：它是 engine 的选课策略配置，protocol 的请求函数不读它；
// 快照只带"发请求需要什么"，避免同一份清单在 session 和 cfg 里各存一份。
func (s *session) snapshot() *protocol.APIConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return &protocol.APIConfig{}
	}
	c := *s.cfg
	return &c
}

// SetWish 设置高级选课用的愿望清单。
//
// 它写入的是会话级字段而不是当前 cfg 快照：初始化和切换页签会重建 cfg，
// 愿望清单不应该因此丢失；Reset 也保留。
func (s *session) SetWish(w protocol.WishList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wish = w.Clone()
}

// Wish 返回当前愿望清单的副本。
func (s *session) Wish() protocol.WishList {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.wish.Clone()
}

// Initialize 拉取公开参数 + 课程列表所需参数，填充 session 的配置。
//
// 已初始化时直接返回 nil；ctx 取消会立即返回（不会发布半成品）。
// 抓参数期间 /status、/tabs 等只读接口照常返回，不会阻塞。
func (s *session) Initialize(ctx context.Context) error {
	// 同一会话只允许一次初始化；第二次调用会在第一次结束后直接命中 Inited。
	s.initMu.Lock()
	defer s.initMu.Unlock()

	if s.Inited() {
		return nil
	}

	// 在私有副本上抓参数：抓取期间读接口看到的仍是"未初始化"，
	// 而不是一份写了一半的 cfg。
	cfg := s.snapshot()

	s.mu.RLock()
	gen := s.generation
	s.mu.RUnlock()

	if err := s.client.GetIndexParams(ctx, cfg, protocol.IndexOptions{}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.client.GetCourseListPre(ctx, cfg, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 发布：只有走到这一步，其他 goroutine 才能看到新配置。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != gen {
		// 抓参数期间发生过 Reset（登出 / force 刷新），这次结果已经过期。
		return ErrSessionReset
	}
	s.cfg = cfg
	s.inited.Store(true)
	return nil
}

// Reset 清空初始化状态，下一次 Initialize 会重新抓取。
//
// 它不等待正在进行的初始化：只把 generation 加一，让那次初始化在发布前
// 发现结果已过期。这样登出不会被 10 分钟的初始化拖住。
func (s *session) Reset() {
	s.mu.Lock()
	s.generation++
	s.cfg = &protocol.APIConfig{}
	s.inited.Store(false)
	s.mu.Unlock()

	s.ClearCourseCache()
}

// Renew 重新抓取选课参数（首页 + 列表前置），不清会话、不清课程缓存。
//
// 用途：长时间运行的自动选课撞上正方的“加密串错误，可以清除浏览器缓存后刷新
// 网页重试！”——浏览器里的“刷新网页”就是重新 GET 一次选课首页，把 xkkz_id /
// xkkz_xh 换成新值。和 Initialize 的区别：
//   - 不判断 Inited：已经初始化过也照抓（这正是它的用途）；
//   - 保留当前页签：首页参数解析会把页签重置成页面默认值（parseYzbIndexHtml），
//     不切回去的话，跑在通识选修上的任务会悄悄变成主修课；
//   - 不清课程缓存：下一轮列表刷新本来就会覆盖人数。
//
// 冷静期内重复调用返回 errRenewTooSoon；抓取失败不发布任何配置，
// 调用方继续用旧快照，把原错误交给上层。
func (s *session) Renew(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()

	now := time.Now()
	if !s.lastRenew.IsZero() && now.Sub(s.lastRenew) < renewCooldown {
		return errRenewTooSoon
	}
	s.lastRenew = now

	old := s.snapshot()

	s.mu.RLock()
	gen := s.generation
	s.mu.RUnlock()

	// 在私有副本上抓参数：抓取期间其他请求继续用旧快照，不会看到写了一半的配置。
	next := *old
	if err := s.client.GetIndexParams(ctx, &next, protocol.IndexOptions{}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 首页参数只带“默认页签”，把用户原来选中的那个切回来（用刷新后的 xkkz_id）。
	if tab, ok := protocol.CurrentTabOf(old); ok {
		fresh, found := findTab(protocol.TabsOf(&next), tab)
		if !found {
			return errTabGone
		}
		next = *protocol.WithTab(&next, fresh)
	}
	if err := s.client.GetCourseListPre(ctx, &next, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 发布：只有走到这一步，其他 goroutine 才能看到新参数。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != gen {
		// 抓参数期间发生过 Reset（登出 / force 刷新），这次结果已经过期。
		return ErrSessionReset
	}
	s.cfg = &next
	s.inited.Store(true)
	return nil
}

// findTab 在刷新后的页签列表里找“原来那个”：先按课程类型代码，再退化成名字。
func findTab(tabs []protocol.TabInfo, want protocol.TabInfo) (protocol.TabInfo, bool) {
	for _, tab := range tabs {
		if want.Kklxdm != "" && tab.Kklxdm == want.Kklxdm {
			return tab, true
		}
	}
	for _, tab := range tabs {
		if want.Kklxmc != "" && tab.Kklxmc == want.Kklxmc {
			return tab, true
		}
	}
	return protocol.TabInfo{}, false
}

// ModeName 返回当前页签名（主要用于调试/展示）。
func (s *session) ModeName() string {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	return protocol.ModeNameOf(cfg)
}

// Tabs 返回所有可选页签（如果页面上有）。
func (s *session) Tabs() []protocol.TabInfo {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	return protocol.TabsOf(cfg)
}

// Window 返回当前选课时间窗（未初始化时为零值）。
func (s *session) Window() protocol.WindowInfo {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	return protocol.WindowOf(cfg)
}

// SelectTab 切换到指定页签，并重新拉取该页签下的选课参数。
//
// 和 Initialize 一样，参数抓取在私有副本上完成，成功后才发布；
// 抓取期间其他请求继续用旧快照，不会看到写了一半的配置。
func (s *session) SelectTab(ctx context.Context, idx int) error {
	s.mu.RLock()
	cfg := s.cfg
	gen := s.generation
	s.mu.RUnlock()

	tabs := protocol.TabsOf(cfg)
	if idx < 0 || idx >= len(tabs) {
		return errBadTab
	}
	next := protocol.WithTab(cfg, tabs[idx])

	s.ClearCourseCache() // 页签变了，课程集合必然不同

	// 拉取该页签下的参数
	if err := s.client.GetCourseListPre(ctx, next, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != gen {
		return ErrSessionReset
	}
	s.cfg = next
	return nil
}

// GetCourseList 获取当前页签下的课程列表。
func (s *session) GetCourseList(ctx context.Context) ([]protocol.CourseListDic, error) {
	return s.client.GetCourseList(ctx, s.snapshot())
}

// GetCourseDetail 获取某门课程所有教学班详情。
func (s *session) GetCourseDetail(ctx context.Context, kchId string) ([]protocol.CourseDetail, error) {
	return s.client.GetCourseDetail(ctx, s.snapshot(), kchId)
}

// ---------- 课程内存缓存 ----------

// Courses 返回缓存的课程列表副本。
func (s *session) Courses() []protocol.CustomCourseDic {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	out := make([]protocol.CustomCourseDic, len(s.courseCache))
	copy(out, s.courseCache)
	return out
}

// CourseCacheLen 返回缓存条目数。
func (s *session) CourseCacheLen() int {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return len(s.courseCache)
}

// ClearCourseCache 清空课程缓存。
// 读路径拿到的都是副本，这里直接置空即可。
func (s *session) ClearCourseCache() {
	s.cacheMu.Lock()
	s.courseCache = nil
	s.cacheMu.Unlock()
}

// CacheCourseList 合并课程列表（同 jxb_id 刷新字段，新条目追加）。
func (s *session) CacheCourseList(list []protocol.CourseListDic) {
	if len(list) == 0 {
		return
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.courseCache = protocol.MergeCourseList(s.courseCache, list)
}

// CacheCourseDetail 合并教学班详情。
func (s *session) CacheCourseDetail(list []protocol.CourseDetail) {
	if len(list) == 0 {
		return
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.courseCache = protocol.MergeCourseDetail(s.courseCache, list)
}

// Choose 执行一次选课。
func (s *session) Choose(ctx context.Context, co *protocol.CustomCourseDic) (protocol.ChooseCourseResult, error) {
	snapshot := s.snapshot()
	return s.client.ChooseCourse(ctx, snapshot.Index, snapshot.Window, co)
}

// Quit 退课。
func (s *session) Quit(ctx context.Context, jxbId, kchId string) (bool, string, error) {
	return s.client.QuitCourse(ctx, s.snapshot().Index, jxbId, kchId)
}

// Selected 已选课程列表。
func (s *session) Selected(ctx context.Context, xkxnm, xkxqm string) ([]protocol.ChosenDic, error) {
	return s.client.GetSelected(ctx, xkxnm, xkxqm)
}
