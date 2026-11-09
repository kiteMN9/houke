package jwmock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	cookieName    = "JSESSIONID"
	controlPrefix = "/__mock"
	maxRequests   = 200
)

type sessionState struct {
	User    string
	Revoked bool
}

type faultState struct {
	Fault
	remaining int
}

// RequestLog 是控制面里能看到的最近请求。
type RequestLog struct {
	At     time.Time `json:"at"`
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Query  string    `json:"query,omitempty"`
	Status int       `json:"status"`
}

// State 是 /__mock/state 的快照。
type State struct {
	Logins        int                 `json:"logins"`
	LoginFailures int                 `json:"loginFailures"`
	Sessions      int                 `json:"sessions"`
	Revoked       int                 `json:"revoked"`
	Chosen        []string            `json:"chosen"`
	ChosenByUser  map[string][]string `json:"chosenByUser,omitempty"`
	Enrolled      map[string]int      `json:"enrolled"`
	Scenario      Scenario            `json:"scenario"`
	ScenarioAgeMs int64               `json:"scenarioAgeMs"`
	Requests      []RequestLog        `json:"requests"`
}

// resetLocked 清空会话、选择、场景和流水，恢复到初始化状态。
func (s *Server) resetLocked() {
	s.sessions = make(map[string]*sessionState)
	s.chosen = make(map[string]map[string]bool)
	s.scenario = Scenario{WindowClosed: s.opts.WindowClosed}
	s.scenarioAt = time.Now()
	s.faults = nil
	s.requests = nil
	s.logins = 0
	s.loginFailures = 0
}

// Reset 清空所有运行状态；账号和课程目录不变。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

// SetScenario 整体替换当前场景。空字段表示恢复默认（例如 RejectAll=""）。
func (s *Server) SetScenario(sc Scenario) error {
	if sc.MaxCredits < 0 {
		return fmt.Errorf("jwmock: MaxCredits 不能为负")
	}
	if sc.MaxCourses < 0 {
		return fmt.Errorf("jwmock: MaxCourses 不能为负")
	}
	if sc.ParamExpire < 0 {
		return fmt.Errorf("jwmock: ParamExpire 不能为负")
	}
	for i, f := range sc.Faults {
		if f.PathContains == "" {
			return fmt.Errorf("jwmock: Faults[%d] 缺少 PathContains", i)
		}
		if f.DelayMs < 0 {
			return fmt.Errorf("jwmock: Faults[%d] DelayMs 不能为负", i)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenario = sc
	s.scenarioAt = time.Now()
	s.faults = make([]faultState, 0, len(sc.Faults))
	for _, f := range sc.Faults {
		remaining := f.Count
		if remaining <= 0 {
			remaining = -1 // 不限次数
		}
		s.faults = append(s.faults, faultState{Fault: f, remaining: remaining})
	}
	return nil
}

// Scenario 返回当前场景的副本。
func (s *Server) Scenario() Scenario {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneScenario(s.scenario)
}

// Snapshot 返回当前运行状态，供控制面和测试断言使用。
func (s *Server) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := State{
		Logins:        s.logins,
		LoginFailures: s.loginFailures,
		Sessions:      len(s.sessions),
		Scenario:      cloneScenario(s.scenario),
		Enrolled:      make(map[string]int, len(s.courses)),
		Requests:      append([]RequestLog(nil), s.requests...),
	}
	now := time.Now()
	if !s.scenarioAt.IsZero() {
		st.ScenarioAgeMs = now.Sub(s.scenarioAt).Milliseconds()
	}
	for _, sess := range s.sessions {
		if sess.Revoked {
			st.Revoked++
		}
	}

	chosenSet := make(map[string]bool)
	for user, set := range s.chosen {
		ids := make([]string, 0, len(set))
		for jxb := range set {
			ids = append(ids, jxb)
			chosenSet[jxb] = true
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			if st.ChosenByUser == nil {
				st.ChosenByUser = make(map[string][]string)
			}
			st.ChosenByUser[user] = ids
		}
	}
	for jxb := range chosenSet {
		st.Chosen = append(st.Chosen, jxb)
	}
	sort.Strings(st.Chosen)

	for _, seed := range s.courses {
		st.Enrolled[seed.JxbID] = s.enrolledLocked(seed)
	}
	return st
}

// RevokeSessions 把当前所有会话标记为失效，返回受影响的会话数。
func (s *Server) RevokeSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sess := range s.sessions {
		if !sess.Revoked {
			sess.Revoked = true
			n++
		}
	}
	return n
}

// ---- 以下方法都要求调用方已持有 s.mu ----

// enrolledLocked 返回教学班的当前已选人数：阶段基线 + 所有账号的选择数。
func (s *Server) enrolledLocked(seed Seed) int {
	return s.baseEnrolledLocked(seed) + s.chosenCountLocked(seed.JxbID)
}

