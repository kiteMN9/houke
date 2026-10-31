package protocol

import "strings"

// 课程集合的合并语义集中在这里：CLI、Web（engine）和以后的 tui 都调用这几个
// 纯函数，避免每个调用方各写一份"怎么把新查询并进已有集合"。
//
// 两条规则由学校接口的数据形态决定（见 CourseAPI.go 里的注释）：
//
//   - 课程列表是"有哪些教学班存在"的全集（GetCourseList）：按 Jxb_id 更新列表侧
//     字段，新教学班追加到末尾。缓存本身是累积视图——不在本次结果里的旧条目
//     保持原样，不会因为换页签或换搜索词而消失。
//   - 课程详情不是全集（GetCourseDetail 只返回某个课程号下的教学班）：只补齐
//     已经在集合里的同 Jxb_id 条目，不追加新条目。这样"集合里有哪些教学班"
//     始终由课程列表决定，详情只负责补 Do_jxb_id / 教师 / 时间等提交参数。
//
// 两个函数都返回新切片，不修改 base：engine 的读路径拿的是快照，调用方可能
// 还持有旧切片，原地改会绕过锁。

// MergeCourseList 把一次课程列表查询结果并进已有课程集合。
//
// 同一个 Jxb_id 出现多次时以最后一次为准；Jxb_id 为空的列表行无法与详情、
// 选课对应，直接丢弃。
func MergeCourseList(base []CustomCourseDic, list []CourseListDic) []CustomCourseDic {
	out := make([]CustomCourseDic, len(base), len(base)+len(list))
	copy(out, base)
	if len(list) == 0 {
		return out
	}

	index := make(map[string]int, len(out))
	for i := range out {
		index[out[i].Jxb_id] = i
	}

	for i := range list {
		in := &list[i]
		if in.JxbId == "" {
			continue
		}
		if j, ok := index[in.JxbId]; ok {
			applyCourseList(&out[j], in)
			continue
		}
		var item CustomCourseDic
		applyCourseList(&item, in)
		index[in.JxbId] = len(out)
		out = append(out, item)
	}
	return out
}

// MergeCourseDetail 把一次课程详情查询结果补进已有课程集合。
//
// 只刷新已存在的同 Jxb_id 条目；详情里多出来的教学班不并入（列表是全集，
// 详情不是全集）。Yxzrs 为空时保留列表里的值——正方9.0之前的系统详情里的已选人数可能缺失。
func MergeCourseDetail(base []CustomCourseDic, list []CourseDetail) []CustomCourseDic {
	out := make([]CustomCourseDic, len(base))
	copy(out, base)
	if len(list) == 0 {
		return out
	}

	index := make(map[string]int, len(out))
	for i := range out {
		index[out[i].Jxb_id] = i
	}

	for i := range list {
		in := &list[i]
		if in.JxbId == "" {
			continue
		}
		j, ok := index[in.JxbId]
		if !ok {
			continue
		}
		d := &out[j]
		d.Do_jxb_id = in.DoJxbId
		d.Jxbrl = in.Jxbrl
		d.Sksj = strings.ReplaceAll(in.Sksj, "<br/>", ",")
		d.Jxdd = in.Jxdd
		d.Jsxx = processLine(in.Jsxx)
		d.Xqumc = strings.ReplaceAll(in.Xqumc, "校", "")
		d.Xqh_id = in.Xqh_id
		d.Kcxzmc = in.Kcxzmc
		d.Kkxymc = in.Kkxymc
		d.Jxms = in.Jxms
		d.Kclbmc = in.Kclbmc
		if in.Yxzrs != "" {
			d.Yxzrs = in.Yxzrs
		}
	}
	return out
}

// FixCapacity 修正课程集合里"课程列表已经查不到"的教学班容量。
//
// yl 模式下，列表里不存在的教学班说明容量是满的选不到课：把已选人数拉到容量值，
// 打印时显示为满员，也不再作为捡漏候选。不改 base，返回新切片。
func FixCapacity(yl bool, courses []CustomCourseDic, list []CourseListDic) []CustomCourseDic {
	if !yl || len(courses) == 0 {
		return courses
	}
	alive := make(map[string]struct{}, len(list))
	for i := range list {
		alive[list[i].JxbId] = struct{}{}
	}
	out := make([]CustomCourseDic, len(courses))
	copy(out, courses)
	for i := range out {
		if out[i].Jxbrl == "" {
			continue
		}
		if _, ok := alive[out[i].Jxb_id]; ok {
			continue
		}
		out[i].Yxzrs = out[i].Jxbrl
	}
	return out
}

// IsKchIdAllSame 判断集合里的教学班是否都属于同一门课程号。按课程号查询的结果应该
// 全同，板块课也可能是同一门课的多个教学班。返回 (是否全同, 第一个不一致的
// 下标)：空集合返回 (false, 0)，全同时下标为 -1。kch_id 为空按相等处理（旧实现
// 只打一行"开发错误"日志，不影响判断）。
func IsKchIdAllSame(courses []CustomCourseDic) (bool, int) {
	if len(courses) == 0 {
		return false, 0
	}
	tmp := courses[0].Kch_id
	for i := range courses {
		if courses[i].Kch_id != tmp {
			return false, i
		}
	}
	return true, -1
}

// appendCourseOnce 追加一个教学班；Jxb_id 已存在或为空时原样返回。
func appendCourseOnce(list []CustomCourseDic, item CustomCourseDic) []CustomCourseDic {
	if item.Jxb_id == "" {
		return list
	}
	for i := range list {
		if list[i].Jxb_id == item.Jxb_id {
			return list
		}
	}
	return append(list, item)
}

// applyCourseList 把列表行里属于"列表侧"的字段写到课程条目上。
// 详情侧字段（Do_jxb_id、教师、上课时间等）不在这里覆盖。
func applyCourseList(dst *CustomCourseDic, in *CourseListDic) {
	dst.Jxb_id = in.JxbId
	dst.Jxbmc = in.Jxbmc
	dst.Kch_id = in.KchId
	dst.Kcmc = in.Kcmc
	dst.Kklxdm = in.Kklxdm
	dst.Kzmc = in.Kzmc
	dst.XF = in.Xf
	dst.Xxkbj = in.Xxkbj
	dst.Year = in.Year
	dst.Yxzrs = in.Yxzrs
	dst.Cxbj = in.Cxbj
	dst.Date = in.Date
	dst.DateDigit = in.DateDigit
	dst.DateDigitSeparator = in.DateDigitSeparator
}
