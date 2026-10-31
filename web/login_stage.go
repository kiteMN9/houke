package web

import (
	"fmt"

	"school_sdk/protocol"
)

// loginStageText 把 protocol 的登录阶段翻译成给用户看的一句话。
//
// 未知阶段返回空串：宁可保留 hook 阶段原来的文案，也不要把界面刷成空白。
// 登录阶段的"数据"由 protocol 记账，这里只负责措辞。
func loginStageText(st protocol.LoginStage) string {
	var text string
	switch st.Phase {
	case protocol.LoginPhasePrepare:
		text = "正在准备登录"
	case protocol.LoginPhaseFetchForm:
		text = "正在获取登录页"
	case protocol.LoginPhaseRTK:
		text = "正在获取 RTK"
	case protocol.LoginPhaseCaptchaImage:
		text = "正在获取验证码图片"
	case protocol.LoginPhaseCaptcha:
		switch st.Detail {
		case protocol.CaptchaDetailSlider:
			text = "正在识别滑块验证码"
		case protocol.CaptchaDetailNeedSlider:
			text = "需要滑动验证"
		case protocol.CaptchaDetailKaptchaAuto:
			text = "正在自动识别验证码"
		case protocol.CaptchaDetailKaptchaManual:
			text = "请填写验证码"
		default:
			text = "正在处理验证码"
		}
	case protocol.LoginPhasePublicKey:
		text = "正在加密密码"
	case protocol.LoginPhaseSubmit:
		text = "正在提交账号密码"
	case protocol.LoginPhasePassword:
		text = "密码不正确，等待输入新密码"
	case protocol.LoginPhasePasswordError:
		text = "门户账号或密码错误"
	case protocol.LoginPhaseAccountLocked:
		text = "门户账户已被锁定，请先解锁"
	case protocol.LoginPhaseSessionValid:
		text = "门户会话仍然有效，跳过密码登录"
	case protocol.LoginPhaseSessionExpired:
		text = "门户会话已失效，正在重新登录"
	case protocol.LoginPhaseFailedAttempts:
		text = "服务端记录了失败次数，可能需要验证码"
	case protocol.LoginPhasePortalFailed:
		text = "门户登录未成功"
	case protocol.LoginPhasePortal:
		text = "正在登录统一身份认证"
	case protocol.LoginPhasePortalTicket:
		text = "正在换取门户票据"
	case protocol.LoginPhaseSSO:
		text = "正在跳转教务系统"
	case protocol.LoginPhaseTicket:
		text = "正在换取教务系统登录态"
	case protocol.LoginPhaseQRCode:
		text = "请使用微信扫码登录"
	case protocol.LoginPhaseQRScanned:
		text = "已扫码，请在手机上确认"
	case protocol.LoginPhaseQRExpired:
		text = "二维码已过期，正在刷新"
	case protocol.LoginPhaseQRRefused:
		text = "已取消微信登录"
	case protocol.LoginPhaseDone:
		text = "登录成功"
	default:
		return ""
	}
	// 轮次只在重试时显示，避免"（第 1 次）"这种噪音。
	if st.Attempt > 1 {
		text = fmt.Sprintf("%s（第 %d 次）", text, st.Attempt)
	}
	return text
}
