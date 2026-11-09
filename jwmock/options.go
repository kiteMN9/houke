// Package jwmock 是正方教务系统（jwglxt）的本地模拟服务端。
//
// 它只实现 school_sdk 真正会走的那部分接口：登录页 / RSA 公钥 / 图形验证码 /
// 滑块验证码 / 选课首页参数 / 页签参数 / 课程列表 / 课程详情 / 选课 / 已选 /
// 退课。默认不连任何真实教务，所有状态都在内存里，因此可以在测试中稳定复现
// “验证码、业务拒绝、5xx、限流、掉线、名额分阶段放出”这些很难在真实教务上
// 等到的场景。
//
// 最小用法：
//
//	srv, err := jwmock.New(jwmock.Options{})
//	if err != nil { ... }
//	ts := httptest.NewServer(srv)
//	defer ts.Close()
//	cfg := config.Settings{
//		BaseURL: ts.URL + srv.BasePath() + "/",
//		Account: "20250001",
//		Password: "test123",
//	}
//
// 控制面挂在固定的 /__mock/ 前缀下（不受 Options.BasePath 影响），可以在
// 运行中替换场景、踢掉会话、查看请求流水。
package jwmock

import (
	"fmt"
	"strings"
)

// LoginMode 决定登录页暴露哪一种人机校验。
type LoginMode string

const (
	// LoginNone 不出现验证码，登录页只有 csrftoken。
	LoginNone LoginMode = "none"
	// LoginKaptcha 出现字符验证码（#yzmDiv + /kaptcha 图片）。
	LoginKaptcha LoginMode = "kaptcha"
	// LoginSlider 出现滑块验证码（.captcha_wrapper + /zfcaptchaLogin）。
	LoginSlider LoginMode = "slider"
)

// 默认值。Options 里的空字段会被这些值补齐。
const (
	DefaultBasePath    = "/jwglxt"
	DefaultUsername    = "20250001"
	DefaultPassword    = "test123"
	DefaultKaptchaCode = "1234"
)

// Options 是模拟服务端的静态配置。
//
// 它只在 New 时读取一次；运行中可变的东西（验证码、拒绝文案、故障）走
// Scenario / SetScenario。
type Options struct {
	// BasePath 是模拟教务的根路径，默认 /jwglxt。
	// 客户端 BaseURL 应写成 http://host:port<BasePath>/。
	BasePath string

	// Username / Password 是唯一允许登录的账号；默认 20250001 / test123。
	Username string
	Password string

	// LoginMode 是初始登录形态，默认 LoginNone。
	LoginMode LoginMode

	// KaptchaCode 是字符验证码的正确答案，默认 1234。
	KaptchaCode string

	// Courses 是课程目录；为空时用内置的演示课程。
	Courses []Seed
}

// Seed 描述一门教学班。
//
// 课程列表里出现的是 JxbID/JxbMc/KchID/Kch/Kcmc 等展示字段；容量和“已选人数”
// 只在详情/列表里出现，分别对应 jxbrl / yxzrs。
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

// Scenario 是可以在运行中替换的一组“异常剧本”。零值表示一切正常。
type Scenario struct {
	// LoginMode / KaptchaCode 为空时沿用 Options 里的默认值。
	LoginMode   LoginMode `json:"loginMode,omitempty"`
	KaptchaCode string    `json:"kaptchaCode,omitempty"`

	// RejectAll 不为空时，所有选课请求都返回这条失败文案（flag=0）。
	RejectAll string `json:"rejectAll,omitempty"`
	// RejectByJxb 按教学班覆盖失败文案，key 可以是 jxb_id 或 do_jxb_id。
	RejectByJxb map[string]string `json:"rejectByJxb,omitempty"`

	// ParamExpire 是“参数过期”响应的次数：课程列表返回 msg/flag，课程详情
	// 返回 "0"。用于验证 engine 的 TakeParamRefresh -> Renew 链路。
	ParamExpire int `json:"paramExpire,omitempty"`

	// Faults 是 HTTP 层故障；按请求路径匹配，命中后计数并返回指定状态码
	// 或先延迟再继续。
	Faults []Fault `json:"faults,omitempty"`

	// EnrolledPhases 按时间修改每门教学班的“已选人数”（不是容量）。
	//
	// 详情里的容量 jxbrl 只会被 engine 抓取一次，改容量传不回缓存；
	// 而列表里的 yxzrs 每轮刷新都会更新，所以“名额分阶段放出”应当改这里。
	EnrolledPhases []EnrolledPhase `json:"enrolledPhases,omitempty"`
}

// Fault 是一次 HTTP 故障注入。
//
// Count 是命中次数（按 HTTP 请求计，不是按业务调用计）：0 表示不限次数。
// 注意客户端对 5xx 会自动重试 3 次，所以一次业务请求可能消耗多个 Count。
type Fault struct {
	PathContains string `json:"pathContains"`
	Status       int    `json:"status,omitempty"`
	DelayMs      int    `json:"delayMs,omitempty"`
	Count        int    `json:"count,omitempty"`
}

