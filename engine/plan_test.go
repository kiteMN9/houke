package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

func TestPlanNormalizedDefaults(t *testing.T) {
	tests := []struct {
		name        string
		plan        Plan
		wantRound   int
		wantItv     time.Duration
		wantRefresh time.Duration
	}{
		{"空值用默认间隔且不限轮次", Plan{}, 0, defaultInterval, defaultRefresh},
		{"负间隔也用默认值", Plan{Interval: -time.Second}, 0, defaultInterval, defaultRefresh},
		{"Once 等价一轮", Plan{Once: true}, 1, defaultInterval, defaultRefresh},
		{"Once 覆盖 MaxRounds", Plan{Once: true, MaxRounds: 5}, 1, defaultInterval, defaultRefresh},
		{"保留显式轮次", Plan{MaxRounds: 3}, 3, defaultInterval, defaultRefresh},
		{"保留显式间隔", Plan{Interval: 2 * time.Second}, 0, 2 * time.Second, defaultRefresh},
		{"保留显式刷新间隔", Plan{Refresh: 5 * time.Second}, 0, defaultInterval, 5 * time.Second},
		{"负刷新间隔也用默认值", Plan{Refresh: -time.Second}, 0, defaultInterval, defaultRefresh},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.plan.normalized()
			if err != nil {
				t.Fatalf("normalized: %v", err)
			}
			if got.MaxRounds != tc.wantRound {
				t.Fatalf("MaxRounds = %d, want %d", got.MaxRounds, tc.wantRound)
			}
			if got.Interval != tc.wantItv {
				t.Fatalf("Interval = %v, want %v", got.Interval, tc.wantItv)
			}
			if got.Refresh != tc.wantRefresh {
				t.Fatalf("Refresh = %v, want %v", got.Refresh, tc.wantRefresh)
			}
		})
	}
}

func TestPlanRejectsNegativeRounds(t *testing.T) {
	if _, err := (Plan{MaxRounds: -1}).normalized(); err == nil {
		t.Fatal("负 MaxRounds 应该被拒绝")
	}
}

func TestCandidatesByPriority(t *testing.T) {
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	list := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野15", Jxbrl: "10", Yxzrs: "10", Do_jxb_id: "full"},
		{Jxbmc: "定向越野16", Do_jxb_id: ""},
		{Jxbmc: "定向越野2", Jxbrl: "60", Yxzrs: "3", Do_jxb_id: "low"},
		{Jxbmc: "定向越野1", Jxbrl: "60", Yxzrs: "30", Do_jxb_id: "high"},
		{Jxbmc: "篮球1", Jxbrl: "60", Yxzrs: "0", Do_jxb_id: "other"},
	}

	got := candidatesByPriority(protocol.MatchGeneral, wants, list)
	// 满员的 定向越野15 留在队列最后当捡漏备选；没 Do_jxb_id 的提交不了、篮球1 没命中愿望。
	wantOrder := []string{"low", "high", "full"}
	if len(got) != len(wantOrder) {
		t.Fatalf("候选数 = %d，期望 %d: %+v", len(got), len(wantOrder), got)
	}
	for i, want := range wantOrder {
		if got[i].Do_jxb_id != want {
			t.Fatalf("排序 = %v，期望 %v", ids(got), wantOrder)
		}
	}
}

// 满员的教学班必须留在候选里：真实抓包（html/zzxkyzb_*.json）里 "定向越野6"
// 是 39/39 满员，被 isFull 过滤掉后 candidatesByPriority 返回空队列，上层只能
// 报"愿望清单没有命中可选的课程"——看起来像愿望没配，其实是这一轮没名额。
func TestCandidatesByPriorityKeepsFullCourse(t *testing.T) {
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	list := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野6", Kcmc: "体育Ⅰ", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "d1"},
	}

	got := candidatesByPriority(protocol.MatchGeneral, wants, list)
	if len(got) != 1 || got[0].Do_jxb_id != "d1" {
		t.Fatalf("候选 = %+v，期望保留满员的 定向越野6", got)
	}
	if !isFull(got[0]) {
		t.Fatalf("isFull(%+v) = false，期望 true", got[0])
	}
}

// 同一优先级下先试有余额的，满员的排后面：别为了捡漏先把有名额的名额放过。
func TestCandidatesByPriorityTriesAvailableFirst(t *testing.T) {
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	list := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野1", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "full"},
		{Jxbmc: "定向越野2", Jxbrl: "39", Yxzrs: "20", Do_jxb_id: "room"},
	}

	got := candidatesByPriority(protocol.MatchGeneral, wants, list)
	if len(got) != 2 || got[0].Do_jxb_id != "room" || got[1].Do_jxb_id != "full" {
		t.Fatalf("候选 = %v，期望 [room full]", ids(got))
	}
}

