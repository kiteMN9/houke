package cas2

// Step 是 cas2 门户登录流程内部的机器可读状态。
//
// 这个流程由几次"看不见"的请求组成：取登录页、取 RSA 公钥加密密码、提交表单、
// 换门户票据。它们的进展与失败以前只打印到控制台，宿主的任务面板只能显示一条
// "正在登录统一身份认证"——重试了几轮、卡在取公钥还是账户被锁，全都不可见。
//
// Step 只描述"发生了什么"，不携带任何文案：给用户看的中文由上层决定
// （protocol 的 cas2StepPhase + web 的 loginStageText），与 QRStatus 的分工一致。
type Step string

const (
	// StepSessionValid 已有门户会话仍然有效，跳过密码登录。
	StepSessionValid Step = "session-valid"
	// StepFetchForm 正在取登录页（提取 execution / failN）。
	StepFetchForm Step = "fetch-form"
	// StepFailedAttempts 登录页带着失败次数：服务端可能要求验证码。
	StepFailedAttempts Step = "failed-attempts"
	// StepPublicKey 正在取 RSA 公钥并加密密码。
	StepPublicKey Step = "public-key"
	// StepSubmit 正在提交账号密码。
	StepSubmit Step = "submit"
	// StepPasswordError 服务端返回 401：账号或密码错误。
	StepPasswordError Step = "password-error"
	// StepAccountLocked 服务端提示账户已被锁定。
	StepAccountLocked Step = "account-locked"
	// StepLoginFailed 登录未成功：服务端拒绝、票据缺失、换票失败或 5xx。
	StepLoginFailed Step = "login-failed"
	// StepSessionExpired 门户会话已失效，正在重新登录；微信模式需重新扫码。
	StepSessionExpired Step = "session-expired"
)

// StepHandler 接收登录流程内部的状态变化。
// attempt 是当前重试轮次（从 1 开始）；0 表示不适用。
// 回调在登录 goroutine 上同步执行，必须快速返回、不得阻塞。
type StepHandler func(step Step, attempt int)

// SetStepHandler 挂载登录步骤回调；传 nil 取消。
func (c *Client) SetStepHandler(h StepHandler) {
	if c == nil {
		return
	}
	c.stepMu.Lock()
	c.stepHandler = h
	c.stepMu.Unlock()
}

// emitStep 在登录 goroutine 上同步上报一次步骤。nil client / 未挂回调时是空操作。
func (c *Client) emitStep(step Step, attempt int) {
	if c == nil || step == "" {
		return
	}
	c.stepMu.RLock()
	h := c.stepHandler
	c.stepMu.RUnlock()
	if h != nil {
		h(step, attempt)
	}
}
