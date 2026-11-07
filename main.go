package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"school_sdk/check_code/kaptcha"
	"school_sdk/utils"
	"school_sdk/web"
)

var version = "school_sdk_go 2.2.0"

func main() {
	code := run()
	// 日志是异步带缓冲的：退出前统一刷新并关闭文件，最后的启动错误 / 停止
	// 日志才不会丢。run 内部不再自己 os.Exit。
	utils.CloseLogger()
	os.Exit(code)
}

// run 执行启动流程并返回进程退出码：0 正常退出，1 启动或运行失败。
//
// 退出信号只在这里统一处理：
//   - 第一次 Ctrl+C（或 SIGTERM）：取消 context，WebUI 优雅收尾——取消
//     运行中的登录/初始化/自动选课任务、断开 SSE、等在途请求结束、日志落盘；
//   - 第二次 Ctrl+C：不再等待，刷新日志后立即退出，防止某个不响应
//     context 的请求把退出卡死。旧版本正因为没有可用的退出路径，用户
//     只能关窗口或 taskkill。
func run() int {
	// 版本信息
	verFlag := flag.Bool("V", false, version)

	webAddr := flag.String("webAddr", "127.0.0.1:8080", "WebUI 监听地址")

	// 解析命令行参数
	flag.Parse()
	if *verFlag {
		fmt.Printf("%s\n", version)
		return 0
	}

	fmt.Println(version)
	store, err := web.NewAccountStore("accounts.json")
	if err != nil {
		return fail("加载本地数据失败", err)
	}

	// 字符验证码识别模型由 WebUI 的「服务端配置 → 验证码模型」页配置（kaptcha.json）。
	// 这里只尝试加载一次；没模型、模型坏了都只提示，不阻止 WebUI 启动。
	mgr := store.Kaptcha()
	if err := mgr.Reload(); err != nil {
		if errors.Is(err, kaptcha.ErrNoModel) {
			fmt.Println("字符验证码：未找到模型，可在 WebUI 的「服务端配置 → 验证码模型」页上传或指定路径")
		} else {
			log.Printf("字符验证码：加载模型失败，可在 WebUI 的「服务端配置 → 验证码模型」页检查: %v", err)
		}
	} else if st := mgr.Status(); st.Enabled {
		fmt.Printf("字符验证码：自动识别已启用（%s，置信度下限 %.2f）\n", st.ModelPath, st.Settings.Threshold)
	} else {
		fmt.Println("字符验证码：自动识别已关闭，遇到字符验证码时人工输入")
	}

	srv, err := web.NewServer(store)
	if err != nil {
		return fail("启动 WebUI 失败", err)
	}

	// 先把端口绑上再打印"已启动"：端口被占用时不会出现假成功。
	ln, err := srv.Listen(*webAddr)
	if err != nil {
		return fail("启动 WebUI 失败", err)
	}

	// 退出信号统一在这里处理：第一次 Ctrl+C（或 SIGTERM）取消 ctx，走优雅
	// 退出；第二次 Ctrl+C 不再等待，强制退出。
	//
	// 这里用显式 channel 而不是 signal.NotifyContext：后者内部只缓存一次
	// 信号，用户连按两下时第二次可能被吞掉，又变成"按了没反应"。容量 2 的
	// channel 能稳稳接住两下。
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchExitSignals(cancel, sigCh)

	fmt.Printf("WebUI 已启动: http://%s\n", ln.Addr())
	fmt.Println("按 Ctrl+C 退出：第一次安全退出，再按一次强制退出")

	if err := srv.Serve(ctx, ln); err != nil {
		return fail("WebUI 运行出错", err)
	}
	fmt.Println("已安全退出")
	return 0
}

// watchExitSignals 处理退出信号。
//
// 第一次：提示并取消 context，交给 srv.Serve / Shutdown 收尾（取消运行中
// 的任务、断开 SSE、等在途请求结束、日志落盘）。
//
// 第二次：优雅退出理论上可能卡住（例如某个 handler 不响应 context），
// 这里保证用户永远有第二条退路——刷新日志后直接退出。
func watchExitSignals(cancel context.CancelFunc, sigCh <-chan os.Signal) {
	<-sigCh
	fmt.Println()
	fmt.Println("正在安全退出：取消运行中的任务、关闭 WebUI…（再按一次 Ctrl+C 可强制退出）")
	cancel()

	<-sigCh
	fmt.Println()
	fmt.Println("强制退出：未等待任务收尾。")
	utils.FlushLogger()
	os.Exit(1)
}

// fail 把启动/运行错误同时写到控制台（stderr）和日志，返回退出码 1。
//
// 不用 log.Fatalf：日志是异步带缓冲的，os.Exit 会跳过刷新，控制台上
// 只剩一行看不懂的错误、日志文件里反倒没有。
func fail(msg string, err error) int {
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	log.Printf("%s: %v", msg, err)
	return 1
}
