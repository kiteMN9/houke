package web

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"school_sdk/utils"
)

// defaultSMTPPort 是新建配置时的默认端口：587 走 STARTTLS，是各家邮箱
// 最通用的提交端口；465 在发送时按隐式 TLS 处理。
const defaultSMTPPort = 587

// smtpSettings 是服务端邮件通知配置，持久化在 server.json 的 smtp 节点里。
//
// 字段沿用旧版 SMTP.json 的名字（host/port/from/to/password/enable），
// 这样第一次启动可以直接把旧文件读进来，不需要用户重填。
type smtpSettings struct {
	Enabled          bool     `json:"enabled"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	Username         string   `json:"username,omitempty"`
	From             string   `json:"from"`
	Password         string   `json:"password,omitempty"`
	To               []string `json:"to,omitempty"`
	NotifyAutoChoose bool     `json:"notifyAutoChoose"`
}

func defaultSMTPSettings() smtpSettings {
	return smtpSettings{Port: defaultSMTPPort, NotifyAutoChoose: true}
}

// normalized 只做"能安全落盘"的清理，不做完整校验：
// 关闭通知时允许字段留空，用户可以先保存一半再补。
func (s smtpSettings) normalized() smtpSettings {
	s.Host = strings.TrimSpace(s.Host)
	s.Username = strings.TrimSpace(s.Username)
	s.From = normalizeMailAddress(s.From)
	if s.Port == 0 {
		s.Port = defaultSMTPPort
	}
	s.To = normalizeMailAddresses(s.To)
	return s
}

// validate 校验字段格式；Enabled 为 true 时还要求能真正发信。
//
// 关闭状态仍校验已填写的字段，避免把明显写错的地址存进去；但允许整体为空，
// 因为"先关掉通知"不应该被旧配置里的空字段卡住。
func (s smtpSettings) validate() error {
	if s.Host != "" {
		if strings.Contains(s.Host, "://") {
			return errors.New("SMTP 服务器只填主机名，不要带 http:// 或 smtp://")
		}
		if strings.ContainsAny(s.Host, " /\\:") {
			return fmt.Errorf("SMTP 服务器只填主机名（端口填在下面），当前为 %q", s.Host)
		}
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("SMTP 端口必须在 1~65535 之间，当前为 %d", s.Port)
	}
	if s.From != "" {
		if _, err := mail.ParseAddress(s.From); err != nil {
			return fmt.Errorf("发件人邮箱格式不正确: %w", err)
		}
	}
	for _, to := range s.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return fmt.Errorf("收件人 %q 格式不正确: %w", to, err)
		}
	}
	if s.Enabled {
		if err := s.validateForSend(); err != nil {
			return err
		}
	}
	return nil
}

// validateForSend 要求一封邮件能真正发出去所需的最小字段集。
func (s smtpSettings) validateForSend() error {
	if s.Host == "" {
		return errors.New("请填写 SMTP 服务器")
	}
	if s.Port <= 0 || s.Port > 65535 {
		return errors.New("请填写有效的 SMTP 端口")
	}
	if s.From == "" {
		return errors.New("请填写发件人邮箱")
	}
	if len(s.To) == 0 {
		return errors.New("请至少填写一个收件人")
	}
	return nil
}

func (s smtpSettings) mailConfig() utils.SMTPConfig {
	username := s.Username
	if username == "" {
		username = s.From
	}
	return utils.SMTPConfig{
		Host:     s.Host,
		Port:     s.Port,
		Username: username,
		From:     s.From,
		Password: s.Password,
		To:       append([]string(nil), s.To...),
	}
}

func normalizeMailAddress(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(v); err == nil {
		return addr.Address
	}
	return v
}

func normalizeMailAddresses(list []string) []string {
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, item := range list {
		addr := normalizeMailAddress(item)
		if addr == "" {
			continue
		}
		key := strings.ToLower(addr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}
	return out
}

// serverSettings 是服务端配置文件的根节点。
//
// 目前只有 smtp 一组；以后加"日志级别""默认监听地址"等选项时，在这里加
// 字段和对应的 view/patch 即可，不用再动配置文件路径。
type serverSettings struct {
	SMTP smtpSettings `json:"smtp"`
}

func defaultServerSettings() serverSettings {
	return serverSettings{SMTP: defaultSMTPSettings()}
}

// smtpView 是返回给前端的视图：密码永远不出后端，只报告是否已设置。
type smtpView struct {
	Enabled          bool     `json:"enabled"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	Username         string   `json:"username"`
	From             string   `json:"from"`
	To               []string `json:"to"`
	NotifyAutoChoose bool     `json:"notifyAutoChoose"`
	HasPassword      bool     `json:"hasPassword"`
}

