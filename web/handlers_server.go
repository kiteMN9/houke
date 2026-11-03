package web

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"school_sdk/utils"
)

// getServerSettings 返回服务端配置（全局，不依赖账号）。
func (s *Server) getServerSettings(c *gin.Context) {
	if s.serverCfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务端配置未初始化"})
		return
	}
	c.JSON(http.StatusOK, s.serverCfg.view())
}

// updateServerSettings 保存服务端配置；只提交需要改的字段。
func (s *Server) updateServerSettings(c *gin.Context) {
	if s.serverCfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务端配置未初始化"})
		return
	}
	var in struct {
		SMTP *smtpPatch `json:"smtp"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误: " + err.Error()})
		return
	}
	if in.SMTP == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 smtp 配置"})
		return
	}
	view, err := s.serverCfg.update(*in.SMTP)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "server": view})
		return
	}
	c.JSON(http.StatusOK, view)
}

// testSMTP 用当前表单（或已保存配置）发一封测试邮件。
//
// 请求体可以为空；带 smtp 时使用页面里尚未保存的值，密码留空则沿用已保存
// 的授权码。这样用户可以先测通再保存，不用把半成品写进 server.json。
func (s *Server) testSMTP(c *gin.Context) {
	if s.serverCfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务端配置未初始化"})
		return
	}
	var in struct {
		SMTP *smtpPatch `json:"smtp"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误: " + err.Error()})
			return
		}
	}
	cfg, err := s.serverCfg.smtpForTest(in.SMTP)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	subject := "School SDK SMTP 测试"
	body := fmt.Sprintf(
		"<div style=\"font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif;line-height:1.7\">"+
			"<p>这是一封来自 <b>School SDK</b> 的测试邮件。</p>"+
			"<p>如果你收到它，说明服务端配置里的 SMTP 已经能正常发信。</p>"+
			"<p style=\"color:#64748b;font-size:12px\">SMTP：%s:%d · 发件人：%s · 时间：%s</p>"+
			"</div>",
		html.EscapeString(cfg.Host), cfg.Port, html.EscapeString(cfg.From),
		time.Now().Format("2006-01-02 15:04:05"),
	)
	if err := utils.SendSMTP(ctx, cfg.mailConfig(), subject, body); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "发送测试邮件失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "to": cfg.To})
}
