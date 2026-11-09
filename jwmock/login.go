package jwmock

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"net/http"
	"time"

	"school_sdk/protocol/config"
)

// handleLoginPage 返回登录页；已登录时按真实行为 302 到首页。
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.loggedIn(r) {
		s.redirect(w, config.MENU)
		return
	}
	s.mu.Lock()
	mode := s.loginModeLocked()
	s.mu.Unlock()
	writeHTML(w, loginPage(mode, s.opts.BasePath))
}

// handlePostLogin 校验账号、RSA 密码和（可选的）字符验证码。
func (s *Server) handlePostLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user := r.PostForm.Get("yhm")
	pass, ok := s.decryptPassword(r.PostForm.Get("mm"))

	s.mu.Lock()
	mode := s.loginModeLocked()
	code := s.kaptchaCodeLocked()
	s.mu.Unlock()

	// 字符验证码分支：错误文案必须与真实页一致，客户端据此重新取图。
	if mode == LoginKaptcha && r.PostForm.Get("yzm") != code {
		s.failLogin(w)
		writeHTML(w, tipsPage("验证码输入错误！"))
		return
	}
	if !ok || user != s.opts.Username || pass != s.opts.Password {
		s.failLogin(w)
		writeHTML(w, tipsPage("用户名或密码不正确"))
		return
	}

	id := newSessionID()
	s.mu.Lock()
	s.sessions[id] = &sessionState{ID: id, User: user}
	s.logins++
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
	})
	s.redirect(w, config.MENU)
}

// failLogin 记一次登录失败。
func (s *Server) failLogin(w http.ResponseWriter) {
	s.mu.Lock()
	s.loginFailures++
	s.mu.Unlock()
}

// decryptPassword 解开客户端用 RSA 公钥加密后的密码。
func (s *Server) decryptPassword(mm string) (string, bool) {
	if mm == "" {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(mm)
	if err != nil {
		return "", false
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, s.key, raw)
	if err != nil {
		return "", false
	}
	return string(plain), true
}

// publicKeyJSON 是登录页取公钥接口的响应。
type publicKeyJSON struct {
	Modulus  string `json:"modulus"`
	Exponent string `json:"exponent"`
}

// handlePublicKey 返回 RSA 公钥（modulus/exponent 都是大端字节的 base64）。
func (s *Server) handlePublicKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, publicKeyJSON{
		Modulus:  base64.StdEncoding.EncodeToString(s.key.N.Bytes()),
		Exponent: base64.StdEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes()),
	})
}

// handleKaptcha 返回字符验证码图片。
func (s *Server) handleKaptcha(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.kaptchaPNG)
}

// handleCaptcha 处理滑块验证码的 4 个动作：resource / refresh / image / verify。
//
// 关键点：refresh 必须返回 vs=not_verify。客户端在 vs=verified 时会跳过图片
// 校验直接返回，但此时登录密码还没加密（mm 为空），postLogin 会以
// CsrfNotExist 失败。
func (s *Server) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		writeJSON(w, http.StatusOK, map[string]any{
			"msg": "", "vs": "verified", "status": "success",
		})
		return
	}
	switch r.URL.Query().Get("type") {
	case "resource":
		// 客户端只扫描前 192 字节，找 rtk:'...'。
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("var mockCaptcha={rtk:'mock-rtk'};\n"))
	case "refresh":
		writeJSON(w, http.StatusOK, map[string]any{
			"msg":    "",
			"t":      time.Now().UnixMilli(),
			"si":     "mock-si",
			"imtk":   "mock-imtk",
			"mi":     "mock-mi",
			"vs":     "not_verify",
			"status": "success",
		})
	case "image":
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.sliderPNG)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"msg": "", "vs": "not_verify", "status": "success",
		})
	}
}
