package cas2

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// stepRecorder 收集 Login 期间上报的步骤，形如 "submit@2"。
type stepRecorder struct {
	mu    sync.Mutex
	steps []string
}

func (r *stepRecorder) handle(step Step, attempt int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, fmt.Sprintf("%s@%d", step, attempt))
}

func (r *stepRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.steps...)
}

// stepTestServer 只实现密码登录用到的三个端点，其余端点不会被请求。
// loginPage 直接作为 GET /cas/login 的响应；post 处理 POST /cas/login。
func stepTestServer(t *testing.T, loginPage string, post http.HandlerFunc) *httptest.Server {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("生成测试 RSA 私钥失败: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("序列化测试公钥失败: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	mux := http.NewServeMux()
	mux.HandleFunc(login, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			post(w, r)
			return
		}
		_, _ = io.WriteString(w, loginPage)
	})
	mux.HandleFunc(pubKey, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pubPEM)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newStepClient 把 cas2 的两个 resty 客户端都钉在本地测试服务器上（不触网）。
func newStepClient(t *testing.T, srv *httptest.Server) (*Client, *stepRecorder) {
	t.Helper()
	c := NewCas("20230001", "secret", "Edg/142.0.0.0", false, config.Settings{}, nil)
	c.http.SetBaseURL(srv.URL)
	c.portalHttp.SetBaseURL(srv.URL)
	rec := &stepRecorder{}
	c.SetStepHandler(rec.handle)
	return c, rec
}

// 登录页样本：extractLoginParams 只认 execution 与 failN 两个 input。
func loginPageWith(failN string) string {
	return `<form><input name="execution" value="e1"/><input name="failN" value="` + failN + `"/></form>`
}

// testTicketJWT 造一张足够 ParseTicket 通过的票（不验签）。
func testTicketJWT(t *testing.T, sub string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"sub":              sub,
		"organizationCode": "00300",
		"exp":              time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal ticket: %v", err)
	}
	return "e30." + base64.RawStdEncoding.EncodeToString(payload) + ".sig"
}

// 密码错误（401）：每一轮都要在时间线上留下"取登录页 → 加密密码 → 提交 → 密码错误"。
// 以前这四次交互在 WebUI 里只有一条"正在登录统一身份认证"。
func TestLoginReportsPasswordErrorSteps(t *testing.T) {
	srv := stepTestServer(t, loginPageWith("-1"), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c, rec := newStepClient(t, srv)

	if c.Login(context.Background()) {
		t.Fatal("401 不应登录成功")
	}

	want := []string{
		"fetch-form@1", "public-key@1", "submit@1", "password-error@0",
		"fetch-form@2", "public-key@2", "submit@2", "password-error@0",
		"fetch-form@3", "public-key@3", "submit@3", "password-error@0",
	}
	if got := rec.snapshot(); !equalSteps(got, want) {
		t.Fatalf("步骤序列 = %v\nwant %v", got, want)
	}
}

// 账户被锁：200 + 锁定提示。要能区分"被锁"和"密码错"，而不是都落到登录失败。
func TestLoginReportsAccountLockedStep(t *testing.T) {
	srv := stepTestServer(t, loginPageWith("-1"), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<div>这个账户已经被锁住了。</div>")
	})
	c, rec := newStepClient(t, srv)

	if c.Login(context.Background()) {
		t.Fatal("账户被锁不应登录成功")
	}

	want := []string{
		"fetch-form@1", "public-key@1", "submit@1", "account-locked@0",
		"fetch-form@2", "public-key@2", "submit@2", "account-locked@0",
		"fetch-form@3", "public-key@3", "submit@3", "account-locked@0",
	}
	if got := rec.snapshot(); !equalSteps(got, want) {
		t.Fatalf("步骤序列 = %v\nwant %v", got, want)
	}
}

// 成功路径：302 带 ticket，步骤止于 submit，不能多报失败；票据要落进会话。
func TestLoginReportsSuccessSteps(t *testing.T) {
	jwt := testTicketJWT(t, "20230001")
	srv := stepTestServer(t, loginPageWith("-1"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://portal.ycit.edu.cn/main.html?ticket="+jwt)
		w.WriteHeader(http.StatusFound)
	})
	c, rec := newStepClient(t, srv)

	if !c.Login(context.Background()) {
		t.Fatal("302 + 合法 ticket 应登录成功")
	}

	want := []string{"fetch-form@1", "public-key@1", "submit@1"}
	if got := rec.snapshot(); !equalSteps(got, want) {
		t.Fatalf("步骤序列 = %v\nwant %v", got, want)
	}
	if c.sess.TicketJWT != jwt {
		t.Errorf("会话票据 = %q, want %q", c.sess.TicketJWT, jwt)
	}
	if c.Account != "20230001" {
		t.Errorf("账号 = %q, want 20230001", c.Account)
	}
}

// 登录页带着历史失败次数时单独上报一次：这能解释"为什么又被要求验证码"。
// 生产代码在这里固定 sleep 2s（等 CAS 的验证码状态稳定），测试照实跑。
func TestLoginReportsFailedAttemptsStep(t *testing.T) {
	jwt := testTicketJWT(t, "20230001")
	srv := stepTestServer(t, loginPageWith("3"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://portal.ycit.edu.cn/main.html?ticket="+jwt)
		w.WriteHeader(http.StatusFound)
	})
	c, rec := newStepClient(t, srv)

	if !c.Login(context.Background()) {
		t.Fatal("302 + 合法 ticket 应登录成功")
	}

	want := []string{"fetch-form@1", "failed-attempts@0", "public-key@1", "submit@1"}
	if got := rec.snapshot(); !equalSteps(got, want) {
		t.Fatalf("步骤序列 = %v\nwant %v", got, want)
	}
}

// 没挂回调时上报必须是空操作（tui / 测试不会因此 panic）。
func TestEmitStepWithoutHandler(t *testing.T) {
	c := NewCas("u", "p", "Edg/142.0.0.0", false, config.Settings{}, nil)
	c.emitStep(StepSubmit, 1)
	var nilClient *Client
	nilClient.SetStepHandler(func(Step, int) {})
	nilClient.emitStep(StepSubmit, 1)
}

func equalSteps(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
