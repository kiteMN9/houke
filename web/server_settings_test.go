package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"school_sdk/check_code/kaptcha"
)

func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }
func intPtr(v int) *int          { return &v }

func TestServerSettingsLegacyImport(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "SMTP.json")
	raw := `{
  "host": "smtp.example.com",
  "port": 465,
  "from": "sender@example.com",
  "to": ["one@example.com", "two@example.com"],
  "password": "secret",
  "enable": true
}`
	if err := os.WriteFile(legacy, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := newServerSettingsManager(filepath.Join(dir, "server.json"))
	if err != nil {
		t.Fatalf("newServerSettingsManager: %v", err)
	}
	view := m.view()
	if !view.LegacyImported {
		t.Fatal("LegacyImported = false，期望 true")
	}
	if view.LegacyFile != legacy {
		t.Errorf("LegacyFile = %q，期望 %q", view.LegacyFile, legacy)
	}
	if !view.SMTP.Enabled || !view.SMTPReady {
		t.Errorf("导入后应启用且可发信: %+v", view)
	}
	if !view.SMTP.HasPassword {
		t.Error("HasPassword = false，期望 true")
	}
	if view.SMTP.Username != "" {
		t.Errorf("Username = %q，旧配置没有独立登录名时应保持为空", view.SMTP.Username)
	}
	if view.SMTP.Port != 465 || len(view.SMTP.To) != 2 {
		t.Errorf("导入字段不完整: %+v", view.SMTP)
	}
	if !view.SMTP.NotifyAutoChoose {
		t.Error("NotifyAutoChoose = false，期望默认开启")
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Error("view 里不应包含 SMTP 密码")
	}
}

