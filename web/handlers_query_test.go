package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 未登录时查询已选 / 成绩必须快速失败，而不是在 handler 里触发一次
// 不可见、不可取消的阻塞登录（那会让前端按钮一直转圈）。
func TestQueriesRequireLogin(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv := mustNewServer(t, store)

	cases := []struct {
		name string
		url  string
	}{
		{"selected", "/api/accounts/" + acc.ID + "/selected?xnm=2024&xqm=3"},
		{"score", "/api/accounts/" + acc.ID + "/score?xnm=2024&xqm=3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.url, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d, body = %s",
					rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "尚未登录") {
				t.Errorf("body = %s, want 含「尚未登录」", rec.Body.String())
			}
		})
	}
}
