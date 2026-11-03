package utils

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig 是一封邮件需要的 SMTP 连接信息。
//
// 它只描述"怎么发"，不负责读写配置文件：配置的持久化与热更新由 web 层的
// serverSettingsManager 负责，这样 utils 不依赖任何宿主。
type SMTPConfig struct {
	Host     string
	Port     int
	Username string // 留空时用 From 作为登录名
	From     string
	Password string
	To       []string
}

// Validate 检查发送一封邮件所必需的基础字段。
//
// 这里只做与 SMTP 协议有关的校验；"启用邮件通知时必须配齐"这类业务规则
// 由 web 层判断，避免 utils 绑死某一个配置页的语义。
func (c SMTPConfig) Validate() error {
	c.Host = strings.TrimSpace(c.Host)
	c.From = strings.TrimSpace(c.From)
	if c.Host == "" {
		return errors.New("SMTP 服务器不能为空")
	}
	if strings.Contains(c.Host, "://") || strings.ContainsAny(c.Host, " /\\:") {
		return fmt.Errorf("SMTP 服务器只填主机名（例如 smtp.qq.com），当前为 %q", c.Host)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("SMTP 端口必须在 1~65535 之间，当前为 %d", c.Port)
	}
	if c.From == "" {
		return errors.New("发件人邮箱不能为空")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("发件人邮箱格式不正确: %w", err)
	}
	if len(c.To) == 0 {
		return errors.New("至少需要一个收件人")
	}
	for _, to := range c.To {
		if _, err := mail.ParseAddress(strings.TrimSpace(to)); err != nil {
			return fmt.Errorf("收件人 %q 格式不正确: %w", to, err)
		}
	}
	return nil
}

// SendSMTP 通过 SMTP 发送一封 HTML 邮件。
//
// 端口 465 按隐式 TLS 处理（先握手再说话），其余端口在服务器支持时升级
// STARTTLS。用户名留空表示不做认证，适合本机中继；需要认证时一律使用
// PLAIN，密码只在加密连接或本机上发送。
func SendSMTP(ctx context.Context, cfg SMTPConfig, subject, htmlBody string) error {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.Username = strings.TrimSpace(cfg.Username)
	// MAIL FROM 只接受纯地址：调用方传 “Name <addr>” 时也收敛成 addr。
	if addr, err := mail.ParseAddress(cfg.From); err == nil {
		cfg.From = addr.Address
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器 %s 失败: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	implicitTLS := cfg.Port == 465
	if implicitTLS {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("SMTP TLS 握手失败: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	defer client.Close()

	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{
				ServerName: cfg.Host,
				MinVersion: tls.VersionTLS12,
			}); err != nil {
				return fmt.Errorf("SMTP STARTTLS 失败: %w", err)
			}
		}
	}

	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = strings.TrimSpace(cfg.From)
	}
	if cfg.Password != "" {
		auth := smtp.PlainAuth("", username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}

	if err := client.Mail(strings.TrimSpace(cfg.From)); err != nil {
		return fmt.Errorf("SMTP MAIL FROM 失败: %w", err)
	}
	for _, to := range cfg.To {
		addr, err := mail.ParseAddress(strings.TrimSpace(to))
		if err != nil {
			return fmt.Errorf("收件人 %q 格式不正确: %w", to, err)
		}
		if err := client.Rcpt(addr.Address); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s 失败: %w", addr.Address, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败: %w", err)
	}
	if _, err := w.Write(buildMessage(cfg, subject, htmlBody)); err != nil {
		_ = w.Close()
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交邮件内容失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("结束 SMTP 会话失败: %w", err)
	}
	return nil
}

// buildMessage 组装一封 UTF-8 HTML 邮件。
//
// 主题用 RFC 2047 编码，正文用 base64：这样中文既不会在头部变成乱码，
// 也不依赖服务器是否接受 8bit 的 DATA。
func buildMessage(cfg SMTPConfig, subject, htmlBody string) []byte {
	from := strings.TrimSpace(cfg.From)
	to := make([]string, 0, len(cfg.To))
	for _, item := range cfg.To {
		if addr, err := mail.ParseAddress(strings.TrimSpace(item)); err == nil {
			to = append(to, addr.Address)
		}
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", sanitizeHeader(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")
	b.WriteString(wrapBase64(base64.StdEncoding.EncodeToString([]byte(htmlBody))))
	b.WriteString("\r\n")
	return b.Bytes()
}

// sanitizeHeader 去掉可能被用来注入额外头部的换行。
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// wrapBase64 按 76 字符换行，满足 SMTP 对行长度的限制。
func wrapBase64(s string) string {
	const width = 76
	if len(s) <= width {
		return s
	}
	var b strings.Builder
	for len(s) > width {
		b.WriteString(s[:width])
		b.WriteString("\r\n")
		s = s[width:]
	}
	b.WriteString(s)
	return b.String()
}