func TestServerSettingsServerFileWinsOverLegacy(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "SMTP.json")
	if err := os.WriteFile(legacy, []byte(`{"host":"old.example.com","enable":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "server.json")
	if err := os.WriteFile(file, []byte(`{"smtp":{"enabled":false,"host":"new.example.com","port":587,"notifyAutoChoose":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatalf("newServerSettingsManager: %v", err)
	}
	view := m.view()
	if view.LegacyImported {
		t.Error("server.json 存在时不应再导入 SMTP.json")
	}
	if view.SMTP.Host != "new.example.com" || view.SMTP.Enabled {
		t.Errorf("没有采用 server.json: %+v", view.SMTP)
	}
	if view.SMTP.NotifyAutoChoose {
		t.Error("显式 false 的 notifyAutoChoose 被默认值覆盖了")
	}
}

func TestServerSettingsMissingFieldsKeepDefaults(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.json")
	if err := os.WriteFile(file, []byte(`{"smtp":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatalf("newServerSettingsManager: %v", err)
	}
	view := m.view()
	if view.SMTP.Port != 587 {
		t.Errorf("Port = %d，期望默认 587", view.SMTP.Port)
	}
	if !view.SMTP.NotifyAutoChoose {
		t.Error("notifyAutoChoose 缺失时应保留默认 true")
	}
	if view.SMTP.Enabled {
		t.Error("enabled 显式 false 被覆盖了")
	}
}

func TestServerSettingsInvalidManualConfigNotReady(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.json")
	raw := `{"smtp":{"enabled":true,"host":"smtp.example.com","port":587,"from":"bad","to":["one@example.com"],"password":"secret"}}`
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatalf("newServerSettingsManager: %v", err)
	}
	view := m.view()
	if view.SMTPReady {
		t.Error("手改的非法发件人被误判为可发信")
	}
	if view.SMTPError == "" {
		t.Error("应为非法发件人给出 SMTPError")
	}
}

func TestServerSettingsUpdateKeepsPassword(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.json")
	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatal(err)
	}

	view, err := m.update(smtpPatch{
		Enabled:  boolPtr(true),
		Host:     stringPtr("smtp.example.com"),
		Port:     intPtr(587),
		From:     stringPtr("sender@example.com"),
		To:       &[]string{"one@example.com"},
		Password: stringPtr("secret"),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !view.SMTPReady || !view.SMTP.HasPassword {
		t.Errorf("保存后应可发信且记录密码: %+v", view)
	}

	// 空密码 = 保持原密码。
	view, err = m.update(smtpPatch{
		Host:     stringPtr("smtp2.example.com"),
		Password: stringPtr(""),
	})
	if err != nil {
		t.Fatalf("update keep password: %v", err)
	}
	if !view.SMTP.HasPassword {
		t.Error("空密码把已保存的密码清掉了")
	}
	if got := m.smtp().Password; got != "secret" {
		t.Errorf("内存密码 = %q，期望 secret", got)
	}

	// 重新从磁盘读，确认密码确实落盘且新主机生效。
	m2, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.smtp().Password; got != "secret" {
		t.Errorf("落盘密码 = %q，期望 secret", got)
	}
	if got := m2.smtp().Host; got != "smtp2.example.com" {
		t.Errorf("落盘 host = %q", got)
	}
	if m2.view().LegacyImported {
		t.Error("保存后不应再标记为旧配置导入")
	}
}

func TestServerSettingsValidation(t *testing.T) {
	dir := t.TempDir()
	m, err := newServerSettingsManager(filepath.Join(dir, "server.json"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.update(smtpPatch{Enabled: boolPtr(true)}); err == nil {
		t.Error("启用但没有任何 SMTP 字段时应报错")
	}
	if _, err := m.update(smtpPatch{
		Enabled: boolPtr(false),
		Host:    stringPtr("smtp://smtp.example.com"),
	}); err == nil {
		t.Error("带协议的服务器地址应报错")
	}
	if _, err := m.update(smtpPatch{
		Enabled: boolPtr(false),
		From:    stringPtr("not-an-email"),
	}); err == nil {
		t.Error("非法发件人应报错")
	}
	if _, err := m.update(smtpPatch{
		Enabled: boolPtr(false),
		To:      &[]string{"not-an-email"},
	}); err == nil {
		t.Error("非法收件人应报错")
	}

	// 关闭通知时允许保存半成品，方便用户先停掉再慢慢改。
	if _, err := m.update(smtpPatch{Enabled: boolPtr(false), Host: stringPtr("smtp.example.com")}); err != nil {
		t.Errorf("关闭状态下保存部分字段不应失败: %v", err)
	}
}

func TestServerSettingsLegacyKaptchaImport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kaptcha.json"),
		[]byte(`{"enabled":false,"threshold":0.8,"workers":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := newServerSettingsManager(filepath.Join(dir, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := m.kaptcha()
	if got.Enabled || got.Threshold != 0.8 || got.Workers != 2 {
		t.Errorf("未导入旧版 kaptcha.json: %+v", got)
	}
	// 只读导入，不写 server.json。
	if _, err := os.Stat(filepath.Join(dir, "server.json")); !os.IsNotExist(err) {
		t.Errorf("导入阶段不应生成 server.json，stat err = %v", err)
	}
}

func TestServerSettingsKaptchaNodeWinsOverLegacy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kaptcha.json"),
		[]byte(`{"enabled":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "server.json")
	if err := os.WriteFile(file,
		[]byte(`{"smtp":{"enabled":false},"kaptcha":{"enabled":true,"threshold":0.75}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatal(err)
	}
	got := m.kaptcha()
	if !got.Enabled || got.Threshold != 0.75 {
		t.Errorf("server.json 里的 kaptcha 节点应优先: %+v", got)
	}
}

func TestServerSettingsKaptchaImportedIntoExistingServerFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kaptcha.json"),
		[]byte(`{"enabled":false,"threshold":0.8}`), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "server.json")
	// 2.2 及之前写出的 server.json：只有 smtp 节点。
	if err := os.WriteFile(file,
		[]byte(`{"smtp":{"enabled":false,"host":"smtp.example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatal(err)
	}
	got := m.kaptcha()
	if got.Enabled || got.Threshold != 0.8 {
		t.Errorf("旧 server.json 没有 kaptcha 节点时应迁移: %+v", got)
	}
	if host := m.smtp().Host; host != "smtp.example.com" {
		t.Errorf("SMTP 设置不应受影响: %q", host)
	}
}

func TestServerSettingsSaveKaptchaKeepsSMTP(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.json")
	m, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.saveKaptcha(kaptcha.Settings{Enabled: false, Threshold: 0.7}); err != nil {
		t.Fatalf("saveKaptcha: %v", err)
	}
	if _, err := m.update(smtpPatch{
		Enabled: boolPtr(false),
		Host:    stringPtr("smtp.example.com"),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	m2, err := newServerSettingsManager(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.kaptcha(); got.Enabled || got.Threshold != 0.7 {
		t.Errorf("SMTP 保存把验证码设置弄丢了: %+v", got)
	}
	if got := m2.smtp().Host; got != "smtp.example.com" {
		t.Errorf("host = %q", got)
	}
}

func TestServerSettingsTestConfigUsesSavedPassword(t *testing.T) {
	dir := t.TempDir()
	m, err := newServerSettingsManager(filepath.Join(dir, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.update(smtpPatch{
		Enabled:  boolPtr(false),
		Host:     stringPtr("smtp.example.com"),
		Port:     intPtr(587),
		From:     stringPtr("sender@example.com"),
		To:       &[]string{"one@example.com"},
		Password: stringPtr("secret"),
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := m.smtpForTest(&smtpPatch{Host: stringPtr("smtp2.example.com"), Password: stringPtr("")})
	if err != nil {
		t.Fatalf("smtpForTest: %v", err)
	}
	if cfg.Host != "smtp2.example.com" {
		t.Errorf("host = %q，期望使用表单里的新值", cfg.Host)
	}
	if cfg.Password != "secret" {
		t.Error("测试配置没有沿用已保存的密码")
	}
}
