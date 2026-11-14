# AGENTS.md

> 本文件是给**在本仓库里工作的 AI agent（以及人类贡献者）**的工作约定。
> 产品介绍看 [README.md](README.md)；本文件只讲「怎么改、别踩什么坑」。
> 本文件与代码/测试冲突时，以代码和测试为准，并顺手把本文件修对。
> 快照：2026-11-11，HEAD `5fea063`（v2.2.0 之后），分支 `webui`。版本号与行号会漂移，别当契约。

## 0. 项目速览

`school_sdk_go`：正方教务（zfsoft/jwglxt）辅助选课工具。Go 1.27（go.mod 1.27.3），单仓库、单进程。

```
main       组装进程：flag、信号、生命周期、优雅退出
 └─ web    HTTP/SSE 宿主：账号管理、长任务、静态前端（唯一宿主）
     └─ engine  会话编排：登录/初始化/自动选课、一次重新登录重试
         └─ protocol  协议实现：发请求、解析、返回数据；不认识宿主
```

- 唯一宿主是 WebUI（`web/`）；TUI 已于 2026-10-28 删除，**不要再加回**。
- 外部依赖只有 `go.mod` 里那些；`check_code/` 与 `check_code/kaptcha/` 保持纯 Go（不新增第三方、不用 cgo）。
- 前端是手写 HTML/CSS/JS，无构建步骤，`web/server.go` 用 `//go:embed static/*` 嵌入二进制。

## 1. 三分钟上手

Windows 上用 PowerShell；命令本身跨平台。

| 目的 | 命令 |
| --- | --- |
| 编译 | `go build ./...` |
| 跑起来 | `go run .`（默认 http://127.0.0.1:8080 ，界面在 `/ui/`） |
| 只看版本 | `go run . -V` |
| 静态检查 | `go vet ./...` |
| 全部测试 | `go test ./... -count=1`（含 jwmock 集成测试，jwmock 本身约 34s） |
| 竞态测试 | `go test -race ./...`（本机约 2–3 分钟，改并发相关代码必跑） |
| 架构门禁 | `go test . -run TestDependencyDirection` |
| 格式检查 | `gofmt -l .`（见 §10 的已知例外） |
| 交叉编译 | CI 用 `GOOS/GOARCH` 构建 5 个产物（windows amd64/386、linux amd64、darwin amd64/arm64）；本地想构建 Windows 可参考被 .gitignore 的 `build_amd64.bat` |

**没有真实教务账号也能联调**（jwmock 本地模拟教务）：

```powershell
# 终端 1：起 mock，默认 127.0.0.1:8099，路径 /jwglxt
go run ./jwmock/cmd/mockjw -config ./jwmock/config.example.json

# 终端 2：起 houke
go run .
```

在 WebUI 里新建账号：BaseURL 填 `http://127.0.0.1:8099/jwglxt/`，账号 `20250001` / 密码 `test123`。
jwmock 还能通过 `/__mock/scenario` 注入窗口关闭、容量满、拒绝、参数过期、5xx 等故障，详见 [jwmock/README.md](jwmock/README.md)。
**不要**把真实账号、真实姓名学号、真实选课数据写进 mock 配置、测试、文档或提交记录。

## 2. 仓库地图：先找对文件