// baseEnrolledLocked 返回场景阶段给出的已选人数基线；没有阶段命中时用种子值。
func (s *Server) baseEnrolledLocked(seed Seed) int {
	base := seed.Enrolled
	elapsed := time.Since(s.scenarioAt).Milliseconds()
	for _, phase := range s.scenario.EnrolledPhases {
		if elapsed < int64(phase.AfterMs) {
			continue
		}
		if v, ok := phase.Enrolled[seed.JxbID]; ok {
			base = v
		}
	}
	if base < 0 {
		base = 0
	}
	if base > seed.Capacity {
		base = seed.Capacity
	}
	return base
}

func (s *Server) chosenCountLocked(jxbID string) int {
	n := 0
	for _, set := range s.chosen {
		if set[jxbID] {
			n++
		}
	}
	return n
}

func (s *Server) userChosenLocked(user string) map[string]bool {
	set := s.chosen[user]
	if set == nil {
		set = make(map[string]bool)
		s.chosen[user] = set
	}
	return set
}

// creditsLocked 返回该账号已选课程的学分总和。
func (s *Server) creditsLocked(user string) float64 {
	var sum float64
	for jxb := range s.chosen[user] {
		if seed, ok := s.seedIndex[jxb]; ok {
			sum += parseCredit(seed.Xf)
		}
	}
	return sum
}

// timeConflictLocked 判断目标教学班是否与已选课程的上课时间相同。
func (s *Server) timeConflictLocked(user string, target Seed) bool {
	if target.Sksj == "" {
		return false
	}
	for jxb := range s.chosen[user] {
		seed, ok := s.seedIndex[jxb]
		if ok && seed.Sksj != "" && seed.Sksj == target.Sksj {
			return true
		}
	}
	return false
}

// sameCourseLocked 判断该账号是否已选过同一门课的其他教学班。
func (s *Server) sameCourseLocked(user string, target Seed) bool {
	for jxb := range s.chosen[user] {
		seed, ok := s.seedIndex[jxb]
		if ok && seed.KchID == target.KchID && seed.JxbID != target.JxbID {
			return true
		}
	}
	return false
}

// rejectionLocked 返回按教学班配置的固定拒绝文案。
func (s *Server) rejectionLocked(seed Seed) string {
	for _, key := range []string{"DO-" + seed.JxbID, seed.JxbID} {
		if msg, ok := s.scenario.RejectByJxb[key]; ok && msg != "" {
			return msg
		}
	}
	return ""
}

func (s *Server) takeParamExpireLocked() bool {
	if s.scenario.ParamExpire <= 0 {
		return false
	}
	s.scenario.ParamExpire--
	return true
}

// consumeFault 返回本次请求要注入的延迟和状态码，并扣减次数。
func (s *Server) consumeFault(path, rel string) (time.Duration, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.faults {
		f := &s.faults[i]
		if f.remaining == 0 {
			continue
		}
		if !strings.Contains(path, f.PathContains) && !strings.Contains(rel, f.PathContains) {
			continue
		}
		if f.remaining > 0 {
			f.remaining--
		}
		return time.Duration(f.DelayMs) * time.Millisecond, f.Status
	}
	return 0, 0
}

func (s *Server) record(c *gin.Context, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := RequestLog{
		At:     time.Now(),
		Method: c.Request.Method,
		Path:   c.Request.URL.Path,
		Query:  c.Request.URL.RawQuery,
		Status: status,
	}
	s.requests = append(s.requests, entry)
	if len(s.requests) > maxRequests {
		s.requests = s.requests[len(s.requests)-maxRequests:]
	}
}

func (s *Server) newSessionID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf[:])
}

func cloneScenario(sc Scenario) Scenario {
	out := sc
	if sc.RejectByJxb != nil {
		out.RejectByJxb = make(map[string]string, len(sc.RejectByJxb))
		for k, v := range sc.RejectByJxb {
			out.RejectByJxb[k] = v
		}
	}
	if sc.RejectNext != nil {
		out.RejectNext = make(map[string]int, len(sc.RejectNext))
		for k, v := range sc.RejectNext {
			out.RejectNext[k] = v
		}
	}
	out.Faults = append([]Fault(nil), sc.Faults...)
	if sc.EnrolledPhases != nil {
		out.EnrolledPhases = make([]EnrolledPhase, len(sc.EnrolledPhases))
		for i, p := range sc.EnrolledPhases {
			out.EnrolledPhases[i] = EnrolledPhase{AfterMs: p.AfterMs, Enrolled: make(map[string]int, len(p.Enrolled))}
			for k, v := range p.Enrolled {
				out.EnrolledPhases[i].Enrolled[k] = v
			}
		}
	}
	return out
}

func parseCredit(xf string) float64 {
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(xf), "%f", &f); err != nil {
		return 0
	}
	return f
}

func (s *Server) seedByID(jxbID string) (Seed, bool) {
	seed, ok := s.seedIndex[jxbID]
	return seed, ok
}

func normalizeJxbID(v string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "DO-"))
}

// sortedChosen 返回该账号已选教学班的副本。
func sortedChosen(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// 编译期断言：Server 必须能直接当 http.Handler 用（测试用 httptest.NewServer）。
var _ http.Handler = (*Server)(nil)
