package jwmock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

// startMock 启动一个本地模拟教务，返回服务实例和可直接填进 Settings.BaseURL 的地址。
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

func newEngine(t *testing.T, baseURL, username, password string) *engine.Engine {
	t.Helper()
	eng, err := engine.New(config.Settings{
		BaseURL:   baseURL,
		Account:   username,
		Password:  password,
		UserAgent: "jwmock-test/1.0",
	}, config.NewSession(), engine.LoginPassword, engine.Options{})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng
}

func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 40*time.Second)
}

func mustLogin(t *testing.T, eng *engine.Engine) {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	if !eng.Login(ctx) {
		t.Fatal("登录失败")
	}
	if err := eng.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func want(names ...string) protocol.WishList {
	return protocol.WishList{WantClassList: names}
}

// runAuto 跑一次自动选课，返回结果和完整事件序列。
func runAuto(t *testing.T, eng *engine.Engine, plan engine.Plan) (protocol.ChooseCourseResult, error, []engine.Event) {
	t.Helper()
	var mu sync.Mutex
	var events []engine.Event
	sink := func(ev engine.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := eng.RunAuto(ctx, plan, eng.CandidateSource(plan.Wants), sink)
	mu.Lock()
	defer mu.Unlock()
	return res, err, append([]engine.Event(nil), events...)
}

func findEvent(events []engine.Event, kind, substr string) bool {
	for _, ev := range events {
		if ev.Kind == kind && strings.Contains(ev.Message, substr) {
			return true
		}
	}
	return false
}

func selectedIDs(t *testing.T, eng *engine.Engine) []string {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	list, err := eng.Selected(ctx, "2025", "3")
	if err != nil {
		t.Fatalf("Selected: %v", err)
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.JxbId)
	}
	return ids
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: HTTP %d %s", url, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("GET %s: 解析响应: %v", url, err)
	}
}

func postJSON(t *testing.T, url, body string, out any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: HTTP %d %s", url, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("POST %s: 解析响应: %v", url, err)
		}
	}
}

func TestLoginAndCatalog(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	ctx, cancel := testContext(t)
	defer cancel()

	list, err := eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("GetCourseList: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("课程列表 = %d 门, want 4", len(list))
	}

	detail, err := eng.GetCourseDetail(ctx, "KCH-MATH")
	if err != nil {
		t.Fatalf("GetCourseDetail: %v", err)
	}
	if len(detail) != 2 {
		t.Fatalf("高等数学教学班 = %d 个, want 2", len(detail))
	}
	if detail[0].Jxbrl == "" {
		t.Fatalf("详情缺少容量: %+v", detail[0])
	}

	if got := srv.Snapshot().Logins; got != 1 {
		t.Fatalf("logins = %d, want 1", got)
	}
}

func TestLoginFailure(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, "wrong-password")

	ctx, cancel := testContext(t)
	defer cancel()
	if eng.Login(ctx) {
		t.Fatal("错误密码不应登录成功")
	}
	if got := srv.Snapshot().LoginFailures; got == 0 {
		t.Fatal("登录失败次数没有被记录")
	}
}

func TestAutoChooseSkipsFull(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	res, err, events := runAuto(t, eng, engine.Plan{
		Wants:     want("高等数学"),
		Interval:  20 * time.Millisecond,
		MaxRounds: 3,
	})
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if res.Flag != "1" {
		t.Fatalf("选课结果 flag = %q, msg = %q", res.Flag, res.Msg)
	}
	if !findEvent(events, engine.EventSuccess, "") {
		t.Fatalf("缺少 success 事件: %+v", events)
	}

	st := srv.Snapshot()
	if len(st.Chosen) != 1 || st.Chosen[0] != "JXB-MATH-01" {
		t.Fatalf("chosen = %v, want [JXB-MATH-01]", st.Chosen)
	}
	if ids := selectedIDs(t, eng); len(ids) != 1 || ids[0] != "JXB-MATH-01" {
		t.Fatalf("已选课程 = %v, want [JXB-MATH-01]", ids)
	}

	ctx, cancel := testContext(t)
	defer cancel()
	ok, _, err := eng.Quit(ctx, "JXB-MATH-01", "KCH-MATH")
	if err != nil {
		t.Fatalf("Quit: %v", err)
	}
	if !ok {
		t.Fatal("Quit 应返回成功")
	}
	if ids := selectedIDs(t, eng); len(ids) != 0 {
		t.Fatalf("退课后已选 = %v, want 空", ids)
	}
}

