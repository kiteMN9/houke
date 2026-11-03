package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"school_sdk/protocol/config"
)

// /status 必须把初始化时解析出的选课时间窗带给前端；
// 字段名与学校页面 hidden input 保持一致（xkkssj/xkjssj）。
func TestStatusIncludesSelectionWindow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case config.ChooseCourseIndex:
			_, _ = io.WriteString(w, `<html><body>
<input id="firstXkkzId" value="10"/>
<input id="firstXkkzXh" value="1"/>
<input id="firstKklxdm" value="1"/>
<input id="xkxnm" value="2025"/>
<input id="xkxqm" value="12"/>
</body></html>`)
		case config.ChooseCourseListPre:
			_, _ = io.WriteString(w, `<html><body>
<input type="hidden" name="xkkssj" id="xkkssj" value="2026-09-09 12:30:00"/>
<input type="hidden" name="xkjssj" id="xkjssj" value="2026-09-23 11:00:00"/>
</body></html>`)
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

	srv := mustNewServer(t, store)
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.TestSetLoggedIn(true)
	if err := rt.engine.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+acc.ID+"/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"window":{"xkkssj":"2026-09-09 12:30:00","xkjssj":"2026-09-23 11:00:00"}`) {
		t.Fatalf("status 没带选课时间窗，body = %s", body)
	}
}
