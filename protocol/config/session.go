package config

import "slices"

// Session 是登录过程中产生的、需要跨进程复用的状态。
//
// 它刻意不是"用户配置"：用户不该编辑它，删掉它也只是下次重新登录。
// 归属也分开——Session 的变化由 client 在登录链路里写入，但落盘由上层
// （Web 的 AccountStore）在登录结束后统一处理，client 不再自己写文件。
//
// 并发约定：写入只发生在登录 goroutine 内；读取只发生在登录结束之后
// （LoginWithContext 返回之后），因此这里不加锁，也不该被当作可变状态
// 到处传递。
type Session struct {
	// TicketJWT 是 CAS2 门户登录凭证；过期后由 client 清空。
	TicketJWT string
	// Identity 是这次登录证明的身份（学号 / 姓名 / 学校）。
	// 目前只有微信扫码能填它：扫码前不知道是谁，扫完 ticket 才知道，
	// 而登录结束到落盘之间正好需要把"是谁"传出去。
	Identity *Identity
	// LearnedRoutes 是登录过程中被服务端确认可用的 route cookie 值，
	// 最近学到的排在最后（旧值会被同一个新值取代）。
	// 它是"下次登录的候选池"，不是用户偏好；用户配置仍归 Data.Routes。
	LearnedRoutes []string
	// CorrectedBaseURL 是登录过程中 302 暴露出来的真实基地址
	// （配置里的路径少写一段时会出现）。空表示这次登录没有修正过地址。
	CorrectedBaseURL string
}

// NewSession 返回一个空的会话产物。
func NewSession() *Session { return &Session{} }

// Snapshot 返回可以安全持久化的值拷贝（切片深拷贝）。
func (s *Session) Snapshot() Session {
	if s == nil {
		return Session{}
	}
	return Session{
		TicketJWT:        s.TicketJWT,
		Identity:         s.Identity.Clone(),
		LearnedRoutes:    slices.Clone(s.LearnedRoutes),
		CorrectedBaseURL: s.CorrectedBaseURL,
	}
}

// Routes 返回候选池的副本。
func (s *Session) Routes() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.LearnedRoutes)
}

// LearnRoute 记下一条确认可用的 route：
// 先去掉同名旧值，再放到末尾，保证"最近可用"的排在最后。
func (s *Session) LearnRoute(route string) {
	if s == nil || route == "" {
		return
	}
	s.ForgetRoute(route)
	s.LearnedRoutes = append(s.LearnedRoutes, route)
}

// ForgetRoute 从候选池里移除一条 route。
// 登录过程中被新值取代的旧值用它清掉，避免候选池只增不减。
func (s *Session) ForgetRoute(route string) {
	if s == nil || route == "" {
		return
	}
	i := slices.Index(s.LearnedRoutes, route)
	if i == -1 {
		return
	}
	s.LearnedRoutes = slices.Delete(s.LearnedRoutes, i, i+1)
}

// Clear 清空会话产物（登出 / 用户手动清空缓存）。
//
// 不动 Identity：清掉的是"这次凭证"，不是"这是谁"。
func (s *Session) Clear() {
	if s == nil {
		return
	}
	s.TicketJWT = ""
	s.LearnedRoutes = nil
	s.CorrectedBaseURL = ""
}
