// Package kaptcha 识别正方（ZFSoft）登录页的字符验证码。
//
// 这里原来依赖 github.com/openschoolcn/kaptcha-onnx；现在 ONNX 解析、推理和
// 图片预处理都已并入本包（internal/onnx、internal/imagex），因此不需要 cgo，
// 也不再有外部模块依赖。
//
// 包内分两层：
//
//   - Recognizer：加载模型、跑推理、按整串置信度判断能不能信；
//   - Manager：把 Recognizer 和一份可持久化的配置（kaptcha.json）绑在一起，
//     支持 WebUI 热切换模型 / 阈值，见 manager.go。
package kaptcha

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"school_sdk/check_code/kaptcha/internal/imagex"
	"school_sdk/check_code/kaptcha/internal/onnx"
)

// 模型规格，必须与导出模型时的训练脚本常量一致。
const (
	NumChars    = 6   // 每张图固定 6 个字符
	NumClasses  = 62  // 0-9A-Za-z
	ImageWidth  = 200 // 模型输入宽
	ImageHeight = 50  // 模型输入高
	Logits      = NumChars * NumClasses
)

// Charset 是类别索引到字符的映射，顺序必须和训练一致。
const Charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const (
	// DefaultModelName 是模型的默认文件名：放在可执行文件旁边、当前目录，
	// 或这两个目录下的 models/ 里都能被自动发现。
	DefaultModelName = "model.onnx"

	// DefaultThreshold 是整串置信度（6 个字符 softmax 概率之积）的下限。
	//
	// 正确识别时置信度通常在 0.99 以上，个别难图会掉到 0.95 左右；猜错时
	// 哪怕只错一位，整串概率也会明显偏低。0.9 能挡掉大部分瞎猜，又几乎不
	// 误伤正确结果（是否合适仍要用真实学校样本校准）。
	DefaultThreshold = 0.9

	// MaxModelBytes 是允许加载 / 上传的模型大小上限。
	MaxModelBytes = 64 << 20
)

var (
	// ErrNoModel 表示找不到模型文件。
	ErrNoModel = errors.New("找不到验证码识别模型")
	// ErrEmptyImage 表示传进来的验证码图片是空的。
	ErrEmptyImage = errors.New("验证码图片为空")
	// ErrLowConfidence 表示识别出了内容，但置信度低于阈值。
	ErrLowConfidence = errors.New("验证码识别置信度不足")
	// ErrBadText 表示识别结果不像验证码（长度或字符集不对）。
	ErrBadText = errors.New("验证码识别结果不可用")
	// ErrDisabled 表示自动识别当前被关闭，或模型还没加载成功。
	ErrDisabled = errors.New("字符验证码自动识别未启用")
)

// Char 是单个字符的识别结果。
type Char struct {
	Index      int     `json:"index"`
	Char       string  `json:"char"`
	Confidence float64 `json:"confidence"`
}

// Result 是一张图片的识别结果。
type Result struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Chars      []Char  `json:"chars"`
}

// Config 是识别器的参数；零值等价于 DefaultConfig()。
type Config struct {
	// Threshold 是整串置信度下限，小于等于 0 时取 DefaultThreshold。
	Threshold float64
	// Workers 是推理并发度，小于等于 0 时由 ONNX 执行器自己决定。
	Workers int
}

// DefaultConfig 返回默认参数。
func DefaultConfig() Config {
	return Config{Threshold: DefaultThreshold}
}

// Recognizer 是并发安全的识别器。
//
// 内部推理是串行的（一张十几毫秒），多个账号同时登录时排队即可：
// 为登录场景开多个 ONNX 解释器只会多占内存。
type Recognizer struct {
	sess *onnx.Session

	mu sync.Mutex // 串行化推理：单张图已经把 CPU 用满了，并行只会互相抢核
	in []float32

	threshold float64
	modelPath string
}

// Load 读入模型文件，modelPath 为空时按 ResolveModelPath 的规则查找。
func Load(modelPath string, cfg Config) (*Recognizer, error) {
	path, err := ResolveModelPath(modelPath)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNoModel, path)
	}
	if st.Size() > MaxModelBytes {
		return nil, fmt.Errorf("验证码识别模型过大（%d 字节，上限 %d）", st.Size(), int64(MaxModelBytes))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNoModel, path, err)
	}
	return LoadBytes(raw, cfg, path)
}

