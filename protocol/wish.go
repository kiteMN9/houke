package protocol

import "strings"

// 愿望清单的匹配规则集中在这里，而不是散落在 scanWant / scanWantAuto / checkRank
// 三个函数里：CLI 的自动选课、以后 Web 的高级选课都要用同一套"这门课是不是我要的"
// 判断，规则只留一份。
//
// 规则本身是学校侧的历史行为，改之前先看 CourseUtil_test.go 里的用例：
//   - 课程名包含愿望课程名即命中；
//   - 填了课程类型时，类型命中后还要看教师：教师清单为空即命中，否则要求教师也命中；
//   - 只填教师时，教师命中即命中。

// Clone 复制三个切片，避免调用方与 Session 共享底层数组。
func (w WishList) Clone() WishList {
	return WishList{
		WantClassList:   append([]string(nil), w.WantClassList...),
		WantTeacherList: append([]string(nil), w.WantTeacherList...),
		WantTypeList:    append([]string(nil), w.WantTypeList...),
	}
}

// Empty 判断清单是否为空（三项都为 0）。
func (w WishList) Empty() bool {
	return len(w.WantClassList) == 0 && len(w.WantTeacherList) == 0 && len(w.WantTypeList) == 0
}

// Match 判断一门课是否命中愿望清单。
//
// WantClassList 里的每一项同时比对教学班名称（Jxbmc）和课程名称（Kcmc）：
// CLI 的历史习惯是填教学班名（"定向越野-01班"），WebUI 的输入框却叫"课程名"，
// 用户会填"高等数学"这种课程名。只比 Jxbmc 的话，课程名能筛出详情却匹配不上，
// 最后报"愿望清单没有命中可选的课程"（见 matchesLoosely 的粗筛）。
func (w WishList) Match(item CustomCourseDic) bool {
	for _, class := range w.WantClassList {
		class = strings.TrimSpace(class)
		if class == "" {
			continue
		}
		if strings.Contains(item.Jxbmc, class) || strings.Contains(item.Kcmc, class) {
			return true
		}
	}
	if len(w.WantTypeList) > 0 {
		for _, courseType := range w.WantTypeList {
			if !strings.Contains(item.Kzmc, courseType) && !strings.Contains(courseType, item.Kzmc) {
				continue
			}
			if len(w.WantTeacherList) == 0 {
				return true
			}
			for _, teacher := range w.WantTeacherList {
				if strings.Contains(item.Jsxx, teacher) {
					return true
				}
			}
		}
		return false
	}
	for _, teacher := range w.WantTeacherList {
		if strings.Contains(item.Jsxx, teacher) {
			return true
		}
	}
	return false
}

// Rank 返回课程在愿望清单中的优先级：越小越想要。
// 没命中任何课程名时返回 len(WantClassList)，列表为空且课程名非空也不例外——
// 调用方用 rank==0 表示"第一志愿"。
// 空白条目的处理与 Match 保持一致：跳过空串、忽略首尾空格，否则 Match 认了
// 的愿望在排序里会落到默认优先级，顺序变成随机的。
func (w WishList) Rank(currentClass string) int {
	rank := len(w.WantClassList)
	currentClass = strings.TrimSpace(currentClass)
	if currentClass == "" {
		return rank
	}
	for i, class := range w.WantClassList {
		class = strings.TrimSpace(class)
		if class == "" {
			continue
		}
		if strings.Contains(class, currentClass) || strings.Contains(currentClass, class) {
			return i
		}
	}
	return rank
}
