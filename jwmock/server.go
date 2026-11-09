package jwmock

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"school_sdk/protocol/config"
)

// Server 是本地正方教务模拟服务。它实现 http.Handler，可以直接 httptest.NewServer。
type Server struct {
	opts      Options
	key       *rsa.PrivateKey
	seedIndex map[string]Seed
	router    *gin.Engine

	mu            sync.Mutex
	sessions      map[string]*sessionState
	chosen        map[string]map[string]bool // username -> jxb set
	scenario      Scenario
	scenarioAt    time.Time
	faults        []faultState
	requests      []RequestLog
	logins        int
	loginFailures int
}

// New 构造一个模拟教务服务。
func New(opts Options) (*Server, error) {
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("jwmock: 生成 RSA 密钥: %w", err)
	}
	s := &Server{
		opts:      o,
		key:       key,
		seedIndex: make(map[string]Seed, len(o.Courses)),
	}
	for _, seed := range o.Courses {
		s.seedIndex[seed.JxbID] = seed
	}
	s.resetLocked()

	gin.SetMode(gin.ReleaseMode)
	s.router = s.buildRouter()
	return s, nil
}

// BasePath 返回业务前缀（例如 /jwglxt）。
func (s *Server) BasePath() string { return s.opts.BasePath }

// Handler 返回底层 http.Handler。
func (s *Server) Handler() http.Handler { return s.router }

// ServeHTTP 让 Server 直接满足 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) buildRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	base := s.opts.BasePath
	business := r.Group(base)
	business.Use(s.faultMiddleware())

	// 公开接口：登录页、登录提交、RSA 公钥。
	business.GET(config.LoginIndex, s.handleLoginPage)
	business.POST(config.LoginIndex, s.handleLoginSubmit)
	business.GET(config.PublicKey, s.handlePublicKey)

	// 登录后的业务接口。
	priv := business.Group("")
	priv.Use(s.authRequired())
	priv.GET(config.MENU, s.handleMenu)
	priv.GET(config.LoginStatus, s.handleLoginStatus)
	priv.GET(config.StudentName, s.handleStudentName)
	priv.GET(config.LOGOUT, s.handleLogout)
	priv.GET(config.ChooseCourseIndex, s.handleIndex)
	priv.POST(config.ChooseCourseTabParams, s.handleTabParams)
	priv.POST(config.ChooseCourseCourseList, s.handleCourseList)
	priv.POST(config.ChooseCourseCourseDetail, s.handleCourseDetail)
	priv.POST(config.ChooseCourse, s.handleChoose)
	priv.POST(config.CourseSelectedList, s.handleSelected)
	priv.POST(config.QuitCourse, s.handleQuit)
	priv.GET(config.ChooseSimpleDoJxb, s.handleSimple)
	priv.POST(config.ChooseSimpleDoJxb, s.handleSimple)
	priv.GET(config.CourseRegistered, s.handleRegistered)
	priv.POST(config.CourseRegistered, s.handleRegistered)

	// 控制面固定挂在 /__mock 下，不受 BasePath 影响，也不参与故障注入。
	ctrl := r.Group(controlPrefix)
	ctrl.GET("/state", s.handleState)
	ctrl.GET("/requests", s.handleRequests)
	ctrl.POST("/scenario", s.handleScenario)
	ctrl.POST("/reset", s.handleReset)
	ctrl.POST("/revoke", s.handleRevoke)

	return r
}

// faultMiddleware 按路径注入延迟/状态码，并记录所有业务请求。
func (s *Server) faultMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		full := c.Request.URL.Path
		rel := strings.TrimPrefix(full, s.opts.BasePath)
		delay, status := s.consumeFault(full, rel)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-c.Request.Context().Done():
				timer.Stop()
				c.Abort()
				return
			}
		}
		if status >= 400 {
			c.Data(status, "text/plain; charset=utf-8", []byte(fmt.Sprintf("jwmock injected HTTP %d", status)))
			s.record(c, status)
			c.Abort()
			return
		}
		c.Next()
		s.record(c, c.Writer.Status())
	}
}

// authRequired 对未登录/已被踢的请求返回 302 kickout=1，客户端据此走重登链路。
func (s *Server) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := s.userOf(c); ok {
			c.Next()
			return
		}
		s.kick(c)
		c.Abort()
	}
}

func (s *Server) userOf(c *gin.Context) (string, bool) {
	cookie, err := c.Cookie(cookieName)
	if err != nil || cookie == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[cookie]
	if !ok || sess.Revoked {
		return "", false
	}
	return sess.User, true
}

func (s *Server) kick(c *gin.Context) {
	c.Redirect(http.StatusFound, s.opts.BasePath+config.LoginIndex+"?kickout=1")
}
