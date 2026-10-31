# WebUI 微信扫码登录

> 归档说明（2026-10-28）：TUI 已删除，文中「TUI `-wx`」「`WXQrcode.png` 落盘」「`main.go` 里的 TUI
> 宿主代码」等段落只作历史记录；现行宿主只有 `web`，二维码通过 `GET /login/qr` 以图片字节返回。
>
> 状态（2026-10-27）：
> - **P0 已完成**：账号登录方式收口为 `loginMode`（`password` / `cas` / `wechat`），
>   经 `engine.LoginKind` 传到 `protocol.NewAPIClient`。
> - **resp2 已修复**：`a26dff4` 把 `wxLogin.go` 第二次 302 的取值改回 `resp2`，
>   已在 `restyv3` 分支验证 TUI 扫码可走通。
> - **P1 已完成**：`WXLogin(ctx)` 已从 CLI 形态改成 Web 可观测、可取消、
>   无文件副作用的登录过程；二维码生成/下载失败与过期都在 3 次预算内重试，
>   测试覆盖解析器、轮询状态、取消与全链路换票。
> - **P2 已完成**：运行态二维码、`GET /login/qr` 图片端点、`/status` 与 SSE 元数据、
>   前端二维码弹窗（倒计时/过期刷新/取消）以及账号页的 wechat 选项均已接入；
>   实现落点见 §5，验收命令见 §7。
> - **无账号入口（2026-10-28）**：扫码登录不再挂在账号上——侧栏点一下就开始扫，
>   按 ticket 里的身份绑定或新建账号，见 §9。账号表单里的 wechat 选项已移除。

---

## 1. 目标与非目标

**目标**

1. WebUI 选中 `loginMode=wechat` 后，登录能生成二维码、在浏览器显示、等待微信扫码，
   扫码成功后复用现有 CAS2 → SSO → 教务系统的换票流程。
2. 登录过程可取消：`/login/cancel` 后 `WXLogin` 在 1 秒内退出，不再继续轮询。
3. 协议层不写文件、不读 stdin、不依赖 stdout；TUI 的二维码落盘由宿主负责。
4. 二维码过期（`wx_errcode=402`）自动刷新，用户拒绝（403）返回可解释的失败。

**非目标**

- 多校 profile：CAS 端点仍是 ycit 字面量，本次只让 WebUI 能用，不解决其他学校。
- 每次登录临时选择方式：登录方式仍是账号级配置。
- 静默重登：扫码必须由用户显式发起，业务请求里不弹二维码（见 §6.2）。

---

## 2. 现状

P0 已打通的链路：

```text
web.Account.LoginMode
  → web/runtime.go loginKindOf()
  → engine.New(..., LoginKind)
  → protocol.NewAPIClient(settings, sess, isCas2, wx, "")
  → cas2.NewCas(..., wx, ...)
```

P1 之前 `WXLogin` 的问题（`protocol/cas2/wxLogin.go`）：

| 问题 | 位置 | 影响 |
|---|---|---|
| 无 `ctx` | `WXLogin/wxUUID/GetScanResultCode/WxLoginFinal` | 取消要等整个轮询结束 |
| 写 `./qrcode.png`、`./WXQrcode.png` | `SaveImgStream` / `os.Remove` | 多账号并发互相覆盖；Web 无法拿到图片字节 |
| 打印到 stdout | `fmt.Println` | Web 服务日志被扫码流程污染 |
| 固定 15 次轮询 | `GetScanResultCode` | 约 17 秒就超时，人还没掏出手机 |
| 只认 `wx_code` | 同上 | 402/403/404 不区分，过期不刷新 |
| 失败无原因 | `WXLogin` 返回 bool | 任务只能显示"登录失败" |
| `NewCasWX` 死代码 | `wxLogin.go:20` | 与 `NewCas` 两套构造，容易改漏 |
| SSO 阶段可能递归登录 | `portal.go:145-150`、`:177-181` | 扫码中可能再弹一个二维码 |

