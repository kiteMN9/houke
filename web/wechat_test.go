package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"school_sdk/protocol/config"
)

// 扫码登录的顺序是反的：先有 ticket，才知道登的是谁，然后才需要一个账号。
// 第一次见到某个身份，就应该凭空造一个账号出来。
func TestUpsertWechatCreatesAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := NewAccountStore(path)
	require.NoError(t, err)

	id := config.Identity{Sub: "20230001", Name: "张三", Type: "学生", Org: "盐城工学院", OrgCode: "00300"}
	sess := config.Session{TicketJWT: "a.b.c", Identity: &id}

	acc, created, err := store.UpsertWechat(id, sess)
	require.NoError(t, err)
	require.True(t, created, "第一次扫码应新建账号")

	assert.Equal(t, "20230001", acc.Username)
	assert.Equal(t, "张三", acc.Name)
	assert.Equal(t, LoginModeWeChat, acc.LoginMode)
	assert.False(t, acc.CasLogin)
	assert.Empty(t, acc.TicketJWT, "对外返回的账号不该带 ticket")

	raw, ok := store.getRaw(acc.ID)
	require.True(t, ok)
	assert.Equal(t, "a.b.c", raw.TicketJWT)
	require.NotNil(t, raw.Identity)
	assert.Equal(t, "00300", raw.Identity.OrgCode)

	// 身份必须落盘：下次启动时才知道这个账号是谁，而不是只有一张会过期的票。
	reloaded, err := NewAccountStore(path)
	require.NoError(t, err)
	got, ok := reloaded.getRaw(acc.ID)
	require.True(t, ok)
	require.NotNil(t, got.Identity)
	assert.Equal(t, "张三", got.Identity.Name)
	assert.Equal(t, LoginModeWeChat, got.effectiveLoginMode())
}

// 同一个人第二次扫码不该变成第二个账号。
func TestUpsertWechatReusesBoundAccount(t *testing.T) {
	store := newTestStore(t)
	id := config.Identity{Sub: "20230001", Name: "张三", OrgCode: "00300"}
	first, created, err := store.UpsertWechat(id, config.Session{TicketJWT: "first"})
	require.NoError(t, err)
	require.True(t, created)

	again := id
	again.Name = "张三改名了"
	second, created, err := store.UpsertWechat(again, config.Session{TicketJWT: "second"})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, second.ID)
	assert.Len(t, store.List(), 1)

	raw, ok := store.getRaw(first.ID)
	require.True(t, ok)
	assert.Equal(t, "second", raw.TicketJWT, "同一账号应换成这次的票")
	assert.Equal(t, "张三改名了", raw.Identity.Name)
}

// 老版本把学号填在 Username 里、从没记过 identity：扫码时要认领它，而不是再建一个。
func TestUpsertWechatAdoptsLegacyAccount(t *testing.T) {
	store := newTestStore(t)
	legacy, err := store.Create(&Account{
		Name:      "老账号",
		Username:  "20230001",
		BaseURL:   config.DefaultBaseURL,
		LoginMode: LoginModeWeChat,
	})
	require.NoError(t, err)

	id := config.Identity{Sub: "20230001", Name: "张三", OrgCode: "00300"}
	acc, created, err := store.UpsertWechat(id, config.Session{TicketJWT: "t"})
	require.NoError(t, err)
	assert.False(t, created, "应复用已有账号")
	assert.Equal(t, legacy.ID, acc.ID)
	assert.Len(t, store.List(), 1)

	raw, ok := store.getRaw(acc.ID)
	require.True(t, ok)
	require.NotNil(t, raw.Identity)
	assert.Equal(t, "张三", raw.Identity.Name)
	assert.Equal(t, "老账号", raw.Name, "用户起的名字不能被 ticket 覆盖")
}

// 没有 sub 就绑不了账号：宁可失败，也不能绑到一个空身份上。
func TestUpsertWechatRejectsEmptyIdentity(t *testing.T) {
	store := newTestStore(t)

	_, _, err := store.UpsertWechat(config.Identity{}, config.Session{})
	require.Error(t, err)
	assert.Empty(t, store.List())
}

// 学号会撞号，所以"用户名相同"只有在同一所学校时才算同一个人。
// 另一所学校的账号绝不能被这次扫码改写：它的票、身份、地址都还是别人的。
func TestUpsertWechatDoesNotAdoptOtherSchoolAccount(t *testing.T) {
	store := newTestStore(t)
	other, err := store.Create(&Account{
		Name:      "另一个学校",
		Username:  "20230001",
		BaseURL:   "https://jwglxt.example.edu.cn/",
		LoginMode: LoginModePassword,
	})
	require.NoError(t, err)

	id := config.Identity{Sub: "20230001", Name: "张三", OrgCode: "00300"}
	acc, created, err := store.UpsertWechat(id, config.Session{TicketJWT: "t"})
	require.NoError(t, err)
	assert.True(t, created, "不同学校应新建账号")
	assert.NotEqual(t, other.ID, acc.ID)
	assert.Len(t, store.List(), 2)

	old, ok := store.getRaw(other.ID)
	require.True(t, ok)
	assert.Equal(t, LoginModePassword, old.effectiveLoginMode())
	assert.Nil(t, old.Identity)
	assert.Empty(t, old.TicketJWT)
}

