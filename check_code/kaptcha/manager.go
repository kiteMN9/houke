package kaptcha

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Settings 是 WebUI 可配置的模型参数，持久化在 kaptcha.json。
//
// 所有账号共用同一份设置：模型只有一份（约 11 MB），按账号分别加载既浪费
// 内存，也没有实际意义。
type Settings struct {
	// Enabled 为 false 时登录遇到字符验证码一律人工输入，模型不参与。
	Enabled bool `json:"enabled"`
	// ModelPath 为空表示按 ResolveModelPath 的默认位置自动查找。
	ModelPath string `json:"modelPath,omitempty"`
	// Threshold 是整串置信度下限，(0,1]。
	Threshold float64 `json:"threshold"`
	// Workers 是 ONNX 算子内部并行度，0 表示按 CPU 核数。
	Workers int `json:"workers,omitempty"`
}

// DefaultSettings 返回默认配置：启用自动识别、阈值 0.9、自动选模型。
func DefaultSettings() Settings {
	return Settings{Enabled: true, Threshold: DefaultThreshold}
}

func (s Settings) normalized() Settings {
	s.ModelPath = strings.TrimSpace(s.ModelPath)
	if math.IsNaN(s.Threshold) || s.Threshold <= 0 || s.Threshold > 1 {
		s.Threshold = DefaultThreshold
	}
	if s.Workers < 0 {
		s.Workers = 0
	}
	if s.Workers > 64 {
		s.Workers = 64
	}
	return s
}

func (s Settings) validate() error {
	if math.IsNaN(s.Threshold) || s.Threshold <= 0 || s.Threshold > 1 {
		return fmt.Errorf("置信度阈值必须在 (0,1] 之间，当前 %.2f", s.Threshold)
	}
	if s.Workers < 0 || s.Workers > 64 {
		return fmt.Errorf("并行度必须在 0~64 之间，当前 %d", s.Workers)
	}
	if p := strings.TrimSpace(s.ModelPath); p != "" && !strings.EqualFold(filepath.Ext(p), ".onnx") {
		return fmt.Errorf("模型文件必须是 .onnx，当前 %q", p)
	}
	return nil
}

// Status 是配置页需要的完整状态快照。
type Status struct {
	Settings     Settings `json:"settings"`
	SettingsFile string   `json:"settingsFile"`

	ModelPath     string `json:"modelPath"`
	ModelName     string `json:"modelName"`
	ModelExists   bool   `json:"modelExists"`
	ModelSize     int64  `json:"modelSize"`
	ModelModified int64  `json:"modelModified"` // Unix 毫秒

	Loaded   bool  `json:"loaded"`
	Enabled  bool  `json:"enabled"` // 实际生效：配置启用且模型已加载
	LoadedAt int64 `json:"loadedAt"`

	Error string `json:"error,omitempty"`

	NumChars      int    `json:"numChars"`
	ImageWidth    int    `json:"imageWidth"`
	ImageHeight   int    `json:"imageHeight"`
	MaxModelBytes int64  `json:"maxModelBytes"`
	DefaultModel  string `json:"defaultModelName"`
}

// Manager 管理一份 kaptcha.json 和一个可热切换的 Recognizer。
//
// 它实现 protocol.KaptchaRecognizer，可以直接注入登录流程；关闭自动识别时
// 返回 ErrDisabled，protocol 会静默回退人工输入。
type Manager struct {
	file string

	applyMu sync.Mutex // 串行化 Apply/Reload，避免并发加载同一份模型

	mu         sync.RWMutex
	settings   Settings
	rec        *Recognizer
	loadedPath string
	loadedAt   time.Time
	loadErr    string
}

// NewManager 读取配置文件；文件不存在时用 defaults。
//
// 这里只读配置，不加载模型：加载可能耗时几百毫秒到几秒，失败也不该阻止
// WebUI 启动。启动时由 main 调一次 Reload。
//
// 配置文件存在但 JSON 损坏时，返回一个使用 defaults 的 Manager 和错误：
// 调用方可以继续启动 WebUI，让用户在配置页重新保存一份合法配置。读取文件
// 本身的错误（权限等）仍返回 nil。
func NewManager(file string, defaults Settings) (*Manager, error) {
	settings := defaults.normalized()
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return &Manager{file: file, settings: defaults.normalized()},
					fmt.Errorf("验证码配置文件 %s 解析失败，暂用默认设置: %w", file, err)
			}
		}
		settings = settings.normalized()
	case os.IsNotExist(err):
	default:
		return nil, fmt.Errorf("读取验证码配置文件 %s 失败: %w", file, err)
	}
	return &Manager{file: file, settings: settings}, nil
}

// File 返回配置文件路径。
func (m *Manager) File() string { return m.file }

// Settings 返回当前配置副本。
func (m *Manager) Settings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

