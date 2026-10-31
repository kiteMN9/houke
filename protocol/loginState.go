package protocol

import (
	"context"
	"sync"
	"time"
)

// =========================================================================
// 登录态（loggedIn）由 APIClient 自己维护
//
// 谁改变了登录态，就由谁记账：
//   - 登录成功 / 失败：LoginWithContext（Login / ReLogin 都走它）
//   - 显式登出：Logout
//   - 会话探测掉线：Ping
//
// 上层（WebUI / CLI）只读 a.LoggedIn()，不要再各存一份 loggedIn：
// 那种写法一旦漏掉某条同步链路（登录超时、被踢下线、保活重新登录…），
// 界面上的登录态就会和客户端真实状态长期不一致。
// =========================================================================

// LoggedIn 返回当前会话是否处于已登录状态。
// 并发安全，可以直接在状态查询 / 推送帧里调用。
func (a *APIClient) LoggedIn() bool {
	if a == nil {
		return false
	}
	return a.isLoggedIn.Load()
}

// setLoggedIn 显式写入登录态。
// 正常链路（登录 / 登出 / 会话探测）内部都会自己维护，只有"登录动作在
// 别处完成"这类特殊情况才需要显式同步；上层不要拿它当自己的状态缓存。
func (a *APIClient) setLoggedIn(v bool) {
	if a == nil {
		return
	}
	a.isLoggedIn.Store(v)
}

// TestSetLoggedIn 供测试显式写入登录态。
// 生产链路不要用它：登录 / 登出 / 会话探测会自己记账。
func (a *APIClient) TestSetLoggedIn(v bool) {
	if a == nil {
		return
	}
	a.isLoggedIn.Store(v)
}

// =========================================================================
// 登录流程的串行化（单账号）
//
// 历史包袱：下面这三样以前是包级变量（loginMU / lastSuccessTime /
// hasSliderCaptcha），Web 多账号一起用的时候就串了：
//   - A 账号登录成功后 1.1 秒内，B 账号的 ReLogin 会直接返回 true，
//     可 B 根本没有会话；
//   - A 账号见过滑块验证码，所有账号都被赶到滑块分支；
//   - 登录锁是全局的：A 登录时，B 的全部选课请求都被挡住。
//
// 现在这些状态跟着 APIClient 走，锁也只串行化本账号。
// =========================================================================

// loginCooldown 是同一账号两次登录之间的冷静期。
// 并发请求同时发现掉线时，只让第一个真正重登，其余的直接复用刚建立的会话。
const loginCooldown = 1100 * time.Millisecond

// loginCoord 是单账号登录流程的锁与过程状态。
type loginCoord struct {
	// mu 串行化本账号所有登录入口（LoginWithContext / ReLogin）。
	// 注意它会被登录流程持有到结束：Web 端等验证码输入时，
	// 同账号的其他登录请求会排队，而不是并发地互相覆盖 cookie。
	mu          sync.Mutex
	lastSuccess time.Time // 本账号最近一次登录成功的时间
	sliderSeen  bool      // 本账号是否见过滑块验证码（跨次登录粘滞，但不跨账号）
	gate        loginGate // 让并发的业务请求等"本账号登录完"
}

// loginGate 是"登录进行中"的广播闸门：登录开始时打开，结束时关闭并唤醒等待者。
//
// 它取代了原来包级的 loginWg：sync.WaitGroup 的 Add 与 Wait 并发属于误用
// （可能 panic），而且那个 WaitGroup 是所有账号共享的——A 登录会阻塞
// B 的全部选课请求。
type loginGate struct {
	mu   sync.Mutex
	done chan struct{} // 非 nil 表示有登录正在进行
}

// begin 打开闸门并返回关闭函数。关闭函数只会生效一次，配合 defer 使用。
func (g *loginGate) begin() (end func()) {
	ch := make(chan struct{})
	g.mu.Lock()
	g.done = ch
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		if g.done == ch {
			g.done = nil
		}
		g.mu.Unlock()
		close(ch)
	}
}

// wait 等待本账号正在进行的登录结束。ctx 取消时提前返回 false。
func (g *loginGate) wait(ctx context.Context) bool {
	g.mu.Lock()
	ch := g.done
	g.mu.Unlock()
	if ch == nil {
		return true
	}
	if ctx == nil {
		<-ch
		return true
	}
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

// waitLogin 在业务请求发出前等待本账号的登录结束。
// 返回 false 表示等待期间 ctx 被取消，调用方应放弃本次请求。
func (a *APIClient) waitLogin(ctx context.Context) bool {
	if a == nil {
		return true
	}
	return a.login.gate.wait(ctx)
}

// loginLocked 执行一次登录，调用方必须已持有 a.login.mu。
// 登录期间打开闸门，让同账号的并发请求等结果，而不是拿着过期 cookie 继续打。
func (a *APIClient) loginLocked(ctx context.Context) bool {
	end := a.login.gate.begin()
	defer end()

	// 每次登录都从空阶段开始，避免上一次的残留让 WebUI 显示错误步骤。
	a.resetLoginStage()
	ok := a.doLogin(ctx)
	a.setLoggedIn(ok)
	if ok {
		a.login.lastSuccess = time.Now()
		a.reportLoginStage(LoginPhaseDone, 0, "")
	}
	return ok
}

// inLoginCooldown 报告本账号是否刚登录成功过。调用方需持有 a.login.mu。
func (a *APIClient) inLoginCooldown(now time.Time) bool {
	return !a.login.lastSuccess.IsZero() && now.Sub(a.login.lastSuccess) < loginCooldown
}

// hasSliderCaptcha 返回本账号是否见过滑块验证码。调用方需持有 a.login.mu。
func (a *APIClient) hasSliderCaptcha() bool {
	return a.login.sliderSeen
}

// setSliderCaptcha 记录本账号是否见过滑块验证码。调用方需持有 a.login.mu。
func (a *APIClient) setSliderCaptcha(v bool) {
	a.login.sliderSeen = v
}
