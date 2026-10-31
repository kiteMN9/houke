# 项目现状分析

> 基线：分支 `webui`，HEAD `f336e7a`（2026-10-27）；`check_code` 的测试补齐与并发修复计入本次改动。
> 2026-10-28 删除了 `tui` 与 protocol 的 CLI 宿主层（`controller` / `CourseCtrl` / `CourseUtil` / `other` /
> `keepSession` / `evaluations`），本文的规模与打印统计已按删除后重算；标「已修复/已删除」的段落保留原状作为历史。
> 统计口径：`*.go` 中的**物理行数**（含空行与注释），非测试文件不含 `_test.go`。
> `file:line` 相对仓库根，会随提交漂移，定位时优先认符号名。

---

## 0. 结论摘要

1. **分层已收口**：`main -> web -> engine -> protocol`，TUI 与 protocol 里的 CLI 宿主（菜单、`XK/JL/Boom`、
   打印）已在 2026-10-28 删除。剩下的分层问题只有 protocol 里尚未清理完的诊断输出（224 行 `fmt.Print*`）。
2. **安全是这个项目唯一的 P0**：Web 无鉴权、无 CSRF、密码回显、明文 0644 落盘。
   当前"只在本机单人用"是它成立的前提，一旦监听 `0.0.0.0` 就不成立。
3. **工程质量在同类个人项目里算高的**：分层门禁、各包 `-race` 全绿、错误约定统一、
   SSE 替代轮询、参数过期自愈。真正的短板是仓库卫生（无 CI、IDE 配置入库、抓包样本裸奔）。
4. **优先做什么**：把 protocol 剩余的打印降到 `log`（§3.2a），然后补鉴权；再谈多校适配。

---

## 1. 规模

| 包 | 源码文件 | 源码行 | 测试文件 | 测试行 | 测试/源码 |
|---|---:|---:|---:|---:|---:|
| `protocol`（含 7 个子包） | 44 | 7,565 | 31 | 2,624 | 35% |
| `web` | 17 | 3,424 | 26 | 3,418 | 100% |
| `engine` | 4 | 1,121 | 6 | 1,228 | 110% |
| `utils` | 10 | 411 | 4 | 125 | 30% |
| `check_code` | 5 | 574 | 3 | 467 | 81% |
| `main`（含 `deps_test.go`） | 1 | 36 | 1 | 87 | — |
| **合计** | **81** | **13,131** | **71** | **7,949** | **61%** |

另有前端 3,478 行（`app.js` 2,248 / `index.html` 381 / `style.css` 849）与 1 个油猴脚本。
`protocol` 根包 23 个源文件，子包为 `cas2`、`config`、`GPA`、`hedge`、`internal`、`rsa`、`cas2/utils`。
删除前 `protocol` 是 9,658 行、`tui` 850 行（2026-10-28 的改动净减约 2,500 行）。

`protocol` 根包对外导出 **40 个函数、77 个方法、41 个类型**——CLI 时代的控制流与打印已经删掉，
这个数字现在基本是"一个请求 + 解析"层的真实体积。

### 1.1 验证基线（本次实测）

```
go build ./...            ok
go vet ./...              ok
go test ./... -count=1    ok（school_sdk 9.1s · engine 7.9s · web 6.7s · protocol 0.5s）
go test -race ./...       ok（14 个包全部通过；school_sdk 12.3s · check_code 14.7s · web 13.0s）
```

根包 `TestDependencyDirection` 也在其中——分层门禁是真在跑的。
`check_code` 的并发初始化与并发识别用例（`TestCacheInitConcurrent` / `TestFindBestMatchConcurrent`）
同样在 `-race` 下运行。以上是删除 TUI 之后的实测值。

---

## 2. 做对了什么（回退就是退步）

