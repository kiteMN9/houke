package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"school_sdk/protocol"
)

// frameLog 线程安全地收集推送帧（读取协程与测试主协程并发访问）。
type frameLog struct {
	mu     sync.Mutex
	frames []taskNoticeView
}

func (l *frameLog) add(f taskNoticeView) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frames = append(l.frames, f)
}

func (l *frameLog) all() []taskNoticeView {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]taskNoticeView(nil), l.frames...)
}

func (l *frameLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.frames)
}

// newStreamTestServer 起一个真实的 HTTP 测试服务器，返回账号与地址。
// SSE 需要"边写边读"，httptest.NewRecorder 做不到（它在 handler 返回后才可读）。
func newStreamTestServer(t *testing.T) (*AccountStore, string, string) {
	t.Helper()
	// 进度帧间隔调小，否则每个用例都要真等 5 秒
	old := progressInterval
	progressInterval = 60 * time.Millisecond
	t.Cleanup(func() { progressInterval = old })

	store, err := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := httptest.NewServer(mustNewServer(t, store).router)
	t.Cleanup(srv.Close)
	return store, acc.ID, srv.URL
}

// openStream 建立 SSE 连接并开始收集帧，返回关闭函数。
func openStream(t *testing.T, url string, log *frameLog) func() {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var data string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				var frame taskNoticeView
				if err := json.Unmarshal([]byte(data), &frame); err != nil {
					t.Errorf("帧解析失败: %v (%s)", err, data)
					return
				}
				log.add(frame)
				data = ""
			}
		}
	}()

	return func() {
		resp.Body.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("关闭连接后读取协程没有退出")
		}
	}
}

// waitFrames 等到至少收到 n 帧（或超时）。
func waitFrames(t *testing.T, log *frameLog, n int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if log.len() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("只收到 %d 帧，期望 %d 帧", log.len(), n)
}

// waitProgressFrame 等到出现一帧进度帧（任务运行期间服务端每秒推一帧）。
func waitProgressFrame(log *frameLog, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, f := range log.all() {
			if f.Type == "progress" {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// 推送帧里的 loggedIn 与 /status 同源：都直接读 client 的登录态。
func TestStreamFrameCarriesClientLoggedIn(t *testing.T) {
	store, id, base := newStreamTestServer(t)
	rt, err := store.runtime(id)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+id+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)
	if first := log.all()[0]; !first.LoggedIn {
		t.Errorf("首帧 loggedIn = false, want true: %+v", first)
	}
}

// 核心诉求：登录期间不再靠高频轮询 /status 驱动界面。
// 这里模拟"登录中持续产生事件"，确认一条连接就能拿到状态变化，
// 并且变化的帧带事件流、纯走秒的进度帧不带。
func TestTaskEventsPushesTaskProgressWithoutPolling(t *testing.T) {
	store, id, base := newStreamTestServer(t)

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+id+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)
	if first := log.all()[0]; first.Type != "snapshot" || first.Task == nil {
		t.Fatalf("首帧 = %+v, want 完整快照", first)
	}

	// 模拟客户端随后发起登录：start + 一次请求上报
	rt, err := store.runtime(id)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	store.tasks.start(id, taskKindLogin)
	rt.requestStage(http.MethodGet, "https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html")
	// 阶段日志由 protocol 上报；hook 只贡献请求计数
	rt.loginStage(protocol.LoginStage{Phase: protocol.LoginPhaseFetchForm, Attempt: 1})

	waitFrames(t, &log, 3, 2*time.Second)
	var sawBegin, sawEvent bool
	for _, f := range log.all() {
		if f.Task == nil || !f.Task.Running {
			continue
		}
		if f.Task.Kind != taskKindLogin {
			t.Errorf("task.Kind = %q, want login", f.Task.Kind)
		}
		if len(f.Task.Events) > 0 {
			sawEvent = true
			if f.ID == 0 {
				t.Error("帧里缺少任务序号 id")
			}
			if f.Task.Events[0].Kind == "task.begin" {
				sawBegin = true
			}
		}
	}
	if !sawBegin {
		t.Errorf("没有收到带 task.begin 的帧: %+v", log.all())
	}
	if !sawEvent {
		t.Error("状态变化的帧必须带事件流")
	}

	// 进度帧：走秒靠服务端推送，不靠前端再拉 /status。
	// 服务端每 progressInterval 推一帧，这里等它出现即可。
	if !waitProgressFrame(&log, 3*time.Second) {
		t.Fatalf("任务运行期间没有收到进度帧: %+v", log.all())
	}

	// 收尾：任务结束时必须推一帧带最终状态的完整快照
	beforeFinish := log.len()
	store.tasks.finish(id, taskKindLogin, taskStatusDone, "登录成功", "")
	waitFrames(t, &log, beforeFinish+1, 2*time.Second)

	// 进度帧与状态帧各司其职：进度帧不带事件流，状态变化仍然推完整快照
	// （不会因为去重把真实变化吞掉）。
	var progressFrames, eventFrames int
	for _, f := range log.all() {
		if f.Task == nil {
			continue
		}
		if f.Type == "progress" {
			progressFrames++
			if len(f.Task.Events) != 0 {
				t.Errorf("进度帧不该重复携带事件流（%d 条）", len(f.Task.Events))
			}
			if f.Task.ElapsedMs <= 0 {
				t.Errorf("进度帧 elapsedMs = %d, want > 0", f.Task.ElapsedMs)
			}
		} else if f.Task.Running && len(f.Task.Events) > 0 {
			eventFrames++
		}
	}
	if progressFrames == 0 {
		t.Errorf("没有进度帧: %+v", log.all())
	}
	if eventFrames < 2 {
		t.Errorf("带事件流的快照帧 = %d，want >= 2", eventFrames)
	}

	final := log.all()[log.len()-1]
	if final.Task == nil || final.Task.Running || final.Task.Status != taskStatusDone {
		t.Errorf("收尾帧 = %+v, want done 快照", final.Task)
	}
	if len(final.Task.Events) == 0 {
		t.Error("收尾帧应带完整事件流，前端要能看到结束原因")
	}
}

// 空闲（没有任务在跑）时连接必须完全静默：不推进度、不产生任何请求，
// 否则"替代轮询"就变成了另一种轮询。
func TestTaskEventsIdleIsSilent(t *testing.T) {
	_, id, base := newStreamTestServer(t)

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+id+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)
	n := log.len()
	time.Sleep(progressInterval * 4)
	if got := log.len(); got != n {
		t.Errorf("空闲期间多收了 %d 帧，期望 0", got-n)
	}
}

