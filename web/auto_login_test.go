package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"school_sdk/jwmock"
)

// startJWMock 启动本地模拟教务，返回服务实例和可直接填进 Account.BaseURL 的地址。
func startJWMock(t *testing.T) (*jwmock.Server, string) {
	t.Helper()
	mock, err := jwmock.New(jwmock.Options{})
	if err != nil {
		t.Fatalf("jwmock.New: %v", err)
	}
	ts := httptest.NewServer(mock)
	t.Cleanup(ts.Close)
	return mock, ts.URL + mock.BasePath() + "/"
}

// 开启自动重登时，未登录点「已选课程」应该先跑一次可见登录再查询，
// 不需要用户先点「登录」；登录过程必须出现在任务面板里（不是静默登录）。
func TestFeatureAutoLoginWhenAutoReloginOn(t *testing.T) {
	mock, base := startJWMock(t)

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: jwmock.DefaultUsername,
		Password: jwmock.DefaultPassword,
		BaseURL:  base,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/accounts/"+acc.ID+"/selected?xnm=2025&xqm=3", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := mock.Snapshot().Logins; got != 1 {
		t.Fatalf("logins = %d, want 1", got)
	}
	snap := srv.tasks.snapshot(acc.ID)
	if snap == nil || snap.Kind != taskKindLogin || snap.Running || snap.Status != taskStatusDone {
		t.Fatalf("登录任务快照 = %+v，期望一条已完成的登录任务", snap)
	}
}

// 同一账号只能有一个长任务：已有登录 / 初始化 / 自动选课在跑时，
// 需要登录的接口直接回 409，不把这条 HTTP 请求挂到另一个任务结束。
func TestFeatureAutoLoginBusy(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "http://127.0.0.1:1/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)
	if _, ok := srv.tasks.startCancelable(acc.ID, taskKindInit, func() {}); !ok {
		t.Fatal("登记模拟任务失败")
	}
	t.Cleanup(func() {
		srv.tasks.finish(acc.ID, taskKindInit, taskStatusDone, "done", "")
	})

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/accounts/"+acc.ID+"/tools/info", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "busy") {
		t.Fatalf("body = %s, want code busy", rec.Body.String())
	}
}

// 微信扫码不能在业务请求里静默开始：没有二维码弹窗就没人扫。
// 此时只回 401，并在文案里说明需要手动登录。
func TestFeatureAutoLoginSkipsWeChat(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:      "n",
		Username:  "u",
		BaseURL:   "http://127.0.0.1:1/",
		LoginMode: LoginModeWeChat,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/accounts/"+acc.ID+"/tools/info", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "微信扫码") {
		t.Fatalf("body = %s, want 含「微信扫码」", rec.Body.String())
	}
	if snap := srv.tasks.snapshot(acc.ID); snap != nil {
		t.Fatalf("微信账号不应起登录任务: %+v", snap)
	}
}
