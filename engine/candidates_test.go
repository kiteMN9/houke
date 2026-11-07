package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"school_sdk/protocol"
	"strings"
	"sync/atomic"
	"testing"
)

// 详情接口返回该课程下所有教学班；Do_jxb_id 是提交选课的唯一凭据。
const detailJSON = `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"10",` +
	`"jsxx":"440015/裴如意/副教授","sksj":"星期二第1-2节{2-13周}"}]`

// detailServer 返回初始化页面；详情路径单独返回教学班 JSON。
func detailServer(t *testing.T, detail string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cxJxbWithKchZzxkYzb") {
			if hits != nil {
				hits.Add(1)
			}
			_, _ = io.WriteString(w, detail)
			return
		}
		_, _ = io.WriteString(w, indexOKHTML)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newCandidateEngine 起一个已初始化、登录态的 Engine，并预置课程列表缓存。
func newCandidateEngine(t *testing.T, srv *httptest.Server, list []protocol.CourseListDic) *Engine {
	t.Helper()
	s := newTestSession(t, srv.URL)
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	e := &Engine{client: s.client, session: s, settings: validSettings(srv.URL)}
	if len(list) > 0 {
		e.CacheCourseList(list)
	}
	return e
}

func courseListEntry(kchID, jxbID, name string) protocol.CourseListDic {
	return protocol.CourseListDic{
		KchId: kchID,
		JxbId: jxbID,
		Kcmc:  name,
		Jxbmc: name + "-0001",
		Kzmc:  "必修",
		Yxzrs: "10",
	}
}

// 列表里的教学班没有 Do_jxb_id，必须先抓详情才能当候选。
func TestCollectCandidatesFetchesDetails(t *testing.T) {
	var hits atomic.Int32
	srv := detailServer(t, detailJSON, &hits)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "高等数学")})

	got, err := e.CollectCandidates(context.Background(), protocol.WishList{WantClassList: []string{"高等数学"}})
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	// 既有实现里 GetCourseDetail 解析成功后要再进一轮循环才返回，所以每门课
	// 固定两次请求；这里用次数确认"只抓了这一门"，不把它当成需要修的行为。
	if hits.Load() != 2 {
		t.Fatalf("详情请求数 = %d，期望 2（每门课两轮）", hits.Load())
	}
	if len(got) != 1 {
		t.Fatalf("候选数 = %d，期望 1", len(got))
	}
	if got[0].Do_jxb_id != "d1" {
		t.Fatalf("Do_jxb_id = %q，期望 d1", got[0].Do_jxb_id)
	}
}

// 课程名不匹配的课程不应该产生详情请求：整张课表几十门，全抓一遍太慢。
func TestCollectCandidatesSkipsUnwantedCourses(t *testing.T) {
	var hits atomic.Int32
	srv := detailServer(t, detailJSON, &hits)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{
		courseListEntry("k1", "j1", "高等数学"),
		courseListEntry("k2", "j2", "大学英语"),
	})

	got, err := e.CollectCandidates(context.Background(), protocol.WishList{WantClassList: []string{"高等数学"}})
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("详情请求数 = %d，期望 2（只请求命中的 1 门）", hits.Load())
	}
	if len(got) != 1 || got[0].Kch_id != "k1" {
		t.Fatalf("候选 = %+v，期望只有 k1", got)
	}
}

// 只填教师时列表里还没有 Jsxx，粗筛必须放行，详情回来后再精确过滤。
func TestCollectCandidatesTeacherOnly(t *testing.T) {
	var hits atomic.Int32
	srv := detailServer(t, detailJSON, &hits)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "高等数学")})

	got, err := e.CollectCandidates(context.Background(), protocol.WishList{WantTeacherList: []string{"裴如意"}})
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("详情请求数 = %d，期望 2（每门课两轮）", hits.Load())
	}
	if len(got) != 1 || got[0].Do_jxb_id != "d1" {
		t.Fatalf("候选 = %+v，期望教师命中的 k1", got)
	}
}

