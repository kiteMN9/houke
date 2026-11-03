# Web API

> 前缀 `/api`，JSON in / JSON out；长任务用 `POST` 触发 + `GET /events`（SSE）观察。
> 路由定义见 `web/server.go`，处理逻辑在 `web/handlers_*.go`。

---

## 1. 通用约定

| 项 | 约定 |
|---|---|
| 认证 | **无**。没有 token、没有 session cookie、没有 CSRF 校验。默认只监听 `127.0.0.1`（`main.go:21`） |
| 错误形状 | `{"error":"中文原因","code":"unauthorized"}`；HTTP 状态码给粗分类，`code` 给稳定语义（见 §1.1）。参数校验类错误可以没有 `code` |
| 账号 ID | 16 位十六进制，新建时由 `newID()` 生成（`web/store.go:83`） |
| `runtime` 错误 | 所有 handler 第一步 `s.store.runtime(id)`，**任何**失败都回 404 —— 包括"账号存在但配置非法" |
| 一次性查询超时 | 30s（课程/成绩/选课/退课），`ping` 20s，`tabs/:index` 30s |
| 长任务超时 | 登录 / 初始化 = 账号的 `loginTimeout`（默认 10m）；自动选课 = 请求预算（默认 24h） |
| 断连中断 | 一次性查询绑 `c.Request.Context()`，浏览器断开即取消；长任务不绑（见 §4） |

### 1.1 错误码与会话失效

`code` 是前端分支的依据，不要求前端匹配中文文案。目前会出现的值：

| code | 典型状态码 | 含义 | 前端动作 |
|---|---|---|---|
| `unauthorized` | 401 | 未登录，或请求过程中发现会话已失效 | 徽标切"未登录"、作废旧状态请求、提示一次、重拉 `/status` |
| `login_failed` | 401 | 登录动作失败（密码错、流程失败） | 只展示"登录失败"，**不**当成会话失效 |
| `no_permission` | 403 | 学校返回"无功能权限"页 | 在结果区说明原因，不弹红色错误 |
| `busy` | 409 | 同账号已有登录 / 初始化 / 自动选课在跑 | 提示等待或取消 |
| `session_reset` | 409 | 初始化期间登出 / 重登，本次结果作废 | 提示重新初始化 |
| `cancelled` | 408 | 一次性请求被取消 | 按取消处理，不提示失败 |
| `timeout` | 504 | 请求或长任务超时 | 展示超时文案 |
| `internal` | 500 | 其他服务器错误 | 展示 `error` |

**会话失效只有一个出口。** protocol 在**任何**响应里发现"服务器已经不认这个会话"
（302 到登录页、页面里没有当前账号、登录页 `#tips`）时，会立刻把 client 的登录态
记成 `false`（`protocol/loginState.go:markSessionExpired`）。因此 `/status` 和 SSE
帧不会在错误发生之后继续报 `loggedIn:true`；Web 层统一回 `401 + code=unauthorized`，
前端 `API.req` 只在这一个地方处理（`web/static/app.js:notifyUnauthorized`）。

登录失败同样用 401，但 code 是 `login_failed`——两种语义必须分开，否则输错密码
会被界面显示成"登录已失效"。

**自动重登是账号级开关**（`autoRelogin`，默认开）。开启时，业务请求 / 初始化发现
会话失效会重登一次并重试一次；关闭时只返回 `401 + unauthorized`，不偷偷跑登录，
由界面提示手动登录。开关通过 `PUT /accounts/:id/auto-relogin` 热切换。

---

