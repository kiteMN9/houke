package web

import (
	"context"
	"net/http"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"
	"school_sdk/protocol/config"

	"github.com/gin-gonic/gin"
)

// ---------- 其他工具 ----------
//
// 这些接口只服务本机 WebUI：个人信息、大头照、当前 Cookie、会话诊断。
// 前两者要求已登录，避免查询时静默触发登录；Cookie/诊断只读本地运行态，
// 不要求登录，也不能返回密码或 JWT 明文。

func (s *Server) toolsInfo(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !rt.engine.LoggedIn() {
		writeUnauthorized(c, "尚未登录，请先登录")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	info, err := rt.engine.UserInfo(ctx)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

func (s *Server) toolsPhoto(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !rt.engine.LoggedIn() {
		writeUnauthorized(c, "尚未登录，请先登录")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	body, contentType, err := rt.engine.UserPhoto(ctx)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, body)
}

func (s *Server) toolsCookies(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	cookies, header := rt.engine.Cookies()
	if cookies == nil {
		cookies = []protocol.CookieSnapshot{}
	}
	c.JSON(http.StatusOK, gin.H{
		"cookies":  cookies,
		"header":   header,
		"count":    len(cookies),
		"loggedIn": rt.engine.LoggedIn(),
	})
}

type toolsDebugAccount struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Username       string    `json:"username"`
	BaseURL        string    `json:"baseUrl"`
	LoginMode      string    `json:"loginMode"`
	UserAgent      string    `json:"userAgent"`
	Timeout        string    `json:"timeout"`
	LoginTimeout   string    `json:"loginTimeout"`
	Want           string    `json:"want,omitempty"`
	Hedging        bool      `json:"hedging"`
	HedgingDelay   string    `json:"hedgingDelay"`
	Routes         []string  `json:"routes"`
	HasPassword    bool      `json:"hasPassword"`
	HasCasPassword bool      `json:"hasCasPassword"`
	CreatedAt      time.Time `json:"createdAt"`
	LastLogin      time.Time `json:"lastLogin"`
}

type toolsDebugEngine struct {
	LoggedIn       bool               `json:"loggedIn"`
	AutoRelogin    bool               `json:"autoRelogin"`
	Inited         bool               `json:"inited"`
	ModeName       string             `json:"modeName"`
	LoginKind      string             `json:"loginKind"`
	CourseCacheLen int                `json:"courseCacheLen"`
	Tabs           []protocol.TabInfo `json:"tabs"`
	Identity       *config.Identity   `json:"identity"`
}

type toolsDebugSession struct {
	HasTicketJWT     bool     `json:"hasTicketJWT"`
	TicketJWTLen     int      `json:"ticketJWTLen"`
	LearnedRoutes    []string `json:"learnedRoutes"`
	CorrectedBaseURL string   `json:"correctedBaseURL"`
}

type toolsDebugSettings struct {
	BaseURL      string   `json:"baseUrl"`
	Account      string   `json:"account"`
	UserAgent    string   `json:"userAgent"`
	Timeout      string   `json:"timeout"`
	Hedging      bool     `json:"hedging"`
	HedgingDelay string   `json:"hedgingDelay"`
	Routes       []string `json:"routes"`
}

func (s *Server) toolsDebug(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	stored, ok := s.store.Get(rt.id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	acc := rt.engine.Settings()
	sess := rt.engine.Session()
	tabs := rt.engine.Tabs()
	if tabs == nil {
		tabs = []protocol.TabInfo{}
	}
	c.JSON(http.StatusOK, gin.H{
		"generatedAt": time.Now().Format(time.RFC3339),
		"account": toolsDebugAccount{
			ID:             stored.ID,
			Name:           stored.Name,
			Username:       acc.Account,
			BaseURL:        acc.BaseURL,
			LoginMode:      loginKindName(rt.engine.LoginKind()),
			UserAgent:      acc.UserAgent,
			Timeout:        acc.Timeout.String(),
			LoginTimeout:   stored.LoginTimeout,
			Want:           stored.Want,
			Hedging:        acc.Hedging,
			HedgingDelay:   acc.HedgingDelay.String(),
			Routes:         nonNilStrings(acc.Routes),
			HasPassword:    stored.Password != "",
			HasCasPassword: stored.CasPassword != "",
			CreatedAt:      stored.CreatedAt,
			LastLogin:      stored.LastLogin,
		},
		"engine": toolsDebugEngine{
			LoggedIn:       rt.engine.LoggedIn(),
			AutoRelogin:    rt.engine.AutoRelogin(),
			Inited:         rt.engine.Inited(),
			ModeName:       rt.engine.ModeName(),
			LoginKind:      loginKindName(rt.engine.LoginKind()),
			CourseCacheLen: rt.engine.CourseCacheLen(),
			Tabs:           tabs,
			Identity:       rt.engine.Identity(),
		},
		"session": toolsDebugSession{
			HasTicketJWT:     sess.TicketJWT != "",
			TicketJWTLen:     len(sess.TicketJWT),
			LearnedRoutes:    nonNilStrings(sess.LearnedRoutes),
			CorrectedBaseURL: sess.CorrectedBaseURL,
		},
		"settings": toolsDebugSettings{
			BaseURL:      acc.BaseURL,
			Account:      acc.Account,
			UserAgent:    acc.UserAgent,
			Timeout:      acc.Timeout.String(),
			Hedging:      acc.Hedging,
			HedgingDelay: acc.HedgingDelay.String(),
			Routes:       nonNilStrings(acc.Routes),
		},
	})
}

func loginKindName(kind engine.LoginKind) string {
	switch kind {
	case engine.LoginCAS:
		return "cas"
	case engine.LoginWeChat:
		return "wechat"
	default:
		return "password"
	}
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
