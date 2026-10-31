package web

import (
	"testing"
	"time"
)

// 远端发起的登录（ping 自动重登 / 初始化顺带登录）没有前端 promise 驱动，
// 只能靠推送帧知道"要验证码了"：快照帧必须带上 pending。
func TestStreamFrameCarriesPendingInput(t *testing.T) {
	store, id, base := newStreamTestServer(t)
	rt, err := store.runtime(id)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}

	rt.inputMu.Lock()
	rt.pendingInput = &pendingInput{
		kind:      "captcha",
		prompt:    "请输入验证码",
		imageData: []byte{0x89, 0x50, 0x4e, 0x47},
		respCh:    make(chan string, 1),
		createdAt: time.Now(),
	}
	rt.inputMu.Unlock()

	var log frameLog
	closeStream := openStream(t, base+"/api/accounts/"+id+"/events", &log)
	defer closeStream()

	waitFrames(t, &log, 1, 2*time.Second)
	first := log.all()[0]
	if first.Type != "snapshot" {
		t.Errorf("首帧 type = %q, want snapshot", first.Type)
	}
	if first.Pending == nil {
		t.Fatal("首帧没带 pending：远端登录需要验证码时前端不会弹输入框")
	}
	if first.Pending.Kind != "captcha" || first.Pending.Prompt != "请输入验证码" {
		t.Errorf("pending = %+v, want kind=captcha prompt=请输入验证码", first.Pending)
	}
	if first.Pending.ImageB64 == "" {
		t.Error("pending 缺验证码图片 base64")
	}
}
