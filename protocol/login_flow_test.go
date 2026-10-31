package protocol

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"school_sdk/protocol/config"
)

// TestLoginReportsSemanticStages 用一条字符验证码登录链路确认：
// 登录页、验证码图片、公钥、提交这几个阶段都由 protocol 自己上报，
// 不依赖 ProtocolHook（这个客户端没有挂 hook）。
func TestLoginReportsSemanticStages(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("生成 RSA 密钥: %v", err)
	}
	modulus := base64.StdEncoding.EncodeToString(key.N.Bytes())
	exponent := base64.StdEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())

	var loginPage, captcha, pubkey, submit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == config.LoginIndex && r.Method == http.MethodGet:
			loginPage.Store(true)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body>
<input id="csrftoken" value="tok">
<div id="yzmDiv">请输入验证码</div>
</body></html>`))
		case r.URL.Path == config.KAPTCHA:
			captcha.Store(true)
			_, _ = w.Write([]byte("fake-kaptcha"))
		case r.URL.Path == config.PublicKey:
			pubkey.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"modulus":  modulus,
				"exponent": exponent,
			})
		case r.URL.Path == config.LoginIndex && r.Method == http.MethodPost:
			submit.Store(true)
			w.Header().Set("Location", config.MENU)
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	api.InputHandler = func(_ context.Context, req *InputRequest) (string, error) {
		if req.Kind != "captcha" {
			t.Errorf("InputHandler kind = %q, want captcha", req.Kind)
		}
		if len(req.ImageData) == 0 {
			t.Error("InputHandler 没拿到验证码图片")
		}
		return "1234", nil
	}

	var phases []LoginPhase
	api.SetLoginObserver(func(st LoginStage) {
		phases = append(phases, st.Phase)
	})

	if !api.LoginWithContext(context.Background()) {
		t.Fatal("登录失败")
	}
	if !loginPage.Load() || !captcha.Load() || !pubkey.Load() || !submit.Load() {
		t.Fatalf("请求缺失: loginPage=%v captcha=%v pubkey=%v submit=%v",
			loginPage.Load(), captcha.Load(), pubkey.Load(), submit.Load())
	}

	want := []LoginPhase{
		LoginPhasePrepare,
		LoginPhaseFetchForm,
		LoginPhaseCaptchaImage,
		LoginPhaseCaptcha, // 没有自动识别器，这里上报"等待输入字符验证码"
		LoginPhasePublicKey,
		LoginPhaseSubmit,
		LoginPhaseDone,
	}
	if !slices.Equal(phases, want) {
		t.Fatalf("阶段序列 = %v, want %v", phases, want)
	}
}

// TestGetRTKReportsStage 确认滑块流程的第一步（取 rtk）也会进时间线。
func TestGetRTKReportsStage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`var zfdun = {rtk:'rtk-value'};`))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	var phases []LoginPhase
	api.SetLoginObserver(func(st LoginStage) {
		phases = append(phases, st.Phase)
	})

	if got := api.getRTK(context.Background()); got != "rtk-value" {
		t.Fatalf("getRTK = %q, want rtk-value", got)
	}
	if len(phases) != 1 || phases[0] != LoginPhaseRTK {
		t.Fatalf("阶段 = %v, want [%s]", phases, LoginPhaseRTK)
	}
}
