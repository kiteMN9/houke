package protocol

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// notOpenIndexHTML 模拟"当前不在选课阶段"：iskxk=0 + nodata 提示 + ver=数字。
const notOpenIndexHTML = `<html><body>
<input id="iskxk" value="0"/>
<div class="nodata"><span>对不起，当前不属于选课阶段，如有需要，请与管理员联系！</span></div>
<!-- ver=2 -->
</body></html>`

// Web 的 /init 不能像 CLI 那样每秒轮询等选课阶段开放：
// WaitForOpen=false 时必须立刻带着原因返回，而不是空转 50 轮。
func TestIndexFailsFastWhenNotInElectiveStage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, notOpenIndexHTML)
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)
	cfg := &APIConfig{}

	start := time.Now()
	err := api.GetIndexParams(context.Background(), cfg, IndexOptions{})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("WaitForOpen=false 仍等待了 %v", elapsed)
	}
	if err == nil {
		t.Fatal("不在选课阶段时应返回错误")
	}

	var stage *StageError
	if !errors.As(err, &stage) {
		t.Fatalf("错误类型应为 *StageError，得到 %T: %v", err, err)
	}
	if stage.Op != "index" {
		t.Errorf("StageError.Op = %q，期望 index", stage.Op)
	}
	if !strings.Contains(stage.Message, "ver=2") {
		t.Errorf("错误信息应带上版本号，得到 %q", stage.Message)
	}
}

// WaitForOpen=true 时保持 CLI 的轮询行为，但必须能被 ctx 打断。
func TestIndexWaitForOpenHonorsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, notOpenIndexHTML)
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	api.setLoggedIn(true)
	cfg := &APIConfig{}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := api.GetIndexParams(ctx, cfg, IndexOptions{WaitForOpen: true})
	if err == nil {
		t.Fatal("ctx 超时时 GetIndexParams 应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误应包装 context.DeadlineExceeded，得到 %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("ctx 取消后没有及时返回，耗时 %v", elapsed)
	}
}
