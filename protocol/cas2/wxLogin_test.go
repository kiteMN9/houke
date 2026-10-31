package cas2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"school_sdk/protocol/config"

	"resty.dev/v3"
)

func TestParseQRPage(t *testing.T) {
	body := `<html><script>var qr = "/connect/qrcode/TESTUUID";</script>
<img class="web_qrcode_img" src="https://open.weixin.qq.com/connect/qrcode/TESTUUID"></html>`

	uuid, img, ok := parseQRPage(body)
	if !ok {
		t.Fatal("parseQRPage 没解析出二维码")
	}
	if uuid != "TESTUUID" {
		t.Fatalf("uuid = %q, want TESTUUID", uuid)
	}
	if img != "https://open.weixin.qq.com/connect/qrcode/TESTUUID" {
		t.Fatalf("img = %q", img)
	}

	for _, bad := range []string{"", "<html>没有二维码</html>", `"/connect/qrcode/x"`} {
		if _, _, ok := parseQRPage(bad); ok {
			t.Fatalf("缺少 uuid 或图片时不应成功: %q", bad)
		}
	}
}

func TestResolveQRImageURL(t *testing.T) {
	cases := []struct{ base, raw, want string }{
		{"https://open.weixin.qq.com", "/connect/qrcode/u", "https://open.weixin.qq.com/connect/qrcode/u"},
		{"https://open.weixin.qq.com", "//cdn.example.com/q.png", "https://cdn.example.com/q.png"},
		{"https://open.weixin.qq.com/a/b", "q.png", "https://open.weixin.qq.com/a/q.png"},
		{"https://open.weixin.qq.com", "https://x.example/q.png", "https://x.example/q.png"},
	}
	for _, tc := range cases {
		got, err := resolveQRImageURL(tc.base, tc.raw)
		if err != nil {
			t.Fatalf("resolveQRImageURL(%q, %q): %v", tc.base, tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("resolveQRImageURL(%q, %q) = %q, want %q", tc.base, tc.raw, got, tc.want)
		}
	}
}

func TestParseScanResult(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		code   string
		status QRStatus
	}{
		{"未扫描", "window.wx_errcode = 408; window.wx_code = '';", "", QRWaiting},
		{"已扫描待确认", "window.wx_errcode = 404; window.wx_code = '';", "", QRScanned},
		{"已确认", "window.wx_errcode = 405; window.wx_code = 'CODE';", "CODE", QRScanned},
		{"过期", "window.wx_errcode = 402;", "", QRExpired},
		{"拒绝", "window.wx_errcode = 403;", "", QRRefused},
		{"只有 code", "window.wx_errcode = 408; window.wx_code = 'CODE';", "CODE", QRScanned},
		{"空 body", "", "", QRWaiting},
	}
	for _, tc := range cases {
		code, status := parseScanResult(tc.body)
		if code != tc.code || status != tc.status {
			t.Errorf("%s: parseScanResult = (%q, %q), want (%q, %q)", tc.name, code, status, tc.code, tc.status)
		}
	}
}

func TestWaitScanCanceled(t *testing.T) {
	c := &Client{http: resty.New()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := c.waitScan(ctx, "uuid", 1, time.Now().Add(time.Minute))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWaitScanExpiredAndRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want QRStatus
	}{
		{"过期", "window.wx_errcode = 402;", QRExpired},
		{"拒绝", "window.wx_errcode = 403;", QRRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := &Client{http: resty.New(), wx: wxEndpoints{qrPoll: srv.URL}}
			code, status, err := c.waitScan(context.Background(), "uuid", 1, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatalf("waitScan: %v", err)
			}
			if code != "" || status != tc.want {
				t.Fatalf("waitScan = (%q, %q), want (\"\", %q)", code, status, tc.want)
			}
		})
	}
}