| 包 | 职责 | 入口/关键文件 |
| --- | --- | --- |
| 根 `main` | 进程装配、flag、Ctrl+C 优雅退出 | `main.go`、`deps_test.go`（架构门禁） |
| `web` | gin 路由、SSE、账号与长任务、静态前端 | `server.go`（路由全在 `registerRoutes`）、`handlers_*.go`、`runtime.go`、`task.go`、`store.go`、`api_error.go`、`static/` |
| `engine` | 会话状态机：登录、初始化、自动选课计划 | `engine.go`、`session.go`、`plan.go`、`candidates.go` |
| `protocol` | 教务协议：请求、解析、页面流程、错误哨兵 | `client.go`、`login.go`、`Course*.go`、`errors.go`、`wish.go`、`config/` |
| `protocol/config` | 配置类型、默认值、端点常量、会话类型 | `defaults.go`、`endpoints.go`、`config.go`、`session.go` |
| `protocol/cas2`、`protocol/rsa`、`protocol/hedge` | CAS 登录、RSA、hedge 请求 | 各自子包 |
| `check_code`、`check_code/kaptcha` | 滑块验证码匹配/轨迹、ONNX 验证码识别 | `matcher.go`、`gen_track.go`、`kaptcha/manager.go` |
| `utils` | 日志、SMTP、时间锁、颜色等 | `logger.go`、`smtp.go`、`checkTime_windows.go` |
| `jwmock` | 本地正方教务 mock + HAR 转换工具 | `jwmock/README.md`、`config.example.json`、`tools/har2config.py` |
| `userscript` | Tampermonkey 滑块脚本（独立交付） | `userscript/README.md` |

文档地图：

| 文档 | 什么时候看 |
| --- | --- |
| [README.md](README.md) | 功能、运行方式、安全提示 |
| [doc/architecture.md](doc/architecture.md) | 分层/依赖方向、类型归属、并发模型、扩展端口（Ask/OnStage/OnInitStage/OnRequest） |
| [doc/configuration.md](doc/configuration.md) | 命令行、`accounts.json`/`server.json` 等运行时文件、默认值来源 |
| [doc/http-api.md](doc/http-api.md) | 所有 HTTP 路由、错误码、SSE 事件、前端约定 |
| [doc/analysis.md](doc/analysis.md) | 当前债务与已知问题（数字为基线时实测） |
| [doc/roadmap.md](doc/roadmap.md) | 计划、验收标准、手工测试清单（需要真实账号） |
| [CHANGELOG.md](CHANGELOG.md) | 每个版本改了什么；发版说明从这里提取 |

## 3. 不可违反的硬约束

1. **依赖方向不能反转**：`main → web → engine → protocol`，`protocol` 不能 import `engine`/`web`；除根包外任何包不能 import `web`；`web`/`engine`/`protocol` 不能 import 根包。由 `deps_test.go` 的 `TestDependencyDirection` 强制执行。
2. **库代码不决定进程生死**：`protocol`/`engine`/`check_code` 里不出现 `panic`、`log.Fatal`、`os.Exit`；错误通过 `error` 往上返回。唯一例外是 `utils/checkTime_windows.go` 里已有的时间锁策略（历史原因，别扩散）。
3. **配置默认值只有一个来源**：`protocol/config/defaults.go` 的 `Default()` 与相关常量。非测试代码不要写死 `"43s"`、`"21s"`、默认 UA、默认 BaseURL 等字面量。
4. **`Settings` 与 `Session` 分离**：`config.Settings` 是用户配置（值拷贝，client 只读）；`config.Session` 是登录产生的会话工件（client 只写内存，由上层快照后持久化）。`protocol` 的 client 不持久化会话、不读 stdin、不决定进程退出（唯一的诊断输出是登录失败样本 `fail/`，见 §8）。
5. **账号配置的唯一持久化点是 `web.AccountStore`**，用户配置到 `Settings` 的唯一映射点是 `web/account.go` 的 `normalize()` / `toSettings()`；反向是 `session()` / `applySession()`。不要在别处平铺 `Account` 字段或另写容器。
6. **外部契约不轻改**：`accounts.json` 字段、HTTP 路由、错误响应里的 `code` 取值（`unauthorized`、`login_failed`、`no_permission`、`busy`、`session_reset`、`session_changed`、`cancelled`、`timeout`、`internal`）、`POST /api/accounts/:id/auto-choose` 的 202 异步语义。要改先看 `doc/http-api.md` 并同步文档。
7. **不要恢复已删除的宿主/形态**：TUI 已删；`protocol` 不再承载 CLI 控制流（stdin、交互式提示、sig/CLI 退出）。需要新能力时，先想清楚它属于 `protocol`（协议）还是 `web`（宿主）。

## 4. 写代码的约定

