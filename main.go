package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"school_sdk/check_code/kaptcha"
	"school_sdk/utils"
	"school_sdk/web"
)

var version = "school_sdk_go 2.0.0"

func main() {
	// 版本信息
	verFlag := flag.Bool("V", false, version)

	webAddr := flag.String("webAddr", "127.0.0.1:8080", "WebUI 监听地址")

	// 解析命令行参数
	flag.Parse()
	if *verFlag {
		fmt.Printf("%s\n", version)
		os.Exit(0)
	}
	utils.Exit()

	fmt.Println(version)
	store, err := web.NewAccountStore("accounts.json")
	if err != nil {
		log.Fatalf("加载本地数据失败: %v", err)
	}

	// 字符验证码识别模型由 WebUI 的「验证码模型」页配置（kaptcha.json）。
	// 这里只尝试加载一次；没模型、模型坏了都只提示，不阻止 WebUI 启动。
	mgr := store.Kaptcha()
	if err := mgr.Reload(); err != nil {
		if errors.Is(err, kaptcha.ErrNoModel) {
			fmt.Println("字符验证码：未找到模型，可在 WebUI 的「验证码模型」页上传或指定路径")
		} else {
			log.Printf("字符验证码：加载模型失败，可在 WebUI 的「验证码模型」页检查: %v", err)
		}
	} else if st := mgr.Status(); st.Enabled {
		fmt.Printf("字符验证码：自动识别已启用（%s，置信度下限 %.2f）\n", st.ModelPath, st.Settings.Threshold)
	} else {
		fmt.Println("字符验证码：自动识别已关闭，遇到字符验证码时人工输入")
	}

	srv, err := web.NewServer(store)
	if err != nil {
		log.Fatalf("启动 WebUI 失败: %v", err)
	}
	fmt.Printf("WebUI 已启动: http://%s\n", *webAddr)
	if err := srv.Run(*webAddr); err != nil {
		fmt.Println(err)
		return
	}
}
