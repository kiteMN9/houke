# jwmock — 本地正方教务模拟服务

`jwmock` 是 `school_sdk` 的测试替身，专注模拟 houke 自动选课会走的那部分教务接口：
登录 / RSA 公钥 / 选课首页 / 页签参数 / 课程列表 / 课程详情 / 选课 / 已选 / 退课。
它不连真实教务，所有状态都在内存里，通过 `/__mock` 控制面可以在运行中切换
「不在选课时间」「名额已满」「时间冲突」「学分/门数上限」「瞬时失败」「5xx/限流」
「会话被踢」「名额分阶段放出」等场景。

登录只校验账号密码（密码走正方同款 RSA PKCS#1 v1.5 加密），不模拟短信/扫码/验证码。

## 快速开始

```bash
# 在 houke 仓库根目录
go run ./jwmock/cmd/mockjw

# 自定义监听地址 / 业务前缀 / 账号
go run ./jwmock/cmd/mockjw -addr 127.0.0.1:8099 -base /jwglxt -user 20250001 -pass test123
```

然后启动 houke，新建账号：

- **BaseURL**：`http://127.0.0.1:8099/jwglxt/`
- **用户名**：`20250001`
- **密码**：`test123`

选课首页、页签、列表、详情、选课、已选、退课都可以直接在 Web UI 里操作。

默认课程目录（覆盖「有名额」「已满」「多学分」「公选课」四种情况）：

| 教学班 | 课程 | 教师 | 学分 | 上课时间 | 容量 | 初始已选 |
| --- | --- | --- | --- | --- | --- | --- |
| JXB-MATH-01 | 高等数学-01班 | 陈爱华 | 4 | 周一第1-2节 | 2 | 1 |
| JXB-MATH-02 | 高等数学-02班 | 李强 | 4 | 周二第1-2节 | 2 | 2（满） |
| JXB-ENG-01 | 大学英语-01班 | 王芳 | 3 | 周三第3-4节 | 40 | 5 |
| JXB-ART-01 | 西方美术鉴赏-01班 | 殷海华 | 2 | 周五第5-6节 | 30 | 0 |

## 控制面

控制面固定挂在 `/__mock` 下（不受业务前缀影响），用于运行中改场景。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/__mock/state` | 当前状态：登录次数、失败次数、会话/失效数、已选、人数、场景、最近 200 条请求 |
| GET | `/__mock/requests` | 只取请求流水（排查客户端到底调了什么） |
| POST | `/__mock/scenario` | 整体替换场景（JSON 见下） |
| POST | `/__mock/reset` | 清空会话、已选、流水，场景恢复初始值 |
| POST | `/__mock/revoke` | 把所有会话标记为失效（下一次业务请求收到 `302 kickout=1`） |

### 场景示例

```bash
curl -X POST http://127.0.0.1:8099/__mock/scenario \
  -H 'Content-Type: application/json' \
  -d '{
    "rejectAll": "当前不在选课时间内",
    "rejectByJxb": {"DO-JXB-MATH-01": "该教学班与已选课程冲突"},
    "maxCredits": 8,
    "maxCourses": 3,
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
| `windowClosed` | 为 true 时所有提交返回「当前不在选课时间内」 |
| `maxCredits` | >0 时，已选学分 + 本次学分超过它会被拒 |
| `maxCourses` | >0 时，已选门数达到它后再提交会被拒 |
| `rejectAll` | 所有选课请求都返回这条 `flag=0` 文案 |
| `rejectByJxb` | 按教学班覆盖拒绝文案，key 可以是 `JXB-x` 或 `DO-JXB-x` |
| `rejectNext` | 某个教学班接下来 N 次提交返回瞬时失败，之后恢复 |
| `rejectNextMsg` | 瞬时失败的文案，默认「系统繁忙，请稍后再试」 |
| `paramExpire` | 触发几次「参数过期」：列表返回不带 `tmpList` 的 `msg/flag`，详情返回 `"0"` |
| `faults` | HTTP 层故障，按请求路径子串匹配，可回状态码或延迟 |
| `enrolledPhases` | 场景生效后第 N 毫秒把教学班的已选人数改成指定值 |