// Status 返回供 WebUI 展示的状态。
func (m *Manager) Status() Status {
	m.mu.RLock()
	settings := m.settings
	rec := m.rec
	loadedPath := m.loadedPath
	loadedAt := m.loadedAt
	loadErr := m.loadErr
	m.mu.RUnlock()

	path := loadedPath
	if path == "" {
		if settings.ModelPath != "" {
			if abs, err := filepath.Abs(settings.ModelPath); err == nil {
				path = abs
			} else {
				path = settings.ModelPath
			}
		} else if p, err := ResolveModelPath(""); err == nil {
			path = p
		}
	}

	modelName := ""
	if path != "" {
		modelName = filepath.Base(path)
	}
	// 零值时间不能直接 UnixMilli：会得到 -62135596800000，前端会显示 1/1/1。
	var loadedAtMS int64
	if !loadedAt.IsZero() {
		loadedAtMS = loadedAt.UnixMilli()
	}

	st := Status{
		Settings:      settings,
		SettingsFile:  m.file,
		ModelPath:     path,
		ModelName:     modelName,
		Loaded:        rec != nil,
		Enabled:       rec != nil && settings.Enabled,
		LoadedAt:      loadedAtMS,
		Error:         loadErr,
		NumChars:      NumChars,
		ImageWidth:    ImageWidth,
		ImageHeight:   ImageHeight,
		MaxModelBytes: MaxModelBytes,
		DefaultModel:  DefaultModelName,
	}
	if path != "" {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			st.ModelExists = true
			st.ModelSize = fi.Size()
			st.ModelModified = fi.ModTime().UnixMilli()
		}
	}
	return st
}

// KaptchaEnabled 报告当前是否真的能自动识别。
//
// 这是 protocol 识别的可选接口：关闭时登录流程直接走人工输入，不会每张
// 验证码都调用一次 Recognize 再报错。
func (m *Manager) KaptchaEnabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.Enabled && m.rec != nil
}

// RecognizeKaptcha 实现 protocol.KaptchaRecognizer。
func (m *Manager) RecognizeKaptcha(ctx context.Context, img []byte) (string, float64, error) {
	if m == nil {
		return "", 0, ErrDisabled
	}
	m.mu.RLock()
	rec := m.rec
	enabled := m.settings.Enabled
	m.mu.RUnlock()
	if !enabled || rec == nil {
		return "", 0, ErrDisabled
	}
	return rec.Recognize(ctx, img)
}

// Reload 按当前保存的设置重新加载模型（不写配置文件）。
func (m *Manager) Reload() error {
	return m.apply(m.Settings(), false)
}

// Apply 校验并应用新设置；成功后才写 kaptcha.json 并替换正在使用的模型。
//
// 加载失败时保留原来的识别器与配置：用户填错一个路径，不应该把已经能用的
// 自动识别弄坏。
func (m *Manager) Apply(s Settings) error {
	return m.apply(s, true)
}

func (m *Manager) apply(s Settings, persist bool) error {
	s.ModelPath = strings.TrimSpace(s.ModelPath)
	if err := s.validate(); err != nil {
		// 参数校验失败没有发生加载，不要把错误写进 Status：否则一次输错阈值，
		// 状态卡会一直显示“模型出错”，直到下一次成功保存。
		return err
	}

	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	if !s.Enabled {
		if persist {
			if err := m.save(s); err != nil {
				return err
			}
		}
		m.mu.Lock()
		m.settings = s
		m.rec = nil
		m.loadedPath = ""
		m.loadedAt = time.Time{}
		m.loadErr = ""
		m.mu.Unlock()
		return nil
	}

	path, err := ResolveModelPath(s.ModelPath)
	if err != nil {
		m.setLoadErr(err)
		return err
	}
	rec, err := Load(path, Config{Threshold: s.Threshold, Workers: s.Workers})
	if err != nil {
		m.setLoadErr(err)
		return err
	}
	if persist {
		if err := m.save(s); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.settings = s
	m.rec = rec
	m.loadedPath = path
	m.loadedAt = time.Now()
	m.loadErr = ""
	m.mu.Unlock()
	return nil
}

func (m *Manager) setLoadErr(err error) {
	m.mu.Lock()
	m.loadErr = err.Error()
	m.mu.Unlock()
}

func (m *Manager) save(s Settings) error {
	data, err := json.Marshal(s, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	tmp := m.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入验证码配置失败: %w", err)
	}
	if err := os.Rename(tmp, m.file); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存验证码配置失败: %w", err)
	}
	return nil
}

// TestResult 是「测试识别」的结果：accepted 为 false 时 reason 说明原因
// （置信度不足或结果不像验证码），此时仍会带回模型认出的文本。
type TestResult struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Accepted   bool    `json:"accepted"`
	Threshold  float64 `json:"threshold"`
	Reason     string  `json:"reason,omitempty"`
}

// TestRecognize 用当前模型识别一张图，不做“失败就报错”的短路：
// 低置信度与非法文本作为正常结果返回，方便配置页展示模型到底认出了什么。
func (m *Manager) TestRecognize(ctx context.Context, img []byte) (*TestResult, error) {
	if m == nil {
		return nil, ErrDisabled
	}
	m.mu.RLock()
	rec := m.rec
	m.mu.RUnlock()
	if rec == nil {
		return nil, ErrDisabled
	}
	text, conf, err := rec.Recognize(ctx, img)
	res := &TestResult{Text: text, Confidence: conf, Threshold: rec.Threshold()}
	if err == nil {
		res.Accepted = true
		return res, nil
	}
	if errors.Is(err, ErrLowConfidence) || errors.Is(err, ErrBadText) {
		res.Reason = err.Error()
		return res, nil
	}
	return nil, err
}