- **语言**：注释、文档、用户可见文案用中文；标识符、包名用英文。注释写「为什么」而不是复述代码。
- **错误**：用 `fmt.Errorf("...: %w", err)` 包装；`protocol.ErrUnauthorized`/`protocol.ErrNoPermission` 用 `errors.Is` 判断；需要给用户看的中文原因用 `protocol.StageError{Op, Message, Err}`。不要用错误字符串做控制流。
- **HTTP 错误**：`web` 的 handler 统一走 `web/api_error.go` 的 `writeAPIError`/`writeEngineError`/`writeUnauthorized`；401 必须带 `code`，避免把「密码错误」显示成「登录已失效」。
- **日志**：宿主（`main`、`web`）用 `log.Printf`（`utils/logger.go` 已接管，写入 `logs/`）；`protocol`/`engine` 新代码不要新增打印，需要信息就返回数据或错误。protocol 里现有约 224 处 `fmt.Print*`（基线实测）是待清理债务（见 `doc/roadmap.md` §2），不要模仿。
- **并发**：持锁不做 IO；共享快照在锁内拷贝、锁外使用；`initMu` 负责串行化初始化/续期，`mu` 保护已发布的 `APIConfig`，`cacheMu` 保护课程缓存；单账号同一时间只允许一个长任务（`web/task.go` 的 `start` 会拒绝第二个）。新增状态时先对照 `doc/architecture.md` 的并发表，别引入第二个真相源。
- **持久化**：写文件用「临时文件 + `os.Rename`」原子替换；含密码/密钥的配置文件权限 `0600`；路径相对进程工作目录。新文件先加进 `.gitignore`。
- **依赖**：新增第三方依赖要有理由，并接受纯 Go/no-cgo 约束；`check_code`、`check_code/kaptcha` 的「纯 Go」是特性，不是偶然。
- **前端**：直接改 `web/static/`，没有打包步骤；所有服务器数据进 DOM 前用 `esc()` 转义，避免 XSS；统一走 `API.req()` 处理 401/错误码；改完重启进程（`go:embed` 在编译期嵌入，浏览器强刷）。
- **改滑块/验证码算法**：`check_code` 与 `userscript/*.user.js` 是两份实现（一份 Go、一份 JS 移植），改匹配/轨迹算法时考虑是否需要同步 userscript。

## 5. 常见任务施工路径

| 任务 | 施工顺序（自底向上） | 别忘了 |
| --- | --- | --- |
| 新增查询能力（考试/课表/GPA 等） | `protocol` 方法 + 解析 + 单测 → `engine` 包装（鉴权/重试） → `web` handler + 路由 → `static` 页面 | `protocol` 已有考试/GPA/课表等方法但 WebUI 还没有页面（成绩页面已有，可作模板）；新路由写进 `doc/http-api.md` |
| 新增配置项 | `protocol/config` 的 `Settings` 字段 + 默认值 + `Validate()` → `web/account.go` 的 `normalize()`/`toSettings()` 映射 → 前端表单 | 更新 `doc/configuration.md` |
| 新增 HTTP 路由 | `web/registerRoutes` 注册 → `handlers_*.go` 实现 → `api_error.go` 统一错误 → SSE/长任务走 `taskStore` | 更新 `doc/http-api.md` 路由表；需要前端就改 `app.js` |
| 新增长任务 | `taskStore.start`/`startCancelable` 登记 → 进度 `emit` → `/api/.../cancel` → `Shutdown` 时被 `cancelAll` 取消 | 长任务不绑 HTTP request context（页面关闭不算取消，这是设计）；但必须可取消、超时有上限 |
| 改登录/会话 | `protocol` 登录分支（密码/CAS/微信）+ 阶段回调 → `engine` 重试一次 → `web` 状态推送 | 会话语义见 `doc/architecture.md`；用 `login_cancel_test.go` 一类测试覆盖 |
| 修 bug | 先写一个能复现的测试（同目录 `_test.go`），再改实现 | 回归测试必须留下 |
| 改端点/URL | `protocol/config/endpoints.go` 常量 | `web/classify.go` 已使用这些常量做阶段映射，不要写死字面量 |