// WebUI 的愿望输入框叫"课程名"，填课程名（Kcmc）也要命中并按课程名排出优先级：
// 填了教学班名的排前面，只命中课程名的排后面。
func TestCandidatesByPriorityMatchesCourseName(t *testing.T) {
	wants := protocol.WishList{WantClassList: []string{"定向越野", "体育Ⅰ"}}
	list := []protocol.CustomCourseDic{
		{Jxbmc: "瑜伽4", Kcmc: "体育Ⅰ", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "yoga"},
		{Jxbmc: "定向越野6", Kcmc: "体育Ⅰ", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "cross"},
		{Jxbmc: "高等数学1", Kcmc: "高等数学", Jxbrl: "60", Yxzrs: "1", Do_jxb_id: "math"},
	}

	got := candidatesByPriority(protocol.MatchGeneral, wants, list)
	if want := []string{"cross", "yoga"}; !equalStrings(ids(got), want) {
		t.Fatalf("候选 = %v，期望 %v（定向越野 是一志愿，体育Ⅰ 是二志愿）", ids(got), want)
	}
}

// 通识选修课先看有没有名额：用户按"教师 + 类型"挑课，命中哪门都算达成，
// 点名的课满员时不能让它挡着有名额的备选。
func TestCandidatesByPriorityGeneralPrefersVacancy(t *testing.T) {
	wants := protocol.WishList{
		WantClassList:   []string{"定向越野"},
		WantTeacherList: []string{"殷海华"},
	}
	list := []protocol.CustomCourseDic{
		{Kch_id: "k1", Jxbmc: "定向越野1", Kcmc: "定向越野", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "named-full"},
		{Kch_id: "k2", Jxbmc: "西方美术鉴赏-0001", Kcmc: "西方美术鉴赏", Jsxx: "440015/殷海华/副教授",
			Jxbrl: "50", Yxzrs: "10", Do_jxb_id: "fallback"},
	}

	got := candidatesByPriority(protocol.MatchGeneral, wants, list)
	if want := []string{"fallback", "named-full"}; !equalStrings(ids(got), want) {
		t.Fatalf("候选 = %v，期望 %v（有名额的备选先试）", ids(got), want)
	}
}

// 板块课相反：志愿里的教学班顺序就是用户真实在意的顺序，哪怕它已经满员，
// 也要排在备选前面（足球13 满员就等它放名额，而不是先去选瑜伽4）。
func TestCandidatesByPriorityBlockKeepsRankOrder(t *testing.T) {
	wants := protocol.WishList{
		WantClassList:   []string{"足球13"},
		WantTeacherList: []string{"裴如意"},
	}
	list := []protocol.CustomCourseDic{
		{Kch_id: "k1", Jxbmc: "足球13", Kcmc: "体育Ⅳ", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "named-full"},
		{Kch_id: "k1", Jxbmc: "瑜伽4", Kcmc: "体育Ⅳ", Jsxx: "440015/裴如意/副教授",
			Jxbrl: "50", Yxzrs: "10", Do_jxb_id: "teacher-vacancy"},
	}

	got := candidatesByPriority(protocol.MatchBlock, wants, list)
	if want := []string{"named-full", "teacher-vacancy"}; !equalStrings(ids(got), want) {
		t.Fatalf("候选 = %v，期望 %v（教学班名优先于余额）", ids(got), want)
	}
}

func ids(list []protocol.CustomCourseDic) []string {
	out := make([]string, len(list))
	for i := range list {
		out[i] = list[i].Do_jxb_id
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunAutoRequiresCandidates(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.RunAuto(context.Background(), Plan{}, nil, nil); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("err = %v, want ErrNoCandidates", err)
	}
}

func TestRunAutoRequiresWish(t *testing.T) {
	eng := newTestEngine(t)
	candidates := []protocol.CustomCourseDic{{Jxbmc: "定向越野15", Do_jxb_id: "x"}}
	if _, err := eng.RunAuto(context.Background(), Plan{}, staticSource(candidates), nil); !errors.Is(err, ErrNoWantedCourse) {
		t.Fatalf("err = %v, want ErrNoWantedCourse", err)
	}
}

// 候选组装期间会话被 Reset：不能在旧候选上继续提交，必须返回租约失效。
func TestRunAutoStopsWhenSessionChangesDuringSource(t *testing.T) {
	eng := newTestEngine(t)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	candidates := []protocol.CustomCourseDic{{Jxbmc: "定向越野15", Do_jxb_id: "x"}}

	calls := 0
	source := func(context.Context) (Batch, error) {
		calls++
		eng.Reset()
		return Batch{Courses: candidates}, nil
	}
	_, err := eng.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 3,
		Interval:  time.Millisecond,
	}, source, nil)
	if !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("err = %v，期望 ErrSessionChanged", err)
	}
	if calls != 1 {
		t.Fatalf("source 调用 %d 次，期望 1", calls)
	}
}

