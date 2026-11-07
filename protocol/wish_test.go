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

// 名字优先的快速路径用 HasCourseNames / MatchesCourseName 判断"哪些课是用户
// 点名的"，规则必须与 Match 的课程名分支一致（空白跳过、首尾空格忽略）。
func TestWishListCourseNameHelpers(t *testing.T) {
	item := CustomCourseDic{Jxbmc: "瑜伽4", Kcmc: "体育Ⅰ"}
	if !(WishList{WantClassList: []string{" 体育Ⅰ "}}).MatchesCourseName(item) {
		t.Fatal("首尾空格的课程名应命中")
	}
	if !(WishList{WantClassList: []string{"瑜伽"}}).MatchesCourseName(item) {
		t.Fatal("教学班名包含课程名应命中")
	}
	if (WishList{WantClassList: []string{"高等数学"}}).MatchesCourseName(item) {
		t.Fatal("不同课程名不该命中")
	}
	if (WishList{WantClassList: []string{"", "  "}}).MatchesCourseName(item) {
		t.Fatal("空白条目不该命中")
	}
	if !(WishList{WantClassList: []string{"体育Ⅰ"}}).HasCourseNames() {
		t.Fatal("有课程名时 HasCourseNames 应为 true")
	}
	if (WishList{WantClassList: []string{"", " "}}).HasCourseNames() {
		t.Fatal("只有空白条目时 HasCourseNames 应为 false")
	}
	if (WishList{WantTeacherList: []string{"陈爱华"}}).HasCourseNames() {
		t.Fatal("只填教师时 HasCourseNames 应为 false")
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

// 课程类型只看 Kzmc（课程性质）：真实列表里"公共艺术"写在 Kzmc；Kclxmc
// （"公共选修课"）是教务的选课分类，不参与愿望类型匹配。
func TestWishListMatchCourseCategory(t *testing.T) {
	item := CustomCourseDic{
		Jxbmc:  "西方美术鉴赏(美育)-评论类-0002",
		Kcmc:   "西方美术鉴赏(美育)-评论类",
		Kzmc:   "公共艺术",
		Kclxmc: "公共选修课",
	}
	for _, courseType := range []string{"公共艺术", "艺术"} {
		if !(WishList{WantTypeList: []string{courseType}}).Match(item) {
			t.Fatalf("类型 %q 没命中：%+v", courseType, item)
		}
	}
	for _, courseType := range []string{"公共选修课", "选修", "体育"} {
		if (WishList{WantTypeList: []string{courseType}}).Match(item) {
			t.Fatalf("类型 %q 不该命中：%+v", courseType, item)
		}
	}
}

// CouldMatch 是抓详情前的粗筛：返回 false 的课一定匹配不上，返回 true 的还要
// 等详情回来才能定论。教师（Jsxx）只在详情里，所以"课程名 + 教师"的愿望必须
// 放行名字不命中的课——否则教师命中的课永远拿不到详情，最后报没有命中。
func TestWishListCouldMatch(t *testing.T) {
	art := CustomCourseDic{
		Jxbmc: "西方美术鉴赏(美育)-评论类-0002",
		Kcmc:  "西方美术鉴赏(美育)-评论类",
		Kzmc:  "公共艺术",
		Jsxx:  "360044/殷海华/副教授",
	}
	tests := []struct {
		name string
		wish WishList
		want bool
	}{
		{"课程名命中", WishList{WantClassList: []string{"美术"}}, true},
		{"课程名不命中", WishList{WantClassList: []string{"定向越野"}}, false},
		{"课程名不命中但有教师", WishList{
			WantClassList:   []string{"定向越野", "乒乓球"},
			WantTeacherList: []string{"超星尔雅", "殷海华"},
		}, true},
		{"只有教师", WishList{WantTeacherList: []string{"殷海华"}}, true},
		{"教师清单全空白不算条件", WishList{
			WantClassList:   []string{"定向越野"},
			WantTeacherList: []string{"  "},
		}, false},
		{"类型命中", WishList{WantTypeList: []string{"艺术"}}, true},
		{"类型不命中", WishList{WantTypeList: []string{"体育"}}, false},
		{"类型不命中时教师也救不了", WishList{
			WantTypeList:    []string{"体育"},
			WantTeacherList: []string{"殷海华"},
		}, false},
		{"空清单", WishList{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.wish.CouldMatch(art); got != tc.want {
				t.Fatalf("CouldMatch(%+v) = %v, want %v", tc.wish, got, tc.want)
			}
			// 粗筛必须是 Match 的超集：Match 认了的课不能在这里被挡掉。
			if tc.wish.Match(art) && !tc.wish.CouldMatch(art) {
				t.Fatalf("Match 命中但 CouldMatch 拒绝：%+v", tc.wish)
			}
		})
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
