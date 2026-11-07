package protocol

import "strings"

// 愿望清单的匹配规则集中在这里，而不是散落在 scanWant / scanWantAuto / checkRank
// 三个函数里：CLI 的自动选课、以后 Web 的高级选课都要用同一套"这门课是不是我要的"
// 判断，规则只留一份。
//
// 规则本身是学校侧的历史行为，改之前先看 CourseUtil_test.go 里的用例：
//   - 课程名包含愿望课程名即命中；
//   - 填了课程类型时，类型命中后还要看教师：教师清单为空即命中，否则要求教师也命中；
//   - 只填教师时，教师命中即命中；
//   - 空白条目一律跳过（首尾空格忽略）：strings.Contains(x, "") 恒为真，
//     多打一个空行就会让类型 / 教师条件失效，变成"什么课都要"。

// Clone 复制三个切片，避免调用方与 Session 共享底层数组。
func (w WishList) Clone() WishList {
	return WishList{
		WantClassList:   append([]string(nil), w.WantClassList...),
		WantTeacherList: append([]string(nil), w.WantTeacherList...),
		WantTypeList:    append([]string(nil), w.WantTypeList...),
	}
}

// Empty 判断清单是否为空：三项都没有非空内容。只填了空白条目也算空——
// 那些条目在 Match 里会被跳过，等于没配置。保存路径会清洗，但直接传进来的
// 请求体不一定，所以这里按内容判断，而不只是看长度。
func (w WishList) Empty() bool {
	return !hasNonBlank(w.WantClassList) && !hasNonBlank(w.WantTeacherList) && !hasNonBlank(w.WantTypeList)
}

// hasNonBlank 报告清单里是否至少有一项非空内容。
func hasNonBlank(items []string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return true
		}
	}
	return false
}

// Match 判断一门课是否命中愿望清单。
//
// WantClassList 里的每一项同时比对教学班名称（Jxbmc）和课程名称（Kcmc）：
// CLI 的历史习惯是填教学班名（"定向越野-01班"），WebUI 的输入框却叫"课程名"，
// 用户会填"高等数学"这种课程名。只比 Jxbmc 的话，课程名能筛出详情却匹配不上，
// 最后报"愿望清单没有命中可选的课程"（见 CouldMatch 的粗筛）。
//
// 空白条目一律跳过，且教师清单"只有空白项"等同于没填教师：Contains 的第二个
// 参数是空串时恒为真，一个空行就会让类型 / 教师条件失效，把"只要艺术类"
// 变成"什么课都要"。
func (w WishList) Match(item CustomCourseDic) bool {
	if w.MatchesCourseName(item) {
		return true
	}
	if hasNonBlank(w.WantTypeList) {
		teacherLimited := hasNonBlank(w.WantTeacherList)
		for _, courseType := range w.WantTypeList {
			courseType = strings.TrimSpace(courseType)
			if courseType == "" || !matchesCourseType(item, courseType) {
				continue
			}
			if !teacherLimited {
				return true
			}
			for _, teacher := range w.WantTeacherList {
				teacher = strings.TrimSpace(teacher)
				if teacher == "" {
					continue
				}
				if strings.Contains(item.Jsxx, teacher) {
					return true
				}
			}
		}
		return false
	}
	for _, teacher := range w.WantTeacherList {
		teacher = strings.TrimSpace(teacher)
		if teacher == "" {
			continue
		}
		if strings.Contains(item.Jsxx, teacher) {
			return true
		}
	}
	return false
}

// HasCourseNames 报告愿望里是否至少填了一门非空课程名。自动选课的"名字优先"
// 快速路径用它判断第一次收集候选要不要先只抓课程名命中的课。
func (w WishList) HasCourseNames() bool {
	return hasNonBlank(w.WantClassList)
}

// MatchesCourseName 判断教学班名（Jxbmc）或课程名（Kcmc）是否包含愿望里的
// 某个课程名。空白条目跳过、首尾空格忽略，规则与 Match / CouldMatch 的课程名
// 分支一致——名字优先的快速路径与最终过滤必须认同一批课。
func (w WishList) MatchesCourseName(item CustomCourseDic) bool {
	for _, class := range w.WantClassList {
		class = strings.TrimSpace(class)
		if class == "" {
			continue
		}
		if strings.Contains(item.Jxbmc, class) || strings.Contains(item.Kcmc, class) {
			return true
		}
	}
	return false
}

// matchesCourseType 判断课程类型是否命中愿望里的类型。
//
// 类型只看列表侧的 Kzmc（课程性质，如"公共艺术"）。Kclxmc（课程类别，如
// "公共选修课"）是教务的选课分类，不是用户在愿望清单里挑的东西，不参与匹配；
// 学生填的类型以 Kzmc 为准。包含匹配是双向的（历史行为）：既认字段包含愿望，
// 也认愿望包含字段；但空字段不能反向包含出"全命中"，所以显式判空。
func matchesCourseType(item CustomCourseDic, courseType string) bool {
	return typeFieldMatches(item.Kzmc, courseType)
}

// typeFieldMatches 是单个类型字段的包含匹配。
func typeFieldMatches(field, courseType string) bool {
	if strings.Contains(field, courseType) {
		return true
	}
	return field != "" && strings.Contains(courseType, field)
}

// CouldMatch 是抓课程详情之前的粗筛：返回 false 的课一定匹配不上，返回 true
// 的还要等详情回来才能定论。engine 用它决定"要不要为这门课请求 GetCourseDetail"。
//
// 需要这个粗筛，是因为课程列表里没有教师：教师（Jsxx）只出现在详情里。愿望里
// 填了教师时，列表里任何一门课都可能是那位老师教的，必须全部抓详情；只看课程名
// 会把教师命中的课挡在门外，最后报"愿望清单没有命中可选的课程"。真实案例：
// 通识选修课列表里全是"公共艺术"类课程，愿望写的是"定向越野 / 乒乓球"（课程名）
// 加"殷海华"（教师），殷老师教的《西方美术鉴赏》课程名一条都不含，于是永远
// 拿不到详情，教师分支也就永远不可能命中。
//
// 有课程类型愿望时可以不抓详情先筛：Match 在有类型时要求类型命中，而类型只看
// 列表侧的 Kzmc。只有课程名一个条件时最省：列表里就能判定，不必为了几门课把
// 整张课表都请求一遍详情。
//
// 本函数必须是 Match 的超集：CouldMatch 为 false 的课，Match 必然为 false。
// 改 Match 的规则时这里要同步（wish_test.go 里有对照用例）。
func (w WishList) CouldMatch(item CustomCourseDic) bool {
	if w.MatchesCourseName(item) {
		return true
	}
	if hasNonBlank(w.WantTypeList) {
		// 有类型条件时 Match 要求类型命中；教师命中不能替代类型命中，
		// 所以类型不命中的课不必抓详情。
		for _, courseType := range w.WantTypeList {
			courseType = strings.TrimSpace(courseType)
			if courseType == "" {
				continue
			}
			if matchesCourseType(item, courseType) {
				return true
			}
		}
		return false
	}
	// 没有类型条件：只剩"只填教师"这一种可能，而教师只在详情里。
	return hasNonBlank(w.WantTeacherList)
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