// 进度回调里切换了会话（Reset / SelectTab）时，下一门提交前就要停，
// 不能把旧队列再发一轮。
func TestRunAutoStopsWhenSessionChangesBeforeChoose(t *testing.T) {
	eng := newTestEngine(t)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	candidates := []protocol.CustomCourseDic{{Jxbmc: "定向越野15", Do_jxb_id: "x"}}

	picked := false
	_, err := eng.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 3,
		Interval:  time.Millisecond,
	}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventBegin:
			eng.Reset()
		case EventPick:
			picked = true
		}
	})
	if !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("err = %v，期望 ErrSessionChanged", err)
	}
	if picked {
		t.Fatal("会话已经变更，仍然发出了选课请求")
	}
}

// 候选组装一开始就发现会话已重置：按租约失效上报，而不是普通候选错误。
func TestRunAutoWrapsSessionErrorFromSource(t *testing.T) {
	eng := newTestEngine(t)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}

	_, err := eng.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 1,
		Interval:  time.Millisecond,
	}, func(context.Context) (Batch, error) {
		return Batch{}, ErrSessionReset
	}, nil)
	if !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("err = %v，期望 ErrSessionChanged", err)
	}
}

// Reset 之后不能再拿零参数去提交：Choose 必须在本地就拒绝。
func TestChooseAfterResetDoesNotSubmit(t *testing.T) {
	eng := newTestEngine(t)
	eng.Reset()

	_, err := eng.Choose(context.Background(), &protocol.CustomCourseDic{
		Kch_id: "k1", Do_jxb_id: "d1",
	})
	if !errors.Is(err, ErrSessionReset) {
		t.Fatalf("err = %v，期望 ErrSessionReset", err)
	}
}

// staticSource 返回固定候选，用来在不碰"刷新"的情况下跑 RunAuto。
// More 留空：这张名单就是全部，RunAuto 不会为了"接着扫"额外要候选。
func staticSource(list []protocol.CustomCourseDic) Source {
	return func(context.Context) (Batch, error) { return Batch{Courses: list}, nil }
}

// 满员时教务返回 flag=-1，msg 是 "分志愿,教学班id,已选人数,本轮已选人数" 的
// 机器串（见 html/zzxkYzbChoosedZy.js），不是 HTTP 错误，也不是"没这门课"。
const chooseFullJSON = `{"flag":"-1","msg":"0,d1,39,39"}`

// autoServer 在 detailServer 的基础上再伪造选课接口，用来跑 RunAuto 的整条链路。
// 已选课程查询接口返回空列表：RunAuto 即使查了也拿不到已选名单，只能靠提交反馈
// 剔除（预过滤的用例走 autoServerFull）。
func autoServer(t *testing.T, detail string) *httptest.Server {
	t.Helper()
	return autoServerFull(t, detail, chooseFullJSON, `[]`)
}

// autoServerWithChoose 同 autoServer，但选课接口返回指定响应体。
func autoServerWithChoose(t *testing.T, detail, chooseBody string) *httptest.Server {
	t.Helper()
	return autoServerFull(t, detail, chooseBody, `[]`)
}

// autoServerFull 同 autoServerWithChoose，另带已选课程查询接口的响应体：
// 候选横跨多个课程号时，RunAuto 开跑前和每次刷新都会查一次已选课程
// （chosenFilterOf，见 needChosenQuery）。
func autoServerFull(t *testing.T, detail, chooseBody, chosenBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cxJxbWithKchZzxkYzb"):
			_, _ = io.WriteString(w, detail)
		case strings.Contains(r.URL.Path, "cxZzxkYzbChoosedDisplay"):
			_, _ = io.WriteString(w, chosenBody)
		case strings.Contains(r.URL.Path, "zzxkybk_xkBcZyZzxkYzb"):
			_, _ = io.WriteString(w, chooseBody)
		case strings.Contains(r.URL.Path, "zzxkyzbjk_xkBcZyZzxkYzb"):
			_, _ = io.WriteString(w, chooseBody)
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 准备一个"一门课、详情已经补好"的引擎，用来自定义选课接口的返回。
func rejectTestEngine(t *testing.T, chooseBody string) (*Engine, []protocol.CustomCourseDic) {
	t.Helper()
	srv := autoServerWithChoose(t, detailJSON, chooseBody)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "定向越野")})
	e.CacheCourseDetail([]protocol.CourseDetail{{JxbId: "j1", DoJxbId: "d1", Jxbrl: "50", Yxzrs: "10"}})
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	return e, candidates
}

