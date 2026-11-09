package jwmock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"school_sdk/engine"
	"school_sdk/jwmock"
	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// startMock 启动模拟服务，返回服务实例和可直接写进 config.Settings 的 BaseURL。
func startMock(t *testing.T, opts jwmock.Options) (*jwmock.Server, string) {
	t.Helper()
	srv, err := jwmock.New(opts)
	if err != nil {
		t.Fatalf("jwmock.New: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts.URL + srv.BasePath() + "/"
}

// newEngine 用默认账号创建真实引擎；ask 为 nil 时不提供人工输入。
func newEngine(t *testing.T, baseURL string, ask func(context.Context, *protocol.InputRequest) (string, error)) *engine.Engine {
	t.Helper()
	cfg := config.Settings{
		BaseURL:   baseURL,
		Account:   jwmock.DefaultUsername,
		Password:  jwmock.DefaultPassword,
		UserAgent: "jwmock-test/1.0",
	}
	eng, err := engine.New(cfg, config.NewSession(), engine.LoginPassword, engine.Options{Ask: ask})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng
}

// mustLogin 登录并初始化；大多数业务测试都从这一步开始。
func mustLogin(t *testing.T, eng *engine.Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !eng.Login(ctx) {
		t.Fatal("登录失败")
	}
	if err := eng.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// collectEvents 返回一个把事件写进切片的 Sink 和取事件的函数。
func collectEvents() (engine.Sink, func() []engine.Event) {
	var mu sync.Mutex
	var events []engine.Event
	sink := func(ev engine.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	}
	return sink, func() []engine.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]engine.Event(nil), events...)
	}
}

// findEvent 判断是否出现过某类事件，且 Message 包含 substr。
func findEvent(events []engine.Event, kind, substr string) bool {
	for _, ev := range events {
		if ev.Kind == kind && strings.Contains(ev.Message, substr) {
			return true
		}
	}
	return false
}

// TestLoginAndAutoChoose 覆盖无验证码登录 → 初始化 → 列表/详情 → 自动选课成功。
func TestLoginAndAutoChoose(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{})
	eng := newEngine(t, baseURL, nil)
	mustLogin(t, eng)

	ctx, cancel := testContext(t)
	defer cancel()

	list, err := eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("GetCourseList: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("课程列表长度 = %d, want 3", len(list))
	}

	detail, err := eng.GetCourseDetail(ctx, "KCH-MATH")
	if err != nil {
		t.Fatalf("GetCourseDetail: %v", err)
	}
	if len(detail) != 2 {
		t.Fatalf("高等数学教学班数 = %d, want 2", len(detail))
	}

	sink, events := collectEvents()
	plan := engine.Plan{
		Wants:     protocol.WishList{WantClassList: []string{"高等数学"}},
		Interval:  20 * time.Millisecond,
		Refresh:   50 * time.Millisecond,
		MaxRounds: 3,
	}
	res, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), sink)
	if err != nil {
		t.Fatalf("RunAuto: %v (events=%v)", err, events())
	}
	if res.Flag != "1" {
		t.Fatalf("选课结果 flag = %q, msg = %q", res.Flag, res.Msg)
	}
	if !findEvent(events(), engine.EventSuccess, "") {
		t.Fatalf("没有 success 事件: %v", events())
	}

	snap := srv.Snapshot()
	if len(snap.Chosen) != 1 || snap.Chosen[0] != "JXB-MATH-01" {
		t.Fatalf("已选 = %v, want [JXB-MATH-01]", snap.Chosen)
	}

	selected, err := eng.Selected(ctx, "2025", "3")
	if err != nil {
		t.Fatalf("Selected: %v", err)
	}
	if len(selected) != 1 {
		t.Fatalf("已选课程数 = %d, want 1", len(selected))
	}

	ok, msg, err := eng.Quit(ctx, "JXB-MATH-01", "KCH-MATH")
	if err != nil || !ok {
		t.Fatalf("Quit = (%v, %q, %v), want ok", ok, msg, err)
	}
	selected, err = eng.Selected(ctx, "2025", "3")
	if err != nil {
		t.Fatalf("退课后 Selected: %v", err)
	}
	if len(selected) != 0 {
		t.Fatalf("退课后已选课程数 = %d, want 0", len(selected))
	}
}

// TestKaptchaLogin 覆盖字符验证码：先输错一次，再从 Ask 拿到正确答案。
func TestKaptchaLogin(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{LoginMode: jwmock.LoginKaptcha})

	var mu sync.Mutex
	asks := 0
	ask := func(_ context.Context, req *protocol.InputRequest) (string, error) {
		if req.Kind == "password" {
			return jwmock.DefaultPassword, nil
		}
		mu.Lock()
		defer mu.Unlock()
		asks++
		if asks == 1 {
			return "0000", nil
		}
		return jwmock.DefaultKaptchaCode, nil
	}

	eng := newEngine(t, baseURL, ask)
	ctx, cancel := testContext(t)
	defer cancel()
	if !eng.Login(ctx) {
		t.Fatal("验证码登录失败")
	}
	if err := eng.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	snap := srv.Snapshot()
	if snap.Logins != 1 {
		t.Fatalf("成功登录次数 = %d, want 1", snap.Logins)
	}
	if snap.LoginFailures < 1 {
		t.Fatalf("登录失败次数 = %d, want >= 1", snap.LoginFailures)
	}
}

