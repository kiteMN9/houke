package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

	got := candidatesByPriority(wants, list)
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

	got := candidatesByPriority(wants, list)
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

	got := candidatesByPriority(wants, list)
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

	got := candidatesByPriority(wants, list)
	if want := []string{"cross", "yoga"}; !equalStrings(ids(got), want) {
		t.Fatalf("候选 = %v，期望 %v（定向越野 是一志愿，体育Ⅰ 是二志愿）", ids(got), want)
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
	source := func(context.Context) ([]protocol.CustomCourseDic, error) {
		calls++
		eng.Reset()
		return candidates, nil
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
	}, func(context.Context) ([]protocol.CustomCourseDic, error) {
		return nil, ErrSessionReset
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
func staticSource(list []protocol.CustomCourseDic) Source {
	return func(context.Context) ([]protocol.CustomCourseDic, error) { return list, nil }
}

// 满员时教务系统返回的是 flag=0 + 提示语，不是 HTTP 错误，也不是"没这门课"。
const chooseFullJSON = `{"flag":"0","msg":"该教学班人数已满"}`

// autoServer 在 detailServer 的基础上再伪造选课接口，用来跑 RunAuto 的整条链路。
func autoServer(t *testing.T, detail string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cxJxbWithKchZzxkYzb"):
			_, _ = io.WriteString(w, detail)
		case strings.Contains(r.URL.Path, "zzxkyzbjk_xkBcZyZzxkYzb"):
			_, _ = io.WriteString(w, chooseFullJSON)
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
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
	var pickMsg string
	_, err = e.RunAuto(context.Background(), Plan{Wants: wants, MaxRounds: 1, Interval: time.Millisecond}, staticSource(candidates), func(ev Event) {
		switch ev.Kind {
		case EventPick:
			picks++
			pickMsg = ev.Message
		case EventFailed:
			fails++
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
	source := func(context.Context) ([]protocol.CustomCourseDic, error) {
		calls++
		if calls == 1 {
			return fullOnly, nil
		}
		return withRoom, nil
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

// 刷新失败（教务抖动、列表被挡）不能把队列清空：本轮继续用上一次的名单提交。
func TestRunAutoKeepsQueueWhenRefreshFails(t *testing.T) {
	srv := autoServer(t, detailJSON)
	e := newCandidateEngine(t, srv, nil)

	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	only := []protocol.CustomCourseDic{
		{Jxbmc: "定向越野1", Jxbrl: "39", Yxzrs: "39", Do_jxb_id: "full"},
	}
	var calls int
	source := func(context.Context) ([]protocol.CustomCourseDic, error) {
		calls++
		if calls == 1 {
			return only, nil
		}
		return nil, errors.New("列表查询被拒绝")
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
	source := func(context.Context) ([]protocol.CustomCourseDic, error) {
		calls++
		if calls == 1 {
			return only, nil
		}
		// 详情没拿到 Do_jxb_id 的条目进不了队列。
		return []protocol.CustomCourseDic{{Jxbmc: "定向越野1"}}, nil
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