func TestSeatReleaseInPhases(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	if err := srv.SetScenario(jwmock.Scenario{EnrolledPhases: []jwmock.EnrolledPhase{
		{AfterMs: 0, Enrolled: map[string]int{"JXB-MATH-01": 2}},
		{AfterMs: 700, Enrolled: map[string]int{"JXB-MATH-01": 0}},
	}}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	res, err, events := runAuto(t, eng, engine.Plan{
		Wants:     want("高等数学"),
		Interval:  100 * time.Millisecond,
		Refresh:   150 * time.Millisecond,
		MaxRounds: 30,
	})
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if res.Flag != "1" {
		t.Fatalf("选课结果 flag = %q, msg = %q", res.Flag, res.Msg)
	}
	if !findEvent(events, engine.EventFailed, "人数已满") {
		t.Fatalf("缺少满员失败事件: %+v", events)
	}
	if !findEvent(events, engine.EventSuccess, "") {
		t.Fatalf("缺少 success 事件: %+v", events)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name     string
		opts     jwmock.Options
		scenario jwmock.Scenario
		wantMsg  string
	}{
		{
			name:     "window_closed",
			scenario: jwmock.Scenario{WindowClosed: true},
			wantMsg:  "当前不在选课时间内",
		},
		{
			name:     "global_reject",
			scenario: jwmock.Scenario{RejectAll: "选课通道已关闭"},
			wantMsg:  "选课通道已关闭",
		},
		{
			name:     "credit_limit",
			scenario: jwmock.Scenario{MaxCredits: 3},
			wantMsg:  "超过学分上限",
		},
		{
			name: "capacity_full",
			opts: jwmock.Options{Courses: []jwmock.Seed{{
				JxbID: "JXB-SOLO", KchID: "KCH-SOLO", Kcmc: "独占课程",
				Xf: "2", Sksj: "周四第1-2节", Capacity: 1, Enrolled: 1,
			}}},
			wantMsg: "人数已满",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, base := startMock(t, tc.opts)
			eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
			mustLogin(t, eng)
			if err := srv.SetScenario(tc.scenario); err != nil {
				t.Fatalf("SetScenario: %v", err)
			}

			wants := want("高等数学")
			if tc.name == "capacity_full" {
				wants = want("独占课程")
			}
			res, err, events := runAuto(t, eng, engine.Plan{Wants: wants, Once: true})
			if !errors.Is(err, engine.ErrNotChosen) {
				t.Fatalf("err = %v, want ErrNotChosen", err)
			}
			if res.Flag == "1" {
				t.Fatalf("不应选上, msg = %q", res.Msg)
			}
			if !findEvent(events, engine.EventFailed, tc.wantMsg) {
				t.Fatalf("缺少失败事件 %q: %+v", tc.wantMsg, events)
			}
			if got := srv.Snapshot().Chosen; len(got) != 0 {
				t.Fatalf("被拒后 chosen = %v, want 空", got)
			}
		})
	}
}

func TestDuplicateChoose(t *testing.T) {
	opts := jwmock.Options{Courses: []jwmock.Seed{{
		JxbID: "JXB-DS-01", KchID: "KCH-DS", Kcmc: "数据结构",
		Xf: "3", Sksj: "周一第3-4节", Capacity: 10,
	}}}
	_, base := startMock(t, opts)
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	if res, err, _ := runAuto(t, eng, engine.Plan{Wants: want("数据结构"), Once: true}); err != nil || res.Flag != "1" {
		t.Fatalf("第一次选课失败: flag=%q err=%v", res.Flag, err)
	}

	res, err, events := runAuto(t, eng, engine.Plan{Wants: want("数据结构"), Once: true})
	if !errors.Is(err, engine.ErrNotChosen) {
		t.Fatalf("重复选课 err = %v, want ErrNotChosen", err)
	}
	if res.Flag == "1" {
		t.Fatal("重复选课不应成功")
	}
	if !findEvent(events, engine.EventFailed, "不能重复选择") {
		t.Fatalf("缺少重复选课失败事件: %+v", events)
	}
}

