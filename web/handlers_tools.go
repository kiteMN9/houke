package web

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"
	"school_sdk/protocol/config"

	"github.com/gin-gonic/gin"
)

// ---------- 其他工具 ----------
//
// 这些接口只服务本机 WebUI：个人信息、大头照、当前 Cookie、会话诊断。
// 前两者需要登录：未登录且开着自动重登时先跑一次可见登录（见 ensureLogin），
// 不静默阻塞在请求里；Cookie/诊断只读本地运行态，不要求登录，也不能返回
// 密码或 JWT 明文。

func (s *Server) toolsInfo(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !s.ensureLogin(c, c.Param("id"), rt) {
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
	if !s.ensureLogin(c, c.Param("id"), rt) {
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

// toolsJSFiles 是诊断页要展示的三份自主选课脚本。
// 路径取 config 里的端点常量（不要在这里写死字面量），别名与仓库 html/
// 下的本地快照同名，这样远程哈希和本地哈希能直接对上号。
var toolsJSFiles = []struct {
	Name string
	Path string
}{
	{"zzxkYzb.js", config.JSzzxkYzb},
	{"zzxkYzbZy.js", config.JSzzxkYzbZy},
	{"zzxkYzbChoosedZy.js", config.JSzzxkYzbChoosedZy},
}

// jsSnapshotDir 是本地快照目录（相对进程工作目录）。仓库里是 html/；
// 单测会把它指到 t.TempDir()，否则跑一次测试就往 repo 里写三份脚本。
var jsSnapshotDir = "html"

type toolsJSHashFile struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	OK          bool   `json:"ok"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Size        int    `json:"size"`
	SHA1        string `json:"sha1,omitempty"`
	LocalFound  bool   `json:"localFound"`
	LocalSize   int    `json:"localSize,omitempty"`
	LocalSHA1   string `json:"localSha1,omitempty"`
	LocalMatch  bool   `json:"localMatch"`
	Saved       bool   `json:"saved"` // 本次把抓到的内容写成了本地快照
	Error       string `json:"error,omitempty"`
	SaveError   string `json:"saveError,omitempty"`
}

// toolsJSHash 用当前会话抓三份自主选课脚本并算 sha1，顺带和本地快照比对。
//
// 正方换掉选课页的 JS 时，本工具的选择器与参数推断可能失效。先把"现在学校
// 发的是哪一份"变成一串哈希：用户报障时报哈希就够定位版本，作者本机还能
// 直接和 html/ 快照比对。
//
// 静态脚本本身是公开资源，**不要求登录**（没会话也能算）；只是学校若把脚本
// 请求拦到登录页，这里按"这一份没拿到"记一笔，不把整次请求判成掉线。
//
// 本地没有副本时顺手存一份（下次就能比对了）；已有副本**不覆盖**——"已变化"
// 正是要看的信息，要更新基准得显式点「存为快照」走 toolsJSHashSave。
func (s *Server) toolsJSHash(c *gin.Context) {
	s.toolsJSHashRun(c, false)
}

// toolsJSHashSave 抓一遍并把三份脚本全部写成本地快照（覆盖已有副本），
// 用于学校更新 JS 后主动把比对基准跟上去。
func (s *Server) toolsJSHashSave(c *gin.Context) {
	s.toolsJSHashRun(c, true)
}

func (s *Server) toolsJSHashRun(c *gin.Context, overwrite bool) {
	c.Header("Cache-Control", "no-store")
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	files := make([]toolsJSHashFile, 0, len(toolsJSFiles))
	saved := 0
	for _, f := range toolsJSFiles {
		item := toolsJSHashFile{Name: f.Name, Path: f.Path}
		if snapshot, ok := readJSSnapshot(f.Name); ok {
			item.LocalFound = true
			item.LocalSize = len(snapshot)
			item.LocalSHA1 = sha1Hex(snapshot)
		}
		asset, err := rt.engine.FetchStatic(ctx, f.Path)
		if err != nil {
			item.Error = err.Error()
			if errors.Is(err, protocol.ErrUnauthorized) {
				item.Error = "学校返回了登录页，可能要求先登录才能取到脚本"
			}
			files = append(files, item)
			continue
		}
		item.OK = true
		item.Status = asset.Status
		item.ContentType = asset.ContentType
		item.Size = len(asset.Body)
		item.SHA1 = sha1Hex(asset.Body)
		item.LocalMatch = item.LocalFound && item.LocalSHA1 == item.SHA1

		if overwrite || !item.LocalFound {
			if err := writeJSSnapshot(f.Name, asset.Body); err != nil {
				item.SaveError = err.Error()
			} else {
				item.Saved = true
				item.LocalFound = true
				item.LocalMatch = true
				item.LocalSize = item.Size
				item.LocalSHA1 = item.SHA1
				saved++
			}
		}
		files = append(files, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"generatedAt": time.Now().Format(time.RFC3339),
		"snapshotDir": jsSnapshotDir,
		"overwritten": overwrite,
		"saved":       saved,
		"files":       files,
	})
}

func sha1Hex(body []byte) string {
	sum := sha1.Sum(body)
	return hex.EncodeToString(sum[:])
}

// readJSSnapshot 读取本地保存的同一份脚本。发行版只有一个 exe、没有这个目录，
// 读不到就当作"没有本地快照"，这不算错误。
func readJSSnapshot(name string) ([]byte, bool) {
	body, err := os.ReadFile(filepath.Join(jsSnapshotDir, name))
	if err != nil {
		return nil, false
	}
	return body, true
}

// writeJSSnapshot 原子写本地快照（临时文件 + Rename，半截文件不会顶掉旧基准）。
func writeJSSnapshot(name string, body []byte) error {
	if err := os.MkdirAll(jsSnapshotDir, 0o755); err != nil {
		return fmt.Errorf("创建快照目录: %w", err)
	}
	tmp, err := os.CreateTemp(jsSnapshotDir, ".jshash-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // Rename 成功后这里什么也不做
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置权限: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(jsSnapshotDir, name)); err != nil {
		return fmt.Errorf("替换快照文件: %w", err)
	}
	return nil
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
