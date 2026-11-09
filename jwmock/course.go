package jwmock

import (
	"net/http"
	"strconv"
	"time"
)

// courseListRow 是课程列表里的一行（对应 protocol.CourseListDic 用到的字段）。
type courseListRow struct {
	JxbID     string `json:"jxb_id"`
	JxbMc     string `json:"jxbmc"`
	Kch       string `json:"kch"`
	KchID     string `json:"kch_id"`
	Kcmc      string `json:"kcmc"`
	Kklxdm    string `json:"kklxdm"`
	Kclxmc    string `json:"kclxmc,omitempty"`
	Kzmc      string `json:"kzmc"`
	Xf        string `json:"xf"`
	Xxkbj     string `json:"xxkbj"`
	Year      string `json:"year"`
	Yxzrs     string `json:"yxzrs"`
	Cxbj      string `json:"cxbj"`
	Date      string `json:"date"`
	DateDigit string `json:"dateDigit"`
	Day       string `json:"day"`
}

// courseDetailRow 是课程详情里的一行（对应 protocol.CourseDetail 用到的字段）。
type courseDetailRow struct {
	DoJxbID string `json:"do_jxb_id"`
	JxbID   string `json:"jxb_id"`
	JxbMc   string `json:"jxbmc"`
	Jxbrs   string `json:"jxbrs"`
	Jxbrl   string `json:"jxbrl"`
	Sksj    string `json:"sksj"`
	Jxdd    string `json:"jxdd"`
	Jsxx    string `json:"jsxx"`
	Yxzrs   string `json:"yxzrs"`
	XqhID   string `json:"xqh_id"`
	Xqumc   string `json:"xqumc"`
	Kcxzmc  string `json:"kcxzmc"`
	Kkxymc  string `json:"kkxymc"`
	Jxms    string `json:"jxms"`
	Kclbmc  string `json:"kclbmc"`
	Xf      string `json:"xf"`
}

// chosenRow 是“已选课程”里的一行（对应 protocol.ChosenDic 用到的字段）。
type chosenRow struct {
	DoJxbID string `json:"do_jxb_id"`
	JxbID   string `json:"jxb_id"`
	JxbMc   string `json:"jxbmc"`
	Kcmc    string `json:"kcmc"`
	Kch     string `json:"kch"`
	KchID   string `json:"kch_id"`
	Xf      string `json:"xf"`
	Kklxdm  string `json:"kklxdm"`
	Sksj    string `json:"sksj"`
	Jsxx    string `json:"jsxx"`
	Jxbrs   string `json:"jxbrs"`
	Yxzrs   string `json:"yxzrs"`
}

// handleChooseCourseIndex 输出选课首页参数（含公开参数、页签、学分）。
func (s *Server) handleChooseCourseIndex(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	writeHTML(w, indexPage("模拟学生", time.Now().Format("2006-01-02 15:04:05")))
}

// handleChooseCourseTabParams 输出页签对应的选课参数。
func (s *Server) handleChooseCourseTabParams(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	writeHTML(w, tabPage())
}

// handleChooseCourseCourseList 输出课程列表。
//
// 注意：参数过期必须返回不带 tmpList 的 {"msg":...,"flag":...}；只要响应里
// 出现 tmpList 键，客户端就会把它当成正常列表。
func (s *Server) handleChooseCourseCourseList(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	_ = r.ParseForm()
	if s.takeParamExpire() {
		writeJSON(w, http.StatusOK, map[string]any{
			"msg":  "参数已过期，请刷新页面重试",
			"flag": "0",
		})
		return
	}

	s.mu.Lock()
	rows := make([]courseListRow, 0, len(s.opts.Courses))
	for _, seed := range s.opts.Courses {
		rows = append(rows, courseListRow{
			JxbID:     seed.JxbID,
			JxbMc:     seed.JxbMc,
			Kch:       seed.Kch,
			KchID:     seed.KchID,
			Kcmc:      seed.Kcmc,
			Kklxdm:    seed.Kklxdm,
			Kclxmc:    seed.Kclxmc,
			Kzmc:      seed.Kzmc,
			Xf:        seed.Xf,
			Xxkbj:     "0",
			Year:      "2025",
			Yxzrs:     strconv.Itoa(s.enrolledLocked(seed)),
			Cxbj:      "0",
			Date:      "2025-09-01",
			DateDigit: "1",
			Day:       "1",
		})
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"tmpList": rows,
		"sfxsjc":  "0",
		"msg":     "",
		"flag":    "",
	})
}

