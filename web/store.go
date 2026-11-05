package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"school_sdk/check_code/kaptcha"
	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// AccountStore 管理所有账号 + 运行态。
type AccountStore struct {
	mu       sync.RWMutex
	filename string
	order    []string
	accounts map[string]*Account
	runtimes map[string]*accountRuntime

	// 长任务（登录/初始化）的可观测状态；随账号删除一并清理。
	tasks *taskStore

	// 字符验证码识别器：所有账号共用一份模型（约 11 MB），运行态拿到的是
	// 这个稳定指针，因此 WebUI 里换模型 / 开关识别会立即对所有账号生效。
	kaptchaMgr *kaptcha.Manager

	// 跨账号共享的底层连接池：账号共用 transport，cookie jar / resty 客户端
	// 仍每账号独立（见 protocol.TransportPool）。账号配置变更重建运行态时
	// 不清池，只在服务关闭时统一关空闲连接。
	transports *protocol.TransportPool
}

// Kaptcha 返回字符验证码配置管理器；WebUI 的模型配置页会用到。
func (s *AccountStore) Kaptcha() *kaptcha.Manager { return s.kaptchaMgr }

// kaptchaRecognizer 返回自动识别器。Manager 永远非 nil，是否真的启用由
// protocol 通过可选的 KaptchaEnabled() 接口动态判断。
func (s *AccountStore) kaptchaRecognizer() protocol.KaptchaRecognizer {
	return s.kaptchaMgr
}

func NewAccountStore(filename string) (*AccountStore, error) {
	mgr, err := kaptcha.NewManager(filepath.Join(filepath.Dir(filename), "kaptcha.json"), kaptcha.DefaultSettings())
	if err != nil {
		if mgr == nil {
			return nil, err
		}
		// 配置损坏不阻止启动：配置页还能重新保存一份合法配置。
		log.Printf("字符验证码配置有问题，暂用默认设置: %v", err)
	}
	s := &AccountStore{
		filename:   filename,
		accounts:   make(map[string]*Account),
		runtimes:   make(map[string]*accountRuntime),
		tasks:      newTaskStore(),
		kaptchaMgr: mgr,
		transports: protocol.NewTransportPool(),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *AccountStore) load() error {
	data, err := os.ReadFile(s.filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) < 2 {
		return nil
	}
	var list []*Account
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("账号文件解析失败: %w", err)
	}
	for _, a := range list {
		a.normalize()
		s.accounts[a.ID] = a
		s.order = append(s.order, a.ID)
	}
	return nil
}

func (s *AccountStore) saveLocked() error {
	list := make([]*Account, 0, len(s.order))
	for _, id := range s.order {
		if a, ok := s.accounts[id]; ok {
			list = append(list, a)
		}
	}
	data, err := json.Marshal(list, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	tmp := s.filename + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filename)
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// List 按创建顺序返回账号列表；会话凭证（TicketJWT）不出前端。
func (s *AccountStore) List() []*Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Account, 0, len(s.order))
	for _, id := range s.order {
		if a, ok := s.accounts[id]; ok {
			c := *a
			//c.Password = ""
			//c.CasPassword = ""
			c.TicketJWT = "" // 会话凭证不出前端，与 Get 保持一致
			out = append(out, &c)
		}
	}
	return out
}

// Get 按 ID 返回账号副本；会话凭证（TicketJWT）不出前端。
func (s *AccountStore) Get(id string) (*Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	if !ok {
		return nil, false
	}
	c := *a
	//c.Password = ""
	//c.CasPassword = ""
	c.TicketJWT = ""
	return &c, true
}

// getRaw 返回原始账号（含密码），仅内部使用。
func (s *AccountStore) getRaw(id string) (*Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	return a, ok
}

func (s *AccountStore) Create(a *Account) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = newID()
	}
	if _, exists := s.accounts[a.ID]; exists {
		return nil, fmt.Errorf("账号ID已存在")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.normalize()
	s.accounts[a.ID] = a
	s.order = append(s.order, a.ID)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	c := *a
	c.Password = ""
	c.CasPassword = ""
	return &c, nil
}

