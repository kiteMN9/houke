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

func (s *Server) deleteAccount(c *gin.Context) {
	if err := s.store.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