// flag=0 且不是"频率过高"是确定性拒绝：RunAuto 必须立即停，不能把同一门课
// 按轮次再打一遍，否则会白跑几小时。
func TestRunAutoStopsOnNonRetryableReject(t *testing.T) {
	const chooseRejectedJSON = `{"flag":"0","msg":"超过学分上限！"}`
	e, candidates := rejectTestEngine(t, chooseRejectedJSON)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}

	var picks, fails int
	var failMsg string
	_, err := e.RunAuto(context.Background(), Plan{Wants: wants, MaxRounds: 5, Interval: time.Millisecond}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
		case EventFailed:
			fails++
			failMsg = ev.Message
		}
	})
	if !errors.Is(err, ErrChooseRejected) {
		t.Fatalf("err = %v，期望 ErrChooseRejected", err)
	}
	if picks != 1 || fails != 1 {
		t.Fatalf("pick/failed = %d/%d，期望 1/1（拒绝后不再重试）", picks, fails)
	}
	var rej *ChooseRejectError
	if !errors.As(err, &rej) || rej.Result.Msg != "超过学分上限！" {
		t.Fatalf("err = %v，期望带上教务文案", err)
	}
	if !strings.Contains(failMsg, "超过学分上限") {
		t.Fatalf("失败事件 = %q，期望带上教务文案", failMsg)
	}
}

// 频率限制是唯一值得重试的 flag=0：同一门课要按轮次继续提交。
func TestRunAutoRetriesRateLimitedChoose(t *testing.T) {
	const chooseBusyJSON = `{"flag":"0","msg":"选课频率过高，请稍后重试！"}`
	e, candidates := rejectTestEngine(t, chooseBusyJSON)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}

	var picks int
	_, err := e.RunAuto(context.Background(), Plan{Wants: wants, MaxRounds: 2, Interval: time.Millisecond}, staticSource(candidates), func(ev Event) {
		if ev.Kind == EventPick {
			picks++
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	if picks != 2 {
		t.Fatalf("pick = %d，期望 2（频率限制继续重试）", picks)
	}
}

// 命中但满员的候选必须真的进队列重试：旧实现把它当"没有候选"，一轮都不提交就
// 报 ErrNoWantedCourse。现在应提交一次、记一次失败，轮次用尽才报 ErrNotChosen。
func TestRunAutoRetriesFullCandidate(t *testing.T) {
	srv := autoServer(t, detailJSON)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "定向越野")})
	e.CacheCourseDetail([]protocol.CourseDetail{{JxbId: "j1", DoJxbId: "d1", Jxbrl: "39", Yxzrs: "39"}})

	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("候选数 = %d，期望 1: %+v", len(candidates), candidates)
	}

	var picks, fails int
	var pickMsg, failMsg string
	_, err = e.RunAuto(context.Background(), Plan{Wants: wants, MaxRounds: 1, Interval: time.Millisecond}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
			pickMsg = ev.Message
		case EventFailed:
			fails++
			failMsg = ev.Message
		}
	})
	if errors.Is(err, ErrNoWantedCourse) {
		t.Fatalf("满员候选被当成了没有候选: %v", err)
	}
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	if picks != 1 || fails != 1 {
		t.Fatalf("pick/failed = %d/%d，期望 1/1", picks, fails)
	}
	if !strings.Contains(pickMsg, "已满 39/39") {
		t.Fatalf("进度文案 = %q，期望写明已满人数", pickMsg)
	}
	// flag=-1 的 msg 是人数机器串，展示前要翻成固定提示（DisplayMsg）。
	if !strings.Contains(failMsg, "该教学班已无余量") {
		t.Fatalf("失败文案 = %q，期望把 -1 的人数机器串翻成人话", failMsg)
	}
}

// 运行中必须周期性刷新候选：第二轮就该看到"新放出来的名额"，并把它排到满员
// 教学班前面先试。
func TestRunAutoRefreshesCandidates(t *testing.T) {
	srv := autoServer(t, detailJSON)
	e := newCandidateEngine(t, srv, nil)

	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	fullOnly := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野1", Kcmc: "体育Ⅰ", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "full"},
	}
	withRoom := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野2", Kcmc: "体育Ⅰ", Jxbrl: "39", Yxzrs: "20", Do_jxb_id: "room"},
		{Jxbmc: "定向越野1", Kcmc: "体育Ⅰ", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "full"},
	}

	var calls int
	source := func(context.Context) (Batch, error) {
		calls++
		if calls == 1 {
			return Batch{Courses: fullOnly}, nil
		}
		return Batch{Courses: withRoom}, nil
	}

	var picks, refreshes int
	var pickMsgs []string
	_, err := e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  80 * time.Millisecond,
		Refresh:   20 * time.Millisecond,
	}, source, func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
			pickMsgs = append(pickMsgs, ev.Message)
		case EventRefresh:
			refreshes++
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	if calls != 2 {
		t.Fatalf("候选源调用 %d 次，期望 2（开跑一次 + 刷新一次）", calls)
	}
	if refreshes != 1 {
		t.Fatalf("刷新事件 = %d，期望 1", refreshes)
	}
	// 第一轮只有满员的 定向越野1；刷新后 定向越野2 有名额，应该先试它。
	if picks != 3 {
		t.Fatalf("提交次数 = %d，期望 3: %v", picks, pickMsgs)
	}
	if len(pickMsgs) > 1 && !strings.Contains(pickMsgs[1], "定向越野2") {
		t.Fatalf("第二轮先试 %q，期望刷新出来的 定向越野2 排到前面", pickMsgs[1])
	}
}

