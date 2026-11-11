package jwmock

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"math/big"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"school_sdk/protocol/config"
)

func (s *Server) handleLoginPage(c *gin.Context) {
	if _, ok := s.userOf(c); ok {
		c.Redirect(http.StatusFound, s.opts.BasePath+config.MENU)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(loginPage(s.opts.BasePath)))
}

func (s *Server) handleLoginSubmit(c *gin.Context) {
	_ = c.Request.ParseForm()
	yhm := strings.TrimSpace(c.PostForm("yhm"))
	mm := c.PostForm("mm")
	user, ok := s.findUser(yhm)
	if !ok || !s.passwordMatches(mm, user.Password) {
		s.mu.Lock()
		s.loginFailures++
		s.mu.Unlock()
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(tipsPage("用户名或密码不正确")))
		return
	}

	id := s.newSessionID()
	s.mu.Lock()
	s.sessions[id] = &sessionState{User: yhm}
	s.logins++
	s.mu.Unlock()

	c.SetCookie(cookieName, id, 0, "/", "", false, true)
	c.Redirect(http.StatusFound, s.opts.BasePath+config.MENU)
}

func (s *Server) handlePublicKey(c *gin.Context) {
	modulus := base64.StdEncoding.EncodeToString(s.key.PublicKey.N.Bytes())
	exponent := base64.StdEncoding.EncodeToString(big.NewInt(int64(s.key.PublicKey.E)).Bytes())
	c.JSON(http.StatusOK, gin.H{"modulus": modulus, "exponent": exponent})
}

func (s *Server) handleMenu(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(menuPage()))
}

func (s *Server) handleLoginStatus(c *gin.Context) {
	c.String(http.StatusOK, "")
}

func (s *Server) handleStudentName(c *gin.Context) {
	user, _ := s.userOf(c)
	body := `<html><body><input type="hidden" id="xm" name="xm" value="模拟学生">` +
		`<input type="hidden" id="yhm" name="yhm" value="` + user + `"></body></html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(body))
}

func (s *Server) handleLogout(c *gin.Context) {
	if cookie, err := c.Cookie(cookieName); err == nil {
		s.mu.Lock()
		if sess, ok := s.sessions[cookie]; ok {
			sess.Revoked = true
		}
		s.mu.Unlock()
	}
	c.SetCookie(cookieName, "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, s.opts.BasePath+config.LoginIndex)
}

func (s *Server) findUser(username string) (User, bool) {
	for _, u := range s.opts.Users {
		if u.Username == username {
			return u, true
		}
	}
	return User{}, false
}

// passwordMatches 解密客户端提交的 RSA 密文（base64(PKCS1v15)）并比较密码。
func (s *Server) passwordMatches(encrypted, want string) bool {
	if encrypted == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return false
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, s.key, raw)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(plain, []byte(want)) == 1
}
