package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// =========================================================================
// 任务状态推送（SSE）
//
// 背景：登录/初始化期间前端原本每 600ms 拉一次 /status，一次 10 分钟的登录
// 就是上千次请求，而其中绝大多数响应体（事件流 + 验证码图片）没有任何变化。
//
// 这里用一条 text/event-stream 长连接替代那圈轮询：
//   - 任务状态一变（事件、阶段、起止）立刻推一帧完整快照；
//   - 任务运行期间每秒推一帧轻量进度（elapsedMs / stageMs / requests），
//     让界面走秒不依赖前端反复请求；
//   - 连接建立时先推一帧，因此刷新页面 / 切账号都能立刻接管正在跑的任务。
//
// /status 依然保留：它是"随时可查"的兜底与首屏入口，只是不再被高频轮询。
// =========================================================================

const (
	// SSE 事件名。客户端只监听这一个，避免与 EventSource 的连接错误事件
	// （onerror）混在一起。
	sseEventTask = "task"

	// 心跳间隔：注释行保活，同时让服务端及时察觉客户端断开。
	sseHeartbeat = 25 * time.Second
)

// progressInterval 进度帧间隔：任务运行期间推一帧轻量进度。
//
// 刻意放慢到 5s：界面上的"已等待 12.3s"由前端本地平滑推进
// （app.js 的 Task.renderBar），服务端推送只是防止本地计时器停摆，
// 不需要真的每秒推一次。慢一点，长任务的推送量从"上百帧"降到十几帧。
//
// 变量而非常量：测试要把它调小，否则每个用例都得真等 5 秒。
var progressInterval = 5 * time.Second

// taskNoticeView 推给前端的一帧。
// task 与 /status 里的 task 同构，前端两条路径共用同一套渲染；
// 另外带上 loggedIn，让登录态变化也能靠推送更新徽标。
type taskNoticeView struct {
	Type     string `json:"type"` // snapshot：带完整事件流；progress：只带进度
	ID       uint64 `json:"id"`   // 任务序号
	Rev      uint64 `json:"rev"`  // 状态版本号（前端据此去重）
	LoggedIn bool   `json:"loggedIn"`
	// Pending 是当前等待中的验证码/密码输入。远端发起的登录（如 ping 自动
	// 重登）只能靠推送知道"要输入了"，所以 snapshot 帧必须带上；
	// progress 帧（只走秒）不带，避免验证码图片每 5 秒重复一遍。
	Pending *pendingInputView `json:"pending"`
	// QR 是当前微信二维码的元数据（不含图片）。字段不加 omitempty：
	// 二维码失效时 snapshot 帧要显式发 qr:null，前端才知道该关掉弹窗。
	QR   *loginQRView `json:"qr"`
	Task *taskView    `json:"task"` // 与 /status 里的 task 同构
}

// taskEvents 推送当前账号的任务状态。
//
// 帧的两种形态（由 type 字段区分，前端都按同一套渲染处理）：
//
//	snapshot  状态真的变了：带完整事件流，前端据此追加日志
//	progress  只是走了几秒：不带事件流，前端只更新计时/阶段
func (s *Server) taskEvents(c *gin.Context) {
	id := c.Param("id")
	rt, err := s.store.runtime(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 必须在写 header 之前注册订阅，否则这段空档里发生的变更会丢
	ch, snap, taskID, unsubscribe := s.tasks.subscribe(id)
	defer unsubscribe()

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// 反代（nginx）默认会缓冲响应，导致事件卡住不发出
	h.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	// 首帧必须是完整快照：刷新页面后要能立刻看到"正在登录 / 卡在哪一步"
	if err := s.writeSSEFrame(c, rt, snap, taskID, false); err != nil {
		return
	}
	lastRev := uint64(0)
	if snap != nil {
		lastRev = snap.Rev
	}

	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			// 客户端断开（关页面 / 切账号 / 网络抖动）
			return

		case n, ok := <-ch:
			if !ok {
				return
			}
			if n.dropped {
				// 账号已被删除：收尾，让前端去刷新账号列表
				_ = writeSSE(c, sseEventTask, fmt.Sprintf(`{"type":"gone","id":%d}`, taskID))
				return
			}
			if n.rev == lastRev {
				// 这一版已经推过（例如刚推完进度帧又来了广播）
				continue
			}
			// tick 表示这一帧只是走秒：剥掉事件流，前端据此跳过整块重绘
			if err := s.writeSSEFrame(c, rt, n.task, n.id, n.tick); err != nil {
				return
			}
			lastRev = n.rev

		case <-ticker.C:
			// 只有任务在跑才需要进度：空闲期间这条连接完全静默，一个字节都不发
			if !s.tasks.running(id, "") {
				continue
			}
			// 推进一次进度：广播会带着"这只是走秒"的标记回到上面的 case
			s.tasks.tickRunning(id)

		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// writeSSEFrame 写一帧任务状态。
// tick=true 表示这一帧只是走秒（剥掉事件流，前端也据此跳过重绘）。
func (s *Server) writeSSEFrame(c *gin.Context, rt *accountRuntime, t *taskView, taskID uint64, tick bool) error {
	// 登录态同样只从 client 读，web 不存副本
	frame := taskNoticeView{Type: "snapshot", ID: taskID, LoggedIn: rt.engine.LoggedIn(), Task: t}
	if !tick {
		// 首帧/事件帧带上待输入请求；progress 帧不带（验证码图片不小）
		frame.Pending = pendingInputViewOf(rt.getPendingInput())
		// 二维码只发元数据；图片由 /login/qr 按 rev 单独取
		frame.QR = rt.qrView()
	}
	if t != nil {
		frame.ID = t.ID
		frame.Rev = t.Rev
		if tick {
			frame.Type = "progress"
			light := *t
			light.Events = nil
			frame.Task = &light
		}
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	// 事件 id 用任务序号：EventSource 重连会带 Last-Event-ID，
	// 前端据此知道"还是同一个任务"，不会误判成新任务而清空日志。
	return writeSSE(c, sseEventTask, string(payload),
		fmt.Sprintf("%d", frame.ID), fmt.Sprintf("%d", frame.Rev))
}

// writeSSE 按 SSE 规范写一条事件并立即 flush。
// opts[0]=id、opts[1]=rev，为空或 "0" 时省略该字段。
func writeSSE(c *gin.Context, event, data string, opts ...string) error {
	w := c.Writer
	if len(opts) > 0 && opts[0] != "" && opts[0] != "0" {
		if _, err := fmt.Fprintf(w, "id: %s\n", opts[0]); err != nil {
			return err
		}
	}
	if len(opts) > 1 && opts[1] != "" {
		if _, err := fmt.Fprintf(w, "rev: %s\n", opts[1]); err != nil {
			return err
		}
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	w.Flush()
	return nil
}
