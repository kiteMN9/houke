package jwmock

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

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

func (s *Server) handleIndex(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8",
		[]byte(indexPage("模拟学生", time.Now().Format("2006-01-02 15:04:05"))))
}

func (s *Server) handleTabParams(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(tabPage()))
}

func (s *Server) handleCourseList(c *gin.Context) {
	if s.takeParamExpire() {
		// 参数过期响应故意不带 tmpList：客户端会先判断 tmpList 再判断 msg/flag。
		c.JSON(http.StatusOK, gin.H{"msg": "参数已过期，请刷新页面重试", "flag": "0"})
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

	c.JSON(http.StatusOK, gin.H{
		"tmpList": rows,
		"sfxsjc":  "0",
		"msg":     "",
		"flag":    "",
	})
}

func (s *Server) handleCourseDetail(c *gin.Context) {
	if s.takeParamExpire() {
		c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(`"0"`))
		return
	}

	kchID := strings.TrimSpace(c.PostForm("kch_id"))
	s.mu.Lock()
	rows := make([]courseDetailRow, 0, len(s.opts.Courses))
	for _, seed := range s.opts.Courses {
		if kchID != "" && seed.KchID != kchID {
			continue
		}
		enrolled := strconv.Itoa(s.enrolledLocked(seed))
		rows = append(rows, courseDetailRow{
			DoJxbID: "DO-" + seed.JxbID,
			JxbID:   seed.JxbID,
			JxbMc:   seed.JxbMc,
			Jxbrs:   enrolled,
			Jxbrl:   strconv.Itoa(seed.Capacity),
			Sksj:    seed.Sksj,
			Jxdd:    seed.Jxdd,
			Jsxx:    seed.Jsxx,
			Yxzrs:   enrolled,
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

	c.JSON(http.StatusOK, rows)
}

// handleChoose 按真实选课接口的优先级判定：会话 -> 时间窗口 -> 固定拒绝 -> 重复 ->
// 容量 -> 时间冲突 -> 学分上限 -> 门数上限 -> 瞬时失败 -> 成功。
func (s *Server) handleChoose(c *gin.Context) {
	user, ok := s.userOf(c)
	if !ok {
		s.kick(c)
		return
	}
	jxbIDs := c.PostForm("jxb_ids")
	kchID := c.PostForm("kch_id")

	s.mu.Lock()
	flag, msg := s.chooseLocked(user, jxbIDs, kchID)
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{"flag": flag, "msg": msg})
}

func (s *Server) chooseLocked(user, jxbIDs, kchID string) (string, string) {
	seed, ok := s.findSeedLocked(jxbIDs, kchID)
	if !ok {
		return "0", "教学班不存在或已失效"
	}
	sc := s.scenario
	if sc.WindowClosed {
		return "0", "当前不在选课时间内"
	}
	if msg := s.rejectionLocked(seed); msg != "" {
		return "0", msg
	}
	if sc.RejectAll != "" {
		return "0", sc.RejectAll
	}

	set := s.userChosenLocked(user)
	if set[seed.JxbID] {
		return "0", "该教学班已选，不能重复选择！"
	}
	if s.sameCourseLocked(user, seed) {
		return "0", "该课程已选，不能重复选择！"
	}
	if seed.Capacity > 0 && s.enrolledLocked(seed) >= seed.Capacity {
		return "0", "该教学班人数已满，请选择其他教学班！"
	}
	if s.timeConflictLocked(user, seed) {
		return "0", "该教学班与已选课程时间冲突！"
	}
	if sc.MaxCredits > 0 && s.creditsLocked(user)+parseCredit(seed.Xf) > sc.MaxCredits {
		return "0", "超过学分上限！"
	}
	if sc.MaxCourses > 0 && len(set) >= sc.MaxCourses {
		return "0", "选课门数已达上限！"
	}
	if n := s.scenario.RejectNext[seed.JxbID]; n > 0 {
		s.scenario.RejectNext[seed.JxbID] = n - 1
		msg := s.scenario.RejectNextMsg
		if msg == "" {
			msg = "系统繁忙，请稍后再试"
		}
		return "0", msg
	}

	set[seed.JxbID] = true
	return "1", "选课成功"
}

func (s *Server) handleSelected(c *gin.Context) {
	user, ok := s.userOf(c)
	if !ok {
		s.kick(c)
		return
	}

	s.mu.Lock()
	set := s.chosen[user]
	rows := make([]chosenRow, 0, len(set))
	for _, jxb := range sortedChosen(set) {
		seed, ok := s.seedIndex[jxb]
		if !ok {
			continue
		}
		enrolled := strconv.Itoa(s.enrolledLocked(seed))
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
			Jxbrs:   enrolled,
			Yxzrs:   enrolled,
		})
	}
	s.mu.Unlock()

	c.JSON(http.StatusOK, rows)
}

func (s *Server) handleQuit(c *gin.Context) {
	user, ok := s.userOf(c)
	if !ok {
		s.kick(c)
		return
	}
	jxbIDs := c.PostForm("jxb_ids")
	kchID := c.PostForm("kch_id")

	removed := false
	s.mu.Lock()
	if seed, ok := s.findSeedLocked(jxbIDs, kchID); ok {
		if set, ok := s.chosen[user]; ok && set[seed.JxbID] {
			delete(set, seed.JxbID)
			removed = true
		}
	}
	s.mu.Unlock()

	body := `"1"`
	if !removed {
		body = `"4"`
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(body))
}

func (s *Server) handleSimple(c *gin.Context) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte("[]"))
}

func (s *Server) handleRegistered(c *gin.Context) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte("[]"))
}

func (s *Server) takeParamExpire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeParamExpireLocked()
}

// findSeedLocked 支持 DO-JXB-x / JXB-x / kch_id 三种定位方式。
func (s *Server) findSeedLocked(jxbIDs, kchID string) (Seed, bool) {
	for _, part := range strings.Split(jxbIDs, ",") {
		id := normalizeJxbID(part)
		if id == "" {
			continue
		}
		if seed, ok := s.seedIndex[id]; ok {
			return seed, true
		}
	}
	if kchID != "" {
		for _, seed := range s.opts.Courses {
			if seed.KchID == kchID {
				return seed, true
			}
		}
	}
	return Seed{}, false
}