// LoadBytes 从内存里的 ONNX 内容建识别器；modelPath 只用于状态展示与日志。
func LoadBytes(raw []byte, cfg Config, modelPath string) (*Recognizer, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("验证码识别模型 %s 内容为空", modelPath)
	}
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	m, err := onnx.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("加载验证码识别模型 %s: %w", modelPath, err)
	}
	var opts []onnx.SessionOption
	if cfg.Workers > 0 {
		opts = append(opts, onnx.WithWorkers(cfg.Workers))
	}
	sess, err := onnx.NewSession(m, opts...)
	if err != nil {
		return nil, fmt.Errorf("加载验证码识别模型 %s: %w", modelPath, err)
	}
	dims := sess.InputDims()
	if len(dims) != 4 {
		return nil, fmt.Errorf("验证码识别模型输入应为 4 维 (N,1,%d,%d)，实际 %v", ImageHeight, ImageWidth, dims)
	}
	if dims[1] > 0 && dims[1] != 1 {
		return nil, fmt.Errorf("验证码识别模型输入通道应为 1，实际 %d", dims[1])
	}
	if dims[2] > 0 && dims[2] != ImageHeight {
		return nil, fmt.Errorf("验证码识别模型输入高应为 %d，实际 %d", ImageHeight, dims[2])
	}
	if dims[3] > 0 && dims[3] != ImageWidth {
		return nil, fmt.Errorf("验证码识别模型输入宽应为 %d，实际 %d", ImageWidth, dims[3])
	}
	return &Recognizer{
		sess:      sess,
		in:        make([]float32, ImageHeight*ImageWidth),
		threshold: threshold,
		modelPath: modelPath,
	}, nil
}

// ModelPath 返回实际使用的模型路径。
func (r *Recognizer) ModelPath() string { return r.modelPath }

// Threshold 返回整串置信度下限。
func (r *Recognizer) Threshold() float64 { return r.threshold }

