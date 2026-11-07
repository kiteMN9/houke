package web

import (
	"context"
	"sync"
	"time"

	"school_sdk/protocol"
)

// =========================================================================
// 长任务可观测性
//
// 登录 / 初始化最长可达「登录超时」（账号级配置，默认 10 分钟）。这里把它们的
// "运行态"变成一份可随时查询的快照（任务类型 / 当前阶段 / 已耗时 / 事件流），使前端：
//   1. 能显示"现在卡在哪一步、已经等了多久"；
//   2. 刷新页面后能从 /status 重新接管正在跑的任务，而不是两眼一抹黑。
//
// 事件来源有三类：
//   - 任务生命周期事件（开始 / 结束 / 取消 / 提交输入 …）由 web 层显式 emit；
//   - 登录阶段由 protocol 通过 engine.Options.OnStage 直接上报（语义阶段，
//     不依赖 HTTP 请求；hook 缺席时仍然有）。中间阶段既更新"当前阶段"，
//     也补一条时间线事件，登录过程不再靠 URL 反推；
//   - 初始化阶段同样由 protocol 通过 engine.Options.OnInitStage 上报，初始化
//     时间线不再依赖 hook；hook 只负责选课 / 未知请求的端点明细。
// =========================================================================

const (
	taskKindLogin = "login"
	taskKindInit  = "init"
	taskKindAuto  = "auto"

	taskStatusRunning   = "running"
	taskStatusDone      = "done"
	taskStatusCancelled = "cancelled"
	taskStatusFailed    = "failed"
	taskStatusTimeout   = "timeout"

	// 服务端保留的事件条数上限（返回给前端的还会再截断一次）。
	maxTaskEvents = 200
	// 返回给前端的事件条数。
	taskEventsPreview = 100
)

// taskEvent 一次可观测事件，同时充当"当前阶段"的载体。
type taskEvent struct {
	T      time.Time
	Kind   string // 分类：task.* / login.* / init.* / http.*
	Stage  string // 短阶段名
	Text   string // 给用户看的一句话
	Detail string // 补充信息（端点），可为空
	Repeat int    // 连续重复次数（>=1）；重试场景下折叠成 "… ×N"
	Final  bool   // 任务收尾事件，必须保留

	// noStage 表示这条事件只写时间线、不改写"当前阶段"。
	// 用于 ProtocolHook 的请求事件：登录阶段已经由 protocol 的 LoginObserver
	// 直接记账时，hook 再按 URL 把阶段改回去就会来回跳。
	noStage bool
}

// accountTask 某个账号当前的（或最近一次的）长任务状态。
type accountTask struct {
	id      uint64
	kind    string
	status  string
	running bool

	startedAt time.Time
	endedAt   time.Time

	stage     string
	stageText string
	stageAt   time.Time

	// stagePinned 表示"当前阶段"已由 protocol 的登录阶段接管（见 login_stage.go）。
	// 之后 hook 请求事件只进时间线；生命周期事件（开始/收尾/待输入）仍可覆盖。
	stagePinned bool

	events []taskEvent
	rev    uint64
	reqs   int // 任务期间发出的 HTTP 请求总数（让用户看到"确实在动"）

	// result 是任务结束时给调用方读的结构化结果（目前自动选课使用）。
	// 运行期间为 nil；finishWithResult 会与 status 一起原子写入。
	result *taskResult

	// 取消函数：仅当任务正在运行时非 nil。
	cancel context.CancelFunc

	// SSE 订阅：有变更时推快照。容量小是故意的——推送频率上限就是
	// "每秒一个进度 + 每次真实事件"，队列满说明客户端已经落后，
	// 丢掉中间态不会丢信息（下一帧就是完整快照）。
	subs map[uint64]chan taskNotice
}

// taskStore 管理全部账号的任务状态。
type taskStore struct {
	mu    sync.Mutex
	seq   uint64
	subID uint64
	tasks map[string]*accountTask
}

func newTaskStore() *taskStore {
	return &taskStore{tasks: make(map[string]*accountTask)}
}

// start 开始一个长任务；若同账号已有任务在跑，返回当时的任务快照与 false。
// 取消函数稍后由 setCancel 登记（startCancelable 是原子版本，优先用它）。
func (ts *taskStore) start(id, kind string) (*accountTask, bool) {
	return ts.startCancelable(id, kind, nil)
}

