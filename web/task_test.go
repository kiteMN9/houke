package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"school_sdk/protocol"
)

func TestTaskStoreLifecycle(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"

	if _, ok := ts.start(id, taskKindLogin); !ok {
		t.Fatal("首次 start 应当成功")
	}
	// 同一账号重复 start 应当失败，并返回正在跑的那个任务
	old, ok := ts.start(id, taskKindLogin)
	if ok {
		t.Fatal("重复 start 应当失败")
	}
	if old == nil || !old.running {
		t.Fatal("重复 start 应返回正在运行的任务")
	}

	ts.emit(id, taskEvent{Kind: "login.page", Stage: "login.page", Text: "正在获取登录页"})
	ts.setCancel(id, taskKindLogin, func() {})
	ts.finish(id, taskKindLogin, taskStatusDone, "登录成功", "")

	snap := ts.snapshot(id)
	if snap == nil {
		t.Fatal("snapshot 不应为 nil")
	}
	if snap.Running {
		t.Error("任务结束后 running 应为 false")
	}
	if snap.Status != taskStatusDone {
		t.Errorf("Status = %q, want %q", snap.Status, taskStatusDone)
	}
	if snap.StageText != "登录成功" {
		t.Errorf("StageText = %q, want 登录成功", snap.StageText)
	}
	if snap.ElapsedMs < 0 {
		t.Errorf("ElapsedMs = %d, 不应为负", snap.ElapsedMs)
	}
	// 开始 + 阶段 + 收尾
	if len(snap.Events) != 3 {
		t.Fatalf("事件数 = %d, want 3: %+v", len(snap.Events), snap.Events)
	}
	// 事件时间必须单调不减，否则前端会显示负的时间差
	for i := 1; i < len(snap.Events); i++ {
		if snap.Events[i].AtMs < snap.Events[i-1].AtMs {
			t.Errorf("事件时间倒退: %+v -> %+v", snap.Events[i-1], snap.Events[i])
		}
	}
}

// 任务结束后仍能读到快照（刷新页面要能看到"上次结果"）。
func TestTaskStoreSnapshotSurvivesFinish(t *testing.T) {
	ts := newTaskStore()
	ts.start("a", taskKindInit)
	ts.finish("a", taskKindInit, taskStatusCancelled, "初始化已取消", "")

	snap := ts.snapshot("a")
	if snap == nil || snap.Running {
		t.Fatal("应当读到已结束的任务快照")
	}
	if snap.Status != taskStatusCancelled {
		t.Errorf("Status = %q, want %q", snap.Status, taskStatusCancelled)
	}
	last := snap.Events[len(snap.Events)-1]
	if !strings.Contains(last.Text, "已取消") {
		t.Errorf("最后一条事件 = %q, want 含「已取消」", last.Text)
	}
}

// 连续重复的同一条事件应折叠成一行并累加次数，避免日志刷屏。
func TestTaskStoreCollapsesRepeats(t *testing.T) {
	ts := newTaskStore()
	ts.start("a", taskKindInit)
	for range 20 {
		ts.emit("a", taskEvent{Kind: "init.index", Stage: "init.index", Text: "正在打开选课页"})
	}
	snap := ts.snapshot("a")
	if len(snap.Events) != 2 { // task.begin + 折叠后的 init.index
		t.Fatalf("事件数 = %d, want 2: %+v", len(snap.Events), snap.Events)
	}
	last := snap.Events[1]
	if last.Text != "正在打开选课页" {
		t.Errorf("最后一条事件 = %q", last.Text)
	}
	if last.Repeat != 20 {
		t.Errorf("Repeat = %d, want 20", last.Repeat)
	}
	// 折叠后时间戳仍要前进，界面才能显示"还在动"
	if last.AtMs < 0 {
		t.Errorf("AtMs = %d", last.AtMs)
	}
}

// 只有"完全连续相同"才折叠；交替出现的内容不能被吞掉。
func TestTaskStoreDoesNotCollapseAlternating(t *testing.T) {
	ts := newTaskStore()
	ts.start("a", taskKindInit)
	for range 3 {
		ts.emit("a", taskEvent{Kind: "k", Stage: "k", Text: "A"})
		ts.emit("a", taskEvent{Kind: "k", Stage: "k", Text: "B"})
	}
	snap := ts.snapshot("a")
	// task.begin + A B A B A B
	if len(snap.Events) != 7 {
		t.Fatalf("事件数 = %d, want 7: %+v", len(snap.Events), snap.Events)
	}
	for i, want := range []string{"初始化已开始", "A", "B", "A", "B", "A", "B"} {
		if snap.Events[i].Text != want {
			t.Errorf("事件[%d] = %q, want %q", i, snap.Events[i].Text, want)
		}
	}
}

