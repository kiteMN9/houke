package web

import (
	"testing"

	"school_sdk/protocol"
)

func TestLoginStageText(t *testing.T) {
	cases := []struct {
		name string
		st   protocol.LoginStage
		want string
	}{
		{"准备", protocol.LoginStage{Phase: protocol.LoginPhasePrepare}, "正在准备登录"},
		{"取登录页", protocol.LoginStage{Phase: protocol.LoginPhaseFetchForm}, "正在获取登录页"},
		{"RTK", protocol.LoginStage{Phase: protocol.LoginPhaseRTK}, "正在获取 RTK"},
		{"验证码图片", protocol.LoginStage{Phase: protocol.LoginPhaseCaptchaImage}, "正在获取验证码图片"},
		{"滑块验证码", protocol.LoginStage{Phase: protocol.LoginPhaseCaptcha, Detail: protocol.CaptchaDetailSlider}, "正在识别滑块验证码"},
		{"需要滑动", protocol.LoginStage{Phase: protocol.LoginPhaseCaptcha, Detail: protocol.CaptchaDetailNeedSlider}, "需要滑动验证"},
		{"自动识别验证码", protocol.LoginStage{Phase: protocol.LoginPhaseCaptcha, Detail: protocol.CaptchaDetailKaptchaAuto}, "正在自动识别验证码"},
		{"等人工填验证码", protocol.LoginStage{Phase: protocol.LoginPhaseCaptcha, Detail: protocol.CaptchaDetailKaptchaManual}, "请填写验证码"},
		{"验证码兜底", protocol.LoginStage{Phase: protocol.LoginPhaseCaptcha}, "正在处理验证码"},
		{"加密密码", protocol.LoginStage{Phase: protocol.LoginPhasePublicKey}, "正在加密密码"},
		{"提交", protocol.LoginStage{Phase: protocol.LoginPhaseSubmit}, "正在提交账号密码"},
		{"新密码", protocol.LoginStage{Phase: protocol.LoginPhasePassword}, "密码不正确，等待输入新密码"},
		{"门户密码错误", protocol.LoginStage{Phase: protocol.LoginPhasePasswordError}, "门户账号或密码错误"},
		{"门户账户锁定", protocol.LoginStage{Phase: protocol.LoginPhaseAccountLocked}, "门户账户已被锁定，请先解锁"},
		{"门户会话有效", protocol.LoginStage{Phase: protocol.LoginPhaseSessionValid}, "门户会话仍然有效，跳过密码登录"},
		{"门户会话失效", protocol.LoginStage{Phase: protocol.LoginPhaseSessionExpired}, "门户会话已失效，正在重新登录"},
		{"失败次数", protocol.LoginStage{Phase: protocol.LoginPhaseFailedAttempts}, "服务端记录了失败次数，可能需要验证码"},
		{"门户登录未成功", protocol.LoginStage{Phase: protocol.LoginPhasePortalFailed}, "门户登录未成功"},
		{"门户", protocol.LoginStage{Phase: protocol.LoginPhasePortal}, "正在登录统一身份认证"},
		{"门户票据", protocol.LoginStage{Phase: protocol.LoginPhasePortalTicket}, "正在换取门户票据"},
		{"SSO", protocol.LoginStage{Phase: protocol.LoginPhaseSSO}, "正在跳转教务系统"},
		{"换票", protocol.LoginStage{Phase: protocol.LoginPhaseTicket}, "正在换取教务系统登录态"},
		{"二维码", protocol.LoginStage{Phase: protocol.LoginPhaseQRCode}, "请使用微信扫码登录"},
		{"已扫码", protocol.LoginStage{Phase: protocol.LoginPhaseQRScanned}, "已扫码，请在手机上确认"},
		{"二维码过期", protocol.LoginStage{Phase: protocol.LoginPhaseQRExpired}, "二维码已过期，正在刷新"},
		{"拒绝扫码", protocol.LoginStage{Phase: protocol.LoginPhaseQRRefused}, "已取消微信登录"},
		{"完成", protocol.LoginStage{Phase: protocol.LoginPhaseDone}, "登录成功"},
		{"首次重试不显示轮次", protocol.LoginStage{Phase: protocol.LoginPhaseSubmit, Attempt: 1}, "正在提交账号密码"},
		{"重试显示轮次", protocol.LoginStage{Phase: protocol.LoginPhaseSubmit, Attempt: 3}, "正在提交账号密码（第 3 次）"},
	}
	for _, tc := range cases {
		if got := loginStageText(tc.st); got != tc.want {
			t.Errorf("%s: loginStageText(%+v) = %q, want %q", tc.name, tc.st, got, tc.want)
		}
	}
}

// 未知阶段返回空串：web 侧据此选择"不覆盖已有文案"。
func TestLoginStageTextUnknown(t *testing.T) {
	if got := loginStageText(protocol.LoginStage{Phase: "login.unknown"}); got != "" {
		t.Fatalf("未知阶段 = %q, want 空串", got)
	}
	if got := loginStageText(protocol.LoginStage{}); got != "" {
		t.Fatalf("空阶段 = %q, want 空串", got)
	}
}