// startCancelable 是 start 的原子版本：建任务与登记取消函数在同一次加锁里
// 完成，调用方（以及 /status 的读者）不会观察到「任务在跑、却取消不了」的
// 中间态，也就不会出现「取消按钮点了只标个已取消、ctx 还活着」这种说谎状态。
//
// 同账号已有任务在跑时返回 (旧任务快照, false)，且不登记 cancel——本次的
// 取消函数仍归调用方所有，由它决定是否调用。
func (ts *taskStore) startCancelable(id, kind string, cancel context.CancelFunc) (*accountTask, bool) {
	ts.mu.Lock()
	old := ts.tasks[id]
	if old != nil && old.running {
		ts.mu.Unlock()
		return old, false
	}
	ts.seq++
	now := time.Now()
	t := &accountTask{
		id:        ts.seq,
		kind:      kind,
		status:    taskStatusRunning,
		running:   true,
		startedAt: now,
		stageAt:   now,
		cancel:    cancel,
	}
	// 新任务顶替旧任务时，必须把已建立的订阅带过来：
	// 订阅者挂在"账号"上，而不是挂在某一次任务上，否则一条常驻的
	// 推送连接会在第一次 start 之后彻底收不到消息。
	if old != nil {
		t.subs = old.subs
	}
	ts.tasks[id] = t
	t.emitLocked(taskEvent{
		Kind:  "task.begin",
		Stage: "task.begin",
		Text:  kindLabel(kind) + "已开始",
	})
	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
	return t, true
}

// finish 结束任务。text 是给用户看的结果描述。
func (ts *taskStore) finish(id, kind, status, text, detail string) {
	ts.finishWithResult(id, kind, 0, status, text, detail, nil)
}

