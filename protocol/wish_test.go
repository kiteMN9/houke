package protocol

import "testing"

func TestWishListMatch(t *testing.T) {
	item := CustomCourseDic{
		Jxbmc: "定向越野-01班",
		Kzmc:  "通识选修课",
		Jsxx:  "320057/陈爱华/副教授",
	}
	tests := []struct {
		name string
		wish WishList
		want bool
	}{
		{"课程名命中", WishList{WantClassList: []string{"定向越野"}}, true},
		{"课程名不命中", WishList{WantClassList: []string{"篮球"}}, false},
		{"只填教师且命中", WishList{WantTeacherList: []string{"陈爱华"}}, true},
		{"只填教师且不命中", WishList{WantTeacherList: []string{"张三"}}, false},
		{"类型命中且未限制教师", WishList{WantTypeList: []string{"通识"}}, true},
		{"类型命中且教师命中", WishList{WantTypeList: []string{"通识"}, WantTeacherList: []string{"陈爱华"}}, true},
		{"类型命中但教师不命中", WishList{WantTypeList: []string{"通识"}, WantTeacherList: []string{"张三"}}, false},
		{"类型不命中且教师不命中", WishList{WantTypeList: []string{"体育"}, WantTeacherList: []string{"张三"}}, false},
		{"空白类型", WishList{WantTypeList: []string{"", "  "}}, false},
		{"空白教师", WishList{WantTeacherList: []string{" ", ""}}, false},
		{"类型命中且教师清单全空白", WishList{WantTypeList: []string{"通识"}, WantTeacherList: []string{"  "}}, true},
		{"类型与教师带首尾空格", WishList{WantTypeList: []string{" 通识 "}, WantTeacherList: []string{" 陈爱华 "}}, true},
		{"类型不命中且教师全空白", WishList{WantTypeList: []string{" 体育 "}, WantTeacherList: []string{""}}, false},
		{"空清单", WishList{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.wish.Match(item); got != tc.want {
				t.Fatalf("Match(%+v) = %v, want %v", tc.wish, got, tc.want)
			}
		})
	}
}

func TestWishListRank(t *testing.T) {
	wish := WishList{WantClassList: []string{"定向越野", "英语电影赏析", "跨文化交流"}}
	if got := wish.Rank("定向越野15"); got != 0 {
		t.Fatalf("Rank(定向越野15) = %d, want 0", got)
	}
	if got := wish.Rank("英语电影赏析1班"); got != 1 {
		t.Fatalf("Rank(英语电影赏析1班) = %d, want 1", got)
	}
	if got := wish.Rank("篮球"); got != len(wish.WantClassList) {
		t.Fatalf("Rank(篮球) = %d, want %d", got, len(wish.WantClassList))
	}
	if got := wish.Rank(""); got != len(wish.WantClassList) {
		t.Fatalf("Rank(空) = %d, want %d", got, len(wish.WantClassList))
	}
	if got := wish.Rank(" 定向越野15 "); got != 0 {
		t.Fatalf("Rank(带空格) = %d, want 0", got)
	}
}

// WebUI 的愿望输入框叫"课程名"：只填 Kcmc、教学班名里没有这个词时也要命中。
// 真实数据里体育板块课的教学班名是"瑜伽4/舞龙4"，课程名统一是"体育Ⅰ"。
func TestWishListMatchCourseName(t *testing.T) {
	item := CustomCourseDic{Jxbmc: "瑜伽4", Kcmc: "体育Ⅰ"}
	if !(WishList{WantClassList: []string{"体育Ⅰ"}}).Match(item) {
		t.Fatal("按课程名填的愿望没命中所属课程")
	}
	if (WishList{WantClassList: []string{"高等数学"}}).Match(item) {
		t.Fatal("不同课程名不该命中")
	}
}

// 空/纯空白条目必须被忽略：strings.Contains(x, "") 恒为真，多打一个空行
// 就会让愿望清单变成"什么课都要"。
func TestWishListMatchIgnoresBlankClass(t *testing.T) {
	item := CustomCourseDic{Jxbmc: "瑜伽4", Kcmc: "体育Ⅰ"}
	if (WishList{WantClassList: []string{"", "   "}}).Match(item) {
		t.Fatal("空白条目不该命中任何课程")
	}
	if !(WishList{WantClassList: []string{" 体育Ⅰ "}}).Match(item) {
		t.Fatal("条目首尾空格应被忽略")
	}
}

// 列表还没带课程类型（Kzmc 为空）时，类型只能靠愿望反向包含来匹配；
// 不能让空 Kzmc 反向包含出"全命中"。
func TestWishListMatchEmptyCourseType(t *testing.T) {
	item := CustomCourseDic{Jxbmc: "瑜伽4", Kcmc: "体育Ⅰ"}
	if (WishList{WantTypeList: []string{"艺术类"}}).Match(item) {
		t.Fatal("Kzmc 为空时不该命中任何类型")
	}
	if (WishList{WantTypeList: []string{"", " "}}).Match(item) {
		t.Fatal("Kzmc 与类型都为空时不该命中")
	}
}

// Empty 按内容判断：只填空白条目等于没配置，保存路径会清洗掉它们，
// 但直接传进来的请求体不一定。
func TestWishListEmpty(t *testing.T) {
	tests := []struct {
		name string
		wish WishList
		want bool
	}{
		{"零值", WishList{}, true},
		{"只有空白条目", WishList{
			WantClassList:   []string{"  "},
			WantTeacherList: []string{""},
			WantTypeList:    []string{" "},
		}, true},
		{"有课程名", WishList{WantClassList: []string{"高等数学"}}, false},
		{"只有教师", WishList{WantTeacherList: []string{"陈爱华"}}, false},
		{"只有类型", WishList{WantTypeList: []string{"艺术类"}}, false},
		{"空白里混一个有效的", WishList{WantTeacherList: []string{" ", "陈爱华"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.wish.Empty(); got != tc.want {
				t.Fatalf("Empty(%+v) = %v, want %v", tc.wish, got, tc.want)
			}
		})
	}
}