// 候选源说"这一遍还没扫完"（Batch.More）时，这一轮没选上就立刻再要一批，
// 不必等 Plan.Refresh：通识选修整页几十个课程号，扫到就选、没选上接着扫。
// 这里把 Refresh 设成 1 小时——能不能接着扫只看 More。
func TestRunAutoRescansWhenSourceHasMore(t *testing.T) {
	srv := autoServer(t, detailJSON)
	e := newCandidateEngine(t, srv, nil)

	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	first := []protocol.CustomCourseDic{
		{Kch_id: "k1", Jxbmc: "定向越野1", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "d1"},
	}
	second := append(append([]protocol.CustomCourseDic{}, first...),
		protocol.CustomCourseDic{Kch_id: "k2", Jxbmc: "定向越野2", Jxbrl: "50", Yxzrs: "20", Do_jxb_id: "d2"})

	var calls int
	source := func(context.Context) (Batch, error) {
		calls++
		switch calls {
		case 1:
			return Batch{Courses: first, More: true}, nil
		case 2:
			return Batch{Courses: second, More: true}, nil
		default:
			return Batch{Courses: second}, nil
		}
	}

	var pickIDs []string
	_, err := e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  time.Millisecond,
		Refresh:   time.Hour,
	}, source, func(ev Event) {
		if ev.Kind == EventPick {
			pickIDs = append(pickIDs, ev.Course.Do_jxb_id)
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	// 开跑要了第一批，第二轮又立刻要了一批：没等刷新周期。
	if calls != 2 {
		t.Fatalf("候选源调用 %d 次，期望 2（More 时不等刷新就续扫）", calls)
	}
	if len(pickIDs) < 2 || pickIDs[1] != "d2" {
		t.Fatalf("提交序列 = %v，期望第二轮先试新扫到的 d2", pickIDs)
	}
}

// 刷新失败（教务抖动、列表被挡）不能把队列清空：本轮继续用上一次的名单提交。
func TestRunAutoKeepsQueueWhenRefreshFails(t *testing.T) {
	srv := autoServer(t, detailJSON)
	e := newCandidateEngine(t, srv, nil)

	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	only := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野1", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "full"},
	}
	var calls int
	source := func(context.Context) (Batch, error) {
		calls++
		if calls == 1 {
			return Batch{Courses: only}, nil
		}
		return Batch{}, errors.New("列表查询被拒绝")
	}

	var picks, failed int
	var failedMsg string
	_, err := e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  80 * time.Millisecond,
		Refresh:   20 * time.Millisecond,
	}, source, func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
		case EventRefreshFailed:
			failed++
			failedMsg = ev.Message
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	if failed != 1 || !strings.Contains(failedMsg, "沿用上一次的名单") {
		t.Fatalf("刷新失败事件 = %d（%q），期望 1 条说明沿用旧名单", failed, failedMsg)
	}
	if picks != 2 {
		t.Fatalf("提交次数 = %d，期望 2（两轮各试一次旧队列）", picks)
	}
}

// 刷新拿到名单、但一个都提交不了（比如这次详情全失败）时，同样不能丢队列。
func TestRunAutoKeepsQueueWhenRefreshHasNoUsableCourse(t *testing.T) {
	srv := autoServer(t, detailJSON)
	e := newCandidateEngine(t, srv, nil)

	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	only := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野1", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "full"},
	}
	var calls int
	source := func(context.Context) (Batch, error) {
		calls++
		if calls == 1 {
			return Batch{Courses: only}, nil
		}
		// 详情没拿到 Do_jxb_id 的条目进不了队列。
		return Batch{Courses: []protocol.CustomCourseDic{{Jxbmc: "定向越野1"}}}, nil
	}

	var picks, failed int
	var failedMsg string
	_, err := e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  80 * time.Millisecond,
		Refresh:   20 * time.Millisecond,
	}, source, func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
		case EventRefreshFailed:
			failed++
			failedMsg = ev.Message
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	if failed != 1 || !strings.Contains(failedMsg, "没有可提交的教学班") {
		t.Fatalf("刷新事件 = %d（%q），期望说明这次没拿到可提交名单", failed, failedMsg)
	}
	if picks != 2 {
		t.Fatalf("提交次数 = %d，期望 2（两轮各试一次旧队列）", picks)
	}
}

