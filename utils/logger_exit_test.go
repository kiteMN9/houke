package utils

import (
	"bufio"
	"os"
	"testing"
	"time"
)

// newTestAsyncLogger 构造一个不启动定时刷新的 asyncLogger，直接用临时文件，
// 避免碰 InitLogger 的全局单例（once 在整个测试进程里只能初始化一次）。
func newTestAsyncLogger(t *testing.T) (*asyncLogger, *os.File) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "logger-*.log")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	// 文件在测试结束时可能仍开着（只测 Flush 的用例不关闭它），Windows 上
	// 会拦住 TempDir 的 RemoveAll，所以统一兜底关闭（重复关闭只返回错误）。
	t.Cleanup(func() { _ = f.Close() })
	a := &asyncLogger{
		bufWriter: bufio.NewWriterSize(f, 64),
		flushFreq: time.Hour,
		file:      f,
	}
	return a, f
}

// useAsyncWriter 把构造出来的 logger 临时挂到全局变量上，测试结束后还原。
// 测试是串行的，不存在并发访问；TestLoggerDisabledUnderTest 依赖退出时
// asyncWriter 恢复为 nil。
func useAsyncWriter(t *testing.T, a *asyncLogger) {
	t.Helper()
	old := asyncWriter
	asyncWriter = a
	t.Cleanup(func() { asyncWriter = old })
}

func TestFlushLoggerWritesBufferedData(t *testing.T) {
	a, f := newTestAsyncLogger(t)
	useAsyncWriter(t, a)

	if _, err := a.Write([]byte("hello flush\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	FlushLogger()

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); got != "hello flush\n" {
		t.Fatalf("落盘内容 = %q, want %q", got, "hello flush\n")
	}
}

// CloseLogger 是进程退出前的最后一步：必须把缓冲刷完并真正关闭文件，
// 而不是像旧的 handleExitSignal 那样只是 flush 一下、还顺手接管了 Ctrl+C。
func TestCloseLoggerFlushesAndCloses(t *testing.T) {
	a, f := newTestAsyncLogger(t)
	useAsyncWriter(t, a)

	if _, err := a.Write([]byte("bye\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	CloseLogger()

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); got != "bye\n" {
		t.Fatalf("落盘内容 = %q, want %q", got, "bye\n")
	}

	// 文件确实被关掉了（Windows 上对已关闭句柄再写会报错）。
	if _, err := f.WriteString("x"); err == nil {
		t.Fatal("CloseLogger 之后日志文件仍可写：文件没有关闭")
	}

	// 可重复调用：退出路径上可能有多处兜底。
	CloseLogger()
	CloseLogger()
}

// 日志未初始化时（例如 go test 默认不启用文件日志）退出函数必须是 no-op，
// 不能 panic，也不能去关 stdout/stderr。
func TestLoggerExitFuncsNoopWithoutInit(t *testing.T) {
	old := asyncWriter
	asyncWriter = nil
	t.Cleanup(func() { asyncWriter = old })

	FlushLogger()
	CloseLogger()
	CloseLogger()
}
