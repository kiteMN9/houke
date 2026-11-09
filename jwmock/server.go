package jwmock

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"school_sdk/protocol/config"
)

// Server 是一个本地模拟的正方教务 HTTP 服务。
//
// 它实现了 http.Handler，可以直接挂到 httptest.NewServer 或 http.ListenAndServe
// 上；业务接口路径与真实教务一致（默认带 /jwglxt 前缀），另外提供 /__mock/
// 控制面用于切换场景、踢会话、查看状态。
type Server struct {
	opts Options
	key  *rsa.PrivateKey

	kaptchaPNG []byte
	sliderPNG  []byte

	mu            sync.Mutex
	sessions      map[string]*sessionState
	chosen        map[string]bool
	scenario      Scenario
	scenarioAt    time.Time
	faults        []faultState
	requests      []RequestLog
	logins        int
	loginFailures int
}

// New 创建模拟服务并生成登录所需的 RSA 密钥。
func New(opts Options) (*Server, error) {
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("jwmock: 生成 RSA 密钥失败: %w", err)
	}
	s := &Server{
		opts:       o,
		key:        key,
		kaptchaPNG: makeKaptchaPNG(),
		sliderPNG:  makeSliderPNG(),
	}
	s.resetLocked()
	return s, nil
}

// BasePath 返回业务路径前缀（结束时带 /）。
func (s *Server) BasePath() string { return s.opts.BasePath }

// Options 返回生效的配置副本。
func (s *Server) Options() Options { return s.opts }

// ServeHTTP 实现控制面 + 业务路由 + 故障注入。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, controlPrefix) {
		s.handleControl(w, r)
		return
	}
	base := s.opts.BasePath
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, base)
	if rel == "" {
		rel = "/"
	}

	// 故障注入：先睡延迟，再按配置直接回错误状态码。
	if delay, status := s.consumeFault(rel); delay > 0 || status >= 400 {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if status >= 400 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, "jwmock 注入的 HTTP %d", status)
			s.record(r, status)
			return
		}
	}

	rec := &statusRecorder{ResponseWriter: w}
	s.route(rec, r, rel)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	s.record(r, rec.status)
}

// statusRecorder 记录 handler 实际写出的状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.status == 0 {
		sr.status = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(p []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(p)
}

// route 按正方端点分发。rel 已去掉 BasePath 前缀。
func (s *Server) route(w http.ResponseWriter, r *http.Request, rel string) {
	switch rel {
	case config.LoginIndex:
		if r.Method == http.MethodPost {
			s.handlePostLogin(w, r)
		} else {
			s.handleLoginPage(w, r)
		}
	case config.PublicKey:
		s.handlePublicKey(w, r)
	case config.KAPTCHA:
		s.handleKaptcha(w, r)
	case config.CAPTCHA:
		s.handleCaptcha(w, r)
	case config.MENU:
		s.handleMenu(w, r)
	case config.LoginStatus:
		s.handleLoginStatus(w, r)
	case config.StudentName:
		s.handleStudentName(w, r)
	case config.LOGOUT:
		s.handleLogout(w, r)

	case config.ChooseCourseIndex:
		s.handleChooseCourseIndex(w, r)
	case config.ChooseCourseTabParams:
		s.handleChooseCourseTabParams(w, r)
	case config.ChooseCourseCourseList:
		s.handleChooseCourseCourseList(w, r)
	case config.ChooseCourseCourseDetail:
		s.handleChooseCourseCourseDetail(w, r)
	case config.ChooseCourse:
		s.handleChooseCourse(w, r)
	case config.CourseSelectedList:
		s.handleCourseSelectedList(w, r)
	case config.QuitCourse:
		s.handleQuitCourse(w, r)
	case config.ChooseSimpleDoJxb:
		s.handleSimpleDoJxb(w, r)
	case config.CourseRegistered:
		s.handleCourseRegistered(w, r)

	default:
		http.NotFound(w, r)
	}
}

// handleMenu 是登录后的首页占位。
func (s *Server) handleMenu(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	writeHTML(w, menuPage())
}

// handleLoginStatus 供 CheckSession 使用：未登录时 302 到登录页。
func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	if !s.loggedIn(r) {
		s.kick(w)
		return
	}
	writeText(w, "1")
}

// handleStudentName 供 CheckSession2 使用；正常返回 200 即可。
func (s *Server) handleStudentName(w http.ResponseWriter, r *http.Request) {
	if !s.loggedIn(r) {
		s.kick(w)
		return
	}
	writeHTML(w, `<div id="xm">模拟学生</div>`+
		`<input type="hidden" id="yhm" value="`+s.opts.Username+`">`)
}

// handleLogout 让当前会话失效。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if sess := s.sessionLocked(r); sess != nil {
		sess.Revoked = true
	}
	s.mu.Unlock()
	s.redirect(w, config.LoginIndex)
}
