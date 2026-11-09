package jwmock

import (
	"html"
	"strings"
)

// indexPage 是选课首页：GetIndexParams 从这里取公开参数、页签和学分信息。
func indexPage(name, now string) string {
	fields := [][2]string{
		{"firstXkkzId", "mock-xkkz-id"},
		{"firstXkkzXh", ""},
		{"firstKklxdm", "10"},
		{"firstKklxmc", "主修"},
		{"firstNjdmId", "2024"},
		{"firstZyhId", "zy-001"},
		{"bh_id", "20240001"},
		{"xkxnm", "2025"},
		{"xkxqm", "3"},
		{"xqh_id", "1"},
		{"jg_id_1", "01"},
		{"xz", "4"},
		{"zyfx_id", ""},
		{"ccdm", "01"},
		{"xbm", "1"},
		{"mzm", "01"},
		{"xsbj", "01"},
		{"xslbdm", "01"},
		{"xszxzt", "1"},
		{"xxdm", "mock"},
		{"zxfs", ""},
		{"tkzgcs_qt", ""},
		{"currentsj", now},
		{"iskxk", "1"},
		{"xm", name},
	}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>学生选课</title></head><body>`)
	b.WriteString(`<h2>学生选课</h2>`)
	for _, f := range fields {
		// getXpathValue 用 //*[@id="X" or @name="X"] 取 value，id/name 都写上更接近真实页面。
		b.WriteString(`<input type="hidden" id="` + f[0] + `" name="` + f[0] + `" value="` + html.EscapeString(f[1]) + `">`)
	}
	// 学分信息：//h5[contains(., "总学分最低")]/font[@color='red'][1]（最低）与 [2]（最高）。
	b.WriteString(`<h5>总学分最低 <font color="red">2.0</font><font color="red">10.0</font></h5>`)
	b.WriteString(`<font id="yxxfs">0.0</font>`)
	// 页签：onclick 必须以 queryCourse(this,' 开头，4 段即 kklxdm/xkkz_id/njdm_id/zyh_id。
	b.WriteString(`<ul><li><a href="javascript:void(0)" onclick="queryCourse(this,'10','mock-xkkz-id','2024','zy-001')">主修</a></li></ul>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

// tabPage 是页签参数页：GetCourseTabParams 从这里取筛选/选课轮次等参数。
func tabPage() string {
	fields := [][2]string{
		{"bklx_id", "1"},
		{"rwlx", "1"},
		{"xkly", "1"},
		{"xklc", "1"},
		{"xklcmc", "第一轮"},
		{"sfkknj", "0"},
		{"rlkz", "1"},
		{"cdrlkz", "1"},
		{"kkbk", "0"},
		{"kkbkdj", ""},
		{"jxbzcxskg", "0"},
		{"xkxskcgskg", "0"},
		{"sfkcfx", "0"},
		{"sfkkjyxdxnxq", "0"},
		{"gnjkxdnj", ""},
		{"sfkkzy", "0"},
		{"kzybkxy", "0"},
		{"sfznkx", "0"},
		{"zdkxms", ""},
		{"sfkxq", "0"},
		{"bbhzxjxb", "0"},
		{"rlzlkz", "0"},
		{"sfkxk", "1"},
		{"sfktk", "0"},
		{"jdlx", "1"},
		{"syts", "2"},
		{"syxs", "2"},
		{"xkkssj", "2025-09-01 08:00:00"},
		{"xkjssj", "2025-09-30 23:59:59"},
	}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>选课参数</title></head><body>`)
	for _, f := range fields {
		b.WriteString(`<input type="hidden" id="` + f[0] + `" name="` + f[0] + `" value="` + html.EscapeString(f[1]) + `">`)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// loginPage 是登录页。三种形态只在“验证码部分”不同：
//   - none：只有 csrftoken；
//   - kaptcha：#yzmDiv 文本非空，客户端据此走字符验证码；
//   - slider：.captcha_wrapper 命中 hasSliderCap，客户端走滑块。
func loginPage(mode LoginMode, base string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>登录</title></head><body>`)
	b.WriteString(`<form method="post" action="` + base + `/xtgl/login_slogin.html">`)
	b.WriteString(`<input type="hidden" id="csrftoken" name="csrftoken" value="mock-csrf-token">`)
	b.WriteString(`<input type="text" id="yhm" name="yhm" value="">`)
	b.WriteString(`<input type="password" id="mm" name="mm" value="">`)
	switch mode {
	case LoginKaptcha:
		b.WriteString(`<div id="yzmDiv">请输入验证码</div>`)
		b.WriteString(`<img id="kaptchaImg" src="` + base + `/kaptcha" alt="验证码">`)
		b.WriteString(`<input type="text" id="yzm" name="yzm" value="">`)
	case LoginSlider:
		b.WriteString(`<div class="captcha_wrapper"><div id="captcha"></div></div>`)
		b.WriteString(`<script src="` + base + `/zfcaptchaLogin?type=resource&amp;instanceId=zfcaptchaLogin&amp;name=zfdun_captcha.js"></script>`)
	}
	b.WriteString(`<button type="submit">登录</button></form></body></html>`)
	return b.String()
}

// tipsPage 是登录失败页：客户端从 #tips 的文本识别“验证码错误 / 密码错误 /
// 需要滑块验证”。
func tipsPage(msg string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>` +
		`<div id="tips">` + html.EscapeString(msg) + `</div></body></html>`
}

// menuPage 是登录成功后的首页占位。客户端通常不会真的请求它——它只是 302 的
// Location，用来让 isLogin / getRawCsrfToken 认出“已经登录”。
func menuPage() string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body><h2>首页</h2></body></html>`
}
