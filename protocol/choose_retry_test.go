package protocol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"school_sdk/protocol/config"
)

// 尝试用尽必须返回 error。
//
// 以前这里返回 (flag=-5, nil)：engine 既不能把它当"请求失败"，也拿不到原因，
// 失败事件里的文案是空的（ChooseCourseResult.DisplayMsg 对 -5 返回 ""）。
func TestChooseCourseExhaustedReturnsError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != config.ChooseCourse {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream broken"))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	result, err := api.ChooseCourse(context.Background(), IndexInfo{}, SelectionWindow{},
		&CustomCourseDic{Do_jxb_id: "DO-JXB-1", Kch_id: "KCH-1"})

	if err == nil {
		t.Fatal("尝试用尽应返回 error，实际是 nil（上层会当成'教务说没选上'）")
	}
	if got, want := hits.Load(), int32(chooseAttempts); got != want {
		t.Fatalf("请求次数 = %d, want %d", got, want)
	}
	if result.Flag != "-5" {
		t.Fatalf("失败结果 flag = %q, want -5（不能带出误导性 flag）", result.Flag)
	}
	var se *StageError
	if !errors.As(err, &se) || se.Op != "choose" {
		t.Fatalf("err = %v (%T)，期望 *StageError{Op: choose}", err, err)
	}
	if se.Err == nil {
		t.Fatal("StageError 应带上最后一次失败原因，便于定位是 5xx 还是解析失败")
	}
	if !strings.Contains(err.Error(), "连续 3 次失败") {
		t.Fatalf("err = %q，期望带上尝试次数", err.Error())
	}
}

// 第一次就成功时不能被"用尽"分支误伤：仍然只发一次请求、返回 nil error。
func TestChooseCourseSuccessKeepsNilError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != config.ChooseCourse {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte(`{"flag":"1","msg":""}`))
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)
	result, err := api.ChooseCourse(context.Background(), IndexInfo{}, SelectionWindow{},
		&CustomCourseDic{Do_jxb_id: "DO-JXB-1", Kch_id: "KCH-1"})

	if err != nil {
		t.Fatalf("选课成功不该报错: %v", err)
	}
	if !result.OK() {
		t.Fatalf("flag = %q, want 1", result.Flag)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("请求次数 = %d, want 1", got)
	}
}
