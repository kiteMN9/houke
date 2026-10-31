package protocol

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"school_sdk/protocol/config"
)

// fakeKaptcha 是可控的字符验证码识别器，用来验证"先自动、失败转人工"这条链路。
type fakeKaptcha struct {
	text    string  // 返回的验证码
	conf    float64 // 返回的置信度
	err     error   // 非空时模拟识别失败（例如低置信度）
	calls   int32
	lastImg []byte
}

func (f *fakeKaptcha) RecognizeKaptcha(_ context.Context, img []byte) (string, float64, error) {
	atomic.AddInt32(&f.calls, 1)
	f.lastImg = img
	if f.err != nil {
		return "", 0, f.err
	}
	return f.text, f.conf, nil
}

func (f *fakeKaptcha) Calls() int { return int(atomic.LoadInt32(&f.calls)) }

// kaptchaTestServer 起一个只要字符验证码的正方登录页：
//   - yzmWant 是唯一正确的验证码，提交对了就 302 到首页；
//   - 提交错了返回带 #tips "验证码输入错误！" 的登录页，和真实站点一致。
type kaptchaTestServer struct {
	srv     *httptest.Server
	yzmWant string
	posts   []string // 每次 POST 提交的 yzm
	images  int32    // /kaptcha 被取了几次
	submits int32    // POST 次数
}

func newKaptchaTestServer(t *testing.T, yzmWant string) *kaptchaTestServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("生成 RSA 密钥: %v", err)
	}
	modulus := base64.StdEncoding.EncodeToString(key.N.Bytes())
	exponent := base64.StdEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())

	ts := &kaptchaTestServer{yzmWant: yzmWant}
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
		got := r.PostForm.Get("yzm")
		atomic.AddInt32(&ts.submits, 1)
		ts.posts = append(ts.posts, got)
		if got != yzmWant {
			// 注意不要回显 value="<account>"，否则会被 isLogin 判成登录成功。
			_, _ = w.Write([]byte(`<html><body><div id="tips">验证码输入错误！</div></body></html>`))
			return
		}
		w.Header().Set("Location", config.MENU)
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc(config.KAPTCHA, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ts.images, 1)
		_, _ = w.Write([]byte("fake-kaptcha-bytes"))
	})
	mux.HandleFunc(config.PublicKey, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"modulus":  modulus,
			"exponent": exponent,
		})
	})
	ts.srv = httptest.NewServer(mux)
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *kaptchaTestServer) Posts() []string { return ts.posts }

// TestKaptchaAutoRecognize 自动识别成功时：不打扰用户，直接带着识别结果提交。
func TestKaptchaAutoRecognize(t *testing.T) {
	ts := newKaptchaTestServer(t, "ab12cd")
	api := newStateTestClient(t, ts.srv.URL)
	fake := &fakeKaptcha{text: "ab12cd", conf: 0.99}
	api.Kaptcha = fake

	var details []string
	api.SetLoginObserver(func(st LoginStage) {
		if st.Phase == LoginPhaseCaptcha {
			details = append(details, st.Detail)
		}
	})

	if !api.LoginWithContext(context.Background()) {
		t.Fatal("登录失败")
	}
	if fake.Calls() != 1 {
		t.Errorf("识别器被调用 %d 次，want 1", fake.Calls())
	}
	if string(fake.lastImg) != "fake-kaptcha-bytes" {
		t.Errorf("识别器拿到的图片 = %q", fake.lastImg)
	}
	posts := ts.Posts()
	if len(posts) != 1 {
		t.Fatalf("提交 %d 次，want 1: %v", len(posts), posts)
	}
	if posts[0] != "ab12cd" {
		t.Errorf("提交的 yzm = %q，want ab12cd", posts[0])
	}
	if len(details) != 1 || details[0] != CaptchaDetailKaptchaAuto {
		t.Errorf("验证码阶段 detail = %v，want [%s]", details, CaptchaDetailKaptchaAuto)
	}
}

// TestKaptchaFallsBackToInput 识别器报错（低置信度、坏图都长这样）时转人工输入。
func TestKaptchaFallsBackToInput(t *testing.T) {
	ts := newKaptchaTestServer(t, "human1")
	api := newStateTestClient(t, ts.srv.URL)
	api.Kaptcha = &fakeKaptcha{err: errors.New("置信度 0.42 < 0.90")}

	var asked int32
	api.InputHandler = func(_ context.Context, req *InputRequest) (string, error) {
		atomic.AddInt32(&asked, 1)
		if req.Kind != "captcha" {
			t.Errorf("InputHandler kind = %q", req.Kind)
		}
		if string(req.ImageData) != "fake-kaptcha-bytes" {
			t.Errorf("InputHandler 拿到的图片 = %q", req.ImageData)
		}
		return "human1", nil
	}

	var details []string
	api.SetLoginObserver(func(st LoginStage) {
		if st.Phase == LoginPhaseCaptcha {
			details = append(details, st.Detail)
		}
	})

	if !api.LoginWithContext(context.Background()) {
		t.Fatal("登录失败")
	}
	if got := atomic.LoadInt32(&asked); got != 1 {
		t.Errorf("人工输入被请求 %d 次，want 1", got)
	}
	if posts := ts.Posts(); len(posts) != 1 || posts[0] != "human1" {
		t.Errorf("提交的 yzm = %v，want [human1]", posts)
	}
	// 先上报"正在自动识别"，再上报"等待输入"：时间线上能看出为什么弹了输入框。
	want := []string{CaptchaDetailKaptchaAuto, CaptchaDetailKaptchaManual}
	if len(details) != len(want) || details[0] != want[0] || details[1] != want[1] {
		t.Errorf("验证码阶段 detail = %v，want %v", details, want)
	}
}

