package protocol

import "testing"

// WindowInfo 是 SelectionWindow 的只读视图：上层（engine/web）不能碰
// APIConfig 的小写字段，这里只验证拷贝与 nil 安全。
func TestWindowOf(t *testing.T) {
	if got := WindowOf(nil); got != (WindowInfo{}) {
		t.Fatalf("WindowOf(nil) = %+v，期望零值", got)
	}

	cfg := &APIConfig{}
	cfg.Window.xkkssj = "2026-09-09 12:30:00"
	cfg.Window.xkjssj = "2026-09-23 11:00:00"
	got := WindowOf(cfg)
	want := WindowInfo{
		Xkkssj: "2026-09-09 12:30:00",
		Xkjssj: "2026-09-23 11:00:00",
	}
	if got != want {
		t.Fatalf("WindowOf = %+v，期望 %+v", got, want)
	}
}

// Valid 是"这次参数页解析成功了吗"的判据：关键字段全空按失败处理，
// 失败结果不能进 engine 的页签缓存。
func TestTabParamsValid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*APIConfig)
		want   bool
	}{
		{"零值", func(*APIConfig) {}, false},
		{"只有 bklx_id", func(c *APIConfig) { c.Window.bklx_id = "1" }, true},
		{"只有 rwlx", func(c *APIConfig) { c.Window.rwlx = "2" }, true},
		{"只有 xkly", func(c *APIConfig) { c.Window.xkly = "1" }, true},
		{"只有 syxs", func(c *APIConfig) { c.Window.syxs = "15" }, true},
		{"只有非关键字段", func(c *APIConfig) { c.Window.xkkssj = "2026-09-09 12:30:00" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &APIConfig{}
			tc.mutate(cfg)
			if got := TabParamsOf(cfg).Valid(); got != tc.want {
				t.Fatalf("Valid = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// 按页签缓存的是 TabParamsOf 那一块：首页解析出来的学分统计不能被旧页签的
// 缓存带回去（WithTabParams 只换页签参数）。
func TestTabParamsCopy(t *testing.T) {
	if got := TabParamsOf(nil); got.Valid() {
		t.Fatalf("TabParamsOf(nil) 不该 Valid: %+v", got)
	}

	src := &APIConfig{}
	src.Window.bklx_id = "1"
	src.Window.xkkssj = "2026-09-09 12:30:00"
	src.Window.selectedCredit = "6.5"
	p := TabParamsOf(src)
	if !p.Valid() {
		t.Fatal("解析出 bklx_id 的页签参数应 Valid")
	}

	dst := &APIConfig{}
	dst.Window.minCredit = "2.0"
	got := WithTabParams(dst, p)
	if got.Window.bklx_id != "1" || got.Window.xkkssj != "2026-09-09 12:30:00" {
		t.Fatalf("页签参数没有复制过去: %+v", got.Window)
	}
	if got.Window.minCredit != "2.0" || got.Window.selectedCredit != "" {
		t.Fatalf("首页统计被页签参数覆盖了: %+v", got.Window)
	}
	if dst.Window.bklx_id != "" {
		t.Fatalf("WithTabParams 修改了原配置: %+v", dst.Window)
	}
	if got := WithTabParams(nil, p); got.Window.bklx_id != "1" {
		t.Fatalf("WithTabParams(nil) 丢了页签参数: %+v", got.Window)
	}
}
