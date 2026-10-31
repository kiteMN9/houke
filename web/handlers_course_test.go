package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"school_sdk/protocol"
)

// 课程浏览页的缓存接口：只读内存，不触发学校接口查询；
// 空缓存返回 []（而不是 null），前端可以直接当数组用。
func TestCachedCoursesReadsMemoryOnly(t *testing.T) {
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
	url := "/api/accounts/" + acc.ID + "/courses/cache"

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty cache: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]" {
		t.Fatalf("empty cache body = %q, want []", got)
	}

	// 直接往 Session 的课程缓存里塞一条，缓存接口应原样返回（不经过学校接口）。
	rt, err := store.runtime(acc.ID)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rt.engine.CacheCourseList([]protocol.CourseListDic{{
		JxbId: "jxb-1",
		KchId: "kch-1",
		Kcmc:  "测试课程",
		Jxbmc: "测试课程-0001",
		Xf:    "2",
	}})

	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cached: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []protocol.CustomCourseDic
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v, body = %s", err, rec.Body.String())
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1, body = %s", len(list), rec.Body.String())
	}
	if list[0].Jxb_id != "jxb-1" || list[0].Kch_id != "kch-1" || list[0].Kcmc != "测试课程" {
		t.Fatalf("cached item = %+v", list[0])
	}
}

func TestCachedCoursesUnknownAccount(t *testing.T) {
	store := newTestStore(t)
	srv := mustNewServer(t, store)

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/nope/courses/cache", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
