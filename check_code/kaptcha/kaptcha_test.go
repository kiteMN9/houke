package kaptcha

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// oracleDir 返回 kaptcha-onnx 仓库路径。
//
// 模型（11 MB）没有复制进这个仓库，仍然从 kaptcha-onnx 仓库加载；默认取
// ../../../kaptcha-onnx（本包在 school_sdk_go/check_code/kaptcha 下），
// 可以用 KAPTCHA_ONNX_DIR 指定别的位置。
// 找不到仓库时跳过相关测试，保证在没有模型的环境里 go test ./... 仍然是绿的。
func oracleDir(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("KAPTCHA_ONNX_DIR"),
		filepath.Join("..", "..", "..", "kaptcha-onnx"),
		filepath.Join("..", "..", "kaptcha-onnx"),
		filepath.Join("..", "kaptcha-onnx"),
	}
	var tried []string
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if _, err := os.Stat(filepath.Join(dir, DefaultModelName)); err == nil {
			return dir
		}
		tried = append(tried, dir)
	}
	t.Skipf("没有 kaptcha-onnx 仓库（找过 %s），跳过需要模型的测试", strings.Join(tried, "、"))
	return ""
}

func loadTestRecognizer(t *testing.T, cfg Config) (*Recognizer, string) {
	t.Helper()
	dir := oracleDir(t)
	rec, err := Load(filepath.Join(dir, DefaultModelName), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return rec, dir
}

// labelOf 从文件名取答案：0000000_d_default_3f2nn5.jpg → 3f2nn5。
// kaptcha-onnx 的数据集就是这么命名的，所以测试不需要额外的标注文件。
func labelOf(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parts := strings.Split(base, "_")
	return parts[len(parts)-1]
}

// sampleFiles 返回本包自带的 6 张样例图；没有时回退到 kaptcha-onnx/testdata。
func sampleFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pat := range []string{"*.jpg", "*.png"} {
		m, err := filepath.Glob(filepath.Join("testdata", pat))
		if err != nil {
			t.Fatalf("glob testdata: %v", err)
		}
		files = append(files, m...)
	}
	if len(files) > 0 {
		return files
	}
	dir := oracleDir(t)
	for _, pat := range []string{"*.jpg", "*.png"} {
		m, err := filepath.Glob(filepath.Join(dir, "testdata", pat))
		if err != nil {
			t.Fatalf("glob %s/testdata: %v", dir, err)
		}
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Skip("没有样例图片")
	}
	return files
}

// TestRecognizeTestdata 用自带的 6 张样例跑端到端识别。
//func TestRecognizeTestdata(t *testing.T) {
//	rec, _ := loadTestRecognizer(t, DefaultConfig())
//	for _, f := range sampleFiles(t) {
//		raw, err := os.ReadFile(f)
//		if err != nil {
//			t.Fatalf("读 %s: %v", f, err)
//		}
//		text, conf, err := rec.Recognize(context.Background(), raw)
//		if err != nil {
//			t.Errorf("%s: Recognize: %v", filepath.Base(f), err)
//			continue
//		}
//		if want := labelOf(f); text != want {
//			t.Errorf("%s: 识别成 %q，答案 %q（置信度 %.4f）", filepath.Base(f), text, want, conf)
//		}
//		if conf <= 0 || conf > 1 {
//			t.Errorf("%s: 置信度超出 (0,1]: %v", filepath.Base(f), conf)
//		}
//	}
//}

// TestAccuracy 在 data/test 上抽样统计准确率，防止换模型/改预处理后悄悄退化。
//
// 这里的数字比 README 上偏低是正常的：data/test 里混了 c/r00..r19 等更难的画风，
// 而且误判的主要是 0/O、1/l 这类形近字符。
func TestAccuracy(t *testing.T) {
	rec, dir := loadTestRecognizer(t, DefaultConfig())
	var files []string
	for _, pat := range []string{"*.jpg", "*.png"} {
		m, err := filepath.Glob(filepath.Join(dir, "data", "test", pat))
		if err != nil {
			t.Fatalf("glob data/test: %v", err)
		}
		files = append(files, m...)
	}
	if len(files) < 100 {
		t.Skipf("data/test 只有 %d 张图，跳过准确率统计", len(files))
	}

	const stride = 10 // 取样 1/10，约 300 张
	var total, ok, accepted, acceptedOK int
	for i := 0; i < len(files); i += stride {
		raw, err := os.ReadFile(files[i])
		if err != nil {
			t.Fatalf("读 %s: %v", files[i], err)
		}
		total++
		want := labelOf(files[i])
		text, _, err := rec.Recognize(context.Background(), raw)
		if err != nil {
			if !errors.Is(err, ErrLowConfidence) && !errors.Is(err, ErrBadText) {
				t.Fatalf("%s: 非预期错误: %v", filepath.Base(files[i]), err)
			}
			continue
		}
		accepted++
		if text == want {
			ok++
			acceptedOK++
		}
	}
	if total == 0 {
		t.Fatal("没有取到样本")
	}
	acc := float64(ok) / float64(total)
	precision := 0.0
	if accepted > 0 {
		precision = float64(acceptedOK) / float64(accepted)
	}
	t.Logf("抽样 %d 张：整串准确率 %.2f%%，阈值过滤后可信 %.2f%%（放行 %d 张）",
		total, acc*100, precision*100, accepted)

	if acc < 0.90 {
		t.Errorf("整串准确率 %.2f%% 低于 90%%", acc*100)
	}
	if accepted > total/4 && precision < 0.96 {
		t.Errorf("阈值放行后的准确率只有 %.2f%%，阈值 %.2f 太松", precision*100, rec.Threshold())
	}
}