// TestSliderLogin 覆盖滑块验证码：refresh 返回 not_verify，图片可解码，
// verify 后与真实教务一样返回 verified。
func TestSliderLogin(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{LoginMode: jwmock.LoginSlider})
	eng := newEngine(t, baseURL, nil)
	ctx, cancel := testContext(t)
	defer cancel()
	if !eng.Login(ctx) {
		t.Fatal("滑块登录失败")
	}
	if snap := srv.Snapshot(); snap.Logins != 1 {
		t.Fatalf("成功登录次数 = %d, want 1", snap.Logins)
	}
}

// TestRejectScenario 覆盖选课业务拒绝：flag=0 + 文案，自动选课以 ErrNotChosen 结束。
func TestRejectScenario(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{})
	eng := newEngine(t, baseURL, nil)
	mustLogin(t, eng)

	const reason = "当前不在选课时间内"
	if err := srv.SetScenario(jwmock.Scenario{RejectAll: reason}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	ctx, cancel := testContext(t)
	defer cancel()
	sink, events := collectEvents()
	plan := engine.Plan{
		Wants:    protocol.WishList{WantClassList: []string{"高等数学"}},
		Once:     true,
		Interval: 20 * time.Millisecond,
	}
	_, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), sink)
	if !errors.Is(err, engine.ErrNotChosen) {
		t.Fatalf("RunAuto err = %v, want ErrNotChosen", err)
	}
	if !findEvent(events(), engine.EventFailed, reason) {
		t.Fatalf("没有包含拒绝文案的 failed 事件: %v", events())
	}
	if snap := srv.Snapshot(); len(snap.Chosen) != 0 {
		t.Fatalf("被拒绝后不应有已选课程: %v", snap.Chosen)
	}
}

// TestSessionRevoked 覆盖被踢：关掉自动重登时报 ErrUnauthorized，打开后能自愈。
func TestSessionRevoked(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{})
	eng := newEngine(t, baseURL, nil)
	mustLogin(t, eng)

	eng.SetAutoRelogin(false)
	if n := srv.RevokeSessions(); n != 1 {
		t.Fatalf("RevokeSessions = %d, want 1", n)
	}

	ctx, cancel := testContext(t)
	defer cancel()
	if _, err := eng.GetCourseList(ctx); !errors.Is(err, protocol.ErrUnauthorized) {
		t.Fatalf("被踢后 GetCourseList err = %v, want ErrUnauthorized", err)
	}

	eng.SetAutoRelogin(true)
	list, err := eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("自动重登后 GetCourseList: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("自动重登后课程列表长度 = %d, want 3", len(list))
	}
	if snap := srv.Snapshot(); snap.Logins < 2 {
		t.Fatalf("自动重登后成功登录次数 = %d, want >= 2", snap.Logins)
	}
}

// TestSeatsReleasedInPhases 覆盖“名额分阶段放出”：先满员失败，后放名额选上。
func TestSeatsReleasedInPhases(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{})
	eng := newEngine(t, baseURL, nil)
	mustLogin(t, eng)

	// 一开始两个数学班都满员；500ms 后 01 班放出 2 个名额。
	if err := srv.SetScenario(jwmock.Scenario{
		EnrolledPhases: []jwmock.EnrolledPhase{
			{AfterMs: 0, Enrolled: map[string]int{"JXB-MATH-01": 2}},
			{AfterMs: 500, Enrolled: map[string]int{"JXB-MATH-01": 0}},
		},
	}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	ctx, cancel := testContext(t)
	defer cancel()
	sink, events := collectEvents()
	plan := engine.Plan{
		Wants:     protocol.WishList{WantClassList: []string{"高等数学"}},
		Interval:  40 * time.Millisecond,
		Refresh:   40 * time.Millisecond,
		MaxRounds: 20,
	}
	res, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), sink)
	if err != nil {
		t.Fatalf("RunAuto: %v (events=%v)", err, events())
	}
	if res.Flag != "1" {
		t.Fatalf("最终 flag = %q, msg = %q", res.Flag, res.Msg)
	}
	if !findEvent(events(), engine.EventFailed, "人数已满") {
		t.Fatalf("缺少满员失败事件: %v", events())
	}
	if !findEvent(events(), engine.EventSuccess, "") {
		t.Fatalf("缺少成功事件: %v", events())
	}
}

