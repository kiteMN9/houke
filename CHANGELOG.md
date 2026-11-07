# Changelog

`school_sdk_go` 的版本变更记录。版本号与 Git tag 对应（如 `v2.1.0`），
构建产物文件名也使用该 tag。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [未发布]

### 修复

- **Ctrl+C 退不掉**：`utils/logger.go` 在包 `init()` 里注册了 `signal.Notify` 刷新日志，副作用是关掉了 Go 默认的 Ctrl+C 终止行为——第一次按键只刷一次日志、进程继续运行，之后再按没人接收，只能关窗口或 `taskkill`。现在退出信号由 `main` 统一处理，日志不再占用信号。
- 端口被占用时不再先打印「WebUI 已启动」再报错：先绑定监听端口，成功后才输出启动地址；启动失败写 stderr 并以退出码 1 结束。
- 启动错误不再用 `log.Fatalf`（异步日志来不及落盘，控制台只剩一行看不懂的错误）：错误同时写 stderr 与日志文件，退出前统一刷新关闭。
- **空白愿望条目会命中所有课程**：`strings.Contains(x, "")` 恒为真，类型或教师里多一个空行就等于没填条件，自动选课会去抢所有课。`protocol.WishList.Match` 现在跳过空白条目（首尾空格也忽略），教师清单只有空白项时按"不限教师"处理，课程类型为空时不再靠反向包含全命中；`WishList.Empty` 同样改为按内容判断。
- **自动选课请求体没清洗愿望清单**：`POST /accounts/:id/auto-choose` 现在和保存路径一样 trim、丢空行、去重，清洗后为空才回落到账号里保存的清单。

### 新增

- **优雅退出**：第一次 Ctrl+C（或 SIGTERM）取消运行中的登录 / 初始化 / 自动选课任务、断开 SSE 长连接、关闭 listener 并等待在途请求（上限 3 秒），日志落盘后以退出码 0 结束；再按一次 Ctrl+C 强制退出（退出码 1）。
- `web.Server` 新增 `Listen` / `Serve(ctx, ln)` / `Shutdown(ctx)`，替换无法关停的 `Run`；`http.Server` 使用服务级 `BaseContext`，退出时 SSE 等长连接会随请求上下文一起结束。
- `web.taskStore.cancelAll()`：退出时把仍在跑的长任务标记为已取消，并向 SSE 推一帧收尾事件。
- `utils.FlushLogger()` / `utils.CloseLogger()`：退出前刷新缓冲并关闭日志文件；删除已废弃的 `utils/exit.go`（双击 Ctrl+C 逻辑，唯一调用点早已被注释）。
- WebUI 自动选课页的「课程类型」示例改成多行（艺术类 / 人文类 / 自然类 / 经管类），愿望清单说明同步写清类型与教师的组合规则。

### 测试

- 新增 `web/shutdown_test.go`（退出取消任务、`Serve` 随 ctx 结束、SSE 不阻塞退出）与 `utils/logger_exit_test.go`（退出前 Flush / Close 落盘且可重复调用）。
- `protocol/wish_test.go` 补空白课程类型 / 教师、教师清单全空白与空 `Kzmc` 的用例；`web/handlers_auto_test.go` 覆盖 `/auto-choose` 请求体清单的 trim、去空行、去重。

## [2.1.0] - 2026-11-03

### 新增

