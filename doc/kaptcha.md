# 字符验证码自动识别

> 状态：已实现并内置（`2.0.0`）。模型可选，缺失时行为与以前一致（弹窗人工输入）。
> 相关代码：`check_code/kaptcha/`（模型、识别、配置）、`protocol/kaptcha.go`（回退策略）、
> `protocol/login.go`（取图处）、`web/handlers_kaptcha.go`（配置接口）、`web/static/`（配置页）。

---

## 1. 它解决什么

正方教务的登录页有两种验证码：

| 类型 | 端点 | 形状 | 谁识别 |
|---|---|---|---|
| 滑块 | `/zfcaptchaLogin` | 背景图 + 缺口图 | `check_code`（模板匹配 + 轨迹生成），一直自动 |
| 字符 | `/kaptcha` | 200×50、6 位 `0-9A-Za-z` | **`check_code/kaptcha` + ONNX 模型**（本文），以前是人工输入 |

登录主流程（`protocol/login.go`）判定 `#yzmDiv` 存在就走 `kaptchaLogin` →
`getKaptchaImage` 取图 → 识别 → `postLogin` 提交。识别这一步就是新增的：

```
GET /kaptcha  ──►  kaptcha.Manager.RecognizeKaptcha  ──►  "3f2nn5"  ──►  postLogin(yzm=3f2nn5)
                          │
                          └─ 低置信度 / 报错 / 已关闭 → 跳过，走原来的 InputHandler 弹窗
```

## 2. 组成

| 位置 | 职责 |
|---|---|
| `check_code/kaptcha/kaptcha.go` | `Load` / `LoadBytes` 加载模型；`Recognizer.Recognize` 做门限判断；`ResolveModelPath` 找 `model.onnx` |
| `check_code/kaptcha/manager.go` | `Manager`：读写 `kaptcha.json`、模型加载/热切换、`Status`、`TestRecognize`；实现 `protocol.KaptchaRecognizer` |
| `check_code/kaptcha/internal/onnx/` | 纯 Go 最小 ONNX 解释器（Conv / Relu / MaxPool / Flatten / Gemm，另兼容推理期恒等的 Identity / Dropout），不需要 cgo、CUDA |
| `check_code/kaptcha/internal/imagex/` | 图片解码、PIL 灰度公式、双线性缩放 |
| `protocol/kaptcha.go` | `recognizeKaptcha`：调接口、校验 4~8 位字母数字、上报阶段；连续判错 2 次降级 |
| `protocol/client.go` | `APIClient.Kaptcha KaptchaRecognizer`（接口；协议层不认识具体实现） |
| `engine/engine.go` | `Options.Kaptcha` → `cli.Kaptcha` |
| `web/store.go` | `AccountStore` 创建并持有 `*kaptcha.Manager`；`buildRuntime` 把它交给每个账号的 Engine |
| `web/handlers_kaptcha.go` | 配置接口：读状态、保存、重载、上传模型、测试识别 |
| `web/static/` | 侧栏「⚙️ 服务端配置」→「🧠 验证码模型」子页 |
| `main.go` | 启动时 `store.Kaptcha().Reload()`；失败只打印提示，不阻止 WebUI 启动 |

模型只有一个，进程级加载、所有账号共用；配置是全局的，不在 `accounts.json` 里。

## 3. 模型

`model.onnx`（11,166,990 B）：输入 `1×1×50×200`，输出 6×62 logits，
62 类字符集 `0-9A-Za-z`。整串置信度取 6 个字符 softmax 概率之积。

ONNX 解释器与预处理代码已经并入本仓库，`go.mod` **不再**依赖
`github.com/openschoolcn/kaptcha-onnx`，也没有本地 `replace`。

模型不进仓库（`.gitignore`）。三种提供方式，按优先级：

1. 配置页「模型路径」显式指定的 `.onnx`（不存在 → 报错，不再猜）；
2. 配置页上传：文件保存到 `kaptcha.json` 同目录的 `models/`，上限 64 MB；
3. 自动查找（`ModelPath` 为空时）：先找可执行文件目录、当前工作目录根部的
   `model.onnx`，再找这两个目录下 `models/model.onnx`。

加载时会校验输入维度：`[N,1,50,200]` 中固定维必须匹配；模型损坏、维度不对都只报错，
保留上一份可用模型，不会让进程退出。

## 4. 配置

「验证码模型」是「⚙️ 服务端配置」页的第一个子页（另一个是「✉️ 邮件通知」），
不依赖当前选中的账号。页面包含：

- **当前状态**：是否启用、模型是否加载、路径/大小/修改时间、最近错误；
- **识别配置**：启用开关、模型路径、置信度下限、推理并发（0 = 自动）；
- **替换模型**：上传 `.onnx`，成功后自动保存并启用；
- **测试识别**：上传一张验证码图片，显示识别文本、置信度、是否过阈值。

