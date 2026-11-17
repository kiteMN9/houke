package jwmock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// countEvents 统计某类事件的条数（用于"这类动作只该发生 N 次"的断言）。
func countEvents(events []engine.Event, kind string) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// countTabParamsRequests 统计 mock 请求流水里参数页（SelectTab 前置）的请求次数。
func countTabParamsRequests(srv *jwmock.Server) int {
	n := 0
	for _, req := range srv.Snapshot().Requests {
		if strings.Contains(req.Path, "cxZzxkYzbDisplay") {
			n++
		}
	}
	return n
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
	if !findEvent(events, engine.EventFailed, "该教学班已无余量") {
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
		wantErr  error
	}{
		{
			// flag=0 的确定性拒绝：不再按轮次重试，直接停。
			name:     "window_closed",
			scenario: jwmock.Scenario{WindowClosed: true},
			wantMsg:  "不在选课时间内",
			wantErr:  engine.ErrChooseRejected,
		},
		{
			name:     "global_reject",
			scenario: jwmock.Scenario{RejectAll: "选课通道已关闭"},
			wantMsg:  "选课通道已关闭",
			wantErr:  engine.ErrChooseRejected,
		},
		{
			name:     "credit_limit",
			scenario: jwmock.Scenario{MaxCredits: 3},
			wantMsg:  "超过学分上限",
			wantErr:  engine.ErrChooseRejected,
		},
		{
			// 满员是 flag=-1，名额可能释放，仍按轮次重试到用尽。
			name: "capacity_full",
			opts: jwmock.Options{Courses: []jwmock.Seed{{
				JxbID: "JXB-SOLO", KchID: "KCH-SOLO", Kcmc: "独占课程",
				Xf: "2", Sksj: "周四第1-2节", Capacity: 1, Enrolled: 1,
			}}},
			wantMsg: "该教学班已无余量",
			wantErr: engine.ErrNotChosen,
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
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
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

// 选课接口的 flag 语义以 html/_xkBcZyZzxkYzb.html 的错误样本为准，不是
// "成功 1 / 失败 0"两种：满员 -1（msg 是 "分志愿,教学班id,已选人数,本轮已选
// 人数" 的机器串）、重复选同一教学班 6、上课时间冲突走 0、成功 1（不带文案）。
//
// 这里直接调 Choose 而不是 RunAuto：轮次用尽时 RunAuto 返回的是零值结果，
// 拿不到失败那次的 flag。
func TestChooseResultFlags(t *testing.T) {
	opts := jwmock.Options{Courses: []jwmock.Seed{
		{JxbID: "JXB-FULL", KchID: "KCH-FULL", Kcmc: "满员课程", Xf: "2", Sksj: "周一第1-2节", Capacity: 1, Enrolled: 1},
		{JxbID: "JXB-DUP", KchID: "KCH-DUP", Kcmc: "重复课程", Xf: "2", Sksj: "周二第1-2节", Capacity: 5},
		{JxbID: "JXB-CFA", KchID: "KCH-CFA", Kcmc: "冲突课程A", Xf: "2", Sksj: "周三第1-2节", Capacity: 5},
		{JxbID: "JXB-CFB", KchID: "KCH-CFB", Kcmc: "冲突课程B", Xf: "2", Sksj: "周三第1-2节", Capacity: 5},
	}}
	_, base := startMock(t, opts)
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	choose := func(name string) protocol.ChooseCourseResult {
		t.Helper()
		ctx, cancel := testContext(t)
		defer cancel()
		candidates, err := eng.CollectCandidates(ctx, want(name))
		if err != nil || len(candidates) != 1 {
			t.Fatalf("CollectCandidates(%s) = %d, %v", name, len(candidates), err)
		}
		res, err := eng.Choose(ctx, &candidates[0])
		if err != nil {
			t.Fatalf("Choose(%s): %v", name, err)
		}
		return res
	}

	// 满员：-1 + 人数机器串。
	res := choose("满员课程")
	if res.Flag != "-1" {
		t.Fatalf("满员 flag = %q，期望 -1", res.Flag)
	}
	if parts := strings.Split(res.Msg, ","); len(parts) != 4 || parts[0] != "0" || parts[1] != "JXB-FULL" || parts[3] != "" {
		t.Fatalf("满员 msg = %q，期望 0,教学班id,已选人数,（第 4 段为空）", res.Msg)
	}

	// 成功：1，不带额外文案。
	if res := choose("重复课程"); res.Flag != "1" || res.Msg != "" {
		t.Fatalf("成功 flag = %q msg = %q", res.Flag, res.Msg)
	}
	// 重复选同一教学班：实测仍是 1（教务对重复提交不区分，同样回 {"flag":"1"}）。
	// 页面脚本里那条 6「该教学班已选中」的分支本校走不到，客户端不能拿它当
	// "已选"的唯一凭据。
	if res := choose("重复课程"); res.Flag != "1" || res.Msg != "" {
		t.Fatalf("重复选课 flag = %q msg = %q，期望仍是 1（实测行为）", res.Flag, res.Msg)
	}

	// 时间冲突：选上 A 后再选同时间的 B，实测走 0 + 文案。
	if res := choose("冲突课程A"); res.Flag != "1" {
		t.Fatalf("选冲突课程A flag = %q，期望 1", res.Flag)
	}
	if res := choose("冲突课程B"); res.Flag != "0" || !strings.Contains(res.Msg, "冲突") {
		t.Fatalf("时间冲突 flag = %q msg = %q，期望 0 + 文案", res.Flag, res.Msg)
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

	// 已经选上的课再次启动自动选课：教务对重复提交回的是 flag=1（实测与第一次
	// 选上同形），所以这一遍会**以成功收尾**，而不是靠 flag=6 认"已选"。关键是
	// 只提交一次就停——不能反复对着同一个班打请求。
	res, err, events := runAuto(t, eng, engine.Plan{Wants: want("数据结构"), MaxRounds: 5})
	if err != nil {
		t.Fatalf("重复选课 err = %v, want nil（重复提交即成功）", err)
	}
	if res.Flag != "1" {
		t.Fatalf("重复选课 flag = %q, want 1", res.Flag)
	}
	if picks := countEvents(events, engine.EventPick); picks != 1 {
		t.Fatalf("提交次数 = %d, want 1（一次成功就收工，不再反复尝试）: %+v", picks, events)
	}
	if countEvents(events, engine.EventSuccess) != 1 {
		t.Fatalf("缺少成功事件: %+v", events)
	}
	for _, ev := range events {
		if ev.Kind == engine.EventFailed || ev.Kind == engine.EventAlreadyChosen {
			t.Fatalf("重复提交不该记成失败/已选: %+v", ev)
		}
	}
}

// 同一门课已经选了一个教学班时，对兄弟教学班提交会收到 flag=0「一门课程只能选
// 一个教学班」。它不是普通的确定性拒绝，而是"这门课已选"：任务要认作已选并让
// 整门课收工，不能被打死，也不能每轮重新对着兄弟班级打。
//
// 三个教学班同属一个课程号 → 不查已选课表 → 第一次提交就撞上这条反馈。
func TestAutoChooseSkipsChosenSiblings(t *testing.T) {
	opts := jwmock.Options{Courses: []jwmock.Seed{
		{JxbID: "JXB-TS-01", KchID: "KCH-TS", Kcmc: "通识课", Xf: "2", Sksj: "周一第1-2节", Capacity: 10},
		{JxbID: "JXB-TS-02", KchID: "KCH-TS", Kcmc: "通识课", Xf: "2", Sksj: "周二第1-2节", Capacity: 10},
		{JxbID: "JXB-TS-03", KchID: "KCH-TS", Kcmc: "通识课", Xf: "2", Sksj: "周三第1-2节", Capacity: 10},
	}}
	_, base := startMock(t, opts)
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	// 先手动选上 01（模拟用户在别的窗口已经选过这门课的一个班）。
	ctx, cancel := testContext(t)
	defer cancel()
	candidates, err := eng.CollectCandidates(ctx, want("通识课"))
	if err != nil || len(candidates) != 3 {
		t.Fatalf("CollectCandidates = %d, %v, want 3", len(candidates), err)
	}
	var first *protocol.CustomCourseDic
	for i := range candidates {
		if candidates[i].Jxb_id == "JXB-TS-01" {
			first = &candidates[i]
			break
		}
	}
	if first == nil {
		t.Fatalf("候选里没有 JXB-TS-01: %+v", candidates)
	}
	if res, err := eng.Choose(ctx, first); err != nil || res.Flag != "1" {
		t.Fatalf("手动选课失败: flag=%q err=%v", res.Flag, err)
	}

	_, err, events := runAuto(t, eng, engine.Plan{Wants: want("通识课"), MaxRounds: 5})
	if !errors.Is(err, engine.ErrAllChosen) {
		t.Fatalf("err = %v, want ErrAllChosen（兄弟班级一并剔除）", err)
	}
	// flag=6（撞上已选的 01）或 flag=0「只能选一个教学班」（撞上 02/03）
	// 都只发生一次：反馈一到就整门课收工。
	if picks := countEvents(events, engine.EventPick); picks != 1 {
		t.Fatalf("提交次数 = %d, want 1: %+v", picks, events)
	}
	if countEvents(events, engine.EventAlreadyChosen) != 1 {
		t.Fatalf("缺少已选事件: %+v", events)
	}
	if findEvent(events, engine.EventFailed, "一门课程只能选一个教学班") {
		t.Fatalf("「只能选一个教学班」不该记成失败: %+v", events)
	}
}

// 板块课（整个页签只有一种课程号、下面几十个教学班）不查已选课程表：已选接口
// 比课程列表贵得多，而这门课选上一个班就结束了，查它省下的只是一次提交。
func TestAutoChooseSkipsChosenQueryForBlockCourse(t *testing.T) {
	opts := jwmock.Options{Courses: []jwmock.Seed{
		{JxbID: "JXB-BK-01", KchID: "KCH-BK", Kcmc: "体育Ⅳ", Xf: "1", Sksj: "周一第1-2节", Capacity: 10},
		{JxbID: "JXB-BK-02", KchID: "KCH-BK", Kcmc: "体育Ⅳ", Xf: "1", Sksj: "周二第1-2节", Capacity: 10},
		{JxbID: "JXB-BK-03", KchID: "KCH-BK", Kcmc: "体育Ⅳ", Xf: "1", Sksj: "周三第1-2节", Capacity: 10},
	}}
	srv, base := startMock(t, opts)
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	res, err, _ := runAuto(t, eng, engine.Plan{Wants: want("体育Ⅳ"), MaxRounds: 3})
	if err != nil || res.Flag != "1" {
		t.Fatalf("板块课选课失败: flag=%q err=%v", res.Flag, err)
	}

	var sawChoose bool
	for _, req := range srv.Snapshot().Requests {
		if strings.Contains(req.Path, "cxZzxkYzbChoosedDisplay") {
			t.Fatalf("板块课不该查询已选课程表，却请求了 %s", req.Path)
		}
		if strings.Contains(req.Path, "xkBcZyZzxkYzb") {
			sawChoose = true
		}
	}
	// 不然上面那条断言可能是"流水根本没记"造成的空过。
	if !sawChoose {
		t.Fatal("请求流水里没有选课请求，已选查询的断言不成立")
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
	// 时间冲突是 flag=0 的确定性拒绝：直接停止，不再按轮次重试。
	if !errors.Is(err, engine.ErrChooseRejected) {
		t.Fatalf("时间冲突 err = %v, want ErrChooseRejected", err)
	}
	if res.Flag == "1" {
		t.Fatal("时间冲突时不应选上")
	}
	if !findEvent(events, engine.EventFailed, "有冲突") {
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
	if !errors.Is(err, engine.ErrChooseRejected) {
		t.Fatalf("门数上限 err = %v, want ErrChooseRejected", err)
	}
	if res.Flag == "1" {
		t.Fatal("超过门数上限时不应选上")
	}
	if !findEvent(events, engine.EventFailed, "最高选课门次限制") {
		t.Fatalf("缺少门数上限失败事件: %+v", events)
	}
}

// 只有"选课频率过高"这种 flag=0 值得重试：中英文两种文案都认。
func TestTransientRejectRecovers(t *testing.T) {
	cases := []struct {
		name    string
		msg     string
		wantSub string
	}{
		{"中文", "选课频率过高，请稍后重试！", "频率过高"},
		{"英文", "The frequency of course selection is too high, please try again in 20 seconds!", "frequency of course selection is too high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, base := startMock(t, jwmock.Options{})
			eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
			mustLogin(t, eng)
			if err := srv.SetScenario(jwmock.Scenario{
				RejectNext:    map[string]int{"JXB-MATH-01": 1},
				RejectNextMsg: tc.msg,
			}); err != nil {
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
				t.Fatalf("频率限制后应重试成功: flag=%q msg=%q", res.Flag, res.Msg)
			}
			if !findEvent(events, engine.EventFailed, tc.wantSub) {
				t.Fatalf("缺少频率限制失败事件: %+v", events)
			}
		})
	}
}

// 确定性拒绝（flag=0 且不是频率限制）要立即停止：同一门课不会被按轮次反复提交。
func TestNonRetryableRejectStops(t *testing.T) {
	srv, base := startMock(t, jwmock.Options{})
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)
	if err := srv.SetScenario(jwmock.Scenario{MaxCredits: 3}); err != nil {
		t.Fatalf("SetScenario: %v", err)
	}

	_, err, events := runAuto(t, eng, engine.Plan{
		Wants:     want("高等数学"),
		Interval:  10 * time.Millisecond,
		MaxRounds: 5,
	})
	if !errors.Is(err, engine.ErrChooseRejected) {
		t.Fatalf("err = %v, want ErrChooseRejected", err)
	}
	picks := 0
	for _, ev := range events {
		if ev.Kind == engine.EventPick {
			picks++
		}
	}
	if picks != 1 {
		t.Fatalf("pick = %d，期望 1（拒绝后不再重试）", picks)
	}
	if !findEvent(events, engine.EventFailed, "超过学分上限") {
		t.Fatalf("缺少学分上限失败事件: %+v", events)
	}
	var rej *engine.ChooseRejectError
	if !errors.As(err, &rej) || !strings.Contains(rej.Result.Msg, "超过学分上限") {
		t.Fatalf("err = %v，期望带上教务文案", err)
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
	if !findEvent(events, engine.EventFailed, "该教学班已无余量") {
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

// TestTabSwitching 验证多页签：首页渲染多个页签、Initialize 能解析出来、
// SelectTab 会切换参数与课程集合，并且能在第二个页签里完成选课。
func TestTabSwitching(t *testing.T) {
	opts := jwmock.Options{
		Tabs: []jwmock.Tab{
			{
				Name: "主修", Kklxdm: "01", XkkzID: "XKKZ-MAIN", XkkzXh: "XH-MAIN", NjdmID: "2025", ZyhID: "07201", Xklc: "1", Xklcmc: "第1轮", Xkkssj: "2025-09-01 08:00:00", Xkjssj: "2025-09-10 23:59:59",
				Courses: []jwmock.Seed{
					{JxbID: "JXB-MAIN-01", JxbMc: "主修课程A-01班", KchID: "KCH-MAIN", Kch: "MAIN101", Kcmc: "主修课程A", Xf: "2", Sksj: "周一第1-2节", Capacity: 5},
				},
			},
			{
				Name: "通识选修课", Kklxdm: "10", XkkzID: "XKKZ-GEN", XkkzXh: "XH-GEN", NjdmID: "2025", ZyhID: "07201", Xklc: "2", Xklcmc: "第2轮", Xkkssj: "2025-09-11 08:00:00", Xkjssj: "2025-09-20 23:59:59",
				Courses: []jwmock.Seed{
					{JxbID: "JXB-GEN-01", JxbMc: "通识课程B-01班", KchID: "KCH-GEN", Kch: "GEN101", Kcmc: "通识课程B", Xf: "2", Sksj: "周二第1-2节", Capacity: 5},
				},
			},
			{
				Name: "特殊课程", Kklxdm: "09", XkkzID: "XKKZ-SPECIAL", XkkzXh: "XH-SPECIAL", NjdmID: "2025", ZyhID: "07201", Xklc: "1", Xklcmc: "第1轮", Xkkssj: "2025-09-21 08:00:00", Xkjssj: "2025-09-30 23:59:59",
				Courses: []jwmock.Seed{
					{JxbID: "JXB-SPECIAL-01", JxbMc: "特殊课程C-01班", KchID: "KCH-SPECIAL", Kch: "SPEC101", Kcmc: "特殊课程C", Xf: "1", Sksj: "周五第1-2节", Capacity: 5},
				},
			},
		},
	}
	srv, base := startMock(t, opts)
	if got := srv.Options().Courses; len(got) != 0 {
		t.Fatalf("页签配套课程时全局课程应为空，实际 %+v", got)
	}
	for i, tab := range srv.Options().Tabs {
		if len(tab.Courses) != 1 {
			t.Fatalf("页签 %d 配套课程数: got %d, want 1", i, len(tab.Courses))
		}
	}
	eng := newEngine(t, base, jwmock.DefaultUsername, jwmock.DefaultPassword)
	mustLogin(t, eng)

	tabs := eng.Tabs()
	if len(tabs) != 3 {
		t.Fatalf("Tabs: got %d, want 3 (%+v)", len(tabs), tabs)
	}
	if tabs[0].Kklxmc != "主修" || tabs[1].Kklxmc != "通识选修课" || tabs[2].Kklxmc != "特殊课程" {
		t.Fatalf("页签名称: %+v", tabs)
	}
	if tabs[1].Kklxdm != "10" || tabs[1].XkkzId != "XKKZ-GEN" || tabs[1].XkkzXh != "XH-GEN" {
		t.Fatalf("第二个页签字段不对: %+v", tabs[1])
	}

	ctx, cancel := testContext(t)
	defer cancel()

	list, err := eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("GetCourseList(主修): %v", err)
	}
	if len(list) != 1 || list[0].Kcmc != "主修课程A" {
		t.Fatalf("主修页签课程: %+v", list)
	}

	if err := eng.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1): %v", err)
	}
	if w := eng.Window(); w.Xkkssj != "2025-09-11 08:00:00" || w.Xkjssj != "2025-09-20 23:59:59" {
		t.Fatalf("切页签后的时间窗: %+v", w)
	}
	list, err = eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("GetCourseList(通识): %v", err)
	}
	if len(list) != 1 || list[0].Kcmc != "通识课程B" {
		t.Fatalf("通识页签课程: %+v", list)
	}

	res, runErr, events := runAuto(t, eng, engine.Plan{
		Wants:     want("通识课程B"),
		Interval:  20 * time.Millisecond,
		MaxRounds: 3,
	})
	if runErr != nil || res.Flag != "1" {
		t.Fatalf("第二个页签自动选课: flag=%q err=%v events=%+v", res.Flag, runErr, events)
	}

	if err := eng.SelectTab(ctx, 2); err != nil {
		t.Fatalf("SelectTab(2): %v", err)
	}
	list, err = eng.GetCourseList(ctx)
	if err != nil {
		t.Fatalf("GetCourseList(特殊): %v", err)
	}
	if len(list) != 1 || list[0].Kcmc != "特殊课程C" {
		t.Fatalf("特殊页签课程: %+v", list)
	}

	// 切回访问过的页签：参数页不再请求，直接用第一次解析出来的那份参数。
	if err := eng.SelectTab(ctx, 1); err != nil {
		t.Fatalf("再次 SelectTab(1): %v", err)
	}
	if w := eng.Window(); w.Xkkssj != "2025-09-11 08:00:00" || w.Xkjssj != "2025-09-20 23:59:59" {
		t.Fatalf("缓存命中的页签时间窗: %+v", w)
	}
	// 初始化抓默认页签 + 第一次进入页签 1/2 各一次 = 3；缓存命中不该再出现。
	if got := countTabParamsRequests(srv); got != 3 {
		t.Fatalf("参数页请求 %d 次，期望 3（缓存命中不重抓）", got)
	}

	// 再点当前页签 = 刷新：绕过缓存重抓一次参数页。
	if err := eng.SelectTab(ctx, 1); err != nil {
		t.Fatalf("刷新当前页签: %v", err)
	}
	if got := countTabParamsRequests(srv); got != 4 {
		t.Fatalf("再点当前页签应重抓参数，参数页请求 %d 次，期望 4", got)
	}

	if ids := selectedIDs(t, eng); len(ids) != 1 || ids[0] != "JXB-GEN-01" {
		t.Fatalf("已选课程: %v", ids)
	}
}

// TestTabCoursesValidation 验证页签课程配置的约束：页签课程与全局课程共享
// JxbID 命名空间，重复即启动失败。
func TestTabCoursesValidation(t *testing.T) {
	_, err := jwmock.New(jwmock.Options{
		Tabs: []jwmock.Tab{
			{Name: "页签A", Kklxdm: "10", Courses: []jwmock.Seed{{JxbID: "JXB-DUP"}}},
			{Name: "页签B", Kklxdm: "09", Courses: []jwmock.Seed{{JxbID: "JXB-DUP"}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "教学班重复") {
		t.Fatalf("重复 JxbID 应报错，实际: %v", err)
	}
}

// TestExampleConfig 确保随仓库提供的 config.example.json 能被 -config 直接加载，
// 并且页签带着自己的课程。
func TestExampleConfig(t *testing.T) {
	raw, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatalf("读取 config.example.json: %v", err)
	}
	var opts jwmock.Options
	if err := json.Unmarshal(raw, &opts); err != nil {
		t.Fatalf("解析 config.example.json: %v", err)
	}
	srv, err := jwmock.New(opts)
	if err != nil {
		t.Fatalf("按示例配置启动: %v", err)
	}
	tabs := srv.Options().Tabs
	if len(tabs) != 2 {
		t.Fatalf("示例页签数: got %d, want 2", len(tabs))
	}
	for i, tab := range tabs {
		if len(tab.Courses) == 0 {
			t.Fatalf("示例页签 %d(%s) 没有配套课程", i, tab.Name)
		}
	}
}
