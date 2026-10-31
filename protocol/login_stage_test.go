package protocol

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"school_sdk/protocol/cas2"
)

// 阶段变化会通知观察者，重复上报同一阶段不会重复通知。
func TestLoginStageReportsChangesOnce(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)

	var mu sync.Mutex
	var got []LoginStage
	a.SetLoginObserver(func(st LoginStage) {
		mu.Lock()
		got = append(got, st)
		mu.Unlock()
	})

	a.reportLoginStage(LoginPhaseFetchForm, 1, "")
	a.reportLoginStage(LoginPhaseFetchForm, 1, "") // 同一阶段重复上报：忽略
	a.reportLoginStage(LoginPhaseSubmit, 1, "")

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("observer 收到 %d 次通知，want 2: %+v", len(got), got)
	}
	if got[0].Phase != LoginPhaseFetchForm || got[0].Attempt != 1 {
		t.Fatalf("第一次通知 = %+v", got[0])
	}
	if got[1].Phase != LoginPhaseSubmit {
		t.Fatalf("第二次通知 = %+v", got[1])
	}
	if st := a.LoginStage(); st.Phase != LoginPhaseSubmit {
		t.Fatalf("LoginStage = %+v, want %s", st, LoginPhaseSubmit)
	}
	if st := a.LoginStage(); st.At.IsZero() {
		t.Fatal("阶段快照缺少时间戳")
	}
}

// 新一轮登录开始前会清掉上一次的阶段残留。
func TestLoginStageReset(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	a.reportLoginStage(LoginPhaseSubmit, 2, "")
	a.resetLoginStage()
	if st := a.LoginStage(); st.Phase != "" {
		t.Fatalf("reset 后 LoginStage = %+v, want 空", st)
	}
}

// nil client 上调用阶段 API 不应 panic（engine 装配时可能先构造再注入）。
func TestLoginStageNilSafety(t *testing.T) {
	var a *APIClient
	a.SetLoginObserver(func(LoginStage) {})
	if st := a.LoginStage(); st.Phase != "" {
		t.Fatalf("nil client LoginStage = %+v", st)
	}
	a.reportLoginStage(LoginPhaseSubmit, 1, "")
	a.resetLoginStage()
}

// 阶段记账要能被并发读写（登录 goroutine 写、SSE / status 读）。
func TestLoginStageConcurrent(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	var notified atomic.Int64
	a.SetLoginObserver(func(LoginStage) { notified.Add(1) })

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := range 100 {
				a.reportLoginStage(LoginPhaseFetchForm, i*100+j+1, "")
			}
		}(i)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = a.LoginStage()
			}
		}()
	}
	wg.Wait()

	if notified.Load() == 0 {
		t.Fatal("并发上报期间 observer 一次都没收到")
	}
}

// 扫码状态要能映射成统一的登录阶段；未知状态不产生阶段。
func TestQRStatusPhase(t *testing.T) {
	cases := []struct {
		status cas2.QRStatus
		want   LoginPhase
	}{
		{cas2.QRWaiting, LoginPhaseQRCode},
		{cas2.QRScanned, LoginPhaseQRScanned},
		{cas2.QRExpired, LoginPhaseQRExpired},
		{cas2.QRRefused, LoginPhaseQRRefused},
		{"unknown", ""},
	}
	for _, tc := range cases {
		if got := qrStatusPhase(tc.status); got != tc.want {
			t.Errorf("qrStatusPhase(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// cas2 密码登录的内部步骤要映射成统一阶段：取登录页 / 加密 / 提交复用直连
// 密码登录的阶段，失败原因各有独立阶段（否则 WebUI 只能显示一句"登录失败"）。
func TestCas2StepPhase(t *testing.T) {
	cases := []struct {
		step cas2.Step
		want LoginPhase
	}{
		{cas2.StepSessionValid, LoginPhaseSessionValid},
		{cas2.StepSessionExpired, LoginPhaseSessionExpired},
		{cas2.StepFetchForm, LoginPhaseFetchForm},
		{cas2.StepFailedAttempts, LoginPhaseFailedAttempts},
		{cas2.StepPublicKey, LoginPhasePublicKey},
		{cas2.StepSubmit, LoginPhaseSubmit},
		{cas2.StepPasswordError, LoginPhasePasswordError},
		{cas2.StepAccountLocked, LoginPhaseAccountLocked},
		{cas2.StepLoginFailed, LoginPhasePortalFailed},
		{"unknown", ""},
	}
	for _, tc := range cases {
		if got := cas2StepPhase(tc.step); got != tc.want {
			t.Errorf("cas2StepPhase(%q) = %q, want %q", tc.step, got, tc.want)
		}
	}
}

// 登录一开始就取消：只应留下 prepare 阶段，且能通过观察者看到。
func TestLoginStagePrepareOnCancelledLogin(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	var seen []LoginPhase
	a.SetLoginObserver(func(st LoginStage) { seen = append(seen, st.Phase) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if a.LoginWithContext(ctx) {
		t.Fatal("已取消的 ctx 不应登录成功")
	}
	if st := a.LoginStage(); st.Phase != LoginPhasePrepare {
		t.Fatalf("LoginStage = %+v, want %s", st, LoginPhasePrepare)
	}
	if len(seen) == 0 || seen[0] != LoginPhasePrepare {
		t.Fatalf("observer 首帧 = %v, want %s", seen, LoginPhasePrepare)
	}
}