// 同校同一个人已经有一个密码账号时，扫码应该并到它上面，而不是多出一个同名账号。
func TestUpsertWechatBindsSameSchoolAccount(t *testing.T) {
	store := newTestStore(t)
	same, err := store.Create(&Account{
		Name:      "我的账号",
		Username:  "20230001",
		BaseURL:   config.DefaultBaseURL,
		LoginMode: LoginModePassword,
	})
	require.NoError(t, err)

	id := config.Identity{Sub: "20230001", Name: "张三", OrgCode: "00300"}
	acc, created, err := store.UpsertWechat(id, config.Session{TicketJWT: "t"})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, same.ID, acc.ID)
	assert.Len(t, store.List(), 1)

	raw, ok := store.getRaw(acc.ID)
	require.True(t, ok)
	assert.Equal(t, LoginModeWeChat, raw.effectiveLoginMode())
	require.NotNil(t, raw.Identity)
	assert.Equal(t, "我的账号", raw.Name, "用户起的名字不能被覆盖")
}

// 扫码成功后运行态必须原地过继给账号：重建 runtime 会丢掉内存里的登录态，
// 而微信登录不允许静默重登，那样扫码就白扫了。
func TestAdoptRuntimeMovesGuestRuntimeToAccount(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{Name: "n", Username: "u", BaseURL: config.DefaultBaseURL})
	require.NoError(t, err)

	rt := mustBuildRuntime(t, store, acc)
	rt.id = guestWechatID
	store.mu.Lock()
	store.runtimes[guestWechatID] = rt
	store.mu.Unlock()

	store.AdoptRuntime(acc.ID, rt)

	got, err := store.runtime(acc.ID)
	require.NoError(t, err)
	assert.Same(t, rt, got, "过继的必须是同一个运行态")
	assert.Equal(t, acc.ID, rt.id)

	store.mu.Lock()
	_, still := store.runtimes[guestWechatID]
	store.mu.Unlock()
	assert.False(t, still, "临时 id 下不应再留着运行态")
}

// 没发起扫码时，这几个接口也要能应答：轮询在页面加载后就会开始。
func TestWechatEndpointsWithoutFlow(t *testing.T) {
	srv := mustNewServer(t, newTestStore(t))

	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wechat/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"active":false`)

	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/wechat/cancel", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"noop"`)

	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wechat/qr?rev=1", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// 身份随会话一起进出账号记录：登录写进去，重建 runtime 时读出来。
func TestAccountSessionCarriesIdentity(t *testing.T) {
	src := &Account{Username: "20230001", Identity: &config.Identity{Sub: "20230001", Name: "张三"}}

	dst := &Account{}
	dst.applySession(*src.session())
	require.NotNil(t, dst.Identity)
	assert.Equal(t, "张三", dst.Identity.Name)

	// 没有身份信息时不能把已有的身份抹掉（密码登录、CAS 登录都拿不到身份）。
	dst.applySession(config.Session{})
	require.NotNil(t, dst.Identity)
	assert.Equal(t, "20230001", dst.Identity.Sub)
}

// 微信账号不能后台自动重登：没有弹窗就没人扫码，任务只会挂在面板上显示
// "登录进行中"，直到超时——用户看到的就是"明明登录成功了却还在登录中"。
func TestStartBackgroundLoginSkipsWechatAccount(t *testing.T) {
	store := newTestStore(t)
	acc, err := store.Create(&Account{
		Name:      "张三",
		Username:  "20230001",
		BaseURL:   config.DefaultBaseURL,
		LoginMode: LoginModeWeChat,
	})
	require.NoError(t, err)

	srv := mustNewServer(t, store)
	rt, err := store.runtime(acc.ID)
	require.NoError(t, err)

	assert.False(t, srv.startBackgroundLogin(acc.ID, rt), "微信账号不该自动重登")
	assert.False(t, srv.tasks.running(acc.ID, taskKindLogin),
		"不该为微信账号挂一个永远等不到扫码的登录任务")
}

// 二维码弹窗不能再提供"复制链接"：复制到另一个标签页的扫码页会自己申请一个新
// uuid，服务端轮询的是自己那张码，扫了也永远等不到结果。
func TestQRDialogHasNoCopyLink(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("static", "index.html"))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "qr-copy-link")
}