// 课程名不命中、但教师命中的课也必须能当候选：教师只在详情里，不能因为
// 名字不匹配就跳过详情，否则"课程名 + 教师"的愿望永远报没有命中。
// 真实案例：通识选修课列表里全是"公共艺术"类课程，愿望写"定向越野 / 乒乓球"
// 加教师"殷海华"，殷老师教的《西方美术鉴赏》就卡在这条粗筛后面。
func TestCollectCandidatesTeacherMatchesWhenCourseNameMisses(t *testing.T) {
	var hits atomic.Int32
	srv := detailServer(t, detailJSON, &hits)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "西方美术鉴赏")})

	got, err := e.CollectCandidates(context.Background(), protocol.WishList{
		WantClassList:   []string{"定向越野", "乒乓球"},
		WantTeacherList: []string{"超星尔雅", "裴如意"},
	})
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("详情请求数 = %d，期望 2（教师愿望必须为每门课抓详情）", hits.Load())
	}
	if len(got) != 1 || got[0].Kch_id != "k1" {
		t.Fatalf("候选 = %+v，期望教师命中的 k1", got)
	}
}

// 未初始化时没有选课参数，直接报错，避免发一堆注定失败的请求。
func TestCollectCandidatesRequiresInit(t *testing.T) {
	srv := detailServer(t, detailJSON, nil)
	s := newTestSession(t, srv.URL)
	e := &Engine{client: s.client, session: s, settings: validSettings(srv.URL)}

	_, err := e.CollectCandidates(context.Background(), protocol.WishList{WantClassList: []string{"高等数学"}})
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("err = %v，期望 ErrNotInitialized", err)
	}
}

// 愿望清单为空时不猜用户想要什么，直接拒绝。
func TestCollectCandidatesRejectsEmptyWish(t *testing.T) {
	srv := detailServer(t, detailJSON, nil)
	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "高等数学")})

	_, err := e.CollectCandidates(context.Background(), protocol.WishList{})
	if !errors.Is(err, ErrNoWantedCourse) {
		t.Fatalf("err = %v，期望 ErrNoWantedCourse", err)
	}
}

// CandidateSource 是自动选课的刷新入口：每次调用都重拉列表，新人数和新开的
// 教学班立刻进缓存；列表"成功但没数据"时报 ErrNoCandidates，但旧缓存必须留着，
// 让 RunAuto 继续用上一次的队列。
func TestCandidateSourceRefreshesListEveryTime(t *testing.T) {
	var listHits atomic.Int32
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cxZzxkYzbPartDisplay"):
			listHits.Add(1)
			_, _ = io.WriteString(w, listBody.Load().(string))
		case strings.Contains(r.URL.Path, "cxJxbWithKchZzxkYzb"):
			_, _ = io.WriteString(w, `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"10"},`+
				`{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"40","yxzrs":"5"}]`)
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(srv.Close)

	e := newCandidateEngine(t, srv, []protocol.CourseListDic{courseListEntry("k1", "j1", "定向越野1")})
	wants := protocol.WishList{WantClassList: []string{"定向越野"}}
	listHits.Store(0)

	// 开跑前的 CollectCandidates 吃缓存，不该碰列表接口。
	if _, err := e.CollectCandidates(context.Background(), wants); err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if listHits.Load() != 0 {
		t.Fatalf("CollectCandidates 请求列表 %d 次，期望 0（缓存已够用）", listHits.Load())
	}

	source := e.CandidateSource(wants)
	if _, err := source(context.Background()); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("空列表 err = %v，期望 ErrNoCandidates", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("刷新请求列表 %d 次，期望 1", listHits.Load())
	}
	if len(e.Courses()) != 1 {
		t.Fatalf("空列表响应不该清掉旧缓存: %+v", e.Courses())
	}

	// 列表里冒出新教学班：刷新后必须进缓存，并且能直接当候选提交。
	listBody.Store(`{"tmpList":[{"kch_id":"k2","jxb_id":"j2","kcmc":"定向越野2",` +
		`"jxbmc":"定向越野2-0001","yxzrs":"5"}]}`)
	got, err := source(context.Background())
	if err != nil {
		t.Fatalf("刷新: %v", err)
	}
	if listHits.Load() != 2 {
		t.Fatalf("刷新请求列表 %d 次，期望 2", listHits.Load())
	}
	hasD2 := false
	for _, course := range got {
		if course.Do_jxb_id == "d2" {
			hasD2 = true
		}
	}
	if !hasD2 {
		t.Fatalf("刷新后的候选里没有新教学班 d2: %+v", got)
	}
}

