package utils

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func UserIsLogin(account, html string) bool {
	//log.Println("====UserIsLogin====")
	// 检查是否存在 value="{account}"
	accountPattern := fmt.Sprintf(`value="%s"`, regexp.QuoteMeta(account))
	re1 := regexp.MustCompile(accountPattern)
	if re1.MatchString(html) {
		return true
	}

	// 检查是否存在 id="tips"
	re2 := regexp.MustCompile(`id="tips"`)
	if !re2.MatchString(html) {
		return true
	}

	// 解析HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return false
	}

	// 获取错误提示信息
	errMsg := strings.TrimSpace(doc.Find("#tips").Text())

	if errMsg == "" {
		return false
	}

	if strings.Contains(errMsg, "验证码") {
		log.Println(errMsg)
		return false
	}

	// fmt.Printf("UserIsLogin(): %s\n", errMsg)
	fmt.Println("\r" + errMsg)
	log.Println(errMsg)
	return false
}