// Recognize 识别一张验证码图片（JPEG/PNG 原始字节），返回验证码文本与整串置信度。
//
// 返回值的语义：
//   - 成功：文本、置信度、nil；
//   - 图片为空：ErrEmptyImage；
//   - 不是图片或推理失败：包一层底层错误；
//   - 结果不像验证码：ErrBadText；
//   - 置信度低于阈值：ErrLowConfidence，同时带回文本与置信度，方便日志排查。
//
// 失败时也尽量返回置信度：调用方因此能区分"模型很确信但这张图坏了"和
// "模型拿不准"，进而决定重取一张还是直接改人工输入。
func (r *Recognizer) Recognize(ctx context.Context, img []byte) (string, float64, error) {
	if len(img) == 0 {
		return "", 0, ErrEmptyImage
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	res, err := r.RecognizeBytes(img)
	if err != nil {
		return "", 0, fmt.Errorf("验证码推理失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	text := strings.TrimSpace(res.Text)
	if !validText(text) {
		return "", res.Confidence, fmt.Errorf("%w: %q", ErrBadText, text)
	}
	if res.Confidence < r.threshold {
		return text, res.Confidence, fmt.Errorf("%w: %.4f < %.2f", ErrLowConfidence, res.Confidence, r.threshold)
	}
	return text, res.Confidence, nil
}

// RecognizeKaptcha 与 Recognize 等价，只是名字和 protocol.KaptchaRecognizer
// 的方法一致：*Recognizer 可以直接注入 protocol.APIClient.Kaptcha。
func (r *Recognizer) RecognizeKaptcha(ctx context.Context, img []byte) (string, float64, error) {
	return r.Recognize(ctx, img)
}

// RecognizeImage 识别一张已经解码好的图片。
func (r *Recognizer) RecognizeImage(img image.Image) (*Result, error) {
	gray, err := imagex.Gray(img, ImageWidth, ImageHeight)
	if err != nil {
		return nil, err
	}
	return r.recognizeGray(gray)
}

// RecognizeBytes 识别一张图片的原始字节（png/jpeg/gif），返回完整结果。
//
// 与 Recognize 不同，这里不做阈值判断，也不会把低置信度当成错误；WebUI 的
// 「测试识别」需要看到模型到底认出了什么。
func (r *Recognizer) RecognizeBytes(raw []byte) (*Result, error) {
	img, err := imagex.Decode(raw)
	if err != nil {
		return nil, err
	}
	return r.RecognizeImage(img)
}

// RecognizeFile 识别一个图片文件。
func (r *Recognizer) RecognizeFile(path string) (*Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return r.RecognizeBytes(raw)
}

func (r *Recognizer) recognizeGray(gray []float32) (*Result, error) {
	if len(gray) != len(r.in) {
		return nil, fmt.Errorf("验证码灰度图长度 %d，期望 %d", len(gray), len(r.in))
	}
	in := &onnx.Tensor{
		DataType: onnx.DataTypeFloat32,
		Dims:     []int64{1, 1, ImageHeight, ImageWidth},
		Floats:   gray,
	}

	r.mu.Lock()
	out, err := r.sess.Run(in)
	r.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("推理失败: %w", err)
	}
	logits, err := out.Float32()
	if err != nil {
		return nil, err
	}
	if len(logits) != Logits {
		return nil, fmt.Errorf("模型输出 %d 个值，期望 %d", len(logits), Logits)
	}

	res := &Result{Chars: make([]Char, 0, NumChars)}
	text := make([]byte, 0, NumChars)
	joint := 1.0
	for i := 0; i < NumChars; i++ {
		seg := logits[i*NumClasses : (i+1)*NumClasses]
		best := 0
		for j := 1; j < len(seg); j++ {
			if seg[j] > seg[best] {
				best = j
			}
		}
		p := softmaxAt(seg, best)
		text = append(text, Charset[best])
		res.Chars = append(res.Chars, Char{Index: i, Char: string(Charset[best]), Confidence: p})
		joint *= p
	}
	res.Text = string(text)
	res.Confidence = joint
	return res, nil
}

// softmaxAt 返回第 idx 类的 softmax 概率（数值稳定写法）。
func softmaxAt(logits []float32, idx int) float64 {
	maxv := logits[0]
	for _, v := range logits[1:] {
		if v > maxv {
			maxv = v
		}
	}
	var sum float64
	for _, v := range logits {
		sum += math.Exp(float64(v - maxv))
	}
	return math.Exp(float64(logits[idx]-maxv)) / sum
}

// validText 粗筛识别结果：正方字符验证码是 6 位字母数字，
// 空串、带空格或标点的结果不该被当成验证码提交。
func validText(s string) bool {
	if len(s) != NumChars {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}

// ResolveModelPath 决定从哪读模型：
//
//  1. explicit 非空：只认它，文件不存在直接报错（不去猜别的路径，避免拿错模型）；
//  2. 可执行文件同目录、当前工作目录下的 DefaultModelName；
//  3. 这两个目录的 models/ 下的 DefaultModelName。
func ResolveModelPath(explicit string) (string, error) {
	if explicit != "" {
		if st, err := os.Stat(explicit); err != nil || st.IsDir() {
			return "", fmt.Errorf("%w: %s", ErrNoModel, explicit)
		}
		return explicit, nil
	}
	var tried []string
	for _, p := range modelCandidates() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		tried = append(tried, p)
	}
	return "", fmt.Errorf("%w（已找过 %s）", ErrNoModel, strings.Join(tried, "、"))
}

// modelCandidates 返回候选路径：可执行文件所在目录、当前工作目录，
// 以及这两个目录下的 models/（WebUI 上传的模型默认放这里）。
func modelCandidates() []string {
	// exe 目录和 cwd 相同（同目录启动、go run）时只保留一份，避免报错文案重复。
	var dirs []string
	addDir := func(dir string) {
		if dir == "" {
			return
		}
		for _, existing := range dirs {
			if existing == dir {
				return
			}
		}
		dirs = append(dirs, dir)
	}
	if exe, err := os.Executable(); err == nil {
		addDir(filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		addDir(wd)
	}
	var paths []string
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, DefaultModelName))
	}
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, "models", DefaultModelName))
	}
	return paths
}