## 6. 测试

- 测试与被测代码同目录：`xxx_test.go`；优先 table-driven + `t.Run`。
- HTTP 用 `httptest.NewServer`/`httptest.NewRecorder`；临时文件用 `t.TempDir()`；测试辅助函数见 `web/testutil_test.go`。
- **禁止依赖真实网络、真实教务账号、真实个人信息**；需要端到端就走 jwmock。
- jwmock 集成测试最重（本机实测约 34s）：`go test ./jwmock/ -v`；不要因为它慢就从 `./...` 里排除。
- 改协议解析、课程合并、登录流程、并发/取消/重试时，务必补相应单测。
- 需要真实账号的手工验证项在 `doc/roadmap.md` §5（登录/重启复用 JWT、错密码、多账号并行、自动选课自愈等），不要用 CI 跑。
- 提交前验证基线：
  ```powershell
  go build ./...
  go vet ./...
  go test ./... -count=1
  go test -race ./...
  go test . -run TestDependencyDirection
  gofmt -l .
  ```
  其中 `gofmt -l` 只应输出 `utils/color/color.go`（见 §10）；若输出了你改过的文件，先格式化再提交。

## 7. 文档与版本

- 改代码时按 [doc/README.md](doc/README.md) 的维护表同步文档：
  - 新增/改 `config.Settings` 字段或默认值 → `doc/configuration.md`
  - 新增/改 HTTP 路由或 SSE 事件 → `doc/http-api.md`
  - 新增/删除包、改变依赖方向 → `doc/architecture.md` 和 `deps_test.go`
  - 完成 roadmap 项 → 在 `doc/roadmap.md` 勾选，并把问题从 `doc/analysis.md` 的问题表里删掉
  - 新增协议扩展端口（Ask/OnStage/OnInitStage/OnRequest 等）→ `doc/architecture.md` §7
- 版本号三处同步：`main.go` 的 `var version`、`README.md` 顶部、`CHANGELOG.md`。打 `v*` tag 会触发 Release workflow（先 vet+test，再交叉构建，发布说明从 CHANGELOG 的 `## [版本]` 段提取；tag 带 `-` 视为 prerelease）。
- 文档写法沿用 [doc/README.md](doc/README.md)：中文、短句、表格优先、结论先行、数字要实测（估算标 `~`）、证据附 `file:line`；不写「以后会」；**永远不要把真实 `accounts.json` 内容贴进文档**。

## 8. 安全与仓库卫生

- WebUI **没有鉴权、没有 CSRF 防护**，默认只监听 `127.0.0.1` 是当前唯一的缓解手段。不要为了「方便」改成监听 `0.0.0.0` 或公网；需要远程访问用 SSH 端口转发。相关加固计划见 `doc/roadmap.md` §1。
- 以下文件/目录**已在 `.gitignore` 中，一律不入库**（别用 `git add -f`）：
  `accounts.json`（明文密码/CAS 密码/JWT）、`server.json`（SMTP 密码）、`SMTP.json`、`kaptcha.json`、`model.onnx`、`models/`、`logs/`、`want.xlsx`、`startTime.json`、`kaptcha.png`/`qrcode.png`/`WXQrcode.png`、`jwmock/real_data/`、`jwmock/config.json`、`.idea/`，以及 `school_sdk*.exe`、`build_*.bat` 等本地构建产物（具体以 `.gitignore` 为准）。
- 登录失败时 `protocol` 会把验证码样本写到工作目录的 `fail/`（`check_code.SaveFile`）；该目录**尚未**被 `.gitignore` 覆盖，提交前检查，不要把样本图或整个目录加进仓库。
- 不要把真实姓名、学号、密码、Cookie/JWT、选课结果写进代码、测试、文档、mock 配置、issue 或 commit message。
- 单实例假设：`accounts.json` 是全量重写、没有跨进程锁。不要并发起两个实例操作同一份账号文件。
- 行尾统一 LF（Windows 上 `core.autocrlf=input`）；不要顺手重排/格式化无关历史文件，保持 diff 最小。