// candidateSourceServer 同时处理课程列表与详情：列表读 listBody，详情按表单里的
// kch_id 返回对应教学班，用来统计"名字优先"实际抓了几门课的详情。
func candidateSourceServer(t *testing.T, listBody *atomic.Value, listHits, detailHits *atomic.Int32, details map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cxZzxkYzbPartDisplay"):
			listHits.Add(1)
			_, _ = io.WriteString(w, listBody.Load().(string))
		case strings.Contains(r.URL.Path, "cxJxbWithKchZzxkYzb"):
			detailHits.Add(1)
			_ = r.ParseForm()
			body := details[r.PostFormValue("kch_id")]
			if body == "" {
				body = "[]"
			}
			_, _ = io.WriteString(w, body)
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 名字优先：第一次收集只抓课程名命中的课，命中就立即返回，不把整页详情拖到
// 提交之前；教师愿望等整表扫描留到刷新时再做。
func TestCandidateSourceNameFirst(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"定向越野","jxbmc":"定向越野-0001","yxzrs":"10"},` +
		`{"kch_id":"k2","jxb_id":"j2","kcmc":"篮球","jxbmc":"篮球-0001","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, map[string]string{
		"k1": `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"10","jsxx":"440015/裴如意/副教授"}]`,
		"k2": `[{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"50","yxzrs":"10","jsxx":"440015/裴如意/副教授"}]`,
	})
	e := newCandidateEngine(t, srv, nil)
	listHits.Store(0)
	source := e.CandidateSource(protocol.WishList{
		WantClassList:   []string{"定向越野"},
		WantTeacherList: []string{"裴如意"},
	})

	got, err := source(context.Background())
	if err != nil {
		t.Fatalf("首次 source: %v", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("首次列表请求 = %d，期望 1", listHits.Load())
	}
	if detailHits.Load() != 2 {
		t.Fatalf("首次详情请求 = %d，期望 2（只抓名字命中的 k1）", detailHits.Load())
	}
	if len(got) != 1 || got[0].Kch_id != "k1" {
		t.Fatalf("首次候选 = %+v，期望只有 k1", got)
	}

	// 第二次调用回到整表扫描：k1 已有 Do_jxb_id，只补 k2 的详情。
	got, err = source(context.Background())
	if err != nil {
		t.Fatalf("第二次 source: %v", err)
	}
	if listHits.Load() != 2 {
		t.Fatalf("第二次列表请求 = %d，期望 2", listHits.Load())
	}
	if detailHits.Load() != 4 {
		t.Fatalf("第二次详情请求 = %d，期望 4（补 k2）", detailHits.Load())
	}
	if len(got) != 2 {
		t.Fatalf("第二次候选 = %+v，期望 k1 + 教师命中的 k2", got)
	}
}

