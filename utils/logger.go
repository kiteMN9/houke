package utils

import (
	"bufio"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// file 是 InitLogger 传入的输出文件。退出时 CloseLogger 要真正关掉它，
	// 仅 Flush 缓冲不够——文件没关，最后一段日志仍可能留在操作系统缓冲区。
	file *os.File
}

func InitLogger(output *os.File, bufSize int, flushInterval time.Duration) {
	once.Do(func() {
		// 初始化带缓冲的writer
		asyncWriter = &asyncLogger{
			bufWriter: bufio.NewWriterSize(output, bufSize),
			flushFreq: flushInterval,
			file:      output,
		}

		// 接管标准log输出
		log.SetOutput(asyncWriter)
		log.SetFlags(log.Ldate | log.Ltime)

		// 启动后台刷新
		go asyncWriter.autoFlush()
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

// FlushLogger 把缓冲区里还没落盘的日志刷到文件。强制退出路径（第二次
// Ctrl+C）只能尽力而为：不关文件、也不等后台的定时刷新协程。
func FlushLogger() {
	if asyncWriter == nil {
		return
	}
	asyncWriter.lock.Lock()
	defer asyncWriter.lock.Unlock()
	_ = asyncWriter.bufWriter.Flush()
}

var closeOnce sync.Once

// CloseLogger 刷新缓冲并关闭日志文件，进程退出前调用；可重复调用。
//
// 旧实现是在包 init 里 signal.Notify 等 Ctrl+C 来刷日志，副作用是整个进程
// 的默认 Ctrl+C 终止行为被关掉：第一次 Ctrl+C 只刷日志、进程不退，之后
// 再按也没人接收。退出信号现在由 main 统一处理，这里只负责"退出前把日志
// 写完、把文件关掉"。
func CloseLogger() {
	if asyncWriter == nil {
		return
	}
	closeOnce.Do(func() {
		asyncWriter.lock.Lock()
		defer asyncWriter.lock.Unlock()
		_ = asyncWriter.bufWriter.Flush()
		f := asyncWriter.file
		// stdout/stderr 是进程级共享句柄，不归 logger 关闭。
		if f == nil || f == os.Stdout || f == os.Stderr {
			return
		}
		_ = f.Close()
	})
}
