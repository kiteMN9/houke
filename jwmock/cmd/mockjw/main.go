// Command mockjw 启动本地正方教务模拟服务。
//
//	go run ./jwmock/cmd/mockjw
//	go run ./jwmock/cmd/mockjw -addr 127.0.0.1:8099 -base /jwglxt
//	go run ./jwmock/cmd/mockjw -config ./jwmock/config.example.json
//
// 然后在 houke 里用 <业务地址> 作为 BaseURL、用打印的账号密码登录。
// -config 指向一个 jwmock.Options 的 JSON 文件，页签和页签课程在同一个文件里配置。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"school_sdk/jwmock"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "监听地址")
	configPath := flag.String("config", "", "JSON 配置文件（jwmock.Options，含 tabs 及每个页签的 courses）")
	base := flag.String("base", jwmock.DefaultBasePath, "业务路径前缀（未用 -config 时生效）")
	user := flag.String("user", jwmock.DefaultUsername, "测试账号（未用 -config 时生效）")
	pass := flag.String("pass", jwmock.DefaultPassword, "测试密码（未用 -config 时生效）")
	windowClosed := flag.Bool("window-closed", false, "初始不在选课时间内（未用 -config 时生效）")
	flag.Parse()

	var opts jwmock.Options
	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("jwmock: 读取配置 %s: %v", *configPath, err)
		}
		if err := json.Unmarshal(raw, &opts); err != nil {
			log.Fatalf("jwmock: 解析配置 %s: %v", *configPath, err)
		}
	} else {
		opts = jwmock.Options{
			BasePath:     *base,
			Users:        []jwmock.User{{Username: *user, Password: *pass}},
			WindowClosed: *windowClosed,
		}
	}

	srv, err := jwmock.New(opts)
	if err != nil {
		log.Fatalf("jwmock: %v", err)
	}

	fmt.Printf("jwmock 业务地址: http://%s%s/\n", *addr, srv.BasePath())
	fmt.Printf("jwmock 控制面:   http://%s/__mock\n", *addr)
	for _, u := range srv.Options().Users {
		fmt.Printf("jwmock 账号:     %s / %s\n", u.Username, u.Password)
	}
	for _, tab := range srv.Options().Tabs {
		if len(tab.Courses) > 0 {
			fmt.Printf("jwmock 页签:     %s（kklxdm=%s，%d 门课）\n", tab.Name, tab.Kklxdm, len(tab.Courses))
		} else {
			fmt.Printf("jwmock 页签:     %s（kklxdm=%s，沿用全局课程）\n", tab.Name, tab.Kklxdm)
		}
	}

	log.Printf("jwmock listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatal(err)
	}
}