// needChosenQuery 决定"值不值得查已选课程表"：只有候选横跨多个课程号时才查。
// 板块课整个页签只有一种课程号（下面几十个教学班），查了也只省一次提交，
// 不如把最贵的请求省掉。
func TestNeedChosenQuery(t *testing.T) {
	cases := []struct {
		name string
		list []protocol.CustomCourseDic
		want bool
	}{
		{"空列表", nil, false},
		{"单个教学班", []protocol.CustomCourseDic{{Kch_id: "k1"}}, false},
		{"板块课：一门课多个教学班", []protocol.CustomCourseDic{
			{Kch_id: "k1"}, {Kch_id: "k1"}, {Kch_id: "k1"},
		}, false},
		{"通识选修：多个课程号", []protocol.CustomCourseDic{
			{Kch_id: "k1"}, {Kch_id: "k2"}, {Kch_id: "k1"},
		}, true},
		{"课程号缺失时保守地查", []protocol.CustomCourseDic{
			{Kch_id: ""}, {Kch_id: ""},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needChosenQuery(tc.list); got != tc.want {
				t.Fatalf("needChosenQuery = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// 开跑前查到的已选教学班必须从队列剔除：不能每轮都对已选的课白打一次请求；
// 同课程号的其它教学班也要一起剔除——教务规定"一门课只能选一个教学班"，
// 对着兄弟班级提交会收到 flag=0 的拒绝。
func TestRunAutoSkipsAlreadyChosenCandidates(t *testing.T) {
	// 已选：k1 课程的 j1、j2 两个教学班（k1 整门课都要剔除）。
	srv := autoServerFull(t, detailJSON, chooseFullJSON,
		`[{"jxb_id":"j1","kch_id":"k1"},{"jxb_id":"j2","kch_id":"k1"}]`)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{
		courseListEntry("k1", "j1", "定向越野"),
		courseListEntry("k1", "j2", "定向越野"),
		courseListEntry("k2", "j3", "乒乓球"),
	})
	e.CacheCourseDetail([]protocol.CourseDetail{
		{JxbId: "j1", DoJxbId: "d1", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j2", DoJxbId: "d2", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j3", DoJxbId: "d3", Jxbrl: "50", Yxzrs: "10"},
	})

	wants := protocol.WishList{WantClassList: []string{"定向越野", "乒乓球"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}

	var picks int
	var pickIDs []string
	var beginMsg string
	_, err = e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  time.Millisecond,
		Refresh:   time.Hour, // 不触发刷新，只验证开跑前的预过滤
	}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventBegin:
			beginMsg = ev.Message
		case EventPick:
			picks++
			pickIDs = append(pickIDs, ev.Course.Do_jxb_id)
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen（满员重试到轮次用尽）", err)
	}
	// 两轮都只提交 j3；j1 / j2（已选）一次都不该碰。
	if want := []string{"d3", "d3"}; !equalStrings(pickIDs, want) {
		t.Fatalf("提交序列 = %v，期望 %v（已选的 j1/j2 不提交）", pickIDs, want)
	}
	if picks != 2 {
		t.Fatalf("提交次数 = %d，期望 2", picks)
	}
	if !strings.Contains(beginMsg, "剔除") {
		t.Fatalf("开跑文案 = %q，期望说明剔除了已选课程", beginMsg)
	}
}

// 命中的候选全部已选上时，任务应该干脆地结束（ErrAllChosen），而不是空转；
// 首次加载可能是提前收手的局部名单，所以要再整页刷新确认一次。
//
// 用两个课程号：已选查询只在候选横跨多个课程号时才做（见 needChosenQuery），
// 单课程号的形态走的是"不查、靠提交反馈"那条路。
func TestRunAutoStopsWhenAllCandidatesChosen(t *testing.T) {
	srv := autoServerFull(t, detailJSON, chooseFullJSON,
		`[{"jxb_id":"j1","kch_id":"k1"},{"jxb_id":"j2","kch_id":"k2"}]`)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{
		courseListEntry("k1", "j1", "定向越野"),
		courseListEntry("k2", "j2", "乒乓球"),
	})
	e.CacheCourseDetail([]protocol.CourseDetail{
		{JxbId: "j1", DoJxbId: "d1", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j2", DoJxbId: "d2", Jxbrl: "50", Yxzrs: "10"},
	})

	wants := protocol.WishList{WantClassList: []string{"定向越野", "乒乓球"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}

	var picks int
	_, err = e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 5,
		Interval:  time.Millisecond,
	}, staticSource(candidates), func(ev Event) {
		if ev.Kind == EventPick {
			picks++
		}
	})
	if !errors.Is(err, ErrAllChosen) {
		t.Fatalf("err = %v，期望 ErrAllChosen", err)
	}
	if picks != 0 {
		t.Fatalf("提交次数 = %d，期望 0（已选课程不该提交）", picks)
	}
}

// 板块课形态（整个页签只有一种课程号，下面挂着几十个教学班）不查已选课程表：
// 已选接口比课程列表贵得多，而这门课选上一个班就结束了，查它的唯一作用是省掉
// 一次提交——不查也只白提交那一次，flag=6 会在一次提交内把"已选"认出来。
func TestRunAutoSkipsChosenQueryForSingleCourseNumber(t *testing.T) {
	var chosenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cxZzxkYzbChoosedDisplay"):
			chosenCalls.Add(1)
			// 就算真的查了，也回一份"j1 已选"的名单：用来确认跳过查询不是
			// 因为查不到数据，而是压根没发这个请求。
			_, _ = io.WriteString(w, `[{"jxb_id":"j1","kch_id":"k1"}]`)
		case strings.Contains(r.URL.Path, "zzxkyzbjk_xkBcZyZzxkYzb"):
			_, _ = io.WriteString(w, `{"flag":"6","msg":"该教学班已选中，刷新页面可见！"}`)
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(srv.Close)

	e := newCandidateEngine(t, srv, []protocol.CourseListDic{
		courseListEntry("k1", "j1", "体育Ⅳ"),
		courseListEntry("k1", "j2", "体育Ⅳ"),
		courseListEntry("k1", "j3", "体育Ⅳ"),
	})
	e.CacheCourseDetail([]protocol.CourseDetail{
		{JxbId: "j1", DoJxbId: "d1", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j2", DoJxbId: "d2", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j3", DoJxbId: "d3", Jxbrl: "50", Yxzrs: "10"},
	})

	wants := protocol.WishList{WantClassList: []string{"体育Ⅳ"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("候选数 = %d，期望 3: %+v", len(candidates), candidates)
	}

	var picks, already int
	_, err = e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 3,
		Interval:  time.Millisecond,
		Refresh:   20 * time.Millisecond,
	}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
		case EventAlreadyChosen:
			already++
		}
	})
	if !errors.Is(err, ErrAllChosen) {
		t.Fatalf("err = %v，期望 ErrAllChosen", err)
	}
	if n := chosenCalls.Load(); n != 0 {
		t.Fatalf("已选课程接口被请求 %d 次，期望 0（板块课不查已选课表）", n)
	}
	if picks != 1 || already != 1 {
		t.Fatalf("pick/already = %d/%d，期望 1/1（只靠一次提交反馈认出已选）", picks, already)
	}
}

