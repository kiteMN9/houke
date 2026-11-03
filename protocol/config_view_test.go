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