// 未知请求只更新阶段与计数，不应写日志。
func TestTouchStageUpdatesStageWithoutLog(t *testing.T) {
	ts := newTaskStore()
	ts.start("a", taskKindInit)
	for range 10 {
		ts.touchStage("a", "http:/poll.html", "正在请求 /poll.html")
	}
	snap := ts.snapshot("a")
	if len(snap.Events) != 1 {
		t.Fatalf("事件数 = %d, want 1（只有 task.begin）", len(snap.Events))
	}
	if snap.Requests != 10 {
		t.Errorf("Requests = %d, want 10", snap.Requests)
	}
	if snap.StageText != "正在请求 /poll.html" {
		t.Errorf("StageText = %q", snap.StageText)
	}
}

// 任务结束后仍在飞的 HTTP 请求不得再往时间线上追加内容。
// （真实场景：用户点了取消，但已发出的请求还在陆续返回并触发 ProtocolHook。）
// 注意：web 层显式 emit 的事件不在此限制内——它们来自正在收尾的调用路径，
// 时序有界，且可能携带用户真正需要看到的信息。
func TestNoRequestEventsAfterFinish(t *testing.T) {
	ts := newTaskStore()
	const id = "a"
	ts.start(id, taskKindLogin)
	ts.emitRequest(id, taskEvent{Kind: "login.page", Stage: "login.page", Text: "正在获取登录页"})

	ts.setCancel(id, taskKindLogin, func() {})
	ts.takeCancel(id, taskKindLogin, true) // 等价于用户点了取消

	ts.emitRequest(id, taskEvent{Kind: "login.pubkey", Stage: "login.pubkey", Text: "正在获取加密公钥"})
	ts.touchStage(id, "http:/late.html", "正在请求 /late.html")

	snap := ts.snapshot(id)
	last := snap.Events[len(snap.Events)-1]
	if last.Text != "登录已取消" {
		t.Errorf("最后一条事件 = %q, want 登录已取消", last.Text)
	}
	for _, e := range snap.Events {
		if strings.Contains(e.Text, "加密公钥") || strings.Contains(e.Text, "late.html") {
			t.Errorf("收尾后的请求事件泄漏进时间线: %+v", e)
		}
	}
}

// 任务结束后被丢弃的请求也不该计入请求数。
func TestNoRequestCountAfterFinish(t *testing.T) {
	ts := newTaskStore()
	ts.start("a", taskKindInit)
	ts.emitRequest("a", taskEvent{Kind: "k", Stage: "k", Text: "x"})
	before := ts.snapshot("a").Requests
	ts.finish("a", taskKindInit, taskStatusDone, "初始化完成", "")
	ts.emitRequest("a", taskEvent{Kind: "k", Stage: "k", Text: "y"})
	ts.touchStage("a", "http:/z", "正在请求 /z")
	if got := ts.snapshot("a").Requests; got != before {
		t.Errorf("Requests = %d, want 保持 %d", got, before)
	}
}

// takeCancel 在 force=true 时要立即把任务标记为已取消，并真的调用 cancel。
func TestTaskStoreTakeCancel(t *testing.T) {
	ts := newTaskStore()
	ts.start("a", taskKindInit)
	called := false
	ts.setCancel("a", taskKindInit, func() { called = true })

	if !ts.takeCancel("a", taskKindInit, true) {
		t.Fatal("takeCancel 应当返回 true")
	}
	if !called {
		t.Error("cancel 函数未被调用")
	}
	if ts.running("a", taskKindInit) {
		t.Error("force 取消后任务不应仍在运行")
	}
	if ts.takeCancel("a", taskKindInit, true) {
		t.Error("重复 takeCancel 应当返回 false")
	}
	// 收尾事件必须留下，方便前端展示"为什么停了"
	snap := ts.snapshot("a")
	last := snap.Events[len(snap.Events)-1]
	if !strings.Contains(last.Text, "已取消") {
		t.Errorf("最后一条事件 = %q, want 含「已取消」", last.Text)
	}
}

