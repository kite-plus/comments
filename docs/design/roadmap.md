# 路线图

> 状态：C2 的代码已完成，C3 部分完成，都只在本地跑过；部署地已定（与 Explore 同机，部署文件本机演练过）；进度按下面的表如实标注 · 最近更新：2026-10-11
> 每个阶段只在退出条件全部满足后才进入下一阶段。进度如实标注，沿用组织主页「如实标注进度」的原则。

---

## 1. 总览

| 阶段 | 要回答的问题 | 交付 | 状态 |
|---|---|---|---|
| **C0 技术验证** | 跨站登录在主流浏览器和国内常见 App 内浏览器里走不走得通？ | 原来计划的原型由生产代码代替；实测表 | 部分：桌面 Chrome 上的自动化测试已有（§2.3），真机矩阵未做 |
| **C1 身份服务** | 已拆成单独的项目，见 [identity 路线图](https://github.com/kite-plus/identity/blob/main/docs/design/roadmap.md) 的 I0–I3 | —— | 由 identity 负责；本地开发栈（ZITADEL v4.19.4）已可用，预发布和生产未上线 |
| **C2 评论内测** | 登录读者在几个真实博客上用得顺不顺？博主管得过来吗？ | `comments.kite.plus` 上线（受邀站点）；Kite 评论插件支持 | 进行中：后端和组件完成并在本地跑通（§4.1），读者中心和控制台的前端在另一条分支，未上线 |
| **C3 开放接入** | 任何博客能不能自助接入、把旧评论搬过来？ | 访客评论、导入、认领、讨论页迁移、开放注册 | 部分实现（§5.1），没有用真实数据验收 |
| **C4 生态** | 主流博客系统能不能装个插件就用？ | 各系统插件、Kite 后台集成、Webhook、FedCM 等 | 未开始 |

**评论这条线暂缓**：身份服务是所有服务的基础设施，先按 identity 路线图做。评论服务依赖它的程度：C0 需要一个预发布的身份服务（identity 的 I1），C2 需要生产可用的身份服务（identity 的 I2）。依赖关系：identity I1 → C0；identity I2 → C2 → C3 → C4。

---

## 2. C0 技术验证

评论方案里真正没把握的，是浏览器在第三方 iframe 里怎么对待登录，其余都是工作量。所以先用最小原型把它测清楚，再写生产代码。身份服务选哪个，由 identity 路线图的 I0 回答。

### 2.1 跨站登录

**环境**：

- 身份服务用 identity I1 的预发布环境 `id-staging.kite.plus`；评论原型放在同一个可注册域名下的 `comments-staging.kite.plus`。
- 两个测试博客放在两个不同的可注册域名上：`kite-plus.github.io` 和一个 `*.pages.dev`。两者都在公共后缀列表里，每个子域名自成一个「站点」，能真实模拟跨站嵌入。
- 最小 Go 服务，只实现 [login.md §4](login.md#4-组件登录协议) 的流程、[login.md §5](login.md#5-站点会话) 的两种会话载体和 `frame-ancestors`，不写评论功能。

**用例**：

| # | 场景 | 通过标准 |
|---|---|---|
| T1 | 身份服务已登录，在博客 A 点「登录」 | 弹窗自动关闭，2 秒内显示已登录 |
| T2 | 没有账号，在弹窗里注册（邮箱验证、GitHub、Passkey 各一次） | 完成后回到评论框，已登录 |
| T3 | 身份服务页面切断了弹窗和 iframe 的联系 | 靠轮询照样完成 |
| T4 | 弹窗被拦截，或弹窗拿不到 opener | 整页跳转，经确认页回到原文，已登录 |
| T5 | 会话持久性：刷新、新标签页、重启浏览器、8 天后再访问 | 记下每个浏览器用的是哪种载体、能保持多久；Safari 26.2 以上要确认分区 Cookie 可用，而且没有被当成跟踪域名 |
| T6 | 换到博客 B（另一个可注册域名） | 点一次即登录，不用再输密码 |
| T7 | 在未验证的 origin 上嵌入组件 | 浏览器拒绝渲染 |
| T8 | 宿主页脚本尝试读令牌、带着分区 Cookie 伪造写请求；宿主页或无关的第三方网站用自己的 challenge 调用建交易接口、提交整页跳转入口，再在自己的服务器上兑换；比较读者登录前后、有没有待审评论时 iframe 报告的高度；检查 iframe 文档和读取接口的请求里有没有 Cookie | 全部失败；高度一致；服务端收不到 Cookie |
| T9 | 「退出此站点」、读者中心撤销会话、在身份服务退出；再做一次：身份服务会话过期、重新登录后才在身份服务退出 | 前两种立即失效；在身份服务退出时，这次身份服务会话期间签发的会话失效，行为和 [login.md §6](login.md#6-退出) 一致 |
| T10 | 宿主页带 `COOP: same-origin` | 自动退回整页跳转 |
| T11 | 重启浏览器后再做 T1 | 仍然不用输密码 |

**浏览器**：

| 平台 | 浏览器 |
|---|---|
| macOS | Chrome、Edge、Firefox、Safari |
| Windows | Chrome、Edge、Firefox |
| iOS | Safari、Chrome、微信内置浏览器、QQ 内置浏览器 |
| Android | Chrome、微信内置浏览器、QQ 浏览器、一款厂商自带浏览器 |
| 特殊模式 | Chrome 无痕、Firefox 严格模式、Brave |

**交付**：`lab/prototypes/comments-login/README.md` 记录每个用例在每个浏览器上的结果，以及据此做出的决定：两种会话载体的实际覆盖面、轮询间隔、整页跳转的触发条件和文案。这些决定回填到 [login.md](login.md)，把对应的 `[待定]` 去掉。

### 2.3 已有的实测（2026-10）

原型没有单独写：协议直接用生产代码实现，用两种方式测。

- **Go 协议测试**（`internal/api`，假身份服务 `oidctest`，`go test` 里跑）：T8 的服务端部分全部覆盖——跨站页面建交易、提交整页跳转入口、用自己的 challenge 再兑换、伪造回调、带着分区 Cookie 的跨站写请求、匿名读取收不到 Cookie 且不改动任何表和会话、iframe 文档不随凭据变化；另有兑换的各个状态、确认页的 binder 绑定、back-channel logout、身份服务事件。
- **浏览器测试**（`e2e/`，Playwright 驱动本机的 Google Chrome，桌面 macOS）：测试桩在本机起假身份服务、评论服务（前面放一个按 Caddyfile 删 Cookie 的代理）、两个不同站点的测试博客和一个无关站点，`make e2e` 运行。真实身份服务那一套（`make e2e-real`）对着本地 ZITADEL 开发栈，用临时账号走真实的登录页。

| # | Chrome（假身份服务） | Chrome（本地 ZITADEL） | 其余浏览器和真机 |
|---|---|---|---|
| T1 | 通过 | 通过：弹窗经登录页后自己关掉 | 待测 |
| T2 | —— | 未测（注册、邮箱验证、GitHub、Passkey） | 待测 |
| T3 | 通过：登录页带 COOP 切断 opener，轮询照样完成 | —— | 待测 |
| T4 | 通过（经 T10 的路径） | —— | 待测 |
| T5 | 分区 Cookie 可用，分区键是博客的站点 | 同左 | 待测，Safari 26.2+ 是重点 |
| T6 | 通过：第二个站点点一次，弹窗不出现页面 | 通过 | 待测 |
| T7 | 通过：`frame-ancestors` 拒绝，组件显示加载失败 | —— | 待测 |
| T8 | 通过：宿主页伪造写请求被拒（服务端看到带 Cookie 的请求并返回 `403`）、读不到 iframe 的存储、自己打开弹窗发 `begin` 建不了交易；登录前后和有待审评论时高度一致；读取请求到达服务时没有 Cookie | 高度一致 | 待测 |
| T9 | 通过：退出此站点、back-channel logout；「退出所有站点」由 API 测试覆盖 | 通过：退出此站点；管理员结束身份服务会话后对应的会话被删 | 待测 |
| T10 | 通过：宿主页带 COOP 时退回整页跳转，经确认页回到原文 | —— | 待测 |
| T11 | —— | 未测 | 待测 |

浏览器测试顺带发现：iframe 用 `Referrer-Policy: no-referrer` 时，Chrome 给整页跳转的表单发 `Origin: null`，入口只能拒绝，已改为 `strict-origin`（[login.md §4.4](login.md#44-整页跳转降级)）。

§2.1 的浏览器矩阵（Edge、Firefox、Safari、iOS、Android、微信和 QQ 内置浏览器、无痕和严格模式、Brave）**全部待测**，要真机，C0 的退出条件不变。

### 2.2 退出条件

- T1–T10 在所有桌面浏览器、iOS Safari、Android Chrome 上通过。
- 微信内置浏览器（iOS、Android）至少通过 T1 或 T4 之一。两者都不通过时，组件在微信里只读，写评论时提示「在浏览器中打开」，并把这个限制写进 login.md。
- 结果表写完。

---

## 3. C1 身份服务

已拆成单独的项目：[identity 仓库](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md)。范围和退出条件都以 [identity 路线图](https://github.com/kite-plus/identity/blob/main/docs/design/roadmap.md) 为准：I0 选型、I1 预发布、I2 生产、I3 Explore 上线。

评论服务从那里接手的是：生产可用的 `id.kite.plus`（I2），满足 [login.md §2](login.md#2-身份服务) 列出的要求；以及登记好的 `comments` 客户端，生产和预发布各一个。

---

## 4. C2 评论内测

**范围**：

- 站点：注册、三种验证方式、[sites.md §4](sites.md#4-站点设置与讨论页管理) 的设置（访客评论先固定为关闭）、平台批准。
- 组件：[widget.md](widget.md) 的全部内容，含深链接、深浅色、中英文、自托管版本。
- 登录：弹窗和整页跳转、站点会话、退出、读者中心的会话列表。
- 评论：只对登录读者开放；两层回复、Markdown、编辑窗口内编辑、删除、置顶、锁定、举报。
- 审核：队列、处置、封禁；平台工具先用命令行加控制台里的最小页面。
- 通知：回复邮件、站长即时通知和每日汇总。
- 读者中心：我的评论、通知、资料、已登录的站点、导出、删除账号（含评论的处理方式）。
- 博主导出（JSON 和 Artrans）。
- Kite 官方评论插件增加「Kite Plus 评论」选项（[sites.md §5.1](sites.md#51-kite-评论插件)）。
- 运维：部署、备份、监控；隐私政策和服务条款（中英文）；`abuse@kite.plus`。

### 4.1 实现进度（2026-10-11）

| 范围 | 状态 |
|---|---|
| 站点：注册、三种验证方式、设置、平台批准 | 完成；访客评论没有固定为关闭，新建站点时必须选（C3 的做法，见 §5.1） |
| 组件：widget.md 的全部内容，含深链接、深浅色、中英文、自托管版本 | 完成；固定版本和 SRI 由 `/api/v1/embed-info` 提供 |
| 登录：弹窗和整页跳转、站点会话、退出、读者中心的会话列表 | 完成，本地对着 ZITADEL 跑通 |
| 评论：两层回复、Markdown、编辑、删除、置顶、锁定、举报 | 完成 |
| 审核：队列、处置、封禁；平台工具 | 接口和命令行完成；控制台页面在另一条分支 |
| 通知：回复邮件、站长即时通知和每日汇总 | 完成，本地经 Mailpit 收到回复邮件 |
| 读者中心：我的评论、通知、资料、已登录的站点、导出、删除账号 | 接口完成；页面在另一条分支 |
| 博主导出（JSON 和 Artrans） | 完成 |
| Kite 评论插件 | 未做（另一个仓库） |
| 运维：部署、备份、监控；隐私政策和服务条款；`abuse@kite.plus` | 部署文件含边缘模式（共用 Explore 的 Caddy，[architecture.md §9](architecture.md#9-部署与运维)），本机按服务器的布局演练过；`scripts/backup.sh` 加密备份并可复制到异地，本机恢复进空库过；未部署，没有镜像发布流程和监控，没有政策文本 |

退出条件都还没有满足：没有外部博客在用，安全检查清单（[login.md §8](login.md#8-威胁模型与安全检查)）没有逐项过，没有做恢复演练；体积预算已达标（embed.js 2.7 KB、界面 14 KB）。

**内测站点**：`www.kite.plus` 的新闻、`demo/blog`，再邀请 3–5 位分别用 Hexo、Hugo、WordPress、Halo 的博主。

**退出条件**：

- 至少 3 个不同可注册域名的外部博客连续使用 2 周，没有未解决的 P0、P1 问题。
- [login.md §8](login.md#8-威胁模型与安全检查) 的安全检查全部通过；条件允许的话，请外部的人审一遍登录协议。
- 评论数据库完成一次恢复演练。
- [widget.md §10](widget.md#10-加载体积与降级) 的体积和加载预算达标。

---

## 5. C3 开放接入

**范围**：

- 访客评论：工作量证明、访客邮箱确认和回复通知（[moderation.md §3.2](moderation.md#32-访客)、[§6.1](moderation.md#61-读者)）。
- 导入 Waline、Twikoo、Artrans、WordPress WXR，带预览和地址映射（[sites.md §7](sites.md#7-导入)）；导出增加 WXR。
- 认领历史评论（[sites.md §6](sites.md#6-认领历史评论)）。
- 讨论页迁移与合并。
- 站点验证的定期复查和域名易主处理（[sites.md §2](sites.md#2-注册与验证)），和开放注册一起上线。
- 开放站点注册，取消平台批准；新建站点时由博主明确选择是否接受访客。
- 各博客系统的接入文档。
- 决定代码高亮。

### 5.1 实现进度（2026-10-11）

| 范围 | 状态 |
|---|---|
| 访客评论：工作量证明、访客邮箱确认和回复通知 | 完成 |
| 导入 Waline、Twikoo、Artrans、WordPress WXR，带预览和地址映射 | 完成，用构造的样例测过，没有用真实数据验收；失败条目下载未做 |
| 导出增加 WXR | 完成，用本服务的 WXR 导入读回验证 |
| 认领历史评论 | 完成，含「只有发起人能验证」和自己邮箱的提示 |
| 讨论页迁移与合并 | 完成，可撤销 |
| 站点验证的定期复查 | 完成；域名易主未做（[sites.md §2](sites.md#2-注册与验证)） |
| 开放站点注册，取消平台批准 | 未做（`SiteApprovalRequired` 仍为开） |
| 各博客系统的接入文档 | 未做 |
| 决定代码高亮 | 未定 |

导出再导入空实例的往返测试已写成 `go test`（`e2e/roundtrip`），重复导入不产生新数据。

**退出条件**：

- 用真实的 Waline、Twikoo、WordPress 数据各做一次导入，抽查无丢失、无重复，重复导入不产生新数据；导入结果里找不到任何原系统的密钥（[sites.md §7](sites.md#7-导入)）。
- 认领流程端到端测试通过：不能借此探测别人的邮箱；别的账号、邮件网关打开验证链接都不会完成验证。
- 导出再导入空实例的往返测试通过（[sites.md §7](sites.md#7-导入)），写进 CI。
- 在 `www.kite.plus` 发布公告。官网规划约定服务上线前不出现在官网上（[website 规划 §1](https://github.com/kite-plus/website/blob/main/docs/design/README.md#1-定位)），到这时才加。

---

## 6. C4 生态

按需求排序，不承诺顺序：

- WordPress、Typecho、Halo 插件；Disqus 导入；从 Giscus 拉取并用 GitHub 登录认领。
- Kite 后台集成：站点 API 令牌、审核接口、Webhook（[sites.md §9](sites.md#9-与-kite-的深度集成c4)）。
- `kite comments pull`：评论同步回站点文件。
- 评论数接口（列表页显示「12 条评论」）、点赞。
- 协管员。
- 与 Explore 互认博客归属；把站点转给别人。
- Chromium 上的 FedCM（[login.md §9](login.md#9-以后fedcm)）。
- 读者自愿公开的个人主页。
- 评论里的图片 `[待定]`。

---

## 7. 待定事项

| 事项 | 写在哪里 | 什么时候定 |
|---|---|---|
| 整页跳转的触发条件；轮询间隔 | [login.md §4](login.md#4-组件登录协议) | 已按设计实现并在桌面 Chrome 测过；C0 真机复测后定稿 |
| ~~评论服务加入后机器怎么扩~~ | [architecture.md §9](architecture.md#9-部署与运维) | 2026-10-11 定：和身份服务一起放在与 Explore 同机的美国服务器上，共用 Explore 的 Caddy，资源够用 |
| 镜像发布流程（现在在服务器上构建） | [architecture.md §9](architecture.md#9-部署与运维) | C2 上线前 |
| 备份异地存放位置 | [architecture.md §9](architecture.md#9-部署与运维)，和身份服务一起定 | identity I2 前 |
| 境外运营的合规结论请懂行的人确认 | [architecture.md §5](architecture.md#5-部署地与合规) | C2 上线前 |
| 被封锁时的备用域名预案 | [architecture.md §11](architecture.md#11-风险) | C2 上线前 |
| 限额数值 | [moderation.md §3.4](moderation.md#34-限额-待定) | C2 内测后 |
| 举报自动退回的阈值 | [moderation.md §4.3](moderation.md#43-举报) | C2 内测后 |
| 违法内容处理时限；服务条款和隐私政策 | [moderation.md §4.2](moderation.md#42-平台) | C2 上线前 |
| 退信处理（每日汇总的发送时间已定为 UTC 01:00） | [moderation.md §6](moderation.md#6-通知) | C2 |
| 代码高亮 | [moderation.md §5](moderation.md#5-内容格式) | C3 前 |
| FedCM 端点怎么和身份服务的会话打通；评论里的图片 | [login.md §9](login.md#9-以后fedcm)、[§6](#6-c4-生态) | C4 |
| 对外的产品名 | [README.md](README.md) | C3 公开前 |

身份服务的待定事项（Google 登录、SMTP、预发布的二级子域证书、Explore 过渡期等）在 [identity 路线图 §6](https://github.com/kite-plus/identity/blob/main/docs/design/roadmap.md#6-待定事项)。