// UpsertWechat 把一次微信扫码得到的身份绑到账号上。
//
// 扫码登录的顺序和别的登录是反的：先有 ticket，才知道登的是谁，然后才需要
// 一个账号来装这次会话。所以这里做的是"按身份找账号，找不到就建一个"：
//   - 已经记着同一个人的账号 → 直接把这次会话写进去；
//   - 用户名就是学号的旧账号（扫码上线前建的，没有 identity）→ 认领它，
//     否则同一个人的账号会凭空多出一份；
//   - 都没有 → 新建，用户名取学号，名字取 ticket 里的姓名。
//
// 第二个返回值报告是否新建了账号，界面用它区分"已绑定"和"已新建"。
func (s *AccountStore) UpsertWechat(id config.Identity, sess config.Session) (*Account, bool, error) {
	if id.Empty() {
		return nil, false, fmt.Errorf("ticket 里没有身份标识，无法绑定账号")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if a, byIdentity := s.findBoundLocked(id); a != nil {
		a.Identity = id.Clone()
		a.LoginMode = LoginModeWeChat
		a.CasLogin = false
		if !byIdentity {
			// 靠用户名认出来的账号：学号就是身份，姓名只在还没有名字时补上，
			// 不覆盖用户自己起的名字。
			a.Username = id.Sub
			if strings.TrimSpace(a.Name) == "" {
				a.Name = id.Display()
			}
		}
		a.applySession(sess)
		a.LastLogin = time.Now()
		if err := s.saveLocked(); err != nil {
			return nil, false, err
		}
		return publicAccount(a), false, nil
	}

	a := &Account{
		ID:        newID(),
		Name:      id.Display(),
		BaseURL:   config.DefaultBaseURL,
		Username:  id.Sub,
		LoginMode: LoginModeWeChat,
		Identity:  id.Clone(),
		CreatedAt: time.Now(),
		LastLogin: time.Now(),
	}
	a.normalize()
	a.applySession(sess)
	s.accounts[a.ID] = a
	s.order = append(s.order, a.ID)
	if err := s.saveLocked(); err != nil {
		// 落盘失败就别留下半成品账号：调用方会报错，界面刷新后也看不到它。
		delete(s.accounts, a.ID)
		s.order = s.order[:len(s.order)-1]
		return nil, false, err
	}
	return publicAccount(a), true, nil
}

// findBoundLocked 按身份找已有账号。byIdentity 报告是否靠 identity 匹配上的；
// 靠用户名匹配时调用方需要补写身份（旧账号没有 identity）。
func (s *AccountStore) findBoundLocked(id config.Identity) (a *Account, byIdentity bool) {
	var byName *Account
	for _, aid := range s.order {
		acc := s.accounts[aid]
		if acc == nil {
			continue
		}
		if acc.Identity.SamePerson(&id) {
			return acc, true
		}
		// 没有 identity 的账号只能靠用户名认：用户名就是学号时视为同一个人。
		// 但学号本身不唯一（不同学校会撞号），所以还要求账号是同一所学校的：
		// 扫码只对应写死的那套门户，默认地址就是它的学校。
		if acc.Username != id.Sub || !acc.Identity.Empty() || !sameSchoolAsScan(acc) {
			continue
		}
		if acc.effectiveLoginMode() == LoginModeWeChat {
			// 扫码上线前建的微信账号：明显就是同一个人，优先认领。
			return acc, false
		}
		if byName == nil {
			byName = acc
		}
	}
	return byName, false
}

// sameSchoolAsScan 判断老账号是不是这次扫码那所学校的。
//
// 微信扫码只能登写死的那套门户（见 cas2 的 wxEndpoints），而老账号没有身份信息，
// 地址是唯一能看的线索：地址对不上就别认——宁可多一个账号，也不能把另一所
// 学校的账号改写成这所学校。
func sameSchoolAsScan(a *Account) bool {
	if a == nil {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(a.BaseURL))
	if err != nil {
		return false
	}
	def, err := url.Parse(config.DefaultBaseURL)
	if err != nil {
		return false
	}
	return u.Hostname() != "" && strings.EqualFold(u.Hostname(), def.Hostname())
}

// AdoptRuntime 把一次匿名扫码建立起来的运行态挂到账号 id 下。
//
// 不能重建：登录态记在 client 内存里（isLoggedIn），拿刚落盘的 JWT 重新构造
// client 会得到一个"有票但没登录"的引擎；而微信登录不允许静默重登，
// 结果就是扫码明明成功了，却什么也用不了。
func (s *AccountStore) AdoptRuntime(id string, rt *accountRuntime) {
	if rt == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.accounts[id]; ok {
		rt.taskTimeout = a.loginTimeoutDuration()
	}
	rt.id = id
	delete(s.runtimes, guestWechatID)
	// 临时 id 下的任务状态到此为止：它属于已经结束的那次扫码，不该跟着账号走。
	s.tasks.drop(guestWechatID)
	s.runtimes[id] = rt
}

// publicAccount 返回给前端的副本：会话凭证不出后端（与 List/Get 保持一致）。
func publicAccount(a *Account) *Account {
	c := *a
	c.TicketJWT = ""
	c.Identity = a.Identity.Clone()
	return &c
}

// Update 更新账号。password / casPassword 为空字符串时视为"不修改"，
// 这样前端可以留空密码字段。
// 更新成功后清空对应 runtime，下次 runtime 访问会用新配置重建。
func (s *AccountStore) Update(id string, patch *Account) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.accounts[id]
	if !ok {
		return nil, fmt.Errorf("账号不存在")
	}

	// 只更新允许修改的字段
	if patch.Name != "" {
		a.Name = patch.Name
	}
	if patch.BaseURL != "" {
		a.BaseURL = patch.BaseURL
	}
	if patch.Username != "" {
		a.Username = patch.Username
	}
	if patch.Password != "" {
		a.Password = patch.Password
	}
	if patch.CasPassword != "" {
		a.CasPassword = patch.CasPassword
	}
	if patch.UserAgent != "" {
		a.UserAgent = patch.UserAgent
	}
	if patch.Timeout != "" {
		a.Timeout = patch.Timeout
	}
	// 登录超时：空字符串表示前端未提交该字段（不修改）；
	// 非法值交给 normalize 回落到默认值
	if patch.LoginTimeout != "" {
		a.LoginTimeout = patch.LoginTimeout
	}
	if patch.Want != "" {
		a.Want = patch.Want
	}
	if patch.HedgingDelay != "" {
		a.HedgingDelay = patch.HedgingDelay
	}
	// Routes 为 nil 表示前端未提交该字段（不修改）；
	// 传空数组表示显式清空，回落到内置默认 route 列表。
	if patch.Routes != nil {
		a.Routes = patch.Routes
	}
	// 登录方式：新前端显式传 loginMode；旧前端只传 casLogin。
	// 旧前端不认识 wechat，不能让它默认的 casLogin=false 把扫码账号降级。
	switch {
	case patch.LoginMode != "" && validLoginMode(patch.LoginMode):
		a.LoginMode = patch.LoginMode
	case patch.LoginMode == "" && a.effectiveLoginMode() != LoginModeWeChat:
		a.CasLogin = patch.CasLogin
		a.LoginMode = "" // 交给 normalize 从 casLogin 重新推导
	}
	// 布尔值前端会显式传，所以直接覆盖
	a.Hedging = patch.Hedging
	// 自动重登开关用专用端点热更新（见 SetAutoRelogin），这里只在调用方
	// 显式提交该字段时跟随更新；nil 表示"本次 PUT 不涉及它"。
	if patch.AutoRelogin != nil {
		a.AutoRelogin = patch.AutoRelogin
	}
	// 自动选课邮件开关同样有专用端点（见 SetAutoNotify）；PUT 显式提交时跟随，
	// nil 表示本次不涉及它。
	if patch.AutoNotify != nil {
		a.AutoNotify = patch.AutoNotify
	}

	a.normalize()

	if err := s.saveLocked(); err != nil {
		return nil, err
	}

	// 关键：清空 runtime，让下次访问用新配置重建。
	// 如果正在被其他请求持有，会自然退化为下一次 runtime 访问时使用新配置。
	delete(s.runtimes, id)

	c := *a
	//c.Password = ""
	//c.CasPassword = ""
	//c.TicketJWT = ""
	return &c, nil
}

