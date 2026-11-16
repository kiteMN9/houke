package engine

import (
	"context"
	"errors"
	"fmt"
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
	// GetCourseDetail 解析成功就返回，每门课一次请求；这里用次数确认"只抓了这一门"。
	if hits.Load() != 1 {
		t.Fatalf("详情请求数 = %d，期望 1（每门课一次请求）", hits.Load())
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
	if hits.Load() != 1 {
		t.Fatalf("详情请求数 = %d，期望 1（只请求命中的 1 门）", hits.Load())
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
	if hits.Load() != 1 {
		t.Fatalf("详情请求数 = %d，期望 1（每门课一次请求）", hits.Load())
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
	if hits.Load() != 1 {
		t.Fatalf("详情请求数 = %d，期望 1（教师愿望必须为每门课抓详情）", hits.Load())
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
	for _, course := range got.Courses {
		if course.Do_jxb_id == "d2" {
			hasD2 = true
		}
	}
	if !hasD2 {
		t.Fatalf("刷新后的候选里没有新教学班 d2: %+v", got.Courses)
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

// 名字优先：第一批只抓课程名命中的课，命中就立即交出去，不把整页详情拖到
// 提交之前；下一批接着整页扫（教师愿望只能在详情里命中），续扫不重拉列表。
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
	if detailHits.Load() != 1 {
		t.Fatalf("首次详情请求 = %d，期望 1（只抓名字命中的 k1）", detailHits.Load())
	}
	if len(got.Courses) != 1 || got.Courses[0].Kch_id != "k1" {
		t.Fatalf("首批候选 = %+v，期望只有 k1", got.Courses)
	}
	if !got.More {
		t.Fatal("首批 More = false，期望 true（整页还没扫完）")
	}

	// 第二批接着扫：不重拉列表，只补 k2 的详情（k1 已有 Do_jxb_id）。
	got, err = source(context.Background())
	if err != nil {
		t.Fatalf("第二批 source: %v", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("续扫列表请求 = %d，期望仍是 1（一遍扫描只拉一次列表）", listHits.Load())
	}
	if detailHits.Load() != 2 {
		t.Fatalf("第二批详情请求 = %d，期望 2（补 k2）", detailHits.Load())
	}
	if len(got.Courses) != 2 {
		t.Fatalf("第二批候选 = %+v，期望 k1 + 教师命中的 k2", got.Courses)
	}
	if got.More {
		t.Fatal("整页扫完后 More 仍为 true")
	}
}

// 名字一门都没命中时不能只返回空：同一批里接着整页扫，扫到第一个教师 / 类型
// 命中的课就交出去，剩下的下一批再扫。
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
	// 点名课没命中：同一批里整页扫，扫到教师命中的 k1 就先交出去，k2 留到下一批。
	if detailHits.Load() != 1 {
		t.Fatalf("详情请求 = %d，期望 1（扫到第一个能提交的就先交）", detailHits.Load())
	}
	if len(got.Courses) != 1 || got.Courses[0].Kch_id != "k1" {
		t.Fatalf("候选 = %+v，期望教师命中的 k1", got.Courses)
	}
	if !got.More {
		t.Fatal("More = false，期望 true（k2 还没扫）")
	}

	// 第二批扫完剩下的：k2 的教师不命中，不凑进候选，也不重拉列表。
	got, err = source(context.Background())
	if err != nil {
		t.Fatalf("第二批 source: %v", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("续扫列表请求 = %d，期望仍是 1", listHits.Load())
	}
	if detailHits.Load() != 2 {
		t.Fatalf("第二批详情请求 = %d，期望 2（补 k2）", detailHits.Load())
	}
	if len(got.Courses) != 1 || got.Courses[0].Kch_id != "k1" {
		t.Fatalf("第二批候选 = %+v，期望仍只有 k1", got.Courses)
	}
	if got.More {
		t.Fatal("整页扫完后 More 仍为 true")
	}
}

// 点名的课全满时也要在同一轮整页探索：名字优先只是"快速目标"，不是"只要它"。
// 通识选修按教师 / 类型挑课，点名课满员就该去试教师命中的其它课程号，而不是
// 守着几门满员的课空转到下个刷新周期。
func TestCandidateSourceExploresWhenNamedCoursesFull(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"定向越野","jxbmc":"定向越野-0001","yxzrs":"50"},` +
		`{"kch_id":"k2","jxb_id":"j2","kcmc":"西方美术鉴赏","jxbmc":"西方美术鉴赏-0001","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, map[string]string{
		"k1": `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"50","jsxx":"440016/张三/教授"}]`,
		"k2": `[{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"50","yxzrs":"10","jsxx":"440015/殷海华/副教授"}]`,
	})
	e := newCandidateEngine(t, srv, nil)
	listHits.Store(0)
	source := e.CandidateSource(protocol.WishList{
		WantClassList:   []string{"定向越野"},
		WantTeacherList: []string{"殷海华"},
	})

	got, err := source(context.Background())
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("列表请求 = %d，期望 1", listHits.Load())
	}
	if detailHits.Load() != 2 {
		t.Fatalf("详情请求 = %d，期望 2（点名课满员要接着整页扫）", detailHits.Load())
	}
	if len(got.Courses) != 2 {
		t.Fatalf("候选 = %+v，期望满员的点名课与教师命中的备选都在队列里", got.Courses)
	}
	var hasNamed, hasFallback bool
	for _, course := range got.Courses {
		switch course.Kch_id {
		case "k1":
			hasNamed = true
		case "k2":
			hasFallback = true
		}
	}
	if !hasNamed || !hasFallback {
		t.Fatalf("候选 = %+v，期望 k1（点名，满员）与 k2（教师命中）", got.Courses)
	}
}

// 板块课（整个页签只有一个课程号）走另一套逻辑：只看教学班名 / 教师，不看
// 课程类型；一次详情补齐全部教学班，没有"名字优先"和"提前收手"。
func TestCandidateSourceBlockCourseMatchesClassNames(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"体育Ⅳ","jxbmc":"足球13","kzmc":"体育","yxzrs":"10"},` +
		`{"kch_id":"k1","jxb_id":"j2","kcmc":"体育Ⅳ","jxbmc":"瑜伽4","kzmc":"体育","yxzrs":"10"},` +
		`{"kch_id":"k1","jxb_id":"j3","kcmc":"体育Ⅳ","jxbmc":"舞龙4","kzmc":"体育","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, map[string]string{
		"k1": `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"10","jsxx":"440015/裴如意/副教授"},` +
			`{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"50","yxzrs":"10","jsxx":"440015/裴如意/副教授"},` +
			`{"jxb_id":"j3","do_jxb_id":"d3","jxbrl":"50","yxzrs":"10","jsxx":"440016/张三/教授"}]`,
	})
	e := newCandidateEngine(t, srv, nil)
	listHits.Store(0)

	// 类型填的是别的类别，也照样命中：板块课的类型没有区分度。
	source := e.CandidateSource(protocol.WishList{
		WantClassList: []string{"足球13", "瑜伽4"},
		WantTypeList:  []string{"艺术类"},
	})
	got, err := source(context.Background())
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if detailHits.Load() != 1 {
		t.Fatalf("详情请求 = %d，期望 1（板块课整页只有一个课程号）", detailHits.Load())
	}
	if len(got.Courses) != 2 {
		t.Fatalf("候选 = %+v，期望足球13 与瑜伽4", got.Courses)
	}
	for _, course := range got.Courses {
		if course.Jxbmc == "舞龙4" {
			t.Fatalf("候选 = %+v，不该收没点名的教学班", got.Courses)
		}
	}
	if got.More {
		t.Fatal("板块课一遍就扫完，More 应为 false")
	}

	// 教师愿望在板块课里同样有效（教师只在详情里，粗筛不能挡）。
	got, err = e.CandidateSource(protocol.WishList{WantTeacherList: []string{"张三"}})(context.Background())
	if err != nil {
		t.Fatalf("教师 source: %v", err)
	}
	if len(got.Courses) != 1 || got.Courses[0].Do_jxb_id != "d3" {
		t.Fatalf("候选 = %+v，期望只有张三教的舞龙4", got.Courses)
	}
}

// 通识选修课整页几十个课程号、每个课程号都要一次详情：候选源一次只扫到
// "下一个命中且有名额"的课程号，扫到就交出去，不等整页抓完；下一批从停下的
// 地方接着扫，扫过的课程号不再重复请求详情，也不重拉列表。
func TestCandidateSourceStopsAtFirstVacancy(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"艺术一","jxbmc":"艺术一-0001","kzmc":"艺术类","yxzrs":"50"},` +
		`{"kch_id":"k2","jxb_id":"j2","kcmc":"艺术二","jxbmc":"艺术二-0001","kzmc":"艺术类","yxzrs":"10"},` +
		`{"kch_id":"k3","jxb_id":"j3","kcmc":"艺术三","jxbmc":"艺术三-0001","kzmc":"艺术类","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, map[string]string{
		"k1": `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"50"}]`,
		"k2": `[{"jxb_id":"j2","do_jxb_id":"d2","jxbrl":"50","yxzrs":"10"}]`,
		"k3": `[{"jxb_id":"j3","do_jxb_id":"d3","jxbrl":"50","yxzrs":"10"}]`,
	})
	e := newCandidateEngine(t, srv, nil)
	listHits.Store(0)
	source := e.CandidateSource(protocol.WishList{WantTypeList: []string{"艺术类"}})

	// 首批：k1 满员继续扫，扫到 k2 有名额就收手，k3 的详情留到下一批。
	got, err := source(context.Background())
	if err != nil {
		t.Fatalf("首次 source: %v", err)
	}
	if detailHits.Load() != 2 {
		t.Fatalf("首次详情请求 = %d，期望 2（扫到 k2 就收手，不抓 k3）", detailHits.Load())
	}
	if len(got.Courses) != 2 || got.Courses[0].Kch_id != "k1" || got.Courses[1].Kch_id != "k2" {
		t.Fatalf("首批候选 = %+v，期望 k1（满员）+ k2（有名额）", got.Courses)
	}
	if !got.More {
		t.Fatal("首批 More = false，期望 true（k3 还没扫）")
	}

	// 第二批：接着扫 k3；k1、k2 已扫过，不再请求详情，也不重拉列表。
	got, err = source(context.Background())
	if err != nil {
		t.Fatalf("第二批 source: %v", err)
	}
	if listHits.Load() != 1 {
		t.Fatalf("续扫列表请求 = %d，期望仍是 1（一遍扫描只拉一次列表）", listHits.Load())
	}
	if detailHits.Load() != 3 {
		t.Fatalf("第二批详情请求 = %d，期望 3（只补 k3）", detailHits.Load())
	}
	if len(got.Courses) != 3 {
		t.Fatalf("第二批候选 = %+v，期望 k1、k2、k3 都在队列里", got.Courses)
	}
	if got.More {
		t.Fatal("整页扫完后 More 仍为 true")
	}

	// 新的一遍：重拉列表，详情全都缓存着，不再产生详情请求。
	got, err = source(context.Background())
	if err != nil {
		t.Fatalf("第三批 source: %v", err)
	}
	if listHits.Load() != 2 {
		t.Fatalf("列表请求 = %d，期望 2（扫完一遍才拉新列表）", listHits.Load())
	}
	if detailHits.Load() != 3 {
		t.Fatalf("第三批详情请求 = %d，期望 3（扫过的课程号不重复请求）", detailHits.Load())
	}
	if len(got.Courses) != 3 {
		t.Fatalf("第三批候选 = %+v，期望仍是 3 个", got.Courses)
	}
}

