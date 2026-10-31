package protocol

import "testing"

func TestMergeCourseListAppendsNewCourses(t *testing.T) {
	list := []CourseListDic{
		{JxbId: "jxb-1", Jxbmc: "高数-0001", KchId: "kch-1", Kcmc: "高数", Kklxdm: "10", Kzmc: "必修", Xf: "3", Yxzrs: "20", Cxbj: "0", Year: "2025", Date: "二○二五年", DateDigit: "2025年", DateDigitSeparator: "2025-1-1"},
		{JxbId: "jxb-2", Jxbmc: "线代-0001", KchId: "kch-2", Kcmc: "线代", Kklxdm: "10", Kzmc: "选修", Xf: "2", Yxzrs: "30", Cxbj: "1", Year: "2025", Date: "二○二五年", DateDigit: "2025年", DateDigitSeparator: "2025-1-2"},
	}

	got := MergeCourseList(nil, list)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	first := got[0]
	if first.Jxb_id != "jxb-1" || first.Jxbmc != "高数-0001" || first.Kch_id != "kch-1" ||
		first.Kcmc != "高数" || first.Kklxdm != "10" || first.Kzmc != "必修" ||
		first.XF != "3" || first.Yxzrs != "20" || first.Cxbj != "0" ||
		first.Year != "2025" || first.Date != "二○二五年" ||
		first.DateDigit != "2025年" || first.DateDigitSeparator != "2025-1-1" {
		t.Errorf("首条课程字段没有完整映射: %+v", first)
	}
	if got[1].Jxb_id != "jxb-2" || got[1].Xxkbj != "" {
		t.Errorf("第二条课程映射错误: %+v", got[1])
	}
}

// 列表侧只刷新列表拥有的字段；详情补出来的 Do_jxb_id/教师/容量不能被列表覆盖。
func TestMergeCourseListRefreshesWithoutLosingDetail(t *testing.T) {
	base := []CustomCourseDic{
		{Jxb_id: "jxb-1", Jxbmc: "旧名字", Kcmc: "旧课程", Yxzrs: "1", Do_jxb_id: "do-1", Jsxx: "裴如意", Jxbrl: "50", Sksj: "周一"},
	}
	list := []CourseListDic{
		{JxbId: "jxb-1", Jxbmc: "新名字", KchId: "kch-1", Kcmc: "新课程", Yxzrs: "42"},
	}

	got := MergeCourseList(base, list)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Jxbmc != "新名字" || got[0].Kcmc != "新课程" || got[0].Yxzrs != "42" || got[0].Kch_id != "kch-1" {
		t.Errorf("列表侧字段没有刷新: %+v", got[0])
	}
	if got[0].Do_jxb_id != "do-1" || got[0].Jsxx != "裴如意" || got[0].Jxbrl != "50" || got[0].Sksj != "周一" {
		t.Errorf("详情侧字段被列表覆盖: %+v", got[0])
	}
	if base[0].Jxbmc != "旧名字" {
		t.Errorf("MergeCourseList 修改了 base: %+v", base[0])
	}
}

// 缓存是累积视图：本次查询没有的旧课程保留，新课程追加在末尾。
func TestMergeCourseListKeepsOldAndAppendsNew(t *testing.T) {
	base := []CustomCourseDic{{Jxb_id: "old", Jxbmc: "旧课程"}}
	list := []CourseListDic{{JxbId: "new", Jxbmc: "新课程"}}

	got := MergeCourseList(base, list)
	if len(got) != 2 || got[0].Jxb_id != "old" || got[1].Jxb_id != "new" {
		t.Fatalf("合并结果 = %+v, want [old new]", got)
	}
	if len(base) != 1 || base[0].Jxb_id != "old" {
		t.Fatalf("base 被修改: %+v", base)
	}
}