// 板块课不查已选，兄弟教学班撞上的 flag=0「一门课程只能选一个教学班」必须当
// "这门课已选"处理：否则它会被当成确定性拒绝，把已经满足愿望的任务打死；
// 也不能每轮都对着同课程号的其它教学班重打。
func TestRunAutoTreatsSiblingRejectAsAlreadyChosen(t *testing.T) {
	const chooseSiblingJSON = `{"flag":"0","msg":"一门课程只能选一个教学班，不可再选！"}`
	srv := autoServerWithChoose(t, detailJSON, chooseSiblingJSON)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{
		courseListEntry("k1", "j1", "体育Ⅳ"),
		courseListEntry("k1", "j2", "体育Ⅳ"),
		courseListEntry("k1", "j3", "体育Ⅳ"),
	})
	e.CacheCourseDetail([]protocol.CourseDetail{
		{JxbId: "j1", DoJxbId: "d1", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j2", DoJxbId: "d2", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j3", DoJxbId: "d3", Jxbrl: "50", Yxzrs: "10"},
	})

	wants := protocol.WishList{WantClassList: []string{"体育Ⅳ"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}

	var picks, already, fails int
	var alreadyMsg string
	_, err = e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 3,
		Interval:  time.Millisecond,
		Refresh:   20 * time.Millisecond,
	}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
		case EventAlreadyChosen:
			already++
			alreadyMsg = ev.Message
		case EventFailed:
			fails++
		}
	})
	if !errors.Is(err, ErrAllChosen) {
		t.Fatalf("err = %v，期望 ErrAllChosen（愿望已经满足，不是拒绝）", err)
	}
	if picks != 1 || already != 1 || fails != 0 {
		t.Fatalf("pick/already/failed = %d/%d/%d，期望 1/1/0（一次反馈就整门课收工）", picks, already, fails)
	}
	if !strings.Contains(alreadyMsg, "其它教学班") {
		t.Fatalf("已选事件文案 = %q，期望说明选上的是别的教学班", alreadyMsg)
	}
}

// 不查已选课表的形态（这里是单教学班的候选）靠 flag=6 的反馈剔除：撞上"已选中"
// 的教学班记进过滤器，之后的轮次不再对它和同课程的兄弟班级提交。
func TestRunAutoDropsCourseOnAlreadyChosenFlag(t *testing.T) {
	const chooseAlreadyJSON = `{"flag":"6","msg":"该教学班已选中，刷新页面可见！"}`
	// 用 autoServerWithChoose：候选只有一个教学班，needChosenQuery 为 false，
	// 已选接口根本不会被请求，只能靠提交反馈。
	e, candidates := rejectTestEngine(t, chooseAlreadyJSON)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}

	var picks, already int
	var alreadyMsg string
	_, err := e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 3,
		Interval:  time.Millisecond,
		Refresh:   time.Hour,
	}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
		case EventAlreadyChosen:
			already++
			alreadyMsg = ev.Message
		}
	})
	if !errors.Is(err, ErrAllChosen) {
		t.Fatalf("err = %v，期望 ErrAllChosen（队列只剩已选课程时停止）", err)
	}
	if picks != 1 || already != 1 {
		t.Fatalf("pick/already = %d/%d，期望 1/1（之后的轮次不再提交已选课程）", picks, already)
	}
	if !strings.Contains(alreadyMsg, "不再重复尝试") {
		t.Fatalf("已选事件文案 = %q，期望说明不再重试", alreadyMsg)
	}
}