---

## 3. 目标链路

```text
浏览器                           web                         engine              protocol/cas2
  │ POST /login                   │                            │                    │
  ├──────────────────────────────►│ doLogin                     │                    │
  │                              ├───────────────────────────►│ Login(ctx)         │
  │                              │                            ├───────────────────►│ WXLogin(ctx)
  │                              │                            │                    ├─ wxUUID(ctx)
  │                              │                            │                    ├─ 下载二维码
  │                              │◄── OnQRCode(image) ────────┤◄───────────────────┤
  │◄── SSE /status(qr) ──────────┤  rt.qr                      │                    │
  │ GET /login/qr?rev=N ────────►│  image/png                  │                    │
  │                              │                            │                    ├─ waitScan(ctx)
  │◄── SSE stage(已扫码/过期) ───┤◄── OnStage(login.qr-*) ────┤◄───────────────────┤
  │                              │                            │                    ├─ finishWXLogin(ctx)
  │                              │◄───────────────────────────┤◄───────────────────┤ sess.TicketJWT
  │◄── SSE done ─────────────────┤ applySession + 落盘         │                    │
```

二维码是"登录产物"，不是"用户输入"：因此走独立的 `OnQRCode` 回调，不复用
`InputHandler` / `pendingInput`。后者是"等一个字符串"的请求-响应通道，
二维码是"服务器产出图片 + 等待手机端外部事件"，语义不同。

---

## 4. P1：协议层改造

### 4.1 类型与回调

```go
// protocol/cas2
type QRCode struct {
    Image     []byte    // PNG 字节
    ExpiresAt time.Time // UI 倒计时用；402 才是权威过期信号
    Attempt   int       // 第几次生成（从 1 开始，刷新时递增）
    PageURL   string    // 微信官方扫码页地址，仅用于排查日志
}

type QRHandler func(qr *QRCode) // qr == nil 表示二维码已失效，宿主应清除显示

type QRStatus string

const (
    QRWaiting QRStatus = "waiting" // 未扫描
    QRScanned QRStatus = "scanned" // 已扫描，等待手机确认
    QRExpired QRStatus = "expired" // 二维码过期
    QRRefused QRStatus = "refused" // 用户在手机上拒绝 / 取消
)

type QRStatusHandler func(status QRStatus, attempt int)

func (c *Client) SetQRHandler(h QRHandler)
func (c *Client) SetQRStatusHandler(h QRStatusHandler)
```

`protocol` 侧只暴露 `QRCode` 别名和图片回调，不让 `engine`/`web` import `cas2`：

```go
// protocol/client.go
type QRCode = cas2.QRCode

func (a *APIClient) SetQRHandler(h func(*QRCode))
```

状态回调由 `protocol.NewAPIClient` 内部接到 `LoginObserver`：

| cas2 状态 | protocol 阶段 | 文案（web） |
|---|---|---|
| `QRWaiting` | `login.qr` | 请使用微信扫码登录 |
| `QRScanned` | `login.qr-scanned` | 已扫码，请在手机上确认 |
| `QRExpired` | `login.qr-expired` | 二维码已过期，正在刷新 |
| `QRRefused` | `login.qr-refused` | 已取消微信登录 |

### 4.2 `WXLogin(ctx)` 状态机

下面的代码块只画主流程，省略了二维码生成/下载失败时的重试分支（见其后说明）。

```go
func (c *Client) WXLogin(ctx context.Context) bool {
    defer c.emitQR(nil) // 无论成功/失败/取消都清空
    for attempt := 1; attempt <= wxQRMaxRefresh; attempt++ {
        uuid, imgURL, pageURL, state, err := c.wxUUID(ctx)
        if err != nil { return false }
        img, err := c.getWXQrCode(ctx, imgURL)
        if err != nil || len(img) == 0 { return false }

        expiresAt := time.Now().Add(wxQRTTL)
        c.emitQR(&QRCode{Image: img, ExpiresAt: expiresAt, Attempt: attempt, PageURL: pageURL})
        c.emitQRStatus(QRWaiting, attempt)

        code, status, err := c.waitScan(ctx, uuid, attempt, expiresAt)
        if err != nil { return false }
        switch status {
        case QRExpired:
            c.emitQR(nil)
            continue
        case QRRefused:
            return false
        }
        if code == "" { return false }
        return c.finishWXLogin(ctx, code, state)
    }
    return false
}
```

