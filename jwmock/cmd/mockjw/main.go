// Command mockjw 启动一个本地正方教务模拟服务，用来手工调试 houke 的
// 登录 / 自动选课流程。
//
//	go run ./jwmock/cmd/mockjw -addr 127.0.0.1:8099
//
// 然后把 houke 里的账号 BaseURL 配成 http://127.0.0.1:8099/jwglxt/，
// 账号 20250001 / 密码 test123。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"school_sdk/jwmock"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "监听地址")
	base := flag.String("base", jwmock.DefaultBasePath, "业务路径前缀")
	mode := flag.String("mode", string(jwmock.LoginNone), "登录形态：none / kaptcha / slider")
	user := flag.String("user", jwmock.DefaultUsername, "测试账号")
	pass := flag.String("pass", jwmock.DefaultPassword, "测试密码")
	kaptcha := flag.String("kaptcha", jwmock.DefaultKaptchaCode, "字符验证码答案")
	flag.Parse()

	srv, err := jwmock.New(jwmock.Options{
		BasePath:    *base,
		Username:    *user,
		Password:    *pass,
		LoginMode:   jwmock.LoginMode(*mode),
		KaptchaCode: *kaptcha,
	})
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}

	fmt.Printf("jwmock 已启动\n")
	fmt.Printf("  业务地址: http://%s%s/\n", *addr, srv.BasePath())
	fmt.Printf("  账号密码: %s / %s\n", *user, *pass)
	fmt.Printf("  登录形态: %s（验证码答案 %s）\n", *mode, *kaptcha)
	fmt.Printf("  控制面:   http://%s/__mock/state\n", *addr)

	log.Fatal(http.ListenAndServe(*addr, srv))
}
