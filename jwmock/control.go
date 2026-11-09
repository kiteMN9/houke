package jwmock

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleState(c *gin.Context) {
	c.JSON(http.StatusOK, s.Snapshot())
}

func (s *Server) handleRequests(c *gin.Context) {
	c.JSON(http.StatusOK, s.Snapshot().Requests)
}

func (s *Server) handleScenario(c *gin.Context) {
	var sc Scenario
	if err := c.ShouldBindJSON(&sc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "场景 JSON 解析失败: " + err.Error()})
		return
	}
	if err := s.SetScenario(sc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.Scenario())
}

func (s *Server) handleReset(c *gin.Context) {
	s.Reset()
	c.JSON(http.StatusOK, s.Snapshot())
}

func (s *Server) handleRevoke(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "revoked": s.RevokeSessions()})
}