// TestWXLoginFullFlow 用 httptest 串起整条链路：取二维码 → 过期刷新 →
// 扫码 → 换票，验证 WXLogin 既不依赖真实微信/CAS，也能把状态交给回调。
func TestWXLoginFullFlow(t *testing.T) {
	var polls atomic.Int32
	var ticket string

	mux := http.NewServeMux()
	mux.HandleFunc("/federatedRedirect", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("federatedName"); got != "openweixin" {
			t.Errorf("federatedName = %q", got)
		}
		w.Header().Set("Location", srvURL(r)+"/federated/openweixin?state=TST-1")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/federated/openweixin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srvURL(r)+"/connect/qrconnect?appid=x&state=TST-1#wechat_redirect")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/connect/qrconnect", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><script>var qr = "/connect/qrcode/TESTUUID";</script>
<img class="web_qrcode_img" src="/connect/qrcode/TESTUUID"></html>`))
	})
	mux.HandleFunc("/connect/qrcode/TESTUUID", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("uuid"); got != "TESTUUID" {
			t.Errorf("uuid = %q", got)
		}
		switch polls.Add(1) {
		case 1:
			_, _ = w.Write([]byte("window.wx_errcode = 402;"))
		default:
			_, _ = w.Write([]byte("window.wx_errcode = 405; window.wx_code = 'WXCODE';"))
		}
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("code"); got != "WXCODE" {
			t.Errorf("code = %q", got)
		}
		if got := r.URL.Query().Get("state"); got != "TST-1" {
			t.Errorf("state = %q", got)
		}
		w.Header().Set("Location", srvURL(r)+"/federatedCallback?federatedCode=SI0aYr&state=TST-1")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/federatedCallback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srvURL(r)+"/portal/session")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/portal/session", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/cas/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srvURL(r)+"/sso?ticket="+ticket)
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/sso", func(w http.ResponseWriter, r *http.Request) {})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	ticket = testTicket(t)

	c := &Client{
		http: resty.New().SetRedirectPolicy(resty.RedirectNoPolicy()),
		wx: wxEndpoints{
			federatedRedirect: srv.URL + "/federatedRedirect",
			service:           "https://portal.example.edu/?path=https://portal.example.edu/main.html#/",
			qrImageBase:       srv.URL,
			qrPoll:            srv.URL + "/poll",
			callback:          srv.URL + "/callback",
			casLogin:          srv.URL + "/cas/login",
		},
		sess: config.NewSession(),
	}

	var mu sync.Mutex
	var qrs []*QRCode
	var statuses []QRStatus
	c.SetQRHandler(func(qr *QRCode) {
		mu.Lock()
		qrs = append(qrs, qr)
		mu.Unlock()
	})
	c.SetQRStatusHandler(func(status QRStatus, attempt int) {
		mu.Lock()
		statuses = append(statuses, status)
		mu.Unlock()
	})

	if !c.WXLogin(context.Background()) {
		t.Fatal("WXLogin 应成功")
	}
	if c.Account != "20230001" {
		t.Fatalf("Account = %q", c.Account)
	}
	if c.sess.TicketJWT != ticket {
		t.Fatalf("TicketJWT 未写入会话")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(qrs) != 4 { // 第一张、过期清空、第二张、结束清空
		t.Fatalf("二维码回调 %d 次，want 4: %+v", len(qrs), qrs)
	}
	if qrs[0] == nil || string(qrs[0].Image) != "PNGDATA" || qrs[0].Attempt != 1 {
		t.Fatalf("第一张二维码 = %+v", qrs[0])
	}
	wantPageURL := srv.URL + "/connect/qrconnect?appid=x&state=TST-1#wechat_redirect"
	if qrs[0].PageURL != wantPageURL {
		t.Fatalf("第一张二维码扫码页 = %q, want %q", qrs[0].PageURL, wantPageURL)
	}
	if qrs[1] != nil || qrs[3] != nil {
		t.Fatalf("二维码失效时应回调 nil: %+v", qrs)
	}
	if qrs[2] == nil || qrs[2].Attempt != 2 {
		t.Fatalf("刷新后的二维码 = %+v", qrs[2])
	}
	wantStatuses := []QRStatus{QRWaiting, QRExpired, QRWaiting, QRScanned}
	if len(statuses) != len(wantStatuses) {
		t.Fatalf("状态 = %v, want %v", statuses, wantStatuses)
	}
	for i := range wantStatuses {
		if statuses[i] != wantStatuses[i] {
			t.Fatalf("状态[%d] = %q, want %q（全部 %v）", i, statuses[i], wantStatuses[i], statuses)
		}
	}
}

func TestWXLoginCanceledDoesNotRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	c := &Client{http: resty.New(), wx: wxEndpoints{federatedRedirect: srv.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if c.WXLogin(ctx) {
		t.Fatal("已取消的 ctx 不应登录成功")
	}
	if hits.Load() != 0 {
		t.Fatalf("已取消时不应发请求，实际 %d 次", hits.Load())
	}
}

// srvURL 从请求里还原 httptest 的基地址；测试只关心路径拼接。
func srvURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// testTicket 造一个 ExtractIDToken 能解析的 JWT（不校验签名）。
func testTicket(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"sub":     "20230001",
		"idToken": "id-token",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "e30." + base64.RawStdEncoding.EncodeToString(payload) + ".sig"
}
