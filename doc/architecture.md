# 架构

> 适用于 `webui` 分支当前 HEAD（`f336e7a`）。所有路径相对仓库根。
> 行号会随提交漂移，定位时以符号名为准。

---

## 1. 分层与依赖

```
                    ┌──────────────────────────────┐
                    │            main              │  flag 解析 / 进程生死
                    └──────────────┬───────────────┘
                                   │
                        ┌──────────▼────────────┐
                        │          web          │  REST + SSE + 内嵌前端
                        └──────────┬────────────┘
                                   │
                        ┌──────────▼────────────┐
                        │         engine        │  会话状态 / 重试策略 / 自动选课
                        └──────────┬────────────┘
                                   │
                  ┌────────────────▼─────────────────┐
                  │             protocol             │   发请求 + 解析 + 登录状态机
                  └──┬────────┬─────────┬───────────┘
                     │        │         │
         ┌───────────▼─┐ ┌────▼─────┐ ┌─▼──────────────┐
         │ protocol/   │ │ check_   │ │    utils       │
         │ config,cas2 │ │ code     │ │  logger/color  │
         │ rsa,hedge…  │ │          │ │  net/time      │
         └─────────────┘ └──────────┘ └────────────────┘
```

**实际 import 边**（`go list` 结果，`deps_test.go` 只放行这些方向）：

| 包 | 内部依赖 |
|---|---|
| `school_sdk` (main) | `check_code/kaptcha`, `utils`, `web` |
| `school_sdk/web` | `engine`, `protocol`, `protocol/config`, `check_code/kaptcha`, `utils` |
| `school_sdk/engine` | `protocol`, `protocol/config` |
| `school_sdk/protocol` | `check_code`, `protocol/{GPA,cas2,config,hedge,internal,rsa}`, `utils` |
| `school_sdk/check_code/kaptcha` | 标准库 + 包内 `internal/{onnx,imagex}`（无第三方依赖） |

`check_code/kaptcha.Manager` 由 `web.AccountStore` 创建并持有；`protocol` 通过
`KaptchaRecognizer` 接口（`protocol/kaptcha.go`）消费识别结果，**不认识**具体实现——
这样协议层仍然可以单独测试、单独复用。

`deps_test.go` 强制的三条规则：

1. `protocol` 不得 import `engine` / `web`；
2. `engine` 不得 import `web`；
3. 除 `main` 外任何包不得 import `web`。


---

## 2. 各层职责

### 2.1 `protocol` —— 请求与解析

定位上它只做两件事：**怎么发一次请求、怎么解析响应**——不读 stdin、不写文件、不决定进程生死、
不自动重登（会话失效返回 `ErrUnauthorized`，由上层决定）。这些约束**还没完全做到**：
选课/登录路径里仍有少量日志式打印，见 [analysis §3.2](analysis.md)。

| 文件 | 职责 |
|---|---|
| `client.go` | `APIClient` 定义、resty 构造、路由 cookie、`Settings()` / `Session()` 快照 |
| `login.go` | 登录主流程（1251 行）：滑块 / 字符验证码 / 无验证码三条分支，密码错误重试 |
| `kaptcha.go` | 字符验证码自动识别：`KaptchaRecognizer` 接口、置信度/文本校验、连续判错后的降级策略（见 [kaptcha.md](kaptcha.md)） |
| `loginState.go` | 登录态（`atomic.Bool`）、`loginCoord` 串行锁与冷静期 |
| `login_stage.go` / `init_stage.go` | 语义阶段记账 + observer |
| `cas2/` | 统一身份认证：`login.go`（门户换票）、`portal.go`、`wxLogin.go`（扫码）、RSA 加密 |
| `CourseAPI.go` | 选课接口：首页参数、列表前置参数、列表、详情、选课、退课、已选 |
| `CourseModel.go` | `APIConfig`（五组状态）、各接口的 DTO |
| `CourseMerge.go` | 课程列表/详情的增量合并（纯函数） |
| `CourseParsing.go` | 选课控制菜单解析（`parseKklxdmXkkz__`）、时间差检查 |
| `config_view.go` | 页签读写（`TabsOf` / `WithTab` / `ModeNameOf`），避免外部碰 `APIConfig` 字段 |
| `paramRefresh.go` | "加密串错误"信号（列表/详情）与文案识别（选课接口） |
| `errors.go` | `ErrUnauthorized`、`StageError{Op,Message,Err}` |
| `service.go` | 会话探测（`CheckSession*`）、302 判定 |
| `detectKeepAlive.go` | 会话探测（`Ping` / `SleepWithContext`） |
| `wish.go` | 愿望清单匹配与打分（纯函数） |
| `score.go` / `exam.go` / `gpa.go` / `GPA/` / `user_info.go` / `wakeup.go` | 查询与导出：一律返回结构体/CSV 字节，打印交给宿主 |