- `wxQRMaxRefresh = 3`，配合登录超时（默认 10 分钟）足够覆盖 2~3 次二维码过期。
- 每次刷新都递增 `Attempt`，前端据此更换图片 URL 并显示"第 N 次"。
- `defer emitQR(nil)` 保证取消/失败后宿主不会残留旧二维码。
- `wxUUID` / `getWXQrCode` 失败不立即放弃：`attempt < wxQRMaxRefresh` 时
  `sleepCtx(ctx, 1s)` 后进入下一次；第三次仍失败才结束，日志为
  "二维码多次刷新仍失败"。`ctx` 已取消时直接返回，不再 sleep。

### 4.3 扫码轮询状态

`GET https://lp.open.weixin.qq.com/connect/l/qrconnect?uuid=...` 返回一段 JS：

| `wx_errcode` | 含义 | 处理 |
|---:|---|---|
| 408 | 未扫描 | 继续每秒轮询，保持 `QRWaiting` |
| 404 | 已扫描，等待确认 | 上报一次 `QRScanned`，继续轮询 |
| 405 | 已确认，带 `wx_code` | 上报 `QRScanned`，返回 code |
| 402 | 二维码过期 | 上报 `QRExpired`，外层重新生成 |
| 403 | 用户拒绝 | 上报 `QRRefused`，登录失败 |

解析器拆成纯函数，便于用固定样本测试：

```go
func parseQRPage(html string) (uuid, imgURL string, ok bool)
func parseScanResult(body string) (code string, status QRStatus)
```

- `parseScanResult` 用正则提取 `wx_errcode` 与 `wx_code`，不要只匹配 `wx_code=''`。
- `parseQRPage` 同时支持相对/绝对图片地址；相对地址用 `https://open.weixin.qq.com` 解析。
- 以实测抓包为准：若学校 CAS 的微信页 errcode 语义不同，只改 `parseScanResult` 的表。

### 4.4 取消、超时与 TTL

- 涉及网络的函数全部带 `ctx`：`wxUUID`、`getWXQrCode`、`waitScan`、`finishWXLogin`。
- 每个 resty 请求 `SetContext(ctx)`；所有 `time.Sleep` 换成 `sleepCtx(ctx, d)`：
  ```go
  select {
  case <-ctx.Done(): return ctx.Err()
  case <-time.After(d):
  }
  ```
- 除 `ctx` 外，`waitScan` 还带 `expiresAt`：即使服务端一直返回 408、迟迟不给 402，
  本地 4 分钟后也会按过期处理并刷新。
- `/login/cancel` 取消 `ctx` 后，`getWXQrCode` 的等待和 `waitScan` 的 1 秒 sleep
  都会立刻返回；登录任务在 1 秒内收尾。

### 4.5 去掉文件与 stdout

`WXLogin` 不再：

- 调 `check_code.SaveImgStream` 写 `./qrcode.png` / `./WXQrcode.png`；
- 调 `os.Remove` 删文件；
- 用 `fmt.Println` 打印"扫描二维码 / 已完成登录"。

TUI 的宿主行为放到 `main.go`：

```go
api.SetQRHandler(func(qr *protocol.QRCode) {
    if qr == nil {
        _ = os.Remove("WXQrcode.png")
        return
    }
    _ = os.WriteFile("WXQrcode.png", qr.Image, 0o644)
    fmt.Println("请用微信扫描 WXQrcode.png 完成登录")
})
```