// finishWithResult 结束任务并原子写入结构化结果。
//
// taskID 非 0 时只结束这一次任务：异步任务被取消后可能已经开始了下一次任务，
// 旧 goroutine 的收尾不能把新任务一起结束。
//
// 结果必须和 status / 收尾事件在同一次加锁里写入：否则订阅者可能先收到
// 一帧"已完成但没有 result"的快照，再补一帧带结果的，前端要处理中间态。
func (ts *taskStore) finishWithResult(id, kind string, taskID uint64, status, text, detail string, result *taskResult) {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || t.kind != kind || !t.running || (taskID != 0 && t.id != taskID) {
		ts.mu.Unlock()
		return
	}
	t.running = false
	t.status = status
	t.endedAt = time.Now()
	t.cancel = nil
	t.result = result
	t.emitLocked(taskEvent{
		Kind:   "task." + status,
		Stage:  "task." + status,
		Text:   text,
		Detail: detail,
		Final:  true,
	})
	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// setCancel 在任务启动之后补登取消函数；任务已不在运行时静默忽略。
//
// 注意：它天然留下"任务已可见、但取消函数还没挂上"的窗口，调用方在这个窗口里
// 取取消函数只能取到 nil（见 runLogin 的注释）。生产代码一律用 startCancelable
// 在启动的同一次加锁里登记；这里保留给需要构造"两步登记"中间态的测试。
func (ts *taskStore) setCancel(id, kind string, cancel context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if t := ts.tasks[id]; t != nil && t.kind == kind && t.running {
		t.cancel = cancel
	}
}

// takeCancel 取出并调用取消函数。
// force=true 时同时把任务标记为已取消——用于初始化：取消信号由 Initialize
// 内部消化后只返回 error，不一定回到 handler 的返回路径。
func (ts *taskStore) takeCancel(id, kind string, force bool) bool {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || t.kind != kind || !t.running || t.cancel == nil {
		ts.mu.Unlock()
		return false
	}
	cancel := t.cancel
	t.cancel = nil
	var notice *taskNotice
	if force {
		t.running = false
		t.status = taskStatusCancelled
		t.endedAt = time.Now()
		t.result = &taskResult{Status: taskStatusCancelled}
		t.emitLocked(taskEvent{
			Kind:  "task.cancelled",
			Stage: "task.cancelled",
			Text:  kindLabel(kind) + "已取消",
			Final: true,
		})
		notice = t.noticeLocked()
	}
	ts.mu.Unlock()

	ts.publish(id, notice)
	cancel()
	return true
}

// running 查询指定类型任务是否在运行。kind 为空表示"任意类型"。
func (ts *taskStore) running(id, kind string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t := ts.tasks[id]
	if t == nil || !t.running {
		return false
	}
	return kind == "" || t.kind == kind
}

// drop 删除账号时清理其任务状态，并停掉可能还在跑的任务。
//
// 账号被删除后，任务表与运行态一起消失：/login/cancel、/init/cancel、
// /auto-choose/cancel 都只会拿到「账号不存在」，没有任何接口再能取消它，
// 任务会一直跑到自己的预算（自动选课默认 24 小时），期间仍然登录、抓页面、
// 提交选课。取消函数登记在任务上（见 startCancelable），运行态里那份只是
// 副本、且马上被 Delete 一起丢掉，所以这里是最后一处能停掉它的地方：
// 删除状态前先摘下 cancel，解锁后再调用——cancel 会唤醒任务的收尾路径，
// 而收尾会去拿 store 的锁，不该在持锁时触发。
func (ts *taskStore) drop(id string) {
	ts.mu.Lock()
	t := ts.tasks[id]
	delete(ts.tasks, id)
	if t == nil {
		ts.mu.Unlock()
		return
	}
	cancel := t.cancel
	t.cancel = nil
	// 通知订阅者"这个账号没了"，让 SSE 连接干净收尾
	notice := &taskNotice{id: t.id, rev: t.rev, dropped: true}
	for sid, ch := range t.subs {
		select {
		case ch <- *notice:
		default:
		}
		delete(t.subs, sid)
	}
	ts.mu.Unlock()

	// 幂等：已结束的任务 cancel 是 nil，已在取消的 ctx 再取消一次是空操作。
	if cancel != nil {
		cancel()
	}
}

// cancelAll 取消所有仍在运行的长任务，返回取消的数量。用于进程退出。
//
// 这些任务的 context 都派生自 context.Background()（不是请求 ctx），不主动
// 取消就会继续跑到自己的预算——自动选课默认 24 小时，进程退出后任务还在
// 登录、抓页面、提交选课。这里把任务标成已取消并推一帧收尾事件，前端
// 能看到干净的结束状态。
//
// 与 drop 同理：cancel 会唤醒任务的收尾路径（收尾要回头拿 ts.mu），
// 所以 cancel 与 publish 都放在解锁之后，不能在持锁时调用。
func (ts *taskStore) cancelAll() int {
	type cancelled struct {
		id     string
		notice *taskNotice
	}
	var (
		cancels []context.CancelFunc
		notices []cancelled
	)
	now := time.Now()

	ts.mu.Lock()
	for id, t := range ts.tasks {
		if !t.running {
			continue
		}
		t.running = false
		t.status = taskStatusCancelled
		t.endedAt = now
		t.result = &taskResult{Status: taskStatusCancelled}
		if t.cancel != nil {
			cancels = append(cancels, t.cancel)
			t.cancel = nil
		}
		t.emitLocked(taskEvent{
			Kind:  "task.cancelled",
			Stage: "task.cancelled",
			Text:  kindLabel(t.kind) + "已取消（进程退出）",
			Final: true,
		})
		notices = append(notices, cancelled{id: id, notice: t.noticeLocked()})
	}
	ts.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	for _, n := range notices {
		ts.publish(n.id, n.notice)
	}
	return len(notices)
}

// emit 由 web 层写入一条生命周期事件（任务不存在时静默忽略）。
func (ts *taskStore) emit(id string, e taskEvent) {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil {
		ts.mu.Unlock()
		return
	}
	t.emitLocked(e)
	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// emitRequest 写一条"HTTP 请求阶段"事件，并累加请求计数。
// 计数只在真正发出请求时增加，因此前端可以拿它当"后台确实在动"的证据。
// 任务已结束时丢弃：取消/超时后仍在飞的请求不该再往时间线上追加内容。
func (ts *taskStore) emitRequest(id string, e taskEvent) {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || !t.running {
		ts.mu.Unlock()
		return
	}
	t.reqs++
	// 初始化任务的阶段与时间线已由 protocol 的 InitStage 接管（见
	// init_stage.go）；hook 的端点事件再写一遍就是同一请求两条日志。
	// 这里只累加请求计数，事件留给 protocol。
	if t.kind == taskKindInit && t.stagePinned {
		t.rev++
		notice := t.noticeLocked()
		ts.mu.Unlock()
		ts.publish(id, notice)
		return
	}
	e.noStage = true // hook 事件只补时间线，当前阶段以 protocol 的阶段为准
	t.emitLocked(e)
	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// setLoginStage 写入 protocol 上报的登录阶段。
//
// 登录任务：
//   - prepare / done 只更新"当前阶段"——"开始登录 / 登录成功"已经由 web 的
//     生命周期事件写过，再补一条就重复了；
//   - 其余阶段既更新阶段，也补一条时间线事件：登录过程现在由 protocol 的
//     语义阶段产生，不再依赖 URL 分类，所以 hook 缺席时日志也不会是空的。
//
// 初始化任务：初始化可能先顺带登录，此时阶段与时间线都跟着登录走；
// 登录结束后再由 init 阶段接管。
//
// 自动选课等其它任务（后台重登）：只补时间线，不动任务阶段，
// 否则会把"正在加载课程列表"改成"正在提交账号密码"。
func (ts *taskStore) setLoginStage(id, stage, text string) {
	if stage == "" {
		return
	}
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || !t.running {
		ts.mu.Unlock()
		return
	}

	switch t.kind {
	case taskKindLogin:
		t.stagePinned = true
		if stage == string(protocol.LoginPhasePrepare) || stage == string(protocol.LoginPhaseDone) {
			t.stage = stage
			if text != "" {
				t.stageText = text
			}
			t.stageAt = time.Now()
			t.rev++
		} else {
			t.emitLocked(taskEvent{Kind: stage, Stage: stage, Text: text})
		}
	case taskKindInit:
		t.stagePinned = true
		t.emitLocked(taskEvent{Kind: stage, Stage: stage, Text: text})
	default:
		// Stage 留空：只进时间线，不碰任务自己的阶段
		t.emitLocked(taskEvent{Kind: stage, Text: text})
	}

	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// setInitStage 写入 protocol 上报的初始化阶段。
//
// 初始化任务会把它当作自己的"当前阶段"；自动选课在运行中也会重抓选课参数
// （撞上"加密串错误"时，见 engine/session.Renew），那时只补时间线，不抢任务
// 自己的阶段——否则面板会从"正在提交"跳成"正在抓取首页参数"。
// 其它任务（登录）与初始化参数无关，没有任务在跑时（例如 SelectTab）也没有
// 任务可写，一律丢弃。
func (ts *taskStore) setInitStage(id, stage, text string) {
	if stage == "" {
		return
	}
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || !t.running {
		ts.mu.Unlock()
		return
	}
	switch t.kind {
	case taskKindInit:
		t.stagePinned = true
		t.emitLocked(taskEvent{Kind: stage, Stage: stage, Text: text})
	case taskKindAuto:
		t.emitLocked(taskEvent{Kind: stage, Text: text})
	}
	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// touchStage 只更新"当前阶段"与请求计数，不写事件日志。
// 用于未知端点：界面上仍能看到"正在请求 /xxx.html"以及请求次数在涨，
// 但不会把事件流刷爆。任务已结束时同样丢弃。
func (ts *taskStore) touchStage(id, stage, text string) {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || !t.running {
		ts.mu.Unlock()
		return
	}
	t.reqs++
	if stage == "" || t.stagePinned {
		ts.mu.Unlock()
		return
	}
	t.stage = stage
	if text != "" {
		t.stageText = text
	}
	t.stageAt = time.Now()
	notice := t.noticeLocked()
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// tickRunning 推进一次"进度"：累计耗时进一帧，但不产生任何日志行。
// 由 SSE 连接的定时器调用，用来替代"前端每秒拉一次 /status 走秒"。
// 帧上会带 tick 标记，前端据此只更新计时、不重绘日志。
func (ts *taskStore) tickRunning(id string) {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || !t.running {
		ts.mu.Unlock()
		return
	}
	t.rev++
	notice := t.noticeLocked()
	notice.tick = true
	ts.mu.Unlock()

	ts.publish(id, notice)
}

// emitLocked 调用方必须持有 ts.mu。
//
// 连续重复的同一条事件（典型场景：登录/初始化在原地重试）不会各占一行，
// 而是折叠成一行并累加 Repeat，界面显示为 "… ×12"。
// 否则一次 10 分钟的重试能刷出上百条一模一样的日志，把真正的信号埋掉。
func (t *accountTask) emitLocked(e taskEvent) {
	if t == nil || e.Text == "" {
		return
	}
	now := time.Now()

	if last := t.lastEventLocked(); last != nil && !e.Final &&
		last.Kind == e.Kind && last.Text == e.Text {
		// 时间戳仍要推进（前端据此知道"还在动"），但不新增日志行
		if now.Before(last.T) {
			now = last.T
		}
		last.T = now
		last.Repeat++
		if e.Detail != "" {
			last.Detail = e.Detail
		}
		t.rev++
	} else {
		// 时间戳严格单调，避免前端展示出负的时间差
		if last := t.lastEventLocked(); last != nil && now.Before(last.T) {
			now = last.T.Add(time.Millisecond)
		}
		e.T = now
		if e.Repeat == 0 {
			e.Repeat = 1
		}
		t.events = append(t.events, e)
		if len(t.events) > maxTaskEvents {
			t.events = append([]taskEvent(nil), t.events[len(t.events)-maxTaskEvents:]...)
		}
		t.rev++
	}

	// hook 请求事件（noStage）在阶段被 protocol 接管后只进时间线；
	// 生命周期事件与待输入事件没有这个标记，仍然可以更新阶段。
	if e.Stage != "" && !(t.stagePinned && e.noStage) {
		t.stage = e.Stage
		t.stageText = e.Text
		t.stageAt = now
	}
}

func (t *accountTask) lastEventLocked() *taskEvent {
	if len(t.events) == 0 {
		return nil
	}
	return &t.events[len(t.events)-1]
}

type taskEventView struct {
	AtMs      int64  `json:"atMs"`      // 距任务开始的毫秒数
	Timestamp int64  `json:"timestamp"` // Unix 毫秒
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Detail    string `json:"detail,omitempty"`
	Repeat    int    `json:"repeat,omitempty"` // 连续重复次数（>1 时前端显示 ×N）
}

// taskResult 是一次任务的结构化结果。
//
// task.status 仍表示生命周期（running/done/failed/timeout/cancelled），
// result.status 表示业务结果（ok/not_chosen/session_changed…），两者分开，
// 前端不必从 HTTP 状态码反推"到底是没选上还是服务坏了"。
type taskResult struct {
	Status string                    `json:"status"`
	Flag   string                    `json:"flag,omitempty"`
	Msg    string                    `json:"msg,omitempty"`
	Course *protocol.CustomCourseDic `json:"course,omitempty"`
	Error  string                    `json:"error,omitempty"`
}

type taskView struct {
	Running   bool            `json:"running"`
	ID        uint64          `json:"id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Stage     string          `json:"stage"`
	StageText string          `json:"stageText"`
	ElapsedMs int64           `json:"elapsedMs"`
	StageMs   int64           `json:"stageMs"`
	StartedAt int64           `json:"startedAt"`
	EndedAt   int64           `json:"endedAt,omitempty"`
	Rev       uint64          `json:"rev"`
	EventSeq  int             `json:"eventSeq"`
	Requests  int             `json:"requests"`
	Events    []taskEventView `json:"events"`
	Result    *taskResult     `json:"result,omitempty"`
}

// snapshot 生成快照。从未跑过任务的账号返回 nil。
func (ts *taskStore) snapshot(id string) *taskView {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.snapshotLocked(id)
}

func (ts *taskStore) snapshotLocked(id string) *taskView {
	t := ts.tasks[id]
	if t == nil || (t.id == 0 && len(t.events) == 0) {
		return nil
	}
	return t.viewLocked(time.Now())
}

// viewLocked 调用方必须持有 ts.mu。
func (t *accountTask) viewLocked(now time.Time) *taskView {
	elapsed := now.Sub(t.startedAt)
	stageMs := now.Sub(t.stageAt).Milliseconds()
	if !t.running {
		elapsed = t.endedAt.Sub(t.startedAt)
		if !t.stageAt.IsZero() {
			stageMs = t.endedAt.Sub(t.stageAt).Milliseconds()
		}
	}

	events := t.events
	if len(events) > taskEventsPreview {
		events = events[len(events)-taskEventsPreview:]
	}
	views := make([]taskEventView, 0, len(events))
	for _, e := range events {
		views = append(views, taskEventView{
			AtMs:      e.T.Sub(t.startedAt).Milliseconds(),
			Timestamp: e.T.UnixMilli(),
			Kind:      e.Kind,
			Text:      e.Text,
			Detail:    e.Detail,
			Repeat:    e.Repeat,
		})
	}

	return &taskView{
		Running:   t.running,
		ID:        t.id,
		Kind:      t.kind,
		Status:    t.status,
		Stage:     t.stage,
		StageText: t.stageText,
		ElapsedMs: elapsed.Milliseconds(),
		StageMs:   stageMs,
		StartedAt: t.startedAt.UnixMilli(),
		EndedAt:   t.endedAt.UnixMilli(),
		Rev:       t.rev,
		EventSeq:  len(t.events),
		Requests:  t.reqs,
		Events:    views,
		Result:    t.result,
	}
}

const (
	// 每个账号的推送队列深度。满了就丢：丢掉的是中间态，
	// 下一帧仍是完整快照，前端不会因此显示错误的进度。
	subscriberQueue = 8
)

// taskNotice 推给订阅者的一帧。
type taskNotice struct {
	id      uint64    // 任务序号：客户端据此发现"后端开了新任务"
	rev     uint64    // 该任务的状态版本号
	tick    bool      // 只是走秒（没有新事件），接收方可以只更新计时
	dropped bool      // 账号已被删除，连接可以收尾
	task    *taskView // 完整快照；nil 表示该账号从未跑过任务
}

// subscriber 一个 SSE 订阅。
type subscriber struct {
	ch chan taskNotice
}

// noticeLocked 冻结当前版本号并生成一帧。调用方必须持有 ts.mu。
//
// 注意：快照是深拷贝，但这里刻意只带 rev —— 订阅者稍后在锁外
// 按需生成快照，避免"每次事件都构造完整快照"的开销。
func (t *accountTask) noticeLocked() *taskNotice {
	return &taskNotice{id: t.id, rev: t.rev}
}

// publish 把一帧广播给订阅者。调用方不得持有 ts.mu：推送给整帧快照
// 生成留在这里做，出错或没人订阅时几乎零成本。
func (ts *taskStore) publish(id string, n *taskNotice) {
	if n == nil {
		return
	}
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil || len(t.subs) == 0 {
		ts.mu.Unlock()
		return
	}
	n.task = t.viewLocked(time.Now())
	targets := make([]chan taskNotice, 0, len(t.subs))
	for _, ch := range t.subs {
		targets = append(targets, ch)
	}
	ts.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- *n:
		default: // 客户端跟不上：丢掉这一帧，绝不阻塞任务本身
		}
	}
}

// subscribe 订阅某账号的任务状态。
// 返回：接收通知的 channel、当前快照（可能为 nil）、任务序号、取消函数。
func (ts *taskStore) subscribe(id string) (<-chan taskNotice, *taskView, uint64, func()) {
	ts.mu.Lock()
	t := ts.tasks[id]
	if t == nil {
		ts.seq++
		now := time.Now()
		// 为尚无任务的账号建一条"空任务"记录：这样第一帧的 id 是稳定的，
		// 客户端不会把"从没跑过"误判成"换了个新任务"。
		t = &accountTask{id: ts.seq, startedAt: now, stageAt: now}
		ts.tasks[id] = t
	}
	if t.subs == nil {
		t.subs = make(map[uint64]chan taskNotice)
	}
	ts.subID++
	sid := ts.subID
	ch := make(chan taskNotice, subscriberQueue)
	t.subs[sid] = ch

	snap := t.viewLocked(time.Now())
	taskID := t.id
	ts.mu.Unlock()

	once := sync.Once{}
	unsubscribe := func() {
		once.Do(func() {
			ts.mu.Lock()
			defer ts.mu.Unlock()
			cur := ts.tasks[id]
			if cur == nil {
				return
			}
			delete(cur.subs, sid)
		})
	}
	return ch, snap, taskID, unsubscribe
}

// kindLabel 返回任务类型的中文名（用于兜底文案与推送标题）。
func kindLabel(kind string) string {
	switch kind {
	case taskKindLogin:
		return "登录"
	case taskKindInit:
		return "初始化"
	case taskKindAuto:
		return "自动选课"
	default:
		return "任务"
	}
}