// serverView 是 GET /api/server 的响应。
type serverView struct {
	SettingsFile string `json:"settingsFile"`
	LegacyFile   string `json:"legacyFile,omitempty"`
	// LegacyImported 表示当前值来自旧版 SMTP.json，尚未写入 server.json。
	// 前端据此提示"保存后才会迁移"，避免用户以为旧文件已经改了。
	LegacyImported bool `json:"legacyImported,omitempty"`

	SMTP      smtpView `json:"smtp"`
	SMTPReady bool     `json:"smtpReady"`
	SMTPError string   `json:"smtpError,omitempty"`
}

// smtpPatch 是 PUT /api/server 的 smtp 节点：指针字段区分"没传"和"传了零值"。
//
// Password 为空字符串表示"保持原密码"：页面不回显密码，用户不重填时不能
// 把已保存的授权码清掉。
type smtpPatch struct {
	Enabled          *bool     `json:"enabled"`
	Host             *string   `json:"host"`
	Port             *int      `json:"port"`
	Username         *string   `json:"username"`
	From             *string   `json:"from"`
	To               *[]string `json:"to"`
	Password         *string   `json:"password"`
	NotifyAutoChoose *bool     `json:"notifyAutoChoose"`
}

func (p smtpPatch) apply(s smtpSettings) smtpSettings {
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.Host != nil {
		s.Host = *p.Host
	}
	if p.Port != nil {
		s.Port = *p.Port
	}
	if p.Username != nil {
		s.Username = *p.Username
	}
	if p.From != nil {
		s.From = *p.From
	}
	if p.To != nil {
		s.To = append([]string(nil), (*p.To)...)
	}
	if p.Password != nil && *p.Password != "" {
		s.Password = *p.Password
	}
	if p.NotifyAutoChoose != nil {
		s.NotifyAutoChoose = *p.NotifyAutoChoose
	}
	return s
}

// serverSettingsManager 读写 server.json，并在第一次启动时从旧版 SMTP.json 导入。
type serverSettingsManager struct {
	file       string
	legacyFile string

	mu             sync.RWMutex
	settings       serverSettings
	legacyImported bool
}

// newServerSettingsManager 读取服务端配置。
//
// 文件不存在时尝试导入同目录的旧版 SMTP.json（1.x 版本使用），只读不写：
// 用户第一次在页面上保存后才生成 server.json。文件损坏时返回一个带默认值
// 的 manager 和错误，调用方可以继续启动 WebUI，让用户在页面里重存一份。
func newServerSettingsManager(file, legacyFile string) (*serverSettingsManager, error) {
	m := &serverSettingsManager{
		file:       file,
		legacyFile: legacyFile,
		settings:   defaultServerSettings(),
	}
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &m.settings); err != nil {
				m.settings = defaultServerSettings()
				return m, fmt.Errorf("服务端配置文件 %s 解析失败，暂用默认设置: %w", file, err)
			}
		}
		m.settings.SMTP = m.settings.SMTP.normalized()
	case os.IsNotExist(err):
		if imported, ok := m.loadLegacy(); ok {
			m.settings = imported
			m.legacyImported = true
		}
	default:
		return nil, fmt.Errorf("读取服务端配置文件 %s 失败: %w", file, err)
	}
	return m, nil
}

