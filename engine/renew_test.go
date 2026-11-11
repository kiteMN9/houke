package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// 正方在选课参数（xkkz_id / xkkz_xh）过期时的标准回应。
const paramExpiredJSON = `{"flag":"0","msg":"加密串错误，可以清除浏览器缓存后刷新网页重试！"}`

// 正常课程列表：一行一个教学班。
const validListJSON = `{"tmpList":[{"jxb_id":"j1","kch_id":"k1","kcmc":"定向越野",` +
	`"jxbmc":"定向越野-0001","yxzrs":"10"}]}`

// renewFixture 模拟教务的四个选课端点：
//   - 首页 / 列表前置：回可解析的页面，用来判断有没有重抓参数；
//   - 列表 / 详情 / 选课：按 listBodies / detailBodies / chooseBodies 依次回应，
//     越界后一直用最后一个，方便表达"第一次加密串错误，之后正常"。
type renewFixture struct {
	srv *httptest.Server

	indexHits  atomic.Int32
	preHits    atomic.Int32
	listCalls  atomic.Int32
	detailCall atomic.Int32
	chooseCall atomic.Int32

	listBodies   atomic.Value // []string
	detailBodies atomic.Value // []string
	chooseBodies atomic.Value // []string
}

func newRenewFixture(t *testing.T) *renewFixture {
	t.Helper()
	f := &renewFixture{}
	f.listBodies.Store([]string{paramExpiredJSON})
	f.detailBodies.Store([]string{paramExpiredJSON})
	f.chooseBodies.Store([]string{paramExpiredJSON})

	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 路径必须取 endpoints.go 里的常量：教务换过一次课程详情地址（多出 jk），
		// 这里写死字面量就会悄悄对不上，测试会以“详情返回空”这种难查的方式失败。
		switch {
		case strings.HasSuffix(r.URL.Path, config.ChooseCourseIndex):
			f.indexHits.Add(1)
			_, _ = io.WriteString(w, indexOKHTML)
		case strings.HasSuffix(r.URL.Path, config.ChooseCourseTabParams):
			f.preHits.Add(1)
			_, _ = io.WriteString(w, indexOKHTML)
		case strings.HasSuffix(r.URL.Path, config.ChooseCourseCourseList):
			_, _ = io.WriteString(w, nextBody(f.listBodies, f.listCalls.Add(1)))
		case strings.HasSuffix(r.URL.Path, config.ChooseCourseCourseDetail):
			_, _ = io.WriteString(w, nextBody(f.detailBodies, f.detailCall.Add(1)))
		case strings.HasSuffix(r.URL.Path, config.ChooseCourse):
			_, _ = io.WriteString(w, nextBody(f.chooseBodies, f.chooseCall.Add(1)))
		default:
			_, _ = io.WriteString(w, indexOKHTML)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// nextBody 取第 n 次请求该回的 body；超出序列长度时一直用最后一个。
func nextBody(holder atomic.Value, n int32) string {
	bodies := holder.Load().([]string)
	i := max(int(n)-1, 0)
	if i >= len(bodies) {
		i = len(bodies) - 1
	}
	return bodies[i]
}

// reset 清空计数：newCandidateEngine 里的 Initialize 本身就会打首页和前置接口。
func (f *renewFixture) reset() {
	f.indexHits.Store(0)
	f.preHits.Store(0)
	f.listCalls.Store(0)
	f.detailCall.Store(0)
	f.chooseCall.Store(0)
}

// 列表接口回“加密串错误”时，engine 应重抓首页参数后再试一次，把正常列表交给上层。
func TestGetCourseListRenewsParamsAfterParamExpiry(t *testing.T) {
	f := newRenewFixture(t)
	e := newCandidateEngine(t, f.srv, nil)
	f.reset()
	f.listBodies.Store([]string{paramExpiredJSON, validListJSON})

	list, err := e.GetCourseList(context.Background())
	if err != nil {
		t.Fatalf("GetCourseList: %v", err)
	}
	if len(list) != 1 || list[0].JxbId != "j1" {
		t.Fatalf("列表 = %+v，期望重试后拿到 j1", list)
	}
	if got := f.listCalls.Load(); got != 2 {
		t.Fatalf("列表请求 %d 次，期望 2（失败一次 + 重试一次）", got)
	}
	if f.indexHits.Load() != 1 || f.preHits.Load() != 1 {
		t.Fatalf("重抓参数 index/pre = %d/%d，期望各 1 次", f.indexHits.Load(), f.preHits.Load())
	}
}

// 详情接口的“加密串错误”能解析成 {msg,flag}，不会走 needInit 分支；
// engine 必须靠文案识别出来，否则自动选课的详情刷新会一直拿到空结果。
func TestGetCourseDetailRenewsParamsAfterParamExpiry(t *testing.T) {
	f := newRenewFixture(t)
	e := newCandidateEngine(t, f.srv, nil)
	f.reset()
	f.detailBodies.Store([]string{paramExpiredJSON, detailJSON})

	detail, err := e.GetCourseDetail(context.Background(), "k1")
	if err != nil {
		t.Fatalf("GetCourseDetail: %v", err)
	}
	if len(detail) != 1 || detail[0].DoJxbId != "d1" {
		t.Fatalf("详情 = %+v，期望重试后拿到 d1", detail)
	}
	if got := f.detailCall.Load(); got < 2 {
		t.Fatalf("详情请求 %d 次，期望至少 2（失败一次 + 重试）", got)
	}
	if f.indexHits.Load() != 1 {
		t.Fatalf("重抓首页参数 %d 次，期望 1", f.indexHits.Load())
	}
}

// 选课接口没有 needInit 通道，只能从返回文案看出参数过期；同样要续一次参数再提交。
func TestChooseRenewsParamsAfterParamExpiry(t *testing.T) {
	f := newRenewFixture(t)
	e := newCandidateEngine(t, f.srv, nil)
	f.reset()
	f.chooseBodies.Store([]string{paramExpiredJSON, `{"flag":"1","msg":"选课成功"}`})

	co := &protocol.CustomCourseDic{Kch_id: "k1", Do_jxb_id: "d1"}
	r, err := e.Choose(context.Background(), co)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if r.Flag != "1" {
		t.Fatalf("选课结果 = %+v，期望重试后选上", r)
	}
	if got := f.chooseCall.Load(); got != 2 {
		t.Fatalf("选课请求 %d 次，期望 2（失败一次 + 重试一次）", got)
	}
	if f.indexHits.Load() != 1 || f.preHits.Load() != 1 {
		t.Fatalf("重抓参数 index/pre = %d/%d，期望各 1 次", f.indexHits.Load(), f.preHits.Load())
	}
}

// 参数一直过期时必须冷静：不能每个请求都去抓一遍首页，否则会把提交节奏拖垮。
func TestRenewIsThrottledByCooldown(t *testing.T) {
	f := newRenewFixture(t)
	e := newCandidateEngine(t, f.srv, nil)
	f.reset()
	f.listBodies.Store([]string{paramExpiredJSON})

	if _, err := e.GetCourseList(context.Background()); err != nil {
		t.Fatalf("第一次 GetCourseList: %v", err)
	}
	if got := f.indexHits.Load(); got != 1 {
		t.Fatalf("第一次应重抓参数，首页请求 %d 次，期望 1", got)
	}

	// 第二次仍回加密串错误，但还在冷静期内：不再重抓首页。
	if _, err := e.GetCourseList(context.Background()); err != nil {
		t.Fatalf("第二次 GetCourseList: %v", err)
	}
	if got := f.indexHits.Load(); got != 1 {
		t.Fatalf("冷静期内又重抓了参数：首页请求 %d 次，期望还是 1", got)
	}
	if got := f.listCalls.Load(); got != 3 {
		t.Fatalf("列表请求 %d 次，期望 3（两次调用各一次 + 第一次的重试）", got)
	}
}

// 两组页签的首页：V1 是刷新前，V2 是刷新后（xkkz_id 全都换了新值）。
const indexTabsV1 = `<html><body>
<ul>
<li><a onclick="queryCourse(this,'1','id1','2025','07201')">特殊课程</a></li>
<li><a onclick="queryCourse(this,'10','id10','2025','07201')">通识选修课</a></li>
</ul>
<input id="firstXkkzId" value="id1"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

const indexTabsV2 = `<html><body>
<ul>
<li><a onclick="queryCourse(this,'1','id1new','2025','07201')">特殊课程</a></li>
<li><a onclick="queryCourse(this,'10','id10new','2025','07201')">通识选修课</a></li>
</ul>
<input id="firstXkkzId" value="id1new"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

// V3 撤掉了“通识选修课”页签：刷新参数时不能悄悄切到剩下的页签继续抢。
const indexTabsV3 = `<html><body>
<ul>
<li><a onclick="queryCourse(this,'1','id1new','2025','07201')">特殊课程</a></li>
</ul>
<input id="firstXkkzId" value="id1new"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

// newTabsSession 起一个首页 HTML 可替换的会话：用来验证重抓参数后的页签是否保住。
func newTabsSession(t *testing.T, body *atomic.Value, indexHits *atomic.Int32) *session {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, config.ChooseCourseIndex) {
			indexHits.Add(1)
			_, _ = io.WriteString(w, body.Load().(string))
			return
		}
		_, _ = io.WriteString(w, indexOKHTML)
	}))
	t.Cleanup(srv.Close)
	return newTestSession(t, srv.URL)
}

// 重抓首页参数会把页签重置成页面默认值；Renew 必须切回用户原来选中的页签，
// 并且用刷新后的 xkkz_id，否则通识选修的自动选课会悄悄跑到主修课上。
func TestSessionRenewKeepsSelectedTab(t *testing.T) {
	var body atomic.Value
	var hits atomic.Int32
	body.Store(indexTabsV1)
	s := newTabsSession(t, &body, &hits)

	if err := s.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.SelectTab(context.Background(), 1); err != nil {
		t.Fatalf("SelectTab: %v", err)
	}
	if got := s.ModeName(); got != "通识选修课" {
		t.Fatalf("ModeName = %q，期望 通识选修课", got)
	}
	s.CacheCourseList([]protocol.CourseListDic{{JxbId: "j1", KchId: "k1", Kcmc: "定向越野"}})
	s.SetWish(protocol.WishList{WantClassList: []string{"定向越野"}})

	body.Store(indexTabsV2)
	if err := s.Renew(context.Background()); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	if got := s.ModeName(); got != "通识选修课" {
		t.Fatalf("Renew 后 ModeName = %q，期望仍停在 通识选修课", got)
	}
	tab, ok := protocol.CurrentTabOf(s.snapshot())
	if !ok || tab.Kklxdm != "10" || tab.XkkzId != "id10new" {
		t.Fatalf("Renew 后页签 = %+v（ok=%v），期望 Kklxdm=10 / XkkzId=id10new", tab, ok)
	}
	if len(s.Courses()) != 1 {
		t.Fatalf("Renew 不该清课程缓存: %+v", s.Courses())
	}
	if got := s.Wish().WantClassList; len(got) != 1 || got[0] != "定向越野" {
		t.Fatalf("Renew 丢了愿望清单: %v", got)
	}
}

// 原页签在刷新后的页签列表里消失了：宁可报错、不发布新参数，
// 也不能让正在跑的自动选课换一套课程集合继续抢。
func TestSessionRenewFailsWhenTabGone(t *testing.T) {
	var body atomic.Value
	var hits atomic.Int32
	body.Store(indexTabsV1)
	s := newTabsSession(t, &body, &hits)

	if err := s.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.SelectTab(context.Background(), 1); err != nil {
		t.Fatalf("SelectTab: %v", err)
	}

	body.Store(indexTabsV3)
	if err := s.Renew(context.Background()); !errors.Is(err, errTabGone) {
		t.Fatalf("err = %v，期望 errTabGone", err)
	}
	if got := s.ModeName(); got != "通识选修课" {
		t.Fatalf("Renew 失败后 ModeName = %q，期望保持原页签不变", got)
	}
}
