# Kite Plus 评论 设计文档

Kite Plus 评论的产品与技术设计。这些文档是**开发期的约束来源**，不是事后补写的说明书：与文档冲突的实现，先改文档，再改代码。

跨项目的身份约定（OIDC、`(iss, sub)`、各应用独立会话）写在 [Explore identity-and-comments.md](https://github.com/kite-plus/explore/blob/main/docs/design/identity-and-comments.md)；身份服务本身（选型、部署、运维）在 [identity 仓库](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md)。这里的文档引用它们，不重复。

**进度**：身份服务的本地开发栈可用之后，评论服务的 C2 代码已经写完并在本地跑通，C3 做了一部分；都没有上线（[roadmap.md §1](roadmap.md#1-总览)）。设计文档已按实现回填，实现中改动设计的地方都在对应章节写明了原因。

## 文档索引

| 文档 | 内容 | 读者 |
|---|---|---|
| [architecture.md](architecture.md) | 总体设计：两个核心判断、定位、目标与非目标、组成与域名、部署地与合规、隐私、技术选型、工程结构、运维、风险 | 所有贡献者，**先读这篇** |
| [login.md](login.md) | 评论服务依赖身份服务的哪些能力、三种会话、组件登录协议（弹窗与整页跳转）、站点会话、退出、账号删除、威胁模型、FedCM | 所有写代码的人 |
| [widget.md](widget.md) | 嵌入代码、iframe 地址、讨论页标识、宿主页消息、外观、深链接、界面、加载预算 | 前端、接入文档作者 |
| [data-model.md](data-model.md) | PostgreSQL 表结构、隐私约定、保留期、schema 守护 | 后端 |
| [api.md](api.md) | 接口约定、鉴权、各类接口、错误码 | 后端、前端 |
| [moderation.md](moderation.md) | 发布规则、反垃圾、限额、审核与处置、内容格式、通知 | 后端、维护者 |
| [sites.md](sites.md) | 站点注册与验证、设置、各博客系统接入、认领历史评论、导入导出、与 Kite 的集成 | 后端、接入文档作者 |
| [roadmap.md](roadmap.md) | C0–C4 各阶段的范围、用例与退出条件、实现进度，待定事项汇总 | 所有贡献者，排期前读 |
| [../export-format.md](../export-format.md) | 博主导出的 JSON 格式（版本 1） | 博主、导入工具作者 |

## 阅读顺序

1. `architecture.md` 的 **§0（两个核心判断）**。不认同这两条，后面的设计都没有意义。
2. `login.md` 的 §1 和 §4：为什么不做「打开即识别」，以及组件里的登录怎么防住宿主页。
3. 按要做的部分读 `widget.md`、`data-model.md`、`api.md`、`moderation.md` 或 `sites.md`。
4. 动手前读 `roadmap.md` 当前阶段的范围和退出条件，以及 `architecture.md` 的 §12（现在必须定的，和现在不做的）。

## 文档约定

沿用 [Kite 设计文档的约定](https://github.com/kite-plus/kite/blob/main/docs/design/README.md)：

- **语言**：正文中文，专有名词保留英文。代码、注释、commit message 一律用英文。
- **`[EV]`**：结论有实测或既有项目佐证。
- **「现在留接口，不现在实现」**：将来做一件事时，如果只需新增代码、不需修改已有接口，就现在不做；反之必须现在做。
- **状态标注**：`[设计中]` 表示实现前还可调整；`[待定]` 表示明确推迟决策，决定的时间点汇总在 [roadmap.md §7](roadmap.md#7-待定事项)。
- **单一出处**：同一条规则只写在一个地方，其他文档链接过去。限额只写在 `moderation.md` §3.4，保留期只写在 `data-model.md` §4，讨论页标识规则只写在 `widget.md` §5；代码里对应的常量只在 `internal/policy` 和 `internal/pagekey`。
- **外部事实注明核实时间**：浏览器行为、身份服务的能力和法规会变。引用时写明核实的年月和出处，C0 用真机复测。

## 当前状态

| 文档 | 状态 | 最近更新 |
|---|---|---|
| architecture.md | C2 已实现；§5 的合规结论上线前需确认 | 2026-10-16 |
| login.md | 已实现，本地实测结论已回填；真机矩阵待 C0 | 2026-10-11 |
| widget.md | 已实现；C0 真机复测后定稿 | 2026-10-16 |
| data-model.md | 已落地为迁移文件 | 2026-10-16 |
| api.md | 已实现，含读者中心和控制台的前端约定（§11） | 2026-10-16 |
| moderation.md | 已实现；限额在 C2 内测后回填 | 2026-10-16 |
| sites.md | 已实现，导入待真实数据验收 | 2026-10-16 |
| roadmap.md | 实现进度已标注 | 2026-10-16 |

2026-09-26 按一次独立审阅做了修订：登录入口只接受本站页面的请求、认领验证绑定发起人、组件高度不随身份变化、读取收不到 Cookie、站点会话 Cookie 按站点命名、读者 ID 按站点区分，以及账号删除、站点生命周期、数值单一出处等一批修正。

2026-10-10：Kite 改为通过官方评论插件 `plugin-comments` 接入（多一个「Kite Plus 评论」选项），不再写进默认主题（[sites.md §5.1](sites.md#51-kite-评论插件)）。

对外的产品名暂用「Kite Plus 评论」（英文 Kite Plus Comments），C3 公开前再定 `[待定]`。

2026-10-16：取消访客评论。评论是为了让读者凭一个账号找回自己说过的话，访客在这之外：组件不再提供访客表单，服务端对访客发表返回 `410`，站点设置里的访客选项和工作量证明删掉；已有和导入的访客评论照常显示、审核、认领，已确认过回复提醒的访客照常收到回复邮件，邮件里提示认领（[moderation.md §3.2](moderation.md#32-访客)、[§6.1](moderation.md#61-读者)）。

2026-10-11：按实现回填。主要的改动和决定：站点 origin 的约束允许 http，只给开发服务器用；记下证明 origin 的令牌哈希供复查；访客邮箱记下语言；新增垃圾域名清单表；iframe 和服务自己的页面用 `strict-origin` 而不是 `no-referrer`；身份服务的事件只当提示，处理前读取用户当前状态；`lang` 属性的归一规则；每日汇总定在 UTC 01:00；工作量证明的具体参数。
