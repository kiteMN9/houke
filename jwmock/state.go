package jwmock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// cookieName 与真实正方一致；客户端只需要一个能带回来的会话 cookie。
const cookieName = "JSESSIONID"

// controlPrefix 是控制面前缀，不随 Options.BasePath 变化。
const controlPrefix = "/__mock"

// sessionState 是一次登录产生的会话。
type sessionState struct {
	ID      string
	User    string
	Revoked bool
}

// faultState 是 Fault 的运行态：remaining < 0 表示不限次数。
type faultState struct {
	Fault
	remaining int
}

// RequestLog 是控制面 GET /__mock/requests 返回的一条请求记录。
type RequestLog struct {
	At     time.Time `json:"at"`
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Query  string    `json:"query,omitempty"`
	Status int       `json:"status"`
}

// State 是控制面 GET /__mock/state 返回的快照。
type State struct {
	Logins        int          `json:"logins"`
	LoginFailures int          `json:"loginFailures"`
	Sessions      int          `json:"sessions"`
	Revoked       int          `json:"revoked"`
	Chosen        []string     `json:"chosen"`
	Scenario      Scenario     `json:"scenario"`
	ScenarioAgeMs int64        `json:"scenarioAgeMs"`
	Requests      []RequestLog `json:"requests"`
}

// resetLocked 把可变状态恢复到 New 时的样子；调用方必须持有 s.mu（New 除外）。
func (s *Server) resetLocked() {
	s.sessions = make(map[string]*sessionState)
	s.chosen = make(map[string]bool)
	s.scenario = Scenario{LoginMode: s.opts.LoginMode, KaptchaCode: s.opts.KaptchaCode}
	s.scenarioAt = time.Now()
	s.faults = nil
	s.requests = nil
	s.logins = 0
	s.loginFailures = 0
}

// Reset 清空会话、已选课程、请求流水和场景，恢复初始状态。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

// SetScenario 整体替换当前场景（未填 LoginMode / KaptchaCode 时沿用默认值）。
func (s *Server) SetScenario(sc Scenario) error {
	switch sc.LoginMode {
	case "":
		sc.LoginMode = s.opts.LoginMode
	case LoginNone, LoginKaptcha, LoginSlider:
	default:
		return fmt.Errorf("jwmock: 未知登录形态 %q", sc.LoginMode)
	}
	if sc.KaptchaCode == "" {
		sc.KaptchaCode = s.opts.KaptchaCode
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenario = sc
	s.scenarioAt = time.Now()
	s.faults = make([]faultState, 0, len(sc.Faults))
	for _, f := range sc.Faults {
		remaining := f.Count
		if remaining <= 0 {
			remaining = -1
		}
		s.faults = append(s.faults, faultState{Fault: f, remaining: remaining})
	}
	return nil
}

// Scenario 返回当前场景的副本。
func (s *Server) Scenario() Scenario {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scenario
}

// RevokeSessions 把所有已登录会话标记为失效，返回失效的会话数。
//
// 下一次业务请求会收到 302 到登录页（带 kickout=1），客户端据此判断被踢。
func (s *Server) RevokeSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, st := range s.sessions {
		if !st.Revoked {
			st.Revoked = true
			n++
		}
	}
	return n
}

// Snapshot 返回当前状态快照，供测试断言或控制面查看。
func (s *Server) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{
		Logins:        s.logins,
		LoginFailures: s.loginFailures,
		Sessions:      len(s.sessions),
		Scenario:      s.scenario,
		ScenarioAgeMs: time.Since(s.scenarioAt).Milliseconds(),
		Chosen:        make([]string, 0, len(s.chosen)),
		Requests:      append([]RequestLog(nil), s.requests...),
	}
	for _, sess := range s.sessions {
		if sess.Revoked {
			st.Revoked++
		}
	}
	for id := range s.chosen {
		st.Chosen = append(st.Chosen, id)
	}
	return st
}

// loginModeLocked 返回当前生效的登录形态；调用方持有 s.mu。
func (s *Server) loginModeLocked() LoginMode {
	if s.scenario.LoginMode != "" {
		return s.scenario.LoginMode
	}
	return s.opts.LoginMode
}

// kaptchaCodeLocked 返回当前生效的字符验证码；调用方持有 s.mu。
func (s *Server) kaptchaCodeLocked() string {
	if s.scenario.KaptchaCode != "" {
		return s.scenario.KaptchaCode
	}
	return s.opts.KaptchaCode
}

// sessionLocked 从请求 cookie 里解析出仍然有效的会话；调用方持有 s.mu。
func (s *Server) sessionLocked(r *http.Request) *sessionState {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	st := s.sessions[c.Value]
	if st == nil || st.Revoked {
		return nil
	}
	return st
}

// loggedIn 报告请求是否带着有效会话。
func (s *Server) loggedIn(r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionLocked(r) != nil
}

// requireLogin 在未登录时写 302 并返回 false。
//
// 客户端把“302 且 Location 含登录页”判定为掉线，因此这里必须真的跳登录页，
// 不能靠 200 + 错误文案糊弄。
func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if s.loggedIn(r) {
		return true
	}
	s.kick(w)
	return false
}

// kick 模拟“会话失效/被踢”：302 到登录页，带 kickout=1。
func (s *Server) kick(w http.ResponseWriter) {
	w.Header().Set("Location", s.opts.BasePath+"/xtgl/login_slogin.html?kickout=1")
	w.WriteHeader(http.StatusFound)
}

// redirect 写 302 到 BasePath 下的某个相对路径。
func (s *Server) redirect(w http.ResponseWriter, rel string) {
	w.Header().Set("Location", s.opts.BasePath+rel)
	w.WriteHeader(http.StatusFound)
}

// enrolledLocked 返回某教学班此刻的已选人数。
//
// 先取 Seed.Enrolled 或最近一个已生效的 EnrolledPhase 值，再加上本进程里
// 真正选上的人数。调用方持有 s.mu。
func (s *Server) enrolledLocked(seed Seed) int {
	base := seed.Enrolled
	since := time.Since(s.scenarioAt)
	for _, p := range s.scenario.EnrolledPhases {
		if since < time.Duration(p.AfterMs)*time.Millisecond {
			continue
		}
		if v, ok := p.Enrolled[seed.JxbID]; ok {
			base = v
		}
	}
	if s.chosen[seed.JxbID] {
		base++
	}
	if base < 0 {
		base = 0
	}
	return base
}

// takeParamExpire 消耗一次“参数过期”注入。
func (s *Server) takeParamExpire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scenario.ParamExpire <= 0 {
		return false
	}
	s.scenario.ParamExpire--
	return true
}

// consumeFault 找出第一个命中的故障并消耗一次；返回延迟与状态码。
func (s *Server) consumeFault(rel string) (time.Duration, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.faults {
		f := &s.faults[i]
		if f.remaining == 0 {
			continue
		}
		if f.PathContains != "" && !strings.Contains(rel, f.PathContains) {
			continue
		}
		if f.remaining > 0 {
			f.remaining--
		}
		return time.Duration(f.DelayMs) * time.Millisecond, f.Status
	}
	return 0, 0
}

// record 追加一条请求流水（只保留最近 200 条）。
func (s *Server) record(r *http.Request, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, RequestLog{
		At:     time.Now(),
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Status: status,
	})
	if len(s.requests) > 200 {
		s.requests = s.requests[len(s.requests)-200:]
	}
}

// newSessionID 生成不可猜的会话 id。
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// writeJSON 写 UTF-8 JSON。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeHTML 写 UTF-8 HTML。
func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// writeText 写纯文本。
func writeText(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
