# school_sdk_go

正方教务系统（zfsoft / jwglxt）选课工具。一套协议实现（`protocol`）+ 一个 WebUI 宿主（`web`），
登录、初始化、选课、自动选课与查询能力都在协议层，宿主只负责交互与展示。

> 版本：`2.0.0`（`go run . -V`）· Go 1.27 · Windows 为主（`utils/` 有少量 `_windows.go`）
>
> 本项目只用于个人账号的辅助操作，请自行确认符合所在学校的使用规定。

---

## 关于本项目

- **从 TUI 版本改过来**：前身是终端菜单式交互——WebUI 从 2026-10-04 开始加，现在完全删除tui部分，
  只剩 `web` 一个宿主。
- **由 DeepSeek Harness 辅助开发**：当前 WebUI 版本的迁移与后续迭代（WebUI、自动选课、验证码识别、
  测试与文档）由 DeepSeek Harness 的 agent 会话辅助完成，过程对应 `git log` 里的逐个提交。
- **分层约定没有变**：`main -> web -> engine -> protocol`，`protocol` 不认识任何宿主（见 §5）。

---

## 1. 能做什么

| 能力 | WebUI | 说明 |
|---|---|---|
| 账号管理（多账号） | ✅ | 增删改查，保存在 `accounts.json` |
| 登录：账号密码 + 验证码 | ✅ | 滑块验证码自动识别（`check_code`）；字符验证码用内置 ONNX 模型识别（`check_code/kaptcha`，可选，见 §2.4），识别不出来才转人工输入 |
| 登录：CAS2 统一身份认证 / 微信扫码 | ✅ | 侧栏「微信扫码登录」不用先建账号：扫码成功后按 ticket 里的身份自动绑定或新建账号 |
| 选课 | ✅ | 单门提交 |
| 自动选课（愿望清单） | ✅ | 候选池 + 定时刷新 + 参数过期自愈 |
| 课程列表 / 教学班详情 | ✅ | 内存缓存 + 增量合并 |
| 已选课程 / 退课 | ✅ | |
| 成绩 / 页签切换 / 会话探测 | ✅ | |
| 个人信息 / 大头照 / Cookie / 会话诊断 | ✅ | 「其他工具」页签；个人信息和大头照要求已登录，诊断不返回密码或 JWT 明文 |
| 考试、GPA、课表 CSV | 待接入 | 协议层已提供结构化 API（`GetExam` / `GetGPA` / `SelectedCSV` / `TimetableCSV`），WebUI 还没有对应页面 |

---

## 2. 快速开始

### 2.1 直接运行

```powershell
go run .                                  # WebUI，默认 http://127.0.0.1:8080 （打开 /ui/）
go run . -webAddr 0.0.0.0:9000            # 换监听地址（注意：Web 无鉴权，见 §6）
go run . -V                               # 只打印版本号
```

首次运行时 `accounts.json` 不存在也**不会报错**：WebUI 里点侧栏「微信扫码登录」扫一下
就会按身份建好账号，或者用「新建账号」手动填。

### 2.2 构建

```powershell
.\build_amd64.bat     # go build -ldflags="-s -w" -trimpath -> school_sdk_go_amd64.exe
.\build_i386.bat      # 32 位
```

两个 `.bat` 与 `run.bat` 都是开发期的手工脚本，`.gitignore` 里已忽略（本地保留）。

### 2.3 验证基线

```powershell
go build ./...
go vet ./...
go test ./...
go test -race ./...
go test . -run TestDependencyDirection     # 架构门禁
```

### 2.4 字符验证码自动识别（可选）

有的学校（如 ycit）登录页弹的是 6 位字符验证码（`/kaptcha` 那张 200×50 的图）。
这里用内置的纯 Go ONNX 推理离线识别，**不需要手动输入**；模型缺失或关闭时退回弹窗人工输入。

启动后打开 WebUI 侧栏「🧠 验证码模型」：

1. 上传 `model.onnx`，或填一个已有模型路径；
2. 按需调整置信度下限、推理并发；
3. 点「保存」，再传一张验证码图片试识别。

配置保存在进程工作目录的 `kaptcha.json`，上传的模型在 `models/`。也可以直接把
`model.onnx` 放在可执行文件目录或工作目录，启动时会自动找（先找两个目录根部的
`model.onnx`，再找各自 `models/` 下的）。启动日志会说明加载成功、没找到还是已关闭。

模型只有一个，所有账号共用（`web.AccountStore` 持有 `*kaptcha.Manager`）；
`protocol` 通过 `KaptchaRecognizer` 接口拿识别结果，不认识具体实现。
回退规则：同一轮登录里自动识别**连续被判错 2 次**就关掉自动识别、本轮改人工输入
（`protocol/kaptcha.go`）；单张图置信度不够只影响这一次，不算失败。
细节与实测准确率见 [doc/kaptcha.md](doc/kaptcha.md)。

---