| # | 设计 | 现状 | 价值 |
|---|---|---|---|
| 1 | **依赖方向门禁** | `deps_test.go` 用 `go list` 校验 4 条规则 | 让"分层"从口头约定变成 CI 可验证的事实 |
| 2 | **配置/会话分离** | `config.Settings`（值拷贝、只读） vs `config.Session`（登录产物） | client 不再写盘；账户文件由 store 独占 |
| 3 | **默认值单一来源** | `config.Default()`，非测试代码禁止字面量 `"43s"`/`"21s"` | 消灭了"同一份配置走不同入口结果不同" |
| 4 | **会话失效上抛** | `ErrUnauthorized` + engine 重试一次 | protocol 不再偷偷重登，重试策略可测、可解释 |
| 5 | **阶段自记账** | `LoginStage` / `InitStage` 由 protocol 上报，不依赖 hook | 宿主不挂 hook 也有进度；WebUI 不用靠 URL 猜阶段 |
| 6 | **长任务可观测** | `taskStore` + SSE 快照/进度帧 + 兜底轮询 | 10 分钟登录从"转圈"变成"卡在哪一步、等了多久" |
| 7 | **参数过期自愈** | `paramRefresh` 信号 + `session.Renew` + 10s 冷静期 | 抢课跑到半夜遇到"加密串错误"能自己恢复 |
| 8 | **合并是纯函数** | `MergeCourseList` / `MergeCourseDetail` / `WishList.Rank` | 无锁可测，engine 只负责加锁拷贝 |
| 9 | **错误不杀进程** | 库层无 `os.Exit`、无 `log.Fatal`（实测 0 处真实调用） | Web 里一次配置错误不会带走整个服务 |

---

## 3. 结构性问题

### 3.1 P0：安全与正确性

| # | 问题 | 证据 | 后果 |
|---|---|---|---|
| S1 | **API 无任何鉴权** | `web/server.go:23-24` 只挂 `gin.Logger()` + `gin.Recovery()`；`/api` 组（`server.go:56-105`）无中间件 | 同机任何进程/浏览器都能登录、选课、读密码；改 `-webAddr 0.0.0.0` 即全网可达 |
| S2 | **无 CSRF / Origin 校验** | 全包无 CSRF token、无 `Origin` 检查（`app.js:40-47` 的 fetch 也不带令牌） | 任意网页可对 `127.0.0.1:8080` 发起跨站请求，浏览器不会拦（简单请求） |
| S3 | **密码回显** | `web/store.go:100-101`、`118-119`、`367-369` 的脱敏代码被注释 | `GET /api/accounts` 返回全部明文密码 |
| S4 | **明文落盘 0644** | `web/store.go:72-80`（`os.WriteFile(tmp, data, 0o644)`） | 同机其他用户可读账号库；备份/同步工具会带走 |
| S6 | **长任务不可随页面取消** | `handlers_login.go:44`、`handlers_init.go:28`、`handlers_auto.go:173` 都用 `context.Background()` | 关掉浏览器后登录/抢课继续跑（最长 24h）；只能显式 cancel 或等超时 |
| S7 | **runtime 错误一律 404** | `handlers_login.go:31-33`、`handlers_course.go:18-20`、`stream.go:68-72` | 配置非法（如 baseURL 写错）也提示"账号不存在"，用户无从排查 |
| S8 | **取消曾与任务启动分成两步**（已修复） | 旧实现 `handlers_login.go` 先登记空 `func(){}`、再登记真取消；现 `startCancelable`（`task.go:116`）把取消函数与"运行中"登记在同一次加锁里，`runLogin`（`handlers_login.go:97`）改为「先建 ctx → 抢 `rt.loginCancel` → 再 start」 | 修复前窗口内 `/login/cancel` 回 `ok` 却只标记任务、ctx 不取消；所有权检查失败还会留下永远 running 的"幽灵任务"，后续登录全被 409 挡住。回归测试见 `web/login_cancel_test.go` |