// 刷新时已选名单要跟着更新：用户在别的窗口手动选了课，队列要立即剔除。
func TestRunAutoReloadsChosenOnRefresh(t *testing.T) {
	var chosenBody atomic.Value
	chosenBody.Store(`[]`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cxZzxkYzbChoosedDisplay"):
			_, _ = io.WriteString(w, chosenBody.Load().(string))
		case strings.Contains(r.URL.Path, "zzxkyzbjk_xkBcZyZzxkYzb"):
			_, _ = io.WriteString(w, chooseFullJSON)
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(srv.Close)
	e := newCandidateEngine(t, srv, nil)
	e.CacheCourseList([]protocol.CourseListDic{
		courseListEntry("k1", "j1", "定向越野"),
		courseListEntry("k2", "j2", "乒乓球"),
	})
	e.CacheCourseDetail([]protocol.CourseDetail{
		{JxbId: "j1", DoJxbId: "d1", Jxbrl: "50", Yxzrs: "10"},
		{JxbId: "j2", DoJxbId: "d2", Jxbrl: "50", Yxzrs: "10"},
	})
	wants := protocol.WishList{WantClassList: []string{"定向越野", "乒乓球"}}
	candidates, err := e.CollectCandidates(context.Background(), wants)
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}

	// 模拟用户在别的窗口手动选课：第一轮把 d1、d2 各试了一次（满员失败），
	// 第二轮的刷新前 j1 已经选上，应从队列剔除。
	var pickCount int
	var pickIDs []string
	_, err = e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  80 * time.Millisecond,
		Refresh:   20 * time.Millisecond,
	}, staticSource(candidates), func(ev Event) {
		if ev.Kind != EventPick {
			return
		}
		pickCount++
		pickIDs = append(pickIDs, ev.Course.Do_jxb_id)
		if pickCount == 2 {
			// 第一轮结束（两个候选都试过一遍）后、第二轮刷新查询前，
			// 把 j1 标成已选。
			chosenBody.Store(`[{"jxb_id":"j1","kch_id":"k1"}]`)
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	// 第一轮试 d1、d2；刷新后 d1 已选被剔除，第二轮只剩 d2。
	if want := []string{"d1", "d2", "d2"}; !equalStrings(pickIDs, want) {
		t.Fatalf("提交序列 = %v，期望 %v（刷新后剔除手动选上的 d1）", pickIDs, want)
	}
}

// 一轮只提交优先级最前面的几个候选：通识选修命中几十个教学班时，
// 逐个提交整个队列会把选课接口打进限流。
func TestRunAutoCapsAttemptsPerRound(t *testing.T) {
	e, _ := rejectTestEngine(t, chooseFullJSON)
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}

	candidates := make([]protocol.CustomCourseDic, 0, 8)
	for i := range 8 {
		candidates = append(candidates, protocol.CustomCourseDic{
			Kch_id:    fmt.Sprintf("k%d", i),
			Jxb_id:    fmt.Sprintf("j%d", i),
			Jxbmc:     fmt.Sprintf("定向越野%d", i),
			Kcmc:      "定向越野",
			Do_jxb_id: fmt.Sprintf("d%d", i),
			Jxbrl:     "39",
			Yxzrs:     "39", // 全部满员，保证每轮都从头尝试整个队列
		})
	}

	var picks int
	_, err := e.RunAuto(context.Background(), Plan{
		Wants:     wants,
		MaxRounds: 2,
		Interval:  time.Millisecond,
		Refresh:   time.Hour,
	}, staticSource(candidates), func(ev Event) {
		if ev.Kind == EventPick {
			picks++
		}
	})
	if !errors.Is(err, ErrNotChosen) {
		t.Fatalf("err = %v，期望 ErrNotChosen", err)
	}
	if picks != 2*maxAttemptsPerRound {
		t.Fatalf("提交次数 = %d，期望 %d（两轮各 %d）", picks, 2*maxAttemptsPerRound, maxAttemptsPerRound)
	}
}

func TestSetWishIsCopiedAndSurvivesReset(t *testing.T) {
	eng := newTestEngine(t)
	wish := protocol.WishList{WantClassList: []string{"高数"}}
	eng.SetWish(wish)

	wish.WantClassList[0] = "被外部改掉"
	if got := eng.Wish().WantClassList[0]; got != "高数" {
		t.Fatalf("SetWish 存了外部切片: %q", got)
	}

	eng.Reset()
	if got := eng.Wish().WantClassList[0]; got != "高数" {
		t.Fatalf("Reset 丢了愿望清单: %q", got)
	}
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := New(validSettings("https://example.com/"), config.NewSession(), LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng
}