// handleChooseCourseCourseDetail 输出某门课的详情数组。
//
// 客户端按 form 里的 kch_id 查询；返回 "0" 会触发参数过期重抓。
func (s *Server) handleChooseCourseCourseDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	_ = r.ParseForm()
	if s.takeParamExpire() {
		writeText(w, `"0"`)
		return
	}
	kchID := r.PostForm.Get("kch_id")

	s.mu.Lock()
	rows := make([]courseDetailRow, 0, 1)
	for _, seed := range s.opts.Courses {
		if kchID != "" && seed.KchID != kchID {
			continue
		}
		enrolled := s.enrolledLocked(seed)
		rows = append(rows, courseDetailRow{
			DoJxbID: "DO-" + seed.JxbID,
			JxbID:   seed.JxbID,
			JxbMc:   seed.JxbMc,
			Jxbrs:   strconv.Itoa(enrolled),
			Jxbrl:   strconv.Itoa(seed.Capacity),
			Sksj:    seed.Sksj,
			Jxdd:    seed.Jxdd,
			Jsxx:    seed.Jsxx,
			Yxzrs:   strconv.Itoa(enrolled),
			XqhID:   "1",
			Xqumc:   "主校区",
			Kcxzmc:  "专业选修课",
			Kkxymc:  "模拟学院",
			Jxms:    "",
			Kclbmc:  "理论课",
			Xf:      seed.Xf,
		})
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, rows)
}

// handleChooseCourse 处理选课。
//
// 判定顺序：拒绝文案 > 同课程重复 > 名额已满 > 成功。
func (s *Server) handleChooseCourse(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	_ = r.ParseForm()
	jxbIDs := r.PostForm.Get("jxb_ids")
	kchID := r.PostForm.Get("kch_id")

	s.mu.Lock()
	defer s.mu.Unlock()

	seed, ok := s.seedByJxbLocked(jxbIDs)
	if !ok && kchID != "" {
		for _, c := range s.opts.Courses {
			if c.KchID == kchID {
				seed, ok = c, true
				break
			}
		}
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{"flag": "0", "msg": "教学班不存在"})
		return
	}

	if msg := s.rejectMessageLocked(seed); msg != "" {
		writeJSON(w, http.StatusOK, map[string]string{"flag": "0", "msg": msg})
		return
	}
	for _, c := range s.opts.Courses {
		if c.KchID == seed.KchID && s.chosen[c.JxbID] {
			writeJSON(w, http.StatusOK, map[string]string{
				"flag": "0",
				"msg":  "该课程已选，不能重复选择！",
			})
			return
		}
	}
	if s.enrolledLocked(seed) >= seed.Capacity {
		writeJSON(w, http.StatusOK, map[string]string{
			"flag": "0",
			"msg":  "该教学班人数已满，请选择其他教学班！",
		})
		return
	}

	s.chosen[seed.JxbID] = true
	writeJSON(w, http.StatusOK, map[string]string{"flag": "1", "msg": "选课成功"})
}

// rejectMessageLocked 返回按教学班/全局配置的拒绝文案；没有配置时返回空。
func (s *Server) rejectMessageLocked(seed Seed) string {
	if len(s.scenario.RejectByJxb) > 0 {
		if msg, ok := s.scenario.RejectByJxb["DO-"+seed.JxbID]; ok {
			return msg
		}
		if msg, ok := s.scenario.RejectByJxb[seed.JxbID]; ok {
			return msg
		}
	}
	return s.scenario.RejectAll
}

// seedByJxbLocked 按 jxb_id 或 do_jxb_id 找教学班。
func (s *Server) seedByJxbLocked(jxb string) (Seed, bool) {
	for _, seed := range s.opts.Courses {
		if seed.JxbID == jxb || "DO-"+seed.JxbID == jxb {
			return seed, true
		}
	}
	return Seed{}, false
}

// handleCourseSelectedList 输出已选课程。
func (s *Server) handleCourseSelectedList(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	_ = r.ParseForm()
	s.mu.Lock()
	rows := make([]chosenRow, 0, len(s.chosen))
	for _, seed := range s.opts.Courses {
		if !s.chosen[seed.JxbID] {
			continue
		}
		enrolled := s.enrolledLocked(seed)
		rows = append(rows, chosenRow{
			DoJxbID: "DO-" + seed.JxbID,
			JxbID:   seed.JxbID,
			JxbMc:   seed.JxbMc,
			Kcmc:    seed.Kcmc,
			Kch:     seed.Kch,
			KchID:   seed.KchID,
			Xf:      seed.Xf,
			Kklxdm:  seed.Kklxdm,
			Sksj:    seed.Sksj,
			Jsxx:    seed.Jsxx,
			Jxbrs:   strconv.Itoa(seed.Capacity),
			Yxzrs:   strconv.Itoa(enrolled),
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, rows)
}

// handleQuitCourse 退课；成功时返回 JSON 字符串 "1"。
func (s *Server) handleQuitCourse(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	_ = r.ParseForm()
	jxb := r.PostForm.Get("jxb_ids")

	s.mu.Lock()
	seed, ok := s.seedByJxbLocked(jxb)
	if ok && s.chosen[seed.JxbID] {
		delete(s.chosen, seed.JxbID)
		s.mu.Unlock()
		writeJSONString(w, `"1"`)
		return
	}
	s.mu.Unlock()
	writeJSONString(w, `"4"`)
}

// writeJSONString 输出一段已经是 JSON 的原文（退课接口返回的是裸字符串）。
func writeJSONString(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// handleSimpleDoJxb 是“按课程查教学班”的简化接口；当前模拟端用不到，返回空数组。
func (s *Server) handleSimpleDoJxb(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// handleCourseRegistered 是“已登记课程”接口；当前模拟端用不到，返回空数组。
func (s *Server) handleCourseRegistered(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}
