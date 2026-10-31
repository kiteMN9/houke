package config

import (
	"strings"
	"testing"
)

func TestCheckUALegal(t *testing.T) {
	legal := []string{FireFoxUA, ChromeUA, EdgeUA, ApppleUA, "MyApp/1.0", "a/b"}
	for _, ua := range legal {
		if !CheckUALegal(ua) {
			t.Errorf("CheckUALegal(%q) = false, want true", ua)
		}
	}

	illegal := []string{"", "   ", "no-slash-here", "/1.0", "MyApp/", "  /  "}
	for _, ua := range illegal {
		if CheckUALegal(ua) {
			t.Errorf("CheckUALegal(%q) = true, want false", ua)
		}
	}
}

func TestNormalizeUA(t *testing.T) {
	// 非法 UA 一律回落到默认 UA
	for _, ua := range []string{"", "   ", "no-slash-here", "/1.0", "MyApp/"} {
		if got := NormalizeUA(ua); got != DefaultUA {
			t.Errorf("NormalizeUA(%q) = %q, want %q", ua, got, DefaultUA)
		}
	}

	// 合法 UA 原样返回
	for _, ua := range []string{FireFoxUA, ChromeUA, EdgeUA, "MyApp/1.0"} {
		if got := NormalizeUA(ua); got != ua {
			t.Errorf("NormalizeUA(%q) = %q, want unchanged", ua, got)
		}
	}

	// 归一化结果必须总能被 strings.Cut(ua, "/") 正常切分
	for _, ua := range []string{"", "no-slash-here", FireFoxUA} {
		before, after, ok := strings.Cut(NormalizeUA(ua), "/")
		if !ok || before == "" || after == "" {
			t.Errorf("NormalizeUA(%q) 无法被 strings.Cut 处理: before=%q after=%q ok=%v",
				ua, before, after, ok)
		}
	}
}