func TestTimeConflict(t *testing.T) {
	opts := jwmock.Options{Courses: []jwmock.Seed{
		{JxbID: "JXB-A-01", KchID: "KCH-A", Kcmc: "课程A", Xf: "2", Sksj: "周一第5-6节", Capacity: 10},
		{JxbID: "JXB-B-01", KchID: "KCH-B", Kcmc: "课程B", Xf: "2", Sksj: "周一第5-6节", Capacity: 10},
	}}
	_, base := startMock(t, opts)
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	if res, err, _ := runAuto(t, eng, engine.Plan{Wants: want("课程A"), Once: true}); err != nil || res.Flag != "1" {
		t.Fatalf("选课程A失败: flag=%q err=%v", res.Flag, err)
	}

	res, err, events := runAuto(t, eng, engine.Plan{Wants: want("课程B"), Once: true})
	if !errors.Is(err, engine.ErrNotChosen) {
		t.Fatalf("时间冲突 err = %v, want ErrNotChosen", err)
	}
	if res.Flag == "1" {
		t.Fatal("时间冲突时不应选上")
	}
	if !findEvent(events, engine.EventFailed, "时间冲突") {
		t.Fatalf("缺少时间冲突失败事件: %+v", events)
	}
}

func TestMaxCourses(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	if res, err, _ := runAuto(t, eng, engine.Plan{Wants: want("大学英语"), Once: true}); err != nil || res.Flag != "1" {
		t.Fatalf("选大学英语失败: flag=%q err=%v", res.Flag, err)
	}
	if err := srv.SetScenario(jwmock.Scenario{MaxCourses: 1}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	res, err, events := runAuto(t, eng, engine.Plan{Wants: want("西方美术鉴赏"), Once: true})
	if !errors.Is(err, engine.ErrNotChosen) {
		t.Fatalf("门数上限 err = %v, want ErrNotChosen", err)
	}
	if res.Flag == "1" {
		t.Fatal("超过门数上限时不应选上")
	}
	if !findEvent(events, engine.EventFailed, "门数已达上限") {
		t.Fatalf("缺少门数上限失败事件: %+v", events)
	}
}

func TestTransientRejectRecovers(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)
	if err := srv.SetScenario(jwmock.Scenario{RejectNext: map[string]int{"JXB-MATH-01": 1}}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	res, err, events := runAuto(t, eng, engine.Plan{
		Wants:     want("高等数学"),
		Interval:  20 * time.Millisecond,
		MaxRounds: 4,
	})
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if res.Flag != "1" {
		t.Fatalf("瞬时失败后应重试成功: flag=%q msg=%q", res.Flag, res.Msg)
	}
	if !findEvent(events, engine.EventFailed, "系统繁忙") {
		t.Fatalf("缺少瞬时失败事件: %+v", events)
	}
}

func TestTwoAccountsLastSeat(t *testing.T) {
	opts := jwmock.Options{
		Users: []jwmock.User{
			{Username: "20250001", Password: "p1"},
			{Username: "20250002", Password: "p2"},
		},
		Courses: []jwmock.Seed{{
			JxbID: "JXB-ONE", KchID: "KCH-ONE", Kcmc: "唯一名额",
			Xf: "2", Sksj: "周五第1-2节", Capacity: 1,
		}},
	}
	srv, base := startMock(t, opts)

	a := newEngine(t, base, "20250001", "p1")
	mustLogin(t, a)
	b := newEngine(t, base, "20250002", "p2")
	mustLogin(t, b)

	if res, err, _ := runAuto(t, a, engine.Plan{Wants: want("唯一名额"), Once: true}); err != nil || res.Flag != "1" {
		t.Fatalf("账号A抢座失败: flag=%q err=%v", res.Flag, err)
	}

	res, err, events := runAuto(t, b, engine.Plan{Wants: want("唯一名额"), Once: true})
	if !errors.Is(err, engine.ErrNotChosen) {
		t.Fatalf("账号B err = %v, want ErrNotChosen", err)
	}
	if res.Flag == "1" {
		t.Fatal("最后一个名额被占后账号B不应选上")
	}
	if !findEvent(events, engine.EventFailed, "人数已满") {
		t.Fatalf("账号B缺少满员事件: %+v", events)
	}

	st := srv.Snapshot()
	if got := st.Enrolled["JXB-ONE"]; got != 1 {
		t.Fatalf("JXB-ONE enrolled = %d, want 1", got)
	}
	if ids := selectedIDs(t, a); len(ids) != 1 || ids[0] != "JXB-ONE" {
		t.Fatalf("账号A已选 = %v, want [JXB-ONE]", ids)
	}
	if ids := selectedIDs(t, b); len(ids) != 0 {
		t.Fatalf("账号B已选 = %v, want 空", ids)
	}
}

func TestSessionRevokedAutoRelogin(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	if n := srv.RevokeSessions(); n != 1 {
		t.Fatalf("revoked = %d, want 1", n)
	}

	res, err, events := runAuto(t, eng, engine.Plan{Wants: want("高等数学"), Once: true})
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if res.Flag != "1" {
		t.Fatalf("被踢后应自动重登并选上: flag=%q msg=%q", res.Flag, res.Msg)
	}
	if !findEvent(events, engine.EventSuccess, "") {
		t.Fatalf("缺少 success 事件: %+v", events)
	}
	if got := srv.Snapshot().Logins; got < 2 {
		t.Fatalf("logins = %d, want >= 2", got)
	}
}

func TestParamExpireRecovers(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)
	if err := srv.SetScenario(jwmock.Scenario{ParamExpire: 1}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	ctx, cancel := testContext(t)
	defer cancel()
	list, err := eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("参数过期后应自动 Renew 并重试: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("课程列表 = %d 门, want 4", len(list))
	}
}

func TestHTTPFaults(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		srv, base := startMock(t, jwmock.Options{})
		eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
		mustLogin(t, eng)
		if err := srv.SetScenario(jwmock.Scenario{Faults: []jwmock.Fault{{
			PathContains: "zzxkyzbjk_xkBcZy", Status: 500, Count: 2,
		}}}); err != nil {
			t.Fatalf("SetScenario: %v", err)
		}

		res, err, _ := runAuto(t, eng, engine.Plan{Wants: want("高等数学"), Once: true})
		if err != nil {
			t.Fatalf("RunAuto: %v", err)
		}
		if res.Flag != "1" {
			t.Fatalf("5xx 自动重试后应成功: flag=%q msg=%q", res.Flag, res.Msg)
		}
	})

	t.Run("429", func(t *testing.T) {
		srv, base := startMock(t, jwmock.Options{})
		eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
		mustLogin(t, eng)
		if err := srv.SetScenario(jwmock.Scenario{Faults: []jwmock.Fault{{
			PathContains: "zzxkyzbjk_xkBcZy", Status: 429, Count: 1,
		}}}); err != nil {
			t.Fatalf("SetScenario: %v", err)
		}

		res, err, _ := runAuto(t, eng, engine.Plan{Wants: want("高等数学"), Once: true})
		if err != nil {
			t.Fatalf("RunAuto: %v", err)
		}
		if res.Flag != "1" {
			t.Fatalf("429 后应重试成功: flag=%q msg=%q", res.Flag, res.Msg)
		}
	})
}

func TestControlPlane(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	ctrl := strings.TrimSuffix(base, srv.BasePath()+"/") + "/__mock"

	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	var st jwmock.State
	getJSON(t, ctrl+"/state", &st)
	if st.Logins != 1 {
		t.Fatalf("state logins = %d, want 1", st.Logins)
	}

	postJSON(t, ctrl+"/scenario", `{"rejectAll":"控制面拒绝"}`, nil)
	if got := srv.Scenario().RejectAll; got != "控制面拒绝" {
		t.Fatalf("scenario.rejectAll = %q, want 控制面拒绝", got)
	}

	postJSON(t, ctrl+"/reset", `{}`, nil)
	if got := srv.Snapshot().Logins; got != 0 {
		t.Fatalf("reset 后 logins = %d, want 0", got)
	}
	if got := srv.Scenario().RejectAll; got != "" {
		t.Fatalf("reset 后 rejectAll = %q, want 空", got)
	}

	var revoked struct {
		OK      bool `json:"ok"`
		Revoked int  `json:"revoked"`
	}
	postJSON(t, ctrl+"/revoke", `{}`, &revoked)
	if !revoked.OK {
		t.Fatal("revoke 响应缺少 ok=true")
	}
}