- **服务端配置页**：侧栏新增「⚙️ 服务端配置」，下分「🧠 验证码模型」和「✉️ 邮件通知」两个页签；原来的验证码模型入口移入此页。
- **SMTP 邮件通知**：新增 `server.json` 保存 SMTP 主机、端口、发件人、用户名、密码和收件人；支持 465 端口隐式 TLS，其它端口使用 STARTTLS。密码只写不读，保存时留空表示沿用原密码。
- **自动选课结果通知**：自动选课结束（成功 / 超时 / 未选上 / 失败 / 中断）后按配置发送邮件；用户主动取消不发送。
- **账号级自动重登**：账号新增 `autoRelogin`（默认开启）。关闭后遇到失效会话不再自动登录，直接返回 `unauthorized`，由用户手动点「登录」。
- **账号级自动通知开关**：账号新增 `autoNotify`（默认开启），与服务端 SMTP 总开关、自动选课结果通知开关共同决定是否发信。
- **选课时间窗**：`GET /api/accounts/:id/status` 新增 `window` 字段（开始 / 结束时间），WebUI 账号栏显示结束时间。
- **统一错误协议**：所有 API 错误响应新增 `code` 字段（`unauthorized`、`login_failed`、`no_permission`、`busy`、`session_reset`、`cancelled`、`timeout`、`internal`），并保留原有 `error` 文案；前端统一处理 401。
- **新接口**：
  - `GET /api/server` / `PUT /api/server`
  - `POST /api/server/smtp/test`
  - `PUT /api/accounts/:id/auto-relogin`
  - `PUT /api/accounts/:id/auto-notify`

### 修复

- 自动选课提交时若会话已失效，会按 `autoRelogin` 设置重登并重试，不再把「未登录」误判为选课失败。
- 成绩查询、个人信息、头像、页签切换等请求遇到 `ErrUnauthorized` 时自动重登并重试一次，避免因会话过期直接失败。
- 旧会话的迟到响应不再把刚重新登录的状态误标为未登录（基于 `lastLoginSuccess` 的时间戳守卫）。
- 课程详情中的学分 `XF` 现在会合并回课程列表。
- 初始化阶段命中未登录时，按账号开关决定「自动重登」还是提示手动登录；重登失败会返回明确的 `login_failed`。
- 选课控制菜单参数解析失败时，错误信息明确为 `Step 2 tab params failed`。

### 重构

- 选课相关解析逻辑从 `protocol/CourseAPI.go` 拆到 `protocol/CourseParsing.go`。
- 页签相关的隐藏参数从 `IndexInfo` 移到 `SelectionWindow`；`ChooseCourse` 改为 `(ctx, idx, win, co)`，由调用方显式传入时间窗。
- 会话失效判断集中到 `protocol/loginState.go` 的 `markSessionExpired`，覆盖登录页跳转、`#tips` 提示、账号不存在等失效响应。
- API 错误输出集中到 `web/api_error.go` 的 `writeAPIError` / `writeEngineError`，不再在各 handler 里拼 `gin.H{"error": ...}`。
- 新增 `web/server_settings.go` 管理 `server.json`：原子写入（临时文件 + rename，权限 `0600`），首次启动只读导入旧版 `SMTP.json` 后不再读取。
- SMTP 发送能力落到 `utils/smtp.go` 的 `SendSMTP`，`web` 层只负责配置和触发。

### 测试

- 新增 `web` 服务端配置、自动通知、状态时间窗、账号开关、统一错误协议等测试。
- 新增 `utils.SendSMTP`、`protocol` 配置视图、`engine` 自动重登关闭、会话时间窗等测试。
- `go build ./...`、`go vet ./...` 通过。

### 升级说明

- 旧账号不需要迁移：`autoRelogin`、`autoNotify` 缺省均按 `true` 处理。
- 旧版 `SMTP.json` 会在首次启动时导入 `server.json`，原文件保留不动；之后只维护 `server.json`。
- 作为库使用 `protocol` 时注意 `ChooseCourse` 签名变化，页签参数改由 `SelectionWindow` 传入。
- Release 工作流在打 tag 后先执行 `go vet ./...` 和 `go test ./...`，构建完成后从本文件提取对应版本段落作为 Release 说明。
- tag 名包含 `-`（例如 `v2.2.0-rc.1`）会自动标记为预发布。

## [2.0.0] - 2026-11-01

- WebUI 成为唯一宿主，移除 TUI 菜单；分层固定为 `main → web → engine → protocol`。
- 整理 endpoint 与依赖版本；补充微信扫码登录的 `Location` 处理。

## 更早版本

2.0.0 之前的版本没有维护变更日志，历史见 `git log` 与 `git tag`。
