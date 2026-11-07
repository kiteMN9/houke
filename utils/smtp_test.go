package utils

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSendSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	msgCh := make(chan string, 1)
	go serveFakeSMTP(ln, msgCh)

	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = SendSMTP(ctx, SMTPConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "sender@example.com",
		From:     "sender@example.com",
		Password: "secret",
		To:       []string{"one@example.com", "two@example.com"},
	}, "选课提醒", "<p>hello</p>")
	if err != nil {
		t.Fatalf("SendSMTP: %v", err)
	}

	select {
	case msg := <-msgCh:
		if !strings.Contains(msg, "Subject: =?utf-8?") {
			t.Errorf("主题没有按 RFC 2047 编码:\n%s", msg)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte("<p>hello</p>"))
		if !strings.Contains(msg, encoded) {
			t.Errorf("正文没有按 base64 编码:\n%s", msg)
		}
		if !strings.Contains(msg, "To: one@example.com, two@example.com") {
			t.Errorf("收件人头部不正确:\n%s", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("假 SMTP 服务器没有收到邮件")
	}
}

func TestSMTPConfigValidate(t *testing.T) {
	base := SMTPConfig{
		Host: "smtp.example.com",
		Port: 587,
		From: "sender@example.com",
		To:   []string{"one@example.com"},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}
	cases := []struct {
		name string
		edit func(*SMTPConfig)
	}{
		{"空主机", func(c *SMTPConfig) { c.Host = "" }},
		{"带协议", func(c *SMTPConfig) { c.Host = "smtp://smtp.example.com" }},
		{"端口为 0", func(c *SMTPConfig) { c.Port = 0 }},
		{"端口越界", func(c *SMTPConfig) { c.Port = 70000 }},
		{"空发件人", func(c *SMTPConfig) { c.From = "" }},
		{"非法发件人", func(c *SMTPConfig) { c.From = "bad" }},
		{"没有收件人", func(c *SMTPConfig) { c.To = nil }},
		{"非法收件人", func(c *SMTPConfig) { c.To = []string{"bad"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.To = append([]string(nil), base.To...)
			tc.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("%s: Validate() = nil，期望报错", tc.name)
			}
		})
	}
}

// serveFakeSMTP 是最小 SMTP 服务端：只够 net/smtp 走完 AUTH、MAIL、RCPT、DATA。
func serveFakeSMTP(ln net.Listener, msgCh chan<- string) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(s string) {
		_, _ = fmt.Fprintf(w, "%s\r\n", s)
		_ = w.Flush()
	}
	reply("220 fake smtp")

	var data strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				reply("250 stored")
				continue
			}
			if strings.HasPrefix(line, "..") {
				line = line[1:]
			}
			data.WriteString(line)
			data.WriteString("\r\n")
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "):
			reply("250-fake")
			reply("250 AUTH PLAIN")
		case strings.HasPrefix(line, "AUTH PLAIN"):
			reply("235 ok")
		case strings.HasPrefix(line, "MAIL FROM:"):
			reply("250 ok")
		case strings.HasPrefix(line, "RCPT TO:"):
			reply("250 ok")
		case line == "DATA":
			inData = true
			reply("354 go ahead")
		case line == "QUIT":
			reply("221 bye")
			msgCh <- data.String()
			return
		default:
			reply("250 ok")
		}
	}
}
