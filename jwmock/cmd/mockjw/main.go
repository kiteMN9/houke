// Command mockjw 启动本地正方教务模拟服务。
//
//	go run ./jwmock/cmd/mockjw
//	go run ./jwmock/cmd/mockjw -addr 127.0.0.1:8099 -base /jwglxt
//
// 然后在 houke 里用 <业务地址> 作为 BaseURL、用打印的账号密码登录。
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
	user := flag.String("user", jwmock.DefaultUsername, "测试账号")
	pass := flag.String("pass", jwmock.DefaultPassword, "测试密码")
	windowClosed := flag.Bool("window-closed", false, "初始不在选课时间内")
	flag.Parse()

	srv, err := jwmock.New(jwmock.Options{
		BasePath:     *base,
		Users:        []jwmock.User{{Username: *user, Password: *pass}},
		WindowClosed: *windowClosed,
	})
	if err != nil {
		log.Fatalf("jwmock: %v", err)
	}

	fmt.Printf("jwmock 业务地址: http://%s%s/\n", *addr, srv.BasePath())
	fmt.Printf("jwmock 控制面:   http://%s/__mock\n", *addr)
	fmt.Printf("jwmock 账号:     %s / %s\n", *user, *pass)

	log.Printf("jwmock listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatal(err)
	}
}
