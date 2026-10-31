# 文档索引

`school_sdk_go` 的文档按「读者要做什么」组织，而不是按时间堆砌。
根目录 [README.md](../README.md) 是项目门面（是什么、怎么跑），这里是从门面进来的地图。

---

## 文档地图

| 文档 | 内容 | 主要读者 |
|---|---|---|
| [../README.md](../README.md) | 项目定位、能力矩阵、快速开始、目录结构、开发约定、安全提示 | 所有人，第一站 |
| [architecture.md](architecture.md) | 分层与依赖方向、各包职责、核心类型所有权、一次选课的数据流、并发模型、扩展点 | 要改代码的人 |
| [configuration.md](configuration.md) | 命令行参数、运行时文件、`accounts.json` 全字段、默认值、baseURL/UA 规则、环境变量 | 要部署或排查配置的人 |
| [http-api.md](http-api.md) | Web API 全表、SSE 帧协议与事件类型、自动选课语义、前端结构 | 写前端 / 调接口的人 |
| [wechat-login-webui.md](wechat-login-webui.md) | 微信扫码登录协议状态机与 Web 呈现（TUI 部分已删除，仅供参考） | 要接扫码登录或排查二维码问题的人 |
| [kaptcha.md](kaptcha.md) | 字符验证码自动识别：模型放置/上传、WebUI 配置页、置信度门限、回退规则、实测准确率 | 要部署或换成自己模型的人 |
| [analysis.md](analysis.md) | 现状体检：规模、做对了什么、问题清单（P0/P1/P2，带 `file:line`）、债务量化 | 接手或评审项目的人 |
| [roadmap.md](roadmap.md) | 速赢清单、安全补丁、protocol 剩余打印的收口计划、多校适配、CI | 要动手改的人 |

## 历史文档（已归档，仅供追溯）

| 文档 | 说明 |
|---|---|
| [archive/config-redesign.md](archive/config-redesign.md) | 配置层重构方案 + Step 1–4 实施记录。**文中 `client/` 即现在的 `protocol/`** |
| [archive/remaining-work-2026-10-25.md](archive/remaining-work-2026-10-25.md) | 以 `c8cba92` 为基线的待办清单，条目大量已完成、计数已过期 |
| [archive/README.md](archive/README.md) | 归档约定与替代关系 |

历史文档里的结论**不要直接当现状用**：两份都在文首加了归档横幅，仍有价值的部分已并入
[analysis.md](analysis.md) 与 [roadmap.md](roadmap.md)。

---

## 阅读路线

**只想跑起来** → 根 [README.md](../README.md) §2 快速开始 → [configuration.md](configuration.md) §1 命令行参数

**第一次读代码** →
1. [architecture.md](architecture.md) §1–§3（分层 + 类型所有权）
2. `main.go`（36 行，唯一入口）
3. `engine/engine.go`（会话与重试策略都在这）
4. [architecture.md](architecture.md) §4 数据流

**要动 protocol 的打印/CLI 残留** →

1. [analysis.md](analysis.md) §3.2（还剩多少、为什么难）
2. [roadmap.md](roadmap.md) §2（下一步与验收）
3. `protocol/login.go` 的 113 处诊断输出是最大的一块

**要加功能** → [http-api.md](http-api.md)（已有接口约定）→ `web/handlers_*.go` 找同类 →
`engine` 加方法 → `protocol` 加请求/解析。

**要动安全** → [analysis.md](analysis.md) §3.1 → [roadmap.md](roadmap.md) §1。

---

## 维护约定

| 改了什么 | 同步更新 |
|---|---|
| 新增/修改 `config.Settings` 字段、默认值 | [configuration.md](configuration.md) §3–§4 |
| 新增 HTTP 路由、SSE 事件类型 | [http-api.md](http-api.md) |
| 新增/删除包、调整依赖方向 | [architecture.md](architecture.md) §1–§2，并在 `deps_test.go` 加规则 |
| 完成一项待办 | [roadmap.md](roadmap.md) 勾掉；条目消失后从 [analysis.md](analysis.md) 的问题表里删 |
| 新增/替换端口（`Ask` / `OnStage` / `OnInitStage` / `OnRequest`） | [architecture.md](architecture.md) §7 |

写作口径：

* 中文、短句、表格优先；结论先给，证据（`file:line`）后置。
* **数字必须实测**（`grep` / 行数统计）；确需估算时用 `~` 标出来，不要让人误以为是精确值。
* 不写"以后会"——要做的进 [roadmap.md](roadmap.md)，已经做的进 git log。
* 涉及凭据时只写字段名，**永远不要**把 `accounts.json` 的真实内容贴进文档。