## 2. 账号

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| GET | `/healthz` | — | `{"ok":true}` | 无鉴权探活 |
| GET | `/accounts` | — | `[]Account` | **含明文密码**（脱敏被注释，`store.go:100-101`）；`ticketJWT` 被清空 |
| POST | `/accounts` | `Account` | 新建的 `Account`（密码已清） | `name` / `username` / `baseUrl` 必填；缺省字段走 `normalize()` |
| GET | `/accounts/:id` | — | `Account` | **含明文密码** |
| PUT | `/accounts/:id` | `Account` 部分字段 | 更新后的 `Account` | 空字符串 = 不修改；`loginMode` 显式传则覆盖；`casLogin` 仅旧前端兼容；`hedging` **无条件覆盖**；`routes` 传 `null` 会保留 |
| DELETE | `/accounts/:id` | — | `{"status":"ok"}` | 先取消正在跑的登录/初始化/自动选课，再删运行态与任务状态 |
| PUT | `/accounts/:id/auto-relogin` | `{"enabled":bool}` | `{"status":"ok","autoRelogin":bool}` | 热切换"会话失效时是否自动重登一次"；**不重建运行态**，不丢登录态。与 `PUT /accounts` 分开，后者会重建运行态 |
| PUT | `/accounts/:id/auto-notify` | `{"enabled":bool}` | `{"status":"ok","autoNotify":bool}` | 热切换该账号"自动选课结束时是否发结果邮件"；**不重建运行态**。服务端 `smtp.notifyAutoChoose` 仍是总开关 |

