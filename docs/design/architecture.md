# Kite Plus 评论 总体设计

> 状态：C2 的代码已完成并在本地对着身份服务的开发栈跑通（[roadmap.md §4](roadmap.md#4-c2-评论内测)）；未上线；部署在与 Explore 同机的服务器上（§5、§9，本机演练过）；§5 的合规结论上线前需找懂行的人确认 · 最近更新：2026-10-11
> 文档约定沿用 [Kite 设计文档](https://github.com/kite-plus/kite/blob/main/docs/design/README.md)：正文中文，专有名词保留英文；`[设计中]` 表示实现前还可调整，`[待定]` 表示明确推迟决策；`[EV]` 表示结论有实测或既有项目佐证。
> 与本文冲突的实现，先改文档，再改代码。
> 本文是总体设计；登录、组件、数据模型、接口、审核、站点接入和路线图各有专门的文档，索引见 [README.md](README.md)。

---

## Context：要解决什么

独立博客的评论现在是这样的：

- **每个博客各管一套**。Waline、Twikoo、Artalk、Giscus、WordPress 自带评论……读者每到一个博客都要重新填昵称、邮箱和网址。
- **读者追溯不了自己的评论**。留过的言散在各处，别人回复了也常常不知道。
- **博主要自己运维评论后端**。数据库、云函数、邮件、反垃圾都得自己管，托管平台也说停就停：LeanCloud 将于 2027-01-12 停止服务，Valine 和用 LeanCloud 存数据的 Waline 用户都得搬家（[LeanCloud 公告](https://docs.leancloud.app/sdk/announcements/sunset-announcement/)）。
- **统一身份的现成方案各有硬伤**。Giscus 要求 GitHub 账号，国内访问不稳定；Disqus 有广告和追踪，国内打不开。

目标结果：

> 读者用一个 Kite Plus 账号，在任何接入的博客上点一下就能评论，并在一个地方看到自己所有的评论和收到的回复；博主两行代码接入，不用运维，数据随时能带走。

Kite Plus 评论面向所有博客系统，**不是 Kite 的附属品**。Kite 的官方评论插件把它列为一个服务选项，输出的也是和其他博客一样的嵌入代码，没有私有通道。

---

## 0. 两个核心判断

### 0.1 统一的是人：身份统一、评论集中保存，但随时能带走

有两种做法：

| 做法 | 读者能追溯自己的评论吗 | 通知与反垃圾 | 博主要做什么 |
|---|---|---|---|
| 只统一身份，评论仍存在各博客自己的后端 | 不能，评论还是散在各处 | 各管各的 | 每个评论后端都要接入 OIDC |
| 身份统一，评论集中保存在评论服务里 | 能 | 全网统一 | 贴两行代码 |

选第二种。集中保存的代价是博主要把评论交给我们，所以下面几条是**硬约束**，不是可选功能：

- 博主随时能导出整个站点（C2 就有）；读者随时能导出、删除自己的评论（[sites.md §8](sites.md#8-导出)）。
- 代码开源，任何人都能自己部署一套。
- 不放广告，不做追踪，不卖数据。
- 迁进来要容易：能导入 Waline、Twikoo、WordPress 等系统的旧评论，读者能认领自己以前的访客评论（[sites.md §6](sites.md#6-认领历史评论)、[§7](sites.md#7-导入)）。
- 以后能把评论同步回博主自己的站点文件（C4，[sites.md §9](sites.md#9-与-kite-的深度集成c4)）。

**可验证**：导出一个站点，导入一个空实例，两边的讨论页、评论和状态完全一致。C3 能导入本服务自己的格式之后，这条写成自动化测试。

### 0.2 博客页面不可信；浏览器的隐私限制是前提，不是障碍

- **谁都能搭博客、嵌入组件**。所以凭据和写操作全在 `comments.kite.plus` 的 iframe 里完成，宿主页拿不到读者的身份和令牌（[login.md §4.3](login.md#43-为什么这样能防住宿主页)）；组件只能出现在验证过的站点上，由浏览器按 `frame-ancestors` 保证（[widget.md §4](widget.md#4-iframe-地址)）。
- **浏览器拦截或分区第三方 Cookie**，而「打开页面就认出读者」本身就是跨站追踪。所以不依赖第三方 Cookie，登录以「每个站点点一次」为基线（[login.md §1](login.md#1-目标与边界)）。
- **可验证**：宿主页脚本能发起的攻击（读令牌、伪造请求、冒充登录）写成自动化测试，C2 起在 CI 里跑（[roadmap.md §2.1](roadmap.md#21-跨站登录) 的 T8）。

---

## 1. 产品定位

- **是**：给独立博客用的托管评论服务，加上统一的读者身份。
- **不是**社交网络：没有关注、私信和动态流。
- **不是**内容平台：不存文章正文，只存讨论页的地址和标题。
- **不是**统计工具：不记录谁看了哪一页。

在 Kite Plus 里，Kite 负责创作与发布，Explore 负责发现；评论服务在这之后，把读者和作者连起来。上线前它不出现在官网上（[website 规划 §1](https://github.com/kite-plus/website/blob/main/docs/design/README.md#1-定位)）。

---

## 2. 目标与非目标

### 2.1 V1 目标（C2–C3）

1. **读者**：一个账号在所有接入的博客评论；在读者中心看到自己的全部评论和回复通知；随时导出、删除。
2. **博主**：任何能插入 HTML 的博客，两行代码接入；审核、反垃圾、通知由服务处理；能导入旧评论，随时导出。
3. **生态**：Kite 的官方评论插件支持；与 Explore 共用 Kite Plus 账号。

### 2.2 明确不做

| 不做 | 原因 |
|---|---|
| 页面浏览量、阅读统计 | 要记录读者行为，违背 §0.2 |
| 打开页面就自动认出读者 | 跨站追踪（[login.md §1](login.md#1-目标与边界)） |
| 广告、推荐、数据变现 | §0.1 |
| 评论里的图片、表情包 | 托管和审核的负担、隐私（[moderation.md §5](moderation.md#5-内容格式)）；C4 后再评估 |
| 微信、QQ 登录；手机号验证 | 需要企业资质和已备案的域名（§5） |
| 第三方反垃圾服务和验证码 | [moderation.md §1](moderation.md#1-原则) |
| 显示 IP 属地 | 境外运营不适用（§5）；将来转为境内运营时再加 |
| 自托管实例之间互通 | 复杂度高，V1 用不上 |

---

## 3. 用户与场景

| 谁 | 场景 |
|---|---|
| 读者 | 在三个技术博客都留过言。以前每次都填昵称和邮箱，现在点一下就登录；有人回复时收到邮件；在读者中心看到三个博客里的全部评论 |
| 老读者 | 在读者中心认领自己以前用 QQ 邮箱留下的访客评论 |
| 用 Hexo 的博主 | 注册站点，在首页放一个 meta 标签完成验证，把两行代码粘进主题，导入 Waline 的旧评论 |
| 用 Kite 的博主 | 在主题设置里填站点 ID，完成 |
| 要离开 LeanCloud 的博主 | 在停服前把 Valine 或 Waline 的评论导出、导入，读者的旧评论不丢 |
| 平台维护者 | 处理违法内容的举报，暂停滥用的站点 |

---

## 4. 系统组成与域名

```text
                  ┌───────────────────────────────┐
                  │ id.kite.plus                  │
                  │ Kite Plus 账号（OIDC 身份服务） │
                  └───────┬───────────────┬───────┘
                          │ OIDC          │ OIDC
          ┌───────────────▼───┐   ┌───────▼─────────────────────┐
          │ explore.kite.plus │   │ comments.kite.plus          │
          │ Explore           │   │ 评论服务：接口、组件、        │
          └───────────────────┘   │ 读者中心 /me、站点控制台 /console │
                                  └───────┬─────────────────────┘
                                          │ embed.js + iframe
                          ┌───────────────▼─────────────────────┐
                          │ 各个博客：Kite、Hexo、Hugo、WordPress、 │
                          │ Halo、Typecho……                      │
                          └─────────────────────────────────────┘
```

| 域名 | 服务 | 说明 |
|---|---|---|
| `id.kite.plus` | Kite Plus 账号 | OIDC issuer，永不更换；注册、登录、账号安全、删除账号。基础设施，单独在 [identity 仓库](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md) 规划 |
| `comments.kite.plus` | 评论服务 | `embed.js`、iframe、接口、读者中心、站点控制台 |
| `explore.kite.plus` | Explore | 已有 |
| `www.kite.plus` | 官网 | GitHub Pages，与这里无关 |
| `kite.plus` | 跳转到 `www` | 不承载任何服务 |

- **为什么叫 `id`**：短，不偏向某一项功能，读者在登录弹窗的地址栏里一眼就能认出来。它是 issuer，会写进每一个令牌，上线后不能改。
- **为什么评论服务和身份服务是两个 origin**：登录页必须禁止被嵌入（防点击劫持），评论组件却必须能被任何已验证的博客嵌入；身份服务是第三方软件，升级节奏也和评论服务不同。
- **为什么评论服务不用一个独立的可注册域名**：考虑过三个理由：
  1. 评论是用户生成的内容。同站的兄弟子域一旦出现 XSS，就能向 `kite.plus` 写 Cookie、发同站请求。
  2. 被大量网站嵌入的域名可能被 Safari 归为跟踪域名，而归类是按整个可注册域名算的。
  3. 评论内容可能导致域名在国内被封锁，连累 `kite.plus` 下的其他服务。

  结论是仍用 `comments.kite.plus`：
  - 评论 HTML 只由服务端净化后输出，风险远小于托管任意用户文件。
  - Safari 的归类对其他服务影响很小：它们用服务端设置的 Cookie，读者一个月内有过交互就不会被清。
  - 陌生的独立域名会降低博主和读者的信任。

  对应的防护有两条：`kite.plus` 下所有服务的会话 Cookie 都用 `__Host-` 前缀，兄弟子域无法覆盖（Explore 的 `explore_session` 在 C1 改名）；写接口都带 CSRF 防护。封锁风险见 §11。

---

## 5. 部署地与合规

**决定：部署在境外，先放在与 Explore 同机的美国 RackNerd 服务器上**（2026-10-11 维护者决定），和身份服务同一台（[identity architecture.md §4](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#4-部署)）。原来首选香港，到国内三网有优化线路（如 CN2 GIA）时延迟最低；现在用已有的机器，不另买服务器，运维集中在一处。代价是到国内的延迟更高、晚高峰更明显，前面的 Cloudflare 免费套餐在中国大陆也没有节点；以 Explore 现在的实际访问为参考，C2 内测前从国内三网实测一次。

以后迁到香港仍然可行：评论服务的 origin 和身份服务的 issuer 都是域名，不绑机器，迁移就是在新机器上恢复备份（§9）再改 DNS。

以下是 2026-09 查到的事实和我们的理解，不是法律意见。

| | 境内运营 | 境外运营 |
|---|---|---|
| 前置手续 | ICP 备案：`.plus` 可以备案（工信部批准的域名注册管理机构名单里有它），但北京、贵州、江苏等地的个人备案不允许博客、论坛类交互内容，实际上要以公司备案；上线 30 天内做公安备案，申报「交互式服务」；上线前按《具有舆论属性或社会动员能力的互联网信息服务安全评估规定》做安全评估 | 不需要 |
| 评论者实名 | 必须。《互联网跟帖评论服务管理规定》第 4 条要求「后台实名、前台自愿」，按手机号等方式认证，不得向未认证的用户提供服务。而阿里云、腾讯云 2025 年起不再受理个人的短信资质 | 不适用 |
| 访客匿名评论 | 不允许（同上） | 可以 |
| 审核 | 要有审核、巡查、应急处置、举报受理等制度，审核人员与服务规模相适应 | 按 [moderation.md](moderation.md) |
| 日志 | 网络日志至少保存 6 个月 | 按 [data-model.md §4](data-model.md#4-保留期) |
| IP 属地 | 《互联网用户账号信息管理规定》第 12 条要求在账号信息页显示 | 不适用 |
| 速度 | 快 | 香港到国内约 20–60 ms；美国到国内通常在 130 ms 以上；晚高峰都取决于线路 |
| 封锁 | 不会 | 有可能，而且没有预警 |

**结论**：对一个人维护的开源项目，境内运营的准入条件和持续义务（公司主体、评论者实名、审核团队、半年日志）都不现实；访客评论这个迁移时的关键功能也做不了。境外运营的代价是跨境延迟和被封锁的风险，在 §11 处理。

**境外运营仍要面对的**：

- **个人信息**：运营者本人在境内，面向境内个人处理个人信息，适用《个人信息保护法》；数据存放在境外可能被视为数据出境。按 2024 年《促进和规范数据跨境流动规定》第 5 条，一年内向境外提供不满 10 万人的非敏感个人信息，免于安全评估、标准合同和认证。收集尽量少的个人信息（[data-model.md §1](data-model.md#1-数据分类)）是最直接的应对。
- **违法信息**：《网络安全法》要求阻断境外的违法信息。我们的做法是认真处理违法内容的举报（[moderation.md §4.2](moderation.md#42-平台)）。
- **规则能否延伸**：跟帖评论的规定能不能管到「运营者在境内、服务器在境外」的服务，官方文本没有明确，也没有查到针对这类个人服务的案例。C2 上线前找懂行的人确认 `[待定]`。
- **上线前必备**：中英文的隐私政策和服务条款，举报渠道 `abuse@kite.plus`。
- **博主自己的备案**：一些省份的个人备案本来就不允许网站有评论等交互功能。接不接入由博主自己判断，接入文档里提醒一句。

**如果将来转为境内运营**（比如成立了公司），要做的是：公司主体的 ICP 备案和公安备案、安全评估；身份服务加手机号验证；全平台关闭访客评论；网络日志保存 6 个月；账号页显示 IP 属地。这些都只需要新增代码和配置，不需要改已有接口，所以现在不做。

---

## 6. 隐私

| 项 | 做法 | 详见 |
|---|---|---|
| 读评论 | 服务端收不到任何会话：`embed.js` 不带 Cookie 加载，iframe 文档和匿名读取接口的 Cookie 在反向代理处删掉，服务端也删；不建记录，讨论页在第一条评论写入时才创建。登录过这个站点的读者，组件加载后另有一个带会话的「我是谁」请求，不带文章地址 | [widget.md §1](widget.md#1-目标)、[§4](widget.md#4-iframe-地址)、[login.md §5](login.md#5-站点会话)、[data-model.md §1](data-model.md#1-数据分类) |
| 宿主页 | 看不出谁在读：组件高度和发给宿主页的消息不随读者身份变化 | [widget.md §9](widget.md#9-界面) |
| Cookie | 都是 `__Host-` 前缀：按站点命名的分区站点会话、第一方的控制台会话、登录时用的 binder。匿名读者一个也没有 | [login.md §3](login.md#3-三种会话) |
| 读者 ID | 读者在每个站点的作者 ID 各不相同，任何人都不能凭 ID 把一个读者在各站的评论串起来，博主的导出之间也对不上 | [data-model.md §3.1](data-model.md#31-账号与身份) |
| 第三方请求 | 组件不发任何第三方请求：没有网络字体、统计脚本和外链头像 | [widget.md §10](widget.md#10-加载体积与降级) |
| 邮箱 | 只存密文和 HMAC，永不公开，也不公开邮箱的哈希值（Waline、Twikoo、Artalk 都公开未加盐的邮箱哈希，能被字典反推）。导入的解析结果里也是密文；日志和发信错误里不留收件地址 | [data-model.md §2](data-model.md#2-约定)、[moderation.md §6.3](moderation.md#63-发信) |
| 导入文件 | 原始文件解析完就丢弃，不落库；解析结果 24 小时后清除 | [sites.md §7](sites.md#7-导入) |
| IP | 只存按月轮换的 HMAC，按保留期清空；日志不记 IP | [data-model.md §2](data-model.md#2-约定) |
| 博主能看到的 | 显示名、个人主页、访客留下的邮箱；看不到登录读者的邮箱 | [sites.md §8](sites.md#8-导出) |
| 读者的权利 | 随时导出、删除；删除几秒内生效，备份按 §9 的保存期轮换掉。唯一的例外是封禁：被封禁的读者删除数据后，封禁记录里的身份 `(issuer, subject)` 保留到封禁解除或过期，只用来执行封禁，隐私政策里写明 | [login.md §7](login.md#7-账号删除与停用)、[data-model.md §3.5](data-model.md#35-审核举报与封禁) |

---

## 7. 技术选型

| 部分 | 选择 | 理由 |
|---|---|---|
| 后端 | Go 1.26（与 Kite、Explore 同一工具链）、gin、pgx/v5、goose、cobra、`log/slog` | 与 Explore 一致，约定、脚本和经验可以直接搬过来 |
| 数据库 | PostgreSQL 16 | 同上 |
| OIDC 客户端 | `coreos/go-oidc` v3、`golang.org/x/oauth2` | 成熟，只做客户端 |
| Markdown 与净化 | goldmark、bluemonday | [moderation.md §5](moderation.md#5-内容格式) |
| 访客验证 | 工作量证明，协议参考 ALTCHA | 自托管，不发第三方请求 |
| 发信 | SMTP，经 `outbox` 由 worker 发送 | [moderation.md §6.3](moderation.md#63-发信) |
| 组件界面 | Preact、TypeScript、Vite | 体积预算（[widget.md §10](widget.md#10-加载体积与降级)） |
| 读者中心、站点控制台 | React 19、Tailwind v4、shadcn/ui、TanStack Query | 与 Explore 后台相同，组件和经验可以复用 |
| 前端交付 | 构建产物用 `embed` 打进 Go 二进制（`web/embed.go`），只有一个镜像；镜像构建里有一个 Node 阶段 | 不需要服务端渲染，比 Explore 少一个 Node 服务。读者中心和控制台怎么交付见 [api.md §11](api.md#11-读者中心和站点控制台的前端约定) |
| 反向代理 | 和身份服务共用同一个 | 选型（Caddy 或 nginx）和限流见 [identity architecture.md §4](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#4-部署) |
| 身份服务 | ZITADEL 或 Logto，由 identity 的 I0 选定 | [identity architecture.md §3](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#3-选型) |

---

## 8. 工程结构

仓库 `kite-plus/comments`，结构沿用 Explore（[Explore project-layout.md](https://github.com/kite-plus/explore/blob/main/docs/design/project-layout.md)）：

```text
comments/
├── cmd/comments/main.go      只调用 internal/cli
├── internal/
│   ├── cli/        serve、worker、migrate up、users promote-admin|demote-admin、
│   │               sites list|approve|suspend|unsuspend、version、dev seed|blogs（只给开发机）
│   ├── config/     COMMENTS_* 环境变量
│   ├── policy/     限额、保留期等产品常量（单一出处）
│   ├── model/      领域类型
│   ├── pagekey/    讨论页标识规范化（widget.md §5）
│   ├── markup/     Markdown 渲染与净化，唯一产出 HTML 的包
│   ├── importer/   各导入格式的解析和导入计划
│   ├── spam/       反垃圾规则
│   ├── pow/        工作量证明
│   ├── crypt/      邮箱加密与 HMAC、IP 密钥派生、令牌和签名
│   ├── oidc/       OIDC 客户端，ID Token 与 Logout Token 校验；oidctest 是测试用的假身份服务
│   ├── idp/        身份服务的用户事件：推送签名、事件查询、按用户当前状态处理
│   ├── verify/     站点验证（DNS、HTTP），含内网地址防护
│   ├── mailer/     SMTP 与邮件模板
│   ├── store/      PostgreSQL；storetest 给测试建独立的 schema
│   ├── api/        HTTP
│   ├── worker/     发信、站长通知、清理、对账、导入、重渲染、复查
│   ├── devblogs/   两个本地测试博客的页面（开发和浏览器测试用）
│   └── buildinfo/  链接时写入的版本
├── migrations/     goose，只向前
├── web/
│   ├── embed.go    把下面两个 dist 打进二进制
│   ├── widget/     embed.js、iframe 界面和弹窗页脚本（Preact）
│   └── console/    读者中心与站点控制台（React，另一条分支开发）
├── e2e/            浏览器测试（Playwright，用本机的 Google Chrome）、测试桩、导出导入往返测试
├── deploy/         Dockerfile、docker-compose.yaml、docker-compose.edge.yaml（边缘模式）、docker-compose.dev.yaml、
│                   Caddyfile（独立模式）、edge/（给共用 Caddy 的站点文件和 Cookie 规则）、.env.example、.env.dev.example
├── scripts/        check-imports.sh、backup.sh
├── testdata/schema/columns.txt
└── docs/           design/ 和 export-format.md
```

**依赖规则**（`scripts/check-imports.sh`，在 `make check` 里跑）：

- `policy`、`model`、`pagekey`、`markup`、`importer`、`spam`、`pow`、`crypt` 是纯逻辑包，不引用 I/O 层（`store`、`api`、`worker`、`cli`、`config`、`oidc`、`verify`、`mailer`、`idp`）、gin、pgx、`net/http` 和 `database/sql`。
- 只有 `api` 引用 gin，只有 `store`（含 `storetest`）引用 pgx，只有 `markup` 引用 goldmark 和 bluemonday。
- `api` 和 `worker` 互不引用，只在数据库里相遇；两边共用的逻辑放在 `idp`、`mailer` 这样的包里。

**配置**：环境变量，前缀 `COMMENTS_`。产品阈值不进配置，放在 `internal/policy`，和 Explore 一样。

| 变量 | 说明 |
|---|---|
| `COMMENTS_DATABASE_URL` | PostgreSQL 连接串 |
| `COMMENTS_HTTP_ADDR` | 默认 `127.0.0.1:8081` |
| `COMMENTS_PUBLIC_URL` | `https://comments.kite.plus` |
| `COMMENTS_TRUSTED_PROXIES` | 反向代理的地址 |
| `COMMENTS_OIDC_ISSUER` | `https://id.kite.plus` |
| `COMMENTS_OIDC_CLIENT_ID`、`COMMENTS_OIDC_CLIENT_SECRET` | OIDC 客户端 |
| `COMMENTS_DATA_KEYS` | 邮箱加密密钥，可以有多把，按版本号轮换 |
| `COMMENTS_HMAC_KEY` | 邮箱 HMAC 的密钥，不能轮换 |
| `COMMENTS_IP_KEY` | IP HMAC 的主密钥，按月派生 |
| `COMMENTS_IDP_WEBHOOK_SECRET` | 校验身份服务事件的签名；要和 `COMMENTS_IDP_API_TOKEN` 一起设，只设它服务拒绝启动：推送只是提示，要用 API 令牌向身份服务核实后才处理 |
| `COMMENTS_IDP_API_TOKEN` | 身份服务的 API 令牌（只读），用来对账和在处理事件前读取用户当前状态（[login.md §7](login.md#7-账号删除与停用)）；不设就不对账，也不能接收推送 |
| `COMMENTS_IDP_API_URL` | 身份服务 API 的地址，默认就是 issuer |
| `COMMENTS_SMTP_URL`、`COMMENTS_MAIL_FROM` | 发信：`smtp://[user:pass@]host:port`（服务器支持时 STARTTLS）或 `smtps://`；不设时邮件留在队列里 |
| `COMMENTS_LOG_LEVEL` | 日志级别 |
| `COMMENTS_DEV_HTTP_ORIGINS` | 只给开发机：允许站点用 http 的 origin，本地测试博客靠它；`COMMENTS_PUBLIC_URL` 是 https 时拒绝启动（[sites.md §2](sites.md#2-注册与验证)） |
| `COMMENTS_ALLOW_PRIVATE_NETWORKS` | 只给开发机：站点验证和头像抓取可以访问内网地址和任意端口；`COMMENTS_PUBLIC_URL` 是 https 时拒绝启动 |

`COMMENTS_DATA_KEYS` 的格式是 `版本:base64密钥,版本:base64密钥`，每把 32 字节，版本号 1–255，最大的那个加密，列出的都能解密。`COMMENTS_HMAC_KEY`、`COMMENTS_IP_KEY` 各是一把 32 字节的 base64 密钥。

---

## 9. 部署与运维

- **机器**：和身份服务一样，在与 Explore 同机的美国 RackNerd 服务器上（§5；机器配置、资源预算和 Cloudflare 设置见 [identity architecture.md §4](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#4-部署)）。评论服务有自己的 Compose 项目（PostgreSQL、一次性的 `migrate`、`server` 即 `comments serve`、`worker`）和内部网络，不和别的服务共用数据库容器。本机演练空闲时一套约 50 MB（空库）`[EV]`，这台机器的余量够用。
- **入口（边缘模式，生产用这个）**：整台机器只有 Explore 的 Caddy 占 80 和 443，为所有主机名终止 TLS、申请证书。`deploy/docker-compose.edge.yaml`（由 `.env` 的 `COMPOSE_FILE` 打开）让 `server` 不占宿主机端口，接到共用的 Docker 网络 `kite-edge`（172.30.10.0/24），别名 `comments`，`COMMENTS_TRUSTED_PROXIES` 设为这个网段；`worker`、`migrate`、PostgreSQL 不接 kite-edge。Caddy 的站点文件 `deploy/edge/comments.caddy` 连同 `comments-routes.caddyfile` 放进 Explore 的 `sites/` 目录。Caddy 只信任 Cloudflare 的地址段，从右往左找出读者，再只把这一个地址写进 `X-Forwarded-For` 交给评论服务，读者自己写进去的值取不到 `[EV]`。
- **Compose 服务名叫 `server`，不叫 `serve`**：Explore 的 Caddy 同时在 Explore 的网络上用 `serve` 这个名字，接上 kite-edge 的容器再叫 `serve`，Docker 只会答其中一个网络的地址，Caddy 可能把 Explore 的请求送到评论服务 `[EV]`。
- **Cookie 规则只写一处**：`deploy/edge/comments-routes.caddyfile`（[widget.md §4](widget.md#4-iframe-地址) 第 5 条）。边缘模式的站点文件和独立模式的 `deploy/Caddyfile` 都导入它，只有上游不同。本机演练：匿名读取路径上的 Cookie 被删，登录后的路径、更深的路径和写请求都保留 `[EV]`。
- **独立模式**：不设 `COMPOSE_FILE` 时，`server` 只监听宿主机的 `127.0.0.1:8081`，给同机的反向代理用，`deploy/Caddyfile` 是它的站点块。生产不用。
- **预发布**：`comments.staging.kite.plus` 是同一台机器上的另一份检出和 Compose 项目（`comments-staging`，别名 `comments-staging`，网段 10.90.1.0/24，见 `deploy/.env.example`），平时停着，测试和升级时启动。它的二级子域在 Cloudflare 上要先解决证书（identity architecture.md §4）。
- **Cloudflare**：设置同身份服务（identity architecture.md §4）。评论服务另要注意缓存：保持默认，不加 Cache Everything 一类的规则。默认只按扩展名缓存，正好覆盖 `embed.js`（5 分钟）、带版本号的 `/embed/v1.x.y.js` 和 `/static/widget/*`（一年）、生成的头像，都遵守源站的 `Cache-Control`；匿名读取接口的 JSON 按 `Accept-Language` 变（`Vary`），而 Cloudflare 除了压缩不认 `Vary`，缓存了就会把一种语言的结果给所有人；iframe 文档 `/embed/v1/thread` 的 CSP 跟着站点状态变。
- **镜像**：还没有发布流程，第一次部署在服务器的检出里用 `deploy/Dockerfile` 构建（[README](../../README.md#deployment)）；发布流程 `[待定]`，C2 上线前。
- **备份**：每天 `scripts/backup.sh`：`pg_dump` 评论服务的库，会话、登录交易、通知、发信队列只留表结构不留数据（[data-model.md §1](data-model.md#1-数据分类)），用 age 公钥加密，在本机和异地各保存 30 天。异地复制用 rclone（`BACKUP_REMOTE`），存放位置和身份服务一起定 `[待定]`（identity I2 前），地址不写进仓库。`COMMENTS_DATA_KEYS`、`COMMENTS_HMAC_KEY`、`COMMENTS_IP_KEY` 不进备份，离线保存，没有它们备份里的邮箱解不开。每个阶段结束做一次恢复到空机器的演练，步骤见 [README](../../README.md#deployment)；2026-10-11 本机演练：备份解密后恢复进空库，29 张表、迁移版本 2 `[EV]`。身份服务的备份由 identity 负责（[identity architecture.md §5](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#5-运维)）。
- **监控**：外部探测 `comments.kite.plus` 的 `/healthz`，探测点包括国内三网；对错误率、发信队列积压、发信失败、磁盘和备份结果设告警。
- **升级**：评论服务随发版升级，数据库迁移只向前；先在预发布上跑。
- **故障**：评论服务不可用只影响组件本身（[widget.md §10](widget.md#10-加载体积与降级)）；在官网新闻和 GitHub 上说明情况。

---

## 10. 里程碑

| 阶段 | 内容 |
|---|---|
| C0 技术验证 | 跨站登录实测；身份服务选型 |
| C1 身份服务 | 已拆成单独的项目，先于评论服务进行，见 [identity 路线图](https://github.com/kite-plus/identity/blob/main/docs/design/roadmap.md) 的 I0–I3 |
| C2 评论内测 | 登录读者、受邀站点、Kite 评论插件支持 |
| C3 开放接入 | 访客评论、导入、认领、开放注册 |
| C4 生态 | 各系统插件、Kite 后台集成、FedCM 等 |

范围、用例和退出条件见 [roadmap.md](roadmap.md)。

---

## 11. 风险

| 风险 | 后果 | 应对 |
|---|---|---|
| 博主不愿意把评论交给集中服务 | 没人用 | §0.1 的硬约束；Kite 默认内置；导入加认领；抓住 LeanCloud 停服前的迁移窗口 |
| 功能不如 Waline、Twikoo 丰富：它们都有表情包、图片上传、点赞、浏览量、IP 属地，还能把新评论推送到 Telegram、飞书等 | 博主觉得是降级 | 主打统一身份、追溯和免运维；点赞和推送到聊天工具的 Webhook 放 C4；浏览量和 IP 属地有意不做（§2.2） |
| 境外服务在国内变慢或被封锁 | 评论区打不开 | 先用与 Explore 同机的美国服务器，慢得影响使用时迁到香港加优化线路（§5）；组件不依赖任何在国内不可用的第三方（Google、Gravatar、GitHub 头像都不直连）；国内三网探测；组件失败不影响宿主页；备用域名的应急预案 `[待定]` |
| 合规边界不清（§5） | 有不确定性 | 收集最少的个人信息；认真处理举报；上线前咨询 |
| 单点故障 | 所有接入的评论区同时不可用 | 失败隔离；备份和恢复演练；及时说明情况 |
| 有人用评论区发布违法内容，或冒充别人的博客 | 平台责任、域名声誉受损 | 站点验证；内测期人工批准站点；举报；暂停站点 |
| 身份服务安全公告频繁 | 账号被接管 | 由 identity 负责：按时升级、预发布环境、限流、推荐 Passkey（[identity architecture.md §5](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#5-运维)） |
| 评论服务被入侵，`embed.js` 被篡改 | `embed.js` 运行在每个接入博客自己的页面里，所有用自动更新写法的博客同时中招 | `embed.js` 保持极小、改动单独审查；部署凭据最少授权；控制台提供锁定版本加 SRI 的写法，也支持自托管（[widget.md §12](widget.md#12-自托管-embedjs)） |
| 浏览器政策变化 | 登录方式要跟着改 | 不依赖第三方 Cookie；分区 Cookie 不可用时有降级；FedCM 作备选 |
| Safari 把 `comments.kite.plus` 归为跟踪域名 | Safari 读者每次重新打开浏览器都要再点一次登录 | 懒加载，减少被嵌入的次数；允许自托管 `embed.js`；C2 起观察 |
| 一个人维护 | 进度慢、响应慢 | 收紧范围（§2.2）；自动化测试和部署；不承诺 SLA |
| 通知邮件被 QQ 邮箱、163 邮箱拒收或进垃圾箱 | 读者收不到回复 | SPF、DKIM、DMARC；合并发送；可靠的发信服务商 |

---

## 12. 现在必须定的，和现在不做的

**现在必须定**：以后再改，都是破坏性改动。

| 决定 | 写在哪里 |
|---|---|
| issuer 是 `https://id.kite.plus` | [identity architecture.md §1](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#1-各服务对身份服务的要求) 的 R1 |
| 用户键是 `(iss, sub)` | [Explore identity-and-comments.md §3](https://github.com/kite-plus/explore/blob/main/docs/design/identity-and-comments.md#3-协议与数据) |
| 评论服务的 origin 是 `comments.kite.plus` | §4 |
| 嵌入元素和属性、iframe 地址、消息协议 v1 | [widget.md §2](widget.md#2-接入代码)、[§4](widget.md#4-iframe-地址)、[§6](widget.md#6-宿主页与-iframe-的消息) |
| 讨论页标识规则 | [widget.md §5](widget.md#5-讨论页标识) |
| 接入代码里的 `crossorigin="anonymous"`；登录返回标记 `#kc-login` | [widget.md §2](widget.md#2-接入代码)、[§8](widget.md#8-深链接) |
| 读者对外的 ID 按站点区分 | [data-model.md §3.1](data-model.md#31-账号与身份) |
| 评论锚点 `#kc-<评论 ID>` | [widget.md §8](widget.md#8-深链接) |
| 一个站点是一组已验证的 origin | [sites.md §2](sites.md#2-注册与验证) |
| 导出格式带版本号 | [sites.md §8](sites.md#8-导出) |
| iframe 带 `allow="identity-credentials-get"` | [login.md §9](login.md#9-以后fedcm) |

**现在不做**：将来做的时候只需新增代码。

- FedCM、公开的个人主页、点赞、评论里的图片、评论数接口。
- 多实例部署（限流挪出进程内存）。
- 转为境内运营所需的改动（§5）。
- 自托管实例之间的互通。
- 用模型审核评论：要把评论内容发给第三方，和 [moderation.md §1](moderation.md#1-原则) 的原则冲突。

---

## 13. 参考

- 法规：[《互联网跟帖评论服务管理规定》](https://www.cac.gov.cn/2022-11/16/c_1670253725725039.htm)、[《具有舆论属性或社会动员能力的互联网信息服务安全评估规定》](https://www.cac.gov.cn/2018-11/15/c_1123716072.htm)、[《互联网用户账号信息管理规定》](https://www.cac.gov.cn/2022-06/26/c_1657868775042841.htm)、[《促进和规范数据跨境流动规定》](https://www.cac.gov.cn/2024-03/22/c_1712776611775634.htm)、[《个人信息保护法》](http://www.npc.gov.cn/npc/c2/c30834/202108/t20210820_313088.html)、[《网络安全法》2025 年修正](http://www.npc.gov.cn/npc/c2/c30834/202510/t20251028_449048.html)。
- 备案：[工信部域名注册管理机构名单](https://domain.miit.gov.cn/managerdns/index)、[腾讯云：各省备案规则](https://cloud.tencent.com/document/product/243/3474)、[阿里云：短信不再受理个人自用资质](https://help.aliyun.com/zh/sms/product-overview/announcement-on-sms-not-supporting-application-for-personal-use-qualification)。
- 第三方登录：[微信网站应用审核规范](https://developers.weixin.qq.com/doc/oplatform/Website_App/operation.html)、[QQ 互联网站审核规范](https://wiki.connect.qq.com/%e7%bd%91%e7%ab%99%e5%ae%a1%e6%a0%b8%e8%a7%84%e8%8c%83)。
- 访问：[Vercel：从中国大陆访问](https://vercel.com/kb/guide/accessing-vercel-hosted-sites-from-mainland-china)、[Cloudflare China Network](https://developers.cloudflare.com/china-network/)、[Twikoo 部署方式](https://twikoo.js.org/backend.html)。
- 浏览器的依据见 [login.md §10](login.md#10-依据)；身份服务的依据见 [identity architecture.md §7](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#7-参考)。