// 从 ProtocolHook 到 /status 的整条链路（不产生任何真实网络请求）。
func TestStatusExposesRunningTask(t *testing.T) {
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
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}

	// 模拟一次登录任务：hook 只贡献请求计数，阶段与日志由 protocol 上报
	store.tasks.start(acc.ID, taskKindLogin)
	rt.requestStage(http.MethodGet, "https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html")
	rt.requestStage(http.MethodPost, "https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html?time=1")
	rt.loginStage(protocol.LoginStage{Phase: protocol.LoginPhaseFetchForm, Attempt: 1})
	rt.loginStage(protocol.LoginStage{Phase: protocol.LoginPhaseSubmit, Attempt: 1})

	srv := mustNewServer(t, store)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil)
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		LoggedIn    bool      `json:"loggedIn"`
		Initialized bool      `json:"initialized"`
		Task        *taskView `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if body.Task == nil {
		t.Fatal("响应里没有 task 快照")
	}
	if !body.Task.Running || body.Task.Kind != taskKindLogin {
		t.Errorf("task = %+v, want running login", body.Task)
	}
	if len(body.Task.Events) != 3 {
		t.Fatalf("事件数 = %d, want 3: %+v", len(body.Task.Events), body.Task.Events)
	}
	if body.Task.Events[0].Kind != "task.begin" {
		t.Errorf("首条事件 = %+v, want task.begin", body.Task.Events[0])
	}
	// POST 到登录页不再产生事件，最后一条是 protocol 上报的"提交账号密码"
	last := body.Task.Events[len(body.Task.Events)-1]
	if last.Text != "正在提交账号密码" {
		t.Errorf("最后一条事件 = %q, want 正在提交账号密码", last.Text)
	}
	if strings.Contains(rec.Body.String(), "time=1") {
		t.Error("响应里带出了原始查询串")
	}
	if body.Task.Requests != 2 {
		t.Errorf("Requests = %d, want 2", body.Task.Requests)
	}

	// 收尾后再查一次：任务应已结束，但快照仍在（前端刷新可见"上次结果"）
	store.tasks.finish(acc.ID, taskKindLogin, taskStatusDone, "登录成功", "")
	rec2 := httptest.NewRecorder()
	srv.router.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	var body2 struct {
		Task *taskView `json:"task"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("解析第二次响应失败: %v", err)
	}
	if body2.Task == nil || body2.Task.Running {
		t.Fatalf("第二次 task = %+v, want 已结束的快照", body2.Task)
	}
	if body2.Task.Status != taskStatusDone {
		t.Errorf("第二次 Status = %q, want %q", body2.Task.Status, taskStatusDone)
	}
}

// hook 的请求事件在登录阶段被 protocol 接管后只补时间线，不改写当前阶段；
// 登录阶段的中间过程由 protocol 上报，自己也要写进时间线。
func TestHookRequestDoesNotOverrideLoginStage(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindLogin)

	ts.emitRequest(id, taskEvent{Kind: "login.slogin", Stage: "login.slogin", Text: "正在获取登录页"})
	ts.setLoginStage(id, string(protocol.LoginPhaseSubmit), "正在提交账号密码")
	// hook 继续上报（取公钥 / 进主菜单）：阶段不能被 URL 版本改回去
	ts.emitRequest(id, taskEvent{Kind: "login.pubkey", Stage: "login.pubkey", Text: "正在获取加密公钥"})
	ts.touchStage(id, "login.menu", "正在进入主菜单")

	snap := ts.snapshot(id)
	if snap.Stage != string(protocol.LoginPhaseSubmit) || snap.StageText != "正在提交账号密码" {
		t.Fatalf("阶段被 hook 覆盖: stage=%q text=%q", snap.Stage, snap.StageText)
	}
	if snap.Requests != 3 {
		t.Errorf("Requests = %d, want 3", snap.Requests)
	}
	// 时间线不丢：hook 的两条请求事件 + protocol 的 submit 阶段都在
	if len(snap.Events) != 4 {
		t.Fatalf("事件数 = %d, want 4: %+v", len(snap.Events), snap.Events)
	}
	if last := snap.Events[len(snap.Events)-1]; last.Text != "正在获取加密公钥" {
		t.Errorf("最后一条事件 = %q, want 正在获取加密公钥", last.Text)
	}
	if submit := snap.Events[2]; submit.Kind != string(protocol.LoginPhaseSubmit) {
		t.Errorf("第 3 条事件 = %q, want login.submit 阶段", submit.Kind)
	}

	// 收尾事件仍然可以覆盖阶段（任务结束的结论优先）
	ts.finish(id, taskKindLogin, taskStatusDone, "登录成功", "")
	if got := ts.snapshot(id); got.StageText != "登录成功" {
		t.Errorf("收尾后 StageText = %q, want 登录成功", got.StageText)
	}
}

// 后台重登可能落在自动选课任务上：只补时间线，不能改写选课任务自己的阶段。
func TestLoginStageIgnoresNonLoginTask(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindAuto)
	ts.emit(id, taskEvent{Kind: "course.choose", Stage: "course.choose", Text: "正在选课"})

	ts.setLoginStage(id, string(protocol.LoginPhaseSubmit), "正在提交账号密码")
	snap := ts.snapshot(id)
	if snap.StageText != "正在选课" {
		t.Fatalf("非登录任务的阶段被改写: %q", snap.StageText)
	}
	if len(snap.Events) != 3 {
		t.Fatalf("事件数 = %d, want 3: %+v", len(snap.Events), snap.Events)
	}
	if last := snap.Events[2]; last.Kind != string(protocol.LoginPhaseSubmit) || last.Text != "正在提交账号密码" {
		t.Errorf("重登阶段没有进时间线: %+v", last)
	}
}

