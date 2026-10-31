package protocol

import (
	"context"
	"log"
	"strings"
	"time"
)

// KaptchaRecognizer 自动识别字符验证码（登录页出现 #yzmDiv 时要填的那种，
// 正方是 6 位字母数字）。
//
// 注入到 APIClient.Kaptcha 后，登录流程先自己识别，识别不了（低置信度、坏图、
// 实现报错）再退回 InputHandler 等人工输入；不注入（nil）就是原来的纯人工行为。
//
// 实现必须尊重 ctx：登录有超时，也有用户点"取消"。img 是 GET /kaptcha 返回的
// 原始图片字节（PNG/JPEG），返回的 text 可以带空白，这里会 trim。
type KaptchaRecognizer interface {
	RecognizeKaptcha(ctx context.Context, img []byte) (text string, confidence float64, err error)
}

// kaptchaEnabled 报告识别器当前是否可用。
//
// 这是一个可选接口：实现方可以在运行时开关自动识别（WebUI 的模型配置页就是
// 这样做的），没实现的老实现一律按可用处理。关闭时直接回退人工输入，不会
// 每张验证码都调用一次 Recognize 再打一条“未启用”日志。
func kaptchaEnabled(r KaptchaRecognizer) bool {
	if r == nil {
		return false
	}
	if e, ok := r.(interface{ KaptchaEnabled() bool }); ok {
		return e.KaptchaEnabled()
	}
	return true
}

// maxKaptchaAutoFails 是自动识别的结果被服务端判错几次后改人工输入。
//
// 连续错两次基本可以断定"这个学校的验证码和模型对不上"（换字体、加干扰线、
// 位数不同、带了日期水印……）。继续自动填只会把登录的重试次数耗光，
// 而人工输入一次就能过，所以宁可早点转人工。
const maxKaptchaAutoFails = 2

// kaptchaPlan 记录"这一轮登录里字符验证码由谁来填"。
//
// 状态按一次登录（doLogin）算，不按单次 kaptchaLogin 算：外层还有 15 次重试，
// 如果把"自动识别不行"记在单次尝试上，就会反复回到自动识别。
type kaptchaPlan struct {
	enabled bool // 还允许自动识别
	fails   int  // 自动识别的结果被服务端判错的次数
}

// newKaptchaPlan 在每次登录开始时重建，上一次登录的失败不影响这一次。
func (a *APIClient) newKaptchaPlan() *kaptchaPlan {
	return &kaptchaPlan{enabled: kaptchaEnabled(a.Kaptcha)}
}

// auto 报告这次是否该走自动识别。
func (p *kaptchaPlan) auto() bool { return p != nil && p.enabled }

// failed 记录一次"自动识别的验证码被判错"；达到上限后本轮登录改用人工输入。
func (p *kaptchaPlan) failed() {
	if p == nil || !p.enabled {
		return
	}
	p.fails++
	if p.fails < maxKaptchaAutoFails {
		return
	}
	p.enabled = false
	log.Printf("字符验证码自动识别连续 %d 次被判错，本轮登录改为人工输入", p.fails)
}

// recognizeKaptcha 让 Kaptcha 识别一张验证码；第二个返回值表示是否拿到了可用结果。
//
// 识别失败只记日志、不打断登录：低置信度、坏图、实现报错都只是"这张没认出来"，
// 重取一张或者让用户输入都能继续。
func (a *APIClient) recognizeKaptcha(ctx context.Context, img []byte) (string, bool) {
	if !kaptchaEnabled(a.Kaptcha) {
		return "", false
	}
	a.reportLoginStage(LoginPhaseCaptcha, 0, CaptchaDetailKaptchaAuto)
	start := time.Now()
	text, confidence, err := a.Kaptcha.RecognizeKaptcha(ctx, img)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		log.Printf("字符验证码自动识别未通过（%s），改为人工输入: %v", elapsed, err)
		return "", false
	}
	text = strings.TrimSpace(text)
	if !validKaptchaText(text) {
		log.Printf("字符验证码自动识别的结果不像验证码（%q），改为人工输入", text)
		return "", false
	}
	log.Printf("字符验证码自动识别成功：%s（置信度 %.3f，用时 %s）", text, confidence, elapsed)
	return text, true
}

// validKaptchaText 粗筛识别结果：正方字符验证码一般是 4~6 位字母数字，
// 这里放宽到 4~8 位以兼容其他学校；空串、带空格或标点、长度明显不对的结果
// 不该被当成验证码提交。
func validKaptchaText(s string) bool {
	if n := len(s); n < 4 || n > 8 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}
