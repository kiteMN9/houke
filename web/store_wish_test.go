package web

import (
	"path/filepath"
	"reflect"
	"testing"

	"school_sdk/protocol"
)

// 愿望清单是账号级配置：清洗后写盘、同步到已存在的运行态，且不重建 runtime。
func TestSetWishPersistsAndUpdatesRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := NewAccountStore(path)
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	acc, err := s.Create(&Account{
		Name:     "n",
		Username: "u",
		Password: "p",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	raw, ok := s.Get(acc.ID)
	if !ok {
		t.Fatal("账号丢失")
	}
	rt := mustBuildRuntime(t, s, raw)
	rt.engine.TestSetLoggedIn(true)
	s.runtimes[acc.ID] = rt

	updated, err := s.SetWish(acc.ID, protocol.WishList{
		WantClassList:   []string{" 数据结构 ", "", "数据结构"},
		WantTeacherList: []string{"裴如意", " 裴如意 "},
	})
	if err != nil {
		t.Fatalf("SetWish: %v", err)
	}
	if want := []string{"数据结构"}; !reflect.DeepEqual(updated.WantClasses, want) {
		t.Errorf("WantClasses = %v, want %v（应去空白、去重）", updated.WantClasses, want)
	}
	if want := []string{"裴如意"}; !reflect.DeepEqual(updated.WantTeachers, want) {
		t.Errorf("WantTeachers = %v, want %v", updated.WantTeachers, want)
	}
	if updated.WantTypes != nil {
		t.Errorf("WantTypes = %v, want nil", updated.WantTypes)
	}

	// 运行态已存在：清单即时生效，登录态与 runtime 指针都不应被换掉。
	if got := s.runtimes[acc.ID]; got != rt {
		t.Error("SetWish 不应重建 runtime")
	}
	if !rt.engine.LoggedIn() {
		t.Error("SetWish 不应清掉登录态")
	}
	if want := []string{"数据结构"}; !reflect.DeepEqual(rt.engine.Wish().WantClassList, want) {
		t.Errorf("engine.Wish() = %v, want %v", rt.engine.Wish().WantClassList, want)
	}

	// 重新加载：清单应已落盘（重启 WebUI 后仍在）。
	reloaded, err := NewAccountStore(path)
	if err != nil {
		t.Fatalf("NewAccountStore(reload): %v", err)
	}
	got, ok := reloaded.Get(acc.ID)
	if !ok {
		t.Fatal("重新加载后账号丢失")
	}
	if want := []string{"数据结构"}; !reflect.DeepEqual(got.WantClasses, want) {
		t.Errorf("落盘后 WantClasses = %v, want %v", got.WantClasses, want)
	}
	if want := []string{"裴如意"}; !reflect.DeepEqual(got.WantTeachers, want) {
		t.Errorf("落盘后 WantTeachers = %v, want %v", got.WantTeachers, want)
	}
}