`NewCasWX`（死代码）与只被注释引用的 `getQrCode` / `qrCode` 常量一并删除。

### 4.6 阶段上报

`LoginPhase` 新增四个值（`protocol/login_stage.go`）：

```go
LoginPhaseQRCode    LoginPhase = "login.qr"
LoginPhaseQRScanned LoginPhase = "login.qr-scanned"
LoginPhaseQRExpired LoginPhase = "login.qr-expired"
LoginPhaseQRRefused LoginPhase = "login.qr-refused"
```

`cas2` 不直接认识 `protocol.LoginPhase`（避免循环依赖）：它只上报
`QRStatus`，由 `protocol.NewAPIClient` 的 `qrStatusPhase` 映射成阶段。
`web/login_stage.go` 增加对应中文文案，任务时间线即可看到
"请使用微信扫码登录 → 已扫码，请在手机上确认 → 换取门户票据"。

### 4.7 SSO 阶段的递归登录

`cas2/portal.go` 的 `GetJwCookie2` 在 token 过期或 401 时会再调 `c.Login(ctx)`。
扫码模式下这会在 SSO 阶段**再弹一个二维码**。P1 在 wx 模式下直接返回失败，
让外层登录任务结束，而不是嵌套扫码：

```go
if c.needLogin() {
    if c.enableWxLogin {
        log.Println("微信扫码登录：门户 token 已过期，跳过递归登录")
        return ""
    }
    c.Login(ctx)
}
```

401 分支同理。

### 4.8 端点注入与测试

把微信/CAS 端点收进 `wxEndpoints`，默认值仍是 ycit；测试用 `httptest`
覆盖整套链路，不需要真实网络：

```go
type wxEndpoints struct {
    federatedRedirect string // https://cas2.ycit.edu.cn/cas/federatedRedirect
    service           string // 门户 service
    qrImageBase       string // https://open.weixin.qq.com
    qrPoll            string // https://lp.open.weixin.qq.com/connect/l/qrconnect
    callback          string // https://cas2.ycit.edu.cn/cas/federation/federatedCallback/openweixin
    casLogin          string // https://cas2.ycit.edu.cn/cas/login
}
```

测试覆盖：

1. `parseQRPage` / `parseScanResult` 的固定样本；
2. `waitScan` 的 408→405、402、403、ctx 取消；
3. `WXLogin` 全链路：假 CAS 302 → 微信 HTML → 二维码 PNG → 405 code →
   callback 302 → `/cas/login` 302（带 ticket JWT）→ `sess.TicketJWT` 写入；
4. `login.qr` 阶段通过 `LoginObserver` 可见；
5. TUI `-wx` 仍能落盘 `WXQrcode.png`（手工）。

---

## 5. P2：Web 呈现（已实现）

Web 侧只消费两个东西：`OnQRCode` 和已有的 `OnStage`。

| 项 | 实现 |
|---|---|
| 运行态 | `web/runtime.go` 的 `accountRuntime.qr`：`image []byte` / `pageURL` / `rev` / `createdAt` / `expiresAt`，由 `qrMu` 保护；每次新码 `rev++` |
| 图片 | `GET /api/accounts/:id/login/qr?rev=N` → `image/png`，`Cache-Control: no-store`、`Pragma: no-cache`；无二维码或 rev 过期 404，rev 非正整数 400；不传 rev 取当前图 |
| 状态 | `/status` 与 SSE snapshot 增加 `"qr": {"rev","createdAt","expiresAt","pageUrl"}`（Unix 毫秒，不含图片字节）；无二维码时为 `null` |
| 清理 | `doLogin` 开始/结束、`cancelLogin` 各分支、`logout` 都清空；runtime 重建自然为空 |
| 前端 | 有 `qr` 就 `showModal()` 二维码 dialog；`id/rev` 变化才换 `src`；每秒刷新阶段文案和 `Ns 后过期`；过期由后端自动刷新（rev 变化）；取消按钮和 Esc 都调用 `/login/cancel`，不是只关弹窗；**不提供“复制链接”**（原因见 9.7） |
| 账号页 | `loginMode` 下拉静态包含 `wechat`；选中后隐藏教务密码输入 |