// 扫描是"一批批往下走"的：每批只前进到下一个有名额的课程号，提交不成就接着扫，
// 不必等一个刷新周期；整页扫完（More=false）之后才重拉列表。旧实现"刷新必须
// 整页扫完"会让第一批提交等到几十个详情抓完——用户要的是命中就选。
func TestCandidateSourceScansInBatches(t *testing.T) {
	var listBody atomic.Value
	listBody.Store(`{"tmpList":[` +
		`{"kch_id":"k1","jxb_id":"j1","kcmc":"艺术一","jxbmc":"艺术一-0001","kzmc":"艺术类","yxzrs":"50"},` +
		`{"kch_id":"k2","jxb_id":"j2","kcmc":"艺术二","jxbmc":"艺术二-0001","kzmc":"艺术类","yxzrs":"10"},` +
		`{"kch_id":"k3","jxb_id":"j3","kcmc":"艺术三","jxbmc":"艺术三-0001","kzmc":"艺术类","yxzrs":"10"},` +
		`{"kch_id":"k4","jxb_id":"j4","kcmc":"艺术四","jxbmc":"艺术四-0001","kzmc":"艺术类","yxzrs":"10"},` +
		`{"kch_id":"k5","jxb_id":"j5","kcmc":"艺术五","jxbmc":"艺术五-0001","kzmc":"艺术类","yxzrs":"10"},` +
		`{"kch_id":"k6","jxb_id":"j6","kcmc":"艺术六","jxbmc":"艺术六-0001","kzmc":"艺术类","yxzrs":"10"}]}`)
	var listHits, detailHits atomic.Int32
	details := map[string]string{}
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("k%d", i)
		if i == 1 {
			details[id] = `[{"jxb_id":"j1","do_jxb_id":"d1","jxbrl":"50","yxzrs":"50"}]`
		} else {
			details[id] = fmt.Sprintf(`[{"jxb_id":"j%d","do_jxb_id":"d%d","jxbrl":"50","yxzrs":"10"}]`, i, i)
		}
	}
	srv := candidateSourceServer(t, &listBody, &listHits, &detailHits, details)
	e := newCandidateEngine(t, srv, nil)
	listHits.Store(0)
	source := e.CandidateSource(protocol.WishList{WantTypeList: []string{"艺术类"}})

	// 每批只前进一个课程号（k1 满员，所以首批是 k1+k2），直到整页扫完。
	batches := 0
	got, err := source(context.Background())
	for {
		if err != nil {
			t.Fatalf("第 %d 批 source: %v", batches+1, err)
		}
		batches++
		if batches > 6 {
			t.Fatalf("扫了 %d 批还没扫完：%+v", batches, got)
		}
		if !got.More {
			break
		}
		got, err = source(context.Background())
	}
	if batches != 5 {
		t.Fatalf("批次 = %d，期望 5（k2..k6 各一批，扫完即止）", batches)
	}
	if listHits.Load() != 1 {
		t.Fatalf("列表请求 = %d，期望 1（一遍扫描只拉一次列表）", listHits.Load())
	}
	if detailHits.Load() != 6 {
		t.Fatalf("详情请求 = %d，期望 6（整页课程号各一次）", detailHits.Load())
	}
	if len(got.Courses) != 6 {
		t.Fatalf("扫完后的候选 = %d 个，期望 6（整页命中都进队列）", len(got.Courses))
	}

	// 扫完一遍后再要：重拉列表、不再抓详情，队列覆盖整页。
	got, err = source(context.Background())
	if err != nil {
		t.Fatalf("新一遍 source: %v", err)
	}
	if listHits.Load() != 2 {
		t.Fatalf("列表请求 = %d，期望 2（扫完一遍才拉新列表）", listHits.Load())
	}
	if detailHits.Load() != 6 {
		t.Fatalf("详情请求 = %d，期望 6（缓存已够用）", detailHits.Load())
	}
	if len(got.Courses) != 6 || got.More {
		t.Fatalf("新一遍候选 = %d 个（More=%v），期望 6 个且扫完", len(got.Courses), got.More)
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

	got := courseNumberGroups(wants, list, protocol.MatchGeneral, false)
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
	byName := courseNumberGroups(wants, list, protocol.MatchGeneral, true)
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
	if detailHits.Load() != 2 {
		t.Fatalf("详情请求 = %d，期望 2（两个课程号各一次详情）", detailHits.Load())
	}
	if len(got) != 1 || got[0].Kch_id != "k2" {
		t.Fatalf("候选 = %+v，期望只有教师命中的 k2", got)
	}
}
