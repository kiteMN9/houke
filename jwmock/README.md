# jwmock — 本地正方教务模拟服务

`jwmock` 是 `school_sdk` 的测试替身：它模拟正方教务系统里 houke 真正会走的那部分接口
（登录 / RSA 公钥 / 图形验证码 / 滑块验证码 / 选课首页 / 页签参数 / 课程列表 / 课程详情 /
选课 / 已选 / 退课），不连任何真实教务，所有状态都在内存里。

它解决的是「自动选课出问题时不好复现」：验证码、选课被拒、5xx、限流、会话被踢、
名额分阶段放出，这些在真实教务上要么等不到、要么不可控。用 `jwmock` 可以把它们
变成一条可重复的测试用例。

## 快速开始

```bash
# 在 houke 仓库根目录
go run ./jwmock/cmd/mockjw -addr 127.0.0.1:8099

# 需要复现滑块/字符验证码时
go run ./jwmock/cmd/mockjw -mode slider
go run ./jwmock/cmd/mockjw -mode kaptcha       # 验证码答案默认 1234
```

然后启动 houke，新建账号：

- **BaseURL**：`http://127.0.0.1:8099/jwglxt/`
- **用户名**：`20250001`
- **密码**：`test123`

选课首页、页签、列表、详情、选课、已选、退课都可以直接在 Web UI 里操作。

默认课程目录（同一门课两个班，方便看排序与满员）：

| 教学班 | 课程 | 教师 | 容量 | 初始已选 |
| --- | --- | --- | --- | --- |
| JXB-MATH-01 | 高等数学-01班 | 陈爱华 | 2 | 1 |
| JXB-MATH-02 | 高等数学-02班 | 李强 | 2 | 2（满） |
| JXB-ART-01 | 西方美术鉴赏-01班 | 殷海华 | 30 | 0 |

## 控制面

控制面固定挂在 `/__mock` 下（不受业务前缀影响），用于运行中改场景。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/__mock/state` | 当前状态：登录次数、会话数、已选、场景、最近 200 条请求 |
| GET | `/__mock/requests` | 只取请求流水（排查客户端到底调了什么） |
| POST | `/__mock/scenario` | 整体替换场景（JSON 见下） |
| POST | `/__mock/reset` | 清空会话、已选、流水，场景恢复初始值 |
| POST | `/__mock/revoke` | 把所有会话标记为失效（下一次请求收到 302 kickout=1） |

### 场景示例

```bash
curl -X POST http://127.0.0.1:8099/__mock/scenario \
  -H 'Content-Type: application/json' \
  -d '{
    "rejectAll": "当前不在选课时间内",
    "rejectByJxb": {"DO-JXB-MATH-01": "该教学班与已选课程冲突"},
    "paramExpire": 1,
    "faults": [
      {"pathContains": "zzxkyzbjk_xkBcZy", "status": 429, "count": 1},
      {"pathContains": "zzxkyzbjk_xkBcZy", "status": 500, "count": 2}
    ],
    "enrolledPhases": [
      {"afterMs": 0, "enrolled": {"JXB-MATH-01": 2}},
      {"afterMs": 3000, "enrolled": {"JXB-MATH-01": 0}}
    ]
  }'
```

| 字段 | 作用 |
| --- | --- |
| `loginMode` | `none` / `kaptcha` / `slider`，空表示沿用启动参数 |
| `kaptchaCode` | 字符验证码的正确答案 |
| `rejectAll` | 所有选课请求都返回这条 `flag=0` 文案 |
| `rejectByJxb` | 按教学班覆盖拒绝文案，key 可以是 `jxb_id` 或 `do_jxb_id` |
| `paramExpire` | 触发几次「参数过期」：列表返回不带 `tmpList` 的 `msg/flag`，详情返回 `"0"` |
| `faults` | HTTP 层故障，按请求路径子串匹配，可回状态码或延迟 |
| `enrolledPhases` | 在场景生效后第 N 毫秒把教学班的已选人数改成指定值 |

## 场景与验证点

- **登录验证码**：`kaptcha` 走 `#yzmDiv` + `/kaptcha` 图片，答案由 `kaptchaCode` 决定；
  `slider` 走 `.captcha_wrapper` + `/zfcaptchaLogin`，`refresh` 必须返回 `vs=not_verify`，
  图片是可解码 PNG，`verify` 后返回 `verified`。
- **选课业务拒绝**：`rejectAll` / `rejectByJxb` 直接决定 `ChooseCourse` 的 `flag=0` 文案，
  自动选课会把它写进 `failed` 事件。
- **5xx / 限流**：`faults` 按 HTTP 请求计数。注意客户端对 5xx 会自动重试 3 次，
  所以 `count` 消耗得比业务调用快；429 不会被 resty 自动重试，但选课接口本身有 5 次尝试。
- **会话失效 / 被踢**：`POST /__mock/revoke` 把现有会话全部标记为失效，下一次业务请求会
  收到 `302` 到 `/xtgl/login_slogin.html?kickout=1`；`POST /__mock/reset` 则相当于清空
  所有会话。客户端据此走 `ErrUnauthorized` / 自动重登链路。
- **名额分阶段放出**：只能改 `yxzrs`（列表里的人数），不要改容量 `jxbrl`——详情只在
  第一次拿到 `Do_jxb_id` 之前抓一次，之后容量不会回传；列表每轮刷新都会更新 `yxzrs`。

## 注意

- 不要在 `ChooseCourseIndex` / `ChooseCourseCourseList` 上注入**持续**的 5xx 或超时：
  这两条链路的客户端重试是无限循环，测试会挂住。适合注入故障的是选课、登录、
  公钥、已选、退课这些有次数上限的接口。
- 客户端 HTTP 超时最少 4 秒（`max(Settings.Timeout, 4s)`），模拟超时要用
  `delayMs > 4000`。
- 控制面故意不参与故障注入和请求流水，避免控制请求污染业务计数。

## 测试

```bash
go test ./jwmock/ -v
```

集成测试用 `httptest` + 真实 `engine` 跑通：登录 → 初始化 → 列表/详情 → 自动选课 →
已选 → 退课，并覆盖字符验证码、滑块、业务拒绝、被踢后自动重登、名额分阶段放出、
5xx/429/超时、参数过期恢复和控制面。整套约 20 秒；超时用例可加 `-short` 跳过。