不要把二维码 base64 塞进 `task.events`：事件流每帧全量重发，二维码会被重复
传输上百次。图片走独立 GET，SSE 只带元数据。SSE 的 `progress` 帧不带有效
`qr`，前端只在 `snapshot` 帧更新它。

实现落点：`web/runtime.go`（状态与 `setQRCode/qrView/qrImage`）、
`web/handlers_login.go`（清理与 `loginQR`）、`web/server.go`（路由）、
`web/stream.go`（snapshot 元数据）、`web/login_qr_test.go`、前端
`web/static/index.html`、`app.js`、`style.css`。

---

## 6. 兼容与安全

### 6.1 账号字段

- `loginMode` 是主字段；`casLogin` 只是兼容镜像（`loginMode=cas` 时为 true）。
- 历史账号没有 `loginMode`：按 `casLogin` 推导；`wechat` 不会写成 `casLogin=true`。
- 旧前端只传 `casLogin` 时仍可用；但不能用默认的 `casLogin=false` 把 wechat 降级。

### 6.2 静默重登

`engine.authorize` 目前会在业务请求里直接 `LoginWithContext`。扫码必须由用户
显式发起，因此 wx 模式下 `authorize` 直接返回 false，把 401 交给前端；由用户点
"登录"（或 ping 的可见重登任务）再扫码。

### 6.3 安全

- `/login/qr` 与现有 `/api` 一样无鉴权；只建议 loopback 使用。
- 登录结束立即清空二维码；`Cache-Control: no-store`。
- `accounts.json` 仍是明文凭据；本次不改变该现状。

---

## 7. 验收

**单元/集成**

```powershell
gofmt -l protocol engine web
go vet ./...
go test ./... -count=1
go test -race ./protocol/cas2 ./protocol ./web
```

**手工**

| 场景 | 期望 |
|---|---|
| WebUI 选 wechat → 登录 | 浏览器出现二维码；任务时间线显示"请使用微信扫码登录" |
| 扫码并确认 | 显示"已扫码，请在手机上确认" → 换票 → 登录成功 |
| 手机拒绝 | 任务失败，时间线最后一条是"已取消微信登录" |
| 二维码过期 | 自动刷新，图片 rev 变化，对话框提示"正在刷新" |
| 扫码中取消 | `/login/cancel` 后 1 秒内任务变 cancelled，二维码清空 |
| TUI `-wx` | 生成 `WXQrcode.png`，扫码后登录成功 |
| 两个账号同时扫码 | 图片/状态互不串 |

---

## 8. 文件清单

| 文件 | P1 改动 |
|---|---|
| `protocol/cas2/wxLogin.go` | 重写：ctx、QR 回调、状态机、解析器、端点注入；删文件 IO/stdout/死代码 |
| `protocol/cas2/login.go` | `WXLogin(ctx)`；`Client` 增加 qr 字段；`newClient` 初始化 `wxEndpoints` |
| `protocol/cas2/portal.go` | wx 模式下禁止 SSO 阶段递归 `Login` |
| `protocol/client.go` | `QRCode` 别名、`SetQRHandler`、`qrStatusPhase` 映射 |
| `protocol/login_stage.go` | 四个 QR 阶段 |
| `engine/engine.go` | `Options.OnQRCode`；wx 模式 `authorize` 不静默登录 |
| `web/login_stage.go` | QR 阶段中文文案 |
| `main.go` | TUI QR handler：落盘 `WXQrcode.png` |
| `protocol/cas2/wxLogin_test.go` | 解析器、waitScan、取消、全链路 |
| `protocol/login_stage_test.go`、`web/login_stage_test.go` | QR 阶段映射/文案 |

P2 改动：

