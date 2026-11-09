// Package jwmock 提供本地正方教务模拟服务，专注选课流程的可控测试。
//
// 它不连接真实教务：登录、选课首页、页签参数、课程列表/详情、选课、已选、退课
// 都在内存里完成，并通过 /__mock 控制面在运行中切换拒绝规则、HTTP 故障、
// 名额阶段和会话失效，用来复现自动选课里那些在真实教务上等不到、也不可控的情况。
package jwmock

import (
	"fmt"
	"strings"
)

const (
	// DefaultBasePath 是模拟服务默认挂载的教务子路径。
	DefaultBasePath = "/jwglxt"
	// DefaultUsername / DefaultPassword 是默认测试账号。
	DefaultUsername = "20250001"
	DefaultPassword = "test123"
)

// User 是模拟教务里的一个学生账号。
type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Options 是模拟服务的初始化配置。
type Options struct {
	// BasePath 是业务接口前缀，默认 /jwglxt。
	BasePath string `json:"basePath,omitempty"`
	// Users 是账号表；为空时使用 20250001/test123。
	Users []User `json:"users,omitempty"`
	// Courses 是教学班目录；为空时使用内置的演示课程。
	Courses []Seed `json:"courses,omitempty"`
	// WindowClosed 为 true 时初始状态不在选课时间内（列表/详情可看，提交被拒）。
	WindowClosed bool `json:"windowClosed,omitempty"`
}

// Seed 是一门课的一个教学班。
//
// 容量（Capacity）只在详情接口下发；已选人数（Enrolled）会随当前状态和
// 所有账号的选择结果变化，在列表接口里每轮刷新。
type Seed struct {
	JxbID    string `json:"jxbId"`
	JxbMc    string `json:"jxbMc"`
	KchID    string `json:"kchId"`
	Kch      string `json:"kch"`
	Kcmc     string `json:"kcmc"`
	Kklxdm   string `json:"kklxdm"`
	Kclxmc   string `json:"kclxmc"`
	Kzmc     string `json:"kzmc"`
	Xf       string `json:"xf"`
	Sksj     string `json:"sksj"`
	Jxdd     string `json:"jxdd"`
	Jsxx     string `json:"jsxx"`
	Capacity int    `json:"capacity"`
	Enrolled int    `json:"enrolled"`
}

// Scenario 是运行中可替换的选课场景。
type Scenario struct {
	// WindowClosed 为 true 时所有提交返回“不在选课时间”。
	WindowClosed bool `json:"windowClosed,omitempty"`
	// MaxCredits > 0 时，已选学分 + 本次学分超过它会被拒。
	MaxCredits float64 `json:"maxCredits,omitempty"`
	// MaxCourses > 0 时，已选门数达到它后再提交会被拒。
	MaxCourses int `json:"maxCourses,omitempty"`
	// RejectAll 非空时，所有提交都返回这句话。
	RejectAll string `json:"rejectAll,omitempty"`
	// RejectByJxb 按教学班返回拒绝文案（键可以是 JXB-x 或 DO-JXB-x）。
	RejectByJxb map[string]string `json:"rejectByJxb,omitempty"`
	// RejectNext 表示某教学班接下来 N 次提交返回瞬时失败，之后恢复正常。
	RejectNext map[string]int `json:"rejectNext,omitempty"`
	// RejectNextMsg 是 RejectNext 使用的文案。
	RejectNextMsg string `json:"rejectNextMsg,omitempty"`
	// ParamExpire 表示接下来 N 次课程列表/详情请求返回“参数过期”。
	ParamExpire int `json:"paramExpire,omitempty"`
	// Faults 是 HTTP 层故障注入。
	Faults []Fault `json:"faults,omitempty"`
	// EnrolledPhases 按时间改教学班的已选人数（不改容量）。
	EnrolledPhases []EnrolledPhase `json:"enrolledPhases,omitempty"`
}

// Fault 对匹配的路径注入状态码或延迟；Count<=0 表示一直生效。
type Fault struct {
	PathContains string `json:"pathContains"`
	Status       int    `json:"status,omitempty"`
	DelayMs      int    `json:"delayMs,omitempty"`
	Count        int    `json:"count,omitempty"`
}

// EnrolledPhase 在场景生效后的第 AfterMs 毫秒，把教学班已选人数设为 map 里的值。
type EnrolledPhase struct {
	AfterMs  int            `json:"afterMs"`
	Enrolled map[string]int `json:"enrolled"`
}