// loadLegacy 读取旧版 SMTP.json。返回的 bool 表示文件里确实有可导入的配置。
func (m *serverSettingsManager) loadLegacy() (serverSettings, bool) {
	cfg := defaultServerSettings()
	if strings.TrimSpace(m.legacyFile) == "" {
		return cfg, false
	}
	data, err := os.ReadFile(m.legacyFile)
	if err != nil {
		return cfg, false
	}
	var legacy struct {
		Host     string   `json:"host"`
		Port     int      `json:"port"`
		From     string   `json:"from"`
		To       []string `json:"to"`
		Password string   `json:"password"`
		Enable   bool     `json:"enable"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return cfg, false
	}
	smtp := cfg.SMTP
	smtp.Enabled = legacy.Enable
	smtp.Host = legacy.Host
	smtp.Port = legacy.Port
	smtp.From = legacy.From
	smtp.To = legacy.To
	smtp.Password = legacy.Password
	// 旧配置没有独立的登录名：保持空值，发送时统一回落到 From。
	// 这样用户以后改发件人，登录名会跟着变，不会被旧值钉住。
	smtp = smtp.normalized()
	cfg.SMTP = smtp

	hasContent := smtp.Host != "" || smtp.From != "" || len(smtp.To) > 0 || smtp.Password != ""
	return cfg, hasContent
}

func (m *serverSettingsManager) view() serverView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.viewLocked()
}

func (m *serverSettingsManager) viewLocked() serverView {
	s := m.settings.SMTP
	view := serverView{
		SettingsFile:   m.file,
		LegacyImported: m.legacyImported,
		SMTP: smtpView{
			Enabled:          s.Enabled,
			Host:             s.Host,
			Port:             s.Port,
			Username:         s.Username,
			From:             s.From,
			To:               append([]string{}, s.To...),
			NotifyAutoChoose: s.NotifyAutoChoose,
			HasPassword:      s.Password != "",
		},
	}
	if m.legacyImported {
		view.LegacyFile = m.legacyFile
	}
	if s.Enabled {
		// 用完整校验（格式 + 必填），避免手改 server.json 后页面误报“可发信”。
		if err := s.validate(); err != nil {
			view.SMTPError = err.Error()
		} else {
			view.SMTPReady = true
		}
	}
	return view
}

// smtp 返回当前 SMTP 配置副本，供自动选课通知读取。
func (m *serverSettingsManager) smtp() smtpSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SMTP
}

// update 应用页面提交的补丁，校验并落盘。
//
// 只有写盘成功才替换内存里的配置：保存失败时正在生效的邮件设置保持不变。
func (m *serverSettingsManager) update(patch smtpPatch) (serverView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := patch.apply(m.settings.SMTP).normalized()
	if err := next.validate(); err != nil {
		return m.viewLocked(), err
	}
	cfg := m.settings
	cfg.SMTP = next
	if err := m.saveLocked(cfg); err != nil {
		return m.viewLocked(), err
	}
	m.settings = cfg
	m.legacyImported = false
	return m.viewLocked(), nil
}

// smtpForTest 生成"发测试邮件"用的配置：可以带上页面里尚未保存的表单值，
// 密码留空时沿用已保存的授权码。
func (m *serverSettingsManager) smtpForTest(patch *smtpPatch) (smtpSettings, error) {
	m.mu.RLock()
	s := m.settings.SMTP
	m.mu.RUnlock()
	if patch != nil {
		s = patch.apply(s)
	}
	s = s.normalized()
	if err := s.validate(); err != nil {
		return s, err
	}
	if err := s.validateForSend(); err != nil {
		return s, err
	}
	return s, nil
}

func (m *serverSettingsManager) saveLocked(cfg serverSettings) error {
	data, err := json.Marshal(cfg, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("序列化服务端配置失败: %w", err)
	}
	dir := filepath.Dir(m.file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	tmp := m.file + ".tmp"
	// 里面有 SMTP 授权码，按 0600 写；Windows 上权限位不生效，但语义要对。
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入服务端配置失败: %w", err)
	}
	// os.WriteFile 不会收紧已存在文件的权限；临时文件可能来自上次异常退出。
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, m.file); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存服务端配置失败: %w", err)
	}
	return nil
}
