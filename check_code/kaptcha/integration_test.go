// 端到端测试：真模型 + 真 HTTP 图片 + protocol 的登录流程，
// 验证"字符验证码不需要人来填"。放在外部测试包里，免得 kaptcha 包
// 为了测试反向依赖 protocol。
package kaptcha_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"school_sdk/check_code/kaptcha"
	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// onnxDir 找 kaptcha-onnx 仓库（模型 + 真实合成验证码图片）。
//
// 找不到就跳过：这个仓库本身不带模型，CI/新克隆默认没有 ../../../kaptcha-onnx。
func onnxDir(t *testing.T) string {
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
		if _, err := os.Stat(filepath.Join(dir, kaptcha.DefaultModelName)); err == nil {
			return dir
		}
		tried = append(tried, dir)
	}
	t.Skipf("没有 kaptcha-onnx 仓库（找过 %s），跳过端到端测试", strings.Join(tried, "、"))
	return ""
}

// TestAutoKaptchaEndToEnd 用真模型跑一条完整登录链路：
//
//	假正方登录页 → 真验证码图片 → kaptcha 识别 → protocol 提交 → 登录成功
//
// 全程没有人工输入。InputHandler 一旦被调用就让测试失败，因为那说明回退了。
func TestAutoKaptchaEndToEnd(t *testing.T) {
	dir := onnxDir(t)
	rec, err := kaptcha.Load(filepath.Join(dir, kaptcha.DefaultModelName), kaptcha.DefaultConfig())
	if err != nil {
		t.Fatalf("加载模型: %v", err)
	}

	// 用一张自带答案的样例图（文件名最后一段就是答案）。
	imgPath := filepath.Join("testdata", "0000000_d_default_3f2nn5.jpg")
	if _, err := os.Stat(imgPath); err != nil {
		imgPath = filepath.Join(dir, "testdata", "0000000_d_default_3f2nn5.jpg")
	}
	img, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatalf("读样例图: %v", err)
	}
	want := "3f2nn5"

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("生成 RSA 密钥: %v", err)
	}
	modulus := base64.StdEncoding.EncodeToString(key.N.Bytes())
	exponent := base64.StdEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())

	var submitted []string
	mux := http.NewServeMux()
	mux.HandleFunc(config.LoginIndex, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<html><body>
<input id="csrftoken" value="tok">
<div id="yzmDiv">请输入验证码</div>
</body></html>`))
			return
		}
		_ = r.ParseForm()
		yzm := r.PostForm.Get("yzm")
		submitted = append(submitted, yzm)
		if yzm != want {
			// 失败页不能回显 value="<账号>"，否则 isLogin 会判成登录成功。
			_, _ = w.Write([]byte(`<html><body><div id="tips">验证码输入错误！</div></body></html>`))
			return
		}
		w.Header().Set("Location", config.MENU)
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc(config.KAPTCHA, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(img)
	})
	mux.HandleFunc(config.PublicKey, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"modulus": modulus, "exponent": exponent})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := config.Settings{
		BaseURL:   srv.URL,
		Account:   "student",
		Password:  "secret",
		UserAgent: "Edg/142.0.0.0",
		Timeout:   config.Duration(10 * time.Second),
	}
	api, err := protocol.NewAPIClient(cfg, nil, false, false, "")
	if err != nil {
		t.Fatalf("NewAPIClient: %v", err)
	}
	api.Kaptcha = rec
	api.InputHandler = func(context.Context, *protocol.InputRequest) (string, error) {
		t.Error("自动识别可用时不该请求人工输入")
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !api.LoginWithContext(ctx) {
		t.Fatalf("登录失败，已提交: %v", submitted)
	}
	if len(submitted) != 1 || submitted[0] != want {
		t.Fatalf("提交的验证码 = %v，want [%s]", submitted, want)
	}
}