| 文件 | 改动 |
|---|---|
| `web/runtime.go` | `loginQR` 运行态、`setQRCode/clearQRCode/qrView/qrImage`、`OnQRCode` 接线 |
| `web/handlers_login.go` | 登录/取消/登出清空二维码；`loginQR` 图片 handler；`/status` 返回 `qr` |
| `web/server.go` | `GET /accounts/:id/login/qr` 路由 |
| `web/stream.go` | snapshot 帧携带 `qr` 元数据（`progress` 帧不带） |
| `web/login_qr_test.go` | 运行态、HTTP、状态、清理、SSE 五组测试 |
| `web/static/index.html` | wechat 选项、二维码 `<dialog>` |
| `web/static/app.js` | `maybeShowQR/syncQRStatus/dismissQRDialog/cancelQRLogin`、SSE 接线、账号切换清理 |
| `web/static/style.css` | 二维码容器/图片/状态文案样式 |

---

## 9. 不挂账号的扫码入口（2026-10-28）

### 9.1 为什么要改

上面几节把 `wechat` 当成账号的一种"登录方式"：先建账号，填名称、地址、学号，
再在登录时弹二维码。可是微信登录的信息流是反的——**扫码之前什么都不知道**：

* 二维码是门户下发的，与"哪个账号"无关；
* 唯一能说明"登的是谁"的东西是 ticket 里的 JWT，它要在扫码成功之后才出现。

在账号的登录方式里放 wechat，等于要求用户先凭空填一个学号，而这个学号正是扫码
要告诉我们的答案。所以入口改成侧栏的「微信扫码登录」：点一下 → 出二维码 →
扫码 → 按 ticket 绑到账号（没有就新建），全程没有一个输入框。

### 9.2 身份是登录的产物

`config.Identity{Sub, Name, Type, Org, OrgCode}`（`protocol/config/identity.go`）由
`utils.ParseTicket` 从 ticket 解析：外层 JWT 给 `sub` 与 `organizationCode`，内层
idToken 给 `ATTR_name` / `ATTR_identityTypeName` / `ATTR_organizationName`。
内层解不开不影响登录——学号在外层已经有了，姓名只是展示（回落显示学号）。

身份先随 `config.Session.Identity` 从 cas2 流回 web，登录结束后落进
`Account.Identity`：`ticketJWT` 会过期，身份不会，两者要分开存。
绑定键是 **`(orgCode, sub)`** 而不是裸 `sub`：`accounts.json` 里可以同时有多所学校，
学号会撞号。

### 9.3 绑定规则（`AccountStore.UpsertWechat`）

| 顺序 | 匹配 | 结果 |
|---|---|---|
| 1 | `identity.sub` + 学校编码相同 | 绑到那个账号，更新 `ticketJWT` / 身份 / `lastLogin` |
| 2 | 没有身份、同校、`username == sub` | 认领它（扫码上线前建的账号没有身份，用户名是唯一线索） |
| 3 | 都没有 | 新建：`username` 取学号、`name` 取 ticket 里的姓名、`loginMode=wechat` |

第 2 条额外要求学校一致（`sameSchoolAsScan`：拿 `baseUrl` 的主机名和默认地址比）：
学号本身不唯一，宁可多出一个账号，也不能把另一所学校的账号改写成这所学校。

### 9.4 扫码成功后不能重建运行态

登录态是 `APIClient.isLoggedIn` 这个内存标记，只有 `Login` / `Ping` / `Logout` 会改它。
拿刚落盘的 JWT 重新构造 runtime，会得到一个"有票但没登录"的引擎；而微信模式
`authorize` 直接返回失败（不静默重登），结果就是扫码明明成功了却什么都用不了。
所以 `AccountStore.AdoptRuntime` 把匿名运行态**原地**挂到账号 id 下，同时清掉
`@wechat` 这个临时 id。

### 9.5 端点与前端

