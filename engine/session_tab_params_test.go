package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// indexCacheTabsHTML 是两个页签的选课首页。
//
// firstXkkzId 指向第一个页签，和真实教务一致（parseYzbIndexHtml 用它抓默认
// 页签的参数页），这样初始化缓存的就是 tab0 的参数。
const indexCacheTabsHTML = `<html><body>
<ul>
<li><a onclick="queryCourse(this,'1','id1','2025','07201')">tab0</a></li>
<li><a onclick="queryCourse(this,'2','id2','2025','07201')">tab1</a></li>
</ul>
<input id="firstXkkzId" value="id1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

// tabParamsHTML 返回一张 parseTabParamsHtml 能解析出参数的参数页。
func tabParamsHTML(xklc, xkkssj, xkjssj string) string {
	return `<html><body>
<input type="hidden" id="xklc" name="xklc" value="` + xklc + `"/>
<input type="hidden" id="rwlx" name="rwlx" value="1"/>
<input type="hidden" id="xkly" name="xkly" value="1"/>
<input type="hidden" id="xkkssj" name="xkkssj" value="` + xkkssj + `"/>
<input type="hidden" id="xkjssj" name="xkjssj" value="` + xkjssj + `"/>
</body></html>`
}

// tabParamsFixture 记录参数页请求次数，并按 xkkz_id 回不同的参数页：
// 页签 1（id1）和页签 2（id2）的轮次与时间窗刻意不同，便于断言拿到的是哪一份。
type tabParamsFixture struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newTabParamsFixture(t *testing.T) *tabParamsFixture {
	t.Helper()
	f := &tabParamsFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, config.ChooseCourseTabParams) {
			f.hits.Add(1)
			if r.PostFormValue("xkkz_id") == "id2" {
				_, _ = io.WriteString(w, tabParamsHTML("2", "2026-10-01 08:00:00", "2026-10-10 23:59:59"))
				return
			}
			_, _ = io.WriteString(w, tabParamsHTML("1", "2026-09-01 08:00:00", "2026-09-10 23:59:59"))
			return
		}
		_, _ = io.WriteString(w, indexCacheTabsHTML)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// 切回访问过的页签直接复用缓存：参数页只在"第一次进入该页签"时请求。
func TestSelectTabReusesCachedTabParams(t *testing.T) {
	f := newTabParamsFixture(t)
	s := newTestSession(t, f.srv.URL)
	ctx := context.Background()

	if err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := f.hits.Load(); got != 1 {
		t.Fatalf("初始化后参数页请求 %d 次，期望 1", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:00" {
		t.Fatalf("初始化窗口 = %+v", w)
	}

	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1): %v", err)
	}
	if got := f.hits.Load(); got != 2 {
		t.Fatalf("第一次进入页签 1 应抓参数，参数页请求 %d 次，期望 2", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-10-01 08:00:00" || w.Xkjssj != "2026-10-10 23:59:59" {
		t.Fatalf("页签 1 窗口 = %+v", w)
	}

	gen := s.Generation()
	if err := s.SelectTab(ctx, 0); err != nil {
		t.Fatalf("SelectTab(0): %v", err)
	}
	if got := f.hits.Load(); got != 2 {
		t.Fatalf("切回初始化过的页签 0 不该重抓参数，参数页请求 %d 次，期望还是 2", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:00" {
		t.Fatalf("缓存命中的页签 0 窗口 = %+v", w)
	}
	if got := s.ModeName(); got != "tab0" {
		t.Fatalf("ModeName = %q，期望 tab0", got)
	}

	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1) 第二次: %v", err)
	}
	if got := f.hits.Load(); got != 2 {
		t.Fatalf("再次切到页签 1 不该重抓参数，参数页请求 %d 次，期望还是 2", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-10-01 08:00:00" {
		t.Fatalf("缓存命中的页签 1 窗口 = %+v", w)
	}
	if got := s.Generation(); got != gen+2 {
		t.Fatalf("两次成功切换后 generation = %d，期望 %d（缓存命中也要递增）", got, gen+2)
	}
}

// Reset（登出 / 强制初始化）必须让页签缓存整体失效，旧会话的参数不能带进新会话。
func TestResetDropsTabParamsCache(t *testing.T) {
	f := newTabParamsFixture(t)
	s := newTestSession(t, f.srv.URL)
	ctx := context.Background()

	if err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1): %v", err)
	}
	if got := f.hits.Load(); got != 2 {
		t.Fatalf("Reset 前参数页请求 %d 次，期望 2", got)
	}

	s.Reset()
	if err := s.Initialize(ctx); err != nil {
		t.Fatalf("Reset 后 Initialize: %v", err)
	}
	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("Reset 后 SelectTab(1): %v", err)
	}
	if got := f.hits.Load(); got != 4 {
		t.Fatalf("Reset 后进入页签 1 应重抓参数，参数页请求 %d 次，期望 4", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-10-01 08:00:00" {
		t.Fatalf("Reset 后页签 1 窗口 = %+v", w)
	}
}

// Renew 会换新 xkkz_id：旧页签参数全部作废，切回另一个页签要重新抓。
func TestRenewDropsTabParamsCache(t *testing.T) {
	f := newTabParamsFixture(t)
	s := newTestSession(t, f.srv.URL)
	ctx := context.Background()

	if err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1): %v", err)
	}
	if err := s.Renew(ctx); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if got := f.hits.Load(); got != 3 {
		t.Fatalf("Renew 应重抓当前页签参数，参数页请求 %d 次，期望 3", got)
	}

	if err := s.SelectTab(ctx, 0); err != nil {
		t.Fatalf("Renew 后 SelectTab(0): %v", err)
	}
	if got := f.hits.Load(); got != 4 {
		t.Fatalf("Renew 后旧缓存应失效，参数页请求 %d 次，期望 4", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:00" {
		t.Fatalf("Renew 后页签 0 窗口 = %+v", w)
	}
}

// 参数页解析失败（关键字段全空）的结果不能进缓存，否则这个页签会一直用空参数，
// 用户再点一次页签也救不回来。
func TestSelectTabDoesNotCacheUnparsableParams(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, config.ChooseCourseTabParams) {
			hits.Add(1)
			if r.PostFormValue("xkkz_id") == "id2" {
				_, _ = io.WriteString(w, `<html><body></body></html>`) // 坏页：没有选课限制参数
				return
			}
			_, _ = io.WriteString(w, tabParamsHTML("1", "2026-09-01 08:00:00", "2026-09-10 23:59:59"))
			return
		}
		_, _ = io.WriteString(w, indexCacheTabsHTML)
	}))
	t.Cleanup(srv.Close)

	s := newTestSession(t, srv.URL)
	ctx := context.Background()
	if err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1): %v", err)
	}
	if w := s.Window(); w != (protocol.WindowInfo{}) {
		t.Fatalf("坏参数页后窗口应为空，实际 %+v", w)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("参数页请求 %d 次，期望 2", got)
	}

	if err := s.SelectTab(ctx, 0); err != nil {
		t.Fatalf("SelectTab(0): %v", err)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:00" {
		t.Fatalf("页签 0 应该命中初始化缓存，窗口 = %+v", w)
	}

	// 坏结果没有缓存：再切回页签 1 必须重新抓，而不是复用空参数。
	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("再次 SelectTab(1): %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("坏参数页不该进缓存：参数页请求 %d 次，期望 3", got)
	}
}

// 再点当前页签 = 刷新：绕过缓存重抓一次，并把新参数写回缓存。
func TestSelectTabRefreshesCurrentTab(t *testing.T) {
	var tab0Hits atomic.Int32 // 页签 0（id1）的参数页请求次数，用来生成不同的时间窗
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, config.ChooseCourseTabParams) {
			switch r.PostFormValue("xkkz_id") {
			case "id2":
				_, _ = io.WriteString(w, tabParamsHTML("2", "2026-10-01 08:00:00", "2026-10-10 23:59:59"))
			default:
				n := tab0Hits.Add(1)
				_, _ = io.WriteString(w, tabParamsHTML("1",
					fmt.Sprintf("2026-09-01 08:00:%02d", n), "2026-09-10 23:59:59"))
			}
			return
		}
		_, _ = io.WriteString(w, indexCacheTabsHTML)
	}))
	t.Cleanup(srv.Close)

	s := newTestSession(t, srv.URL)
	ctx := context.Background()
	if err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := tab0Hits.Load(); got != 1 {
		t.Fatalf("初始化后页签 0 参数页请求 %d 次，期望 1", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:01" {
		t.Fatalf("初始化窗口 = %+v", w)
	}

	// 当前就在页签 0：再点一次应强制重抓，而不是命中缓存。
	if err := s.SelectTab(ctx, 0); err != nil {
		t.Fatalf("SelectTab(0) 刷新: %v", err)
	}
	if got := tab0Hits.Load(); got != 2 {
		t.Fatalf("再点当前页签应重抓参数，请求 %d 次，期望 2", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:02" {
		t.Fatalf("刷新后的窗口 = %+v", w)
	}

	// 刷新结果要写回缓存：切走再切回来不该再请求，拿到的是刷新后的那份参数。
	if err := s.SelectTab(ctx, 1); err != nil {
		t.Fatalf("SelectTab(1): %v", err)
	}
	if err := s.SelectTab(ctx, 0); err != nil {
		t.Fatalf("SelectTab(0) 切回: %v", err)
	}
	if got := tab0Hits.Load(); got != 2 {
		t.Fatalf("切回页签 0 应命中缓存，请求 %d 次，期望还是 2", got)
	}
	if w := s.Window(); w.Xkkssj != "2026-09-01 08:00:02" {
		t.Fatalf("缓存命中的窗口 = %+v，期望刷新后的参数", w)
	}
}