// SetWish 持久化愿望清单，并同步到已经建好的运行态。
//
// 这里不走 Update：愿望清单只影响自动选课，不需要重建 runtime —— 重建会丢掉
// 登录态与课程缓存，用户改一个课程名就被踢回登录页显然不合理。
func (s *AccountStore) SetWish(id string, w protocol.WishList) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.accounts[id]
	if !ok {
		return nil, fmt.Errorf("账号不存在")
	}
	a.applyWish(w)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	// 运行态已存在就即时生效；不存在时 buildRuntime 会从账号里读。
	if rt, ok := s.runtimes[id]; ok && rt.engine != nil {
		rt.engine.SetWish(a.wish())
	}

	c := *a
	c.TicketJWT = ""
	c.WantClasses = slices.Clone(a.WantClasses)
	c.WantTeachers = slices.Clone(a.WantTeachers)
	c.WantTypes = slices.Clone(a.WantTypes)
	return &c, nil
}

// SetAutoRelogin 更新账号的"会话失效时自动重登"开关。
//
// 和 SetWish 一样不走 Update：Update 会丢弃运行态，而切一个开关不应该
// 把用户踢回登录页。运行态已存在时直接热更新 engine 的策略。
func (s *AccountStore) SetAutoRelogin(id string, enabled bool) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.accounts[id]
	if !ok {
		return nil, fmt.Errorf("账号不存在")
	}
	a.AutoRelogin = &enabled
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	if rt, ok := s.runtimes[id]; ok && rt.engine != nil {
		rt.engine.SetAutoRelogin(enabled)
	}
	return publicAccount(a), nil
}

