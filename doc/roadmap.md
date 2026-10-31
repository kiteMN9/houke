# 待办与施工顺序

> 基线：`webui` 分支 `f336e7a`。问题清单与证据见 [analysis.md](analysis.md)。
> 原则：每一步都能单独编译、单独提交、单独回滚；不改外部契约（`accounts.json`、HTTP 路由）。

---

## 0. 速赢清单（每项 < 1 小时，可一次提交）

| # | 事项 | 位置 | 备注 |
|---|---|---|---|
| Q1 | 恢复密码脱敏 | `web/store.go:97-98`、`115-116`、`218-219`（把注释放开） | 前端表单要同步改成"留空=不改"，见 S-3 |
| Q2 | `accounts.json` 权限 0644 → 0600 | `web/store.go:77` | Windows 上不生效，但零成本 |
| Q3 | `.idea/` 移出版本库 | `git rm -r --cached .idea` + `.gitignore` | 6 个文件 |
| Q4 | `.gitignore` 归并 | 94 行里约 80 行是一次性文件名 | 按 `*.json` 白名单反向排除，或按目录归类 |
| Q5 | 加 `.gitattributes` | `* text=auto eol=lf` | 消除行尾噪音 |
| Q6 | 删 `school_sdk.exe`（40 MB） | 根目录 | 已 ignore |
| Q7 | 删死 API / 死文件 | `protocol.APIClient.ReLogin`、`utils/{markString,today,qrCode,timeCountDown,checkTime,stuNumCheck}*.go` | `checkTime_windows.go` 是策略代码，先确认意图 |
| Q8 | `stuNumCheck` 两个文件一起删 | `stuNumCheck.go` 只有非 Windows 实现，`_windows.go` 只有 build tag | Windows 上这个函数根本不存在，留着是坑 |
| Q9 | `runtime` 错误区分 404 / 500 | `handlers_*.go` 的 `s.store.runtime(id)` | 配置非法应该回 500 带原因，而不是"账号不存在" |
| Q10 | ~~`/login/cancel` 竞态~~ **已修** | `handlers_login.go:97`、`task.go:116`（`startCancelable`） | 取消登记并入任务启动：任务可见即可取消；`web/login_cancel_test.go` 覆盖原子登记 / 幽灵任务 / 取消真正生效三条 |
| Q11 | `doc/*.png`、`html/` 明确去向 | — | 收进 `doc/samples/`（并注明许可）或移出仓库 |

---

## 1. 安全补丁（优先级最高）

现状：`/api` 无鉴权、无 CSRF、密码回显、明文落盘。单机单人时风险可控，但一旦 `-webAddr 0.0.0.0` 就完全敞开。

| 步骤 | 做法 | 验收 |
|---|---|---|
| S-1 | 启动时生成随机 token（或读 `--token`），写进 `http://127.0.0.1:8080/ui/?t=<token>`；API 中间件校验 `Authorization: Bearer` 或 `?t=` | 不带 token 访问 `/api/accounts` 返回 401；带 token 正常 |
| S-2 | 校验 `Origin` / `Sec-Fetch-Site`，只接受同源 | 跨站 POST 被拒 |
| S-3 | 恢复密码脱敏 + 前端"留空不改" | `GET /api/accounts` 响应里没有 `password` / `casPassword` |
| S-4 | 绑定 loopback 时打印醒目提示；非 loopback 必须有 token | 日志能看出当前暴露面 |

> 折中方案（如果不想做 token）：启动时检测监听地址，非 `127.0.0.1` 直接拒绝启动并提示。
> 这比"能绑但不设防"好。

---

## 2. 主线：把 CLI 从 `protocol` 洗出来（TUI 已删除）

> 状态：**已完成大半**（2026-10-28）。删掉了 `tui` 与 protocol 的 CLI 宿主层：
> `controller.go` / `CourseCtrl.go` / `CourseUtil.go` / `other.go` / `keepSession.go` /
> `evaluations.go` 全部移除，`XK` / `JL` / `Boom` 与菜单/打印一起消失；
> 查询类能力改成返回数据，等 WebUI 接页面。

### 2.1 已完成：查询能力的数据化

| 原来 | 现在 | 状态 |
|---|---|---|
| `gpa.go` `getGPA()` 打印 | `GetGPA(ctx) (GPA.Report, error)` + `GPA.Parse` | ✅ |
| `exam.go` `GetExam()` 打印 | `GetExam(ctx, year, term) ([]ExamItem, error)` | ✅ |
| `score.go` `GetScore()` 打印 | 已有 `WebGetScore`，打印版已删 | ✅ |
| `user_info.go` `PrintStudentInfo2` | 已有 `GetJsonInfo`，打印版已删 | ✅ |
| `wakeup.go` 写 CSV 文件 | `SelectedCSV` / `TimetableCSV` 返回带 BOM 的 `[]byte`，落盘交给宿主 | ✅ |
| `login.go` 验证码读 stdin | 没有 `InputHandler` 时直接报错 | ✅ |
| 字符验证码只能人工输入 | 内置 `check_code/kaptcha` 自动识别，低置信度/连续判错回退人工（[kaptcha.md](kaptcha.md)） | ✅ |

顺带删掉的死代码：`GetScoreRaw`、`GetSuggestYearTerm2`、`evaluations.go`、`keepSession.go`、
`utils/{smtp,termcolor,userInputWithSigInt}.go`。`config/excel.go` 的 `ReadExcel` 保留
（愿望清单导入能力），但当前没有调用方。

