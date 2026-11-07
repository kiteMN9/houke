package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// newAutoRuntime 建一个账号并返回运行态；loggedIn 控制是否伪造登录态。
func newAutoRuntime(t *testing.T, loggedIn bool) (*AccountStore, *accountRuntime) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"flag":"0","msg":"ok"}`))
	}))
	t.Cleanup(upstream.Close)

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  upstream.URL + "/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(loggedIn)
	return store, rt
}

// 未登录时不应该开始自动选课：后面每一步都依赖会话。
func TestAutoChooseRequiresLogin(t *testing.T) {
	store, _ := newAutoRuntime(t, false)
	srv := mustNewServer(t, store)
	acc := store.order[0]

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc+"/auto-choose",
		strings.NewReader(`{"wantClasses":["高等数学"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}

// 已登录但没初始化时，返回"先 /init"，而不是让 CollectCandidates 去撞空参数。
func TestAutoChooseRequiresInit(t *testing.T) {
	store, _ := newAutoRuntime(t, true)
	srv := mustNewServer(t, store)
	acc := store.order[0]

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc+"/auto-choose",
		strings.NewReader(`{"wantClasses":["高等数学"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "请先调用 /init") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// 没有任务在跑时，取消接口是幂等的 noop。
func TestCancelAutoChooseNoop(t *testing.T) {
	store, _ := newAutoRuntime(t, true)
	srv := mustNewServer(t, store)
	acc := store.order[0]

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc+"/auto-choose/cancel", nil)
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "noop") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// 自动选课是超长任务：运行预算由请求的时长/结束时间决定，默认 24 小时，
// 不再借用登录/初始化的 10 分钟默认值。
func TestAutoTimeout(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name    string
		req     autoChooseReq
		want    time.Duration
		wantErr string
	}{
		{"default", autoChooseReq{}, defaultAutoTimeout, ""},
		{"duration", autoChooseReq{DurationSeconds: 7200}, 2 * time.Hour, ""},
		{"fractional duration", autoChooseReq{DurationSeconds: 1.5}, 1500 * time.Millisecond, ""},
		{"endAt", autoChooseReq{EndAt: now.Add(3 * time.Hour).UnixMilli()}, 3 * time.Hour, ""},
		{"endAt wins", autoChooseReq{DurationSeconds: 60, EndAt: now.Add(time.Hour).UnixMilli()}, time.Hour, ""},
		{"negative duration", autoChooseReq{DurationSeconds: -1}, 0, "运行时长不能为负数"},
		{"negative endAt", autoChooseReq{EndAt: -1}, 0, "结束时间不能为负数"},
		{"past endAt", autoChooseReq{EndAt: now.Add(-time.Minute).UnixMilli()}, 0, "结束时间必须晚于当前时间"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := autoTimeout(tc.req, now)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v，期望包含 %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("autoTimeout: %v", err)
			}
			if got != tc.want {
				t.Fatalf("timeout = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// 长预算的文案要按天/小时展开，不能出现「1503 分钟」这种数字。
func TestAutoTimeoutText(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30 秒"},
		{10 * time.Minute, "10 分钟"},
		{2*time.Hour + 30*time.Minute, "2 小时 30 分钟"},
		{24 * time.Hour, "1 天"},
		{25*time.Hour + 3*time.Minute, "1 天 1 小时 3 分钟"},
		{48 * time.Hour, "2 天"},
	}
	for _, tc := range cases {
		if got := autoTimeoutText(tc.in); got != tc.want {
			t.Errorf("autoTimeoutText(%v) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// 运行预算不合法时应该在登记任务之前就返回 400，而不是先报"未登录"。
func TestAutoChooseRejectsInvalidLimit(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"past endAt", `{"wantClasses":["高等数学"],"endAt":1}`, "结束时间必须晚于当前时间"},
		{"negative duration", `{"wantClasses":["高等数学"],"durationSeconds":-1}`, "运行时长不能为负数"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newAutoRuntime(t, false)
			srv := mustNewServer(t, store)
			acc := store.order[0]

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc+"/auto-choose",
				strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			srv.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body = %s，期望包含 %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// autoError 决定前端看到的是"业务失败"还是"请求失败"。
func TestAutoErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		code int
		body string
	}{
		{"not_chosen", context.Background(), engine.ErrNotChosen, http.StatusOK, "not_chosen"},
		{"session_changed", context.Background(), engine.ErrSessionChanged, http.StatusConflict, "会话已变更"},
		{"session_reset", context.Background(), engine.ErrSessionReset, http.StatusConflict, "会话已重置"},
		{"no_wanted", context.Background(), engine.ErrNoWantedCourse, http.StatusBadRequest, "愿望清单"},
		{"not_initialized", context.Background(), engine.ErrNotInitialized, http.StatusBadRequest, "请先调用 /init"},
		{"unauthorized", context.Background(), fmt.Errorf("course: %w", protocol.ErrUnauthorized), http.StatusUnauthorized, "登录已失效"},
		{"canceled", canceledContext(), context.Canceled, http.StatusOK, "cancelled"},
		{"timeout", deadlineContext(), context.DeadlineExceeded, http.StatusGatewayTimeout, "自动选课超时"},
		{"internal", context.Background(), errors.New("boom"), http.StatusInternalServerError, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			s := &Server{}
			s.autoError(c, tc.ctx, tc.err)

			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("body = %s，期望包含 %q", rec.Body.String(), tc.body)
			}
		})
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func deadlineContext() context.Context {
	// 已经过期的 deadline：ctx.Err() 立即是 DeadlineExceeded。
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}