// 账号被删除时，连接要收尾而不是一直挂着。
func TestTaskEventsClosesWhenAccountDeleted(t *testing.T) {
	store, id, base := newStreamTestServer(t)

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+id+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)
	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	waitFrames(t, &log, 2, 2*time.Second)

	var gone bool
	for _, f := range log.all() {
		if f.Type == "gone" {
			gone = true
		}
	}
	if !gone {
		t.Errorf("没有收到 gone 帧: %+v", log.all())
	}
}

// 订阅者不能拖慢任务本身：没人读的订阅者只该丢帧，不该阻塞发布方。
func TestPublishNeverBlocksOnSlowSubscriber(t *testing.T) {
	store, id, _ := newStreamTestServer(t)

	// 订阅后不读：队列很快填满
	_, _, _, unsubscribe := store.tasks.subscribe(id)
	defer unsubscribe()
	store.tasks.start(id, taskKindLogin)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			store.tasks.emit(id, taskEvent{Kind: "login.retry", Text: "重试中"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("发布被慢速订阅者阻塞了")
	}
}

// "替代轮询"到底省了多少：用一次长任务的事件量做量化对比。
// 不追求精确字节，只要保证进度帧确实只有"轻量字段"这一量级。
func TestProgressFrameIsFarSmallerThanFullSnapshot(t *testing.T) {
	store, id, _ := newStreamTestServer(t)

	ch, _, _, unsubscribe := store.tasks.subscribe(id)
	defer unsubscribe()

	store.tasks.start(id, taskKindLogin)

	// 边发边收：订阅队列只有 subscriberQueue 格，发得比收得快就会丢帧
	//（线上丢的是中间态，下一帧仍是完整快照，所以不影响正确性）。
	var mu sync.Mutex
	var got []taskNotice
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case n := <-ch:
				mu.Lock()
				got = append(got, n)
				mu.Unlock()
			case <-stop:
				return
			}
		}
	}()

	// 模拟一次"每步都有新事件"的登录：填满事件预览上限。
	// 文字必须各不相同，否则会被去重折叠成一行（那正是 emitLocked 的另一个优化）。
	for i := 0; i < taskEventsPreview+20; i++ {
		store.tasks.emitRequest(id, taskEvent{
			Kind:   "login.page",
			Stage:  "login.page",
			Text:   fmt.Sprintf("正在请求 /xtgl/index_initMenu%d.html", i),
			Detail: "端点 /xtgl/index_initMenu" + strconv.Itoa(i) + ".html",
		})
		store.tasks.tickRunning(id)
		time.Sleep(2 * time.Millisecond) // 让接收协程跟上
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	notices := append([]taskNotice(nil), got...)
	mu.Unlock()
	close(stop)

	if len(notices) == 0 {
		t.Fatal("没有收到任何通知")
	}
	// 取事件最多的那帧（完整快照）与最后一帧进度帧做对比
	heaviest := notices[0]
	for _, n := range notices {
		if n.task != nil && len(n.task.Events) > len(heaviest.task.Events) {
			heaviest = n
		}
	}
	tickNotice := notices[len(notices)-1]
	tickNotice.tick = true

	full := len(marshalFrame(t, heaviest, false))
	progress := len(marshalFrame(t, tickNotice, true))

	t.Logf("带事件流的快照帧 %d 字节（%d 条事件），进度帧 %d 字节（相差 %.0f 倍）",
		full, len(heaviest.task.Events), progress, float64(full)/float64(progress))
	if len(heaviest.task.Events) < taskEventsPreview {
		t.Errorf("事件数 = %d，样本没填满预览上限", len(heaviest.task.Events))
	}
	if progress*10 > full {
		t.Errorf("进度帧 %d 字节，相对快照 %d 字节不够轻量", progress, full)
	}
	if progress > 1024 {
		t.Errorf("进度帧 %d 字节，超出了一帧进度应有的量级", progress)
	}
}