// 名字一门都没命中时不能只返回空：第一次调用在刷新完列表后立刻回落到整表
// 扫描，教师/类型命中的课仍要能进候选，而且列表只请求一次。
func TestCandidateSourceNameFirstFallsBackToFullScan(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"篮球","jxbmc":"篮球-0001","yxzrs":"10"},` +
		`{"kch_id":"k2","jxb_id":"j2","kcmc":"排球","jxbmc":"排球-0001","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, map[string]string{
		"k1": `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"10","jsxx":"440015/裴如意/副教授"}]`,
		"k2": `[{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"50","yxzrs":"10","jsxx":"440016/张三/教授"}]`,
	})
	e := newCandidateEngine(t, srv, nil)
	listHits.Store(0)
	source := e.CandidateSource(protocol.WishList{
		WantClassList:   []string{"定向越野"},
		WantTeacherList: []string{"裴如意"},
	})

	got, err := source(context.Background())
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("列表请求 = %d，期望 1（回落扫描复用刚刷新的列表）", listHits.Load())
	}
	if detailHits.Load() != 4 {
		t.Fatalf("详情请求 = %d，期望 4（整表扫描两门课）", detailHits.Load())
	}
	if len(got) != 1 || got[0].Kch_id != "k1" {
		t.Fatalf("候选 = %+v，期望教师命中的 k1", got)
	}
}

// 课程号是补详情的单位：一个课程号只抓一次详情，下面挂着的教学班一起补齐；
// 名字没命中（或名字优先时名字没命中）的课程号整组跳过。
func TestCourseNumberGroups(t *testing.T) {
	wants := protocol.WishList{WantClassList: []string{"定向越野", "乒乓球"}}
	list := []protocol.CustomCourseDic{
		{Kch_id: "k2", Jxbmc: "乒乓球-0001", Kcmc: "乒乓球", Do_jxb_id: "d2"},
		{Kch_id: "k1", Jxbmc: "定向越野-0001", Kcmc: "定向越野"},
		{Kch_id: "k1", Jxbmc: "定向越野-0002", Kcmc: "定向越野"},
		{Kch_id: "k3", Jxbmc: "篮球-0001", Kcmc: "篮球"},
	}

	got := courseNumberGroups(wants, list, false)
	want := []courseNumber{
		{kchID: "k1", rank: 0, needsDetail: true},
		{kchID: "k2", rank: 1, needsDetail: false},
	}
	if len(got) != len(want) {
		t.Fatalf("课程号分组 = %+v，期望 %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个课程号 = %+v，期望 %+v", i, got[i], want[i])
		}
	}

	// 名字优先只保留课程名命中的课程号。
	byName := courseNumberGroups(wants, list, true)
	if len(byName) != 2 || byName[0].kchID != "k1" || byName[1].kchID != "k2" {
		t.Fatalf("名字优先的课程号 = %+v，期望 k1、k2", byName)
	}
}

// 某个课程号下的教学班一个都没命中愿望时整组跳过：它的详情照样抓（教师只在
// 详情里），但不会拿没命中的教学班凑候选，也不会挡住后面的课程号。
func TestCollectCandidatesSkipsCourseNumberWithoutMatch(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"高等数学","jxbmc":"高等数学-0001","yxzrs":"10"},` +
		`{"kch_id":"k2","jxb_id":"j2","kcmc":"大学英语","jxbmc":"大学英语-0001","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, map[string]string{
		"k1": `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"10","jsxx":"440016/张三/教授"}]`,
		"k2": `[{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"50","yxzrs":"10","jsxx":"440015/裴如意/副教授"}]`,
	})
	e := newCandidateEngine(t, srv, nil)
	got, err := e.CollectCandidates(context.Background(), protocol.WishList{WantTeacherList: []string{"裴如意"}})
	if err != nil {
		t.Fatalf("CollectCandidates: %v", err)
	}
	if detailHits.Load() != 4 {
		t.Fatalf("详情请求 = %d，期望 4（两个课程号各一次，每次 2 个 POST）", detailHits.Load())
	}
	if len(got) != 1 || got[0].Kch_id != "k2" {
		t.Fatalf("候选 = %+v，期望只有教师命中的 k2", got)
	}
}
