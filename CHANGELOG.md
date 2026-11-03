# Changelog

`school_sdk_go` 的版本变更记录。版本号与 Git tag 对应（如 `v2.1.0`），
构建产物文件名也使用该 tag。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

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
