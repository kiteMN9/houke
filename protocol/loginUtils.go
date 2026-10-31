package protocol

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log"
	"regexp"
	"school_sdk/protocol/config"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// 内部使用：ctx 检查
func (a *APIClient) loginCtxOK(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		log.Println("[login] 已被取消")
		return false
	default:
		return true
	}
}

func hasSliderCap(doc *goquery.Document) bool {
	return doc.Find(".captcha_wrapper").Length() > 0 ||
		doc.Find("script[src*='zfdun_captcha.js']").Length() > 0 ||
		doc.Find("link[href*='zfdun_captcha.css']").Length() > 0
}

func isLogin(account, html string) (bool, error) {
	if html == "" {
		return false, nil
	}
	if account == "" {
		return false, IncorrectPassword
	}
	accountPattern := `value="` + regexp.QuoteMeta(account) + `"`
	re1 := regexp.MustCompile(accountPattern)
	if re1.MatchString(html) {
		return true, nil
	}

	// 解析HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return false, nil
	}

	// 获取错误提示信息
	errMsg := strings.TrimSpace(doc.Find("#tips").Text())

	if errMsg == "" {
		return false, nil
	}

	if strings.Contains(errMsg, "验证码输入错误！") {
		log.Println(errMsg)
		return false, InputYzmErr
	}

	fmt.Println(errMsg)
	log.Println(errMsg)
	if strings.Contains(errMsg, "用户名或密码不正确") {
		return false, IncorrectPassword
	}
	if strings.Contains(errMsg, "请先滑动图片进行验证！") {
		return false, ExistVerify
	}
	return false, nil
}

func generateLoginExtend(UserAgent string) []byte {
	// 后端统一做 UA 合法性校验：能被 strings.Cut(UserAgent, "/") 正常切分，
	// 否则回落到默认 UA（config.DefaultUA）。
	UserAgent = config.NormalizeUA(UserAgent)
	// 查找第一个 '/' 的位置
	_, after, ok := strings.Cut(UserAgent, "/")
	modifiedUserAgent := UserAgent

	if ok {
		// 截取第一个 '/' 之后的内容
		modifiedUserAgent = after
	}

	// 创建 JSON 结构体
	loginExtend := struct {
		AppName    string `json:"appName"`
		UserAgent  string `json:"userAgent"`
		AppVersion string `json:"appVersion"`
	}{
		AppName:    "Netscape",
		UserAgent:  UserAgent,
		AppVersion: modifiedUserAgent,
	}

	// 序列化为 JSON 字符串
	jsonBytes, _ := json.Marshal(loginExtend)
	//LoginExtend := string(jsonBytes)
	return jsonBytes
}
