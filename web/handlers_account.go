package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) listAccounts(c *gin.Context) {
	c.JSON(http.StatusOK, s.store.List())
}

func (s *Server) createAccount(c *gin.Context) {
	var a Account
	if err := c.ShouldBindJSON(&a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if a.Name == "" || a.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name 和 username 不能为空"})
		return
	}
	if a.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "baseUrl 不能为空"})
		return
	}
	// 其余缺省值由 AccountStore.Create → Account.normalize 统一补齐
	created, err := s.store.Create(&a)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, created)
}

func (s *Server) getAccount(c *gin.Context) {
	a, ok := s.store.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	c.JSON(http.StatusOK, a)
}

func (s *Server) updateAccount(c *gin.Context) {
	id := c.Param("id")

	// 先确认存在（避免把 Create 用的同名字段语义搞混）
	if _, ok := s.store.Get(id); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}

	var patch Account
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := s.store.Update(id, &patch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// toggleReq 是账号级布尔开关的请求体：指针区分"没传"和"传了 false"。
type toggleReq struct {
	Enabled *bool `json:"enabled"`
}

// setAutoRelogin 热切换账号的自动重登开关（不重建运行态，不丢登录态）。
func (s *Server) setAutoRelogin(c *gin.Context) {
	var req toggleReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		writeAPIError(c, http.StatusBadRequest, "", "enabled 必须是布尔值")
		return
	}
	id := c.Param("id")
	if _, ok := s.store.Get(id); !ok {
		writeAPIError(c, http.StatusNotFound, "", "账号不存在")
		return
	}
	a, err := s.store.SetAutoRelogin(id, *req.Enabled)
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"autoRelogin": a.autoReloginEnabled(),
	})
}

// setAutoNotify 热切换账号的"自动选课结束是否发结果邮件"开关。
//
// 和自动重登一样不重建运行态：切一个通知开关不该把用户踢回登录页。
func (s *Server) setAutoNotify(c *gin.Context) {
	var req toggleReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		writeAPIError(c, http.StatusBadRequest, "", "enabled 必须是布尔值")
		return
	}
	id := c.Param("id")
	if _, ok := s.store.Get(id); !ok {
		writeAPIError(c, http.StatusNotFound, "", "账号不存在")
		return
	}
	a, err := s.store.SetAutoNotify(id, *req.Enabled)
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"autoNotify": a.autoNotifyEnabled(),
	})
}

func (s *Server) deleteAccount(c *gin.Context) {
	if err := s.store.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