* `POST /wechat/login`、`GET /wechat/status`、`GET /wechat/qr`、`POST /wechat/cancel`
  （不挂在 `/accounts/:id` 下，因为此刻还没有账号；语义见 [http-api.md](http-api.md#31-无账号扫码登录wechat)）。
* 前端不用 SSE：没有账号可订阅，轮询 `/wechat/status` 一秒一次，成功后拿 `accountId`
  切到那个账号。二维码弹窗与账号登录共用，靠 `wechatFlow.active` 决定取消走哪条路。
* 账号表单的「登录方式」只剩 `password` / `cas`；已有 wechat 账号仍会显示出
  「微信扫码登录」（`setLoginMode` 会补一个动态选项），也能照旧自己弹二维码登录。

### 9.6 文件清单

| 文件 | 改动 |
|---|---|
| `protocol/config/identity.go` | 新增 `Identity`（`Empty/Clone/Display/SamePerson`） |
| `protocol/config/session.go` | `Session.Identity`，`Snapshot` 深拷贝；`Clear` 不清身份 |
| `protocol/cas2/utils/ticket2idToken.go` | `ParseTicket` 一次解出 `IDToken` / `Exp` / `Identity`；`ExtractIDToken` 保留为包装 |
| `protocol/cas2/wxLogin.go`、`login.go` | 扫码成功与缓存 JWT 两条路径都把身份写进 `sess.Identity` |
| `protocol/client.go`、`engine/engine.go` | `Identity()` 读当前登录证明的身份 |
| `web/account.go` | `Account.Identity` 落盘；`session()/applySession` 带上身份 |
| `web/store.go` | `UpsertWechat`、`findBoundLocked`、`sameSchoolAsScan`、`AdoptRuntime` |
| `web/handlers_login.go` | 抽出 `runLogin`（机制 + `persist` 回调），`doLogin` 变成薄包装 |
| `web/wechat.go` | `wechatFlow`、四个 handler、匿名 runtime、绑定与过继 |
| `web/server.go` | `/api/wechat/*` 四条约路由 + `wechat` 字段 |
| `web/static/*` | 侧栏按钮、扫码弹窗复用、轮询与收尾、去掉账号表单里的 wechat 选项 |
| `main.go` | TUI `-wx` 不再要求学号/密码（登录成功才知道是谁） |
| `web/wechat_test.go`、`protocol/cas2/utils/identity_test.go` | 绑定规则、过继、端点、票据解析 |

### 9.7 两个"扫了却没登录"的坑（2026-10-28）

上线当天用户报了两个现象：**扫码能成功，但复制链接去扫就一直等二维码**；以及
**登录成功后任务面板还停在"登录进行中"**。原因是两件独立的事。

**（1）复制链接的二维码是另一张码。** `pageUrl` 是
`https://open.weixin.qq.com/connect/qrconnect?appid=…&state=…#wechat_redirect`，
里面**没有 uuid**。uuid 是打开扫码页时由页面自己向微信申请的，服务端记下的只是
"它那次页面加载"拿到的那个。把链接粘到另一个标签页，那个页面会申请一个新 uuid、
显示一张新码；手机上确认后 `wx_code` 只会回到那个标签页的 JS，服务端这边的
`lp.open.weixin.qq.com/connect/l/qrconnect?uuid=<旧 uuid>` 永远等不到 → 界面一直
"正在获取二维码…"。所以这个入口从根上就是错的，直接去掉；弹窗提示改成"扫本窗口
里的这张图"。

**（2）微信账号不能后台自动重登。** `ping` 发现会话失效会
`startBackgroundLogin` 起一次完整登录任务（任务面板可见、可取消）。密码/CAS 能
静默登，但微信必须有人扫二维码——没有弹窗就没有二维码，任务只会挂着显示
"登录进行中"，直到 `loginTimeout`（默认 10 分钟）超时。这正好解释了"登录成功后
仍显示登录进行中"。现在 `startBackgroundLogin` 对 `loginMode=wechat` 的账号直接
返回 false，`/ping` 的响应多一个 `hint` 字段让界面提示"需要手动点微信扫码登录"。