func TestRecognizeEmptyImage(t *testing.T) {
	rec, _ := loadTestRecognizer(t, DefaultConfig())
	if _, _, err := rec.Recognize(context.Background(), nil); !errors.Is(err, ErrEmptyImage) {
		t.Fatalf("空图片应返回 ErrEmptyImage，实际 %v", err)
	}
	if _, _, err := rec.Recognize(context.Background(), []byte{}); !errors.Is(err, ErrEmptyImage) {
		t.Fatalf("空切片应返回 ErrEmptyImage，实际 %v", err)
	}
}

func TestRecognizeNotAnImage(t *testing.T) {
	rec, _ := loadTestRecognizer(t, DefaultConfig())
	_, _, err := rec.Recognize(context.Background(), []byte("this is not a png"))
	if err == nil {
		t.Fatal("非图片数据应当报错")
	}
	if errors.Is(err, ErrEmptyImage) || errors.Is(err, ErrLowConfidence) {
		t.Fatalf("非图片数据的错误分类不对: %v", err)
	}
}

func TestRecognizeCanceledContext(t *testing.T) {
	rec, _ := loadTestRecognizer(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := rec.Recognize(ctx, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}
}

// TestThresholdRejects 把阈值调到 1 以上：结果一定低于阈值，用来验证
// "认出来了但拿不准"会走 ErrLowConfidence 而不是被当成成功。
func TestThresholdRejects(t *testing.T) {
	dir := oracleDir(t)
	rec, err := Load(filepath.Join(dir, DefaultModelName), Config{Threshold: 1.01})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	files := sampleFiles(t)
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("读 %s: %v", files[0], err)
	}
	text, conf, err := rec.Recognize(context.Background(), raw)
	if !errors.Is(err, ErrLowConfidence) {
		t.Fatalf("应当返回 ErrLowConfidence，实际 %v", err)
	}
	if text == "" {
		t.Error("低置信度时仍应带回识别文本，便于排查")
	}
	if conf <= 0 || conf >= 1.01 {
		t.Errorf("置信度不合理: %v", conf)
	}
}

// TestRecognizeConcurrent 同时识别同一张图：识别器要能被多个账号登录共用。
func TestRecognizeConcurrent(t *testing.T) {
	rec, _ := loadTestRecognizer(t, DefaultConfig())
	files := sampleFiles(t)
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("读 %s: %v", files[0], err)
	}
	want := labelOf(files[0])

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, _, err := rec.Recognize(context.Background(), raw)
			if err != nil {
				errs <- err
				return
			}
			if text != want {
				errs <- errors.New("并发识别结果不一致: " + text)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestValidText(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"abc123", true},
		{"ABCZ09", true},
		{"", false},
		{"abc12", false},   // 少一位
		{"abc1234", false}, // 多一位
		{"abc 12", false},  // 带空格
		{"abc-12", false},  // 带标点
		{"中文abcd", false},
	}
	for _, c := range cases {
		if got := validText(c.in); got != c.want {
			t.Errorf("validText(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveModelPathExplicit(t *testing.T) {
	_, err := ResolveModelPath(filepath.Join(t.TempDir(), "nope.onnx"))
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("显式路径不存在应返回 ErrNoModel，实际 %v", err)
	}

	dir := oracleDir(t)
	want := filepath.Join(dir, DefaultModelName)
	got, err := ResolveModelPath(want)
	if err != nil {
		t.Fatalf("ResolveModelPath: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestResolveModelPathAutoDetect 验证"把 model.onnx 放到当前目录"的零配置发现。
func TestResolveModelPathAutoDetect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultModelName), []byte("dummy"), 0o600); err != nil {
		t.Fatalf("写占位模型: %v", err)
	}
	t.Chdir(dir)
	got, err := ResolveModelPath("")
	if err != nil {
		t.Fatalf("ResolveModelPath: %v", err)
	}
	if got != DefaultModelName && got != filepath.Join(dir, DefaultModelName) {
		t.Fatalf("应当在当前目录发现 %s，实际 %q", DefaultModelName, got)
	}
}

func TestResolveModelPathNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := ResolveModelPath("")
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("没有模型时应返回 ErrNoModel，实际 %v", err)
	}
	if !strings.Contains(err.Error(), DefaultModelName) {
		t.Errorf("错误信息应提到 %s，实际 %v", DefaultModelName, err)
	}
}

func TestLoadBytesInvalid(t *testing.T) {
	if _, err := LoadBytes([]byte("not an onnx model"), DefaultConfig(), "bad.onnx"); err == nil {
		t.Fatal("坏模型应当报错")
	}
}

func TestLoadBytesThresholdDefault(t *testing.T) {
	dir := oracleDir(t)
	raw, err := os.ReadFile(filepath.Join(dir, DefaultModelName))
	if err != nil {
		t.Fatalf("读模型: %v", err)
	}
	rec, err := LoadBytes(raw, Config{}, "model.onnx")
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if rec.Threshold() != DefaultThreshold {
		t.Fatalf("零值 Config 应取默认阈值 %v，实际 %v", DefaultThreshold, rec.Threshold())
	}
	if rec.ModelPath() != "model.onnx" {
		t.Fatalf("ModelPath = %q", rec.ModelPath())
	}
}