### 2.2 `engine` —— 会话语义

`Engine` = 一个 `protocol.APIClient` + 一份 `session`（`engine/engine.go:44-49`）。

它负责三件上层不该自己写的事：

* **初始化**：`Initialize` 抓 `GetIndexParams` + `GetCourseListPre`，成功后才发布快照；
* **自愈**：`retryOnUnauthorized`（重登一次）+ `call`（参数过期后 `session.Renew` 再试一次）；
* **自动选课**：`Plan`（`plan.go`）+ `CandidateSource`（`candidates.go`），可复用给 Web 和将来别的宿主。

并发设计（`engine/session.go:42-75` 有完整注释）：

* `initMu` 只串行化初始化/`Renew`，持锁期间做网络 IO 但**不持有状态锁**；
* `mu`（`RWMutex`）只保护已发布的 `*APIConfig` 快照，持锁路径不做 IO；
  每个请求 `snapshot()` 拿一份拷贝，请求内部怎么改都影响不到别人；
* `inited` 用原子变量，`/status` 这类热路径连读锁都省了；
* `generation` 让"Reset 之前发起的初始化"在发布时作废（`ErrSessionReset`）；
* 课程缓存 `cacheMu` 单独一把锁，读永远返回副本。

### 2.3 `web` —— WebUI 宿主

`web.Server` 只装配 gin 路由；业务状态在 `AccountStore`（账号 + 运行态）与 `taskStore`（长任务）。

| 文件 | 职责 |
|---|---|
| `server.go` | 路由注册、`/ui` 静态资源（`go:embed static`） |
| `store.go` | `accounts.json` 读写、账号 CRUD、运行态缓存、原子落盘（tmp + rename） |
| `account.go` | `Account` 模型、`normalize()`、`toSettings()` / `session()` / `applySession()` 唯一映射点 |
| `runtime.go` | `accountRuntime`：engine + 登录/初始化/自动选课三把独立锁 + pendingInput + 阶段回调 |
| `task.go` | 长任务状态机（running/done/failed/cancelled/timeout）、事件流、SSE 订阅 |
| `stream.go` | SSE 推帧（`snapshot` / `progress`）、心跳 |
| `classify.go` | 请求 URL → 阶段文案（只覆盖选课类端点，登录/初始化阶段由 protocol 上报） |
| `server_settings.go` | 服务端配置（`server.json`）：SMTP 设置、旧 `SMTP.json` 导入、校验与原子落盘 |
| `handlers_*.go` | 按功能拆的 handler：account / login / init / course / auto / score / wish / kaptcha / server |

### 2.4 `utils` / `check_code`

* `utils`：日志（`logger.go`，`init()` 改 `time.Local`、接管 `log` 输出）、颜色包 `utils/color`、
  私网 IP 判断、NTP 校时与时间锁（`checkTime_windows.go`）、双击 Ctrl+C 退出（`exit.go`）、
  SMTP 发信（`smtp.go`，支持 465 隐式 TLS / 587 STARTTLS）。
* `check_code`：验证码识别。
  滑块：`matcher.go` 用 10 张内置模板做逐列像素绝对差匹配（结果 `+70` 补偿滑块宽度），
  模板由 `sync.Once` 后台预加载，就绪标志用 `atomic.Bool`、模板与初始化错误走 `RWMutex`，
  `getCachedImages` 返回副本；`gen_track.go` 生成拖动轨迹，`save.go` 落盘样本，`display*.go` 调试显示。
  `matcher_test.go`/`gen_track_test.go`/`save_test.go` 覆盖掩码提取、滑窗边界与 `+70`、并发初始化/识别、
  轨迹形态与落盘。
  字符：`check_code/kaptcha/` 内置纯 Go ONNX 解释器（`internal/onnx`）与图片预处理（`internal/imagex`），
  `kaptcha.go` 做加载/门限/结果校验，`manager.go` 管 `kaptcha.json`、热切换、配置页状态与测试识别；
  不依赖 cgo，也不依赖外部模块。详见 [kaptcha.md](kaptcha.md)。

---

## 3. 核心类型与所有权