// SetAutoNotify 更新账号的"自动选课结束是否发结果邮件"开关。
//
// 和 SetAutoRelogin 一样不走 Update：Update 会丢弃运行态。通知在任务结束时
// 才读取账号，因此这里不需要通知 engine，落盘即可。
func (s *AccountStore) SetAutoNotify(id string, enabled bool) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.accounts[id]
	if !ok {
		return nil, fmt.Errorf("账号不存在")
	}
	a.AutoNotify = &enabled
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return publicAccount(a), nil
}

func (s *AccountStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; !ok {
		return fmt.Errorf("账号不存在")
	}
	delete(s.accounts, id)
	delete(s.runtimes, id)
	s.tasks.drop(id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return s.saveLocked()
}

func (s *AccountStore) update(id string, fn func(*Account)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return fmt.Errorf("账号不存在")
	}
	fn(a)
	return s.saveLocked()
}

// runtime 懒加载创建账号对应的运行态；配置变更后由 dropRuntime 使其失效。
func (s *AccountStore) runtime(id string) (*accountRuntime, error) {
	s.mu.RLock()
	rt, okRT := s.runtimes[id]
	a, okAcc := s.accounts[id]
	s.mu.RUnlock()
	if !okAcc {
		return nil, fmt.Errorf("账号不存在")
	}
	if okRT {
		return rt, nil
	}

	// 构建（不持锁）；会话产物随 *config.Session 交给 client，登录结束后由 doLogin 取快照落盘。
	// 配置不合法时直接返回错误，不把半成品放进 runtimes 缓存。
	newRT, err := buildRuntime(s, id, a)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.runtimes[id]; ok {
		return existing, nil
	}
	s.runtimes[id] = newRT
	return newRT, nil
}

// dropRuntime 丢弃账号的运行态缓存，使下次 runtime 访问按最新配置重建。
func (s *AccountStore) dropRuntime(id string) {
	s.mu.Lock()
	delete(s.runtimes, id)
	s.mu.Unlock()
}
