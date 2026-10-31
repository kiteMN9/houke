package protocol

import (
	"context"
	"errors"
	baseCfg "school_sdk/protocol/config"
	"school_sdk/utils"
	"time"
)

func SleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop() // 确保函数退出时停止定时器，避免泄露

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PingResult 描述一次 ping（登录状态探测）的结果。
type PingResult struct {
	OK        bool   `json:"ok"`        // 请求本身是否成功（服务器可达）
	LoggedIn  bool   `json:"loggedIn"`  // 会话是否仍然有效
	Status    int    `json:"status"`    // HTTP 状态码
	LatencyMs int64  `json:"latencyMs"` // 往返耗时（毫秒）
	Message   string `json:"message"`   // 失败原因 / 补充说明
}

// Ping 探测一次教务系统的登录状态，不触发重新登录。
// 逻辑参考 detectTime：先看是否 302 掉登录页，再看响应体里是否还有当前账号。
//
// 探测结果会同步到 client 自己的登录态（a.LoggedIn）：拿到响应后才据此更新，
// 请求本身失败（断网 / 超时）不动状态——网络抖动不等于掉线。
func (a *APIClient) Ping(ctx context.Context) *PingResult {
	start := time.Now()
	resp, err := a.Http.R().
		SetRetryCount(0).
		SetContext(ctx).
		SetTimeout(15 * time.Second).
		Get(baseCfg.LoginStatus)

	res := &PingResult{LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Message = "请求已取消"
		} else if errors.Is(err, context.DeadlineExceeded) {
			res.Message = "请求超时"
		} else {
			res.Message = err.Error()
		}
		return res
	}

	res.Status = resp.StatusCode()

	if a.CheckLogout302(resp) {
		res.Message = "会话已失效（被重定向到登录页）"
		a.setLoggedIn(false)
		return res
	}

	if utils.UserIsLogin(a.cfg.Account, resp.String()) {
		res.OK = true
		res.LoggedIn = true
		res.Message = "会话有效"
		a.setLoggedIn(true)
		return res
	}

	res.OK = true
	res.Message = "服务器可达，但当前未登录"
	a.setLoggedIn(false)
	return res
}