| 类型 | 位置 | 谁写 | 谁读 | 是否落盘 |
|---|---|---|---|---|
| `config.Settings` | `protocol/config/config.go:13` | 上层构造，client 只读（值拷贝） | client / engine | 间接（`accounts.json` 的对应字段） |
| `config.Session` | `protocol/config/session.go:14` | client 登录链路 | 上层登录结束后取快照 | `accounts.json`（`ticketJWT` / `learnedRoutes`） |
| `web.Account` | `web/account.go:21` | `AccountStore`（持锁） | handler / `toSettings` | ✅ `accounts.json` |
| `protocol.APIConfig` | `protocol/CourseModel.go:23` | `GetIndexParams` / `SelectTab` / `Renew` | 选课/查询请求 | ❌ 运行态 |
| `engine.session` | `engine/session.go:49` | engine 自己 | engine 自己 | ❌ |
| `accountRuntime` | `web/runtime.go:16` | `AccountStore` | handler | ❌ |

两条硬约定：

1. **`Settings` 与 `Session` 分离**：用户配置与登录产物生命周期不同。`Settings` 值拷贝、
   调用方改不到 client 内部；`Session` 由 client 在登录 goroutine 里写，登录结束后上层
   取 `Snapshot()` 落盘，client 自己**从不写文件**。
2. **配置只有一个默认值来源**：`config.Default()`（`defaults.go:29`）。`Account.normalize()`
   把磁盘上的脏值（非法时长、登录页 URL、坏 UA）收敛成默认值，`toSettings()` 是唯一映射点。

---

## 4. 一次「Web 登录 → 初始化 → 选课」的数据流

```
浏览器                    web                       engine                protocol
  │  POST /login            │                          │                     │
  ├────────────────────────►│ tasks.start("login")     │                     │
  │                         ├─────────────────────────►│ Login(ctx)          │
  │                         │                          ├────────────────────►│ LoginWithContext
  │                         │                          │   reportLoginStage ─┤ 阶段记账
  │                         │◄── OnStage(login.*) ─────┤                     │
  │◄── SSE snapshot ────────┤                          │                     │
  │                         │                          │   需要验证码时 ─────┤
  │                         │◄── Ask(InputRequest) ────┤   InputHandler      │
  │◄── pending（图/提示）───┤                          │                     │
  │  POST /login/input      │                          │                     │
  ├────────────────────────►│ submitInput ────────────►│ 拿到输入，继续 ─────┤
  │◄── SSE done ────────────┤ applySession + 落盘       │                     │
  │                         │                          │                     │
  │  POST /init             │                          │                     │
  ├────────────────────────►├─────────────────────────►│ Initialize          │
  │                         │                          ├─ GetIndexParams ───►│
  │                         │                          ├─ GetCourseListPre ─►│
  │◄── SSE done ────────────┤                          │ 发布新快照           │
  │                         │                          │                     │
  │  GET /courses           │                          │                     │
  ├────────────────────────►├─────────────────────────►│ GetCourseList       │
  │                         │                          ├────────────────────►│
  │                         │◄───── CacheCourseList ───┤◄──── 解析后的列表 ───┤
  │◄──── JSON ──────────────┤                          │                     │
```

要点：

* **两个长任务共用一套可观测机制**：`taskStore` 记账，SSE 推 `snapshot`（状态变了）/ `progress`（只走秒）。
* **登录与初始化互不阻塞**：`accountRuntime` 里 `loginMu` / `initMu` / `autoMu` 三把独立的锁。
* **同一账号同时只能有一个长任务**（`task.go:105` 的 `start` 返回 `false`）；自动选课期间
  不能再登录/初始化，前端需要提示。

---

## 5. 两条自愈链路

### 5.1 会话失效 → `ErrUnauthorized`

以前 protocol 内部有 22 处 `ReLogin()` 调用，登录时会在请求线程里偷偷重登。
现在 protocol 只返回 `protocol.ErrUnauthorized`：

```
请求 → 302 到登录页 / 未登录提示 ──► protocol：loggedIn=false + ErrUnauthorized
        │
        ├─ engine.retryOnUnauthorized：未登录就重登一次 + 重试一次
        │  （受账号 autoRelogin 开关控制；只一次，彻底失效就交给上层）
        └─ 仍失败 ──► web：401 + code=unauthorized
                        └─► 前端统一处理：徽标切未登录 + 提示 + 重拉 /status
```

登录态不是只由登录 / 登出 / Ping 维护：**任何**业务响应发现"服务器不认这个会话"
都会当场记账（`markSessionExpired`），否则 `/status` 会在错误弹窗之后继续报
`loggedIn:true`，前端只能靠用户自己 Ping 才发现掉线。

自动重登由账号级 `autoRelogin` 控制（默认开，见 `web/account.go`）：
关闭后 `authorize()` 直接返回 false，课程 / 成绩 / 工具 / 初始化都只回 401，
不在业务请求里偷偷跑登录；Ping 也不再起后台重登。

### 5.2 选课参数过期 → `Renew`

正方的"加密串错误，请刷新网页重试"意味着 `xkkz_id` 被轮换了：

