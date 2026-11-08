package web

import (
	"slices"
	"strings"
	"time"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// 长任务（登录 / 初始化）的默认最长等待时间。
// 3 分钟对"需要人工输入验证码 / 新密码"的流程经常不够，所以放宽到 10 分钟，
// 并允许每个账号在 WebUI 里单独配置。
const (
	defaultLoginTimeout  = "10m"
	fallbackLoginTimeout = 10 * time.Minute
)

// 登录方式。password / cas / wechat 三种方式共用同一条 engine→protocol 链路，
// 差别只在 protocol.NewAPIClient 的 wx 参数；二维码由宿主（Web/TUI）呈现。
const (
	LoginModePassword = "password"
	LoginModeCAS      = "cas"
	LoginModeWeChat   = "wechat"
)

// Account 持久化账号信息（不含运行态）。
type Account struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	BaseURL      string   `json:"baseUrl"`
	Username     string   `json:"username"`
	Password     string   `json:"password,omitempty"`
	CasPassword  string   `json:"casPassword,omitempty"`
	CasLogin     bool     `json:"casLogin"`            // 旧字段：是否走 CAS2 密码登录；由 LoginMode 反向同步
	LoginMode    string   `json:"loginMode,omitempty"` // password | cas | wechat
	UserAgent    string   `json:"userAgent"`
	Timeout      string   `json:"timeout"`
	LoginTimeout string   `json:"loginTimeout"` // 登录/初始化任务的最长等待时间（Go duration）
	Hedging      bool     `json:"hedging"`      // 是否启用请求对冲（hedging）
	HedgingDelay string   `json:"hedgingDelay"` // 对冲请求之间的间隔
	Routes       []string `json:"routes"`       // 自定义 route cookie 列表（空 = 使用内置默认）

	// AutoRelogin 是"会话失效时是否自动重新登录一次"的开关（默认开启）。
	// 用指针是为了区分"老账号没写过这个字段"（按开启处理）和"显式关闭"；
	// 它只影响 engine 的重登策略，不属于 protocol 配置。
	AutoRelogin *bool `json:"autoRelogin,omitempty"`

	// AutoNotify 是"自动选课结束时是否发结果邮件"的账号级开关（默认开启）。
	// 服务端配置里的 smtp.enabled / smtp.notifyAutoChoose 仍是总开关，这里只
	// 决定这个账号是否参与通知。指针的语义与 AutoRelogin 相同：老账号没有该
	// 字段时按开启处理，显式 false 才关闭。
	AutoNotify *bool `json:"autoNotify,omitempty"`

	LearnedRoutes []string `json:"learnedRoutes,omitempty"` // 登录过程中学到的 route（client 回传，不是用户偏好）
	TicketJWT     string   `json:"ticketJWT,omitempty"`     // CAS2 门户登录 JWT 缓存

	// Identity 是登录证明的身份（学号 / 姓名 / 学校）。
	// 扫码登录时它是登录的产物：先扫码拿到 ticket，才知道登的是谁、该把这次
	// 会话记到哪个账号上。密码 / CAS 登录也会顺手记一份（ticket 里就有），
	// 但没有也不影响任何流程。
	Identity *config.Identity `json:"identity,omitempty"`

	// 高级选课（自动选课）的愿望清单：课程名 / 教师 / 课程类型，任填一项即可。
	// 存在账号里而不是会话里，登录/初始化/重启后都还在，WebUI 与 CLI 共用。
	WantClasses  []string `json:"wantClasses,omitempty"`
	WantTeachers []string `json:"wantTeachers,omitempty"`
	WantTypes    []string `json:"wantTypes,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	LastLogin time.Time `json:"lastLogin"`
}

// normalize 补齐缺省值并校正非法输入。
// 调用方需保证 a 已被独占持有（创建 / 更新 / 加载阶段）。
func (a *Account) normalize() {
	// 默认值只有一处来源（config.Default），这里不再写死 "43s" / "21s"。
	// 非法值（空、负数、缺单位）一律回落到默认值：磁盘上的脏数据不能让
	// 登录秒超时，也不能让 client 在运行时去改配置。
	if _, err := config.ParseDuration(a.Timeout); err != nil {
		a.Timeout = config.Default().Timeout.String()
	}
	if _, err := config.ParseDuration(a.HedgingDelay); err != nil {
		a.HedgingDelay = config.Default().HedgingDelay.String()
	}
	// 登录超时必须合法，否则回落到默认值（避免磁盘上的脏值让登录秒超时）
	if _, err := config.ParseDuration(a.LoginTimeout); err != nil {
		a.LoginTimeout = defaultLoginTimeout
	}
	// UA 必须能被 strings.Cut(ua, "/") 正常切分，否则回落到默认 UA
	a.UserAgent = config.NormalizeUA(a.UserAgent)
	// 历史账号可能存着登录页地址：统一收敛成 scheme://host/，
	// 避免 client 在请求时悄悄截断（截断后的地址与用户填的不一致）。
	a.BaseURL = config.NormalizeBaseURL(a.BaseURL)
	// 自动重登：老账号没有这个字段，默认按开启处理；显式 false 才关闭。
	if a.AutoRelogin == nil {
		enabled := true
		a.AutoRelogin = &enabled
	}
	// 自动选课结果邮件：与自动重登一致，老账号默认开启。总开关在服务端配置里，
	// 账号级开关只能把它再关掉，不能绕过 smtp.enabled 单独发信。
	if a.AutoNotify == nil {
		enabled := true
		a.AutoNotify = &enabled
	}
	// 登录方式：loginMode 优先；历史账号只有 casLogin，按它推导。
	// CasLogin 保留为兼容镜像，只表示"CAS 密码登录"，不再承载 wechat。
	a.LoginMode = a.effectiveLoginMode()
	a.CasLogin = a.LoginMode == LoginModeCAS
	// 愿望清单来自 WebUI/配置文件，可能是手输的：去掉空行与首尾空白，去重。
	a.WantClasses = cleanWishItems(a.WantClasses)
	a.WantTeachers = cleanWishItems(a.WantTeachers)
	a.WantTypes = cleanWishItems(a.WantTypes)
	// 没有学号的身份等于没有身份：留着会让扫码绑定匹配到一个空键。
	if a.Identity.Empty() {
		a.Identity = nil
	}
}

// autoReloginEnabled 返回账号是否开启自动重登；nil（老账号 / 未 normalize）
// 按开启处理，保证默认行为不变。
func (a *Account) autoReloginEnabled() bool {
	return a == nil || a.AutoRelogin == nil || *a.AutoRelogin
}

// autoNotifyEnabled 返回账号是否接收自动选课结果邮件；nil（老账号 / 未 normalize）
// 按开启处理，保证默认行为不变。服务端 SMTP 总开关不在这个判断里。
func (a *Account) autoNotifyEnabled() bool {
	return a == nil || a.AutoNotify == nil || *a.AutoNotify
}

// validLoginMode 报告 mode 是否是已知的登录方式。
func validLoginMode(mode string) bool {
	switch mode {
	case LoginModePassword, LoginModeCAS, LoginModeWeChat:
		return true
	default:
		return false
	}
}

// effectiveLoginMode 返回当前生效的登录方式（不会修改 Account）。
// 新字段 loginMode 优先；为空或非法时回落到历史字段 casLogin。
func (a *Account) effectiveLoginMode() string {
	if validLoginMode(a.LoginMode) {
		return a.LoginMode
	}
	if a.CasLogin {
		return LoginModeCAS
	}
	return LoginModePassword
}

// wish 把账号里的愿望清单转成协议层模型。
func (a *Account) wish() protocol.WishList {
	return protocol.WishList{
		WantClassList:   slices.Clone(a.WantClasses),
		WantTeacherList: slices.Clone(a.WantTeachers),
		WantTypeList:    slices.Clone(a.WantTypes),
	}
}

// applyWish 把愿望清单写回账号（清洗后）。
func (a *Account) applyWish(w protocol.WishList) {
	a.WantClasses = cleanWishItems(w.WantClassList)
	a.WantTeachers = cleanWishItems(w.WantTeacherList)
	a.WantTypes = cleanWishItems(w.WantTypeList)
}

// cleanWishItems 去掉空白项与重复项；全空时返回 nil，让 JSON 里直接省略该字段。
func cleanWishItems(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// loginTimeoutDuration 返回该账号配置的长任务超时；配置缺失/非法时回落默认值。
func (a *Account) loginTimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(a.LoginTimeout); err == nil && d > 0 {
		return d
	}
	return fallbackLoginTimeout
}

// session 把持久化的会话产物装回 client 需要的形态。
func (a *Account) session() *config.Session {
	s := config.NewSession()
	s.TicketJWT = a.TicketJWT
	// 身份也装回去：登录成功之后才能靠它认出"这个账号是谁"，
	// 而重建 runtime 时若丢了它，账号就只剩用户名可猜了。
	s.Identity = a.Identity.Clone()
	s.LearnedRoutes = slices.Clone(a.LearnedRoutes)
	return s
}

// applySession 把 client 登录后的会话快照写回账号记录。
// client 不再自己写文件之后，这里是会话产物唯一的落盘点。
func (a *Account) applySession(s config.Session) {
	a.TicketJWT = s.TicketJWT
	a.LearnedRoutes = s.LearnedRoutes
	// 身份只在这次登录确实拿到了才覆盖：密码登录拿不到身份时，
	// 不能把上一次扫码记下的姓名/学校抹掉。
	if s.Identity != nil {
		a.Identity = s.Identity.Clone()
	}
	// 登录过程中服务端暴露出的真实地址（302 修正）：落盘后下次启动不用再修一遍。
	if s.CorrectedBaseURL != "" {
		a.BaseURL = s.CorrectedBaseURL
	}
}

// settingsDuration 解析账号里的时长字符串，非法时回落到 fallback。
// settings 由 toSettings 构造；运行时拿到的 Duration 一定合法。
func settingsDuration(s string, fallback config.Duration) config.Duration {
	if d, err := config.ParseDuration(s); err == nil {
		return config.Duration(d)
	}
	return fallback
}

// toSettings 把账号记录转成 client 需要的只读配置。
// 这是 Accounts ↔ Settings 的唯一映射点：加字段时改这里一处，编译器会提醒所有漏改的地方。
func (a *Account) toSettings() config.Settings {
	d := config.Default()
	d.BaseURL = config.NormalizeBaseURL(a.BaseURL)
	d.Account = a.Username
	d.Password = a.Password
	d.CasPassword = a.CasPassword
	d.UserAgent = config.NormalizeUA(a.UserAgent)
	d.Timeout = settingsDuration(a.Timeout, d.Timeout)
	d.Hedging = a.Hedging
	d.HedgingDelay = settingsDuration(a.HedgingDelay, d.HedgingDelay)
	d.Routes = slices.Clone(a.Routes)
	return d
}
