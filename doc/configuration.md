# 配置与数据文件

> 默认值的唯一出处是 `protocol/config/defaults.go:18-23`；账号字段的唯一映射点是
> `web/account.go` 的 `normalize()` / `toSettings()` / `session()` / `applySession()`。
> 登录方式另外由 `web/runtime.go` 的 `loginKindOf()` 翻译成 `engine.LoginKind`。

---

## 1. 命令行参数（`main.go` 的 flag 定义）

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-V` | `false` | 打印 `school_sdk_go 2.1.0` 后退出 |
| `-webAddr` | `127.0.0.1:8080` | WebUI 监听地址 |

程序只剩一个宿主：启动即拉起 WebUI。账号、教务地址、登录方式、超时等全部来自
`accounts.json`（在 WebUI 里维护），不再有命令行覆盖项。字符验证码的模型路径、
置信度门限、启用开关也不在命令行，统一在 WebUI 的「⚙️ 服务端配置」→「🧠 验证码模型」子页配置，
保存在 `server.json` 的 `kaptcha` 节点（见 §2.2）。

```powershell
go run .                       # WebUI，默认 http://127.0.0.1:8080
go run . -webAddr 0.0.0.0:9000 # 换监听地址
```

### 退出行为

| 操作 | 行为 | 退出码 |
|---|---|---|
| 第一次 Ctrl+C / SIGTERM | 取消运行中的登录 / 初始化 / 自动选课任务，断开 SSE，关闭 listener 并等待在途请求（最多 3 秒），日志落盘后退出 | `0` |
| 第二次 Ctrl+C | 不再等待收尾，刷新日志后立即退出 | `1` |
| 启动失败（端口占用、`accounts.json` 损坏等） | 错误写 stderr 与日志文件，不会先打印「WebUI 已启动」 | `1` |

关闭控制台窗口（Windows 的 CTRL_CLOSE_EVENT）走与第一次 Ctrl+C 相同的优雅退出路径，
但系统只给几秒宽限，超时后进程会被系统直接结束；日志里的最后一行可能因此缺失。

---

## 2. 运行时文件

路径都相对**进程工作目录**（不是二进制所在目录）。

| 文件 | 谁读写 | 缺失时 | 是否入库 |
|---|---|---|---|
| `accounts.json` | `web/store.go`（唯一持久化） | WebUI 正常启动，账号列表为空 | ❌ `.gitignore:83` |
| `want.xlsx` | `protocol/config/excel.go` 的 `ReadExcel` | 读失败时返回错误 | ❌ `.gitignore:4` |
| `logs/*.log` | `utils/logger.go`，按启动时间命名 | 自动建目录 | ❌ `.gitignore:10` |
| `fail/fail_<x>_<ms>.png` | 登录失败时的验证码样本（`protocol/login.go:257`） | 自动建目录 | ❌ `.gitignore:82` |
| `server.json` | `web/server_settings.go`（读一次，保存时重写；含 `smtp` / `kaptcha` 两组） | 用默认设置：邮件通知关闭；若存在旧版 `SMTP.json` / `kaptcha.json` 则只读导入 | ❌ `.gitignore:28` |
| `kaptcha.json` | 旧版 2.2 及之前的验证码配置；当前只读导入，不再写 | 不导入，使用默认设置 | ❌ `.gitignore:23` |
| `SMTP.json` | 旧版 1.x 邮件配置；2.x 只读导入，不再写 | 不导入，使用默认设置 | ❌ `.gitignore:11` |
| `models/` | 配置页上传模型的存放目录（`web/handlers_kaptcha.go`） | 不上传就没有；首次上传自动建 | ❌ `.gitignore:103` |
| `model.onnx` | `check_code/kaptcha` 的 `ResolveModelPath`（只读） | 自动识别不可用，改为人工输入 | ❌ `.gitignore:100` |

> `model.onnx` 可以不放在工作目录：程序先找**可执行文件所在目录**和**工作目录**
> 根部的 `model.onnx`，再找它们各自的 `models/` 子目录；
> 也可以在配置页里指定任意路径或直接上传。见 [kaptcha.md](kaptcha.md) §3。

### 2.1 `kaptcha.json`（旧版验证码配置）

2.2 及之前版本把字符验证码的设置写在这个文件里；当前版本已并入 `server.json`
的 `kaptcha` 节点（见 §2.2）。

启动时如果 `server.json` 不存在、或者存在但没有 `kaptcha` 节点，会从同目录的
`kaptcha.json` **只读导入**一次；用户第一次保存后写入 `server.json`，之后不再读取
这个旧文件。文件损坏时静默使用默认设置，配置页保存一次即可覆盖。

### 2.2 `server.json`（服务端配置）

由 WebUI「⚙️ 服务端配置」的「✉️ 邮件通知」/「🧠 验证码模型」两个子页维护，
保存即热更新，不用重启。文件结构：

```json
{
  "smtp": {
    "enabled": true,
    "host": "smtp.qq.com",
    "port": 587,
    "username": "you@qq.com",
    "from": "you@qq.com",
    "password": "<授权码>",
    "to": ["notify@example.com"],
    "notifyAutoChoose": true
  },
  "kaptcha": {
    "enabled": true,
    "modelPath": "models/model.onnx",
    "threshold": 0.9,
    "workers": 0
  }
}
```

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `smtp.enabled` | bool | `false` | 是否启用邮件通知；关闭后仍保留其他字段 |
| `smtp.host` | string | 空 | SMTP 主机名，不带协议和端口 |
| `smtp.port` | number | `587` | 465 按隐式 TLS，其他端口尝试 STARTTLS |
| `smtp.username` | string | 空 | 登录名；空 = 使用 `from` |
| `smtp.from` | string | 空 | 发件人邮箱，同时用于 SMTP 认证 |
| `smtp.password` | string | 空 | 授权码 / 密码；**只写不读**，`GET /api/server` 只返回 `hasPassword` |
| `smtp.to` | []string | 空 | 收件人；页面按行/逗号/分号输入，后端 trim + 去重 |
| `smtp.notifyAutoChoose` | bool | `true` | 自动选课结果通知的总开关；成功/超时/未选上/登录失效时发给 `to`，用户取消不发。账号还要自己的 `autoNotify=true` 才会发 |
| `kaptcha.enabled` | bool | `true` | 关闭后不加载模型，遇到字符验证码直接人工输入 |
| `kaptcha.modelPath` | string | 空 | 显式模型路径；空 = 按 kaptcha.md §3 的顺序自动查找 |
| `kaptcha.threshold` | number | `0.90` | 整串置信度下限，范围 `(0,1]`；低于它转人工 |
| `kaptcha.workers` | number | `0` | 推理并发，`0` = 自动；范围 `0~64` |

写盘用 `.tmp` + `os.Rename`，并按 `0600` 创建（Windows 上权限位不生效，但仍按最小权限语义写）。
文件损坏时不阻止 WebUI 启动：打印错误并暂用默认设置，页面保存一次即可覆盖。
第一次启动若没有 `server.json`，会只读导入同目录的旧版 `SMTP.json`（`host/port/from/to/password/enable`）
和 `kaptcha.json`；邮件通知页会提示「尚未保存」。点一次保存后以 `server.json` 为准，旧文件不再读写。
验证码设置只有在模型加载成功后才会写进 `server.json`；保存失败时旧配置与旧模型保持不变。

> `want.xlsx` 的解析能力（`config.ReadExcel`）还留在协议层，但当前没有调用方：
> WebUI 的愿望清单存在 `accounts.json` 的 `wantClasses/wantTeachers/wantTypes` 里。

---

## 3. `accounts.json`

格式：顶层就是 `[]Account`，2 空格缩进，**0644**，写盘用 `.tmp` + `os.Rename` 原子替换（`web/store.go:65-81`）。
运行期只在启动时读一次，之后**从不重读**；每次变更全量重写。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `id` | string | 随机 16 hex | 主键，新建时生成 |
| `name` | string | — | 显示名（必填） |
| `baseUrl` | string | `https://jwglxt.ycit.edu.cn/` | 教务根地址，见 §5 |
| `username` | string | — | 学号（必填） |
| `password` | string | — | 教务密码，**明文** |
| `casPassword` | string | — | 统一身份认证密码，**明文**（可与教务密码不同） |
| `loginMode` | string | `password` | 登录方式：`password` / `cas` / `wechat`。空值按 `casLogin` 推导；`wechat` 由「微信扫码登录」入口写入（账号表单不再提供这个选项），登录时弹出二维码（可复制微信官方扫码页链接） |
| `casLogin` | bool | `false` | 旧字段：是否走 CAS2 门户密码登录；由 `loginMode` 反向同步（`loginMode=cas` 时为 `true`） |
| `userAgent` | string | `FireFoxUA` | 必须含 `/`，否则回落默认（见 §6） |
| `timeout` | string | `"43s"` | 单请求超时，Go duration 字符串，< 4s 会在客户端被抬到 4s |
| `loginTimeout` | string | `"10m"` | 登录/初始化长任务的最长等待时间 |
| `autoRelogin` | bool | `true` | 会话失效时是否自动重登一次；关闭后点具体功能遇到失效只提示手动登录（初始化也不会预登录） |
| `autoNotify` | bool | `true` | 自动选课结束时是否给该账号发结果邮件；服务端 `smtp.notifyAutoChoose` 仍是总开关，这里只能再关闭。WebUI 自动选课页可热切换 |
| `hedging` | bool | `false` | 是否启用请求对冲（同一请求延迟后重发） |
| `hedgingDelay` | string | `"21s"` | 对冲间隔，仅 `hedging=true` 时生效 |
| `routes` | []string | `[]` | 用户自定义 `route` cookie 候选；空 = 用内置两条 |
| `learnedRoutes` | []string | 空 | 登录过程中学到的 route（client 回传，不是用户偏好） |
| `ticketJWT` | string | 空 | CAS2 门户 JWT 缓存；**不出前端**（`store.go:99,117`） |
| `identity` | object | 空 | 扫码登录证明的身份：`{sub,name,type,org,orgCode}`。由 ticket 解析得来，是"这个账号是谁"的落盘形式（`ticketJWT` 会过期，身份不会）；再次扫码时按 `sub` + 学校编码匹配到它 |
| `wantClasses` | []string | 空 | 自动选课愿望：课程名（同时比教学班名与课程名，包含匹配；任一项命中即可）。填了课程名时先只抓命中的教学班并优先提交，一门都没命中才回落到整表扫描 |
| `wantTeachers` | []string | 空 | 自动选课愿望：教师。列表接口没有教师字段，只能从课程详情里拿到，所以填了教师时会对当前页签的每门课抓一次详情 |
| `wantTypes` | []string | 空 | 自动选课愿望：课程类型（包含匹配，只看 `kzmc` 课程性质，如「公共艺术」）。填了类型时要求类型命中；同时填了教师则还要求教师命中 |
| `createdAt` / `lastLogin` | time | — | 创建 / 最近登录时间 |

示例（`wantClasses` 与 `password` 都请勿提交到仓库）：

```json
[
  {
    "id": "5d59a447c01d7332",
    "name": "2306",
    "baseUrl": "https://jwglxt.ycit.edu.cn/",
    "username": "230112112006",
    "password": "<明文>",
    "casPassword": "<明文>",
    "loginMode": "cas",
    "casLogin": true,
    "userAgent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:157.0) Gecko/20100101 Firefox/157.0",
    "timeout": "43s",
    "loginTimeout": "10m",
    "autoRelogin": true,
    "autoNotify": true,
    "hedging": false,
    "hedgingDelay": "21s",
    "routes": [],
    "wantClasses": ["定向越野"],
    "createdAt": "2026-10-04T23:32:59.9660436+08:00",
    "lastLogin": "2026-10-22T19:21:26.1130953+08:00"
  }
]
```

### 3.1 `normalize()` 会修什么（`web/account.go:60-87`）

| 情况 | 处理 |
|---|---|
| `timeout` / `hedgingDelay` / `loginTimeout` 非法（空、负数、缺单位） | 回落默认值 |
| `loginMode` 为空 / 非法 | 按 `casLogin` 推导（`true → cas`，否则 `password`），并同步 `casLogin` 镜像；`wechat` 不会被改写成 `casLogin=true` |
| `userAgent` 不含 `/` | 回落 `DefaultUA` |
| `baseUrl` 是登录页 / 带 query | `NormalizeBaseURL` 收敛成 `scheme://host[/prefix]/` |
| 愿望清单有空行、重复、首尾空白 | 去重 + trim；全空则写回 `nil`（JSON 里省略） |
| 缺少 `autoRelogin`（老账号） | 按 `true` 写回；显式 `false` 原样保留 |
| 缺少 `autoNotify`（老账号） | 按 `true` 写回；显式 `false` 原样保留 |

---

## 4. 默认值

| 项 | 值 | 定义处 |
|---|---|---|
| BaseURL | `https://jwglxt.ycit.edu.cn/` | `config/defaults.go:19` |
| 单请求超时 | `43s` | `:20` |
| 对冲间隔 | `21s` | `:21` |
| User-Agent | `FireFoxUA`（Firefox 157 / Win10） | `config/useragent.go:8,13` |
| 登录任务超时 | `10m` | `web/account.go:16` |
| 自动重登 | `true` | `web/account.go`（`Account.autoReloginEnabled`） |
| 自动选课邮件通知 | `true` | `web/account.go`（`Account.autoNotifyEnabled`） |
| 自动选课轮间隔 | `1.5s` | `engine/plan.go:17` |
| 自动选课候选刷新 | `30s` | `engine/plan.go:25` |
| 自动选课超时 | `24h` | `web/handlers_auto.go:47` |
| 选课参数重抓冷静期 | `10s` | `engine/session.go:31` |

**约定**：非测试代码里不允许再出现字面量 `"43s"` / `"21s"` / 默认 UA（`defaults.go:17` 的注释）。
新增默认值时改 `Default()` 一处即可，`normalize()` 和 `buildRuntime` 都从它起步。

---

## 5. baseUrl 规则

**允许**：`http://` / `https://`、任意主机（含端口）、**子路径前缀**（`/jwglxt/`、`/a/b/`）。
resty 的拼接方式是 `TrimRight(baseURL,"/") + "/path"`，所以目录部署天然被支持。

**归一化**（`config.NormalizeBaseURL`）：

| 输入 | 输出 |
|---|---|
| `host` （无协议） | `https://host/` |
| `https://host/jwglxt` | `https://host/jwglxt/` |
| `https://host/jwglxt/xtgl/login_slogin.html` | `https://host/jwglxt/` |
| `https://host/xtgl/index_initMenu.html?x=1` | `https://host/` |

**拒绝**（`config.ValidateBaseURL`，构造 client 时返回错误 → Web 上是 500）：

* 非 http/https；
* 没有主机名；
* 带查询参数或 `#`；
* 路径里含页面地址（`/xtgl/login_slogin.html`、`/xtgl/index_initMenu.html`、`/xsxk/zzxkyzb_cxZzxkYzbIndex.html`）；
* 路径里含 `..`。

---

## 6. User-Agent

`config.CheckUALegal`：非空，且 `strings.Cut(ua, "/")` 的斜杠两侧都有非空白内容。
原因：正方登录的 `extend` 字段取第一个 `/` 之后的部分当 `appVersion`。

内置四款（`config/useragent.go:5-13`）：`EdgeUA`、`ChromeUA`、`FireFoxUA`（默认）、`ApppleUA`。

---

## 7. 会话产物（`config.Session`）

| 字段 | 来源 | 用途 |
|---|---|---|
| `TicketJWT` | CAS2 门户登录成功后写入 | 下次进程启动直接换票，跳过扫码/密码 |
| `LearnedRoutes` | 登录过程中 SSO 302 暴露的 `route` cookie | 下次登录的候选池（用户 `routes` 优先，学到的补后去重） |
| `CorrectedBaseURL` | 302 暴露的真实地址 | 落盘后覆盖 `Account.BaseURL`（下次不用再修一遍） |

它们**不是**用户配置：用户不该编辑，删掉只是下次重新登录。生命周期见
[architecture.md §3](architecture.md#3-核心类型与所有权)。

---

## 8. 环境变量

| 变量 | 值 | 效果 | 位置 |
|---|---|---|---|
| `proxy` | `1` | 走 `http://127.0.0.1:8866`，且 `InsecureSkipVerify` | `protocol/client.go:140` |
| `trace` | `1` | resty 打开 trace 日志 | `protocol/client.go:152` |
| `SCHOOL_SDK_LOG_IN_TEST` | 任意 | `utils/logger.go` 判断"在测试进程里"，跳过日志初始化 | `utils/logger.go:76` |

`proxy` / `trace` 是历史开关，**没有**写进 `Settings`，属于"知道的人用"的调试通道。

---

## 9. 敏感信息

| 位置 | 内容 | 风险 |
|---|---|---|
| `accounts.json` | 教务密码、CAS 密码、JWT 全明文，0644 | 同机任何用户可读 |
| `server.json` | SMTP 授权码全明文，按 0600 写入；API 不回显密码 | Unix 上仅文件所有者可读；Windows 上权限位不生效 |
| `GET /api/accounts*` | 把密码回给前端（脱敏代码被注释） | 本机可用；局域网暴露即泄漏 |
| `html/`、`账号集/`、`doc/*.png` | 抓包样本 / 截图，含真实姓名与学号 | 未纳管，**不要外发** |

全部已在 `.gitignore` 里（除 `html/`、`doc/*.png`，见 [roadmap](roadmap.md#4-仓库与工程)）。