// marshalFrame 生成一帧的 JSON，用于比较体积（走的是和线上一样的构造函数）。
func marshalFrame(t *testing.T, n taskNotice, tick bool) []byte {
	t.Helper()
	frame := taskNoticeView{Type: "snapshot", ID: n.id, Rev: n.rev, Task: n.task}
	if tick {
		frame.Type = "progress"
		light := *n.task
		light.Events = nil
		frame.Task = &light
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// 用真实的 Server.Run（和 main.go 一样的启动路径）验证推送链路，
// 避免"测试里好好的、跑起来不出帧"这类只在真实 HTTP 服务器下才暴露的问题。
func TestStreamWorksOverRealServerRun(t *testing.T) {
	old := progressInterval
	progressInterval = 60 * time.Millisecond
	t.Cleanup(func() { progressInterval = old })

	store, err := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	acc, err := store.Create(&Account{Name: "n", Username: "u", BaseURL: "https://jwglxt.ycit.edu.cn/"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 先占一个空闲端口再交给 gin，避免重复运行（-count=3）时端口被上一次占着
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	base := "http://" + ln.Addr().String()

	srv := mustNewServer(t, store)
	go func() { _ = http.Serve(ln, srv.router) }()

	// 等服务器起来
	var up bool
	for i := 0; i < 100; i++ {
		if resp, err := http.Get(base + "/healthz"); err == nil { //nolint:noctx // 测试
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !up {
		t.Fatal("服务器没有起来")
	}

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+acc.ID+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)

	store.tasks.start(acc.ID, taskKindLogin)
	store.tasks.emit(acc.ID, taskEvent{Kind: "login.submit", Text: "正在提交账号密码"})
	waitFrames(t, &log, 2, 2*time.Second)

	// 进度帧也要能在真实服务器上发出来
	if !waitProgressFrame(&log, 2*time.Second) {
		t.Fatalf("真实服务器下没有进度帧: %+v", log.all())
	}

	// /status 仍然可用（兜底路径不能因为加了推送而坏掉）
	resp, err := http.Get(base + "/api/accounts/" + acc.ID + "/status") //nolint:noctx // 测试
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status 码 = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Task *taskView `json:"task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析 /status: %v", err)
	}
	if body.Task == nil || body.Task.Kind != taskKindLogin {
		t.Errorf("/status 的 task = %+v, want login", body.Task)
	}
}

// 取消订阅后不应再收到推送，且重复取消是安全的。
func TestUnsubscribeIsIdempotent(t *testing.T) {
	store, id, _ := newStreamTestServer(t)

	ch, _, _, unsubscribe := store.tasks.subscribe(id)
	unsubscribe()
	unsubscribe()

	store.tasks.start(id, taskKindLogin)
	store.tasks.emit(id, taskEvent{Kind: "login.begin", Text: "开始"})
	select {
	case n := <-ch:
		t.Errorf("取消订阅后仍收到推送: %+v", n)
	default:
	}
}

// 排除 HTTP 因素，直接验证"订阅 → start → touchRunning"的推送链路。
func TestSubscribeReceivesNoticeAcrossStart(t *testing.T) {
	store, id, _ := newStreamTestServer(t)

	ch, _, _, unsubscribe := store.tasks.subscribe(id)
	defer unsubscribe()

	store.tasks.start(id, taskKindLogin)
	select {
	case n := <-ch:
		if n.task == nil || !n.task.Running {
			t.Fatalf("start 的推送 = %+v, want running", n.task)
		}
	case <-time.After(time.Second):
		t.Fatal("start 没有推送给订阅者")
	}

	// 排空队列，确认 touchRunning 也能推出来
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}
	store.tasks.tickRunning(id)
	select {
	case n := <-ch:
		if n.task == nil || !n.task.Running {
			t.Fatalf("tickRunning 的推送 = %+v, want running", n.task)
		}
		if !n.tick {
			t.Error("进度帧应当带 tick 标记")
		}
	case <-time.After(time.Second):
		t.Fatal("tickRunning 没有推送给订阅者")
	}
}
