package kaptcha

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewManagerDefaults(t *testing.T) {
	m := NewManager(DefaultSettings())
	got := m.Settings()
	if !got.Enabled {
		t.Error("默认应启用自动识别")
	}
	if got.Threshold != DefaultThreshold {
		t.Errorf("默认阈值 = %v, want %v", got.Threshold, DefaultThreshold)
	}
	if m.KaptchaEnabled() {
		t.Error("模型尚未加载时 KaptchaEnabled 应为 false")
	}
	st := m.Status()
	// 未加载时零值时间不能变成 -62135596800000，空路径也不能让 Base 得到 "."。
	if st.LoadedAt != 0 {
		t.Errorf("未加载时 LoadedAt = %d, want 0", st.LoadedAt)
	}
	if st.ModelName == "." {
		t.Errorf("ModelName 不应为 %q", st.ModelName)
	}
}

func TestManagerConfigure(t *testing.T) {
	m := NewManager(DefaultSettings())
	m.Configure(Settings{Enabled: false, Threshold: 0.8}, "server.json")

	got := m.Settings()
	if got.Enabled || got.Threshold != 0.8 {
		t.Errorf("Configure 未生效: %+v", got)
	}
	if st := m.Status(); st.SettingsFile != "server.json" {
		t.Errorf("SettingsFile = %q, want server.json", st.SettingsFile)
	}
}

func TestModelCandidatesUnique(t *testing.T) {
	paths := modelCandidates()
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if seen[p] {
			t.Errorf("候选路径重复: %s", p)
		}
		seen[p] = true
	}
}

func TestManagerDisable(t *testing.T) {
	m := NewManager(DefaultSettings())
	var saved *Settings
	err := m.Apply(Settings{Enabled: false, Threshold: DefaultThreshold}, func(s Settings) error {
		cp := s
		saved = &cp
		return nil
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if saved == nil || saved.Enabled {
		t.Errorf("save 回调应拿到关闭后的设置，实际 %+v", saved)
	}
	if m.KaptchaEnabled() {
		t.Error("关闭后 KaptchaEnabled 应为 false")
	}
	if _, _, err := m.RecognizeKaptcha(context.Background(), []byte("x")); !errors.Is(err, ErrDisabled) {
		t.Errorf("关闭后 RecognizeKaptcha 应返回 ErrDisabled，实际 %v", err)
	}
	if _, err := m.TestRecognize(context.Background(), []byte("x")); !errors.Is(err, ErrDisabled) {
		t.Errorf("关闭后 TestRecognize 应返回 ErrDisabled，实际 %v", err)
	}
}

func TestManagerApplySaveFailureKeepsOldSettings(t *testing.T) {
	m := NewManager(DefaultSettings())
	boom := errors.New("写盘失败")
	err := m.Apply(Settings{Enabled: false, Threshold: DefaultThreshold}, func(Settings) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Apply err = %v, want %v", err, boom)
	}
	if !m.Settings().Enabled {
		t.Error("save 失败时不应提交新设置")
	}
}

func TestManagerApplyValidation(t *testing.T) {
	m := NewManager(DefaultSettings())
	cases := []Settings{
		{Enabled: true, Threshold: 0},
		{Enabled: true, Threshold: 1.2},
		{Enabled: true, Threshold: DefaultThreshold, Workers: -1},
		{Enabled: true, Threshold: DefaultThreshold, Workers: 65},
		{Enabled: true, Threshold: DefaultThreshold, ModelPath: "model.bin"},
	}
	for _, s := range cases {
		if err := m.Apply(s, nil); err == nil {
			t.Errorf("Apply(%+v) 应当报错", s)
		}
	}
	if m.Settings().Threshold != DefaultThreshold {
		t.Errorf("校验失败后设置被改坏了: %+v", m.Settings())
	}
	if st := m.Status(); st.Error != "" {
		t.Errorf("参数校验失败不应污染加载状态: %q", st.Error)
	}
}

func TestManagerApplyMissingModelKeepsOldSettings(t *testing.T) {
	m := NewManager(DefaultSettings())
	err := m.Apply(Settings{
		Enabled:   true,
		ModelPath: filepath.Join(t.TempDir(), "nope.onnx"),
		Threshold: 0.8,
	}, nil)
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("Apply 不存在的模型应返回 ErrNoModel，实际 %v", err)
	}
	if got := m.Settings(); got.Threshold != DefaultThreshold || got.ModelPath != "" {
		t.Errorf("加载失败后设置不该变，实际 %+v", got)
	}
	if st := m.Status(); st.Error == "" {
		t.Error("Status 应带回加载错误")
	}
}

func TestManagerApplyRealModel(t *testing.T) {
	dir := oracleDir(t)
	model := filepath.Join(dir, DefaultModelName)
	m := NewManager(DefaultSettings())
	if err := m.Apply(Settings{Enabled: true, ModelPath: model, Threshold: DefaultThreshold}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !m.KaptchaEnabled() {
		t.Fatal("加载成功后 KaptchaEnabled 应为 true")
	}
	st := m.Status()
	if !st.Loaded || !st.Enabled || !st.ModelExists {
		t.Errorf("Status 未体现加载成功: %+v", st)
	}
	if st.ModelPath != model {
		t.Errorf("ModelPath = %q, want %q", st.ModelPath, model)
	}

	files := sampleFiles(t)
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("读样例图: %v", err)
	}
	text, conf, err := m.RecognizeKaptcha(context.Background(), raw)
	if err != nil {
		t.Fatalf("RecognizeKaptcha: %v", err)
	}
	if text != labelOf(files[0]) {
		t.Errorf("识别成 %q，答案 %q", text, labelOf(files[0]))
	}
	if conf <= 0 || conf > 1 {
		t.Errorf("置信度超出 (0,1]: %v", conf)
	}

	// TestRecognize 不做阈值短路，低置信度也作为正常结果返回。
	res, err := m.TestRecognize(context.Background(), raw)
	if err != nil {
		t.Fatalf("TestRecognize: %v", err)
	}
	if res.Text != text || !res.Accepted || res.Confidence != conf {
		t.Errorf("TestRecognize = %+v, want text=%q conf=%v accepted", res, text, conf)
	}
}