// TestHTTPFaults 覆盖 5xx（客户端自动重试）与 429（不重试，选课层再试）。
func TestHTTPFaults(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		srv, baseURL := startMock(t, jwmock.Options{})
		eng := newEngine(t, baseURL, nil)
		mustLogin(t, eng)

		if err := srv.SetScenario(jwmock.Scenario{Faults: []jwmock.Fault{{
			PathContains: "zzxkyzbjk_xkBcZy",
			Status:       500,
			Count:        2,
		}}}); err != nil {
			t.Fatalf("SetScenario: %v", err)
		}

		ctx, cancel := testContext(t)
		defer cancel()
		plan := engine.Plan{
			Wants:     protocol.WishList{WantClassList: []string{"西方美术鉴赏"}},
			Interval:  20 * time.Millisecond,
			MaxRounds: 2,
		}
		res, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), nil)
		if err != nil {
			t.Fatalf("RunAuto: %v", err)
		}
		if res.Flag != "1" {
			t.Fatalf("5xx 重试后 flag = %q, msg = %q", res.Flag, res.Msg)
		}
	})

	t.Run("429", func(t *testing.T) {
		srv, baseURL := startMock(t, jwmock.Options{})
		eng := newEngine(t, baseURL, nil)
		mustLogin(t, eng)

		if err := srv.SetScenario(jwmock.Scenario{Faults: []jwmock.Fault{{
			PathContains: "zzxkyzbjk_xkBcZy",
			Status:       429,
			Count:        1,
		}}}); err != nil {
			t.Fatalf("SetScenario: %v", err)
		}

		ctx, cancel := testContext(t)
		defer cancel()
		plan := engine.Plan{
			Wants:     protocol.WishList{WantClassList: []string{"西方美术鉴赏"}},
			Interval:  20 * time.Millisecond,
			MaxRounds: 2,
		}
		res, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), nil)
		if err != nil {
			t.Fatalf("RunAuto: %v", err)
		}
		if res.Flag != "1" {
			t.Fatalf("429 后重试 flag = %q, msg = %q", res.Flag, res.Msg)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		if testing.Short() {
			t.Skip("需要等客户端 4 秒超时")
		}
		srv, baseURL := startMock(t, jwmock.Options{})
		eng := newEngine(t, baseURL, nil)
		mustLogin(t, eng)

		if err := srv.SetScenario(jwmock.Scenario{Faults: []jwmock.Fault{{
			PathContains: "zzxkyzbjk_xkBcZy",
			DelayMs:      4500,
			Count:        1,
		}}}); err != nil {
			t.Fatalf("SetScenario: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		plan := engine.Plan{
			Wants:     protocol.WishList{WantClassList: []string{"西方美术鉴赏"}},
			Interval:  20 * time.Millisecond,
			MaxRounds: 2,
		}
		res, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), nil)
		if err != nil {
			t.Fatalf("超时后 RunAuto: %v", err)
		}
		if res.Flag != "1" {
			t.Fatalf("超时后重试 flag = %q, msg = %q", res.Flag, res.Msg)
		}
	})
}

// TestControlPlane 覆盖 /__mock/ 控制面的最小闭环：读状态、换场景、重置。
func TestControlPlane(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{})
	ctrl := strings.TrimSuffix(baseURL, srv.BasePath()+"/") + "/__mock"

	getState := func() jwmock.State {
		t.Helper()
		resp, err := http.Get(ctrl + "/state")
		if err != nil {
			t.Fatalf("GET /__mock/state: %v", err)
		}
		defer resp.Body.Close()
		var st jwmock.State
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatalf("解析 state: %v", err)
		}
		return st
	}

	if st := getState(); st.Logins != 0 {
		t.Fatalf("初始 logins = %d, want 0", st.Logins)
	}

	resp, err := http.Post(ctrl+"/scenario", "application/json",
		strings.NewReader(`{"rejectAll":"控制面拒绝"}`))
	if err != nil {
		t.Fatalf("POST /__mock/scenario: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST scenario 状态码 = %d", resp.StatusCode)
	}
	if st := getState(); st.Scenario.RejectAll != "控制面拒绝" {
		t.Fatalf("scenario.RejectAll = %q", st.Scenario.RejectAll)
	}

	resp, err = http.Post(ctrl+"/reset", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /__mock/reset: %v", err)
	}
	resp.Body.Close()
	if st := getState(); st.Scenario.RejectAll != "" {
		t.Fatalf("reset 后 scenario.RejectAll = %q", st.Scenario.RejectAll)
	}
}

// TestParamExpireRecovers 覆盖参数过期：列表返回 msg/flag，引擎自动 Renew 后重试成功。
func TestParamExpireRecovers(t *testing.T) {
	srv, baseURL := startMock(t, jwmock.Options{})
	eng := newEngine(t, baseURL, nil)
	mustLogin(t, eng)

	if err := srv.SetScenario(jwmock.Scenario{ParamExpire: 1}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	ctx, cancel := testContext(t)
	defer cancel()
	list, err := eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("参数过期后 GetCourseList: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("参数过期恢复后课程列表长度 = %d, want 3", len(list))
	}
}