### 2.2 还剩什么

* `protocol` 非测试代码 44 文件 7,565 行，仍有 **224 行 `fmt.Print*`**（`login.go` 113、
  `CourseAPI.go` 54 最多）与 **69 处 `time.Sleep`**——登录/选课的诊断输出与请求退避，
  是下一节 AST 门禁的前置工作。
* WebUI 还没有考试 / GPA / 个人信息 / 课表 CSV 的页面；协议 API 已就绪，补 handler + 前端即可。
* `XK` / `JL` / `Boom` 的"自动选课/翻页"能力不再有 CLI 实现；要保留请走
  `engine.Choose` + `engine.Plan`，不要再往 protocol 里加打印式循环。

### 2.3 上 AST 门禁

`deps_test.go:10-12` 的注释写了两条规则，等 §2.2 的打印收口后再启用：

1. `protocol` 不得 import `fmt` / `log` / `os`（`os.Stdout`）/ `survey` / `utils/color`；
2. `protocol` 不得出现 `time.Sleep`。

用 `go/ast` 遍历即可，不需要额外依赖。

---

## 3. 多校适配（可选，纯重构）

现状：`ycit` 52 处，盐、CAS 路径、SSO 入口、`TERM` 表、route 池全是字面量。

```
type Profile struct {
    Name       string
    BaseURL    string            // 默认值
    Endpoints  map[string]string // 覆盖 config/endpoints.go 的常量
    Login      LoginKind         // 正方密码 / CAS2 / CAS2+微信
    Salt       string
    Routes     []string
    TermCodes  map[int]string
}
```

* `protocol` 只保留"请求/解析"逻辑，差异全部走 profile；
* `Settings` 增加 `Profile` 字段（空 = 默认）；
* `web/classify.go` 的端点匹配改为基于 `config.Endpoints` 生成，消灭第二份表。

验收：默认 profile 行为与今天完全一致（回归测试）；新增一个 profile 不需要改 `protocol` 代码。

---

## 4. 仓库与工程

| 事项 | 做法 | 验收 |
|---|---|---|
| CI | GitHub Actions：`go build/vet/test` + `go test -race ./...` + `gofmt -l` + `TestDependencyDirection` | PR 上全绿 |
| 文档 | 已有 `README.md` + `doc/`（本次整理） | 新人按 README 能跑起来 |
| `.gitattributes` | `* text=auto eol=lf` | 提交不再提示行尾转换 |
| 样本 | `html/`、`doc/*.png` 移出或收进 `doc/samples/` 并注明"含真实姓名，勿外发" | `git status` 干净 |
| 测试 | 给 `GetExam` / `GetGPA` / `SelectedCSV` / `TimetableCSV` 补解析用例（`GPA.Parse` 已有） | 返回结构体的契约不再漂移 |
| 生命周期 | `Delete` 前先 cancel 该账号的 running task（`store.go:259-261`） | 删账号后不再有 goroutine 写日志 |

---

## 5. 需要真实账号才能验证的（长期挂账）

这些在单元测试里覆盖不到，改动登录/会话相关代码后必须手工过一遍：

- [ ] WebUI：登录 → 重启进程 → 复用 `ticketJWT` 免登录
- [ ] WebUI：故意输错密码 → 前端提交新密码 → `accounts.json` 里密码已更新
- [ ] WebUI：两个账号的 `learnedRoutes` 互不串
- [ ] WebUI：账号 A 登录时账号 B 也登录，互不阻塞（`loginCoord` 是 per-client）
- [ ] WebUI：自动选课跑 30 分钟以上，撞到一次"加密串错误"能自愈继续
- [ ] 滑块验证码：连续 10 次登录的成功率（内置模板的 `+70` 偏移已由 `TestFindBestMatch` 覆盖，这里验证真实服务）

---

## 6. 已决定不做

| 事项 | 理由 |
|---|---|
| 把 `XK` / `JL` / `Boom` 迁到别的宿主 | TUI 已删，CLI 版随之删除；WebUI 的 `POST /choose` + 自动选课 `Plan` 是等价能力，不再保留打印式循环 |
| `Boom`（多线程野蛮选课）上 web | 随 TUI 删除，不再保留 |
| `stress` | 历史遗留，已删除（`363ff71`） |
| 给 `accounts.json` 加密 | 密钥没地方放，收益是心理上的；真正的解法是"少存密码、多用 JWT" |
| 一把梭重构 | `JL` 与课程集合分开提交，保证每步可回滚 |

---

## 附录 A：验证命令

```powershell
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./...
go test . -run TestDependencyDirection
gofmt -l protocol engine web utils check_code kaptcha main.go deps_test.go
```

## 附录 B：相关提交（近期）

```
8503a65 加密串错误
46c805e auto choose course
ed375ac ProtocolHook
5b5bd25 PR3: 课程集合归 tui, 初始化搬 tui, protocol.Controller 瘦身, 打印可注入
df63968 PR1: 合并语义收成两个纯函数，锁只留在 engine 会话缓存一处
d51ceb3 rename（client -> protocol）
c8cba92 refactor(tui): 已选查询与退课搬到 tui
a1e2d35 refactor: 会话状态从 protocol.Session 迁到 engine
11211c6 refactor(protocol): 会话失效改为 ErrUnauthorized，engine 统一重登重试
bca0161 架构重构 P1 P2（改名 + engine 门面）
```