// 初始化可能先顺带登录：登录阶段要同时更新初始化任务的阶段和时间线。
func TestLoginStageFollowsInitTask(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindInit)

	ts.setLoginStage(id, string(protocol.LoginPhaseSubmit), "正在提交账号密码")
	snap := ts.snapshot(id)
	if snap.StageText != "正在提交账号密码" {
		t.Fatalf("初始化任务的登录阶段 = %q", snap.StageText)
	}
	if len(snap.Events) != 2 || snap.Events[1].Kind != string(protocol.LoginPhaseSubmit) {
		t.Fatalf("登录阶段没有进初始化任务时间线: %+v", snap.Events)
	}
}

// prepare / done 与生命周期事件文案重复，只更新阶段，不再补日志行。
func TestLoginStageSkipsDuplicateLifecycleEvents(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindLogin)

	ts.setLoginStage(id, string(protocol.LoginPhasePrepare), "正在准备登录")
	ts.setLoginStage(id, string(protocol.LoginPhaseDone), "登录成功")

	snap := ts.snapshot(id)
	if len(snap.Events) != 1 {
		t.Fatalf("事件数 = %d, want 1（只有 begin）: %+v", len(snap.Events), snap.Events)
	}
	if snap.Stage != string(protocol.LoginPhaseDone) || snap.StageText != "登录成功" {
		t.Errorf("阶段 = %q/%q, want login.done/登录成功", snap.Stage, snap.StageText)
	}
}

// 初始化阶段只作用于初始化任务：更新当前阶段并补一条时间线。
func TestInitStageUpdatesInitTask(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindInit)

	ts.setInitStage(id, string(protocol.InitPhaseIndex), "正在获取选课首页参数")
	snap := ts.snapshot(id)
	if snap.Stage != string(protocol.InitPhaseIndex) || snap.StageText != "正在获取选课首页参数" {
		t.Fatalf("阶段 = %q/%q, want init.index", snap.Stage, snap.StageText)
	}
	if len(snap.Events) != 2 || snap.Events[1].Kind != string(protocol.InitPhaseIndex) {
		t.Fatalf("初始化阶段没有进时间线: %+v", snap.Events)
	}
}

// 没有初始化任务时（初始化结束后 SelectTab 仍会抓课程列表参数）直接丢弃。
func TestInitStageIgnoredWithoutInitTask(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindLogin)

	ts.setInitStage(id, string(protocol.InitPhaseListPre), "正在获取课程列表参数")
	snap := ts.snapshot(id)
	if snap.StageText != "登录已开始" {
		t.Fatalf("登录任务的阶段被初始化阶段改写: %q", snap.StageText)
	}
	if len(snap.Events) != 1 {
		t.Fatalf("事件数 = %d, want 1: %+v", len(snap.Events), snap.Events)
	}
}

// 自动选课运行中重抓参数（撞上"加密串错误"）时只补时间线，不改任务阶段：
// 面板的"当前阶段"应该继续显示正在提交，而不是跳成"正在抓取首页参数"。
func TestInitStageAppendsToAutoTimeline(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindAuto)
	ts.emit(id, taskEvent{Kind: "auto.pick", Stage: "auto.pick", Text: "第 1 轮：尝试《定向越野》"})

	ts.setInitStage(id, string(protocol.InitPhaseIndex), "正在获取选课首页参数")
	snap := ts.snapshot(id)
	if snap.Stage != "auto.pick" || snap.StageText != "第 1 轮：尝试《定向越野》" {
		t.Fatalf("自动选课的阶段被初始化阶段改写: %q/%q", snap.Stage, snap.StageText)
	}
	if last := snap.Events[len(snap.Events)-1]; last.Kind != string(protocol.InitPhaseIndex) {
		t.Fatalf("重抓参数没有进时间线: %+v", snap.Events)
	}
}

// 初始化时间线已由 protocol 接管，hook 的端点事件只累加计数，避免同一请求两条日志。
func TestInitTaskDropsHookEndpointEvents(t *testing.T) {
	ts := newTaskStore()
	const id = "acc1"
	ts.start(id, taskKindInit)
	ts.setInitStage(id, string(protocol.InitPhaseIndex), "正在获取选课首页参数")

	ts.emitRequest(id, taskEvent{Kind: "init.index", Stage: "init.index", Text: "正在打开选课页"})
	snap := ts.snapshot(id)
	if snap.Requests != 1 {
		t.Errorf("请求计数 = %d, want 1", snap.Requests)
	}
	if len(snap.Events) != 2 {
		t.Fatalf("hook 事件不应进初始化时间线: %+v", snap.Events)
	}
	if snap.StageText != "正在获取选课首页参数" {
		t.Errorf("阶段 = %q", snap.StageText)
	}
}
