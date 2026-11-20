package protocol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"school_sdk/protocol/config"
)

// 尝试用尽必须返回 error。
//
// 以前这里返回 (nil, nil)：上层（engine.chosenFilterOf / WebUI 已选列表）会把
// "名单没拿到"当成"一门都没选"，自动选课还会因此把已选过滤器清空。
func TestGetSelectedExhaustedReturnsError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != config.CourseSelectedList {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream broken"))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	list, err := api.GetSelected(context.Background(), "2026", "3")

	if err == nil {
		t.Fatal("尝试用尽应返回 error，实际是 nil（上层会当成'一门都没选'）")
	}
	if got, want := hits.Load(), int32(selectedAttempts); got != want {
		t.Fatalf("请求次数 = %d, want %d", got, want)
	}
	if list != nil {
		t.Fatalf("失败时不应返回名单，得到 %d 条", len(list))
	}
	var se *StageError
	if !errors.As(err, &se) || se.Op != "selected" {
		t.Fatalf("err = %v (%T)，期望 *StageError{Op: selected}", err, err)
	}
	if se.Err == nil {
		t.Fatal("StageError 应带上最后一次失败原因，便于定位是 5xx 还是解析失败")
	}
}

// 一门都没选上时教务可能回空响应体：这是"没有已选课程"，不能报错、也不该重试。
func TestGetSelectedEmptyBodyIsNoCourses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != config.CourseSelectedList {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusOK) // 空响应体
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	list, err := api.GetSelected(context.Background(), "2026", "3")

	if err != nil {
		t.Fatalf("空响应体应视为没有已选课程，实际报错: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("名单 = %d 条, want 0", len(list))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("空名单不该重试，共 %d 次请求", got)
	}
}

// 正常响应仍然只发一次请求、返回 nil error（防止"用尽"分支误伤正常路径）。
func TestGetSelectedSuccessKeepsNilError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != config.CourseSelectedList {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"jxb_id":"JXB-1","kch_id":"KCH-1","kcmc":"高等数学"}]`))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	list, err := api.GetSelected(context.Background(), "2026", "3")

	if err != nil {
		t.Fatalf("正常响应不该报错: %v", err)
	}
	if len(list) != 1 || list[0].JxbId != "JXB-1" {
		t.Fatalf("名单 = %+v, want 1 条 JXB-1", list)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("请求次数 = %d, want 1", got)
	}
}
