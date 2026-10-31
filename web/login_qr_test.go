package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"school_sdk/protocol"
)

// 二维码运行态：rev 递增、旧 rev 不可取、清空后不可取。
func TestQRCodeRuntimeState(t *testing.T) {
	rt := &accountRuntime{}
	if rt.qrView() != nil {
		t.Fatal("空运行态不应有二维码")
	}
	if _, ok := rt.qrImage(0); ok {
		t.Fatal("空运行态不应能取到图片")
	}

	img1 := []byte{0x89, 0x50, 0x4e, 0x47, 0x01}
	exp1 := time.Now().Add(2 * time.Minute)
	wantPageURL := "https://open.weixin.qq.com/connect/qrconnect?appid=wx&state=s#wechat_redirect"
	rt.setQRCode(&protocol.QRCode{Image: img1, ExpiresAt: exp1, PageURL: wantPageURL})
	v1 := rt.qrView()
	if v1 == nil {
		t.Fatal("设置后应有二维码")
	}
	if v1.Rev != 1 {
		t.Fatalf("首张 rev = %d, want 1", v1.Rev)
	}
	if v1.ExpiresAt != exp1.UnixMilli() {
		t.Fatalf("expiresAt = %d, want %d", v1.ExpiresAt, exp1.UnixMilli())
	}
	if v1.PageURL != wantPageURL {
		t.Fatalf("pageUrl = %q, want %q", v1.PageURL, wantPageURL)
	}
	got, ok := rt.qrImage(v1.Rev)
	if !ok || !bytes.Equal(got, img1) {
		t.Fatalf("qrImage = %v, %v, want %v, true", got, ok, img1)
	}
	// 返回副本：调用方改字节不能污染运行态
	got[0] = 0
	if again, _ := rt.qrImage(v1.Rev); !bytes.Equal(again, img1) {
		t.Fatal("qrImage 返回的不是副本")
	}

	img2 := []byte{0x89, 0x50, 0x4e, 0x47, 0x02}
	rt.setQRCode(&protocol.QRCode{Image: img2, ExpiresAt: exp1})
	v2 := rt.qrView()
	if v2.Rev != 2 {
		t.Fatalf("刷新后 rev = %d, want 2", v2.Rev)
	}
	if _, ok := rt.qrImage(v1.Rev); ok {
		t.Fatal("旧 rev 不应还能取到图片")
	}

	// 空图片与 nil 都表示二维码失效
	rt.setQRCode(&protocol.QRCode{Image: nil})
	if rt.qrView() != nil {
		t.Fatal("空图片应清空二维码")
	}
	rt.setQRCode(&protocol.QRCode{Image: img2})
	rt.clearQRCode()
	if rt.qrView() != nil {
		t.Fatal("clearQRCode 后不应有二维码")
	}
}

// 二维码图片端点：当前 rev 返回 PNG + no-store，旧 rev / 无二维码 404。
func TestLoginQRImageHTTP(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{Name: "n", Username: "u", BaseURL: "https://jwglxt.ycit.edu.cn/"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	base := "/api/accounts/" + acc.ID + "/login/qr"

	// 没有二维码：404，不能是空 200（前端会显示破图）
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无二维码 status = %d, want 404", rec.Code)
	}

	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	rt.setQRCode(&protocol.QRCode{Image: png, ExpiresAt: time.Now().Add(time.Minute)})
	rev := rt.qrView().Rev

	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base+"?rev="+strconv.FormatInt(rev, 10), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Errorf("图片字节不一致: %v", rec.Body.Bytes())
	}

	// 不传 rev 也取当前二维码
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("不带 rev status = %d, want 200", rec.Code)
	}

	// 旧 rev：404，避免慢响应覆盖新二维码
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base+"?rev="+strconv.FormatInt(rev+1, 10), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("旧 rev status = %d, want 404", rec.Code)
	}

	// 非法 rev：400
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base+"?rev=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 rev status = %d, want 400", rec.Code)
	}
}

// /status 带二维码元数据，但不含图片字节。
func TestStatusIncludesQRMetadata(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{Name: "n", Username: "u", BaseURL: "https://jwglxt.ycit.edu.cn/"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	exp := time.Now().Add(time.Minute)
	pageURL := "https://open.weixin.qq.com/connect/qrconnect?appid=wx&state=s#wechat_redirect"
	rt.setQRCode(&protocol.QRCode{Image: []byte{0x89, 0x50}, ExpiresAt: exp, PageURL: pageURL})

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		QR *loginQRView `json:"qr"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if body.QR == nil {
		t.Fatal("/status 没带 qr")
	}
	if body.QR.Rev != 1 || body.QR.ExpiresAt != exp.UnixMilli() {
		t.Fatalf("qr = %+v", body.QR)
	}
	if body.QR.PageURL != pageURL {
		t.Fatalf("qr.pageUrl = %q, want %q", body.QR.PageURL, pageURL)
	}
}

// 登出与取消登录都必须立刻清掉二维码，避免界面上留着一张已失效的图。
func TestLogoutAndCancelClearQR(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{Name: "n", Username: "u", BaseURL: "https://jwglxt.ycit.edu.cn/"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"logout", "/api/accounts/" + acc.ID + "/logout"},
		{"cancel", "/api/accounts/" + acc.ID + "/login/cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := store.runtime(acc.ID)
			if err != nil {
				t.Fatalf("runtime: %v", err)
			}
			rt.setQRCode(&protocol.QRCode{Image: []byte{0x89}, ExpiresAt: time.Now().Add(time.Minute)})
			rec := httptest.NewRecorder()
			srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if rt.qrView() != nil {
				t.Fatal("请求后二维码仍存在")
			}
		})
	}
}

// SSE 首帧带二维码元数据，进度帧不重复携带。
func TestStreamSnapshotCarriesQR(t *testing.T) {
	store, id, base := newStreamTestServer(t)
	rt, err := store.runtime(id)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	pageURL := "https://open.weixin.qq.com/connect/qrconnect?appid=wx&state=s#wechat_redirect"
	rt.setQRCode(&protocol.QRCode{Image: []byte{0x89}, ExpiresAt: time.Now().Add(time.Minute), PageURL: pageURL})

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+id+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)
	first := log.all()[0]
	if first.Type != "snapshot" {
		t.Errorf("首帧 type = %q, want snapshot", first.Type)
	}
	if first.QR == nil {
		t.Fatal("首帧没带 qr：刷新页面后二维码弹窗无法恢复")
	}
	if first.QR.Rev != 1 {
		t.Errorf("qr.rev = %d, want 1", first.QR.Rev)
	}
	if first.QR.PageURL != pageURL {
		t.Errorf("qr.pageUrl = %q, want %q", first.QR.PageURL, pageURL)
	}
}