## 3. 目录结构

```
.
├── main.go                 # 唯一入口：起 WebUI
├── deps_test.go            # 架构门禁（import 方向），不是业务测试
├── protocol/               # 协议层：发请求 + 解析 + 登录状态机（不含宿主交互）
│   ├── config/             #   Settings / Session / 默认值 / baseURL 归一化 / 端点常量
│   ├── cas2/               #   CAS2 门户登录、微信扫码、RSA 加密
│   ├── rsa/  hedge/  internal/  GPA/
│   ├── CourseAPI.go        #   选课相关请求与解析（列表、详情、选课、退课、已选）
│   ├── CourseParsing.go    #   课表参数与选课控制菜单的解析
│   ├── login.go            #   登录主流程（滑块 / 字符验证码 / 无验证码三条分支）
│   └── exam.go  gpa.go  user_info.go  wakeup.go   # 查询类能力，一律返回结构体
├── engine/                 # 选课引擎：会话状态、初始化、重登重试、自动选课 Plan
├── web/                    # WebUI 宿主：REST + SSE + 内嵌前端（web/static → /ui/）
├── utils/                  # 日志、颜色、时间等公共部件
├── check_code/             # 验证码识别：滑块模板匹配 + 字符 kaptcha（模型、配置、ONNX 解释器）
├── doc/                    # 文档，见下
└── userscript/             # 浏览器油猴脚本：滑块验证码纯 HTTP 通过
```

分层方向（`deps_test.go` 强制）：

```
main ──► web ──► engine ──► protocol ──► {config, cas2, hedge, internal, check_code, utils}
  │        └──────────────► check_code/kaptcha
  └───────────────────────► check_code/kaptcha
```

`web` 是唯一的宿主，只有 `main` 能装配它；`protocol` 不认识任何宿主。
`check_code/kaptcha` 只依赖标准库和包内 `internal/{onnx,imagex}`，没有第三方依赖；
它的 `Manager` 由 `web.AccountStore` 创建，`protocol` 只认 `KaptchaRecognizer` 接口。

---

## 4. 文档

| 文档 | 内容 |
|---|---|
| [doc/README.md](doc/README.md) | 文档索引与阅读路线 |
| [doc/architecture.md](doc/architecture.md) | 分层、依赖方向、一次选课的数据流、并发模型 |
| [doc/configuration.md](doc/configuration.md) | 命令行参数、`accounts.json` 字段、运行时文件、默认值 |
| [doc/kaptcha.md](doc/kaptcha.md) | 字符验证码自动识别：模型放置/上传、WebUI 配置页、置信度门限、回退规则、实测准确率 |
| [doc/http-api.md](doc/http-api.md) | Web API 全表 + SSE 事件 |
| [doc/analysis.md](doc/analysis.md) | 项目现状分析：模块职责、量化指标、问题清单（带 `file:line`） |
| [doc/roadmap.md](doc/roadmap.md) | 待办与建议施工顺序 |
| [doc/archive/README.md](doc/archive/README.md) | 历史设计与旧清单（仅存档，路径可能是重构前的旧包名） |
| [userscript/README.md](userscript/README.md) | 油猴脚本说明 |

---

## 5. 开发约定

1. **方向不能反**：`protocol` 不认识 `engine/web`，`engine` 不认识 `web`。
   违反时 `go test . -run TestDependencyDirection` 会失败。
2. **库代码不决定进程生死**：`protocol`/`engine` 里不出现 `log.Fatal` / `os.Exit`（例外见
   `utils/checkTime_windows.go` 的时间锁策略）；退出与 HTTP 状态码由 `main` / handler 决定。
3. **配置只有一份默认值**：`protocol/config/defaults.go` 的 `Default()`。非测试代码里不再出现
   字面量 `"43s"` / `"21s"` / 默认 UA。
4. **会话产物与用户配置分开**：用户设置进 `config.Settings`，JWT / 学到的 route / 修正过的地址进
   `config.Session`；协议客户端（`APIClient`）只改内存，落盘由 `web.AccountStore` 负责。
5. **改完跑一遍** §2.3；提交前 `gofmt -l` 应无输出。

---

## 6. 安全提示（务必阅读）

- `accounts.json` 里是**明文密码 + CAS 密码 + JWT**。
  文件在 `.gitignore` 里，提交前确认 `git status` 干净。
- WebUI **没有认证、没有 CSRF 防护**，所有 `/api` 默认人人可用；默认只监听 `127.0.0.1` 是唯一缓解。
  如需局域网访问，先看 [roadmap 的鉴权条目](doc/roadmap.md#1-安全补丁优先级最高)。
- 前端拿到的账号详情**包含密码**（脱敏代码被注释掉了），只在本机单人使用时可以接受。
- 仓库里的 `html/`、`账号集/`、`功能展示/`、`doc/*.png` 是抓包/历史样本与截图，含真实姓名与账号信息，
  未纳管，不建议外发。
