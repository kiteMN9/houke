package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"school_sdk/protocol/config"
)

// 初始化阶段必须按真实链路顺序上报：取首页 → 解析页签 → 取列表参数 → 解析规则。
// 这里不 mock 阶段调用，直接跑 GetIndexParams / GetCourseListPre。
func TestInitReportsSemanticStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case config.ChooseCourseIndex:
			// 最小可用页面：不含 #tips，登录态判定通过；带一个 xkkz_id
			// 让 parseYzbIndexHtml 认为页面结构正常；解析函数对缺字段是宽容的。
			_, _ = w.Write([]byte(`<html><body><input id="firstXkkzId" value="test"></body></html>`))
		case config.ChooseCourseTabParams:
			_, _ = w.Write([]byte(`<html><body><div id="x">ok</div></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	api := newStateTestClient(t, srv.URL)

	var mu sync.Mutex
	var got []InitPhase
	api.SetInitObserver(func(st InitStage) {
		mu.Lock()
		got = append(got, st.Phase)
		mu.Unlock()
	})

	cfg := &APIConfig{}
	if err := api.GetIndexParams(context.Background(), cfg, IndexOptions{}); err != nil {
		t.Fatalf("GetIndexParams: %v", err)
	}
	if err := api.GetCourseTabParams(context.Background(), cfg, false); err != nil {
		t.Fatalf("GetCourseTabParams: %v", err)
	}

	want := []InitPhase{
		InitPhaseIndex,
		InitPhaseIndexParse,
		InitPhaseListPre,
		InitPhaseListPreParse,
	}
	if len(got) != len(want) {
		t.Fatalf("阶段序列 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("阶段序列 = %v, want %v", got, want)
		}
	}
	if st := api.InitStage(); st.Phase != InitPhaseListPreParse {
		t.Errorf("最终阶段 = %q, want %q", st.Phase, InitPhaseListPreParse)
	}
}
