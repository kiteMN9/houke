package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// indexOKHTML 是 GetIndexParams 能「成功」的最小页面：
// 没有 value="账号"、也没有 id="tips"，UserIsLogin 会视为已登录；
// 带 firstXkkzId/firstXkkzXh，parseYzbIndexHtml 不会把它当成 Step 1 失败。
const indexOKHTML = `<html><body>
<input id="firstXkkzId" value="10"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

// indexWithTabsHTML 在 indexOKHTML 的基础上带两个页签，供 SelectTab 用。
const indexWithTabsHTML = `<html><body>
<ul>
<li><a onclick="queryCourse(this,'1','id1','2025','07201')">tab0</a></li>
<li><a onclick="queryCourse(this,'2','id2','2025','07201')">tab1</a></li>
</ul>
<input id="firstXkkzId" value="10"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

// blockingIndexServer 模拟"初始化正卡在学校接口上"的窗口：
// 第一个请求到达后挂住，直到 release() 或客户端取消。
type blockingIndexServer struct {
	srv     *httptest.Server
	arrived chan struct{}
	release func()
}

func newBlockingIndexServer(t *testing.T) *blockingIndexServer {
	t.Helper()
	arrived := make(chan struct{}, 4)
	releaseCh := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCh) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		select {
		case <-releaseCh:
		case <-r.Context().Done():
			return // 客户端取消后别把 handler 永久挂住
		}
		_, _ = io.WriteString(w, indexOKHTML)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(release) // LIFO：先 release 再 Close，避免 Close 等 handler

	return &blockingIndexServer{srv: srv, arrived: arrived, release: release}
}

func (b *blockingIndexServer) waitArrived(t *testing.T) {
	t.Helper()
	select {
	case <-b.arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("初始化没有发出请求")
	}
}

// /status 依赖的三个只读接口在初始化卡住时必须立即返回。
// 回归点：以前会话锁被 Initialize 跨网络调用持有，读一次 Inited
// 就要等到登录超时（最长 10 分钟）。
func TestReadsDoNotBlockDuringInitialize(t *testing.T) {
	b := newBlockingIndexServer(t)
	s := newTestSession(t, b.srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Initialize(ctx) }()
	b.waitArrived(t)

	start := time.Now()
	inited := s.Inited()
	mode := s.ModeName()
	tabs := s.Tabs()
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("初始化期间读取会话状态被阻塞了 %v", d)
	}
	if inited {
		t.Error("初始化尚未完成，Inited 应为 false")
	}
	if mode != "" {
		t.Errorf("初始化尚未完成，ModeName 应为空，得到 %q", mode)
	}
	if len(tabs) != 0 {
		t.Errorf("初始化尚未完成，Tabs 应为空，得到 %v", tabs)
	}

	b.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Initialize: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("释放请求后 Initialize 仍未返回")
	}
	if !s.Inited() {
		t.Fatal("初始化完成后 Inited 应为 true")
	}
}

// 初始化期间登出（Reset）不能被网络请求挡住，并且那次初始化结果要作废。
func TestResetDuringInitializeDoesNotBlock(t *testing.T) {
	b := newBlockingIndexServer(t)
	s := newTestSession(t, b.srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Initialize(ctx) }()
	b.waitArrived(t)

	start := time.Now()
	s.Reset()
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("Reset 被初始化阻塞了 %v", d)
	}

	b.release()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSessionReset) {
			t.Fatalf("初始化结果应因 Reset 失效，得到 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Initialize 未返回")
	}
	if s.Inited() {
		t.Fatal("Reset 之后不应处于已初始化状态")
	}
}

// 同一会话并发 Initialize 只应该真正抓一次参数，其余调用复用结果。
func TestConcurrentInitializeRunsOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, indexOKHTML)
	}))
	defer srv.Close()

	s := newTestSession(t, srv.URL)

	const n = 4
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- s.Initialize(context.Background())
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("并发的 Initialize 出错: %v", err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("初始化应只抓一次参数（index + listPre，共 2 个请求），实际 %d 个", got)
	}
}

