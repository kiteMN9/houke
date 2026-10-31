package config

import "strings"

const (
	EdgeUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36 Edg/154.0.0.0"
	ChromeUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"
	FireFoxUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:157.0) Gecko/20100101 Firefox/157.0"
	ApppleUA  = "Mozilla/5.0 (Macintosh; Apple macOS 26_1_0) AppleWebKit/537.36 (KHTML, like Gecko) Safari/537.36 Chrome/152.0.0.0"
	//USERAGENT = "Mozilla/5.0 (iPad; CPU OS 8_0_2 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) Version/8.0 Mobile/12A405 Safari/600.1.4"

	// DefaultUA 是非法 / 缺失 User-Agent 时统一回落的默认值。
	DefaultUA = FireFoxUA
)

// CheckUALegal 判断 User-Agent 是否合法。
// 合法性要求：非空，且能被 strings.Cut(UA, "/") 正常切分——
// 即必须包含至少一个 '/'，且 '/' 两侧都有非空白内容。
// 原因：登录扩展字段用第一个 '/' 之后的内容作为 appVersion，
// 形如 “Mozilla/5.0 (...)” 的 UA 才能得到有意义的 appVersion。
func CheckUALegal(UA string) bool {
	if strings.TrimSpace(UA) == "" {
		return false
	}
	before, after, ok := strings.Cut(UA, "/")
	if !ok {
		return false
	}
	return strings.TrimSpace(before) != "" && strings.TrimSpace(after) != ""
}

// NormalizeUA 返回合法 UA；非法时返回 DefaultUA。
func NormalizeUA(UA string) string {
	if !CheckUALegal(UA) {
		return DefaultUA
	}
	return UA
}

//if(intBro.msie==true||intBro.safari==true||intBro.mozilla==true||intBro.chrome==true){
//if(intBro.msie==true&&intBro.version<9){
//window.location.href = _path+"/xtgl/init_cxBrowser.html";
//}