> S1–S4 的共同前提是"单机单人"。要么明确写进 README 作为使用约束（当前选择，见根 README §6），
> 要么补鉴权（见 [roadmap §1](roadmap.md#1-安全补丁优先级最高)）。**不能既不补、又对外监听。**

### 3.2 P1：`protocol` 里残留的打印

**(a) CLI 宿主已删除（2026-10-28）**

`controller.go`（`Step` 分派）、`CourseCtrl.go`（`XK`/`JL`/`Boom`）、`CourseUtil.go`（打印/愿望检查/手动选课）、
`other.go`（"其他"菜单）、`keepSession.go`、`evaluations.go` 与整个 `tui/` 一起删除，CLI 控制流
（≈1,018 行）、stdin 读取、`signal.Notify` 随之归零。删掉的最典型签名是
`HandChooseCourse(..., sigCh chan os.Signal)`——协议层收一个 `chan os.Signal`，等于承认自己知道终端的存在。

查询类能力改成了返回数据（详见 [roadmap §2](roadmap.md#2-主线把-cli-从-protocol-洗出来tui-已删除)）：
`GetGPA` / `GetExam` / `GetJsonInfo` / `WebGetScore` 返回结构体，`SelectedCSV` / `TimetableCSV` 返回带 BOM 的字节。

残留的打印与等待（`protocol` 非测试代码）：

| 指标 | 数量 | 分布 |
|---|---:|---|
| `fmt.Print*` | **224** | `login.go` 113、`CourseAPI.go` 54、`portal.go` 18、`excel.go` 11、`service.go` 11、`rsa.go` 5、其余 12 |
| `time.Sleep` | **69** | `login.go` 45、`CourseAPI.go` 15、`portal.go` 4，其余为退避 |
| `utils.UserInputWithSigInt`（读 stdin） | **0** | 验证码在没设 `InputHandler` 时直接报错 |
| `signal.Notify` | **0** | — |
| `os.Exit` / `log.Fatal` | **0**（真实调用） | `login.go:57` 那处只在注释里 |

`login.go` 的 113 处打印几乎全是登录过程的诊断输出（"第 n 次请求"、"验证码识别为…"），
下一步是把它们降到 `log`，这也是 `deps_test.go` 注释里两条 AST 规则的前置条件。

**(b) `tui` 与 `protocol.Controller` 已删除**

分层收敛为 `main -> web -> engine -> protocol`：`engine` 是唯一的编排点（重登、参数自愈、并发），
`web` 是唯一的宿主。原先 TUI 直接调 `protocol` 造成的能力不对称（会话失效不重试、不刷新参数、
自动选课读 `want.xlsx`）随两套实现一起消失。`want.xlsx` 因此没有读取方，
`config.ReadExcel` 作为"以后导入愿望清单"的能力保留。

**(c) 单校硬编码**

`ycit` 在非测试代码里 **35 处 / 7 文件**：`cas2/portal.go` 7、`cas2/login.go` 6、`login.go` 6、
`cas2/wxLogin.go` 5、`client.go` 3、`cas2/ycitCasEndpoints.go` 1、`config/defaults.go` 1。此外还有：

* MD5 盐字面量 `salt354waragthaswrg`（`protocol/cas2/login.go:149,157`）；
* 两条写死的 route MD5（`protocol/client.go:319-322`）与 `Domain: "ycit.edu.cn"`（`protocol/client.go:255`）；
* SSO 路径 `/sso/hnyyxyiotlogin`（`protocol/login.go:1060`）、CAS 路径常量（`protocol/cas2/ycitCasEndpoints.go`）；
* 学期编码表 `TERM = map[int]string{0: "", 1: "3", 2: "12", 3: "16"}`（`protocol/client.go:386`，可被任意改写）。

`BaseURL` 已经是可配的，但"登录链路长什么样"仍然只对 ycit 成立。多校适配的正确做法是抽
`school profile`（端点 + 盐 + 默认值 + route 池），而不是继续加 `if strings.Contains(baseURL, ...)`。

**(d) 同一份知识写两遍**

* 端点表：`protocol/config/endpoints.go`（常量）与 `web/classify.go:56-73`（裸字符串后缀）。
  新增端点要改两处，漏了就丢阶段。

GPA 与成绩的"双实现"已在 2026-10-28 消除：`GPA.Parse` 取代了 `gpa.go` 里重复的打印解析，
`GetScore` / `GetScoreRaw` 删除后只剩 `WebGetScore`。

**(e) 死代码与未接通的链路**

| 对象 | 状态 |
|---|---|
| `protocol.APIClient.ReLogin` | 只剩注释（`login.go:35-41` 整段被 `//` 注释掉）；重登已归 engine |
| `NewClientWithCookieJar`（`client.go:296`） | 无生产调用点（只出现在测试注释里），`onlyCookieMethod` 分支走不到 |
| `utils/markString.go` | `MaskString` 零调用（脱敏在 web 侧，且那几行被注释掉了） |
| `utils/qrCode.go` | `QrCodeDecode` / `QrCodePrint` / `ClearQrCode` 零调用，拖着 `gozxing`、`skip2/go-qrcode` 两个依赖 |
| `utils/checkTime.go` + `checkTime_windows.go` | `CheckNTPTime` / `getNTPTime` 零调用（NTP 校时 + 2026-09-19 时间锁，属策略代码），拖着 `beevik/ntp` |
| `utils/net.go` 的 `NetCheck` / `IsExist` | 零调用 |
| `utils/logger.go` 的 `InitLogger` | 零调用（`init()` 里已自动接管 `log`） |
| `protocol/config/excel.go` | `ReadExcel` 现在**没有调用方**（原 TUI 菜单 6 用它读 `want.xlsx`），保留为愿望清单导入能力 |
| `protocol.GetGPA` / `GetExam` / `GetJsonInfo` / `SelectedCSV` / `TimetableCSV` | 协议层已就绪、WebUI 还没接页面——这是"能力保留"的决定，不是遗漏 |

`utils` 的死代码合计约 200 行，且三个依赖（`gozxing`、`skip2/go-qrcode`、`beevik/ntp`）只被它们引用，
删掉后 `go mod tidy` 能顺手瘦身；`excelize` 仍被 `config/excel.go` 使用。

### 3.3 P2：工程与可维护性

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| E1 | 仓库没有 README、没有 CI | 本次已补 README；`.github/` 不存在 | 加 Actions：build / vet / test / `-race` |
| E2 | `.idea/` 被跟踪（6 个文件） | `git ls-files .idea` | `git rm -r --cached .idea` + ignore |
| E3 | `html/`（15 个抓包样本）与 `doc/*.png` 既不跟踪也不忽略 | `git status` 长期出现 `??` | 移出仓库或收进 `doc/samples/` 并明确许可 |
| E4 | 40 MB 的 `school_sdk.exe` 留在工作区 | 根目录 | 删除（已 ignore，不影响代码） |
| E5 | `.gitignore` 94 行里约 80 行是一次性历史文件名 | `configX.json`/`Zjut.json`/`6.json`… | 按模式归并 |
| E6 | 无 `.gitattributes` | `git config core.autocrlf` = `input` | 加 `* text=auto eol=lf` |
| E7 | `PUT /accounts/:id` 语义含糊 | `store.go:297-371`（空串=不改，无法清空）、`:354`（bool 无条件覆盖） | 改 PATCH + 指针字段，或写进 API 文档 |
| E8 | 单进程假设 | `store.go:68-84` 每次变更全量重写、无跨进程锁 | 文档写明"同时只能跑一个实例" |
| E9 | `store.getRaw` 返回内部活指针 | `store.go:125-130`（RUnlock 之后返回 `*Account`） | 返回拷贝（当前只有测试在用） |
| E10 | `PUT /accounts/:id` 的响应会带 `ticketJWT` | `store.go:366-369`（清空的三行全被注释），而 `List`/`Get` 会清 | 响应统一走脱敏后的 view 结构体 |
| E11 | 账号被删时不停任务（已修复） | `store.go:402-418` 删除即连运行态与任务状态一起丢，旧 `drop` 只清状态、弃置 `cancel`；现 `task.go:234-268` 摘下 `cancel` 后在锁外调用，`cancelInitCourse`/`cancelAutoChoose` 补 `takeCancel` 兜底（`handlers_init.go:153`、`handlers_auto.go:301`） | 删除前 cancel ✅ 回归测试见 `web/delete_cancel_test.go` |
| E12 | SSE 用了非标准 `rev:` 字段 | `stream.go:187` | 浏览器忽略它，前端只好从 `data.rev` 去重；保留但写清楚 |
| E13 | 测试缺口 | `protocol` 的查询类 API（`Exam`/`GPA`/CSV）只有部分解析用例；无 E2E（`check_code` 已补单测与并发用例） | 给 `GetExam` / `SelectedCSV` / `TimetableCSV` 补解析用例 |

---

## 4. 债务量化（迁移目标）

| 指标 | 删除 TUI 前 | 现在 | 目标 | 说明 |
|---|---:|---:|---:|---|
| `protocol` 非测试 `fmt.Print*` | 444 | **224** | 0 | 输出降到 `log`，不再是 CLI 菜单打印 |
| `protocol` 非测试 `time.Sleep` | 89 | **69** | 仅保留请求退避 | 剩下的基本是登录/选课的退避 |
| `protocol` 读 stdin | 7 | **0** ✅ | 0 | — |
| `protocol` `signal.Notify` | 4 | **0** ✅ | 0 | — |
| `protocol` CLI 控制流 | ~1,018 行 | **0** ✅ | 0 | `controller.go` + `CourseCtrl.go` + `other.go` 已删 |
| `ycit` 出现次数 | 52 | **35** | ≤ 3（profile + 默认值 + 注释） | 抽 school profile |
| 端点表 | 2 份 | 2 份 | 1 份 | `web/classify.go` 基于 protocol 常量生成 |
| 死文件/死 API | 8 | **7** | 0 | 见 §3.2(e) |

真正的验收不是行数，而是把 `deps_test.go:9-11` 注释里规划的两条 AST 规则变成真的：

1. `protocol` 不得 import `fmt` / `log` / `os.Stdout` / `os.Exit` / `survey` / `color`；
2. `protocol` 不得出现 `time.Sleep`。

---

## 5. 已知但当前可以接受

* **密码明文存储**：需求是"本机自动登录选课"，加密只是把密钥换个地方放。真正的解法是
  "不存密码，存 cookie/JWT"（`ticketJWT` 已经在做），但教务密码在密码重置流程后仍需用户重填。
* **没有多校适配**：项目从 ycit 生长出来，抽 profile 是纯重构，没有新功能收益。
* **前端无构建步骤**：`app.js` 2,248 行手写、无框架。丑但零工具链，对单文件应用是合理取舍。
* **`utils` 的 `init()` 改 `time.Local`、接管 `log`**：副作用大，但这是"整个程序用 CST、日志落文件"
  的最省事实现。要改就得先把日志初始化显式化。
* **内部 `AccountStore.update()` 不走 `normalize()`、不重建 runtime**（`store.go:420-429`）：
  登录结束回写 `ticketJWT` / `learnedRoutes` 走这条路，建 runtime 会把刚登录的会话丢掉。
  代价是脏值能绕过校验——但这些字段只由 client 产出，可信。
* **`checkTime_windows.go` 的时间锁**：策略代码，不在错误处理范畴。

---

## 6. 旧文档与现实的偏差

`doc/archive/config-redesign.md` 与 `doc/archive/remaining-work-2026-10-25.md` 写于分层重构过程中，
已在文首标注归档。其中相当一部分已经过期，本次整理时逐条核对：

| 旧文档的说法 | 实际情况 |
|---|---|
| 路径全部写成 `client/`（`client/config`、`client/login.go`） | 包已改名 `protocol/`（P1 重命名） |
| `remaining-work.md` 基线 `c8cba92`，把整套 CLI（`XK`/`JL`/`Boom`/`Other`）列为待迁 | `Other` 菜单曾迁入 `tui`；2026-10-28 TUI 与 `CourseCtrl` / `CourseUtil` / `XK` / `JL` / `Boom` 一并删除 |
| `fmt.Print*` 544、`UserInputWithSigInt` 16、`signal.Notify` 8、`time.Sleep` 89 | 现在 224 / 0 / 0 / 69 |
| "`go test -race` 仍报一处既有 race（`utils/logger.go` 的 `time.Local` vs `check_code`）" | **现在 `-race ./...` 全绿**；`check_code` 也补了并发用例，初始化竞争有测试兜底 |
| 分层方向 `tui -> engine -> protocol` | 现状 `main -> web -> engine -> protocol`；`tui` 已删除 |
| "`.idea/` 未跟踪" | `.idea/` 有 6 个文件被跟踪 |
| "`core.autocrlf=true`" | 当前是 `input` |
| `config-redesign.md` 的 Step 1–4 | 已完成（默认值收敛 / 会话剥离 / 删 `Data` / 错误收口） |
| Step 5（`version` 字段、`RejectUnknownMembers`、密码掩码、secrets 分离） | **未做**，转入 [roadmap](roadmap.md) |