// TestKaptchaInvalidTextFallsBack 识别器"成功"返回了不像验证码的东西也要转人工。
func TestKaptchaInvalidTextFallsBack(t *testing.T) {
	ts := newKaptchaTestServer(t, "ok1234")
	api := newStateTestClient(t, ts.srv.URL)
	api.Kaptcha = &fakeKaptcha{text: "!!!", conf: 1}
	api.InputHandler = func(context.Context, *InputRequest) (string, error) { return "ok1234", nil }

	if !api.LoginWithContext(context.Background()) {
		t.Fatal("登录失败")
	}
	if posts := ts.Posts(); len(posts) != 1 || posts[0] != "ok1234" {
		t.Errorf("提交的 yzm = %v，want [ok1234]", posts)
	}
}

// TestKaptchaGivesUpAfterTwoFailures 自动识别连续两次被判错就转人工，
// 不再浪费重试次数：一次登录里识别器只该被用两次。
func TestKaptchaGivesUpAfterTwoFailures(t *testing.T) {
	ts := newKaptchaTestServer(t, "true1x")
	api := newStateTestClient(t, ts.srv.URL)
	fake := &fakeKaptcha{text: "wrong1", conf: 0.99}
	api.Kaptcha = fake
	var asked int32
	api.InputHandler = func(context.Context, *InputRequest) (string, error) {
		atomic.AddInt32(&asked, 1)
		return "true1x", nil
	}

	if !api.LoginWithContext(context.Background()) {
		t.Fatal("登录失败")
	}
	if fake.Calls() != maxKaptchaAutoFails {
		t.Errorf("识别器被调用 %d 次，want %d", fake.Calls(), maxKaptchaAutoFails)
	}
	if got := atomic.LoadInt32(&asked); got != 1 {
		t.Errorf("人工输入被请求 %d 次，want 1", got)
	}
	posts := ts.Posts()
	if len(posts) != 3 {
		t.Fatalf("提交 %d 次，want 3: %v", len(posts), posts)
	}
	if posts[2] != "true1x" {
		t.Errorf("最后一次提交 = %q，want true1x", posts[2])
	}
}

// TestKaptchaNilUsesInputHandler 不注入识别器时行为不变：直接问人。
func TestKaptchaNilUsesInputHandler(t *testing.T) {
	ts := newKaptchaTestServer(t, "plain1")
	api := newStateTestClient(t, ts.srv.URL)
	api.InputHandler = func(context.Context, *InputRequest) (string, error) { return "plain1", nil }

	if !api.LoginWithContext(context.Background()) {
		t.Fatal("登录失败")
	}
	if posts := ts.Posts(); len(posts) != 1 || posts[0] != "plain1" {
		t.Errorf("提交的 yzm = %v，want [plain1]", posts)
	}
	if got := atomic.LoadInt32(&ts.images); got != 1 {
		t.Errorf("取图 %d 次，want 1", got)
	}
}

func TestKaptchaPlan(t *testing.T) {
	// 没有识别器：永远走人工。
	api := &APIClient{}
	if plan := api.newKaptchaPlan(); plan.auto() {
		t.Error("Kaptcha 为 nil 时不该走自动识别")
	}

	// 有识别器：前 maxKaptchaAutoFails-1 次失败仍然自动，之后转人工。
	api.Kaptcha = &fakeKaptcha{}
	plan := api.newKaptchaPlan()
	if !plan.auto() {
		t.Fatal("注入识别器后应当先走自动识别")
	}
	for i := 1; i < maxKaptchaAutoFails; i++ {
		plan.failed()
		if !plan.auto() {
			t.Fatalf("第 %d 次失败就放弃自动识别了", i)
		}
	}
	plan.failed()
	if plan.auto() {
		t.Fatal("连续失败达到上限后应转人工输入")
	}
	// nil plan 与人工输入路径不该 panic。
	var nilPlan *kaptchaPlan
	nilPlan.failed()
	if nilPlan.auto() {
		t.Error("nil plan 不该走自动识别")
	}
}

func TestValidKaptchaText(t *testing.T) {
	cases := map[string]bool{
		"ab12cd":    true,
		"1234":      true, // 有的学校是 4 位
		"AB12cd":    true,
		"ab12cd7":   true, // 容忍 6~8 位，具体位数由识别器保证
		"":          false,
		"abc":       false, // 太短
		"ab12cd789": false, // 太长
		"ab 12cd":   false,
		"ab-12cd":   false,
	}
	for in, want := range cases {
		if got := validKaptchaText(in); got != want {
			t.Errorf("validKaptchaText(%q) = %v, want %v", in, got, want)
		}
	}
}