// EnrolledPhase 在 Scenario 生效后的 AfterMs 毫秒起把指定教学班的已选人数
// 改成 Enrolled 里的值；按时间顺序后面的阶段覆盖前面的。
type EnrolledPhase struct {
	AfterMs  int            `json:"afterMs"`
	Enrolled map[string]int `json:"enrolled"`
}

// withDefaults 归一化 Options 并补齐课程默认字段；返回值是内部使用的副本。
func (o Options) withDefaults() (Options, error) {
	if o.BasePath == "" {
		o.BasePath = DefaultBasePath
	}
	if !strings.HasPrefix(o.BasePath, "/") {
		o.BasePath = "/" + o.BasePath
	}
	o.BasePath = strings.TrimRight(o.BasePath, "/")
	if o.BasePath == "" {
		o.BasePath = DefaultBasePath
	}

	switch o.LoginMode {
	case "", LoginNone:
		o.LoginMode = LoginNone
	case LoginKaptcha, LoginSlider:
	default:
		return o, fmt.Errorf("jwmock: 未知登录形态 %q（可选 none / kaptcha / slider）", o.LoginMode)
	}

	if o.Username == "" {
		o.Username = DefaultUsername
	}
	if o.Password == "" {
		o.Password = DefaultPassword
	}
	if o.KaptchaCode == "" {
		o.KaptchaCode = DefaultKaptchaCode
	}

	seeds := o.Courses
	if len(seeds) == 0 {
		seeds = defaultSeeds()
	}
	out := make([]Seed, len(seeds))
	seen := make(map[string]bool, len(seeds))
	for i, s := range seeds {
		if s.JxbID == "" {
			s.JxbID = fmt.Sprintf("JXB-%02d", i+1)
		}
		s = s.withDefaults()
		if seen[s.JxbID] {
			return o, fmt.Errorf("jwmock: 教学班 %q 重复", s.JxbID)
		}
		seen[s.JxbID] = true
		if s.Capacity <= 0 {
			return o, fmt.Errorf("jwmock: 教学班 %q 的容量必须为正数", s.JxbID)
		}
		if s.Enrolled < 0 || s.Enrolled > s.Capacity {
			return o, fmt.Errorf("jwmock: 教学班 %q 的已选人数 %d 不在 0..%d 之间", s.JxbID, s.Enrolled, s.Capacity)
		}
		out[i] = s
	}
	o.Courses = out
	return o, nil
}

// withDefaults 补齐一门教学班的展示字段。
func (s Seed) withDefaults() Seed {
	if s.Kcmc == "" {
		s.Kcmc = "示例课程"
	}
	if s.JxbMc == "" {
		s.JxbMc = s.Kcmc + "-01班"
	}
	if s.KchID == "" {
		s.KchID = "KCH-" + s.JxbID
	}
	if s.Kch == "" {
		s.Kch = s.KchID
	}
	if s.Kklxdm == "" {
		s.Kklxdm = "10"
	}
	if s.Kclxmc == "" {
		s.Kclxmc = "公共选修课"
	}
	if s.Kzmc == "" {
		s.Kzmc = "选修"
	}
	if s.Xf == "" {
		s.Xf = "2"
	}
	if s.Sksj == "" {
		s.Sksj = "周一第1-2节"
	}
	if s.Jxdd == "" {
		s.Jxdd = "教1-101"
	}
	if s.Jsxx == "" {
		s.Jsxx = "320057/陈爱华/副教授"
	}
	if s.Capacity <= 0 {
		s.Capacity = 2
	}
	return s
}

// defaultSeeds 是内置演示目录：同一门课的两个班（一个未满、一个满员），
// 外加一门教师不同的课，方便演示愿望清单的排序与筛选。
func defaultSeeds() []Seed {
	return []Seed{
		{
			JxbID: "JXB-MATH-01", JxbMc: "高等数学-01班", KchID: "KCH-MATH", Kch: "MATH101",
			Kcmc: "高等数学", Kklxdm: "10", Kclxmc: "公共基础课", Kzmc: "必修",
			Xf: "4", Sksj: "周一第1-2节{第1-16周}", Jxdd: "教1-101",
			Jsxx: "320057/陈爱华/副教授", Capacity: 2, Enrolled: 1,
		},
		{
			JxbID: "JXB-MATH-02", JxbMc: "高等数学-02班", KchID: "KCH-MATH", Kch: "MATH101",
			Kcmc: "高等数学", Kklxdm: "10", Kclxmc: "公共基础课", Kzmc: "必修",
			Xf: "4", Sksj: "周三第3-4节{第1-16周}", Jxdd: "教1-102",
			Jsxx: "320058/李强/讲师", Capacity: 2, Enrolled: 2,
		},
		{
			JxbID: "JXB-ART-01", JxbMc: "西方美术鉴赏-01班", KchID: "KCH-ART", Kch: "ART201",
			Kcmc: "西方美术鉴赏", Kklxdm: "10", Kclxmc: "公共选修课", Kzmc: "公共艺术",
			Xf: "2", Sksj: "周五第5-6节{第1-8周}", Jxdd: "艺术楼-201",
			Jsxx: "320061/殷海华/教授", Capacity: 30, Enrolled: 0,
		},
	}
}