`Account` 字段见 [configuration.md](configuration.md#3-accountsjson)。

### 2.1 愿望清单（自动选课的持久化配置）

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| GET | `/accounts/:id/wish` | — | `{"wantClasses":[],"wantTeachers":[],"wantTypes":[]}` | 未配置时三个字段为 `null` |
| PUT | `/accounts/:id/wish` | 同结构，**全量替换** | 替换后的 `wishView` | 传 `[]` 就是清空；入库前 trim + 去重（`web/account.go:93-108`） |

单独一个端点、不并进 `PUT /accounts`：后者用"空字符串 = 不修改"表达部分更新，而数组没有等价的
哨兵值，混在一起会让"清空"和"没提交"分不清（`handlers_wish.go:37-40`）。

写入只影响自动选课，**不重建运行态**——改一个课程名不该把用户踢回登录页（`store.go:224-227`）。

---

## 3. 会话与登录

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| POST | `/accounts/:id/login` | — | 200 `{"status":"ok"}` / 200 `{"status":"cancelled"}` / 401 `code=login_failed` / 409 `code=busy` / 504 `code=timeout` | **长任务**，最长 `loginTimeout`；`loginMode=wechat` 时生成二维码 |
| POST | `/accounts/:id/login/cancel` | — | `{"status":"ok"\|"noop"}` | 取消正在跑的登录 |
| GET | `/accounts/:id/login/pending` | — | `{"pending":false}` 或 `{"pending":true,"kind":"captcha"\|"password","prompt":"…","ageMs":123,"imageBase64":"…"}` | 前端轮询兜底；正常走 SSE |
| GET | `/accounts/:id/login/qr` | `?rev=N`（可选） | 200 `image/png` / 400 rev 非法 / 404 无二维码或 rev 已过期 | `Cache-Control: no-store`；不传 rev 取当前图。二维码只存内存，登录结束即清空 |
| POST | `/accounts/:id/login/input` | `{"value":"..."}` | `{"status":"ok"}` | 无待输入 → 409；`kind=="password"` 时**顺手把新密码写进 accounts.json**（`handlers_login.go:315`） |
| POST | `/accounts/:id/cas/jwt/clear` | — | `{"status":"ok"}` | 清 `ticketJWT` 并丢弃运行态（下次登录会重新走门户） |
| POST | `/accounts/:id/logout` | — | `{"status":"ok"}` | `Logout()` + `Reset()`（清登录态与初始化参数） |
| GET | `/accounts/:id/status` | — | `{"loggedIn":bool,"initialized":bool,"modeName":"…","window":{"xkkssj":"…","xkjssj":"…"},"task":{…},"pending":{…},"qr":{…}}` | 首屏与兜底轮询；不被初始化阻塞；`qr` 无二维码时为 `null`；`qr` 含 `rev`/`createdAt`/`expiresAt`/`pageUrl`，`pageUrl` 仅用于排查（扫码必须在同一窗口，见 wechat-login-webui.md §9.7） |
| GET | `/accounts/:id/events` | — | `text/event-stream`，见 §6 | SSE |
| GET | `/accounts/:id/ping` | — | `{"ok":bool,"loggedIn":bool,"status":200,"latencyMs":42,"message":"…","relogin":bool,"hint":"…"}` | ≤20s；判定掉线、无任务在跑且 `autoRelogin=true` 时，**后台起一次可见重登**并立即返回 `relogin:true`；开关关闭或 `loginMode=wechat` 时返回 `relogin:false`，在 `hint` 里说明原因 |

`window` 来自选课列表前置页的 hidden input（`xkkssj` 选课开始、`xkjssj` 选课结束）；
未初始化时两项均为空串，初始化完成、切换页签或重新初始化后随 `/status` 刷新。

### 3.1 无账号扫码登录（`/wechat`）

微信扫码的入口不需要账号：点一下就开始扫码，拿到 ticket 才知道是谁。所以这一组
端点不挂在 `/accounts/:id` 下，实现见 `web/wechat.go`。

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| POST | `/wechat/login` | — | 200 `{"status":"ok"}` / 409 已有扫码在进行中 | 立即返回，登录在后台跑；超时用 `loginTimeout` 兜底 |
| GET | `/wechat/status` | — | `{"active":bool,"qr":{…}\|null,"task":{…}\|null,"pending":{…}\|null}`；完成后追加 `accountId` / `created` / `account`，失败追加 `error` | 前端每秒轮询（没有账号可订阅 SSE）；`task` 是 `@wechat` 那个临时任务的快照 |
| GET | `/wechat/qr` | `?rev=N`（可选） | 200 `image/png` / 400 rev 非法 / 404 无二维码 | 与账号登录同一套 rev 机制 |
| POST | `/wechat/cancel` | — | `{"status":"ok"\|"noop"}` | 取消后 `status` 里出现 `"error":"已取消"` |

绑定规则（`AccountStore.UpsertWechat`）：

1. 同一身份（`identity.sub` + 学校编码）→ 绑到那个账号，更新会话与 `lastLogin`；
2. 没有身份、同校、`username == sub` 的旧账号 → 认领它（扫码上线前的账号没有 identity，
   用户名是唯一线索；学校用 `baseUrl` 主机名判断，因为学号会撞号）；
3. 都没有 → 新建：`username` 取学号、`name` 取 ticket 里的姓名、`loginMode=wechat`。

扫码成功后**不重建运行态**：登录态在 client 内存里，重建会得到"有票但没登录"的引擎，
而微信模式不允许静默重登；`AdoptRuntime` 把匿名运行态直接挂到账号 id 下。

`POST /accounts/:id/login` 的老路径没有变：`loginMode=wechat` 的账号仍能自己弹二维码。

---

## 4. 初始化、课程、选课

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| POST | `/accounts/:id/init` | `?force=1` 强制重抓 | `{"status":"ok","modeName":"…"}` / `{"status":"cancelled"}` / 401 `code=unauthorized`（初始化时发现会话失效且未开启自动重登）/ 409 `code=busy` 或 `session_reset` / 504 `code=timeout` / 500 | **长任务**；抓 `GetIndexParams` + `GetCourseListPre`。未登录时是否先自动登录、初始化中失效是否重登一次，都看账号的 `autoRelogin` |
| POST | `/accounts/:id/init/cancel` | — | `{"status":"ok"\|"noop"}` | |
| GET | `/accounts/:id/tabs` | — | `[]protocol.TabInfo` | 未初始化时为空 |
| POST | `/accounts/:id/tabs/:index` | — | `{"status":"ok","modeName":"…"}` | 切换页签 → 重抓该页签参数 + 清课程缓存 |
| GET | `/accounts/:id/courses` | `?force=1` 先清缓存 | `[]protocol.CustomCourseDic` | 需已初始化；**会访问学校接口** |
| GET | `/accounts/:id/courses/cache` | — | 同上（可能为 `[]`） | 只读内存，不访问网络 |
| GET | `/accounts/:id/courses/:kchId` | — | 完整课程列表（该课程号已补详情） | 补详情是串行请求，慢 |
| DELETE | `/accounts/:id/courses` | — | `{"status":"ok"}` | 清空内存缓存 |
| POST | `/accounts/:id/choose` | `{"kchId":"…","jxbId":"…","xxkbj":"…","cxbj":"…"}` | `protocol.ChooseCourseResult` | `kchId` / `jxbId` 必填；参数过期时 engine 会自动重抓一次再试 |
| POST | `/accounts/:id/quit` | `{"kchId":"…","jxbId":"…"}` | `{"ok":bool,"msg":"…"}` | |
| GET | `/accounts/:id/selected` | `?xnm=2026&xqm=3` | `[]protocol.ChosenDic` | 参数必填；未登录 → 401（避免内部阻塞登录）；无功能权限 → 403 + `code=no_permission` |
| GET | `/accounts/:id/score` | `?xnm=2026&xqm=3` | `[]protocol.Score` | 参数必填；未登录 → 401；无功能权限 → 403 + `code=no_permission`；超时 → 504 |

**无功能权限**：学校对没有开通的模块（公共账号查成绩、查个人信息等）返回的是
HTTP 200 + 一张“无功能权限”的 HTML 错误页，不是 JSON，也不是登录页。协议层把它
识别成 `protocol.ErrNoPermission`（不重试、不当成掉线），HTTP 层统一回
`403 {"error":"该账号无此功能权限","code":"no_permission"}`；前端据此在结果区
说明原因，而不是弹红色错误。

### 4.1 自动选课

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| POST | `/accounts/:id/auto-choose` | `{wantClasses?,wantTeachers?,wantTypes?,intervalSeconds?,refreshSeconds?,maxRounds?,once?,durationSeconds?,endAt?}` | 200 `{"status":"ok","flag":"1","msg":"…","course":{…}}` / 200 `{"status":"not_chosen","error":"…"}` / 200 `{"status":"cancelled"}` / 400 / 401 / 409 / 504 |
| POST | `/accounts/:id/auto-choose/cancel` | — | `{"status":"ok"\|"noop"}` |

语义：

* **前置条件**：已登录（401）、已初始化（400）；请求不带愿望清单时回落到账号里已保存的清单，仍为空则 400。
* **预算**：`endAt` > `durationSeconds` > 默认 24h；非法参数在建任务**之前**就 400。
* **节奏**：`intervalSeconds` 默认 1.5s（两轮提交之间）；`refreshSeconds` 默认 30s（重拉候选，让新放出的名额进入队列）。
* **"没选上"不算错误**：返回 200 + `not_chosen`，由前端展示原因。
* **取消是幂等的**：没有在跑的任务时返回 200 + `noop`，调用方要把 `noop` 当成
  "已经没有在跑的任务"来收尾，不能停留在"正在取消…"这类进行中提示。
* **长任务互斥**：同账号同一时刻只能有一个任务（`login` / `init` / `auto` 三者互斥），第二个请求回 409。

### 4.2 其他工具

只服务本机 WebUI，路由前缀 `/accounts/:id/tools`。个人信息和大头照要求账号已登录
（避免查询时静默触发登录）；Cookie 和会话诊断只读本地运行态，不要求登录。

| 方法 | 路径 | 响应 | 备注 |
|---|---|---|---|
| GET | `/accounts/:id/tools/info` | `protocol.UserInfo` | 未登录 → 401；无功能权限 → 403 + `code=no_permission`；超时 → 504 |
| GET | `/accounts/:id/tools/photo` | 原图字节 + `image/*` | 未登录 → 401；未上传/非图片 → 500；响应带 `Cache-Control: no-store` |
| GET | `/accounts/:id/tools/cookies` | `{"cookies":[…],"header":"name=value; …","count":N,"loggedIn":bool}` | 不要求登录；只读内存 cookie jar |
| GET | `/accounts/:id/tools/debug` | `{"generatedAt":…,"account":{…},"engine":{…},"session":{…},"settings":{…}}` | 不要求登录；只返回 hasPassword / hasTicketJWT 等标志 |

要点：

* `/tools/info` 与 `/tools/photo` **不会**在后台静默重登；未登录直接 401，前端提示先登录。
* `/tools/photo` 先嗅探图片字节再判断登录态：学校未上传照片时返回的 HTML 页面不会被误判成掉线。
* `/tools/cookies` 的 `header` 可直接作为 Cookie 请求头，等同于登录凭证；只在本机 WebUI 展示。
* 四个 `/tools/*` 响应都带 `Cache-Control: no-store`，个人信息与 Cookie 不会被浏览器缓存。
* `/tools/debug` 用显式结构体组装，**不会 marshal `Account`**：只暴露 `hasPassword` /
  `hasCasPassword`，以及 JWT 是否存在和长度，不返回密码或 JWT 明文。

---

## 5. 服务端配置与验证码模型（全局）

### 5.1 服务端配置（`/server`）

实现 `web/handlers_server.go` + `web/server_settings.go`，不依赖当前选中的账号；
配置保存在 `server.json`（见 [configuration.md](configuration.md#22-serverjson服务端配置)）。

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| GET | `/server` | — | `serverView` | 当前 SMTP 配置 + 配置是否完整（不测试连接）；**不返回密码**，只返回 `hasPassword` |
| PUT | `/server` | `{"smtp":{...}}`，字段可省略 | `serverView` | 空密码 = 保持原密码；启用时必须能发信；保存成功才生效 |
| POST | `/server/smtp/test` | 可空，或同上 `{"smtp":{...}}` | `{"ok":true,"to":[...]}` | 用页面当前表单 / 已保存配置发测试邮件；密码留空沿用已保存值 |

`serverView` 示例：

```json
{
  "settingsFile": "server.json",
  "smtp": {
    "enabled": true,
    "host": "smtp.qq.com",
    "port": 587,
    "username": "...",
    "from": "...",
    "to": ["..."],
    "notifyAutoChoose": true,
    "hasPassword": true
  },
  "smtpReady": true,
  "smtpError": ""
}
```

`legacyImported` / `legacyFile` 只在第一次启动从旧 `SMTP.json` 导入、尚未保存时出现。
发送失败返回 502，参数/配置错误返回 400；两者都带 `error` 文案。

自动选课结束时，服务端按 `smtp.enabled && smtp.notifyAutoChoose && 账号.autoNotify`
在后台发结果邮件（成功、超时、重试用尽、登录失效）；用户主动取消不发，账号级
`autoNotify=false` 也不发。发送失败只写 `logs/` 日志，不影响任务结果。

### 5.2 验证码模型（`/kaptcha`）

模型只有一份，所有账号共用；配置说明见 [kaptcha.md](kaptcha.md) §4。实现
`web/handlers_kaptcha.go`，不依赖当前选中的账号。

| 方法 | 路径 | 请求 | 响应 | 备注 |
|---|---|---|---|---|
| GET | `/kaptcha` | — | `Status` | 当前设置 + 模型加载状态 |
| PUT | `/kaptcha` | `Settings` 的部分字段 | `Status` | 只提交要改的字段；阈值 `(0,1]`、并发 `0~64`、模型后缀必须是 `.onnx`；加载成功才落盘 |
| POST | `/kaptcha/reload` | — | `Status` | 按已保存设置重新加载模型；失败 400 + `status` |
| POST | `/kaptcha/model` | multipart `file` | `Status` | `.onnx`，≤64 MB；保存到 `kaptcha.json` 同目录的 `models/`，文件名会被清洗；加载成功才启用 |
| POST | `/kaptcha/test` | multipart `image` | `{"text","confidence","accepted","threshold","reason"}` | ≤4 MB；只识别不提交；未启用/无模型 → 400 |

`Status` 关键字段：`settings`、`settingsFile`、`modelPath`、`modelName`、`modelExists`、
`modelSize`、`modelModified`、`loaded`、`enabled`、`loadedAt`、`error`、`numChars`、
`imageWidth`、`imageHeight`、`maxModelBytes`、`defaultModelName`。
`enabled` 是“配置启用且模型已加载”的实际生效值，配置页徽标用它。

应用到模型加载阶段失败时，错误体里还会带 `status`，前端可以就地刷新状态而不用再发一次 GET。

---

## 6. SSE（`/accounts/:id/events`）

服务端实现 `web/stream.go`。

```
event: task
id: 42            ← 任务序号（EventSource 重连时用于对齐）
rev: 7            ← 非标准字段，浏览器忽略；前端从 data.rev 去重
data: {"type":"snapshot","id":42,"rev":7,"loggedIn":true,"pending":null,"qr":null,"task":{...}}

: ping            ← 每 25s 一行注释保活
```

| 帧 | 何时 | `task.events` | 前端处理 |
|---|---|---|---|
| `type:"snapshot"` | 连接建立（首帧必为 snapshot）、状态真的变了 | 完整（最多 100 条） | 全量渲染 + 追加日志；`qr` 元数据只在这里更新 |
| `type:"progress"` | 任务运行中每 5s | `null`（剥掉） | 只更新计时/阶段，不重绘日志 |
| `type:"gone"` | 账号被删除 | — | 刷新账号列表并断开 |

客户端约定（`web/static/app.js`）：

* 只监听 `task` 事件；
* `onerror` 后立刻切 4s 兜底轮询 `/status`，SSE 恢复后关闭轮询；
* 标签页隐藏时主动断开 SSE 并转轮询，可见时重连；
* 用 `id + rev` 去重，收到重连后的旧帧直接丢弃。

### 6.1 `task` 对象

```jsonc
{
  "running": true,
  "id": 42, "kind": "login",           // login | init | auto
  "status": "running",                  // running | done | cancelled | failed | timeout
  "stage": "login.submit", "stageText": "正在提交账号密码",
  "elapsedMs": 8123,                    // 任务已耗时
  "stageMs": 1200,                      // 当前阶段已耗时
  "startedAt": 1761475200000,           // Unix 毫秒
  "endedAt": 0,                         // 0 = 未结束
  "rev": 7,                             // 状态版本号（前端去重）
  "eventSeq": 23, "requests": 17,       // 事件序号 / 本任务发出的请求数
  "events": [
    { "atMs": 120, "timestamp": 1761475200120, "kind": "login.page",
      "text": "正在获取登录页", "repeat": 2 }
  ]
}
```

`events` 服务端最多保留 200 条、下发 100 条；连续重复（同 kind+text、非收尾）折叠成 `repeat: N`。

### 6.2 事件类型

| 来源 | 事件 |
|---|---|
| 任务生命周期（web） | `task.begin`、`task.done`、`task.failed`、`task.cancelled`、`task.timeout`、`task.input.captcha`、`task.input.password`、`task.input.submitted` |
| 登录阶段（protocol 上报） | `login.prepare`、`login.fetch-form`、`login.rtk`、`login.captcha-image`、`login.captcha`、`login.public-key`、`login.submit`、`login.password`、`login.portal`、`login.portal-ticket`、`login.sso`、`login.ticket`、`login.done`；扫码特有 `login.qr`、`login.qr-scanned`、`login.qr-expired`、`login.qr-refused`；门户密码登录特有 `login.password-error`、`login.account-locked`、`login.session-valid`、`login.session-expired`、`login.failed-attempts`、`login.portal-failed` |
| 初始化阶段（protocol 上报） | `init.index`、`init.index-wait`、`init.index-parse`、`init.list-pre`、`init.list-pre-parse` |
| web 自有 | `login.begin`、`init.login`、`init.reset`、`init.begin`、`auto.plan`、`auto.collect`、`auto.{begin,pick,failed,success,refresh,refresh_failed}` |
| 请求反推（`classify.go`） | `init.prepare`、`init.courses`、`init.detail`、`init.selected`、`course.choose`、`course.quit`、`http:<path>`（不写日志，只推进"当前阶段"） |

---

## 7. 前端

`web/static/` 通过 `//go:embed static/*` 打在二进制里（`web/server.go:12`），挂在 `/ui`。

| 文件 | 规模 | 说明 |
|---|---|---|
| `index.html` | 664 行 | 侧栏账号列表 + 「⚙️ 服务端配置」全局页（验证码模型 / 邮件通知两个子页） + 顶部徽标 + 常驻任务面板 + 6 个页签（初始化 / 课程 / 已选 / 成绩 / 自动选课 / 其他工具）+ 三个 `<dialog>`（账号表单、验证码/密码输入、微信二维码） |
| `app.js` | 3635 行 | `API.req` 统一封装 fetch；`Task` 负责 SSE 渲染与本地计时；验证码配置页 / SMTP 表单用 `API.upload` 与 `API.req`；无框架、无构建步骤 |
| `style.css` | 1208 行 | 手写样式 |

改造前端后**必须重新 `go build`**（`go:embed` 在编译期打包），直接改 `static/` 不会热更新。