// 切页签同样要"抓完再发布"：成功后 ModeName/Tabs 反映新页签。
func TestSelectTabPublishesNewSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, indexWithTabsHTML)
	}))
	defer srv.Close()

	s := newTestSession(t, srv.URL)
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := len(s.Tabs()); got != 2 {
		t.Fatalf("Tabs = %d，期望 2", got)
	}

	before := s.Generation()
	if err := s.SelectTab(context.Background(), 1); err != nil {
		t.Fatalf("SelectTab: %v", err)
	}
	if got := s.Generation(); got != before+1 {
		t.Fatalf("成功切换页签后 generation = %d，期望 %d", got, before+1)
	}
	if got := s.ModeName(); got != "tab1" {
		t.Fatalf("ModeName = %q，期望 tab1", got)
	}
	if got := s.Tabs()[0].Kklxmc; got != "tab0" {
		t.Fatalf("切页签后 Tabs 被改坏: %q", got)
	}
	if err := s.SelectTab(context.Background(), 5); !errors.Is(err, errBadTab) {
		t.Fatalf("越界下标应返回 errBadTab，得到 %v", err)
	}
	if got := s.Generation(); got != before+1 {
		t.Fatalf("失败的页签切换不该递增 generation：%d", got)
	}
}

func TestSessionWishSurvivesReset(t *testing.T) {
	s := newSession(nil)
	s.SetWish(protocol.WishList{WantClassList: []string{"高数"}})

	if got := s.Wish().WantClassList; len(got) != 1 || got[0] != "高数" {
		t.Fatalf("Wish 丢了刚设置的清单: %v", got)
	}

	s.Reset()

	if s.Inited() {
		t.Fatal("Reset 后不应仍处于已初始化状态")
	}
	if got := s.Wish().WantClassList; len(got) != 1 || got[0] != "高数" {
		t.Fatalf("Reset 丢了愿望清单: %v", got)
	}
}

func TestSessionWishIsIsolated(t *testing.T) {
	s := newSession(nil)
	wish := protocol.WishList{WantTeacherList: []string{"陈爱华"}}
	s.SetWish(wish)

	wish.WantTeacherList[0] = "被外部改掉"
	if got := s.Wish().WantTeacherList[0]; got != "陈爱华" {
		t.Fatalf("SetWish 存了外部切片: %q", got)
	}

	got := s.Wish()
	got.WantTeacherList[0] = "读回来改掉"
	if again := s.Wish().WantTeacherList[0]; again != "陈爱华" {
		t.Fatalf("Wish 返回了内部切片: %q", again)
	}
}

// 选课时间窗属于发布后的快照：初始化成功才能读到，Reset 后清空。
func TestSessionWindowFollowsInitialize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == config.ChooseCourseTabParams {
			_, _ = io.WriteString(w, `<html><body>
<input type="hidden" name="xkkssj" id="xkkssj" value="2026-09-09 12:30:00"/>
<input type="hidden" name="xkjssj" id="xkjssj" value="2026-09-23 11:00:00"/>
</body></html>`)
			return
		}
		_, _ = io.WriteString(w, indexOKHTML)
	}))
	t.Cleanup(srv.Close)

	s := newTestSession(t, srv.URL)
	if got := s.Window(); got != (protocol.WindowInfo{}) {
		t.Fatalf("初始化前 Window = %+v，期望零值", got)
	}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := s.Window(); got != (protocol.WindowInfo{
		Xkkssj: "2026-09-09 12:30:00",
		Xkjssj: "2026-09-23 11:00:00",
	}) {
		t.Fatalf("初始化后 Window = %+v", got)
	}

	s.Reset()
	if got := s.Window(); got != (protocol.WindowInfo{}) {
		t.Fatalf("Reset 后 Window = %+v，期望零值", got)
	}
}

// newTestSession 构造一个已登录的会话，指向 httptest 服务器。
func newTestSession(t *testing.T, baseURL string) *session {
	t.Helper()
	cfg := config.Settings{
		BaseURL:   baseURL,
		Account:   "u",
		Password:  "p",
		UserAgent: "Edg/142.0.0.0",
		Timeout:   config.Duration(5 * time.Second),
	}
	cli, err := protocol.NewAPIClient(cfg, config.NewSession(), false, false, "")
	if err != nil {
		t.Fatalf("NewAPIClient: %v", err)
	}
	cli.TestSetLoggedIn(true)
	return newSession(cli)
}