## 9. 提交与协作

- 一次提交只做一个逻辑改动，保证「单独编译、单独提交、单独回滚」（`doc/roadmap.md` 的原则）。
- 提交信息用中文摘要，前缀可选但推荐 `fix:` / `feat:` / `refactor:` / `docs:` / `ui:`，例如：
  - `fix: 修复 401 未带 code 导致密码错误被显示为登录失效`
  - `feat: 自动选课支持按课程号聚合候选`
- 提交前 `git status` / `git diff` 检查：不夹带敏感文件、日志、二进制；不修改与本次无关的文件。
- CI（`.github/workflows/workflow.yml`）在每次 push 跑 `go vet` + `go test` + 5 个产物的交叉构建；**不跑 gofmt 和 -race**，这两项本地自己保证。
- 发版走 tag + `go.yml`；不要在 PR 里手工改 release 产物。

## 10. 已知陷阱 / 现状例外

- `gofmt -l .` 会报 `utils/color/color.go`：历史 CRLF 文件，是当前唯一例外；不要为了让这条命令全绿而重排它（roadmap Q5 计划用 `.gitattributes` 统一处理）。
- README 顶部的版本号会落后（快照时写 `2.1.0`，而 `main.go` 已是 `2.2.0`）；`doc/analysis.md`/`doc/architecture.md` 里的 baseline 行号与数字也会漂移。以 `main.go`、`git log`、实际符号为准，发现明显过时就顺手修。
- `engine/engine.go` 等处的旧注释里还残留 `tui` 字样（历史），不要据此认为 TUI 还在，也不要新增 tui 相关引用。
- `POST /api/accounts/:id/auto-choose` 是**异步任务**（202 + SSE 进度），别改回同步长请求。
- 长任务用 `context.Background` 是设计选择（浏览器断连不取消），但必须能被 `/cancel` 和进程 `Shutdown` 取消；新增长任务若绕过 `taskStore` 会破坏这个不变量。
- 改 `accounts.json` 字段时，至少同步：`web/account.go` 的 `normalize()`/`toSettings()`/`session()`/`applySession()`、`doc/configuration.md`、`doc/http-api.md` 的账号字段。
- 端点的唯一来源是 `protocol/config/endpoints.go`；`web/classify.go` 的阶段文案映射依赖这些常量，不要另写一份。
- 前端静态文件被 `//go:embed` 进二进制：`go run .` 每次启动会重新嵌入；已编译的 exe 不会自动更新。
- `utils/logger.go` 的 `init()` 会把 `time.Local` 设为 CST、接管全局 `log` 输出到 `logs/`；`go test` 下日志默认关闭。不要在库代码里依赖日志副作用。
- `go test ./...` 不需要网络；`./...` 包含 `jwmock` 集成测试，慢是正常的。无测试文件的包：`protocol/hedge`、`protocol/internal`、`utils/color`、`check_code/kaptcha/internal/*` 等。

## 11. 收尾自检清单

完成一次改动前，逐项确认：

- [ ] `go build ./...`、`go vet ./...`、`go test ./... -count=1` 全绿
- [ ] 改了并发/取消/重试/缓存：`go test -race ./...` 全绿
- [ ] 改了包/import：`go test . -run TestDependencyDirection` 通过
- [ ] `gofmt -l .` 没有新增输出（允许 `utils/color/color.go` 这一历史例外）
- [ ] 新行为有对应测试；修 bug 有回归测试
- [ ] 按 §7 同步了对应文档；需要时更新 `CHANGELOG.md`
- [ ] `git status` 干净，没有敏感文件、日志、二进制
- [ ] 本文件里被你改动影响到的条目已更新（过时条目删除，不堆积）

本文件的维护原则：**短、可执行、与代码同步**。新增约定先写进这里再依赖它；发现它和代码不一致，以代码为准并修正本文件。
