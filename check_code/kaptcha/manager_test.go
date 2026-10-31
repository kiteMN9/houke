package kaptcha

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewManagerDefaults(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kaptcha.json")
	m, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	got := m.Settings()
	if !got.Enabled {
		t.Error("默认应启用自动识别")
	}
	if got.Threshold != DefaultThreshold {
		t.Errorf("默认阈值 = %v, want %v", got.Threshold, DefaultThreshold)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("NewManager 不应创建配置文件，stat err = %v", err)
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

func TestNewManagerCorruptConfigFallsBack(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kaptcha.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(file, DefaultSettings())
	if err == nil {
		t.Fatal("损坏的配置应返回错误")
	}
	if m == nil {
		t.Fatal("损坏的配置也应返回一个可用的 Manager，供配置页覆盖保存")
	}
	if !m.Settings().Enabled || m.Settings().Threshold != DefaultThreshold {
		t.Errorf("损坏配置回落后应使用默认设置: %+v", m.Settings())
	}
	// 配置页保存一次就能把损坏文件覆盖成合法 JSON。
	if err := m.Apply(Settings{Enabled: false, Threshold: DefaultThreshold}); err != nil {
		t.Fatalf("覆盖保存: %v", err)
	}
	m2, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("重新读取: %v", err)
	}
	if m2.Settings().Enabled {
		t.Error("重新读取后 enabled 应为 false")
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

func TestManagerDisablePersists(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kaptcha.json")
	m, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Apply(Settings{Enabled: false, Threshold: DefaultThreshold}); err != nil {
		t.Fatalf("Apply: %v", err)
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

	// 配置文件必须真正写下去，重启后仍然是关闭状态。
	m2, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("重新读取: %v", err)
	}
	if m2.Settings().Enabled {
		t.Error("重新加载后 enabled 应为 false")
	}
}

func TestManagerApplyValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kaptcha.json")
	m, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	cases := []Settings{
		{Enabled: true, Threshold: 0},
		{Enabled: true, Threshold: 1.2},
		{Enabled: true, Threshold: DefaultThreshold, Workers: -1},
		{Enabled: true, Threshold: DefaultThreshold, Workers: 65},
		{Enabled: true, Threshold: DefaultThreshold, ModelPath: "model.bin"},
	}
	for _, s := range cases {
		if err := m.Apply(s); err == nil {
			t.Errorf("Apply(%+v) 应当报错", s)
		}
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("校验失败不应写配置文件，stat err = %v", err)
	}
	if m.Settings().Threshold != DefaultThreshold {
		t.Errorf("校验失败后设置被改坏了: %+v", m.Settings())
	}
	if st := m.Status(); st.Error != "" {
		t.Errorf("参数校验失败不应污染加载状态: %q", st.Error)
	}
}

func TestManagerApplyMissingModelKeepsOldSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kaptcha.json")
	m, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	err = m.Apply(Settings{
		Enabled:   true,
		ModelPath: filepath.Join(t.TempDir(), "nope.onnx"),
		Threshold: 0.8,
	})
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
	file := filepath.Join(t.TempDir(), "kaptcha.json")
	m, err := NewManager(file, DefaultSettings())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Apply(Settings{Enabled: true, ModelPath: model, Threshold: DefaultThreshold}); err != nil {
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