func TestMergeCourseListSkipsEmptyJxbIDAndDedupes(t *testing.T) {
	list := []CourseListDic{
		{JxbId: "", Jxbmc: "没有 id 的行"},
		{JxbId: "same", Jxbmc: "第一次"},
		{JxbId: "same", Jxbmc: "第二次"},
	}

	got := MergeCourseList(nil, list)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1（空 id 丢弃，同 id 只留一条）", len(got))
	}
	if got[0].Jxbmc != "第二次" {
		t.Errorf("同一 Jxb_id 应以最后一次为准: %+v", got[0])
	}
}

func TestMergeCourseDetailEnrichesExisting(t *testing.T) {
	base := []CustomCourseDic{
		{Jxb_id: "jxb-1", Jxbmc: "高数-0001", Yxzrs: "20"},
		{Jxb_id: "jxb-2", Jxbmc: "线代-0001", Yxzrs: "30"},
	}
	list := []CourseDetail{
		{
			JxbId: "jxb-1", DoJxbId: "do-1", Jxbrl: "60", Sksj: "星期一第1-2节<br/>星期三第3-4节",
			Jxdd: "知行楼108", Jsxx: "440015/裴如意/副教授", Xqumc: "北校区", Xqh_id: "3",
			Kcxzmc: "必修", Kkxymc: "外国语学院", Jxms: "理论", Kclbmc: "公共必修课", Yxzrs: "21",
		},
		{JxbId: "jxb-2", DoJxbId: "do-2", Jxbrl: "70"},
	}

	got := MergeCourseDetail(base, list)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	first := got[0]
	if first.Do_jxb_id != "do-1" || first.Jxbrl != "60" || first.Jxdd != "知行楼108" ||
		first.Xqh_id != "3" || first.Kcxzmc != "必修" || first.Kkxymc != "外国语学院" ||
		first.Jxms != "理论" || first.Kclbmc != "公共必修课" || first.Yxzrs != "21" {
		t.Errorf("详情字段没有补全: %+v", first)
	}
	if first.Sksj != "星期一第1-2节,星期三第3-4节" {
		t.Errorf("Sksj = %q, want 把 <br/> 换成逗号", first.Sksj)
	}
	if first.Jsxx != "裴如意/副教授" {
		t.Errorf("Jsxx = %q, want 去掉教工号前缀", first.Jsxx)
	}
	if first.Xqumc != "北区" {
		t.Errorf("Xqumc = %q, want 去掉“校”字", first.Xqumc)
	}
	if base[0].Do_jxb_id != "" {
		t.Errorf("MergeCourseDetail 修改了 base: %+v", base[0])
	}
}

// 列表是全集，详情不是全集：详情里多出来的教学班不能进集合。
func TestMergeCourseDetailDropsUnknownJxb(t *testing.T) {
	base := []CustomCourseDic{{Jxb_id: "jxb-1", Jxbmc: "高数-0001"}}
	list := []CourseDetail{
		{JxbId: "jxb-1", DoJxbId: "do-1"},
		{JxbId: "jxb-other", DoJxbId: "do-other"},
		{JxbId: "", DoJxbId: "do-empty"},
	}

	got := MergeCourseDetail(base, list)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1（不追加详情里的新教学班）", len(got))
	}
	if got[0].Do_jxb_id != "do-1" {
		t.Errorf("Do_jxb_id = %q, want do-1", got[0].Do_jxb_id)
	}
}

func TestMergeCourseDetailKeepsYxzrsWhenEmpty(t *testing.T) {
	base := []CustomCourseDic{{Jxb_id: "jxb-1", Yxzrs: "20", Jxbrl: "5"}}

	got := MergeCourseDetail(base, []CourseDetail{{JxbId: "jxb-1", Jxbrl: "60"}})
	if got[0].Yxzrs != "20" {
		t.Errorf("Yxzrs = %q, want 保留列表里的 20", got[0].Yxzrs)
	}
	if got[0].Jxbrl != "60" {
		t.Errorf("Jxbrl = %q, want 60", got[0].Jxbrl)
	}
}
