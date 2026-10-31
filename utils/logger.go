package utils

import (
	"bufio"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// 只需在init中调用一次初始化
func init() {
	cstZone := time.FixedZone("CST", 8*3600)
	time.Local = cstZone

	// go test（*.test / *.test.exe）下 logger 不生效：
	// 不创建 logs 目录和日志文件、不起后台刷新协程、不接管全局 log 输出，
	// 日志仍按 Go 默认行为写到 stderr。
	// 确实需要在测试里写日志文件时，测试内显式调用 InitLogger 即可。
	if isUnderTest() && !forceLogInTest() {
		return
	}

	//InitLogger()
	timestamp := time.Now().Unix()
	tm := time.Unix(timestamp, 0)
	timeStr := tm.Format("2006-01-02_15-04-05")
	logDir := "logs/"
	if !IsExist(logDir) {
		err := os.Mkdir(logDir, 0755)
		if err != nil {
			return
		}
	}
	file, err := os.OpenFile(logDir+timeStr+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		// 日志文件打不开就降级到 stderr，不该让整个进程退出。
		log.Println("无法打开日志文件，日志将只输出到控制台:", err)
		return
	}

	//// 设置日志输出到文件（同时保留控制台输出）
	//log.SetOutput(file)
	//InitLogger(os.Stderr, 4096, time.Second)
	InitLogger(file, 4096, 800*time.Millisecond)
}

func IsExist(f string) bool {
	_, err := os.Stat(f)
	return err == nil || os.IsExist(err)
}

// isUnderTest 判断当前进程是否为 go test 构建出来的测试二进制
func isUnderTest() bool {
	if isTestBinaryName(os.Args[0]) {
		return true
	}
	// 兜底：testing 包注册的参数，例如 -test.v
	return flag.Lookup("test.v") != nil
}

// isTestBinaryName 判断可执行文件名是否形如 <pkg>.test / <pkg>.test.exe
func isTestBinaryName(arg0 string) bool {
	name := strings.ToLower(filepath.Base(arg0))
	name = strings.TrimSuffix(name, ".exe")
	return strings.HasSuffix(name, ".test")
}

// forceLogInTest 测试环境下强制启用日志（默认测试下不生效）
func forceLogInTest() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SCHOOL_SDK_LOG_IN_TEST"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

var (
	asyncWriter *asyncLogger
	once        sync.Once
)

// asyncLogger 封装所有日志处理逻辑
type asyncLogger struct {
	bufWriter *bufio.Writer
	lock      sync.Mutex
	flushFreq time.Duration
}

func InitLogger(output *os.File, bufSize int, flushInterval time.Duration) {
	once.Do(func() {
		// 初始化带缓冲的writer
		asyncWriter = &asyncLogger{
			bufWriter: bufio.NewWriterSize(output, bufSize),
			flushFreq: flushInterval,
		}

		// 接管标准log输出
		log.SetOutput(asyncWriter)
		log.SetFlags(log.Ldate | log.Ltime)

		// 启动后台刷新
		go asyncWriter.autoFlush()

		// 注册退出信号处理
		go asyncWriter.handleExitSignal()
	})
}

// Write 实现io.Writer接口
func (a *asyncLogger) Write(p []byte) (n int, err error) {
	a.lock.Lock()
	defer a.lock.Unlock()
	return a.bufWriter.Write(p)
}

// autoFlush 定时刷新缓冲区
func (a *asyncLogger) autoFlush() {
	for range time.Tick(a.flushFreq) {
		a.lock.Lock()
		err := a.bufWriter.Flush()
		if err != nil {
			a.lock.Unlock()
			return
		}
		a.lock.Unlock()
	}
}

// handleExitSignal 处理退出信号
func (a *asyncLogger) handleExitSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	a.lock.Lock()
	err := a.bufWriter.Flush()
	if err != nil {
		a.lock.Unlock()
		return
	}
	a.lock.Unlock()
	//os.Exit(0)
}
