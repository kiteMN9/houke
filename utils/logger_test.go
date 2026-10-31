package utils

import (
	"log"
	"os"
	"testing"
)

// go test 环境下 logger 应当不生效
func TestLoggerDisabledUnderTest(t *testing.T) {
	if !isUnderTest() {
		t.Fatal("在 go test 二进制里 isUnderTest() 应为 true")
	}
	if asyncWriter != nil {
		t.Fatal("go test 环境下 asyncWriter 不应被初始化")
	}
	if _, ok := log.Writer().(*asyncLogger); ok {
		t.Fatal("go test 环境下全局 log 输出不应被 logger 接管")
	}
	if log.Writer() != os.Stderr {
		t.Fatalf("go test 环境下 log 输出应保持默认的 os.Stderr，当前为 %T", log.Writer())
	}
}

func TestIsTestBinaryName(t *testing.T) {
	cases := []struct {
		arg0 string
		want bool
	}{
		{`C:\code\school_sdk_go\utils.test.exe`, true},
		{`/tmp/go-build123/b001/utils.test`, true},
		{`utils.test`, true},
		{`latest.exe`, false}, // 不能因为结尾是 test 就误判
		{`C:\code\school_sdk_go\school_sdk.exe`, false},
		{`school_sdk`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isTestBinaryName(c.arg0); got != c.want {
			t.Errorf("isTestBinaryName(%q) = %v, want %v", c.arg0, got, c.want)
		}
	}
}

func TestForceLogInTest(t *testing.T) {
	t.Setenv("SCHOOL_SDK_LOG_IN_TEST", "1")
	if !forceLogInTest() {
		t.Fatal("SCHOOL_SDK_LOG_IN_TEST=1 时应强制启用日志")
	}
	t.Setenv("SCHOOL_SDK_LOG_IN_TEST", "")
	if forceLogInTest() {
		t.Fatal("未设置环境变量时不应强制启用日志")
	}
}