func (o Options) withDefaults() (Options, error) {
	base := strings.TrimSpace(o.BasePath)
	if base == "" {
		base = DefaultBasePath
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	base = strings.TrimRight(base, "/")
	if strings.ContainsAny(base, "?#") || strings.Contains(base, "://") {
		return o, fmt.Errorf("jwmock: BasePath 不合法: %q", o.BasePath)
	}
	o.BasePath = base

	if len(o.Users) == 0 {
		o.Users = []User{{Username: DefaultUsername, Password: DefaultPassword}}
	}
	seenUser := make(map[string]bool, len(o.Users))
	for i := range o.Users {
		o.Users[i].Username = strings.TrimSpace(o.Users[i].Username)
		if o.Users[i].Username == "" || o.Users[i].Password == "" {
			return o, fmt.Errorf("jwmock: 第 %d 个账号缺少用户名或密码", i)
		}
		if seenUser[o.Users[i].Username] {
			return o, fmt.Errorf("jwmock: 账号重复: %s", o.Users[i].Username)
		}
		seenUser[o.Users[i].Username] = true
	}

	if len(o.Courses) == 0 {
		o.Courses = defaultSeeds()
	}
	seenJxb := make(map[string]bool, len(o.Courses))
	for i := range o.Courses {
		seed := o.Courses[i].withDefaults()
		if seed.JxbID == "" {
			return o, fmt.Errorf("jwmock: 第 %d 门课缺少 JxbID", i)
		}
		if seenJxb[seed.JxbID] {
			return o, fmt.Errorf("jwmock: 教学班重复: %s", seed.JxbID)
		}
		seenJxb[seed.JxbID] = true
		o.Courses[i] = seed
	}
	return o, nil
}

func (s Seed) withDefaults() Seed {
	s.JxbID = strings.TrimSpace(s.JxbID)
	s.KchID = strings.TrimSpace(s.KchID)
	s.Kch = strings.TrimSpace(s.Kch)
	s.Kcmc = strings.TrimSpace(s.Kcmc)
	if s.KchID == "" {
		s.KchID = s.JxbID
	}
	if s.Kch == "" {
		s.Kch = s.KchID
	}
	if s.Kcmc == "" {
		s.Kcmc = s.KchID
	}
	if s.JxbMc == "" {
		s.JxbMc = s.Kcmc
	}
	if s.Kklxdm == "" {
		s.Kklxdm = "10"
	}
	if s.Kclxmc == "" {
		s.Kclxmc = "公共基础课"
	}
	if s.Kzmc == "" {
		s.Kzmc = "必修"
	}
	if s.Xf == "" {
		s.Xf = "0"
	}
	if s.Capacity <= 0 {
		s.Capacity = 30
	}
	if s.Enrolled < 0 {
		s.Enrolled = 0
	}
	if s.Enrolled > s.Capacity {
		s.Enrolled = s.Capacity
	}
	return s
}

// defaultSeeds 是默认课程表：一门课两个教学班（一个尚有名额、一个已满），
// 外加一门必选课和一门公选课，方便验证排序、满员和愿望匹配。
func defaultSeeds() []Seed {
	return []Seed{
		{
			JxbID: "JXB-MATH-01", JxbMc: "高等数学-01班", KchID: "KCH-MATH", Kch: "MATH101",
			Kcmc: "高等数学", Kklxdm: "10", Kclxmc: "公共基础课", Kzmc: "必修", Xf: "4",
			Sksj: "周一第1-2节", Jxdd: "教学楼-101", Jsxx: "320057/陈爱华/副教授",
			Capacity: 2, Enrolled: 1,
		},
		{
			JxbID: "JXB-MATH-02", JxbMc: "高等数学-02班", KchID: "KCH-MATH", Kch: "MATH101",
			Kcmc: "高等数学", Kklxdm: "10", Kclxmc: "公共基础课", Kzmc: "必修", Xf: "4",
			Sksj: "周二第1-2节", Jxdd: "教学楼-102", Jsxx: "320061/李强/讲师",
			Capacity: 2, Enrolled: 2,
		},
		{
			JxbID: "JXB-ENG-01", JxbMc: "大学英语-01班", KchID: "KCH-ENG", Kch: "ENG101",
			Kcmc: "大学英语", Kklxdm: "10", Kclxmc: "公共基础课", Kzmc: "必修", Xf: "3",
			Sksj: "周三第3-4节", Jxdd: "外语楼-201", Jsxx: "320102/王芳/副教授",
			Capacity: 40, Enrolled: 5,
		},
		{
			JxbID: "JXB-ART-01", JxbMc: "西方美术鉴赏-01班", KchID: "KCH-ART", Kch: "ART201",
			Kcmc: "西方美术鉴赏", Kklxdm: "10", Kclxmc: "公共选修课", Kzmc: "公共艺术", Xf: "2",
			Sksj: "周五第5-6节", Jxdd: "艺术楼-201", Jsxx: "320061/殷海华/教授",
			Capacity: 30, Enrolled: 0,
		},
	}
}