## 选课判定顺序

`ChooseCourse` 按真实接口的优先级返回 `flag=0` + 文案；顺序固定，便于写断言：

1. 教学班不存在 →「教学班不存在或已失效」
2. `windowClosed` →「当前不在选课时间内」
3. `rejectByJxb` → 配置的文案
4. `rejectAll` → 配置的文案
5. 同教学班已选 →「该教学班已选，不能重复选择！」
6. 同课程（`kch_id`）其他班已选 →「该课程已选，不能重复选择！」
7. 已选人数 ≥ 容量 →「该教学班人数已满，请选择其他教学班！」
8. 与已选课程 `sksj` 相同 →「该教学班与已选课程时间冲突！」
9. 超过 `maxCredits` →「超过学分上限！」
10. 达到 `maxCourses` →「选课门数已达上限！」
11. `rejectNext` 仍有次数 →「系统繁忙，请稍后再试」
12. 否则 `flag=1` +「选课成功」，并把教学班写进该账号的已选集合

已选人数 = 场景阶段基线 + 所有账号成功选课数；列表每轮刷新都会重新计算，详情里的
容量固定为教学班容量（客户端只在第一次拿到 `Do_jxb_id` 前抓详情）。

## 场景与验证点

- **登录**：账号密码错误返回 `#tips`「用户名或密码不正确」，engine 的 `Login` 返回 false；
  成功返回 `302` 到 `/xtgl/index_initMenu.html` 并下发 `JSESSIONID`。
- **选课业务拒绝**：`rejectAll` / `rejectByJxb` / `windowClosed` / `maxCredits` /
  `maxCourses` / 满员 / 重复 / 时间冲突都会进入自动选课的 `failed` 事件。
- **名额分阶段放出**：`enrolledPhases` 只改 `yxzrs`（列表人数），不改容量；先满员失败、
  放出后同一轮刷新即可选上，用来验证 `Refresh` 与满员排序。
- **会话失效 / 被踢**：`POST /__mock/revoke` 后下一次业务请求收到
  `302` 到 `/xtgl/login_slogin.html?kickout=1`，客户端走 `ErrUnauthorized` /
  自动重登链路；`POST /__mock/reset` 则清空所有会话。
- **5xx / 限流**：`faults` 按 HTTP 请求计数。resty 对 5xx 自动重试 3 次，所以
  `count` 消耗得比业务调用快；429 不会被 resty 自动重试，但选课接口本身有 5 次尝试。
- **参数过期**：`paramExpire` 让列表/详情返回参数过期信号，engine 会 `Renew` 后重试。
- **多账号**：每个账号独立已选集合、共享教学班名额，可验证「最后一个名额被抢」。

## 注意

- 不要在 `ChooseCourseIndex` / `ChooseCourseCourseList` 上注入**持续**的 5xx 或超时：
  这两条链路的客户端重试是无限循环，测试会挂住。适合注入故障的是选课、登录、
  公钥、已选、退课这些有次数上限的接口。
- 客户端 HTTP 超时最少 4 秒（`max(Settings.Timeout, 4s)`），模拟超时要用
  `delayMs > 4000`。
- 控制面故意不参与故障注入和请求流水，避免控制请求污染业务计数。
- 业务接口前缀默认 `/jwglxt`，可以用 `-base` 改；控制面始终是 `/__mock`。

## 测试

```bash
go test ./jwmock/ -v
```

集成测试用 `httptest` + 真实 `engine` 跑通「登录 → 初始化 → 列表/详情 → 自动选课 →
已选 → 退课」，并覆盖：满员跳过、名额分阶段放出、选课窗口关闭/全局拒绝/学分上限/
容量满、重复选课、时间冲突、门数上限、瞬时失败重试、多账号抢最后一个名额、
被踢后自动重登、参数过期恢复、5xx/429 重试和控制面。整套约 24 秒。