配置保存在 `kaptcha.json`（与 `accounts.json` 同目录，2 空格缩进，0644，
`.tmp` + `os.Rename` 原子写）。字段：

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `enabled` | bool | `true` | 关闭后不加载模型，遇到字符验证码直接人工输入 |
| `modelPath` | string | 空 | 显式模型路径；空 = 自动查找 |
| `threshold` | number | `0.90` | 整串置信度下限，范围 `(0,1]` |
| `workers` | number | `0` | 推理并发；`0` = 按 CPU 自动（当前识别器内部串行） |

接口（`web/server.go`，实现见 `web/handlers_kaptcha.go`）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/kaptcha` | 当前设置 + 加载状态 |
| PUT | `/api/kaptcha` | 部分更新；校验通过并成功加载后才落盘 |
| POST | `/api/kaptcha/reload` | 按已保存配置重新加载 |
| POST | `/api/kaptcha/model` | multipart 上传模型（`file` 字段，`.onnx`，≤64 MB） |
| POST | `/api/kaptcha/test` | multipart 上传图片（`image` 字段，≤4 MB），只识别不提交 |

保存失败时返回 400/500，旧配置与旧模型保持不变；上传的模型文件即使加载失败也会保留，
方便排查或替换。

## 5. 判定与回退

两类"识别不出来"处理方式不同，别混：

| 情况 | 谁发现 | 处理 |
|---|---|---|
| 置信度 < 门限、图片解不开、结果不像验证码、功能被关闭 | 本地（`check_code/kaptcha`） | 只跳过这一张，**不算失败**，直接弹人工输入 |
| 提交后学校回 `验证码输入错误！` | 服务端（`InputYzmErr`） | `plan.failed()` 记一次；连续 2 次 → 本轮关掉自动识别 |

`protocol` 通过可选接口 `interface{ KaptchaEnabled() bool }` 判断当前是否启用；
`Manager` 实现了它，关闭时不会每张图都产生一条"识别失败"日志。

第二轮判错后 `kaptchaPlan.enabled=false`，之后每张图都直接问人（`protocol/kaptcha.go`）。
这样"模型刚好不认识这所学校的验证码"的代价是 2 次多余提交，而不是无限循环。

阶段文案（`protocol/login_stage.go` + `web/login_stage.go`）：

| Detail | WebUI 显示 |
|---|---|
| `正在自动识别字符验证码` | 正在自动识别验证码 |
| `等待输入字符验证码` | 请填写验证码 |
| `滑块验证码` / `需要滑动验证` | 正在识别滑块验证码 / 需要滑动验证 |

## 6. 实测

`check_code/kaptcha/kaptcha_test.go`（真模型，`data/test` 抽样 300 张，stride 10）：

| 指标 | 数值 |
|---|---|
| 整串全对 | 92.33%（277/300） |
| 门限 0.90 放行 | 278/300 = 92.7% |
| 放行里是对的 | 99.64%（277/278） |

对照 kaptcha-onnx README 的全量结果：62 类 94.87%、kaptcha 默认 20 字符集 98.87%。
单张推理 ~16 ms（Ryzen 7 4800H）。默认门限就取 0.90：约 7% 的图转人工，放行的几乎都对。

> `data/test` 是合成数据，**不等于**你学校的验证码。接入前建议拿真实样本在配置页
> 「测试识别」里验一遍。

推理在 `Recognizer` 内串行（内部 mutex），多账号同时登录会排队；
一张 16 ms，实际影响可以忽略。

## 7. 验证

```powershell
# 单元测试（没有模型时自动 skip，不会误报失败）
go test ./check_code/kaptcha/ -count=1

# 端到端：真模型 + 真 HTTP 图片 + protocol 登录流程，全程不需要人
go test ./check_code/kaptcha/ -run TestAutoKaptchaEndToEnd -count=1 -v

# 回退策略：假正方站 + 假识别器
go test ./protocol/ -run Kaptcha -count=1 -v
```

WebUI 手动验证：启动后打开「⚙️ 服务端配置」→「🧠 验证码模型」，保存一次配置、上传模型、再上传一张验证码
图片看识别结果；然后在账号页登录，确认字符验证码不再弹窗。

## 8. 边界

- **不是万能**：换个字体/换了验证码形态（点选、算术题、行为验证），模型也会失手；
  失手就走 §5 的回退，登录仍然可用。
- **模型不进仓库**：11 MB 的二进制放 `.gitignore`，随发布物一起分发；`kaptcha.json`
  与 `models/` 也不入库。
- **配置是进程级的**：不支持按账号开关；真需要区分就起两个进程（各自的工作目录）。
- **Web 无鉴权**：配置页能改模型路径、上传文件，只应监听 `127.0.0.1`（见
  [configuration.md](configuration.md) §1）。