* 列表/详情接口：protocol 置 `paramRefresh` 原子标志（`paramRefresh.go`），engine 的 `call`
  用 `TakeParamRefresh()` 单消费者取走 → `session.Renew` 重抓首页参数 → 重试一次；
* 选课接口：没有隐式通道，只能从返回文案识别（`NeedsParamRefresh`），`Engine.Choose` 里
  再做一次同样的重抓 + 重试；
* `Renew` 保留当前页签、不清课程缓存，并有 **10s 冷静期**（`engine/session.go:31`）：
  服务端持续报错时不要每个请求都去抓首页。

---

## 6. 并发模型一览

| 层 | 锁 | 保护对象 | 持锁时允许 IO？ |
|---|---|---|---|
| `protocol.APIClient` | `login.mu` + gate | 登录流程串行化（per client） | 是（登录本身就要发请求） |
| `protocol.APIClient` | `loginStage.mu` / `initStage.mu` | 阶段快照 | 否（observer 在锁外调用） |
| `engine.session` | `initMu` | 初始化 / Renew | 是（设计上就是要持锁做 IO） |
| `engine.session` | `mu`（RW） | 已发布的 `APIConfig` | 否 |
| `engine.session` | `cacheMu`（RW） | 课程缓存 | 否 |
| `web.AccountStore` | `mu`（RW） | accounts map + order + runtimes | 否（`saveLocked` 在写锁内落盘） |
| `web.taskStore` | `mu` | 全部账号任务状态 | 否 |
| `web.serverSettingsManager` | `mu`（RW） | `server.json` 的 SMTP 设置 | 是（保存时 `.tmp` + rename） |
| `web.accountRuntime` | `loginMu` / `initMu` / `autoMu` / `inputMu` | 任务互斥、pendingInput | 视任务而定 |
| `check_code` | `sync.Once` + `cacheMutex`（RW）+ `atomic.Bool` | 10 张内置模板、初始化错误、就绪标志 | 否（PNG 解码在锁外，发布时短暂持写锁） |
| `check_code/kaptcha.Manager` | `applyMu` + `mu`（RW） | `kaptcha.json` 设置、当前 `Recognizer`、加载错误 | 是（`kaptcha.json`，保存时 `.tmp` + rename） |

已知薄弱点见 [analysis.md](analysis.md#3-结构性问题)。

---

## 7. 扩展点（宿主与协议之间的端口）

| 端口 | 类型 | 挂载方式 | 用途 |
|---|---|---|---|
| `Ask` | `protocol.InputHandler` | `engine.Options.Ask` / `APIClient.InputHandler` | 验证码、新密码；Web 走 SSE + pendingInput；未设置时登录直接报错 |
| `OnStage` | `func(LoginStage)` | `APIClient.SetLoginObserver` | 登录语义阶段（不依赖 HTTP hook） |
| `OnInitStage` | `func(InitStage)` | `APIClient.SetInitObserver` | 初始化语义阶段 |
| `OnRequest` | `internal.ProtocolHook` | `APIClient.SetProtocolHook` | 每次请求发出前（含 cas2 的两个 resty 客户端） |
| `Kaptcha` | `protocol.KaptchaRecognizer` | `engine.Options.Kaptcha` | 字符验证码自动识别；关闭或识别失败时回退人工输入（见 [kaptcha.md](kaptcha.md)） |
| `Plan.Sink` | `func(engine.Event)` | `RunAuto(ctx, plan, source, sink)` 的 `sink` 参数 | 自动选课进度事件 |
| `Plan.Source` | `func(ctx) ([]CustomCourseDic, error)` | `RunAuto` 的 `source` 参数 | 候选来源；`Engine.CandidateSource` 是绑定缓存的实现 |

设计口径：**钩子可以缺席**。阶段与登录态由 protocol 自己记账（`loginState` / `loginStage`），
hook 只补充"请求明细"，因此不挂 hook 的宿主也能显示进度。

---

## 8. 错误与可观测性约定

* 库代码返回 `error`，不 `panic` / `log.Fatal` / `os.Exit`；终端与 HTTP 状态码由宿主决定。
* `StageError{Op, Message, Err}`：`Message` 是可以直接给用户看的中文，`Op` 是阶段名，
  `errors.Is/As` 可用。
* 判断"登录失效"只认 `errors.Is(err, protocol.ErrUnauthorized)`，不要再匹配字符串。
* Web 的错误统一走 `web/api_error.go` 的 `writeEngineError` / `writeUnauthorized`：
  401 必须带 `code`（`unauthorized` 或 `login_failed`），前端按 code 分支。
* 日志：`utils/logger.go` 统一接管全局 `log`，每次启动写一个 `logs/<启动时间>.log`。
