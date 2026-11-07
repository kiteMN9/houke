package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

// autoIndexHTML 是 GetIndexParams 能接受的最小页面：没有登录提示，
// 且带 firstXkkzId/xkxnm/xkxqm，Initialize 可以发布一份配置。
const autoIndexHTML = `<html><body>
<input id="firstXkkzId" value="10"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`

// 异步化的核心约定：POST /auto-choose 只登记任务并返回 202，
// 任务在后台跑，客户端从 /status 或 SSE 读最终结果。
func TestAutoChooseReturnsAcceptedAndRunsInBackground(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case config.ChooseCourseIndex:
			_, _ = io.WriteString(w, autoIndexHTML)
		case config.ChooseCourseTabParams:
			_, _ = io.WriteString(w, `<html><body><div id="x">ok</div></body></html>`)
		case config.ChooseCourseCourseList:
			// 课程列表必须返回 JSON；返回 HTML 会让 GetCourseList
			// 的失败重试循环一直空转，任务迟迟不结束。
			_, _ = io.WriteString(w, `{"msg":"测试环境没有课程","flag":"0"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  upstream.URL + "/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)
	if err := rt.engine.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	srv := mustNewServer(t, store)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+acc.ID+"/auto-choose",
		strings.NewReader(`{"wantClasses":["高等数学"],"durationSeconds":30}`))
	req.Header.Set("Content-Type", "application/json")
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string    `json:"status"`
		Task   *taskView `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是 JSON: %v, body = %s", err, rec.Body.String())
	}
	if resp.Status != "running" {
		t.Fatalf("status = %q, want running", resp.Status)
	}
	if resp.Task == nil || resp.Task.Kind != taskKindAuto {
		t.Fatalf("响应缺少自动选课任务: %+v", resp.Task)
	}

	// 课程列表返回空数据，RunAuto 会以 ErrNoCandidates 很快收尾；
	// 这里验证它确实在后台收尾，并且结果能从任务快照读到。
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap := srv.tasks.snapshot(acc.ID)
		if snap != nil && !snap.Running {
			if snap.Result == nil || snap.Result.Status != "no_candidates" {
				t.Fatalf("任务结果 = %+v，期望 no_candidates", snap.Result)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("自动选课任务没有在 5 秒内结束")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
